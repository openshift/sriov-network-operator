package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	mcfgv1 "github.com/openshift/machine-config-operator/pkg/apis/machineconfiguration.openshift.io/v1"
	daemonconsts "github.com/openshift/machine-config-operator/pkg/daemon/constants"
	mcfginformers "github.com/openshift/machine-config-operator/pkg/generated/informers/externalversions"
	"golang.org/x/time/rate"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/strategicpatch"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	listerv1 "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/leaderelection"
	"k8s.io/client-go/tools/leaderelection/resourcelock"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/kubectl/pkg/drain"
	"sigs.k8s.io/controller-runtime/pkg/log"

	sriovnetworkv1 "github.com/k8snetworkplumbingwg/sriov-network-operator/api/v1"
	snclientset "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/client/clientset/versioned"
	sninformer "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/client/informers/externalversions"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/consts"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/helper"
	snolog "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/log"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/platforms"
	plugin "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/plugins"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/systemd"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/vars"
)

const (
	// updateDelay is the baseline speed at which we react to changes.  We don't
	// need to react in milliseconds as any change would involve rebooting the node.
	updateDelay = 5 * time.Second
	// maxUpdateBackoff is the maximum time to react to a change as we back off
	// in the face of errors.
	maxUpdateBackoff = 60 * time.Second
)

type Message struct {
	syncStatus    string
	lastSyncError string
}

type Daemon struct {
	client snclientset.Interface
	// kubeClient allows interaction with Kubernetes, including the node we are running on.
	kubeClient kubernetes.Interface

	nodeState *sriovnetworkv1.SriovNetworkNodeState

	enabledPlugins map[string]plugin.VendorPlugin

	HostHelpers helper.HostHelpersInterface

	platformHelpers platforms.Interface

	// channel used by callbacks to signal Run() of an error
	exitCh chan<- error

	// channel used to ensure all spawned goroutines exit when we exit.
	stopCh <-chan struct{}

	syncCh <-chan struct{}

	refreshCh chan<- Message

	mu *sync.Mutex

	drainer *drain.Helper

	node *corev1.Node

	drainable bool

	disableDrain bool

	nodeLister listerv1.NodeLister

	workqueue workqueue.RateLimitingInterface

	mcpName string

	eventRecorder *EventRecorder
}

const (
	udevScriptsPath = "/bindata/scripts/load-udev.sh"
	annoKey         = "sriovnetwork.openshift.io/state"
	annoIdle        = "Idle"
	annoDraining    = "Draining"
	annoMcpPaused   = "Draining_MCP_Paused"
)

// writer implements io.Writer interface as a pass-through for log.Log.
type writer struct {
	logFunc func(msg string, keysAndValues ...interface{})
}

// Write passes string(p) into writer's logFunc and always returns len(p)
func (w writer) Write(p []byte) (n int, err error) {
	w.logFunc(string(p))
	return len(p), nil
}

func New(
	client snclientset.Interface,
	kubeClient kubernetes.Interface,
	hostHelpers helper.HostHelpersInterface,
	platformHelper platforms.Interface,
	exitCh chan<- error,
	stopCh <-chan struct{},
	syncCh <-chan struct{},
	refreshCh chan<- Message,
	er *EventRecorder,
) *Daemon {
	return &Daemon{
		client:          client,
		kubeClient:      kubeClient,
		HostHelpers:     hostHelpers,
		platformHelpers: platformHelper,
		exitCh:          exitCh,
		stopCh:          stopCh,
		syncCh:          syncCh,
		refreshCh:       refreshCh,
		nodeState:       &sriovnetworkv1.SriovNetworkNodeState{},
		drainer: &drain.Helper{
			Client:              kubeClient,
			Force:               true,
			IgnoreAllDaemonSets: true,
			DeleteEmptyDirData:  true,
			GracePeriodSeconds:  -1,
			Timeout:             90 * time.Second,
			OnPodDeletedOrEvicted: func(pod *corev1.Pod, usingEviction bool) {
				verbStr := "Deleted"
				if usingEviction {
					verbStr = "Evicted"
				}
				log.Log.Info(fmt.Sprintf("%s pod from Node %s/%s", verbStr, pod.Namespace, pod.Name))
			},
			Out:    writer{log.Log.Info},
			ErrOut: writer{func(msg string, kv ...interface{}) { log.Log.Error(nil, msg, kv...) }},
			Ctx:    context.Background(),
		},
		workqueue: workqueue.NewNamedRateLimitingQueue(workqueue.NewMaxOfRateLimiter(
			&workqueue.BucketRateLimiter{Limiter: rate.NewLimiter(rate.Limit(updateDelay), 1)},
			workqueue.NewItemExponentialFailureRateLimiter(1*time.Second, maxUpdateBackoff)), "SriovNetworkNodeState"),
		eventRecorder: er,
	}
}

