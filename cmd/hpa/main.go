package main

import (
	"github.com/SUMMERLm/serverless/pkg/serverless_hpa/route"
	"github.com/gin-gonic/gin"
	"k8s.io/klog/v2"
)

func main() {
	r := route.InitRouter()
	// listen on 8000
	gin.SetMode("debug")
	err := r.Run(":8000")
	if err != nil {
		klog.Error(err)
	}
}
