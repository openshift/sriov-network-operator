package network

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/cenkalti/backoff"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/consts"
	dputilsPkg "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/host/internal/lib/dputils"
	netlinkPkg "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/host/internal/lib/netlink"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/host/types"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/utils"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/vars"
)

type network struct {
	utilsHelper utils.CmdInterface
	dputilsLib  dputilsPkg.DPUtilsLib
	netlinkLib  netlinkPkg.NetlinkLib
}

func New(utilsHelper utils.CmdInterface, dputilsLib dputilsPkg.DPUtilsLib, netlinkLib netlinkPkg.NetlinkLib) types.NetworkInterface {
	return &network{
		utilsHelper: utilsHelper,
		dputilsLib:  dputilsLib,
		netlinkLib:  netlinkLib,
	}
}

// TryToGetVirtualInterfaceName get the interface name of a virtio interface
func (n *network) TryToGetVirtualInterfaceName(pciAddr string) string {
	log.Log.Info("TryToGetVirtualInterfaceName() get interface name for device", "device", pciAddr)

	// To support different driver that is not virtio-pci like mlx
	name := n.TryGetInterfaceName(pciAddr)
	if name != "" {
		return name
	}

	netDir, err := filepath.Glob(filepath.Join(vars.FilesystemRoot, consts.SysBusPciDevices, pciAddr, "virtio*", "net"))
	if err != nil || len(netDir) < 1 {
		return ""
	}

	fInfos, err := os.ReadDir(netDir[0])
	if err != nil {
		log.Log.Error(err, "TryToGetVirtualInterfaceName(): failed to read net directory", "dir", netDir[0])
		return ""
	}

	names := make([]string, 0)
	for _, f := range fInfos {
		names = append(names, f.Name())
	}

	if len(names) < 1 {
		return ""
	}

	return names[0]
}

func (n *network) TryGetInterfaceName(pciAddr string) string {
	names, err := n.dputilsLib.GetNetNames(pciAddr)
	if err != nil || len(names) < 1 {
		return ""
	}
	netDevName := names[0]

	// Switchdev PF and their VFs representors are existing under the same PCI address since kernel 5.8
	// if device is switchdev then return PF name
	for _, name := range names {
		if !n.IsSwitchdev(name) {
			continue
		}
		// Try to get the phys port name, if not exists then fallback to check without it
		// phys_port_name should be in formant p<port-num> e.g p0,p1,p2 ...etc.
		if physPortName, err := n.GetPhysPortName(name); err == nil {
			if !vars.PfPhysPortNameRe.MatchString(physPortName) {
				continue
			}
		}
		return name
	}

	log.Log.V(2).Info("tryGetInterfaceName()", "name", netDevName)
	return netDevName
}

func (n *network) GetPhysSwitchID(name string) (string, error) {
	swIDFile := filepath.Join(vars.FilesystemRoot, consts.SysClassNet, name, "phys_switch_id")
	physSwitchID, err := os.ReadFile(swIDFile)
	if err != nil {
		return "", err
	}
	if physSwitchID != nil {
		return strings.TrimSpace(string(physSwitchID)), nil
	}
	return "", nil
}

func (n *network) GetPhysPortName(name string) (string, error) {
	devicePortNameFile := filepath.Join(vars.FilesystemRoot, consts.SysClassNet, name, "phys_port_name")
	physPortName, err := os.ReadFile(devicePortNameFile)
	if err != nil {
		return "", err
	}
	if physPortName != nil {
		return strings.TrimSpace(string(physPortName)), nil
	}
	return "", nil
}

func (n *network) IsSwitchdev(name string) bool {
	switchID, err := n.GetPhysSwitchID(name)
	if err != nil || switchID == "" {
		return false
	}

	return true
}

func (n *network) GetNetdevMTU(pciAddr string) int {
	log.Log.V(2).Info("GetNetdevMTU(): get MTU", "device", pciAddr)
	ifaceName := n.TryGetInterfaceName(pciAddr)
	if ifaceName == "" {
		return 0
	}

	link, err := n.netlinkLib.LinkByName(ifaceName)
	if err != nil {
		log.Log.Error(err, "GetNetdevMTU(): fail to get Link ", "device", ifaceName)
		return 0
	}

	return link.Attrs().MTU
}

func (n *network) SetNetdevMTU(pciAddr string, mtu int) error {
	log.Log.V(2).Info("SetNetdevMTU(): set MTU", "device", pciAddr, "mtu", mtu)
	if mtu <= 0 {
		log.Log.V(2).Info("SetNetdevMTU(): refusing to set MTU", "mtu", mtu)
		return nil
	}
	b := backoff.NewConstantBackOff(1 * time.Second)
	err := backoff.Retry(func() error {
		ifaceName := n.TryGetInterfaceName(pciAddr)
		if ifaceName == "" {
			log.Log.Error(nil, "SetNetdevMTU(): fail to get interface name", "device", pciAddr)
			return fmt.Errorf("failed to get netdevice for device %s", pciAddr)
		}

		link, err := n.netlinkLib.LinkByName(ifaceName)
		if err != nil {
			log.Log.Error(err, "SetNetdevMTU(): fail to get Link ", "device", ifaceName)
			return err
		}
		return n.netlinkLib.LinkSetMTU(link, mtu)
	}, backoff.WithMaxRetries(b, 10))

	if err != nil {
		log.Log.Error(err, "SetNetdevMTU(): fail to set mtu after retrying")
		return err
	}
	return nil
}

// GetNetDevMac returns network device MAC address or empty string if address cannot be
// retrieved.
func (n *network) GetNetDevMac(ifaceName string) string {
	log.Log.V(2).Info("GetNetDevMac(): get Mac", "device", ifaceName)
	link, err := n.netlinkLib.LinkByName(ifaceName)
	if err != nil {
		log.Log.Error(err, "GetNetDevMac(): failed to get Link", "device", ifaceName)
		return ""
	}
	return link.Attrs().HardwareAddr.String()
}

func (n *network) GetNetDevLinkSpeed(ifaceName string) string {
	log.Log.V(2).Info("GetNetDevLinkSpeed(): get LinkSpeed", "device", ifaceName)
	speedFilePath := filepath.Join(vars.FilesystemRoot, consts.SysClassNet, ifaceName, "speed")
	data, err := os.ReadFile(speedFilePath)
	if err != nil {
		log.Log.Error(err, "GetNetDevLinkSpeed(): fail to read Link Speed file", "path", speedFilePath)
		return ""
	}

	return fmt.Sprintf("%s Mb/s", strings.TrimSpace(string(data)))
}
