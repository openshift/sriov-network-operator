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
	PluginName     string
	SpecVersion    string
}

type mlnxNic struct {
	enableSriov bool
	totalVfs    int
	linkType    string
	singlePort  bool
	firstPort   bool
}

const (
	EthLinkType        = "ETH"
	InfinibandLinkType = "IB"
	TotalVfs           = "NUM_OF_VFS"
	EnableSriov        = "SRIOV_EN"
	MellanoxVendorId   = "15b3"
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
	attributesToChange =  map[string]mlnxNic{}

	for _, iface := range new.Spec.Interfaces {
		if !isMlnxNicAndInNode(iface.PciAddress, new) {
			continue
		}
		ifaceFwData, err := getMlnxNicFwData(iface.PciAddress)
		if err != nil {
			return false, false, err
		}
		ifaceData, err := getMlnxNicData(iface.PciAddress, new)
		if err != nil {
			return false, false, err
		}
		if ifaceData.enableSriov != ifaceFwData.enableSriov || ifaceData.linkType != ifaceFwData.linkType || 
			ifaceData.totalVfs != ifaceFwData.totalVfs {
			needReboot = true
			break
		}
		attrs := &mlnxNic{totalVfs: -1}
		requireChange := false
		
		if ifaceFwData.totalVfs != iface.NumVfs {
			attrs.totalVfs = iface.NumVfs
			requireChange = true
		}
		if iface.NumVfs == 0 && ifaceFwData.enableSriov {
			attrs.enableSriov = false
			requireChange = true
		} else if iface.NumVfs > 0 && !ifaceFwData.enableSriov {
			attrs.enableSriov = true
			requireChange = true
		}
		if ifaceFwData.linkType != EthLinkType {
			ifaceFwData.linkType = EthLinkType
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
	glog.Infof("mellanox-plugin needDrain %v needReboot %v", needDrain, needReboot)
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
			if fwArgs.singlePort {
				cmdArgs = append(cmdArgs, "LINK_TYPE=" + EthLinkType)
			} else if fwArgs.firstPort {
				cmdArgs = append(cmdArgs, "LINK_TYPE_P1=" + EthLinkType)
			} else {
				cmdArgs = append(cmdArgs, "LINK_TYPE_P2=" + EthLinkType)
			}
		}
		out, err := runCommand("mstconfig", cmdArgs...)
		if err != nil {
			glog.Errorf("mellanox-plugin configFW(): failed : %v : %s", err, out)
			return err
		}
	}
	return nil
}

func mstconfigReadData(pciAddress string, attributes ...string) (string, error) {
	glog.Infof("mellanox-plugin mstconfigReadData(): try to read %s for device %s", attributes, pciAddress)

	args := []string{"-d", pciAddress, "q"}
	args = append(args, attributes...)
	out, err := runCommand("mstconfig", args...)
	if err != nil {
		glog.Errorf("mellanox-plugin mstconfigReadData(): failed : %v : %s", err, out)
	}
	return out, err
}

func runCommand(command string, args ...string) (string, error) {
	glog.Infof("mellanox-plugin runCommand(): %s %v", command, args)
	var stdout, stderr bytes.Buffer

	cmd := exec.Command(command, args...)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	return stdout.String(), err
}

func getMlnxNicFwData(pciAddress string) (*mlnxNic, error) {
	glog.Infof("mellanox-plugin getMlnxNicFwData(): for device %s", pciAddress)
	fwData := &mlnxNic{}
	attrs := []string{TotalVfs, EnableSriov}
	singlePort, err := isSinglePortNic(pciAddress) 
	if err != nil {
		return nil, err
	}
	if singlePort {
		attrs = append(attrs, "LINK_TYPE")
		fwData.singlePort = true
	} else {
		fwData.singlePort = false
		if isFirstPort(pciAddress) {
			attrs = append(attrs, "LINK_TYPE_P1")
			fwData.firstPort = true
		} else {
			attrs = append(attrs, "LINK_TYPE_P2")
			fwData.firstPort = false
		}
	}
	out, err := mstconfigReadData(pciAddress, attrs...)
	if err != nil {
		return nil, err
	}
	mstData := parseMstconfigOutput(out, attrs)
	if strings.Contains(mstData[EnableSriov], "True"){
		fwData.enableSriov = true
	} else {
		fwData.enableSriov = false
	}
	i, err := strconv.Atoi(mstData[TotalVfs])
	if err != nil {
		return nil, err
	}
	fwData.totalVfs = i
	var linkType string
	if fwData.singlePort {
		linkType = mstData["LINK_TYPE"]
	} else if fwData.firstPort {
		linkType = mstData["LINK_TYPE_P1"]
	} else {
		linkType = mstData["LINK_TYPE_P2"]
	}
	fwData.linkType = linkType[:len(linkType) - 3]

	return fwData, nil
}

func parseMstconfigOutput(mstOutput string, attributes []string) map[string]string {
	glog.Infof("mellanox-plugin parseMstconfigOutput(): Attributes %v", attributes)
	fwData := map[string]string{}
	formatRegex := regexp.MustCompile(`(?P<Attribute>\S+)\s+(?P<Value>\S+)`)
	mstOutputLines := strings.Split(mstOutput, "\n")
	for _, attr := range attributes {
		for _, line := range mstOutputLines {
			if strings.Contains(line, attr) {
				regexResult := formatRegex.FindStringSubmatch(line)
				fwData[attr] = regexResult[2]

			}
		}
	}
	return fwData
}

func isFirstPort(pciAddress string) bool {
	glog.Infof("mellanox-plugin isFirstPort(): device %s", pciAddress)
	return pciAddress[len(pciAddress) - 1] == '0'
}

func isSinglePortNic(pciAddress string) (bool, error) {
	glog.Infof("mellanox-plugin isSinglePortNic(): device %s", pciAddress)
	attrs := []string{"LINK_TYPE"}
	_, err := mstconfigReadData(pciAddress, attrs...)
	if err == nil {
		return true, nil
	}
	attrs = []string{"LINK_TYPE_P2"}
	_, err = mstconfigReadData(pciAddress, attrs...)
	if err != nil {
		return false, err
	}
	return false, nil
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

func getMlnxNicData (pciAddress string, state *sriovnetworkv1.SriovNetworkNodeState) (*mlnxNic, error) {
	glog.Infof("mellanox-plugin getMlnxNicData(): device %s", pciAddress)
	var iface sriovnetworkv1.InterfaceExt
	ifaceData := &mlnxNic{}
	for _, iface = range state.Status.Interfaces {
		if iface.PciAddress == pciAddress {
			break
		}
	}
	ifaceData.totalVfs = iface.TotalVfs
	if iface.TotalVfs > 0 {
		ifaceData.enableSriov = true
	}
	args := []string{iface.Name}
	out, err := runCommand("ifconfig", args...)
	if err != nil {
		glog.Errorf("mellanox-plugin getMlnxNicData(): %v : %v", err, out)
		return nil, err
	}
	if strings.Contains(out, "Infiniband") {
		ifaceData.linkType = InfinibandLinkType
	} else {
		ifaceData.linkType = EthLinkType
	}
	return ifaceData, nil
}
