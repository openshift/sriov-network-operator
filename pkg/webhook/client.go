package webhook

import (
	"os"

	"github.com/golang/glog"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

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
		glog.Error("fail to setup client")
		return err
	}

	snclient = snclientset.NewForConfigOrDie(config)
	kubeclient = kubernetes.NewForConfigOrDie(config)

	return nil
}
