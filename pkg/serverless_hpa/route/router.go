package route

// 路由包

import (
	"github.com/SUMMERLm/serverless/pkg/serverless_hpa/api"
	"github.com/gin-gonic/gin"
)

func InitRouter() *gin.Engine {
	router := gin.Default()
	// get Test
	router.GET("/", api.GetIndex)
	// post Test
	router.POST("/test", api.PostTest)
	// serverless hpa up or down
	router.POST("/serverless_hpa", api.ServerLessHpa)
	// serverless pod quota manage
	router.POST("/serverless_qps_quota_hpa", api.ServerLessQPSQuotaHpa)
	// 0-1 manage
	router.POST("/serverless/zeroalert", api.ZeroAlert)
	// cdn provider
	router.POST("/cdnsuppliers/new", api.CdnProviderRegister)
	router.POST("/cdnsuppliers/recycle", api.CdnProviderRecycle)
	router.POST("/cdnsuppliers/update", api.CdnProviderUpdate)

	return router
}
