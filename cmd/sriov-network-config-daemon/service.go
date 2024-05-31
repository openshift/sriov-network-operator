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

	"github.com/go-logr/logr"
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

const (
	PhasePre  = "pre"
	PhasePost = "post"
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

func init() {
	rootCmd.AddCommand(serviceCmd)
	serviceCmd.Flags().StringVarP(&phaseArg, "phase", "p", PhasePre, fmt.Sprintf("configuration phase, supported values are: %s, %s", PhasePre, PhasePost))
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
	setupLog := log.Log.WithName("sriov-config-service").WithValues("phase", phaseArg)

	setupLog.V(0).Info("Starting sriov-config-service", "version", version.Version)

	// Mark that we are running on host
	vars.UsingSystemdMode = true
	vars.InChroot = true

	sriovConf, err := readConf(setupLog)
	if err != nil {
		return updateSriovResultErr(setupLog, phaseArg, err)
	}
	setupLog.V(2).Info("sriov-config-service", "config", sriovConf)
	vars.DevMode = sriovConf.UnsupportedNics

	if err := initSupportedNics(); err != nil {
		return updateSriovResultErr(setupLog, phaseArg, fmt.Errorf("failed to initialize list of supported NIC ids: %v", err))
	}

	hostHelpers, err := newHostHelpersFunc()
	if err != nil {
		return updateSriovResultErr(setupLog, phaseArg, fmt.Errorf("failed to create hostHelpers: %v", err))
	}

	if phaseArg == PhasePre {
		err = phasePre(setupLog, sriovConf, hostHelpers)
	} else {
		err = phasePost(setupLog, sriovConf, hostHelpers)
	}
	if err != nil {
		return updateSriovResultErr(setupLog, phaseArg, err)
	}
	return updateSriovResultOk(setupLog, phaseArg)
}

func readConf(setupLog logr.Logger) (*systemd.SriovConfig, error) {
	nodeStateSpec, err := systemd.ReadConfFile()
	if err != nil {
		if _, err := os.Stat(systemd.SriovSystemdConfigPath); !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("failed to read the sriov configuration file in path %s: %v", systemd.SriovSystemdConfigPath, err)
		}
		setupLog.Info("configuration file not found, use default config")
		nodeStateSpec = &systemd.SriovConfig{
			Spec:            sriovv1.SriovNetworkNodeStateSpec{},
			UnsupportedNics: false,
			PlatformType:    consts.Baremetal,
		}
	}
	return nodeStateSpec, nil
}

func initSupportedNics() error {
	supportedNicIds, err := systemd.ReadSriovSupportedNics()
	if err != nil {
		return fmt.Errorf("failed to read list of supported nic ids: %v", err)
	}
	sriovv1.InitNicIDMapFromList(supportedNicIds)
	return nil
}

func phasePre(setupLog logr.Logger, conf *systemd.SriovConfig, hostHelpers helper.HostHelpersInterface) error {
	// make sure there is no stale result file to avoid situation when we
	// read outdated info in the Post phase when the Pre silently failed (should not happen)
	if err := systemd.RemoveSriovResult(); err != nil {
		return fmt.Errorf("failed to remove sriov result file: %v", err)
	}

	_, err := hostHelpers.TryEnableRdma()
	if err != nil {
		setupLog.Error(err, "warning, failed to enable RDMA")
	}
	hostHelpers.TryEnableTun()
	hostHelpers.TryEnableVhostNet()

	return callPlugin(setupLog, PhasePre, conf, hostHelpers)
}

func phasePost(setupLog logr.Logger, conf *systemd.SriovConfig, hostHelpers helper.HostHelpersInterface) error {
	setupLog.V(0).Info("check result of the Pre phase")
	prePhaseResult, err := systemd.ReadSriovResult()
	if err != nil {
		return fmt.Errorf("failed to read result of the pre phase: %v", err)
	}
	if prePhaseResult.SyncStatus != consts.SyncStatusInProgress {
		return fmt.Errorf("unexpected result of the pre phase: %s, syncError: %s", prePhaseResult.SyncStatus, prePhaseResult.LastSyncError)
	}
	setupLog.V(0).Info("Pre phase succeed, continue execution")

	return callPlugin(setupLog, PhasePost, conf, hostHelpers)
}

