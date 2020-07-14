package daemon

import (
	"context"
	"fmt"
	"path/filepath"
	"plugin"
	"reflect"
	"sync"
	"time"

	"github.com/golang/glog"
	"github.com/pkg/errors"
	"golang.org/x/time/rate"

	sninformer "github.com/openshift/sriov-network-operator/pkg/client/informers/externalversions"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"

	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/retry"
	"k8s.io/client-go/util/workqueue"

	sriovnetworkv1 "github.com/openshift/sriov-network-operator/pkg/apis/sriovnetwork/v1"
	snclientset "github.com/openshift/sriov-network-operator/pkg/client/clientset/versioned"
	"github.com/openshift/sriov-network-operator/pkg/drain"
	"github.com/openshift/sriov-network-operator/pkg/utils"
)

type AcceleratorDaemon struct {
	// name is the node name.
	name      string
	namespace string

	client snclientset.Interface

	// kubeClient allows interaction with Kubernetes, including the node we are running on.
	kubeClient *kubernetes.Clientset

	config *rest.Config

	nodeState *sriovnetworkv1.SriovAcceleratorNodeState

	nodeStateStatus *sriovnetworkv1.SriovAcceleratorNodeStateStatus

	LoadedPlugins map[string]AcceleratorVendorPlugin

	// channel used to ensure all spawned goroutines exit when we exit.
	stopCh <-chan struct{}

	mu *sync.Mutex

	workqueue workqueue.RateLimitingInterface

	drainManager *drain.DrainManager

	OnHeartbeatFailure func()
}

func NewAcceleratorDaemon(
	nodeName string,
	client snclientset.Interface,
	kubeClient *kubernetes.Clientset,
	drainManager *drain.DrainManager,
	f func(),
	stopCh <-chan struct{},
) *AcceleratorDaemon {

	acceleratorkDaemon := &AcceleratorDaemon{
		name:               nodeName,
		client:             client,
		kubeClient:         kubeClient,
		stopCh:             stopCh,
		OnHeartbeatFailure: f,
		drainManager:       drainManager,
		nodeState:          &sriovnetworkv1.SriovAcceleratorNodeState{},
		nodeStateStatus:    &sriovnetworkv1.SriovAcceleratorNodeStateStatus{},
		workqueue: workqueue.NewNamedRateLimitingQueue(workqueue.NewMaxOfRateLimiter(
			&workqueue.BucketRateLimiter{Limiter: rate.NewLimiter(rate.Limit(updateDelay), 1)},
			workqueue.NewItemExponentialFailureRateLimiter(1*time.Second, maxUpdateBackoff)), "SriovAcceleratorNodeState"),
	}

	// Update the status once
	acceleratorkDaemon.UpdateNodeStateStatus()

	return acceleratorkDaemon
}

type AcceleratorVendorPlugin interface {
	// Return the name of plugin
	Name() string
	// Return the SpecVersion followed by plugin
	Spec() string
	// Invoked when SriovAcceleratorNodeState CR is created, return if need dain and/or reboot node
	OnNodeStateAdd(state *sriovnetworkv1.SriovAcceleratorNodeState) (bool, bool, error)
	// Invoked when SriovAcceleratorNodeState CR is updated, return if need dain and/or reboot node
	OnNodeStateChange(old, new *sriovnetworkv1.SriovAcceleratorNodeState) (bool, bool, error)
	// Apply config change
	Apply() error
}

var acceleratorPluginMap = map[string]string{
	"1172": "intel_fec_accelerator_plugin",
}

const (
	AcceleratorSpecVersion   = "1.0"
	AcceleratorGenericPlugin = "generic_accelerator_plugin"
)

// Run the config daemon
func (ad *AcceleratorDaemon) Run(errCh chan<- error) {
	glog.V(0).Info("Run(): start accelerator daemon")
	// Only watch own SriovAcceleratorNodeState CR
	defer utilruntime.HandleCrash()
	defer ad.workqueue.ShutDown()

	// Run the status writer
	go ad.RunPeriodicStatusWriter()

	var timeout int64 = 5
	ad.mu = &sync.Mutex{}
	informerFactory := sninformer.NewFilteredSharedInformerFactory(ad.client,
		time.Second*15,
		namespace,
		func(lo *metav1.ListOptions) {
			lo.FieldSelector = "metadata.name=" + ad.name
			lo.TimeoutSeconds = &timeout
		},
	)

	informer := informerFactory.Sriovnetwork().V1().SriovAcceleratorNodeStates().Informer()
	informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: ad.enqueueSriovAcceleratorNodeState,
		UpdateFunc: func(old, new interface{}) {
			ad.enqueueSriovAcceleratorNodeState(new)
		},
	})

	go informer.Run(ad.stopCh)
	if ok := cache.WaitForCacheSync(ad.stopCh, informer.HasSynced); !ok {
		errCh <- fmt.Errorf("failed to wait for SriovAcceleratorNodeStates cache to sync")
		return
	}

	glog.Info("Starting accelerator workers")
	// Launch one workers to process
	wait.Until(ad.runWorker, time.Second, ad.stopCh)
}

