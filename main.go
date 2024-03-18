/*
Copyright 2021.

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
	"flag"
	"os"

	// Import all Kubernetes client auth plugins (e.g. Azure, GCP, OIDC, etc.)
	// to ensure that exec-entrypoint and run can make use of them.
	netattdefv1 "github.com/k8snetworkplumbingwg/network-attachment-definition-client/pkg/apis/k8s.cni.cncf.io/v1"
	openshiftconfigv1 "github.com/openshift/api/config/v1"
	mcfgv1 "github.com/openshift/machine-config-operator/pkg/apis/machineconfiguration.openshift.io/v1"

	// Import all Kubernetes client auth plugins (e.g. Azure, GCP, OIDC, etc.)
	// to ensure that exec-entrypoint and run can make use of them.
	// Import all Kubernetes client auth plugins (e.g. Azure, GCP, OIDC, etc.)
	_ "k8s.io/client-go/plugin/pkg/client/auth"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/kubernetes"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	_ "k8s.io/client-go/plugin/pkg/client/auth/gcp"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/metrics/server"
	"sigs.k8s.io/controller-runtime/pkg/webhook"

	sriovnetworkv1 "github.com/k8snetworkplumbingwg/sriov-network-operator/api/v1"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/controllers"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/leaderelection"

	snolog "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/log"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/platforms"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/utils"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/vars"
	//+kubebuilder:scaffold:imports
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))

	utilruntime.Must(sriovnetworkv1.AddToScheme(scheme))
	utilruntime.Must(netattdefv1.AddToScheme(scheme))
	utilruntime.Must(mcfgv1.AddToScheme(scheme))
	utilruntime.Must(openshiftconfigv1.AddToScheme(scheme))
	//+kubebuilder:scaffold:scheme
}

func main() {
	var metricsAddr string
	var enableLeaderElection bool
	var probeAddr string

	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8080", "The address the metric endpoint binds to.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	flag.BoolVar(&enableLeaderElection, "leader-elect", false,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")
	snolog.BindFlags(flag.CommandLine)
	flag.Parse()
	snolog.InitLog()

	restConfig := ctrl.GetConfigOrDie()

	kubeClient, err := client.New(restConfig, client.Options{Scheme: scheme})
	if err != nil {
		setupLog.Error(err, "couldn't create client")
		os.Exit(1)
	}

	if vars.ResourcePrefix == "" {
		setupLog.Error(nil, "RESOURCE_PREFIX environment variable can't be empty")
		os.Exit(1)
	}

	le := leaderelection.GetLeaderElectionConfig(kubeClient, enableLeaderElection)

	mgr, err := ctrl.NewManager(restConfig, ctrl.Options{
		Scheme:                 scheme,
		Metrics:                server.Options{BindAddress: metricsAddr},
		WebhookServer:          webhook.NewServer(webhook.Options{Port: 9443}),
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         enableLeaderElection,
		LeaseDuration:          &le.LeaseDuration,
		RenewDeadline:          &le.RenewDeadline,
		RetryPeriod:            &le.RetryPeriod,
		LeaderElectionID:       "a56def2a.openshift.io",
		Cache:                  cache.Options{DefaultNamespaces: map[string]cache.Config{vars.Namespace: {}}},
	})
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}
	mgrGlobal, err := ctrl.NewManager(restConfig, ctrl.Options{
		Scheme:  scheme,
		Metrics: server.Options{BindAddress: "0"},
	})
	if err != nil {
		setupLog.Error(err, "unable to start global manager")
		os.Exit(1)
	}

	err = mgrGlobal.GetCache().IndexField(context.Background(), &sriovnetworkv1.SriovNetwork{}, "spec.networkNamespace", func(o client.Object) []string {
		return []string{o.(*sriovnetworkv1.SriovNetwork).Spec.NetworkNamespace}
	})
	if err != nil {
		setupLog.Error(err, "unable to create index field for cache")
		os.Exit(1)
	}

	err = mgrGlobal.GetCache().IndexField(context.Background(), &sriovnetworkv1.SriovIBNetwork{}, "spec.networkNamespace", func(o client.Object) []string {
		return []string{o.(*sriovnetworkv1.SriovIBNetwork).Spec.NetworkNamespace}
	})
	if err != nil {
		setupLog.Error(err, "unable to create index field for cache")
		os.Exit(1)
	}

	if err := initNicIDMap(); err != nil {
		setupLog.Error(err, "unable to init NicIdMap")
		os.Exit(1)
	}

	// Initial global info
	vars.Config = restConfig
	vars.Scheme = mgrGlobal.GetScheme()

	platformsHelper, err := platforms.NewDefaultPlatformHelper()
	if err != nil {
		setupLog.Error(err, "couldn't create openshift context")
		os.Exit(1)
	}

	if err = (&controllers.SriovNetworkReconciler{
		Client: mgrGlobal.GetClient(),
		Scheme: mgrGlobal.GetScheme(),
	}).SetupWithManager(mgrGlobal); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "SriovNetwork")
		os.Exit(1)
	}
	if err = (&controllers.SriovIBNetworkReconciler{
		Client: mgrGlobal.GetClient(),
		Scheme: mgrGlobal.GetScheme(),
	}).SetupWithManager(mgrGlobal); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "SriovIBNetwork")
		os.Exit(1)
	}
	if err = (&controllers.SriovNetworkNodePolicyReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "SriovNetworkNodePolicy")
		os.Exit(1)
	}
	if err = (&controllers.SriovOperatorConfigReconciler{
		Client:         mgr.GetClient(),
		Scheme:         mgr.GetScheme(),
		PlatformHelper: platformsHelper,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "SriovOperatorConfig")
		os.Exit(1)
	}
	if err = (&controllers.SriovNetworkPoolConfigReconciler{
		Client:         mgr.GetClient(),
		Scheme:         mgr.GetScheme(),
		PlatformHelper: platformsHelper,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "SriovNetworkPoolConfig")
		os.Exit(1)
	}

	// we need a client that doesn't use the local cache for the objects
	drainKClient, err := client.New(restConfig, client.Options{
		Scheme: scheme,
		Cache: &client.CacheOptions{
			DisableFor: []client.Object{
				&sriovnetworkv1.SriovNetworkNodeState{},
				&corev1.Node{},
				&mcfgv1.MachineConfigPool{},
			},
		},
	})
	if err != nil {
		setupLog.Error(err, "unable to create drain kubernetes client")
		os.Exit(1)
	}

	drainController, err := controllers.NewDrainReconcileController(drainKClient,
		mgr.GetScheme(),
		mgr.GetEventRecorderFor("SR-IOV operator"),
		platformsHelper)
	if err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "DrainReconcile")
		os.Exit(1)
	}

	if err = drainController.SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to setup controller with manager", "controller", "DrainReconcile")
		os.Exit(1)
	}
	// +kubebuilder:scaffold:builder

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up ready check")
		os.Exit(1)
	}

	stopCh := ctrl.SetupSignalHandler()
	go func() {
		if err := mgrGlobal.Start(stopCh); err != nil {
			setupLog.Error(err, "Manager Global exited non-zero")
			os.Exit(1)
		}
	}()

	// Remove all finalizers after controller is shut down
	defer utils.Shutdown()

	setupLog.Info("starting manager")
	if err := mgr.Start(stopCh); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}

func initNicIDMap() error {
	kubeclient := kubernetes.NewForConfigOrDie(ctrl.GetConfigOrDie())
	if err := sriovnetworkv1.InitNicIDMapFromConfigMap(kubeclient, vars.Namespace); err != nil {
		return err
	}

	return nil
}
