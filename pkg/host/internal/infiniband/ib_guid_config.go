package infiniband

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/host/internal/lib/netlink"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/host/types"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/utils"
)

type ibPfGUIDJSONConfig struct {
	PciAddress string         `json:"pciAddress,omitempty"`
	PfGUID     string         `json:"pfGuid,omitempty"`
	GUIDs      []string       `json:"guids,omitempty"`
	GUIDsRange *GUIDRangeJSON `json:"guidsRange,omitempty"`
}

type GUIDRangeJSON struct {
	Start string `json:"start,omitempty"`
	End   string `json:"end,omitempty"`
}

type ibPfGUIDConfig struct {
	GUIDs     []GUID
	GUIDRange *GUIDRange
}

type GUIDRange struct {
	Start GUID
	End   GUID
}

func getIbGUIDConfig(configPath string, netlinkLib netlink.NetlinkLib, networkHelper types.NetworkInterface) (map[string]ibPfGUIDConfig, error) {
	links, err := netlinkLib.LinkList()
	if err != nil {
		return nil, err
	}
	rawConfigs, err := readJSONConfig(configPath)
	if err != nil {
		return nil, err
	}
	resultConfigs := map[string]ibPfGUIDConfig{}
	// Parse JSON config into an internal struct
	for _, rawConfig := range rawConfigs {
		pciAddress, err := getPfPciAddressFromRawConfig(rawConfig, links, networkHelper)
		if err != nil {
			return nil, fmt.Errorf("failed to extract pci address from ib guid config: %w", err)
		}
		if len(rawConfig.GUIDs) == 0 && (rawConfig.GUIDsRange == nil || (rawConfig.GUIDsRange.Start == "" || rawConfig.GUIDsRange.End == "")) {
			return nil, fmt.Errorf("either guid list or guid range should be provided, got none")
		}
		if len(rawConfig.GUIDs) != 0 && rawConfig.GUIDsRange != nil {
			return nil, fmt.Errorf("either guid list or guid range should be provided, got both")
		}
		if rawConfig.GUIDsRange != nil && ((rawConfig.GUIDsRange.Start != "" && rawConfig.GUIDsRange.End == "") || (rawConfig.GUIDsRange.Start == "" && rawConfig.GUIDsRange.End != "")) {
			return nil, fmt.Errorf("both guid rangeStart and rangeEnd should be provided, got one")
		}
		if len(rawConfig.GUIDs) != 0 {
			var guids []GUID
			for _, guidStr := range rawConfig.GUIDs {
				guid, err := ParseGUID(guidStr)
				if err != nil {
					return nil, fmt.Errorf("failed to parse ib guid %s: %w", guidStr, err)
				}
				guids = append(guids, guid)
			}
			resultConfigs[pciAddress] = ibPfGUIDConfig{
				GUIDs: guids,
			}
			continue
		}

		rangeStart, err := ParseGUID(rawConfig.GUIDsRange.Start)
		if err != nil {
			return nil, fmt.Errorf("failed to parse ib guid range start: %w", err)
		}
		rangeEnd, err := ParseGUID(rawConfig.GUIDsRange.End)
		if err != nil {
			return nil, fmt.Errorf("failed to parse ib guid range end: %w", err)
		}
		if rangeEnd < rangeStart {
			return nil, fmt.Errorf("range end cannot be less then range start")
		}
		resultConfigs[pciAddress] = ibPfGUIDConfig{
			GUIDRange: &GUIDRange{
				Start: rangeStart,
				End:   rangeEnd,
			},
		}
	}
	return resultConfigs, nil
}

// readJSONConfig reads the file at the given path and unmarshals the contents into an array of ibPfGUIDJSONConfig structs
func readJSONConfig(configPath string) ([]ibPfGUIDJSONConfig, error) {
	data, err := os.ReadFile(utils.GetHostExtensionPath(configPath))
	if err != nil {
		return nil, fmt.Errorf("failed to read ib guid config: %w", err)
	}
	var configs []ibPfGUIDJSONConfig
	if err := json.Unmarshal(data, &configs); err != nil {
		return nil, fmt.Errorf("failed to unmarshal content of ib guid config: %w", err)
	}
	return configs, nil
}

func getPfPciAddressFromRawConfig(pfRawConfig ibPfGUIDJSONConfig, links []netlink.Link, networkHelper types.NetworkInterface) (string, error) {
	if pfRawConfig.PciAddress != "" && pfRawConfig.PfGUID != "" {
		return "", fmt.Errorf("either PCI address or PF GUID required to describe an interface, both provided")
	}
	if pfRawConfig.PciAddress == "" && pfRawConfig.PfGUID == "" {
		return "", fmt.Errorf("either PCI address or PF GUID required to describe an interface, none provided")
	}
	if pfRawConfig.PciAddress != "" {
		return pfRawConfig.PciAddress, nil
	}
	// PfGUID is provided, need to resolve the pci address
	for _, link := range links {
		if link.Attrs().HardwareAddr.String() == pfRawConfig.PfGUID {
			return networkHelper.GetPciAddressFromInterfaceName(link.Attrs().Name)
		}
	}
	return "", fmt.Errorf("no matching link found for pf guid: %s", pfRawConfig.PfGUID)
}
