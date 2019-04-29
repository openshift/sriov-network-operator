package daemon

import(
	"fmt"
	"os"
	"time"
	"reflect"

	"github.com/golang/glog"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	
	// "k8s.io/client-go/kubernetes/scheme"
	sriovnetworkv1 "github.com/pliurh/sriov-network-operator/pkg/apis/sriovnetwork/v1"
	snclientset "github.com/pliurh/sriov-network-operator/pkg/client/clientset/versioned"
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
	var err error
	// Pull NodeState before entering the loop
	if dn.nodeState, err= pullNodeState(dn.client, dn.name, namespace); err != nil {
		return err
	}
	// Create a ticker to pull NodeState
	tickerPull := time.NewTicker(time.Second * time.Duration(15))
	defer tickerPull.Stop()
	glog.Infof("Resync period to pull NodeState: %d [s]", 15)
	var wNodeState watch.Interface
	if wNodeState, err = nodeStateWatch(dn.client, dn.name, namespace); err != nil {
		return err
	}
	defer wNodeState.Stop()

	for {
		select {
		case <-dn.stopCh:
			glog.Infof("stop Daemon")
			return nil

		case nodeStateEvent, ok := <-wNodeState.ResultChan():
			glog.Infof("wNodeState.ResultChan()")
			if !ok {
				return fmt.Errorf("NodeState event watch channel closed.")
			}
			nodeStateChangeHandler(nodeStateEvent, dn.nodeState)

		case <-tickerPull.C:
			glog.Infof("tickerPull.C")
			// if err := pullLabels(clientset, &tuned, nodeName); err != nil {
			// 	return err
			// }
		}
	}
}

func nodeStateWatch(clientset snclientset.Interface, nodeName string, namespace string) (watch.Interface, error) {
	glog.Infof("nodeStateWatch watch node=%s, namespace=%s", nodeName, namespace)
	w, err := clientset.SriovnetworkV1().SriovNetworkNodeStates(namespace).Watch(metav1.ListOptions{FieldSelector: "metadata.name=" + nodeName})
	if err != nil {
		return nil, fmt.Errorf("Unexpected error watching nodeState on %s: %v", nodeName, err)
	}
	return w, nil
}

func pullNodeState(clientset snclientset.Interface, nodeName string, namespace string) (*sriovnetworkv1.SriovNetworkNodeState, error) {
	nodeState, err:= clientset.SriovnetworkV1().SriovNetworkNodeStates(namespace).Get(nodeName,metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("Unexpected error getting nodeState on %s: %v", nodeName, err)
	}
	return nodeState, nil
}

func nodeStateChangeHandler(event watch.Event, nodeState *sriovnetworkv1.SriovNetworkNodeState) {
	newState, ok := event.Object.(*sriovnetworkv1.SriovNetworkNodeState)
	if !ok {
		glog.Warningf("Unexpected object received: %#v", event.Object)
		return
	}

	if reflect.DeepEqual(newState.Spec.Interfaces, nodeState.Spec.Interfaces) {
		return
	}

	if err:= syncNodeState(newState); err != nil{
		glog.Warningf("Failed to sync newNodeState")
		return
	}
}
