package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/golang/glog"
	"github.com/openshift/sriov-network-operator/pkg/drain"
	"github.com/openshift/sriov-network-operator/pkg/utils"
	"github.com/pkg/errors"
	"golang.org/x/time/rate"
	"io/ioutil"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/util/retry"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	sninformer "github.com/openshift/sriov-network-operator/pkg/client/informers/externalversions"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"

	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"

	sriovnetworkv1 "github.com/openshift/sriov-network-operator/pkg/apis/sriovnetwork/v1"
	snclientset "github.com/openshift/sriov-network-operator/pkg/client/clientset/versioned"
)

const (
	CheckpointFileName = "sno-initial-node-state.json"
)

type NetworkDaemon struct {
	// name is the node name.
	name      string
	namespace string

	client snclientset.Interface

	// kubeClient allows interaction with Kubernetes, including the node we are running on.
	kubeClient *kubernetes.Clientset

	config *rest.Config

	nodeState *sriovnetworkv1.SriovNetworkNodeState

	nodeStateStatus *sriovnetworkv1.SriovNetworkNodeStateStatus

	LoadedPlugins map[string]VendorPlugin

	// channel used to ensure all spawned goroutines exit when we exit.
	stopCh <-chan struct{}

	mu *sync.Mutex

	workqueue workqueue.RateLimitingInterface

	drainManager *drain.DrainManager

	OnHeartbeatFailure func()
}

func NewNetworkDaemon(
	nodeName string,
	client snclientset.Interface,
	kubeClient *kubernetes.Clientset,
	drainManager *drain.DrainManager,
	f func(),
	stopCh <-chan struct{},
) *NetworkDaemon {

	destdir := os.Getenv("DEST_DIR")
	if destdir == "" {
		destdir = "/host/etc"
	}

	networkDaemon := &NetworkDaemon{
		name:               nodeName,
		client:             client,
		kubeClient:         kubeClient,
		stopCh:             stopCh,
		OnHeartbeatFailure: f,
		drainManager:       drainManager,
		nodeState:          &sriovnetworkv1.SriovNetworkNodeState{},
		nodeStateStatus:    &sriovnetworkv1.SriovNetworkNodeStateStatus{},
		workqueue: workqueue.NewNamedRateLimitingQueue(workqueue.NewMaxOfRateLimiter(
			&workqueue.BucketRateLimiter{Limiter: rate.NewLimiter(rate.Limit(updateDelay), 1)},
			workqueue.NewItemExponentialFailureRateLimiter(1*time.Second, maxUpdateBackoff)), "SriovNetworkNodeState"),
	}

	// Update the status once
	networkDaemon.UpdateNodeStateStatus()
	networkDaemon.writeCheckpointFile(destdir)

	return networkDaemon
}

// Run the config daemon
func (nd *NetworkDaemon) Run(errCh chan<- error) {
	glog.V(0).Info("Run(): start network daemon")
	// Only watch own SriovNetworkNodeState CR
	defer utilruntime.HandleCrash()
	defer nd.workqueue.ShutDown()

	tryEnableRdma()
	if err := tryCreateUdevRule(); err != nil {
		errCh <- fmt.Errorf("failed to create udev rules: %v", err)
		return
	}
	// Run the status writer
	go nd.RunPeriodicStatusWriter()

	var timeout int64 = 5
	nd.mu = &sync.Mutex{}
	informerFactory := sninformer.NewFilteredSharedInformerFactory(nd.client,
		time.Second*15,
		namespace,
		func(lo *metav1.ListOptions) {
			lo.FieldSelector = "metadata.name=" + nd.name
			lo.TimeoutSeconds = &timeout
		},
	)

	informer := informerFactory.Sriovnetwork().V1().SriovNetworkNodeStates().Informer()
	informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: nd.enqueueSriovNetworkNodeState,
		UpdateFunc: func(old, new interface{}) {
			nd.enqueueSriovNetworkNodeState(new)
		},
	})

	go informer.Run(nd.stopCh)
	if ok := cache.WaitForCacheSync(nd.stopCh, informer.HasSynced); !ok {
		errCh <- fmt.Errorf("failed to wait for SriovNetworkNodeStates cache to sync")
		return
	}

	glog.Info("Starting network workers")
	// Launch one workers to process
	wait.Until(nd.runWorker, time.Second, nd.stopCh)
}

