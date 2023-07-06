package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/golang/glog"
	"github.com/pkg/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/util/retry"

	sriovnetworkv1 "github.com/k8snetworkplumbingwg/sriov-network-operator/api/v1"
	snclientset "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/client/clientset/versioned"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/utils"
)

const (
	CheckpointFileName = "sno-initial-node-state.json"
)

type NodeStateStatusWriter struct {
	client                 snclientset.Interface
	node                   string
	status                 sriovnetworkv1.SriovNetworkNodeStateStatus
	OnHeartbeatFailure     func()
	openStackDevicesInfo   utils.OSPDevicesInfo
	withUnsupportedDevices bool
}

// NewNodeStateStatusWriter Create a new NodeStateStatusWriter
func NewNodeStateStatusWriter(c snclientset.Interface, n string, f func(), devMode bool) *NodeStateStatusWriter {
	return &NodeStateStatusWriter{
		client:                 c,
		node:                   n,
		OnHeartbeatFailure:     f,
		withUnsupportedDevices: devMode,
	}
}

// RunOnce initial the interface status for both baremetal and virtual environments
func (w *NodeStateStatusWriter) RunOnce(destDir string, platformType utils.PlatformType) error {
	glog.V(0).Infof("RunOnce()")
	msg := Message{}

	if platformType == utils.VirtualOpenStack {
		ns, err := w.getCheckPointNodeState(destDir)
		if err != nil {
			return err
		}

		if ns == nil {
			metaData, networkData, err := utils.GetOpenstackData(true)
			if err != nil {
				glog.Errorf("RunOnce(): failed to read OpenStack data: %v", err)
			}

			w.openStackDevicesInfo, err = utils.CreateOpenstackDevicesInfo(metaData, networkData)
			if err != nil {
				return err
			}
		} else {
			w.openStackDevicesInfo = utils.CreateOpenstackDevicesInfoFromNodeStatus(ns)
		}
	}

	glog.V(0).Info("RunOnce(): first poll for nic status")
	if err := w.pollNicStatus(platformType); err != nil {
		glog.Errorf("RunOnce(): first poll failed: %v", err)
	}

	ns, err := w.setNodeStateStatus(msg)
	if err != nil {
		glog.Errorf("RunOnce(): first writing to node status failed: %v", err)
	}
	return w.writeCheckpointFile(ns, destDir)
}

// Run reads from the writer channel and sets the interface status. It will
// return if the stop channel is closed. Intended to be run via a goroutine.
func (w *NodeStateStatusWriter) Run(stop <-chan struct{}, refresh <-chan Message, syncCh chan<- struct{}, platformType utils.PlatformType) error {
	glog.V(0).Infof("Run(): start writer")
	msg := Message{}

	for {
		select {
		case <-stop:
			glog.V(0).Info("Run(): stop writer")
			return nil
		case msg = <-refresh:
			glog.V(0).Info("Run(): refresh trigger")
			if err := w.pollNicStatus(platformType); err != nil {
				continue
			}
			_, err := w.setNodeStateStatus(msg)
			if err != nil {
				glog.Errorf("Run() refresh: writing to node status failed: %v", err)
			}

			if msg.syncStatus == syncStatusSucceeded || msg.syncStatus == syncStatusFailed {
				syncCh <- struct{}{}
			}
		case <-time.After(30 * time.Second):
			glog.V(2).Info("Run(): period refresh")
			if err := w.pollNicStatus(platformType); err != nil {
				continue
			}
			w.setNodeStateStatus(msg)
		}
	}
}

func (w *NodeStateStatusWriter) pollNicStatus(platformType utils.PlatformType) error {
	glog.V(2).Info("pollNicStatus()")
	var iface []sriovnetworkv1.InterfaceExt
	var err error

	if platformType == utils.VirtualOpenStack {
		iface, err = utils.DiscoverSriovDevicesVirtual(w.openStackDevicesInfo)
	} else {
		iface, err = utils.DiscoverSriovDevices(w.withUnsupportedDevices)
	}
	if err != nil {
		return err
	}
	w.status.Interfaces = iface

	return nil
}

