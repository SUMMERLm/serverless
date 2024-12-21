package quota

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"strconv"
	"time"

	"encoding/json"
	"net/http"

	"github.com/SUMMERLm/serverless/pkg/apis/serverless/v1"
	"github.com/SUMMERLm/serverless/pkg/comm"
	"github.com/SUMMERLm/serverless/pkg/etcd_lock"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"

	ioUtil "io"

	coreV1 "k8s.io/api/core/v1"
	metaV1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilRuntime "k8s.io/apimachinery/pkg/util/runtime"
)

const (
	oldClusterState    = "oldClusterState"
	newClusterState    = "newClusterState"
	updateClusterState = "updateClusterState"
)

const (
	steady   = "steady"
	requireQ = "require"
	returnQ  = "return"
)

// syncHandler compares the actual state with the desired, and attempts to
// converge the two. It then updates the Status block of the Quota resource
// with the current status of the resource.
func (c *Controller) syncHandler(key string) error {
	//  Convert the namespace/name string into a distinct namespace and name
	namespace, name, err := cache.SplitMetaNamespaceKey(key)
	if err != nil {
		utilRuntime.HandleError(fmt.Errorf("invalid resource key: %s", key))
		return nil
	}

	// clusterConf, err := c.kubeclientset.CoreV1().ConfigMaps("serverless-system").Get(context.TODO(), "quota-conf", metav1.GetOptions{})
	quota, err := c.quotasLister.Quotas(namespace).Get(name)
	if err != nil {
		utilRuntime.HandleError(fmt.Errorf("invalid local resource key: %s", key))
	}
	// 该quota cr被删除，则进入err ！=nil
	if err != nil {
		//  processing.
		if errors.IsNotFound(err) {
			klog.Infof("local Quota 对象被删除，上一层级执行相应对象资源回收业务: %s/%s ...", namespace, name)
			utilRuntime.HandleError(fmt.Errorf("Quota '%s' in work queue no longer exists", key))
			//  测试不删除上级quota CR，上线回收需要该处解决完hpa的apply问题后还原
			err := c.localQuotaDelete(namespace, name)
			if err != nil {
				klog.Infof("Delete target %q. Error: %s \n", quota.Name, err)
				return err
			}
			return nil
		}
	} else {
		// 该quota为新建或者更新
		// 根据本节点所属gaia的层级进行对应动作
		// *****关联关系:根据集群主节点主机名称进行识别，匹配对应hyperParentNode cr里面spec字段*****
		// gaia的crd获取本节点及对应父节点的关联关系，进行quota的cr刷选并在本地进行quota创建
		//  Get the parent quota resource with this namespace/name
		// klog.Info("quota-conf of quota is %s", clusterConf.Data["parentClusterName"])
		quotaCopy := quota.DeepCopy()
		quotaparent, err := c.quotasParentLister.Quotas(namespace).Get(name)
		// quota parent是否存在，不存在则新建，存在则聚合
		// 聚合网络注册信息：networkRegister
		// 聚合foundingmember 标签
		if err == nil {
			// 上级quota存在，更新上级quota
			// 更新操作：聚合本级的quota到上级quota，
			// 注意上级quota的分布式锁控制避免冲突
			// 判断该cr是不是还属于该集群：判断条件为spec字段，对齐新建操作。
			// 1:聚合网络标识，注册给rcs
			// 2:合并quota值
			// 3:quota申请或者回收逻辑，基于不用字段变化进行控制
			// 判断localquota为新建还是更新
			err := c.localQuotaUpdate(quotaparent, quotaCopy)
			if err != nil {
				klog.Infof("localQuotaUpdate error msg is: %s", err)
				return err
			}
			return nil
		} else {
			// 上级不存在，新建上级quota
			// 新建操作：多个cluster聚合上级field，多个field聚合上级global
			// 某个当前层级初始化新建一个上级的quota资源，剩余其他的当前层级更新上级quota资源
			klog.Infof("parent quota not exist ,start new parent quota New")
			err := c.localQuotaNew(quotaCopy, name, namespace)
			if err != nil {
				klog.Infof("localQuotaNew error msg is: %s", err)
				return err
			}
			return nil
		}
	}
	return nil
}

// enqueueQuota takes a Quota resource and converts it into a namespace/name
// string which is then put onto the work queue. This method should *not* be
// passed resources of any type other than Quota.
func (c *Controller) enqueueQuota(obj interface{}) {
	var key string
	var err error
	if key, err = cache.MetaNamespaceKeyFunc(obj); err != nil {
		utilRuntime.HandleError(err)
		return
	}
	c.workqueue.Add(key)
}

// 删除操作
func (c *Controller) enqueueQuotaForDelete(obj interface{}) {
	var key string
	var err error
	//  从缓存中删除指定对象
	key, err = cache.DeletionHandlingMetaNamespaceKeyFunc(obj)
	if err != nil {
		utilRuntime.HandleError(err)
		return
	}
	// 再将key放入队列
	c.workqueue.AddRateLimited(key)
}

func (c *Controller) localQuotaDelete(namespace string, name string) error {
	// 本级quota删除，则删除对应上级quota
	// 更新quota相关值，场景梳理S3
	quota, err := c.quotaParentclientset.ServerlessV1().Quotas(namespace).Get(context.TODO(), name, metaV1.GetOptions{})
	if err != nil {
		klog.Info(err)
	} else {
		// 对接网络接口进行scnID删除
		clusterConf, err := c.kubeclientset.CoreV1().ConfigMaps("serverless-system").Get(context.TODO(), "quota-conf", metaV1.GetOptions{})
		if err != nil {
			return err
		}
		if quota.Spec.ClusterAreaType == comm.ClusterAreaTypeField {
			var sidRegisterRemoveurl = clusterConf.Data["sidRegisterDelUrl"]
			err = c.sidNetworkRemove(quota, sidRegisterRemoveurl)
			if err != nil {
				klog.Info(err)
			}
		}
		err = c.quotaParentclientset.ServerlessV1().Quotas(namespace).Delete(context.TODO(), name, metaV1.DeleteOptions{})
		if err != nil {
			klog.Info(err)
		}
	}
	return nil
}

func (c *Controller) quotaLocalCopyOfParentState(quotaCopy *v1.Quota, quotaLocalCopyOfParent *v1.Quota) bool {
	var oldQpsQuotaDelete = false
	if len(quotaCopy.Spec.PodQpsQuota) > 0 {
		// klog.Infof("pod qps quota is: %s", quotaCopy.Spec.PodQpsQuota[0].ClusterName)
		// 新增的pod
		var numOfPodQpsInParent = 0
		// 遍历父级网络注册相关值
		if len(quotaLocalCopyOfParent.Spec.PodQpsQuota) > 0 {
			for _, podParentQpsQuota := range quotaLocalCopyOfParent.Spec.PodQpsQuota {
				if quotaCopy.Spec.PodQpsQuota[0].ClusterName == podParentQpsQuota.ClusterName {
					numOfPodQpsInParent++
				}
			}
			// klog.Infof("local length of quota is: %s", len(quotaCopy.Spec.PodQpsQuota))
			// klog.Infof("parent length of quota is: %s", numOfPodQpsInParent)
			if len(quotaCopy.Spec.PodQpsQuota) < numOfPodQpsInParent {
				oldQpsQuotaDelete = true
			}
		}
	}
	// cluster pod 为0，父节点还有该cluster的pod qps信息
	if len(quotaCopy.Spec.PodQpsQuota) == 0 {
		// klog.Infof("pod qps quota is: %s", quotaCopy.Spec.PodQpsQuota[0].ClusterName)
		// 新增的pod
		var numOfPodQpsInParent = 0
		// 遍历父级网络注册相关值
		if len(quotaLocalCopyOfParent.Spec.PodQpsQuota) > 0 {
			for _, podParentQpsQuota := range quotaLocalCopyOfParent.Spec.PodQpsQuota {
				if quotaCopy.Spec.LocalName == podParentQpsQuota.ClusterName {
					numOfPodQpsInParent++
				}
			}
			// klog.Infof("local length of quota is: %s", len(quotaCopy.Spec.PodQpsQuota))
			// klog.Infof("parent length of quota is: %s", numOfPodQpsInParent)
			if len(quotaCopy.Spec.PodQpsQuota) < numOfPodQpsInParent {
				oldQpsQuotaDelete = true
			}
		}
	}
	return oldQpsQuotaDelete
}

// 优化锁获取条件：变化取锁
func (c *Controller) localQuotaUpdate(quotaparent *v1.Quota, quotaCopy *v1.Quota) error {
	clusterConf, err := c.kubeclientset.CoreV1().ConfigMaps("serverless-system").Get(context.TODO(), "quota-conf", metaV1.GetOptions{})
	if err != nil {
		return err
	}
	var etcdEndpointQuota = []string{clusterConf.Data["etcdEndpoint"]}

	// time.Sleep(time.Duration(10) * time.Second)

	quota, err := c.quotasLister.Quotas(quotaCopy.Namespace).Get(quotaCopy.Name)
	if err != nil {
		klog.Errorf(err.Error())
		return err
	}
	quotaCopy = quota.DeepCopy()
	// 获得锁后重新拿最新的数据进行业务
	quotaparent, err = c.quotasParentLister.Quotas(quotaparent.Namespace).Get(quotaparent.Name)
	if err != nil {
		klog.Errorf(err.Error())
		return err
	}
	quotaLocalCopyOfParent := quotaparent.DeepCopy()
	quotaCopy.UID = ""
	//  哪些场景直接返回，不需要无效加锁做操作而是直接返回
	//  稳定状态直接返回，不需要走锁定及相关无效操作
	// case1 cluster级
	oldQPSQuotaDelete := false
	if quotaCopy.Spec.ClusterAreaType == comm.ClusterAreaTypeCluster {
		clusterQuotaRequire, _ := strconv.Atoi(quotaCopy.Labels["quota.cluster.pml.com.cn/quotaRequire"])
		clusterQuota, _ := strconv.Atoi(quotaCopy.Labels["quota.cluster.pml.com.cn/quota"])
		clusterQuotaRemain, _ := strconv.Atoi(quotaCopy.Labels["quota.cluster.pml.com.cn/quotaRemain"])
		fieldQuotaRequire, _ := strconv.Atoi(quotaLocalCopyOfParent.Labels["quota.cluster.pml.com.cn/quotaRequire"])
		fieldQuota, _ := strconv.Atoi(quotaLocalCopyOfParent.Labels["quota.cluster.pml.com.cn/quota"])
		fieldQuotaRemain, _ := strconv.Atoi(quotaLocalCopyOfParent.Labels["quota.cluster.pml.com.cn/quotaRemain"])
		//  networkRegister判断
		// 如果上级quota下的networkRegister有当前cluster的，则进行下面的是否直接返回，否则，继续进行后续
		if len(quotaCopy.Spec.NetworkRegister) > 0 {
			exist := false
			for _, value := range quotaLocalCopyOfParent.Spec.NetworkRegister {
				// 比较本地与上级所有遍历的值进行比较，如果都不相同，则进行注册
				if quotaCopy.Spec.NetworkRegister[0].Clustername == value.Clustername {
					exist = true
				}
			}
			if exist {
				// cluster级require等于quota，直接返回
				// 判断pod qps数据结构是否变化
				oldQPSQuotaDelete = c.quotaLocalCopyOfParentState(quotaCopy, quotaLocalCopyOfParent)
				klog.Info(oldQPSQuotaDelete)
				if !oldQPSQuotaDelete {
					if clusterQuotaRequire == clusterQuota && clusterQuotaRemain == 0 {
						return nil
					}
					if clusterQuotaRequire > clusterQuota && clusterQuotaRemain == 0 && fieldQuotaRequire > fieldQuota && fieldQuotaRemain == 0 {
						return nil
					}
				}
			}
		} else {
			// cluster级require等于quota，直接返回
			oldQPSQuotaDelete = c.quotaLocalCopyOfParentState(quotaCopy, quotaLocalCopyOfParent)
			klog.Info(oldQPSQuotaDelete)
			if !oldQPSQuotaDelete {
				if clusterQuotaRequire == clusterQuota && clusterQuotaRemain == 0 {
					return nil
				}
				if clusterQuotaRequire > clusterQuota && clusterQuotaRemain == 0 && fieldQuotaRequire > fieldQuota && fieldQuotaRemain == 0 {
					return nil
				}
			}
		}
		// cluster级require大于quota 同时 上级field require大于quota，且remain==0，直接返回
	} else if quotaCopy.Spec.ClusterAreaType == comm.ClusterAreaTypeField {
		fieldQuotaRequire, _ := strconv.Atoi(quotaCopy.Labels["quota.cluster.pml.com.cn/quotaRequire"])
		// klog.Infof("FieldQuotaRequire is: %s", fieldQuotaRequire)
		fieldQuota, _ := strconv.Atoi(quotaCopy.Labels["quota.cluster.pml.com.cn/quota"])
		fieldQuotaRemain, _ := strconv.Atoi(quotaCopy.Labels["quota.cluster.pml.com.cn/quotaRemain"])
		// klog.Infof("FieldQuota is: %s", fieldQuota)
		globalQuotaRemain, _ := strconv.Atoi(quotaLocalCopyOfParent.Labels["quota.cluster.pml.com.cn/quotaRemain"])
		// field级require等于quota，直接返回
		if fieldQuotaRequire == fieldQuota && fieldQuotaRemain == 0 {
			return nil
		}
		// field级require大于quota， 同时上级global quota remian使用完为0，则直接返回
		if fieldQuotaRequire > fieldQuota && globalQuotaRemain == 0 {
			return nil
		}
		state := true
		for i := 0; i < len(quotaCopy.Spec.ChildClusterState); i++ {
			if quotaCopy.Spec.ChildClusterState[i].ClusterState != steady {
				state = false
			}
		}
		if fieldQuotaRequire == fieldQuota && fieldQuotaRemain > 0 && !state {
			return nil
		}
	}
	// 当前为cluster级
	// 父级正在向global申请quota
	// 当前为field级。。。
	if locker, err := etcd_lock.New(etcdEndpointQuota, option); err != nil {
		klog.Errorf("创建锁失败：%+v", err)
		return err
	} else if who, ok := locker.Acquire(quotaCopy.Name + quotaCopy.Namespace); ok {
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
		// 获得锁后重新拿最新的数据进行业务
		// 本级新增相关数据，聚合到上级新增
		// 1:聚合网络标识，支持多个标识
		// 遍历本地网络注册相关值
		err = c.localQuotaUpdateNetwork(quotaCopy, quotaLocalCopyOfParent)
		if err != nil {
			klog.Errorf(err.Error())
			return err
		}
		// 2.0:quota扩容：先扩容，后申请及回收
		err = c.globalQuotaScale(quotaCopy, quotaLocalCopyOfParent)
		if err != nil {
			klog.Errorf(err.Error())
			return err
		}

		// 2:quota请求回收
		err = c.localQuotaRequireOrReturn(quotaCopy, quotaLocalCopyOfParent)
		if err != nil {
			klog.Errorf(err.Error())
			return err
		}

		// 3:聚合集群quota状态相关信息给上级

		err = c.localQuotaUpdateClusterMsg(quotaCopy, quotaLocalCopyOfParent)
		if err != nil {
			klog.Errorf(err.Error())
			return err
		}
		// 4:本地pod quota更新，对应更新quota注册信息
		err = c.localQuotaUpdateQuota(quotaCopy, quotaLocalCopyOfParent, oldQPSQuotaDelete)
		if err != nil {
			klog.Errorf(err.Error())
			return err
		}
		klog.Infof("进程 %+v 的任务处理完了", os.Getpid())
	} else {
		klog.Infof("获取锁失败，锁现在在 %+v 手中", who)
		return errors.NewBadRequest("cannot take etcd mux and requeue")
	}
	return nil
}