func (nd *NetworkDaemon) enqueueSriovNetworkNodeState(obj interface{}) {
	var ns *sriovnetworkv1.SriovNetworkNodeState
	var ok bool
	if ns, ok = obj.(*sriovnetworkv1.SriovNetworkNodeState); !ok {
		utilruntime.HandleError(fmt.Errorf("expected SriovNetworkNodeState but got %#v", obj))
		return
	}
	key := ns.GetGeneration()
	nd.workqueue.Add(key)
}

func (nd *NetworkDaemon) runWorker() {
	for nd.processNextWorkItem() {
	}
}

func (nd *NetworkDaemon) processNextWorkItem() bool {
	glog.V(2).Infof("worker queue size: %d", nd.workqueue.Len())
	obj, shutdown := nd.workqueue.Get()
	glog.V(2).Infof("get item: %d", obj.(int64))
	if shutdown {
		return false
	}

	// We wrap this block in a func so we can defer c.workqueue.Done.
	err := func(obj interface{}) error {
		// We call Done here so the workqueue knows we have finished
		// processing this item.
		defer nd.workqueue.Done(obj)
		var key int64
		var ok bool
		if key, ok = obj.(int64); !ok {
			// As the item in the workqueue is actually invalid, we call
			// Forget here.
			nd.workqueue.Forget(obj)
			utilruntime.HandleError(fmt.Errorf("expected workItem in workqueue but got %#v", obj))
			return nil
		}
		var err error

		err = nd.nodeStateSyncHandler(key)
		if err != nil {
			nd.nodeStateStatus.SyncStatus = "Failed"
			nd.nodeStateStatus.LastSyncError = err.Error()
			nd.UpdateNodeStateStatus()

			nd.workqueue.AddRateLimited(key)
			return fmt.Errorf("error syncing: %s, requeuing", err.Error())
		}
		// Finally, if no error occurs we Forget this item so it does not
		// get queued again until another change happens.
		nd.workqueue.Forget(obj)
		glog.Infof("Successfully synced")
		return nil
	}(obj)

	if err != nil {
		utilruntime.HandleError(err)
	}

	return true
}

func (nd *NetworkDaemon) loadVendorPlugins(ns *sriovnetworkv1.SriovNetworkNodeState) error {
	pl := registerPlugins(ns)
	pl = append(pl, GenericPlugin)
	nd.LoadedPlugins = make(map[string]VendorPlugin)

	for _, pn := range pl {
		filePath := filepath.Join(pluginsPath, "network", pn+".so")
		glog.Infof("loadVendorPlugins(): try to load plugin %s", pn)
		p, err := loadPlugin(filePath)
		if err != nil {
			glog.Errorf("loadVendorPlugins(): fail to load plugin %s: %v", filePath, err)
			return err
		}
		nd.LoadedPlugins[p.Name()] = p
	}
	return nil
}

