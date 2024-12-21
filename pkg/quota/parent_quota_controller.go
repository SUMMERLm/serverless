package quota

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/SUMMERLm/serverless/pkg/apis/serverless/v1"
	"github.com/SUMMERLm/serverless/pkg/comm"
	"github.com/SUMMERLm/serverless/pkg/etcd_lock"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/retry"
	"k8s.io/klog/v2"

	coreV1 "k8s.io/api/core/v1"
	metaV1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilRuntime "k8s.io/apimachinery/pkg/util/runtime"
)

// syncHandler compares the actual state with the desired, and attempts to
// converge the two. It then updates the Status block of the Quota resource
// with the current status of the resource.
func (c *Controller) syncHandlerParent(key string) error {
	//  Convert the namespace/name string into a distinct namespace and name
	namespace, name, err := cache.SplitMetaNamespaceKey(key)
	if err != nil {
		utilRuntime.HandleError(fmt.Errorf("invalid resource key: %s", key))
		return nil
	}

	//  Get the Quota resource with this namespace/name
	quota, err := c.quotasLister.Quotas(namespace).Get(name)
	// clusterConf, err := c.kubeclientset.CoreV1().ConfigMaps("serverless-system").Get(context.TODO(), "quota-conf", metav1.GetOptions{})
	// quotaParent, err := c.quotasParentLister.Quotas(namespace).Get(name)

	if err != nil {
		//  The Quota resource may no longer exist, in which case we stop processing.
		// 父集群quota删除，本集群不做动作
		if errors.IsNotFound(err) {
			return c.parentQuotaDelete(key, namespace, name)
		}
	} else {
		// 父集群新建或者更新
		// 父集群新建操作，本集群不做动作
		// 父集群更新操作，通过识别不同的更新内容进行相关业务逻辑动作
		if err != nil {
			klog.Errorf(err.Error())
		}
		// clusterConf, err := c.kubeclientset.CoreV1().ConfigMaps("serverless-system").Get(context.TODO(), "quota-conf", metav1.GetOptions{})
		// if err != nil {
		// 	return err
		// }
		quotalocal, err := c.quotasLister.Quotas(namespace).Get(name)
		if err != nil {
			klog.Info("cannot get local quota:%s", err)
			return nil
		}
		quotaparent, err := c.quotasParentLister.Quotas(namespace).Get(name)
		// var etcdEndpointQuota = []string{clusterConf.Data["etcdEndpoint"]}

		if err == nil {
			// 更新操作
			err := c.parentQuotaUpdate(quotaparent, quotalocal, namespace, name)
			if err != nil {
				return err
			}
		} else {
			// 新建操作
			return c.parentQuotaNew(namespace, name)
		}
	}
	//  If an error occurs during Get/Create, we'll requeue the item so we can
	//  attempt processing again later. This could have been caused by a
	//  temporary network failure, or any other transient reason.
	// if err != nil {
	// 	return err
	// }
	c.recorder.Event(quota, coreV1.EventTypeNormal, SuccessSynced, MessageResourceSynced)
	return nil
}

// enqueueQuotaParent takes a Quota resource and converts it into a namespace/name
// string which is then put onto the work queue. This method should *not* be
// passed resources of any type other than Quota.
func (c *Controller) enqueueQuotaParent(obj interface{}) {
	var key string
	var err error
	if key, err = cache.MetaNamespaceKeyFunc(obj); err != nil {
		utilRuntime.HandleError(err)
		return
	}
	c.parentworkqueue.Add(key)
}

// 删除操作
func (c *Controller) enqueueQuotaParentForDelete(obj interface{}) {
	var key string
	var err error
	//  从缓存中删除指定对象
	key, err = cache.DeletionHandlingMetaNamespaceKeyFunc(obj)
	if err != nil {
		utilRuntime.HandleError(err)
		return
	}
	// 再将key放入队列
	c.parentworkqueue.AddRateLimited(key)
}

func (c *Controller) parentQuotaDelete(key string, namespace string, name string) error {
	// klog.Infof("Parent Quota对象被删除，这里执行实际的删除业务: %s/%s ...:Nothing to do", namespace, name)
	// utilruntime.HandleError(fmt.Errorf("Quota '%s' in work queue no longer exists", key))
	return nil
}

func (c *Controller) parentQuotaUpdate(quotaparent *v1.Quota, quotalocal *v1.Quota, namespace string, name string) error {
	// quotaLocalCopy := quotalocal.DeepCopy()
	// time.Sleep(time.Duration(5) * time.Second)
	// 锁粒度细化，满足条件需要变更的情况下上锁并进行相关业务，否则不加锁
	if quotalocal.Spec.ClusterAreaType == comm.ClusterAreaTypeCluster {
		// 场景1: 触发0-1扩容
		// 场景2: 触发quota申请情况
		quotalocal.UID = ""
		// 本级新增相关数据，聚合到上级新增
		// 1:聚合网络标识，支持多个标识
		// 遍历本地网络注册相关值
		err := c.parentQuotaUpdateAlertZero(quotalocal, quotaparent)
		if err != nil {
			klog.Errorf(err.Error())
			return err
		}
		// TODO： 待验证
		// 父集群quota变化，立即触发子集群进行quota的分配
		klog.Infof("quotaParent update msg is : %s", quotaparent.Name)
		err = c.localQuotaUpdate(quotaparent, quotalocal)
		klog.Info(err)
		if err != nil {
			klog.Errorf(err.Error())
			return err
			// 失败则等待子集群主动请求
		}
	}
	return nil
}

func (c *Controller) parentQuotaNew(name string, namespace string) error {
	// 新建操作：父集群新建动作，本集群不做动作
	return nil
}

