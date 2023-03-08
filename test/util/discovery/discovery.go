package discovery

import (
	"context"
	"os"
	"strconv"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	runtimeclient "sigs.k8s.io/controller-runtime/pkg/client"

	sriovv1 "github.com/k8snetworkplumbingwg/sriov-network-operator/api/v1"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/test/util/client"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/test/util/cluster"
)

// Enabled indicates whether test discovery mode is enabled.
func Enabled() bool {
	discoveryMode, _ := strconv.ParseBool(os.Getenv("DISCOVERY_MODE"))
	return discoveryMode
}

// DiscoveredResources discovers resources needed by the tests in discovery mode
func DiscoveredResources(clients *client.ClientSet, sriovInfos *cluster.EnabledNodes, operatorNamespace string, filterPolicy func(sriovv1.SriovNetworkNodePolicy) bool, filterDevices func(string, []*sriovv1.InterfaceExt) (*sriovv1.InterfaceExt, bool)) (preferredNode string, preferredResourceName string, preferredResourceCount int, preferredDevice *sriovv1.InterfaceExt, err error) {
	policyList := sriovv1.SriovNetworkNodePolicyList{}
	err = clients.List(context.Background(), &policyList, runtimeclient.InNamespace(operatorNamespace))
	if err != nil {
		return
	}
	nodes, err := getSriovNodes(clients, sriovInfos.Nodes)
	if err != nil {
		return
	}

	for _, policy := range policyList.Items {
		if !filterPolicy(policy) {
			continue
		}
		resourceName := policy.Spec.ResourceName
		for _, node := range nodes {
			sriovDeviceList, err := sriovInfos.FindSriovDevices(node.ObjectMeta.Name)
			if err != nil {
				continue
			}
			device, ok := filterDevices(node.ObjectMeta.Name, sriovDeviceList)
			if !ok {
				continue
			}
			quantity := node.Status.Allocatable[corev1.ResourceName("openshift.io/"+resourceName)]
			resourceCount64, _ := (&quantity).AsInt64()
			resourceCount := int(resourceCount64)
			if resourceCount > preferredResourceCount {
				preferredResourceCount = resourceCount
				preferredResourceName = resourceName
				preferredNode = node.ObjectMeta.Name
				preferredDevice = device
			}
		}
	}
	return
}

func getSriovNodes(clients *client.ClientSet, sriovNodeNames []string) ([]corev1.Node, error) {
	var nodes []corev1.Node
	for _, sriovNodeName := range sriovNodeNames {
		node, err := clients.CoreV1Interface.Nodes().Get(context.Background(), sriovNodeName, metav1.GetOptions{})
		if err != nil {
			return nil, err
		}
		nodes = append(nodes, *node)
	}
	return nodes, nil
}
