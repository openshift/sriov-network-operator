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
	linkType    string
	firstPort   bool
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

// Initialize our plugin and set up initial values
func init() {
	Plugin = MellanoxPlugin{
		PluginName:  "mellanox_plugin",
		SpecVersion: "1.0",
	}
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

	for _, iface := range new.Spec.Interfaces {
		if !isMlnxNicAndInNode(iface.PciAddress, new) {
			continue
		}
		fwCurrent, fwNext, err := getMlnxNicFwData(iface.PciAddress)
		if err != nil {
			return false, false, err
		}
		if fwCurrent.enableSriov != fwNext.enableSriov || fwCurrent.totalVfs != fwNext.totalVfs ||
			fwCurrent.linkType != fwNext.linkType {
			needReboot = true
			break
		}
		attrs := &mlnxNic{totalVfs: -1, firstPort: fwCurrent.firstPort}
		requireChange := false

		if fwCurrent.totalVfs != iface.NumVfs {
			attrs.totalVfs = iface.NumVfs
			requireChange = true
		}
		if iface.NumVfs == 0 && fwCurrent.enableSriov {
			attrs.enableSriov = false
			requireChange = true
		} else if iface.NumVfs > 0 && !fwCurrent.enableSriov {
			attrs.enableSriov = true
			requireChange = true
		}
		if fwCurrent.linkType != EthLinkType && fwCurrent.linkType != PreconfiguredLinkType && fwCurrent.linkType != UknownLinkType {
			attrs.linkType = EthLinkType
			requireChange = true
		}
		if requireChange {
			attributesToChange[iface.PciAddress] = *attrs
			needReboot = true
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
		if len(fwArgs.linkType) > 0 {
			if fwArgs.firstPort {
				cmdArgs = append(cmdArgs, fmt.Sprintf("%s=%s", LinkTypeP1, EthLinkType))
			} else {
				cmdArgs = append(cmdArgs, fmt.Sprintf("%s=%s", LinkTypeP2, EthLinkType))
			}
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
	attrs := []string{TotalVfs, EnableSriov}
	firstPort := isFirstPort(pciAddress)

	if firstPort {
		attrs = append(attrs, LinkTypeP1)
	} else {
		attrs = append(attrs, LinkTypeP2)
	}
	out, err := mstConfigReadData(pciAddress)
	if err != nil {
		glog.Errorf("mellanox-plugin getMlnxNicFwData(): failed %v", err)
		return
	}
	mstCurrentData, mstNextData := parseMstconfigOutput(out, attrs)
	current, err = mlnxNicFromMap(mstCurrentData, firstPort)
	if err != nil {
		glog.Errorf("mellanox-plugin getMlnxNicFwData(): %v", err)
		return
	}
	next, err = mlnxNicFromMap(mstNextData, firstPort)
	if err != nil {
		glog.Errorf("mellanox-plugin getMlnxNicFwData(): %v", err)
	}
	return
}

func mlnxNicFromMap(mstData map[string]string, firstPort bool) (*mlnxNic, error) {
	glog.Infof("mellanox-plugin mlnxNicFromMap() %v, %v", mstData, firstPort)
	fwData := &mlnxNic{firstPort: firstPort}
	var linkType string
	if strings.Contains(mstData[EnableSriov], "True") {
		fwData.enableSriov = true
	}
	i, err := strconv.Atoi(mstData[TotalVfs])
	if err != nil {
		return nil, err
	}
	fwData.totalVfs = i
	if fwData.firstPort {
		linkType = mstData[LinkTypeP1]
	} else {
		linkType = mstData[LinkTypeP2]
	}

	if strings.Contains(linkType, EthLinkType) {
		fwData.linkType = EthLinkType
	} else if strings.Contains(linkType, InfinibandLinkType) {
		fwData.linkType = InfinibandLinkType
	} else if len(linkType) > 0 {
		glog.Warningf("mellanox-plugin getMlnxNicFwData(): link type %s is not one of [ETH, IB]", linkType)
		fwData.linkType = UknownLinkType
	} else {
		glog.Warning("mellanox-plugin getMlnxNicFwData(): LINK_TYPE_P* attribute was not found")
		fwData.linkType = PreconfiguredLinkType
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

func isFirstPort(pciAddress string) bool {
	glog.Infof("mellanox-plugin isFirstPort(): device %s", pciAddress)
	return pciAddress[len(pciAddress)-1] == '0'
}

func isMlnxNicAndInNode(pciAddress string, state *sriovnetworkv1.SriovNetworkNodeState) bool {
	glog.Infof("mellanox-plugin isMlnxNicAndInNode(): device %s", pciAddress)
	for _, iface := range state.Status.Interfaces {
		if iface.PciAddress == pciAddress {
			if iface.Vendor == MellanoxVendorId {
				return true
			}
			return false
		}
	}
	return false
}