func (ad *AcceleratorDaemon) enqueueSriovAcceleratorNodeState(obj interface{}) {
	var ns *sriovnetworkv1.SriovAcceleratorNodeState
	var ok bool
	if ns, ok = obj.(*sriovnetworkv1.SriovAcceleratorNodeState); !ok {
		utilruntime.HandleError(fmt.Errorf("expected SriovAcceleratorNodeState but got %#v", obj))
		return
	}
	key := ns.GetGeneration()
	ad.workqueue.Add(key)
}

func (ad *AcceleratorDaemon) runWorker() {
	for ad.processNextWorkItem() {
	}
}

func (ad *AcceleratorDaemon) processNextWorkItem() bool {
	glog.V(2).Infof("worker queue size: %d", ad.workqueue.Len())
	obj, shutdown := ad.workqueue.Get()
	glog.V(2).Infof("get item: %d", obj.(int64))
	if shutdown {
		return false
	}

	// We wrap this block in a func so we can defer c.workqueue.Done.
	err := func(obj interface{}) error {
		// We call Done here so the workqueue knows we have finished
		// processing this item.
		defer ad.workqueue.Done(obj)
		var key int64
		var ok bool
		if key, ok = obj.(int64); !ok {
			// As the item in the workqueue is actually invalid, we call
			// Forget here.
			ad.workqueue.Forget(obj)
			utilruntime.HandleError(fmt.Errorf("expected workItem in workqueue but got %#v", obj))
			return nil
		}
		var err error

		err = ad.nodeStateSyncHandler(key)
		if err != nil {
			ad.nodeStateStatus.SyncStatus = "Failed"
			ad.nodeStateStatus.LastSyncError = err.Error()
			ad.UpdateNodeStateStatus()

			ad.workqueue.AddRateLimited(key)
			return fmt.Errorf("error syncing: %s, requeuing", err.Error())
		}
		// Finally, if no error occurs we Forget this item so it does not
		// get queued again until another change happens.
		ad.workqueue.Forget(obj)
		glog.Infof("Successfully synced")
		return nil
	}(obj)

	if err != nil {
		utilruntime.HandleError(err)
	}

	return true
}

func registerAcceleratorPlugins(ns *sriovnetworkv1.SriovAcceleratorNodeState) []string {
	pluginNames := make(map[string]bool)
	for _, iface := range ns.Status.Accelerators {
		if val, ok := acceleratorPluginMap[iface.Vendor]; ok {
			pluginNames[val] = true
		}
	}
	rawList := reflect.ValueOf(pluginNames).MapKeys()
	glog.Infof("registerAcceleratorPlugins(): %v", rawList)
	nameList := make([]string, len(rawList))
	for i := 0; i < len(rawList); i++ {
		nameList[i] = rawList[i].String()
	}
	return nameList
}

// loadPlugin loads a single plugin from a file path
func loadAcceleratorPlugin(path string) (AcceleratorVendorPlugin, error) {
	glog.Infof("loadPlugin(): load plugin from %s", path)
	plug, err := plugin.Open(path)
	if err != nil {
		return nil, err
	}

	symbol, err := plug.Lookup("Plugin")
	if err != nil {
		return nil, err
	}

	// Cast the loaded symbol to the AcceleratorVendorPlugin
	p, ok := symbol.(AcceleratorVendorPlugin)
	if !ok {
		return nil, fmt.Errorf("Unable to load plugin")
	}

	// Check the spec to ensure we are supported version
	if p.Spec() != AcceleratorSpecVersion {
		return nil, fmt.Errorf("Spec mismatch")
	}

	return p, nil
}

