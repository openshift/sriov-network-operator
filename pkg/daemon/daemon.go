package daemon

import (
	"bytes"
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
	drain "github.com/openshift/kubernetes-drain"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/strategicpatch"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	listerv1 "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/cache"
	// "k8s.io/client-go/kubernetes/scheme"
	sriovnetworkv1 "github.com/openshift/sriov-network-operator/pkg/apis/sriovnetwork/v1"
	snclientset "github.com/openshift/sriov-network-operator/pkg/client/clientset/versioned"
	sninformer "github.com/openshift/sriov-network-operator/pkg/client/informers/externalversions"
	"github.com/openshift/sriov-network-operator/pkg/utils"
)

type Message struct {
	syncStatus    string
	lastSyncError string
}

type Daemon struct {
	// name is the node name.
	name      string
	namespace string

	client snclientset.Interface
	// kubeClient allows interaction with Kubernetes, including the node we are running on.
	kubeClient *kubernetes.Clientset

	nodeState *sriovnetworkv1.SriovNetworkNodeState

	LoadedPlugins map[string]VendorPlugin

	// channel used by callbacks to signal Run() of an error
	exitCh chan<- error

	// channel used to ensure all spawned goroutines exit when we exit.
	stopCh <-chan struct{}

	refreshCh chan<- Message

	dpReboot bool

	mu *sync.Mutex
}

const (
	scriptsPath  = "/bindata/scripts/enable-rdma.sh"
	annoKey      = "sriovnetwork.openshift.io/state"
	annoIdle     = "Idle"
	annoDraining = "Draining"
)

var namespace = os.Getenv("NAMESPACE")
var pluginsPath = os.Getenv("PLUGINSPATH")

func New(
	nodeName string,
	client snclientset.Interface,
	kubeClient *kubernetes.Clientset,
	exitCh chan<- error,
	stopCh <-chan struct{},
	refreshCh chan<- Message,
) *Daemon {
	return &Daemon{
		name:       nodeName,
		client:     client,
		kubeClient: kubeClient,
		exitCh:     exitCh,
		stopCh:     stopCh,
		refreshCh:  refreshCh,
	}
}

func (dn *Daemon) Run(stopCh <-chan struct{}, exitCh <-chan error) error {
	glog.V(0).Info("Run(): start daemon")
	// Only watch own SriovNetworkNodeState CR

	tryEnableRdma()
	if err := tryCreateUdevRule(); err != nil {
		return err
	}
	var timeout int64 = 5
	dn.mu = &sync.Mutex{}
	informerFactory := sninformer.NewFilteredSharedInformerFactory(dn.client,
		time.Second*15,
		namespace,
		func(lo *v1.ListOptions) {
			lo.FieldSelector = "metadata.name=" + dn.name
			lo.TimeoutSeconds = &timeout
		},
	)

	informer := informerFactory.Sriovnetwork().V1().SriovNetworkNodeStates().Informer()
	informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    dn.nodeStateAddHandler,
		UpdateFunc: dn.nodeStateChangeHandler,
	})

	cfgInformerFactory := sninformer.NewFilteredSharedInformerFactory(dn.client,
		time.Second*30,
		namespace,
		func(lo *v1.ListOptions) {
			lo.FieldSelector = "metadata.name=" + "default"
		},
	)

	cfgInformer := cfgInformerFactory.Sriovnetwork().V1().SriovOperatorConfigs().Informer()
	cfgInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    dn.operatorConfigAddHandler,
		UpdateFunc: dn.operatorConfigChangeHandler,
	})

	time.Sleep(5 * time.Second)
	go informer.Run(dn.stopCh)
	go cfgInformer.Run(dn.stopCh)

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
		}
	}
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
}

