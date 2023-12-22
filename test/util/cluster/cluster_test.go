package cluster

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"

	sriovv1 "github.com/k8snetworkplumbingwg/sriov-network-operator/api/v1"
)

func init() {
	sriovv1.InitNicIDMapFromList([]string{
		"8086 158b 154c",
		"15b3 1015 1016",
	})
}

func TestFindSriovDevices(t *testing.T) {
	nodes := &EnabledNodes{
		Nodes: []string{"worker-0", "worker-1"},
		States: map[string]sriovv1.SriovNetworkNodeState{
			"worker-0": {
				Status: sriovv1.SriovNetworkNodeStateStatus{
					Interfaces: sriovv1.InterfaceExts{
						makeConnectX4LX("eno1np0", "0000:19:00.0"),
						makeConnectX4LX("eno1np1", "0000:19:00.1"),
						makeSfp28("ens3f0", "0000:d8:00.0"),
						makeSfp28("ens3f1", "0000:d8:00.0"),
					},
				},
			},
			"worker-1": {
				Status: sriovv1.SriovNetworkNodeStateStatus{
					Interfaces: sriovv1.InterfaceExts{
						makeConnectX4LX("eno1np0", "0000:19:00.0"),
						makeConnectX4LX("eno1np1", "0000:19:00.1"),
						makeSfp28("ens3f0", "0000:d8:00.0"),
						makeSfp28("ens3f1", "0000:d8:00.0"),
					},
				},
			},
		},
		IsSecureBootEnabled: map[string]bool{
			"worker-0": false,
			"worker-1": true,
		},
	}

	w0, err := nodes.FindSriovDevices("worker-0")
	assert.NoError(t, err)

	w0Names := extractNames(w0)
	assert.Contains(t, w0Names, "eno1np0")
	assert.Contains(t, w0Names, "eno1np1")
	assert.Contains(t, w0Names, "ens3f0")
	assert.Contains(t, w0Names, "ens3f1")

	w1, err := nodes.FindSriovDevices("worker-1")
	assert.NoError(t, err)

	w1Names := extractNames(w1)
	// worker-1 has SecureBoot enabled
	assert.NotContains(t, w1Names, "eno1np0")
	assert.NotContains(t, w1Names, "eno1np1")
	assert.Contains(t, w1Names, "ens3f0")
	assert.Contains(t, w1Names, "ens3f1")
}

func TestFindSriovDevicesFilteredByEnv(t *testing.T) {
	nodes := &EnabledNodes{
		Nodes: []string{"worker-0"},
		States: map[string]sriovv1.SriovNetworkNodeState{
			"worker-0": {
				Status: sriovv1.SriovNetworkNodeStateStatus{
					Interfaces: sriovv1.InterfaceExts{
						makeConnectX4LX("eno1np0", "0000:19:00.0"),
						makeConnectX4LX("eno1np1", "0000:19:00.1"),
					},
				},
			},
		},
		IsSecureBootEnabled: map[string]bool{
			"worker-0": false,
		},
	}

	originalValue := os.Getenv("SRIOV_NODE_AND_DEVICE_NAME_FILTER")
	defer os.Setenv("SRIOV_NODE_AND_DEVICE_NAME_FILTER", originalValue)

	os.Setenv("SRIOV_NODE_AND_DEVICE_NAME_FILTER", ".*:eno1np1")
	w0, err := nodes.FindSriovDevices("worker-0")
	assert.NoError(t, err)
	w0Names := extractNames(w0)
	assert.NotContains(t, w0Names, "eno1np0")
	assert.Contains(t, w0Names, "eno1np1")

	os.Setenv("SRIOV_NODE_AND_DEVICE_NAME_FILTER", ".*:eno1.*")
	w0, err = nodes.FindSriovDevices("worker-0")
	assert.NoError(t, err)
	w0Names = extractNames(w0)
	assert.Contains(t, w0Names, "eno1np0")
	assert.Contains(t, w0Names, "eno1np1")
}

func TestFindSriovDevicesFilteredByEnvOnDifferentNode(t *testing.T) {
	nodes := &EnabledNodes{
		Nodes: []string{"worker-0"},
		States: map[string]sriovv1.SriovNetworkNodeState{
			"worker-0": {
				Status: sriovv1.SriovNetworkNodeStateStatus{
					Interfaces: sriovv1.InterfaceExts{
						makeConnectX4LX("eno1", "0000:19:00.0"),
						makeConnectX4LX("eno2", "0000:19:00.1"),
					},
				},
			},
			"worker-1": {
				Status: sriovv1.SriovNetworkNodeStateStatus{
					Interfaces: sriovv1.InterfaceExts{
						makeConnectX4LX("eno1", "0000:19:00.0"),
						makeConnectX4LX("eno2", "0000:19:00.1"),
					},
				},
			},
		},
		IsSecureBootEnabled: map[string]bool{
			"worker-0": false,
		},
	}

	originalValue := os.Getenv("SRIOV_NODE_AND_DEVICE_NAME_FILTER")
	defer os.Setenv("SRIOV_NODE_AND_DEVICE_NAME_FILTER", originalValue)
	os.Setenv("SRIOV_NODE_AND_DEVICE_NAME_FILTER", "worker-0:eno1|worker-1:eno2")

	w0, err := nodes.FindSriovDevices("worker-0")
	assert.NoError(t, err)
	w0Names := extractNames(w0)
	assert.Contains(t, w0Names, "eno1")
	assert.NotContains(t, w0Names, "eno2")

	w1, err := nodes.FindSriovDevices("worker-1")
	assert.NoError(t, err)
	w1Names := extractNames(w1)
	assert.NotContains(t, w1Names, "eno1")
	assert.Contains(t, w1Names, "eno2")
}

func makeConnectX4LX(name, pci string) sriovv1.InterfaceExt {
	return sriovv1.InterfaceExt{
		Vendor: "15b3", DeviceID: "1015", Driver: "mlx5_core",
		Name: name, TotalVfs: 8, PciAddress: pci,
	}
}

func makeSfp28(name, pci string) sriovv1.InterfaceExt {
	return sriovv1.InterfaceExt{
		Vendor: "8086", DeviceID: "158b", Driver: "i40e",
		Name: name, TotalVfs: 64, PciAddress: pci,
	}
}

func extractNames(in []*sriovv1.InterfaceExt) []string {
	ret := make([]string, 0, len(in))
	for _, intf := range in {
		ret = append(ret, intf.Name)
	}
	return ret
}
