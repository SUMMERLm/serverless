/*
Copyright 2023 summerlmm.

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

package controller

import (
	"context"
	"encoding/json"
	"fmt"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/klog/v2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	serverlessv1 "github.com/SUMMERLm/serverless/api/v1"
	hermesv1 "github.com/jinxin-fu/hermes/pkg/adaptor/apis/hermes/v1"
)

type HpaThreadhold struct {
	CpuMin int `json:"cpuMin"`
	CpuMax int `json:"cpuMax"`
	MemMin int `json:"memMin"`
	MemMax int `json:"memMax"`
	QpsMin int `json:"qpsMin"`
	QpsMax int `json:"qpsMax"`
}

// ServerlessReconciler reconciles a Serverless object
const serverlessFinalizerName string = "serverless.hpa.finalizers.pml.com.cn"
const subPromNamespace string = "hypermonitor"

// ServerlessReconciler reconciles a Serverless object
type ServerlessReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

//+kubebuilder:rbac:groups=serverless.pml.com.cn,resources=serverlesses,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=serverless.pml.com.cn,resources=serverlesses/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=serverless.pml.com.cn,resources=serverlesses/finalizers,verbs=update
//+kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=apps,resources=replicasets,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=hermes.pml.com,resources=subscriberrules,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=serverless.pml.com.cn,resources=quotas,verbs=get;list;watch;create;update;patch;delete

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the Serverless object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.14.4/pkg/reconcile

func (r *ServerlessReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	_ = log.FromContext(ctx)

	serverlessInstance := &serverlessv1.Serverless{}
	err := r.Get(ctx, req.NamespacedName, serverlessInstance)

	if err != nil {
		klog.Error(err, "unable to fetch serverlessInstance")
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// examine DeletionTimestamp to determine if object is under deletion
	if serverlessInstance.ObjectMeta.DeletionTimestamp.IsZero() {
		// The object is not being deleted, so if it does not have our finalizer,
		// then lets add the finalizer and update the object. This is equivalent
		// registering our finalizer.
		if !controllerutil.ContainsFinalizer(serverlessInstance, serverlessFinalizerName) {
			controllerutil.AddFinalizer(serverlessInstance, serverlessFinalizerName)
			//todo: judge to remove logical or not
			if err := r.Update(ctx, serverlessInstance); err != nil {
				return ctrl.Result{}, err
			}

		}
	} else {
		// The object is being deleted
		if controllerutil.ContainsFinalizer(serverlessInstance, serverlessFinalizerName) {
			// our finalizer is present, so lets handle any external dependency
			if err := r.deleteExternalPrometheusResources(ctx, req, serverlessInstance); err != nil {
				// if fail to delete the external dependency here, return with error
				// so that it can be retried
				return ctrl.Result{}, err
			}
			// our finalizer is present, so lets handle any external dependency
			//if serverlessInstance.Spec.Workload.TraitServerless.MaxQPS > 0 {
			//	if err := r.deleteExternalPrometheusQuotaResources(ctx, serverlessInstance); err != nil {
			//		// if fail to delete the external dependency here, return with error
			//		// so that it can be retried
			//		return ctrl.Result{}, err
			//	}
			//}
			// remove our finalizer from the list and update it.
			controllerutil.RemoveFinalizer(serverlessInstance, serverlessFinalizerName)
			if err := r.Update(ctx, serverlessInstance); err != nil {
				return ctrl.Result{}, err
			}
		}

		// Stop reconciliation as the item is being deleted
		return ctrl.Result{}, nil
	}

	// find deployment
	if req.Name == "" {
		utilruntime.HandleError(fmt.Errorf("deployment name must be specified"))
		return ctrl.Result{}, err
	}

	threadholds_cr := serverlessInstance.Spec.Workload.TraitServerless.Threshold
	var threadHold HpaThreadhold

	var thread = map[string]int{
		"cpuMin": 0,
		"cpuMax": 0,
		"memMin": 0,
		"memMax": 0,
		"qpsMin": 0,
		"qpsMax": 0,
	}

	json.Unmarshal([]byte(threadholds_cr), &thread)
	json.Unmarshal([]byte(threadholds_cr), &threadHold)
	threadHold.MemMax = thread["memMax"]
	threadHold.MemMin = thread["memMin"]
	threadHold.CpuMax = thread["cpuMax"]
	threadHold.CpuMin = thread["cpuMin"]
	threadHold.QpsMax = thread["qpsMax"]
	threadHold.QpsMin = thread["qpsMin"]

	klog.Infof("Serverless deployment threadhold: %s ", threadHold)
	hpaWebUrl, err := r.hpaurlFromConfigmap(ctx, "serverless-config", "serverless-system")
	if err != nil {
		klog.Error(err)
		return ctrl.Result{}, err
	}
	localCluser, parentCLuster, err := r.localAndParentClusterNameFromConfigmap(ctx, "serverless-config", "serverless-system")
	if err != nil {
		klog.Error(err)
		return ctrl.Result{}, err
	}
	etcdEndpoint, err := r.etcdEndpointFromConfigmap(ctx, "serverless-config", "serverless-system")
	if err != nil {
		klog.Error(err)
		return ctrl.Result{}, err
	}
	klog.Infof("HPA url from configmap is : %s ", hpaWebUrl)
	klog.Infof("localClusterName and parentCluster name  from configmap is : %s ,%s", localCluser, parentCLuster)

	deployment := &appsv1.Deployment{}
	replicaSet := &appsv1.ReplicaSet{}
	podsOfReplica := &corev1.PodList{}

	//req.Name = serverlessInstance.Spec.Name
	//req.Namespace = serverlessInstance.Spec.Namespace
	err = r.Get(ctx, req.NamespacedName, deployment)
	req.Name = serverlessInstance.Name
	req.Namespace = serverlessInstance.Namespace
	if err != nil {
		// if not found, create a new deploy
		//judge funding member
		klog.Infof("Serverlss deployment create new")
		if err = r.createDeployment(ctx, serverlessInstance); err != nil {
			klog.Error(err, "error")
			return ctrl.Result{}, err
		}

		req.Name = serverlessInstance.Spec.Name
		req.Namespace = serverlessInstance.Spec.Namespace
		err = r.createSubPromWithYaml(ctx, req, serverlessInstance.Spec.Name, serverlessInstance.Spec.Namespace, threadHold, hpaWebUrl)
		if err != nil {
			klog.Error(err)
			return ctrl.Result{}, err
		}
		err = r.createQuota(ctx, req, localCluser, parentCLuster, etcdEndpoint, serverlessInstance, serverlessInstance.Spec.Name, serverlessInstance.Spec.Namespace)
		if err != nil {
			klog.Error(err)
			return ctrl.Result{}, err
		}

	} else {
		//UPDATE
		//TODO judge funding member
		klog.Infof("Serverless deployment update")
		if err = r.updateDeployment(ctx, req, serverlessInstance, deployment, replicaSet, podsOfReplica, localCluser); err != nil {
			//if err = r.createDeployment(ctx, serverlessInstance); err != nil {
			klog.Error(err, "error")
			return ctrl.Result{}, err
		}

		req.Name = serverlessInstance.Spec.Name
		req.Namespace = serverlessInstance.Spec.Namespace
		//Todo update qouta subProm
		err = r.updateSubPromWithYaml(ctx, req, serverlessInstance.Spec.Name, serverlessInstance.Spec.Namespace, threadHold, hpaWebUrl)
		if err != nil {
			klog.Error(err)
			return ctrl.Result{}, err
		}
		//TODO update quota cr: next S add GlobalQuota
		err = r.updateQuota(ctx, req, localCluser, parentCLuster, etcdEndpoint, serverlessInstance, serverlessInstance.Spec.Name, serverlessInstance.Spec.Namespace)
		if err != nil {
			klog.Error(err)
			return ctrl.Result{}, err
		}
	}

	return ctrl.Result{}, nil
}

func (r *ServerlessReconciler) deleteExternalPrometheusResources(ctx context.Context, req ctrl.Request, serverless *serverlessv1.Serverless) error {
	// Delete any external prometheus resources associated with the serverless
	// Ensure that delete implementation is idempotent and safe to invoke
	// multiple times for same object.
	var Subscribelist hermesv1.SubscriberRuleList
	//err := r.List(ctx, podsOfReplica, client.InNamespace(req.Namespace), client.MatchingLabels{"apps.gaia.io/component": req.Name}); err != nil {

	if err := r.List(ctx, &Subscribelist, client.InNamespace(subPromNamespace), client.MatchingLabels{"serverless.cluster.pml.com.cn/serverless": req.Name + req.Namespace}); err != nil {
		return nil
	}
	for _, sub := range Subscribelist.Items {
		err := r.Delete(ctx, &sub)
		if err != nil {
			klog.Error(err, "error")
			return err
		}
	}
	return nil
}

func (r *ServerlessReconciler) hpaurlFromConfigmap(ctx context.Context, configMapName string, Namespace string) (string, error) {
	foundConfigMap := &corev1.ConfigMap{}
	err := r.Get(ctx, types.NamespacedName{Name: configMapName, Namespace: Namespace}, foundConfigMap)
	if err != nil {
		// If a configMap name is provided, then it must exist
		// You will likely want to create an Event for the user to understand why their reconcile is failing.
		klog.Error(err, "error")
		return "", err
	}
	return foundConfigMap.Data["hpaUrl"], nil
}

func (r *ServerlessReconciler) QuotaurlFromConfigmap(ctx context.Context, configMapName string, Namespace string) (string, error) {
	foundConfigMap := &corev1.ConfigMap{}
	err := r.Get(ctx, types.NamespacedName{Name: configMapName, Namespace: Namespace}, foundConfigMap)
	if err != nil {
		// If a configMap name is provided, then it must exist
		// You will likely want to create an Event for the user to understand why their reconcile is failing.
		klog.Error(err, "error")
		return "", err
	}
	return foundConfigMap.Data["qpsQuotaUrl"], nil
}

func (r *ServerlessReconciler) localAndParentClusterNameFromConfigmap(ctx context.Context, configMapName string, Namespace string) (string, string, error) {
	foundConfigMap := &corev1.ConfigMap{}
	err := r.Get(ctx, types.NamespacedName{Name: configMapName, Namespace: Namespace}, foundConfigMap)
	if err != nil {
		// If a configMap name is provided, then it must exist
		// You will likely want to create an Event for the user to understand why their reconcile is failing.
		klog.Error(err, "error")
		return "", "", err
	}
	return foundConfigMap.Data["localClusterName"], foundConfigMap.Data["parentClusterName"], nil
}

func (r *ServerlessReconciler) etcdEndpointFromConfigmap(ctx context.Context, configMapName string, Namespace string) (string, error) {
	foundConfigMap := &corev1.ConfigMap{}
	err := r.Get(ctx, types.NamespacedName{Name: configMapName, Namespace: Namespace}, foundConfigMap)
	if err != nil {
		// If a configMap name is provided, then it must exist
		// You will likely want to create an Event for the user to understand why their reconcile is failing.
		klog.Error(err, "error")
		return "", err
	}
	return foundConfigMap.Data["etcdEndpoint"], nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *ServerlessReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&serverlessv1.Serverless{}).
		Complete(r)
}
