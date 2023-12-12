package controller

import (
	"context"
	quotav1 "github.com/SUMMERLm/quota/api/v1"
	serverlessv1 "github.com/SUMMERLm/serverless/api/v1"
	"github.com/SUMMERLm/serverless/pkg/etcd_lock"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/klog/v2"
	"os"
	"reflect"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"strconv"
	"strings"
	"time"
)

const (
	steady   = "steady"
	requireQ = "require"
	returnQ  = "return"
)

func (r *ServerlessReconciler) createQuota(ctx context.Context, req ctrl.Request, localCluser string, parentCLuster string, etcdEndpoint string, serverlessInstance *serverlessv1.Serverless, name string, namespace string) error {
	const action = "create"
	klog.Infof("start create Quota With Yaml %s", name)
	klog.Infof(localCluser)
	klog.Infof(parentCLuster)
	klog.Infof(serverlessInstance.Name)
	var serverlessInstanceSecure serverlessv1.Serverless
	serverlessInstanceSecure = *serverlessInstance
	//first time wait deploy msg sync
	err := r.quotaAction(ctx, req, localCluser, parentCLuster, etcdEndpoint, serverlessInstanceSecure, name, namespace, action)
	if err != nil {
		klog.Error(err)
		return err
	}
	klog.Infof("end create Quota With Yaml%s", name)

	return nil
}

func (r *ServerlessReconciler) updateQuota(ctx context.Context, req ctrl.Request, localCluser string, parentCLuster string, etcdEndpoint string, serverlessInstance *serverlessv1.Serverless, name string, namespace string) error {
	const action = "update"
	klog.Infof("start update Quota With Yaml %s", name)
	var serverlessInstanceSecure serverlessv1.Serverless
	serverlessInstanceSecure = *serverlessInstance
	err := r.quotaAction(ctx, req, localCluser, parentCLuster, etcdEndpoint, serverlessInstanceSecure, name, namespace, action)
	if err != nil {
		klog.Error(err)
		return err
	}
	klog.Infof("end update Quota With Yaml %s", name)
	return nil
}

