package main

import (
	"bytes"
	"os/exec"
	"strings"
	"strconv"

	"github.com/golang/glog"
	sriovnetworkv1 "github.com/pliurh/sriov-network-operator/pkg/apis/sriovnetwork/v1"
	"github.com/pliurh/sriov-network-operator/pkg/utils"
)

type IntelPlugin struct {
	PluginName     string
	SpecVersion    string
	DesireState    *sriovnetworkv1.SriovNetworkNodeState
	LastState      *sriovnetworkv1.SriovNetworkNodeState
	LoadVfioDriver bool
}

var Plugin IntelPlugin
const (
	scriptsPath = "bindata/scripts/enable-kargs.sh"
)

// Initialize our plugin and set up initial values
func init() {
	Plugin = IntelPlugin{
		PluginName:  "intel_plugin",
		SpecVersion: "1.0",
	}
}

// Name returns the name of the plugin
func (p *IntelPlugin) Name() string {
	return p.PluginName
}

// SpecVersion returns the version of the spec expected by the plugin
func (p *IntelPlugin) Spec() string {
	return p.SpecVersion
}

// OnNodeStateAdd Invoked when SriovNetworkNodeState CR is created, return if need dain and/or reboot node
func (p *IntelPlugin) OnNodeStateAdd(state *sriovnetworkv1.SriovNetworkNodeState) (needDrain bool, needReboot bool, err error) {
	glog.Info("OnNodeStateAdd()")

	return p.OnNodeStateChange(nil, state)
}

// OnNodeStateChange Invoked when SriovNetworkNodeState CR is updated, return if need dain and/or reboot node
func (p *IntelPlugin) OnNodeStateChange(old, new *sriovnetworkv1.SriovNetworkNodeState) (needDrain bool, needReboot bool, err error) {
	glog.Info("OnNodeStateChange()")
	needDrain = true
	needReboot = false
	err = nil

	if needVfioDriver(new) {
		p.LoadVfioDriver = true
		if needReboot, err = tryEnableIommuInKernelArgs(); err != nil {
			glog.Errorf("OnNodeStateAdd():fail to enable iommu in kernel args: %v", err)
			return false, false, err
		}
	}
	if needReboot {
		needDrain = true
	}

	return
}

// Apply config change
func (p *IntelPlugin) Apply() error {
	glog.Info("Apply()")
	if p.LoadVfioDriver {
		if err := utils.LoadKernelModule("vfio_pci"); err != nil {
			glog.Errorf("Apply(): fail to load vfio_pci kmod: %v", err)
			return err
		}
	}
	return nil
}

func needVfioDriver(state *sriovnetworkv1.SriovNetworkNodeState) bool {
	for _, iface := range state.Spec.Interfaces {
		if iface.DeviceType == "vfio-pci" {
			return true
		}
	}
	return false
}

func tryEnableIommuInKernelArgs() (bool, error) {
	glog.Info("tryEnableIommuInKernelArgs()")
	args := [2]string{"intel_iommu=on", "iommu=pt"}
	var stdout, stderr bytes.Buffer
	cmd := exec.Command("/bin/sh", scriptsPath, args[0], args[1])
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		glog.Errorf("tryEnableIommuInKernelArgs(): fail to enable iommu %s: %v", args, err)
		return false, err
	}

	i, err := strconv.Atoi(strings.TrimSpace(stdout.String()))
	if err == nil {
		if i > 0 {
			return true, nil
		}
	}
	return false, err
}