// Run the config daemon
func (dn *Daemon) Run(stopCh <-chan struct{}, exitCh <-chan error) error {
	log.Log.V(0).Info("Run()", "node", vars.NodeName)

	if vars.ClusterType == consts.ClusterTypeOpenshift {
		log.Log.V(0).Info("Run(): start daemon.", "openshiftFlavor", dn.platformHelpers.GetFlavor())
	} else {
		log.Log.V(0).Info("Run(): start daemon.")
	}

	if !vars.UsingSystemdMode {
		log.Log.V(0).Info("Run(): daemon running in daemon mode")
		dn.HostHelpers.TryEnableRdma()
		dn.HostHelpers.TryEnableTun()
		dn.HostHelpers.TryEnableVhostNet()
		err := systemd.CleanSriovFilesFromHost(vars.ClusterType == consts.ClusterTypeOpenshift)
		if err != nil {
			log.Log.Error(err, "failed to remove all the systemd sriov files")
		}
	} else {
		log.Log.V(0).Info("Run(): daemon running in systemd mode")
	}

	// Only watch own SriovNetworkNodeState CR
	defer utilruntime.HandleCrash()
	defer dn.workqueue.ShutDown()

	if err := dn.prepareNMUdevRule(); err != nil {
		log.Log.Error(err, "failed to prepare udev files to disable network manager on requested VFs")
	}
	if err := dn.tryCreateSwitchdevUdevRule(); err != nil {
		log.Log.Error(err, "failed to create udev files for switchdev")
	}

	var timeout int64 = 5
	var metadataKey = "metadata.name"
	dn.mu = &sync.Mutex{}
	informerFactory := sninformer.NewFilteredSharedInformerFactory(dn.client,
		time.Second*15,
		vars.Namespace,
		func(lo *metav1.ListOptions) {
			lo.FieldSelector = metadataKey + "=" + vars.NodeName
			lo.TimeoutSeconds = &timeout
		},
	)

	informer := informerFactory.Sriovnetwork().V1().SriovNetworkNodeStates().Informer()
	informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: dn.enqueueNodeState,
		UpdateFunc: func(old, new interface{}) {
			dn.enqueueNodeState(new)
		},
	})

	cfgInformerFactory := sninformer.NewFilteredSharedInformerFactory(dn.client,
		time.Second*30,
		vars.Namespace,
		func(lo *metav1.ListOptions) {
			lo.FieldSelector = metadataKey + "=" + "default"
		},
	)

	cfgInformer := cfgInformerFactory.Sriovnetwork().V1().SriovOperatorConfigs().Informer()
	cfgInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    dn.operatorConfigAddHandler,
		UpdateFunc: dn.operatorConfigChangeHandler,
	})

	rand.Seed(time.Now().UnixNano())
	nodeInformerFactory := informers.NewSharedInformerFactory(dn.kubeClient,
		time.Second*15,
	)
	dn.nodeLister = nodeInformerFactory.Core().V1().Nodes().Lister()
	nodeInformer := nodeInformerFactory.Core().V1().Nodes().Informer()
	nodeInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    dn.nodeAddHandler,
		UpdateFunc: dn.nodeUpdateHandler,
	})
	go cfgInformer.Run(dn.stopCh)
	go nodeInformer.Run(dn.stopCh)
	time.Sleep(5 * time.Second)
	go informer.Run(dn.stopCh)
	if ok := cache.WaitForCacheSync(stopCh, cfgInformer.HasSynced, nodeInformer.HasSynced, informer.HasSynced); !ok {
		return fmt.Errorf("failed to wait for caches to sync")
	}

	log.Log.Info("Starting workers")
	// Launch one worker to process
	go wait.Until(dn.runWorker, time.Second, stopCh)
	log.Log.Info("Started workers")

	for {
		select {
		case <-stopCh:
			log.Log.V(0).Info("Run(): stop daemon")
			return nil
		case err, more := <-exitCh:
			log.Log.Error(err, "got an error")
			if more {
				dn.refreshCh <- Message{
					syncStatus:    consts.SyncStatusFailed,
					lastSyncError: err.Error(),
				}
			}
			return err
		case <-time.After(30 * time.Second):
			log.Log.V(2).Info("Run(): period refresh")
			if err := dn.tryCreateSwitchdevUdevRule(); err != nil {
				log.Log.V(2).Error(err, "Could not create udev rule")
			}
		}
	}
}

func (dn *Daemon) runWorker() {
	for dn.processNextWorkItem() {
	}
}

func (dn *Daemon) enqueueNodeState(obj interface{}) {
	var ns *sriovnetworkv1.SriovNetworkNodeState
	var ok bool
	if ns, ok = obj.(*sriovnetworkv1.SriovNetworkNodeState); !ok {
		utilruntime.HandleError(fmt.Errorf("expected SriovNetworkNodeState but got %#v", obj))
		return
	}
	key := ns.GetGeneration()
	dn.workqueue.Add(key)
}

func (dn *Daemon) processNextWorkItem() bool {
	log.Log.V(2).Info("processNextWorkItem", "worker-queue-size", dn.workqueue.Len())
	obj, shutdown := dn.workqueue.Get()
	if shutdown {
		return false
	}

	log.Log.V(2).Info("get item from queue", "item", obj.(int64))

	// We wrap this block in a func so we can defer c.workqueue.Done.
	err := func(obj interface{}) error {
		// We call Done here so the workqueue knows we have finished
		// processing this item.
		defer dn.workqueue.Done(obj)
		var key int64
		var ok bool
		if key, ok = obj.(int64); !ok {
			// As the item in the workqueue is actually invalid, we call
			// Forget here.
			dn.workqueue.Forget(obj)
			utilruntime.HandleError(fmt.Errorf("expected workItem in workqueue but got %#v", obj))
			return nil
		}

		err := dn.nodeStateSyncHandler()
		if err != nil {
			// Ereport error message, and put the item back to work queue for retry.
			dn.refreshCh <- Message{
				syncStatus:    consts.SyncStatusFailed,
				lastSyncError: err.Error(),
			}
			<-dn.syncCh
			dn.workqueue.AddRateLimited(key)
			return fmt.Errorf("error syncing: %s, requeuing", err.Error())
		}
		// Finally, if no error occurs we Forget this item so it does not
		// get queued again until another change happens.
		dn.workqueue.Forget(obj)
		log.Log.Info("Successfully synced")
		return nil
	}(obj)

	if err != nil {
		utilruntime.HandleError(err)
	}

	return true
}

