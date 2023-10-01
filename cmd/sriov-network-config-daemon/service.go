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
	"flag"
	"fmt"
	"os"

	"github.com/golang/glog"
	"github.com/spf13/cobra"

	sriovv1 "github.com/k8snetworkplumbingwg/sriov-network-operator/api/v1"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/host"
	plugin "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/plugins"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/plugins/generic"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/plugins/virtual"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/systemd"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/utils"
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
	flag.Set("logtostderr", "true")
	flag.Parse()

	// To help debugging, immediately log version
	glog.V(2).Infof("Version: %+v", version.Version)

	glog.V(0).Info("Starting sriov-config-service")
	supportedNicIds, err := systemd.ReadSriovSupportedNics()
	if err != nil {
		glog.Errorf("failed to read list of supported nic ids")
		sriovResult := &systemd.SriovResult{
			SyncStatus:    "Failed",
			LastSyncError: fmt.Sprintf("failed to read list of supported nic ids: %v", err),
		}
		err = systemd.WriteSriovResult(sriovResult)
		if err != nil {
			glog.Errorf("sriov-config-service failed to write sriov result file with content %v error: %v", *sriovResult, err)
			return fmt.Errorf("sriov-config-service failed to write sriov result file with content %v error: %v", *sriovResult, err)
		}
		return fmt.Errorf("sriov-config-service failed to read list of supported nic ids: %v", err)
	}
	sriovv1.InitNicIDMapFromList(supportedNicIds)

	nodeStateSpec, err := systemd.ReadConfFile()
	if err != nil {
		if _, err := os.Stat(systemd.SriovSystemdConfigPath); !errors.Is(err, os.ErrNotExist) {
			glog.Errorf("failed to read the sriov configuration file in path %s: %v", systemd.SriovSystemdConfigPath, err)
			sriovResult := &systemd.SriovResult{
				SyncStatus:    "Failed",
				LastSyncError: fmt.Sprintf("failed to read the sriov configuration file in path %s: %v", systemd.SriovSystemdConfigPath, err),
			}
			err = systemd.WriteSriovResult(sriovResult)
			if err != nil {
				glog.Errorf("sriov-config-service failed to write sriov result file with content %v error: %v", *sriovResult, err)
				return fmt.Errorf("sriov-config-service failed to write sriov result file with content %v error: %v", *sriovResult, err)
			}
		}

		nodeStateSpec = &systemd.SriovConfig{
			Spec:            sriovv1.SriovNetworkNodeStateSpec{},
			UnsupportedNics: false,
			PlatformType:    utils.Baremetal,
		}
	}

	glog.V(2).Infof("sriov-config-service read config: %v", nodeStateSpec)

	storeManager, err := utils.NewStoreManager(true)
	if err != nil {
		glog.Errorf("failed to create store manager: %v", err)
		return err
	}
	// Load kernel modules
	hostManager := host.NewHostManager(true)
	_, err = hostManager.TryEnableRdma()
	if err != nil {
		glog.Warningf("failed to enable RDMA: %v", err)
	}
	hostManager.TryEnableTun()
	hostManager.TryEnableVhostNet()

	var configPlugin plugin.VendorPlugin
	var ifaceStatuses []sriovv1.InterfaceExt
	if nodeStateSpec.PlatformType == utils.Baremetal {
		// Bare metal support
		ifaceStatuses, err = utils.DiscoverSriovDevices(nodeStateSpec.UnsupportedNics, storeManager)
		if err != nil {
			glog.Errorf("sriov-config-service: failed to discover sriov devices on the host: %v", err)
			return fmt.Errorf("sriov-config-service: failed to discover sriov devices on the host:  %v", err)
		}

		// Create the generic plugin
		configPlugin, err = generic.NewGenericPlugin(true, hostManager, storeManager)
		if err != nil {
			glog.Errorf("sriov-config-service: failed to create generic plugin %v", err)
			return fmt.Errorf("sriov-config-service failed to create generic plugin %v", err)
		}
	} else if nodeStateSpec.PlatformType == utils.VirtualOpenStack {
		// Openstack support
		metaData, networkData, err := utils.GetOpenstackData(false)
		if err != nil {
			glog.Errorf("sriov-config-service: failed to read OpenStack data: %v", err)
			return fmt.Errorf("sriov-config-service failed to read OpenStack data: %v", err)
		}

		openStackDevicesInfo, err := utils.CreateOpenstackDevicesInfo(metaData, networkData)
		if err != nil {
			glog.Errorf("failed to read OpenStack data: %v", err)
			return fmt.Errorf("sriov-config-service failed to read OpenStack data: %v", err)
		}

		ifaceStatuses, err = utils.DiscoverSriovDevicesVirtual(openStackDevicesInfo)
		if err != nil {
			glog.Errorf("sriov-config-service:failed to read OpenStack data: %v", err)
			return fmt.Errorf("sriov-config-service: failed to read OpenStack data: %v", err)
		}

		// Create the virtual plugin
		configPlugin, err = virtual.NewVirtualPlugin(true)
		if err != nil {
			glog.Errorf("sriov-config-service: failed to create virtual plugin %v", err)
			return fmt.Errorf("sriov-config-service: failed to create virtual plugin %v", err)
		}
	}

	nodeState := &sriovv1.SriovNetworkNodeState{
		Spec:   nodeStateSpec.Spec,
		Status: sriovv1.SriovNetworkNodeStateStatus{Interfaces: ifaceStatuses},
	}

	_, _, err = configPlugin.OnNodeStateChange(nodeState)
	if err != nil {
		glog.Errorf("sriov-config-service: failed to run OnNodeStateChange to update the generic plugin status %v", err)
		return fmt.Errorf("sriov-config-service: failed to run OnNodeStateChange to update the generic plugin status %v", err)
	}

	sriovResult := &systemd.SriovResult{
		SyncStatus:    "Succeeded",
		LastSyncError: "",
	}

	err = configPlugin.Apply()
	if err != nil {
		glog.Errorf("sriov-config-service failed to run apply node configuration %v", err)
		sriovResult.SyncStatus = "Failed"
		sriovResult.LastSyncError = err.Error()
	}

	err = systemd.WriteSriovResult(sriovResult)
	if err != nil {
		glog.Errorf("sriov-config-service failed to write sriov result file with content %v error: %v", *sriovResult, err)
		return fmt.Errorf("sriov-config-service failed to write sriov result file with content %v error: %v", *sriovResult, err)
	}

	glog.V(0).Info("Shutting down sriov-config-service")
	return nil
}
