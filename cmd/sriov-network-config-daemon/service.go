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
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"sigs.k8s.io/controller-runtime/pkg/log"

	sriovv1 "github.com/k8snetworkplumbingwg/sriov-network-operator/api/v1"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/consts"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/helper"
	snolog "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/log"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/platforms"
	plugin "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/plugins"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/plugins/generic"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/plugins/virtual"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/systemd"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/vars"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/version"
)

var (
	serviceCmd = &cobra.Command{
		Use:   "service",
		Short: "Starts SR-IOV service Config",
		Long:  "",
		RunE:  runServiceCmd,
	}
)

func init() {
	rootCmd.AddCommand(serviceCmd)
}

func runServiceCmd(cmd *cobra.Command, args []string) error {
	// init logger
	snolog.InitLog()
	setupLog := log.Log.WithName("sriov-config-service")

	// To help debugging, immediately log version
	setupLog.V(2).Info("sriov-config-service", "version", version.Version)

	setupLog.V(0).Info("Starting sriov-config-service")

	// Mark that we are running on host
	vars.UsingSystemdMode = true
	vars.InChroot = true

	supportedNicIds, err := systemd.ReadSriovSupportedNics()
	if err != nil {
		setupLog.Error(err, "failed to read list of supported nic ids")
		sriovResult := &systemd.SriovResult{
			SyncStatus:    consts.SyncStatusFailed,
			LastSyncError: fmt.Sprintf("failed to read list of supported nic ids: %v", err),
		}
		err = systemd.WriteSriovResult(sriovResult)
		if err != nil {
			setupLog.Error(err, "failed to write sriov result file", "content", *sriovResult)
			return fmt.Errorf("sriov-config-service failed to write sriov result file with content %v error: %v", *sriovResult, err)
		}
		return fmt.Errorf("sriov-config-service failed to read list of supported nic ids: %v", err)
	}
	sriovv1.InitNicIDMapFromList(supportedNicIds)

	nodeStateSpec, err := systemd.ReadConfFile()
	if err != nil {
		if _, err := os.Stat(systemd.SriovSystemdConfigPath); !errors.Is(err, os.ErrNotExist) {
			setupLog.Error(err, "failed to read the sriov configuration file", "path", systemd.SriovSystemdConfigPath)
			sriovResult := &systemd.SriovResult{
				SyncStatus:    consts.SyncStatusFailed,
				LastSyncError: fmt.Sprintf("failed to read the sriov configuration file in path %s: %v", systemd.SriovSystemdConfigPath, err),
			}
			err = systemd.WriteSriovResult(sriovResult)
			if err != nil {
				setupLog.Error(err, "failed to write sriov result file", "content", *sriovResult)
				return fmt.Errorf("sriov-config-service failed to write sriov result file with content %v error: %v", *sriovResult, err)
			}
		}

		nodeStateSpec = &systemd.SriovConfig{
			Spec:            sriovv1.SriovNetworkNodeStateSpec{},
			UnsupportedNics: false,
			PlatformType:    consts.Baremetal,
		}
	}

	setupLog.V(2).Info("sriov-config-service", "config", nodeStateSpec)
	hostHelpers, err := helper.NewDefaultHostHelpers()
	if err != nil {
		setupLog.Error(err, "failed to create hostHelpers")
		return updateSriovResultErr("failed to create hostHelpers")
	}
	platformHelper, err := platforms.NewDefaultPlatformHelper()
	if err != nil {
		setupLog.Error(err, "failed to create platformHelpers")
		return updateSriovResultErr("failed to create platformHelpers")
	}

	_, err = hostHelpers.TryEnableRdma()
	if err != nil {
		setupLog.Error(err, "warning, failed to enable RDMA")
	}
	hostHelpers.TryEnableTun()
	hostHelpers.TryEnableVhostNet()

	var configPlugin plugin.VendorPlugin
	var ifaceStatuses []sriovv1.InterfaceExt
	if nodeStateSpec.PlatformType == consts.Baremetal {
		// Bare metal support
		vars.DevMode = nodeStateSpec.UnsupportedNics
		ifaceStatuses, err = hostHelpers.DiscoverSriovDevices(hostHelpers)
		if err != nil {
			setupLog.Error(err, "failed to discover sriov devices on the host")
			return updateSriovResultErr(fmt.Sprintf("sriov-config-service: failed to discover sriov devices on the host:  %v", err))
		}

		// Create the generic plugin
		configPlugin, err = generic.NewGenericPlugin(hostHelpers)
		if err != nil {
			setupLog.Error(err, "failed to create generic plugin")
			return updateSriovResultErr(fmt.Sprintf("sriov-config-service failed to create generic plugin %v", err))
		}
	} else if nodeStateSpec.PlatformType == consts.VirtualOpenStack {
		err = platformHelper.CreateOpenstackDevicesInfo()
		if err != nil {
			setupLog.Error(err, "failed to read OpenStack data")
			return updateSriovResultErr(fmt.Sprintf("sriov-config-service failed to read OpenStack data: %v", err))
		}

		ifaceStatuses, err = platformHelper.DiscoverSriovDevicesVirtual()
		if err != nil {
			setupLog.Error(err, "failed to read OpenStack data")
			return updateSriovResultErr(fmt.Sprintf("sriov-config-service: failed to read OpenStack data: %v", err))
		}

		// Create the virtual plugin
		configPlugin, err = virtual.NewVirtualPlugin(hostHelpers)
		if err != nil {
			setupLog.Error(err, "failed to create virtual plugin")
			return updateSriovResultErr(fmt.Sprintf("sriov-config-service: failed to create virtual plugin %v", err))
		}
	}

	nodeState := &sriovv1.SriovNetworkNodeState{
		Spec:   nodeStateSpec.Spec,
		Status: sriovv1.SriovNetworkNodeStateStatus{Interfaces: ifaceStatuses},
	}

	_, _, err = configPlugin.OnNodeStateChange(nodeState)
	if err != nil {
		setupLog.Error(err, "failed to run OnNodeStateChange to update the generic plugin status")
		return updateSriovResultErr(fmt.Sprintf("sriov-config-service: failed to run OnNodeStateChange to update the generic plugin status %v", err))
	}

	sriovResult := &systemd.SriovResult{
		SyncStatus:    consts.SyncStatusSucceeded,
		LastSyncError: "",
	}

	err = configPlugin.Apply()
	if err != nil {
		setupLog.Error(err, "failed to run apply node configuration")
		sriovResult.SyncStatus = consts.SyncStatusFailed
		sriovResult.LastSyncError = err.Error()
	}

	err = systemd.WriteSriovResult(sriovResult)
	if err != nil {
		setupLog.Error(err, "failed to write sriov result file", "content", *sriovResult)
		return fmt.Errorf("sriov-config-service failed to write sriov result file with content %v error: %v", *sriovResult, err)
	}

	setupLog.V(0).Info("shutting down sriov-config-service")
	return nil
}

func updateSriovResultErr(errMsg string) error {
	sriovResult := &systemd.SriovResult{
		SyncStatus:    consts.SyncStatusFailed,
		LastSyncError: errMsg,
	}

	err := systemd.WriteSriovResult(sriovResult)
	if err != nil {
		log.Log.Error(err, "failed to write sriov result file", "content", *sriovResult)
		return fmt.Errorf("sriov-config-service failed to write sriov result file with content %v error: %v", *sriovResult, err)
	}
	return nil
}