func (r *ServerlessReconciler) quotaAction(ctx context.Context, req ctrl.Request, localCluser string, parentCLuster string, etcdEndpoint string, serverlessInstance serverlessv1.Serverless, name string, namespace string, action string) error {
	//judge type
	var networkRegisterMsg []quotav1.NetworkRegisterSpec
	var childClusterState []quotav1.ChildClusterState
	var etcdEndpointQuota = []string{etcdEndpoint}
	var option = etcd_lock.Option{
		ConnectionTimeout: 5 * time.Second,
		Prefix:            "ServerlessQuotaLocker:",
		Debug:             false,
	}
	if action == "create" {
		quotaType := "noLimit"
		globalQuota := 0
		quotaStep := 0
		quotaRequire := 0
		quotaUsed := 0
		quotalocal := 0
		foundingMember := serverlessInstance.Spec.Workload.TraitServerless.Foundingmember
		// 新建和更新的值配置不一样，需要更新逻辑
		if serverlessInstance.Spec.Workload.TraitServerless.MaxQPS > 0 {
			quotaType = "qps"
			globalQuota = int(serverlessInstance.Spec.Workload.TraitServerless.MaxQPS)
			quotaStep = int(serverlessInstance.Spec.Workload.TraitServerless.QpsStep)
			//元老实例初始化，配额预先分配(走申请流程)
			if serverlessInstance.Spec.Workload.TraitServerless.Foundingmember {
				//quotalocal = quotaStep
				quotaRequire = quotaStep
				//quotaUsed = quotaStep

			}
		} else if serverlessInstance.Spec.Workload.TraitServerless.MaxReplicas > 0 {
			quotaType = "replica"
			globalQuota = int(serverlessInstance.Spec.Workload.TraitServerless.MaxReplicas)
			quotaStep = int(serverlessInstance.Spec.Workload.TraitServerless.ResplicasStep)
			//元老实例初始化，配额预先分配
			if serverlessInstance.Spec.Workload.TraitServerless.Foundingmember {
				quotaRequire = quotaStep

			}
		}

		klog.Infof("quota type  is :%s ", quotaType)
		quotas := &quotav1.Quota{
			TypeMeta: metav1.TypeMeta{
				APIVersion: "serverless.pml.com.cn/v1",
				Kind:       "Quota",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: namespace,
				Labels: map[string]string{
					"quota.cluster.pml.com.cn/globalQuota":    strconv.Itoa(globalQuota),
					"quota.cluster.pml.com.cn/type":           quotaType,
					"quota.cluster.pml.com.cn/quotaStep":      strconv.Itoa(quotaStep),
					"quota.cluster.pml.com.cn/quota":          strconv.Itoa(quotalocal),
					"quota.cluster.pml.com.cn/quotaRequire":   strconv.Itoa(quotaRequire),
					"quota.cluster.pml.com.cn/quotaUsed":      strconv.Itoa(quotaUsed),
					"quota.cluster.pml.com.cn/foundingMember": strconv.FormatBool(foundingMember),
					"quota.cluster.pml.com.cn/deployScale":    "false",
					"quota.cluster.pml.com.cn/deployDown":     "false",
					"quota.cluster.pml.com.cn/quotaRemain":    "0",
					"quota.cluster.pml.com.cn/init":           "true"},
			},
		}
		if err := controllerutil.SetControllerReference(&serverlessInstance, quotas, r.Scheme); err != nil {
			klog.Error(err)
			return err
		}

		//multi containers
		for containers := range serverlessInstance.Spec.Module.Spec.Containers {
			//multi env of network
			for networkMsg := range serverlessInstance.Spec.Module.Spec.Containers[containers].Env {
				if serverlessInstance.Spec.Module.Spec.Containers[0].Env[networkMsg].Name[0:5] == "SCNID" {
					// 去除重复
					//TODO 单pod多scnid
					ScnIdList := strings.Split(serverlessInstance.Spec.Module.Spec.Containers[0].Env[networkMsg].Value, string(','))
					for _, scnid := range ScnIdList {
						registerMsg := quotav1.NetworkRegisterSpec{
							//Scnid:       serverlessInstance.Spec.Module.Spec.Containers[0].Env[networkMsg].Value,
							Scnid:       scnid,
							Clustername: localCluser,
						}
						networkRegisterMsg = append(networkRegisterMsg, registerMsg)
						networkRegisterMsg, _ = r.duplicateRemove(registerMsg, networkRegisterMsg)
					}
				}
			}
		}
		if quotaType != "noLimit" {
			if foundingMember {
				childClusterStateMsg := quotav1.ChildClusterState{
					ClusterName:  localCluser,
					ClusterState: requireQ,
					Quota:        0,
					QuotaRequire: quotaStep,
					QuotaRemain:  0,
				}
				childClusterState = append(childClusterState, childClusterStateMsg)
			} else {
				childClusterStateMsg := quotav1.ChildClusterState{
					ClusterName:  localCluser,
					ClusterState: steady,
					Quota:        0,
					QuotaRequire: 0,
					QuotaRemain:  0,
				}
				childClusterState = append(childClusterState, childClusterStateMsg)
			}
		}
		klog.Infof("networkRegisterMsg is: %s", networkRegisterMsg)
		quotaSpec := quotav1.QuotaSpec{
			SupervisorName:    parentCLuster,
			LocalName:         localCluser,
			ChildClusterState: childClusterState,
			//网络注册相关信息需从pod的annotation中提取并写入
			NetworkRegister: networkRegisterMsg,
			ChildName:       []string{},
			ClusterAreaType: "cluster",
		}

		quotas.Spec = quotaSpec
		//time.Sleep(time.Duration(5) * time.Second)

		klog.Infof("create obj is :%s ", quotas)
		//初始化创建quota不需要抢占锁
		err := r.Create(ctx, quotas)
		if err != nil {
			return err
		}
		return nil
	} else if action == "update" {
		quotasOld := &quotav1.Quota{}
		err := r.Get(ctx, req.NamespacedName, quotasOld)
		if err != nil {
			return err
		}
		//TODO Next S: global quota increase or decrease
		//TODO Next S: cluster serverless  return or new
		globalQuota := 0
		if serverlessInstance.Spec.Workload.TraitServerless.MaxQPS > 0 {
			globalQuota = int(serverlessInstance.Spec.Workload.TraitServerless.MaxQPS)
		} else if serverlessInstance.Spec.Workload.TraitServerless.MaxReplicas > 0 {
			globalQuota = int(serverlessInstance.Spec.Workload.TraitServerless.MaxReplicas)
		}
		if quotasOld.Labels["quota.cluster.pml.com.cn/globalQuota"] == strconv.Itoa(globalQuota) {
			return nil
		}
		quotasOld.Labels["quota.cluster.pml.com.cn/globalQuota"] = strconv.Itoa(globalQuota)
		klog.Infof("updates on the spec of quota %q, syncing start", quotasOld.Name)
		//time.Sleep(time.Duration(5) * time.Second)
		if locker, err := etcd_lock.New(etcdEndpointQuota, option); err != nil {
			klog.Infof("创建锁失败：%+v", err)
		} else if who, ok := locker.Acquire(name + namespace); ok {
			defer locker.Release()
			// 抢到锁后执行业务逻辑，没有抢到则退出
			klog.Infof("进程 %+v 持有锁 %+v 正在处理任务中...", os.Getpid(), locker.GetId())
			err = r.Update(ctx, quotasOld)
			if err != nil {
				return err
			}
			klog.Infof("进程 %+v 的任务处理完了", os.Getpid())
		} else {
			klog.Infof("获取锁失败，锁现在在 %+v 手中", who)
			return err
		}
		klog.Infof("updates on the spec of quota %q, syncing done", quotasOld.Name)
	}
	return nil
}