// local新建，上级parent无则新建，有则合并
// 按级别定制化
func (c *Controller) localQuotaNew(quotaCopy *v1.Quota, name string, namespace string) error {
	clusterConf, err := c.kubeclientset.CoreV1().ConfigMaps("serverless-system").Get(context.TODO(), "quota-conf", metaV1.GetOptions{})
	if err != nil {
		return err
	}
	var etcdEndpointQuota = []string{clusterConf.Data["etcdEndpoint"]}

	quotaCopy.UID = ""
	quotaCopy.ResourceVersion = ""
	quotaCopy.OwnerReferences = nil
	// 根据级别判断进行字段填充
	quotaCopy.Spec.LocalName = clusterConf.Data["parentClusterName"]
	if clusterConf.Data["clusterLevel"] == comm.ClusterAreaTypeCluster {
		// field quota初始化
		// field supervisor is global
		// time.Sleep(time.Duration(5) * time.Second)
		if locker, err := etcd_lock.New(etcdEndpointQuota, option); err != nil {
			klog.Errorf("创建锁失败：%+v", err)
			return err
		} else if who, ok := locker.Acquire(name + namespace); ok {
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
			//  判断上级quota是否已经存在
			quotaCopy.Labels["quota.cluster.pml.com.cn/init"] = comm.False
			_, err := c.kubeParentClientset.CoreV1().Namespaces().Get(context.TODO(), namespace, metaV1.GetOptions{})
			if err != nil {
				nameSpace := coreV1.Namespace{
					TypeMeta:   metaV1.TypeMeta{},
					ObjectMeta: metaV1.ObjectMeta{},
					Spec:       coreV1.NamespaceSpec{},
					Status:     coreV1.NamespaceStatus{},
				}
				nameSpace.Name = namespace
				_, errCreateNS := c.kubeParentClientset.CoreV1().Namespaces().Create(context.TODO(), &nameSpace, metaV1.CreateOptions{})
				if errCreateNS != nil {
					klog.Infof("new namespace error: %s", errCreateNS)
					return errCreateNS
				}
			}
			c.quotaclientset.ServerlessV1().Quotas(quotaCopy.Namespace).Update(context.TODO(), quotaCopy, metaV1.UpdateOptions{})
			//_, errQuotaUpdate := c.quotaclientset.ServerlessV1().Quotas(quotaCopy.Namespace).Update(context.TODO(), quotaCopy, metaV1.UpdateOptions{})
			//if errQuotaUpdate != nil {
			//	klog.Errorf(errQuotaUpdate.Error())
			//	return errQuotaUpdate
			//}
			quotaClusterParentOld, err := c.quotaParentclientset.ServerlessV1().Quotas(quotaCopy.Namespace).Get(context.TODO(), quotaCopy.Name, metaV1.GetOptions{})
			klog.Infof("check parent quota cr exist")
			if err != nil {
				// 不存在,新建
				klog.Infof("check parent quota cr exist: not exist")
				quotaCopy.Spec.SupervisorName = comm.ClusterAreaTypeGlobal
				quotaCopy.Spec.ClusterAreaType = comm.ClusterAreaTypeField
				quotaCopy.Labels["quota.cluster.pml.com.cn/init"] = comm.True
				if err != nil {
					klog.Infof("parent quota not exist, start create")
					quotaParent, errNewQuota := c.quotaParentclientset.ServerlessV1().Quotas(quotaCopy.Namespace).Create(context.TODO(), quotaCopy, metaV1.CreateOptions{})
					if errNewQuota != nil {
						klog.Errorf(errNewQuota.Error())
						return errNewQuota
					}
					klog.Infof("new parent quota error:%s", err)
					klog.Infof("parent quota not exist, done create")
					clusterConf, err = c.kubeclientset.CoreV1().ConfigMaps("serverless-system").Get(context.TODO(), "quota-conf", metaV1.GetOptions{})
					if err != nil {
						klog.Errorf(err.Error())
						return err
					}
					var sidRegisterURL = clusterConf.Data["sidRegisterUrl"]
					klog.Info("sid register while first create field quota cr")
					go func() {
						err := c.sidNetworkRegister(quotaParent, sidRegisterURL)
						if err != nil {
							klog.Error(err)
						}
					}()
					//err = c.sidNetworkRegister(quotaParent, sidRegisterURL)
					//if err != nil {
					//	klog.Errorf(err.Error())
					//	return err
					//}
					return errors.NewBadRequest("retry to require quota after init quota CR")
				} else {
					klog.Infof("parent quota exist, no need to create")
				}
			}
			klog.Infof(quotaClusterParentOld.Name)
			klog.Infof("进程 %+v 的任务处理完了", os.Getpid())
			return nil
		} else {
			klog.Infof("获取锁失败，锁现在在 %+v 手中", who)
			return errors.NewBadRequest("获取锁失败")
		}
	} else if clusterConf.Data["clusterLevel"] == comm.ClusterAreaTypeField {
		// time.Sleep(time.Duration(5) * time.Second)
		if locker, err := etcd_lock.New(etcdEndpointQuota, option); err != nil {
			klog.Errorf("创建锁失败：%+v", err)
			return err
		} else if who, ok := locker.Acquire(name + namespace); ok {
			defer func() {
				err = locker.Release()
				if err != nil {
					klog.Error(err)
				} else {
					klog.Infof("defer release done")
				}
			}()
			//  抢到锁后执行业务逻辑，没有抢到则退出
			//  判断上级quota是否已经存在
			quotaCopy.Labels["quota.cluster.pml.com.cn/init"] = comm.False
			// c.quotaclientset.ServerlessV1().Quotas(quotaCopy.Namespace).Update(context.TODO(), quotaCopy, metav1.UpdateOptions{})
			_, err := c.kubeParentClientset.CoreV1().Namespaces().Get(context.TODO(), namespace, metaV1.GetOptions{})
			if err != nil {
				nameSpace := coreV1.Namespace{
					TypeMeta:   metaV1.TypeMeta{},
					ObjectMeta: metaV1.ObjectMeta{},
					Spec:       coreV1.NamespaceSpec{},
					Status:     coreV1.NamespaceStatus{},
				}
				nameSpace.Name = namespace
				_, err = c.kubeParentClientset.CoreV1().Namespaces().Create(context.TODO(), &nameSpace, metaV1.CreateOptions{})
				if err != nil {
					klog.Infof("new namespace error: %s", err)
					return err
				}
			}
			quotaClusterParentOld, err := c.quotaParentclientset.ServerlessV1().Quotas(quotaCopy.Namespace).Get(context.TODO(), quotaCopy.Name, metaV1.GetOptions{})
			if err != nil {
				// 不存在,新建
				// global supervisor is null
				quotaCopy.Spec.SupervisorName = ""
				quotaCopy.Spec.ClusterAreaType = comm.ClusterAreaTypeGlobal
				quotaCopy.Labels["quota.cluster.pml.com.cn/init"] = comm.True
				//  global quota 	初始化
				globalQuota, _ := strconv.Atoi(quotaCopy.Labels["quota.cluster.pml.com.cn/globalQuota"])
				quota, _ := strconv.Atoi(quotaCopy.Labels["quota.cluster.pml.com.cn/quota"])
				// quotaUsed, _ := strconv.Atoi(quotaCopy.Labels["quota.cluster.pml.com.cn/quota"])
				quotaRequire, _ := strconv.Atoi(quotaCopy.Labels["quota.cluster.pml.com.cn/quota"])
				quotaCopy.Labels["quota.cluster.pml.com.cn/quota"] = strconv.Itoa(globalQuota)
				quotaCopy.Labels["quota.cluster.pml.com.cn/quotaRemain"] = strconv.Itoa(globalQuota - quota)
				// 上级quota使用多少为下级quota聚合
				quotaCopy.Labels["quota.cluster.pml.com.cn/quotaUsed"] = strconv.Itoa(quota)
				quotaCopy.Labels["quota.cluster.pml.com.cn/quotaRequire"] = strconv.Itoa(quotaRequire)
				quotaParent, err := c.quotaParentclientset.ServerlessV1().Quotas(quotaCopy.Namespace).Create(context.TODO(), quotaCopy, metaV1.CreateOptions{})
				klog.Info(quotaParent.Name)
				if err != nil {
					klog.Infof("new parent quota error:%s", err)
					klog.Errorf(err.Error())
					return err
				}
				return errors.NewBadRequest("retry to require quota after init quota CR")
			}
			klog.Info(quotaClusterParentOld.Name)
			klog.Infof("进程 %+v 的任务处理完了", os.Getpid())
		} else {
			klog.Infof("获取锁失败，锁现在在 %+v 手中", who)
			return errors.NewBadRequest("cannot get mux, need retry")
		}
	}
	// 先得到先进行创建
	return nil
}

func (c *Controller) localQuotaUpdateNetwork(quotaCopy *v1.Quota, quotaLocalCopyOfParent *v1.Quota) error {
	clusterConf, err := c.kubeclientset.CoreV1().ConfigMaps("serverless-system").Get(context.TODO(), "quota-conf", metaV1.GetOptions{})
	if err != nil {
		return err
	}
	if quotaCopy.Spec.ClusterAreaType == comm.ClusterAreaTypeCluster {
		var sidRegisterUrl = clusterConf.Data["sidRegisterUrl"]
		if len(quotaCopy.Spec.NetworkRegister) > 0 {
			for i := 0; i < len(quotaCopy.Spec.NetworkRegister); i++ {
				klog.Info(quotaCopy.Spec.NetworkRegister[i].Scnid)
				multiMsgOfNet := 0
				// 遍历父级网络注册相关值
				for _, value := range quotaLocalCopyOfParent.Spec.NetworkRegister {
					// 比较本地与上级所有遍历的值进行比较，如果都不相同，则进行注册
					if quotaCopy.Spec.NetworkRegister[i].Clustername == value.Clustername && quotaCopy.Spec.NetworkRegister[i].Scnid == value.Scnid {
						multiMsgOfNet++
						break
					} else {
						multiMsgOfNet = 0
					}
				}
				klog.Info(multiMsgOfNet)
				// 有新的scn进来，开启注册
				if multiMsgOfNet == 0 {
					klog.Infof("Add child cluster: %s  scnid: %s into parent cluster", quotaCopy.Spec.NetworkRegister[i].Clustername, quotaCopy.Spec.NetworkRegister[i].Scnid)
					quotaLocalCopyOfParent.Spec.NetworkRegister = append(quotaLocalCopyOfParent.Spec.NetworkRegister, quotaCopy.Spec.NetworkRegister[i])
					go func() {
						err := c.sidNetworkRegister(quotaLocalCopyOfParent, sidRegisterUrl)
						if err != nil {
							klog.Error(err)
						}
					}()

					//err := c.sidNetworkRegister(quotaLocalCopyOfParent, sidRegisterUrl)
					//if err != nil {
					//	return err
					//}
					initQuotaLocalCopyOfParent, _ := c.quotaParentclientset.ServerlessV1().Quotas(quotaLocalCopyOfParent.Namespace).Get(context.TODO(), quotaLocalCopyOfParent.Name, metaV1.GetOptions{})
					quotaLocalCopyOfParent.ResourceVersion = initQuotaLocalCopyOfParent.ResourceVersion
					_, err = c.quotaParentclientset.ServerlessV1().Quotas(quotaLocalCopyOfParent.Namespace).Update(context.TODO(), quotaLocalCopyOfParent, metaV1.UpdateOptions{})
					if err != nil {
						return err
					}
				}
			}
		}
	}
	return nil
}

