package common

import (
	"context"
	"strconv"
	"strings"

	"github.com/SUMMERLm/serverless/pkg/comm"
	"github.com/SUMMERLm/serverless/pkg/serverless_hpa/lib"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/klog/v2"

	hermesV1 "github.com/jinxin-fu/hermes/pkg/adaptor/apis/hermes/v1"
	coreV1 "k8s.io/api/core/v1"
	metaV1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type HpaThreadhold struct {
	CpuMin int `json:"cpuMin"`
	CpuMax int `json:"cpuMax"`
	MemMin int `json:"memMin"`
	MemMax int `json:"memMax"`
	QpsMin int `json:"qpsMin"`
	QpsMax int `json:"qpsMax"`
}

const subPromNamespace string = "hypermonitor"

func (r *Hpa) qpsQuotaHpaInit(pod coreV1.Pod, qpsQuotaHpaUrl string, action string, qpsQuota int64) error {
	klog.Infof("Start add qps quota Hpa Alert")
	subGvr := schema.GroupVersionResource{Group: "hermes.pml.com", Version: "v1", Resource: "subscriberrules"}
	var aggerateRulesHigh = "sum(avg_over_time(io_sid_traffics{component_id=\"\",pod=\"" + pod.Name + "\"}[5s]))> bool " + strconv.Itoa(int(qpsQuota)) + " >0"
	var aggerateRulesLow = "sum(avg_over_time(io_sid_traffics{component_id=\"\",pod=\"" + pod.Name + "\"}[5s]))< bool " + strconv.Itoa(1) + " >0"

	subscribeRule := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "hermes.pml.com/v1",
			"kind":       "SubscriberRule",
			"metadata": map[string]interface{}{
				"name":      pod.Name + comm.SubscribeQPS,
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
									"alert": r.NamespaceHpa + "+" + r.NameHpa + "+" + pod.Name + "+pod_qps_quota_up",

									"annotations": map[string]interface{}{
										"aggerateRules":   aggerateRulesHigh,
										"receiverAddress": qpsQuotaHpaUrl,
										"returnValueFlag": comm.False,
									},
									"expr": aggerateRulesHigh,
									"for":  "1s",
									"labels": map[string]interface{}{
										"alertlabel": "serverless",
										"severity":   "critical",
									},
								},
								{
									"alert": r.NamespaceHpa + "+" + r.NameHpa + "+" + pod.Name + "+pod_qps_quota_down",

									"annotations": map[string]interface{}{
										"aggerateRules": aggerateRulesLow,
										//TODO 配置文件
										"receiverAddress": qpsQuotaHpaUrl,
										"returnValueFlag": comm.False,
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
				"subscribeType":     "SubsCondition",
				"subscriberAddress": qpsQuotaHpaUrl,
			},
		},
	}

	if action == "init" {
		klog.Infof("create subscribeRule ")
		_, subscribeRuleErr := lib.DynamicClient.Resource(subGvr).Namespace(subPromNamespace).Create(context.TODO(), subscribeRule, metaV1.CreateOptions{})
		if subscribeRuleErr != nil {
			return subscribeRuleErr
		}
		return nil
	} else if action == "delete" {
		subscribeDeleteRuleErr := lib.DynamicClient.Resource(subGvr).Namespace(subPromNamespace).Delete(context.TODO(), pod.Name, metaV1.DeleteOptions{})
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
		subscribeDeleteRuleErr := lib.DynamicClient.Resource(subGvr).Namespace(subPromNamespace).Delete(context.TODO(), podName.(string)+comm.SubscribeQPS, metaV1.DeleteOptions{})
		if subscribeDeleteRuleErr != nil {
			return subscribeDeleteRuleErr
		}
		return nil
	}
	return nil
}
func (quotaController *Quota) qpsQuotaHpaUpdate(pod coreV1.Pod, qpsQuotaHpaUrl string, action string, qpsQuota int64, quotaStep int64) error {
	klog.Infof("Start update qps quota Hpa Alert")
	subGvr := schema.GroupVersionResource{Group: "hermes.pml.com", Version: "v1", Resource: "subscriberrules"}
	var aggerateRulesHigh string
	var aggerateRulesLow string
	lowString := strconv.FormatInt(qpsQuota-quotaStep, 10)
	lowFloat, _ := strconv.ParseFloat(lowString, 64)
	var a float64 = 0.000000000000000001
	low64 := lowFloat + a
	aggerateRulesHigh = "sum(avg_over_time(io_sid_traffics{component_id=\"\",pod=\"" + pod.Name + "\"}[5s]))> bool " + strconv.Itoa(int(qpsQuota)) + " >0"
	aggerateRulesLow = "sum(avg_over_time(io_sid_traffics{component_id=\"\",pod=\"" + pod.Name + "\"}[5s]))< bool " + strconv.FormatFloat(low64, 'E', -1, 64) + " >0"
	klog.Infof(aggerateRulesHigh)
	klog.Infof(aggerateRulesLow)
	subscribeRule := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "hermes.pml.com/v1",
			"kind":       "SubscriberRule",
			"metadata": map[string]interface{}{
				"name":      pod.Name + comm.SubscribeQPS,
				"namespace": subPromNamespace,
				"labels": map[string]interface{}{
					"serverless.cluster.pml.com.cn/serverless": quotaController.NameQuota + quotaController.NamespaceQuota,
				},
			},
			"spec": map[string]interface{}{
				"prometheusRule": map[string]interface{}{
					"groups": []map[string]interface{}{
						{ //"name": "serverless.rules.sample-s2-6-f88d697f8-kfq24.default",
							"name": "serverless.rules." + pod.Name + "." + quotaController.NamespaceQuota,
							"rules": []map[string]interface{}{
								{
									"alert": quotaController.NamespaceQuota + "+" + quotaController.NameQuota + "+" + pod.Name + "+pod_qps_quota_up" + "+" + strconv.FormatInt(qpsQuota, 10),
									"annotations": map[string]interface{}{
										"aggerateRules":   aggerateRulesHigh,
										"receiverAddress": qpsQuotaHpaUrl,
										"returnValueFlag": comm.False,
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
									"alert": quotaController.NamespaceQuota + "+" + quotaController.NameQuota + "+" + pod.Name + "+pod_qps_quota_down" + "+" + strconv.FormatInt(qpsQuota, 10),
									"annotations": map[string]interface{}{
										"aggerateRules":   aggerateRulesLow,
										"receiverAddress": qpsQuotaHpaUrl,
										"returnValueFlag": comm.False,
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
				"subscribeType":     "SubsCondition",
				"subscriberAddress": qpsQuotaHpaUrl,
			},
		},
	}

	if action == "update" {
		//更新替换删除+创建
		klog.Infof("update subscribeRule ")
		subsribeOld, subscribeGetRuleErr := lib.DynamicClient.Resource(subGvr).Namespace(subPromNamespace).Get(context.TODO(), pod.Name+comm.SubscribeQPS, metaV1.GetOptions{})
		if subscribeGetRuleErr != nil {
			return subscribeGetRuleErr
		}
		subscribeRule.SetResourceVersion(subsribeOld.GetResourceVersion())
		_, subscribeUpdateRuleErr := lib.DynamicClient.Resource(subGvr).Namespace(subPromNamespace).Update(context.TODO(), subscribeRule, metaV1.UpdateOptions{})
		if subscribeUpdateRuleErr != nil {
			return subscribeUpdateRuleErr
		}

		return nil
	}
	return nil
}

func (quotaController *Quota) qpsQuotaVerify() (bool, error) {
	klog.Infof("Start check qps quota manage")
	subGvr := schema.GroupVersionResource{Group: "hermes.pml.com", Version: "v1", Resource: "subscriberrules"}
	podQpsName := quotaController.NameOfPod + comm.SubscribeQPS
	klog.Infof("podQpsName is :%s", podQpsName)
	resultSubsribe, subscribeGetRuleErr := lib.DynamicClient.Resource(subGvr).Namespace(subPromNamespace).Get(context.TODO(), podQpsName, metaV1.GetOptions{})
	if subscribeGetRuleErr != nil {
		return false, subscribeGetRuleErr
	}
	proSubscribeRule := &hermesV1.SubscriberRule{}
	err := runtime.DefaultUnstructuredConverter.FromUnstructured(resultSubsribe.UnstructuredContent(), proSubscribeRule)
	if err != nil {
		klog.Error(err)
		return false, nil
	}
	for _, d := range proSubscribeRule.Spec.PrometheusRule.Groups {
		sqlAlert := d.Rules[0].Alert
		sqlAlertQuota := strings.Split(sqlAlert, "+")
		klog.Infof("pod sqlAlertQuota is: %s", sqlAlertQuota)
		quotaSubScribeLocal := sqlAlertQuota[len(sqlAlertQuota)-1]
		klog.Infof("pod quota local is: %s", quotaSubScribeLocal)
		quotaSubScribeInt, _ := strconv.ParseInt(quotaSubScribeLocal, 10, 64)
		klog.Infof("pod quotaSubScribeInt quota local: %s", quotaSubScribeInt)
		quotaRemoteInt, _ := strconv.ParseInt(quotaController.LocalQuota, 10, 64)
		klog.Infof("pod quotaRemoteInt quota remote: %s", quotaRemoteInt)
		if quotaSubScribeInt != quotaRemoteInt {
			return false, nil
		} else {
			return true, nil
		}
	}
	return false, nil
}
