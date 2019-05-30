package daemon

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"time"

	"github.com/golang/glog"
	drain "github.com/openshift/kubernetes-drain"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	// "k8s.io/client-go/informers"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	// "k8s.io/client-go/kubernetes/scheme"
	sriovnetworkv1 "github.com/pliurh/sriov-network-operator/pkg/apis/sriovnetwork/v1"
	snclientset "github.com/pliurh/sriov-network-operator/pkg/client/clientset/versioned"
	sninformer "github.com/pliurh/sriov-network-operator/pkg/client/informers/externalversions"
)

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

	refreshCh chan<- struct{}

	dpReboot bool
}

var namespace = os.Getenv("NAMESPACE")
var pluginsPath = os.Getenv("PLUGINSPATH")

func New(
	nodeName string,
	client snclientset.Interface,
	kubeClient *kubernetes.Clientset,
	exitCh chan<- error,
	stopCh <-chan struct{},
	refreshCh chan<- struct{},
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

	informerFactory := sninformer.NewFilteredSharedInformerFactory(dn.client,
		time.Second*30,
		namespace,
		func(lo *v1.ListOptions) {
			lo.FieldSelector = "metadata.name=" + dn.name
		},
	)

	informer := informerFactory.Sriovnetwork().V1().SriovNetworkNodeStates().Informer()
	informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    dn.nodeStateAddHandler,
		UpdateFunc: dn.nodeStateChangeHandler,
	})

	go informer.Run(dn.stopCh)

	for {
		select {
		case <-stopCh:
			glog.V(0).Info("Run(): stop daemon")
			return nil
		case err := <-exitCh:
			glog.Warningf("Got an error: %v", err)
			return err
		}
	}
}

func (dn *Daemon) nodeStateAddHandler(obj interface{}) {
	// "k8s.io/apimachinery/pkg/apis/meta/v1" provides an Object
	// interface that allows us to get metadata easily
	nodeState := obj.(*sriovnetworkv1.SriovNetworkNodeState)
	glog.V(2).Infof("nodeStateAddHandler(): New SriovNetworkNodeState Added to Store: %s", nodeState.GetName())

	err := dn.loadVendorPlugins(nodeState)
	if err != nil {
		glog.Errorf("nodeStateAddHandler(): failed to load vendor plugin: %v", err)
		dn.exitCh <- err
		return
	}

	node, err := dn.kubeClient.CoreV1().Nodes().Get(nodeState.GetName(), metav1.GetOptions{})
	if err != nil {
		glog.Errorf("nodeStateAddHandler(): failed to get node: %v", err)
		dn.exitCh <- err
		return
	}

	reqReboot := false
	reqDrain := false

	for k, p := range dn.LoadedPlugins {
		d, r, err:= p.OnNodeStateAdd(nodeState)
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
		// dn.drainNode(node)
		// defer drain.Uncordon(dn.kubeClient.CoreV1().Nodes(), node, nil)
	}
	for k, p := range dn.LoadedPlugins {
		if k != GenericPlugin {
			err:= p.Apply()
			if err != nil {
				glog.Errorf("nodeStateAddHandler(): plugin %s fail to apply: %v", k, err)
				dn.exitCh <- err
				return
			}
		}
	}

	// Apply generic_plugin last
	err = dn.LoadedPlugins[GenericPlugin].Apply()
	if err != nil {
		glog.Errorf("nodeStateAddHandler(): generic_plugin fail to apply: %v", err)
		dn.exitCh <- err
		return
	}

	if reqReboot {
		go rebootNode()
		return
	}

	// restart device plugin pod
	if reqDrain {
		if err := dn.restartDevicePluginPod(node); err != nil {
			dn.exitCh <- err
			return
		}
	}
	dn.refreshCh <- struct{}{}
}

