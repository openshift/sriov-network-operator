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
	"time"

	"github.com/go-logr/logr"
	"github.com/spf13/cobra"
	"sigs.k8s.io/controller-runtime/pkg/log"

	sriovv1 "github.com/k8snetworkplumbingwg/sriov-network-operator/api/v1"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/consts"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/helper"
	hosttypes "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/host/types"
	snolog "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/log"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/platforms"
	plugin "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/plugins"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/plugins/generic"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/plugins/virtual"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/utils"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/vars"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/version"
)

const (
	PhasePre  = "pre"
	PhasePost = "post"

	// InitializationDeviceDiscoveryTimeoutSec constant defines the number of
	// seconds to wait for devices to be registered in the system with the expected name.
	InitializationDeviceDiscoveryTimeoutSec = 60
	// InitializationDeviceUdevProcessingTimeoutSec constant defines the number of seconds to wait for udev rules to process
	InitializationDeviceUdevProcessingTimeoutSec = 60
)

var (
	serviceCmd = &cobra.Command{
		Use:   "service",
		Short: "Starts SR-IOV service Config",
		Long:  "",
		RunE:  runServiceCmd,
	}
	phaseArg string

	newGenericPluginFunc  = generic.NewGenericPlugin
	newVirtualPluginFunc  = virtual.NewVirtualPlugin
	newHostHelpersFunc    = helper.NewDefaultHostHelpers
	newPlatformHelperFunc = platforms.NewDefaultPlatformHelper
)

// ServiceConfig is a struct that encapsulates the configuration and dependencies
// needed by the SriovNetworkConfigDaemon systemd service.
type ServiceConfig struct {
	hostHelper  helper.HostHelpersInterface // Provides host-specific helper functions
	log         logr.Logger                 // Handles logging for the service
	sriovConfig *hosttypes.SriovConfig      // Contains the SR-IOV network configuration settings
}

func init() {
	rootCmd.AddCommand(serviceCmd)
	serviceCmd.Flags().StringVarP(&phaseArg, "phase", "p", PhasePre, fmt.Sprintf("configuration phase, supported values are: %s, %s", PhasePre, PhasePost))
}

func newServiceConfig(setupLog logr.Logger) (*ServiceConfig, error) {
	hostHelpers, err := newHostHelpersFunc()
	if err != nil {
		return nil, fmt.Errorf("failed to create host helpers: %v", err)
	}

	return &ServiceConfig{
		hostHelpers,
		setupLog,
		nil,
	}, nil
}

// The service supports two configuration phases:
// * pre(default) - before the NetworkManager or systemd-networkd
// * post - after the NetworkManager or systemd-networkd
// "sriov-config" systemd unit is responsible for starting the service in the "pre" phase mode.
// "sriov-config-post-network" systemd unit starts the service in the "post" phase mode.
// The service may use different plugins for each phase and call different initialization flows.
// The "post" phase checks the completion status of the "pre" phase by reading the sriov result file.
// The "pre" phase should set "InProgress" status if it succeeds or "Failed" otherwise.
// If the result of the "pre" phase is different than "InProgress", then the "post" phase will not be executed
// and the execution result will be forcefully set to "Failed".
func runServiceCmd(cmd *cobra.Command, args []string) error {
	if phaseArg != PhasePre && phaseArg != PhasePost {
		return fmt.Errorf("invalid value for \"--phase\" argument, valid values are: %s, %s", PhasePre, PhasePost)
	}
	// init logger
	snolog.InitLog()
	snolog.SetLogLevel(2)
	setupLog := log.Log.WithName("sriov-config-service").WithValues("phase", phaseArg)

	setupLog.V(0).Info("Starting sriov-config-service", "version", version.Version)

	// Mark that we are running on host
	vars.UsingSystemdMode = true
	vars.InChroot = true

	sc, err := newServiceConfig(setupLog)
	if err != nil {
		setupLog.Error(err, "failed to create the service configuration controller, Exiting")
		return err
	}

	err = sc.readConf()
	if err != nil {
		return sc.updateSriovResultErr(phaseArg, err)
	}

	setupLog.V(2).Info("sriov-config-service", "config", sc.sriovConfig)
	vars.DevMode = sc.sriovConfig.UnsupportedNics
	vars.ManageSoftwareBridges = sc.sriovConfig.ManageSoftwareBridges
	vars.OVSDBSocketPath = sc.sriovConfig.OVSDBSocketPath

	if err := sc.initSupportedNics(); err != nil {
		return sc.updateSriovResultErr(phaseArg, fmt.Errorf("failed to initialize list of supported NIC ids: %v", err))
	}

	sc.waitForDevicesInitialization()

	if phaseArg == PhasePre {
		err = sc.phasePre()
	} else {
		err = sc.phasePost()
	}
	if err != nil {
		return sc.updateSriovResultErr(phaseArg, err)
	}
	return sc.updateSriovResultOk(phaseArg)
}

