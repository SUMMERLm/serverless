/*
Copyright 2017 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package quota

import (
	"fmt"
	"time"

	"github.com/SUMMERLm/serverless/pkg/comm"
	"github.com/SUMMERLm/serverless/pkg/etcd_lock"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog/v2"

	clientSet "github.com/SUMMERLm/serverless/pkg/generated/clientset/versioned"
	quotaScheme "github.com/SUMMERLm/serverless/pkg/generated/clientset/versioned/scheme"
	informers "github.com/SUMMERLm/serverless/pkg/generated/informers/externalversions/serverless/v1"
	lister "github.com/SUMMERLm/serverless/pkg/generated/listers/serverless/v1"
	coreV1 "k8s.io/api/core/v1"
	utilRuntime "k8s.io/apimachinery/pkg/util/runtime"
	typedCoreV1 "k8s.io/client-go/kubernetes/typed/core/v1"
)

const controllerAgentName = "quota-controller"
const controllerParentAgentName = "quota-Parent-controller"

const (
	// SuccessSynced is used as part of the Event 'reason' when a Quota is synced
	SuccessSynced = "Synced"
	// ErrResourceExists is used as part of the Event 'reason' when a Quota fails
	// to sync due to a Deployment of the same name already existing.
	ErrResourceExists = "ErrResourceExists"

	// MessageResourceExists is the message used for Events when a resource
	// fails to sync due to a Deployment already existing
	MessageResourceExists = "Resource %q already exists and is not managed by Quota"
	// MessageResourceSynced is the message used for an Event fired when a Quota
	// is synced successfully
	MessageResourceSynced = "quota synced successfully"
)

var option = etcd_lock.Option{
	ConnectionTimeout: comm.ConnectTimeOut * time.Second,
	Prefix:            "ServerlessQuotaLocker:",
	Debug:             false,
}

// Controller is the controller implementation for Quota resources
type Controller struct {
	// kubeclientset is a standard kubernetes clientset
	kubeclientset       kubernetes.Interface
	kubeParentClientset kubernetes.Interface
	dynamicClient       dynamic.DynamicClient
	// quotaclientset is a clientset for our own API group
	quotaclientset       clientSet.Interface
	quotaParentclientset clientSet.Interface
	quotasLister         lister.QuotaLister
	quotasSynced         cache.InformerSynced
	quotasParentLister   lister.QuotaLister
	quotasParentSynced   cache.InformerSynced
	// workqueue is a rate limited work queue. This is used to queue work to be
	// processed instead of performing it as soon as a change happens. This
	// means we can ensure we only process a fixed amount of resources at a
	// time, and makes it easy to ensure we are never processing the same item
	// simultaneously in two different workers.
	workqueue       workqueue.RateLimitingInterface
	parentworkqueue workqueue.RateLimitingInterface
	// recorder is an event recorder for recording Event resources to the
	// Kubernetes API.
	recorder      record.EventRecorder
	parentrecoder record.EventRecorder
}

// NewController returns a new sample controller
func NewController(
	kubeclientset kubernetes.Interface,
	kubeParentClientset kubernetes.Interface,
	dynamicClient dynamic.DynamicClient,
	quotaclientset clientSet.Interface,
	quotaParentclientset clientSet.Interface,
	quotaInformer informers.QuotaInformer,
	quotaParentInformer informers.QuotaInformer) *Controller {
	// Create event broadcaster
	// Add sample-controller types to the default Kubernetes Scheme so Events can be
	// logged for sample-controller types.
	utilRuntime.Must(quotaScheme.AddToScheme(scheme.Scheme))
	klog.Info("Creating event broadcaster")
	eventBroadcaster := record.NewBroadcaster()
	eventBroadcaster.StartStructuredLogging(0)
	eventBroadcaster.StartRecordingToSink(&typedCoreV1.EventSinkImpl{Interface: kubeclientset.CoreV1().Events("")})
	recorder := eventBroadcaster.NewRecorder(scheme.Scheme, coreV1.EventSource{Component: controllerAgentName})
	parentrecoder := eventBroadcaster.NewRecorder(scheme.Scheme, coreV1.EventSource{Component: controllerParentAgentName})

	controller := &Controller{
		kubeclientset:        kubeclientset,
		kubeParentClientset:  kubeParentClientset,
		dynamicClient:        dynamicClient,
		quotaclientset:       quotaclientset,
		quotaParentclientset: quotaParentclientset,
		quotasLister:         quotaInformer.Lister(),
		quotasSynced:         quotaInformer.Informer().HasSynced,
		quotasParentLister:   quotaParentInformer.Lister(),
		quotasParentSynced:   quotaParentInformer.Informer().HasSynced,

		workqueue:       workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "Quotas"),
		parentworkqueue: workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "QuotasParent"),
		recorder:        recorder,
		parentrecoder:   parentrecoder,
	}

	klog.Info("Setting up event handlers")
	// Set up an event handler for when local Quota resources change
	_, err := quotaInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: controller.enqueueQuota,
		UpdateFunc: func(old, new interface{}) {
			controller.enqueueQuota(new)
		},
		DeleteFunc: controller.enqueueQuotaForDelete,
	})
	if err != nil {
		klog.Error(err)
	}
	// Set up an event handler for when Parent Quota  resources change
	_, err = quotaParentInformer.Informer().AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: controller.enqueueQuotaParent,
		UpdateFunc: func(old, new interface{}) {
			controller.enqueueQuotaParent(new)
		},
		DeleteFunc: controller.enqueueQuotaParentForDelete,
	})
	if err != nil {
		klog.Error(err)
	}

	return controller
}

// Run will set up the event handlers for types we are interested in, as well
// as syncing informer caches and starting workers. It will block until stopCh
// is closed, at which point it will shutdown the workqueue and wait for
// workers to finish processing their current work items.
func (c *Controller) Run(workers int, stopCh <-chan struct{}) error {
	defer utilRuntime.HandleCrash()
	defer c.workqueue.ShutDown()
	defer c.parentworkqueue.ShutDown()

	// Start the informer factories to begin populating the informer caches
	klog.Info("Starting Quota controller")

	// Wait for the caches to be synced before starting workers
	klog.Info("Waiting for informer caches to sync")
	if ok := cache.WaitForCacheSync(stopCh, c.quotasSynced); !ok {
		return fmt.Errorf("failed to wait for caches to sync")
	}
	klog.Info("Waiting for parent informer caches to sync")

	if ok := cache.WaitForCacheSync(stopCh, c.quotasParentSynced); !ok {
		return fmt.Errorf("failed to wait for parent caches to sync")
	}
	klog.Info("Starting local workers")
	// Launch two workers to process Quota resources
	for i := 0; i < workers; i++ {
		go wait.Until(c.runWorker, time.Second, stopCh)
	}

	klog.Info("Starting parent workers")
	for i := 0; i < workers; i++ {
		go wait.Until(c.runParentWorker, time.Second, stopCh)
	}
	klog.Info("Started workers")
	<-stopCh
	klog.Info("Shutting down workers")

	return nil
}

// runWorker is a long-running function that will continually call the
// processNextWorkItem function in order to read and process a message on the
// workqueue.
func (c *Controller) runWorker() {
	for c.processNextWorkItem() {
	}
}

func (c *Controller) runParentWorker() {
	for c.processNextParentWorkItem() {
	}
}

// processNextWorkItem will read a single work item off the workqueue and
// attempt to process it, by calling the syncHandler.
func (c *Controller) processNextWorkItem() bool {
	obj, shutdown := c.workqueue.Get()

	if shutdown {
		return false
	}

	// We wrap this block in a func so we can defer c.workqueue.Done.
	err := func(obj interface{}) error {
		// We call Done here so the workqueue knows we have finished
		// processing this item. We also must remember to call Forget if we
		// do not want this work item being re-queued. For example, we do
		// not call Forget if a transient error occurs, instead the item is
		// put back on the workqueue and attempted again after a back-off
		// period.
		defer c.workqueue.Done(obj)
		var key string
		var ok bool
		// We expect strings to come off the workqueue. These are of the
		// form namespace/name. We do this as the delayed nature of the
		// workqueue means the items in the informer cache may actually be
		// more up to date that when the item was initially put onto the
		// workqueue.
		if key, ok = obj.(string); !ok {
			// As the item in the workqueue is actually invalid, we call
			// Forget here else we'd go into a loop of attempting to
			// process a work item that is invalid.
			c.workqueue.Forget(obj)
			utilRuntime.HandleError(fmt.Errorf("expected string in workqueue but got %#v", obj))
			return nil
		}
		// Run the syncHandler, passing it the namespace/name string of the
		// Quota resource to be synced.
		if err := c.syncHandler(key); err != nil {
			// Put the item back on the workqueue to handle any transient errors.
			c.workqueue.AddRateLimited(key)
			return fmt.Errorf("error syncing '%s': %s, requeuing", key, err.Error())
		}
		// Finally, if no error occurs we Forget this item so it does not
		// get queued again until another change happens.
		c.workqueue.Forget(obj)
		klog.Infof("Successfully synced '%s'", key)
		return nil
	}(obj)

	if err != nil {
		utilRuntime.HandleError(err)
		return true
	}

	return true
}

func (c *Controller) processNextParentWorkItem() bool {
	objparent, shutdownparent := c.parentworkqueue.Get()
	if shutdownparent {
		return false
	}

	// We wrap this block in a func so we can defer c.workqueue.Done.
	errparent := func(objparent interface{}) error {
		// We call Done here so the workqueue knows we have finished
		// processing this item. We also must remember to call Forget if we
		// do not want this work item being re-queued. For example, we do
		// not call Forget if a transient error occurs, instead the item is
		// put back on the workqueue and attempted again after a back-off
		// period.
		defer c.parentworkqueue.Done(objparent)
		var key string
		var ok bool
		// We expect strings to come off the workqueue. These are of the
		// form namespace/name. We do this as the delayed nature of the
		// workqueue means the items in the informer cache may actually be
		// more up to date that when the item was initially put onto the
		// workqueue.
		if key, ok = objparent.(string); !ok {
			// As the item in the workqueue is actually invalid, we call
			// Forget here else we'd go into a loop of attempting to
			// process a work item that is invalid.
			c.parentworkqueue.Forget(objparent)
			utilRuntime.HandleError(fmt.Errorf("expected string in workqueue but got %#v", objparent))
			return nil
		}
		// Run the syncHandler, passing it the namespace/name string of the
		// Quota resource to be synced.
		if err := c.syncHandlerParent(key); err != nil {
			// Put the item back on the parentworkqueue to handle any transient errors.
			c.parentworkqueue.AddRateLimited(key)
			return fmt.Errorf("error syncing '%s': %s, requeuing", key, err.Error())
		}
		// Finally, if no error occurs we Forget this item so it does not
		// get queued again until another change happens.
		c.parentworkqueue.Forget(objparent)
		klog.Infof("Successfully synced '%s'", key)
		return nil
	}(objparent)

	if errparent != nil {
		utilRuntime.HandleError(errparent)
		return true
	}

	return true
}

/***
func (c *Controller) handleObject(obj interface{}) {
	var object metav1.Object
	var ok bool
	if object, ok = obj.(metav1.Object); !ok {
		tombstone, ok := obj.(cache.DeletedFinalStateUnknown)
		if !ok {
			utilruntime.HandleError(fmt.Errorf("error decoding object, invalid type"))
			return
		}
		object, ok = tombstone.Obj.(metav1.Object)
		if !ok {
			utilruntime.HandleError(fmt.Errorf("error decoding object tombstone, invalid type"))
			return
		}
		klog.V(4).Infof("Recovered deleted object '%s' from tombstone", object.GetName())
	}
	klog.V(4).Infof("Processing object: %s", object.GetName())
	if ownerRef := metav1.GetControllerOf(object); ownerRef != nil {

		return
	}
}
***/