func (dn *Daemon) nodeAddHandler(obj interface{}) {
	dn.nodeUpdateHandler(nil, obj)
}

func (dn *Daemon) nodeUpdateHandler(old, new interface{}) {
	node, err := dn.nodeLister.Get(vars.NodeName)
	if errors.IsNotFound(err) {
		log.Log.V(2).Info("nodeUpdateHandler(): node has been deleted", "name", vars.NodeName)
		return
	}
	dn.node = node.DeepCopy()

	nodes, err := dn.nodeLister.List(labels.Everything())
	if err != nil {
		log.Log.Error(err, "nodeUpdateHandler(): failed to list nodes")
		return
	}

	// Checking if other nodes are draining
	for _, otherNode := range nodes {
		if otherNode.GetName() == vars.NodeName {
			continue
		}

		drainingAnnotationValue := otherNode.Annotations[annoKey]
		if drainingAnnotationValue == annoDraining || drainingAnnotationValue == annoMcpPaused {
			log.Log.V(2).Info("nodeUpdateHandler(): node is not drainable, another node is draining",
				"other-node", otherNode.Name, "annotation", annoKey+"="+drainingAnnotationValue)
			dn.drainable = false
			return
		}
	}

	if !dn.drainable {
		log.Log.V(2).Info("nodeUpdateHandler(): node is now drainable")
	}

	dn.drainable = true
}

func (dn *Daemon) operatorConfigAddHandler(obj interface{}) {
	dn.operatorConfigChangeHandler(&sriovnetworkv1.SriovOperatorConfig{}, obj)
}

func (dn *Daemon) operatorConfigChangeHandler(old, new interface{}) {
	newCfg := new.(*sriovnetworkv1.SriovOperatorConfig)
	snolog.SetLogLevel(newCfg.Spec.LogLevel)

	newDisableDrain := newCfg.Spec.DisableDrain
	if dn.disableDrain != newDisableDrain {
		dn.disableDrain = newDisableDrain
		log.Log.Info("Set Disable Drain", "value", dn.disableDrain)
	}
}