// S2关注field级稳定状态下归还，只进行field级同步
func (c *Controller) localQuotaUpdateClusterMsg(quotaCopy *v1.Quota, quotaLocalCopyOfParent *v1.Quota) error {
	var clusterStateMsg string
	var numOfChild int
	if quotaCopy.Spec.ClusterAreaType == comm.ClusterAreaTypeCluster {
		if len(quotaCopy.Spec.ChildClusterState) > 0 {
			for i := 0; i < len(quotaCopy.Spec.ChildClusterState); i++ {
				klog.Info(quotaCopy.Spec.ChildClusterState[i].ClusterName)
				// 遍历父级集群状态相关值
				k := 0
				for j := 0; j < len(quotaLocalCopyOfParent.Spec.ChildClusterState); j++ {
					// 比较本地与上级所有遍历的值进行比较，如果都不相同，则进行注册
					if quotaCopy.Spec.ChildClusterState[i].ClusterName == quotaLocalCopyOfParent.Spec.ChildClusterState[j].ClusterName &&
						quotaCopy.Spec.ChildClusterState[i].ClusterState == quotaLocalCopyOfParent.Spec.ChildClusterState[j].ClusterState &&
						quotaCopy.Spec.ChildClusterState[i].QuotaRequire == quotaLocalCopyOfParent.Spec.ChildClusterState[j].QuotaRequire &&
						quotaCopy.Spec.ChildClusterState[i].Quota == quotaLocalCopyOfParent.Spec.ChildClusterState[j].Quota &&
						quotaCopy.Spec.ChildClusterState[i].QuotaRemain == quotaLocalCopyOfParent.Spec.ChildClusterState[j].QuotaRemain {
						clusterStateMsg = oldClusterState
						break
					} else if (quotaCopy.Spec.ChildClusterState[i].ClusterName == quotaLocalCopyOfParent.Spec.ChildClusterState[j].ClusterName &&
						quotaCopy.Spec.ChildClusterState[i].ClusterState != quotaLocalCopyOfParent.Spec.ChildClusterState[j].ClusterState) ||
						(quotaCopy.Spec.ChildClusterState[i].ClusterName == quotaLocalCopyOfParent.Spec.ChildClusterState[j].ClusterName &&
							quotaCopy.Spec.ChildClusterState[i].ClusterState == quotaLocalCopyOfParent.Spec.ChildClusterState[j].ClusterState &&
							(quotaCopy.Spec.ChildClusterState[i].Quota != quotaLocalCopyOfParent.Spec.ChildClusterState[j].Quota ||
								quotaCopy.Spec.ChildClusterState[i].QuotaRemain != quotaLocalCopyOfParent.Spec.ChildClusterState[j].QuotaRemain ||
								quotaCopy.Spec.ChildClusterState[i].QuotaRequire != quotaLocalCopyOfParent.Spec.ChildClusterState[j].QuotaRequire)) {
						clusterStateMsg = updateClusterState
						numOfChild = j
						break
					}
					k++
				}
				if k == len(quotaLocalCopyOfParent.Spec.ChildClusterState) {
					clusterStateMsg = newClusterState
				}
				// 有新的scn进来，开启注册
				klog.Infof(clusterStateMsg)
				if clusterStateMsg == newClusterState {
					// if locker, err := etcd_lock.New(etcdEndpointQuota, option); err != nil {
					// 	klog.Errorf("创建锁失败：%+v", err)
					// } else if who, ok := locker.Acquire(quotaCopy.Name + quotaCopy.Namespace); ok {
					// 	defer locker.Release()
					//  抢到锁后执行业务逻辑，没有抢到则退出
					// klog.Infof("进程 %+v 持有锁 %+v 正在处理任务中...", os.Getpid(), locker.GetId())
					klog.Infof("Add child clusterState clusterName %s clusterState  %s into parent cluster", quotaCopy.Spec.ChildClusterState[i].ClusterName, quotaCopy.Spec.ChildClusterState[i].ClusterState)
					quotaLocalCopyOfParent.Spec.ChildClusterState = append(quotaLocalCopyOfParent.Spec.ChildClusterState, quotaCopy.Spec.ChildClusterState[i])
					initQuotaLocalCopyOfParent, _ := c.quotaParentclientset.ServerlessV1().Quotas(quotaLocalCopyOfParent.Namespace).Get(context.TODO(), quotaLocalCopyOfParent.Name, metaV1.GetOptions{})
					quotaLocalCopyOfParent.ResourceVersion = initQuotaLocalCopyOfParent.ResourceVersion
					_, err := c.quotaParentclientset.ServerlessV1().Quotas(quotaLocalCopyOfParent.Namespace).Update(context.TODO(), quotaLocalCopyOfParent, metaV1.UpdateOptions{})
					if err != nil {
						return err
					}
					err = c.ClusterStateUpdate(quotaCopy)
					if err != nil {
						klog.Error(err)
					}
					return nil
					// } else {
					// 	klog.Infof("获取锁失败，锁现在在 %+v 手中", who)
					// 	return nil
					// }
				} else if clusterStateMsg == updateClusterState {
					// if locker, err := etcd_lock.New(etcdEndpointQuota, option); err != nil {
					// 	klog.Errorf("创建锁失败：%+v", err)
					// } else if who, ok := locker.Acquire(quotaCopy.Name + quotaCopy.Namespace); ok {
					// 	defer locker.Release()
					// 	//  抢到锁后执行业务逻辑，没有抢到则退出
					// 	klog.Infof("进程 %+v 持有锁 %+v 正在处理任务中...", os.Getpid(), locker.GetId())
					klog.Infof("update child clusterState : clsutername %s state  %s into parent cluster", quotaCopy.Spec.ChildClusterState[i].ClusterName, quotaCopy.Spec.ChildClusterState[i].ClusterState)

					// delete old
					quotaLocalCopyOfParent.Spec.ChildClusterState = append(quotaLocalCopyOfParent.Spec.ChildClusterState[0:numOfChild], quotaLocalCopyOfParent.Spec.ChildClusterState[numOfChild+1:]...)
					// add new
					quotaLocalCopyOfParent.Spec.ChildClusterState = append(quotaLocalCopyOfParent.Spec.ChildClusterState, quotaCopy.Spec.ChildClusterState[i])
					initQuotaLocalCopyOfParent, _ := c.quotaParentclientset.ServerlessV1().Quotas(quotaLocalCopyOfParent.Namespace).Get(context.TODO(), quotaLocalCopyOfParent.Name, metaV1.GetOptions{})
					quotaLocalCopyOfParent.ResourceVersion = initQuotaLocalCopyOfParent.ResourceVersion
					_, err := c.quotaParentclientset.ServerlessV1().Quotas(quotaLocalCopyOfParent.Namespace).Update(context.TODO(), quotaLocalCopyOfParent, metaV1.UpdateOptions{})
					if err != nil {
						return err
					}
					err = c.ClusterStateUpdate(quotaCopy)
					if err != nil {
						klog.Error(err)
					}
					return nil
				}
			}
		}
	}
	return nil
}

// (重复注册规避)
func (c *Controller) localQuotaUpdateQuota(quotaCopy *v1.Quota, quotaLocalCopyOfParent *v1.Quota, oldQPSQuotaDelete bool) error {
	// 2：聚合podQpsQuota
	clusterConf, err := c.kubeclientset.CoreV1().ConfigMaps("serverless-system").Get(context.TODO(), "quota-conf", metaV1.GetOptions{})
	if err != nil {
		return err
	}
	var registerQPSURL = clusterConf.Data["qpsRegisterUrl"]
	// 5：本级quotaQPs相关数据删除，上级相应删除（只处理cluster-field的相关数据，global暂时不处理）
	//   上级list里面过滤出当前级别对应的数据，与本级进行比较，如果本级不存在，则删除上级相应的数据
	//   根据级别判断进行字段填充
	// localClusterName := clusterConf.Data["localClusterName"]
	var newQPSQuota bool
	// 老的qps更新
	var oldQPSQuotaUpdate bool
	// 老的qps删除
	// var oldQpsQuotaDelete bool
	// 老的qps不变
	var oldQPSQuota bool
	// var quotaInit bool
	var newPodQPS int
	if clusterConf.Data["clusterLevel"] == comm.ClusterAreaTypeCluster {
		if len(quotaCopy.Spec.PodQpsQuota) > 0 {
			// 删除ParentQpsQuota
			for _, podQpsQuota := range quotaCopy.Spec.PodQpsQuota {
				klog.Infof("pod qps quota is: %s", podQpsQuota)
				// 新增的pod
				// 遍历父级网络注册相关值
				// var numOfPodQpsInParent int
				if len(quotaLocalCopyOfParent.Spec.PodQpsQuota) > 0 {
					for _, podParentQpsQuota := range quotaLocalCopyOfParent.Spec.PodQpsQuota {
						// if podQpsQuota.ClusterName == podParentQpsQuota.ClusterName {
						// 	numOfPodQpsInParent = numOfPodQpsInParent + 1
						// }
						if podQpsQuota.PodName == podParentQpsQuota.PodName && podQpsQuota.QpsQuota == podParentQpsQuota.QpsQuota {
							newPodQPS++
						}
					}
					for _, podParentQpsQuota := range quotaLocalCopyOfParent.Spec.PodQpsQuota {
						if podQpsQuota.PodName == podParentQpsQuota.PodName && podQpsQuota.QpsQuota != podParentQpsQuota.QpsQuota {
							oldQPSQuotaUpdate = true
							break
						}
						if podQpsQuota.PodName == podParentQpsQuota.PodName && podQpsQuota.QpsQuota == podParentQpsQuota.QpsQuota {
							oldQPSQuota = true
							break
						}
					}
					// if len(quotaCopy.Spec.PodQpsQuota) < numOfPodQpsInParent {
					// 	oldQpsQuotaDelete = true
					// }
					klog.Infof("newQpsQuota is: %s", newQPSQuota)
					klog.Infof("qps pod leng is: %s", newPodQPS)
					klog.Infof("quotaCopy.Spec.PodQpsQuota length is", len(quotaCopy.Spec.PodQpsQuota))
					if newPodQPS != len(quotaCopy.Spec.PodQpsQuota) {
						newQPSQuota = true
					} else {
						newQPSQuota = false
					}
					klog.Infof("newQpsQuota is: %s", newQPSQuota)
					if newQPSQuota || oldQPSQuotaUpdate || oldQPSQuotaDelete {
						break
					}
				} else {
					quotaLocalCopyOfParent.Spec.PodQpsQuota = quotaCopy.Spec.PodQpsQuota
					// TODO Test CHECK
					// quotaInit = true
					initQuotaLocalCopyOfParent, _ := c.quotaParentclientset.ServerlessV1().Quotas(quotaLocalCopyOfParent.Namespace).Get(context.TODO(), quotaLocalCopyOfParent.Name, metaV1.GetOptions{})
					quotaLocalCopyOfParent.ResourceVersion = initQuotaLocalCopyOfParent.ResourceVersion
					_, err = c.quotaParentclientset.ServerlessV1().Quotas(quotaLocalCopyOfParent.Namespace).Update(context.TODO(), quotaLocalCopyOfParent, metaV1.UpdateOptions{})
					if err != nil {
						return err
					}
					go func() {
						err := c.podQpsRegister(quotaLocalCopyOfParent, registerQPSURL)
						if err != nil {
							klog.Error(err)
						}
					}()
					//err = c.podQpsRegister(quotaLocalCopyOfParent, registerQPSURL)
					//if err != nil {
					//	return err
					//}
					return nil
				}
			}
			var numOfPodQpsInLocal = 0
			for _, podQpsQuota := range quotaCopy.Spec.PodQpsQuota {
				klog.Infof("pod qps quota is: %s", podQpsQuota)
				// 新增的pod
				var numOfPodQpsInParent = 0
				// 遍历父级网络注册相关值
				if len(quotaLocalCopyOfParent.Spec.PodQpsQuota) > 0 {
					for _, podParentQpsQuota := range quotaLocalCopyOfParent.Spec.PodQpsQuota {
						if podQpsQuota.ClusterName == podParentQpsQuota.ClusterName && podQpsQuota.PodName == podParentQpsQuota.PodName {
							numOfPodQpsInLocal++
						}
						if podQpsQuota.ClusterName == podParentQpsQuota.ClusterName {
							numOfPodQpsInParent++
						}
					}
					klog.Infof("local length of quota is: %s", len(quotaCopy.Spec.PodQpsQuota))
					klog.Infof("parent length of quota is: %s", numOfPodQpsInLocal)
					if len(quotaCopy.Spec.PodQpsQuota) < numOfPodQpsInParent {
						oldQPSQuotaDelete = true
					}
				}
			}

			// 新建，更新，删除的情况下进行
			klog.Infof("newQpsQuota is: %s", newQPSQuota)
			klog.Infof("oldQpsQuotaUpdate is: %s", oldQPSQuotaUpdate)
			klog.Infof("oldQpsQuota is: %s", oldQPSQuota)
			klog.Infof("oldQpsQuotaDelete is: %s", oldQPSQuotaDelete)

			if newQPSQuota || oldQPSQuotaUpdate || oldQPSQuotaDelete {
				for j := 0; j < len(quotaLocalCopyOfParent.Spec.PodQpsQuota); j++ {
					if quotaLocalCopyOfParent.Spec.PodQpsQuota[j].ClusterName == quotaCopy.Spec.LocalName {
						quotaLocalCopyOfParent.Spec.PodQpsQuota = append(quotaLocalCopyOfParent.Spec.PodQpsQuota[:j], quotaLocalCopyOfParent.Spec.PodQpsQuota[j+1:]...)
						j--
					}
				}
				quotaLocalCopyOfParent.Spec.PodQpsQuota = append(quotaLocalCopyOfParent.Spec.PodQpsQuota, quotaCopy.Spec.PodQpsQuota...)
				klog.Info(quotaLocalCopyOfParent.Spec.PodQpsQuota)
				initQuotaLocalCopyOfParent, _ := c.quotaParentclientset.ServerlessV1().Quotas(quotaLocalCopyOfParent.Namespace).Get(context.TODO(), quotaLocalCopyOfParent.Name, metaV1.GetOptions{})
				quotaLocalCopyOfParent.ResourceVersion = initQuotaLocalCopyOfParent.ResourceVersion
				_, err = c.quotaParentclientset.ServerlessV1().Quotas(quotaLocalCopyOfParent.Namespace).Update(context.TODO(), quotaLocalCopyOfParent, metaV1.UpdateOptions{})
				if err != nil {
					return err
				}
				// 调用注册接口对网络pod pqs信息注册
				go func() {
					err := c.podQpsRegister(quotaLocalCopyOfParent, registerQPSURL)
					if err != nil {
						klog.Error(err)
					}
				}()
				//err = c.podQpsRegister(quotaLocalCopyOfParent, registerQPSURL)
				//if err != nil {
				//	return err
				//}
			}
		} else if len(quotaCopy.Spec.PodQpsQuota) == 0 {
			// 删除ParentQpsQuota
			for j := 0; j < len(quotaLocalCopyOfParent.Spec.PodQpsQuota); j++ {
				if quotaLocalCopyOfParent.Spec.PodQpsQuota[j].ClusterName == quotaCopy.Spec.LocalName {
					quotaLocalCopyOfParent.Spec.PodQpsQuota = append(quotaLocalCopyOfParent.Spec.PodQpsQuota[:j], quotaLocalCopyOfParent.Spec.PodQpsQuota[j+1:]...)
					j--
				}
			}
			quotaLocalCopyOfParent.Spec.PodQpsQuota = append(quotaLocalCopyOfParent.Spec.PodQpsQuota, quotaCopy.Spec.PodQpsQuota...)
			klog.Info(quotaLocalCopyOfParent.Spec.PodQpsQuota)
			initQuotaLocalCopyOfParent, _ := c.quotaParentclientset.ServerlessV1().Quotas(quotaLocalCopyOfParent.Namespace).Get(context.TODO(), quotaLocalCopyOfParent.Name, metaV1.GetOptions{})
			quotaLocalCopyOfParent.ResourceVersion = initQuotaLocalCopyOfParent.ResourceVersion
			_, err = c.quotaParentclientset.ServerlessV1().Quotas(quotaLocalCopyOfParent.Namespace).Update(context.TODO(), quotaLocalCopyOfParent, metaV1.UpdateOptions{})
			if err != nil {
				klog.Error(err)
				return err
			}
			// 调用注册接口对网络pod pqs信息注册
			go func() {
				err := c.podQpsRegister(quotaLocalCopyOfParent, registerQPSURL)
				if err != nil {
					klog.Error(err)
				}
			}()
			//if err != nil {
			//	klog.Error(err)
			//	return err
			//}
		}
	}
	return nil
}

