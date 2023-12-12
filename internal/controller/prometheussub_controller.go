package controller

import (
	"context"
	hermesv1 "github.com/jinxin-fu/hermes/pkg/adaptor/apis/hermes/v1"
	prometheusv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/klog/v2"
	"reflect"
	ctrl "sigs.k8s.io/controller-runtime"
	"strconv"
	"time"
)

func (r *ServerlessReconciler) createSubPromWithYaml(ctx context.Context, req ctrl.Request, name string, namespace string, hpa HpaThreadhold, hpaWebUrl string) error {
	const action = "create"
	klog.Infof("satrt create SubProm With Yaml %s", name)
	//first time wait deploy msg sync
	time.Sleep(time.Duration(5) * time.Second)
	err := r.subPromWithYaml(ctx, req, name, namespace, hpa, hpaWebUrl, action)
	if err != nil {
		klog.Error(err)
		return err
	}
	klog.Infof("end create SubProm With Yaml %s", name)

	return nil
}

func (r *ServerlessReconciler) updateSubPromWithYaml(ctx context.Context, req ctrl.Request, name string, namespace string, hpa HpaThreadhold, hpaWebUrl string) error {
	const action = "update"
	klog.Infof("satrt update SubProm With Yaml %s", name)
	err := r.subPromWithYaml(ctx, req, name, namespace, hpa, hpaWebUrl, action)
	if err != nil {
		klog.Error(err)
		return err
	}
	klog.Infof("end update SubProm With Yaml %s", name)
	return nil
}

