package cluster

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	k8sfake "k8s.io/client-go/kubernetes/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	sriovv1 "github.com/k8snetworkplumbingwg/sriov-network-operator/api/v1"
	testclient "github.com/k8snetworkplumbingwg/sriov-network-operator/test/util/client"
)

func TestSriovStableWaitsForDevicePlugin(t *testing.T) {
	scheme := runtime.NewScheme()
	assert.NoError(t, sriovv1.AddToScheme(scheme))
	state := &sriovv1.SriovNetworkNodeState{
		ObjectMeta: metav1.ObjectMeta{Name: "worker", Namespace: "operator"},
		Spec:       sriovv1.SriovNetworkNodeStateSpec{Interfaces: sriovv1.Interfaces{{Name: "msnicp0"}}},
		Status: sriovv1.SriovNetworkNodeStateStatus{Conditions: []metav1.Condition{
			{Type: sriovv1.ConditionReady, Status: metav1.ConditionTrue},
		}},
	}
	plugin := &appsv1.DaemonSet{
		ObjectMeta: metav1.ObjectMeta{Name: "sriov-device-plugin", Namespace: "operator", Generation: 1},
		Status:     appsv1.DaemonSetStatus{ObservedGeneration: 1, DesiredNumberScheduled: 1, UpdatedNumberScheduled: 1},
	}
	configDaemon := plugin.DeepCopy()
	configDaemon.Name = "sriov-network-config-daemon"
	configDaemon.Status.NumberReady = 1
	kube := k8sfake.NewSimpleClientset(plugin, configDaemon)
	clients := &testclient.ClientSet{
		Client:          fake.NewClientBuilder().WithScheme(scheme).WithObjects(state).Build(),
		AppsV1Interface: kube.AppsV1(),
	}
	stable, err := SriovStable("operator", clients)
	assert.NoError(t, err)
	assert.False(t, stable)
	plugin.Status.NumberReady = 1
	_, err = kube.AppsV1().DaemonSets("operator").UpdateStatus(context.Background(), plugin, metav1.UpdateOptions{})
	assert.NoError(t, err)
	stable, err = SriovStable("operator", clients)
	assert.NoError(t, err)
	assert.True(t, stable)
	configDaemon.Status.NumberReady = 0
	_, err = kube.AppsV1().DaemonSets("operator").UpdateStatus(context.Background(), configDaemon, metav1.UpdateOptions{})
	assert.NoError(t, err)
	stable, err = SriovStable("operator", clients)
	assert.NoError(t, err)
	assert.False(t, stable, "a config-daemon rollout must finish before workloads start")
}

func TestStateStableRequiresCurrentGeneration(t *testing.T) {
	state := sriovv1.SriovNetworkNodeState{
		ObjectMeta: metav1.ObjectMeta{Generation: 2},
		Status: sriovv1.SriovNetworkNodeStateStatus{Conditions: []metav1.Condition{
			{Type: sriovv1.ConditionReady, Status: metav1.ConditionTrue, ObservedGeneration: 1},
		}},
	}
	stable, err := stateStable(state)
	assert.NoError(t, err)
	assert.False(t, stable)
	state.Status.Conditions[0].ObservedGeneration = 2
	stable, err = stateStable(state)
	assert.NoError(t, err)
	assert.True(t, stable)
	state.Status.Conditions = nil
	state.Status.SyncStatus = "Succeeded"
	stable, err = stateStable(state)
	assert.NoError(t, err)
	assert.False(t, stable, "syncStatus alone must not replace readiness conditions")
}

func init() {
	sriovv1.InitNicIDMapFromList([]string{
		"8086 158b 154c",
		"8086 10c9 10ca",
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

func TestNativeDeviceFilterAndSwitchdevDriverList(t *testing.T) {
	t.Setenv(NodeAndDeviceNameFilterEnvVar, "^worker:sriovtest0$")
	n := &EnabledNodes{Nodes: []string{"worker"}, States: map[string]sriovv1.SriovNetworkNodeState{"worker": {
		Status: sriovv1.SriovNetworkNodeStateStatus{Interfaces: sriovv1.InterfaceExts{
			{Name: "msnicp0", Driver: "mock_smartnic_pf", Vendor: "8086", DeviceID: "10c9", TotalVfs: 7},
			{Name: "eth1", Driver: "igb", Vendor: "8086", DeviceID: "10c9", TotalVfs: 7},
			{Name: "sriovtest0", Driver: "igb", Vendor: "8086", DeviceID: "10c9", TotalVfs: 7},
		}},
	}}}
	_, devices, err := n.FindSriovDevicesAndNode()
	assert.NoError(t, err)
	assert.Equal(t, []string{"sriovtest0"}, extractNames(devices))
	assert.Equal(t, VirtualCluster(), IsPFDriverSupported("mock_smartnic_pf"))
	assert.Equal(t, VirtualCluster(), IsVFDriverSupported("mock_smartnic_vf"))
	if VirtualCluster() {
		assert.ElementsMatch(t, []string{"mlx5_core", "ice", "mock_smartnic_pf"}, supportedSwitchdevDrivers)
	} else {
		assert.ElementsMatch(t, []string{"mlx5_core", "ice"}, supportedSwitchdevDrivers)
	}
}

func TestDefaultRouteInterfaceMatchesExactDevice(t *testing.T) {
	routes := []string{"default via 192.0.2.1 dev eth10", "default via fe80::1 dev eno1 proto ra", "192.0.2.0/24 dev eth1"}
	assert.True(t, IsDefaultRouteInterface("eth10", routes))
	assert.True(t, IsDefaultRouteInterface("eno1", routes))
	assert.False(t, IsDefaultRouteInterface("eth1", routes))
	assert.False(t, IsDefaultRouteInterface("eth2", []string{"", "default"}))
}

func TestUnusedSriovLinkRequiresCarrierOnlyForTraffic(t *testing.T) {
	down := "2: eno1: <NO-CARRIER,BROADCAST,MULTICAST,UP> mtu 1500 state DOWN"
	up := "2: eno1: <BROADCAST,MULTICAST,UP,LOWER_UP> mtu 1500 state UP"
	assert.True(t, isUnusedSriovLink(down, false), "legacy configuration tests may use disconnected PFs")
	assert.False(t, isUnusedSriovLink(down, true), "switchdev traffic requires an active carrier")
	assert.True(t, isUnusedSriovLink(up, true))
	assert.False(t, isUnusedSriovLink(up+" master ovs-system", true), "the primary OVS port must stay excluded")
}
