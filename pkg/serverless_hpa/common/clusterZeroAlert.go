package common

import (
	"context"
	"errors"
	"os"
	"time"

	"github.com/SUMMERLm/serverless/pkg/comm"
	"github.com/SUMMERLm/serverless/pkg/etcd_lock"
	"github.com/SUMMERLm/serverless/pkg/serverless_hpa/lib"
	"github.com/avast/retry-go"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/klog/v2"

	quotaV1 "github.com/SUMMERLm/quota/api/v1"
	metaV1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type AlertRequest struct {
	FieldName              string                   `json:"fieldName"`
	ServerlessClusterAlert []ServerlessClusterAlert `json:"serverlessClusterAlert"` // 支持多个集群会被同时触发
	Sid                    string                   `json:"sid"`                    // 集群的sid
}

type ServerlessClusterAlert struct {
	ClusterName string `json:"clusterName"` // cluster名称
	ZeroAlert   bool   `json:"zeroAlert"`   // 0-1触发标志
}

func (alertRequest *AlertRequest) ZeroAlertInit() bool {
	klog.Infof(alertRequest.FieldName)

	klog.Infof(alertRequest.ServerlessClusterAlert[0].ClusterName)
	klog.Infof(alertRequest.Sid)
	go alertRequest.doZeroAlertInit()
	return true
}

func (alertRequest *AlertRequest) doZeroAlertInit() bool {
	quotaGvr := schema.GroupVersionResource{Group: "serverless.pml.com.cn", Version: "v1", Resource: "quotas"}
	resultQuotaList, err := lib.DynamicClient.Resource(quotaGvr).List(context.TODO(), metaV1.ListOptions{})
	if err != nil {
		klog.Error(err)
	}
	quotaList := &quotaV1.QuotaList{}
	if err = runtime.DefaultUnstructuredConverter.FromUnstructured(resultQuotaList.UnstructuredContent(), quotaList); err != nil {
		klog.Error(err)
	}
	klog.Info(len(quotaList.Items))

	for _, quotaMsg := range quotaList.Items {
		for _, netWorkMsg := range quotaMsg.Spec.NetworkRegister {
			klog.Infof(netWorkMsg.Scnid)
			if netWorkMsg.Scnid == alertRequest.Sid {
				quotaName := quotaMsg.Name
				quotaNamespace := quotaMsg.Namespace
				resultAlertQuota, _ := lib.DynamicClient.Resource(quotaGvr).Namespace(quotaNamespace).Get(context.TODO(), quotaName, metaV1.GetOptions{})
				quotaChildAlert, _, err := unstructured.NestedSlice(resultAlertQuota.Object, "spec", "childAlert")
				for _, alertCluster := range alertRequest.ServerlessClusterAlert {
					ChildAlertMsg := map[string]interface{}{"clusterName": alertCluster.ClusterName, "alert": alertCluster.ZeroAlert}

					klog.Info("quotaChildAlert is:  %s", quotaChildAlert)
					if err != nil {
						klog.Errorf("quota alert msg not found or error in spec: %v", err)
					}
					var in bool = false
					for _, item := range quotaChildAlert {
						alertMSg := item.(map[string]interface{})
						if alertMSg["clusterName"] == alertCluster.ClusterName {
							in = true
						}
					}
					if !in {
						quotaChildAlert = append(quotaChildAlert, ChildAlertMsg)
						klog.Info(quotaChildAlert)
					}
				}
				// 重试多次,成功一次则
				err = retry.Do(
					func() error {
						var locker *etcd_lock.EtcdLocker
						if locker, err = etcd_lock.New(lib.EtcdEndpointQuota, quoaHpaOption); err != nil {
							klog.Infof("创建锁失败：%+v", err)
							return err
						} else if who, ok := locker.Acquire(quotaMsg.Name + quotaMsg.Namespace); ok {
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

							if err4 := unstructured.SetNestedSlice(resultAlertQuota.Object, quotaChildAlert, "spec", "childAlert"); err4 != nil {
								klog.Error(err4)
								return err4
							}
							_, updateQuotaErr := lib.DynamicClient.Resource(quotaGvr).Namespace(quotaNamespace).Update(context.TODO(), resultAlertQuota, metaV1.UpdateOptions{})
							if updateQuotaErr != nil {
								klog.Info(updateQuotaErr)
								return updateQuotaErr
							}
							klog.Infof("The %s time to alert 0-1 of serverless %s", quotaMsg.Name)
						} else {
							klog.Infof("获取锁失败，锁现在在 %+v 手中", who)
							return errors.New("can not get mux ")
						}
						return err
					},
					retry.Delay(time.Second),
					retry.Attempts(comm.ZeroAlertRetryTime),
					retry.DelayType(retry.FixedDelay),
				)
				if err != nil {
					klog.Infof("scale up  failed: %v", err)
					return false
				}
			}
		}
	}
	return true
}
