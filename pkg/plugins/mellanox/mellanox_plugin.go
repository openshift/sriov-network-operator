package main

import (
	"bytes"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"

	"github.com/golang/glog"
	sriovnetworkv1 "github.com/openshift/sriov-network-operator/pkg/apis/sriovnetwork/v1"
)

type MellanoxPlugin struct {
	PluginName  string
	SpecVersion string
}

type mlnxNic struct {
	enableSriov bool
	totalVfs    int
	linkTypeP1  string
	linkTypeP2  string
}

const (
	EthLinkType           = "ETH"
	InfinibandLinkType    = "IB"
	PreconfiguredLinkType = "Preconfigured"
	UknownLinkType        = "Uknown"
	TotalVfs              = "NUM_OF_VFS"
	EnableSriov           = "SRIOV_EN"
	LinkTypeP1            = "LINK_TYPE_P1"
	LinkTypeP2            = "LINK_TYPE_P2"
	MellanoxVendorId      = "15b3"
)

var Plugin MellanoxPlugin
var attributesToChange map[string]mlnxNic
var mellanoxNicsStatus map[string]map[string]sriovnetworkv1.InterfaceExt
var mellanoxNicsSpec map[string]sriovnetworkv1.Interface

// Initialize our plugin and set up initial values
func init() {
	Plugin = MellanoxPlugin{
		PluginName:  "mellanox_plugin",
		SpecVersion: "1.0",
	}
	mellanoxNicsStatus = map[string]map[string]sriovnetworkv1.InterfaceExt{}
}

// Name returns the name of the plugin
func (p *MellanoxPlugin) Name() string {
	return p.PluginName
}

// SpecVersion returns the version of the spec expected by the plugin
func (p *MellanoxPlugin) Spec() string {
	return p.SpecVersion
}

// OnNodeStateAdd Invoked when SriovNetworkNodeState CR is created, return if need dain and/or reboot node
func (p *MellanoxPlugin) OnNodeStateAdd(state *sriovnetworkv1.SriovNetworkNodeState) (needDrain bool, needReboot bool, err error) {
	glog.Info("mellanox-plugin OnNodeStateAdd()")

	return p.OnNodeStateChange(nil, state)
}

// OnNodeStateChange Invoked when SriovNetworkNodeState CR is updated, return if need dain and/or reboot node
func (p *MellanoxPlugin) OnNodeStateChange(old, new *sriovnetworkv1.SriovNetworkNodeState) (needDrain bool, needReboot bool, err error) {
	glog.Info("mellanox-Plugin OnNodeStateChange()")
	needDrain = false
	needReboot = false
	err = nil
	attributesToChange = map[string]mlnxNic{}
	mellanoxNicsSpec = map[string]sriovnetworkv1.Interface{}
	processedNics := map[string]bool{}

	// Read mellanox NIC status once
	if mellanoxNicsStatus == nil || len(mellanoxNicsStatus) == 0 {
		for _, iface := range new.Status.Interfaces {
			if iface.Vendor != MellanoxVendorId {
				continue
			}

			pciPrefix := getPciAddressPrefix(iface.PciAddress)
			if ifaces, ok := mellanoxNicsStatus[pciPrefix]; ok {
				ifaces[iface.PciAddress] = iface
			} else {
				mellanoxNicsStatus[pciPrefix] = map[string]sriovnetworkv1.InterfaceExt{iface.PciAddress: iface}
			}
		}
	}

	// Add only mellanox cards that required changes in the map, to help track dual port NICs
	for _, iface := range new.Spec.Interfaces {
		pciPrefix := getPciAddressPrefix(iface.PciAddress)
		if _, ok := mellanoxNicsStatus[pciPrefix]; !ok {
			continue
		}

		mellanoxNicsSpec[iface.PciAddress] = iface
	}

	for _, ifaceSpec := range mellanoxNicsSpec {
		pciPrefix := getPciAddressPrefix(ifaceSpec.PciAddress)
		// skip processed nics, help not running the same logic 2 times for dual port NICs
		if _, ok := processedNics[pciPrefix]; ok {
			continue
		}
		processedNics[pciPrefix] = true

		fwCurrent, fwNext, err := getMlnxNicFwData(ifaceSpec.PciAddress)
		if err != nil {
			return false, false, err
		}

		isDualPort := isDualPort(ifaceSpec.PciAddress)
		// Attributes to change
		attrs := &mlnxNic{totalVfs: -1}
		var changeWithoutReboot bool

		var totalVfs int
		totalVfs, needReboot, changeWithoutReboot = handleTotalVfs(fwCurrent, fwNext, attrs, ifaceSpec, isDualPort)
		sriovEnNeedReboot, sriovEnChangeWithoutReboot := handleEnableSriov(totalVfs, fwCurrent, fwNext, attrs)
		needReboot = needReboot || sriovEnNeedReboot
		changeWithoutReboot = changeWithoutReboot || sriovEnChangeWithoutReboot

		needLinkChange, err := handleLinkType(pciPrefix, fwCurrent, attrs)
		if err != nil {
			return false, false, err
		}

		needReboot = needReboot || needLinkChange
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

		// Skip unsupported devices
		if id := sriovnetworkv1.GetVfDeviceId(portsMap[pciAddress].DeviceID); id != "" {
			continue
		}

		_, fwNext, err := getMlnxNicFwData(pciAddress)
		if err != nil {
			return false, false, err
		}

		if fwNext.totalVfs > 0 || fwNext.enableSriov {
			attributesToChange[pciAddress] = mlnxNic{totalVfs: 0}
			glog.V(2).Infof("Changing TotalVfs %d to 0, doesn't require rebooting", fwNext.totalVfs)
		}
	}

	if needReboot {
		needDrain = true
	}
	glog.V(2).Infof("mellanox-plugin needDrain %v needReboot %v", needDrain, needReboot)
	return
}

