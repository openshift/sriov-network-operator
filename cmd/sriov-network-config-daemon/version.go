package main

import (
	"flag"
	"fmt"

	"github.com/pliurh/sriov-network-operator/pkg/version"
	"github.com/spf13/cobra"
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
	flag.Set("logtostderr", "true")
	flag.Parse()

	program := "SriovNetworkConfigDaemon"
	version := "v" + version.Version.String()

	fmt.Println(program, version)
}
