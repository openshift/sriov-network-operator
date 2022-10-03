package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"math/rand"
	"os"
	"os/exec"
	"os/signal"
	"path"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/golang/glog"
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

	sriovnetworkv1 "github.com/k8snetworkplumbingwg/sriov-network-operator/api/v1"
	snclientset "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/client/clientset/versioned"
	sninformer "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/client/informers/externalversions"
	plugin "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/plugins"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/utils"
)

const (
	// updateDelay is the baseline speed at which we react to changes.  We don't
	// need to react in milliseconds as any change would involve rebooting the node.
	updateDelay = 5 * time.Second
	// maxUpdateBackoff is the maximum time to react to a change as we back off
	// in the face of errors.
	maxUpdateBackoff = 60 * time.Second
)

var (
	defaultRebootTimeout = time.Hour
)

type Message struct {
	syncStatus    string
	lastSyncError string
}

type Daemon struct {
	// name is the node name.
	name string

	platform utils.PlatformType

	client snclientset.Interface
	// kubeClient allows interaction with Kubernetes, including the node we are running on.
	kubeClient kubernetes.Interface

	openshiftContext utils.OpenshiftContext

	nodeState *sriovnetworkv1.SriovNetworkNodeState

	enabledPlugins map[string]plugin.VendorPlugin

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

	updatingFlagMutex sync.Mutex
	updatingFlag      bool
}

const (
	rdmaScriptsPath     = "/bindata/scripts/enable-rdma.sh"
	udevScriptsPath     = "/bindata/scripts/load-udev.sh"
	annoKey             = "sriovnetwork.openshift.io/state"
	annoIdle            = "Idle"
	annoDraining        = "Draining"
	syncStatusSucceeded = "Succeeded"
	syncStatusFailed    = "Failed"
)

var namespace = os.Getenv("NAMESPACE")

// used by test to mock interactions with filesystem
var filesystemRoot string = ""

// writer implements io.Writer interface as a pass-through for glog.
type writer struct {
	logFunc func(args ...interface{})
}

// Write passes string(p) into writer's logFunc and always returns len(p)
func (w writer) Write(p []byte) (n int, err error) {
	w.logFunc(string(p))
	return len(p), nil
}

func New(
	nodeName string,
	client snclientset.Interface,
	kubeClient kubernetes.Interface,
	openshiftContext utils.OpenshiftContext,
	exitCh chan<- error,
	stopCh <-chan struct{},
	syncCh <-chan struct{},
	refreshCh chan<- Message,
	platformType utils.PlatformType,
) *Daemon {
	return &Daemon{
		name:             nodeName,
		platform:         platformType,
		client:           client,
		kubeClient:       kubeClient,
		openshiftContext: openshiftContext,
		exitCh:           exitCh,
		stopCh:           stopCh,
		syncCh:           syncCh,
		refreshCh:        refreshCh,
		nodeState:        &sriovnetworkv1.SriovNetworkNodeState{},
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
				glog.Info(fmt.Sprintf("%s pod from Node %s/%s", verbStr, pod.Namespace, pod.Name))
			},
			Out:    writer{glog.Info},
			ErrOut: writer{glog.Error},
			Ctx:    context.Background(),
		},
		workqueue: workqueue.NewNamedRateLimitingQueue(workqueue.NewMaxOfRateLimiter(
			&workqueue.BucketRateLimiter{Limiter: rate.NewLimiter(rate.Limit(updateDelay), 1)},
			workqueue.NewItemExponentialFailureRateLimiter(1*time.Second, maxUpdateBackoff)), "SriovNetworkNodeState"),
	}
}