func (dn *Daemon) nodeStateChangeHandler(old, new interface{}) {
	newState := new.(*sriovnetworkv1.SriovNetworkNodeState)
	oldState := old.(*sriovnetworkv1.SriovNetworkNodeState)
	if reflect.DeepEqual(newState.Spec.Interfaces, oldState.Spec.Interfaces) {
		glog.V(2).Infof("nodeStateChangeHandler(): Interface not changed")
		return
	}

	node, err := dn.kubeClient.CoreV1().Nodes().Get(newState.GetName(), metav1.GetOptions{})
	if err != nil {
		glog.Errorf("nodeStateChangeHandler(): failed to get node: %v", err)
		dn.exitCh <- err
		return
	}

	reqReboot := false
	reqDrain := false

	for k, p := range dn.LoadedPlugins {
		d, r, err:= p.OnNodeStateChange(oldState, newState)
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
		// dn.drainNode(node)
		// defer drain.Uncordon(dn.kubeClient.CoreV1().Nodes(), node, nil)
	}
	for k, p := range dn.LoadedPlugins {
		if k != GenericPlugin {
			err:= p.Apply()
			if err != nil {
				glog.Errorf("nodeStateChangeHandler(): plugin %s fail to apply: %v", k, err)
				dn.exitCh <- err
				return
			}
		}
	}

	// Apply generic_plugin last
	err = dn.LoadedPlugins[GenericPlugin].Apply()
	if err != nil {
		glog.Errorf("nodeStateChangeHandler(): generic_plugin fail to apply: %v", err)
		dn.exitCh <- err
		return
	}

	if reqReboot {
		go rebootNode()
		return
	}

	// restart device plugin pod
	if reqDrain {
		if err := dn.restartDevicePluginPod(node); err != nil {
			dn.exitCh <- err
			return
		}
	}
	dn.refreshCh <- struct{}{}
}

func (dn *Daemon) restartDevicePluginPod(node *corev1.Node) error {
	glog.V(2).Infof("restartDevicePluginPod(): Need to restart device plugin pod")
	pods, err := dn.kubeClient.CoreV1().Pods(namespace).List(metav1.ListOptions{
		LabelSelector: "app=sriov-device-plugin",
		FieldSelector: "spec.nodeName=" + dn.name,
	})
	if err != nil {
		glog.Warningf("restartDevicePluginPod(): Failed to list device plugin pod: %s", err)
		return nil
	}
	glog.V(2).Infof("restartDevicePluginPod(): Found device plugin pod %s", pods.Items[0].GetName())
	err = dn.kubeClient.CoreV1().Pods(namespace).Delete(pods.Items[0].GetName(), &metav1.DeleteOptions{})
	if err != nil {
		glog.Errorf("restartDevicePluginPod(): Failed to delete device plugin pod: %s", err)
		return err
	}

	return nil
}

func (dn *Daemon) loadVendorPlugins(ns *sriovnetworkv1.SriovNetworkNodeState) error {
	pl := registerPlugins(ns)
	pl = append(pl, GenericPlugin)
	dn.LoadedPlugins = make(map[string]VendorPlugin)
 
    for _, pn := range(pl) {
		filePath := filepath.Join(pluginsPath, pn+".so")
        glog.Infof("loadVendorPlugins(): try to load plugin %s", pn)
        p, err := loadOnePlugin(filePath)
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
	cmd := exec.Command("systemctl", "reboot")
	if err := cmd.Run(); err != nil {
		glog.Error("failed to reboot node")
	}
}

func (dn *Daemon) drainNode(node *corev1.Node) {
	glog.Info("Update prepared; beginning drain")

	backoff := wait.Backoff{
		Steps:    5,
		Duration: 10 * time.Second,
		Factor:   2,
	}
	var lastErr error

	if err := wait.ExponentialBackoff(backoff, func() (bool, error) {
		err := drain.Drain(dn.kubeClient, []*corev1.Node{node}, &drain.DrainOptions{
			DeleteLocalData:    true,
			Force:              true,
			GracePeriodSeconds: 600,
			IgnoreDaemonsets:   true,
		})
		if err == nil {
			return true, nil
		}
		lastErr = err
		glog.Infof("Draining failed with: %v, retrying", err)
		return false, nil
	}); err != nil {
		if err == wait.ErrWaitTimeout {
			glog.Errorf("failed to drain node (%d tries): %v :%v", backoff.Steps, err, lastErr)
		}
		glog.Errorf("failed to drain node: %v", err)
	}
	glog.Info("drain complete, reboot node")
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
	pluginNames := make(map[string]string)
	for _, iface := range ns.Status.Interfaces {
			pluginNames[pluginMap[iface.Vendor]] = "Y"
	}
	rawList := reflect.ValueOf(pluginNames).MapKeys()
	nameList := make([]string, len(rawList))
    for i := 0; i < len(rawList); i++ {
        nameList[i] = rawList[i].String()
    }
	return nameList
}
