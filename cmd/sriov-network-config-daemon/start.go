package main

import (
	"context"
	"flag"
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/golang/glog"
	configv1 "github.com/openshift/api/config/v1"
	mcfgv1 "github.com/openshift/machine-config-operator/pkg/apis/machineconfiguration.openshift.io/v1"
	"github.com/spf13/cobra"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/connrotation"

	sriovnetworkv1 "github.com/k8snetworkplumbingwg/sriov-network-operator/api/v1"
	snclientset "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/client/clientset/versioned"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/daemon"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/utils"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/version"
)

var (
	startCmd = &cobra.Command{
		Use:   "start",
		Short: "Starts SR-IOV Network Config Daemon",
		Long:  "",
		Run:   runStartCmd,
	}

	startOpts struct {
		kubeconfig string
		nodeName   string
	}
)

func init() {
	rootCmd.AddCommand(startCmd)
	startCmd.PersistentFlags().StringVar(&startOpts.kubeconfig, "kubeconfig", "", "Kubeconfig file to access a remote cluster (testing only)")
	startCmd.PersistentFlags().StringVar(&startOpts.nodeName, "node-name", "", "kubernetes node name daemon is managing.")
}

func runStartCmd(cmd *cobra.Command, args []string) {
	flag.Set("logtostderr", "true")
	flag.Parse()

	// To help debugging, immediately log version
	glog.V(2).Infof("Version: %+v", version.Version)

	if startOpts.nodeName == "" {
		name, ok := os.LookupEnv("NODE_NAME")
		if !ok || name == "" {
			glog.Fatalf("node-name is required")
		}
		startOpts.nodeName = name
	}

	// This channel is used to ensure all spawned goroutines exit when we exit.
	stopCh := make(chan struct{})
	defer close(stopCh)

	// This channel is used to signal Run() something failed and to jump ship.
	// It's purely a chan<- in the Daemon struct for goroutines to write to, and
	// a <-chan in Run() for the main thread to listen on.
	exitCh := make(chan error)
	defer close(exitCh)

	// This channel is to make sure main thread will wait until the writer finish
	// to report lastSyncError in SriovNetworkNodeState object.
	syncCh := make(chan struct{})
	defer close(syncCh)

	refreshCh := make(chan daemon.Message)
	defer close(refreshCh)

	var config *rest.Config
	var err error

	if os.Getenv("CLUSTER_TYPE") == utils.ClusterTypeOpenshift {
		kubeconfig, err := clientcmd.LoadFromFile("/host/etc/kubernetes/kubeconfig")
		if err != nil {
			glog.Errorf("failed to load kubelet kubeconfig: %v", err)
		}
		clusterName := kubeconfig.Contexts[kubeconfig.CurrentContext].Cluster
		apiURL := kubeconfig.Clusters[clusterName].Server

		url, err := url.Parse(apiURL)
		if err != nil {
			glog.Errorf("failed to parse api url from kubelet kubeconfig: %v", err)
		}

		// The kubernetes in-cluster functions don't let you override the apiserver
		// directly; gotta "pass" it via environment vars.
		glog.V(0).Infof("overriding kubernetes api to %s", apiURL)
		os.Setenv("KUBERNETES_SERVICE_HOST", url.Hostname())
		os.Setenv("KUBERNETES_SERVICE_PORT", url.Port())
	}

	kubeconfig := os.Getenv("KUBECONFIG")
	if kubeconfig != "" {
		config, err = clientcmd.BuildConfigFromFlags("", kubeconfig)
	} else {
		// creates the in-cluster config
		config, err = rest.InClusterConfig()
	}

	if err != nil {
		panic(err.Error())
	}

	closeAllConns, err := updateDialer(config)
	if err != nil {
		panic(err.Error())
	}

	sriovnetworkv1.AddToScheme(scheme.Scheme)
	mcfgv1.AddToScheme(scheme.Scheme)
	configv1.Install(scheme.Scheme)

	snclient := snclientset.NewForConfigOrDie(config)
	kubeclient := kubernetes.NewForConfigOrDie(config)
	openshiftContext, err := utils.NewOpenshiftContext(config, scheme.Scheme)
	if err != nil {
		panic(err)
	}

	config.Timeout = 5 * time.Second
	writerclient := snclientset.NewForConfigOrDie(config)

	mode := os.Getenv("DEV_MODE")
	devMode := false
	if mode == "TRUE" {
		devMode = true
		glog.V(0).Info("dev mode enabled")
	}

	glog.V(0).Info("starting node writer")
	nodeWriter := daemon.NewNodeStateStatusWriter(writerclient, startOpts.nodeName, closeAllConns, devMode)

	destdir := os.Getenv("DEST_DIR")
	if destdir == "" {
		destdir = "/host/tmp"
	}

	platformType := utils.Baremetal

	nodeInfo, err := kubeclient.CoreV1().Nodes().Get(context.Background(), startOpts.nodeName, v1.GetOptions{})
	if err == nil {
		for key, pType := range utils.PlatformMap {
			if strings.Contains(strings.ToLower(nodeInfo.Spec.ProviderID), strings.ToLower(key)) {
				platformType = pType
			}
		}
	} else {
		glog.Warningf("Failed to fetch node state %s, %v!", startOpts.nodeName, err)
	}
	glog.V(0).Infof("Running on platform: %s", platformType.String())

	var namespace = os.Getenv("NAMESPACE")
	if err := sriovnetworkv1.InitNicIDMap(kubeclient, namespace); err != nil {
		glog.Errorf("failed to run init NicIdMap: %v", err)
		panic(err.Error())
	}

	// block the deamon process until nodeWriter finish first its run
	err = nodeWriter.RunOnce(destdir, platformType)
	if err != nil {
		glog.Errorf("failed to run writer: %v", err)
		panic(err.Error())
	}
	go nodeWriter.Run(stopCh, refreshCh, syncCh, platformType)

	glog.V(0).Info("Starting SriovNetworkConfigDaemon")
	err = daemon.New(
		startOpts.nodeName,
		snclient,
		kubeclient,
		openshiftContext,
		exitCh,
		stopCh,
		syncCh,
		refreshCh,
		platformType,
	).Run(stopCh, exitCh)
	if err != nil {
		glog.Errorf("failed to run daemon: %v", err)
	}
	glog.V(0).Info("Shutting down SriovNetworkConfigDaemon")
}

// updateDialer instruments a restconfig with a dial. the returned function allows forcefully closing all active connections.
func updateDialer(clientConfig *rest.Config) (func(), error) {
	if clientConfig.Transport != nil || clientConfig.Dial != nil {
		return nil, fmt.Errorf("there is already a transport or dialer configured")
	}
	f := &net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}
	d := connrotation.NewDialer(f.DialContext)
	clientConfig.Dial = d.DialContext
	return d.CloseAll, nil
}
