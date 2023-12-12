package lib

import (
	"context"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/klog/v2"
)

var K8sClient *kubernetes.Clientset
var DynamicClient *dynamic.DynamicClient

// TODO 上线还原
var EtcdEndpointQuota []string
var HpaWebUrl string

func init() {
	//本地调试，上线还原
	/*
		var kubeconfig *string
		config.InitConfig()
		k8s_config_path := viper.GetString("k8sconfig.path")
		EtcdEndpointQuota = viper.GetStringSlice("etcd.endpoint")
		kubeconfig = flag.String("kubeconfig", k8s_config_path, "absolute path to the kubeconfig file")

		flag.Parse()
		config, err := clientcmd.BuildConfigFromFlags("", *kubeconfig)
		// create the clientset
		K8sClient, err = kubernetes.NewForConfig(config)
		if err != nil {
			panic(err)
		}

		DynamicClient, _ = dynamic.NewForConfig(config)
	*/
	//*
	//上线还原，走配置文件
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

	clusterConf, err := K8sClient.CoreV1().ConfigMaps("serverless-system").Get(context.TODO(), "serverless-hpa-conf", metav1.GetOptions{})
	if err != nil {
		klog.Error(err)
	}
	EtcdEndpointQuota = []string{clusterConf.Data["etcd-endpoint"]}
	HpaWebUrl = clusterConf.Data["qps-quota-url"]
	klog.Info("etcd endpoint is: %s", EtcdEndpointQuota)
	klog.Info("hpa  url is: %s", HpaWebUrl)
	//*/
}