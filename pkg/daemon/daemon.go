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
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/golang/glog"
	"golang.org/x/time/rate"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/api/core/v1"
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
	"k8s.io/client-go/util/workqueue"
	"k8s.io/kubectl/pkg/drain"

	// "k8s.io/client-go/kubernetes/scheme"
	sriovnetworkv1 "github.com/openshift/sriov-network-operator/api/v1"
	snclientset "github.com/openshift/sriov-network-operator/pkg/client/clientset/versioned"
	sninformer "github.com/openshift/sriov-network-operator/pkg/client/informers/externalversions"
	"github.com/openshift/sriov-network-operator/pkg/utils"
)

const (
	// updateDelay is the baseline speed at which we react to changes.  We don't
	// need to react in milliseconds as any change would involve rebooting the node.
	updateDelay = 5 * time.Second
	// maxUpdateBackoff is the maximum time to react to a change as we back off
	// in the face of errors.
	maxUpdateBackoff = 60 * time.Second

	UNSUPPORTED_NIC_ID_CONFIGMAP = "unsupported-nic-ids"
)

type Message struct {
	syncStatus    string
	lastSyncError string
}

type Daemon struct {
	// name is the node name.
	name      string
	namespace string

	platform utils.PlatformType

	client snclientset.Interface
	// kubeClient allows interaction with Kubernetes, including the node we are running on.
	kubeClient *kubernetes.Clientset

	nodeState *sriovnetworkv1.SriovNetworkNodeState

	LoadedPlugins map[string]VendorPlugin

	// channel used by callbacks to signal Run() of an error
	exitCh chan<- error

	// channel used to ensure all spawned goroutines exit when we exit.
	stopCh <-chan struct{}

	syncCh <-chan struct{}

	refreshCh chan<- Message

	dpReboot bool

	mu *sync.Mutex

	drainer *drain.Helper

	node *corev1.Node

	drainable bool

	disableDrain bool

	nodeLister listerv1.NodeLister

	workqueue workqueue.RateLimitingInterface
}

type workItem struct {
	old, new *sriovnetworkv1.SriovNetworkNodeState
}

const (
	scriptsPath  = "/bindata/scripts/enable-rdma.sh"
	annoKey      = "sriovnetwork.openshift.io/state"
	annoIdle     = "Idle"
	annoDraining = "Draining"
)

var namespace = os.Getenv("NAMESPACE")
var pluginsPath = os.Getenv("PLUGINSPATH")

// writer implements io.Writer interface as a pass-through for klog.
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
	kubeClient *kubernetes.Clientset,
	exitCh chan<- error,
	stopCh <-chan struct{},
	syncCh <-chan struct{},
	refreshCh chan<- Message,
	platformType utils.PlatformType,
) *Daemon {
	return &Daemon{
		name:       nodeName,
		platform:   platformType,
		client:     client,
		kubeClient: kubeClient,
		exitCh:     exitCh,
		stopCh:     stopCh,
		syncCh:     syncCh,
		refreshCh:  refreshCh,
		drainable:  true,
		nodeState:  &sriovnetworkv1.SriovNetworkNodeState{},
		drainer: &drain.Helper{
			Client:              kubeClient,
			Force:               true,
			IgnoreAllDaemonSets: true,
			DeleteLocalData:     true,
			GracePeriodSeconds:  -1,
			Timeout:             90 * time.Second,
			OnPodDeletedOrEvicted: func(pod *corev1.Pod, usingEviction bool) {
				verbStr := "Deleted"
				if usingEviction {
					verbStr = "Evicted"
				}
				glog.Info(fmt.Sprintf("%s pod from Node", verbStr),
					"pod", fmt.Sprintf("%s/%s", pod.Name, pod.Namespace))
			},
			Out:    writer{glog.Info},
			ErrOut: writer{glog.Error},
		},
		workqueue: workqueue.NewNamedRateLimitingQueue(workqueue.NewMaxOfRateLimiter(
			&workqueue.BucketRateLimiter{Limiter: rate.NewLimiter(rate.Limit(updateDelay), 1)},
			workqueue.NewItemExponentialFailureRateLimiter(1*time.Second, maxUpdateBackoff)), "SriovNetworkNodeState"),
	}
}

