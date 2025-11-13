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
	"net/url"
	"os"
	"strings"

	ocpconfigapi "github.com/openshift/api/config/v1"
	"github.com/spf13/cobra"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/kubernetes"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	runtimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/metrics/server"

	sriovnetworkv1 "github.com/k8snetworkplumbingwg/sriov-network-operator/api/v1"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/consts"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/daemon"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/featuregate"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/helper"
	snolog "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/log"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/platform"
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

	scheme = runtime.NewScheme()
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

	// Init Scheme
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(sriovnetworkv1.AddToScheme(scheme))
	utilruntime.Must(ocpconfigapi.AddToScheme(scheme))

	// Init logger
	snolog.InitLog()
}

func configGlobalVariables() error {
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

	vars.Scheme = scheme

	return nil
}

func useKubeletKubeConfig() {
	fnLogger := log.Log.WithName("sriov-network-config-daemon")

	kubeconfig, err := clientcmd.LoadFromFile("/host/etc/kubernetes/kubeconfig")
	if err != nil {
		fnLogger.Error(err, "failed to load kubelet kubeconfig")
	}
	clusterName := kubeconfig.Contexts[kubeconfig.CurrentContext].Cluster
	apiURL := kubeconfig.Clusters[clusterName].Server

	urlPath, err := url.Parse(apiURL)
	if err != nil {
		fnLogger.Error(err, "failed to parse api url from kubelet kubeconfig")
	}

	// The kubernetes in-cluster functions don't let you override the apiserver
	// directly; gotta "pass" it via environment vars.
	fnLogger.V(0).Info("overriding kubernetes api", "new-url", apiURL)
	err = os.Setenv("KUBERNETES_SERVICE_HOST", urlPath.Hostname())
	if err != nil {
		fnLogger.Error(err, "failed to set KUBERNETES_SERVICE_HOST environment variable")
	}
	err = os.Setenv("KUBERNETES_SERVICE_PORT", urlPath.Port())
	if err != nil {
		fnLogger.Error(err, "failed to set KUBERNETES_SERVICE_PORT environment variable")
	}
}

func getOperatorConfig(kClient runtimeclient.Client) (*sriovnetworkv1.SriovOperatorConfig, error) {
	defaultConfig := &sriovnetworkv1.SriovOperatorConfig{}
	err := kClient.Get(context.Background(), types.NamespacedName{Namespace: vars.Namespace, Name: consts.DefaultConfigName}, defaultConfig)
	if err != nil {
		return nil, err
	}
	return defaultConfig, nil
}

func initFeatureGates(defaultConfig *sriovnetworkv1.SriovOperatorConfig) (featuregate.FeatureGate, error) {
	fnLogger := log.Log.WithName("initFeatureGates")
	featureGates := featuregate.New()
	featureGates.Init(defaultConfig.Spec.FeatureGates)
	fnLogger.Info("Enabled featureGates", "featureGates", featureGates.String())

	return featureGates, nil
}

func initLogLevel(defaultConfig *sriovnetworkv1.SriovOperatorConfig) error {
	fnLogger := log.Log.WithName("initLogLevel")
	snolog.SetLogLevel(defaultConfig.Spec.LogLevel)
	fnLogger.V(2).Info("logLevel sets", "logLevel", defaultConfig.Spec.LogLevel)
	return nil
}

