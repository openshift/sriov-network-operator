package utils

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/golang/glog"
)

//BlueField mode representation
type BlueFieldMode int

const (
	bluefieldDpu BlueFieldMode = iota
	bluefieldConnectXMode

	internalCPUPageSupplier   = "INTERNAL_CPU_PAGE_SUPPLIER"
	internalCPUEswitchManager = "INTERNAL_CPU_ESWITCH_MANAGER"
	internalCPUIbVporto       = "INTERNAL_CPU_IB_VPORT0"
	internalCPUOffloadEngine  = "INTERNAL_CPU_OFFLOAD_ENGINE"
	internalCPUModel          = "INTERNAL_CPU_MODEL"

	ecpf        = "ECPF"
	extHostPf   = "EXT_HOST_PF"
	embeddedCPU = "EMBEDDED_CPU"

	disabled = "DISABLED"
	enabled  = "ENABLED"
)

func MstConfigReadData(pciAddress string) (string, error) {
	glog.Infof("MstConfigReadData(): device %s", pciAddress)
	args := []string{"-e", "-d", pciAddress, "q"}
	out, err := RunCommand("mstconfig", args...)
	return out, err
}

func ParseMstconfigOutput(mstOutput string, attributes []string) (fwCurrent, fwNext map[string]string) {
	glog.Infof("ParseMstconfigOutput(): Attributes %v", attributes)
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

func mellanoxBlueFieldMode(PciAddress string) (BlueFieldMode, error) {
	glog.V(2).Infof("MellanoxBlueFieldMode():checking mode for card %s", PciAddress)
	out, err := MstConfigReadData(PciAddress)
	if err != nil {
		glog.Errorf("MellanoxBlueFieldMode(): failed to get mlx nic fw data %v", err)
		return -1, fmt.Errorf("failed to get mlx nic fw data %v", err)
	}

	attrs := []string{internalCPUPageSupplier,
		internalCPUEswitchManager,
		internalCPUIbVporto,
		internalCPUOffloadEngine,
		internalCPUModel}
	mstCurrentData, _ := ParseMstconfigOutput(out, attrs)

	internalCPUPageSupplierstatus, exist := mstCurrentData[internalCPUPageSupplier]
	if !exist {
		return 0, fmt.Errorf("failed to find %s in the mstconfig output command", internalCPUPageSupplier)
	}

	internalCPUEswitchManagerStatus, exist := mstCurrentData[internalCPUEswitchManager]
	if !exist {
		return 0, fmt.Errorf("failed to find %s in the mstconfig output command", internalCPUEswitchManager)
	}

	internalCPUIbVportoStatus, exist := mstCurrentData[internalCPUIbVporto]
	if !exist {
		return 0, fmt.Errorf("failed to find %s in the mstconfig output command", internalCPUIbVporto)
	}

	internalCPUOffloadEngineStatus, exist := mstCurrentData[internalCPUOffloadEngine]
	if !exist {
		return 0, fmt.Errorf("failed to find %s in the mstconfig output command", internalCPUOffloadEngine)
	}

	internalCPUModelStatus, exist := mstCurrentData[internalCPUModel]
	if !exist {
		return 0, fmt.Errorf("failed to find %s in the mstconfig output command", internalCPUModel)
	}

	// check for DPU
	if strings.Contains(internalCPUPageSupplierstatus, ecpf) &&
		strings.Contains(internalCPUEswitchManagerStatus, ecpf) &&
		strings.Contains(internalCPUIbVportoStatus, ecpf) &&
		strings.Contains(internalCPUOffloadEngineStatus, enabled) &&
		strings.Contains(internalCPUModelStatus, embeddedCPU) {
		glog.V(2).Infof("MellanoxBlueFieldMode():card %s in DPU mode", PciAddress)
		return bluefieldDpu, nil
	} else if strings.Contains(internalCPUPageSupplierstatus, extHostPf) &&
		strings.Contains(internalCPUEswitchManagerStatus, extHostPf) &&
		strings.Contains(internalCPUIbVportoStatus, extHostPf) &&
		strings.Contains(internalCPUOffloadEngineStatus, disabled) &&
		strings.Contains(internalCPUModelStatus, embeddedCPU) {
		glog.V(2).Infof("MellanoxBlueFieldMode():card %s in ConnectX mode", PciAddress)
		return bluefieldConnectXMode, nil
	}

	glog.Errorf("MellanoxBlueFieldMode(): unknown card status for %s mstconfig output \n %s", PciAddress, out)
	return -1, fmt.Errorf("MellanoxBlueFieldMode(): unknown card status for %s", PciAddress)
}
