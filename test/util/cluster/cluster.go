package cluster

import (
	"context"
	"fmt"
	"io"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	sriovv1 "github.com/k8snetworkplumbingwg/sriov-network-operator/api/v1"
	testclient "github.com/k8snetworkplumbingwg/sriov-network-operator/test/util/client"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/test/util/nodes"
	runtimeclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// EnabledNodes provides info on sriov enabled nodes of the cluster.
type EnabledNodes struct {
	Nodes  []string
	States map[string]sriovv1.SriovNetworkNodeState
}

var (
	supportedPFDrivers = []string{"mlx5_core", "i40e", "ixgbe"}
	supportedVFDrivers = []string{"iavf", "vfio-pci", "mlx5_core"}
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
		isStable, err := stateStable(state)
		if err != nil {
			return nil, err
		}
		if !isStable {
			return nil, fmt.Errorf("Sync status still in progress")
		}

		node := state.Name
		for _, itf := range state.Status.Interfaces {
			if IsPFDriverSupported(itf.Driver) && sriovv1.IsSupportedDevice(itf.DeviceID) {
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
		if IsPFDriverSupported(itf.Driver) && sriovv1.IsSupportedDevice(itf.DeviceID) {
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
		if IsPFDriverSupported(itf.Driver) && sriovv1.IsSupportedDevice(itf.DeviceID) {
			devices = append(devices, &s.Status.Interfaces[i])
		}
	}
	return devices, nil
}

// FindOneVfioSriovDevice retrieves a node with a valid sriov device for vfio
func (n *EnabledNodes) FindOneVfioSriovDevice() (string, sriovv1.InterfaceExt) {
	for _, node := range n.Nodes {
		for _, nic := range n.States[node].Status.Interfaces {
			if nic.Vendor == "8086" {
				return node, nic
			}
		}
	}
	return "", sriovv1.InterfaceExt{}
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
		nodeReady, err := stateStable(state)
		if err != nil {
			return false, err
		}
		if !nodeReady {
			return false, nil
		}
	}
	return true, nil
}

func stateStable(state sriovv1.SriovNetworkNodeState) (bool, error) {
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

// IsSingleNode validates if the environment is single node cluster
// This is done by checking numer of nodes, it can later be substituted by an env variable if needed
func IsSingleNode(clients *testclient.ClientSet) (bool, error) {
	nodes, err := clients.Nodes().List(context.Background(), metav1.ListOptions{})
	if err != nil {
		return false, err
	}
	return len(nodes.Items) == 1, nil
}

func GetNodeDrainState(clients *testclient.ClientSet, operatorNamespace string) (bool, error) {
	sriovOperatorConfg := &sriovv1.SriovOperatorConfig{}
	err := clients.Get(context.TODO(), runtimeclient.ObjectKey{Name: "default", Namespace: operatorNamespace}, sriovOperatorConfg)
	return sriovOperatorConfg.Spec.DisableDrain, err
}

func SetDisableNodeDrainState(clients *testclient.ClientSet, operatorNamespace string, state bool) error {
	sriovOperatorConfg := &sriovv1.SriovOperatorConfig{}
	err := clients.Get(context.TODO(), runtimeclient.ObjectKey{Name: "default", Namespace: operatorNamespace}, sriovOperatorConfg)
	if err != nil {
		return err
	}
	sriovOperatorConfg.Spec.DisableDrain = state
	err = clients.Update(context.TODO(), sriovOperatorConfg)
	if err != nil {
		return err
	}
	return nil
}