// 本级deploy大于0，则直接删除field级的childalert相关信息并返回
func (c *Controller) parentQuotaUpdateAlertZero(quotaLocalCopy *v1.Quota, quotaLocalCopyOfParent *v1.Quota) error {
	for _, clusterAlert := range quotaLocalCopyOfParent.Spec.ChildAlert {
		if clusterAlert.ClusterName == quotaLocalCopy.Spec.LocalName && clusterAlert.Alert {
			// 匹配到本集群则进行0-1扩容
			// 检测本集群serverless相关的deploy replica是否为0，为0则0-1，否则跳过
			// 设置本集群serverless quota的alert属性
			// 	设置完成后，如果本次未扩容成功，需等待quota申请情况，申请到则在后续同步时检查并扩容
			//   quota申请不到，则不能进行0-1的扩容
			var in = false
			for _, alert := range quotaLocalCopy.Spec.ChildAlert {
				if alert.ClusterName == clusterAlert.ClusterName {
					in = true
				}
			}
			if in {
				// 清理field cr 0-1关于本cluster的信息
				retryErr := retry.RetryOnConflict(retry.DefaultRetry, func() error {
					clusterConf, err := c.kubeclientset.CoreV1().ConfigMaps("serverless-system").Get(context.TODO(), "quota-conf", metaV1.GetOptions{})
					if err != nil {
						return err
					}
					var etcdEndpointQuota = []string{clusterConf.Data["etcdEndpoint"]}
					// 清除无效的0-1
					if locker, err := etcd_lock.New(etcdEndpointQuota, option); err != nil {
						klog.Errorf("创建锁失败：%+v", err)
					} else if who, ok := locker.Acquire(quotaLocalCopy.Name + quotaLocalCopy.Namespace); ok {
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
						for i, alert := range quotaLocalCopyOfParent.Spec.ChildAlert {
							if alert.ClusterName == quotaLocalCopy.Spec.ChildAlert[0].ClusterName {
								quotaLocalCopyOfParent.Spec.ChildAlert = append(quotaLocalCopyOfParent.Spec.ChildAlert[:i], quotaLocalCopyOfParent.Spec.ChildAlert[i+1:]...)
								initQuotaLocalCopyOfParent, _ := c.quotaParentclientset.ServerlessV1().Quotas(quotaLocalCopyOfParent.Namespace).Get(context.TODO(), quotaLocalCopyOfParent.Name, metaV1.GetOptions{})
								quotaLocalCopyOfParent.ResourceVersion = initQuotaLocalCopyOfParent.ResourceVersion
								_, err := c.quotaParentclientset.ServerlessV1().Quotas(quotaLocalCopyOfParent.Namespace).Update(context.TODO(), quotaLocalCopyOfParent, metaV1.UpdateOptions{})
								if err != nil {
									klog.Error(err)
								}
							}
						}
						klog.Infof("0-1 is in processing， no need duplicate Scale 0-1 of serverless ...: %s, namespace: %s\n", quotaLocalCopy.Name, quotaLocalCopy.Namespace)
					} else {
						klog.Infof("获取锁失败，锁现在在 %+v 手中", who)
						return nil
					}
					return nil
				})
				if retryErr != nil {
					klog.Errorf("0-1 failed: %v", retryErr)
					return retryErr
				}
				return nil
			} else {
				deploymentsClient := c.kubeclientset.AppsV1().Deployments(quotaLocalCopy.Namespace)
				retryErr := retry.RetryOnConflict(retry.DefaultRetry, func() error {
					//  Retrieve the latest version of Deployment before attempting update
					//  RetryOnConflict uses exponential backoff to avoid exhausting the apiserver
					result, getErr := deploymentsClient.Get(context.TODO(), quotaLocalCopy.Name, metaV1.GetOptions{})
					if getErr != nil {
						klog.Errorf("Failed to get latest version of Deployment: %v", getErr)
						return getErr
					}
					clusterConf, err := c.kubeclientset.CoreV1().ConfigMaps("serverless-system").Get(context.TODO(), "quota-conf", metaV1.GetOptions{})
					if err != nil {
						return err
					}
					var etcdEndpointQuota = []string{clusterConf.Data["etcdEndpoint"]}
					ClusterQRequire, _ := strconv.Atoi(quotaLocalCopy.Labels["quota.cluster.pml.com.cn/quotaRequire"])
					if *result.Spec.Replicas > 0 || ClusterQRequire > 0 {
						// 清除无效的0-1
						if locker, err := etcd_lock.New(etcdEndpointQuota, option); err != nil {
							klog.Errorf("创建锁失败：%+v", err)
						} else if who, ok := locker.Acquire(quotaLocalCopy.Name + quotaLocalCopy.Namespace); ok {
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
							quotaLocalCopy.Spec.ChildAlert = append(quotaLocalCopy.Spec.ChildAlert, clusterAlert)
							for i, alert := range quotaLocalCopyOfParent.Spec.ChildAlert {
								if alert.ClusterName == quotaLocalCopy.Spec.ChildAlert[0].ClusterName {
									quotaLocalCopyOfParent.Spec.ChildAlert = append(quotaLocalCopyOfParent.Spec.ChildAlert[:i], quotaLocalCopyOfParent.Spec.ChildAlert[i+1:]...)
									initQuotaLocalCopyOfParent, _ := c.quotaParentclientset.ServerlessV1().Quotas(quotaLocalCopyOfParent.Namespace).Get(context.TODO(), quotaLocalCopyOfParent.Name, metaV1.GetOptions{})
									quotaLocalCopyOfParent.ResourceVersion = initQuotaLocalCopyOfParent.ResourceVersion
									_, err := c.quotaParentclientset.ServerlessV1().Quotas(quotaLocalCopyOfParent.Namespace).Update(context.TODO(), quotaLocalCopyOfParent, metaV1.UpdateOptions{})
									if err != nil {
										klog.Error(err)
									}
								}
							}
							klog.Infof("当前不为0，无效的Scale 0-1 serverless ...: %s, namespace: %s\n", quotaLocalCopy.Name, quotaLocalCopy.Namespace)
						} else {
							klog.Infof("获取锁失败，锁现在在 %+v 手中", who)
							return nil
						}
					} else {
						// 清除0-1标志
						// 0-1
						if locker, err := etcd_lock.New(etcdEndpointQuota, option); err != nil {
							klog.Errorf("创建锁失败：%+v", err)
						} else if who, ok := locker.Acquire(quotaLocalCopy.Name + quotaLocalCopy.Namespace); ok {
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
							quotaLocalCopy.Spec.ChildAlert = append(quotaLocalCopy.Spec.ChildAlert, clusterAlert)
							for i, alert := range quotaLocalCopyOfParent.Spec.ChildAlert {
								if alert.ClusterName == quotaLocalCopy.Spec.ChildAlert[0].ClusterName {
									quotaLocalCopyOfParent.Spec.ChildAlert = append(quotaLocalCopyOfParent.Spec.ChildAlert[:i], quotaLocalCopyOfParent.Spec.ChildAlert[i+1:]...)
									initQuotaLocalCopyOfParent, _ := c.quotaParentclientset.ServerlessV1().Quotas(quotaLocalCopyOfParent.Namespace).Get(context.TODO(), quotaLocalCopyOfParent.Name, metaV1.GetOptions{})
									quotaLocalCopyOfParent.ResourceVersion = initQuotaLocalCopyOfParent.ResourceVersion
									_, err = c.quotaParentclientset.ServerlessV1().Quotas(quotaLocalCopyOfParent.Namespace).Update(context.TODO(), quotaLocalCopyOfParent, metaV1.UpdateOptions{})
									if err != nil {
										klog.Error(err)
									}
								}
							}

							remain, _ := strconv.Atoi(quotaLocalCopy.Labels["quota.cluster.pml.com.cn/quotaRemain"])
							step, _ := strconv.Atoi(quotaLocalCopy.Labels["quota.cluster.pml.com.cn/quotaStep"])
							// quotaUsed, _ := strconv.Atoi(quotaLocalCopy.Labels["quota.cluster.pml.com.cn/quotaUsed"])
							// 0-1 quota检查
							//  本地存在quota，直接进行0-1扩容，并进行相关quota的设置
							//  本地没有quota，进行quota申请，申请得到则在后续update时进行0-1扩容
							klog.Info(remain)

							if quotaLocalCopy.Labels["quota.cluster.pml.com.cn/type"] != comm.ServerlessTypeNoLimit {
								// 场景1: 触发0-1扩容
								if remain >= step {
									err = c.zeroAlertDeploy(quotaLocalCopy)
									if err != nil {
										klog.Error(err)
										return err
									}
									klog.Infof("Scale 0-1 serverless done...: %s, namespace: %s\n", quotaLocalCopy.Name, quotaLocalCopy.Namespace)
								} else {
									// 场景2: 触发quota申请情况
									// Quota 申请
									klog.Infof("Scale 0-1 serverless start : quota require...: %s, namespace: %s\n", quotaLocalCopy.Name, quotaLocalCopy.Namespace)
									quotaLocalCopy.Labels["quota.cluster.pml.com.cn/quotaRequire"] = strconv.Itoa(step)
									//
									klog.Infof("child alert msg of child is: %s", quotaLocalCopy)
									// TODO： state状态设置为require
									initQuotaLocalCopy, _ := c.quotaclientset.ServerlessV1().Quotas(quotaLocalCopy.Namespace).Get(context.TODO(), quotaLocalCopy.Name, metaV1.GetOptions{})
									quotaLocalCopy.ResourceVersion = initQuotaLocalCopy.ResourceVersion
									_, err = c.quotaclientset.ServerlessV1().Quotas(quotaLocalCopy.Namespace).Update(context.TODO(), quotaLocalCopy, metaV1.UpdateOptions{})
									if err != nil {
										klog.Info(err)
									}
									err = c.ClusterStateUpdate(quotaLocalCopy)
									if err != nil {
										klog.Info(err)
									}
								}
							} else {
								err := c.zeroAlertDeployNoLimit(quotaLocalCopy)
								if err != nil {
									klog.Error(err)
									return err
								}
								klog.Infof("Scale 0-1 serverless done...: %s, namespace: %s\n", quotaLocalCopy.Name, quotaLocalCopy.Namespace)
							}
						} else {
							klog.Infof("获取锁失败，锁现在在 %+v 手中", who)
							return nil
						}
					}
					return nil
				})
				if retryErr != nil {
					klog.Errorf("0-1 failed: %v", retryErr)
					return retryErr
				}
			}
		}
	}
	return nil
}