func (dn *Daemon) tryCreateUdevRuleWrapper() error {
	ns, nodeStateErr := dn.client.SriovnetworkV1().SriovNetworkNodeStates(namespace).Get(
		context.Background(),
		dn.name,
		metav1.GetOptions{},
	)
	if nodeStateErr != nil {
		glog.Warningf("Could not fetch node state %s: %v, skip updating switchdev udev rules", dn.name, nodeStateErr)
	} else {
		err := tryCreateSwitchdevUdevRule(ns)
		if err != nil {
			glog.Warningf("Failed to create switchdev udev rules: %v", err)
		}
	}

	// update udev rule only if we are on a BM environment
	// for virtual environments we don't disable the vfs as they may be used by the platform/host
	if dn.platform != utils.VirtualOpenStack {
		err := tryCreateNMUdevRule()
		if err != nil {
			return err
		}
	}

	return nil
}

// Run the config daemon
func (dn *Daemon) Run(stopCh <-chan struct{}, exitCh <-chan error) error {
	if utils.ClusterType == utils.ClusterTypeOpenshift {
		glog.V(0).Infof("Run(): start daemon. openshiftFlavor: %s", dn.openshiftContext.OpenshiftFlavor)
	} else {
		glog.V(0).Infof("Run(): start daemon.")
	}
	// Only watch own SriovNetworkNodeState CR
	defer utilruntime.HandleCrash()
	defer dn.workqueue.ShutDown()

	signalStopCh := make(chan struct{})
	dn.installSignalHandler(signalStopCh)

	tryEnableRdma()
	tryEnableTun()
	tryEnableVhostNet()

	if err := dn.tryCreateUdevRuleWrapper(); err != nil {
		return err
	}

	var timeout int64 = 5
	dn.mu = &sync.Mutex{}
	informerFactory := sninformer.NewFilteredSharedInformerFactory(dn.client,
		time.Second*15,
		namespace,
		func(lo *metav1.ListOptions) {
			lo.FieldSelector = "metadata.name=" + dn.name
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
		namespace,
		func(lo *metav1.ListOptions) {
			lo.FieldSelector = "metadata.name=" + "default"
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

	glog.Info("Starting workers")
	// Launch one workers to process
	go wait.Until(dn.runWorker, time.Second, stopCh)
	glog.Info("Started workers")

	for {
		select {
		case <-stopCh:
			glog.V(0).Info("Run(): stop daemon")
			return nil
		case <-signalStopCh:
			glog.V(0).Info("Run(): signal received, stop daemon")
			return nil
		case err, more := <-exitCh:
			glog.Warningf("Got an error: %v", err)
			if more {
				dn.refreshCh <- Message{
					syncStatus:    syncStatusFailed,
					lastSyncError: err.Error(),
				}
			}
			return err
		case <-time.After(30 * time.Second):
			glog.V(2).Info("Run(): period refresh")
			if err := dn.tryCreateUdevRuleWrapper(); err != nil {
				glog.V(2).Info("Could not create udev rule: ", err)
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
	glog.V(2).Infof("worker queue size: %d", dn.workqueue.Len())
	obj, shutdown := dn.workqueue.Get()
	if shutdown {
		return false
	}

	glog.V(2).Infof("get item: %d", obj.(int64))

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
				syncStatus:    syncStatusFailed,
				lastSyncError: err.Error(),
			}
			<-dn.syncCh
			dn.workqueue.AddRateLimited(key)
			return fmt.Errorf("error syncing: %s, requeuing", err.Error())
		}
		// Finally, if no error occurs we Forget this item so it does not
		// get queued again until another change happens.
		dn.workqueue.Forget(obj)
		glog.Infof("Successfully synced")
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
	node, err := dn.nodeLister.Get(dn.name)
	if errors.IsNotFound(err) {
		glog.V(2).Infof("nodeUpdateHandler(): node %v has been deleted", dn.name)
		return
	}
	dn.node = node.DeepCopy()
	nodes, err := dn.nodeLister.List(labels.Everything())
	if err != nil {
		return
	}
	for _, node := range nodes {
		if node.GetName() != dn.name && node.Annotations[annoKey] == annoDraining {
			dn.drainable = false
			return
		}
	}
	dn.drainable = true
}

func (dn *Daemon) operatorConfigAddHandler(obj interface{}) {
	dn.operatorConfigChangeHandler(&sriovnetworkv1.SriovOperatorConfig{}, obj)
}

func (dn *Daemon) operatorConfigChangeHandler(old, new interface{}) {
	newCfg := new.(*sriovnetworkv1.SriovOperatorConfig)
	var level = glog.Level(newCfg.Spec.LogLevel)
	if level != flag.Lookup("v").Value.(flag.Getter).Get() {
		glog.Infof("Set log verbose level to: %d", level)
		flag.Set("v", level.String())
	}
	newDisableDrain := newCfg.Spec.DisableDrain
	if dn.disableDrain != newDisableDrain {
		dn.disableDrain = newDisableDrain
		glog.Infof("Set Disable Drain to: %t", dn.disableDrain)
	}
}

func (dn *Daemon) nodeStateSyncHandler() error {
	var err error
	// Get the latest NodeState
	var latestState *sriovnetworkv1.SriovNetworkNodeState
	latestState, err = dn.client.SriovnetworkV1().SriovNetworkNodeStates(namespace).Get(context.Background(), dn.name, metav1.GetOptions{})
	if err != nil {
		glog.Warningf("nodeStateSyncHandler(): Failed to fetch node state %s: %v", dn.name, err)
		return err
	}
	latest := latestState.GetGeneration()
	glog.V(0).Infof("nodeStateSyncHandler(): new generation is %d", latest)

	if dn.nodeState.GetGeneration() == latest {
		glog.V(0).Infof("nodeStateSyncHandler(): Interface not changed")
		if latestState.Status.LastSyncError != "" ||
			latestState.Status.SyncStatus != syncStatusSucceeded {
			dn.refreshCh <- Message{
				syncStatus:    syncStatusSucceeded,
				lastSyncError: "",
			}
			// wait for writer to refresh the status
			<-dn.syncCh
		}

		return nil
	}

	if latestState.GetGeneration() == 1 && len(latestState.Spec.Interfaces) == 0 {
		glog.V(0).Infof("nodeStateSyncHandler(): Name: %s, Interface policy spec not yet set by controller", latestState.Name)
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
		syncStatus:    "InProgress",
		lastSyncError: "",
	}

	// load plugins if has not loaded
	if len(dn.enabledPlugins) == 0 {
		dn.enabledPlugins, err = enablePlugins(dn.platform, latestState)
		if err != nil {
			glog.Errorf("nodeStateSyncHandler(): failed to enable vendor plugins error: %v", err)
			return err
		}
	}

	reqReboot := false
	reqDrain := false
	for k, p := range dn.enabledPlugins {
		d, r := false, false
		if dn.nodeState.GetName() == "" {
			glog.V(0).Infof("nodeStateSyncHandler(): calling OnNodeStateChange for a new node state")
		} else {
			glog.V(0).Infof("nodeStateSyncHandler(): calling OnNodeStateChange for an updated node state")
		}
		d, r, err = p.OnNodeStateChange(latestState)
		if err != nil {
			glog.Errorf("nodeStateSyncHandler(): plugin %s error: %v", k, err)
			return err
		}
		glog.V(0).Infof("nodeStateSyncHandler(): plugin %s: reqDrain %v, reqReboot %v", k, d, r)
		reqDrain = reqDrain || d
		reqReboot = reqReboot || r
	}
	glog.V(0).Infof("nodeStateSyncHandler(): reqDrain %v, reqReboot %v disableDrain %v", reqDrain, reqReboot, dn.disableDrain)

	for k, p := range dn.enabledPlugins {
		if k != GenericPluginName {
			err := p.Apply()
			if err != nil {
				glog.Errorf("nodeStateSyncHandler(): plugin %s fail to apply: %v", k, err)
				return err
			}
		}
	}

	if reqDrain {
		if !dn.isNodeDraining() {
			if !dn.disableDrain {
				ctx, cancel := context.WithCancel(context.TODO())
				defer cancel()

				glog.Infof("nodeStateSyncHandler(): get drain lock for sriov daemon")
				done := make(chan bool)
				go dn.getDrainLock(ctx, done)
				<-done
			}
		}

		glog.Info("nodeStateSyncHandler(): drain node")
		if err := dn.drainNode(); err != nil {
			return err
		}
	}

	if !reqReboot {
		selectedPlugin, ok := dn.enabledPlugins[GenericPluginName]
		if ok {
			// Apply generic_plugin last
			err = selectedPlugin.Apply()
			if err != nil {
				glog.Errorf("nodeStateSyncHandler(): generic_plugin fail to apply: %v", err)
				return err
			}
		}
	}

	if reqReboot {
		glog.Info("nodeStateSyncHandler(): reboot node")
		if err := rebootNode(); err != nil {
			glog.Errorf("nodeStateSyncHandler(): failed to reboot the node: %v", err)
			return err
		}
		// If reboot is delayed we need to wait to be killed via SIGTERM/SIGKILL
		time.Sleep(defaultRebootTimeout)
		return fmt.Errorf("failed to reboot")
	}

	// restart device plugin pod
	glog.Info("nodeStateSyncHandler(): restart device plugin pod")
	if err := dn.restartDevicePluginPod(); err != nil {
		glog.Errorf("nodeStateSyncHandler(): fail to restart device plugin pod: %v", err)
		return err
	}
	if dn.isNodeDraining() {
		if err := dn.completeDrain(); err != nil {
			glog.Errorf("nodeStateSyncHandler(): failed to complete draining: %v", err)
			return err
		}
	} else {
		if !dn.nodeHasAnnotation(annoKey, annoIdle) {
			if err := dn.annotateNode(dn.name, annoIdle); err != nil {
				glog.Errorf("nodeStateSyncHandler(): failed to annotate node: %v", err)
				return err
			}
		}
	}
	glog.Info("nodeStateSyncHandler(): sync succeeded")
	dn.nodeState = latestState.DeepCopy()
	dn.refreshCh <- Message{
		syncStatus:    syncStatusSucceeded,
		lastSyncError: "",
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

func (dn *Daemon) isNodeDraining() bool {
	if anno, ok := dn.node.Annotations[annoKey]; ok && anno == annoDraining {
		return true
	}
	return false
}

func (dn *Daemon) completeDrain() error {
	if !dn.disableDrain {
		if err := drain.RunCordonOrUncordon(dn.drainer, dn.node, false); err != nil {
			return err
		}
	}

	if err := dn.annotateNode(dn.name, annoIdle); err != nil {
		glog.Errorf("completeDrain(): failed to annotate node: %v", err)
		return err
	}
	dn.disableUpdatingFlag()
	return nil
}

func (dn *Daemon) restartDevicePluginPod() error {
	dn.mu.Lock()
	defer dn.mu.Unlock()
	glog.V(2).Infof("restartDevicePluginPod(): try to restart device plugin pod")

	var podToDelete string
	pods, err := dn.kubeClient.CoreV1().Pods(namespace).List(context.Background(), metav1.ListOptions{
		LabelSelector: "app=sriov-device-plugin",
		FieldSelector: "spec.nodeName=" + dn.name,
	})
	if err != nil {
		if errors.IsNotFound(err) {
			glog.Info("restartDevicePluginPod(): device plugin pod exited")
			return nil
		}
		glog.Warningf("restartDevicePluginPod(): Failed to list device plugin pod: %s, retrying", err)
		return err
	}

	if len(pods.Items) == 0 {
		glog.Info("restartDevicePluginPod(): device plugin pod exited")
		return nil
	}
	podToDelete = pods.Items[0].Name

	glog.V(2).Infof("restartDevicePluginPod(): Found device plugin pod %s, deleting it", podToDelete)
	err = dn.kubeClient.CoreV1().Pods(namespace).Delete(context.Background(), podToDelete, metav1.DeleteOptions{})
	if errors.IsNotFound(err) {
		glog.Info("restartDevicePluginPod(): pod to delete not found")
		return nil
	}
	if err != nil {
		glog.Errorf("restartDevicePluginPod(): Failed to delete device plugin pod: %s, retrying", err)
		return err
	}

	if err := wait.PollImmediateUntil(3*time.Second, func() (bool, error) {
		_, err := dn.kubeClient.CoreV1().Pods(namespace).Get(context.Background(), podToDelete, metav1.GetOptions{})
		if errors.IsNotFound(err) {
			glog.Info("restartDevicePluginPod(): device plugin pod exited")
			return true, nil
		}

		if err != nil {
			glog.Warningf("restartDevicePluginPod(): Failed to check for device plugin exit: %s, retrying", err)
		} else {
			glog.Infof("restartDevicePluginPod(): waiting for device plugin %s to exit", podToDelete)
		}
		return false, nil
	}, dn.stopCh); err != nil {
		glog.Errorf("restartDevicePluginPod(): failed to wait for checking pod deletion: %v", err)
		return err
	}

	return nil
}

func rebootNode() error {
	glog.Infof("rebootNode(): trigger node reboot")
	exit, err := utils.Chroot("/host")
	if err != nil {
		glog.Errorf("rebootNode(): %v", err)
		return err
	}
	defer exit()
	// creates a new transient systemd unit to reboot the system.
	// With the upstream implementation of kubelet graceful shutdown feature,
	// we don't explicitly stop the kubelet so that kubelet can gracefully shutdown
	// pods when `GracefulNodeShutdown` feature gate is enabled.
	// kubelet uses systemd inhibitor locks to delay node shutdown to terminate pods.
	// https://kubernetes.io/docs/concepts/architecture/nodes/#graceful-node-shutdown
	cmd := exec.Command("systemd-run", "--unit", "sriov-network-config-daemon-reboot",
		"--description", "sriov-network-config-daemon reboot node", "/bin/sh", "-c", "systemctl reboot")

	return cmd.Run()
}

func (dn *Daemon) annotateNode(node, value string) error {
	glog.Infof("annotateNode(): Annotate node %s with: %s", node, value)

	oldNode, err := dn.kubeClient.CoreV1().Nodes().Get(context.Background(), dn.name, metav1.GetOptions{})
	if err != nil {
		glog.Infof("annotateNode(): Failed to get node %s %v, retrying", node, err)
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
			dn.name,
			types.StrategicMergePatchType,
			patchBytes,
			metav1.PatchOptions{})
		if err != nil {
			glog.Infof("annotateNode(): Failed to patch node %s: %v", node, err)
			return err
		}
	}
	return nil
}

func (dn *Daemon) getDrainLock(ctx context.Context, done chan bool) {
	var err error

	lock := &resourcelock.LeaseLock{
		LeaseMeta: metav1.ObjectMeta{
			Name:      "config-daemon-draining-lock",
			Namespace: namespace,
		},
		Client: dn.kubeClient.CoordinationV1(),
		LockConfig: resourcelock.ResourceLockConfig{
			Identity: dn.name,
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
				glog.V(2).Info("getDrainLock(): started leading")
				for {
					time.Sleep(3 * time.Second)
					if dn.drainable {
						glog.V(2).Info("getDrainLock(): no other node is draining")
						err = dn.annotateNode(dn.name, annoDraining)
						if err != nil {
							glog.Errorf("getDrainLock(): Failed to annotate node: %v", err)
							continue
						}
						done <- true
						return
					}
					glog.V(2).Info("getDrainLock(): other node is draining, wait...")
				}
			},
			OnStoppedLeading: func() {
				glog.V(2).Info("getDrainLock(): stopped leading")
			},
		},
	})
}

func (dn *Daemon) drainNode() error {
	if dn.disableDrain {
		glog.Info("drainNode(): disable drain is true skipping drain")
		return nil
	}

	glog.Info("drainNode(): Update prepared")
	var err error

	backoff := wait.Backoff{
		Steps:    5,
		Duration: 10 * time.Second,
		Factor:   2,
	}
	var lastErr error

	glog.Info("drainNode(): Start draining")
	if err = wait.ExponentialBackoff(backoff, func() (bool, error) {
		err := drain.RunCordonOrUncordon(dn.drainer, dn.node, true)
		if err != nil {
			lastErr = err
			glog.Infof("Cordon failed with: %v, retrying", err)
			return false, nil
		}
		err = drain.RunNodeDrain(dn.drainer, dn.name)
		if err == nil {
			return true, nil
		}
		lastErr = err
		glog.Infof("Draining failed with: %v, retrying", err)
		return false, nil
	}); err != nil {
		if err == wait.ErrWaitTimeout {
			glog.Errorf("drainNode(): failed to drain node (%d tries): %v :%v", backoff.Steps, err, lastErr)
		}
		glog.Errorf("drainNode(): failed to drain node: %v", err)
		return err
	}
	dn.enableUpdatingFlag()
	glog.Info("drainNode(): drain complete")
	return nil
}

func (dn *Daemon) getUpdatingFlag() bool {
	dn.updatingFlagMutex.Lock()
	defer dn.updatingFlagMutex.Unlock()
	return dn.updatingFlag
}

func (dn *Daemon) enableUpdatingFlag() {
	dn.updatingFlagMutex.Lock()
	defer dn.updatingFlagMutex.Unlock()
	glog.Info("enableUpdatingFlag(): Setting flag to true")
	dn.updatingFlag = true
}

func (dn *Daemon) disableUpdatingFlag() {
	dn.updatingFlagMutex.Lock()
	defer dn.updatingFlagMutex.Unlock()
	glog.Info("disableUpdatingFlag(): Setting flag to false")
	dn.updatingFlag = false
}

func (dn *Daemon) installSignalHandler(signalStopCh chan struct{}) {
	termChan := make(chan os.Signal, 2048)
	signal.Notify(termChan, syscall.SIGTERM)

	// Catch SIGTERM - if we're actively updating, we should avoid
	// having the process be killed.
	go func() {
		for sig := range termChan {
			//nolint:gocritic
			switch sig {
			case syscall.SIGTERM:
				updateActive := dn.getUpdatingFlag()
				if updateActive {
					glog.Info("Got SIGTERM, but actively updating")
				} else {
					glog.Info("Got SIGTERM, shutting down")
					close(signalStopCh)
					return
				}
			}
		}
	}()
}

func tryEnableTun() {
	if err := utils.LoadKernelModule("tun"); err != nil {
		glog.Errorf("tryEnableTun(): TUN kernel module not loaded: %v", err)
	}
}

func tryEnableVhostNet() {
	if err := utils.LoadKernelModule("vhost_net"); err != nil {
		glog.Errorf("tryEnableVhostNet(): VHOST_NET kernel module not loaded: %v", err)
	}
}

func tryEnableRdma() (bool, error) {
	glog.V(2).Infof("tryEnableRdma()")
	var stdout, stderr bytes.Buffer

	cmd := exec.Command("/bin/bash", path.Join(filesystemRoot, rdmaScriptsPath))
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

func tryCreateSwitchdevUdevRule(nodeState *sriovnetworkv1.SriovNetworkNodeState) error {
	glog.V(2).Infof("tryCreateSwitchdevUdevRule()")
	var newContent string
	filePath := path.Join(filesystemRoot, "/host/etc/udev/rules.d/20-switchdev.rules")

	for _, ifaceStatus := range nodeState.Status.Interfaces {
		if ifaceStatus.EswitchMode == sriovnetworkv1.ESwithModeSwitchDev {
			switchID, err := utils.GetPhysSwitchID(ifaceStatus.Name)
			if err != nil {
				return err
			}
			portName, err := utils.GetPhysPortName(ifaceStatus.Name)
			if err != nil {
				return err
			}
			newContent = newContent + fmt.Sprintf("SUBSYSTEM==\"net\", ACTION==\"add|move\", ATTRS{phys_switch_id}==\"%s\", ATTR{phys_port_name}==\"pf%svf*\", IMPORT{program}=\"/etc/udev/switchdev-vf-link-name.sh $attr{phys_port_name}\", NAME=\"%s_$env{NUMBER}\"\n", switchID, strings.TrimPrefix(portName, "p"), ifaceStatus.Name)
		}
	}

	oldContent, err := ioutil.ReadFile(filePath)
	// if oldContent = newContent, don't do anything
	if err == nil && newContent == string(oldContent) {
		return nil
	}

	glog.V(2).Infof("Old udev content '%v' and new content '%v' differ. Writing to file %v.",
		strings.TrimSuffix(string(oldContent), "\n"),
		strings.TrimSuffix(newContent, "\n"),
		filePath)

	// if the file does not exist or if oldContent != newContent
	// write to file and create it if it doesn't exist
	err = ioutil.WriteFile(filePath, []byte(newContent), 0664)
	if err != nil {
		glog.Errorf("tryCreateSwitchdevUdevRule(): fail to write file: %v", err)
		return err
	}

	var stdout, stderr bytes.Buffer
	cmd := exec.Command("/bin/bash", path.Join(filesystemRoot, udevScriptsPath))
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return err
	}
	glog.V(2).Infof("tryCreateSwitchdevUdevRule(): %v", cmd.Stdout)

	i, err := strconv.Atoi(strings.TrimSpace(stdout.String()))
	if err == nil {
		if i == 0 {
			glog.V(2).Infof("tryCreateSwitchdevUdevRule(): switchdev udev rules loaded")
		} else {
			glog.V(2).Infof("tryCreateSwitchdevUdevRule(): switchdev udev rules not loaded")
		}
	}
	return nil
}

func tryCreateNMUdevRule() error {
	glog.V(2).Infof("tryCreateNMUdevRule()")
	dirPath := path.Join(filesystemRoot, "/host/etc/udev/rules.d")
	filePath := path.Join(dirPath, "10-nm-unmanaged.rules")

	newContent := fmt.Sprintf("ACTION==\"add|change|move\", ATTRS{device}==\"%s\", ENV{NM_UNMANAGED}=\"1\"\n", strings.Join(sriovnetworkv1.GetSupportedVfIds(), "|"))

	// add NM udev rules for renaming VF rep
	newContent = newContent + "SUBSYSTEM==\"net\", ACTION==\"add|move\", ATTRS{phys_switch_id}!=\"\", ATTR{phys_port_name}==\"pf*vf*\", ENV{NM_UNMANAGED}=\"1\"\n"

	oldContent, err := ioutil.ReadFile(filePath)
	// if oldContent = newContent, don't do anything
	if err == nil && newContent == string(oldContent) {
		return nil
	}

	glog.V(2).Infof("Old udev content '%v' and new content '%v' differ. Writing to file %v.",
		strings.TrimSuffix(string(oldContent), "\n"),
		strings.TrimSuffix(newContent, "\n"),
		filePath)

	err = os.MkdirAll(dirPath, os.ModePerm)
	if err != nil && !os.IsExist(err) {
		glog.Errorf("tryCreateNMUdevRule(): failed to create dir %s: %v", dirPath, err)
		return err
	}

	// if the file does not exist or if oldContent != newContent
	// write to file and create it if it doesn't exist
	err = ioutil.WriteFile(filePath, []byte(newContent), 0666)
	if err != nil {
		glog.Errorf("tryCreateNMUdevRule(): fail to write file: %v", err)
		return err
	}
	return nil
}