// Apply config change
func (p *MellanoxPlugin) Apply() error {
	glog.Info("mellanox-plugin Apply()")
	return configFW()
}

func configFW() error {
	glog.Info("mellanox-plugin configFW()")
	for pciAddr, fwArgs := range attributesToChange {
		cmdArgs := []string{"-d", pciAddr, "-y", "set"}
		if fwArgs.enableSriov {
			cmdArgs = append(cmdArgs, fmt.Sprintf("%s=True", EnableSriov))
		} else if fwArgs.totalVfs == 0 {
			cmdArgs = append(cmdArgs, fmt.Sprintf("%s=False", EnableSriov))
		}
		if fwArgs.totalVfs > -1 {
			cmdArgs = append(cmdArgs, fmt.Sprintf("%s=%d", TotalVfs, fwArgs.totalVfs))
		}
		if len(fwArgs.linkTypeP1) > 0 {
			cmdArgs = append(cmdArgs, fmt.Sprintf("%s=%s", LinkTypeP1, fwArgs.linkTypeP1))
		}
		if len(fwArgs.linkTypeP2) > 0 {
			cmdArgs = append(cmdArgs, fmt.Sprintf("%s=%s", LinkTypeP2, fwArgs.linkTypeP2))
		}

		glog.V(2).Infof("mellanox-plugin: configFW(): %v", cmdArgs)
		if len(cmdArgs) <= 4 {
			continue
		}
		_, err := runCommand("mstconfig", cmdArgs...)
		if err != nil {
			glog.Errorf("mellanox-plugin configFW(): failed : %v", err)
			return err
		}
	}
	return nil
}

func mstConfigReadData(pciAddress string) (string, error) {
	glog.Infof("mellanox-plugin mstConfigReadData(): device %s", pciAddress)
	args := []string{"-e", "-d", pciAddress, "q"}
	out, err := runCommand("mstconfig", args...)
	return out, err
}

func runCommand(command string, args ...string) (string, error) {
	glog.Infof("mellanox-plugin runCommand(): %s %v", command, args)
	var stdout, stderr bytes.Buffer

	cmd := exec.Command(command, args...)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	glog.V(2).Infof("mellanox-plugin: runCommand(): %s, %v", command, args)
	err := cmd.Run()
	glog.V(2).Infof("mellanox-plugin: runCommand(): %s", stdout.String())
	return stdout.String(), err
}

