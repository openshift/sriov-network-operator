package cluster

import (
	"context"
	"fmt"
	"io"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	sriovv1 "github.com/openshift/sriov-network-operator/api/v1"
	testclient "github.com/openshift/sriov-network-operator/test/util/client"
	"github.com/openshift/sriov-network-operator/test/util/nodes"
)

// EnabledNodes provides info on sriov enabled nodes of the cluster.
type EnabledNodes struct {
	Nodes  []string
	States map[string]sriovv1.SriovNetworkNodeState
}

var (
	supportedPFDrivers = []string{"mlx5_core", "i40e", "ixgbe"}
	supportedVFDrivers = []string{"iavf", "vfio-pci", "mlx5_core"}
	supportedDevices   = []string{"1583", "158b", "10fb", "1015", "1017"}
)

// DiscoverSriov retrieves Sriov related information of a given cluster.
func DiscoverSriov(clients *testclient.ClientSet, operatorNamespace string) (*EnabledNodes, error) {
	nodeStates, err := clients.SriovNetworkNodeStates(operatorNamespace).List(context.Background(), metav1.ListOptions{})
	res := &EnabledNodes{}
	res.States = make(map[string]sriovv1.SriovNetworkNodeState)
	res.Nodes = make([]string, 0)
	if err != nil {
		return nil, fmt.Errorf("Failed to retrieve note states %v", err)
	}

	ss, err := nodes.MatchingOptionalSelectorState(clients, nodeStates.Items)
	if err != nil {
		return nil, fmt.Errorf("Failed to find matching node states %v", err)
	}

	for _, state := range ss {
		isStable, err := stateStable(state, clients, operatorNamespace)
		if err != nil {
			return nil, err
		}
		if !isStable {
			return nil, fmt.Errorf("Sync status still in progress")
		}

		node := state.Name
		for _, itf := range state.Status.Interfaces {
			if IsPFDriverSupported(itf.Driver) {
				res.Nodes = append(res.Nodes, node)
				res.States[node] = state
				break
			}
		}
	}

	if len(res.Nodes) == 0 {
		return nil, fmt.Errorf("No sriov enabled node found")
	}
	return res, nil
}

// FindOneSriovDevice retrieves a valid sriov device for the given node.
func (n *EnabledNodes) FindOneSriovDevice(node string) (*sriovv1.InterfaceExt, error) {
	s, ok := n.States[node]
	if !ok {
		return nil, fmt.Errorf("Node %s not found", node)
	}
	for _, itf := range s.Status.Interfaces {
		if IsPFDriverSupported(itf.Driver) && isDeviceSupported(itf.DeviceID) {
			return &itf, nil
		}
	}
	return nil, fmt.Errorf("Unable to find sriov devices in node %s", node)
}

// FindSriovDevices retrieves all valid sriov devices for the given node.
func (n *EnabledNodes) FindSriovDevices(node string) ([]*sriovv1.InterfaceExt, error) {
	devices := []*sriovv1.InterfaceExt{}
	s, ok := n.States[node]
	if !ok {
		return nil, fmt.Errorf("Node %s not found", node)
	}

	for i, itf := range s.Status.Interfaces {
		if IsPFDriverSupported(itf.Driver) {
			devices = append(devices, &s.Status.Interfaces[i])
		}
	}
	return devices, nil
}

// FindOneMellanoxSriovDevice retrieves a valid sriov device for the given node.
func (n *EnabledNodes) FindOneMellanoxSriovDevice(node string) (*sriovv1.InterfaceExt, error) {
	s, ok := n.States[node]
	if !ok {
		return nil, fmt.Errorf("Node %s not found", node)
	}
	for _, itf := range s.Status.Interfaces {
		if itf.Driver == "mlx5_core" {
			return &itf, nil
		}
	}

	return nil, fmt.Errorf("Unable to find a mellanox sriov devices in node %s", node)
}

// SriovStable tells if all the node states are in sync (and the cluster is ready for another round of tests)
func SriovStable(operatorNamespace string, clients *testclient.ClientSet) (bool, error) {
	nodeStates, err := clients.SriovNetworkNodeStates(operatorNamespace).List(context.Background(), metav1.ListOptions{})
	switch err {
	case io.ErrUnexpectedEOF:
		return false, err
	case nil:
		break
	default:
		return false, fmt.Errorf("Failed to fetch nodes state %v", err)
	}

	if len(nodeStates.Items) == 0 {
		return false, nil
	}
	for _, state := range nodeStates.Items {
		nodeReady, err := stateStable(state, clients, operatorNamespace)
		if err != nil {
			return false, err
		}
		if !nodeReady {
			return false, nil
		}
	}
	return true, nil
}

func stateStable(state sriovv1.SriovNetworkNodeState, clients *testclient.ClientSet, operatorNamespace string) (bool, error) {
	switch state.Status.SyncStatus {
	case "Succeeded":
		return true, nil
	// When the config daemon is restarted the status will be empty
	// This doesn't mean the config was applied
	case "":
		return false, nil
	}
	return false, nil
}

func IsPFDriverSupported(driver string) bool {
	for _, supportedDriver := range supportedPFDrivers {
		if strings.Contains(driver, supportedDriver) {
			return true
		}
	}
	return false
}

func IsVFDriverSupported(driver string) bool {
	for _, supportedDriver := range supportedVFDrivers {
		if strings.Contains(driver, supportedDriver) {
			return true
		}
	}
	return false
}

func isDeviceSupported(deviceID string) bool {
	for _, d := range supportedDevices {
		if deviceID == d {
			return true
		}
	}
	return false
}

func IsClusterStable(clients *testclient.ClientSet) (bool, error) {
	nodes, err := clients.Nodes().List(context.Background(), metav1.ListOptions{})
	if err != nil {
		return false, err
	}

	for _, node := range nodes.Items {
		if node.Spec.Unschedulable {
			return false, nil
		}
	}

	return true, nil
}