func (c *Controller) zeroAlertDeploy(quotaLocalCopy *v1.Quota) error {
	clusterConf, err := c.kubeclientset.CoreV1().ConfigMaps("serverless-system").Get(context.TODO(), "quota-conf", metaV1.GetOptions{})
	if err != nil {
		return err
	}
	remain, _ := strconv.Atoi(quotaLocalCopy.Labels["quota.cluster.pml.com.cn/quotaRemain"])
	step, _ := strconv.Atoi(quotaLocalCopy.Labels["quota.cluster.pml.com.cn/quotaStep"])
	quotaUsed, _ := strconv.Atoi(quotaLocalCopy.Labels["quota.cluster.pml.com.cn/quotaUsed"])
	deploymentsClient := c.kubeclientset.AppsV1().Deployments(quotaLocalCopy.Namespace)
	retryErr := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		//  Retrieve the latest version of Deployment before attempting update
		//  RetryOnConflict uses exponential backoff to avoid exhausting the apiserver
		result, getErr := deploymentsClient.Get(context.TODO(), quotaLocalCopy.Name, metaV1.GetOptions{})
		if getErr != nil {
			klog.Errorf("Failed to get latest version of Deployment: %v", getErr)
			return getErr
		}
		// scale down base on the availableReplica instead of replica
		var updateErr error = nil

		if *result.Spec.Replicas == 0 {
			*result.Spec.Replicas = int32(1)
			_, updateErr = deploymentsClient.Update(context.TODO(), result, metaV1.UpdateOptions{})
			if updateErr != nil {
				klog.Info(updateErr)
				return updateErr
			}
		}
		if quotaLocalCopy.Labels["quota.cluster.pml.com.cn/type"] == comm.ServerlessTypeQPS {
			time.Sleep(comm.SleepTime * time.Second)
			var labelselector string
			for k, v := range result.Spec.Selector.MatchLabels {
				klog.Infof(k, v)
				if k == comm.PodSLSSelect {
					labelselector = k + "=" + v
					klog.Infof(labelselector)
				}
			}
			options := metaV1.ListOptions{
				LabelSelector: labelselector,
			}
			//  get the pod list
			//  https:// pkg.go.dev/k8s.io/client-go@v11.0.0+incompatible/kubernetes/typed/core/v1?tab=doc#PodInterface
			podList, _ := c.kubeclientset.CoreV1().Pods(quotaLocalCopy.Namespace).List(context.TODO(), options)

			quotaLocalCopy.Labels["quota.cluster.pml.com.cn/quotaRemain"] = strconv.Itoa(remain - step)
			quotaLocalCopy.Labels["quota.cluster.pml.com.cn/quotaUsed"] = strconv.Itoa(quotaUsed + step)
			klog.Infof("quotaLocalCopy labels is ", quotaLocalCopy.Labels)
			// 0-1触发完成后标志删除
			quotaLocalCopy.Spec.ChildAlert = quotaLocalCopy.Spec.ChildAlert[1:]

			for _, podInfo := range podList.Items {
				klog.Infof("pods-name=%v\n", podInfo.Name)
				klog.Infof("pods-status=%v\n", podInfo.Status.Phase)
				klog.Infof("pods-condition=%v\n", podInfo.Status.Conditions)
				// quota更新，加入新增pod的相关qpsQuota设置
				var in = false
				for _, podQuotaInfo := range quotaLocalCopy.Spec.PodQpsQuota {
					// podQuota, _ := podQuotaInfo.(map[string]interface{})
					if podInfo.Name == podQuotaInfo.PodName {
						in = true
					}
				}
				if !in {
					// add podQuotaInfo
					var podQpsQuota = v1.PodQpsQuotaSpec{PodName: podInfo.Name, QpsQuota: step, ClusterName: quotaLocalCopy.Spec.LocalName}

					quotaLocalCopy.Spec.PodQpsQuota = append(quotaLocalCopy.Spec.PodQpsQuota, podQpsQuota)
					var poqQpsReal = v1.PodQpsQuotaRealSpec{
						PodName: podInfo.Name,
						QpsReal: 0,
					}
					quotaLocalCopy.Spec.PodQpsReal = append(quotaLocalCopy.Spec.PodQpsReal, poqQpsReal)
					var podQpsIncreaseOrDecrease = v1.PodQpsIncreaseOrDecreaseSpec{
						PodName:               podInfo.Name,
						QpsIncreaseOrDecrease: 0,
					}
					quotaLocalCopy.Spec.PodQpsIncreaseOrDecrease = append(quotaLocalCopy.Spec.PodQpsIncreaseOrDecrease, podQpsIncreaseOrDecrease)
					klog.Infof("quotaLocalCopy msg is ", quotaLocalCopy)
					initQuotaLocalCopy, _ := c.quotaclientset.ServerlessV1().Quotas(quotaLocalCopy.Namespace).Get(context.TODO(), quotaLocalCopy.Name, metaV1.GetOptions{})
					quotaLocalCopy.ResourceVersion = initQuotaLocalCopy.ResourceVersion
					_, err := c.quotaclientset.ServerlessV1().Quotas(quotaLocalCopy.Namespace).Update(context.TODO(), quotaLocalCopy, metaV1.UpdateOptions{})
					if err != nil {
						klog.Error(err)
					}

					var hpaWebUrl = clusterConf.Data["qpsQuotaUrl"]
					var action = "init"
					err = c.qpsQuotaHpaAction(&podInfo, quotaLocalCopy.Name, hpaWebUrl, action, int64(step), int64(step))
					if err != nil {
						klog.Error(err)
						return err
					}
				}
			}
		} else if quotaLocalCopy.Labels["quota.cluster.pml.com.cn/type"] == comm.ServerlessTypeReplica || quotaLocalCopy.Labels["quota.cluster.pml.com.cn/type"] == comm.ServerlessTypeNoLimit {
			quotaLocalCopy.Labels["quota.cluster.pml.com.cn/quotaRemain"] = strconv.Itoa(remain - step)
			quotaLocalCopy.Labels["quota.cluster.pml.com.cn/quotaUsed"] = strconv.Itoa(quotaUsed + step)
			klog.Infof("quotaLocalCopy labels is ", quotaLocalCopy.Labels)
			// 0-1触发完成后标志删除
			quotaLocalCopy.Spec.ChildAlert = quotaLocalCopy.Spec.ChildAlert[1:]
			initQuotaLocalCopy, _ := c.quotaclientset.ServerlessV1().Quotas(quotaLocalCopy.Namespace).Get(context.TODO(), quotaLocalCopy.Name, metaV1.GetOptions{})
			quotaLocalCopy.ResourceVersion = initQuotaLocalCopy.ResourceVersion
			_, err := c.quotaclientset.ServerlessV1().Quotas(quotaLocalCopy.Namespace).Update(context.TODO(), quotaLocalCopy, metaV1.UpdateOptions{})
			if err != nil {
				klog.Error(err)
				return err
			}
		}

		//  apply change
		return updateErr
	})

	if retryErr != nil {
		klog.Errorf("0-1 failed: %v", retryErr)
		return retryErr
	}
	return nil
}

