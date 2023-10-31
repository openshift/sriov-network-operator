package webhook

import (
	"os"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/log"

	snclientset "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/client/clientset/versioned"
)

var snclient snclientset.Interface
var kubeclient *kubernetes.Clientset

func SetupInClusterClient() error {
	var err error
	var config *rest.Config

	kubeconfig := os.Getenv("KUBECONFIG")
	if kubeconfig != "" {
		config, err = clientcmd.BuildConfigFromFlags("", kubeconfig)
	} else {
		// creates the in-cluster config
		config, err = rest.InClusterConfig()
	}

	if err != nil {
		log.Log.Error(nil, "fail to setup client")
		return err
	}

	snclient = snclientset.NewForConfigOrDie(config)
	kubeclient = kubernetes.NewForConfigOrDie(config)

	return nil
}