func (s *ServiceConfig) readConf() error {
	nodeStateSpec, err := s.hostHelper.ReadConfFile()
	if err != nil {
		if _, err := os.Stat(utils.GetHostExtensionPath(consts.SriovSystemdConfigPath)); !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("failed to read the sriov configuration file in path %s: %v", utils.GetHostExtensionPath(consts.SriovSystemdConfigPath), err)
		}
		s.log.Info("configuration file not found, use default config")
		nodeStateSpec = &hosttypes.SriovConfig{
			Spec:            sriovv1.SriovNetworkNodeStateSpec{},
			UnsupportedNics: false,
			PlatformType:    consts.Baremetal,
		}
	}
	s.sriovConfig = nodeStateSpec
	return nil
}

func (s *ServiceConfig) initSupportedNics() error {
	supportedNicIds, err := s.hostHelper.ReadSriovSupportedNics()
	if err != nil {
		return fmt.Errorf("failed to read list of supported nic ids: %v", err)
	}
	sriovv1.InitNicIDMapFromList(supportedNicIds)
	return nil
}

func (s *ServiceConfig) phasePre() error {
	// make sure there is no stale result file to avoid situation when we
	// read outdated info in the Post phase when the Pre silently failed (should not happen)
	if err := s.hostHelper.RemoveSriovResult(); err != nil {
		return fmt.Errorf("failed to remove sriov result file: %v", err)
	}

	_, err := s.hostHelper.CheckRDMAEnabled()
	if err != nil {
		s.log.Error(err, "warning, failed to check RDMA state")
	}
	s.hostHelper.TryEnableTun()
	s.hostHelper.TryEnableVhostNet()

	return s.callPlugin(PhasePre)
}

func (s *ServiceConfig) phasePost() error {
	s.log.V(0).Info("check result of the Pre phase")
	prePhaseResult, _, err := s.hostHelper.ReadSriovResult()
	if err != nil {
		return fmt.Errorf("failed to read result of the pre phase: %v", err)
	}
	if prePhaseResult.SyncStatus != consts.SyncStatusInProgress {
		return fmt.Errorf("unexpected result of the pre phase: %s, syncError: %s", prePhaseResult.SyncStatus, prePhaseResult.LastSyncError)
	}
	s.log.V(0).Info("Pre phase succeed, continue execution")

	return s.callPlugin(PhasePost)
}

func (s *ServiceConfig) callPlugin(phase string) error {
	configPlugin, err := s.getPlugin(phase)
	if err != nil {
		return err
	}

	if configPlugin == nil {
		s.log.V(0).Info("no plugin for the platform for the current phase, skip calling", "platform", s.sriovConfig.PlatformType)
		return nil
	}

	nodeState, err := s.getNetworkNodeState(phase)
	if err != nil {
		return err
	}
	_, _, err = configPlugin.OnNodeStateChange(nodeState)
	if err != nil {
		return fmt.Errorf("failed to run OnNodeStateChange to update the plugin status %v", err)
	}

	if err = configPlugin.Apply(); err != nil {
		return fmt.Errorf("failed to apply configuration: %v", err)
	}
	s.log.V(0).Info("plugin call succeed")
	return nil
}

func (s *ServiceConfig) getPlugin(phase string) (plugin.VendorPlugin, error) {
	var (
		configPlugin plugin.VendorPlugin
		err          error
	)
	switch s.sriovConfig.PlatformType {
	case consts.Baremetal:
		switch phase {
		case PhasePre:
			configPlugin, err = newGenericPluginFunc(s.hostHelper,
				generic.WithSkipVFConfiguration(),
				generic.WithSkipBridgeConfiguration())
		case PhasePost:
			configPlugin, err = newGenericPluginFunc(s.hostHelper)
		}
		if err != nil {
			return nil, fmt.Errorf("failed to create generic plugin for %v", err)
		}
	case consts.VirtualOpenStack:
		switch phase {
		case PhasePre:
			configPlugin, err = newVirtualPluginFunc(s.hostHelper)
			if err != nil {
				return nil, fmt.Errorf("failed to create virtual plugin %v", err)
			}
		case PhasePost:
			s.log.Info("skip post configuration phase for virtual cluster")
			return nil, nil
		}
	}
	return configPlugin, nil
}

