package daemon

import (
	"fmt"
	"time"

	"github.com/golang/glog"
	"github.com/pkg/errors"
	sriovnetworkv1 "github.com/pliurh/sriov-network-operator/pkg/apis/sriovnetwork/v1"
	snclientset "github.com/pliurh/sriov-network-operator/pkg/client/clientset/versioned"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/util/retry"
)

type NodeStateStatusWriter struct {
	client snclientset.Interface
	node   string
	status sriovnetworkv1.SriovNetworkNodeStateStatus
}

// NewNodeStateStatusWriter Create a new NodeStateStatusWriter
func NewNodeStateStatusWriter(c snclientset.Interface, n string) *NodeStateStatusWriter {
	return &NodeStateStatusWriter{
		client: c,
		node:   n,
	}
}

// Run reads from the writer channel and sets the interface status. It will
// return if the stop channel is closed. Intended to be run via a goroutine.
func (nm *NodeStateStatusWriter) Run(stop <-chan struct{}, refresh <-chan struct{}) {
	glog.V(0).Info("Run(): start writer")
	for {
		select {
		case <-stop:
			glog.V(0).Info("Run(): stop writer")
			return
		case <-refresh:
			if err := pollNicStatus(nm); err != nil {
				continue
			}
			setNodeStateStatus(nm.client, nm.node, nm.status)
		case <-time.After(30 * time.Second):
			if err := pollNicStatus(nm); err != nil {
				continue
			}
			setNodeStateStatus(nm.client, nm.node, nm.status)
		}
	}
}

func pollNicStatus(nm *NodeStateStatusWriter) error {
	glog.V(2).Info("pollNicStatus()")
	iface, err := DiscoverSriovDevices()
	if err != nil {
		return err
	}
	nm.status.Interfaces = iface

	return nil
}

func updateNodeStateStatusRetry(client snclientset.Interface, nodeName string, f func(*sriovnetworkv1.SriovNetworkNodeState)) (*sriovnetworkv1.SriovNetworkNodeState, error) {
	var nodeState *sriovnetworkv1.SriovNetworkNodeState
	err := retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		n, getErr := getNodeState(client, nodeName)
		if getErr != nil {
			return getErr
		}

		// Call the status modifier.
		f(n)

		var err error
		nodeState, err = client.SriovnetworkV1().SriovNetworkNodeStates(namespace).UpdateStatus(n)
		return err
	})
	if err != nil {
		// may be conflict if max retries were hit
		return nil, fmt.Errorf("Unable to update node %q: %v", nodeState, err)
	}

	return nodeState, nil
}

func setNodeStateStatus(client snclientset.Interface, nodeName string, status sriovnetworkv1.SriovNetworkNodeStateStatus) (*sriovnetworkv1.SriovNetworkNodeState, error) {
	nodeState, err := updateNodeStateStatusRetry(client, nodeName, func(nodeState *sriovnetworkv1.SriovNetworkNodeState) {
		nodeState.Status = status
	})
	return nodeState, err
}

// getNodeState queries the kube apiserver to get the SriovNetworkNodeState CR
func getNodeState(client snclientset.Interface, nodeName string) (*sriovnetworkv1.SriovNetworkNodeState, error) {
	var lastErr error
	var n *sriovnetworkv1.SriovNetworkNodeState
	err := wait.PollImmediate(10*time.Second, 5*time.Minute, func() (bool, error) {
		n, lastErr = client.SriovnetworkV1().SriovNetworkNodeStates(namespace).Get(nodeName, metav1.GetOptions{})
		if lastErr == nil {
			return true, nil
		}
		glog.Warningf("Failed to fetch node %s (%v); retrying...", nodeName, lastErr)
		return false, nil
	})
	if err != nil {
		if err == wait.ErrWaitTimeout {
			return nil, errors.Wrapf(lastErr, "Timed out trying to fetch node %s", nodeName)
		}
		return nil, err
	}
	return n, nil
}
