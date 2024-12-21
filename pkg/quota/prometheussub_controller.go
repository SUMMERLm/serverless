package quota

import (
	"context"
	"strconv"

	"github.com/SUMMERLm/serverless/pkg/comm"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/klog/v2"
)

const subPromNamespace string = "hypermonitor"

func (q *Controller) qpsQuotaHpaAction(pod *corev1.Pod, quotaLocalCopyName string, qpsQuotaHpaUrl string, action string, qpsQuota int64, quotaStep int64) error {
	klog.Infof("Start add qps quota Hpa Alert")
	subGvr := schema.GroupVersionResource{Group: "hermes.pml.com", Version: "v1", Resource: "subscriberrules"}
	var aggerateRulesHigh string
	var aggerateRulesLow string
	lowString := strconv.FormatInt(qpsQuota-quotaStep, 10)
	lowFloat, _ := strconv.ParseFloat(lowString, 64)
	var a float64 = 0.000000000000000001
	low64 := lowFloat + a
	//  更新情况下重新计算上下阈值
	// avg_over_time
	switch {
	case action == "init" || action == "delete":
		{
			aggerateRulesHigh = "sum(avg_over_time(io_sid_traffics{component_id=\"\",pod=\"" + pod.Name + "\"}[5s]))> bool " + strconv.Itoa(int(qpsQuota)) + " >0"
			aggerateRulesLow = "sum(avg_over_time(io_sid_traffics{component_id=\"\",pod=\"" + pod.Name + "\"}[5s]))< bool " + strconv.FormatFloat(low64, 'E', -1, 64) + " >0"
		}
	case action == "update":
		{
			aggerateRulesHigh = "sum(avg_over_time(io_sid_traffics{component_id=\"\",pod=\"" + pod.Name + "\"}[5s]))> bool " + strconv.Itoa(int(qpsQuota)) + " >0"
			aggerateRulesLow = "sum(avg_over_time(io_sid_traffics{component_id=\"\",pod=\"" + pod.Name + "\"}[5s]))< bool " + strconv.FormatFloat(low64, 'E', -1, 64) + " >0"
		}
	}

	subscribeRule := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "hermes.pml.com/v1",
			"kind":       "SubscriberRule",
			"metadata": map[string]interface{}{
				"name":      pod.Name + comm.SubscribeQPS,
				"namespace": subPromNamespace,
				"labels": map[string]interface{}{
					"serverless.cluster.pml.com.cn/serverless": quotaLocalCopyName + pod.Namespace,
				},
			},
			"spec": map[string]interface{}{
				"prometheusRule": map[string]interface{}{
					"groups": []map[string]interface{}{
						{ //"name": "serverless.rules.sample-s2-6-f88d697f8-kfq24.default",
							"name": "serverless.rules." + pod.Name + "." + pod.Namespace,
							"rules": []map[string]interface{}{
								{
									"alert": pod.Namespace + "+" + quotaLocalCopyName + "+" + pod.Name + "+pod_qps_quota_up" + "+" + strconv.FormatInt(qpsQuota, 10),

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
									"alert": pod.Namespace + "+" + quotaLocalCopyName + "+" + pod.Name + "+pod_qps_quota_down" + "+" + strconv.FormatInt(qpsQuota, 10),

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
				"subscribeType": "SubsCondition",
				//"subscriberAddress": "http://172.24.33.32:32000/serverles_qps_quota_hpa",
				"subscriberAddress": qpsQuotaHpaUrl,
			},
		},
	}

	if action == "init" {
		klog.Infof("create subscribeRule ")
		_, subscribeRuleErr := q.dynamicClient.Resource(subGvr).Namespace(subPromNamespace).Create(context.TODO(), subscribeRule, metav1.CreateOptions{})
		if subscribeRuleErr != nil {
			return subscribeRuleErr
		}
		return nil
	} else if action == "delete" {
		subscribeDeleteRuleErr := q.dynamicClient.Resource(subGvr).Namespace(subPromNamespace).Delete(context.TODO(), pod.Name, metav1.DeleteOptions{})
		if subscribeDeleteRuleErr != nil {
			return subscribeDeleteRuleErr
		}
		return nil
	} else if action == "update" {
		klog.Infof("update pod qps Quota")
		subsribeOld, subscribeGetRuleErr := q.dynamicClient.Resource(subGvr).Namespace(subPromNamespace).Get(context.TODO(), pod.Name+comm.SubscribeQPS, metav1.GetOptions{})
		if subscribeGetRuleErr != nil {
			return subscribeGetRuleErr
		}
		subscribeRule.SetResourceVersion(subsribeOld.GetResourceVersion())
		_, subscribeUpdateRuleErr := q.dynamicClient.Resource(subGvr).Namespace(subPromNamespace).Update(context.TODO(), subscribeRule, metav1.UpdateOptions{})
		if subscribeUpdateRuleErr != nil {
			return subscribeUpdateRuleErr
		}
		return nil
	}
	return nil
}
