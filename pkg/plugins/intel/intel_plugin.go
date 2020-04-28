package main

import (
	"fmt"
	"net/url"
	"reflect"
	"strings"

	"github.com/golang/glog"
	sriovnetworkv1 "github.com/openshift/sriov-network-operator/pkg/apis/sriovnetwork/v1"
	ddp "github.com/openshift/sriov-network-operator/pkg/plugins/intel/ddp"
	"github.com/openshift/sriov-network-operator/pkg/utils"
)

type IntelPlugin struct {
	PluginName  string
	SpecVersion string
	DesireState *sriovnetworkv1.SriovNetworkNodeState
	LastState   *sriovnetworkv1.SriovNetworkNodeState
}

var Plugin IntelPlugin

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

// Spec returns the version of the spec expected by the plugin
func (p *IntelPlugin) Spec() string {
	return p.SpecVersion
}

// OnNodeStateAdd Invoked when SriovNetworkNodeState CR is created, return if need drain and/or reboot node
func (p *IntelPlugin) OnNodeStateAdd(state *sriovnetworkv1.SriovNetworkNodeState) (needDrain bool, needReboot bool, err error) {
	glog.Info("intel-plugin OnNodeStateAdd()")
	needDrain = false
	needReboot = false
	err = nil
	p.DesireState = state
	return
}

// OnNodeStateChange Invoked when SriovNetworkNodeState CR is updated, return if need drain and/or reboot node
func (p *IntelPlugin) OnNodeStateChange(old, new *sriovnetworkv1.SriovNetworkNodeState) (needDrain bool, needReboot bool, err error) {
	glog.Info("intel-plugin OnNodeStateChange()")
	needDrain = false
	needReboot = false
	err = nil
	p.DesireState = new

	return
}

// Apply config change
func (p *IntelPlugin) Apply() error {
	glog.Infof("intel-plugin Apply(): desiredState=%v", p.DesireState.Spec)

	if p.LastState != nil {
		glog.Infof("intel-plugin Apply(): lastState=%v", p.LastState.Spec)
		if reflect.DeepEqual(p.LastState.Spec.Interfaces, p.DesireState.Spec.Interfaces) {
			glog.Info("intel-plugin Apply(): nothing to apply")
			return nil
		}
	}

	if err := cleanupLastState(p); err != nil {
		glog.Errorf("intel-plugin Apply(): failed to cleanup previous state: '%v'", err)
		return err
	}

	if err := ddp.SyncHostWContainer(); err != nil {
		glog.Errorf("intel-plugin Apply(): unable to copy DDP software from host to container with error: '%v'", err)
	}

	for _, ifaceStatus := range p.DesireState.Status.Interfaces {
		for _, iface := range p.DesireState.Spec.Interfaces {
			if iface.PciAddress != ifaceStatus.PciAddress {
				continue
			}
			// check if we need to change state
			if update, err := ddp.NeedsUpdate(iface.Ddp, ifaceStatus.Ddp); err != nil {
				errMsg := fmt.Sprintf("intel-plugin Apply(): failed to detect if state change needed with error: '%v'", err)
				glog.Error(errMsg)
				return fmt.Errorf(errMsg)
			} else if !update {
				continue
			}

			if ok := isRootPciAddress(iface.PciAddress); !ok {
				errMsg := fmt.Sprintf("intel-plugin Apply(): PCI address is not a root physical function address")
				glog.Error(errMsg)
				return fmt.Errorf(errMsg)
			}

			if ok, err := isUrl(iface.Ddp); err != nil && !ok {
				errMsg := fmt.Sprintf("intel-plugin Apply(): failed to validate DDP URL with error: '%v'", err)
				glog.Error(errMsg)
				return fmt.Errorf(errMsg)
			}

			if ok, err := ddp.HasDdpSupport(iface.PciAddress); !ok {
				errMsg := fmt.Sprintf("intel-plugin Apply(): device with PCI address '%s' may not have DDP support " +
					"with error: '%v'", iface.PciAddress, err)
				glog.Error(errMsg)
				return fmt.Errorf(errMsg)
			}

			if err := ddp.UnloadDdp(iface.Name, iface.PciAddress); err != nil {
				errMsg := fmt.Sprintf("intel-plugin Apply(): failed to unload DDP package with error: '%v'", err)
				glog.Error(errMsg)
				return fmt.Errorf(errMsg)
			}

			if iface.Ddp == "" {
				continue
			}

			if err := ddp.LoadDdp(iface.Ddp, iface.PciAddress, iface.Name); err != nil {
				errMsg := fmt.Sprintf("intel-plugin Apply(): failed to load DDP package with error: '%v'", err)
				glog.Error(errMsg)
				return fmt.Errorf(errMsg)
			}
		}
	}

	if err := ddp.SyncContainerWHost(); err != nil {
		glog.Errorf("intel-plugin Apply(): unable to copy DDP software from container to host with error: '%v'", err)
	}

	if err := utils.SyncNodeState(p.DesireState); err != nil {
		return err
	}
	p.LastState = &sriovnetworkv1.SriovNetworkNodeState{}
	*p.LastState = *p.DesireState

	return nil
}

// isUrl check if argument string is structured as a valid URL
func isUrl(urlUnderTest string) (bool, error) {
	pUrl, err := url.Parse(urlUnderTest)
	if err != nil {
		return false, fmt.Errorf("intel-plugin isUrl(): failed to parse URL '%s'", urlUnderTest)
	}

	if pUrl.Scheme == "" || pUrl.Host == "" || pUrl.Path == "" {
		return false, fmt.Errorf("intel-plugin isUrl(): invalid URL: '%s'. Please define URL similar to format " +
			"'http(s)://hostname/path/${valid_ddp_file_name}.zip'", urlUnderTest)
	}

	return true, nil
}

// isRootPciAddress checks if argument 'addr' which is a devices PCI address is the first physical function
func isRootPciAddress(addr string) bool {
	if compare := strings.Compare(addr[len(addr)-5:], ":00.0"); compare != 0 {
		return false
	}
	return true
}


// cleanupLastState will unload DDP if a CR was deleted which invoked DDP
func cleanupLastState(p *IntelPlugin) error {
	if p.LastState == nil {
		return nil
	}

	for _, ifaceLast := range p.LastState.Spec.Interfaces {
		var isFoundDesire bool
		var hadDDPEnabled bool
		for _, ifaceDesire := range p.DesireState.Spec.Interfaces {
			if strings.Compare(ifaceLast.PciAddress, ifaceDesire.PciAddress) == 0 {
				isFoundDesire = true
			}
		}

		if ifaceLast.Ddp != "" {
			hadDDPEnabled = true
		}

		// Check if we want to restore NIC to default i.e No DDP profile loaded
		if !isFoundDesire && hadDDPEnabled {
			if err := ddp.UnloadDdp(ifaceLast.Name, ifaceLast.PciAddress); err != nil {
				return fmt.Errorf("intel-plugin cleanupLastState(): failed to unload DDP profile: '%v'", err)
			}
			glog.Infof("intel-plugin cleanupLastState(): removed DDP profile from device with PCI address: '%s'", ifaceLast.PciAddress)
		}
	}
	return nil
}