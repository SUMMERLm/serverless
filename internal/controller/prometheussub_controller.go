package controller

import (
	"context"
	"reflect"
	"strconv"
	"time"

	"github.com/SUMMERLm/serverless/pkg/comm"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/klog/v2"

	hermesV1 "github.com/jinxin-fu/hermes/pkg/adaptor/apis/hermes/v1"
	prometheusV1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	appsV1 "k8s.io/api/apps/v1"
	metaV1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
)

func (r *ServerlessReconciler) createSubPromWithYaml(ctx context.Context, req ctrl.Request, name string, namespace string, hpa HpaThreadhold, hpaWebURL string) error {
	const action = "create"
	klog.Infof("start create SubProm With Yaml %s", name)
	var sleepDuration int = 3
	// first time wait deploy msg sync
	time.Sleep(time.Duration(sleepDuration) * time.Second)
	err := r.subPromWithYaml(ctx, req, name, namespace, hpa, hpaWebURL, action)
	if err != nil {
		klog.Error(err)
		return err
	}
	klog.Infof("end create SubProm With Yaml %s", name)

	return nil
}

func (r *ServerlessReconciler) updateSubPromWithYaml(ctx context.Context, req ctrl.Request, name string, namespace string, hpa HpaThreadhold, hpaWebURL string) error {
	const action = "update"
	klog.Infof("satrt update SubProm With Yaml %s", name)
	err := r.subPromWithYaml(ctx, req, name, namespace, hpa, hpaWebURL, action)
	if err != nil {
		klog.Error(err)
		return err
	}
	klog.Infof("end update SubProm With Yaml %s", name)
	return nil
}

