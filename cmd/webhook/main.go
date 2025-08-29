package main

import (
	"flag"
	"os"

	"github.com/spf13/cobra"
	"sigs.k8s.io/controller-runtime/pkg/log"

	snolog "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/log"
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
	snolog.BindFlags(flag.CommandLine)
	rootCmd.PersistentFlags().AddGoFlagSet(flag.CommandLine)
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		log.Log.Error(err, "Error executing sriov-network-operator-webhook")
		os.Exit(1)
	}
}
