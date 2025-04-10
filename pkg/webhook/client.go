package webhook

import (
	"os"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	runtimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/vars"
)

var client runtimeclient.Client
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
		log.Log.Error(nil, "fail to create config")
		return err
	}

	client, err = runtimeclient.New(config, runtimeclient.Options{Scheme: vars.Scheme})
	if err != nil {
		log.Log.Error(nil, "fail to setup client")
		return err
	}

	kubeclient, err = kubernetes.NewForConfig(config)
	if err != nil {
		log.Log.Error(nil, "fail to setup kubernetes client")
		return err
	}

	return nil
}