func (r *ServerlessReconciler) subPromWithYaml(ctx context.Context, req ctrl.Request, name string, namespace string, hpa HpaThreadhold, hpaWebURL string, action string) error {
	srs := &hermesV1.SubscriberRule{
		TypeMeta: metaV1.TypeMeta{
			APIVersion: "hermes.pml.com/v1",
			Kind:       "SubscriberRule",
		},
		ObjectMeta: metaV1.ObjectMeta{
			Name:      name,
			Namespace: subPromNamespace,
			Labels: map[string]string{
				"serverless.cluster.pml.com.cn/serverless": req.Name + req.Namespace,
			},
		},
	}
	// add deployement uuid
	deployment := &appsV1.Deployment{}
	err := r.Get(ctx, req.NamespacedName, deployment)
	// hpa threadhold
	if err != nil {
		klog.Error(err)
		return err
	}
	uidOfDeploy := deployment.UID
	klog.Infof("uidOfDeploy:=deployment.UID", uidOfDeploy)

	//Todo: use dynamic admission controller to check the threadhold
	if hpa.CPUMax > 0 || hpa.MemMax > 0 || hpa.QPSMax > 0 {
		klog.Infof("add subprom Max")
	} else {
		klog.Error("limit of serverless is false")
	}
	var aggerateRulesHigh string
	var aggerateRulesLow string
	switch {
	case hpa.CPUMin > 0 && hpa.MemMin > 0 && hpa.QPSMin > 0:
		aggerateRulesHigh = "((sum(avg_over_time(io_sid_traffics{component_id=\"" + string(uidOfDeploy) + "\",component_name=\"" + name + "\"}[60s]))) > bool " + strconv.Itoa(hpa.QPSMax) + ") + (sum(avg_over_time(container_memory_usage_bytes{container!=\"\",component_id=\"" + string(uidOfDeploy) + "\",component_name=\"" + name + "\"}[60s]))/sum((avg_over_time(container_spec_memory_limit_bytes{container!=\"\",component_id=\"" + string(uidOfDeploy) + "\",component_name=\"" + name + "\"}[60s])))*100 > bool " + strconv.Itoa(hpa.MemMax) + ") + (sum(rate(container_cpu_usage_seconds_total{container!=\"\",component_id=\"" + string(uidOfDeploy) + "\",component_name=\"" + name + "\"}[60s])) / sum(avg_over_time(container_spec_cpu_quota{container!=\"\",component_id=\"" + string(uidOfDeploy) + "\",component_name=\"" + name + "\"}[60s])/100000)*100 > bool " + strconv.Itoa(hpa.CPUMax) + ") >0"
		aggerateRulesLow = "((sum(avg_over_time(io_sid_traffics{component_id=\"" + string(uidOfDeploy) + "\",component_name=\"" + name + "\"}[60s])))  < bool " + strconv.Itoa(hpa.QPSMin) + ") + (sum(avg_over_time(container_memory_usage_bytes{container!=\"\",component_id=\"" + string(uidOfDeploy) + "\",component_name=\"" + name + "\"}[60s]))/sum((avg_over_time(container_spec_memory_limit_bytes{container!=\"\",component_id=\"" + string(uidOfDeploy) + "\",component_name=\"" + name + "\"}[60s])))*100 < bool " + strconv.Itoa(hpa.MemMin) + ") + (sum(rate(container_cpu_usage_seconds_total{container!=\"\",component_id=\"" + string(uidOfDeploy) + "\",component_name=\"" + name + "\"}[60s])) / sum(avg_over_time(container_spec_cpu_quota{container!=\"\",component_id=\"" + string(uidOfDeploy) + "\",component_name=\"" + name + "\"}[60s])/100000)*100 < bool " + strconv.Itoa(hpa.CPUMin) + ") >2"
	case hpa.CPUMin > 0 && hpa.MemMin > 0 && hpa.QPSMin == 0:
		aggerateRulesHigh = "(sum(avg_over_time(container_memory_usage_bytes{container!=\"\",component_id=\"" + string(uidOfDeploy) + "\",component_name=\"" + name + "\"}[60s]))/sum((avg_over_time(container_spec_memory_limit_bytes{container!=\"\",component_id=\"" + string(uidOfDeploy) + "\",component_name=\"" + name + "\"}[60s])))*100 > bool " + strconv.Itoa(hpa.MemMax) + ") + (sum(rate(container_cpu_usage_seconds_total{container!=\"\",component_id=\"" + string(uidOfDeploy) + "\",component_name=\"" + name + "\"}[60s])) / sum(avg_over_time(container_spec_cpu_quota{container!=\"\",component_id=\"" + string(uidOfDeploy) + "\",component_name=\"" + name + "\"}[60s])/100000)*100 > bool " + strconv.Itoa(hpa.CPUMax) + ") >0"
		aggerateRulesLow = "(sum(avg_over_time(container_memory_usage_bytes{container!=\"\",component_id=\"" + string(uidOfDeploy) + "\",component_name=\"" + name + "\"}[60s]))/sum((avg_over_time(container_spec_memory_limit_bytes{container!=\"\",component_id=\"" + string(uidOfDeploy) + "\",component_name=\"" + name + "\"}[60s])))*100 < bool " + strconv.Itoa(hpa.MemMin) + ") + (sum(rate(container_cpu_usage_seconds_total{container!=\"\",component_id=\"" + string(uidOfDeploy) + "\",component_name=\"" + name + "\"}[60s])) / sum(avg_over_time(container_spec_cpu_quota{container!=\"\",component_id=\"" + string(uidOfDeploy) + "\",component_name=\"" + name + "\"}[60s])/100000)*100 < bool " + strconv.Itoa(hpa.CPUMin) + ") >1"
	case hpa.CPUMin > 0 && hpa.QPSMin > 0 && hpa.MemMin == 0:
		aggerateRulesHigh = "((sum(rate(io_sid_traffics{component_id=\"" + string(uidOfDeploy) + "\",component_name=\"" + name + "\"}[60s])))/(count(rate(io_sid_traffics{component_id=\"" + string(uidOfDeploy) + "\",component_name=\"" + name + "\"}[60s])))" + "*sum(((count(io_sid_traffics{pod=~\"" + name + ".*\"})by(pod))) )/count(((count(io_sid_traffics{pod=~\"" + name + ".*\"})by(pod))))" + "> bool " + strconv.Itoa(hpa.QPSMax) + ") + (sum(rate(container_cpu_usage_seconds_total{container!=\"\",component_id=\"" + string(uidOfDeploy) + "\",component_name=\"" + name + "\"}[60s])) / sum(avg_over_time(container_spec_cpu_quota{container!=\"\",component_id=\"" + string(uidOfDeploy) + "\",component_name=\"" + name + "\"}[60s])/100000)*100 > bool " + strconv.Itoa(hpa.CPUMax) + ") >0"
		aggerateRulesLow = "((sum(rate(io_sid_traffics{component_id=\"" + string(uidOfDeploy) + "\",component_name=\"" + name + "\"}[60s])))/(count(rate(io_sid_traffics{component_id=\"" + string(uidOfDeploy) + "\",component_name=\"" + name + "\"}[60s]))" + "*sum(((count(io_sid_traffics{pod=~\"" + name + ".*\"})by(pod))) )/count(((count(io_sid_traffics{pod=~\"" + name + ".*\"})by(pod))))" + ")< bool " + strconv.Itoa(hpa.QPSMin) + ") + (sum(rate(container_cpu_usage_seconds_total{container!=\"\",component_id=\"" + string(uidOfDeploy) + "\",component_name=\"" + name + "\"}[60s])) / sum(avg_over_time(container_spec_cpu_quota{container!=\"\",component_id=\"" + string(uidOfDeploy) + "\",component_name=\"" + name + "\"}[60s])/100000)*100 < bool " + strconv.Itoa(hpa.CPUMin) + ") >1"
	case hpa.MemMin > 0 && hpa.QPSMin > 0 && hpa.CPUMin == 0:
		aggerateRulesHigh = "((sum(avg_over_time(io_sid_traffics{component_id=\"" + string(uidOfDeploy) + "\",component_name=\"" + name + "\"}[60s]))) > bool " + strconv.Itoa(hpa.QPSMax) + ") + (sum(avg_over_time(container_memory_usage_bytes{container!=\"\",component_id=\"" + string(uidOfDeploy) + "\",component_name=\"" + name + "\"}[60s]))/sum((avg_over_time(container_spec_memory_limit_bytes{container!=\"\",component_id=\"" + string(uidOfDeploy) + "\",component_name=\"" + name + "\"}[60s])))*100 > bool " + strconv.Itoa(hpa.MemMax) + ")>0"
		aggerateRulesLow = "((sum(avg_over_time(io_sid_traffics{component_id=\"" + string(uidOfDeploy) + "\",component_name=\"" + name + "\"}[60s]))) < bool " + strconv.Itoa(hpa.QPSMin) + ") + (sum(avg_over_time(container_memory_usage_bytes{container!=\"\",component_id=\"" + string(uidOfDeploy) + "\",component_name=\"" + name + "\"}[60s]))/sum((avg_over_time(container_spec_memory_limit_bytes{container!=\"\",component_id=\"" + string(uidOfDeploy) + "\",component_name=\"" + name + "\"}[60s])))*100 < bool " + strconv.Itoa(hpa.MemMin) + ")>1"
	case hpa.CPUMin > 0 && hpa.MemMin == 0 && hpa.QPSMin == 0:
		aggerateRulesHigh = "sum(rate(container_cpu_usage_seconds_total{container!=\"\",component_id=\"" + string(uidOfDeploy) + "\",component_name=\"" + name + "\"}[60s])) / sum(avg_over_time(container_spec_cpu_quota{container!=\"\",component_id=\"" + string(uidOfDeploy) + "\",component_name=\"" + name + "\"}[60s])/100000)*100 > bool " + strconv.Itoa(hpa.CPUMax) + " >0"
		aggerateRulesLow = "sum(rate(container_cpu_usage_seconds_total{container!=\"\",component_id=\"" + string(uidOfDeploy) + "\",component_name=\"" + name + "\"}[60s])) / sum(avg_over_time(container_spec_cpu_quota{container!=\"\",component_id=\"" + string(uidOfDeploy) + "\",component_name=\"" + name + "\"}[60s])/100000)*100 < bool " + strconv.Itoa(hpa.CPUMin) + " >0"
	case hpa.MemMin > 0 && hpa.CPUMin == 0 && hpa.QPSMin == 0:
		aggerateRulesHigh = "sum(avg_over_time(container_memory_usage_bytes{container!=\"\",component_id=\"" + string(uidOfDeploy) + "\",component_name=\"" + name + "\"}[60s]))/sum((avg_over_time(container_spec_memory_limit_bytes{container!=\"\",component_id=\"" + string(uidOfDeploy) + "\",component_name=\"" + name + "\"}[60s])))*100 > bool " + strconv.Itoa(hpa.MemMax) + " >0"
		aggerateRulesLow = "sum(avg_over_time(container_memory_usage_bytes{container!=\"\",component_id=\"" + string(uidOfDeploy) + "\",component_name=\"" + name + "\"}[60s]))/sum((avg_over_time(container_spec_memory_limit_bytes{container!=\"\",component_id=\"" + string(uidOfDeploy) + "\",component_name=\"" + name + "\"}[60s])))*100 < bool " + strconv.Itoa(hpa.MemMin) + " >0"
	case hpa.QPSMin > 0 && hpa.MemMin == 0 && hpa.CPUMin == 0:
		aggerateRulesHigh = "sum(avg_over_time(io_sid_traffics{component_id=\"" + string(uidOfDeploy) + "\",component_name=\"" + name + "\"}[60s])) > bool " + strconv.Itoa(hpa.QPSMax) + " >0"
		aggerateRulesLow = "sum(avg_over_time(io_sid_traffics{component_id=\"" + string(uidOfDeploy) + "\",component_name=\"" + name + "\"}[60s])) < bool " + strconv.Itoa(hpa.QPSMin) + " >0"
	}
	subReciverAddr := hpaWebURL
	alertHeaderUp := namespace + "+" + name + "+scale_up"
	alertHeaderDown := namespace + "+" + name + "+scale_down"
	klog.Infof("aggerateRulesHigh: ", aggerateRulesHigh)
	klog.Infof("aggerateRulesLow: ", aggerateRulesLow)

	// 60s可做成配置文件
	srRuleSpec := hermesV1.SubscriberRuleSpec{
		SubscribeType:     "SubsCondition",
		SubscriberAddress: subReciverAddr,
		PrometheusRule: prometheusV1.PrometheusRuleSpec{
			Groups: []prometheusV1.RuleGroup{
				{
					Name: "serverless.rules." + name + "." + namespace,
					Rules: []prometheusV1.Rule{
						{
							Alert: alertHeaderUp,
							Expr:  intstr.FromString(aggerateRulesHigh),
							Labels: map[string]string{
								"alertlabel": "serverless",
								"severity":   "critical",
								// "serverless.cluster.pml.com.cn/serverless": req.Name + req.Namespace,
							},
							Annotations: map[string]string{
								"aggerateRules":   aggerateRulesHigh,
								"receiverAddress": subReciverAddr,
								"returnValueFlag": comm.False,
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
								"returnValueFlag": comm.False,
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
		subscribeInstance := &hermesV1.SubscriberRule{}
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
