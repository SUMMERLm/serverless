package quota

import (
	"context"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/klog/v2"
	"strconv"
)

const subPromNamespace string = "hypermonitor"

func (q *Controller) qpsQuotaHpaAction(pod *corev1.Pod, quotaLocalCopyName string, qpsQuotaHpaUrl string, action string, qpsQuota int64, quotaStep int64) error {
	klog.Infof("Start add qps quota Hpa Alert")
	subGvr := schema.GroupVersionResource{Group: "hermes.pml.com", Version: "v1", Resource: "subscriberrules"}
	var aggerateRulesHigh string
	var aggerateRulesLow string
	//  更新情况下重新计算上下阈值
	//avg_over_time
	if action == "init" || action == "delete" {
		if int(qpsQuota) > 0 {
			aggerateRulesHigh = "avg(avg_over_time(io_sid_traffics{pod=\"" + pod.Name + "\"}[2m]))> bool " + strconv.Itoa(int(qpsQuota)-1) + " >0"
			aggerateRulesLow = "avg(avg_over_time(io_sid_traffics{pod=\"" + pod.Name + "\"}[2m]))< bool " + strconv.Itoa(int(qpsQuota-quotaStep)+1) + " >0"
		} else {
			aggerateRulesHigh = "avg(avg_over_time(io_sid_traffics{pod=\"" + pod.Name + "\"}[2m]))> bool " + strconv.Itoa(int(qpsQuota)) + " >0"
			aggerateRulesLow = "avg(avg_over_time(io_sid_traffics{pod=\"" + pod.Name + "\"}[2m]))< bool " + strconv.Itoa(int(qpsQuota-quotaStep)+1) + " >0"
		}
		//aggerateRulesHigh = "avg(avg_over_time(io_sid_traffics{pod=\"" + pod.Name + "\"}[2m]))> bool " + strconv.Itoa(int(qpsQuota)-1) + " >0"
		//aggerateRulesLow = "avg(avg_over_time(io_sid_traffics{pod=\"" + pod.Name + "\"}[2m]))< bool " + strconv.Itoa(1) + " >0"

	} else if action == "update" {
		if int(qpsQuota) > 0 {
			aggerateRulesHigh = "avg(avg_over_time(io_sid_traffics{pod=\"" + pod.Name + "\"}[2m]))> bool " + strconv.Itoa(int(qpsQuota)-1) + " >0"
			aggerateRulesLow = "avg(avg_over_time(io_sid_traffics{pod=\"" + pod.Name + "\"}[2m]))< bool " + strconv.Itoa(int(qpsQuota-quotaStep)+1) + " >0"
		} else {
			aggerateRulesHigh = "avg(avg_over_time(io_sid_traffics{pod=\"" + pod.Name + "\"}[2m]))> bool " + strconv.Itoa(int(qpsQuota)) + " >0"
			aggerateRulesLow = "avg(avg_over_time(io_sid_traffics{pod=\"" + pod.Name + "\"}[2m]))< bool " + strconv.Itoa(int(qpsQuota-quotaStep)+1) + " >0"
		}
		//aggerateRulesHigh = "avg(avg_over_time(io_sid_traffics{pod=\"" + pod.Name + "\"}[2m]))> bool " + strconv.Itoa(int(qpsQuota)-1) + " >0"
		//aggerateRulesLow = "avg(avg_over_time(io_sid_traffics{pod=\"" + pod.Name + "\"}[2m]))< bool " + strconv.Itoa(int(qpsQuota-quotaStep)+1) + " >0"
	}

	subscribeRule := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "hermes.pml.com/v1",
			"kind":       "SubscriberRule",
			"metadata": map[string]interface{}{
				"name":      pod.Name + "-qps",
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
									//"alert":       "default+sample-s2-6-f88d697f8-kfq24+pod_qps_quota_up",
									"alert": pod.Namespace + "+" + quotaLocalCopyName + "+" + pod.Name + "+pod_qps_quota_up" + "+" + strconv.FormatInt(qpsQuota, 10),

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
									"alert": pod.Namespace + "+" + quotaLocalCopyName + "+" + pod.Name + "+pod_qps_quota_down" + "+" + strconv.FormatInt(qpsQuota, 10),

									"annotations": map[string]interface{}{
										//"aggerateRules":   "((sum(rate(io_sid_traffics{uid=\"a96e1818-43bc-45b5-90ae-f197b1e3bb9f\",pod=\"sample-s2-6-f88d697f8-kfq24\"}[2m])))/(count(rate(io_sid_traffics{uid=\"a96e1818-43bc-45b5-90ae-f197b1e3bb9f\",pod=\"sample-s2-6-f88d697f8-kfq24\"}[2m])))*sum(((count(io_sid_traffics{pod=~\"sample-s2-6-f88d697f8-kfq24.*\"})by(pod))) )/count(((count(io_sid_traffics{pod=~\"sample-s2-6-f88d697f8-kfq24.*\"})by(pod)))))< bool 1 >0",
										"aggerateRules":   aggerateRulesLow,
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
		subsribeOld, subscribeGetRuleErr := q.dynamicClient.Resource(subGvr).Namespace(subPromNamespace).Get(context.TODO(), pod.Name+"-qps", metav1.GetOptions{})
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
