package controller

import (
	"context"
	"strconv"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/klog/v2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	serverlessv1 "github.com/SUMMERLm/serverless/api/v1"
)

func (r *ServerlessReconciler) createDeployment(ctx context.Context, serverless *serverlessv1.Serverless) error {
	//serverless init num of deploy.
	foundingMenmber := serverless.Spec.Workload.TraitServerless.Foundingmember
	klog.Infof("Founding member : ", foundingMenmber)

	replica := int32(0)
	//noQuota type no need to apply for quota
	if foundingMenmber == true && serverless.Spec.Workload.TraitServerless.MaxQPS == 0 && serverless.Spec.Workload.TraitServerless.MaxReplicas == 0 {
		replica = int32(1)
	}
	klog.Infof("start create deployment", serverless.Spec.Name)
	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: serverless.Spec.Namespace,
			Name:      serverless.Spec.Name,
		},
		Spec: appsv1.DeploymentSpec{
			// init num is 1 for serverless
			Replicas: &replica,
			Selector: &metav1.LabelSelector{
				MatchLabels: serverless.Spec.Module.Labels,
			},
		},
	}
	deployment.Spec.Template = serverless.Spec.Module
	// owner reference to be used when delete serverless cr
	if err := controllerutil.SetControllerReference(serverless, deployment, r.Scheme); err != nil {
		return err
	}
	// new deployment
	if err := r.Create(ctx, deployment); err != nil {
		return err
	}

	klog.Infof("end create deployment with replica is: ", *deployment.Spec.Replicas)
	klog.Infof("end create deployment name is: ", serverless.Spec.Name)
	klog.Infof("create deployment success")
	return nil
}

func (r *ServerlessReconciler) updateDeployment(ctx context.Context, req ctrl.Request, serverless *serverlessv1.Serverless, deployment *appsv1.Deployment, replicaSet *appsv1.ReplicaSet, podsOfReplica *corev1.PodList, localCluser string) error {
	foundingMenmber := serverless.Spec.Workload.TraitServerless.Foundingmember
	klog.Infof("Founding member : ", foundingMenmber)
	replica := deployment.Spec.Replicas
	deployMentByServerless := &appsv1.Deployment{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Deployment",
			APIVersion: "apps/v1",
		}, ObjectMeta: metav1.ObjectMeta{
			Namespace: serverless.Spec.Namespace,
			Name:      serverless.Spec.Name,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: replica,
			Selector: &metav1.LabelSelector{
				MatchLabels: serverless.Spec.Module.Labels,
			},
		},
	}
	deployMentByServerless.Spec.Template = serverless.Spec.Module

	if err := controllerutil.SetControllerReference(serverless, deployMentByServerless, r.Scheme); err != nil {
		klog.Error(err)
		return err
	}
	klog.Infof("start update deployment")
	if err := r.Update(ctx, deployMentByServerless); err != nil {
		klog.Error(err, "update deployment error")
		return err
	}
	klog.Infof("end update deployment relice is: ", *deployMentByServerless.Spec.Replicas)
	klog.Infof("end update deployment name is: ", serverless.Spec.Name)
	klog.Infof("update deployment success")
	var replicaSetList appsv1.ReplicaSetList
	if err := r.List(ctx, &replicaSetList, client.InNamespace(req.Namespace), client.HasLabels{"apps.gaia.io/component: sample-s2-6"}); err != nil {
		return err
	}
	if err := r.List(ctx, podsOfReplica, client.InNamespace(req.Namespace), client.MatchingLabels{"apps.gaia.io/component": req.Name}); err != nil {
		return err
	}

	if serverless.Spec.Workload.TraitServerless.QpsStep > 0 {
		//场景1 qps
		//qps场景下注册pod的quota，其他场景不需要注册pod的qps
		//当前serverless下属pod中状态为ready的pod进行qps quota初始化
		//***调谐 pod伸缩或者意外删除重启等,避免不必要的quota申请回收*** S2
		//quota加锁进行使用
		//1 聚合当前所有pod的qps quota使用情况：匹配pod的扩缩导致的相关pod qps资源的振荡
		//聚合当前pod的quota qps使用情况，根据使用情况进行当前pod的qps annotation patch
		//如果有：剩余，则直接进行pod属性的patch，更新quota CR的qouta申请及回收相关，进行quota申请或者回收
		//如果没有：申请quota，申请到则进行pod annotation的patch。这时候需要走到quota控制器进行pod annotation的patch

		//元老实例特殊流程
		if serverless.Spec.Workload.TraitServerless.Foundingmember == true && len(podsOfReplica.Items) == 1 {
			for _, pod := range podsOfReplica.Items {
				klog.Infof("podName: %s, qpsStep: %s,IsPodReady: %s", pod.Name, strconv.Itoa(int(serverless.Spec.Workload.TraitServerless.QpsStep)), IsPodReady(&pod))
				//2 pod状态为ready
				//3 pod的annotation qps首次设置
				//4 （元老实例配额预分配）
				if pod.Annotations["qps"] == "" {
					//quota是否有剩余
					klog.Infof("start annotation pod of Founding memeber pod qps quota Etc")
					patchMsg := "{\"metadata\":{\"annotations\":{\"qps\":\"" + strconv.Itoa(int(serverless.Spec.Workload.TraitServerless.QpsStep)) + "\"}}}"
					klog.Infof("annotation of pod is: %s", patchMsg)
					patch := []byte(patchMsg)
					_ = r.Patch(context.Background(), &pod, client.RawPatch(types.StrategicMergePatchType, patch))
				}
			}

			return nil
		}
	} else if serverless.Spec.Workload.TraitServerless.ResplicasStep > 0 {
		//场景2 副本数作为quota
		return nil
	} else {
		//场景3 无限制
		return nil
	}

	return nil
}
