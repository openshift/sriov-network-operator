package cluster

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/utils/pointer"
	runtimeclient "sigs.k8s.io/controller-runtime/pkg/client"

	sriovv1 "github.com/k8snetworkplumbingwg/sriov-network-operator/api/v1"
	testclient "github.com/k8snetworkplumbingwg/sriov-network-operator/test/util/client"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/test/util/nodes"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/test/util/pod"
)

// EnabledNodes provides info on sriov enabled nodes of the cluster.
type EnabledNodes struct {
	Nodes               []string
	States              map[string]sriovv1.SriovNetworkNodeState
	IsSecureBootEnabled map[string]bool
}

var (
	supportedPFDrivers = []string{"mlx5_core", "i40e", "ixgbe", "ice", "igb"}
	supportedVFDrivers = []string{"iavf", "vfio-pci", "mlx5_core", "igbvf"}
	mlxVendorID        = "15b3"
	intelVendorID      = "8086"
)

// Name of environment variable to filter which decives can be discovered by FindSriovDevices and FindOneSriovDevice.
// The filter is a regexp matched against node names and device name in the form <node_name>:<device_name>
//
// For example, given the following devices in the cluster:
//
// worker-0:eno1
// worker-0:eno2
// worker-1:eno1
// worker-1:eno2
// worker-1:ens1f0
// worker-1:ens1f1
//
// Values:
// - `.*:eno1` matches `worker-0:eno1,worker-1:eno1`
// - `worker-0:eno.*` matches `worker-0:eno1,worker-0:eno2`
// - `worker-0:eno1|worker-1:eno2` matches `worker-0:eno1,worker-1:eno2`
const NodeAndDeviceNameFilterEnvVar string = "SRIOV_NODE_AND_DEVICE_NAME_FILTER"