func (c *Controller) foundingMemberPodInit(quotaLocalCopy *v1.Quota) error {
	clusterConf, err := c.kubeclientset.CoreV1().ConfigMaps("serverless-system").Get(context.TODO(), "quota-conf", metaV1.GetOptions{})
	if err != nil {
		return err
	}
	remain, _ := strconv.Atoi(quotaLocalCopy.Labels["quota.cluster.pml.com.cn/quotaRemain"])
	step, _ := strconv.Atoi(quotaLocalCopy.Labels["quota.cluster.pml.com.cn/quotaStep"])
	quotaUsed, _ := strconv.Atoi(quotaLocalCopy.Labels["quota.cluster.pml.com.cn/quotaUsed"])
	deploymentsClient := c.kubeclientset.AppsV1().Deployments(quotaLocalCopy.Namespace)
	if err != nil {
		klog.Error(err)
	}
	retryErr := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		//  Retrieve the latest version of Deployment before attempting update
		//  RetryOnConflict uses exponential backoff to avoid exhausting the apiserver
		result, getErr := deploymentsClient.Get(context.TODO(), quotaLocalCopy.Name, metaV1.GetOptions{})
		if getErr != nil {
			klog.Errorf("Failed to get latest version of Deployment: %v", getErr)
			return getErr
		}
		// scale down base on the availableReplica instead of replica
		var updateErr error = nil

		if *result.Spec.Replicas == 0 {
			*result.Spec.Replicas = int32(1)
			_, updateErr = deploymentsClient.Update(context.TODO(), result, metaV1.UpdateOptions{})
			klog.Info(updateErr)
		}
		// 0-1拉起，设置该置
		quotaLocalCopy.Labels["quota.cluster.pml.com.cn/zeroDownTry"] = "3"
		// Quota类型判断，并按需进行pod qps信息的注册
		if quotaLocalCopy.Labels["quota.cluster.pml.com.cn/type"] == comm.ServerlessTypeQPS {
			// deploy replica变更导致生成的pod信息延迟
			time.Sleep(comm.SleepTime * time.Second)
			var labelselector string
			for k, v := range result.Spec.Selector.MatchLabels {
				klog.Infof(k, v)
				if k == comm.PodSLSSelect {
					labelselector = k + "=" + v
					klog.Infof(labelselector)
				}
			}
			options := metaV1.ListOptions{
				LabelSelector: labelselector,
			}
			//  get the pod list
			//  https:// pkg.go.dev/k8s.io/client-go@v11.0.0+incompatible/kubernetes/typed/core/v1?tab=doc#PodInterface
			podList, _ := c.kubeclientset.CoreV1().Pods(quotaLocalCopy.Namespace).List(context.TODO(), options)

			quotaLocalCopy.Labels["quota.cluster.pml.com.cn/quotaRemain"] = strconv.Itoa(remain - step)
			quotaLocalCopy.Labels["quota.cluster.pml.com.cn/quotaUsed"] = strconv.Itoa(quotaUsed + step)
			quotaLocalCopy.Labels["quota.cluster.pml.com.cn/init"] = comm.False
			for _, podInfo := range podList.Items {
				klog.Infof("pods-name=%v\n", podInfo.Name)
				klog.Infof("pods-status=%v\n", podInfo.Status.Phase)
				klog.Infof("pods-condition=%v\n", podInfo.Status.Conditions)
				// quota更新，加入新增pod的相关qpsQuota设置
				var in = false
				for _, podQuotaInfo := range quotaLocalCopy.Spec.PodQpsQuota {
					// podQuota, _ := podQuotaInfo.(map[string]interface{})
					if podInfo.Name == podQuotaInfo.PodName {
						in = true
					}
				}
				if !in {
					// add podQuotaInfo
					var podQpsQuota = v1.PodQpsQuotaSpec{PodName: podInfo.Name, QpsQuota: step, ClusterName: quotaLocalCopy.Spec.LocalName}

					quotaLocalCopy.Spec.PodQpsQuota = append(quotaLocalCopy.Spec.PodQpsQuota, podQpsQuota)
					var poqQpsReal = v1.PodQpsQuotaRealSpec{
						PodName: podInfo.Name,
						QpsReal: 0,
					}
					quotaLocalCopy.Spec.PodQpsReal = append(quotaLocalCopy.Spec.PodQpsReal, poqQpsReal)
					var podQpsIncreaseOrDecrease = v1.PodQpsIncreaseOrDecreaseSpec{
						PodName:               podInfo.Name,
						QpsIncreaseOrDecrease: 0,
					}
					quotaLocalCopy.Spec.PodQpsIncreaseOrDecrease = append(quotaLocalCopy.Spec.PodQpsIncreaseOrDecrease, podQpsIncreaseOrDecrease)

					var hpaWebUrl = clusterConf.Data["qpsQuotaUrl"]
					var action = "init"
					err = c.qpsQuotaHpaAction(&podInfo, quotaLocalCopy.Name, hpaWebUrl, action, int64(step), int64(step))
					if err != nil {
						klog.Error(err)
						return err
					}
					klog.Infof("quotaLocalCopy msg is : %s", quotaLocalCopy)
					initQuotaLocalCopy, _ := c.quotaclientset.ServerlessV1().Quotas(quotaLocalCopy.Namespace).Get(context.TODO(), quotaLocalCopy.Name, metaV1.GetOptions{})
					quotaLocalCopy.ResourceVersion = initQuotaLocalCopy.ResourceVersion
					_, err := c.quotaclientset.ServerlessV1().Quotas(quotaLocalCopy.Namespace).Update(context.TODO(), quotaLocalCopy, metaV1.UpdateOptions{})
					if err != nil {
						klog.Error(err)
						return err
					}
				}
			}
		} else if quotaLocalCopy.Labels["quota.cluster.pml.com.cn/type"] == comm.ServerlessTypeReplica || quotaLocalCopy.Labels["quota.cluster.pml.com.cn/type"] == comm.ServerlessTypeNoLimit {
			quotaLocalCopy.Labels["quota.cluster.pml.com.cn/quotaRemain"] = strconv.Itoa(remain - step)
			quotaLocalCopy.Labels["quota.cluster.pml.com.cn/quotaUsed"] = strconv.Itoa(quotaUsed + step)
			quotaLocalCopy.Labels["quota.cluster.pml.com.cn/init"] = comm.False

			initQuotaLocalCopy, errGetQuota := c.quotaclientset.ServerlessV1().Quotas(quotaLocalCopy.Namespace).Get(context.TODO(), quotaLocalCopy.Name, metaV1.GetOptions{})
			if errGetQuota != nil {
				klog.Error(errGetQuota)
				return errGetQuota
			}
			quotaLocalCopy.ResourceVersion = initQuotaLocalCopy.ResourceVersion
			_, errUpdateQuota := c.quotaclientset.ServerlessV1().Quotas(quotaLocalCopy.Namespace).Update(context.TODO(), quotaLocalCopy, metaV1.UpdateOptions{})
			if errUpdateQuota != nil {
				klog.Error(errUpdateQuota)
				return errUpdateQuota
			}
		}

		//  apply change
		return updateErr
	})

	if retryErr != nil {
		klog.Errorf("0-1 failed: %v", retryErr)
		return retryErr
	}
	return nil
}

