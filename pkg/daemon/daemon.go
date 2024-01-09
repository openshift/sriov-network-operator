package daemon

import (
	"bytes"
	"context"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
	"sigs.k8s.io/controller-runtime/pkg/client"
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
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/utils"
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
	client client.Client

	sriovClient snclientset.Interface
	// kubeClient allows interaction with Kubernetes, including the node we are running on.
	kubeClient kubernetes.Interface

	desiredNodeState *sriovnetworkv1.SriovNetworkNodeState
	currentNodeState *sriovnetworkv1.SriovNetworkNodeState

	// list of disabled plugins
	disabledPlugins []string

	loadedPlugins map[string]plugin.VendorPlugin

	HostHelpers helper.HostHelpersInterface

	platformHelpers platforms.Interface

	// channel used by callbacks to signal Run() of an error
	exitCh chan<- error

	// channel used to ensure all spawned goroutines exit when we exit.
	stopCh <-chan struct{}

	syncCh <-chan struct{}

	refreshCh chan<- Message

	mu *sync.Mutex

	disableDrain bool

	workqueue workqueue.RateLimitingInterface

	eventRecorder *EventRecorder
}

const (
	udevScriptsPath = "/bindata/scripts/load-udev.sh"
)

func New(
	client client.Client,
	sriovClient snclientset.Interface,
	kubeClient kubernetes.Interface,
	hostHelpers helper.HostHelpersInterface,
	platformHelper platforms.Interface,
	exitCh chan<- error,
	stopCh <-chan struct{},
	syncCh <-chan struct{},
	refreshCh chan<- Message,
	er *EventRecorder,
	disabledPlugins []string,
) *Daemon {
	return &Daemon{
		client:           client,
		sriovClient:      sriovClient,
		kubeClient:       kubeClient,
		HostHelpers:      hostHelpers,
		platformHelpers:  platformHelper,
		exitCh:           exitCh,
		stopCh:           stopCh,
		syncCh:           syncCh,
		refreshCh:        refreshCh,
		desiredNodeState: &sriovnetworkv1.SriovNetworkNodeState{},
		currentNodeState: &sriovnetworkv1.SriovNetworkNodeState{},
		workqueue: workqueue.NewNamedRateLimitingQueue(workqueue.NewMaxOfRateLimiter(
			&workqueue.BucketRateLimiter{Limiter: rate.NewLimiter(rate.Limit(updateDelay), 1)},
			workqueue.NewItemExponentialFailureRateLimiter(1*time.Second, maxUpdateBackoff)), "SriovNetworkNodeState"),
		eventRecorder:   er,
		disabledPlugins: disabledPlugins,
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
	informerFactory := sninformer.NewFilteredSharedInformerFactory(dn.sriovClient,
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

	cfgInformerFactory := sninformer.NewFilteredSharedInformerFactory(dn.sriovClient,
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
	go cfgInformer.Run(dn.stopCh)
	time.Sleep(5 * time.Second)
	go informer.Run(dn.stopCh)
	if ok := cache.WaitForCacheSync(stopCh, cfgInformer.HasSynced, informer.HasSynced); !ok {
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

func (dn *Daemon) operatorConfigAddHandler(obj interface{}) {
	dn.operatorConfigChangeHandler(&sriovnetworkv1.SriovOperatorConfig{}, obj)
}

func (dn *Daemon) operatorConfigChangeHandler(old, new interface{}) {
	newCfg := new.(*sriovnetworkv1.SriovOperatorConfig)
	if newCfg.Namespace != vars.Namespace || newCfg.Name != consts.DefaultConfigName {
		log.Log.V(2).Info("unsupported SriovOperatorConfig", "namespace", newCfg.Namespace, "name", newCfg.Name)
		return
	}

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
	var sriovResult = &systemd.SriovResult{SyncStatus: consts.SyncStatusSucceeded, LastSyncError: ""}
	dn.desiredNodeState, err = dn.sriovClient.SriovnetworkV1().SriovNetworkNodeStates(vars.Namespace).Get(context.Background(), vars.NodeName, metav1.GetOptions{})
	if err != nil {
		log.Log.Error(err, "nodeStateSyncHandler(): Failed to fetch node state", "name", vars.NodeName)
		return err
	}
	latest := dn.desiredNodeState.GetGeneration()
	log.Log.V(0).Info("nodeStateSyncHandler(): new generation", "generation", latest)

	if dn.currentNodeState.GetGeneration() == latest && !utils.ObjectHasAnnotation(dn.desiredNodeState, consts.NodeStateDrainAnnotationCurrent, consts.DrainComplete) {
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
		if dn.desiredNodeState.Status.LastSyncError != "" ||
			dn.desiredNodeState.Status.SyncStatus != consts.SyncStatusSucceeded {
			dn.refreshCh <- Message{
				syncStatus:    consts.SyncStatusSucceeded,
				lastSyncError: "",
			}
			// wait for writer to refresh the status
			<-dn.syncCh
		}

		return nil
	}

	if dn.desiredNodeState.GetGeneration() == 1 && len(dn.desiredNodeState.Spec.Interfaces) == 0 {
		err = dn.HostHelpers.ClearPCIAddressFolder()
		if err != nil {
			log.Log.Error(err, "failed to clear the PCI address configuration")
			return err
		}

		log.Log.V(0).Info(
			"nodeStateSyncHandler(): interface policy spec not yet set by controller for sriovNetworkNodeState",
			"name", dn.desiredNodeState.Name)
		if dn.desiredNodeState.Status.SyncStatus != "Succeeded" {
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
	updatedState, err := dn.sriovClient.SriovnetworkV1().SriovNetworkNodeStates(vars.Namespace).Get(context.Background(), vars.NodeName, metav1.GetOptions{})
	if err != nil {
		log.Log.Error(err, "nodeStateSyncHandler(): Failed to fetch node state", "name", vars.NodeName)
		return err
	}
	dn.desiredNodeState.Status = updatedState.Status

	// load plugins if it has not loaded
	if len(dn.loadedPlugins) == 0 {
		dn.loadedPlugins, err = loadPlugins(dn.desiredNodeState, dn.HostHelpers, dn.disabledPlugins)
		if err != nil {
			log.Log.Error(err, "nodeStateSyncHandler(): failed to enable vendor plugins")
			return err
		}
	}

	reqReboot := false
	reqDrain := false

	// check if any of the plugins required to drain or reboot the node
	for k, p := range dn.loadedPlugins {
		d, r := false, false
		if dn.currentNodeState.GetName() == "" {
			log.Log.V(0).Info("nodeStateSyncHandler(): calling OnNodeStateChange for a new node state")
		} else {
			log.Log.V(0).Info("nodeStateSyncHandler(): calling OnNodeStateChange for an updated node state")
		}
		d, r, err = p.OnNodeStateChange(dn.desiredNodeState)
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
		systemdConfModified, err := systemd.WriteConfFile(dn.desiredNodeState)
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
		// require reboot if drain needed for systemd mode
		reqReboot = reqReboot || systemdConfModified || reqDrain
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

	for k, p := range dn.loadedPlugins {
		// Skip both the general and virtual plugin apply them last
		if k != GenericPluginName && k != VirtualPluginName {
			err := p.Apply()
			if err != nil {
				log.Log.Error(err, "nodeStateSyncHandler(): plugin Apply failed", "plugin-name", k)
				return err
			}
		}
	}

	if reqDrain ||
		(utils.ObjectHasAnnotationKey(dn.desiredNodeState, consts.NodeStateDrainAnnotationCurrent) &&
			!utils.ObjectHasAnnotation(dn.desiredNodeState, consts.NodeStateDrainAnnotationCurrent, consts.DrainIdle)) {
		if utils.ObjectHasAnnotation(dn.desiredNodeState, consts.NodeStateDrainAnnotationCurrent, consts.DrainComplete) {
			log.Log.Info("nodeStateSyncHandler(): the node complete the draining")
		} else if !dn.isNodeDraining() {
			if !dn.disableDrain {
				if reqReboot {
					log.Log.Info("nodeStateSyncHandler(): apply 'Reboot_Required' label for node")
					if err := dn.applyRequirement(consts.RebootRequired); err != nil {
						return err
					}

					log.Log.Info("nodeStateSyncHandler(): apply 'Reboot_Required' label for nodeState")
					if err := utils.AnnotateObject(dn.desiredNodeState,
						consts.NodeStateDrainAnnotation,
						consts.RebootRequired, dn.client); err != nil {
						return err
					}

					return nil
				}
				log.Log.Info("nodeStateSyncHandler(): apply 'Drain_Required' label for node")
				if err := dn.applyRequirement(consts.DrainRequired); err != nil {
					return err
				}

				log.Log.Info("nodeStateSyncHandler(): apply 'Drain_Required' label for nodeState")
				if err := utils.AnnotateObject(dn.desiredNodeState,
					consts.NodeStateDrainAnnotation,
					consts.DrainRequired, dn.client); err != nil {
					return err
				}
				return nil
			}
		} else {
			log.Log.Info("nodeStateSyncHandler(): the node is still draining waiting")
			return nil
		}
	}

	if !reqReboot && !vars.UsingSystemdMode {
		// For BareMetal machines apply the generic plugin
		selectedPlugin, ok := dn.loadedPlugins[GenericPluginName]
		if ok {
			// Apply generic plugin last
			err = selectedPlugin.Apply()
			if err != nil {
				log.Log.Error(err, "nodeStateSyncHandler(): generic plugin fail to apply")
				return err
			}
		}

		// For Virtual machines apply the virtual plugin
		selectedPlugin, ok = dn.loadedPlugins[VirtualPluginName]
		if ok {
			// Apply virtual plugin last
			err = selectedPlugin.Apply()
			if err != nil {
				log.Log.Error(err, "nodeStateSyncHandler(): virtual plugin failed to apply")
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

	log.Log.Info("nodeStateSyncHandler(): apply 'Idle' label for node")
	if err := dn.applyRequirement(consts.DrainIdle); err != nil {
		log.Log.Error(err, "nodeStateSyncHandler(): failed to annotate node")
		return err
	}

	log.Log.Info("nodeStateSyncHandler(): apply 'Idle' label for nodeState")
	if err := utils.AnnotateObject(dn.desiredNodeState,
		consts.NodeStateDrainAnnotation,
		consts.DrainIdle, dn.client); err != nil {
		return err
	}

	log.Log.Info("nodeStateSyncHandler(): sync succeeded")
	dn.currentNodeState = dn.desiredNodeState.DeepCopy()
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

// isNodeDraining: check if the node is draining
// both Draining and MCP paused labels will return true
func (dn *Daemon) isNodeDraining() bool {
	anno, ok := dn.desiredNodeState.Labels[consts.NodeStateDrainAnnotationCurrent]
	if !ok {
		return false
	}

	return anno == consts.Draining
}

func (dn *Daemon) applyRequirement(label string) error {
	log.Log.Info("applyDrainRequired(): no other node is draining")
	err := utils.AnnotateNode(vars.NodeName, consts.NodeDrainAnnotation, label, dn.client)
	if err != nil {
		log.Log.Error(err, "applyDrainRequired(): Failed to annotate node")
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
		LabelSelector:   "app=sriov-device-plugin",
		FieldSelector:   "spec.nodeName=" + vars.NodeName,
		ResourceVersion: "0",
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

func (dn *Daemon) tryCreateSwitchdevUdevRule() error {
	log.Log.V(2).Info("tryCreateSwitchdevUdevRule()")
	nodeState, nodeStateErr := dn.sriovClient.SriovnetworkV1().SriovNetworkNodeStates(vars.Namespace).Get(
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