func (c *Controller) podQpsRegister(quota *v1.Quota, registerQpsUrl string) error {
	klog.Infof("start register pod qps msg")
	method := "POST"
	klog.Infof("%s pod qps quota msg is :%s ", quota.Name, quota.Spec.PodQpsQuota)
	byteDate, err := json.Marshal(quota.Spec.PodQpsQuota)
	if err != nil {
		return err
	}
	var payload = bytes.NewReader(byteDate)
	client := &http.Client{}
	// 本地测试，上线删除
	// registerQpsUrl = "http:// 127.0.0.1:8001/serverless/zeroalert"
	req, err := http.NewRequest(method, registerQpsUrl, payload)
	if err != nil {
		klog.Error(err)
		return err
	}
	req.Header.Add("Content-Type", "application/json")
	res, err := client.Do(req)
	if err != nil {
		klog.Error(err)
		return err
	}
	defer res.Body.Close()

	body, err := ioUtil.ReadAll(res.Body)
	if err != nil {
		klog.Error(err)
		return err
	}
	klog.Infof(string(body))
	klog.Infof("end register pod qps msg")
	return nil
}

func (c *Controller) sidNetworkRegister(quota *v1.Quota, sidRegisterUrl string) error {
	klog.Infof("start register sid msg")
	method := "POST"
	klog.Infof("%s quota sid msg  is :%s ", quota.Name, quota.Spec.NetworkRegister)
	byteDate, err := json.Marshal(quota.Spec.NetworkRegister)
	if err != nil {
		return err
	}
	var payload = bytes.NewReader(byteDate)
	client := &http.Client{}
	//  本地测试，上线删除
	// sidRegisterUrl = "http:// 127.0.0.1:8001/serverless/zeroalert"
	req, err := http.NewRequest(method, sidRegisterUrl, payload)
	if err != nil {
		klog.Error(err)
		return err
	}
	req.Header.Add("Content-Type", "application/json")
	res, err := client.Do(req)
	if err != nil {
		klog.Error(err)
		return err
	}
	defer res.Body.Close()

	body, err := ioUtil.ReadAll(res.Body)
	if err != nil {
		klog.Error(err)
		return err
	}
	klog.Infof(string(body))
	klog.Infof("end register sid msg")

	return nil
}

func (c *Controller) sidNetworkRemove(quota *v1.Quota, sidRegisterDelUrl string) error {
	klog.Infof("start remove register sid msg")
	method := "DELETE"
	klog.Infof("%s quota sid msg  is :%s ", quota.Name, quota.Spec.NetworkRegister)
	byteDate, err := json.Marshal(quota.Spec.NetworkRegister)
	if err != nil {
		return err
	}
	var payload = bytes.NewReader(byteDate)
	client := &http.Client{}
	//  本地测试，上线删除
	// sidRegisterUrl = "http:// 127.0.0.1:8001/serverless/zeroalert"
	req, err := http.NewRequest(method, sidRegisterDelUrl, payload)
	if err != nil {
		return err
	}
	req.Header.Add("Content-Type", "application/json")
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()

	body, err := ioUtil.ReadAll(res.Body)
	if err != nil {
		return err
	}
	klog.Infof(string(body))
	klog.Infof("end remove register sid msg")

	return nil
}

func (c *Controller) localQuotaRequireOrReturn(quotaCopy *v1.Quota, quotaLocalCopyOfParent *v1.Quota) error {
	clusterConf, err := c.kubeclientset.CoreV1().ConfigMaps("serverless-system").Get(context.TODO(), "quota-conf", metaV1.GetOptions{})
	if err != nil {
		return err
	}
	// 5：本级quotaQPs相关数据删除，上级相应删除（只处理cluster-field的相关数据，global暂时不处理）
	//   上级list里面过滤出当前级别对应的数据，与本级进行比较，如果本级不存在，则删除上级相应的数据
	//   根据级别判断进行字段填充
	localClusterName := clusterConf.Data["localClusterName"]
	// qps类型，pod qps管理
	if clusterConf.Data["clusterLevel"] == comm.ClusterAreaTypeCluster && quotaLocalCopyOfParent.Labels["quota.cluster.pml.com.cn/type"] == comm.ServerlessTypeQPS {
		quotaCopy.UID = ""
		for i := 0; i < len(quotaLocalCopyOfParent.Spec.PodQpsQuota); i++ {
			var in = false
			if quotaLocalCopyOfParent.Spec.PodQpsQuota[i].ClusterName == localClusterName {
				for _, podQqsQuota := range quotaCopy.Spec.PodQpsQuota {
					if quotaLocalCopyOfParent.Spec.PodQpsQuota[i].PodName == podQqsQuota.PodName {
						in = true
					}
				}
				if !in {
					quotaLocalCopyOfParent.Spec.PodQpsQuota = append(quotaLocalCopyOfParent.Spec.PodQpsQuota[0:i], quotaLocalCopyOfParent.Spec.PodQpsQuota[i+1:]...)
				}
			}
		}
		err := c.ClusterStateUpdate(quotaCopy)
		if err != nil {
			klog.Error(err)
		}
	}

	// 6:quota申请及回收
	// 场景cluster级quota申请c-f-g c-f，注意判断级别，级别对应的动作不同
	//   0-1触发，或者其他扩容请求导致的扩容
	// if locker, err := etcd_lock.New(etcdEndpointQuota, option); err != nil {
	// 	klog.Errorf("创建锁失败：%+v", err)
	// } else if who, ok := locker.Acquire(quotaCopy.Name + quotaCopy.Namespace); ok {
	// 	defer locker.Release()
	// 	//  抢到锁后执行业务逻辑，没有抢到则退出
	// 	klog.Infof("进程 %+v 持有锁 %+v 正在处理任务中...", os.Getpid(), locker.GetId())
	if clusterConf.Data["clusterLevel"] == comm.ClusterAreaTypeCluster {
		err := c.localQuotaRequireOrReturnCluster(quotaCopy, quotaLocalCopyOfParent)
		if err != nil {
			klog.Info(err)
			return err
		}
		return nil
	} else if clusterConf.Data["clusterLevel"] == comm.ClusterAreaTypeField {
		err := c.localQuotaRequireOrReturnField(quotaCopy, quotaLocalCopyOfParent)
		if err != nil {
			klog.Info(err)
			return err
		}
		return nil
	}
	// } else {
	// 	klog.Infof("获取锁失败，锁现在在 %+v 手中", who)
	// 	return nil
	// }
	return nil
}

