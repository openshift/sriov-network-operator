package mellanox

import (
	"fmt"

	"sigs.k8s.io/controller-runtime/pkg/log"

	sriovnetworkv1 "github.com/k8snetworkplumbingwg/sriov-network-operator/api/v1"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/helper"
	plugin "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/plugins"
	mlx "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/vendors/mellanox"
)

var PluginName = "mellanox"

type MellanoxPlugin struct {
	PluginName  string
	SpecVersion string
	helpers     helper.HostHelpersInterface
}

var attributesToChange map[string]mlx.MlxNic
var mellanoxNicsStatus map[string]map[string]sriovnetworkv1.InterfaceExt
var mellanoxNicsSpec map[string]sriovnetworkv1.Interface

// Initialize our plugin and set up initial values
func NewMellanoxPlugin(helpers helper.HostHelpersInterface) (plugin.VendorPlugin, error) {
	mellanoxNicsStatus = map[string]map[string]sriovnetworkv1.InterfaceExt{}

	return &MellanoxPlugin{
		PluginName:  PluginName,
		SpecVersion: "1.0",
		helpers:     helpers,
	}, nil
}

// Name returns the name of the plugin
func (p *MellanoxPlugin) Name() string {
	return p.PluginName
}

// SpecVersion returns the version of the spec expected by the plugin
func (p *MellanoxPlugin) Spec() string {
	return p.SpecVersion
}