func (c *Controller) scaleUpDeploy(quotaLocalCopy *v1.Quota) error {
	clusterConf, err := c.kubeclientset.CoreV1().ConfigMaps("serverless-system").Get(context.TODO(), "quota-conf", metaV1.GetOptions{})
	if err != nil {
		return err
	}
	remain, _ := strconv.Atoi(quotaLocalCopy.Labels["quota.cluster.pml.com.cn/quotaRemain"])
	step, _ := strconv.Atoi(quotaLocalCopy.Labels["quota.cluster.pml.com.cn/quotaStep"])
	quotaUsed, _ := strconv.Atoi(quotaLocalCopy.Labels["quota.cluster.pml.com.cn/quotaUsed"])
	if err != nil {
		klog.Error(err)
	}
	deploymentsClient := c.kubeclientset.AppsV1().Deployments(quotaLocalCopy.Namespace)
	retryErr := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		//  Retrieve the latest version of Deployment before attempting update
		//  RetryOnConflict uses exponential backoff to avoid exhausting the apiserver
		result, getErr := deploymentsClient.Get(context.TODO(), quotaLocalCopy.Name, metaV1.GetOptions{})
		if getErr != nil {
			klog.Errorf("Failed to get latest version of Deployment: %v", getErr)
			return getErr
		}
		// scale down base on the availableReplica instead of replica
		var updateErr error

		*result.Spec.Replicas++
		_, updateErr = deploymentsClient.Update(context.TODO(), result, metaV1.UpdateOptions{})
		if updateErr != nil {
			klog.Info(updateErr)
		}
		if quotaLocalCopy.Labels["quota.cluster.pml.com.cn/type"] == comm.ServerlessTypeQPS {
			time.Sleep(comm.SleepTime * time.Second)
			var labelselector string
			for k, v := range result.Spec.Selector.MatchLabels {
				klog.Infof(k, v)
				if k == comm.PodSLSSelect {
					labelselector = k + "=" + v
					klog.Infof(labelselector)
				}
			}
			options := metaV1.ListOptions{
				LabelSelector: labelselector,
			}
			//  get the pod list
			//  https:// pkg.go.dev/k8s.io/client-go@v11.0.0+incompatible/kubernetes/typed/core/v1?tab=doc#PodInterface
			podList, _ := c.kubeclientset.CoreV1().Pods(quotaLocalCopy.Namespace).List(context.TODO(), options)

			quotaLocalCopy.Labels["quota.cluster.pml.com.cn/quotaRemain"] = strconv.Itoa(remain - step)
			quotaLocalCopy.Labels["quota.cluster.pml.com.cn/quotaUsed"] = strconv.Itoa(quotaUsed + step)
			quotaLocalCopy.Labels["quota.cluster.pml.com.cn/deployScale"] = comm.False

			for _, podInfo := range podList.Items {
				klog.Infof("pods-name=%v\n", podInfo.Name)
				klog.Infof("pods-status=%v\n", podInfo.Status.Phase)
				klog.Infof("pods-condition=%v\n", podInfo.Status.Conditions)
				// quota更新，加入新增pod的相关qpsQuota设置
				var in = false
				for _, podQuotaInfo := range quotaLocalCopy.Spec.PodQpsQuota {
					// podQuota, _ := podQuotaInfo.(map[string]interface{})
					if podInfo.Name == podQuotaInfo.PodName {
						in = true
					}
				}
				if !in {
					// add podQuotaInfo
					var podQpsQuota = v1.PodQpsQuotaSpec{PodName: podInfo.Name, QpsQuota: step, ClusterName: quotaLocalCopy.Spec.LocalName}

					quotaLocalCopy.Spec.PodQpsQuota = append(quotaLocalCopy.Spec.PodQpsQuota, podQpsQuota)
					var poqQpsReal = v1.PodQpsQuotaRealSpec{
						PodName: podInfo.Name,
						QpsReal: 0,
					}
					quotaLocalCopy.Spec.PodQpsReal = append(quotaLocalCopy.Spec.PodQpsReal, poqQpsReal)
					var podQpsIncreaseOrDecrease = v1.PodQpsIncreaseOrDecreaseSpec{
						PodName:               podInfo.Name,
						QpsIncreaseOrDecrease: 0,
					}
					quotaLocalCopy.Spec.PodQpsIncreaseOrDecrease = append(quotaLocalCopy.Spec.PodQpsIncreaseOrDecrease, podQpsIncreaseOrDecrease)

					var hpaWebUrl = clusterConf.Data["qpsQuotaUrl"]
					initQuotaLocalCopy, _ := c.quotaclientset.ServerlessV1().Quotas(quotaLocalCopy.Namespace).Get(context.TODO(), quotaLocalCopy.Name, metaV1.GetOptions{})
					quotaLocalCopy.ResourceVersion = initQuotaLocalCopy.ResourceVersion
					_, err = c.quotaclientset.ServerlessV1().Quotas(quotaLocalCopy.Namespace).Update(context.TODO(), quotaLocalCopy, metaV1.UpdateOptions{})
					if err != nil {
						klog.Error(err)
					}
					var action = "init"
					err = c.qpsQuotaHpaAction(&podInfo, quotaLocalCopy.Name, hpaWebUrl, action, int64(step), int64(step))
					if err != nil {
						klog.Error(err)
					}
				}
			}
		} else if quotaLocalCopy.Labels["quota.cluster.pml.com.cn/type"] == comm.ServerlessTypeReplica || quotaLocalCopy.Labels["quota.cluster.pml.com.cn/type"] == comm.ServerlessTypeNoLimit {
			quotaLocalCopy.Labels["quota.cluster.pml.com.cn/quotaRemain"] = strconv.Itoa(remain - step)
			quotaLocalCopy.Labels["quota.cluster.pml.com.cn/quotaUsed"] = strconv.Itoa(quotaUsed + step)
			quotaLocalCopy.Labels["quota.cluster.pml.com.cn/deployScale"] = comm.False
			initQuotaLocalCopy, _ := c.quotaclientset.ServerlessV1().Quotas(quotaLocalCopy.Namespace).Get(context.TODO(), quotaLocalCopy.Name, metaV1.GetOptions{})
			quotaLocalCopy.ResourceVersion = initQuotaLocalCopy.ResourceVersion
			_, err := c.quotaclientset.ServerlessV1().Quotas(quotaLocalCopy.Namespace).Update(context.TODO(), quotaLocalCopy, metaV1.UpdateOptions{})
			if err != nil {
				klog.Error(err)
			}
		}
		//  apply change
		return updateErr
	})

	if retryErr != nil {
		klog.Errorf("scale up failed: %v", retryErr)
		return retryErr
	}
	return nil
}