func (c *Controller) localQuotaRequireOrReturnCluster(quotaCopy *v1.Quota, quotaLocalCopyOfParent *v1.Quota) error {
	//  c-f
	// cluser级申请，关联field级quota以及本级quota和pod扩缩
	clusterQuotaRequire, _ := strconv.Atoi(quotaCopy.Labels["quota.cluster.pml.com.cn/quotaRequire"])
	klog.Infof("clusterQuotaRequire is: %s", clusterQuotaRequire)
	clusterQuota, _ := strconv.Atoi(quotaCopy.Labels["quota.cluster.pml.com.cn/quota"])
	clusterQuotaUsed, _ := strconv.Atoi(quotaCopy.Labels["quota.cluster.pml.com.cn/quotaUsed"])
	klog.Infof("clusterQuota is: %s", clusterQuota)
	clusterQuotaRemain, _ := strconv.Atoi(quotaCopy.Labels["quota.cluster.pml.com.cn/quotaRemain"])
	fieldQuotaRequire, _ := strconv.Atoi(quotaLocalCopyOfParent.Labels["quota.cluster.pml.com.cn/quotaRequire"])
	fieldQuotaRemain, _ := strconv.Atoi(quotaLocalCopyOfParent.Labels["quota.cluster.pml.com.cn/quotaRemain"])
	fieldQuotaUsed, _ := strconv.Atoi(quotaLocalCopyOfParent.Labels["quota.cluster.pml.com.cn/quotaUsed"])
	fieldQuota, _ := strconv.Atoi(quotaLocalCopyOfParent.Labels["quota.cluster.pml.com.cn/quota"])

	// quotaStep, _ := strconv.Atoi(quotaCopy.Labels["quota.cluster.pml.com.cn/Step"])

	klog.Infof("clusterQuotaRemain is: %s", clusterQuotaRemain)
	if quotaLocalCopyOfParent.Labels["quota.cluster.pml.com.cn/type"] != comm.ServerlessTypeNoLimit {
		if clusterQuotaRequire == clusterQuota && clusterQuotaRemain == clusterQuotaRequire && quotaCopy.Labels["quota.cluster.pml.com.cn/deployScale"] == comm.False {
			if quotaLocalCopyOfParent.Labels["quota.cluster.pml.com.cn/type"] == comm.ServerlessTypeQPS {
				if len(quotaCopy.Spec.PodQpsIncreaseOrDecrease) == 0 {
					quotaCopy.Labels["quota.cluster.pml.com.cn/quotaRequire"] = strconv.Itoa(0)
					clusterQuotaRequire = 0
				} else if len(quotaCopy.Spec.PodQpsIncreaseOrDecrease) > 0 {
					qpsManageIsZero := true
					for _, PodQpsIncreaseOrDecreaseSpec := range quotaCopy.Spec.PodQpsIncreaseOrDecrease {
						if PodQpsIncreaseOrDecreaseSpec.QpsIncreaseOrDecrease == 1 {
							qpsManageIsZero = false
							break
						}
					}
					if qpsManageIsZero {
						quotaCopy.Labels["quota.cluster.pml.com.cn/quotaRequire"] = strconv.Itoa(0)
						clusterQuotaRequire = 0
					}
				}
			} else if quotaLocalCopyOfParent.Labels["quota.cluster.pml.com.cn/type"] == comm.ServerlessTypeReplica {
				quotaCopy.Labels["quota.cluster.pml.com.cn/quotaRequire"] = strconv.Itoa(0)
				clusterQuotaRequire = 0
			}
		}
		var errLabelQuotaRemain error
		if clusterQuotaRemain > 0 && c.revisionCheck(quotaCopy, quotaLocalCopyOfParent) {
			// 本地有quota需求的优先使用本地已有的资源
			// 本地需要使用的地方先使用，不够的申请，多余的归还
			// cluster级quotaremian存在，同时有pod申请或者
			klog.Infof("remain quota local")
			// 得到quota后进行相关业务操作 按场景进行判断，扩容完成后进行quota的对应变更(优先级管理)
			if quotaCopy.Labels["quota.cluster.pml.com.cn/foundingMember"] == comm.True && quotaCopy.Labels["quota.cluster.pml.com.cn/init"] == comm.True {
				// 场景0 元老实例
				clusterQuotaRemain, errLabelQuotaRemain = strconv.Atoi(quotaCopy.Labels["quota.cluster.pml.com.cn/quotaRemain"])
				if errLabelQuotaRemain != nil {
					klog.Error(errLabelQuotaRemain)
				}
				if clusterQuotaRemain > 0 {
					err := c.foundingMemberPodInit(quotaCopy)
					if err != nil {
						klog.Error(err)
					}
				}
			}
			// 先扩容deploy
			if quotaCopy.Labels["quota.cluster.pml.com.cn/deployScale"] == comm.True {
				// 场景2 deploy scale up
				// deploy加1个replica
				// quota相关属性值变更
				// quotaCopy.Labels["quota.cluster.pml.com.cn/quota"] = strconv.Itoa(clusterQuota + clusterQuotaRequireMore)
				// quotaCopy.Labels["quota.cluster.pml.com.cn/quotaRemain"] = strconv.Itoa(clusterQuotaRequireMore)
				clusterQuotaRemain, errLabelQuotaRemain = strconv.Atoi(quotaCopy.Labels["quota.cluster.pml.com.cn/quotaRemain"])
				if errLabelQuotaRemain != nil {
					klog.Error(errLabelQuotaRemain)
				}
				if clusterQuotaRemain > 0 {
					err := c.scaleUpDeploy(quotaCopy)
					if err != nil {
						klog.Error(err)
					}
				}
			}
			clusterQuotaRemain, errLabelQuotaRemain = strconv.Atoi(quotaCopy.Labels["quota.cluster.pml.com.cn/quotaRemain"])
			if errLabelQuotaRemain != nil {
				klog.Error(errLabelQuotaRemain)
			}
			if quotaLocalCopyOfParent.Labels["quota.cluster.pml.com.cn/type"] == comm.ServerlessTypeQPS && len(quotaCopy.Spec.PodQpsIncreaseOrDecrease) > 0 && clusterQuotaRemain > 0 {
				for i, PodQpsIncreaseOrDecreaseSpec := range quotaCopy.Spec.PodQpsIncreaseOrDecrease {
					clusterQuotaRemain, _ = strconv.Atoi(quotaCopy.Labels["quota.cluster.pml.com.cn/quotaRemain"])
					if PodQpsIncreaseOrDecreaseSpec.QpsIncreaseOrDecrease > 0 && clusterQuotaRemain > 0 {
						// quotaCopy.Labels["quota.cluster.pml.com.cn/quota"] = strconv.Itoa(clusterQuota + clusterQuotaRequireMore)
						// quotaCopy.Labels["quota.cluster.pml.com.cn/quotaRemain"] = strconv.Itoa(clusterQuotaRequireMore)
						// quota相关值更新,遍历多个podQpsQuota计算
						clusterQuotaRemain, _ = strconv.Atoi(quotaCopy.Labels["quota.cluster.pml.com.cn/quotaRemain"])
						if clusterQuotaRemain > 0 {
							err := c.podQpsQuotaAdd(i, quotaCopy)
							if err != nil {
								klog.Error(err)
							}
						}
					}
				}
			}
			err := c.ClusterStateUpdate(quotaCopy)
			if err != nil {
				klog.Error(err)
			}
			err = c.ParentClusterStateUpdate(quotaLocalCopyOfParent, quotaCopy.Name)
			if err != nil {
				klog.Error(err)
			}
			// 本地quotaremian用于给正在申请podqps扩容的pod直接使用，用不完了返回
		}

		if clusterQuotaRequire > clusterQuota && clusterQuotaRemain == 0 && c.revisionCheck(quotaCopy, quotaLocalCopyOfParent) {
			// 本地没有剩余资源，需求向上申请
			clusterQuotaRequireMore := clusterQuotaRequire - clusterQuota
			fieldQuotaRequire, _ = strconv.Atoi(quotaLocalCopyOfParent.Labels["quota.cluster.pml.com.cn/quotaRequire"])
			klog.Info("fieldQuotaRequire is: %s", fieldQuotaRequire)
			fieldQuota, _ = strconv.Atoi(quotaLocalCopyOfParent.Labels["quota.cluster.pml.com.cn/quota"])
			klog.Info("fieldQuota is:%s", fieldQuota)
			fieldQuotaRemain, _ = strconv.Atoi(quotaLocalCopyOfParent.Labels["quota.cluster.pml.com.cn/quotaRemain"])
			klog.Info("fieldQuotaRemain is:%s", fieldQuotaRemain)

			if fieldQuotaRemain > 0 {
				// field级当前有剩余配额，直接分配
				klog.Infof("field级当前有剩余配额，直接分配,fieldQuotaRemain is:%s", fieldQuotaRemain)
				if fieldQuotaRemain >= clusterQuotaRequireMore {
					// case1 足够分配
					// TODO 直接变更quota 相关属性信息 取代 变更单个属性后加减，解决重复申请的问题
					fieldQuotaUsed, _ = strconv.Atoi(quotaLocalCopyOfParent.Labels["quota.cluster.pml.com.cn/quotaUsed"])

					fieldQuotaRemain -= clusterQuotaRequireMore
					fieldQuotaUsed += clusterQuotaRequireMore
					// fieldQuotaRequire此刻大于fieldQuota：正在申请新的quota,fieldQuotaRequire不变
					// fieldQuotaRequire此刻小于fieldQuota：正在返还不用的quota，fieldQuotaRequire增加本次用掉的，不需要返还用掉的部分，同时继续返还剩余未用掉的部分。
					if fieldQuotaRequire < fieldQuota {
						fieldQuotaRequire += clusterQuotaRequireMore
					}
					// 申请得到，field级quota按需变更
					quotaLocalCopyOfParent.Labels["quota.cluster.pml.com.cn/quotaRemain"] = strconv.Itoa(fieldQuotaRemain)
					quotaLocalCopyOfParent.Labels["quota.cluster.pml.com.cn/quotaUsed"] = strconv.Itoa(fieldQuotaUsed)
					quotaLocalCopyOfParent.Labels["quota.cluster.pml.com.cn/quotaRequire"] = strconv.Itoa(fieldQuotaRequire)

					initQuotaLocalCopyOfParent, err := c.quotaParentclientset.ServerlessV1().Quotas(quotaLocalCopyOfParent.Namespace).Get(context.TODO(), quotaLocalCopyOfParent.Name, metaV1.GetOptions{})
					if err != nil {
						klog.Error(err)
						return err
					}
					quotaLocalCopyOfParent.ResourceVersion = initQuotaLocalCopyOfParent.ResourceVersion
					_, err = c.quotaParentclientset.ServerlessV1().Quotas(quotaLocalCopyOfParent.Namespace).Update(context.TODO(), quotaLocalCopyOfParent, metaV1.UpdateOptions{})
					if err != nil {
						return err
					}
					// 申请得到：cluster级quota按需变更
					quotaCopy.Labels["quota.cluster.pml.com.cn/quota"] = strconv.Itoa(clusterQuotaRequire)
					// quotaCopy.Labels["quota.cluster.pml.com.cn/quota"] = strconv.Itoa(clusterQuota + clusterQuotaRequireMore)
					quotaCopy.Labels["quota.cluster.pml.com.cn/quotaRemain"] = strconv.Itoa(clusterQuotaRequireMore)
					// 得到quota后进行相关业务操作 按场景进行判断，扩容完成后进行quota的对应变更
					//  foundIngmember
					if quotaCopy.Labels["quota.cluster.pml.com.cn/foundingMember"] == comm.True && quotaCopy.Labels["quota.cluster.pml.com.cn/init"] == comm.True {
						// 场景0 元老实例

						err = c.foundingMemberPodInit(quotaCopy)
						if err != nil {
							klog.Error(err)
							return err
						}
						// return nil
					}
					clusterQuotaRemain, _ = strconv.Atoi(quotaCopy.Labels["quota.cluster.pml.com.cn/quotaRemain"])
					if len(quotaCopy.Spec.ChildAlert) > 0 && clusterQuotaRemain > 0 {
						if quotaCopy.Spec.ChildAlert[0].Alert {
							// 场景1 0-1场景优先
							err = c.zeroAlertDeploy(quotaCopy)
							if err != nil {
								klog.Error(err)
								return err
							}
						}
					}
					clusterQuotaRemain, _ = strconv.Atoi(quotaCopy.Labels["quota.cluster.pml.com.cn/quotaRemain"])
					if quotaCopy.Labels["quota.cluster.pml.com.cn/deployScale"] == comm.True && clusterQuotaRemain > 0 {
						// 场景2 deploy scale up
						// deploy加1个replica
						// quota相关属性值变更
						// quotaCopy.Labels["quota.cluster.pml.com.cn/quota"] = strconv.Itoa(clusterQuota + clusterQuotaRequireMore)
						// quotaCopy.Labels["quota.cluster.pml.com.cn/quotaRemain"] = strconv.Itoa(clusterQuotaRequireMore)
						err = c.scaleUpDeploy(quotaCopy)
						if err != nil {
							klog.Error(err)
						}
					}
					clusterQuotaRemain, _ = strconv.Atoi(quotaCopy.Labels["quota.cluster.pml.com.cn/quotaRemain"])
					if quotaLocalCopyOfParent.Labels["quota.cluster.pml.com.cn/type"] == comm.ServerlessTypeQPS && clusterQuotaRemain > 0 {
						for i, PodQpsIncreaseOrDecreaseSpec := range quotaCopy.Spec.PodQpsIncreaseOrDecrease {
							if PodQpsIncreaseOrDecreaseSpec.QpsIncreaseOrDecrease > 0 {
								// quotaCopy.Labels["quota.cluster.pml.com.cn/quota"] = strconv.Itoa(clusterQuota + clusterQuotaRequireMore)
								// quotaCopy.Labels["quota.cluster.pml.com.cn/quotaRemain"] = strconv.Itoa(clusterQuotaRequireMore)
								// quota相关值更新,遍历多个podQpsQuota计算
								err = c.podQpsQuotaAdd(i, quotaCopy)
								if err != nil {
									klog.Error(err)
								}
							}
						}
					}
					err = c.ClusterStateUpdate(quotaCopy)
					if err != nil {
						klog.Error(err)
					}
					err = c.ParentClusterStateUpdate(quotaLocalCopyOfParent, quotaCopy.Name)
					if err != nil {
						klog.Error(err)
					}
				} else {
					// case2 field级分配完，cluster级quota仍然不够
					//  field级quotaRemain变成0，都分配给cluster级
					fieldQuotaUsed, _ = strconv.Atoi(quotaLocalCopyOfParent.Labels["quota.cluster.pml.com.cn/quotaUsed"])
					fieldQuotaUsed += fieldQuotaRemain
					// 申请得到：cluster级quota按需变更
					// 申请得到的quota加上原有的quota
					quotaCopy.Labels["quota.cluster.pml.com.cn/quota"] = strconv.Itoa(clusterQuota + fieldQuotaRemain)
					quotaCopy.Labels["quota.cluster.pml.com.cn/quotaRemain"] = strconv.Itoa(fieldQuotaRemain)
					// fieldQuotaRequire此刻大于fieldQuota：正在申请新的quota,fieldQuotaRequire不变
					// fieldQuotaRequire此刻小于fieldQuota：正在返还不用的quota，fieldQuotaRequire增加本次用掉的，不需要返还用掉的部分，同时继续返还剩余未用掉的部分。
					if fieldQuotaRequire <= fieldQuota {
						fieldQuotaRequire += clusterQuotaRequireMore - fieldQuotaRemain
					}
					fieldQuotaRemain = 0
					// 申请得到，field级quota按需变更
					quotaLocalCopyOfParent.Labels["quota.cluster.pml.com.cn/quotaRemain"] = strconv.Itoa(fieldQuotaRemain)
					quotaLocalCopyOfParent.Labels["quota.cluster.pml.com.cn/quotaUsed"] = strconv.Itoa(fieldQuotaUsed)
					quotaLocalCopyOfParent.Labels["quota.cluster.pml.com.cn/quotaRequire"] = strconv.Itoa(fieldQuotaRequire)
					initQuotaLocalCopyOfParent, _ := c.quotaParentclientset.ServerlessV1().Quotas(quotaLocalCopyOfParent.Namespace).Get(context.TODO(), quotaLocalCopyOfParent.Name, metaV1.GetOptions{})
					quotaLocalCopyOfParent.ResourceVersion = initQuotaLocalCopyOfParent.ResourceVersion
					_, err := c.quotaParentclientset.ServerlessV1().Quotas(quotaLocalCopyOfParent.Namespace).Update(context.TODO(), quotaLocalCopyOfParent, metaV1.UpdateOptions{})
					if err != nil {
						klog.Error(err)
					}
					// 得到quota后进行相关业务操作 按场景进行判断，扩容完成后进行quota的对应变更(优先级管理)
					if quotaCopy.Labels["quota.cluster.pml.com.cn/foundingMember"] == comm.True && quotaCopy.Labels["quota.cluster.pml.com.cn/init"] == comm.True {
						// 场景0 元老实例
						clusterQuotaRemain, _ = strconv.Atoi(quotaCopy.Labels["quota.cluster.pml.com.cn/quotaRemain"])
						if clusterQuotaRemain > 0 {
							err = c.foundingMemberPodInit(quotaCopy)
							if err != nil {
								klog.Error(err)
								return err
							}
						}
					}
					clusterQuotaRemain, _ = strconv.Atoi(quotaCopy.Labels["quota.cluster.pml.com.cn/quotaRemain"])
					if len(quotaCopy.Spec.ChildAlert) > 0 && clusterQuotaRemain > 0 {
						if quotaCopy.Spec.ChildAlert[0].Alert {
							// 场景1 0-1场景优先
							clusterQuotaRemain, _ = strconv.Atoi(quotaCopy.Labels["quota.cluster.pml.com.cn/quotaRemain"])
							if clusterQuotaRemain > 0 {
								err = c.zeroAlertDeploy(quotaCopy)
								if err != nil {
									klog.Error(err)
									return err
								}
							}
						}
					}
					clusterQuotaRemain, _ = strconv.Atoi(quotaCopy.Labels["quota.cluster.pml.com.cn/quotaRemain"])
					if quotaCopy.Labels["quota.cluster.pml.com.cn/deployScale"] == comm.True && clusterQuotaRemain > 0 {
						// 场景2 deploy scale up
						// deploy加1个replica
						// quota相关属性值变更
						// quotaCopy.Labels["quota.cluster.pml.com.cn/quota"] = strconv.Itoa(clusterQuota + clusterQuotaRequireMore)
						// quotaCopy.Labels["quota.cluster.pml.com.cn/quotaRemain"] = strconv.Itoa(clusterQuotaRequireMore)
						clusterQuotaRemain, _ = strconv.Atoi(quotaCopy.Labels["quota.cluster.pml.com.cn/quotaRemain"])
						if clusterQuotaRemain > 0 {
							err = c.scaleUpDeploy(quotaCopy)
							if err != nil {
								klog.Error(err)
								return err
							}
						}
					}
					clusterQuotaRemain, _ = strconv.Atoi(quotaCopy.Labels["quota.cluster.pml.com.cn/quotaRemain"])
					if quotaLocalCopyOfParent.Labels["quota.cluster.pml.com.cn/type"] == comm.ServerlessTypeQPS && len(quotaCopy.Spec.PodQpsIncreaseOrDecrease) > 0 && clusterQuotaRemain > 0 {
						for i, PodQpsIncreaseOrDecreaseSpec := range quotaCopy.Spec.PodQpsIncreaseOrDecrease {
							clusterQuotaRemain, _ = strconv.Atoi(quotaCopy.Labels["quota.cluster.pml.com.cn/quotaRemain"])
							if PodQpsIncreaseOrDecreaseSpec.QpsIncreaseOrDecrease > 0 && clusterQuotaRemain > 0 {
								// quotaCopy.Labels["quota.cluster.pml.com.cn/quota"] = strconv.Itoa(clusterQuota + clusterQuotaRequireMore)
								// quotaCopy.Labels["quota.cluster.pml.com.cn/quotaRemain"] = strconv.Itoa(clusterQuotaRequireMore)
								// quota相关值更新,遍历多个podQpsQuota计算
								clusterQuotaRemain, _ = strconv.Atoi(quotaCopy.Labels["quota.cluster.pml.com.cn/quotaRemain"])
								if clusterQuotaRemain > 0 {
									err = c.podQpsQuotaAdd(i, quotaCopy)
									if err != nil {
										klog.Error(err)
										return err
									}
								}
							}
						}
					}
					err = c.ClusterStateUpdate(quotaCopy)
					if err != nil {
						klog.Error(err)
						return err
					}
					err = c.ParentClusterStateUpdate(quotaLocalCopyOfParent, quotaCopy.Name)
					if err != nil {
						klog.Error(err)
						return err
					}
				}
			} else {
				// field级当前无剩余配额，field级向global申请
				// 变更field级quota相关属性。 quotaRequire增加
				// Done
				fieldQuotaRequire, _ = strconv.Atoi(quotaLocalCopyOfParent.Labels["quota.cluster.pml.com.cn/quotaRequire"])
				QuotaStep, _ := strconv.Atoi(quotaLocalCopyOfParent.Labels["quota.cluster.pml.com.cn/quotaStep"])
				clusterQuotaRequireMore = clusterQuotaRequire - clusterQuota
				// 正在申请，还未申请到，下级再有的申请不管
				// 如果现在已经处于平衡，申请粒度为当前所需的more值
				if fieldQuotaRequire-fieldQuota < QuotaStep {
					fieldQuotaRequire += clusterQuotaRequireMore
					quotaLocalCopyOfParent.Labels["quota.cluster.pml.com.cn/quotaRequire"] = strconv.Itoa(fieldQuotaRequire)
					initQuotaLocalCopyOfParent, _ := c.quotaParentclientset.ServerlessV1().Quotas(quotaLocalCopyOfParent.Namespace).Get(context.TODO(), quotaLocalCopyOfParent.Name, metaV1.GetOptions{})
					quotaLocalCopyOfParent.ResourceVersion = initQuotaLocalCopyOfParent.ResourceVersion
					_, errQuotaUpdate := c.quotaParentclientset.ServerlessV1().Quotas(quotaLocalCopyOfParent.Namespace).Update(context.TODO(), quotaLocalCopyOfParent, metaV1.UpdateOptions{})
					if errQuotaUpdate != nil {
						klog.Error(errQuotaUpdate)
						return errQuotaUpdate
					}
				}
			}
		}
		if clusterQuotaRequire < clusterQuota && c.revisionCheck(quotaCopy, quotaLocalCopyOfParent) {
			// QuotaRequire小于quota，进行配额归还
			//  cluster quota变更：
			//   场景1: quotaRemain剩余且大于归还数
			//     归还数=quota-quotaRequire
			// 	  quoteRequire不变
			// 	  quota变成quotaRequire
			//     quotaRemain=quotaRemain-归还数
			//     quotaUsed=quotaUsed-归还数
			//    field级quota变更
			//      quotaRequire=quotaRequire-归还数
			//  场景2：quotaRemain剩余且小于等于归还数
			//   实际归还数=quotaRemain
			//     quota=quota-实际归还数
			//     quotaRequire=quotaRequire-实际归还数
			//     quotaRemain=0
			//     QuotaUsed=quota
			//    field级quota变更
			//      quotaRequire=quotaRequire-实际归还数
			returnQuota := clusterQuota - clusterQuotaRequire
			if clusterQuotaRemain >= returnQuota {
				quotaCopy.Labels["quota.cluster.pml.com.cn/quota"] = strconv.Itoa(clusterQuota - returnQuota)
				quotaCopy.Labels["quota.cluster.pml.com.cn/quotaRemain"] = strconv.Itoa(clusterQuotaRemain - returnQuota)
				// quotaCopy.Labels["quota.cluster.pml.com.cn/quotaUsed"] = strconv.Itoa(clusterQuotaUsed - returnQuota)
				// quotaCopy.Labels["quota.cluster.pml.com.cn/quota"] = strconv.Itoa(clusterQuota + clusterQuotaRequireMore)
				err := c.ClusterStateUpdate(quotaCopy)
				if err != nil {
					klog.Error(err)
					return err
				}
				_, err = c.quotaclientset.ServerlessV1().Quotas(quotaCopy.Namespace).Update(context.TODO(), quotaCopy, metaV1.UpdateOptions{})
				if err != nil {
					klog.Error(err)
					//return err
				}
				//  边界quota归还
				if fieldQuota > fieldQuotaRequire {
					quotaLocalCopyOfParent.Labels["quota.cluster.pml.com.cn/quotaRequire"] = strconv.Itoa(fieldQuotaRequire - returnQuota)
				} else {
					quotaLocalCopyOfParent.Labels["quota.cluster.pml.com.cn/quotaRequire"] = strconv.Itoa(fieldQuota - returnQuota)
				}
				quotaLocalCopyOfParent.Labels["quota.cluster.pml.com.cn/quotaUsed"] = strconv.Itoa(fieldQuotaUsed - returnQuota)
				quotaLocalCopyOfParent.Labels["quota.cluster.pml.com.cn/quotaRemain"] = strconv.Itoa(fieldQuotaRemain + returnQuota)
				err = c.ParentClusterStateUpdate(quotaLocalCopyOfParent, quotaCopy.Name)
				if err != nil {
					klog.Error(err)
					return err
				}

				initQuotaLocalCopyOfParent, _ := c.quotaParentclientset.ServerlessV1().Quotas(quotaLocalCopyOfParent.Namespace).Get(context.TODO(), quotaLocalCopyOfParent.Name, metaV1.GetOptions{})
				quotaLocalCopyOfParent.ResourceVersion = initQuotaLocalCopyOfParent.ResourceVersion
				_, err = c.quotaParentclientset.ServerlessV1().Quotas(quotaLocalCopyOfParent.Namespace).Update(context.TODO(), quotaLocalCopyOfParent, metaV1.UpdateOptions{})
				if err != nil {
					klog.Error(err)
					return err
				}
			} else {
				newRequire := returnQuota - clusterQuotaRemain
				returnQuota = clusterQuotaRemain
				quotaCopy.Labels["quota.cluster.pml.com.cn/quota"] = strconv.Itoa(clusterQuota - returnQuota)
				quotaCopy.Labels["quota.cluster.pml.com.cn/quotaRemain"] = strconv.Itoa(clusterQuotaRemain - returnQuota)
				quotaCopy.Labels["quota.cluster.pml.com.cn/quotaUsed"] = strconv.Itoa(clusterQuotaUsed - returnQuota)
				quotaCopy.Labels["quota.cluster.pml.com.cn/quotaRequire"] = strconv.Itoa(clusterQuotaRequire + newRequire)
				_, err := c.quotaclientset.ServerlessV1().Quotas(quotaCopy.Namespace).Update(context.TODO(), quotaCopy, metaV1.UpdateOptions{})
				if err != nil {
					klog.Error(err)
					return err
				}
				if fieldQuota > fieldQuotaRequire {
					quotaLocalCopyOfParent.Labels["quota.cluster.pml.com.cn/quotaRequire"] = strconv.Itoa(fieldQuotaRequire - returnQuota)
				} else {
					quotaLocalCopyOfParent.Labels["quota.cluster.pml.com.cn/quotaRequire"] = strconv.Itoa(fieldQuota - returnQuota)
				}
				quotaLocalCopyOfParent.Labels["quota.cluster.pml.com.cn/quotaUsed"] = strconv.Itoa(fieldQuotaUsed - returnQuota)
				quotaLocalCopyOfParent.Labels["quota.cluster.pml.com.cn/quotaRemain"] = strconv.Itoa(fieldQuotaRemain + returnQuota)
				err = c.ParentClusterStateUpdate(quotaLocalCopyOfParent, quotaCopy.Name)
				if err != nil {
					klog.Error(err)
					return err
				}
				initQuotaLocalCopyOfParent, _ := c.quotaParentclientset.ServerlessV1().Quotas(quotaLocalCopyOfParent.Namespace).Get(context.TODO(), quotaLocalCopyOfParent.Name, metaV1.GetOptions{})
				quotaLocalCopyOfParent.ResourceVersion = initQuotaLocalCopyOfParent.ResourceVersion
				_, err = c.quotaParentclientset.ServerlessV1().Quotas(quotaLocalCopyOfParent.Namespace).Update(context.TODO(), quotaLocalCopyOfParent, metaV1.UpdateOptions{})
				if err != nil {
					klog.Error(err)
					return err
				}
			}
		}
	} else {
		switch {
		case quotaCopy.Labels["quota.cluster.pml.com.cn/foundingMember"] == comm.True && quotaCopy.Labels["quota.cluster.pml.com.cn/init"] == comm.True:
			{
				// 场景0 元老实例

				err := c.foundingMemberPodInitNoLimit(quotaCopy)
				if err != nil {
					klog.Error(err)
					return err
				}
				return nil
			}
		case len(quotaCopy.Spec.ChildAlert) > 0:
			{
				if quotaCopy.Spec.ChildAlert[0].Alert {
					// 场景1 0-1场景优先
					err := c.zeroAlertDeployNoLimit(quotaCopy)
					if err != nil {
						klog.Error(err)
						return err
					}
					return nil
				}
			}
		case quotaCopy.Labels["quota.cluster.pml.com.cn/deployScale"] == comm.True:
			{
				// 场景2 deploy scale up
				// deploy加1个replica
				// quota相关属性值变更
				// quotaCopy.Labels["quota.cluster.pml.com.cn/quota"] = strconv.Itoa(clusterQuota + clusterQuotaRequireMore)
				// quotaCopy.Labels["quota.cluster.pml.com.cn/quotaRemain"] = strconv.Itoa(clusterQuotaRequireMore)
				err := c.scaleUpDeployNoLimit(quotaCopy)
				if err != nil {
					klog.Error(err)
					return err
				}
				return nil
			}
		}
		// if quotaCopy.Labels["quota.cluster.pml.com.cn/foundingMember"] == comm.True && quotaCopy.Labels["quota.cluster.pml.com.cn/init"] == comm.True {
		//	// 场景0 元老实例
		//
		//	err := c.foundingMemberPodInitNoLimit(quotaCopy)
		//	if err != nil {
		//		klog.Error(err)
		//		return err
		//	}
		//	return nil
		// } else if len(quotaCopy.Spec.ChildAlert) > 0 {
		//	if quotaCopy.Spec.ChildAlert[0].Alert {
		//		// 场景1 0-1场景优先
		//		err := c.zeroAlertDeployNoLimit(quotaCopy)
		//		if err != nil {
		//			klog.Error(err)
		//			return err
		//		}
		//		return nil
		//	}
		// } else if quotaCopy.Labels["quota.cluster.pml.com.cn/deployScale"] == comm.True {
		//	// 场景2 deploy scale up
		//	// deploy加1个replica
		//	// quota相关属性值变更
		//	// quotaCopy.Labels["quota.cluster.pml.com.cn/quota"] = strconv.Itoa(clusterQuota + clusterQuotaRequireMore)
		//	// quotaCopy.Labels["quota.cluster.pml.com.cn/quotaRemain"] = strconv.Itoa(clusterQuotaRequireMore)
		//	err := c.scaleUpDeployNoLimit(quotaCopy)
		//	if err != nil {
		//		klog.Error(err)
		//		return err
		//	}
		//	return nil
		// }
	}
	return nil
}

