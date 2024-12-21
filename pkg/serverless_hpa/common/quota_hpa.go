package common

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/SUMMERLm/serverless/pkg/comm"
	"github.com/SUMMERLm/serverless/pkg/etcd_lock"
	"github.com/SUMMERLm/serverless/pkg/serverless_hpa/lib"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/klog/v2"

	quotaV1 "github.com/SUMMERLm/quota/api/v1"
	v1 "k8s.io/api/core/v1"
	metaV1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type Quota struct {
	NameQuota         string
	NamespaceQuota    string
	NameOfPod         string
	NameSpaceQpsQuota string
	LocalQuota        string
	RealQps           string
}

var quoaHpaOption = etcd_lock.Option{
	ConnectionTimeout: comm.ConnectTimeOut * time.Second,
	Prefix:            "ServerlessQuotaLocker:",
	Debug:             false,
}

// pod quota require.
// 添加 单个pod quota，
// 否则聚合本deploy下属的pod quota进行分配，如果有剩余，则进行分配，否则申请
func (quotaController *Quota) QuotaRequire() bool {
	// quota获取并确认是否有配额，有则进行扩容，无则申请quota
	// 申请到配额，更新quota、更新pod quota、更新 subsql
	// pod QpsReal 判断当前pod Quota值是否足够，够了直接返回，不够再进行申请，避免无效申请多余quota再回收产生的震荡
	check, err := quotaController.qpsQuotaVerify()
	if err != nil {
		klog.Error(err)
	}
	klog.Infof("verify status of pod quota is : %s", check)
	if !check {
		return true
	}
	quotaGvr := schema.GroupVersionResource{Group: "serverless.pml.com.cn", Version: "v1", Resource: "quotas"}
	resultQuota, err := lib.DynamicClient.Resource(quotaGvr).Namespace(quotaController.NamespaceQuota).Get(context.TODO(), quotaController.NameQuota, metaV1.GetOptions{})
	if err != nil {
		klog.Error(err)
	}
	quotaPodQuota, foundQpsQuota, err := unstructured.NestedSlice(resultQuota.Object, "spec", "podQpsQuota")
	klog.Info("quotaPodQuota is:  %s", quotaPodQuota)
	if err != nil || !foundQpsQuota || len(quotaPodQuota) == 0 {
		klog.Errorf("quota label not found or error in spec: %v", err)
	}
	klog.Infof("Quota Msg  is: %s", quotaController)
	localPodQuota, _ := strconv.Atoi(quotaController.LocalQuota)
	realQps, _ := strconv.ParseFloat(quotaController.RealQps, 64)
	klog.Infof("localPodQuota is: %s and realQps is: %s", localPodQuota, realQps)
	if int(realQps) < localPodQuota {
		return true
	}
	for _, podQuotaInfo := range quotaPodQuota {
		podQuota, _ := podQuotaInfo.(map[string]interface{})
		if quotaController.NameOfPod == podQuota["podName"] {
			if _, ok := podQuota["qpsQuota"]; ok {
				if podQuota["qpsQuota"].(int64) < int64(localPodQuota) {
					klog.Infof("quota local is %s and quota to manage is %s", podQuota["qpsQuota"].(int64), localPodQuota)
					klog.Info("unsteady state,no need to manage quota")
					return true
				}
			} else {
				if 0 != int64(localPodQuota) {
					klog.Infof("quota local is %s and quota to manage is %s", podQuota["qpsQuota"].(int64), localPodQuota)
					klog.Info("unsteady state,no need to manage quota")
					return true
				}
			}
		}
	}
	// podList := &apiv1.PodList{}
	quota := &quotaV1.Quota{}
	if err = runtime.DefaultUnstructuredConverter.FromUnstructured(resultQuota.UnstructuredContent(), quota); err != nil {
		klog.Error(err)
	}
	klog.Infof("before pod qps quota add: quota name is: %s", quota.Name)
	klog.Infof("before pod qps quota add: quota of all pod is: %s", quota.Spec.PodQpsQuota)
	klog.Infof("before pod qps quota add: quota of serverless is : %s ", quota.Labels)
	klog.Infof("before pod qps quota add: quota of serverless %s  remain is : %s ", quota.Name, quota.Labels["quota.cluster.pml.com.cn/quotaRemain"])
	quotaRemain, _ := strconv.Atoi(quota.Labels["quota.cluster.pml.com.cn/quotaRemain"])
	quotaStep, _ := strconv.Atoi(quota.Labels["quota.cluster.pml.com.cn/quotaStep"])
	quotaLocal, _ := strconv.Atoi(quota.Labels["quota.cluster.pml.com.cn/quota"])

	quotaUsed, _ := strconv.Atoi(quota.Labels["quota.cluster.pml.com.cn/quotaUsed"])
	quotaRequire, _ := strconv.Atoi(quota.Labels["quota.cluster.pml.com.cn/quotaRequire"])

	quotalabels, foundQuota, err := unstructured.NestedStringMap(resultQuota.Object, "metadata", "labels")
	klog.Info("quota.cluster.pml.com.cn/quotaRemain is:  %s", quotalabels["quota.cluster.pml.com.cn/quotaRemain"])
	if err != nil || !foundQuota || len(quotalabels) == 0 {
		klog.Errorf("quota label  not found or error in spec: %v", err)
	}
	if err := unstructured.SetNestedStringMap(resultQuota.Object, quotalabels, "metadata", "labels"); err != nil {
		klog.Error(err)
	}
	// podqpsQuota增加。podqpsalert设置为0。
	// judge quota ==localQuota
	// clusterName := quota.Spec.LocalName
	if quotaRemain >= quotaStep {
		// 当前quotaremain够用。
		// quotaRemain减少一个分片，quotaUsed增加一个分片。分片分配给pod，
		if locker, err := etcd_lock.New(lib.EtcdEndpointQuota, quoaHpaOption); err != nil {
			klog.Errorf("创建锁失败：%+v", err)
		} else if who, ok := locker.Acquire(quotaController.NameQuota + quotaController.NamespaceQuota); ok {
			defer func() {
				err = locker.Release()
				if err != nil {
					klog.Error(err)
				} else {
					klog.Infof("defer release done")
				}
			}()
			// 抢到锁后执行业务逻辑，没有抢到则退出
			klog.Infof("进程 %+v 持有锁 %+v 正在处理任务中...", os.Getpid(), locker.GetId())

			quotalabels, foundQuota, err := unstructured.NestedStringMap(resultQuota.Object, "metadata", "labels")
			klog.Info("quota.cluster.pml.com.cn/quotaRemain is:  %s", quotalabels["quota.cluster.pml.com.cn/quotaRemain"])
			if err != nil || !foundQuota || len(quotalabels) == 0 {
				klog.Errorf("quota label  not found or error in spec: %v", err)
			}
			// quotaRemain减少
			// quotaUsed增加
			// quotaRequire增加，减少回收的资源数。
			quotalabels["quota.cluster.pml.com.cn/quotaRemain"] = strconv.Itoa(quotaRemain - quotaStep)
			quotalabels["quota.cluster.pml.com.cn/quotaUsed"] = strconv.Itoa(quotaUsed + quotaStep)
			if quotaRequire < quotaLocal {
				quotalabels["quota.cluster.pml.com.cn/quotaRequire"] = strconv.Itoa(quotaRequire + quotaStep)
			}
			// 当前cluster有quota，不需要走申请，直接分配quotaRequire
			// quotalabels["quota.cluster.pml.com.cn/quotaRequire"] = strconv.Itoa(quotaRequire - quotaStep)
			if err = unstructured.SetNestedStringMap(resultQuota.Object, quotalabels, "metadata", "labels"); err != nil {
				klog.Error(err)
			}
			// podqpsQuota增加。podqpsalert设置为0。
			quotaPodQuota, foundQpsQuota, err := unstructured.NestedSlice(resultQuota.Object, "spec", "podQpsQuota")
			klog.Info("quotaPodQuota is:  %s", quotaPodQuota)
			if err != nil || !foundQpsQuota || len(quotaPodQuota) == 0 {
				klog.Errorf("quota label not found or error in spec: %v", err)
			}
			for i, podQuotaInfo := range quotaPodQuota {
				podQuota, _ := podQuotaInfo.(map[string]interface{})
				if quotaController.NameOfPod == podQuota["podName"] {
					// add podQuotaInfo
					_, ok = podQuota["qpsQuota"]
					var qpsQuota int64
					if ok {
						qpsQuota = podQuota["qpsQuota"].(int64) + int64(quotaStep)
					} else {
						qpsQuota = int64(quotaStep)
					}
					var podQuotaMsg = map[string]interface{}{"podName": podQuota["podName"], "qpsQuota": qpsQuota, "clusterName": podQuota["clusterName"]}
					klog.Info(podQuotaMsg)
					quotaPodQuota[i] = podQuotaMsg
					klog.Info(quotaPodQuota)

					if err = unstructured.SetNestedSlice(resultQuota.Object, quotaPodQuota, "spec", "podQpsQuota"); err != nil {
						klog.Error(err)
					}
					resultQuota = quotaController.quotaStateManage(resultQuota, quotalabels)
					_, updateQuotaErr := lib.DynamicClient.Resource(quotaGvr).Namespace(quotaController.NamespaceQuota).Update(context.TODO(), resultQuota, metaV1.UpdateOptions{})
					if updateQuotaErr != nil {
						klog.Info(updateQuotaErr)
					}
					podinfo, _ := lib.K8sClient.CoreV1().Pods(quotaController.NamespaceQuota).Get(context.TODO(), podQuota["podName"].(string), metaV1.GetOptions{})
					// hpaWebUrl := viper.GetString("serverless-config.qpsQuotaUrl")
					// var action = "update"
					// quotaController.qpsQuotaHpaUpdate(*podinfo, hpaWebUrl, action, podQuotaMsg["qpsQuota"].(int64), int64(quotaStep))
					quotaController.podQpsQuotaHpaUpdate(podinfo, podQuotaMsg["qpsQuota"].(int64), int64(quotaStep))
				}
			}
			return true
		} else {
			klog.Infof("获取锁失败，锁现在在 %+v 手中", who)
		}
	} else if quotaRemain == 0 {
		// quotaRequire 变更（+1）
		// podqpsalert设置为1（代表需要一个分片）。
		quotalabels, foundQuota, err := unstructured.NestedStringMap(resultQuota.Object, "metadata", "labels")
		if err != nil || !foundQuota || len(quotalabels) == 0 {
			klog.Errorf("quota label  not found or error in spec: %v", err)
		}
		klog.Info("quota.cluster.pml.com.cn/quotaRemain is:  %s", quotalabels["quota.cluster.pml.com.cn/quotaRemain"])
		klog.Info("quota.cluster.pml.com.cn/quotaRequire is:  %s", quotalabels["quota.cluster.pml.com.cn/quotaRequire"])
		klog.Info("quota.cluster.pml.com.cn/quota is:  %s", quotalabels["quota.cluster.pml.com.cn/quota"])
		klog.Info("quota.cluster.pml.com.cn/quotaUsed is:  %s", quotalabels["quota.cluster.pml.com.cn/quotaUsed"])

		if err = unstructured.SetNestedStringMap(resultQuota.Object, quotalabels, "metadata", "labels"); err != nil {
			klog.Error(err)
		}
		// podqpsQuota增加。podqpsalert设置为0。
		quotaPodQpsIncreaseOrDecrease, foundQpsAlertQuota, err := unstructured.NestedSlice(resultQuota.Object, "spec", "podQpsIncreaseOrDecrease")
		klog.Info("quotaPodQpsIncreaseOrDecrease is:  %s", quotaPodQpsIncreaseOrDecrease)
		if err != nil || !foundQpsAlertQuota || len(quotaPodQpsIncreaseOrDecrease) == 0 {
			klog.Errorf("quota alert msg not found or error in spec: %v", err)
		}
		for i, podAlertQuotaInfo := range quotaPodQpsIncreaseOrDecrease {
			podAlertQuota, _ := podAlertQuotaInfo.(map[string]interface{})
			if quotaController.NameOfPod == podAlertQuota["podName"] {
				// add podQuotaInfo
				klog.Info(quotaPodQpsIncreaseOrDecrease[i])
				quotaPodQpsIncreaseOrDecreaseMsg := quotaPodQpsIncreaseOrDecrease[i].(map[string]interface{})
				klog.Info(quotaPodQpsIncreaseOrDecreaseMsg["qpsIncreaseOrDecrease"])
				if quotaPodQpsIncreaseOrDecreaseMsg["qpsIncreaseOrDecrease"] == int64(1) && quotaRequire > quotaLocal {
					// 不需要加锁，直接返回
					klog.Infof("already in scale，return")
					return true
				} else {
					if locker, err := etcd_lock.New(lib.EtcdEndpointQuota, quoaHpaOption); err != nil {
						klog.Errorf("创建锁失败：%+v", err)
					} else if who, ok := locker.Acquire(quotaController.NameQuota + quotaController.NamespaceQuota); ok {
						defer func() {
							err = locker.Release()
							if err != nil {
								klog.Error(err)
							} else {
								klog.Infof("defer release done")
							}
						}()
						// 抢到锁后执行业务逻辑，没有抢到则退出
						var podAlertQuotaMsg = map[string]interface{}{"podName": podAlertQuota["podName"], "qpsIncreaseOrDecrease": int64(1)}
						klog.Info(podAlertQuotaMsg)
						quotaPodQpsIncreaseOrDecrease[i] = podAlertQuotaMsg
						klog.Info(quotaPodQpsIncreaseOrDecrease)
						// 初次请求，无剩余配额则进行配额申请
						quotalabels["quota.cluster.pml.com.cn/quotaRequire"] = strconv.Itoa(quotaRequire + quotaStep)
						if err := unstructured.SetNestedStringMap(resultQuota.Object, quotalabels, "metadata", "labels"); err != nil {
							klog.Error(err)
						}
						if err := unstructured.SetNestedSlice(resultQuota.Object, quotaPodQpsIncreaseOrDecrease, "spec", "podQpsIncreaseOrDecrease"); err != nil {
							klog.Error(err)
						}
						resultQuota = quotaController.quotaStateManage(resultQuota, quotalabels)
						_, updateQuotaErr := lib.DynamicClient.Resource(quotaGvr).Namespace(quotaController.NamespaceQuota).Update(context.TODO(), resultQuota, metaV1.UpdateOptions{})
						if updateQuotaErr != nil {
							klog.Info(updateQuotaErr)
						}
						klog.Infof("进程 %+v 持有锁 %+v 正在处理任务中...", os.Getpid(), locker.GetId())
					} else {
						klog.Infof("获取锁失败，锁现在在 %+v 手中", who)
					}
				}
			}
		}
	}
	klog.Infof("after pod qps quota add: quota name is: %s", quota.Name)
	klog.Infof("after pod qps quota add: quota of all pod is: %s", quota.Spec.PodQpsQuota)
	klog.Infof("after pod qps quota add: quota of serverless is : %s ", quota.Labels)
	klog.Infof("after pod qps quota add: quota of serverless %s  remain is : %s ", quota.Name, quota.Labels["quota.cluster.pml.com.cn/quotaRemain"])
	fmt.Printf("after qps quota  scale up serverless done...: %s, namespace: %s\n", quota.Name, quota.Namespace)
	return true
}

