package daemon

import (
	"os"
	"os/exec"
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

	// channel used by callbacks to signal Run() of an error
	exitCh chan<- error

	// channel used to ensure all spawned goroutines exit when we exit.
	stopCh <-chan struct{}

	refreshCh chan<- struct{}

	dpReboot bool
}

var namespace = os.Getenv("NAMESPACE")

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

func (dn *Daemon) Run() error {
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

	informer.Run(dn.stopCh)

	for {
		select {
		case <-dn.stopCh:
			glog.V(0).Info("Run(): stop daemon")
			return nil
		}
	}
}

func (dn *Daemon) nodeStateAddHandler(obj interface{}) {
	// "k8s.io/apimachinery/pkg/apis/meta/v1" provides an Object
	// interface that allows us to get metadata easily
	nodeState := obj.(*sriovnetworkv1.SriovNetworkNodeState)
	glog.V(2).Infof("nodeStateChangeHandler(): New SriovNetworkNodeState Added to Store: %s", nodeState.GetName())
	glog.V(2).Infof("nodeStateAddHandler(): sync %s", nodeState.GetName())
	if err := syncNodeState(nodeState); err != nil {
		glog.Warningf("nodeStateChangeHandler(): Failed to sync nodeState. ERR: %s", err)
		return
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

	node, err := dn.kubeClient.CoreV1().Nodes().Get(oldState.GetName(), metav1.GetOptions{})
	if err != nil {
		glog.Errorf("failed to get node: %v", err)
	}

	if needRestartNode(oldState, newState) {
		dn.drainNode(node)
		////////////////////////////
		/// TODO: call vendor plugin
		////////////////////////////
		cmd := exec.Command("systemctl", "reboot")
		if err := cmd.Run(); err != nil {
			glog.Error("failed to reboot node")
		}
	} else {
		if err := drain.Uncordon(dn.kubeClient.CoreV1().Nodes(), node, nil); err != nil {
			glog.Errorf("nodeStateChangeHandler(): failed to make node Schedulable: %v", err)
		}
	}

	restartDP := needRestartDevicePlugin(oldState, newState)
	if restartDP {
		dn.drainNode(node)
	}

	glog.V(2).Infof("nodeStateChangeHandler(): sync %s", newState.GetName())
	if err := syncNodeState(newState); err != nil {
		glog.Warningf("nodeStateChangeHandler(): Failed to sync newNodeState. ERR: %s", err)
		return
	}

	if restartDP {
		glog.V(2).Infof("nodeStateChangeHandler(): Need to restart device plugin pod")
		pods, err := dn.kubeClient.CoreV1().Pods(namespace).List(metav1.ListOptions{
			LabelSelector: "app=sriov-device-plugin",
			FieldSelector: "spec.nodeName=" + dn.name,
		})
		if err != nil {
			glog.Warningf("nodeStateChangeHandler(): Failed to list device plugin pod. ERR: %s", err)
			return
		}
		glog.V(2).Infof("nodeStateChangeHandler(): Found device plugin pod %s", pods.Items[0].GetName())
		err = dn.kubeClient.CoreV1().Pods(namespace).Delete(pods.Items[0].GetName(), &metav1.DeleteOptions{})
		if err != nil {
			glog.Warningf("nodeStateChangeHandler(): Failed to delete device plugin pod. ERR: %s", err)
			return
		}
		if err := drain.Uncordon(dn.kubeClient.CoreV1().Nodes(), node, nil); err != nil {
			glog.Errorf("nodeStateChangeHandler(): failed to make node Schedulable: %v", err)
		}
	}
	dn.refreshCh <- struct{}{}
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

func needRestartNode(oldState, newState *sriovnetworkv1.SriovNetworkNodeState) bool {
	for _, in := range newState.Spec.Interfaces {
		for _, io := range oldState.Status.Interfaces {
			if in.PciAddress == io.PciAddress {
				if io.Vendor == "0x15b3" && in.NumVfs < io.TotalVfs {
					return true
				}
			}
		}
	}
	return false
}
