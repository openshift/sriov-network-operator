package main

import (
	"flag"
	"os"

	"github.com/golang/glog"
	"github.com/pliurh/sriov-network-operator/pkg/version"
	"github.com/spf13/cobra"
)

var (
	startCmd = &cobra.Command{
		Use:   "start",
		Short: "Starts Machine Config Daemon",
		Long:  "",
		Run:   runStartCmd,
	}

	startOpts struct {
		kubeconfig             string
		nodeName               string
	}
)

func init() {
	rootCmd.AddCommand(startCmd)
	startCmd.PersistentFlags().StringVar(&startOpts.kubeconfig, "kubeconfig", "", "Kubeconfig file to access a remote cluster (testing only)")
	startCmd.PersistentFlags().StringVar(&startOpts.nodeName, "node-name", "", "kubernetes node name daemon is managing.")
}

func runStartCmd(cmd *cobra.Command, args []string) {
	flag.Set("logtostderr", "true")
	flag.Parse()

	// To help debugging, immediately log version
	glog.Infof("Version: %+v", version.Version)

	if startOpts.nodeName == "" {
		name, ok := os.LookupEnv("NODE_NAME")
		if !ok || name == "" {
			glog.Fatalf("node-name is required")
		}
		startOpts.nodeName = name
	}

	// Ensure that the rootMount exists
	if _, err := os.Stat(startOpts.rootMount); err != nil {
		if os.IsNotExist(err) {
			glog.Fatalf("rootMount %s does not exist", startOpts.rootMount)
		}
		glog.Fatalf("Unable to verify rootMount %s exists: %s", startOpts.rootMount, err)
	}

}
