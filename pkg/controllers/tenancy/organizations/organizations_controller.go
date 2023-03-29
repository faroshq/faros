package organizations

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	jsonpatch "github.com/evanphx/json-patch"
	kcpcache "github.com/kcp-dev/apimachinery/v2/pkg/cache"
	"github.com/kcp-dev/client-go/kubernetes"
	kcpclientset "github.com/kcp-dev/kcp/pkg/client/clientset/versioned/cluster"
	"github.com/kcp-dev/kcp/pkg/logging"
	"github.com/kcp-dev/logicalcluster/v3"

	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog/v2"

	tenancyv1alpha1 "github.com/faroshq/faros/pkg/apis/tenancy/v1alpha1"
	"github.com/faroshq/faros/pkg/bootstrap"
	farosclientset "github.com/faroshq/faros/pkg/client/clientset/versioned/cluster"
	tenancyinformers "github.com/faroshq/faros/pkg/client/informers/externalversions/tenancy/v1alpha1"
	tenancylisters "github.com/faroshq/faros/pkg/client/listers/tenancy/v1alpha1"
	"github.com/faroshq/faros/pkg/config"
)

const (
	controllerName = "faros-organizations"
	finalizerName  = "organizations.tenancy.faros.sh/finalizer"
)

// Controller watches Faros Organizations and makes sure they represented by KCP organization workspaces
// Controller runs inside controllers workspace virtual workspace for tenancy.
// For now tenancy objects are located only in single workspace, but in the future we
// can scale them to multiple workspaces per shard if needed.
type Controller struct {
	config *config.Config

	queue workqueue.RateLimitingInterface

	kcpClientSet         kcpclientset.ClusterInterface
	coreClientSet        kubernetes.ClusterInterface
	farosClientSet       farosclientset.ClusterInterface
	organizationsIndexer cache.Indexer
	organizationsLister  tenancylisters.OrganizationClusterLister

	bootstraper bootstrap.Bootstraper
}

func NewController(
	config *config.Config,
	kcpClientSet kcpclientset.ClusterInterface,
	coreClientSet kubernetes.ClusterInterface,
	farosClientSet farosclientset.ClusterInterface,
	organizationsInformer tenancyinformers.OrganizationClusterInformer,
) (*Controller, error) {
	queue := workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), controllerName)

	bootstraper, err := bootstrap.New(&config.FarosKCPConfig)
	if err != nil {
		return nil, err
	}

	c := &Controller{
		config:               config,
		queue:                queue,
		bootstraper:          bootstraper,
		kcpClientSet:         kcpClientSet,
		coreClientSet:        coreClientSet,
		farosClientSet:       farosClientSet,
		organizationsIndexer: organizationsInformer.Informer().GetIndexer(),
		organizationsLister:  organizationsInformer.Lister(),
	}

	organizationsInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    func(obj interface{}) { c.enqueue(obj) },
		UpdateFunc: func(oldObj, obj interface{}) { c.enqueueUpdate(oldObj, obj) },
		DeleteFunc: func(obj interface{}) { c.enqueue(obj) },
	})

	return c, nil
}

func (c *Controller) enqueueUpdate(objOld, objNew interface{}) {
	oldMeta, newMeta, oldStatus, newStatus := toQueueElementType(objOld, objNew)
	if oldMeta.GetResourceVersion() == newMeta.GetResourceVersion() {
		return
	}

	if oldMeta.GetGeneration() != newMeta.GetGeneration() {
		c.enqueue(objNew)
		return
	}

	if !equality.Semantic.DeepEqual(oldStatus, newStatus) {
		c.enqueue(objNew)
		return
	}
}

func toQueueElementType(oldObj, obj interface{}) (oldMeta, newMeta metav1.Object, oldStatus, newStatus interface{}) {
	switch typedObj := obj.(type) {
	case *tenancyv1alpha1.Organization:
		newMeta = typedObj
		newStatus = typedObj.Status
		if oldObj != nil {
			typedOldObj := oldObj.(*tenancyv1alpha1.Organization)
			oldStatus = typedOldObj.Status
			oldMeta = typedOldObj
		}
	}
	return
}

func (c *Controller) enqueue(obj interface{}) {
	key, err := kcpcache.MetaClusterNamespaceKeyFunc(obj)
	if err != nil {
		runtime.HandleError(err)
		return
	}

	logger := logging.WithQueueKey(logging.WithReconciler(klog.Background(), controllerName), key)
	logger.V(2).Info("queueing Organization")
	c.queue.Add(key)
}

func (c *Controller) Start(ctx context.Context, numThreads int) {
	defer runtime.HandleCrash()
	defer c.queue.ShutDown()

	logger := logging.WithReconciler(klog.FromContext(ctx), controllerName)
	ctx = klog.NewContext(ctx, logger)
	logger.Info("Starting controller")
	defer logger.Info("Shutting down controller")

	for i := 0; i < numThreads; i++ {
		go wait.Until(func() { c.startWorker(ctx) }, time.Second, ctx.Done())
	}

	<-ctx.Done()
}

