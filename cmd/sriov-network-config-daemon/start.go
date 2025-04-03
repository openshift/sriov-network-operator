/*
Copyright 2023.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

	http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/
package main

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/connrotation"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	configv1 "github.com/openshift/api/config/v1"
	mcfgv1 "github.com/openshift/machine-config-operator/pkg/apis/machineconfiguration.openshift.io/v1"

	sriovnetworkv1 "github.com/k8snetworkplumbingwg/sriov-network-operator/api/v1"
	snclientset "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/client/clientset/versioned"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/consts"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/daemon"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/featuregate"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/helper"
	snolog "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/log"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/platforms"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/vars"
)

// stringList is a list of strings, implements pflag.Value interface
type stringList []string

func (sl *stringList) String() string {
	return strings.Join(*sl, ",")
}

func (sl *stringList) Set(arg string) error {
	elems := strings.Split(arg, ",")

	for _, elem := range elems {
		if len(elem) == 0 {
			return fmt.Errorf("empty plugin name")
		}
		*sl = append(*sl, elem)
	}
	return nil
}

func (sl *stringList) Type() string {
	return "CommaSeparatedString"
}

var (
	startCmd = &cobra.Command{
		Use:   "start",
		Short: "Starts SR-IOV Network Config Daemon",
		Long:  "",
		RunE:  runStartCmd,
	}

	startOpts struct {
		kubeconfig            string
		nodeName              string
		systemd               bool
		disabledPlugins       stringList
		parallelNicConfig     bool
		manageSoftwareBridges bool
		ovsSocketPath         string
	}
)

func init() {
	rootCmd.AddCommand(startCmd)
	startCmd.PersistentFlags().StringVar(&startOpts.kubeconfig, "kubeconfig", "", "Kubeconfig file to access a remote cluster (testing only)")
	startCmd.PersistentFlags().StringVar(&startOpts.nodeName, "node-name", "", "kubernetes node name daemon is managing")
	startCmd.PersistentFlags().BoolVar(&startOpts.systemd, "use-systemd-service", false, "use config daemon in systemd mode")
	startCmd.PersistentFlags().VarP(&startOpts.disabledPlugins, "disable-plugins", "", "comma-separated list of plugins to disable")
	startCmd.PersistentFlags().BoolVar(&startOpts.parallelNicConfig, "parallel-nic-config", false, "perform NIC configuration in parallel")
	startCmd.PersistentFlags().BoolVar(&startOpts.manageSoftwareBridges, "manage-software-bridges", false, "enable management of software bridges")
	startCmd.PersistentFlags().StringVar(&startOpts.ovsSocketPath, "ovs-socket-path", vars.OVSDBSocketPath, "path for OVSDB socket")
}

func runStartCmd(cmd *cobra.Command, args []string) error {
	// init logger
	snolog.InitLog()
	setupLog := log.Log.WithName("sriov-network-config-daemon")

	// Mark that we are running inside a container
	vars.UsingSystemdMode = false
	if startOpts.systemd {
		vars.UsingSystemdMode = true
	}

	vars.ParallelNicConfig = startOpts.parallelNicConfig
	vars.ManageSoftwareBridges = startOpts.manageSoftwareBridges
	vars.OVSDBSocketPath = startOpts.ovsSocketPath

	if startOpts.nodeName == "" {
		name, ok := os.LookupEnv("NODE_NAME")
		if !ok || name == "" {
			return fmt.Errorf("node-name is required")
		}
		startOpts.nodeName = name
	}
	vars.NodeName = startOpts.nodeName

	for _, p := range startOpts.disabledPlugins {
		if _, ok := vars.DisableablePlugins[p]; !ok {
			return fmt.Errorf("%s plugin cannot be disabled", p)
		}
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

	// On openshift we use the kubeconfig from kubelet on the node where the daemon is running
	// this allow us to improve security as every daemon has access only to its own node
	if vars.ClusterType == consts.ClusterTypeOpenshift {
		kubeconfig, err := clientcmd.LoadFromFile("/host/etc/kubernetes/kubeconfig")
		if err != nil {
			setupLog.Error(err, "failed to load kubelet kubeconfig")
		}
		clusterName := kubeconfig.Contexts[kubeconfig.CurrentContext].Cluster
		apiURL := kubeconfig.Clusters[clusterName].Server

		urlPath, err := url.Parse(apiURL)
		if err != nil {
			setupLog.Error(err, "failed to parse api url from kubelet kubeconfig")
		}

		// The kubernetes in-cluster functions don't let you override the apiserver
		// directly; gotta "pass" it via environment vars.
		setupLog.V(0).Info("overriding kubernetes api", "new-url", apiURL)
		err = os.Setenv("KUBERNETES_SERVICE_HOST", urlPath.Hostname())
		if err != nil {
			setupLog.Error(err, "failed to set KUBERNETES_SERVICE_HOST environment variable")
		}
		err = os.Setenv("KUBERNETES_SERVICE_PORT", urlPath.Port())
		if err != nil {
			setupLog.Error(err, "failed to set KUBERNETES_SERVICE_PORT environment variable")
		}
	}

	kubeconfig := os.Getenv("KUBECONFIG")
	if kubeconfig != "" {
		config, err = clientcmd.BuildConfigFromFlags("", kubeconfig)
	} else {
		// creates the in-cluster config
		config, err = rest.InClusterConfig()
	}

	if err != nil {
		return err
	}

	vars.Config = config
	vars.Scheme = scheme.Scheme

	closeAllConns, err := updateDialer(config)
	if err != nil {
		return err
	}

	err = sriovnetworkv1.AddToScheme(scheme.Scheme)
	if err != nil {
		setupLog.Error(err, "failed to load sriov network CRDs to scheme")
		return err
	}

	err = mcfgv1.AddToScheme(scheme.Scheme)
	if err != nil {
		setupLog.Error(err, "failed to load machine config CRDs to scheme")
		return err
	}

	err = configv1.Install(scheme.Scheme)
	if err != nil {
		setupLog.Error(err, "failed to load openshift config CRDs to scheme")
		return err
	}

	kClient, err := client.New(config, client.Options{Scheme: scheme.Scheme})
	if err != nil {
		setupLog.Error(err, "couldn't create client")
		os.Exit(1)
	}

	snclient := snclientset.NewForConfigOrDie(config)
	kubeclient := kubernetes.NewForConfigOrDie(config)

	hostHelpers, err := helper.NewDefaultHostHelpers()
	if err != nil {
		setupLog.Error(err, "failed to create hostHelpers")
		return err
	}

	platformHelper, err := platforms.NewDefaultPlatformHelper()
	if err != nil {
		setupLog.Error(err, "failed to create platformHelper")
		return err
	}

	writerclient := snclientset.NewForConfigOrDie(config)

	eventRecorder := daemon.NewEventRecorder(writerclient, kubeclient)
	defer eventRecorder.Shutdown()

	setupLog.V(0).Info("starting node writer")
	nodeWriter := daemon.NewNodeStateStatusWriter(writerclient,
		closeAllConns,
		eventRecorder,
		hostHelpers,
		platformHelper)

	nodeInfo, err := kubeclient.CoreV1().Nodes().Get(context.Background(), startOpts.nodeName, v1.GetOptions{})
	if err == nil {
		for key, pType := range vars.PlatformsMap {
			if strings.Contains(strings.ToLower(nodeInfo.Spec.ProviderID), strings.ToLower(key)) {
				vars.PlatformType = pType
			}
		}
	} else {
		setupLog.Error(err, "failed to fetch node state, exiting", "node-name", startOpts.nodeName)
		return err
	}
	setupLog.Info("Running on", "platform", vars.PlatformType.String())

	if err := sriovnetworkv1.InitNicIDMapFromConfigMap(kubeclient, vars.Namespace); err != nil {
		setupLog.Error(err, "failed to run init NicIdMap")
		return err
	}

	eventRecorder.SendEvent("ConfigDaemonStart", "Config Daemon starting")

	// block the deamon process until nodeWriter finish first its run
	err = nodeWriter.RunOnce()
	if err != nil {
		setupLog.Error(err, "failed to run writer")
		return err
	}
	go nodeWriter.Run(stopCh, refreshCh, syncCh)

	// Init feature gates once to prevent race conditions.
	defaultConfig := &sriovnetworkv1.SriovOperatorConfig{}
	err = kClient.Get(context.Background(), types.NamespacedName{Namespace: vars.Namespace, Name: consts.DefaultConfigName}, defaultConfig)
	if err != nil {
		log.Log.Error(err, "Failed to get default SriovOperatorConfig object")
		return err
	}
	featureGates := featuregate.New()
	featureGates.Init(defaultConfig.Spec.FeatureGates)
	vars.MlxPluginFwReset = featureGates.IsEnabled(consts.MellanoxFirmwareResetFeatureGate)
	log.Log.Info("Enabled featureGates", "featureGates", featureGates.String())

	setupLog.V(0).Info("Starting SriovNetworkConfigDaemon")
	err = daemon.New(
		kClient,
		snclient,
		kubeclient,
		hostHelpers,
		platformHelper,
		exitCh,
		stopCh,
		syncCh,
		refreshCh,
		eventRecorder,
		featureGates,
		startOpts.disabledPlugins,
	).Run(stopCh, exitCh)
	if err != nil {
		setupLog.Error(err, "failed to run daemon")
	}
	setupLog.V(0).Info("Shutting down SriovNetworkConfigDaemon")
	return err
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
