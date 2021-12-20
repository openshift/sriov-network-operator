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
	"fmt"
	"os"

	netattdefv1 "github.com/k8snetworkplumbingwg/network-attachment-definition-client/pkg/apis/k8s.cni.cncf.io/v1"
	mcfgv1 "github.com/openshift/machine-config-operator/pkg/apis/machineconfiguration.openshift.io/v1"
	"k8s.io/apimachinery/pkg/api/errors"

	// Import all Kubernetes client auth plugins (e.g. Azure, GCP, OIDC, etc.)
	// to ensure that exec-entrypoint and run can make use of them.
	_ "k8s.io/client-go/plugin/pkg/client/auth"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/kubernetes"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	_ "k8s.io/client-go/plugin/pkg/client/auth/gcp"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	sriovnetworkv1 "github.com/k8snetworkplumbingwg/sriov-network-operator/api/v1"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/controllers"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/leaderelection"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/utils"
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
	opts := zap.Options{
		Development: true,
	}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	restConfig := ctrl.GetConfigOrDie()
	kubeClient, err := client.New(restConfig, client.Options{Scheme: scheme})
	if err != nil {
		setupLog.Error(err, "couldn't create client")
		os.Exit(1)
	}

	le := leaderelection.GetLeaderElectionConfig(kubeClient, enableLeaderElection)

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))
	namespace := os.Getenv("NAMESPACE")
	mgr, err := ctrl.NewManager(restConfig, ctrl.Options{
		Scheme:                 scheme,
		MetricsBindAddress:     metricsAddr,
		Port:                   9443,
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         enableLeaderElection,
		LeaseDuration:          &le.LeaseDuration,
		RenewDeadline:          &le.RenewDeadline,
		RetryPeriod:            &le.RetryPeriod,
		LeaderElectionID:       "a56def2a.openshift.io",
		Namespace:              namespace,
	})
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}
	mgrGlobal, err := ctrl.NewManager(restConfig, ctrl.Options{
		Scheme:             scheme,
		MetricsBindAddress: "0",
	})
	if err != nil {
		setupLog.Error(err, "unable to start global manager")
		os.Exit(1)
	}

	if err := initNicIdMap(); err != nil {
		setupLog.Error(err, "unable to init NicIdMap")
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
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "SriovOperatorConfig")
		os.Exit(1)
	}
	if err = (&controllers.SriovNetworkPoolConfigReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "SriovNetworkPoolConfig")
		os.Exit(1)
	}
	// +kubebuilder:scaffold:builder

	// Create a default SriovNetworkNodePolicy
	err = createDefaultPolicy(kubeClient)
	if err != nil {
		setupLog.Error(err, "unable to create default SriovNetworkNodePolicy")
		os.Exit(1)
	}

	// Create default SriovOperatorConfig
	err = createDefaultOperatorConfig(kubeClient)
	if err != nil {
		setupLog.Error(err, "unable to create default SriovOperatorConfig")
		os.Exit(1)
	}

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

func initNicIdMap() error {
	namespace := os.Getenv("NAMESPACE")
	kubeclient := kubernetes.NewForConfigOrDie(ctrl.GetConfigOrDie())
	if err := sriovnetworkv1.InitNicIdMap(kubeclient, namespace); err != nil {
		return err
	}

	return nil
}

func createDefaultPolicy(c client.Client) error {
	logger := setupLog.WithName("createDefaultPolicy")
	policy := &sriovnetworkv1.SriovNetworkNodePolicy{
		Spec: sriovnetworkv1.SriovNetworkNodePolicySpec{
			NumVfs:       0,
			NodeSelector: make(map[string]string),
			NicSelector:  sriovnetworkv1.SriovNetworkNicSelector{},
		},
	}
	name := "default"
	namespace := os.Getenv("NAMESPACE")
	err := c.Get(context.TODO(), types.NamespacedName{Name: name, Namespace: namespace}, policy)
	if err != nil {
		if errors.IsNotFound(err) {
			logger.Info("Create a default SriovNetworkNodePolicy")
			policy.Namespace = namespace
			policy.Name = name
			err = c.Create(context.TODO(), policy)
			if err != nil {
				return err
			}
		}
		// Error reading the object - requeue the request.
		return err
	}
	return nil
}

func createDefaultOperatorConfig(c client.Client) error {
	logger := setupLog.WithName("createDefaultOperatorConfig")
	singleNode, err := utils.IsSingleNodeCluster(c)
	if err != nil {
		return fmt.Errorf("Couldn't check the amount of nodes in the cluster")
	}

	enableAdmissionController := os.Getenv("ENABLE_ADMISSION_CONTROLLER") == "true"
	config := &sriovnetworkv1.SriovOperatorConfig{
		Spec: sriovnetworkv1.SriovOperatorConfigSpec{
			EnableInjector:           func() *bool { b := enableAdmissionController; return &b }(),
			EnableOperatorWebhook:    func() *bool { b := enableAdmissionController; return &b }(),
			ConfigDaemonNodeSelector: map[string]string{},
			LogLevel:                 2,
			DisableDrain:             singleNode,
		},
	}
	name := "default"
	namespace := os.Getenv("NAMESPACE")
	err = c.Get(context.TODO(), types.NamespacedName{Name: name, Namespace: namespace}, config)
	if err != nil {
		if errors.IsNotFound(err) {
			logger.Info("Create default SriovOperatorConfig")
			config.Namespace = namespace
			config.Name = name
			err = c.Create(context.TODO(), config)
			if err != nil {
				return err
			}
		}
		// Error reading the object - requeue the request.
		return err
	}
	return nil
}
