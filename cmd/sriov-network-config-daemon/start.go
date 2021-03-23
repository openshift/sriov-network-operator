package main

import (
	"flag"
	"fmt"
	"net"
	"os"
	"time"

	"github.com/golang/glog"
	sriovnetworkv1 "github.com/openshift/sriov-network-operator/pkg/apis/sriovnetwork/v1"
	snclientset "github.com/openshift/sriov-network-operator/pkg/client/clientset/versioned"
	"github.com/openshift/sriov-network-operator/pkg/daemon"
	"github.com/openshift/sriov-network-operator/pkg/version"
	"github.com/spf13/cobra"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/connrotation"

	mcfgv1 "github.com/openshift/machine-config-operator/pkg/apis/machineconfiguration.openshift.io/v1"
	mcclientset "github.com/openshift/machine-config-operator/pkg/generated/clientset/versioned"
)

var (
	startCmd = &cobra.Command{
		Use:   "start",
		Short: "Starts Machine Config Daemon",
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

	snclient := snclientset.NewForConfigOrDie(config)
	kubeclient := kubernetes.NewForConfigOrDie(config)
	mcclient := mcclientset.NewForConfigOrDie(config)

	config.Timeout = 5 * time.Second
	writerclient := snclientset.NewForConfigOrDie(config)
	glog.V(0).Info("starting node writer")
	nodeWriter := daemon.NewNodeStateStatusWriter(writerclient, startOpts.nodeName, closeAllConns)

	destdir := os.Getenv("DEST_DIR")
	if destdir == "" {
		destdir = "/host/etc"
	}

	// block the deamon process until nodeWriter finish first its run
	nodeWriter.Run(stopCh, refreshCh, syncCh, destdir, true)
	go nodeWriter.Run(stopCh, refreshCh, syncCh, "", false)

	glog.V(0).Info("Starting SriovNetworkConfigDaemon")
	err = daemon.New(
		startOpts.nodeName,
		snclient,
		kubeclient,
		mcclient,
		exitCh,
		stopCh,
		syncCh,
		refreshCh,
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
	d := connrotation.NewDialer((&net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}).DialContext)
	clientConfig.Dial = d.DialContext
	return d.CloseAll, nil
}