func (dn *Daemon) nodeStateSyncHandler() error {
	var err error
	// Get the latest NodeState
	var latestState *sriovnetworkv1.SriovNetworkNodeState
	var sriovResult = &systemd.SriovResult{SyncStatus: consts.SyncStatusSucceeded, LastSyncError: ""}
	latestState, err = dn.client.SriovnetworkV1().SriovNetworkNodeStates(vars.Namespace).Get(context.Background(), vars.NodeName, metav1.GetOptions{})
	if err != nil {
		log.Log.Error(err, "nodeStateSyncHandler(): Failed to fetch node state", "name", vars.NodeName)
		return err
	}
	latest := latestState.GetGeneration()
	log.Log.V(0).Info("nodeStateSyncHandler(): new generation", "generation", latest)

	if vars.ClusterType == consts.ClusterTypeOpenshift && !dn.platformHelpers.IsHypershift() {
		if err = dn.getNodeMachinePool(); err != nil {
			return err
		}
	}

	if dn.nodeState.GetGeneration() == latest {
		if vars.UsingSystemdMode {
			serviceEnabled, err := dn.HostHelpers.IsServiceEnabled(systemd.SriovServicePath)
			if err != nil {
				log.Log.Error(err, "nodeStateSyncHandler(): failed to check if sriov-config service exist on host")
				return err
			}
			postNetworkServiceEnabled, err := dn.HostHelpers.IsServiceEnabled(systemd.SriovPostNetworkServicePath)
			if err != nil {
				log.Log.Error(err, "nodeStateSyncHandler(): failed to check if sriov-config-post-network service exist on host")
				return err
			}

			// if the service doesn't exist we should continue to let the k8s plugin to create the service files
			// this is only for k8s base environments, for openshift the sriov-operator creates a machine config to will apply
			// the system service and reboot the node the config-daemon doesn't need to do anything.
			if !(serviceEnabled && postNetworkServiceEnabled) {
				sriovResult = &systemd.SriovResult{SyncStatus: consts.SyncStatusFailed,
					LastSyncError: fmt.Sprintf("some sriov systemd services are not available on node: "+
						"sriov-config available:%t, sriov-config-post-network available:%t", serviceEnabled, postNetworkServiceEnabled)}
			} else {
				sriovResult, err = systemd.ReadSriovResult()
				if err != nil {
					log.Log.Error(err, "nodeStateSyncHandler(): failed to load sriov result file from host")
					return err
				}
			}
			if sriovResult.LastSyncError != "" || sriovResult.SyncStatus == consts.SyncStatusFailed {
				log.Log.Info("nodeStateSyncHandler(): sync failed systemd service error", "last-sync-error", sriovResult.LastSyncError)

				// add the error but don't requeue
				dn.refreshCh <- Message{
					syncStatus:    consts.SyncStatusFailed,
					lastSyncError: sriovResult.LastSyncError,
				}
				<-dn.syncCh
				return nil
			}
		}
		log.Log.V(0).Info("nodeStateSyncHandler(): Interface not changed")
		if latestState.Status.LastSyncError != "" ||
			latestState.Status.SyncStatus != consts.SyncStatusSucceeded {
			dn.refreshCh <- Message{
				syncStatus:    consts.SyncStatusSucceeded,
				lastSyncError: "",
			}
			// wait for writer to refresh the status
			<-dn.syncCh
		}

		return nil
	}

	if latestState.GetGeneration() == 1 && len(latestState.Spec.Interfaces) == 0 {
		err = dn.HostHelpers.ClearPCIAddressFolder()
		if err != nil {
			log.Log.Error(err, "failed to clear the PCI address configuration")
			return err
		}

		log.Log.V(0).Info(
			"nodeStateSyncHandler(): interface policy spec not yet set by controller for sriovNetworkNodeState",
			"name", latestState.Name)
		if latestState.Status.SyncStatus != "Succeeded" {
			dn.refreshCh <- Message{
				syncStatus:    "Succeeded",
				lastSyncError: "",
			}
			// wait for writer to refresh status
			<-dn.syncCh
		}
		return nil
	}

	dn.refreshCh <- Message{
		syncStatus:    consts.SyncStatusInProgress,
		lastSyncError: "",
	}
	// wait for writer to refresh status then pull again the latest node state
	<-dn.syncCh

	// we need to load the latest status to our object
	// if we don't do it we can have a race here where the user remove the virtual functions but the operator didn't
	// trigger the refresh
	updatedState, err := dn.client.SriovnetworkV1().SriovNetworkNodeStates(vars.Namespace).Get(context.Background(), vars.NodeName, metav1.GetOptions{})
	if err != nil {
		log.Log.Error(err, "nodeStateSyncHandler(): Failed to fetch node state", "name", vars.NodeName)
		return err
	}
	latestState.Status = updatedState.Status

	// load plugins if it has not loaded
	if len(dn.enabledPlugins) == 0 {
		dn.enabledPlugins, err = enablePlugins(latestState, dn.HostHelpers)
		if err != nil {
			log.Log.Error(err, "nodeStateSyncHandler(): failed to enable vendor plugins")
			return err
		}
	}

	reqReboot := false
	reqDrain := false

	// check if any of the plugins required to drain or reboot the node
	for k, p := range dn.enabledPlugins {
		d, r := false, false
		if dn.nodeState.GetName() == "" {
			log.Log.V(0).Info("nodeStateSyncHandler(): calling OnNodeStateChange for a new node state")
		} else {
			log.Log.V(0).Info("nodeStateSyncHandler(): calling OnNodeStateChange for an updated node state")
		}
		d, r, err = p.OnNodeStateChange(latestState)
		if err != nil {
			log.Log.Error(err, "nodeStateSyncHandler(): OnNodeStateChange plugin error", "plugin-name", k)
			return err
		}
		log.Log.V(0).Info("nodeStateSyncHandler(): OnNodeStateChange result", "plugin", k, "drain-required", d, "reboot-required", r)
		reqDrain = reqDrain || d
		reqReboot = reqReboot || r
	}

	// When running using systemd check if the applied configuration is the latest one
	// or there is a new config we need to apply
	// When using systemd configuration we write the file
	if vars.UsingSystemdMode {
		log.Log.V(0).Info("nodeStateSyncHandler(): writing systemd config file to host")
		systemdConfModified, err := systemd.WriteConfFile(latestState)
		if err != nil {
			log.Log.Error(err, "nodeStateSyncHandler(): failed to write configuration file for systemd mode")
			return err
		}
		if systemdConfModified {
			// remove existing result file to make sure that we will not use outdated result, e.g. in case if
			// systemd service was not triggered for some reason
			err = systemd.RemoveSriovResult()
			if err != nil {
				log.Log.Error(err, "nodeStateSyncHandler(): failed to remove result file for systemd mode")
				return err
			}
		}
		reqDrain = reqDrain || systemdConfModified
		reqReboot = reqReboot || systemdConfModified
		log.Log.V(0).Info("nodeStateSyncHandler(): systemd mode WriteConfFile results",
			"drain-required", reqDrain, "reboot-required", reqReboot, "disable-drain", dn.disableDrain)

		err = systemd.WriteSriovSupportedNics()
		if err != nil {
			log.Log.Error(err, "nodeStateSyncHandler(): failed to write supported nic ids file for systemd mode")
			return err
		}
	}
	log.Log.V(0).Info("nodeStateSyncHandler(): aggregated daemon",
		"drain-required", reqDrain, "reboot-required", reqReboot, "disable-drain", dn.disableDrain)

	for k, p := range dn.enabledPlugins {
		// Skip both the general and virtual plugin apply them last
		if k != GenericPluginName && k != VirtualPluginName {
			err := p.Apply()
			if err != nil {
				log.Log.Error(err, "nodeStateSyncHandler(): plugin Apply failed", "plugin-name", k)
				return err
			}
		}
	}
	if dn.platformHelpers.IsOpenshiftCluster() && !dn.platformHelpers.IsHypershift() {
		if err = dn.getNodeMachinePool(); err != nil {
			return err
		}
	}
	if reqDrain {
		if !dn.isNodeDraining() {
			if !dn.disableDrain {
				ctx, cancel := context.WithCancel(context.TODO())
				defer cancel()

				log.Log.Info("nodeStateSyncHandler(): get drain lock for sriov daemon")
				done := make(chan bool)
				go dn.getDrainLock(ctx, done)
				<-done
			}
		}

		if dn.platformHelpers.IsOpenshiftCluster() && !dn.platformHelpers.IsHypershift() {
			log.Log.Info("nodeStateSyncHandler(): pause MCP")
			if err := dn.pauseMCP(); err != nil {
				return err
			}
		}

		if dn.disableDrain {
			log.Log.Info("nodeStateSyncHandler(): disable drain is true skipping drain")
		} else {
			log.Log.Info("nodeStateSyncHandler(): drain node")
			if err := dn.drainNode(); err != nil {
				return err
			}
		}
	}

	if !reqReboot && !vars.UsingSystemdMode {
		// For BareMetal machines apply the generic plugin
		selectedPlugin, ok := dn.enabledPlugins[GenericPluginName]
		if ok {
			// Apply generic_plugin last
			err = selectedPlugin.Apply()
			if err != nil {
				log.Log.Error(err, "nodeStateSyncHandler(): generic_plugin fail to apply")
				return err
			}
		}

		// For Virtual machines apply the virtual plugin
		selectedPlugin, ok = dn.enabledPlugins[VirtualPluginName]
		if ok {
			// Apply virtual_plugin last
			err = selectedPlugin.Apply()
			if err != nil {
				log.Log.Error(err, "nodeStateSyncHandler(): virtual_plugin failed to apply")
				return err
			}
		}
	}

	if reqReboot {
		log.Log.Info("nodeStateSyncHandler(): reboot node")
		dn.eventRecorder.SendEvent("RebootNode", "Reboot node has been initiated")
		dn.rebootNode()
		return nil
	}

	// restart device plugin pod
	log.Log.Info("nodeStateSyncHandler(): restart device plugin pod")
	if err := dn.restartDevicePluginPod(); err != nil {
		log.Log.Error(err, "nodeStateSyncHandler(): fail to restart device plugin pod")
		return err
	}
	if dn.isNodeDraining() {
		if err := dn.completeDrain(); err != nil {
			log.Log.Error(err, "nodeStateSyncHandler(): failed to complete draining")
			return err
		}
	} else {
		if !dn.nodeHasAnnotation(annoKey, annoIdle) {
			if err := dn.annotateNode(vars.NodeName, annoIdle); err != nil {
				log.Log.Error(err, "nodeStateSyncHandler(): failed to annotate node")
				return err
			}
		}
	}
	log.Log.Info("nodeStateSyncHandler(): sync succeeded")
	dn.nodeState = latestState.DeepCopy()
	if vars.UsingSystemdMode {
		dn.refreshCh <- Message{
			syncStatus:    sriovResult.SyncStatus,
			lastSyncError: sriovResult.LastSyncError,
		}
	} else {
		dn.refreshCh <- Message{
			syncStatus:    consts.SyncStatusSucceeded,
			lastSyncError: "",
		}
	}
	// wait for writer to refresh the status
	<-dn.syncCh
	return nil
}

