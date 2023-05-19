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
	prometheusv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	appsv1 "k8s.io/api/apps/v1"
	"reflect"

	"context"
	"encoding/json"
	"fmt"
	hermesv1 "github.com/jinxin-fu/hermes/pkg/adaptor/apis/hermes/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"strconv"
	"time"

	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	serverlessv1 "github.com/SUMMERLm/serverless/api/v1"
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
//+kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=hermes.pml.com,resources=subscriberrules,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=serverless.pml.com.cn,resources=quota,verbs=get;list;watch;create;update;patch;delete

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
		//hpaWebUrl := os.Getenv("SERVERLESS_HPA_URL")
		//klog.Infof("HPA url is: %s ", hpaWebUrl)
		hpaWebUrl, err := r.hpaurlFromConfigmap(ctx, "serverless-config", "serverless-system")
		if err != nil {
			klog.Error(err)
			return ctrl.Result{}, err
		}
		klog.Infof("HPA url from configmap is : %s ", hpaWebUrl)
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
		if err = r.updateDeployment(ctx, serverlessInstance, deployment); err != nil {
			//if err = r.createDeployment(ctx, serverlessInstance); err != nil {
			klog.Error(err, "error")
			return ctrl.Result{}, err
		}

		//hpaWebUrl := os.Getenv("SERVERLESS_HPA_URL")
		//klog.Infof("HPA url is : %s ", hpaWebUrl)
		hpaWebUrl, err := r.hpaurlFromConfigmap(ctx, "serverless-config", "serverless-system")
		if err != nil {
			klog.Error(err)
			return ctrl.Result{}, err
		}
		klog.Infof("HPA url from configmap is : %s ", hpaWebUrl)
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

func (r *ServerlessReconciler) createDeployment(ctx context.Context, serverless *serverlessv1.Serverless) error {
	//serverless init num of deploy.
	replica := int32(1)
	klog.Infof("start create deployment", serverless.Spec.Name)
	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: serverless.Spec.Namespace,
			Name:      serverless.Spec.Name,
		},
		Spec: appsv1.DeploymentSpec{
			// init num is 1 for serverless
			Replicas: &replica,
			Selector: &metav1.LabelSelector{
				MatchLabels: serverless.Spec.Module.Labels,
			},
		},
	}
	deployment.Spec.Template = serverless.Spec.Module
	//deployment.Spec.Replicas = &replica
	// owner reference to be used when delete serverless cr
	if err := controllerutil.SetControllerReference(serverless, deployment, r.Scheme); err != nil {
		return err
	}
	// new deployment
	if err := r.Create(ctx, deployment); err != nil {
		return err
	}

	klog.Infof("end create deployment", *deployment.Spec.Replicas)
	klog.Infof("end create deployment", serverless.Spec.Name)

	return nil
}

func (r *ServerlessReconciler) updateDeployment(ctx context.Context, serverless *serverlessv1.Serverless, deployment *appsv1.Deployment) error {
	replica := deployment.Spec.Replicas
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
			Replicas: replica,
			Selector: &metav1.LabelSelector{
				MatchLabels: serverless.Spec.Module.Labels,
			},
		},
	}
	deployMentByServerless.Spec.Template = serverless.Spec.Module

	if err := controllerutil.SetControllerReference(serverless, deployMentByServerless, r.Scheme); err != nil {
		klog.Error(err)
		return err
	}
	klog.Infof("start update deployment")
	if err := r.Update(ctx, deployMentByServerless); err != nil {
		klog.Error(err, "update deployment error")
		return err
	}
	klog.Infof("end update deployment: relice is", *deployMentByServerless.Spec.Replicas)
	klog.Infof("end update deployment: name is ", serverless.Spec.Name)
	klog.Infof("update deployment success")
	return nil
}