func (c *Controller) zeroAlertDeployNoLimit(quotaLocalCopy *v1.Quota) error {
	deploymentsClient := c.kubeclientset.AppsV1().Deployments(quotaLocalCopy.Namespace)
	retryErr := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		//  Retrieve the latest version of Deployment before attempting update
		//  RetryOnConflict uses exponential backoff to avoid exhausting the apiserver
		result, getErr := deploymentsClient.Get(context.TODO(), quotaLocalCopy.Name, metaV1.GetOptions{})
		if getErr != nil {
			klog.Errorf("Failed to get latest version of Deployment: %v", getErr)
			return getErr
		}
		// scale down base on the availableReplica instead of replica
		var updateErr error = nil

		if *result.Spec.Replicas == 0 {
			*result.Spec.Replicas = int32(1)
			_, updateErr = deploymentsClient.Update(context.TODO(), result, metaV1.UpdateOptions{})
			if updateErr != nil {
				klog.Info(updateErr)
			}
		}
		// 0-1触发完成后标志删除
		quotaLocalCopy.Spec.ChildAlert = quotaLocalCopy.Spec.ChildAlert[1:]

		// c.quotaclientset.ServerlessV1().Quotas(quotaLocalCopy.Namespace).Update(context.TODO(), quotaLocalCopy, metav1.UpdateOptions{})
		initQuotaLocalCopy, _ := c.quotaclientset.ServerlessV1().Quotas(quotaLocalCopy.Namespace).Get(context.TODO(), quotaLocalCopy.Name, metaV1.GetOptions{})
		quotaLocalCopy.ResourceVersion = initQuotaLocalCopy.ResourceVersion
		_, err := c.quotaclientset.ServerlessV1().Quotas(quotaLocalCopy.Namespace).Update(context.TODO(), quotaLocalCopy, metaV1.UpdateOptions{})
		if err != nil {
			klog.Error(err)
		}
		//  apply change
		return updateErr
	})

	if retryErr != nil {
		klog.Errorf("0-1 failed: %v", retryErr)
		return retryErr
	}
	return nil
}

