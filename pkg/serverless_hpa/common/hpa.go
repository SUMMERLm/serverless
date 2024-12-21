package common

import (
	"context"
	"errors"
	"os"
	"reflect"
	"strconv"
	"time"

	"github.com/SUMMERLm/serverless/pkg/comm"
	"github.com/SUMMERLm/serverless/pkg/etcd_lock"
	"github.com/SUMMERLm/serverless/pkg/serverless_hpa/lib"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/util/retry"
	"k8s.io/klog/v2"

	quotaV1 "github.com/SUMMERLm/quota/api/v1"
	v1 "k8s.io/api/core/v1"
	metaV1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type Hpa struct {
	NameHpa           string
	NamespaceHpa      string
	NameSpaceQpsQuota string
}

const (
	steady   = "steady"
	requireQ = "require"
	returnQ  = "return"
)

var timDuration int = 5

// var hpa Hpa
var option = etcd_lock.Option{
	ConnectionTimeout: time.Duration(timDuration) * time.Second,
	Prefix:            "ServerlessQuotaLocker:",
	Debug:             false,
}

func (hpa *Hpa) HpaScaleUp() bool {
	// quota获取并确认是否有配额，有则进行扩容，无则申请quota
	// 锁住quota，减少remain并扩容，扩容结束后释放锁
	// 重复申请扩容pod的问题：判断*result.Spec.Replicas > result.Status.AvailableReplicas则不扩容，等待原有扩容完成，才可以进行新的扩容

	// 锁优化，判断当前是否正在扩容，如果是，则直接返回
	quotaGvr := schema.GroupVersionResource{Group: "serverless.pml.com.cn", Version: "v1", Resource: "quotas"}
	unStructObj, err := lib.DynamicClient.Resource(quotaGvr).Namespace(hpa.NamespaceHpa).Get(context.TODO(), hpa.NameHpa, metaV1.GetOptions{})
	if err != nil {
		klog.Error(err)
	}
	// podList := &apiv1.PodList{}
	quota := &quotaV1.Quota{}
	if err = runtime.DefaultUnstructuredConverter.FromUnstructured(unStructObj.UnstructuredContent(), quota); err != nil {
		klog.Error(err)
	}
	deployScale := quota.Labels["quota.cluster.pml.com.cn/deployScale"]
	if deployScale == comm.True {
		klog.Infof("In Scaling UP, Nothing To do")
		return true
	}
	deploymentsClient := lib.K8sClient.AppsV1().Deployments(hpa.NamespaceHpa)
	klog.Infof("Scale up serverless begin... : %s, namespace: %s\n", hpa.NameHpa, hpa.NamespaceHpa)
	//result, getErr := deploymentsClient.Get(context.TODO(), hpa.NameHpa, metaV1.GetOptions{})
	//if getErr != nil {
	//	klog.Errorf("Failed to get latest version of Deployment: %v", getErr)
	//}
	quotaType, errLabelType := quota.Labels["quota.cluster.pml.com.cn/type"]
	if !errLabelType {
		return false
	}
	klog.Infof("quota type of serverless is: %s", quotaType)

	klog.Infof("quota name is: %s", quota.Name)
	klog.Infof("quota of all pod is: %s", quota.Spec.PodQpsQuota)
	klog.Infof("quota of serverless is : %s ", quota.Labels)
	klog.Infof("quota of serverless %s  remain is : %s ", hpa.NameHpa, quota.Labels["quota.cluster.pml.com.cn/quotaRemain"])
	quotaRemain, errQuotaRemainLabel := strconv.Atoi(quota.Labels["quota.cluster.pml.com.cn/quotaRemain"])
	if errQuotaRemainLabel != nil {
		klog.Error(errQuotaRemainLabel)
		return false
	}
	quotaStep, errQuotaStepLabel := strconv.Atoi(quota.Labels["quota.cluster.pml.com.cn/quotaStep"])
	if errQuotaStepLabel != nil {
		klog.Error(errQuotaStepLabel)
		return false
	}
	quotaUsed, errQuotaUsedLabel := strconv.Atoi(quota.Labels["quota.cluster.pml.com.cn/quotaUsed"])
	if errQuotaUsedLabel != nil {
		klog.Error(errQuotaUsedLabel)
		return false
	}
	quotaLocal, errQuotaLabel := strconv.Atoi(quota.Labels["quota.cluster.pml.com.cn/quota"])
	if errQuotaLabel != nil {
		klog.Error(errQuotaLabel)
		return false
	}
	quotaRequire, errQuotaRequireLabel := strconv.Atoi(quota.Labels["quota.cluster.pml.com.cn/quotaRequire"])
	if errQuotaRequireLabel != nil {
		klog.Error(errQuotaRequireLabel)
		return false
	}
	clusterName := quota.Spec.LocalName
	// 上线需要还原，只有新扩的pod可用才进行下一次扩容
	//if *result.Spec.Replicas == result.Status.AvailableReplicas {
	if locker, err := etcd_lock.New(lib.EtcdEndpointQuota, option); err != nil {
		klog.Errorf("创建锁失败：%+v", err)
	} else if who, ok := locker.Acquire(hpa.NameHpa + hpa.NamespaceHpa); ok {
		defer func() {
			err = locker.Release()
			if err != nil {
				klog.Error(err)
			} else {
				klog.Infof("defer release done")
			}
		}()
		//  抢到锁后执行业务逻辑，没有抢到则退出
		klog.Infof("进程 %+v 持有锁 %+v 正在处理任务中...", os.Getpid(), locker.GetId())
		// TODO quot类型识别测试
		if quotaType == comm.ServerlessTypeQPS || quotaType == comm.ServerlessTypeReplica {
			if quotaRemain >= quotaStep {
				klog.Infof("qouta remain is enough ,start scale the serverless ; %s", hpa.NameHpa)
				// quota 相关值配置，quotaRequire不变，remain变小等
				// Get后，变更相关lablel 重新apply
				// quotaRemainString := (decimal.NewFromInt(int64(quotaRemain)).Sub(decimal.NewFromInt(int64(quotaStep)))).String()
				klog.Infof("Updated deployment...")
				// maxReplica := viper.GetString("hpa.max_replica")
				// upStep := viper.GetString("hpa.up_step")
				max_replica := -1
				if err != nil {
					klog.Errorf("Failed to get max_replica %v", err)
				}
				up_step := 1
				if err != nil {
					klog.Errorf("Failed to get upstep: %v", err)
				}
				// deploymentsClient := lib.K8sClient.AppsV1().Deployments(hpa.NamespaceHpa)
				klog.Infof("Scale up serverless begin... : %s, namespace: %s\n", hpa.NameHpa, hpa.NamespaceHpa)
				retryErr := retry.RetryOnConflict(retry.DefaultRetry, func() error {
					//  Retrieve the latest version of Deployment before attempting update
					//  RetryOnConflict uses exponential backoff to avoid exhausting the apiserver
					result, getErr := deploymentsClient.Get(context.TODO(), hpa.NameHpa, metaV1.GetOptions{})
					if getErr != nil {
						klog.Errorf("Failed to get latest version of Deployment: %v", getErr)
					}
					if quotaType == comm.ServerlessTypeQPS {
						if int32(max_replica) == int32(-1) {
							klog.Infof("max serverless replica is %s, this means endless\n", max_replica)
							// TODO 上线需要还原
							// 可用副本数➕1 避免集群环境问题导致的无效扩容
							//*result.Spec.Replicas = result.Status.AvailableReplicas + int32(up_step)
							// 6/10 演示，快速扩容
							*result.Spec.Replicas = result.Status.Replicas + int32(up_step)
						} else {
							klog.Infof("max serverless replica is %s\n", max_replica)
							// scale up base on the availableReplica instead of replica
							// TODO 上线需要还原
							// 可用副本数➕1 避免集群环境问题导致的无效扩容
							//*result.Spec.Replicas = result.Status.AvailableReplicas + int32(up_step)
							// 6/10 演示，快速扩容
							*result.Spec.Replicas = result.Status.Replicas + int32(up_step)
							if *result.Spec.Replicas > int32(max_replica) {
								*result.Spec.Replicas = int32(max_replica)
							}
						}
					} else if quotaType == comm.ServerlessTypeReplica {
						*result.Spec.Replicas = result.Status.Replicas + int32(quotaStep)
					}
					// *result.Spec.Replicas
					_, updateErr := deploymentsClient.Update(context.TODO(), result, metaV1.UpdateOptions{})
					if updateErr != nil {
						klog.Infof("update deploy err:%s", updateErr)
					}

					var labelselector string
					for k, v := range result.Spec.Selector.MatchLabels {
						klog.Infof(k, v)
						if k == comm.PodSLSSelect {
							labelselector = k + "=" + v
							klog.Infof(labelselector)
						}
					}
					// 扩容后进行新增pod的qps设置：在serverless update deploy处进行设置??No 扩容后立即设置，保持quota及时对齐
					options := metaV1.ListOptions{
						LabelSelector: labelselector,
					}

					//  get the pod list
					//  https:// pkg.go.dev/k8s.io/client-go@v11.0.0+incompatible/kubernetes/typed/core/v1?tab=doc#PodInterface
					time.Sleep(comm.SleepTime * time.Second)
					podList, _ := lib.K8sClient.CoreV1().Pods(hpa.NamespaceHpa).List(context.TODO(), options)

					retryQuotaErr := retry.RetryOnConflict(retry.DefaultRetry, func() error {
						//  Retrieve the latest version of Deployment before attempting update
						//  RetryOnConflict uses exponential backoff to avoid exhausting the apiserver
						resultQuota, getQuotaErr := lib.DynamicClient.Resource(quotaGvr).Namespace(hpa.NamespaceHpa).Get(context.TODO(), hpa.NameHpa, metaV1.GetOptions{})
						if getQuotaErr != nil {
							klog.Errorf("failed to get latest version of Quota: %v", getQuotaErr)
						}
						quotaLabels, foundQuota, errQuotaLabels := unstructured.NestedStringMap(resultQuota.Object, "metadata", "labels")
						klog.Info("quota.cluster.pml.com.cn/quotaRemain is:  %s", quotaLabels["quota.cluster.pml.com.cn/quotaRemain"])
						if errQuotaLabels != nil || !foundQuota || len(quotaLabels) == 0 {
							klog.Errorf("quota label  not found or error in spec: %v", err)
						}
						// quotaRemain减少
						// quotaUsed增加
						quotaLabels["quota.cluster.pml.com.cn/quotaRemain"] = strconv.Itoa(quotaRemain - quotaStep)
						quotaLabels["quota.cluster.pml.com.cn/quotaUsed"] = strconv.Itoa(quotaUsed + quotaStep)
						// 判断quotaRequire是否小于quota，小于则进行变更（增加一个分片）
						if quotaRequire < quotaLocal {
							quotaLabels["quota.cluster.pml.com.cn/quotaRequire"] = strconv.Itoa(quotaRequire + quotaStep)
						}
						quotaLabels["quota.cluster.pml.com.cn/deployScale"] = comm.False
						// 当前cluster有quota，不需要走申请，直接分配quotaRequire
						// quotalabels["quota.cluster.pml.com.cn/quotaRequire"] = strconv.Itoa(quotaRequire - quotaStep)
						if err = unstructured.SetNestedStringMap(resultQuota.Object, quotaLabels, "metadata", "labels"); err != nil {
							klog.Error(err)
						}
						if quotaType == comm.ServerlessTypeQPS {
							// qps类型需要进一步控制pod的qpshpa
							//  quotaInit Start; 扩容情况下最少为1个实例，第一个实例相关信息在0-1触发以及元老实例时加入
							quotaPodQuota, foundQpsQuota, errPodQpsQuota := unstructured.NestedSlice(resultQuota.Object, "spec", "podQpsQuota")
							klog.Info("quotaPodQuota is:  %s", quotaPodQuota)
							if errPodQpsQuota != nil || !foundQpsQuota || len(quotaPodQuota) == 0 {
								klog.Errorf("quota label not found or error in spec: %v", errPodQpsQuota)
							}
							qpsRealOfPodQuota, foundQpsRealQuota, errPodQpsReal := unstructured.NestedSlice(resultQuota.Object, "spec", "podQpsReal")
							if errPodQpsReal != nil || !foundQpsRealQuota || len(qpsRealOfPodQuota) == 0 {
								klog.Errorf("quota label  not found or error in spec: %v", errPodQpsReal)
							}

							qpsAlertOfPodQuota, foundQpsAlertQuota, errPodQpsIncreaseOrDecrease := unstructured.NestedSlice(resultQuota.Object, "spec", "podQpsIncreaseOrDecrease")
							if errPodQpsIncreaseOrDecrease != nil || !foundQpsAlertQuota || len(qpsAlertOfPodQuota) == 0 {
								klog.Errorf("quota label  not found or error in spec: %v", errPodQpsIncreaseOrDecrease)
							}
							for _, podInfo := range podList.Items {
								klog.Infof("pods-name=%v\n", podInfo.Name)
								klog.Infof("pods-status=%v\n", podInfo.Status.Phase)
								klog.Infof("pods-condition=%v\n", podInfo.Status.Conditions)
								// quota更新，加入新增pod的相关qpsQuota设置
								var in = false
								for _, podQuotaInfo := range quotaPodQuota {
									podQuota, _ := podQuotaInfo.(map[string]interface{})
									if podInfo.Name == podQuota["podName"] {
										in = true
									}
								}
								if !in {
									// add podQuotaInfo
									var podQuotaMsg = map[string]interface{}{"podName": podInfo.Name, "qpsQuota": int64(quotaStep), "clusterName": clusterName}
									var podQuotaRealMsg = map[string]interface{}{"podName": podInfo.Name, "qpsReal": int64(0)}
									var podQuotaAlertMsg = map[string]interface{}{"podName": podInfo.Name, "qpsIncreaseOrDecrease": int64(0)}
									quotaPodQuota = append(quotaPodQuota, podQuotaMsg)
									qpsRealOfPodQuota = append(qpsRealOfPodQuota, podQuotaRealMsg)
									qpsAlertOfPodQuota = append(qpsAlertOfPodQuota, podQuotaAlertMsg)
									// sub alert注册
									// hpaWebUrl := viper.GetString("serverless-config.qpsQuotaUrl")

									var action = "init"
									//online
									err = hpa.qpsQuotaHpaInit(podInfo, lib.HpaWebURL, action, podQuotaMsg["qpsQuota"].(int64))
									//debug
									//err = hpa.qpsQuotaHpaInit(podInfo, "http://192.168.33.11:32000/serverless_qps_quota_hpa", action, podQuotaMsg["qpsQuota"].(int64))
									if err != nil {
										klog.Error(err)
									}
									//  apply change
									if err = unstructured.SetNestedSlice(resultQuota.Object, quotaPodQuota, "spec", "podQpsQuota"); err != nil {
										klog.Error(err)
									}
									if err = unstructured.SetNestedSlice(resultQuota.Object, qpsRealOfPodQuota, "spec", "podQpsReal"); err != nil {
										klog.Error(err)
									}
									if err = unstructured.SetNestedSlice(resultQuota.Object, qpsAlertOfPodQuota, "spec", "podQpsIncreaseOrDecrease"); err != nil {
										klog.Error(err)
									}
								}
							}
						}
						klog.Infof("After update quota info is : %s", resultQuota)
						resultQuota = hpa.quotaStateManageOfHpa(resultQuota, quotaLabels)
						_, updateQuotaErr := lib.DynamicClient.Resource(quotaGvr).Namespace(hpa.NamespaceHpa).Update(context.TODO(), resultQuota, metaV1.UpdateOptions{})
						return updateQuotaErr
					})
					if retryQuotaErr != nil {
						klog.Errorf("update failed: %v", retryQuotaErr)
					}
					// 聚合pod qps quota：serverless update中进行
					// qps hpa 注册在serverless服务的update中进行
					return updateErr
				})
				if retryErr != nil {
					klog.Errorf("scale up  failed: %v", retryErr)
					return false
				}
				klog.Infof("scale up serverless done...: %s, namespace: %s\n", hpa.NameHpa, hpa.NamespaceHpa)
				return true
			} else {
				// 申请quota
				klog.Infof("quota remain is not enough ,start require the quota from field: %s", hpa.NameHpa)
				retryQuotaErr := retry.RetryOnConflict(retry.DefaultRetry, func() error {
					//  Retrieve the latest version of Deployment before attempting update
					//  RetryOnConflict uses exponential backoff to avoid exhausting the apiserver
					resultQuota, getQuotaErr := lib.DynamicClient.Resource(quotaGvr).Namespace(hpa.NamespaceHpa).Get(context.TODO(), hpa.NameHpa, metaV1.GetOptions{})
					if getQuotaErr != nil {
						klog.Errorf("failed to get latest version of Quota: %v", getQuotaErr)
					}
					quotalabels, foundQuota, _ := unstructured.NestedStringMap(resultQuota.Object, "metadata", "labels")
					klog.Info("quota.cluster.pml.com.cn/quotaRemain is:  %s", quotalabels["quota.cluster.pml.com.cn/quotaRemain"])
					if err != nil || !foundQuota || len(quotalabels) == 0 {
						klog.Errorf("quota label  not found or error in spec: %v", err)
					}
					// quotaRequire增加，重复最多增加1个
					// TODO 此处变更同qpsQuotaIncrease处申请quota变更项一致：quotaRequire
					//     可优化：定制该信息，并对齐quotaIncrease
					if quotaRequire == quotaLocal {
						quotalabels["quota.cluster.pml.com.cn/quotaRequire"] = strconv.Itoa(quotaLocal + quotaStep)
					} else {
						quotalabels["quota.cluster.pml.com.cn/quotaRequire"] = strconv.Itoa(quotaRequire)
					}
					quotalabels["quota.cluster.pml.com.cn/deployScale"] = comm.True
					if err = unstructured.SetNestedStringMap(resultQuota.Object, quotalabels, "metadata", "labels"); err != nil {
						klog.Error(err)
					}
					resultQuota = hpa.quotaStateManageOfHpa(resultQuota, quotalabels)

					_, updateQuotaErr := lib.DynamicClient.Resource(quotaGvr).Namespace(hpa.NamespaceHpa).Update(context.TODO(), resultQuota, metaV1.UpdateOptions{})
					return updateQuotaErr
				})
				if retryQuotaErr != nil {
					klog.Errorf("update failed: %v", retryQuotaErr)
				}
				return true
			}
		} else if quotaType == comm.ServerlessTypeNoLimit {
			klog.Infof("qouta remain is enough ,start scale the serverless ; %s", hpa.NameHpa)
			// quota 相关值配置，quotaRequire不变，remain变小等
			// Get后，变更相关lablel 重新apply
			// quotaRemainString := (decimal.NewFromInt(int64(quotaRemain)).Sub(decimal.NewFromInt(int64(quotaStep)))).String()

			klog.Infof("Updated deployment...")

			// maxReplica := viper.GetString("hpa.max_replica")
			// upStep := viper.GetString("hpa.up_step")
			max_replica := -1
			if err != nil {
				klog.Errorf("Failed to get max_replica %v", err)
			}
			up_step := 1
			if err != nil {
				klog.Errorf("Failed to get upstep: %v", err)
			}
			// deploymentsClient := lib.K8sClient.AppsV1().Deployments(hpa.NamespaceHpa)
			klog.Infof("Scale up serverless begin... : %s, namespace: %s\n", hpa.NameHpa, hpa.NamespaceHpa)
			retryErr := retry.RetryOnConflict(retry.DefaultRetry, func() error {
				//  Retrieve the latest version of Deployment before attempting update
				//  RetryOnConflict uses exponential backoff to avoid exhausting the apiserver
				result, getErr := deploymentsClient.Get(context.TODO(), hpa.NameHpa, metaV1.GetOptions{})
				if getErr != nil {
					klog.Errorf("Failed to get latest version of Deployment: %v", getErr)
				}

				// TODO 上线需要还原
				if max_replica == -1 {
					// 可用副本数➕1 避免集群环境问题导致的无效扩容
					//*result.Spec.Replicas = result.Status.AvailableReplicas + int32(up_step)
					// 6/10 演示，快速扩容
					*result.Spec.Replicas = result.Status.Replicas + int32(up_step)
				} else {
					// 可用副本数➕1 避免集群环境问题导致的无效扩容
					//*result.Spec.Replicas = result.Status.AvailableReplicas + int32(up_step)
					// 6/10 演示，快速扩容
					*result.Spec.Replicas = result.Status.Replicas + int32(up_step)
					if *result.Spec.Replicas > int32(max_replica) {
						*result.Spec.Replicas = int32(max_replica)
					}
				}

				// *result.Spec.Replicas
				_, updateErr := deploymentsClient.Update(context.TODO(), result, metaV1.UpdateOptions{})
				//  get the pod list
				//  https:// pkg.go.dev/k8s.io/client-go@v11.0.0+incompatible/kubernetes/typed/core/v1?tab=doc#PodInterface

				return updateErr
			})
			if retryErr != nil {
				klog.Errorf("scale up  failed: %v", retryErr)
				return false
			}
			klog.Infof("scale up serverless done...: %s, namespace: %s\n", hpa.NameHpa, hpa.NamespaceHpa)
			return true
		}
		klog.Infof("进程 %+v 的任务处理完了", os.Getpid())
	} else {
		klog.Infof("获取锁失败，锁现在在 %+v 手中", who)
	}
	return true
	//}
	//klog.Infof("some pod of serverless %s is init, do not need to scale", hpa.NameHpa)
	//return true
}

// 元老实例特殊处理，最后一个实例的quota控制，最小化quota为1个step，如果不是，强制设置
func (hpa *Hpa) HpaScaleDown() bool {
	klog.Infof("Scale down serverless begin...: %s, namespace: %s\n", hpa.NameHpa, hpa.NamespaceHpa)
	quotaGvr := schema.GroupVersionResource{Group: "serverless.pml.com.cn", Version: "v1", Resource: "quotas"}
	unStructObj, err := lib.DynamicClient.Resource(quotaGvr).Namespace(hpa.NamespaceHpa).Get(context.TODO(), hpa.NameHpa, metaV1.GetOptions{})
	if err != nil {
		klog.Error(err)
	}
	quota := &quotaV1.Quota{}
	if err = runtime.DefaultUnstructuredConverter.FromUnstructured(unStructObj.UnstructuredContent(), quota); err != nil {
		klog.Error(err)
	}
	deployDown := quota.Labels["quota.cluster.pml.com.cn/deployDown"]
	zeroDownTry, _ := strconv.Atoi(quota.Labels["quota.cluster.pml.com.cn/zeroDownTry"])
	resultQuota, getQuotaErr := lib.DynamicClient.Resource(quotaGvr).Namespace(hpa.NamespaceHpa).Get(context.TODO(), hpa.NameHpa, metaV1.GetOptions{})
	if getQuotaErr != nil {
		klog.Errorf("failed to get latest version of Quota: %v", getQuotaErr)
	}
	quotalabels, foundQuota, err := unstructured.NestedStringMap(resultQuota.Object, "metadata", "labels")
	// 非元老实例最后一个pod缩容控制
	downStep := 1
	if err != nil {
		klog.Errorf("Failed to get down_step %v", err)
	}
	deploymentsClient := lib.K8sClient.AppsV1().Deployments(hpa.NamespaceHpa)
	quotaType, errTypeLabel := quota.Labels["quota.cluster.pml.com.cn/type"]
	if !errTypeLabel {
		klog.Error(errTypeLabel)
	}
	quotaStep, errQuotaStepLabel := strconv.Atoi(quota.Labels["quota.cluster.pml.com.cn/quotaStep"])
	if errQuotaStepLabel != nil {
		klog.Error(errQuotaStepLabel)
	}
	if quotaType == comm.ServerlessTypeReplica {
		downStep = quotaStep
	}
	klog.Infof("quota name is: %s", quota.Name)
	klog.Infof("quota of all pod is: %s", quota.Spec.PodQpsQuota)
	klog.Infof("quota of serverless is : %s ", quota.Labels)
	klog.Infof("quota of serverless %s  remain is : %s ", hpa.NameHpa, quota.Labels["quota.cluster.pml.com.cn/quotaRemain"])

	klog.Info("quota.cluster.pml.com.cn/quotaRemain is:  %s", quotalabels["quota.cluster.pml.com.cn/quotaRemain"])
	if err != nil || !foundQuota || len(quotalabels) == 0 {
		klog.Errorf("quota label  not found or error in spec: %v", err)
	}
	// 元老实例判断：是 最小化为1  否 可以缩为0
	// 元老实例特例，直接返回，不需要加锁控制
	foundingMember, _ := strconv.ParseBool(quota.Labels["quota.cluster.pml.com.cn/foundingMember"])
	var lastFoundingMember bool = false
	result, getErr := deploymentsClient.Get(context.TODO(), hpa.NameHpa, metaV1.GetOptions{})
	if getErr != nil {
		klog.Errorf("Failed to get latest version of Deployment: %v", getErr)
	}
	if !foundingMember && zeroDownTry > 0 && *result.Spec.Replicas == int32(1) {
		if locker, err := etcd_lock.New(lib.EtcdEndpointQuota, option); err != nil {
			klog.Errorf("创建锁失败：%+v", err)
		} else if who, ok := locker.Acquire(hpa.NameHpa + hpa.NamespaceHpa); ok {
			defer func() {
				err = locker.Release()
				if err != nil {
					klog.Error(err)
				} else {
					klog.Infof("defer release done")
				}
			}()
			// 设置缩容状态管理
			zeroDownTry -= 1
			quotalabels["quota.cluster.pml.com.cn/zeroDownTry"] = strconv.Itoa(zeroDownTry)
			klog.Infof("1-0 scale down control by 3 times try，still have %s times", zeroDownTry)
			if err := unstructured.SetNestedStringMap(resultQuota.Object, quotalabels, "metadata", "labels"); err != nil {
				klog.Error(err)
			}
			_, updateQuotaErr := lib.DynamicClient.Resource(quotaGvr).Namespace(hpa.NamespaceHpa).Update(context.TODO(), resultQuota, metaV1.UpdateOptions{})
			if updateQuotaErr != nil {
				klog.Info(updateQuotaErr)
			}
			return true
		} else {
			klog.Infof("获取锁失败，锁现在在 %+v 手中", who)
		}
		return true
	}
	// 非元老实例缩容，直接返回
	if !foundingMember && *result.Spec.Replicas == int32(0) {
		if locker, err := etcd_lock.New(lib.EtcdEndpointQuota, option); err != nil {
			klog.Errorf("创建锁失败：%+v", err)
		} else if who, ok := locker.Acquire(hpa.NameHpa + hpa.NamespaceHpa); ok {
			defer func() {
				err = locker.Release()
				if err != nil {
					klog.Error(err)
				} else {
					klog.Infof("defer release done")
				}
			}()
			// 设置缩容状态管理
			if deployDown == comm.True {
				quotalabels["quota.cluster.pml.com.cn/deployDown"] = comm.False
				klog.Infof("not a founding member and replica is 0，set the state of deployDown： false")
				if err := unstructured.SetNestedStringMap(resultQuota.Object, quotalabels, "metadata", "labels"); err != nil {
					klog.Error(err)
				}
				_, updateQuotaErr := lib.DynamicClient.Resource(quotaGvr).Namespace(hpa.NamespaceHpa).Update(context.TODO(), resultQuota, metaV1.UpdateOptions{})
				if updateQuotaErr != nil {
					klog.Info(updateQuotaErr)
				}
				return true
			}
			return true
		} else {
			klog.Infof("获取锁失败，锁现在在 %+v 手中", who)
		}
		return true
	}
	if deployDown == comm.True {
		klog.Infof("In Scaling Down, Nothing To do")
		return true
	}
	// downStep := viper.GetString("hpa.down_step")
	if quotalabels["quota.cluster.pml.com.cn/type"] == comm.ServerlessTypeQPS {
		if foundingMember && *result.Spec.Replicas == int32(1) && len(quota.Spec.PodQpsQuota) == 1 {
			return true
		}
	} else {
		if foundingMember && *result.Spec.Replicas == int32(1) {
			return true
		}
	}
	// 最小化场景下不再进行锁获取及其他操作，锁粒度细化
	if locker, err := etcd_lock.New(lib.EtcdEndpointQuota, option); err != nil {
		klog.Errorf("创建锁失败：%+v", err)
	} else if who, ok := locker.Acquire(hpa.NameHpa + hpa.NamespaceHpa); ok {
		defer func() {
			err = locker.Release()
			if err != nil {
				klog.Error(err)
			} else {
				klog.Infof("defer release done")
			}
		}()
		retryErr := retry.RetryOnConflict(retry.DefaultRetry, func() error {
			// 设置缩容状态管理
			quotalabels["quota.cluster.pml.com.cn/deployDown"] = comm.True
			klog.Infof("scale down state manage begin: True")
			if err := unstructured.SetNestedStringMap(resultQuota.Object, quotalabels, "metadata", "labels"); err != nil {
				klog.Error(err)
			}
			_, updateQuotaErr := lib.DynamicClient.Resource(quotaGvr).Namespace(hpa.NamespaceHpa).Update(context.TODO(), resultQuota, metaV1.UpdateOptions{})
			if updateQuotaErr != nil {
				klog.Info(updateQuotaErr)
			}
			defer func() {
				resultQuota, getQuotaErr = lib.DynamicClient.Resource(quotaGvr).Namespace(hpa.NamespaceHpa).Get(context.TODO(), hpa.NameHpa, metaV1.GetOptions{})
				if getQuotaErr != nil {
					klog.Errorf("failed to get latest version of Quota: %v", getQuotaErr)
				}
				quotalabels["quota.cluster.pml.com.cn/deployDown"] = comm.False
				if err := unstructured.SetNestedStringMap(resultQuota.Object, quotalabels, "metadata", "labels"); err != nil {
					klog.Error(err)
				}
				_, updateQuotaErr = lib.DynamicClient.Resource(quotaGvr).Namespace(hpa.NamespaceHpa).Update(context.TODO(), resultQuota, metaV1.UpdateOptions{})
				if updateQuotaErr != nil {
					klog.Info(updateQuotaErr)
				}
				klog.Infof("scale down state manage end: False")
			}()
			resultQuota, getQuotaErr = lib.DynamicClient.Resource(quotaGvr).Namespace(hpa.NamespaceHpa).Get(context.TODO(), hpa.NameHpa, metaV1.GetOptions{})
			if getQuotaErr != nil {
				klog.Errorf("failed to get latest version of Quota: %v", getQuotaErr)
			}
			//  抢到锁后执行业务逻辑，没有抢到则退出
			klog.Infof("进程 %+v 持有锁 %+v 正在处理任务中...", os.Getpid(), locker.GetId())
			// var beforeScaleDownReplica int32
			retryErr := retry.RetryOnConflict(retry.DefaultRetry, func() error {
				//  Retrieve the latest version of Deployment before attempting update
				//  RetryOnConflict uses exponential backoff to avoid exhausting the apiserver

				// scale down base on the availableReplica instead of replica
				// beforeScaleDownReplica = result.Status.Replicas
				if result.Status.Replicas >= int32(downStep) {
					*result.Spec.Replicas = result.Status.Replicas - int32(downStep)
					if foundingMember && *result.Spec.Replicas <= 1 {
						lastFoundingMember = true
					}
					if foundingMember && *result.Spec.Replicas <= 0 {
						*result.Spec.Replicas = 1
					}
					if !foundingMember && *result.Spec.Replicas < 0 {
						*result.Spec.Replicas = 0
					}
				}
				// 元老实例最小化为1控制
				klog.Info(*result.Spec.Replicas)
				_, updateErr := deploymentsClient.Update(context.TODO(), result, metaV1.UpdateOptions{})
				// // 优化项，在0-1触发完成后自动删除了该属性信息。
				// // 缩容至0，删除0-1扩容标识项
				// if *result.Spec.Replicas == 0 {
				// 	childAlert, _, err := unstructured.NestedSlice(resultQuota.Object, "spec", "childAlert")
				// 	if err != nil {
				// 		klog.Errorf("quota label  not found or error in spec: %v", err)
				// 	}
				// 	for i, alert := range childAlert {
				// 		alertMsg, _ := alert.(map[string]interface{})
				// 		// 匹配上，进行处理
				// 		if alertMsg["clusterName"] == quota.Spec.LocalName {
				// 			// 被删除的相关quota信息清理
				// 			childAlert = append(childAlert[:i], childAlert[i+1:]...)
				// 			klog.Info(childAlert)
				// 			klog.Info(childAlert)
				// 			childAlert = nilElementDrop(childAlert)
				// 			if err := unstructured.SetNestedSlice(resultQuota.Object, childAlert, "spec", "childAlert"); err != nil {
				// 				klog.Error(err)
				// 			}
				// 			// deleteQuota再createQuota存在导致上级quota删除的问题，需要应用apply方法，apply版本问题需要解决
				// 			// 测试验证
				// 			_, updateQuotaErr := lib.DynamicClient.Resource(quotaGvr).Namespace(hpa.NamespaceHpa).Update(context.TODO(), resultQuota, metav1.UpdateOptions{})
				// 			if updateQuotaErr != nil {
				// 				klog.Info(updateQuotaErr)
				// 			}
				// 			//
				// 			// DeleteQuotaErr := lib.DynamicClient.Resource(quotaGvr).Namespace(hpa.NamespaceHpa).Delete(context.TODO(), hpa.NameHpa, metav1.DeleteOptions{})
				// 			// if DeleteQuotaErr != nil {
				// 			// 	klog.Info(DeleteQuotaErr)
				// 			// }
				// 			// time.Sleep(5 * time.Second)
				// 			// resultQuota.SetResourceVersion("")
				// 			// klog.Info(resultQuota)
				// 			// _, applyQuotaErr := lib.DynamicClient.Resource(quotaGvr).Namespace(hpa.NamespaceHpa).Create(context.TODO(), resultQuota, metav1.CreateOptions{})
				// 			//
				// 			// if applyQuotaErr != nil {
				// 			// 	klog.Info(applyQuotaErr)
				// 			// }
				//
				// 		}
				// 	}
				// }
				return updateErr
			})
			if retryErr != nil {
				klog.Errorf("scale down failed: %v", retryErr)
				return retryErr
			}
			klog.Infof("Scale down serverless done...: %s, namespace: %s\n", hpa.NameHpa, hpa.NamespaceHpa)
			// 缩容后对比quotaCR与实际deployment下属pod name 对比，删除quotaCR里面不存在的pod qps，并按需进行quotaCR的quota 回收 label配置
			result, getErr = deploymentsClient.Get(context.TODO(), hpa.NameHpa, metaV1.GetOptions{})
			if getErr != nil {
				klog.Errorf("Failed to get latest version of Deployment: %v", getErr)
			}
			var labelselector string
			for k, v := range result.Spec.Selector.MatchLabels {
				klog.Infof(k, v)
				if k == comm.PodSLSSelect {
					labelselector = k + "=" + v
					klog.Infof(labelselector)
				}
			}

			// 扩容后进行新增pod的qps设置：在serverless update deploy处进行设置??No 扩容后立即设置，保持quota及时对齐
			options := metaV1.ListOptions{
				LabelSelector: labelselector,
			}

			//  get the pod list
			//  https:// pkg.go.dev/k8s.io/client-go@v11.0.0+incompatible/kubernetes/typed/core/v1?tab=doc#PodInterface
			// judge the Terminating pod is zero
			// TODO test for check
			switch {
			case quotaType == comm.ServerlessTypeQPS:
				{
					podList, _ := lib.K8sClient.CoreV1().Pods(hpa.NamespaceHpa).List(context.TODO(), options)
					qpsRealOfPodQuota, foundQpsRealQuota, err := unstructured.NestedSlice(resultQuota.Object, "spec", "podQpsReal")
					if err != nil || !foundQpsRealQuota || len(qpsRealOfPodQuota) == 0 {
						klog.Errorf("quota label  not found or error in spec: %v", err)
					}
					var sleepDuration int = 5
					for i := 0; i < comm.PodMsgGetTime; i++ {
						podList, _ = lib.K8sClient.CoreV1().Pods(hpa.NamespaceHpa).List(context.TODO(), options)
						if len(podList.Items) < len(qpsRealOfPodQuota) {
							break
						} else {
							time.Sleep(time.Duration(sleepDuration) * time.Second)
						}
						if i == comm.PodMsgGetTime-1 {
							err = errors.New("retry time all used")
							return err
						}
					}
					qpsAlertOfPodQuota, foundQpsAlertQuota, err := unstructured.NestedSlice(resultQuota.Object, "spec", "podQpsIncreaseOrDecrease")
					if err != nil || !foundQpsAlertQuota || len(qpsAlertOfPodQuota) == 0 {
						klog.Errorf("quota label  not found or error in spec: %v", err)
					}
					quotaPodQuota, foundQpsQuota, err := unstructured.NestedSlice(resultQuota.Object, "spec", "podQpsQuota")
					klog.Info("quotaPodQuota is:  %s", quotaPodQuota)
					if err != nil || !foundQpsQuota || len(quotaPodQuota) == 0 {
						klog.Errorf("quota label not found or error in spec: %v", err)
					} else {
						// 元老实例最小化quota控制，最后一个pod的quota相关属性不变
						for _, podQuotaInfo := range quotaPodQuota {
							podQuota, _ := podQuotaInfo.(map[string]interface{})
							// quota更新，删除缩容pod的相关qpsQuota设置
							var in = true
							var i = 0
							var podInfoMsg = v1.Pod{}
							for _, podInfo := range podList.Items {
								if podQuota["podName"] == podInfo.Name {
									i++
									podInfoMsg = podInfo
								}
							}
							if i != 1 {
								in = false
							}
							klog.Info(i)
							klog.Info(in)
							if !in {
								// 删除不存在的pod的相关podquota信息，以及不存在pod的quota回收
								klog.Info(podQuota)
								quotaReturn := podQuota["qpsQuota"]
								klog.Info(quotaReturn)

								returnNum, _ := quotaReturn.(int64)
								klog.Info(returnNum)
								quotaRemain, _ := strconv.Atoi(quota.Labels["quota.cluster.pml.com.cn/quotaRemain"])
								quotaUsed, _ := strconv.Atoi(quota.Labels["quota.cluster.pml.com.cn/quotaUsed"])
								quotaRequire, _ := strconv.Atoi(quota.Labels["quota.cluster.pml.com.cn/quotaRequire"])
								quotaStep, _ = strconv.Atoi(quota.Labels["quota.cluster.pml.com.cn/quotaStep"])
								klog.Info(quotaRemain)
								// 删除pod的相关quota回收（quotalabels相关值）
								quotaRemain += int(returnNum)
								quotaUsed -= int(returnNum)
								quotaRequire -= int(returnNum)
								klog.Info(quotaRemain)
								klog.Info("quota.cluster.pml.com.cn/quotaRemain is:  %s", quotalabels["quota.cluster.pml.com.cn/quotaRemain"])
								// quotalabels["quota.cluster.pml.com.cn/quota"] = strconv.Itoa(quota)
								quotalabels["quota.cluster.pml.com.cn/quotaRemain"] = strconv.Itoa(quotaRemain)
								quotalabels["quota.cluster.pml.com.cn/quotaUsed"] = strconv.Itoa(quotaUsed)
								quotalabels["quota.cluster.pml.com.cn/quotaRequire"] = strconv.Itoa(quotaRequire)
								var twiceQuota int = 2
								if quota.Labels["quota.cluster.pml.com.cn/deployScale"] == comm.True {
									quotalabels["quota.cluster.pml.com.cn/deployScale"] = comm.False
									quotaRequire -= twiceQuota * quotaStep
									quotalabels["quota.cluster.pml.com.cn/quotaRequire"] = strconv.Itoa(quotaRequire)
								}
								klog.Info(quota.Labels)
								if err := unstructured.SetNestedStringMap(resultQuota.Object, quotalabels, "metadata", "labels"); err != nil {
									klog.Error(err)
								}
								klog.Info(podQuota["podName"])
								// 遍历podQuota list，匹配in== false的podQuota
								for i, podQuotaInfoDel := range quotaPodQuota {
									podQuotaIn, _ := podQuotaInfoDel.(map[string]interface{})
									// 匹配上，进行处理
									if podQuotaIn["podName"] == podQuota["podName"] {
										// TODO More Test to Check
										// 被删除的相关quota信息清理
										// quotaPodQuotai := append(quotaPodQuota[:i], quotaPodQuota[i+1:]...)
										// klog.Info(quotaPodQuota)
										// klog.Info(quotaPodQuotai)
										// quotaPodQuota = nilElementDrop(quotaPodQuotai)
										quotaPodQuota = nilElementDrop(append(quotaPodQuota[:i], quotaPodQuota[i+1:]...))
										// klog.Info(quotaPodQuota)
										// qpsRealOfPodQuotai := append(qpsRealOfPodQuota[:i], qpsRealOfPodQuota[i+1:]...)
										// qpsRealOfPodQuota = nilElementDrop(qpsRealOfPodQuotai)
										qpsRealOfPodQuota = nilElementDrop(append(qpsRealOfPodQuota[:i], qpsRealOfPodQuota[i+1:]...))
										// qpsAlertOfPodQuotai := append(qpsAlertOfPodQuota[:i], qpsAlertOfPodQuota[i+1:]...)
										// qpsAlertOfPodQuota = nilElementDrop(qpsAlertOfPodQuotai)
										qpsAlertOfPodQuota = nilElementDrop(append(qpsAlertOfPodQuota[:i], qpsAlertOfPodQuota[i+1:]...))
										if err := unstructured.SetNestedSlice(resultQuota.Object, quotaPodQuota, "spec", "podQpsQuota"); err != nil {
											klog.Error(err)
										}
										if err := unstructured.SetNestedSlice(resultQuota.Object, qpsRealOfPodQuota, "spec", "podQpsReal"); err != nil {
											klog.Error(err)
										}
										if err := unstructured.SetNestedSlice(resultQuota.Object, qpsAlertOfPodQuota, "spec", "podQpsIncreaseOrDecrease"); err != nil {
											klog.Error(err)
										}
										// 最后一个元老pod特殊处理
										if lastFoundingMember {
											// 最后一个pod的quota最小化控制
											// 查看最后一个pod的qps是否为0或nil 是的话则设置最小化为1step
											klog.Info(len(quotaPodQuota))
											for i, podQuotaInfo := range quotaPodQuota {
												podQuota, _ = podQuotaInfo.(map[string]interface{})
												// _, ok := podQuota["qpsQuota"]
												if podQuota["qpsQuota"] == int64(0) {
													// 设置最后一个pod的quota为1
													podQuota["qpsQuota"] = int64(quotaStep)
													quotaPodQuota[i] = podQuota
													if err := unstructured.SetNestedSlice(resultQuota.Object, quotaPodQuota, "spec", "podQpsQuota"); err != nil {
														klog.Error(err)
													}
													quotaRemain -= quotaStep
													quotaUsed += quotaStep
													quotalabels["quota.cluster.pml.com.cn/quotaRemain"] = strconv.Itoa(quotaRemain)
													quotalabels["quota.cluster.pml.com.cn/quotaUsed"] = strconv.Itoa(quotaUsed)
													quotalabels["quota.cluster.pml.com.cn/quotaRequire"] = strconv.Itoa(quotaRequire - quotaStep)
													if err := unstructured.SetNestedStringMap(resultQuota.Object, quotalabels, "metadata", "labels"); err != nil {
														klog.Error(err)
													}
												}
											}
										}
										// 删除不相关的managed信息，无法应用server端apply，版本问题，此处先删除再创建
										// https:// kubernetes.io/zh-cn/docs/reference/using-api/server-side-apply/
										managedFields, foundMetadata, err := unstructured.NestedSlice(resultQuota.Object, "metadata", "managedFields")
										if err != nil || !foundMetadata || len(managedFields) == 0 {
											klog.Errorf("metdata  not found or error in spec: %v", err)
										}
										managedFields = managedFields[:0]
										if err = unstructured.SetNestedSlice(resultQuota.Object, managedFields, "metadata", "managedFields"); err != nil {
											klog.Error(err)
										}
										resultQuota = hpa.quotaStateManageOfHpa(resultQuota, quotalabels)
										// apply 而不是先删除再新建
										_, updateQuotaErr = lib.DynamicClient.Resource(quotaGvr).Namespace(hpa.NamespaceHpa).Update(context.TODO(), resultQuota, metaV1.UpdateOptions{})
										if updateQuotaErr != nil {
											klog.Info(updateQuotaErr)
											return updateQuotaErr
										}
										// pod sub alert回收
										var action = "delete"
										klog.Info(podInfoMsg)
										podName, errBool := podQuota["podName"]
										if !errBool {
											klog.Info(errBool)
										}
										err = hpa.qpsQuotaHpaDel(podName, action)
										if err != nil {
											klog.Error(err)
										}
									}
								}
							}
						}
					}
				}
			case quotaType == comm.ServerlessTypeReplica:
				{
					// 元老实例最小化quota控制，最后一个pod的quota相关属性不变
					quotaRemain, _ := strconv.Atoi(quota.Labels["quota.cluster.pml.com.cn/quotaRemain"])
					quotaUsed, _ := strconv.Atoi(quota.Labels["quota.cluster.pml.com.cn/quotaUsed"])
					quotaRequire, _ := strconv.Atoi(quota.Labels["quota.cluster.pml.com.cn/quotaRequire"])
					quotaStep, _ = strconv.Atoi(quota.Labels["quota.cluster.pml.com.cn/quotaStep"])
					klog.Info(quotaRemain)
					// 删除pod的相关quota回收（quotalabels相关值）
					quotaRemain += quotaStep
					quotaUsed -= quotaStep
					quotaRequire -= quotaStep
					klog.Info(quotaRemain)
					klog.Info("quota.cluster.pml.com.cn/quotaRemain is:  %s", quotalabels["quota.cluster.pml.com.cn/quotaRemain"])
					quotalabels["quota.cluster.pml.com.cn/quotaRemain"] = strconv.Itoa(quotaRemain)
					quotalabels["quota.cluster.pml.com.cn/quotaUsed"] = strconv.Itoa(quotaUsed)
					quotalabels["quota.cluster.pml.com.cn/quotaRequire"] = strconv.Itoa(quotaRequire)
					if quota.Labels["quota.cluster.pml.com.cn/deployScale"] == comm.True {
						quotalabels["quota.cluster.pml.com.cn/deployScale"] = comm.False
						// 正在扩容的情况下，还未完成，缩容，则原有扩容需要的申请分片归还
						quotaRequire -= quotaStep
						quotalabels["quota.cluster.pml.com.cn/quotaRequire"] = strconv.Itoa(quotaRequire)
					}
					klog.Info(quota.Labels)
					if err := unstructured.SetNestedStringMap(resultQuota.Object, quotalabels, "metadata", "labels"); err != nil {
						klog.Error(err)
					}
					// https:// kubernetes.io/zh-cn/docs/reference/using-api/server-side-apply/
					managedFields, foundMetadata, err := unstructured.NestedSlice(resultQuota.Object, "metadata", "managedFields")
					if err != nil || !foundMetadata || len(managedFields) == 0 {
						klog.Errorf("metdata  not found or error in spec: %v", err)
					}
					managedFields = managedFields[:0]
					if err := unstructured.SetNestedSlice(resultQuota.Object, managedFields, "metadata", "managedFields"); err != nil {
						klog.Error(err)
					}
					resultQuota = hpa.quotaStateManageOfHpa(resultQuota, quotalabels)
					// apply 而不是先删除再新建
					_, updateQuotaErr := lib.DynamicClient.Resource(quotaGvr).Namespace(hpa.NamespaceHpa).Update(context.TODO(), resultQuota, metaV1.UpdateOptions{})
					if updateQuotaErr != nil {
						klog.Info(updateQuotaErr)
						return updateQuotaErr
					}
				}
			case quotaType == comm.ServerlessTypeNoLimit:
				{
					klog.Infof("noLimit type of serverless no quota Manage")
				}
			}
			// TODO test for check
			// if quotaType == comm.ServerlessTypeQPS {
			//	podList, _ := lib.K8sClient.CoreV1().Pods(hpa.NamespaceHpa).List(context.TODO(), options)
			//	qpsRealOfPodQuota, foundQpsRealQuota, err := unstructured.NestedSlice(resultQuota.Object, "spec", "podQpsReal")
			//	if err != nil || !foundQpsRealQuota || len(qpsRealOfPodQuota) == 0 {
			//		klog.Errorf("quota label  not found or error in spec: %v", err)
			//	}
			//	var sleepDuration int = 5
			//	for i := 0; i < comm.PodMsgGetTime; i++ {
			//		podList, _ = lib.K8sClient.CoreV1().Pods(hpa.NamespaceHpa).List(context.TODO(), options)
			//		if len(podList.Items) < len(qpsRealOfPodQuota) {
			//			break
			//		} else {
			//			time.Sleep(time.Duration(sleepDuration) * time.Second)
			//		}
			//		if i == comm.PodMsgGetTime-1 {
			//			err = errors.New("retry time all used")
			//			return err
			//		}
			//	}
			//	qpsAlertOfPodQuota, foundQpsAlertQuota, err := unstructured.NestedSlice(resultQuota.Object, "spec", "podQpsIncreaseOrDecrease")
			//	if err != nil || !foundQpsAlertQuota || len(qpsAlertOfPodQuota) == 0 {
			//		klog.Errorf("quota label  not found or error in spec: %v", err)
			//	}
			//	quotaPodQuota, foundQpsQuota, err := unstructured.NestedSlice(resultQuota.Object, "spec", "podQpsQuota")
			//	klog.Info("quotaPodQuota is:  %s", quotaPodQuota)
			//	if err != nil || !foundQpsQuota || len(quotaPodQuota) == 0 {
			//		klog.Errorf("quota label not found or error in spec: %v", err)
			//	} else {
			//		// 元老实例最小化quota控制，最后一个pod的quota相关属性不变
			//		for _, podQuotaInfo := range quotaPodQuota {
			//			podQuota, _ := podQuotaInfo.(map[string]interface{})
			//			// quota更新，删除缩容pod的相关qpsQuota设置
			//			var in = true
			//			var i = 0
			//			var podInfoMsg = v1.Pod{}
			//			for _, podInfo := range podList.Items {
			//				if podQuota["podName"] == podInfo.Name {
			//					i++
			//					podInfoMsg = podInfo
			//				}
			//			}
			//			if i != 1 {
			//				in = false
			//			}
			//			klog.Info(i)
			//			klog.Info(in)
			//			if !in {
			//				// 删除不存在的pod的相关podquota信息，以及不存在pod的quota回收
			//				klog.Info(podQuota)
			//				quotaReturn := podQuota["qpsQuota"]
			//				klog.Info(quotaReturn)
			//
			//				returnNum, _ := quotaReturn.(int64)
			//				klog.Info(returnNum)
			//				quotaRemain, _ := strconv.Atoi(quota.Labels["quota.cluster.pml.com.cn/quotaRemain"])
			//				quotaUsed, _ := strconv.Atoi(quota.Labels["quota.cluster.pml.com.cn/quotaUsed"])
			//				quotaRequire, _ := strconv.Atoi(quota.Labels["quota.cluster.pml.com.cn/quotaRequire"])
			//				quotaStep, _ = strconv.Atoi(quota.Labels["quota.cluster.pml.com.cn/quotaStep"])
			//				klog.Info(quotaRemain)
			//				// 删除pod的相关quota回收（quotalabels相关值）
			//				quotaRemain += int(returnNum)
			//				quotaUsed -= int(returnNum)
			//				quotaRequire -= int(returnNum)
			//				klog.Info(quotaRemain)
			//				klog.Info("quota.cluster.pml.com.cn/quotaRemain is:  %s", quotalabels["quota.cluster.pml.com.cn/quotaRemain"])
			//				// quotalabels["quota.cluster.pml.com.cn/quota"] = strconv.Itoa(quota)
			//				quotalabels["quota.cluster.pml.com.cn/quotaRemain"] = strconv.Itoa(quotaRemain)
			//				quotalabels["quota.cluster.pml.com.cn/quotaUsed"] = strconv.Itoa(quotaUsed)
			//				quotalabels["quota.cluster.pml.com.cn/quotaRequire"] = strconv.Itoa(quotaRequire)
			//				var twiceQuota int = 2
			//				if quota.Labels["quota.cluster.pml.com.cn/deployScale"] == comm.True {
			//					quotalabels["quota.cluster.pml.com.cn/deployScale"] = comm.False
			//					quotaRequire -= twiceQuota * quotaStep
			//					quotalabels["quota.cluster.pml.com.cn/quotaRequire"] = strconv.Itoa(quotaRequire)
			//				}
			//				klog.Info(quota.Labels)
			//				if err := unstructured.SetNestedStringMap(resultQuota.Object, quotalabels, "metadata", "labels"); err != nil {
			//					klog.Error(err)
			//				}
			//				klog.Info(podQuota["podName"])
			//				// 遍历podQuota list，匹配in== false的podQuota
			//				for i, podQuotaInfoDel := range quotaPodQuota {
			//					podQuotaIn, _ := podQuotaInfoDel.(map[string]interface{})
			//					// 匹配上，进行处理
			//					if podQuotaIn["podName"] == podQuota["podName"] {
			//						// TODO More Test to Check
			//						// 被删除的相关quota信息清理
			//						//quotaPodQuotai := append(quotaPodQuota[:i], quotaPodQuota[i+1:]...)
			//						//klog.Info(quotaPodQuota)
			//						//klog.Info(quotaPodQuotai)
			//						//quotaPodQuota = nilElementDrop(quotaPodQuotai)
			//						quotaPodQuota = nilElementDrop(append(quotaPodQuota[:i], quotaPodQuota[i+1:]...))
			//						//klog.Info(quotaPodQuota)
			//						//qpsRealOfPodQuotai := append(qpsRealOfPodQuota[:i], qpsRealOfPodQuota[i+1:]...)
			//						//qpsRealOfPodQuota = nilElementDrop(qpsRealOfPodQuotai)
			//						qpsRealOfPodQuota = nilElementDrop(append(qpsRealOfPodQuota[:i], qpsRealOfPodQuota[i+1:]...))
			//						//qpsAlertOfPodQuotai := append(qpsAlertOfPodQuota[:i], qpsAlertOfPodQuota[i+1:]...)
			//						//
			//						//qpsAlertOfPodQuota = nilElementDrop(qpsAlertOfPodQuotai)
			//						qpsAlertOfPodQuota = nilElementDrop(append(qpsAlertOfPodQuota[:i], qpsAlertOfPodQuota[i+1:]...))
			//						if err := unstructured.SetNestedSlice(resultQuota.Object, quotaPodQuota, "spec", "podQpsQuota"); err != nil {
			//							klog.Error(err)
			//						}
			//						if err := unstructured.SetNestedSlice(resultQuota.Object, qpsRealOfPodQuota, "spec", "podQpsReal"); err != nil {
			//							klog.Error(err)
			//						}
			//						if err := unstructured.SetNestedSlice(resultQuota.Object, qpsAlertOfPodQuota, "spec", "podQpsIncreaseOrDecrease"); err != nil {
			//							klog.Error(err)
			//						}
			//						// 最后一个元老pod特殊处理
			//						if lastFoundingMember {
			//							// 最后一个pod的quota最小化控制
			//							// 查看最后一个pod的qps是否为0或nil 是的话则设置最小化为1step
			//							klog.Info(len(quotaPodQuota))
			//							for i, podQuotaInfo := range quotaPodQuota {
			//								podQuota, _ = podQuotaInfo.(map[string]interface{})
			//								// _, ok := podQuota["qpsQuota"]
			//								if podQuota["qpsQuota"] == int64(0) {
			//									// 设置最后一个pod的quota为1
			//									podQuota["qpsQuota"] = int64(quotaStep)
			//									quotaPodQuota[i] = podQuota
			//									if err := unstructured.SetNestedSlice(resultQuota.Object, quotaPodQuota, "spec", "podQpsQuota"); err != nil {
			//										klog.Error(err)
			//									}
			//									quotaRemain -= quotaStep
			//									quotaUsed += quotaStep
			//									quotalabels["quota.cluster.pml.com.cn/quotaRemain"] = strconv.Itoa(quotaRemain)
			//									quotalabels["quota.cluster.pml.com.cn/quotaUsed"] = strconv.Itoa(quotaUsed)
			//									quotalabels["quota.cluster.pml.com.cn/quotaRequire"] = strconv.Itoa(quotaRequire - quotaStep)
			//									if err := unstructured.SetNestedStringMap(resultQuota.Object, quotalabels, "metadata", "labels"); err != nil {
			//										klog.Error(err)
			//									}
			//								}
			//							}
			//						}
			//						// 删除不相关的managed信息，无法应用server端apply，版本问题，此处先删除再创建
			//						// https:// kubernetes.io/zh-cn/docs/reference/using-api/server-side-apply/
			//						managedFields, foundMetadata, err := unstructured.NestedSlice(resultQuota.Object, "metadata", "managedFields")
			//						if err != nil || !foundMetadata || len(managedFields) == 0 {
			//							klog.Errorf("metdata  not found or error in spec: %v", err)
			//						}
			//						managedFields = managedFields[:0]
			//						if err = unstructured.SetNestedSlice(resultQuota.Object, managedFields, "metadata", "managedFields"); err != nil {
			//							klog.Error(err)
			//						}
			//						resultQuota = hpa.quotaStateManageOfHpa(resultQuota, quotalabels)
			//						// apply 而不是先删除再新建
			//						_, updateQuotaErr = lib.DynamicClient.Resource(quotaGvr).Namespace(hpa.NamespaceHpa).Update(context.TODO(), resultQuota, metaV1.UpdateOptions{})
			//						if updateQuotaErr != nil {
			//							klog.Info(updateQuotaErr)
			//							return updateQuotaErr
			//						}
			//						// pod sub alert回收
			//						var action = "delete"
			//						klog.Info(podInfoMsg)
			//						podName, errBool := podQuota["podName"]
			//						if !errBool {
			//							klog.Info(errBool)
			//						}
			//						err = hpa.qpsQuotaHpaDel(podName, action)
			//						if err != nil {
			//							klog.Error(err)
			//						}
			//					}
			//				}
			//			}
			//		}
			//	}
			// } else if quotaType == comm.ServerlessTypeReplica {
			//	// 元老实例最小化quota控制，最后一个pod的quota相关属性不变
			//	quotaRemain, _ := strconv.Atoi(quota.Labels["quota.cluster.pml.com.cn/quotaRemain"])
			//	quotaUsed, _ := strconv.Atoi(quota.Labels["quota.cluster.pml.com.cn/quotaUsed"])
			//	quotaRequire, _ := strconv.Atoi(quota.Labels["quota.cluster.pml.com.cn/quotaRequire"])
			//	quotaStep, _ = strconv.Atoi(quota.Labels["quota.cluster.pml.com.cn/quotaStep"])
			//	klog.Info(quotaRemain)
			//	// 删除pod的相关quota回收（quotalabels相关值）
			//	quotaRemain += quotaStep
			//	quotaUsed -= quotaStep
			//	quotaRequire -= quotaStep
			//	klog.Info(quotaRemain)
			//	klog.Info("quota.cluster.pml.com.cn/quotaRemain is:  %s", quotalabels["quota.cluster.pml.com.cn/quotaRemain"])
			//	quotalabels["quota.cluster.pml.com.cn/quotaRemain"] = strconv.Itoa(quotaRemain)
			//	quotalabels["quota.cluster.pml.com.cn/quotaUsed"] = strconv.Itoa(quotaUsed)
			//	quotalabels["quota.cluster.pml.com.cn/quotaRequire"] = strconv.Itoa(quotaRequire)
			//	if quota.Labels["quota.cluster.pml.com.cn/deployScale"] == comm.True {
			//		quotalabels["quota.cluster.pml.com.cn/deployScale"] = comm.False
			//		// 正在扩容的情况下，还未完成，缩容，则原有扩容需要的申请分片归还
			//		quotaRequire -= quotaStep
			//		quotalabels["quota.cluster.pml.com.cn/quotaRequire"] = strconv.Itoa(quotaRequire)
			//	}
			//	klog.Info(quota.Labels)
			//	if err := unstructured.SetNestedStringMap(resultQuota.Object, quotalabels, "metadata", "labels"); err != nil {
			//		klog.Error(err)
			//	}
			//	// https:// kubernetes.io/zh-cn/docs/reference/using-api/server-side-apply/
			//	managedFields, foundMetadata, err := unstructured.NestedSlice(resultQuota.Object, "metadata", "managedFields")
			//	if err != nil || !foundMetadata || len(managedFields) == 0 {
			//		klog.Errorf("metdata  not found or error in spec: %v", err)
			//	}
			//	managedFields = managedFields[:0]
			//	if err := unstructured.SetNestedSlice(resultQuota.Object, managedFields, "metadata", "managedFields"); err != nil {
			//		klog.Error(err)
			//	}
			//	resultQuota = hpa.quotaStateManageOfHpa(resultQuota, quotalabels)
			//	// apply 而不是先删除再新建
			//	_, updateQuotaErr := lib.DynamicClient.Resource(quotaGvr).Namespace(hpa.NamespaceHpa).Update(context.TODO(), resultQuota, metaV1.UpdateOptions{})
			//	if updateQuotaErr != nil {
			//		klog.Info(updateQuotaErr)
			//		return updateQuotaErr
			//	}
			// } else if quotaType == comm.ServerlessTypeNoLimit {
			//	klog.Infof("noLimit type of serverless no quota Manage")
			// }
			klog.Infof("After update quota info is : %s", resultQuota)
			return nil
		})
		if retryErr != nil {
			return false
		} else {
			return true
		}
	} else {
		klog.Infof("获取锁失败，锁现在在 %+v 手中", who)
	}
	return true
}

func nilElementDrop(multiPodQuota []interface{}) []interface{} {
	var PodQuota []interface{}
	for _, element := range multiPodQuota {
		if reflect.TypeOf(element).Kind() == reflect.Map {
			PodQuota = append(PodQuota, element)
		}
	}
	return PodQuota
}

func (hpa *Hpa) quotaStateManageOfHpa(resultQuota *unstructured.Unstructured, quotaLabels map[string]string) *unstructured.Unstructured {
	klog.Infof("start manage state of serverless quota while replica hpa ")
	childClusterState, foundchildClusterState, err := unstructured.NestedSlice(resultQuota.Object, "spec", "childClusterState")
	if err != nil || !foundchildClusterState || len(childClusterState) == 0 {
		klog.Errorf("quota childClusterState  not found or error in spec: %v", err)
	}
	quotaRemain, _ := strconv.Atoi(quotaLabels["quota.cluster.pml.com.cn/quotaRemain"])
	quotaLocal, _ := strconv.Atoi(quotaLabels["quota.cluster.pml.com.cn/quota"])
	quotaRequire, _ := strconv.Atoi(quotaLabels["quota.cluster.pml.com.cn/quotaRequire"])
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
		//
		//if quotaLocal == quotaRequire && quotaRemain == 0 {
		//	childClusterStateMsg["clusterState"] = steady
		//} else if quotaLocal < quotaRequire {
		//	childClusterStateMsg["clusterState"] = requireQ
		//} else if quotaLocal > quotaRequire {
		//	childClusterStateMsg["clusterState"] = returnQ
		//}
		klog.Infof("end manage state of serverless quota:  state is: %s ", childClusterStateMsg["clusterState"])
	}
	if err := unstructured.SetNestedSlice(resultQuota.Object, childClusterState, "spec", "childClusterState"); err != nil {
		klog.Error(err)
	}
	return resultQuota
}