func (ad *AcceleratorDaemon) loadVendorPlugins(ns *sriovnetworkv1.SriovAcceleratorNodeState) error {
	pl := registerAcceleratorPlugins(ns)
	pl = append(pl, AcceleratorGenericPlugin)
	ad.LoadedPlugins = make(map[string]AcceleratorVendorPlugin)

	for _, pn := range pl {
		filePath := filepath.Join(pluginsPath, "accelerator", pn+".so")
		glog.Infof("loadVendorPlugins(): try to load plugin %s", pn)
		p, err := loadAcceleratorPlugin(filePath)
		if err != nil {
			glog.Errorf("loadVendorPlugins(): fail to load plugin %s: %v", filePath, err)
			return err
		}
		ad.LoadedPlugins[p.Name()] = p
	}
	return nil
}

func (ad *AcceleratorDaemon) nodeStateSyncHandler(generation int64) error {
	var err, lastErr error
	glog.V(0).Infof("nodeStateSyncHandler(): new generation is %d", generation)
	// Get the latest NodeState
	var latestState *sriovnetworkv1.SriovAcceleratorNodeState
	err = wait.PollImmediate(10*time.Second, 1*time.Minute, func() (bool, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		latestState, lastErr = ad.client.SriovnetworkV1().SriovAcceleratorNodeStates(namespace).Get(ctx, ad.name, metav1.GetOptions{})
		if lastErr == nil {
			return true, nil
		}
		glog.Warningf("nodeStateSyncHandler(): Failed to fetch node state %s (%v); retrying...", ad.name, lastErr)
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

	if ad.nodeState.GetGeneration() == latest {
		glog.V(0).Infof("nodeStateSyncHandler(): Interface not changed")
		return nil
	}

	ad.nodeStateStatus.SyncStatus = "InProgress"
	ad.nodeStateStatus.LastSyncError = ""
	ad.UpdateNodeStateStatus()

	// load plugins if has not loaded
	if len(ad.LoadedPlugins) == 0 {
		err = ad.loadVendorPlugins(latestState)
		if err != nil {
			glog.Errorf("nodeStateSyncHandler(): failed to load vendor plugin: %v", err)
			return err
		}
	}

	reqReboot := false
	reqDrain := false
	for k, p := range ad.LoadedPlugins {
		d, r := false, false
		if ad.nodeState.GetName() == "" {
			d, r, err = p.OnNodeStateAdd(latestState)
		} else {
			d, r, err = p.OnNodeStateChange(ad.nodeState, latestState)
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
		if err := ad.drainManager.DrainNode("accelerator"); err != nil {
			return err
		}
	}
	for k, p := range ad.LoadedPlugins {
		if k != AcceleratorGenericPlugin {
			err := p.Apply()
			if err != nil {
				glog.Errorf("nodeStateSyncHandler(): plugin %s fail to apply: %v", k, err)
				return err
			}
		}
	}

	if len(ad.LoadedPlugins) > 1 && !reqReboot {
		// Apply generic_plugin last
		err = ad.LoadedPlugins[AcceleratorGenericPlugin].Apply()
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
	if reqDrain || (latestState.Spec.DpConfigVersion != ad.nodeState.Spec.DpConfigVersion) {
		glog.Info("nodeStateSyncHandler(): restart device plugin pod")
		if err := restartDevicePluginPod(ad.name, "sriov-accelerator-device-plugin", ad.kubeClient, ad.mu, ad.stopCh); err != nil {
			glog.Errorf("nodeStateSyncHandler(): fail to restart device plugin pod: %v", err)
			return err
		}
	}

	err = ad.drainManager.AnnotateNode("accelerator")
	if err != nil {
		glog.Errorf("nodeStateSyncHandler(): failed to annotate node: %v", err)
		return err
	}

	glog.Info("nodeStateSyncHandler(): sync succeeded")
	ad.nodeState = latestState.DeepCopy()
	ad.nodeStateStatus.SyncStatus = "Succeeded"
	ad.nodeStateStatus.LastSyncError = ""
	ad.UpdateNodeStateStatus()

	return nil
}

func (ad *AcceleratorDaemon) UpdateNodeStateStatus() {
	glog.V(0).Infof("UpdateNodeStateStatus()")
	if err := ad.pollAcceleratorNicStatus(); err != nil {
		glog.Errorf("RunStatusWriter(): accelerator nics status poll failed: %v", err)
	}
	ad.updateNodeStateStatusRetry()
}

// Run reads from the writer channel and sets the interface status. It will
// return if the stop channel is closed. Intended to be run via a goroutine.
func (ad *AcceleratorDaemon) RunPeriodicStatusWriter() {
	glog.V(0).Infof("RunStatusWriter(): start accelerator writer")
	for {
		select {
		case <-ad.stopCh:
			glog.V(0).Info("RunStatusWriter(): stop accelerator writer")
			return
		case <-time.After(30 * time.Second):
			glog.V(2).Info("RunStatusWriter(): period refresh")
			if err := ad.pollAcceleratorNicStatus(); err != nil {
				glog.V(2).Infof("failed to run pollAcceleratorNicStatus: %v", err)
				continue
			}
			ad.updateNodeStateStatusRetry()
		}
	}
}

func (ad *AcceleratorDaemon) pollAcceleratorNicStatus() error {
	glog.V(2).Info("pollAcceleratorNicStatus()")
	iface, err := utils.DiscoverSriovAcceleratorDevices()
	if err != nil {
		return err
	}

	ad.nodeStateStatus.Accelerators = iface
	return nil
}

// getAcceleratorNodeState queries the kube apiserver to get the SriovAcceleratorNodeState CR
func (ad *AcceleratorDaemon) getAcceleratorNodeState() (*sriovnetworkv1.SriovAcceleratorNodeState, error) {
	glog.V(2).Info("getAcceleratorNodeState()")
	var lastErr error
	var n *sriovnetworkv1.SriovAcceleratorNodeState
	err := wait.PollImmediate(10*time.Second, 5*time.Minute, func() (bool, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		n, lastErr = ad.client.SriovnetworkV1().SriovAcceleratorNodeStates(namespace).Get(ctx, ad.name, metav1.GetOptions{})
		if lastErr == nil {
			return true, nil
		}

		glog.Infof("getAcceleratorNodeState(): Failed to fetch accelerator node state %s (%v); close all connections and retry...", ad.name, lastErr)
		// Use the Get() also as an client-go keepalive indicator for the TCP connection.
		ad.OnHeartbeatFailure()
		return false, nil
	})
	if err != nil {
		if err == wait.ErrWaitTimeout {
			return nil, errors.Wrapf(lastErr, "Timed out trying to fetch node %s", ad.name)
		}
		return nil, err
	}
	return n, nil
}

func (ad *AcceleratorDaemon) UpdateAcceleratorNodeState(n *sriovnetworkv1.SriovAcceleratorNodeState) error {
	glog.V(2).Info("UpdateAcceleratorNodeState()")
	var lastErr error

	err := wait.PollImmediate(10*time.Second, 5*time.Minute, func() (bool, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, lastErr = ad.client.SriovnetworkV1().SriovAcceleratorNodeStates(namespace).UpdateStatus(ctx, n, metav1.UpdateOptions{})
		if lastErr == nil {
			return true, nil
		}

		if k8serrors.IsConflict(lastErr) {
			return true, nil
		}

		glog.Infof("UpdateAcceleratorNodeState(): Failed to update accelerator node state %s (%v); close all connections and retry...", ad.name, lastErr)
		// Use the Get() also as an client-go keepalive indicator for the TCP connection.
		ad.OnHeartbeatFailure()
		return false, nil
	})
	if err != nil {
		if err == wait.ErrWaitTimeout {
			return errors.Wrapf(lastErr, "Timed out trying to update node %s", ad.name)
		}
		return err
	}
	return lastErr
}

func (ad *AcceleratorDaemon) updateNodeStateStatusRetry() {
	glog.V(2).Infof("updateNodeStateStatusRetry()")
	err := retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		n, err := ad.getAcceleratorNodeState()
		if err != nil {
			glog.V(0).Infof("failed go get latest sriov accelerator node state: %v", err)
			return err
		}

		n.Status.Accelerators = ad.nodeStateStatus.Accelerators
		if ad.nodeStateStatus.LastSyncError != "" || n.Status.SyncStatus == "Succeeded" {
			// clear lastSyncError when sync Succeeded
			n.Status.LastSyncError = ad.nodeStateStatus.LastSyncError
		}

		n.Status.SyncStatus = ad.nodeStateStatus.SyncStatus

		glog.V(0).Infof("updateNodeStateStatusRetry(): updating syncStatus: %s, lastSyncError: %s", n.Status.SyncStatus, n.Status.LastSyncError)
		err = ad.UpdateAcceleratorNodeState(n)
		if err != nil {
			glog.V(0).Infof("failed go update latest sriov accelerator node state: %v", err)
		} else {
			glog.V(0).Infof("updateNodeStateStatusRetry(): updated syncStatus: %s, lastSyncError: %s", n.Status.SyncStatus, n.Status.LastSyncError)
		}

		return err
	})
	if err != nil {
		// may be conflict if max retries were hit
		glog.V(0).Infof("Unable to update accelerator node state: %v", err)
	}
}