func (dn *Daemon) nodeHasAnnotation(annoKey string, value string) bool {
	// Check if node already contains annotation
	if anno, ok := dn.node.Annotations[annoKey]; ok && (anno == value) {
		return true
	}
	return false
}

// isNodeDraining: check if the node is draining
// both Draining and MCP paused labels will return true
func (dn *Daemon) isNodeDraining() bool {
	anno, ok := dn.node.Annotations[annoKey]
	if !ok {
		return false
	}

	return anno == annoDraining || anno == annoMcpPaused
}

func (dn *Daemon) completeDrain() error {
	if !dn.disableDrain {
		if err := drain.RunCordonOrUncordon(dn.drainer, dn.node, false); err != nil {
			return err
		}
	}

	if dn.platformHelpers.IsOpenshiftCluster() && !dn.platformHelpers.IsHypershift() {
		log.Log.Info("completeDrain(): resume MCP", "mcp-name", dn.mcpName)
		pausePatch := []byte("{\"spec\":{\"paused\":false}}")
		if _, err := dn.platformHelpers.GetMcClient().MachineconfigurationV1().MachineConfigPools().Patch(context.Background(), dn.mcpName, types.MergePatchType, pausePatch, metav1.PatchOptions{}); err != nil {
			log.Log.Error(err, "completeDrain(): failed to resume MCP", "mcp-name", dn.mcpName)
			return err
		}
	}

	if err := dn.annotateNode(vars.NodeName, annoIdle); err != nil {
		log.Log.Error(err, "completeDrain(): failed to annotate node")
		return err
	}
	return nil
}