func (dn *Daemon) getUnsupportedNicIdConfigMap() (*v1.ConfigMap, error) {
	cm, err := dn.kubeClient.CoreV1().ConfigMaps(namespace).Get(
		context.Background(),
		UNSUPPORTED_NIC_ID_CONFIGMAP,
		metav1.GetOptions{},
	)
	return cm, err
}

func (dn *Daemon) annotateUnsupportedNicIdConfigMap(cm *v1.ConfigMap, nodeName string) (*v1.ConfigMap, error) {
	jsonData, err := json.Marshal(cm.Data)
	if err != nil {
		return nil, err
	}
	cm.ObjectMeta.Annotations["openshift.io/"+nodeName] = string(jsonData)
	return dn.kubeClient.CoreV1().ConfigMaps(namespace).Update(context.Background(), cm, metav1.UpdateOptions{})
}

func (dn *Daemon) tryCreateUdevRuleWrapper() error {
	var unsupportedNicIdMap map[string]string

	// retrieve config map
	cm, configMapErr := dn.getUnsupportedNicIdConfigMap()
	if configMapErr != nil {
		glog.V(2).Info("Could not retrieve Nic Id ConfigMap: ", configMapErr)
	} else {
		unsupportedNicIdMap = cm.Data
	}

	// update udev rule and if update succeeds
	ruleWritten, err := tryCreateUdevRule(unsupportedNicIdMap)
	if err != nil {
		return err
	}

	// annotage ConfigMap if it exists
	if configMapErr == nil && ruleWritten {
		if _, err := dn.annotateUnsupportedNicIdConfigMap(cm, dn.name); err != nil {
			glog.V(2).Info("Could not update Nic Id ConfigMap", "err", err)
		}
	}
	return nil
}

