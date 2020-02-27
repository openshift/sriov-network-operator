package main

import (
	"flag"

	"github.com/fsnotify/fsnotify"
	"github.com/golang/glog"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"

	"github.com/openshift/sriov-network-operator/pkg/version"
)

const (
	componentName = "sriov-network-config-daemon"
)

var (
	rootCmd = &cobra.Command{
		Use:   componentName,
		Short: "Run SR-IoV Network Config Daemon",
		Long:  "Runs the SR-IoV Network Config Daemon which handles SR-IoV components configuration on the host",
	}
)

func init() {
	pflag.CommandLine.AddGoFlagSet(flag.CommandLine)
}

func main() {
	viper.SetConfigName("config")
	viper.SetConfigType("json")
	viper.AddConfigPath(".")
	viper.AddConfigPath("/etc/sriov-network-config-daemon")
	err := viper.ReadInConfig()
	if err != nil {
		glog.Warningf("Fail to read config file: %v", err)
	}
	level := viper.GetString("logLevel")
	glog.Infof("Set log verbose level to: %s", level)
	flag.Set("v", level)
	flag.Set("alsologtostderr", "true")
	flag.Parse()
	// To help debugging, immediately log version
	glog.Infof("Version: %+v", version.Version)

	viper.WatchConfig()
	viper.OnConfigChange(func(e fsnotify.Event) {
		glog.Infof("Config file changed: %s", e.Name)
		level := viper.GetString("logLevel")
		glog.Infof("Set log verbose level to: %s", level)
		flag.Set("v", level)
		flag.Parse()
	})
	if err := rootCmd.Execute(); err != nil {
		glog.Exitf("Error executing mcd: %v", err)
	}
}
