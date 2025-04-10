package client

import (
	"os"

	netattdefv1 "github.com/k8snetworkplumbingwg/network-attachment-definition-client/pkg/apis/k8s.cni.cncf.io/v1"
	clientmachineconfigv1 "github.com/openshift/client-go/machineconfiguration/clientset/versioned/typed/machineconfiguration/v1"
	apiext "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/discovery"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	appsv1client "k8s.io/client-go/kubernetes/typed/apps/v1"
	coordinationv1 "k8s.io/client-go/kubernetes/typed/coordination/v1"
	corev1client "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	runtimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/client/versioned/typed/monitoring/v1"

	sriovv1 "github.com/k8snetworkplumbingwg/sriov-network-operator/api/v1"
	snolog "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/log"
)

func init() {
	snolog.InitLog()
}

// ClientSet provides the struct to talk with relevant API
type ClientSet struct {
	corev1client.CoreV1Interface
	clientmachineconfigv1.MachineconfigurationV1Interface

	appsv1client.AppsV1Interface
	discovery.DiscoveryInterface
	Config *rest.Config
	runtimeclient.Client
	coordinationv1.CoordinationV1Interface
	monitoringv1.MonitoringV1Interface
}

// New returns a *ClientBuilder with the given kubeconfig.
func New(kubeconfig string) *ClientSet {
	var config *rest.Config
	var err error

	if kubeconfig == "" {
		kubeconfig = os.Getenv("KUBECONFIG")
	}

	if kubeconfig != "" {
		log.Log.V(4).Info("Loading kube client config", "path", kubeconfig)
		config, err = clientcmd.BuildConfigFromFlags("", kubeconfig)
	} else {
		log.Log.V(4).Info("Using in-cluster kube client config")
		config, err = rest.InClusterConfig()
	}
	if err != nil {
		log.Log.Error(err, "Error while building client config")
		return nil
	}

	clientSet := &ClientSet{}
	clientSet.CoreV1Interface = corev1client.NewForConfigOrDie(config)
	clientSet.MachineconfigurationV1Interface = clientmachineconfigv1.NewForConfigOrDie(config)
	clientSet.AppsV1Interface = appsv1client.NewForConfigOrDie(config)
	clientSet.DiscoveryInterface = discovery.NewDiscoveryClientForConfigOrDie(config)
	clientSet.CoordinationV1Interface = coordinationv1.NewForConfigOrDie(config)
	clientSet.MonitoringV1Interface = monitoringv1.NewForConfigOrDie(config)
	clientSet.Config = config

	crScheme := runtime.NewScheme()
	clientgoscheme.AddToScheme(crScheme)
	netattdefv1.SchemeBuilder.AddToScheme(crScheme)
	sriovv1.AddToScheme(crScheme)
	apiext.AddToScheme(crScheme)

	clientSet.Client, err = runtimeclient.New(config, runtimeclient.Options{
		Scheme: crScheme,
	})
	if err != nil {
		log.Log.Error(err, "Error while creating ClientSet")
		return nil
	}
	return clientSet
}