func (c *Controller) localQuotaRequireOrReturnField(quotaCopy *v1.Quota, quotaLocalCopyOfParent *v1.Quota) error {
	// field级申请，关联global级quota以及本级quota
	fieldQuotaRequire, _ := strconv.Atoi(quotaCopy.Labels["quota.cluster.pml.com.cn/quotaRequire"])
	klog.Infof("fieldQuotaRequire is: %s", fieldQuotaRequire)
	fieldQuota, _ := strconv.Atoi(quotaCopy.Labels["quota.cluster.pml.com.cn/quota"])
	klog.Infof("fieldQuota is: %s", fieldQuota)
	fieldQuotaRemain, _ := strconv.Atoi(quotaCopy.Labels["quota.cluster.pml.com.cn/quotaRemain"])
	fieldQuotaUsed, _ := strconv.Atoi(quotaCopy.Labels["quota.cluster.pml.com.cn/quotaUsed"])
	klog.Infof("fieldQuotaRemain is: %s", fieldQuotaRemain)
	globalQuota, _ := strconv.Atoi(quotaLocalCopyOfParent.Labels["quota.cluster.pml.com.cn/quota"])
	klog.Infof("globalQuota is: %s", globalQuota)
	// fieldQuotaRequire不变
	// 判断globalQuotaRemain是否有剩余quota可分配
	// 有
	//    有 足够
	//    有 一部分
	// 无

	// QuotaRequire大于quota，进行配额申请
	// 当前quotaRemain存在配额，则上级不再分配
	if fieldQuotaRequire > fieldQuota && fieldQuotaRemain == 0 {
		fieldQuotaRequireMore := fieldQuotaRequire - fieldQuota
		globalQuotaRequire, _ := strconv.Atoi(quotaLocalCopyOfParent.Labels["quota.cluster.pml.com.cn/quotaRequire"])
		klog.Info("fieldQuotaRequire is: %s", globalQuotaRequire)
		globalQuota, _ = strconv.Atoi(quotaLocalCopyOfParent.Labels["quota.cluster.pml.com.cn/quota"])
		klog.Info("globalQuota is:%s", globalQuota)
		globalQuotaRemain, _ := strconv.Atoi(quotaLocalCopyOfParent.Labels["quota.cluster.pml.com.cn/quotaRemain"])
		klog.Info("globalQuotaRemain is:%s", globalQuotaRemain)
		if globalQuotaRemain > 0 {
			// global级当前有剩余配额，直接分配
			klog.Infof("global级当前有剩余配额，直接分配,globalQuotaRemain is:%s", globalQuotaRemain)
			switch {
			case globalQuotaRemain >= fieldQuotaRequireMore && c.revisionCheck(quotaCopy, quotaLocalCopyOfParent):
				{
					// case1 足够分配
					klog.Infof("globalQuotaRemain >= fieldQuotaRequireMore")
					klog.Infof("before quote require,field quota msg is: %s", quotaCopy)
					klog.Infof("before quote require,global quota msg is: %s", quotaLocalCopyOfParent)

					globalQuotaUsed, _ := strconv.Atoi(quotaLocalCopyOfParent.Labels["quota.cluster.pml.com.cn/quotaUsed"])

					globalQuotaRemain -= fieldQuotaRequireMore
					globalQuotaUsed += fieldQuotaRequireMore
					globalQuotaRequire += fieldQuotaRequireMore
					// 申请得到：field级quota按需变更
					quotaCopy.Labels["quota.cluster.pml.com.cn/quota"] = strconv.Itoa(fieldQuotaRequire)
					quotaCopy.Labels["quota.cluster.pml.com.cn/quotaRemain"] = strconv.Itoa(fieldQuotaRemain + fieldQuotaRequireMore)
					// quotaCopy.Labels["quota.cluster.pml.com.cn/quotaRemain"] = strconv.Itoa(fieldQuota - fieldQuotaUsed)
					klog.Infof("quotaCopy is: %s", quotaCopy.Labels)
					initQuotaLocalCopy, _ := c.quotaclientset.ServerlessV1().Quotas(quotaCopy.Namespace).Get(context.TODO(), quotaCopy.Name, metaV1.GetOptions{})
					quotaCopy.ResourceVersion = initQuotaLocalCopy.ResourceVersion
					_, err := c.quotaclientset.ServerlessV1().Quotas(quotaCopy.Namespace).Update(context.TODO(), quotaCopy, metaV1.UpdateOptions{})
					if err != nil {
						klog.Info(err)
						return err
					}
					// 申请得到，global级quota按需变更
					quotaLocalCopyOfParent.Labels["quota.cluster.pml.com.cn/quotaRemain"] = strconv.Itoa(globalQuotaRemain)
					quotaLocalCopyOfParent.Labels["quota.cluster.pml.com.cn/quotaUsed"] = strconv.Itoa(globalQuotaUsed)
					quotaLocalCopyOfParent.Labels["quota.cluster.pml.com.cn/quotaRequire"] = strconv.Itoa(globalQuotaRequire)
					initQuotaLocalCopyOfParent, _ := c.quotaParentclientset.ServerlessV1().Quotas(quotaLocalCopyOfParent.Namespace).Get(context.TODO(), quotaLocalCopyOfParent.Name, metaV1.GetOptions{})
					quotaLocalCopyOfParent.ResourceVersion = initQuotaLocalCopyOfParent.ResourceVersion
					_, err = c.quotaParentclientset.ServerlessV1().Quotas(quotaLocalCopyOfParent.Namespace).Update(context.TODO(), quotaLocalCopyOfParent, metaV1.UpdateOptions{})
					if err != nil {
						klog.Info(err)
						return err
					}
					klog.Infof("After quote require,field quota msg is: %s", quotaCopy)
					klog.Infof("After quote require,global quota msg is: %s", quotaLocalCopyOfParent)
					return nil
					// 得到quota后进行相关业务操作 按场景进行判断，扩容完成后进行quota的对应变更
				}
			case c.revisionCheck(quotaCopy, quotaLocalCopyOfParent):
				{
					// case2 global级分配完，field 级quota仍然不够
					//  field级quotaRemain变成0，都分配给field级
					klog.Infof("globalQuotaRemain < fieldQuotaRequireMore")
					klog.Infof("before quote require,field quota msg is: %s", quotaCopy)
					klog.Infof("before quote require,global quota msg is: %s", quotaLocalCopyOfParent)
					globalQuotaUsed, _ := strconv.Atoi(quotaLocalCopyOfParent.Labels["quota.cluster.pml.com.cn/quotaUsed"])

					globalQuotaUsed += globalQuotaRemain
					fieldQuota += globalQuotaRemain
					// globalQuotaRequire = globalQuotaRequire + (fieldQuotaRequireMore - globalQuotaRemain)
					globalQuotaRequire = globalQuota
					globalQuotaRemain = 0
					// 申请得到，global级quota按需变更
					quotaLocalCopyOfParent.Labels["quota.cluster.pml.com.cn/quotaRemain"] = strconv.Itoa(globalQuotaRemain)
					quotaLocalCopyOfParent.Labels["quota.cluster.pml.com.cn/quotaUsed"] = strconv.Itoa(globalQuotaUsed)
					quotaLocalCopyOfParent.Labels["quota.cluster.pml.com.cn/quotaRequire"] = strconv.Itoa(globalQuotaRequire)

					initQuotaLocalCopyOfParent, _ := c.quotaParentclientset.ServerlessV1().Quotas(quotaLocalCopyOfParent.Namespace).Get(context.TODO(), quotaLocalCopyOfParent.Name, metaV1.GetOptions{})
					quotaLocalCopyOfParent.ResourceVersion = initQuotaLocalCopyOfParent.ResourceVersion
					_, err := c.quotaParentclientset.ServerlessV1().Quotas(quotaLocalCopyOfParent.Namespace).Update(context.TODO(), quotaLocalCopyOfParent, metaV1.UpdateOptions{})
					if err != nil {
						klog.Info(err)
						return err
					}
					// 申请得到：cluster级quota按需变更
					// 申请得到的quota加上原有的quota
					fieldQuotaRemain = fieldQuota - fieldQuotaUsed
					quotaCopy.Labels["quota.cluster.pml.com.cn/quota"] = strconv.Itoa(fieldQuota)
					quotaCopy.Labels["quota.cluster.pml.com.cn/quotaRemain"] = strconv.Itoa(fieldQuotaRemain)
					initQuotaLocalCopy, _ := c.quotaclientset.ServerlessV1().Quotas(quotaCopy.Namespace).Get(context.TODO(), quotaCopy.Name, metaV1.GetOptions{})
					quotaCopy.ResourceVersion = initQuotaLocalCopy.ResourceVersion
					_, err = c.quotaclientset.ServerlessV1().Quotas(quotaCopy.Namespace).Update(context.TODO(), quotaCopy, metaV1.UpdateOptions{})
					if err != nil {
						klog.Info(err)
						return err
					}
					klog.Infof("After quote require,field quota msg is: %s", quotaCopy)
					klog.Infof("After quote require,global quota msg is: %s", quotaLocalCopyOfParent)
					return nil
				}
			default:
				{
					return nil
				}
			}
			// if globalQuotaRemain >= fieldQuotaRequireMore && c.revisionCheck(quotaCopy, quotaLocalCopyOfParent) {
			//	// case1 足够分配
			//	klog.Infof("globalQuotaRemain >= fieldQuotaRequireMore")
			//	klog.Infof("before quote require,field quota msg is: %s", quotaCopy)
			//	klog.Infof("before quote require,global quota msg is: %s", quotaLocalCopyOfParent)
			//
			//	globalQuotaUsed, _ := strconv.Atoi(quotaLocalCopyOfParent.Labels["quota.cluster.pml.com.cn/quotaUsed"])
			//
			//	globalQuotaRemain -= fieldQuotaRequireMore
			//	globalQuotaUsed += fieldQuotaRequireMore
			//	globalQuotaRequire += fieldQuotaRequireMore
			//	// 申请得到：field级quota按需变更
			//	quotaCopy.Labels["quota.cluster.pml.com.cn/quota"] = strconv.Itoa(fieldQuotaRequire)
			//	quotaCopy.Labels["quota.cluster.pml.com.cn/quotaRemain"] = strconv.Itoa(fieldQuotaRemain + fieldQuotaRequireMore)
			//	// quotaCopy.Labels["quota.cluster.pml.com.cn/quotaRemain"] = strconv.Itoa(fieldQuota - fieldQuotaUsed)
			//	klog.Infof("quotaCopy is: %s", quotaCopy.Labels)
			//	initQuotaLocalCopy, _ := c.quotaclientset.ServerlessV1().Quotas(quotaCopy.Namespace).Get(context.TODO(), quotaCopy.Name, metaV1.GetOptions{})
			//	quotaCopy.ResourceVersion = initQuotaLocalCopy.ResourceVersion
			//	_, err := c.quotaclientset.ServerlessV1().Quotas(quotaCopy.Namespace).Update(context.TODO(), quotaCopy, metaV1.UpdateOptions{})
			//	if err != nil {
			//		klog.Info(err)
			//		return err
			//	}
			//	// 申请得到，global级quota按需变更
			//	quotaLocalCopyOfParent.Labels["quota.cluster.pml.com.cn/quotaRemain"] = strconv.Itoa(globalQuotaRemain)
			//	quotaLocalCopyOfParent.Labels["quota.cluster.pml.com.cn/quotaUsed"] = strconv.Itoa(globalQuotaUsed)
			//	quotaLocalCopyOfParent.Labels["quota.cluster.pml.com.cn/quotaRequire"] = strconv.Itoa(globalQuotaRequire)
			//	initQuotaLocalCopyOfParent, _ := c.quotaParentclientset.ServerlessV1().Quotas(quotaLocalCopyOfParent.Namespace).Get(context.TODO(), quotaLocalCopyOfParent.Name, metaV1.GetOptions{})
			//	quotaLocalCopyOfParent.ResourceVersion = initQuotaLocalCopyOfParent.ResourceVersion
			//	_, err = c.quotaParentclientset.ServerlessV1().Quotas(quotaLocalCopyOfParent.Namespace).Update(context.TODO(), quotaLocalCopyOfParent, metaV1.UpdateOptions{})
			//	if err != nil {
			//		klog.Info(err)
			//		return err
			//	}
			//	klog.Infof("After quote require,field quota msg is: %s", quotaCopy)
			//	klog.Infof("After quote require,global quota msg is: %s", quotaLocalCopyOfParent)
			//	return nil
			//	// 得到quota后进行相关业务操作 按场景进行判断，扩容完成后进行quota的对应变更
			// } else if c.revisionCheck(quotaCopy, quotaLocalCopyOfParent) {
			//	// case2 global级分配完，field 级quota仍然不够
			//	//  field级quotaRemain变成0，都分配给field级
			//	klog.Infof("globalQuotaRemain < fieldQuotaRequireMore")
			//	klog.Infof("before quote require,field quota msg is: %s", quotaCopy)
			//	klog.Infof("before quote require,global quota msg is: %s", quotaLocalCopyOfParent)
			//	globalQuotaUsed, _ := strconv.Atoi(quotaLocalCopyOfParent.Labels["quota.cluster.pml.com.cn/quotaUsed"])
			//
			//	globalQuotaUsed += globalQuotaRemain
			//	fieldQuota += globalQuotaRemain
			//	// globalQuotaRequire = globalQuotaRequire + (fieldQuotaRequireMore - globalQuotaRemain)
			//	globalQuotaRequire = globalQuota
			//	globalQuotaRemain = 0
			//	// 申请得到，global级quota按需变更
			//	quotaLocalCopyOfParent.Labels["quota.cluster.pml.com.cn/quotaRemain"] = strconv.Itoa(globalQuotaRemain)
			//	quotaLocalCopyOfParent.Labels["quota.cluster.pml.com.cn/quotaUsed"] = strconv.Itoa(globalQuotaUsed)
			//	quotaLocalCopyOfParent.Labels["quota.cluster.pml.com.cn/quotaRequire"] = strconv.Itoa(globalQuotaRequire)
			//
			//	initQuotaLocalCopyOfParent, _ := c.quotaParentclientset.ServerlessV1().Quotas(quotaLocalCopyOfParent.Namespace).Get(context.TODO(), quotaLocalCopyOfParent.Name, metaV1.GetOptions{})
			//	quotaLocalCopyOfParent.ResourceVersion = initQuotaLocalCopyOfParent.ResourceVersion
			//	_, err := c.quotaParentclientset.ServerlessV1().Quotas(quotaLocalCopyOfParent.Namespace).Update(context.TODO(), quotaLocalCopyOfParent, metaV1.UpdateOptions{})
			//	if err != nil {
			//		klog.Info(err)
			//		return err
			//	}
			//	// 申请得到：cluster级quota按需变更
			//	// 申请得到的quota加上原有的quota
			//	fieldQuotaRemain = fieldQuota - fieldQuotaUsed
			//	quotaCopy.Labels["quota.cluster.pml.com.cn/quota"] = strconv.Itoa(fieldQuota)
			//	quotaCopy.Labels["quota.cluster.pml.com.cn/quotaRemain"] = strconv.Itoa(fieldQuotaRemain)
			//	initQuotaLocalCopy, _ := c.quotaclientset.ServerlessV1().Quotas(quotaCopy.Namespace).Get(context.TODO(), quotaCopy.Name, metaV1.GetOptions{})
			//	quotaCopy.ResourceVersion = initQuotaLocalCopy.ResourceVersion
			//	_, err = c.quotaclientset.ServerlessV1().Quotas(quotaCopy.Namespace).Update(context.TODO(), quotaCopy, metaV1.UpdateOptions{})
			//	if err != nil {
			//		klog.Info(err)
			//		return err
			//	}
			//	klog.Infof("After quote require,field quota msg is: %s", quotaCopy)
			//	klog.Infof("After quote require,global quota msg is: %s", quotaLocalCopyOfParent)
			//	return nil
			// } else {
			//	return nil
			// }
		}
		// else {
		// 	// TODO s3 global级当前无剩余配额，等待globalQuota变化（全局quota调整），同时变更quota再进行后续reconcile
		// 	// Done
		// 	globalQuotaRequire, _ = strconv.Atoi(quotaLocalCopyOfParent.Labels["quota.cluster.pml.com.cn/quotaRequire"])
		// 	QuotaStep, _ := strconv.Atoi(quotaLocalCopyOfParent.Labels["quota.cluster.pml.com.cn/quotaStep"])
		// 	fieldQuotaRequireMore = fieldQuotaRequire - fieldQuota
		// 	if globalQuotaRequire-globalQuota < QuotaStep {
		//
		// 		// globalQuotaRequire = globalQuotaRequire + fieldQuotaRequireMore
		// 		// globalQuotaRequire = globalQuotaRequire
		// 		quotaLocalCopyOfParent.Labels["quota.cluster.pml.com.cn/quotaRequire"] = strconv.Itoa(globalQuotaRequire)
		// 		klog.Infof("quotaLocalCopyOfParent.Labels is: %s", quotaLocalCopyOfParent.Labels)
		// 		initQuotaLocalCopyOfParent, _ := c.quotaParentclientset.ServerlessV1().Quotas(quotaLocalCopyOfParent.Namespace).Get(context.TODO(), quotaLocalCopyOfParent.Name, metav1.GetOptions{})
		// 		quotaLocalCopyOfParent.ResourceVersion = initQuotaLocalCopyOfParent.ResourceVersion
		// 		c.quotaParentclientset.ServerlessV1().Quotas(quotaLocalCopyOfParent.Namespace).Update(context.TODO(), quotaLocalCopyOfParent, metav1.UpdateOptions{})
		// 	}

		// }
	} else if fieldQuotaRequire < fieldQuota {
		// QuotaRequire小于quota，进行配额归还
		//  field quota变更触发：
		//  归还数=quota-quotaRequire
		//  场景1: quotaRemain剩余且大于归还数
		// 	quoteRequire不变
		// 	quota变成quotaRequire
		//   quotaRemain=quotaRemain-归还数
		//   quotaUsed=quotaUsed-归还数
		//  global quota变更：
		//    quotaRequire=quotaRequire-归还数
		//    quota不变
		//    quotaRemain=quotaRemain-归还数
		//    quotaUsed=quotaUsed-归还数
		//  场景2：quotaRemain剩余但是小于等于归还数
		//   实际归还数=quotaRemain
		//   quota=quota-实际归还数
		//   quotaRequire=quotaRequire-实际归还数
		//   quotaRemain=0
		//   QuotaUsed=quota
		//  global quota变更：
		//    quotaRequire=quotaRequire-实际归还数
		//    quota不变
		//    quotaRemain=quotaRemain+实际归还数
		//    quotaUsed=quotaUsed-实际归还数

		globalQuotaRequire, _ := strconv.Atoi(quotaLocalCopyOfParent.Labels["quota.cluster.pml.com.cn/quotaRequire"])
		klog.Info("fieldQuotaRequire is: %s", globalQuotaRequire)
		globalQuota, _ = strconv.Atoi(quotaLocalCopyOfParent.Labels["quota.cluster.pml.com.cn/quota"])
		klog.Info("fieldQuota is:%s", globalQuota)
		globalQuotaRemain, _ := strconv.Atoi(quotaLocalCopyOfParent.Labels["quota.cluster.pml.com.cn/quotaRemain"])
		globalQuotaUsed, _ := strconv.Atoi(quotaLocalCopyOfParent.Labels["quota.cluster.pml.com.cn/quotaUsed"])
		klog.Infof("start return quota ")
		klog.Info("globalQuotaRemain is:%s", globalQuotaRemain)
		returnQuota := fieldQuota - fieldQuotaRequire
		if fieldQuotaRemain >= returnQuota && c.revisionCheck(quotaCopy, quotaLocalCopyOfParent) {
			klog.Infof("before quote return,field quota msg is: %s", quotaCopy)
			klog.Infof("before quote return,global quota msg is: %s", quotaLocalCopyOfParent)
			klog.Infof("start return quota: return quota is : %s ", returnQuota)
			quotaCopy.Labels["quota.cluster.pml.com.cn/quota"] = strconv.Itoa(fieldQuota - returnQuota)
			quotaCopy.Labels["quota.cluster.pml.com.cn/quotaRemain"] = strconv.Itoa(fieldQuotaRemain - returnQuota)
			// quotaCopy.Labels["quota.cluster.pml.com.cn/quotaUsed"] = strconv.Itoa(fieldQuotaUsed - returnQuota)
			// quotaCopy.Labels["quota.cluster.pml.com.cn/quota"] = strconv.Itoa(clusterQuota + clusterQuotaRequireMore)
			initQuotaLocalCopy, _ := c.quotaclientset.ServerlessV1().Quotas(quotaCopy.Namespace).Get(context.TODO(), quotaCopy.Name, metaV1.GetOptions{})
			quotaCopy.ResourceVersion = initQuotaLocalCopy.ResourceVersion
			_, err := c.quotaclientset.ServerlessV1().Quotas(quotaCopy.Namespace).Update(context.TODO(), quotaCopy, metaV1.UpdateOptions{})
			if err != nil {
				klog.Error(err)
				return err
			}
			quotaLocalCopyOfParent.Labels["quota.cluster.pml.com.cn/quotaRequire"] = strconv.Itoa(globalQuotaRequire - returnQuota)
			quotaLocalCopyOfParent.Labels["quota.cluster.pml.com.cn/quotaRemain"] = strconv.Itoa(globalQuotaRemain + returnQuota)
			quotaLocalCopyOfParent.Labels["quota.cluster.pml.com.cn/quotaUsed"] = strconv.Itoa(globalQuotaUsed - returnQuota)
			initQuotaLocalCopyOfParent, _ := c.quotaParentclientset.ServerlessV1().Quotas(quotaLocalCopyOfParent.Namespace).Get(context.TODO(), quotaLocalCopyOfParent.Name, metaV1.GetOptions{})
			quotaLocalCopyOfParent.ResourceVersion = initQuotaLocalCopyOfParent.ResourceVersion
			_, err = c.quotaParentclientset.ServerlessV1().Quotas(quotaLocalCopyOfParent.Namespace).Update(context.TODO(), quotaLocalCopyOfParent, metaV1.UpdateOptions{})
			if err != nil {
				klog.Error(err)
				return err
			}
			klog.Infof("after quote return,field quota msg is: %s", quotaCopy)
			klog.Infof("after quote return,global quota msg is: %s", quotaLocalCopyOfParent)
			return nil
		} else if fieldQuotaRemain < returnQuota && c.revisionCheck(quotaCopy, quotaLocalCopyOfParent) {
			klog.Infof("before quote return,field quota msg is: %s", quotaCopy)
			klog.Infof("before quote return,global quota msg is: %s", quotaLocalCopyOfParent)
			newRequire := returnQuota - fieldQuotaRemain
			returnQuota = fieldQuotaRemain
			klog.Infof("start return quota: return quota is : %s ", returnQuota)
			quotaCopy.Labels["quota.cluster.pml.com.cn/quota"] = strconv.Itoa(fieldQuota - returnQuota)
			quotaCopy.Labels["quota.cluster.pml.com.cn/quotaRemain"] = strconv.Itoa(fieldQuotaRemain - returnQuota)
			quotaCopy.Labels["quota.cluster.pml.com.cn/quotaUsed"] = strconv.Itoa(fieldQuotaUsed - returnQuota)
			quotaCopy.Labels["quota.cluster.pml.com.cn/quotaRequire"] = strconv.Itoa(fieldQuotaRequire + newRequire)
			initQuotaLocalCopy, _ := c.quotaclientset.ServerlessV1().Quotas(quotaCopy.Namespace).Get(context.TODO(), quotaCopy.Name, metaV1.GetOptions{})
			quotaCopy.ResourceVersion = initQuotaLocalCopy.ResourceVersion
			_, err := c.quotaclientset.ServerlessV1().Quotas(quotaCopy.Namespace).Update(context.TODO(), quotaCopy, metaV1.UpdateOptions{})
			if err != nil {
				klog.Error(err)
				return err
			}
			quotaLocalCopyOfParent.Labels["quota.cluster.pml.com.cn/quotaRequire"] = strconv.Itoa(globalQuotaRequire - returnQuota)
			quotaLocalCopyOfParent.Labels["quota.cluster.pml.com.cn/quotaRemain"] = strconv.Itoa(globalQuotaRemain + returnQuota)
			quotaLocalCopyOfParent.Labels["quota.cluster.pml.com.cn/quotaUsed"] = strconv.Itoa(globalQuotaUsed - returnQuota)
			initQuotaLocalCopyOfParent, _ := c.quotaParentclientset.ServerlessV1().Quotas(quotaLocalCopyOfParent.Namespace).Get(context.TODO(), quotaLocalCopyOfParent.Name, metaV1.GetOptions{})
			quotaLocalCopyOfParent.ResourceVersion = initQuotaLocalCopyOfParent.ResourceVersion
			_, err = c.quotaParentclientset.ServerlessV1().Quotas(quotaLocalCopyOfParent.Namespace).Update(context.TODO(), quotaLocalCopyOfParent, metaV1.UpdateOptions{})
			if err != nil {
				klog.Error(err)
				return err
			}
			klog.Infof("after quote return,field quota msg is: %s", quotaCopy)
			klog.Infof("after quote return,global quota msg is: %s", quotaLocalCopyOfParent)
			return nil
		} else {
			return nil
		}
	} else if fieldQuotaRequire == fieldQuota && fieldQuotaRemain > 0 && c.revisionCheck(quotaCopy, quotaLocalCopyOfParent) {
		// field 级管理平稳状态quota归还
		childClusterStates := 0
		for _, childClusterStateMsg := range quotaCopy.Spec.ChildClusterState {
			if childClusterStateMsg.ClusterState != "steady" {
				childClusterStates++
			}
		}
		if childClusterStates == 0 {
			// field级归还申请到后cluster级不使用的quota
			klog.Infof("before quote return,field quota msg is: %s", quotaCopy)
			klog.Infof("before quote return,global quota msg is: %s", quotaLocalCopyOfParent)
			globalQuotaRequire, _ := strconv.Atoi(quotaLocalCopyOfParent.Labels["quota.cluster.pml.com.cn/quotaRequire"])
			klog.Info("fieldQuotaRequire is: %s", globalQuotaRequire)
			globalQuota, _ := strconv.Atoi(quotaLocalCopyOfParent.Labels["quota.cluster.pml.com.cn/quota"])
			klog.Info("fieldQuota is:%s", globalQuota)
			globalQuotaRemain, _ := strconv.Atoi(quotaLocalCopyOfParent.Labels["quota.cluster.pml.com.cn/quotaRemain"])
			globalQuotaUsed, _ := strconv.Atoi(quotaLocalCopyOfParent.Labels["quota.cluster.pml.com.cn/quotaUsed"])
			klog.Infof("start return quota ")
			klog.Info("globalQuotaRemain is:%s", globalQuotaRemain)
			quotaCopy.Labels["quota.cluster.pml.com.cn/quota"] = strconv.Itoa(fieldQuota - fieldQuotaRemain)
			quotaCopy.Labels["quota.cluster.pml.com.cn/quotaRemain"] = strconv.Itoa(0)
			quotaCopy.Labels["quota.cluster.pml.com.cn/quotaRequire"] = strconv.Itoa(fieldQuotaRequire - fieldQuotaRemain)
			initQuotaLocalCopy, _ := c.quotaclientset.ServerlessV1().Quotas(quotaCopy.Namespace).Get(context.TODO(), quotaCopy.Name, metaV1.GetOptions{})
			quotaCopy.ResourceVersion = initQuotaLocalCopy.ResourceVersion
			_, err := c.quotaclientset.ServerlessV1().Quotas(quotaCopy.Namespace).Update(context.TODO(), quotaCopy, metaV1.UpdateOptions{})
			if err != nil {
				klog.Error(err)
			}
			quotaLocalCopyOfParent.Labels["quota.cluster.pml.com.cn/quotaRequire"] = strconv.Itoa(globalQuotaRequire - fieldQuotaRemain)
			quotaLocalCopyOfParent.Labels["quota.cluster.pml.com.cn/quotaRemain"] = strconv.Itoa(globalQuotaRemain + fieldQuotaRemain)
			quotaLocalCopyOfParent.Labels["quota.cluster.pml.com.cn/quotaUsed"] = strconv.Itoa(globalQuotaUsed - fieldQuotaRemain)
			initQuotaLocalCopyOfParent, _ := c.quotaParentclientset.ServerlessV1().Quotas(quotaLocalCopyOfParent.Namespace).Get(context.TODO(), quotaLocalCopyOfParent.Name, metaV1.GetOptions{})
			quotaLocalCopyOfParent.ResourceVersion = initQuotaLocalCopyOfParent.ResourceVersion
			_, err = c.quotaParentclientset.ServerlessV1().Quotas(quotaLocalCopyOfParent.Namespace).Update(context.TODO(), quotaLocalCopyOfParent, metaV1.UpdateOptions{})
			if err != nil {
				klog.Error(err)
				return err
			}
			klog.Infof("after quote return,field quota msg is: %s", quotaCopy)
			klog.Infof("after quote return,global quota msg is: %s", quotaLocalCopyOfParent)
		}
	}
	return nil
}