func (r *ServerlessReconciler) subPromWithYaml(ctx context.Context, req ctrl.Request, name string, namespace string, hpa HpaThreadhold, hpaWebUrl string, action string) error {
	srs := &hermesv1.SubscriberRule{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "hermes.pml.com/v1",
			Kind:       "SubscriberRule",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: subPromNamespace,
			Labels: map[string]string{
				"serverless.cluster.pml.com.cn/serverless": req.Name + req.Namespace,
			},
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
	/*
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
	*/
	if hpa.CpuMin > 0 && hpa.MemMin > 0 && hpa.QpsMin > 0 {
		aggerateRulesHigh = "((avg(avg_over_time(io_sid_traffics{component_id=\"" + string(uidOfDeploy) + "\",component_name=\"" + name + "\"}[2m]))) > bool " + strconv.Itoa(hpa.QpsMax) + ") + (sum(avg_over_time(container_memory_usage_bytes{component_id=\"" + string(uidOfDeploy) + "\",component_name=\"" + name + "\"}[2m]))/sum((avg_over_time(container_spec_memory_limit_bytes{component_id=\"" + string(uidOfDeploy) + "\",component_name=\"" + name + "\"}[2m])))*100 > bool " + strconv.Itoa(hpa.MemMax) + ") + (sum(rate(container_cpu_usage_seconds_total{component_id=\"" + string(uidOfDeploy) + "\",component_name=\"" + name + "\"}[2m])) / sum(avg_over_time(container_spec_cpu_quota{component_id=\"" + string(uidOfDeploy) + "\",component_name=\"" + name + "\"}[2m])/100000)*100 > bool " + strconv.Itoa(hpa.CpuMax) + ") >0"
		aggerateRulesLow = "((avg(avg_over_time(io_sid_traffics{component_id=\"" + string(uidOfDeploy) + "\",component_name=\"" + name + "\"}[2m])))  < bool " + strconv.Itoa(hpa.QpsMin) + ") + (sum(avg_over_time(container_memory_usage_bytes{component_id=\"" + string(uidOfDeploy) + "\",component_name=\"" + name + "\"}[2m]))/sum((avg_over_time(container_spec_memory_limit_bytes{component_id=\"" + string(uidOfDeploy) + "\",component_name=\"" + name + "\"}[2m])))*100 < bool " + strconv.Itoa(hpa.MemMin) + ") + (sum(rate(container_cpu_usage_seconds_total{component_id=\"" + string(uidOfDeploy) + "\",component_name=\"" + name + "\"}[2m])) / sum(avg_over_time(container_spec_cpu_quota{component_id=\"" + string(uidOfDeploy) + "\",component_name=\"" + name + "\"}[2m])/100000)*100 < bool " + strconv.Itoa(hpa.CpuMin) + ") >2"
	} else if hpa.CpuMin > 0 && hpa.MemMin > 0 && hpa.QpsMin == 0 {
		aggerateRulesHigh = "(sum(avg_over_time(container_memory_usage_bytes{component_id=\"" + string(uidOfDeploy) + "\",component_name=\"" + name + "\"}[2m]))/sum((avg_over_time(container_spec_memory_limit_bytes{component_id=\"" + string(uidOfDeploy) + "\",component_name=\"" + name + "\"}[2m])))*100 > bool " + strconv.Itoa(hpa.MemMax) + ") + (sum(rate(container_cpu_usage_seconds_total{component_id=\"" + string(uidOfDeploy) + "\",component_name=\"" + name + "\"}[2m])) / sum(avg_over_time(container_spec_cpu_quota{component_id=\"" + string(uidOfDeploy) + "\",component_name=\"" + name + "\"}[2m])/100000)*100 > bool " + strconv.Itoa(hpa.CpuMax) + ") >0"
		aggerateRulesLow = "(sum(avg_over_time(container_memory_usage_bytes{component_id=\"" + string(uidOfDeploy) + "\",component_name=\"" + name + "\"}[2m]))/sum((avg_over_time(container_spec_memory_limit_bytes{component_id=\"" + string(uidOfDeploy) + "\",component_name=\"" + name + "\"}[2m])))*100 < bool " + strconv.Itoa(hpa.MemMin) + ") + (sum(rate(container_cpu_usage_seconds_total{component_id=\"" + string(uidOfDeploy) + "\",component_name=\"" + name + "\"}[2m])) / sum(avg_over_time(container_spec_cpu_quota{component_id=\"" + string(uidOfDeploy) + "\",component_name=\"" + name + "\"}[2m])/100000)*100 < bool " + strconv.Itoa(hpa.CpuMin) + ") >1"
	} else if hpa.CpuMin > 0 && hpa.QpsMin > 0 && hpa.MemMin == 0 {
		aggerateRulesHigh = "((sum(rate(io_sid_traffics{component_id=\"" + string(uidOfDeploy) + "\",component_name=\"" + name + "\"}[2m])))/(count(rate(io_sid_traffics{component_id=\"" + string(uidOfDeploy) + "\",component_name=\"" + name + "\"}[2m])))" + "*sum(((count(io_sid_traffics{pod=~\"" + name + ".*\"})by(pod))) )/count(((count(io_sid_traffics{pod=~\"" + name + ".*\"})by(pod))))" + "> bool " + strconv.Itoa(hpa.QpsMax) + ") + (sum(rate(container_cpu_usage_seconds_total{component_id=\"" + string(uidOfDeploy) + "\",component_name=\"" + name + "\"}[2m])) / sum(avg_over_time(container_spec_cpu_quota{component_id=\"" + string(uidOfDeploy) + "\",component_name=\"" + name + "\"}[2m])/100000)*100 > bool " + strconv.Itoa(hpa.CpuMax) + ") >0"
		aggerateRulesLow = "((sum(rate(io_sid_traffics{component_id=\"" + string(uidOfDeploy) + "\",component_name=\"" + name + "\"}[2m])))/(count(rate(io_sid_traffics{component_id=\"" + string(uidOfDeploy) + "\",component_name=\"" + name + "\"}[2m]))" + "*sum(((count(io_sid_traffics{pod=~\"" + name + ".*\"})by(pod))) )/count(((count(io_sid_traffics{pod=~\"" + name + ".*\"})by(pod))))" + ")< bool " + strconv.Itoa(hpa.QpsMin) + ") + (sum(rate(container_cpu_usage_seconds_total{component_id=\"" + string(uidOfDeploy) + "\",component_name=\"" + name + "\"}[2m])) / sum(avg_over_time(container_spec_cpu_quota{component_id=\"" + string(uidOfDeploy) + "\",component_name=\"" + name + "\"}[2m])/100000)*100 < bool " + strconv.Itoa(hpa.CpuMin) + ") >1"
		klog.Infof("add subprom Min")
	} else if hpa.MemMin > 0 && hpa.QpsMin > 0 && hpa.CpuMin == 0 {
		aggerateRulesHigh = "((avg(avg_over_time(io_sid_traffics{component_id=\"" + string(uidOfDeploy) + "\",component_name=\"" + name + "\"}[2m]))) > bool " + strconv.Itoa(hpa.QpsMax) + ") + (sum(avg_over_time(container_memory_usage_bytes{component_id=\"" + string(uidOfDeploy) + "\",component_name=\"" + name + "\"}[2m]))/sum((avg_over_time(container_spec_memory_limit_bytes{component_id=\"" + string(uidOfDeploy) + "\",component_name=\"" + name + "\"}[2m])))*100 > bool " + strconv.Itoa(hpa.MemMax) + ")>0"
		aggerateRulesLow = "((avg(avg_over_time(io_sid_traffics{component_id=\"" + string(uidOfDeploy) + "\",component_name=\"" + name + "\"}[2m]))) < bool " + strconv.Itoa(hpa.QpsMin) + ") + (sum(avg_over_time(container_memory_usage_bytes{component_id=\"" + string(uidOfDeploy) + "\",component_name=\"" + name + "\"}[2m]))/sum((avg_over_time(container_spec_memory_limit_bytes{component_id=\"" + string(uidOfDeploy) + "\",component_name=\"" + name + "\"}[2m])))*100 < bool " + strconv.Itoa(hpa.MemMin) + ")>1"
		klog.Infof("add subprom Min")
	} else if hpa.CpuMin > 0 && hpa.MemMin == 0 && hpa.QpsMin == 0 {
		aggerateRulesHigh = "sum(rate(container_cpu_usage_seconds_total{component_id=\"" + string(uidOfDeploy) + "\",component_name=\"" + name + "\"}[2m])) / sum(avg_over_time(container_spec_cpu_quota{component_id=\"" + string(uidOfDeploy) + "\",component_name=\"" + name + "\"}[2m])/100000)*100 > bool " + strconv.Itoa(hpa.CpuMax) + " >0"
		aggerateRulesLow = "sum(rate(container_cpu_usage_seconds_total{component_id=\"" + string(uidOfDeploy) + "\",component_name=\"" + name + "\"}[2m])) / sum(avg_over_time(container_spec_cpu_quota{component_id=\"" + string(uidOfDeploy) + "\",component_name=\"" + name + "\"}[2m])/100000)*100 < bool " + strconv.Itoa(hpa.CpuMin) + " >0"
		klog.Infof("add subprom Min")
	} else if hpa.MemMin > 0 && hpa.CpuMin == 0 && hpa.QpsMin == 0 {
		aggerateRulesHigh = "sum(avg_over_time(container_memory_usage_bytes{component_id=\"" + string(uidOfDeploy) + "\",component_name=\"" + name + "\"}[2m]))/sum((avg_over_time(container_spec_memory_limit_bytes{component_id=\"" + string(uidOfDeploy) + "\",component_name=\"" + name + "\"}[2m])))*100 > bool " + strconv.Itoa(hpa.MemMax) + " >0"
		aggerateRulesLow = "sum(avg_over_time(container_memory_usage_bytes{component_id=\"" + string(uidOfDeploy) + "\",component_name=\"" + name + "\"}[2m]))/sum((avg_over_time(container_spec_memory_limit_bytes{component_id=\"" + string(uidOfDeploy) + "\",component_name=\"" + name + "\"}[2m])))*100 < bool " + strconv.Itoa(hpa.MemMin) + " >0"
	} else if hpa.QpsMin > 0 && hpa.MemMin == 0 && hpa.CpuMin == 0 {
		aggerateRulesHigh = "avg(avg_over_time(io_sid_traffics{component_id=\"" + string(uidOfDeploy) + "\",component_name=\"" + name + "\"}[2m])) > bool " + strconv.Itoa(hpa.QpsMax) + " >0"
		aggerateRulesLow = "avg(avg_over_time(io_sid_traffics{component_id=\"" + string(uidOfDeploy) + "\",component_name=\"" + name + "\"}[2m])) < bool " + strconv.Itoa(hpa.QpsMin) + " >0"
	}
	subReciverAddr := hpaWebUrl
	alertHeaderUp := namespace + "+" + name + "+scale_up"
	alertHeaderDown := namespace + "+" + name + "+scale_down"
	klog.Infof("aggerateRulesHigh: ", aggerateRulesHigh)
	klog.Infof("aggerateRulesLow: ", aggerateRulesLow)

	//2m可做成配置文件
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
								//"serverless.cluster.pml.com.cn/serverless": req.Name + req.Namespace,
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
								//"serverless.cluster.pml.com.cn/serverless": req.Name + req.Namespace,
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
		//req.Namespace = namespace
		klog.Infof("updates on the spec of promruleInstance %q, syncing done", subscribeInstance.Name)
	}
	return nil
}

