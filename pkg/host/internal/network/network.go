package network

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/cenkalti/backoff"
	"github.com/vishvananda/netlink/nl"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/consts"
	dputilsPkg "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/host/internal/lib/dputils"
	ethtoolPkg "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/host/internal/lib/ethtool"
	netlinkPkg "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/host/internal/lib/netlink"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/host/types"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/utils"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/vars"
)

type network struct {
	utilsHelper utils.CmdInterface
	dputilsLib  dputilsPkg.DPUtilsLib
	netlinkLib  netlinkPkg.NetlinkLib
	ethtoolLib  ethtoolPkg.EthtoolLib
}

func New(utilsHelper utils.CmdInterface, dputilsLib dputilsPkg.DPUtilsLib, netlinkLib netlinkPkg.NetlinkLib, ethtoolLib ethtoolPkg.EthtoolLib) types.NetworkInterface {
	return &network{
		utilsHelper: utilsHelper,
		dputilsLib:  dputilsLib,
		netlinkLib:  netlinkLib,
		ethtoolLib:  ethtoolLib,
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

// GetNetDevNodeGUID returns the network interface node GUID if device is RDMA capable otherwise returns empty string
func (n *network) GetNetDevNodeGUID(pciAddr string) string {
	if len(pciAddr) == 0 {
		return ""
	}

	rdmaDevicesPath := filepath.Join(vars.FilesystemRoot, consts.SysBusPciDevices, pciAddr, "infiniband")
	rdmaDevices, err := os.ReadDir(rdmaDevicesPath)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			log.Log.Error(err, "GetNetDevNodeGUID(): failed to read RDMA related directory", "pciAddr", pciAddr)
		}
		return ""
	}

	if len(rdmaDevices) != 1 {
		log.Log.Error(err, "GetNetDevNodeGUID(): expected just one RDMA device", "pciAddr", pciAddr, "numOfDevices", len(rdmaDevices))
		return ""
	}

	rdmaLink, err := n.netlinkLib.RdmaLinkByName(rdmaDevices[0].Name())
	if err != nil {
		log.Log.Error(err, "GetNetDevNodeGUID(): failed to get RDMA link", "pciAddr", pciAddr)
		return ""
	}

	return rdmaLink.Attrs.NodeGuid
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

// GetDevlinkDeviceParam returns devlink parameter for the device as a string, if the parameter has multiple values
// then the function will return only first one from the list.
func (n *network) GetDevlinkDeviceParam(pciAddr, paramName string) (string, error) {
	funcLog := log.Log.WithValues("device", pciAddr, "param", paramName)
	funcLog.V(2).Info("GetDevlinkDeviceParam(): get device parameter")
	param, err := n.netlinkLib.DevlinkGetDeviceParamByName(consts.BusPci, pciAddr, paramName)
	if err != nil {
		funcLog.Error(err, "GetDevlinkDeviceParam(): fail to get devlink device param")
		return "", err
	}
	if len(param.Values) == 0 {
		err = fmt.Errorf("param %s has no value", paramName)
		funcLog.Error(err, "GetDevlinkDeviceParam(): error")
		return "", err
	}
	var value string
	switch param.Type {
	case nl.DEVLINK_PARAM_TYPE_U8, nl.DEVLINK_PARAM_TYPE_U16, nl.DEVLINK_PARAM_TYPE_U32:
		var valData uint64
		switch v := param.Values[0].Data.(type) {
		case uint8:
			valData = uint64(v)
		case uint16:
			valData = uint64(v)
		case uint32:
			valData = uint64(v)
		default:
			return "", fmt.Errorf("unexpected uint type type")
		}
		value = strconv.FormatUint(valData, 10)

	case nl.DEVLINK_PARAM_TYPE_STRING:
		value = param.Values[0].Data.(string)
	case nl.DEVLINK_PARAM_TYPE_BOOL:
		value = strconv.FormatBool(param.Values[0].Data.(bool))
	default:
		return "", fmt.Errorf("unknown value type: %d", param.Type)
	}
	funcLog.V(2).Info("GetDevlinkDeviceParam(): result", "value", value)
	return value, nil
}

// SetDevlinkDeviceParam set devlink parameter for the device, accepts paramName and value
// as a string. Automatically set CMODE for the parameter and converts the value to the right
// type before submitting it.
func (n *network) SetDevlinkDeviceParam(pciAddr, paramName, value string) error {
	funcLog := log.Log.WithValues("device", pciAddr, "param", paramName, "value", value)
	funcLog.V(2).Info("SetDevlinkDeviceParam(): set device parameter")
	param, err := n.netlinkLib.DevlinkGetDeviceParamByName(consts.BusPci, pciAddr, paramName)
	if err != nil {
		funcLog.Error(err, "SetDevlinkDeviceParam(): can't get existing param data")
		return err
	}
	if len(param.Values) == 0 {
		err = fmt.Errorf("param %s has no value", paramName)
		funcLog.Error(err, "SetDevlinkDeviceParam(): error")
		return err
	}
	targetCMOD := param.Values[0].CMODE
	var typedValue interface{}
	var v uint64
	switch param.Type {
	case nl.DEVLINK_PARAM_TYPE_U8:
		v, err = strconv.ParseUint(value, 10, 8)
		typedValue = uint8(v)
	case nl.DEVLINK_PARAM_TYPE_U16:
		v, err = strconv.ParseUint(value, 10, 16)
		typedValue = uint16(v)
	case nl.DEVLINK_PARAM_TYPE_U32:
		v, err = strconv.ParseUint(value, 10, 32)
		typedValue = uint32(v)
	case nl.DEVLINK_PARAM_TYPE_STRING:
		err = nil
		typedValue = value
	case nl.DEVLINK_PARAM_TYPE_BOOL:
		typedValue, err = strconv.ParseBool(value)
	default:
		return fmt.Errorf("parameter has unknown value type: %d", param.Type)
	}
	if err != nil {
		err = fmt.Errorf("failed to convert value %s to the required type: %T, devlink paramType is: %d", value, typedValue, param.Type)
		funcLog.Error(err, "SetDevlinkDeviceParam(): error")
		return err
	}
	if err := n.netlinkLib.DevlinkSetDeviceParam(consts.BusPci, pciAddr, paramName, targetCMOD, typedValue); err != nil {
		funcLog.Error(err, "SetDevlinkDeviceParam(): failed to set parameter")
		return err
	}
	return nil
}

// EnableHwTcOffload makes sure that hw-tc-offload feature is enabled if device supports it
func (n *network) EnableHwTcOffload(ifaceName string) error {
	log.Log.V(2).Info("EnableHwTcOffload(): enable offloading", "device", ifaceName)
	hwTcOffloadFeatureName := "hw-tc-offload"

	knownFeatures, err := n.ethtoolLib.FeatureNames(ifaceName)
	if err != nil {
		log.Log.Error(err, "EnableHwTcOffload(): can't list supported features", "device", ifaceName)
		return err
	}
	if _, isKnown := knownFeatures[hwTcOffloadFeatureName]; !isKnown {
		log.Log.V(0).Info("EnableHwTcOffload(): can't enable feature, feature is not supported", "device", ifaceName)
		return nil
	}
	currentFeaturesState, err := n.ethtoolLib.Features(ifaceName)
	if err != nil {
		log.Log.Error(err, "EnableHwTcOffload(): can't read features state for device", "device", ifaceName)
		return err
	}
	if currentFeaturesState[hwTcOffloadFeatureName] {
		log.Log.V(2).Info("EnableHwTcOffload(): already enabled", "device", ifaceName)
		return nil
	}
	if err := n.ethtoolLib.Change(ifaceName, map[string]bool{hwTcOffloadFeatureName: true}); err != nil {
		log.Log.Error(err, "EnableHwTcOffload(): can't set feature for device", "device", ifaceName)
		return err
	}
	updatedFeaturesState, err := n.ethtoolLib.Features(ifaceName)
	if err != nil {
		log.Log.Error(err, "EnableHwTcOffload(): can't read features state for device", "device", ifaceName)
		return err
	}
	if updatedFeaturesState[hwTcOffloadFeatureName] {
		log.Log.V(2).Info("EnableHwTcOffload(): feature enabled", "device", ifaceName)
		return nil
	}
	log.Log.V(0).Info("EnableHwTcOffload(): feature is still disabled, not supported by device", "device", ifaceName)
	return nil
}

// GetNetDevLinkAdminState returns the admin state of the interface.
func (n *network) GetNetDevLinkAdminState(ifaceName string) string {
	log.Log.V(2).Info("GetNetDevLinkAdminState(): get LinkAdminState", "device", ifaceName)
	if len(ifaceName) == 0 {
		return ""
	}

	link, err := n.netlinkLib.LinkByName(ifaceName)
	if err != nil {
		log.Log.Error(err, "GetNetDevLinkAdminState(): failed to get link", "device", ifaceName)
		return ""
	}

	if n.netlinkLib.IsLinkAdminStateUp(link) {
		return consts.LinkAdminStateUp
	}

	return consts.LinkAdminStateDown
}