func (nd *NetworkDaemon) nodeStateSyncHandler(generation int64) error {
	var err, lastErr error
	glog.V(0).Infof("nodeStateSyncHandler(): new generation is %d", generation)
	// Get the latest NodeState
	var latestState *sriovnetworkv1.SriovNetworkNodeState
	err = wait.PollImmediate(10*time.Second, 1*time.Minute, func() (bool, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		latestState, lastErr = nd.client.SriovnetworkV1().SriovNetworkNodeStates(namespace).Get(ctx, nd.name, metav1.GetOptions{})
		if lastErr == nil {
			return true, nil
		}
		glog.Warningf("nodeStateSyncHandler(): Failed to fetch node state %s (%v); retrying...", nd.name, lastErr)
		return false, nil
	})
	if err != nil {
		if err == wait.ErrWaitTimeout {
			return fmt.Errorf("nodeStateSyncHandler(): Timed out trying to fetch the latest node state: %v", lastErr)
		}
		return fmt.Errorf("%v: %v", err, lastErr)
	}

	latest := latestState.GetGeneration()
	if latest != generation {
		glog.Infof("nodeStateSyncHandler(): the latest generation is %d, skip", latest)
		return nil
	}

	if nd.nodeState.GetGeneration() == latest {
		glog.V(0).Infof("nodeStateSyncHandler(): Interface not changed")
		return nil
	}

	nd.nodeStateStatus.SyncStatus = "InProgress"
	nd.nodeStateStatus.LastSyncError = ""
	nd.UpdateNodeStateStatus()

	// load plugins if has not loaded
	if len(nd.LoadedPlugins) == 0 {
		err = nd.loadVendorPlugins(latestState)
		if err != nil {
			glog.Errorf("nodeStateSyncHandler(): failed to load vendor plugin: %v", err)
			return err
		}
	}

	reqReboot := false
	reqDrain := false
	for k, p := range nd.LoadedPlugins {
		d, r := false, false
		if nd.nodeState.GetName() == "" {
			d, r, err = p.OnNodeStateAdd(latestState)
		} else {
			d, r, err = p.OnNodeStateChange(nd.nodeState, latestState)
		}
		if err != nil {
			glog.Errorf("nodeStateSyncHandler(): plugin %s error: %v", k, err)
			return err
		}
		reqDrain = reqDrain || d
		reqReboot = reqReboot || r
	}
	glog.V(0).Infof("nodeStateSyncHandler(): reqDrain %v, reqReboot %v", reqDrain, reqReboot)

	if reqDrain {
		glog.Info("nodeStateSyncHandler(): drain node")
		if err := nd.drainManager.DrainNode(); err != nil {
			return err
		}
	}
	for k, p := range nd.LoadedPlugins {
		if k != GenericPlugin {
			err := p.Apply()
			if err != nil {
				glog.Errorf("nodeStateSyncHandler(): plugin %s fail to apply: %v", k, err)
				return err
			}
		}
	}

	if len(nd.LoadedPlugins) > 1 && !reqReboot {
		// Apply generic_plugin last
		err = nd.LoadedPlugins[GenericPlugin].Apply()
		if err != nil {
			glog.Errorf("nodeStateSyncHandler(): generic_plugin fail to apply: %v", err)
			return err
		}
	}

	if reqReboot {
		glog.Info("nodeStateSyncHandler(): reboot node")
		rebootNode()
		return nil
	}

	// restart device plugin pod
	if reqDrain || (latestState.Spec.DpConfigVersion != nd.nodeState.Spec.DpConfigVersion) {
		glog.Info("nodeStateSyncHandler(): restart device plugin pod")
		if err := restartDevicePluginPod(nd.name, nd.kubeClient, nd.mu, nd.stopCh); err != nil {
			glog.Errorf("nodeStateSyncHandler(): fail to restart device plugin pod: %v", err)
			return err
		}
	}

	err = nd.drainManager.AnnotateNode()
	if err != nil {
		glog.Errorf("nodeStateSyncHandler(): failed to annotate node: %v", err)
		return err
	}

	glog.Info("nodeStateSyncHandler(): sync succeeded")
	nd.nodeState = latestState.DeepCopy()
	nd.nodeStateStatus.SyncStatus = "Succeeded"
	nd.nodeStateStatus.LastSyncError = ""
	nd.UpdateNodeStateStatus()

	return nil
}

func (nd *NetworkDaemon) UpdateNodeStateStatus() {
	glog.V(0).Infof("UpdateNodeStateStatus()")
	if err := nd.pollNetworkNicStatus(); err != nil {
		glog.Errorf("RunStatusWriter(): network nics status poll failed: %v", err)
	}
	nd.updateNodeStateStatusRetry()
}