func (dn *Daemon) nodeStateAddHandler(obj interface{}) {
	// "k8s.io/apimachinery/pkg/apis/meta/v1" provides an Object
	// interface that allows us to get metadata easily
	nodeState := obj.(*sriovnetworkv1.SriovNetworkNodeState)
	glog.V(2).Infof("nodeStateAddHandler(): New SriovNetworkNodeState Added to Store: %s", nodeState.GetName())
	dn.refreshCh <- Message{
		syncStatus:    "InProgress",
		lastSyncError: "",
	}
	err := dn.loadVendorPlugins(nodeState)
	if err != nil {
		glog.Errorf("nodeStateAddHandler(): failed to load vendor plugin: %v", err)
		dn.exitCh <- err
		return
	}

	reqReboot := false
	reqDrain := false

	for k, p := range dn.LoadedPlugins {
		d, r, err := p.OnNodeStateAdd(nodeState)
		if err != nil {
			glog.Errorf("nodeStateAddHandler(): plugin %s error: %v", k, err)
			dn.exitCh <- err
			return
		}
		reqDrain = reqDrain || d
		reqReboot = reqReboot || r
	}
	glog.V(2).Infof("nodeStateAddHandler(): reqDrain %v, reqReboot %v", reqDrain, reqReboot)

	if reqDrain {
		glog.Info("nodeStateAddHandler(): drain node")
		dn.drainNode(nodeState.GetName())
		defer dn.uncordon(nodeState.GetName())
	}
	for k, p := range dn.LoadedPlugins {
		if k != GenericPlugin {
			err := p.Apply()
			if err != nil {
				glog.Errorf("nodeStateAddHandler(): plugin %s fail to apply: %v", k, err)
				dn.exitCh <- err
				return
			}
		}
	}

	if len(dn.LoadedPlugins) > 1 && !reqReboot {
		// Apply generic_plugin last
		err = dn.LoadedPlugins[GenericPlugin].Apply()
		if err != nil {
			glog.Errorf("nodeStateAddHandler(): generic_plugin fail to apply: %v", err)
			dn.exitCh <- err
			return
		}
	}

	if reqReboot {
		glog.Info("nodeStateAddHandler(): reboot node")
		rebootNode()
		return
	}

	// restart device plugin pod
	if reqDrain {
		if err := dn.restartDevicePluginPod(); err != nil {
			glog.Errorf("nodeStateAddHandler(): fail to restart device plugin pod: %v", err)
			dn.exitCh <- err
			return
		}
	}

	dn.refreshCh <- Message{
		syncStatus:    "Succeeded",
		lastSyncError: "",
	}
}

func (dn *Daemon) uncordon(name string) {
	glog.Info("uncordon(): uncordon node")
	node, err := dn.kubeClient.CoreV1().Nodes().Get(name, metav1.GetOptions{})
	if err != nil {
		glog.Errorf("uncordon(): failed to get node: %v", err)
		dn.exitCh <- err
		return
	}
	backoff := wait.Backoff{
		Steps:    5,
		Duration: 10 * time.Second,
		Factor:   2,
	}
	var lastErr error

	if err := wait.ExponentialBackoff(backoff, func() (bool, error) {
		err := drain.Uncordon(dn.kubeClient.CoreV1().Nodes(), node, nil)
		if err == nil {
			return true, nil
		}
		lastErr = err
		glog.Infof("uncordon(): uncordon failed with: %v, retrying", err)
		return false, nil
	}); err != nil {
		if err == wait.ErrWaitTimeout {
			glog.Errorf("uncordon(): failed to uncordon node (%d tries): %v :%v", backoff.Steps, err, lastErr)
		}
		glog.Errorf("uncordon(): failed to uncordon node: %v", err)
	}
	glog.Info("uncordon(): uncordon complete")
}