func (c *Controller) globalQuotaScale(quotaCopy *v1.Quota, quotaLocalCopyOfParent *v1.Quota) error {
	clusterConf, err := c.kubeclientset.CoreV1().ConfigMaps("serverless-system").Get(context.TODO(), "quota-conf", metaV1.GetOptions{})
	if err != nil {
		return err
	}
	// 5：本级quotaQPs相关数据删除，上级相应删除（只处理cluster-field的相关数据，global暂时不处理）
	//   上级list里面过滤出当前级别对应的数据，与本级进行比较，如果本级不存在，则删除上级相应的数据
	//   根据级别判断进行字段填充
	// 6:quota申请及回收
	// 场景cluster级quota申请c-f-g c-f，注意判断级别，级别对应的动作不同
	//   0-1触发，或者其他扩容请求导致的扩容
	// if locker, err := etcd_lock.New(etcdEndpointQuota, option); err != nil {
	// 	klog.Errorf("创建锁失败：%+v", err)
	// } else if who, ok := locker.Acquire(quotaCopy.Name + quotaCopy.Namespace); ok {
	// 	defer locker.Release()
	// 	//  抢到锁后执行业务逻辑，没有抢到则退出
	// 	klog.Infof("进程 %+v 持有锁 %+v 正在处理任务中...", os.Getpid(), locker.GetId())
	if clusterConf.Data["clusterLevel"] == comm.ClusterAreaTypeCluster {
		err := c.localGlobalQuotaScaleCluster(quotaCopy, quotaLocalCopyOfParent)
		if err != nil {
			klog.Info(err)
			return err
		}
		return nil
	} else if clusterConf.Data["clusterLevel"] == comm.ClusterAreaTypeField {
		err := c.localGlobalQuotaScaleField(quotaCopy, quotaLocalCopyOfParent)
		if err != nil {
			klog.Info(err)
			return err
		}
		return nil
	}
	// } else {
	// 	klog.Infof("获取锁失败，锁现在在 %+v 手中", who)
	// 	return nil
	// }
	return nil
}