func getMlnxNicFwData(pciAddress string) (current, next *mlnxNic, err error) {
	glog.Infof("mellanox-plugin getMlnxNicFwData(): device %s", pciAddress)
	err = nil
	attrs := []string{TotalVfs, EnableSriov, LinkTypeP1, LinkTypeP2}

	out, err := mstConfigReadData(pciAddress)
	if err != nil {
		glog.Errorf("mellanox-plugin getMlnxNicFwData(): failed %v", err)
		return
	}
	mstCurrentData, mstNextData := parseMstconfigOutput(out, attrs)
	current, err = mlnxNicFromMap(mstCurrentData)
	if err != nil {
		glog.Errorf("mellanox-plugin getMlnxNicFwData(): %v", err)
		return
	}
	next, err = mlnxNicFromMap(mstNextData)
	if err != nil {
		glog.Errorf("mellanox-plugin getMlnxNicFwData(): %v", err)
	}
	return
}

func mlnxNicFromMap(mstData map[string]string) (*mlnxNic, error) {
	glog.Infof("mellanox-plugin mlnxNicFromMap() %v", mstData)
	fwData := &mlnxNic{}
	if strings.Contains(mstData[EnableSriov], "True") {
		fwData.enableSriov = true
	}
	i, err := strconv.Atoi(mstData[TotalVfs])
	if err != nil {
		return nil, err
	}

	fwData.totalVfs = i
	fwData.linkTypeP1 = getLinkType(mstData[LinkTypeP1])
	if linkTypeP2, ok := mstData[LinkTypeP2]; ok {
		fwData.linkTypeP2 = getLinkType(linkTypeP2)
	}

	return fwData, nil
}

func parseMstconfigOutput(mstOutput string, attributes []string) (fwCurrent, fwNext map[string]string) {
	glog.Infof("mellanox-plugin parseMstconfigOutput(): Attributes %v", attributes)
	fwCurrent = map[string]string{}
	fwNext = map[string]string{}
	formatRegex := regexp.MustCompile(`(?P<Attribute>\w+)\s+(?P<Default>\S+)\s+(?P<Current>\S+)\s+(?P<Next>\S+)`)
	mstOutputLines := strings.Split(mstOutput, "\n")
	for _, attr := range attributes {
		for _, line := range mstOutputLines {
			if strings.Contains(line, attr) {
				regexResult := formatRegex.FindStringSubmatch(line)
				fwCurrent[attr] = regexResult[3]
				fwNext[attr] = regexResult[4]
				break
			}
		}
	}
	return
}

func getPciAddressPrefix(pciAddress string) string {
	return pciAddress[:len(pciAddress)-1]
}

func isDualPort(pciAddress string) bool {
	glog.Infof("mellanox-plugin isDualPort(): pciAddress %s", pciAddress)
	pciAddressPrefix := getPciAddressPrefix(pciAddress)
	return len(mellanoxNicsStatus[pciAddressPrefix]) > 1
}

func getLinkType(linkType string) string {
	glog.Infof("mellanox-plugin getLinkType(): linkType %s", linkType)
	if strings.Contains(linkType, EthLinkType) {
		return EthLinkType
	} else if strings.Contains(linkType, InfinibandLinkType) {
		return InfinibandLinkType
	} else if len(linkType) > 0 {
		glog.Warningf("mellanox-plugin getMlnxNicFwData(): link type %s is not one of [ETH, IB]", linkType)
		return UknownLinkType
	} else {
		glog.Warning("mellanox-plugin getMlnxNicFwData(): LINK_TYPE_P* attribute was not found")
		return PreconfiguredLinkType
	}
}

func isLinkTypeRequireChange(iface sriovnetworkv1.Interface, ifaceStatus sriovnetworkv1.InterfaceExt, fwLinkType string) (bool, error) {
	glog.Infof("mellanox-plugin isLinkTypeRequireChange(): device %s", iface.PciAddress)
	if iface.LinkType != "" && !strings.EqualFold(ifaceStatus.LinkType, iface.LinkType) {
		if !strings.EqualFold(iface.LinkType, EthLinkType) && !strings.EqualFold(iface.LinkType, InfinibandLinkType) {
			return false, fmt.Errorf("mellanox-plugin OnNodeStateChange(): Not supported link type: %s,"+
				" supported link types: [eth, ETH, ib, and IB]", iface.LinkType)
		}
		if fwLinkType == UknownLinkType {
			return false, fmt.Errorf("mellanox-plugin OnNodeStateChange(): Unknown link type: %s", fwLinkType)
		}
		if fwLinkType == PreconfiguredLinkType {
			return false, fmt.Errorf("mellanox-plugin OnNodeStateChange(): Network card %s does not support link type change", iface.PciAddress)
		}

		return true, nil
	}

	return false, nil
}

