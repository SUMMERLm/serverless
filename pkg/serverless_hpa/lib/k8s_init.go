package lib

import (
	"context"

	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/klog/v2"

	metaV1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var K8sClient *kubernetes.Clientset
var DynamicClient *dynamic.DynamicClient

var EtcdEndpointQuota []string
var HpaWebURL string

func init() {
	config, err := clientcmd.BuildConfigFromFlags("", "/conf/serverless/config")
	if err != nil {
		panic(err.Error())
	}
	// create the clientset
	K8sClient, err = kubernetes.NewForConfig(config)
	if err != nil {
		panic(err)
	}

	DynamicClient, _ = dynamic.NewForConfig(config)

	clusterConf, err := K8sClient.CoreV1().ConfigMaps("serverless-system").Get(context.TODO(), "serverless-hpa-conf", metaV1.GetOptions{})
	if err != nil {
		klog.Error(err)
	}
	EtcdEndpointQuota = []string{clusterConf.Data["etcd-endpoint"]}
	HpaWebURL = clusterConf.Data["qps-quota-url"]
	klog.Info("etcd endpoint is: %s", EtcdEndpointQuota)
	klog.Info("hpa  url is: %s", HpaWebURL)
}
