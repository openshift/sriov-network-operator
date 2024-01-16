package network

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/cenkalti/backoff"
	"sigs.k8s.io/controller-runtime/pkg/log"

	dputils "github.com/k8snetworkplumbingwg/sriov-network-device-plugin/pkg/utils"

	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/consts"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/host/types"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/utils"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/vars"
)

type network struct {
	utilsHelper utils.CmdInterface
}

func New(utilsHelper utils.CmdInterface) types.NetworkInterface {
	return &network{utilsHelper: utilsHelper}
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
	names, err := dputils.GetNetNames(pciAddr)
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

func mtuFilePath(ifaceName string, pciAddr string) string {
	mtuFile := "net/" + ifaceName + "/mtu"
	return filepath.Join(vars.FilesystemRoot, consts.SysBusPciDevices, pciAddr, mtuFile)
}

func (n *network) GetNetdevMTU(pciAddr string) int {
	log.Log.V(2).Info("GetNetdevMTU(): get MTU", "device", pciAddr)
	ifaceName := n.TryGetInterfaceName(pciAddr)
	if ifaceName == "" {
		return 0
	}
	data, err := os.ReadFile(mtuFilePath(ifaceName, pciAddr))
	if err != nil {
		log.Log.Error(err, "GetNetdevMTU(): fail to read mtu file", "path", mtuFilePath)
		return 0
	}
	mtu, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		log.Log.Error(err, "GetNetdevMTU(): fail to convert mtu to int", "raw-mtu", strings.TrimSpace(string(data)))
		return 0
	}
	return mtu
}

func (n *network) SetNetdevMTU(pciAddr string, mtu int) error {
	log.Log.V(2).Info("SetNetdevMTU(): set MTU", "device", pciAddr, "mtu", mtu)
	if mtu <= 0 {
		log.Log.V(2).Info("SetNetdevMTU(): refusing to set MTU", "mtu", mtu)
		return nil
	}
	b := backoff.NewConstantBackOff(1 * time.Second)
	err := backoff.Retry(func() error {
		ifaceName, err := dputils.GetNetNames(pciAddr)
		if err != nil {
			log.Log.Error(err, "SetNetdevMTU(): fail to get interface name", "device", pciAddr)
			return err
		}
		if len(ifaceName) < 1 {
			return fmt.Errorf("SetNetdevMTU(): interface name is empty")
		}
		mtuFilePath := mtuFilePath(ifaceName[0], pciAddr)
		return os.WriteFile(mtuFilePath, []byte(strconv.Itoa(mtu)), os.ModeAppend)
	}, backoff.WithMaxRetries(b, 10))
	if err != nil {
		log.Log.Error(err, "SetNetdevMTU(): fail to write mtu file after retrying")
		return err
	}
	return nil
}

func (n *network) GetNetDevMac(ifaceName string) string {
	log.Log.V(2).Info("GetNetDevMac(): get Mac", "device", ifaceName)
	macFilePath := filepath.Join(vars.FilesystemRoot, consts.SysClassNet, ifaceName, "address")
	data, err := os.ReadFile(macFilePath)
	if err != nil {
		log.Log.Error(err, "GetNetDevMac(): fail to read Mac file", "path", macFilePath)
		return ""
	}

	return strings.TrimSpace(string(data))
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
