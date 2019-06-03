package main

import (
	"flag"

	"github.com/golang/glog"
	"github.com/spf13/cobra"
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
	rootCmd.PersistentFlags().AddGoFlagSet(flag.CommandLine)
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		glog.Exitf("Error executing mcd: %v", err)
	}
}