func (s *ServiceConfig) getNetworkNodeState(phase string) (*sriovv1.SriovNetworkNodeState, error) {
	var (
		ifaceStatuses []sriovv1.InterfaceExt
		bridges       sriovv1.Bridges
		err           error
	)
	switch s.sriovConfig.PlatformType {
	case consts.Baremetal:
		ifaceStatuses, err = s.hostHelper.DiscoverSriovDevices(s.hostHelper)
		if err != nil {
			return nil, fmt.Errorf("failed to discover sriov devices on the host:  %v", err)
		}
		if phase != PhasePre && vars.ManageSoftwareBridges {
			// openvswitch is not available during the pre phase
			bridges, err = s.hostHelper.DiscoverBridges()
			if err != nil {
				return nil, fmt.Errorf("failed to discover managed bridges on the host:  %v", err)
			}
		}
	case consts.VirtualOpenStack:
		platformHelper, err := newPlatformHelperFunc()
		if err != nil {
			return nil, fmt.Errorf("failed to create platformHelpers")
		}
		err = platformHelper.CreateOpenstackDevicesInfo()
		if err != nil {
			return nil, fmt.Errorf("failed to read OpenStack data: %v", err)
		}
		ifaceStatuses, err = platformHelper.DiscoverSriovDevicesVirtual()
		if err != nil {
			return nil, fmt.Errorf("failed to discover devices: %v", err)
		}
	}
	return &sriovv1.SriovNetworkNodeState{
		Spec:   s.sriovConfig.Spec,
		Status: sriovv1.SriovNetworkNodeStateStatus{Interfaces: ifaceStatuses, Bridges: bridges},
	}, nil
}

func (s *ServiceConfig) updateSriovResultErr(phase string, origErr error) error {
	s.log.Error(origErr, "service call failed")
	err := s.updateResult(consts.SyncStatusFailed, fmt.Sprintf("%s: %v", phase, origErr))
	if err != nil {
		return err
	}
	return origErr
}

func (s *ServiceConfig) updateSriovResultOk(phase string) error {
	s.log.V(0).Info("service call succeed")
	syncStatus := consts.SyncStatusSucceeded
	if phase == PhasePre {
		syncStatus = consts.SyncStatusInProgress
	}
	return s.updateResult(syncStatus, "")
}

func (s *ServiceConfig) updateResult(result, msg string) error {
	sriovResult := &hosttypes.SriovResult{
		SyncStatus:    result,
		LastSyncError: msg,
	}
	err := s.hostHelper.WriteSriovResult(sriovResult)
	if err != nil {
		s.log.Error(err, "failed to write sriov result file", "content", *sriovResult)
		return fmt.Errorf("sriov-config-service failed to write sriov result file with content %v error: %v", *sriovResult, err)
	}
	s.log.V(0).Info("result file updated", "SyncStatus", sriovResult.SyncStatus, "LastSyncError", msg)
	return nil
}

// waitForDevicesInitialization should be executed in both the pre and post-networking stages.
// This function ensures that the network devices specified in the configuration are registered
// and handled by UDEV. Sometimes, the initialization of network devices might take a significant
// amount of time, and the sriov-config systemd service may start before the devices are fully
// processed, leading to failure.
//
// To address this, we not only check if the devices are registered with the correct name but also
// wait for the udev event queue to empty. This increases the likelihood that the service will start
// only when the devices are fully initialized. It is required to call this function in the
// "post-networking" phase as well because the OS network manager might change device configurations,
// and we need to ensure these changes are fully processed before starting the post-networking part.
//
// The timeouts used in this function are intentionally kept low to avoid blocking the OS loading
// process for too long in case of any issues.
//
// Note: Currently, this function handles only Baremetal clusters. We do not have evidence that
// this logic is required for virtual clusters.
func (s *ServiceConfig) waitForDevicesInitialization() {
	if s.sriovConfig.PlatformType != consts.Baremetal {
		// skip waiting on virtual cluster
		return
	}
	// wait for devices from the spec to be registered in the system with expected names
	devicesToWait := make(map[string]string, len(s.sriovConfig.Spec.Interfaces))
	for _, d := range s.sriovConfig.Spec.Interfaces {
		devicesToWait[d.PciAddress] = d.Name
	}
	deadline := time.Now().Add(time.Second * time.Duration(InitializationDeviceDiscoveryTimeoutSec))
	for time.Now().Before(deadline) {
		for pciAddr, name := range devicesToWait {
			if s.hostHelper.TryGetInterfaceName(pciAddr) == name {
				s.log.Info("Device ready", "pci", pciAddr, "name", name)
				delete(devicesToWait, pciAddr)
			}
		}
		if len(devicesToWait) == 0 {
			break
		}
		time.Sleep(time.Second)
	}
	if len(devicesToWait) != 0 {
		s.log.Info("WARNING: some devices were not initialized", "devices", devicesToWait, "timeout", InitializationDeviceDiscoveryTimeoutSec)
	}
	if err := s.hostHelper.WaitUdevEventsProcessed(InitializationDeviceUdevProcessingTimeoutSec); err != nil {
		s.log.Info("WARNING: failed to wait for udev events processing", "reason", err.Error(),
			"timeout", InitializationDeviceUdevProcessingTimeoutSec)
	}
}