// Run reads from the writer channel and sets the interface status. It will
// return if the stop channel is closed. Intended to be run via a goroutine.
func (nd *NetworkDaemon) RunPeriodicStatusWriter() {
	glog.V(0).Infof("RunStatusWriter(): start network writer")
	for {
		select {
		case <-nd.stopCh:
			glog.V(0).Info("RunStatusWriter(): stop network writer")
			return
		case <-time.After(30 * time.Second):
			glog.V(2).Info("RunStatusWriter(): period refresh")
			if err := nd.pollNetworkNicStatus(); err != nil {
				glog.V(2).Infof("failed to run pollNetworkNicStatus: %v", err)
				continue
			}
			nd.updateNodeStateStatusRetry()
		}
	}
}

func (nd *NetworkDaemon) pollNetworkNicStatus() error {
	glog.V(2).Info("pollNetworkNicStatus()")
	iface, err := utils.DiscoverSriovNetworkDevices()
	if err != nil {
		return err
	}

	nd.nodeStateStatus.Interfaces = iface
	return nil
}

// getNetworkNodeState queries the kube apiserver to get the SriovNetworkNodeState CR
func (nd *NetworkDaemon) getNetworkNodeState() (*sriovnetworkv1.SriovNetworkNodeState, error) {
	glog.V(2).Info("getNetworkNodeState()")
	var lastErr error
	var n *sriovnetworkv1.SriovNetworkNodeState
	err := wait.PollImmediate(10*time.Second, 5*time.Minute, func() (bool, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		n, lastErr = nd.client.SriovnetworkV1().SriovNetworkNodeStates(namespace).Get(ctx, nd.name, metav1.GetOptions{})
		if lastErr == nil {
			return true, nil
		}

		glog.Infof("getNetworkNodeState(): Failed to fetch network node state %s (%v); close all connections and retry...", nd.name, lastErr)
		// Use the Get() also as an client-go keepalive indicator for the TCP connection.
		nd.OnHeartbeatFailure()
		return false, nil
	})
	if err != nil {
		if err == wait.ErrWaitTimeout {
			return nil, errors.Wrapf(lastErr, "Timed out trying to fetch node %s", nd.name)
		}
		return nil, err
	}
	return n, nil
}

func (nd *NetworkDaemon) UpdateNetworkNodeState(n *sriovnetworkv1.SriovNetworkNodeState) (*sriovnetworkv1.SriovNetworkNodeState, error) {
	glog.V(2).Info("UpdateNetworkNodeState()")
	var lastErr error

	err := wait.PollImmediate(10*time.Second, 5*time.Minute, func() (bool, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		n, lastErr = nd.client.SriovnetworkV1().SriovNetworkNodeStates(namespace).UpdateStatus(ctx, n, metav1.UpdateOptions{})
		if lastErr == nil {
			return true, nil
		}

		if k8serrors.IsConflict(lastErr) {
			return false, nil
		}

		glog.Infof("UpdateNetworkNodeState(): Failed to update network node state %s (%v); close all connections and retry...", nd.name, lastErr)
		// Use the Get() also as an client-go keepalive indicator for the TCP connection.
		nd.OnHeartbeatFailure()
		return false, nil
	})
	if err != nil {
		if err == wait.ErrWaitTimeout {
			return nil, errors.Wrapf(lastErr, "Timed out trying to update node %s", nd.name)
		}
		return nil, err
	}
	return n, lastErr
}

