package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/version"
)

var (
	versionCmd = &cobra.Command{
		Use:   "version",
		Short: "Print the version number of SR-IoV Network Config Daemon",
		Long:  `All software has versions. This is SR-IoV Network Config Daemon's.`,
		Run:   runVersionCmd,
	}
)

func init() {
	rootCmd.AddCommand(versionCmd)
}

func runVersionCmd(cmd *cobra.Command, args []string) {
	program := "SriovNetworkConfigDaemon"
	version := "v" + version.Version.String()

	fmt.Println(program, version)
}
