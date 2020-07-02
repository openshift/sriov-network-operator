package daemon

import (
	"context"
	"flag"
	"fmt"
	"net"
	"os"
	"reflect"
	"sync"
	"time"

	"github.com/golang/glog"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/connrotation"

	// "k8s.io/client-go/kubernetes/scheme"
	sriovnetworkv1 "github.com/openshift/sriov-network-operator/pkg/apis/sriovnetwork/v1"
	snclientset "github.com/openshift/sriov-network-operator/pkg/client/clientset/versioned"
	sninformer "github.com/openshift/sriov-network-operator/pkg/client/informers/externalversions"
	"github.com/openshift/sriov-network-operator/pkg/drain"
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
	// name is the node name.
	name      string
	namespace string

	client snclientset.Interface
	// kubeClient allows interaction with Kubernetes, including the node we are running on.
	kubeClient *kubernetes.Clientset

	config *rest.Config

	syncCh <-chan struct{}

	stopCh chan struct{}

	refreshCh chan<- Message

	drainManager *drain.DrainManager

	networkDaemon *NetworkDaemon
}

const (
	scriptsPath = "/bindata/scripts/enable-rdma.sh"
)

var namespace = os.Getenv("NAMESPACE")
var pluginsPath = os.Getenv("PLUGINSPATH")

func New(
	nodeName string,
	config *rest.Config,
) *Daemon {

	closeAllConns, err := updateDialer(config)
	if err != nil {
		panic(err)
	}

	snclient := snclientset.NewForConfigOrDie(config)
	kubeclient := kubernetes.NewForConfigOrDie(config)

	daemon := &Daemon{
		name:       nodeName,
		client:     snclient,
		kubeClient: kubeclient,
		config:     config,
		stopCh:     make(chan struct{}),
	}

	// Create the drain manager
	daemon.drainManager = drain.NewDrainManager(daemon.kubeClient, nodeName, daemon.stopCh)

	// Create the network daemon
	daemon.networkDaemon = NewNetworkDaemon(nodeName, daemon.client, daemon.kubeClient, daemon.drainManager, closeAllConns, daemon.stopCh)

	// TODO: create the accelerator daemon

	return daemon
}

// Run the config daemon
func (dn *Daemon) Run() error {
	glog.V(0).Info("Run(): start main daemon")
	defer close(dn.stopCh)

	// Run the drain manager
	drainErrChan := make(chan error)
	dn.drainManager.Run(drainErrChan)

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

	go cfgInformer.Run(dn.stopCh)
	if ok := cache.WaitForCacheSync(dn.stopCh, cfgInformer.HasSynced); !ok {
		return fmt.Errorf("failed to wait for operator config cache to sync")
	}

	netErrChan := make(chan error)
	go dn.networkDaemon.Run(netErrChan)

	for {
		select {
		case <-dn.stopCh:
			glog.V(0).Info("Run(): stop daemon")
			return nil
		case err := <-netErrChan:
			glog.V(0).Infof("Run(): failed to run network daemon: %v", err)
			return err
		case err := <-drainErrChan:
			glog.V(0).Infof("Run(): failed to run drain manager: %v", err)
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

func restartDevicePluginPod(nodeName string, kubeClient *kubernetes.Clientset, mutex *sync.Mutex, stopCh <-chan struct{}) error {
	mutex.Lock()
	defer mutex.Unlock()
	glog.V(2).Infof("restartDevicePluginPod(): try to restart device plugin pod")

	var podToDelete string
	var foundPodToDelete bool
	if err := wait.PollImmediateUntil(3*time.Second, func() (bool, error) {
		pods, err := kubeClient.CoreV1().Pods(namespace).List(context.Background(), metav1.ListOptions{
			LabelSelector: "app=sriov-network-device-plugin",
			FieldSelector: "spec.nodeName=" + nodeName,
		})

		if errors.IsNotFound(err) {
			glog.Info("restartDevicePluginPod(): device plugin pod exited")
			return true, nil
		}

		if err != nil {
			glog.Warningf("restartDevicePluginPod(): Failed to list device plugin pod: %s, retrying", err)
			return false, nil
		}

		if len(pods.Items) == 0 {
			glog.Info("restartDevicePluginPod(): device plugin pod exited")
			return true, nil
		}
		podToDelete = pods.Items[0].Name
		foundPodToDelete = true
		return true, nil
	}, stopCh); err != nil {
		glog.Errorf("restartDevicePluginPod(): failed to wait for finding pod to delete: %v", err)
		return err
	}

	if !foundPodToDelete {
		glog.Info("restartDevicePluginPod(): device plugin pod was not there")
		return nil
	}

	if err := wait.PollImmediateUntil(3*time.Second, func() (bool, error) {
		glog.V(2).Infof("restartDevicePluginPod(): Found device plugin pod %s, deleting it", podToDelete)
		err := kubeClient.CoreV1().Pods(namespace).Delete(context.Background(), podToDelete, metav1.DeleteOptions{})
		if errors.IsNotFound(err) {
			glog.Info("restartDevicePluginPod(): pod to delete not found")
			return true, nil
		}
		if err != nil {
			glog.Errorf("restartDevicePluginPod(): Failed to delete device plugin pod: %s, retrying", err)
			return false, nil
		}
		return true, nil
	}, stopCh); err != nil {
		glog.Errorf("restartDevicePluginPod(): failed to wait for pod deletion: %v", err)
		return err
	}

	if err := wait.PollImmediateUntil(3*time.Second, func() (bool, error) {
		_, err := kubeClient.CoreV1().Pods(namespace).Get(context.Background(), podToDelete, metav1.GetOptions{})
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
	}, stopCh); err != nil {
		glog.Errorf("restartDevicePluginPod(): failed to wait for checking pod deletion: %v", err)
		return err
	}

	return nil
}

type GlogLogger struct {
}

func (a GlogLogger) Log(v ...interface{}) {
	glog.Info(v...)
}

func (a GlogLogger) Logf(format string, v ...interface{}) {
	glog.Infof(format, v...)
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

// updateDialer instruments a restconfig with a dial. the returned function allows forcefully closing all active connections.
func updateDialer(clientConfig *rest.Config) (func(), error) {
	if clientConfig.Transport != nil || clientConfig.Dial != nil {
		return nil, fmt.Errorf("there is already a transport or dialer configured")
	}
	d := connrotation.NewDialer((&net.Dialer{Timeout: 30 * time.Second, KeepAlive: 15 * time.Second}).DialContext)
	clientConfig.Dial = d.DialContext
	return d.CloseAll, nil
}