func runStartCmd(cmd *cobra.Command, args []string) error {
	setupLog := log.Log.WithName("sriov-network-config-daemon")
	stopSignalCh := ctrl.SetupSignalHandler()

	// Load global variables
	err := configGlobalVariables()
	if err != nil {
		setupLog.Error(err, "unable to config global variables")
		return err
	}

	var config *rest.Config

	// On openshift we use the kubeconfig from kubelet on the node where the daemon is running
	// this allow us to improve security as every daemon has access only to its own node
	if vars.ClusterType == consts.ClusterTypeOpenshift {
		useKubeletKubeConfig()
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

	// create clients
	kubeclient := kubernetes.NewForConfigOrDie(config)
	kClient, err := runtimeclient.New(
		config,
		runtimeclient.Options{
			Scheme: vars.Scheme})
	if err != nil {
		setupLog.Error(err, "couldn't create generic client")
		os.Exit(1)
	}

	nodeInfo, err := kubeclient.CoreV1().Nodes().Get(context.Background(), vars.NodeName, metav1.GetOptions{})
	if err != nil {
		setupLog.Error(err, "failed to fetch node state, exiting", "node-name", startOpts.nodeName)
		return err
	}
	// check for a platform
	for key, pType := range vars.PlatformsMap {
		if strings.Contains(strings.ToLower(nodeInfo.Spec.ProviderID), strings.ToLower(key)) {
			vars.PlatformType = pType
		}
	}
	setupLog.Info("Running on", "platform", vars.PlatformType)

	// create helpers
	hostHelpers, err := helper.NewDefaultHostHelpers()
	if err != nil {
		setupLog.Error(err, "failed to create hostHelpers")
		return err
	}

	plat, err := platform.New(vars.PlatformType, hostHelpers)
	if err != nil {
		setupLog.Error(err, "failed to create hypervisor")
		return err
	}

	eventRecorder := daemon.NewEventRecorder(kClient, kubeclient, scheme)
	defer eventRecorder.Shutdown()

	// Initial supported nic IDs
	if err := sriovnetworkv1.InitNicIDMapFromConfigMap(kubeclient, vars.Namespace); err != nil {
		setupLog.Error(err, "failed to run init NicIdMap")
		return err
	}

	operatorConfig, err := getOperatorConfig(kClient)
	if err != nil {
		setupLog.Error(err, "Failed to get operator config object")
		return err
	}

	// init feature gates
	fg, err := initFeatureGates(operatorConfig)
	if err != nil {
		setupLog.Error(err, "failed to initialize feature gates")
		return err
	}

	// init log level
	if err := initLogLevel(operatorConfig); err != nil {
		setupLog.Error(err, "failed to initialize log level")
		return err
	}

	// init disable drain
	vars.DisableDrain = operatorConfig.Spec.DisableDrain

	// Init manager
	setupLog.V(0).Info("Starting SR-IOV Network Config Daemon")
	nodeStateSelector, err := fields.ParseSelector(fmt.Sprintf("metadata.name=%s,metadata.namespace=%s", vars.NodeName, vars.Namespace))
	if err != nil {
		setupLog.Error(err, "failed to parse sriovNetworkNodeState name selector")
		return err
	}
	operatorConfigSelector, err := fields.ParseSelector(fmt.Sprintf("metadata.name=%s,metadata.namespace=%s", consts.DefaultConfigName, vars.Namespace))
	if err != nil {
		setupLog.Error(err, "failed to parse sriovOperatorConfig name selector")
		return err
	}

	mgr, err := ctrl.NewManager(vars.Config, ctrl.Options{
		Scheme:  vars.Scheme,
		Metrics: server.Options{BindAddress: "0"}, // disable metrics server for now as the daemon runs with hostNetwork
		Cache: cache.Options{ // cache only the SriovNetworkNodeState with the node name
			ByObject: map[runtimeclient.Object]cache.ByObject{
				&sriovnetworkv1.SriovNetworkNodeState{}: {Field: nodeStateSelector},
				&sriovnetworkv1.SriovOperatorConfig{}:   {Field: operatorConfigSelector}}},
	})
	if err != nil {
		setupLog.Error(err, "unable to create manager")
		os.Exit(1)
	}

	dm := daemon.New(
		kClient,
		hostHelpers,
		plat,
		eventRecorder,
		fg)

	// Init Daemon configuration on the node
	if err = dm.Init(startOpts.disabledPlugins); err != nil {
		setupLog.Error(err, "unable to initialize daemon")
		os.Exit(1)
	}

	// Setup reconcile loop with manager
	if err = dm.SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to setup daemon with manager for SriovNetworkNodeState")
		os.Exit(1)
	}

	// Setup reconcile loop with manager
	if err = daemon.NewOperatorConfigNodeReconcile(kClient).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create setup daemon manager for OperatorConfig")
		os.Exit(1)
	}

	setupLog.Info("Starting Manager")
	return mgr.Start(stopSignalCh)
}