func (c *Controller) startWorker(ctx context.Context) {
	for c.processNextWorkItem(ctx) {
	}
}

func (c *Controller) processNextWorkItem(ctx context.Context) bool {
	// Wait until there is a new item in the working queue
	k, quit := c.queue.Get()
	if quit {
		return false
	}
	key := k.(string)

	logger := logging.WithQueueKey(klog.FromContext(ctx), key)
	ctx = klog.NewContext(ctx, logger)
	logger.V(1).Info("processing key")

	// No matter what, tell the queue we're done with this key, to unblock
	// other workers.
	defer c.queue.Done(key)

	if requeue, err := c.process(ctx, key); err != nil {
		runtime.HandleError(fmt.Errorf("%q controller failed to sync %q, err: %w", controllerName, key, err))
		c.queue.AddRateLimited(key)
		return true
	} else if requeue {
		// only requeue if we didn't error, but we still want to requeue
		c.queue.Add(key)
	}
	c.queue.Forget(key)
	return true
}

func (c *Controller) process(ctx context.Context, key string) (bool, error) {
	logger := klog.FromContext(ctx)

	cluster, _, name, err := kcpcache.SplitMetaClusterNamespaceKey(key)
	if err != nil {
		runtime.HandleError(err)
		return false, nil
	}

	obj, err := c.organizationsLister.Cluster(cluster).Get(name)
	if err != nil {
		if errors.IsNotFound(err) || errors.IsForbidden(err) { // KCP  returns 401 on not found
			return false, nil // object deleted before we handled it
		}
		return false, err
	}

	old := obj
	obj = obj.DeepCopy()

	ctx = klog.NewContext(ctx, logger)

	var errs []error
	requeue, err := c.reconcile(ctx, obj)
	if err != nil {
		errs = append(errs, err)
	}

	// Regardless of whether reconcile returned an error or not, always try to patch status if needed. Return the
	// reconciliation error at the end.

	// If the object being reconciled changed as a result, update it.
	if err := c.patchIfNeeded(ctx, old, obj); err != nil {
		errs = append(errs, err)
	}

	return requeue, utilerrors.NewAggregate(errs)
}

func (c *Controller) patchIfNeeded(ctx context.Context, old, obj *tenancyv1alpha1.Organization) error {
	specOrObjectMetaChanged := !equality.Semantic.DeepEqual(old.Spec, obj.Spec) || !equality.Semantic.DeepEqual(old.ObjectMeta, obj.ObjectMeta)
	statusChanged := !equality.Semantic.DeepEqual(old.Status, obj.Status)

	if specOrObjectMetaChanged && statusChanged {
		panic("Programmer error: spec and status changed in same reconcile iteration")
	}

	if !specOrObjectMetaChanged && !statusChanged {
		return nil
	}

	clusterOrganizationForPatch := func(organization *tenancyv1alpha1.Organization) tenancyv1alpha1.Organization {
		var ret tenancyv1alpha1.Organization
		if specOrObjectMetaChanged {
			ret.ObjectMeta = organization.ObjectMeta
			ret.Spec = organization.Spec
		} else {
			ret.Status = organization.Status
		}
		return ret
	}

	clusterName := logicalcluster.From(old)
	name := old.Name

	oldForPatch := clusterOrganizationForPatch(old)
	// to ensure they appear in the patch as preconditions
	oldForPatch.UID = ""
	oldForPatch.ResourceVersion = ""

	oldData, err := json.Marshal(oldForPatch)
	if err != nil {
		return fmt.Errorf("failed to Marshal old data for Workspace %s|%s: %w", clusterName, name, err)
	}

	newForPatch := clusterOrganizationForPatch(obj)
	// to ensure they appear in the patch as preconditions
	newForPatch.UID = old.UID
	newForPatch.ResourceVersion = old.ResourceVersion

	newData, err := json.Marshal(newForPatch)
	if err != nil {
		return fmt.Errorf("failed to Marshal new data for Organization %s|%s: %w", clusterName, name, err)
	}

	patchBytes, err := jsonpatch.CreateMergePatch(oldData, newData)
	if err != nil {
		return fmt.Errorf("failed to create patch for Organization %s|%s: %w", clusterName, name, err)
	}

	var subresources []string
	if statusChanged {
		subresources = []string{"status"}
	}

	_, err = c.farosClientSet.Cluster(clusterName.Path()).TenancyV1alpha1().Organizations().Patch(ctx, obj.Name, types.MergePatchType, patchBytes, metav1.PatchOptions{}, subresources...)
	return err
}
