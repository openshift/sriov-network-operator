package utils

import (
	"fmt"
	"regexp"
	"strings"

	"sigs.k8s.io/controller-runtime/pkg/log"
)

// BlueField mode representation
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
	log.Log.Info("MstConfigReadData()", "device", pciAddress)
	args := []string{"-e", "-d", pciAddress, "q"}
	out, err := RunCommand("mstconfig", args...)
	return out, err
}

func ParseMstconfigOutput(mstOutput string, attributes []string) (fwCurrent, fwNext map[string]string) {
	log.Log.Info("ParseMstconfigOutput()", "attributes", attributes)
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
	log.Log.V(2).Info("MellanoxBlueFieldMode(): checking mode for device", "device", PciAddress)
	out, err := MstConfigReadData(PciAddress)
	if err != nil {
		log.Log.Error(err, "MellanoxBlueFieldMode(): failed to get mlx nic fw data")
		return -1, fmt.Errorf("failed to get mlx nic fw data %w", err)
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
		log.Log.V(2).Info("MellanoxBlueFieldMode(): device in DPU mode", "device", PciAddress)
		return bluefieldDpu, nil
	} else if strings.Contains(internalCPUPageSupplierstatus, extHostPf) &&
		strings.Contains(internalCPUEswitchManagerStatus, extHostPf) &&
		strings.Contains(internalCPUIbVportoStatus, extHostPf) &&
		strings.Contains(internalCPUOffloadEngineStatus, disabled) &&
		strings.Contains(internalCPUModelStatus, embeddedCPU) {
		log.Log.V(2).Info("MellanoxBlueFieldMode(): device in ConnectX mode", "device", PciAddress)
		return bluefieldConnectXMode, nil
	}

	log.Log.Error(err, "MellanoxBlueFieldMode(): unknown device status",
		"device", PciAddress, "mstconfig-output", out)
	return -1, fmt.Errorf("MellanoxBlueFieldMode(): unknown device status for %s", PciAddress)
}
