package bootstrap

import (
	"context"
	"fmt"

	rootfarosservicescontrollersphase0 "github.com/faroshq/faros/pkg/bootstrap/templates/root-faros-services-controllers-phase0"
	rootfarosservicescontrollersphase1 "github.com/faroshq/faros/pkg/bootstrap/templates/root-faros-services-controllers-phase1"
	bootstraputils "github.com/faroshq/faros/pkg/util/bootstrap"
	"github.com/kcp-dev/logicalcluster/v3"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	"k8s.io/klog/v2"
)

func (b *bootstrap) BootstrapControllersWorkspace(ctx context.Context) error {
	err := b.bootstrapControllersWorkspacePhase1(ctx)
	if err != nil {
		return err
	}
	err = b.bootstrapControllersWorkspacePhase2(ctx)
	if err != nil {
		return err
	}
	return nil
}

func (b *bootstrap) bootstrapControllersWorkspacePhase1(ctx context.Context) error {
	klog.Infof("Bootstrapping %s workspace - phase1", b.config.ControllersWorkspace)
	targetRest, err := b.clientFactory.GetWorkspaceRestConfig(ctx, b.config.ControllersWorkspace)
	if err != nil {
		return err
	}

	discoveryClient, err := discovery.NewDiscoveryClientForConfig(targetRest)
	if err != nil {
		return err
	}

	dynamicClient, err := dynamic.NewForConfig(targetRest)
	if err != nil {
		return err
	}

	clusterPath := logicalcluster.NewPath("root")

	exportWorkload, err := b.kcpClientSet.Cluster(clusterPath).ApisV1alpha1().APIExports().Get(ctx, "workload.kcp.io", metav1.GetOptions{})
	if err != nil {
		return err
	}

	exportTenancy, err := b.kcpClientSet.Cluster(clusterPath).ApisV1alpha1().APIExports().Get(ctx, "tenancy.kcp.io", metav1.GetOptions{})
	if err != nil {
		return err
	}

	return rootfarosservicescontrollersphase0.Bootstrap(ctx, discoveryClient, dynamicClient, bootstraputils.ReplaceOption(
		"ROOT_WORKLOAD_IDENTITY", exportWorkload.Status.IdentityHash,
		"ROOT_TENANCY_IDENTITY", exportTenancy.Status.IdentityHash,
	))
}

func (b *bootstrap) bootstrapControllersWorkspacePhase2(ctx context.Context) error {
	klog.Infof("Bootstrapping %s workspace - phase2", b.config.ControllersWorkspace)
	targetRest, err := b.clientFactory.GetWorkspaceRestConfig(ctx, b.config.ControllersWorkspace)
	if err != nil {
		return err
	}

	discoveryClient, err := discovery.NewDiscoveryClientForConfig(targetRest)
	if err != nil {
		return err
	}

	dynamicClient, err := dynamic.NewForConfig(targetRest)
	if err != nil {
		return err
	}

	controllersCluster := logicalcluster.NewPath(b.config.ControllersWorkspace)
	parent, ok := controllersCluster.Parent()
	if !ok {
		return fmt.Errorf("invalid parent cluster path")
	}

	controllersWorkspace, err := b.kcpClientSet.Cluster(parent).TenancyV1alpha1().Workspaces().Get(ctx, controllersCluster.Base(), metav1.GetOptions{})
	if err != nil {
		return err
	}

	// organizations workspace details (for cluster name)
	organizationsCluster := logicalcluster.NewPath(b.config.ControllersOrganizationWorkspace)
	parent, ok = organizationsCluster.Parent()
	if !ok {
		return fmt.Errorf("invalid parent cluster path")
	}

	organizationsWorkspace, err := b.kcpClientSet.Cluster(parent).TenancyV1alpha1().Workspaces().Get(ctx, organizationsCluster.Base(), metav1.GetOptions{})
	if err != nil {
		return err
	}

	clusterName, ok := organizationsWorkspace.Annotations[workspaceClusterAnnotationKey]
	if !ok {
		return fmt.Errorf("workspace %q has no cluster annotation", controllersWorkspace.Name)
	}

	secret, err := b.coreClientSet.Cluster(controllersCluster).CoreV1().Secrets("default").Get(ctx, "faros-controllers-token", metav1.GetOptions{})
	if err != nil {
		return err
	}

	name := b.config.ControllerFarosConfigSecretName
	err = rootfarosservicescontrollersphase1.Bootstrap(ctx, discoveryClient, dynamicClient, bootstraputils.ReplaceOption(
		"SERVER_URL", controllersWorkspace.Spec.URL,
		"FAROS_CONTROLLER_TOKEN", string(secret.Data["token"]),
		"FAROS_SECRET_NAME", name,
		"FAROS_SECRET_KEY", b.config.ControllerKubeConfigSecretKey,
		"FAROS_SKIP_TLS_VERIFY", fmt.Sprintf("%v", b.config.SkipTLSVerify),
		"FAROS_CONTROLLER_CLUSTER_NAME", clusterName,
		"FAROS_CONTROLLER_CLUSTER_KEY", b.config.ControllerClusterNameSecretKey,
	))
	if err != nil {
		return err
	}

	// move secret from kcp to hosting cluster for more isolated controller process running
	original, err := b.coreClientSet.Cluster(controllersCluster).CoreV1().Secrets("default").Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return err
	}
	kubeConfigSecret := original.DeepCopy()
	kubeConfigSecret.ResourceVersion = ""
	kubeConfigSecret.Namespace = b.config.HostingClusterNamespace

	originalHosted, err := b.hostingCoreClientSet.CoreV1().Secrets(b.config.HostingClusterNamespace).Get(ctx, kubeConfigSecret.Name, metav1.GetOptions{})
	switch {
	case apierrors.IsNotFound(err):
		klog.V(4).Infof("Creating faros config secret %q", kubeConfigSecret.Name)
		_, err = b.hostingCoreClientSet.CoreV1().Secrets(b.config.HostingClusterNamespace).Create(ctx, kubeConfigSecret, metav1.CreateOptions{})
		if err != nil {
			return err
		}
	case err == nil:
		klog.V(4).Infof("Patching faros config secret %q", kubeConfigSecret.Name)
		originalHosted.Data = kubeConfigSecret.Data

		klog.V(4).Info("Patching  faros config secret %q", kubeConfigSecret.Name)
		_, err = b.hostingCoreClientSet.CoreV1().Secrets(b.config.HostingClusterNamespace).Update(ctx, originalHosted, metav1.UpdateOptions{})
		if err != nil {
			return err
		}
	default:
		return fmt.Errorf("failed to get the faros config secret  %s", kubeConfigSecret.Name)
	}
	return nil
}
