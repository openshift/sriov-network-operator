/*
Copyright 2023.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

	http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/
package main

import (
	"flag"

	"github.com/spf13/cobra"
	"sigs.k8s.io/controller-runtime/pkg/log"

	snolog "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/log"
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
	snolog.BindFlags(flag.CommandLine)
	rootCmd.PersistentFlags().AddGoFlagSet(flag.CommandLine)
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		log.Log.Error(err, "error executing sriov-network-config-daemon")
	}
}