// pod quota return.
// 归还单个pod quota，归还至本级quota池中（label）
// 否则聚合本deploy下属的pod quota进行分配，如果有剩余，则进行分配，否则申请
// ***quotaRequire =quotlocal+qpsincrese*n
// ***quotalocal = quotaused+quotaRemian
func (quotaController *Quota) QuotaReturn() bool {
	// 锁粒度细化
	// pod QpsReal 判断当前pod Quota值是否足够，够了直接返回，不够再进行申请，避免无效申请多余quota再回收产生的震荡
	check, err := quotaController.qpsQuotaVerify()
	if err != nil {
		klog.Error(err)
		return false
	}
	klog.Infof("verify status of pod quota is : %s", check)
	if !check {
		return true
	}
	quotaGvr := schema.GroupVersionResource{Group: "serverless.pml.com.cn", Version: "v1", Resource: "quotas"}
	resultQuota, err := lib.DynamicClient.Resource(quotaGvr).Namespace(quotaController.NamespaceQuota).Get(context.TODO(), quotaController.NameQuota, metaV1.GetOptions{})
	if err != nil {
		klog.Error(err)
		return false
	}
	quotaPodQuota, foundQpsQuota, err := unstructured.NestedSlice(resultQuota.Object, "spec", "podQpsQuota")
	klog.Info("quotaPodQuota is:  %s", quotaPodQuota)
	if err != nil || !foundQpsQuota || len(quotaPodQuota) == 0 {
		klog.Errorf("quota label not found or error in spec: %v", err)
	}

	klog.Infof("Quota Msg  is: %s", quotaController)
	localPodQuota, _ := strconv.Atoi(quotaController.LocalQuota)
	realQps, _ := strconv.ParseFloat(quotaController.RealQps, 64)
	klog.Infof("localPodQuota is: %s and realQps is: %s", localPodQuota, realQps)
	if int(realQps)+1 > localPodQuota {
		return true
	}
	klog.Info(localPodQuota)
	for _, podQuotaInfo := range quotaPodQuota {
		podQuota, _ := podQuotaInfo.(map[string]interface{})
		if quotaController.NameOfPod == podQuota["podName"] {
			if _, ok := podQuota["qpsQuota"]; ok {
				if podQuota["qpsQuota"].(int64) != int64(localPodQuota) {
					klog.Infof("quota loacl is %s and quota to manage is %s", podQuota["qpsQuota"].(int64), localPodQuota)
					klog.Info("unsteady state,no need to manage quota")
					return true
				}
			} else {
				if 0 != int64(localPodQuota) {
					klog.Infof("quota loacl is %s and quota to manage is %s", podQuota["qpsQuota"].(int64), localPodQuota)
					klog.Info("unsteady state,no need to manage quota")
					return true
				}
			}
		}
	}
	// podList := &apiv1.PodList{}
	quota := &quotaV1.Quota{}
	if err = runtime.DefaultUnstructuredConverter.FromUnstructured(resultQuota.UnstructuredContent(), quota); err != nil {
		klog.Error(err)
	}
	klog.Infof("before pod qps quota return: quota name is: %s", quota.Name)
	klog.Infof("before pod qps quota return: quota of all pod is: %s", quota.Spec.PodQpsQuota)
	klog.Infof("before pod qps quota return: quota of serverless is : %s ", quota.Labels)
	klog.Infof("before pod qps quota return: quota of serverless %s  remain is : %s ", quota.Name, quota.Labels["quota.cluster.pml.com.cn/quotaRemain"])
	quotaRemain, _ := strconv.Atoi(quota.Labels["quota.cluster.pml.com.cn/quotaRemain"])
	quotaStep, _ := strconv.Atoi(quota.Labels["quota.cluster.pml.com.cn/quotaStep"])
	quotaLocal, _ := strconv.Atoi(quota.Labels["quota.cluster.pml.com.cn/quota"])

	quotaUsed, _ := strconv.Atoi(quota.Labels["quota.cluster.pml.com.cn/quotaUsed"])
	quotaRequire, _ := strconv.Atoi(quota.Labels["quota.cluster.pml.com.cn/quotaRequire"])
	quotalabels, foundQuota, err := unstructured.NestedStringMap(resultQuota.Object, "metadata", "labels")
	if err != nil || !foundQuota || len(quotalabels) == 0 {
		klog.Errorf("quota label  not found or error in spec: %v", err)
	}

	klog.Info("quota.cluster.pml.com.cn/quotaRemain is:  %s", quotalabels["quota.cluster.pml.com.cn/quotaRemain"])
	klog.Info("quota.cluster.pml.com.cn/quotaRequire is:  %s", quotalabels["quota.cluster.pml.com.cn/quotaRequire"])
	klog.Info("quota.cluster.pml.com.cn/quota is:  %s", quotalabels["quota.cluster.pml.com.cn/quota"])
	klog.Info("quota.cluster.pml.com.cn/quotaUsed is:  %s", quotalabels["quota.cluster.pml.com.cn/quotaUsed"])

	foundingMember := quotalabels["quota.cluster.pml.com.cn/foundingMember"]
	if foundingMember == comm.True {
		klog.Infof("A serverless founding member")
	} else {
		klog.Infof("Not a serverless founding member")
	}
	// quotalabels["quota.cluster.pml.com.cn/quotaRequire"] = strconv.Itoa(quotaRequire - quotaStep)
	if err = unstructured.SetNestedStringMap(resultQuota.Object, quotalabels, "metadata", "labels"); err != nil {
		klog.Error(err)
	}
	quotaPodQpsIncreaseOrDecrease, foundQpsAlertQuota, err := unstructured.NestedSlice(resultQuota.Object, "spec", "podQpsIncreaseOrDecrease")
	klog.Info("quotaPodQpsIncreaseOrDecrease is:  %s", quotaPodQpsIncreaseOrDecrease)
	if err != nil || !foundQpsAlertQuota || len(quotaPodQpsIncreaseOrDecrease) == 0 {
		klog.Errorf("quota alert msg not found or error in spec: %v", err)
	}
	// TODO 判断如果当前元老的最后一个pod，且该pod的quota为1个step，且该quota状态为平稳状态，直接返回，不需要走后续加锁流程
	if foundingMember == comm.True && len(quotaPodQpsIncreaseOrDecrease) == 1 && quotaRemain == 0 && quotaRequire == quotaStep && quotaStep == quotaLocal {
		return true
	} else {
		if locker, err := etcd_lock.New(lib.EtcdEndpointQuota, quoaHpaOption); err != nil {
			klog.Errorf("创建锁失败：%+v", err)
		} else if who, ok := locker.Acquire(quotaController.NameQuota + quotaController.NamespaceQuota); ok {
			defer func() {
				err = locker.Release()
				if err != nil {
					klog.Error(err)
				} else {
					klog.Infof("defer release done")
				}
			}()
			// 抢到锁后执行业务逻辑，没有抢到则退出
			klog.Infof("进程 %+v 持有锁 %+v 正在处理任务中...", os.Getpid(), locker.GetId())
			for i, podAlertQuotaInfo := range quotaPodQpsIncreaseOrDecrease {
				podAlertQuota, _ := podAlertQuotaInfo.(map[string]interface{})
				if quotaController.NameOfPod == podAlertQuota["podName"] {
					// add podQuotaInfo
					klog.Info(quotaPodQpsIncreaseOrDecrease[i])
					// if (quotaPodQpsIncreaseOrDecrease[i].(map))["qpsIncreaseOrDecrease"]=="1"{
					// 	return true
					// }
					quotaPodQpsIncreaseOrDecreaseMsg := quotaPodQpsIncreaseOrDecrease[i].(map[string]interface{})
					klog.Info(quotaPodQpsIncreaseOrDecreaseMsg["qpsIncreaseOrDecrease"])
					// 原有扩容状态，开始缩容
					// ********1*******************
					if quotaPodQpsIncreaseOrDecreaseMsg["qpsIncreaseOrDecrease"] == int64(1) {
						klog.Infof("used in scale up but now scale down")
						// case1：qpsIncreaseOrDecrease为1状态：当前pod的quota处于申请中的状态
						// 	1：原有podQpsIncreaseOrDecrease：qpsIncreaseOrDecrease 设置0（可能存在扩容（1）还未完成（quotaRequire-1step），进行缩容（置为0，通常不存在归还不成功的状态。如果存在则置为-1））
						// 2：原有podQpsQuota：
						// 是最后一个pod：判断是不是元老实例
						//  2.1：是元老实例：最后一个pod的qpsquota至少为1个step控制（如果qpsQuota>=1个分片，则qpsQuota减1 step，否则不变（保证quota最小化为0，不能为负数））
						//  2.2：非元老实例：则可以最后一个qpsquota为0（如果qpsQuota>=1个分片，则qpsQuota减1 step，否则不变（保证quota最小化为0，不能为负数））
						// 不是最后一个pod：
						//  2.3：如果qpsQuota>=1个分片，则qpsQuota减1 step，否则不变（保证quota最小化为0，不能为负数）
						// 3：quotaRequire-1step(最小化控制)
						// 4: subsqpl更新
						// 缩容alert设置为0

						var podAlertQuotaMsg = map[string]interface{}{"podName": podAlertQuota["podName"], "qpsIncreaseOrDecrease": int64(0)}
						klog.Info(podAlertQuotaMsg)
						quotaPodQpsIncreaseOrDecrease[i] = podAlertQuotaMsg
						klog.Info(quotaPodQpsIncreaseOrDecrease)
						if err := unstructured.SetNestedSlice(resultQuota.Object, quotaPodQpsIncreaseOrDecrease, "spec", "podQpsIncreaseOrDecrease"); err != nil {
							klog.Error(err)
						}

						quotaPodQuota, foundQpsQuota, err := unstructured.NestedSlice(resultQuota.Object, "spec", "podQpsQuota")
						klog.Info("quotaPodQuota is:  %s", quotaPodQuota)
						if err != nil || !foundQpsQuota || len(quotaPodQuota) == 0 {
							klog.Errorf("quota label not found or error in spec: %v", err)
						}
						// 最后一个pod
						// ***************1.1*******************
						// if len(quotaPodQuota) == 1 {
						if quotaController.lastPodOfServerless(quotaPodQuota) {
							klog.Infof("The last one pod of serverless: %s", quotaController.NameQuota)
							// 元老实例
							// **********1.1.1**********
							if foundingMember == comm.True {
								// 扩容状态，最后一个pod，元老实例缩容
								for i, podQuotaInfo := range quotaPodQuota {
									podQuota, _ := podQuotaInfo.(map[string]interface{})
									if quotaController.NameOfPod == podQuota["podName"] {
										// del podQuotaInfo
										if podQuota["qpsQuota"].(int64) > int64(quotaStep) {
											var qpsQuota = podQuota["qpsQuota"].(int64) - int64(quotaStep)
											var podQuotaMsg = map[string]interface{}{"podName": podQuota["podName"], "qpsQuota": qpsQuota, "clusterName": podQuota["clusterName"]}
											klog.Info(podQuotaMsg)
											quotaPodQuota[i] = podQuotaMsg
											// if quotaRequire-quotaLocal >= quotaStep {
											quotalabels["quota.cluster.pml.com.cn/quotaRequire"] = strconv.Itoa(quotaRequire - 2*quotaStep)
											// }
											quotalabels["quota.cluster.pml.com.cn/quotaRemain"] = strconv.Itoa(quotaRemain + quotaStep)
											quotalabels["quota.cluster.pml.com.cn/quotaUsed"] = strconv.Itoa(quotaUsed - quotaStep)
											resultQuota = quotaController.quotaStateManage(resultQuota, quotalabels)
											podinfo, _ := lib.K8sClient.CoreV1().Pods(quotaController.NamespaceQuota).Get(context.TODO(), podQuota["podName"].(string), metaV1.GetOptions{})
											quotaController.podQpsQuotaHpaUpdate(podinfo, podQuotaMsg["qpsQuota"].(int64), int64(quotaStep))
										} else {
											// 元老实例最后一个pod的quota最小化为1step
											// if quotaRequire-quotaLocal >= quotaStep {
											quotalabels["quota.cluster.pml.com.cn/quotaRequire"] = strconv.Itoa(quotaRequire - quotaStep)
											// }
											resultQuota = quotaController.quotaStateManage(resultQuota, quotalabels)

											var qpsQuota = int64(quotaStep)
											var podQuotaMsg = map[string]interface{}{"podName": podQuota["podName"], "qpsQuota": qpsQuota, "clusterName": podQuota["clusterName"]}
											klog.Info(podQuotaMsg)
											quotaPodQuota[i] = podQuotaMsg
											podinfo, _ := lib.K8sClient.CoreV1().Pods(quotaController.NamespaceQuota).Get(context.TODO(), podQuota["podName"].(string), metaV1.GetOptions{})
											quotaController.podQpsQuotaHpaUpdate(podinfo, podQuotaMsg["qpsQuota"].(int64), int64(quotaStep))
										}
										klog.Info(quotaPodQuota)
										if err := unstructured.SetNestedSlice(resultQuota.Object, quotaPodQuota, "spec", "podQpsQuota"); err != nil {
											klog.Error(err)
										}
										if err := unstructured.SetNestedStringMap(resultQuota.Object, quotalabels, "metadata", "labels"); err != nil {
											klog.Error(err)
										}
									}
								}
							} else {
								// ***************1.1.2*******************
								// 非元老实例
								for i, podQuotaInfo := range quotaPodQuota {
									podQuota, _ := podQuotaInfo.(map[string]interface{})
									if quotaController.NameOfPod == podQuota["podName"] {
										// del podQuotaInfo
										if podQuota["qpsQuota"].(int64) >= int64(quotaStep) {
											var qpsQuota = podQuota["qpsQuota"].(int64) - int64(quotaStep)
											var podQuotaMsg = map[string]interface{}{"podName": podQuota["podName"], "qpsQuota": qpsQuota, "clusterName": podQuota["clusterName"]}
											klog.Info(podQuotaMsg)
											quotaPodQuota[i] = podQuotaMsg
											// if quotaRequire-quotaLocal >= quotaStep {
											// 扩容状态，缩quota
											klog.Infof("used in require state: quotaRequire return 2 step of quota of: %s", quotaController.NameQuota)
											quotalabels["quota.cluster.pml.com.cn/quotaRequire"] = strconv.Itoa(quotaRequire - 2*quotaStep)
											klog.Infof(quotalabels["quota.cluster.pml.com.cn/quotaRequire"])
											// } else {
											// 平稳状态，缩quota
											// 	klog.Infof("used in require state: quotaRequire return 1 step of quota of: %s", quotaController.NameQuota)
											// 	quotalabels["quota.cluster.pml.com.cn/quotaRequire"] = strconv.Itoa(quotaRequire - quotaStep)
											// 	klog.Infof(quotalabels["quota.cluster.pml.com.cn/quotaRequire"])
											// }
											quotalabels["quota.cluster.pml.com.cn/quotaRemain"] = strconv.Itoa(quotaRemain + quotaStep)
											quotalabels["quota.cluster.pml.com.cn/quotaUsed"] = strconv.Itoa(quotaUsed - quotaStep)
											podinfo, _ := lib.K8sClient.CoreV1().Pods(quotaController.NamespaceQuota).Get(context.TODO(), podQuota["podName"].(string), metaV1.GetOptions{})
											quotaController.podQpsQuotaHpaUpdate(podinfo, podQuotaMsg["qpsQuota"].(int64), int64(quotaStep))
										} else {
											klog.Infof("used in require state: quotaRequire return 2 step of quota of: %s", quotaController.NameQuota)
											quotalabels["quota.cluster.pml.com.cn/quotaRequire"] = strconv.Itoa(quotaRequire - quotaStep)
											klog.Infof(quotalabels["quota.cluster.pml.com.cn/quotaRequire"])
										}
										klog.Info(quotaPodQuota)
										if err := unstructured.SetNestedSlice(resultQuota.Object, quotaPodQuota, "spec", "podQpsQuota"); err != nil {
											klog.Error(err)
										}
										if err := unstructured.SetNestedStringMap(resultQuota.Object, quotalabels, "metadata", "labels"); err != nil {
											klog.Error(err)
										}
									}
								}
							}
						} else {
							// 非最后一个实例
							// ***************1.2*******************
							for i, podQuotaInfo := range quotaPodQuota {
								podQuota, _ := podQuotaInfo.(map[string]interface{})
								if quotaController.NameOfPod == podQuota["podName"] {
									// del podQuotaInfo
									_, ok := podQuota["qpsQuota"]
									if ok {
										if podQuota["qpsQuota"].(int64) >= int64(quotaStep) {
											var qpsQuota = podQuota["qpsQuota"].(int64) - int64(quotaStep)
											var podQuotaMsg = map[string]interface{}{"podName": podQuota["podName"], "qpsQuota": qpsQuota, "clusterName": podQuota["clusterName"]}
											klog.Info(podQuotaMsg)

											quotaPodQuota[i] = podQuotaMsg
											quotalabels["quota.cluster.pml.com.cn/quotaRequire"] = strconv.Itoa(quotaRequire - quotaStep)
											quotalabels["quota.cluster.pml.com.cn/quotaUsed"] = strconv.Itoa(quotaUsed - quotaStep)
											quotalabels["quota.cluster.pml.com.cn/quotaRemain"] = strconv.Itoa(quotaRemain + quotaStep)
											podinfo, _ := lib.K8sClient.CoreV1().Pods(quotaController.NamespaceQuota).Get(context.TODO(), podQuota["podName"].(string), metaV1.GetOptions{})
											quotaController.podQpsQuotaHpaUpdate(podinfo, podQuotaMsg["qpsQuota"].(int64), int64(quotaStep))
										}
									} else {
										quotalabels["quota.cluster.pml.com.cn/quotaRequire"] = strconv.Itoa(quotaRequire - quotaStep)
										quotalabels["quota.cluster.pml.com.cn/quotaUsed"] = strconv.Itoa(quotaUsed - quotaStep)
										quotalabels["quota.cluster.pml.com.cn/quotaRemain"] = strconv.Itoa(quotaRemain + quotaStep)
									}
									klog.Info(quotaPodQuota)
									if err := unstructured.SetNestedSlice(resultQuota.Object, quotaPodQuota, "spec", "podQpsQuota"); err != nil {
										klog.Error(err)
									}
									if err := unstructured.SetNestedStringMap(resultQuota.Object, quotalabels, "metadata", "labels"); err != nil {
										klog.Error(err)
									}
									resultQuota = quotaController.quotaStateManage(resultQuota, quotalabels)
								}
							}
						}
						_, updateQuotaErr := lib.DynamicClient.Resource(quotaGvr).Namespace(quotaController.NamespaceQuota).Update(context.TODO(), resultQuota, metaV1.UpdateOptions{})
						if updateQuotaErr != nil {
							klog.Info(updateQuotaErr)
						}
						return true
					} else {
						// **********2************
						// 原有平稳状态，开始缩容
						klog.Infof("used in steady but now scale down")
						// case2:qpsIncreaseOrDecrease为0状态：当前pod的quota处于稳定状态
						// 1:原有podQpsQuota：
						// 是最后一个pod：判断是不是元老实例
						// 		 1.1：是元老实例：最后一个pod的qpsquota至少为1个step控制（如果qpsQuota>=1个分片，则qpsQuota减1 step，否则不变（保证quota最小化为0，不能为负数））
						// 		 1.2：非元老实例：则最后一个qpsquota可以为0（如果qpsQuota>=1个分片，则qpsQuota减1 step，否则不变（保证quota最小化为0，不能为负数））
						// 不是最后一个pod：
						// 		 1.3：如果qpsQuota>=1个分片，则qpsQuota减1 step，否则不变（保证quota最小化为0，不能为负数）
						// 2:quotaUsed 减1 step完成后，quotaRemain = quotalocal-quotaUsed。不单单是加1 step
						// 3:subsqpl更新
						quotaPodQuota, foundQpsQuota, err := unstructured.NestedSlice(resultQuota.Object, "spec", "podQpsQuota")
						klog.Info("quotaPodQuota is:  %s", quotaPodQuota)
						if err != nil || !foundQpsQuota || len(quotaPodQuota) == 0 {
							klog.Errorf("quota label not found or error in spec: %v", err)
						}
						// 最后一个pod
						// **********2.1************
						// 判断最后一个pod的逻辑需要更新，不是length（PodQuotainfo），应为pod qps quota不为0且podAlertIncrease为0的个数
						// quotaController.lastPodOfServerless(quotaPodQuota)
						// if len(quotaPodQuota) == 1 {
						if quotaController.lastPodOfServerless(quotaPodQuota) {
							// 平稳状态最后一个pod拥有quota
							klog.Infof("The last one pod of serverless: %s", quotaController.NameQuota)
							if foundingMember == comm.True {
								// **********2.1.1************
								// 平稳状态最后一个pod拥有quota，且为元老serverless
								for i, podQuotaInfo := range quotaPodQuota {
									podQuota, _ := podQuotaInfo.(map[string]interface{})
									if quotaController.NameOfPod == podQuota["podName"] {
										// del podQuotaInfo
										// 最后一个pod的qpsquota大于step，可以缩容
										_, ok := podQuota["qpsQuota"]
										if ok {
											if podQuota["qpsQuota"].(int64) > int64(quotaStep) {
												var qpsQuota = podQuota["qpsQuota"].(int64) - int64(quotaStep)
												var podQuotaMsg = map[string]interface{}{"podName": podQuota["podName"], "qpsQuota": qpsQuota, "clusterName": podQuota["clusterName"]}
												klog.Info(podQuotaMsg)
												quotaPodQuota[i] = podQuotaMsg
												if quotaUsed > quotaStep {
													quotalabels["quota.cluster.pml.com.cn/quotaUsed"] = strconv.Itoa(quotaUsed - quotaStep)
													quotalabels["quota.cluster.pml.com.cn/quotaRemain"] = strconv.Itoa(quotaRemain + quotaStep)
													quotalabels["quota.cluster.pml.com.cn/quotaRequire"] = strconv.Itoa(quotaRequire - quotaStep)
												}
												podinfo, _ := lib.K8sClient.CoreV1().Pods(quotaController.NamespaceQuota).Get(context.TODO(), podQuota["podName"].(string), metaV1.GetOptions{})
												quotaController.podQpsQuotaHpaUpdate(podinfo, podQuotaMsg["qpsQuota"].(int64), int64(quotaStep))
											} else {
												// 最后一个pod的qpsquota等于step，不可以缩容
												// 元老实例最后一个pod的quota最小化为1step
												var qpsQuota = int64(quotaStep)
												quotalabels["quota.cluster.pml.com.cn/quotaRequire"] = strconv.Itoa(quotaStep)
												quotalabels["quota.cluster.pml.com.cn/quotaUsed"] = strconv.Itoa(quotaStep)
												quotalabels["quota.cluster.pml.com.cn/quotaRemain"] = strconv.Itoa(quotaLocal - quotaStep)

												var podQuotaMsg = map[string]interface{}{"podName": podQuota["podName"], "qpsQuota": qpsQuota, "clusterName": podQuota["clusterName"]}
												klog.Info(podQuotaMsg)
												quotaPodQuota[i] = podQuotaMsg
												podinfo, _ := lib.K8sClient.CoreV1().Pods(quotaController.NamespaceQuota).Get(context.TODO(), podQuota["podName"].(string), metaV1.GetOptions{})
												quotaController.podQpsQuotaHpaUpdate(podinfo, podQuotaMsg["qpsQuota"].(int64), int64(quotaStep))
											}
										}
										klog.Info(quotaPodQuota)
										if err := unstructured.SetNestedSlice(resultQuota.Object, quotaPodQuota, "spec", "podQpsQuota"); err != nil {
											klog.Error(err)
										}
										if err := unstructured.SetNestedStringMap(resultQuota.Object, quotalabels, "metadata", "labels"); err != nil {
											klog.Error(err)
										}
									}
								}
							} else {
								// 平稳状态、最后一个pod、非元老serverless拥有quota，可以缩最小化为0
								// **********2.1.2************
								for i, podQuotaInfo := range quotaPodQuota {
									podQuota, _ := podQuotaInfo.(map[string]interface{})
									if quotaController.NameOfPod == podQuota["podName"] {
										// del podQuotaInfo
										if podQuota["qpsQuota"].(int64) >= int64(quotaStep) {
											var qpsQuota = podQuota["qpsQuota"].(int64) - int64(quotaStep)
											var podQuotaMsg = map[string]interface{}{"podName": podQuota["podName"], "qpsQuota": qpsQuota, "clusterName": podQuota["clusterName"]}
											klog.Info(podQuotaMsg)
											quotaPodQuota[i] = podQuotaMsg
											if quotaUsed >= quotaStep {
												quotalabels["quota.cluster.pml.com.cn/quotaUsed"] = strconv.Itoa(quotaUsed - quotaStep)
												quotalabels["quota.cluster.pml.com.cn/quotaRemain"] = strconv.Itoa(quotaRemain + quotaStep)
												quotalabels["quota.cluster.pml.com.cn/quotaRequire"] = strconv.Itoa(quotaRequire - quotaStep)
											}
											podinfo, _ := lib.K8sClient.CoreV1().Pods(quotaController.NamespaceQuota).Get(context.TODO(), podQuota["podName"].(string), metaV1.GetOptions{})
											quotaController.podQpsQuotaHpaUpdate(podinfo, podQuotaMsg["qpsQuota"].(int64), int64(quotaStep))
										}
										klog.Info(quotaPodQuota)
										if err := unstructured.SetNestedSlice(resultQuota.Object, quotaPodQuota, "spec", "podQpsQuota"); err != nil {
											klog.Error(err)
										}
										if err := unstructured.SetNestedStringMap(resultQuota.Object, quotalabels, "metadata", "labels"); err != nil {
											klog.Error(err)
										}
										resultQuota = quotaController.quotaStateManage(resultQuota, quotalabels)
									}
								}
							}
						} else {
							// **********2.2************
							// 非最后一个pod，不存在foundmember的控制场景
							for i, podQuotaInfo := range quotaPodQuota {
								podQuota, _ := podQuotaInfo.(map[string]interface{})
								if quotaController.NameOfPod == podQuota["podName"] {
									// del podQuotaInfo
									_, ok := podQuota["qpsQuota"]
									if ok {
										if podQuota["qpsQuota"].(int64) >= int64(quotaStep) {
											var qpsQuota = podQuota["qpsQuota"].(int64) - int64(quotaStep)
											var podQuotaMsg = map[string]interface{}{"podName": podQuota["podName"], "qpsQuota": qpsQuota, "clusterName": podQuota["clusterName"]}
											klog.Info(podQuotaMsg)
											quotaPodQuota[i] = podQuotaMsg
											quotalabels["quota.cluster.pml.com.cn/quotaRequire"] = strconv.Itoa(quotaRequire - quotaStep)
											// TODO S3 待优化：遍历本地是否存在需要扩quota的pod，将当前还回的pod quota直接应用于需要扩quota的pod
											quotalabels["quota.cluster.pml.com.cn/quotaRemain"] = strconv.Itoa(quotaRemain + quotaStep)
											quotalabels["quota.cluster.pml.com.cn/quotaUsed"] = strconv.Itoa(quotaUsed - quotaStep)
											// quotalabels["quota.cluster.pml.com.cn/quotaRemain"] = strconv.Itoa(quotaRemain + quotaStep)
											// quotalabels["quota.cluster.pml.com.cn/quotaUsed"] = strconv.Itoa(quotaUsed - quotaStep)

											podinfo, _ := lib.K8sClient.CoreV1().Pods(quotaController.NamespaceQuota).Get(context.TODO(), podQuota["podName"].(string), metaV1.GetOptions{})
											quotaController.podQpsQuotaHpaUpdate(podinfo, podQuotaMsg["qpsQuota"].(int64), int64(quotaStep))
										}
									}
									klog.Info(quotaPodQuota)
									if err := unstructured.SetNestedSlice(resultQuota.Object, quotaPodQuota, "spec", "podQpsQuota"); err != nil {
										klog.Error(err)
									}
									if err := unstructured.SetNestedStringMap(resultQuota.Object, quotalabels, "metadata", "labels"); err != nil {
										klog.Error(err)
									}
									resultQuota = quotaController.quotaStateManage(resultQuota, quotalabels)
								}
							}
						}
						_, updateQuotaErr := lib.DynamicClient.Resource(quotaGvr).Namespace(quotaController.NamespaceQuota).Update(context.TODO(), resultQuota, metaV1.UpdateOptions{})
						if updateQuotaErr != nil {
							klog.Info(updateQuotaErr)
						}
						return true
					}
				}
			}
			klog.Infof("after pod qps quota return: quota name is: %s", quota.Name)
			klog.Infof("after pod qps quota return: quota of all pod is: %s", quota.Spec.PodQpsQuota)
			klog.Infof("after pod qps quota return: quota of serverless is : %s ", quota.Labels)
			klog.Infof("after pod qps quota return: quota of serverless %s  remain is : %s ", quota.Name, quota.Labels["quota.cluster.pml.com.cn/quotaRemain"])

			fmt.Printf("pod qps quota  scale down done...: %s, namespace: %s\n", quota.Name, quota.Namespace)
		} else {
			klog.Infof("获取锁失败，锁现在在 %+v 手中", who)
		}
		return true
	}
}