func (r *ServerlessReconciler) qpsQuotaHpaInit(ctx context.Context, req ctrl.Request, name string, namespace string, pod corev1.Pod, qpsQuotaHpaUrl string, action string, qpsQuota int) error {
	klog.Infof("Start add qps quota Hpa Alert")
	srs := &hermesv1.SubscriberRule{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "hermes.pml.com/v1",
			Kind:       "SubscriberRule",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name + "-qps",
			Namespace: subPromNamespace,
			Labels: map[string]string{
				"serverless.cluster.pml.com.cn/serverless": req.Name + req.Namespace,
			},
		},
	}
	//add pod uuid
	uidOfPod := pod.UID
	nameOfPod := pod.Name
	var aggerateRulesHigh = "((sum(rate(io_sid_traffics{uid=\"" + string(uidOfPod) + "\",pod=\"" + nameOfPod + "\"}[2m])))/(count(rate(io_sid_traffics{uid=\"" + string(uidOfPod) + "\",pod=\"" + nameOfPod + "\"}[2m])))" + "*sum(((count(io_sid_traffics{pod=~\"" + nameOfPod + ".*\"})by(pod))) )/count(((count(io_sid_traffics{pod=~\"" + nameOfPod + ".*\"})by(pod))))" + ")> bool " + strconv.Itoa(qpsQuota-1) + " >0"
	var aggerateRulesLow = "((sum(rate(io_sid_traffics{uid=\"" + string(uidOfPod) + "\",pod=\"" + nameOfPod + "\"}[2m])))/(count(rate(io_sid_traffics{uid=\"" + string(uidOfPod) + "\",pod=\"" + nameOfPod + "\"}[2m])))" + "*sum(((count(io_sid_traffics{pod=~\"" + nameOfPod + ".*\"})by(pod))) )/count(((count(io_sid_traffics{pod=~\"" + nameOfPod + ".*\"})by(pod))))" + ")< bool " + strconv.Itoa(1) + " >0"

	subReciverAddr := qpsQuotaHpaUrl
	alertHeaderUp := req.Namespace + "+" + req.Name + "+" + nameOfPod + "+pod_qps_quota_up" + "+" + strconv.Itoa(qpsQuota)
	alertHeaderDown := req.Namespace + "+" + req.Name + "+" + nameOfPod + "+pod_qps_quota_down" + "+" + strconv.Itoa(qpsQuota)
	klog.Infof("aggerateRulesHigh: ", aggerateRulesHigh)
	klog.Infof("aggerateRulesLow: ", aggerateRulesLow)

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
								//"serverless.cluster.pml.com.cn/serverless": req.Name + req.Namespace,
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
								//"serverless.cluster.pml.com.cn/serverless": req.Name + req.Namespace,
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

	if action == "init" {
		klog.Infof("create obj is :%s ", srs)
		err := r.Create(ctx, srs)
		if err != nil {
			return err
		}
		klog.Infof("end add qps quota Hpa Alert of Init")

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
		//req.Namespace = namespace
		klog.Infof("updates on the spec of promruleInstance %q, syncing done", subscribeInstance.Name)
	}
	klog.Infof("end add qps quota Hpa Alert of Update")

	return nil
}