func (r *ServerlessReconciler) GetPodQuotaOfServerless(ctx context.Context, req ctrl.Request, podName string, step int32) (bool, error) {
	//judge type
	quotas := &quotav1.Quota{}
	err := r.Get(ctx, req.NamespacedName, quotas)
	if err != nil {
		return false, err
	}
	if len(quotas.Spec.PodQpsQuota) > 0 {
		for num, podQpsQuota := range quotas.Spec.PodQpsQuota {
			if podName == podQpsQuota.PodName {
				if int32(podQpsQuota.QpsQuota-quotas.Spec.PodQpsReal[num].QpsReal) >= step {
					return true, nil
				} else {
					return false, nil
				}
			}
		}
	}
	return false, nil
}

func (r *ServerlessReconciler) qpsOfPodQuotaInit(ctx context.Context, req ctrl.Request, etcdEndpoint string, localClusers string, podName string, quota int) error {
	var etcdEndpointQuota = []string{etcdEndpoint}
	klog.Info(etcdEndpointQuota)
	quotasOld := &quotav1.Quota{}
	err := r.Get(ctx, req.NamespacedName, quotasOld)
	if err != nil {
		return err
	}
	quotasOld.Spec.PodQpsQuota = append(quotasOld.Spec.PodQpsQuota, quotav1.PodQpsQuotaSpec{
		PodName:     podName,
		QpsQuota:    quota,
		ClusterName: localClusers,
	})
	quotasOld.Spec.PodQpsReal = append(quotasOld.Spec.PodQpsReal, quotav1.PodQpsQuotaRealSpec{
		PodName: podName,
		QpsReal: int(0),
	})
	quotasOld.Spec.PodQpsIncreaseOrDecrease = append(quotasOld.Spec.PodQpsIncreaseOrDecrease, quotav1.PodQpsIncreaseOrDecreaseSpec{
		PodName:               podName,
		QpsIncreaseOrDecrease: int(0),
	})

	var option = etcd_lock.Option{
		ConnectionTimeout: 5 * time.Second,
		Prefix:            "ServerlessQuotaLocker:",
		Debug:             false,
	}
	//TODO Next S: global quota increase or decrease
	//获取etcd锁再更新，否则返回错误，等待下一个周期
	//time.Sleep(time.Duration(5) * time.Second)
	if locker, err := etcd_lock.New(etcdEndpointQuota, option); err != nil {
		klog.Infof("创建锁失败：%+v", err)
		//return err
	} else if who, ok := locker.Acquire(req.Name + req.Namespace); ok {
		defer locker.Release()
		// 抢到锁后执行业务逻辑，没有抢到则退出
		klog.Infof("进程 %+v 持有锁 %+v 正在处理任务中...", os.Getpid(), locker.GetId())
		err = r.Update(ctx, quotasOld)
		if err != nil {
			return err
		}
		klog.Infof("进程 %+v 的任务处理完了", os.Getpid())
	} else {
		klog.Infof("获取锁失败，锁现在在 %+v 手中", who)
		return err
	}
	klog.Infof("updates on the spec of quota:pod quota Spec  %q, syncing done", quotasOld.Name)
	return nil
}

func (r *ServerlessReconciler) duplicateRemove(registerMsg quotav1.NetworkRegisterSpec, networkRegisterMsg []quotav1.NetworkRegisterSpec) ([]quotav1.NetworkRegisterSpec, error) {
	var in = 0
	for i, msg := range networkRegisterMsg {
		if registerMsg.Scnid == msg.Scnid && registerMsg.Clustername == msg.Clustername {
			in = in + 1
			if in == 2 {
				in = 0
				networkRegisterMsg = append(networkRegisterMsg[:i], networkRegisterMsg[i+1:]...)
			}
		}
	}
	networkRegisterMsg = r.nilElementDrop(networkRegisterMsg)
	klog.Info("networkRegisterMsg is : %s", networkRegisterMsg)
	return networkRegisterMsg, nil
}

func (r *ServerlessReconciler) nilElementDrop(networkRegisterMsg []quotav1.NetworkRegisterSpec) []quotav1.NetworkRegisterSpec {
	var networkRegisterMsgDelNil []quotav1.NetworkRegisterSpec
	for _, element := range networkRegisterMsg {
		typeofnetwork := reflect.TypeOf(element).Kind()
		klog.Info(typeofnetwork)
		if reflect.TypeOf(element).Kind() == reflect.Struct {
			networkRegisterMsgDelNil = append(networkRegisterMsgDelNil, element)
		}
	}
	return networkRegisterMsgDelNil
}
