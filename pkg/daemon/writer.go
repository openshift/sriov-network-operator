package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/pkg/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/log"

	sriovnetworkv1 "github.com/k8snetworkplumbingwg/sriov-network-operator/api/v1"
	snclientset "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/client/clientset/versioned"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/consts"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/helper"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/platforms"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/vars"
)

const (
	CheckpointFileName = "sno-initial-node-state.json"
	Unknown            = "Unknown"
)

type NodeStateStatusWriter struct {
	client             snclientset.Interface
	status             sriovnetworkv1.SriovNetworkNodeStateStatus
	OnHeartbeatFailure func()
	platformHelper     platforms.Interface
	hostHelper         helper.HostHelpersInterface
	eventRecorder      *EventRecorder
}

// NewNodeStateStatusWriter Create a new NodeStateStatusWriter
func NewNodeStateStatusWriter(c snclientset.Interface,
	f func(), er *EventRecorder,
	hostHelper helper.HostHelpersInterface,
	platformHelper platforms.Interface) *NodeStateStatusWriter {
	return &NodeStateStatusWriter{
		client:             c,
		OnHeartbeatFailure: f,
		eventRecorder:      er,
		hostHelper:         hostHelper,
		platformHelper:     platformHelper,
	}
}

// RunOnce initial the interface status for both baremetal and virtual environments
func (w *NodeStateStatusWriter) RunOnce() error {
	log.Log.V(0).Info("RunOnce()")
	msg := Message{}

	if vars.PlatformType == consts.VirtualOpenStack {
		ns, err := w.getCheckPointNodeState()
		if err != nil {
			return err
		}

		if ns == nil {
			err = w.platformHelper.CreateOpenstackDevicesInfo()
			if err != nil {
				return err
			}
		} else {
			w.platformHelper.CreateOpenstackDevicesInfoFromNodeStatus(ns)
		}
	}

	log.Log.V(0).Info("RunOnce(): first poll for nic status")
	if err := w.pollNicStatus(); err != nil {
		log.Log.Error(err, "RunOnce(): first poll failed")
	}

	ns, err := w.setNodeStateStatus(msg)
	if err != nil {
		log.Log.Error(err, "RunOnce(): first writing to node status failed")
	}
	return w.writeCheckpointFile(ns)
}

// Run reads from the writer channel and sets the interface status. It will
// return if the stop channel is closed. Intended to be run via a goroutine.
func (w *NodeStateStatusWriter) Run(stop <-chan struct{}, refresh <-chan Message, syncCh chan<- struct{}) error {
	log.Log.V(0).Info("Run(): start writer")
	msg := Message{}

	for {
		select {
		case <-stop:
			log.Log.V(0).Info("Run(): stop writer")
			return nil
		case msg = <-refresh:
			log.Log.V(0).Info("Run(): refresh trigger")
			if err := w.pollNicStatus(); err != nil {
				continue
			}
			_, err := w.setNodeStateStatus(msg)
			if err != nil {
				log.Log.Error(err, "Run() refresh: writing to node status failed")
			}
			syncCh <- struct{}{}
		case <-time.After(30 * time.Second):
			log.Log.V(2).Info("Run(): period refresh")
			if err := w.pollNicStatus(); err != nil {
				continue
			}
			w.setNodeStateStatus(msg)
		}
	}
}

func (w *NodeStateStatusWriter) pollNicStatus() error {
	log.Log.V(2).Info("pollNicStatus()")
	var iface []sriovnetworkv1.InterfaceExt
	var bridges sriovnetworkv1.Bridges
	var rdmaMode string
	var err error

	if vars.PlatformType == consts.VirtualOpenStack {
		iface, err = w.platformHelper.DiscoverSriovDevicesVirtual()
		if err != nil {
			return err
		}
	} else {
		iface, err = w.hostHelper.DiscoverSriovDevices(w.hostHelper)
		if err != nil {
			return err
		}
		if vars.ManageSoftwareBridges {
			bridges, err = w.hostHelper.DiscoverBridges()
			if err != nil {
				return err
			}
		}
	}

	rdmaMode, err = w.hostHelper.DiscoverRDMASubsystem()
	if err != nil {
		return err
	}

	w.status.Interfaces = iface
	w.status.Bridges = bridges
	w.status.System.RdmaMode = rdmaMode

	return nil
}

func (w *NodeStateStatusWriter) updateNodeStateStatusRetry(f func(*sriovnetworkv1.SriovNetworkNodeState)) (*sriovnetworkv1.SriovNetworkNodeState, error) {
	var nodeState *sriovnetworkv1.SriovNetworkNodeState
	var oldStatus, newStatus, lastError string

	err := retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		n, getErr := w.getNodeState()
		if getErr != nil {
			return getErr
		}
		oldStatus = n.Status.SyncStatus

		// Call the status modifier.
		f(n)

		newStatus = n.Status.SyncStatus
		lastError = n.Status.LastSyncError

		var err error
		nodeState, err = w.client.SriovnetworkV1().SriovNetworkNodeStates(vars.Namespace).UpdateStatus(context.Background(), n, metav1.UpdateOptions{})
		if err != nil {
			log.Log.V(0).Error(err, "updateNodeStateStatusRetry(): fail to update the node status")
		}
		return err
	})
	if err != nil {
		// may be conflict if max retries were hit
		return nil, fmt.Errorf("unable to update node %v: %v", nodeState, err)
	}

	w.recordStatusChangeEvent(oldStatus, newStatus, lastError)

	return nodeState, nil
}