func (dn *Daemon) nodeStateChangeHandler(old, new interface{}) {
	var err error
	newState := new.(*sriovnetworkv1.SriovNetworkNodeState)
	oldState := old.(*sriovnetworkv1.SriovNetworkNodeState)
	glog.V(2).Infof("nodeStateChangeHandler(): current generation is %d", newState.GetObjectMeta().GetGeneration())
	if newState.GetObjectMeta().GetGeneration() == oldState.GetObjectMeta().GetGeneration() {
		glog.V(2).Infof("nodeStateChangeHandler(): Interface not changed")
		return
	}
	dn.refreshCh <- Message{
		syncStatus:    "InProgress",
		lastSyncError: "",
	}
	reqReboot := false
	reqDrain := false

	for k, p := range dn.LoadedPlugins {
		d, r, err := p.OnNodeStateChange(oldState, newState)
		if err != nil {
			glog.Errorf("nodeStateChangeHandler(): plugin %s error: %v", k, err)
			dn.exitCh <- err
			return
		}
		reqDrain = reqDrain || d
		reqReboot = reqReboot || r
	}
	glog.V(2).Infof("nodeStateChangeHandler(): reqDrain %v, reqReboot %v", reqDrain, reqReboot)

	if reqDrain {
		glog.Info("nodeStateChangeHandler(): drain node")
		dn.drainNode(newState.GetName())
		defer dn.uncordon(newState.GetName())
	}
	for k, p := range dn.LoadedPlugins {
		if k != GenericPlugin {
			err := p.Apply()
			if err != nil {
				glog.Errorf("nodeStateChangeHandler(): plugin %s fail to apply: %v", k, err)
				dn.exitCh <- err
				return
			}
		}
	}

	if len(dn.LoadedPlugins) > 1 && !reqReboot {
		// Apply generic_plugin last
		err = dn.LoadedPlugins[GenericPlugin].Apply()
		if err != nil {
			glog.Errorf("nodeStateChangeHandler(): generic_plugin fail to apply: %v", err)
			dn.exitCh <- err
			return
		}
	}

	if reqReboot {
		glog.Info("nodeStateChangeHandler(): reboot node")
		go rebootNode()
		return
	}

	// restart device plugin pod
	if reqDrain || (newState.Spec.DpConfigVersion != oldState.Spec.DpConfigVersion) {
		glog.Info("nodeStateChangeHandler(): restart device plugin pod")
		if err := dn.restartDevicePluginPod(); err != nil {
			glog.Errorf("nodeStateChangeHandler(): fail to restart device plugin pod: %v", err)
			dn.exitCh <- err
			return
		}
	}
	dn.refreshCh <- Message{
		syncStatus:    "Succeeded",
		lastSyncError: "",
	}
}

func (dn *Daemon) restartDevicePluginPod() error {
	dn.mu.Lock()
	defer dn.mu.Unlock()
	glog.V(2).Infof("restartDevicePluginPod(): try to restart device plugin pod")
	pods, err := dn.kubeClient.CoreV1().Pods(namespace).List(metav1.ListOptions{
		LabelSelector: "app=sriov-device-plugin",
		FieldSelector: "spec.nodeName=" + dn.name,
	})
	if err != nil {
		glog.Warningf("restartDevicePluginPod(): Failed to list device plugin pod: %s", err)
		return nil
	}
	if len(pods.Items) > 0 {
		glog.V(2).Infof("restartDevicePluginPod(): Found device plugin pod %s", pods.Items[0].GetName())
		err = dn.kubeClient.CoreV1().Pods(namespace).Delete(pods.Items[0].GetName(), &metav1.DeleteOptions{})
		if err != nil {
			glog.Errorf("restartDevicePluginPod(): Failed to delete device plugin pod: %s", err)
			return err
		}

		var lastErr error

		if err := wait.PollImmediateUntil(3*time.Second, func() (bool, error) {
			_, err := dn.kubeClient.CoreV1().Pods(namespace).Get(pods.Items[0].GetName(), metav1.GetOptions{})
			if err != nil {
				if errors.IsNotFound(err) {
					return true, nil
				}
				lastErr = err
				glog.Infof("restartDevicePluginPod(): unexpected error: %v, retrying", err)
				return false, err
			}
			glog.Info("restartDevicePluginPod(): waiting for pod get deleted")
			return false, nil
		}, dn.stopCh); err != nil {
			glog.Errorf("restartDevicePluginPod(): failed to wait for pod deletion complete: %v", err)
		}
	}

	return nil
}

