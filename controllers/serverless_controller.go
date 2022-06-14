/*
Copyright 2022.

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
package controllers

import (
	"context"
	"encoding/json"
	"fmt"
	hermesv1 "github.com/jinxin-fu/hermes/pkg/adaptor/apis/hermes/v1"
	prometheusv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	"k8s.io/utils/pointer"
	"reflect"

	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/klog/v2"
	"os"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"strconv"

	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	serverlessv1 "github.com/SUMMERLm/serverless/api/v1"
)

type HpaThreadhold struct {
	cpuMin int `json:"cpuMin"`
	cpuMax int `json:"cpuMax"`
	memMin int `json:"memMin"`
	memMax int `json:"memMax"`
	qpsMin int `json:"qpsMin"`
	qpsMax int `json:"qpsMax"`
}

// ServerlessReconciler reconciles a Serverless object
const serverlessFinalizerName string = "serverless.hpa.finalizers.pml.com.cn"
const subPromNamespace string = "hypermonitor"

type ServerlessReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

//+kubebuilder:rbac:groups=serverless.pml.com.cn,resources=serverlesses,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=serverless.pml.com.cn,resources=serverlesses/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=serverless.pml.com.cn,resources=serverlesses/finalizers,verbs=update
//+kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=hermes.pml.com,resources=subscriberrules,verbs=get;list;watch;create;update;patch;delete

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// the Serverless object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.11.0/pkg/reconcile
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
			if err := r.Update(ctx, serverlessInstance); err != nil {
				return ctrl.Result{}, err
			}
		}
	} else {
		// The object is being deleted
		if controllerutil.ContainsFinalizer(serverlessInstance, serverlessFinalizerName) {
			// our finalizer is present, so lets handle any external dependency
			if err := r.deleteExternalPrometheusResources(ctx, serverlessInstance); err != nil {
				// if fail to delete the external dependency here, return with error
				// so that it can be retried
				return ctrl.Result{}, err
			}

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
		"cpuMin": 20,
		"cpuMax": 70,
		"memMin": 20,
		"memMax": 80,
		"qpsMin": 10,
		"qpsMax": 100,
	}

	json.Unmarshal([]byte(threadholds_cr), &thread)
	json.Unmarshal([]byte(threadholds_cr), &threadHold)
	threadHold.memMax = thread["memMax"]
	threadHold.memMin = thread["memMin"]
	threadHold.cpuMax = thread["cpuMax"]
	threadHold.cpuMin = thread["cpuMin"]
	threadHold.qpsMax = thread["qpsMax"]
	threadHold.qpsMin = thread["qpsMin"]

	klog.Infof("Servlss deployment threadhold: %s ", threadHold)

	deployment := &appsv1.Deployment{}
	req.Name = serverlessInstance.Spec.Name
	req.Namespace = serverlessInstance.Spec.Namespace
	err = r.Get(ctx, req.NamespacedName, deployment)
	req.Name = serverlessInstance.Name
	req.Namespace = serverlessInstance.Namespace

	if err != nil {
		// if not found, create a new deploy
		klog.Infof("Servlss deployment create new")
		if err = r.createDeployment(ctx, serverlessInstance); err != nil {
			klog.Error(err, "error")
			return ctrl.Result{}, err
		}
		hpaWebUrl := os.Getenv("SERVERLESS_HPA_URL")
		klog.Infof("HPA url is : %s ", hpaWebUrl)
		req.Name = serverlessInstance.Spec.Name
		req.Namespace = serverlessInstance.Spec.Namespace
		err = r.createSubPromWithYaml(ctx, req, serverlessInstance, serverlessInstance.Spec.Name, serverlessInstance.Spec.Namespace, threadHold, hpaWebUrl)
		if err != nil {
			klog.Error(err)
			return ctrl.Result{}, err
		}
	} else {
		//UPDATE
		klog.Infof("Servlss deployment update")
		if err = r.updateDeployment(ctx, serverlessInstance); err != nil {
			//if err = r.createDeployment(ctx, serverlessInstance); err != nil {
			klog.Error(err, "error")
			return ctrl.Result{}, err
		}

		hpaWebUrl := os.Getenv("SERVERLESS_HPA_URL")
		klog.Infof("HPA url is : %s ", hpaWebUrl)
		req.Name = serverlessInstance.Spec.Name
		req.Namespace = serverlessInstance.Spec.Namespace
		err = r.updateSubPromWithYaml(ctx, req, serverlessInstance, serverlessInstance.Spec.Name, serverlessInstance.Spec.Namespace, threadHold, hpaWebUrl)
		if err != nil {
			klog.Error(err)
			return ctrl.Result{}, err
		}
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *ServerlessReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&serverlessv1.Serverless{}).
		Complete(r)
}

func (r *ServerlessReconciler) createDeployment(ctx context.Context, serverless *serverlessv1.Serverless) error {
	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: serverless.Spec.Namespace,
			Name:      serverless.Spec.Name,
		},
		Spec: appsv1.DeploymentSpec{
			// init num is 2 for serverless
			Replicas: pointer.Int32Ptr(2),
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"app": serverless.Spec.Name,
				},
			},
		},
	}
	deployment.Spec.Template = serverless.Spec.Module
	// owner reference to be used when delete serverless cr
	if err := controllerutil.SetControllerReference(serverless, deployment, r.Scheme); err != nil {
		return err
	}
	// new deployment
	if err := r.Create(ctx, deployment); err != nil {
		return err
	}

	return nil
}

func (r *ServerlessReconciler) updateDeployment(ctx context.Context, serverless *serverlessv1.Serverless) error {
	deployMentByServerless := &appsv1.Deployment{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Deployment",
			APIVersion: "apps/v1",
		}, ObjectMeta: metav1.ObjectMeta{
			Namespace: serverless.Spec.Namespace,
			Name:      serverless.Spec.Name,
		},
		Spec: appsv1.DeploymentSpec{
			// init num is 2 for serverless
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"app": serverless.Spec.Name,
				},
			},
		},
	}
	deployMentByServerless.Spec.Template = serverless.Spec.Module

	if err := controllerutil.SetControllerReference(serverless, deployMentByServerless, r.Scheme); err != nil {
		klog.Error(err)
		return err
	}
	klog.Infof("successfully update deployment by serverless %q", deployMentByServerless)
	klog.Infof("start update deployment")
	if err := r.Update(ctx, deployMentByServerless); err != nil {
		klog.Error(err, "update deployment error")
		return err
	}
	klog.Infof("update deployment success")
	return nil
}

func (r *ServerlessReconciler) createSubPromWithYaml(ctx context.Context, req ctrl.Request, serverless *serverlessv1.Serverless, name string, namespace string, hpa HpaThreadhold, hpaWebUrl string) error {
	const action = "create"
	klog.Infof("satrt create SubProm With Yaml %s", name)
	err := r.subPromWithYaml(ctx, req, serverless, name, namespace, hpa, hpaWebUrl, action)
	if err != nil {
		klog.Error(err)
		return err
	}
	klog.Infof("end create SubProm With Yaml%s", name)

	return nil
}

func (r *ServerlessReconciler) updateSubPromWithYaml(ctx context.Context, req ctrl.Request, serverless *serverlessv1.Serverless, name string, namespace string, hpa HpaThreadhold, hpaWebUrl string) error {
	const action = "update"
	klog.Infof("satrt update SubProm With Yaml %s", name)
	err := r.subPromWithYaml(ctx, req, serverless, name, namespace, hpa, hpaWebUrl, action)
	if err != nil {
		klog.Error(err)
		return err
	}
	klog.Infof("end update SubProm With Yaml %s", name)
	return nil
}

func (r *ServerlessReconciler) subPromWithYaml(ctx context.Context, req ctrl.Request, serverless *serverlessv1.Serverless, name string, namespace string, hpa HpaThreadhold, hpaWebUrl string, action string) error {
	srs := &hermesv1.SubscriberRule{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "hermes.pml.com/v1",
			Kind:       "SubscriberRule",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: subPromNamespace,
		},
	}
	//hpa threadhold
	//Todo: use dynamic admission controller to check the threadhold
	if hpa.cpuMax > 0 || hpa.memMax > 0 || hpa.qpsMax > 0 {
		klog.Infof("add subprom Max")
	} else {
		klog.Error("limit of serverless is false")
	}
	if hpa.cpuMin > 0 || hpa.memMin > 0 || hpa.qpsMin > 0 {
		klog.Infof("add subprom Min")
	} else {
		klog.Error("limit of serverless is false")
	}
	subReciverAddr := hpaWebUrl
	alertHeaderUp := namespace + "+" + name + "+scale_up"
	alertHeaderDown := namespace + "+" + name + "+scale_down"
	aggerateRulesHigh := "(sum(avg_over_time(container_memory_rss{component_name=\"" + name + "\"}[5m]))/sum((avg_over_time(container_spec_memory_limit_bytes{component_name=\"" + name + "\"}[5m])))*100 > bool " + strconv.Itoa(hpa.memMax) + ") or (sum(avg_over_time(container_cpu_usage_seconds_total{component_name=\"" + name + "\"}[5m])) / sum(avg_over_time(container_spec_cpu_quota{component_name=\"" + name + "\"}[5m])/100000)*100 > bool " + strconv.Itoa(hpa.cpuMax) + ") "
	aggerateRulesLow := "(sum(avg_over_time(container_memory_rss{component_name=\"" + name + "\"}[5m]))/sum((avg_over_time(container_spec_memory_limit_bytes{component_name=\"" + name + "\"}[5m])))*100 < bool " + strconv.Itoa(hpa.memMin) + ") and (sum(avg_over_time(container_cpu_usage_seconds_total{component_name=\"" + name + "\"}[5m])) / sum(avg_over_time(container_spec_cpu_quota{component_name=\"" + name + "\"}[5m])/100000)*100 < bool " + strconv.Itoa(hpa.cpuMin) + ") "
	srRuleSpec := hermesv1.SubscriberRuleSpec{
		SubscribeType:     "SubsCondition",
		SubscriberAddress: subReciverAddr,
		PrometheusRule: prometheusv1.PrometheusRuleSpec{
			Groups: []prometheusv1.RuleGroup{
				{
					Name: "serverless.rules." + name + "." + namespace,
					Rules: []prometheusv1.Rule{
						{
							Alert: alertHeaderUp,
							Expr:  intstr.FromString(aggerateRulesHigh),
							Labels: map[string]string{
								"alertlabel": "serverless",
							},
							Annotations: map[string]string{
								"aggerateRules":   aggerateRulesHigh,
								"receiverAddress": subReciverAddr,
								"returnValueFlag": "false",
							},
						},
						{
							Alert: alertHeaderDown,
							Expr:  intstr.FromString(aggerateRulesLow),
							Labels: map[string]string{
								"alertlabel": "serverless",
							},
							Annotations: map[string]string{
								"aggerateRules":   aggerateRulesLow,
								"receiverAddress": subReciverAddr,
								"returnValueFlag": "false",
							},
						},
					},
				},
			},
		},
	}
	srs.Spec = srRuleSpec

	if action == "create" {
		klog.Infof("create obj is :%s ", srs)
		err := r.Create(ctx, srs)
		if err != nil {
			return err
		}
		return nil
	} else if action == "update" {
		subscribeInstance := &hermesv1.SubscriberRule{}
		req.Namespace = subPromNamespace
		err := r.Get(ctx, req.NamespacedName, subscribeInstance)
		if err != nil {
			return err
		}
		if reflect.DeepEqual(subscribeInstance.Spec, srs.Spec) {
			klog.Infof("no updates on the spec of promruleInstance %q, skipping syncing", subscribeInstance.Name)
			return nil
		}
		klog.Infof("updates on the spec of promruleInstance %q, syncing start", subscribeInstance.Name)
		subscribeInstance.Spec = srs.Spec
		err = r.Update(ctx, subscribeInstance)
		if err != nil {
			return err
		}
		req.Namespace = namespace
		klog.Infof("updates on the spec of promruleInstance %q, syncing done", subscribeInstance.Name)
	}
	return nil
}

func (r *ServerlessReconciler) deleteExternalPrometheusResources(ctx context.Context, serverless *serverlessv1.Serverless) error {
	// Delete any external prometheus resources associated with the serverless
	// Ensure that delete implementation is idempotent and safe to invoke
	// multiple times for same object.
	srs := &hermesv1.SubscriberRule{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "hermes.pml.com/v1",
			Kind:       "SubscriberRule",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      serverless.Spec.Name,
			Namespace: subPromNamespace,
		},
	}
	klog.Infof("Recicle SubscriberRule obj is :%s ", srs)
	err := r.Delete(ctx, srs)
	if err != nil {
		return err
	}
	return nil

}
