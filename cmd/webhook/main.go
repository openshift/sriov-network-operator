package main

import (
	"flag"
	"os"

	"github.com/golang/glog"
	"github.com/spf13/cobra"
)

const (
	componentName = "sriov-network-operator-webhook"
)

var (
	rootCmd = &cobra.Command{
		Use:   componentName,
		Short: "Run SR-IoV Operator Webhook Daemon",
		Long:  "Run Webhook Daemon which validates/mutates the Custom Resource of the SR-IoV Network Operator",
	}
)

func init() {
	rootCmd.PersistentFlags().AddGoFlagSet(flag.CommandLine)
}

func main() {
	flag.Set("logtostderr", "true")
	flag.Parse()
	glog.Info("Run sriov-network-operator-webhook")

	if err := rootCmd.Execute(); err != nil {
		glog.Exitf("Error executing sriov-network-operator-webhook: %v", err)
		os.Exit(1)
	}
}