func (quotaController *Quota) podQpsQuotaHpaUpdate(podinfo *v1.Pod, qpsQuota int64, quotaStep int64) bool {
	// hpaWebUrl := viper.GetString("serverless-config.qpsQuotaUrl")
	var action = "update"
	//online
	if err := quotaController.qpsQuotaHpaUpdate(*podinfo, lib.HpaWebURL, action, qpsQuota, quotaStep); err != nil {
		klog.Error(err)
	}
	//debug
	//if err := quotaController.qpsQuotaHpaUpdate(*podinfo, "http://192.168.33.11:32000/serverless_qps_quota_hpa", action, qpsQuota, quotaStep); err != nil {
	//	klog.Error(err)
	//}

	return true
}

func (quotaController *Quota) lastPodOfServerless(quotaPodQuota []interface{}) bool {
	var lastPod int
	var checkLastPodNumber int = 2
	for _, podQuotaInfo := range quotaPodQuota {
		_, ok := podQuotaInfo.(map[string]interface{})["qpsQuota"]
		if ok {
			podQuota, _ := podQuotaInfo.(map[string]interface{})
			if podQuota["qpsQuota"].(int64) > 0 {
				klog.Infof("pod qps quota info is : %s", podQuota)
				lastPod += 1
			}
		}
	}
	klog.Infof("last pod judge:%s", lastPod)
	return lastPod < checkLastPodNumber
}