func (dn *Daemon) restartDevicePluginPod() error {
	dn.mu.Lock()
	defer dn.mu.Unlock()
	log.Log.V(2).Info("restartDevicePluginPod(): try to restart device plugin pod")

	var podToDelete string
	pods, err := dn.kubeClient.CoreV1().Pods(vars.Namespace).List(context.Background(), metav1.ListOptions{
		LabelSelector: "app=sriov-device-plugin",
		FieldSelector: "spec.nodeName=" + vars.NodeName,
	})
	if err != nil {
		if errors.IsNotFound(err) {
			log.Log.Info("restartDevicePluginPod(): device plugin pod exited")
			return nil
		}
		log.Log.Error(err, "restartDevicePluginPod(): Failed to list device plugin pod, retrying")
		return err
	}

	if len(pods.Items) == 0 {
		log.Log.Info("restartDevicePluginPod(): device plugin pod exited")
		return nil
	}
	podToDelete = pods.Items[0].Name

	log.Log.V(2).Info("restartDevicePluginPod(): Found device plugin pod, deleting it", "pod-name", podToDelete)
	err = dn.kubeClient.CoreV1().Pods(vars.Namespace).Delete(context.Background(), podToDelete, metav1.DeleteOptions{})
	if errors.IsNotFound(err) {
		log.Log.Info("restartDevicePluginPod(): pod to delete not found")
		return nil
	}
	if err != nil {
		log.Log.Error(err, "restartDevicePluginPod(): Failed to delete device plugin pod, retrying")
		return err
	}

	if err := wait.PollImmediateUntil(3*time.Second, func() (bool, error) {
		_, err := dn.kubeClient.CoreV1().Pods(vars.Namespace).Get(context.Background(), podToDelete, metav1.GetOptions{})
		if errors.IsNotFound(err) {
			log.Log.Info("restartDevicePluginPod(): device plugin pod exited")
			return true, nil
		}

		if err != nil {
			log.Log.Error(err, "restartDevicePluginPod(): Failed to check for device plugin exit, retrying")
		} else {
			log.Log.Info("restartDevicePluginPod(): waiting for device plugin pod to exit", "pod-name", podToDelete)
		}
		return false, nil
	}, dn.stopCh); err != nil {
		log.Log.Error(err, "restartDevicePluginPod(): failed to wait for checking pod deletion")
		return err
	}

	return nil
}

func (dn *Daemon) rebootNode() {
	log.Log.Info("rebootNode(): trigger node reboot")
	exit, err := dn.HostHelpers.Chroot(consts.Host)
	if err != nil {
		log.Log.Error(err, "rebootNode(): chroot command failed")
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
		"--description", "sriov-network-config-daemon reboot node", "/bin/sh", "-c", "systemctl stop kubelet.service; reboot")

	if err := cmd.Run(); err != nil {
		log.Log.Error(err, "failed to reboot node")
	}
}

func (dn *Daemon) annotateNode(node, value string) error {
	log.Log.Info("annotateNode(): Annotate node", "name", node, "value", value)

	oldNode, err := dn.kubeClient.CoreV1().Nodes().Get(context.Background(), vars.NodeName, metav1.GetOptions{})
	if err != nil {
		log.Log.Error(err, "annotateNode(): Failed to get node, retrying", "name", node)
		return err
	}

	oldData, err := json.Marshal(oldNode)
	if err != nil {
		return err
	}

	newNode := oldNode.DeepCopy()
	if newNode.Annotations == nil {
		newNode.Annotations = map[string]string{}
	}
	if newNode.Annotations[annoKey] != value {
		newNode.Annotations[annoKey] = value
		newData, err := json.Marshal(newNode)
		if err != nil {
			return err
		}
		patchBytes, err := strategicpatch.CreateTwoWayMergePatch(oldData, newData, corev1.Node{})
		if err != nil {
			return err
		}
		_, err = dn.kubeClient.CoreV1().Nodes().Patch(context.Background(),
			vars.NodeName,
			types.StrategicMergePatchType,
			patchBytes,
			metav1.PatchOptions{})
		if err != nil {
			log.Log.Error(err, "annotateNode(): Failed to patch node", "name", node)
			return err
		}
	}
	return nil
}

func (dn *Daemon) getNodeMachinePool() error {
	desiredConfig, ok := dn.node.Annotations[daemonconsts.DesiredMachineConfigAnnotationKey]
	if !ok {
		log.Log.Error(nil, "getNodeMachinePool(): Failed to find the the desiredConfig Annotation")
		return fmt.Errorf("getNodeMachinePool(): Failed to find the the desiredConfig Annotation")
	}
	mc, err := dn.platformHelpers.GetMcClient().MachineconfigurationV1().MachineConfigs().Get(context.TODO(), desiredConfig, metav1.GetOptions{})
	if err != nil {
		log.Log.Error(err, "getNodeMachinePool(): Failed to get the desired Machine Config")
		return err
	}
	for _, owner := range mc.OwnerReferences {
		if owner.Kind == "MachineConfigPool" {
			dn.mcpName = owner.Name
			return nil
		}
	}

	log.Log.Error(nil, "getNodeMachinePool(): Failed to find the MCP of the node")
	return fmt.Errorf("getNodeMachinePool(): Failed to find the MCP of the node")
}