func (w *NodeStateStatusWriter) setNodeStateStatus(msg Message) (*sriovnetworkv1.SriovNetworkNodeState, error) {
	nodeState, err := w.updateNodeStateStatusRetry(func(nodeState *sriovnetworkv1.SriovNetworkNodeState) {
		nodeState.Status.Interfaces = w.status.Interfaces
		nodeState.Status.Bridges = w.status.Bridges
		nodeState.Status.System = w.status.System
		if msg.lastSyncError != "" || msg.syncStatus == consts.SyncStatusSucceeded {
			// clear lastSyncError when sync Succeeded
			nodeState.Status.LastSyncError = msg.lastSyncError
		}
		nodeState.Status.SyncStatus = msg.syncStatus

		log.Log.V(0).Info("setNodeStateStatus(): status",
			"sync-status", nodeState.Status.SyncStatus,
			"last-sync-error", nodeState.Status.LastSyncError)
	})
	if err != nil {
		return nil, err
	}
	return nodeState, nil
}

// recordStatusChangeEvent sends event in case oldStatus differs from newStatus
func (w *NodeStateStatusWriter) recordStatusChangeEvent(oldStatus, newStatus, lastError string) {
	if oldStatus != newStatus {
		if oldStatus == "" {
			oldStatus = Unknown
		}
		if newStatus == "" {
			newStatus = Unknown
		}
		eventMsg := fmt.Sprintf("Status changed from: %s to: %s", oldStatus, newStatus)
		if lastError != "" {
			eventMsg = fmt.Sprintf("%s. Last Error: %s", eventMsg, lastError)
		}
		w.eventRecorder.SendEvent("SyncStatusChanged", eventMsg)
	}
}

// getNodeState queries the kube apiserver to get the SriovNetworkNodeState CR
func (w *NodeStateStatusWriter) getNodeState() (*sriovnetworkv1.SriovNetworkNodeState, error) {
	var lastErr error
	var n *sriovnetworkv1.SriovNetworkNodeState
	err := wait.PollImmediate(10*time.Second, 5*time.Minute, func() (bool, error) {
		n, lastErr = w.client.SriovnetworkV1().SriovNetworkNodeStates(vars.Namespace).Get(context.Background(), vars.NodeName, metav1.GetOptions{})
		if lastErr == nil {
			return true, nil
		}
		log.Log.Error(lastErr, "getNodeState(): Failed to fetch node state, close all connections and retry...", "name", vars.NodeName)
		// Use the Get() also as an client-go keepalive indicator for the TCP connection.
		w.OnHeartbeatFailure()
		return false, nil
	})
	if err != nil {
		if err == wait.ErrWaitTimeout {
			return nil, errors.Wrapf(lastErr, "Timed out trying to fetch node %s", vars.NodeName)
		}
		return nil, err
	}
	return n, nil
}

func (w *NodeStateStatusWriter) writeCheckpointFile(ns *sriovnetworkv1.SriovNetworkNodeState) error {
	configdir := filepath.Join(vars.Destdir, CheckpointFileName)
	file, err := os.OpenFile(configdir, os.O_RDWR|os.O_CREATE, 0644)
	if err != nil {
		return err
	}
	defer file.Close()
	log.Log.Info("writeCheckpointFile(): try to decode the checkpoint file")
	if err = json.NewDecoder(file).Decode(&sriovnetworkv1.InitialState); err != nil {
		log.Log.V(2).Error(err, "writeCheckpointFile(): fail to decode, writing new file instead")
		log.Log.Info("writeCheckpointFile(): write checkpoint file")
		if err = file.Truncate(0); err != nil {
			return err
		}
		if _, err = file.Seek(0, 0); err != nil {
			return err
		}
		if err = json.NewEncoder(file).Encode(*ns); err != nil {
			return err
		}
		sriovnetworkv1.InitialState = *ns
	}
	return nil
}

func (w *NodeStateStatusWriter) getCheckPointNodeState() (*sriovnetworkv1.SriovNetworkNodeState, error) {
	log.Log.Info("getCheckPointNodeState()")
	configdir := filepath.Join(vars.Destdir, CheckpointFileName)
	file, err := os.OpenFile(configdir, os.O_RDONLY, 0644)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer file.Close()
	if err = json.NewDecoder(file).Decode(&sriovnetworkv1.InitialState); err != nil {
		return nil, err
	}

	return &sriovnetworkv1.InitialState, nil
}
