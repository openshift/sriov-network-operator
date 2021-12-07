package utils

import (
	"context"

	"github.com/golang/glog"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func IsSingleNodeCluster(c client.Client) (bool, error) {
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
