package utils

import (
	"context"
	"fmt"
	"os"

	"github.com/golang/glog"

	configv1 "github.com/openshift/api/config/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	// default Infrastructure resource name for Openshift
	infraResourceName = "cluster"
)

func IsSingleNodeCluster(c client.Client) (bool, error) {
	if os.Getenv("CLUSTER_TYPE") == ClusterTypeOpenshift {
		return openshiftSingleNodeClusterStatus(c)
	}
	return k8sSingleNodeClusterStatus(c)
}

func k8sSingleNodeClusterStatus(c client.Client) (bool, error) {
	nodeList := &corev1.NodeList{}
	err := c.List(context.TODO(), nodeList)
	if err != nil {
		glog.Errorf("IsSingleNodeCluster(): Failed to list nodes: %v", err)
		return false, err
	}

	if len(nodeList.Items) == 1 {
		glog.Infof("IsSingleNodeCluster(): one node found in the cluster")
		return true, nil
	}
	return false, nil
}

func openshiftSingleNodeClusterStatus(c client.Client) (bool, error) {
	infra := &configv1.Infrastructure{}
	err := c.Get(context.TODO(), types.NamespacedName{Name: infraResourceName}, infra)
	if err != nil {
		return false, err
	}
	if infra == nil {
		return false, fmt.Errorf("getting resource Infrastructure (name: %s) succeeded but object was nil", infraResourceName)
	}
	return infra.Status.ControlPlaneTopology == configv1.SingleReplicaTopologyMode, nil
}