func (c *Controller) foundingMemberPodInitNoLimit(quotaLocalCopy *v1.Quota) error {
	deploymentsClient := c.kubeclientset.AppsV1().Deployments(quotaLocalCopy.Namespace)
	retryErr := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		//  Retrieve the latest version of Deployment before attempting update
		//  RetryOnConflict uses exponential backoff to avoid exhausting the apiserver
		result, getErr := deploymentsClient.Get(context.TODO(), quotaLocalCopy.Name, metaV1.GetOptions{})
		if getErr != nil {
			klog.Errorf("Failed to get latest version of Deployment: %v", getErr)
			return getErr
		}
		// scale down base on the availableReplica instead of replica
		var updateErr error = nil

		if *result.Spec.Replicas == 0 {
			*result.Spec.Replicas = int32(1)
			_, updateErr = deploymentsClient.Update(context.TODO(), result, metaV1.UpdateOptions{})
			klog.Info(updateErr)
		}
		//  apply change
		return updateErr
	})

	if retryErr != nil {
		klog.Errorf("0-1 failed: %v", retryErr)
		return retryErr
	}
	return nil
}

func (c *Controller) scaleUpDeployNoLimit(quotaLocalCopy *v1.Quota) error {
	deploymentsClient := c.kubeclientset.AppsV1().Deployments(quotaLocalCopy.Namespace)
	retryErr := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		//  Retrieve the latest version of Deployment before attempting update
		//  RetryOnConflict uses exponential backoff to avoid exhausting the apiserver
		result, getErr := deploymentsClient.Get(context.TODO(), quotaLocalCopy.Name, metaV1.GetOptions{})
		if getErr != nil {
			klog.Errorf("Failed to get latest version of Deployment: %v", getErr)
			return getErr
		}
		// scale down base on the availableReplica instead of replica
		var updateErr error

		*result.Spec.Replicas++
		_, updateErr = deploymentsClient.Update(context.TODO(), result, metaV1.UpdateOptions{})
		if updateErr != nil {
			klog.Info(updateErr)
		}
		quotaLocalCopy.Labels["quota.cluster.pml.com.cn/deployScale"] = comm.False

		initQuotaLocalCopy, _ := c.quotaclientset.ServerlessV1().Quotas(quotaLocalCopy.Namespace).Get(context.TODO(), quotaLocalCopy.Name, metaV1.GetOptions{})
		quotaLocalCopy.ResourceVersion = initQuotaLocalCopy.ResourceVersion
		_, err := c.quotaclientset.ServerlessV1().Quotas(quotaLocalCopy.Namespace).Update(context.TODO(), quotaLocalCopy, metaV1.UpdateOptions{})
		if err != nil {
			klog.Error(err)
		}
		//  apply change
		return updateErr
	})

	if retryErr != nil {
		klog.Errorf("scale up failed: %v", retryErr)
		return retryErr
	}
	return nil
}
func (c *Controller) ClusterStateUpdate(quotaLocalCopy *v1.Quota) error {
	oldState := quotaLocalCopy.Spec.ChildClusterState[0].ClusterState
	remain, _ := strconv.Atoi(quotaLocalCopy.Labels["quota.cluster.pml.com.cn/quotaRemain"])
	require, _ := strconv.Atoi(quotaLocalCopy.Labels["quota.cluster.pml.com.cn/quotaRequire"])
	quota, _ := strconv.Atoi(quotaLocalCopy.Labels["quota.cluster.pml.com.cn/quota"])
	quotaLocalCopy.Spec.ChildClusterState[0].Quota = quota
	quotaLocalCopy.Spec.ChildClusterState[0].QuotaRemain = remain
	quotaLocalCopy.Spec.ChildClusterState[0].QuotaRequire = require
	if require == quota && remain == 0 {
		quotaLocalCopy.Spec.ChildClusterState[0].ClusterState = steady
	} else if require > quota {
		quotaLocalCopy.Spec.ChildClusterState[0].ClusterState = requireQ
	} else if require < quota {
		quotaLocalCopy.Spec.ChildClusterState[0].ClusterState = returnQ
	}
	if oldState != quotaLocalCopy.Spec.ChildClusterState[0].ClusterState {
		retryErr := retry.RetryOnConflict(retry.DefaultRetry, func() error {
			//  Retrieve the latest version of Deployment before attempting update
			//  RetryOnConflict uses exponential backoff to avoid exhausting the apiserver
			if quotaLocalCopy.Labels["quota.cluster.pml.com.cn/type"] == comm.ServerlessTypeReplica || quotaLocalCopy.Labels["quota.cluster.pml.com.cn/type"] == comm.ServerlessTypeQPS {
				initQuotaLocalCopy, _ := c.quotaclientset.ServerlessV1().Quotas(quotaLocalCopy.Namespace).Get(context.TODO(), quotaLocalCopy.Name, metaV1.GetOptions{})
				quotaLocalCopy.ResourceVersion = initQuotaLocalCopy.ResourceVersion
				_, err := c.quotaclientset.ServerlessV1().Quotas(quotaLocalCopy.Namespace).Update(context.TODO(), quotaLocalCopy, metaV1.UpdateOptions{})
				if err != nil {
					klog.Error(err)
					return err
				}
			}
			return nil
		})

		if retryErr != nil {
			klog.Errorf("cluster state update err: %v", retryErr)
			return retryErr
		}
	}
	return nil
}

