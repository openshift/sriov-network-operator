/*


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
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	_ "k8s.io/client-go/plugin/pkg/client/auth/gcp"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	sriovnetworkv1 "github.com/openshift/sriov-network-operator/api/v1"
	"github.com/openshift/sriov-network-operator/controllers"
	// +kubebuilder:scaffold:imports
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
	// +kubebuilder:scaffold:scheme
}

func main() {
	var metricsAddr string
	var enableLeaderElection bool
	flag.StringVar(&metricsAddr, "metrics-addr", ":8080", "The address the metric endpoint binds to.")
	flag.BoolVar(&enableLeaderElection, "enable-leader-election", false,
		"Enable leader election for controllers manager. "+
			"Enabling this will ensure there is only one active controllers manager.")
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseDevMode(true)))
	namespace := os.Getenv("NAMESPACE")
	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:             scheme,
		MetricsBindAddress: metricsAddr,
		Port:               9443,
		LeaderElection:     enableLeaderElection,
		LeaderElectionID:   "578c20de.sriovnetwork.openshift.io",
		Namespace:          namespace,
	})
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}
	mgrGlobal, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:             scheme,
		MetricsBindAddress: "0",
	})
	if err != nil {
		setupLog.Error(err, "unable to start global manager")
		os.Exit(1)
	}

	if err = (&controllers.SriovIBNetworkReconciler{
		Client: mgrGlobal.GetClient(),
		Log:    ctrl.Log.WithName("controllers").WithName("SriovIBNetwork"),
		Scheme: mgrGlobal.GetScheme(),
	}).SetupWithManager(mgrGlobal); err != nil {
		setupLog.Error(err, "unable to create controllers", "controllers", "SriovIBNetwork")
		os.Exit(1)
	}
	if err = (&controllers.SriovNetworkReconciler{
		Client: mgrGlobal.GetClient(),
		Log:    ctrl.Log.WithName("controllers").WithName("SriovNetwork"),
		Scheme: mgrGlobal.GetScheme(),
	}).SetupWithManager(mgrGlobal); err != nil {
		setupLog.Error(err, "unable to create controllers", "controllers", "SriovNetwork")
		os.Exit(1)
	}
	if err = (&controllers.SriovNetworkNodePolicyReconciler{
		Client: mgr.GetClient(),
		Log:    ctrl.Log.WithName("controllers").WithName("SriovNetworkNodePolicy"),
		Scheme: mgr.GetScheme(),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controllers", "controllers", "SriovNetworkNodePolicy")
		os.Exit(1)
	}
	if err = (&controllers.SriovOperatorConfigReconciler{
		Client: mgr.GetClient(),
		Log:    ctrl.Log.WithName("controllers").WithName("SriovOperatorConfig"),
		Scheme: mgr.GetScheme(),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controllers", "controllers", "SriovOperatorConfig")
		os.Exit(1)
	}
	// +kubebuilder:scaffold:builder

	// Create a default SriovNetworkNodePolicy
	err = createDefaultPolicy(ctrl.GetConfigOrDie())
	if err != nil {
		setupLog.Error(err, "unable to create default SriovNetworkNodePolicy")
		os.Exit(1)
	}

	// Create default SriovOperatorConfig
	err = createDefaultOperatorConfig(ctrl.GetConfigOrDie())
	if err != nil {
		setupLog.Error(err, "unable to create default SriovOperatorConfig")
		os.Exit(1)
	}

	stopCh := ctrl.SetupSignalHandler()
	go func() {
		if err := mgrGlobal.Start(stopCh); err != nil {
			setupLog.Error(err, "Manager Global exited non-zero")
			os.Exit(1)
		}
	}()

	setupLog.Info("starting manager")
	if err := mgr.Start(stopCh); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}

func createDefaultPolicy(cfg *rest.Config) error {
	logger := setupLog.WithName("createDefaultPolicy")
	c, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		return fmt.Errorf("Couldn't create client: %v", err)
	}
	policy := &sriovnetworkv1.SriovNetworkNodePolicy{
		Spec: sriovnetworkv1.SriovNetworkNodePolicySpec{
			NumVfs:       0,
			NodeSelector: make(map[string]string),
			NicSelector:  sriovnetworkv1.SriovNetworkNicSelector{},
		},
	}
	name := "default"
	namespace := os.Getenv("NAMESPACE")
	err = c.Get(context.TODO(), types.NamespacedName{Name: name, Namespace: namespace}, policy)
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

func createDefaultOperatorConfig(cfg *rest.Config) error {
	logger := setupLog.WithName("createDefaultOperatorConfig")
	c, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		return fmt.Errorf("Couldn't create client: %v", err)
	}
	config := &sriovnetworkv1.SriovOperatorConfig{
		Spec: sriovnetworkv1.SriovOperatorConfigSpec{
			EnableInjector:           func() *bool { b := true; return &b }(),
			EnableOperatorWebhook:    func() *bool { b := true; return &b }(),
			ConfigDaemonNodeSelector: map[string]string{},
			LogLevel:                 2,
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