func (dn *Daemon) getDrainLock(ctx context.Context, done chan bool) {
	var err error

	lock := &resourcelock.LeaseLock{
		LeaseMeta: metav1.ObjectMeta{
			Name:      "config-daemon-draining-lock",
			Namespace: vars.Namespace,
		},
		Client: dn.kubeClient.CoordinationV1(),
		LockConfig: resourcelock.ResourceLockConfig{
			Identity: vars.NodeName,
		},
	}

	// start the leader election
	leaderelection.RunOrDie(ctx, leaderelection.LeaderElectionConfig{
		Lock:            lock,
		ReleaseOnCancel: true,
		LeaseDuration:   5 * time.Second,
		RenewDeadline:   3 * time.Second,
		RetryPeriod:     1 * time.Second,
		Callbacks: leaderelection.LeaderCallbacks{
			OnStartedLeading: func(ctx context.Context) {
				log.Log.V(2).Info("getDrainLock(): started leading")
				for {
					time.Sleep(3 * time.Second)
					if dn.node.Annotations[annoKey] == annoMcpPaused {
						// The node in Draining_MCP_Paused state, no other node is draining. Skip drainable checking
						done <- true
						return
					}
					if dn.drainable {
						log.Log.V(2).Info("getDrainLock(): no other node is draining")
						err = dn.annotateNode(vars.NodeName, annoDraining)
						if err != nil {
							log.Log.Error(err, "getDrainLock(): failed to annotate node")
							continue
						}
						done <- true
						return
					}
					log.Log.V(2).Info("getDrainLock(): other node is draining, wait...")
				}
			},
			OnStoppedLeading: func() {
				log.Log.V(2).Info("getDrainLock(): stopped leading")
			},
		},
	})
}

func (dn *Daemon) pauseMCP() error {
	log.Log.Info("pauseMCP(): pausing MCP")
	var err error

	mcpInformerFactory := mcfginformers.NewSharedInformerFactory(dn.platformHelpers.GetMcClient(),
		time.Second*30,
	)
	mcpInformer := mcpInformerFactory.Machineconfiguration().V1().MachineConfigPools().Informer()

	ctx, cancel := context.WithCancel(context.TODO())
	defer cancel()
	paused := dn.node.Annotations[annoKey] == annoMcpPaused

	mcpEventHandler := func(obj interface{}) {
		mcp := obj.(*mcfgv1.MachineConfigPool)
		if mcp.GetName() != dn.mcpName {
			return
		}
		// Always get the latest object
		newMcp, err := dn.platformHelpers.GetMcClient().MachineconfigurationV1().MachineConfigPools().Get(ctx, dn.mcpName, metav1.GetOptions{})
		if err != nil {
			log.Log.V(2).Error(err, "pauseMCP(): Failed to get MCP", "mcp-name", dn.mcpName)
			return
		}
		if mcfgv1.IsMachineConfigPoolConditionFalse(newMcp.Status.Conditions, mcfgv1.MachineConfigPoolDegraded) &&
			mcfgv1.IsMachineConfigPoolConditionTrue(newMcp.Status.Conditions, mcfgv1.MachineConfigPoolUpdated) &&
			mcfgv1.IsMachineConfigPoolConditionFalse(newMcp.Status.Conditions, mcfgv1.MachineConfigPoolUpdating) {
			log.Log.V(2).Info("pauseMCP(): MCP is ready", "mcp-name", dn.mcpName)
			if paused {
				log.Log.V(2).Info("pauseMCP(): stop MCP informer")
				cancel()
				return
			}
			if newMcp.Spec.Paused {
				log.Log.V(2).Info("pauseMCP(): MCP was paused by other, wait...", "mcp-name", dn.mcpName)
				return
			}
			log.Log.Info("pauseMCP(): pause MCP", "mcp-name", dn.mcpName)
			pausePatch := []byte("{\"spec\":{\"paused\":true}}")
			_, err = dn.platformHelpers.GetMcClient().MachineconfigurationV1().MachineConfigPools().Patch(context.Background(), dn.mcpName, types.MergePatchType, pausePatch, metav1.PatchOptions{})
			if err != nil {
				log.Log.V(2).Error(err, "pauseMCP(): failed to pause MCP", "mcp-name", dn.mcpName)
				return
			}
			err = dn.annotateNode(vars.NodeName, annoMcpPaused)
			if err != nil {
				log.Log.V(2).Error(err, "pauseMCP(): Failed to annotate node")
				return
			}
			paused = true
			return
		}
		if paused {
			log.Log.Info("pauseMCP(): MCP is processing, resume MCP", "mcp-name", dn.mcpName)
			pausePatch := []byte("{\"spec\":{\"paused\":false}}")
			_, err = dn.platformHelpers.GetMcClient().MachineconfigurationV1().MachineConfigPools().Patch(context.Background(), dn.mcpName, types.MergePatchType, pausePatch, metav1.PatchOptions{})
			if err != nil {
				log.Log.V(2).Error(err, "pauseMCP(): fail to resume MCP", "mcp-name", dn.mcpName)
				return
			}
			err = dn.annotateNode(vars.NodeName, annoDraining)
			if err != nil {
				log.Log.V(2).Error(err, "pauseMCP(): Failed to annotate node")
				return
			}
			paused = false
		}
		log.Log.Info("pauseMCP():MCP is not ready, wait...",
			"mcp-name", newMcp.GetName(), "mcp-conditions", newMcp.Status.Conditions)
	}

	mcpInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: mcpEventHandler,
		UpdateFunc: func(old, new interface{}) {
			mcpEventHandler(new)
		},
	})

	// The Draining_MCP_Paused state means the MCP work has been paused by the config daemon in previous round.
	// Only check MCP state if the node is not in Draining_MCP_Paused state
	if !paused {
		mcpInformerFactory.Start(ctx.Done())
		mcpInformerFactory.WaitForCacheSync(ctx.Done())
		<-ctx.Done()
	}

	return err
}

