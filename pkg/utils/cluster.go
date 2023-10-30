package utils

import (
	"context"
	"fmt"
	"os"

	"sigs.k8s.io/controller-runtime/pkg/log"

	configv1 "github.com/openshift/api/config/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	// default Infrastructure resource name for Openshift
	infraResourceName        = "cluster"
	workerRoleName           = "worker"
	masterRoleName           = "master"
	workerNodeLabelKey       = "node-role.kubernetes.io/worker"
	masterNodeLabelKey       = "node-role.kubernetes.io/master"
	controlPlaneNodeLabelKey = "node-role.kubernetes.io/control-plane"
)

func getNodeRole(node corev1.Node) string {
	for k := range node.Labels {
		if k == workerNodeLabelKey {
			return workerRoleName
		} else if k == masterNodeLabelKey || k == controlPlaneNodeLabelKey {
			return masterRoleName
		}
	}
	return ""
}

func IsSingleNodeCluster(c client.Client) (bool, error) {
	if os.Getenv("CLUSTER_TYPE") == ClusterTypeOpenshift {
		topo, err := openshiftControlPlaneTopologyStatus(c)
		if err != nil {
			return false, err
		}
		if topo == configv1.SingleReplicaTopologyMode {
			return true, nil
		}
		return false, nil
	}
	return k8sSingleNodeClusterStatus(c)
}

// IsExternalControlPlaneCluster detects control plane location of the cluster.
// On OpenShift, the control plane topology is configured in configv1.Infrastucture struct.
// On kubernetes, it is determined by which node the sriov operator is scheduled on. If operator
// pod is schedule on worker node, it is considered as external control plane.
func IsExternalControlPlaneCluster(c client.Client) (bool, error) {
	if os.Getenv("CLUSTER_TYPE") == ClusterTypeOpenshift {
		topo, err := openshiftControlPlaneTopologyStatus(c)
		if err != nil {
			return false, err
		}
		if topo == "External" {
			return true, nil
		}
	} else if os.Getenv("CLUSTER_TYPE") == ClusterTypeKubernetes {
		role, err := operatorNodeRole(c)
		if err != nil {
			return false, err
		}
		if role == workerRoleName {
			return true, nil
		}
	}
	return false, nil
}

func k8sSingleNodeClusterStatus(c client.Client) (bool, error) {
	nodeList := &corev1.NodeList{}
	err := c.List(context.TODO(), nodeList)
	if err != nil {
		log.Log.Error(err, "k8sSingleNodeClusterStatus(): Failed to list nodes")
		return false, err
	}

	if len(nodeList.Items) == 1 {
		log.Log.Info("k8sSingleNodeClusterStatus(): one node found in the cluster")
		return true, nil
	}
	return false, nil
}

// operatorNodeRole returns role of the node where operator is scheduled on
func operatorNodeRole(c client.Client) (string, error) {
	node := corev1.Node{}
	err := c.Get(context.TODO(), types.NamespacedName{Name: os.Getenv("NODE_NAME")}, &node)
	if err != nil {
		log.Log.Error(err, "k8sIsExternalTopologyMode(): Failed to get node")
		return "", err
	}

	return getNodeRole(node), nil
}

func openshiftControlPlaneTopologyStatus(c client.Client) (configv1.TopologyMode, error) {
	infra := &configv1.Infrastructure{}
	err := c.Get(context.TODO(), types.NamespacedName{Name: infraResourceName}, infra)
	if err != nil {
		return "", fmt.Errorf("openshiftControlPlaneTopologyStatus(): Failed to get Infrastructure (name: %s): %v", infraResourceName, err)
	}
	return infra.Status.ControlPlaneTopology, nil
}