// OnNodeStateChange Invoked when SriovNetworkNodeState CR is created or updated, return if need dain and/or reboot node
func (p *MellanoxPlugin) OnNodeStateChange(new *sriovnetworkv1.SriovNetworkNodeState) (needDrain bool, needReboot bool, err error) {
	log.Log.Info("mellanox plugin OnNodeStateChange()")

	needDrain = false
	needReboot = false
	err = nil
	attributesToChange = map[string]mlx.MlxNic{}
	mellanoxNicsStatus = map[string]map[string]sriovnetworkv1.InterfaceExt{}
	mellanoxNicsSpec = map[string]sriovnetworkv1.Interface{}
	processedNics := map[string]bool{}

	// fill mellanoxNicsStatus
	for _, iface := range new.Status.Interfaces {
		if iface.Vendor != mlx.MellanoxVendorID {
			continue
		}

		pciPrefix := mlx.GetPciAddressPrefix(iface.PciAddress)
		if ifaces, ok := mellanoxNicsStatus[pciPrefix]; ok {
			ifaces[iface.PciAddress] = iface
		} else {
			mellanoxNicsStatus[pciPrefix] = map[string]sriovnetworkv1.InterfaceExt{iface.PciAddress: iface}
		}
	}

	// Add only mellanox cards that required changes in the map, to help track dual port NICs
	for _, iface := range new.Spec.Interfaces {
		pciPrefix := mlx.GetPciAddressPrefix(iface.PciAddress)
		if _, ok := mellanoxNicsStatus[pciPrefix]; !ok {
			continue
		}
		mellanoxNicsSpec[iface.PciAddress] = iface
	}

	if p.helpers.IsKernelLockdownMode() {
		if len(mellanoxNicsSpec) > 0 {
			log.Log.Info("Lockdown mode detected, failing on interface update for mellanox devices")
			return false, false, fmt.Errorf("mellanox device detected when in lockdown mode")
		}
		log.Log.Info("Lockdown mode detected, skpping mellanox nic processing")
		return
	}

	for _, ifaceSpec := range mellanoxNicsSpec {
		pciPrefix := mlx.GetPciAddressPrefix(ifaceSpec.PciAddress)
		// skip processed nics, help not running the same logic 2 times for dual port NICs
		if _, ok := processedNics[pciPrefix]; ok {
			continue
		}
		processedNics[pciPrefix] = true
		fwCurrent, fwNext, err := p.helpers.GetMlxNicFwData(ifaceSpec.PciAddress)
		if err != nil {
			return false, false, err
		}

		isDualPort := mlx.IsDualPort(ifaceSpec.PciAddress, mellanoxNicsStatus)
		// Attributes to change
		attrs := &mlx.MlxNic{TotalVfs: -1}
		var changeWithoutReboot bool

		totalVfs, totalVfsNeedReboot, totalVfsChangeWithoutReboot := mlx.HandleTotalVfs(fwCurrent, fwNext, attrs, ifaceSpec, isDualPort, mellanoxNicsSpec)
		sriovEnNeedReboot, sriovEnChangeWithoutReboot := mlx.HandleEnableSriov(totalVfs, fwCurrent, fwNext, attrs)
		needReboot = totalVfsNeedReboot || sriovEnNeedReboot
		changeWithoutReboot = totalVfsChangeWithoutReboot || sriovEnChangeWithoutReboot

		needLinkChange, err := mlx.HandleLinkType(pciPrefix, fwCurrent, attrs, mellanoxNicsSpec, mellanoxNicsStatus)
		if err != nil {
			return false, false, err
		}
		needReboot = needReboot || needLinkChange

		// no FW changes allowed when NIC is externally managed
		if ifaceSpec.ExternallyManaged {
			if totalVfsNeedReboot || totalVfsChangeWithoutReboot {
				return false, false, fmt.Errorf(
					"interface %s required a change in the TotalVfs but the policy is externally managed failing: firmware TotalVf %d requested TotalVf %d",
					ifaceSpec.PciAddress, fwCurrent.TotalVfs, totalVfs)
			}
			if needLinkChange {
				return false, false, fmt.Errorf("change required for link type but the policy is externally managed, failing")
			}
		}

		if needReboot || changeWithoutReboot {
			attributesToChange[ifaceSpec.PciAddress] = *attrs
		}
	}

	// Set total VFs to 0 for mellanox interfaces with no spec
	for pciPrefix, portsMap := range mellanoxNicsStatus {
		if _, ok := processedNics[pciPrefix]; ok {
			continue
		}

		// Add the nic to processed Nics to not repeat the process for dual nic ports
		processedNics[pciPrefix] = true
		pciAddress := pciPrefix + "0"

		// Skip externally managed NICs
		if p.nicHasExternallyManagedPFs(portsMap) {
			continue
		}

		// Skip unsupported devices
		if id := sriovnetworkv1.GetVfDeviceID(portsMap[pciAddress].DeviceID); id == "" {
			continue
		}

		_, fwNext, err := p.helpers.GetMlxNicFwData(pciAddress)
		if err != nil {
			return false, false, err
		}

		if fwNext.TotalVfs > 0 || fwNext.EnableSriov {
			attributesToChange[pciAddress] = mlx.MlxNic{TotalVfs: 0}
			log.Log.V(2).Info("Changing TotalVfs to 0, doesn't require rebooting", "fwNext.totalVfs", fwNext.TotalVfs)
		}
	}

	if needReboot {
		needDrain = true
	}
	log.Log.V(2).Info("mellanox plugin", "need-drain", needDrain, "need-reboot", needReboot)
	return
}

// Apply config change
func (p *MellanoxPlugin) Apply() error {
	if p.helpers.IsKernelLockdownMode() {
		log.Log.Info("mellanox plugin Apply() - skipping due to lockdown mode")
		return nil
	}
	log.Log.Info("mellanox plugin Apply()")
	return p.helpers.MlxConfigFW(attributesToChange)
}

// nicHasExternallyManagedPFs returns true if one of the ports(interface) of the NIC is marked as externally managed
// in StoreManagerInterface.
func (p *MellanoxPlugin) nicHasExternallyManagedPFs(nicPortsMap map[string]sriovnetworkv1.InterfaceExt) bool {
	for _, iface := range nicPortsMap {
		pfStatus, exist, err := p.helpers.LoadPfsStatus(iface.PciAddress)
		if err != nil {
			log.Log.Error(err, "failed to load PF status from disk", "address", iface.PciAddress)
			continue
		}
		if !exist {
			continue
		}
		if pfStatus.ExternallyManaged {
			log.Log.V(2).Info("PF is extenally managed, skip FW TotalVfs reset")
			return true
		}
	}
	return false
}
