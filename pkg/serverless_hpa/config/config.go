package config

import (
	"fmt"
	"github.com/spf13/viper"
)

func InitConfig() {
	viper.AddConfigPath("./config")
	viper.SetConfigName("config")
	viper.SetConfigType("yaml")
	fmt.Printf("init config start")
	if err := viper.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); ok {
			panic("未找到配置文件")
		} else {
			panic("加载配置文件失败")
		}
	}
	viper.WatchConfig()
	fmt.Printf("init config done\n")

}
