package synctargets

import (
	"context"
	"fmt"
	"net/url"

	kcpworkloadv1alpha1 "github.com/kcp-dev/kcp/pkg/apis/workload/v1alpha1"
	"github.com/kcp-dev/logicalcluster/v3"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/kube-openapi/pkg/util/sets"

	"github.com/faroshq/faros/pkg/apis/workload"
	"github.com/faroshq/faros/pkg/util/synctarget"
)

func (c *Controller) renderSyncerTemplate(ctx context.Context,
	cluster logicalcluster.Path,
	syncTarget *kcpworkloadv1alpha1.SyncTarget,
	expectedResourcesForPermission sets.String,
	syncTargetOwnerReferences []metav1.OwnerReference,
	token, syncerID string) error {

	// Set server url from faros config config-map
	cm, err := c.coreClientSet.CoreV1().Cluster(cluster).ConfigMaps(workload.FarosConfigMapNamespace).Get(ctx, workload.FarosConfigMapName, metav1.GetOptions{})
	if err != nil {
		return err
	}

	server, ok := cm.Data[workload.FarosConfigMapServerKey]
	if !ok {
		return fmt.Errorf("faros configmap %s/%s does not contain key %s", workload.FarosConfigMapNamespace, workload.FarosConfigMapName, workload.FarosConfigMapServerKey)
	}

	u, err := url.Parse(server)
	if err != nil {
		return err
	}

	templateInputs := synctarget.TemplateInput{
		ServerURL: u.Host,
		Token:     token,
		//CAData: nil, // TODO: Add CAData
		KCPNamespace:   workload.FarosConfigMapNamespace,
		Namespace:      syncerID,
		SyncTargetPath: logicalcluster.From(syncTarget).Path().String(),
		SyncTarget:     syncTarget.Name,
		SyncTargetUID:  string(syncTarget.UID),

		Image:                               c.config.SyncerConfig.Image,
		Replicas:                            c.config.SyncerConfig.Replicas,
		ResourcesToSync:                     c.config.SyncerConfig.ResourceToSync,
		QPS:                                 c.config.SyncerConfig.QPS,
		Burst:                               c.config.SyncerConfig.Burst,
		FeatureGatesString:                  c.config.SyncerConfig.FeatureGatesString,
		APIImportPollIntervalString:         c.config.SyncerConfig.APIImportPollIntervalString,
		DownstreamNamespaceCleanDelayString: c.config.SyncerConfig.DownstreamNamespaceCleanDelayString,
	}

	template, err := synctarget.RenderSyncerResources(templateInputs, syncerID, expectedResourcesForPermission.List())
	if err != nil {
		return err
	}

	templateCM := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      syncerID,
			Namespace: workload.FarosConfigMapNamespace,
			Labels: map[string]string{
				workload.FarosConfigMapLabelName:               syncTarget.Name,
				workload.FarosConfigMapLabelBootstrapBootstrap: "",
			},
			OwnerReferences: syncTargetOwnerReferences,
		},
		Data: map[string]string{
			"resources.yaml": string(template),
		},
	}

	current, err := c.coreClientSet.CoreV1().Cluster(cluster).ConfigMaps(workload.FarosConfigMapNamespace).Get(ctx, templateCM.Name, metav1.GetOptions{})
	switch {
	case apierrors.IsNotFound(err):
		_, err := c.coreClientSet.CoreV1().Cluster(cluster).ConfigMaps(templateCM.Namespace).Create(ctx, templateCM, metav1.CreateOptions{})
		if err != nil && !apierrors.IsAlreadyExists(err) {
			return fmt.Errorf("failed to create the SyncTarget template ConfigMap in workspace %s: %s", cluster.String(), err)
		}
	case err == nil:
		current.Data = templateCM.Data
		current.Labels = templateCM.Labels
		current.OwnerReferences = mergeOwnerReference(templateCM.OwnerReferences, syncTargetOwnerReferences)
		_, err := c.coreClientSet.CoreV1().Cluster(cluster).ConfigMaps(templateCM.Namespace).Update(ctx, templateCM, metav1.UpdateOptions{})
		if err != nil {
			return fmt.Errorf("failed to update the SyncTarget template ConfigMap %s", err)
		}
	default:
		return fmt.Errorf("failed to create the cTarget template %s", err)
	}

	return nil
}
