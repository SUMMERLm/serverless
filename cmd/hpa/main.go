package main

import (
	"github.com/gin-gonic/gin"
	"github.com/spf13/viper"

	"github.com/SUMMERLm/serverless/pkg/serverless_hpa/config"
	"github.com/SUMMERLm/serverless/pkg/serverless_hpa/route"
)

func main() {
	r := route.InitRouter()
	//config init
	config.InitConfig()
	//listen on 8000
	gin.SetMode(viper.GetString("server.run_mode"))
	r.Run(viper.GetString("server.addr"))
}