// DiscoverSriov retrieves Sriov related information of a given cluster.
func DiscoverSriov(clients *testclient.ClientSet, operatorNamespace string) (*EnabledNodes, error) {
	nodeStates, err := clients.SriovNetworkNodeStates(operatorNamespace).List(context.Background(), metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to retrieve note states %v", err)
	}

	res := &EnabledNodes{}
	res.States = make(map[string]sriovv1.SriovNetworkNodeState)
	res.Nodes = make([]string, 0)
	res.IsSecureBootEnabled = make(map[string]bool)

	ss, err := nodes.MatchingOptionalSelectorState(clients, nodeStates.Items)
	if err != nil {
		return nil, fmt.Errorf("failed to find matching node states %v", err)
	}

	err = sriovv1.InitNicIDMapFromConfigMap(kubernetes.NewForConfigOrDie(clients.Config), operatorNamespace)
	if err != nil {
		return nil, fmt.Errorf("failed to InitNicIdMap %v", err)
	}

	for _, state := range ss {
		isStable, err := stateStable(state)
		if err != nil {
			return nil, err
		}
		if !isStable {
			return nil, fmt.Errorf("sync status still in progress")
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

	for _, node := range res.Nodes {
		isSecureBootEnabled, err := GetNodeSecureBootState(clients, node, operatorNamespace)
		if err != nil {
			return nil, err
		}

		res.IsSecureBootEnabled[node] = isSecureBootEnabled
	}

	if len(res.Nodes) == 0 {
		return nil, fmt.Errorf("no sriov enabled node found")
	}
	return res, nil
}

// FindOneSriovDevice retrieves a valid sriov device for the given node, filtered by `SRIOV_NODE_AND_DEVICE_NAME_FILTER` environment variable.
func (n *EnabledNodes) FindOneSriovDevice(node string) (*sriovv1.InterfaceExt, error) {
	ret, err := n.FindSriovDevices(node)
	if err != nil {
		return nil, err
	}

	if len(ret) == 0 {
		return nil, fmt.Errorf("unable to find sriov devices in node %s", node)
	}

	return ret[0], nil
}

// FindSriovDevices retrieves all valid sriov devices for the given node, filtered by `SRIOV_NODE_AND_DEVICE_NAME_FILTER` environment variable.
func (n *EnabledNodes) FindSriovDevices(node string) ([]*sriovv1.InterfaceExt, error) {
	devices, err := n.FindSriovDevicesIgnoreFilters(node)
	if err != nil {
		return nil, err
	}

	sriovDeviceNameFilter, ok := os.LookupEnv(NodeAndDeviceNameFilterEnvVar)
	if !ok {
		return devices, nil
	}

	filteredDevices := []*sriovv1.InterfaceExt{}
	for _, device := range devices {
		match, err := regexp.MatchString(sriovDeviceNameFilter, node+":"+device.Name)
		if err != nil {
			return nil, err
		}

		if match {
			filteredDevices = append(filteredDevices, device)
		}
	}

	return filteredDevices, nil
}

// FindSriovDevicesIgnoreFilters retrieves all valid sriov devices for the given node.
func (n *EnabledNodes) FindSriovDevicesIgnoreFilters(node string) ([]*sriovv1.InterfaceExt, error) {
	devices := []*sriovv1.InterfaceExt{}
	s, ok := n.States[node]
	if !ok {
		return nil, fmt.Errorf("node %s not found", node)
	}

	for i, itf := range s.Status.Interfaces {
		if IsPFDriverSupported(itf.Driver) && sriovv1.IsSupportedDevice(itf.DeviceID) {
			// Skip mlx interfaces if secure boot is enabled
			// TODO: remove this when mlx support secure boot/lockdown mode
			if itf.Vendor == mlxVendorID && n.IsSecureBootEnabled[node] {
				continue
			}

			// if the sriov is not enable in the kernel for intel nic the totalVF will be 0 so we skip the device
			// That is not the case for Mellanox devices that will report 0 until we configure the sriov interfaces
			// with the mstconfig package
			if itf.Vendor == intelVendorID && itf.TotalVfs == 0 {
				continue
			}

			devices = append(devices, &s.Status.Interfaces[i])
		}
	}
	return devices, nil
}

// FindOneSriovNodeAndDevice finds a cluster node with one SR-IOV devices respecting the `SRIOV_NODE_AND_DEVICE_NAME_FILTER` filter.
func (n *EnabledNodes) FindOneSriovNodeAndDevice() (string, *sriovv1.InterfaceExt, error) {
	errs := []error{}
	for _, node := range n.Nodes {
		devices, err := n.FindSriovDevices(node)
		if err != nil {
			errs = append(errs, err)
			continue
		}

		if len(devices) > 0 {
			return node, devices[0], nil
		}
	}

	return "", nil, fmt.Errorf("can't find any SR-IOV devices in cluster's nodes: %w", errors.Join(errs...))
}

// FindOneVfioSriovDevice retrieves a node with a valid sriov device for vfio
func (n *EnabledNodes) FindOneVfioSriovDevice() (string, sriovv1.InterfaceExt) {
	for _, node := range n.Nodes {
		for _, nic := range n.States[node].Status.Interfaces {
			if nic.Vendor == intelVendorID && sriovv1.IsSupportedModel(nic.Vendor, nic.DeviceID) && nic.TotalVfs != 0 {
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
		return nil, fmt.Errorf("node %s not found", node)
	}

	// return error here as mlx interfaces are not supported when secure boot is enabled
	// TODO: remove this when mlx support secure boot/lockdown mode
	if n.IsSecureBootEnabled[node] {
		return nil, fmt.Errorf("secure boot is enabled on the node mellanox cards are not supported")
	}

	for _, itf := range s.Status.Interfaces {
		if itf.Vendor == mlxVendorID && sriovv1.IsSupportedModel(itf.Vendor, itf.DeviceID) {
			return &itf, nil
		}
	}

	return nil, fmt.Errorf("unable to find a mellanox sriov devices in node %s", node)
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
		return false, fmt.Errorf("failed to fetch nodes state %v", err)
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
	nodes, err := clients.CoreV1Interface.Nodes().List(context.Background(), metav1.ListOptions{})
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
	nodes, err := clients.CoreV1Interface.Nodes().List(context.Background(), metav1.ListOptions{})
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

func GetNodeSecureBootState(clients *testclient.ClientSet, nodeName, namespace string) (bool, error) {
	podDefinition := pod.GetDefinition()
	podDefinition = pod.RedefineWithNodeSelector(podDefinition, nodeName)
	podDefinition = pod.RedefineAsPrivileged(podDefinition)
	podDefinition.Namespace = namespace

	volume := corev1.Volume{Name: "host", VolumeSource: corev1.VolumeSource{HostPath: &corev1.HostPathVolumeSource{Path: "/"}}}
	mount := corev1.VolumeMount{Name: "host", MountPath: "/host"}
	podDefinition = pod.RedefineWithMount(podDefinition, volume, mount)
	created, err := clients.Pods(namespace).Create(context.Background(), podDefinition, metav1.CreateOptions{})
	if err != nil {
		return false, err
	}

	defer func() {
		err = clients.Pods(namespace).Delete(context.Background(), created.Name, metav1.DeleteOptions{GracePeriodSeconds: pointer.Int64Ptr(0)})
		if err != nil {
			err = fmt.Errorf("failed to remove the check secure boot status pod for node %s: %v", nodeName, err)
		}
	}()

	var runningPod *corev1.Pod
	err = wait.PollImmediate(time.Second, 3*time.Minute, func() (bool, error) {
		runningPod, err = clients.Pods(namespace).Get(context.Background(), created.Name, metav1.GetOptions{})
		if err != nil {
			return false, err
		}

		if runningPod.Status.Phase != corev1.PodRunning {
			return false, nil
		}

		return true, nil
	})
	if err != nil {
		return false, err
	}

	stdout, _, err := pod.ExecCommand(clients, runningPod, "cat", "/host/sys/kernel/security/lockdown")

	if strings.Contains(stdout, "No such file or directory") {
		return false, nil
	}
	if err != nil {
		return false, err
	}

	return strings.Contains(stdout, "[integrity]") || strings.Contains(stdout, "[confidentiality]"), nil
}

func VirtualCluster() bool {
	if v, exist := os.LookupEnv("CLUSTER_HAS_EMULATED_PF"); exist && v != "" {
		return true
	}
	return false
}
