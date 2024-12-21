package api

import (
	"io"
	"strings"

	"encoding/json"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/common/model"
	"k8s.io/klog/v2"

	common2 "github.com/SUMMERLm/serverless/pkg/serverless_hpa/common"
)

var done bool
var doScale = "0"
var doQuotaScale = "0"
var stateDone = 200
var stateError0 = 500
var stateError1 = 501
var stateError2 = 502
var stateError3 = 400

type HpaMsg struct {
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
	HpaAction string `json:"hpaAction"`
}

func GetIndex(c *gin.Context) {
	c.String(stateDone, "Hello, get test")
}

func PostTest(c *gin.Context) {
	hpaMsg := c.GetHeader("Alertname")
	klog.Infof(hpaMsg)
	c.String(stateDone, "Hello，post test")
}

func ServerLessHpa(c *gin.Context) {
	//   get message by params
	hpaMsg := c.GetHeader("Alertname")
	//   TODO 上线还原
	body := c.Request.Body
	x, _ := io.ReadAll(body)
	var qResult model.Vector
	err := json.Unmarshal(x, &qResult)
	if err != nil {
		klog.Infof(err.Error())
		klog.Infof("Hpa header msg from prometheus is:: %s\n", hpaMsg)
		done = false
		c.JSON(stateError1, done)
		//return
	}
	if len(qResult) == 0 {
		done = false
		klog.Infof("Hpa header msg from prometheus is:: %s\n", hpaMsg)
		c.JSON(stateError2, done)
		//return
	}
	singleValue := *qResult[0]
	doScale = singleValue.Value.String()
	//doScale := "2"
	if doScale != "0" {
		klog.Infof("Hpa header msg from prometheus is:: %s\n", hpaMsg)
		hpaMsgList := strings.Split(hpaMsg, string('+'))
		for _, str := range hpaMsgList {
			klog.Infof("Hpa header msg from prometheus is:: %s\n", str)
		}
		namespaceHpa := hpaMsgList[0]
		nameHpa := hpaMsgList[1]
		hpaAction := hpaMsgList[2]
		hpa := common2.Hpa{
			NameHpa:           nameHpa,
			NamespaceHpa:      namespaceHpa,
			NameSpaceQpsQuota: "hypermonitor",
		}
		//   common.Init()
		switch hpaAction {
		case "scale_up":
			klog.Infof("Scale_up: serverless  name: %s, namespace: %s,  hpaAction: %s;\n", nameHpa, namespaceHpa, hpaAction)
			done = hpa.HpaScaleUp()
		case "scale_down":
			klog.Infof("Scale_down: serverless  name: %s, namespace: %s, hpaAction: %s;\n", nameHpa, namespaceHpa, hpaAction)
			done = hpa.HpaScaleDown()
		default:
			klog.Infof("nothing to do....")
			done = true
		}
		if done {
			c.JSON(stateDone, done)
		} else {
			c.JSON(stateError0, done)
		}
	}
}

func ServerLessQPSQuotaHpa(c *gin.Context) {
	//   get message by params
	hpaMsg := c.GetHeader("Alertname")
	//   TODO 上线还原
	body := c.Request.Body
	x, _ := io.ReadAll(body)
	var qResult model.Vector
	err := json.Unmarshal(x, &qResult)
	if err != nil {
		klog.Infof(err.Error())
		klog.Infof("Hpa header msg from prometheus is:: %s\n", hpaMsg)
		done = false
		c.JSON(stateError1, done)
		//return
	}
	if len(qResult) == 0 {
		done = false
		klog.Infof("Hpa header msg from prometheus is:: %s\n", hpaMsg)
		c.JSON(stateError2, done)
		//return
	}
	singleValue := *qResult[0]
	doQuotaScale = singleValue.Value.String()
	//   doQuotaScale = "2"
	if doQuotaScale != "0" {
		klog.Infof("Quota hpa header msg from prometheus is: %s\n", hpaMsg)
		hpaMsgList := strings.Split(hpaMsg, string('+'))
		for _, str := range hpaMsgList {
			klog.Infof("Quota hpa header msg from prometheus is: %s\n", str)
		}
		namespaceQuota := hpaMsgList[0]
		nameQuota := hpaMsgList[1]
		nameOfPod := hpaMsgList[2]
		quotaAction := hpaMsgList[3]
		localQuota := hpaMsgList[4]
		realQPS := hpaMsgList[5]
		quota := common2.Quota{
			NameQuota:         nameQuota,
			NamespaceQuota:    namespaceQuota,
			NameOfPod:         nameOfPod,
			LocalQuota:        localQuota,
			RealQps:           realQPS,
			NameSpaceQpsQuota: "hypermonitor",
		}
		//   common.Init()
		switch quotaAction {
		case "pod_qps_quota_up":
			klog.Infof("Pod quota Scale_up: serverless  name: %s, namespace: %s,  quotaAction: %s;\n", nameQuota, namespaceQuota, quotaAction)
			done = quota.QuotaRequire()
		case "pod_qps_quota_down":
			klog.Infof("Pod quota Scale_down: serverless  name: %s, namespace: %s,  quotaAction: %s;\n", nameQuota, namespaceQuota, quotaAction)
			done = quota.QuotaReturn()
		default:
			klog.Infof("Quota nothing to do ....")
			done = true
		}
		if done {
			c.JSON(stateDone, done)
		} else {
			c.JSON(stateError0, done)
		}
	}
}

func ZeroAlert(c *gin.Context) {
	var reqInfo common2.AlertRequest
	err := c.BindJSON(&reqInfo)
	if err != nil {
		klog.Info(err)
		c.JSON(stateDone, gin.H{"errcode": stateError3, "description": "Post Data Err"})
		//return
	} else {
		done = reqInfo.ZeroAlertInit()
		c.JSON(stateDone, done)
	}
	return
}

func CdnProviderRegister(c *gin.Context) {
	var reqInfo common2.CdnProvider
	err := c.BindJSON(&reqInfo)
	if err != nil {
		klog.Info(err)
		c.JSON(stateDone, gin.H{"errcode": stateError3, "description": "Post Data Err"})
		//return
	} else {
		done = reqInfo.Register()
		c.JSON(stateDone, done)
	}
	return
}

func CdnProviderRecycle(c *gin.Context) {
	var reqInfo common2.CdnProvider
	err := c.BindJSON(&reqInfo)
	if err != nil {
		klog.Info(err)
		c.JSON(stateDone, gin.H{"errcode": stateError3, "description": "Post Data Err"})
		//return
	} else {
		done = reqInfo.Recycle()
		c.JSON(stateDone, done)
	}
	return
}

func CdnProviderUpdate(c *gin.Context) {
	var reqInfo common2.CdnProvider
	err := c.BindJSON(&reqInfo)
	if err != nil {
		klog.Info(err)
		c.JSON(stateDone, gin.H{"errcode": stateError3, "description": "Post Data Err"})
		//return
	} else {
		done = reqInfo.Update()
		c.JSON(stateDone, done)
	}
	return
}
