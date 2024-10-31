package cpu

import (
	"fmt"

	ghwPkg "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/host/internal/lib/ghw"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/host/types"
)

type cpuInfoProvider struct {
	ghwLib ghwPkg.GHWLib
}

func New(ghwLib ghwPkg.GHWLib) *cpuInfoProvider {
	return &cpuInfoProvider{
		ghwLib: ghwLib,
	}
}

func (c *cpuInfoProvider) GetCPUVendor() (types.CPUVendor, error) {
	cpuInfo, err := c.ghwLib.CPU()
	if err != nil {
		return -1, fmt.Errorf("can't retrieve the CPU vendor: %w", err)
	}

	if len(cpuInfo.Processors) == 0 {
		return -1, fmt.Errorf("wrong CPU information retrieved: %v", cpuInfo)
	}

	switch cpuInfo.Processors[0].Vendor {
	case "GenuineIntel":
		return types.CPUVendorIntel, nil
	case "AuthenticAMD":
		return types.CPUVendorAMD, nil
	case "ARM":
		return types.CPUVendorARM, nil
	}

	return -1, fmt.Errorf("unknown CPU vendor: %s", cpuInfo.Processors[0].Vendor)
}