func (dn *Daemon) drainNode() error {
	log.Log.Info("drainNode(): Update prepared")
	var err error

	backoff := wait.Backoff{
		Steps:    5,
		Duration: 10 * time.Second,
		Factor:   2,
	}
	var lastErr error

	log.Log.Info("drainNode(): Start draining")
	dn.eventRecorder.SendEvent("DrainNode", "Drain node has been initiated")
	if err = wait.ExponentialBackoff(backoff, func() (bool, error) {
		err := drain.RunCordonOrUncordon(dn.drainer, dn.node, true)
		if err != nil {
			lastErr = err
			log.Log.Error(err, "cordon failed, retrying")
			return false, nil
		}
		err = drain.RunNodeDrain(dn.drainer, vars.NodeName)
		if err == nil {
			return true, nil
		}
		lastErr = err
		log.Log.Error(err, "Draining failed, retrying")
		return false, nil
	}); err != nil {
		if err == wait.ErrWaitTimeout {
			log.Log.Error(err, "drainNode(): failed to drain node", "tries", backoff.Steps, "last-error", lastErr)
		}
		dn.eventRecorder.SendEvent("DrainNode", "Drain node failed")
		log.Log.Error(err, "drainNode(): failed to drain node")
		return err
	}
	dn.eventRecorder.SendEvent("DrainNode", "Drain node completed")
	log.Log.Info("drainNode(): drain complete")
	return nil
}

// TODO: move this to host interface
func (dn *Daemon) tryCreateSwitchdevUdevRule() error {
	log.Log.V(2).Info("tryCreateSwitchdevUdevRule()")
	nodeState, nodeStateErr := dn.client.SriovnetworkV1().SriovNetworkNodeStates(vars.Namespace).Get(
		context.Background(),
		vars.NodeName,
		metav1.GetOptions{},
	)
	if nodeStateErr != nil {
		log.Log.Error(nodeStateErr, "could not fetch node state, skip updating switchdev udev rules", "name", vars.NodeName)
		return nil
	}

	var newContent string
	filePath := path.Join(vars.FilesystemRoot, "/host/etc/udev/rules.d/20-switchdev.rules")

	for _, ifaceStatus := range nodeState.Status.Interfaces {
		if ifaceStatus.EswitchMode == sriovnetworkv1.ESwithModeSwitchDev {
			switchID, err := dn.HostHelpers.GetPhysSwitchID(ifaceStatus.Name)
			if err != nil {
				return err
			}
			portName, err := dn.HostHelpers.GetPhysPortName(ifaceStatus.Name)
			if err != nil {
				return err
			}
			newContent = newContent + fmt.Sprintf("SUBSYSTEM==\"net\", ACTION==\"add|move\", ATTRS{phys_switch_id}==\"%s\", ATTR{phys_port_name}==\"pf%svf*\", IMPORT{program}=\"/etc/udev/switchdev-vf-link-name.sh $attr{phys_port_name}\", NAME=\"%s_$env{NUMBER}\"\n", switchID, strings.TrimPrefix(portName, "p"), ifaceStatus.Name)
		}
	}

	oldContent, err := os.ReadFile(filePath)
	// if oldContent = newContent, don't do anything
	if err == nil && newContent == string(oldContent) {
		return nil
	}

	log.Log.V(2).Info("Old udev content and new content differ. Writing new content to file.",
		"old-content", strings.TrimSuffix(string(oldContent), "\n"),
		"new-content", strings.TrimSuffix(newContent, "\n"),
		"path", filePath)

	// if the file does not exist or if oldContent != newContent
	// write to file and create it if it doesn't exist
	err = os.WriteFile(filePath, []byte(newContent), 0664)
	if err != nil {
		log.Log.Error(err, "tryCreateSwitchdevUdevRule(): fail to write file")
		return err
	}

	var stdout, stderr bytes.Buffer
	cmd := exec.Command("/bin/bash", path.Join(vars.FilesystemRoot, udevScriptsPath))
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return err
	}
	log.Log.V(2).Info("tryCreateSwitchdevUdevRule(): stdout", "output", cmd.Stdout)

	i, err := strconv.Atoi(strings.TrimSpace(stdout.String()))
	if err == nil {
		if i == 0 {
			log.Log.V(2).Info("tryCreateSwitchdevUdevRule(): switchdev udev rules loaded")
		} else {
			log.Log.V(2).Info("tryCreateSwitchdevUdevRule(): switchdev udev rules not loaded")
		}
	}
	return nil
}

func (dn *Daemon) prepareNMUdevRule() error {
	// we need to remove the Red Hat Virtio network device from the udev rule configuration
	// if we don't remove it when running the config-daemon on a virtual node it will disconnect the node after a reboot
	// even that the operator should not be installed on virtual environments that are not openstack
	// we should not destroy the cluster if the operator is installed there
	supportedVfIds := []string{}
	for _, vfID := range sriovnetworkv1.GetSupportedVfIds() {
		if vfID == "0x1000" || vfID == "0x1041" {
			continue
		}
		supportedVfIds = append(supportedVfIds, vfID)
	}

	return dn.HostHelpers.PrepareNMUdevRule(supportedVfIds)
}