// Run the config daemon
func (dn *Daemon) Run(stopCh <-chan struct{}, exitCh <-chan error) error {
	glog.V(0).Info("Run(): start daemon")
	// Only watch own SriovNetworkNodeState CR
	defer utilruntime.HandleCrash()
	defer dn.workqueue.ShutDown()

	tryEnableRdma()

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
		case err := <-exitCh:
			glog.Warningf("Got an error: %v", err)
			dn.refreshCh <- Message{
				syncStatus:    "Failed",
				lastSyncError: err.Error(),
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
	glog.V(2).Infof("get item: %d", obj.(int64))
	if shutdown {
		return false
	}

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
		var err error

		err = dn.nodeStateSyncHandler(key)
		if err != nil {
			// Ereport error message, and put the item back to work queue for retry.
			dn.refreshCh <- Message{
				syncStatus:    "Failed",
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
			glog.V(2).Infof("nodeUpdateHandler(): node %s is draining", node.Name)
			dn.drainable = false
			return
		}
	}
	glog.V(2).Infof("nodeUpdateHandler(): no other node is draining")
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

func (dn *Daemon) nodeStateSyncHandler(generation int64) error {
	var err error
	glog.V(0).Infof("nodeStateSyncHandler(): new generation is %d", generation)
	// Get the latest NodeState
	var latestState *sriovnetworkv1.SriovNetworkNodeState
	latestState, err = dn.client.SriovnetworkV1().SriovNetworkNodeStates(namespace).Get(context.Background(), dn.name, metav1.GetOptions{})
	if err != nil {
		glog.Warningf("nodeStateSyncHandler(): Failed to fetch node state %s: %v", dn.name, err)
		return err
	}
	latest := latestState.GetGeneration()
	if dn.nodeState.GetGeneration() == latest {
		glog.V(0).Infof("nodeStateSyncHandler(): Interface not changed")
		if latestState.Status.LastSyncError != "" ||
			latestState.Status.SyncStatus != "Succeeded" {
			dn.refreshCh <- Message{
				syncStatus:    "Succeeded",
				lastSyncError: "",
			}
		}

		return nil
	}

	dn.refreshCh <- Message{
		syncStatus:    "InProgress",
		lastSyncError: "",
	}

	// load plugins if has not loaded
	if len(dn.LoadedPlugins) == 0 {
		err = dn.loadVendorPlugins(latestState)
		if err != nil {
			glog.Errorf("nodeStateSyncHandler(): failed to load vendor plugin: %v", err)
			return err
		}
	}

	reqReboot := false
	reqDrain := false
	mcoReboot := false
	for k, p := range dn.LoadedPlugins {
		d, r := false, false
		if dn.nodeState.GetName() == "" {
			d, r, err = p.OnNodeStateAdd(latestState)
		} else {
			d, r, err = p.OnNodeStateChange(dn.nodeState, latestState)
		}
		if err != nil {
			glog.Errorf("nodeStateSyncHandler(): plugin %s error: %v", k, err)
			return err
		}

		if p.Name() == "mco_plugin" && d && !r {
			mcoReboot = true
		}

		glog.V(0).Infof("nodeStateSyncHandler(): plugin %s: reqDrain %v, reqReboot %v", k, d, r)
		reqDrain = reqDrain || d
		reqReboot = reqReboot || r
	}
	glog.V(0).Infof("nodeStateSyncHandler(): reqDrain %v, reqReboot %v disableDrain %v", reqDrain, reqReboot, dn.disableDrain)

	for k, p := range dn.LoadedPlugins {
		if k != GenericPlugin && k != McoPlugin {
			err := p.Apply()
			if err != nil {
				glog.Errorf("nodeStateSyncHandler(): plugin %s fail to apply: %v", k, err)
				return err
			}
		}
	}
	if mcoReboot {
		glog.V(0).Infof("nodeStateSyncHandler(): MCO will trigger reboot")
		err = dn.LoadedPlugins[McoPlugin].Apply()
		if err != nil {
			glog.Errorf("nodeStateSyncHandler(): mco_plugin fail to apply: %v", err)
			return err
		}
		return nil
	}
	if reqDrain && !dn.disableDrain {
		glog.Info("nodeStateSyncHandler(): drain node")
		if err := dn.drainNode(dn.name); err != nil {
			return err
		}
	}

	if len(dn.LoadedPlugins) > 1 && !reqReboot {
		// Apply generic_plugin last
		err = dn.LoadedPlugins[GenericPlugin].Apply()
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
	if reqDrain || latestState.Spec.DpConfigVersion != dn.nodeState.Spec.DpConfigVersion {
		glog.Info("nodeStateSyncHandler(): restart device plugin pod")
		if err := dn.restartDevicePluginPod(); err != nil {
			glog.Errorf("nodeStateSyncHandler(): fail to restart device plugin pod: %v", err)
			return err
		}
	}

	if anno, ok := dn.node.Annotations[annoKey]; ok && anno == annoDraining {
		if err := dn.completeDrain(); err != nil {
			glog.Errorf("nodeStateSyncHandler(): failed to complete draining: %v", err)
			return err
		}
	} else if !ok {
		if err := dn.annotateNode(dn.name, annoIdle); err != nil {
			glog.Errorf("nodeStateSyncHandler(): failed to annotate node: %v", err)
			return err
		}
	}
	glog.Info("nodeStateSyncHandler(): sync succeeded")
	dn.nodeState = latestState.DeepCopy()
	dn.refreshCh <- Message{
		syncStatus:    "Succeeded",
		lastSyncError: "",
	}
	// wait for writer to refresh the status
	<-dn.syncCh
	return nil
}

func (dn *Daemon) completeDrain() error {
	if err := drain.RunCordonOrUncordon(dn.drainer, dn.node, false); err != nil {
		return err
	}

	if err := dn.annotateNode(dn.name, annoIdle); err != nil {
		glog.Errorf("drainNode(): failed to annotate node: %v", err)
		return err
	}
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

func (dn *Daemon) loadVendorPlugins(ns *sriovnetworkv1.SriovNetworkNodeState) error {
	var pl []string

	if dn.platform == utils.VirtualOpenStack {
		pl = append(pl, VirtualPlugin)
	} else {
		pl = registerPlugins(ns)
		if utils.ClusterType == utils.ClusterTypeOpenshift {
			pl = append(pl, McoPlugin)
		}
		pl = append(pl, GenericPlugin)
	}

	dn.LoadedPlugins = make(map[string]VendorPlugin)

	for _, pn := range pl {
		filePath := filepath.Join(pluginsPath, pn+".so")
		glog.Infof("loadVendorPlugins(): try to load plugin %s", pn)
		p, err := loadPlugin(filePath)
		if err != nil {
			glog.Errorf("loadVendorPlugins(): fail to load plugin %s: %v", filePath, err)
			return err
		}
		dn.LoadedPlugins[p.Name()] = p
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

type GlogLogger struct {
}

func (a GlogLogger) Log(v ...interface{}) {
	glog.Info(v...)
}

func (a GlogLogger) Logf(format string, v ...interface{}) {
	glog.Infof(format, v...)
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

func (dn *Daemon) drainNode(name string) error {
	glog.Info("drainNode(): Update prepared")
	var err error
	// wait a random time to avoid all the nodes drain at the same time
	wait.PollUntil(time.Duration(rand.Intn(15)+1)*time.Second, func() (bool, error) {
		if !dn.drainable {
			glog.Info("drainNode(): other node is draining, waiting...")
		}
		return dn.drainable, nil
	}, dn.stopCh)

	err = dn.annotateNode(dn.name, annoDraining)
	if err != nil {
		glog.Errorf("drainNode(): Failed to annotate node: %v", err)
		return err
	}

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
	glog.Info("drainNode(): drain complete")
	return nil
}

func registerPlugins(ns *sriovnetworkv1.SriovNetworkNodeState) []string {
	pluginNames := make(map[string]bool)
	for _, iface := range ns.Status.Interfaces {
		if val, ok := pluginMap[iface.Vendor]; ok {
			pluginNames[val] = true
		}
	}
	rawList := reflect.ValueOf(pluginNames).MapKeys()
	glog.Infof("registerPlugins(): %v", rawList)
	nameList := make([]string, len(rawList))
	for i := 0; i < len(rawList); i++ {
		nameList[i] = rawList[i].String()
	}
	return nameList
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

func tryCreateUdevRule(unsupportedNicIdMap map[string]string) (bool, error) {
	glog.V(2).Infof("tryCreateUdevRule()")
	filePath := "/host/etc/udev/rules.d/10-nm-unmanaged.rules"

	new_content := fmt.Sprintf("ACTION==\"add|change|move\", ATTRS{device}==\"%s\", ENV{NM_UNMANAGED}=\"1\"\n", strings.Join(sriovnetworkv1.GetMergedVfIds(unsupportedNicIdMap), "|"))

	old_content, err := ioutil.ReadFile(filePath)
	// if old_content = new_content, don't do anything
	if err == nil && new_content == string(old_content) {
		return false, nil
	}

	glog.V(2).Infof("Old udev content '%v' and new content '%v' differ. Writing to file %v.",
		strings.TrimSuffix(string(old_content), "\n"),
		strings.TrimSuffix(new_content, "\n"),
		filePath)

	// if the file does not exist or if old_content != new_content
	// write to file and create it if it doesn't exist
	err = ioutil.WriteFile(filePath, []byte(new_content), 0666)
	if err != nil {
		glog.Errorf("tryCreateUdevRule(): fail to write file: %v", err)
		return false, err
	}
	return true, nil
}
