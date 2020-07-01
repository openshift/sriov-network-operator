package discovery

import (
	"context"
	"os"
	"strconv"

	sriovv1 "github.com/openshift/sriov-network-operator/pkg/apis/sriovnetwork/v1"
	"github.com/openshift/sriov-network-operator/test/util/client"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	runtimeclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// Enabled indicates whether test discovery mode is enabled.
func Enabled() bool {
	discoveryMode, _ := strconv.ParseBool(os.Getenv("DISCOVERY_MODE"))
	return discoveryMode
}

// DiscoveredResources discovers resources needed by the tests in discovery mode
func DiscoveredResources(clients *client.ClientSet, sriovNodes []string, operatorNamespace string, filter func(sriovv1.SriovNetworkNodePolicy) bool) (preferredNode string, preferredResourceName string, preferredResourceCount int, err error) {
	policyList := sriovv1.SriovNetworkNodePolicyList{}
	err = clients.List(context.Background(), &policyList, runtimeclient.InNamespace(operatorNamespace))
	if err != nil {
		return
	}
	nodes, err := getSriovNodes(clients, sriovNodes)
	if err != nil {
		return
	}

	for _, policy := range policyList.Items {
		if !filter(policy) {
			continue
		}
		resourceName := policy.Spec.ResourceName
		for _, node := range nodes {
			quantity := node.Status.Allocatable[corev1.ResourceName("openshift.io/"+resourceName)]
			resourceCount64, _ := (&quantity).AsInt64()
			resourceCount := int(resourceCount64)
			if resourceCount > preferredResourceCount {
				preferredResourceCount = resourceCount
				preferredResourceName = resourceName
				preferredNode = node.ObjectMeta.Name
			}
		}
	}
	return
}

func getSriovNodes(clients *client.ClientSet, sriovNodeNames []string) ([]corev1.Node, error) {
	var nodes []corev1.Node
	for _, sriovNodeName := range sriovNodeNames {
		node, err := clients.Nodes().Get(context.Background(), sriovNodeName, metav1.GetOptions{})
		if err != nil {
			return nil, err
		}
		nodes = append(nodes, *node)
	}
	return nodes, nil
}

func containsNode(name string, sriovNodes []string) bool {
	for _, node := range sriovNodes {
		if node == name {
			return true
		}
	}
	return false
}