func (w *NodeStateStatusWriter) updateNodeStateStatusRetry(f func(*sriovnetworkv1.SriovNetworkNodeState)) (*sriovnetworkv1.SriovNetworkNodeState, error) {
	var nodeState *sriovnetworkv1.SriovNetworkNodeState
	err := retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		n, getErr := w.getNodeState()
		if getErr != nil {
			return getErr
		}

		// Call the status modifier.
		f(n)

		var err error
		nodeState, err = w.client.SriovnetworkV1().SriovNetworkNodeStates(namespace).UpdateStatus(context.Background(), n, metav1.UpdateOptions{})
		if err != nil {
			glog.V(0).Infof("updateNodeStateStatusRetry(): fail to update the node status: %v", err)
		}
		return err
	})
	if err != nil {
		// may be conflict if max retries were hit
		return nil, fmt.Errorf("unable to update node %v: %v", nodeState, err)
	}

	return nodeState, nil
}

func (w *NodeStateStatusWriter) setNodeStateStatus(msg Message) (*sriovnetworkv1.SriovNetworkNodeState, error) {
	nodeState, err := w.updateNodeStateStatusRetry(func(nodeState *sriovnetworkv1.SriovNetworkNodeState) {
		nodeState.Status.Interfaces = w.status.Interfaces
		if msg.lastSyncError != "" || msg.syncStatus == syncStatusSucceeded {
			// clear lastSyncError when sync Succeeded
			nodeState.Status.LastSyncError = msg.lastSyncError
		}
		nodeState.Status.SyncStatus = msg.syncStatus

		glog.V(0).Infof("setNodeStateStatus(): syncStatus: %s, lastSyncError: %s", nodeState.Status.SyncStatus, nodeState.Status.LastSyncError)
	})
	if err != nil {
		return nil, err
	}
	return nodeState, nil
}

// getNodeState queries the kube apiserver to get the SriovNetworkNodeState CR
func (w *NodeStateStatusWriter) getNodeState() (*sriovnetworkv1.SriovNetworkNodeState, error) {
	var lastErr error
	var n *sriovnetworkv1.SriovNetworkNodeState
	err := wait.PollImmediate(10*time.Second, 5*time.Minute, func() (bool, error) {
		n, lastErr = w.client.SriovnetworkV1().SriovNetworkNodeStates(namespace).Get(context.Background(), w.node, metav1.GetOptions{})
		if lastErr == nil {
			return true, nil
		}
		glog.Warningf("getNodeState(): Failed to fetch node state %s (%v); close all connections and retry...", w.node, lastErr)
		// Use the Get() also as an client-go keepalive indicator for the TCP connection.
		w.OnHeartbeatFailure()
		return false, nil
	})
	if err != nil {
		if err == wait.ErrWaitTimeout {
			return nil, errors.Wrapf(lastErr, "Timed out trying to fetch node %s", w.node)
		}
		return nil, err
	}
	return n, nil
}

func (w *NodeStateStatusWriter) writeCheckpointFile(ns *sriovnetworkv1.SriovNetworkNodeState, destDir string) error {
	configdir := filepath.Join(destDir, CheckpointFileName)
	file, err := os.OpenFile(configdir, os.O_RDWR|os.O_CREATE, 0644)
	if err != nil {
		return err
	}
	defer file.Close()
	glog.Info("writeCheckpointFile(): try to decode the checkpoint file")
	if err = json.NewDecoder(file).Decode(&utils.InitialState); err != nil {
		glog.V(2).Infof("writeCheckpointFile(): fail to decode: %v", err)
		glog.Info("writeCheckpointFile(): write checkpoint file")
		if err = file.Truncate(0); err != nil {
			return err
		}
		if _, err = file.Seek(0, 0); err != nil {
			return err
		}
		if err = json.NewEncoder(file).Encode(*ns); err != nil {
			return err
		}
		utils.InitialState = *ns
	}
	return nil
}

func (w *NodeStateStatusWriter) getCheckPointNodeState(destDir string) (*sriovnetworkv1.SriovNetworkNodeState, error) {
	glog.Infof("getCheckPointNodeState()")
	configdir := filepath.Join(destDir, CheckpointFileName)
	file, err := os.OpenFile(configdir, os.O_RDONLY, 0644)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer file.Close()
	if err = json.NewDecoder(file).Decode(&utils.InitialState); err != nil {
		return nil, err
	}

	return &utils.InitialState, nil
}
