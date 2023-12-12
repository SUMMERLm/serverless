package common

import (
	"context"
	"github.com/SUMMERLm/serverless/pkg/serverless_hpa/lib"
	hermesv1 "github.com/jinxin-fu/hermes/pkg/adaptor/apis/hermes/v1"
	prometheusv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/klog/v2"
	"reflect"
	"strconv"
	"time"
)

type HpaThreadhold struct {
	CpuMin int `json:"cpuMin"`
	CpuMax int `json:"cpuMax"`
	MemMin int `json:"memMin"`
	MemMax int `json:"memMax"`
	QpsMin int `json:"qpsMin"`
	QpsMax int `json:"qpsMax"`
}

const serverlessFinalizerName string = "serverless.hpa.finalizers.pml.com.cn"
const subPromNamespace string = "hypermonitor"

func (r *Hpa) createSubPromWithYaml(name string, namespace string, hpa HpaThreadhold, hpaWebUrl string) error {
	const action = "create"
	klog.Infof("satrt create SubProm With Yaml %s", name)
	//first time wait deploy msg sync
	time.Sleep(time.Duration(10) * time.Second)
	err := r.subPromWithYaml(name, namespace, hpa, hpaWebUrl, action)
	if err != nil {
		klog.Error(err)
		return err
	}
	klog.Infof("end create SubProm With Yaml %s", name)

	return nil
}

func (r *Hpa) updateSubPromWithYaml(name string, namespace string, hpa HpaThreadhold, hpaWebUrl string) error {
	const action = "update"
	klog.Infof("satrt update SubProm With Yaml %s", name)
	err := r.subPromWithYaml(name, namespace, hpa, hpaWebUrl, action)
	if err != nil {
		klog.Error(err)
		return err
	}
	klog.Infof("end update SubProm With Yaml %s", name)
	return nil
}

func (r *Hpa) subPromWithYaml(name string, namespace string, hpa HpaThreadhold, hpaWebUrl string, action string) error {
	srs := &hermesv1.SubscriberRule{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "hermes.pml.com/v1",
			Kind:       "SubscriberRule",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels: map[string]string{
				"serverless.cluster.pml.com.cn/serverless": r.NameHpa + r.NamespaceHpa,
			},
		},
	}
	//add deployement uuid
	deploymentsClient := lib.K8sClient.AppsV1().Deployments(r.NamespaceHpa)
	klog.Infof("Scale up serverless begin... : %s, namespace: %s\n", r.NameHpa, r.NamespaceHpa)
	deployment, getErr := deploymentsClient.Get(context.TODO(), r.NameHpa, metav1.GetOptions{})
	if getErr != nil {
		klog.Errorf("Failed to get latest version of Deployment: %v", getErr)
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
		//获取srs，创建

		//podList := &apiv1.PodList{}

		//err := r.Create(ctx, srs)

		return nil
	} else if action == "update" {
		subscribeInstance := &hermesv1.SubscriberRule{}
		//获取，更新
		subGvr := schema.GroupVersionResource{Group: "hermes.pml.com", Version: "v1", Resource: "subscriberrules"}
		unStructObj, err := lib.DynamicClient.Resource(subGvr).Namespace(r.NamespaceHpa).Get(context.TODO(), r.NameHpa, metav1.GetOptions{})
		if err != nil {
			klog.Error(err)
		}
		sub := &hermesv1.SubscriberRule{}
		if err = runtime.DefaultUnstructuredConverter.FromUnstructured(unStructObj.UnstructuredContent(), sub); err != nil {
			klog.Error(err)
		}

		if reflect.DeepEqual(subscribeInstance.Spec, srs.Spec) {
			klog.Infof("no updates on the spec of promruleInstance %q, skipping syncing", subscribeInstance.Name)
			return nil
		}
		klog.Infof("updates on the spec of promruleInstance %q, syncing start", subscribeInstance.Name)
		subscribeInstance.Spec = srs.Spec
		//err = r.Update(ctx, subscribeInstance)
		//if err != nil {
		//	return err
		//}
		//req.Namespace = namespace
		klog.Infof("updates on the spec of promruleInstance %q, syncing done", subscribeInstance.Name)
	}
	return nil
}

