package daemon

import(
	"os"
	"time"
	"reflect"

	"github.com/golang/glog"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	// "k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	// "k8s.io/client-go/kubernetes/scheme"
	sriovnetworkv1 "github.com/pliurh/sriov-network-operator/pkg/apis/sriovnetwork/v1"
	snclientset "github.com/pliurh/sriov-network-operator/pkg/client/clientset/versioned"
	sninformer "github.com/pliurh/sriov-network-operator/pkg/client/informers/externalversions"
)

type Daemon struct {
	// name is the node name.
	name string
	namespace string

	client snclientset.Interface
	// kubeClient allows interaction with Kubernetes, including the node we are running on.
	kubeClient kubernetes.Interface

	nodeState *sriovnetworkv1.SriovNetworkNodeState

	// channel used by callbacks to signal Run() of an error
	exitCh chan<- error

	// channel used to ensure all spawned goroutines exit when we exit.
	stopCh <-chan struct{}
}

var namespace = os.Getenv("NAMESPACE")

func New(
	nodeName string,
	client snclientset.Interface,
	exitCh chan<- error,
	stopCh <-chan struct{},
) (*Daemon) {
	return &Daemon{
		name: nodeName,
		client: client,
		exitCh: exitCh,
		stopCh: stopCh,
	}
}

func (dn *Daemon) Run() error {
	// Only watch own SriovNetworkNodeState CR
	informerFactory := sninformer.NewSharedInformerFactoryWithOptions(dn.client,
		time.Second*30,
		sninformer.WithNamespace(namespace),
		sninformer.WithTweakListOptions(func(lo *v1.ListOptions){
			lo.FieldSelector = "metadata.name="+dn.name
		}),
	)

    informer := informerFactory.Sriovnetwork().V1().SriovNetworkNodeStates().Informer()
    informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
        AddFunc: nodeStateAddHandler,
		UpdateFunc: nodeStateChangeHandler,
    })

    informer.Run(dn.stopCh)

	for {
		select {
		case <-dn.stopCh:
			glog.Infof("stop Daemon")
			return nil
		default:
			time.Sleep(2 * time.Second)
		}
	}
}

func nodeStateAddHandler(obj interface{}) {
	// "k8s.io/apimachinery/pkg/apis/meta/v1" provides an Object
	// interface that allows us to get metadata easily
	nodeState := obj.(*sriovnetworkv1.SriovNetworkNodeState)
	glog.Infof("nodeStateChangeHandler(): New SriovNetworkNodeState Added to Store: %s", nodeState.GetName())
	glog.Infof("nodeStateAddHandler(): sync %s", nodeState.GetName())
	if err:= syncNodeState(nodeState); err != nil{
		glog.Warningf("nodeStateChangeHandler(): Failed to sync nodeState")
		return
	}
}

func nodeStateChangeHandler(old, new interface {}) {
	newState := new.(*sriovnetworkv1.SriovNetworkNodeState)
	oldState := old.(*sriovnetworkv1.SriovNetworkNodeState)
	if reflect.DeepEqual(newState.Spec.Interfaces, oldState.Spec.Interfaces){
		glog.Infof("nodeStateChangeHandler(): Interface not changed")
		return
	}
	glog.Infof("nodeStateChangeHandler(): sync %s", newState.GetName())
	if err:= syncNodeState(newState); err != nil{
		glog.Warningf("nodeStateChangeHandler(): Failed to sync newNodeState")
		return
	}
}