func (c *Controller) localGlobalQuotaScaleCluster(quotaCopy *v1.Quota, quotaLocalCopyOfParent *v1.Quota) error {
	//  c-f
	// cluser级G quota扩容 关联field级G quota扩容
	clusterGlobalQuota, _ := strconv.Atoi(quotaCopy.Labels["quota.cluster.pml.com.cn/globalQuota"])
	fieldGlobalQuota, _ := strconv.Atoi(quotaLocalCopyOfParent.Labels["quota.cluster.pml.com.cn/globalQuota"])
	if quotaLocalCopyOfParent.Labels["quota.cluster.pml.com.cn/type"] != comm.ServerlessTypeNoLimit {
		if clusterGlobalQuota > fieldGlobalQuota && c.revisionCheck(quotaCopy, quotaLocalCopyOfParent) {
			klog.Infof("clusterGlobalQuota > fieldGlobalQuota,开始GQ全局c-f quota扩容同步")
			//field级qlobalQuota变更为cluster级的globalQuota
			quotaLocalCopyOfParent.Labels["quota.cluster.pml.com.cn/globalQuota"] = strconv.Itoa(clusterGlobalQuota)
			_, err := c.quotaParentclientset.ServerlessV1().Quotas(quotaLocalCopyOfParent.Namespace).Update(context.TODO(), quotaLocalCopyOfParent, metaV1.UpdateOptions{})
			if err != nil {
				return err
			}
		}
	}
	klog.Infof("clusterGlobalQuota > fieldGlobalQuota,完成c-f全局quota上级同步")

	return nil
}

func (c *Controller) localGlobalQuotaScaleField(quotaCopy *v1.Quota, quotaLocalCopyOfParent *v1.Quota) error {
	// field级G quota扩容 关联global级G quota扩容
	fieldGlobalQuota, _ := strconv.Atoi(quotaCopy.Labels["quota.cluster.pml.com.cn/globalQuota"])
	globalGlobalQuota, _ := strconv.Atoi(quotaLocalCopyOfParent.Labels["quota.cluster.pml.com.cn/globalQuota"])
	globalQuotaUsed, _ := strconv.Atoi(quotaLocalCopyOfParent.Labels["quota.cluster.pml.com.cn/quotaUsed"])
	if quotaLocalCopyOfParent.Labels["quota.cluster.pml.com.cn/type"] != comm.ServerlessTypeNoLimit {
		if fieldGlobalQuota > globalGlobalQuota && c.revisionCheck(quotaCopy, quotaLocalCopyOfParent) {
			klog.Infof("fieldGlobalQuota > globalGlobalQuota,开始GQ全局f-g quota扩容同步")
			//field级qlobalQuota变更为cluster级的globalQuota
			quotaLocalCopyOfParent.Labels["quota.cluster.pml.com.cn/globalQuota"] = strconv.Itoa(fieldGlobalQuota)
			quotaLocalCopyOfParent.Labels["quota.cluster.pml.com.cn/quota"] = strconv.Itoa(fieldGlobalQuota)
			globalQuotaRemain := fieldGlobalQuota - globalQuotaUsed
			quotaLocalCopyOfParent.Labels["quota.cluster.pml.com.cn/quotaRemain"] = strconv.Itoa(globalQuotaRemain)
			_, err := c.quotaParentclientset.ServerlessV1().Quotas(quotaLocalCopyOfParent.Namespace).Update(context.TODO(), quotaLocalCopyOfParent, metaV1.UpdateOptions{})
			if err != nil {
				return err
			}
		}
	}
	klog.Infof("clusterGlobalQuota > fieldGlobalQuota,完成c-f全局quota上级同步")
	return nil
}

func (c *Controller) revisionCheck(quotaCopy *v1.Quota, quotaLocalCopyOfParent *v1.Quota) bool { // 判断是否存在quotarevision的变化
	time.Sleep(comm.SleepTimeShort * time.Second)
	quotaToUpdate, err := c.quotasLister.Quotas(quotaCopy.Namespace).Get(quotaCopy.Name)
	if err != nil {
		klog.Errorf(err.Error())
		return false
	}
	quotaToUpdate = quotaToUpdate.DeepCopy()
	// 获得锁后重新拿最新的数据进行业务
	quotaLocalCopyOfParentToUpdate, err := c.quotasParentLister.Quotas(quotaLocalCopyOfParent.Namespace).Get(quotaLocalCopyOfParent.Name)
	if err != nil {
		klog.Errorf(err.Error())
		return false
	}
	quotaLocalCopyOfParentToUpdate = quotaLocalCopyOfParentToUpdate.DeepCopy()
	klog.Infof(quotaToUpdate.ResourceVersion, quotaCopy.ResourceVersion)
	klog.Infof(quotaLocalCopyOfParentToUpdate.ResourceVersion, quotaLocalCopyOfParent.ResourceVersion)
	if quotaToUpdate.ResourceVersion != quotaCopy.ResourceVersion || quotaLocalCopyOfParent.ResourceVersion != quotaLocalCopyOfParentToUpdate.ResourceVersion {
		klog.Infof("update before quota manage!!!!Run Again")
		return false
	}
	return true
}