func (r *Hpa) qpsQuotaHpaInit(pod corev1.Pod, qpsQuotaHpaUrl string, action string, qpsQuota int64) error {
	klog.Infof("Start add qps quota Hpa Alert")
	subGvr := schema.GroupVersionResource{Group: "hermes.pml.com", Version: "v1", Resource: "subscriberrules"}
	var aggerateRulesHigh = "avg(avg_over_time(io_sid_traffics{pod=\"" + pod.Name + "\"}[2m]))> bool " + strconv.Itoa(int(qpsQuota)-1) + " >0"
	var aggerateRulesLow = "avg(avg_over_time(io_sid_traffics{pod=\"" + pod.Name + "\"}[2m]))< bool " + strconv.Itoa(1) + " >0"

	subscribeRule := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "hermes.pml.com/v1",
			"kind":       "SubscriberRule",
			"metadata": map[string]interface{}{
				"name":      pod.Name + "-qps",
				"namespace": subPromNamespace,
				"labels": map[string]interface{}{
					"serverless.cluster.pml.com.cn/serverless": r.NameHpa + r.NamespaceHpa,
				},
			},
			"spec": map[string]interface{}{
				"prometheusRule": map[string]interface{}{
					"groups": []map[string]interface{}{
						{ //"name": "serverless.rules.sample-s2-6-f88d697f8-kfq24.default",
							"name": "serverless.rules." + pod.Name + "." + r.NamespaceHpa,
							"rules": []map[string]interface{}{
								{
									//"alert":       "default+sample-s2-6-f88d697f8-kfq24+pod_qps_quota_up",
									"alert": r.NamespaceHpa + "+" + r.NameHpa + "+" + pod.Name + "+pod_qps_quota_up",

									"annotations": map[string]interface{}{
										"aggerateRules":   aggerateRulesHigh,
										"receiverAddress": qpsQuotaHpaUrl,
										"returnValueFlag": "false",
									},
									"expr": aggerateRulesHigh,
									"for":  "1s",
									"labels": map[string]interface{}{
										"alertlabel": "serverless",
										"severity":   "critical",
									},
								},
								{
									//"alert":       "default+sample-s2-6-f88d697f8-kfq24+pod_qps_quota_up",
									"alert": r.NamespaceHpa + "+" + r.NameHpa + "+" + pod.Name + "+pod_qps_quota_down",

									"annotations": map[string]interface{}{
										//"aggerateRules":   "((sum(rate(io_sid_traffics{uid=\"a96e1818-43bc-45b5-90ae-f197b1e3bb9f\",pod=\"sample-s2-6-f88d697f8-kfq24\"}[2m])))/(count(rate(io_sid_traffics{uid=\"a96e1818-43bc-45b5-90ae-f197b1e3bb9f\",pod=\"sample-s2-6-f88d697f8-kfq24\"}[2m])))*sum(((count(io_sid_traffics{pod=~\"sample-s2-6-f88d697f8-kfq24.*\"})by(pod))) )/count(((count(io_sid_traffics{pod=~\"sample-s2-6-f88d697f8-kfq24.*\"})by(pod)))))< bool 1 >0",
										"aggerateRules": aggerateRulesLow,
										//TODO 配置文件
										"receiverAddress": qpsQuotaHpaUrl,
										"returnValueFlag": "false",
									},
									//"expr": "((sum(rate(io_sid_traffics{uid=\"a96e1818-43bc-45b5-90ae-f197b1e3bb9f\",pod=\"sample-s2-6-f88d697f8-kfq24\"}[2m])))/(count(rate(io_sid_traffics{uid=\"a96e1818-43bc-45b5-90ae-f197b1e3bb9f\",pod=\"sample-s2-6-f88d697f8-kfq24\"}[2m])))*sum(((count(io_sid_traffics{pod=~\"sample-s2-6-f88d697f8-kfq24.*\"})by(pod))) )/count(((count(io_sid_traffics{pod=~\"sample-s2-6-f88d697f8-kfq24.*\"})by(pod)))))< bool 1 >0",
									"expr": aggerateRulesLow,
									"for":  "1s",
									"labels": map[string]interface{}{
										"alertlabel": "serverless",
										"severity":   "critical",
									},
								},
							},
						},
					},
				},
				"subscribeType":     "SubsCondition",
				"subscriberAddress": qpsQuotaHpaUrl,
			},
		},
	}

	if action == "init" {
		klog.Infof("create subscribeRule ")
		_, subscribeRuleErr := lib.DynamicClient.Resource(subGvr).Namespace(subPromNamespace).Create(context.TODO(), subscribeRule, metav1.CreateOptions{})
		if subscribeRuleErr != nil {
			return subscribeRuleErr
		}
		return nil
	} else if action == "delete" {
		subscribeDeleteRuleErr := lib.DynamicClient.Resource(subGvr).Namespace(subPromNamespace).Delete(context.TODO(), pod.Name, metav1.DeleteOptions{})
		if subscribeDeleteRuleErr != nil {
			return subscribeDeleteRuleErr
		}
		return nil
	}
	return nil
}

