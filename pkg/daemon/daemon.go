package daemon

import(
	"os"

	// "github.com/golang/glog"
	"k8s.io/client-go/kubernetes"
	// "k8s.io/client-go/kubernetes/scheme"
)

type Daemon struct {
	// name is the node name.
	name string

	// kubeClient allows interaction with Kubernetes, including the node we are running on.
	kubeClient kubernetes.Interface

	// channel used by callbacks to signal Run() of an error
	exitCh chan<- error

	// channel used to ensure all spawned goroutines exit when we exit.
	stopCh <-chan struct{}
}

var namespace string = os.Getenv("NAMESPACE")