func (quotaController *Quota) quotaStateManage(resultQuota *unstructured.Unstructured, quotalabels map[string]string) *unstructured.Unstructured {
	klog.Infof("start manage state of serverless quota while replica hpa ")
	childClusterState, foundchildClusterState, err := unstructured.NestedSlice(resultQuota.Object, "spec", "childClusterState")
	if err != nil || !foundchildClusterState || len(childClusterState) == 0 {
		klog.Errorf("quota childClusterState  not found or error in spec: %v", err)
	}
	quotaRemain, _ := strconv.Atoi(quotalabels["quota.cluster.pml.com.cn/quotaRemain"])
	quotaLocal, _ := strconv.Atoi(quotalabels["quota.cluster.pml.com.cn/quota"])
	quotaRequire, _ := strconv.Atoi(quotalabels["quota.cluster.pml.com.cn/quotaRequire"])
	for _, childClusterStateMsg := range childClusterState {
		childClusterStateMsg, _ := childClusterStateMsg.(map[string]interface{})
		childClusterStateMsg["quota"] = int64(quotaLocal)
		childClusterStateMsg["quotaRequire"] = int64(quotaRequire)
		childClusterStateMsg["quotaRemain"] = int64(quotaRemain)
		switch {
		case quotaLocal == quotaRequire && quotaRemain == 0:
			{
				childClusterStateMsg["clusterState"] = steady
			}
		case quotaLocal < quotaRequire:
			{
				childClusterStateMsg["clusterState"] = requireQ
			}
		case quotaLocal > quotaRequire:
			{
				childClusterStateMsg["clusterState"] = returnQ
			}
		}
		//if quotaLocal == quotaRequire && quotaRemain == 0 {
		//	childClusterStateMsg["clusterState"] = steady
		//} else if quotaLocal < quotaRequire {
		//	childClusterStateMsg["clusterState"] = requireQ
		//} else if quotaLocal > quotaRequire {
		//	childClusterStateMsg["clusterState"] = returnQ
		//}
		// childClusterState = append(childClusterState[0:i-1], childClusterState[i:]...)
		klog.Infof("end manage state of serverless quota:  state is: %s ", childClusterStateMsg["clusterState"])
	}
	if err := unstructured.SetNestedSlice(resultQuota.Object, childClusterState, "spec", "childClusterState"); err != nil {
		klog.Error(err)
	}
	return resultQuota
}