func getOtherPortSpec(pciAddress string) *sriovnetworkv1.Interface {
	glog.Infof("mellanox-plugin getOtherPortSpec(): pciAddress %s", pciAddress)
	pciAddrPrefix := getPciAddressPrefix(pciAddress)
	pciAddrSuffix := pciAddress[len(pciAddrPrefix):]

	if pciAddrSuffix == "0" {
		iface := mellanoxNicsSpec[pciAddrPrefix+"1"]
		return &iface
	}

	iface := mellanoxNicsSpec[pciAddrPrefix+"0"]
	return &iface
}

// handleTotalVfs return required total VFs or max (required VFs for dual port NIC) and needReboot if totalVfs will change
func handleTotalVfs(fwCurrent, fwNext, attrs *mlnxNic, ifaceSpec sriovnetworkv1.Interface, isDualPort bool) (
	totalVfs int, needReboot, changeWithoutReboot bool) {

	totalVfs = ifaceSpec.NumVfs
	// Check if the other port is changing the number of VF
	if isDualPort {
		otherIfaceSpec := getOtherPortSpec(ifaceSpec.PciAddress)
		if otherIfaceSpec != nil {
			if otherIfaceSpec.NumVfs > totalVfs {
				totalVfs = otherIfaceSpec.NumVfs
			}
		}
	}

	if fwCurrent.totalVfs != totalVfs {
		glog.V(2).Infof("Changing TotalVfs %d to %d, needs reboot", fwCurrent.totalVfs, totalVfs)
		attrs.totalVfs = totalVfs
		needReboot = true
	}

	// Remove policy then re-apply it
	if !needReboot && fwNext.totalVfs != totalVfs {
		glog.V(2).Infof("Changing TotalVfs %d to 0, doesn't require rebooting", fwCurrent.totalVfs)
		attrs.totalVfs = totalVfs
		changeWithoutReboot = true
	}

	return
}

// handleEnableSriov based on totalVfs it decide to disable (totalVfs=0) or enable (totalVfs changed from 0) sriov
// and need reboot if enableSriov will change
func handleEnableSriov(totalVfs int, fwCurrent, fwNext, attrs *mlnxNic) (needReboot, changeWithoutReboot bool) {
	if totalVfs == 0 && fwCurrent.enableSriov {
		glog.V(2).Info("disabling Sriov, needs reboot")
		attrs.enableSriov = false
		return true, false
	} else if totalVfs > 0 && !fwCurrent.enableSriov {
		glog.V(2).Info("enabling Sriov, needs reboot")
		attrs.enableSriov = true
		return true, false
	} else if totalVfs > 0 && !fwNext.enableSriov {
		attrs.enableSriov = true
		return false, true
	}

	return false, false
}

func getIfaceStatus(pciAddress string) sriovnetworkv1.InterfaceExt {
	return mellanoxNicsStatus[getPciAddressPrefix(pciAddress)][pciAddress]
}

// handleLinkType based on existing linkType and requested link
func handleLinkType(pciPrefix string, fwData, attr *mlnxNic) (bool, error) {
	needReboot := false

	pciAddress := pciPrefix + "0"
	if firstPortSpec, ok := mellanoxNicsSpec[pciAddress]; ok {
		ifaceStatus := getIfaceStatus(pciAddress)
		needChange, err := isLinkTypeRequireChange(firstPortSpec, ifaceStatus, fwData.linkTypeP1)
		if err != nil {
			return false, err
		}

		if needChange {
			glog.V(2).Infof("Changing LinkTypeP1 %s to %s, needs reboot", fwData.linkTypeP1, firstPortSpec.LinkType)
			attr.linkTypeP1 = firstPortSpec.LinkType
			needReboot = true
		}
	}

	pciAddress = pciPrefix + "1"
	if secondPortSpec, ok := mellanoxNicsSpec[pciAddress]; ok {
		ifaceStatus := getIfaceStatus(pciAddress)
		needChange, err := isLinkTypeRequireChange(secondPortSpec, ifaceStatus, fwData.linkTypeP2)
		if err != nil {
			return false, err
		}

		if needChange {
			glog.V(2).Infof("Changing LinkTypeP2 %s to %s, needs reboot", fwData.linkTypeP2, secondPortSpec.LinkType)
			attr.linkTypeP2 = secondPortSpec.LinkType
			needReboot = true
		}
	}

	return needReboot, nil
}