func (dn *Daemon) loadVendorPlugins(ns *sriovnetworkv1.SriovNetworkNodeState) error {
	pl := registerPlugins(ns)
	pl = append(pl, GenericPlugin)
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
		glog.Error("rebootNode(): %v", err)
	}
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
		glog.Error("failed to reboot node: %v", err)
	}
	if err := exit(); err != nil {
		glog.Error("rebootNode(): %v", err)
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

func (dn *Daemon) annotateNode(node, value string, lister listerv1.NodeLister) error {
	glog.Infof("annotateNode(): Annotate node %s with: %s", node, value)
	oldNode, err := lister.Get(dn.name)
	if err != nil {
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
		_, err = dn.kubeClient.CoreV1().Nodes().Patch(dn.name, types.StrategicMergePatchType, patchBytes)
		if err != nil {
			return err
		}
	}
	return nil
}

func (dn *Daemon) drainNode(name string) {
	glog.Info("drainNode(): Update prepared")
	node, err := dn.kubeClient.CoreV1().Nodes().Get(name, metav1.GetOptions{})
	if err != nil {
		glog.Errorf("drainNode(): failed to get node: %v", err)
	}
	rand.Seed(time.Now().UnixNano())
	informerFactory := informers.NewSharedInformerFactory(dn.kubeClient,
		time.Second*15,
	)
	stop := make(chan struct{})
	lister := informerFactory.Core().V1().Nodes().Lister()
	informer := informerFactory.Core().V1().Nodes().Informer()
	informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		UpdateFunc: func(oldObj, newObj interface{}) {
			nn := newObj.(*corev1.Node)
			if nn.Annotations[annoKey] == annoDraining || nn.GetName() == dn.name {
				return
			}
			ok := true
			// wait a random time to avoid all the nodes checking the nodes anno at the same time
			time.Sleep(time.Duration(rand.Intn(6000)) * time.Millisecond)
			nodes, err := lister.List(labels.Everything())
			if err != nil {
				return
			}
			glog.Infof("drainNode(): Check if any other node is draining")
			for _, node := range nodes {
				if node.GetName() != dn.name && node.Annotations[annoKey] == annoDraining {
					glog.Infof("drainNode(): node %s is draining", node.Name)
					ok = false
				}
			}
			if ok {
				glog.Info("drainNode(): No other node is draining, stop watching")
				select {
				case <-stop:
				default:
					close(stop)
				}
			}
		},
	})
	informer.Run(stop)

	err = dn.annotateNode(dn.name, annoDraining, lister)
	if err != nil {
		glog.Errorf("drainNode(): Failed to annotate node: %v", err)
	}

	backoff := wait.Backoff{
		Steps:    5,
		Duration: 10 * time.Second,
		Factor:   2,
	}
	var lastErr error

	logger := GlogLogger{}

	glog.Info("drainNode(): Start draining")
	if err := wait.ExponentialBackoff(backoff, func() (bool, error) {
		err := drain.Drain(dn.kubeClient, []*corev1.Node{node}, &drain.DrainOptions{
			DeleteLocalData:    true,
			Force:              true,
			GracePeriodSeconds: -1,
			IgnoreDaemonsets:   true,
			Logger:             logger,
		})
		if err == nil {
			return true, nil
		}
		lastErr = err
		glog.Infof("drainNode(): Draining failed with: %v, retrying", err)
		return false, nil
	}); err != nil {
		if err == wait.ErrWaitTimeout {
			glog.Errorf("drainNode(): failed to drain node (%d tries): %v :%v", backoff.Steps, err, lastErr)
		}
		glog.Errorf("drainNode(): failed to drain node: %v", err)
	}
	glog.Info("drainNode(): drain complete")
	err = dn.annotateNode(dn.name, annoIdle, lister)
	if err != nil {
		glog.Errorf("drainNode(): failed to annotate node: %v", err)
	}
}

func needRestartDevicePlugin(oldState, newState *sriovnetworkv1.SriovNetworkNodeState) bool {
	var found bool
	for _, in := range newState.Spec.Interfaces {
		found = false
		for _, io := range oldState.Spec.Interfaces {
			if in.PciAddress == io.PciAddress {
				found = true
				if in.NumVfs != io.NumVfs {
					return true
				}
			}
		}
		if !found {
			return true
		}
	}
	return false
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