func (nd *NetworkDaemon) updateNodeStateStatusRetry() {
	glog.V(2).Infof("updateNodeStateStatusRetry()")
	err := retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		n, err := nd.getNetworkNodeState()
		if err != nil {
			glog.V(0).Infof("failed go get latest sriov network node state: %v", err)
			return err
		}

		n.Status.Interfaces = nd.nodeStateStatus.Interfaces
		if nd.nodeStateStatus.LastSyncError != "" || n.Status.SyncStatus == "Succeeded" {
			// clear lastSyncError when sync Succeeded
			n.Status.LastSyncError = nd.nodeStateStatus.LastSyncError
		}

		n.Status.SyncStatus = nd.nodeStateStatus.SyncStatus

		glog.V(0).Infof("updateNodeStateStatusRetry(): updating syncStatus: %s, lastSyncError: %s", n.Status.SyncStatus, n.Status.LastSyncError)
		n, err = nd.UpdateNetworkNodeState(n)
		if err != nil {
			glog.V(0).Infof("failed go update latest sriov network node state: %v", err)
		}
		glog.V(0).Infof("updateNodeStateStatusRetry(): updated syncStatus: %s, lastSyncError: %s", n.Status.SyncStatus, n.Status.LastSyncError)

		return err
	})
	if err != nil {
		// may be conflict if max retries were hit
		glog.V(0).Infof("Unable to update network node state: %v", err)
	}
}

func tryEnableRdma() (bool, error) {
	glog.V(2).Infof("tryEnableRdma()")
	var stdout, stderr bytes.Buffer

	cmd := exec.Command("/bin/bash", scriptsPath)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		glog.Errorf("tryEnableRdma(): fail to enable rdma %v: %v", err, cmd.Stderr)
		return false, err
	}
	glog.V(2).Infof("tryEnableRdma(): %v", cmd.Stdout)

	i, err := strconv.Atoi(strings.TrimSpace(stdout.String()))
	if err == nil {
		if i == 0 {
			glog.V(2).Infof("tryEnableRdma(): RDMA kernel modules loaded")
			return true, nil
		} else {
			glog.V(2).Infof("tryEnableRdma(): RDMA kernel modules not loaded")
			return false, nil
		}
	}
	return false, err
}

func tryCreateUdevRule() error {
	glog.V(2).Infof("tryCreateUdevRule()")
	filePath := "/host/etc/udev/rules.d/10-nm-unmanaged.rules"
	_, err := os.Stat(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			glog.V(2).Infof("tryCreateUdevRule(): file not existed, create file")
			_, err := os.Create(filePath)
			if err != nil {
				glog.Errorf("tryCreateUdevRule(): fail to create file: %v", err)
				return err
			}
		} else {
			return err
		}
	}
	content := fmt.Sprintf("ACTION==\"add|change\", ATTRS{device}==\"%s\", ENV{NM_UNMANAGED}=\"1\"\n", strings.Join(sriovnetworkv1.VfIds, "|"))
	err = ioutil.WriteFile(filePath, []byte(content), 0666)
	if err != nil {
		glog.Errorf("tryCreateUdevRule(): fail to write file: %v", err)
		return err
	}
	return nil
}

func rebootNode() {
	glog.Infof("rebootNode(): trigger node reboot")
	exit, err := utils.Chroot("/host")
	if err != nil {
		glog.Errorf("rebootNode(): %v", err)
	}
	defer exit()
	// creates a new transient systemd unit to reboot the system.
	// We explictily try to stop kubelet.service first, before anything else; this
	// way we ensure the rest of system stays running, because kubelet may need
	// to do "graceful" shutdown by e.g. de-registering with a load balancer.
	// However note we use `;` instead of `&&` so we keep rebooting even
	// if kubelet failed to shutdown - that way the machine will still eventually reboot
	// as systemd will time out the stop invocation.
	cmd := exec.Command("systemd-run", "--unit", "sriov-network-config-daemon-reboot",
		"--description", fmt.Sprintf("sriov-network-config-daemon reboot node"), "/bin/sh", "-c", "systemctl stop kubelet.service; reboot")

	if err := cmd.Run(); err != nil {
		glog.Errorf("failed to reboot node: %v", err)
	}
}

func (nd *NetworkDaemon) writeCheckpointFile(destDir string) error {
	ns := &sriovnetworkv1.SriovNetworkNodeState{Status: *nd.nodeStateStatus}
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