func (r *ServerlessReconciler) createSubPromWithYaml(ctx context.Context, req ctrl.Request, serverless *serverlessv1.Serverless, name string, namespace string, hpa HpaThreadhold, hpaWebUrl string) error {
	const action = "create"
	klog.Infof("satrt create SubProm With Yaml %s", name)
	//first time wait deploy msg sync
	time.Sleep(time.Duration(15) * time.Second)
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
	//add deployement uuid
	deployment := &appsv1.Deployment{}
	err := r.Get(ctx, req.NamespacedName, deployment)
	//hpa threadhold
	if err != nil {
		klog.Error(err)
		return err
	}
	uidOfDeploy := deployment.UID
	klog.Infof("uidOfDeploy:=deployment.UID", uidOfDeploy)

	//Todo: use dynamic admission controller to check the threadhold
	if hpa.CpuMax > 0 || hpa.MemMax > 0 || hpa.QpsMax > 0 {
		klog.Infof("add subprom Max")
	} else {
		klog.Error("limit of serverless is false")
	}
	var aggerateRulesHigh string
	var aggerateRulesLow string
	if hpa.CpuMin > 0 && hpa.MemMin > 0 && hpa.QpsMin > 0 {
		aggerateRulesHigh = "((sum(rate(io_sid_traffics{component_id=\"" + string(uidOfDeploy) + "\",component_name=\"" + name + "\"}[2m])))/(count(rate(io_sid_traffics{component_id=\"" + string(uidOfDeploy) + "\",component_name=\"" + name + "\"}[2m])))" + "*sum(((count(io_sid_traffics{pod=~\"" + name + ".*\"})by(pod))) )/count(((count(io_sid_traffics{pod=~\"" + name + ".*\"})by(pod))))" + "> bool " + strconv.Itoa(hpa.QpsMax) + ") + (sum(avg_over_time(container_memory_usage_bytes{component_id=\"" + string(uidOfDeploy) + "\",component_name=\"" + name + "\"}[2m]))/sum((avg_over_time(container_spec_memory_limit_bytes{component_id=\"" + string(uidOfDeploy) + "\",component_name=\"" + name + "\"}[2m])))*100 > bool " + strconv.Itoa(hpa.MemMax) + ") + (sum(rate(container_cpu_usage_seconds_total{component_id=\"" + string(uidOfDeploy) + "\",component_name=\"" + name + "\"}[2m])) / sum(avg_over_time(container_spec_cpu_quota{component_id=\"" + string(uidOfDeploy) + "\",component_name=\"" + name + "\"}[2m])/100000)*100 > bool " + strconv.Itoa(hpa.CpuMax) + ") >0"
		aggerateRulesLow = "((sum(rate(io_sid_traffics{component_id=\"" + string(uidOfDeploy) + "\",component_name=\"" + name + "\"}[2m])))/(count(rate(io_sid_traffics{component_id=\"" + string(uidOfDeploy) + "\",component_name=\"" + name + "\"}[2m]))" + "*sum(((count(io_sid_traffics{pod=~\"" + name + ".*\"})by(pod))) )/count(((count(io_sid_traffics{pod=~\"" + name + ".*\"})by(pod))))" + ")< bool " + strconv.Itoa(hpa.QpsMin) + ") + (sum(avg_over_time(container_memory_usage_bytes{component_id=\"" + string(uidOfDeploy) + "\",component_name=\"" + name + "\"}[2m]))/sum((avg_over_time(container_spec_memory_limit_bytes{component_id=\"" + string(uidOfDeploy) + "\",component_name=\"" + name + "\"}[2m])))*100 < bool " + strconv.Itoa(hpa.MemMin) + ") + (sum(rate(container_cpu_usage_seconds_total{component_id=\"" + string(uidOfDeploy) + "\",component_name=\"" + name + "\"}[2m])) / sum(avg_over_time(container_spec_cpu_quota{component_id=\"" + string(uidOfDeploy) + "\",component_name=\"" + name + "\"}[2m])/100000)*100 < bool " + strconv.Itoa(hpa.CpuMin) + ") >2"
	} else if hpa.CpuMin > 0 && hpa.MemMin > 0 && hpa.QpsMin == 0 {
		aggerateRulesHigh = "(sum(avg_over_time(container_memory_usage_bytes{component_id=\"" + string(uidOfDeploy) + "\",component_name=\"" + name + "\"}[2m]))/sum((avg_over_time(container_spec_memory_limit_bytes{component_id=\"" + string(uidOfDeploy) + "\",component_name=\"" + name + "\"}[2m])))*100 > bool " + strconv.Itoa(hpa.MemMax) + ") + (sum(rate(container_cpu_usage_seconds_total{component_id=\"" + string(uidOfDeploy) + "\",component_name=\"" + name + "\"}[2m])) / sum(avg_over_time(container_spec_cpu_quota{component_id=\"" + string(uidOfDeploy) + "\",component_name=\"" + name + "\"}[2m])/100000)*100 > bool " + strconv.Itoa(hpa.CpuMax) + ") >0"
		aggerateRulesLow = "(sum(avg_over_time(container_memory_usage_bytes{component_id=\"" + string(uidOfDeploy) + "\",component_name=\"" + name + "\"}[2m]))/sum((avg_over_time(container_spec_memory_limit_bytes{component_id=\"" + string(uidOfDeploy) + "\",component_name=\"" + name + "\"}[2m])))*100 < bool " + strconv.Itoa(hpa.MemMin) + ") + (sum(rate(container_cpu_usage_seconds_total{component_id=\"" + string(uidOfDeploy) + "\",component_name=\"" + name + "\"}[2m])) / sum(avg_over_time(container_spec_cpu_quota{component_id=\"" + string(uidOfDeploy) + "\",component_name=\"" + name + "\"}[2m])/100000)*100 < bool " + strconv.Itoa(hpa.CpuMin) + ") >1"
	} else if hpa.CpuMin > 0 && hpa.QpsMin > 0 && hpa.MemMin == 0 {
		aggerateRulesHigh = "((sum(rate(io_sid_traffics{component_id=\"" + string(uidOfDeploy) + "\",component_name=\"" + name + "\"}[2m])))/(count(rate(io_sid_traffics{component_id=\"" + string(uidOfDeploy) + "\",component_name=\"" + name + "\"}[2m])))" + "*sum(((count(io_sid_traffics{pod=~\"" + name + ".*\"})by(pod))) )/count(((count(io_sid_traffics{pod=~\"" + name + ".*\"})by(pod))))" + "> bool " + strconv.Itoa(hpa.QpsMax) + ") + (sum(rate(container_cpu_usage_seconds_total{component_id=\"" + string(uidOfDeploy) + "\",component_name=\"" + name + "\"}[2m])) / sum(avg_over_time(container_spec_cpu_quota{component_id=\"" + string(uidOfDeploy) + "\",component_name=\"" + name + "\"}[2m])/100000)*100 > bool " + strconv.Itoa(hpa.CpuMax) + ") >0"
		aggerateRulesLow = "((sum(rate(io_sid_traffics{component_id=\"" + string(uidOfDeploy) + "\",component_name=\"" + name + "\"}[2m])))/(count(rate(io_sid_traffics{component_id=\"" + string(uidOfDeploy) + "\",component_name=\"" + name + "\"}[2m]))" + "*sum(((count(io_sid_traffics{pod=~\"" + name + ".*\"})by(pod))) )/count(((count(io_sid_traffics{pod=~\"" + name + ".*\"})by(pod))))" + ")< bool " + strconv.Itoa(hpa.QpsMin) + ") + (sum(rate(container_cpu_usage_seconds_total{component_id=\"" + string(uidOfDeploy) + "\",component_name=\"" + name + "\"}[2m])) / sum(avg_over_time(container_spec_cpu_quota{component_id=\"" + string(uidOfDeploy) + "\",component_name=\"" + name + "\"}[2m])/100000)*100 < bool " + strconv.Itoa(hpa.CpuMin) + ") >1"
		klog.Infof("add subprom Min")
	} else if hpa.MemMin > 0 && hpa.QpsMin > 0 && hpa.CpuMin == 0 {
		aggerateRulesHigh = "((sum(rate(io_sid_traffics{component_id=\"" + string(uidOfDeploy) + "\",component_name=\"" + name + "\"}[2m])))/(count(rate(io_sid_traffics{component_id=\"" + string(uidOfDeploy) + "\",component_name=\"" + name + "\"}[2m]))" + "*sum(((count(io_sid_traffics{pod=~\"" + name + ".*\"})by(pod))) )/count(((count(io_sid_traffics{pod=~\"" + name + ".*\"})by(pod))))" + ")> bool " + strconv.Itoa(hpa.QpsMax) + ") + (sum(avg_over_time(container_memory_usage_bytes{component_id=\"" + string(uidOfDeploy) + "\",component_name=\"" + name + "\"}[2m]))/sum((avg_over_time(container_spec_memory_limit_bytes{component_id=\"" + string(uidOfDeploy) + "\",component_name=\"" + name + "\"}[2m])))*100 > bool " + strconv.Itoa(hpa.MemMax) + ")>0"
		aggerateRulesLow = "((sum(rate(io_sid_traffics{component_id=\"" + string(uidOfDeploy) + "\",component_name=\"" + name + "\"}[2m])))/(count(rate(io_sid_traffics{component_id=\"" + string(uidOfDeploy) + "\",component_name=\"" + name + "\"}[2m]))" + "*sum(((count(io_sid_traffics{pod=~\"" + name + ".*\"})by(pod))) )/count(((count(io_sid_traffics{pod=~\"" + name + ".*\"})by(pod))))" + ")< bool " + strconv.Itoa(hpa.QpsMin) + ") + (sum(avg_over_time(container_memory_usage_bytes{component_id=\"" + string(uidOfDeploy) + "\",component_name=\"" + name + "\"}[2m]))/sum((avg_over_time(container_spec_memory_limit_bytes{component_id=\"" + string(uidOfDeploy) + "\",component_name=\"" + name + "\"}[2m])))*100 < bool " + strconv.Itoa(hpa.MemMin) + ")>1"
		klog.Infof("add subprom Min")
	} else if hpa.CpuMin > 0 && hpa.MemMin == 0 && hpa.QpsMin == 0 {
		aggerateRulesHigh = "sum(rate(container_cpu_usage_seconds_total{component_id=\"" + string(uidOfDeploy) + "\",component_name=\"" + name + "\"}[2m])) / sum(avg_over_time(container_spec_cpu_quota{component_id=\"" + string(uidOfDeploy) + "\",component_name=\"" + name + "\"}[2m])/100000)*100 > bool " + strconv.Itoa(hpa.CpuMax) + " >0"
		aggerateRulesLow = "sum(rate(container_cpu_usage_seconds_total{component_id=\"" + string(uidOfDeploy) + "\",component_name=\"" + name + "\"}[2m])) / sum(avg_over_time(container_spec_cpu_quota{component_id=\"" + string(uidOfDeploy) + "\",component_name=\"" + name + "\"}[2m])/100000)*100 < bool " + strconv.Itoa(hpa.CpuMin) + " >0"
		klog.Infof("add subprom Min")
	} else if hpa.MemMin > 0 && hpa.CpuMin == 0 && hpa.QpsMin == 0 {
		aggerateRulesHigh = "sum(avg_over_time(container_memory_usage_bytes{component_id=\"" + string(uidOfDeploy) + "\",component_name=\"" + name + "\"}[2m]))/sum((avg_over_time(container_spec_memory_limit_bytes{component_id=\"" + string(uidOfDeploy) + "\",component_name=\"" + name + "\"}[2m])))*100 > bool " + strconv.Itoa(hpa.MemMax) + " >0"
		aggerateRulesLow = "sum(avg_over_time(container_memory_usage_bytes{component_id=\"" + string(uidOfDeploy) + "\",component_name=\"" + name + "\"}[2m]))/sum((avg_over_time(container_spec_memory_limit_bytes{component_id=\"" + string(uidOfDeploy) + "\",component_name=\"" + name + "\"}[2m])))*100 < bool " + strconv.Itoa(hpa.MemMin) + " >0"
	} else if hpa.QpsMin > 0 && hpa.MemMin == 0 && hpa.CpuMin == 0 {
		aggerateRulesHigh = "((sum(rate(io_sid_traffics{component_id=\"" + string(uidOfDeploy) + "\",component_name=\"" + name + "\"}[2m])))/(count(rate(io_sid_traffics{component_id=\"" + string(uidOfDeploy) + "\",component_name=\"" + name + "\"}[2m])))" + "*sum(((count(io_sid_traffics{pod=~\"" + name + ".*\"})by(pod))) )/count(((count(io_sid_traffics{pod=~\"" + name + ".*\"})by(pod))))" + ")> bool " + strconv.Itoa(hpa.QpsMax) + " >0"
		//aggerateRulesLow = "((sum(rate(io_sid_traffics{component_id=\"" + string(uidOfDeploy) + "\",component_name=\"" + name + "\"}[2m])))/(count(rate(io_sid_traffics{component_id=\"" + string(uidOfDeploy) + "\",component_name=\"" + name + "\"}[2m])))\n" + "\t*(count(io_sid_traffics{pod=~\"" + name + ".*\"})/2)\n" + ")< bool " + strconv.Itoa(hpa.QpsMin) + " >0"
		aggerateRulesLow = "((sum(rate(io_sid_traffics{component_id=\"" + string(uidOfDeploy) + "\",component_name=\"" + name + "\"}[2m])))/(count(rate(io_sid_traffics{component_id=\"" + string(uidOfDeploy) + "\",component_name=\"" + name + "\"}[2m])))" + "*sum(((count(io_sid_traffics{pod=~\"" + name + ".*\"})by(pod))) )/count(((count(io_sid_traffics{pod=~\"" + name + ".*\"})by(pod))))" + ")< bool " + strconv.Itoa(hpa.QpsMin) + " >0"
	}
	subReciverAddr := hpaWebUrl
	alertHeaderUp := namespace + "+" + name + "+scale_up"
	alertHeaderDown := namespace + "+" + name + "+scale_down"
	klog.Infof("aggerateRulesHigh: ", aggerateRulesHigh)
	klog.Infof("aggerateRulesLow: ", aggerateRulesLow)

	//avg_over_time(io_sid_traffics{component_name="nginx3"}[2m])
	//2m可做成配置文件
	//aggerateRulesHigh := "(io_sid_traffics{component_name=\"" + name + "\"}> bool " + strconv.Itoa(hpa.QpsMax) + ") + (sum(avg_over_time(container_memory_usage_bytes{component_name=\"" + name + "\"}[2m]))/sum((avg_over_time(container_spec_memory_limit_bytes{component_name=\"" + name + "\"}[2m])))*100 > bool " + strconv.Itoa(hpa.MemMax) + ") + (sum(avg_over_time(container_cpu_usage_seconds_total{component_name=\"" + name + "\"}[2m])) / sum(avg_over_time(container_spec_cpu_quota{component_name=\"" + name + "\"}[2m])/100000)*100 > bool " + strconv.Itoa(hpa.CpuMax) + ") "
	//aggerateRulesLow := "(io_sid_traffics{component_name=\"" + name + "\"}< bool " + strconv.Itoa(hpa.QpsMin) + ") and (sum(avg_over_time(container_memory_usage_bytes{component_name=\"" + name + "\"}[2m]))/sum((avg_over_time(container_spec_memory_limit_bytes{component_name=\"" + name + "\"}[2m])))*100 < bool " + strconv.Itoa(hpa.MemMin) + ") and (sum(avg_over_time(container_cpu_usage_seconds_total{component_name=\"" + name + "\"}[2m])) / sum(avg_over_time(container_spec_cpu_quota{component_name=\"" + name + "\"}[2m])/100000)*100 < bool " + strconv.Itoa(hpa.CpuMin) + ") "
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
								"severity":   "critical",
							},
							Annotations: map[string]string{
								"aggerateRules":   aggerateRulesHigh,
								"receiverAddress": subReciverAddr,
								"returnValueFlag": "false",
							},
							For: "1s",
						},
						{
							Alert: alertHeaderDown,
							Expr:  intstr.FromString(aggerateRulesLow),
							Labels: map[string]string{
								"alertlabel": "serverless",
								"severity":   "critical",
							},
							Annotations: map[string]string{
								"aggerateRules":   aggerateRulesLow,
								"receiverAddress": subReciverAddr,
								"returnValueFlag": "false",
							},
							For: "1s",
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

func (r *ServerlessReconciler) hpaurlFromConfigmap(ctx context.Context, configMapName string, Namespace string) (string, error) {
	foundConfigMap := &corev1.ConfigMap{}
	err := r.Get(ctx, types.NamespacedName{Name: configMapName, Namespace: Namespace}, foundConfigMap)
	if err != nil {
		// If a configMap name is provided, then it must exist
		// You will likely want to create an Event for the user to understand why their reconcile is failing.
		return "", err
	}
	return foundConfigMap.Data["hpaUrl"], nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *ServerlessReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&serverlessv1.Serverless{}).
		Complete(r)
}