func callPlugin(setupLog logr.Logger, phase string, conf *systemd.SriovConfig, hostHelpers helper.HostHelpersInterface) error {
	configPlugin, err := getPlugin(setupLog, phase, conf, hostHelpers)
	if err != nil {
		return err
	}

	if configPlugin == nil {
		setupLog.V(0).Info("no plugin for the platform for the current phase, skip calling", "platform", conf.PlatformType)
		return nil
	}

	nodeState, err := getNetworkNodeState(setupLog, conf, hostHelpers)
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
	setupLog.V(0).Info("plugin call succeed")
	return nil
}

func getPlugin(setupLog logr.Logger, phase string,
	conf *systemd.SriovConfig, hostHelpers helper.HostHelpersInterface) (plugin.VendorPlugin, error) {
	var (
		configPlugin plugin.VendorPlugin
		err          error
	)
	switch conf.PlatformType {
	case consts.Baremetal:
		switch phase {
		case PhasePre:
			configPlugin, err = newGenericPluginFunc(hostHelpers, generic.WithSkipVFConfiguration())
		case PhasePost:
			configPlugin, err = newGenericPluginFunc(hostHelpers)
		}
		if err != nil {
			return nil, fmt.Errorf("failed to create generic plugin for %v", err)
		}
	case consts.VirtualOpenStack:
		switch phase {
		case PhasePre:
			configPlugin, err = newVirtualPluginFunc(hostHelpers)
			if err != nil {
				return nil, fmt.Errorf("failed to create virtual plugin %v", err)
			}
		case PhasePost:
			setupLog.Info("skip post configuration phase for virtual cluster")
			return nil, nil
		}
	}
	return configPlugin, nil
}

func getNetworkNodeState(setupLog logr.Logger, conf *systemd.SriovConfig,
	hostHelpers helper.HostHelpersInterface) (*sriovv1.SriovNetworkNodeState, error) {
	var (
		ifaceStatuses []sriovv1.InterfaceExt
		err           error
	)
	switch conf.PlatformType {
	case consts.Baremetal:
		ifaceStatuses, err = hostHelpers.DiscoverSriovDevices(hostHelpers)
		if err != nil {
			return nil, fmt.Errorf("failed to discover sriov devices on the host:  %v", err)
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
		Spec:   conf.Spec,
		Status: sriovv1.SriovNetworkNodeStateStatus{Interfaces: ifaceStatuses},
	}, nil
}

func updateSriovResultErr(setupLog logr.Logger, phase string, origErr error) error {
	setupLog.Error(origErr, "service call failed")
	err := updateResult(setupLog, consts.SyncStatusFailed, fmt.Sprintf("%s: %v", phase, origErr))
	if err != nil {
		return err
	}
	return origErr
}

func updateSriovResultOk(setupLog logr.Logger, phase string) error {
	setupLog.V(0).Info("service call succeed")
	syncStatus := consts.SyncStatusSucceeded
	if phase == PhasePre {
		syncStatus = consts.SyncStatusInProgress
	}
	return updateResult(setupLog, syncStatus, "")
}

func updateResult(setupLog logr.Logger, result, msg string) error {
	sriovResult := &systemd.SriovResult{
		SyncStatus:    result,
		LastSyncError: msg,
	}
	err := systemd.WriteSriovResult(sriovResult)
	if err != nil {
		setupLog.Error(err, "failed to write sriov result file", "content", *sriovResult)
		return fmt.Errorf("sriov-config-service failed to write sriov result file with content %v error: %v", *sriovResult, err)
	}
	setupLog.V(0).Info("result file updated", "SyncStatus", sriovResult.SyncStatus, "LastSyncError", msg)
	return nil
}