func (c *Controller) ParentClusterStateUpdate(quotaLocalCopy *v1.Quota, clusterName string) error {
	remain, _ := strconv.Atoi(quotaLocalCopy.Labels["quota.cluster.pml.com.cn/quotaRemain"])
	require, _ := strconv.Atoi(quotaLocalCopy.Labels["quota.cluster.pml.com.cn/quotaRequire"])
	quota, _ := strconv.Atoi(quotaLocalCopy.Labels["quota.cluster.pml.com.cn/quota"])

	retryErr := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		//  Retrieve the latest version of Deployment before attempting update
		//  RetryOnConflict uses exponential backoff to avoid exhausting the apiserver
		if quotaLocalCopy.Labels["quota.cluster.pml.com.cn/type"] == comm.ServerlessTypeReplica || quotaLocalCopy.Labels["quota.cluster.pml.com.cn/type"] == comm.ServerlessTypeQPS {
			for _, ChildClusterState := range quotaLocalCopy.Spec.ChildClusterState {
				if ChildClusterState.ClusterName == clusterName {
					quotaLocalCopy.Spec.ChildClusterState[0].Quota = quota
					quotaLocalCopy.Spec.ChildClusterState[0].QuotaRemain = remain
					quotaLocalCopy.Spec.ChildClusterState[0].QuotaRequire = require
					if require == quota && remain == 0 {
						quotaLocalCopy.Spec.ChildClusterState[0].ClusterState = steady
					} else if require > quota {
						quotaLocalCopy.Spec.ChildClusterState[0].ClusterState = requireQ
					} else if require < quota {
						quotaLocalCopy.Spec.ChildClusterState[0].ClusterState = returnQ
					}

					initQuotaLocalCopy, _ := c.quotaclientset.ServerlessV1().Quotas(quotaLocalCopy.Namespace).Get(context.TODO(), quotaLocalCopy.Name, metaV1.GetOptions{})
					quotaLocalCopy.ResourceVersion = initQuotaLocalCopy.ResourceVersion
					_, err := c.quotaclientset.ServerlessV1().Quotas(quotaLocalCopy.Namespace).Update(context.TODO(), quotaLocalCopy, metaV1.UpdateOptions{})
					if err != nil {
						klog.Error(err)
						return err
					}
				}
			}
		}
		return nil
	})

	if retryErr != nil {
		klog.Errorf("scale up failed: %v", retryErr)
		return retryErr
	}
	return nil
}

func (c *Controller) podQpsQuotaAdd(i int, quotaLocalCopy *v1.Quota) error {
	// qpsIncreaseDecrease遍历，并将相关业务
	clusterConf, err := c.kubeclientset.CoreV1().ConfigMaps("serverless-system").Get(context.TODO(), "quota-conf", metaV1.GetOptions{})
	if err != nil {
		return err
	}
	remain, _ := strconv.Atoi(quotaLocalCopy.Labels["quota.cluster.pml.com.cn/quotaRemain"])
	step, _ := strconv.Atoi(quotaLocalCopy.Labels["quota.cluster.pml.com.cn/quotaStep"])
	quotaUsed, _ := strconv.Atoi(quotaLocalCopy.Labels["quota.cluster.pml.com.cn/quotaUsed"])
	quotaLocalCopy.Spec.PodQpsIncreaseOrDecrease[i].QpsIncreaseOrDecrease = 0
	quotaLocalCopy.Spec.PodQpsQuota[i].QpsQuota += step
	var qpsQuotaOfPod = quotaLocalCopy.Spec.PodQpsQuota[i].QpsQuota
	quotaLocalCopy.Labels["quota.cluster.pml.com.cn/quotaRemain"] = strconv.Itoa(remain - step)
	quotaLocalCopy.Labels["quota.cluster.pml.com.cn/quotaUsed"] = strconv.Itoa(quotaUsed + step)

	//  get the pod list
	//  https:// pkg.go.dev/k8s.io/client-go@v11.0.0+incompatible/kubernetes/typed/core/v1?tab=doc#PodInterface
	podInfo, _ := c.kubeclientset.CoreV1().Pods(quotaLocalCopy.Namespace).Get(context.TODO(), quotaLocalCopy.Spec.PodQpsQuota[i].PodName, metaV1.GetOptions{})

	// quota更新，加入新增pod的相关qpsQuota设置
	var hpaWebUrl = clusterConf.Data["qpsQuotaUrl"]
	// hpa update,	先拿到之前的podQpsQuota值再进行加1step，更新为最新的qpshpa
	var action = "update"
	err = c.qpsQuotaHpaAction(podInfo, quotaLocalCopy.Name, hpaWebUrl, action, int64(qpsQuotaOfPod), int64(step))
	if err != nil {
		klog.Error(err)
		klog.Error(err)
	}
	klog.Infof("quotaLocalCopy msg is : %s", quotaLocalCopy)
	initQuotaLocalCopy, _ := c.quotaclientset.ServerlessV1().Quotas(quotaLocalCopy.Namespace).Get(context.TODO(), quotaLocalCopy.Name, metaV1.GetOptions{})
	quotaLocalCopy.ResourceVersion = initQuotaLocalCopy.ResourceVersion
	_, err = c.quotaclientset.ServerlessV1().Quotas(quotaLocalCopy.Namespace).Update(context.TODO(), quotaLocalCopy, metaV1.UpdateOptions{})
	if err != nil {
		klog.Error(err)
	}
	return nil
}