func (r *Hpa) qpsQuotaHpaDel(podName interface{}, action string) error {
	klog.Infof("Start add qps quota Hpa Alert")
	subGvr := schema.GroupVersionResource{Group: "hermes.pml.com", Version: "v1", Resource: "subscriberrules"}
	if action == "delete" {
		subscribeDeleteRuleErr := lib.DynamicClient.Resource(subGvr).Namespace(subPromNamespace).Delete(context.TODO(), podName.(string)+"-qps", metav1.DeleteOptions{})
		if subscribeDeleteRuleErr != nil {
			return subscribeDeleteRuleErr
		}
		return nil
	}
	return nil
}
func (q *Quota) qpsQuotaHpaUpdate(pod corev1.Pod, qpsQuotaHpaUrl string, action string, qpsQuota int64, quotaStep int64) error {
	klog.Infof("Start update qps quota Hpa Alert")
	subGvr := schema.GroupVersionResource{Group: "hermes.pml.com", Version: "v1", Resource: "subscriberrules"}
	var aggerateRulesHigh string
	var aggerateRulesLow string
	if int(qpsQuota) > 0 {
		aggerateRulesHigh = "avg(avg_over_time(io_sid_traffics{pod=\"" + pod.Name + "\"}[2m]))> bool " + strconv.Itoa(int(qpsQuota)-1) + " >0"
		aggerateRulesLow = "avg(avg_over_time(io_sid_traffics{pod=\"" + pod.Name + "\"}[2m]))< bool " + strconv.Itoa(int(qpsQuota-quotaStep)+1) + " >0"
	} else {
		aggerateRulesHigh = "avg(avg_over_time(io_sid_traffics{pod=\"" + pod.Name + "\"}[2m]))> bool " + strconv.Itoa(int(qpsQuota)) + " >0"
		aggerateRulesLow = "avg(avg_over_time(io_sid_traffics{pod=\"" + pod.Name + "\"}[2m]))< bool " + strconv.Itoa(int(qpsQuota-quotaStep)+1) + " >0"
	}
	//var aggerateRulesHigh = "((sum(rate(io_sid_traffics{uid=\"" + string(pod.UID) + "\",pod=\"" + pod.Name + "\"}[2m])))/(count(rate(io_sid_traffics{uid=\"" + string(pod.UID) + "\",pod=\"" + pod.Name + "\"}[2m])))" + "*sum(((count(io_sid_traffics{pod=~\"" + pod.Name + ".*\"})by(pod))) )/count(((count(io_sid_traffics{pod=~\"" + pod.Name + ".*\"})by(pod))))" + ")> bool " + strconv.Itoa(int(qpsQuota)-1) + " >0"
	//var aggerateRulesLow = "((sum(rate(io_sid_traffics{uid=\"" + string(pod.UID) + "\",pod=\"" + pod.Name + "\"}[2m])))/(count(rate(io_sid_traffics{uid=\"" + string(pod.UID) + "\",pod=\"" + pod.Name + "\"}[2m])))" + "*sum(((count(io_sid_traffics{pod=~\"" + pod.Name + ".*\"})by(pod))) )/count(((count(io_sid_traffics{pod=~\"" + pod.Name + ".*\"})by(pod))))" + ")< bool " + strconv.Itoa(int(qpsQuota-quotaStep)) + " >0"
	klog.Infof(aggerateRulesHigh)
	klog.Infof(aggerateRulesLow)
	subscribeRule := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "hermes.pml.com/v1",
			"kind":       "SubscriberRule",
			"metadata": map[string]interface{}{
				"name":      pod.Name + "-qps",
				"namespace": subPromNamespace,
				"labels": map[string]interface{}{
					"serverless.cluster.pml.com.cn/serverless": q.NameQuota + q.NamespaceQuota,
				},
			},
			"spec": map[string]interface{}{
				"prometheusRule": map[string]interface{}{
					"groups": []map[string]interface{}{
						{ //"name": "serverless.rules.sample-s2-6-f88d697f8-kfq24.default",
							"name": "serverless.rules." + pod.Name + "." + q.NamespaceQuota,
							"rules": []map[string]interface{}{
								{
									"alert": q.NamespaceQuota + "+" + q.NameQuota + "+" + pod.Name + "+pod_qps_quota_up" + "+" + strconv.FormatInt(qpsQuota, 10),
									"annotations": map[string]interface{}{
										"aggerateRules":   aggerateRulesHigh,
										"receiverAddress": qpsQuotaHpaUrl,
										"returnValueFlag": "false",
									},
									"expr": aggerateRulesHigh,
									"for":  "1s",
									"labels": map[string]interface{}{
										"alertlabel": "serverless",
										"severity":   "critical",
									},
								},
								{
									//"alert":       "default+sample-s2-6-f88d697f8-kfq24+pod_qps_quota_up",
									"alert": q.NamespaceQuota + "+" + q.NameQuota + "+" + pod.Name + "+pod_qps_quota_down" + "+" + strconv.FormatInt(qpsQuota, 10),
									"annotations": map[string]interface{}{
										"aggerateRules":   aggerateRulesLow,
										"receiverAddress": qpsQuotaHpaUrl,
										"returnValueFlag": "false",
									},
									"expr": aggerateRulesLow,
									"for":  "1s",
									"labels": map[string]interface{}{
										"alertlabel": "serverless",
										"severity":   "critical",
									},
								},
							},
						},
					},
				},
				"subscribeType": "SubsCondition",
				//"subscriberAddress": "http://172.24.33.32:32000/serverles_qps_quota_hpa",
				"subscriberAddress": qpsQuotaHpaUrl,
			},
		},
	}

	if action == "update" {
		//更新替换删除+创建
		klog.Infof("update subscribeRule ")
		subsribeOld, subscribeGetRuleErr := lib.DynamicClient.Resource(subGvr).Namespace(subPromNamespace).Get(context.TODO(), pod.Name+"-qps", metav1.GetOptions{})
		if subscribeGetRuleErr != nil {
			return subscribeGetRuleErr
		}
		subscribeRule.SetResourceVersion(subsribeOld.GetResourceVersion())
		_, subscribeUpdateRuleErr := lib.DynamicClient.Resource(subGvr).Namespace(subPromNamespace).Update(context.TODO(), subscribeRule, metav1.UpdateOptions{})
		if subscribeUpdateRuleErr != nil {
			return subscribeUpdateRuleErr
		}

		return nil
	}
	return nil
}
