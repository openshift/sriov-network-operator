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

package controllers

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	netattdefv1 "github.com/k8snetworkplumbingwg/network-attachment-definition-client/pkg/apis/k8s.cni.cncf.io/v1"
	. "github.com/onsi/ginkgo"
	"github.com/onsi/ginkgo/config"
	. "github.com/onsi/gomega"
	mcfgv1 "github.com/openshift/machine-config-operator/pkg/apis/machineconfiguration.openshift.io/v1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/controller-runtime/pkg/envtest/printer"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	sriovnetworkv1 "github.com/k8snetworkplumbingwg/sriov-network-operator/api/v1"
	constants "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/utils"
	//+kubebuilder:scaffold:imports
)

// These tests use Ginkgo (BDD-style Go testing framework). Refer to
// http://onsi.github.io/ginkgo/ to learn more about Ginkgo.

var cfg *rest.Config
var (
	k8sClient client.Client
	testEnv   *envtest.Environment

	ctx    context.Context
	cancel context.CancelFunc
)

// Define utility constants for object names and testing timeouts/durations and intervals.
const (
	testNamespace = "openshift-sriov-network-operator"

	timeout  = time.Second * 10
	duration = time.Second * 100
	interval = time.Millisecond * 250
)

var _ = BeforeSuite(func(done Done) {
	logf.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true)))

	// Go to project root directory
	os.Chdir("..")

	By("bootstrapping test environment")
	testEnv = &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("config", "crd", "bases"), filepath.Join("test", "util", "crds")},
		ErrorIfCRDPathMissing: true,
	}

	cfg, err := testEnv.Start()
	Expect(err).NotTo(HaveOccurred())
	Expect(cfg).NotTo(BeNil())

	err = sriovnetworkv1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())
	err = netattdefv1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())
	err = mcfgv1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	//+kubebuilder:scaffold:scheme

	// A client is created for our test CRUD operations.
	k8sClient, err = client.New(cfg, client.Options{Scheme: scheme.Scheme})
	Expect(err).NotTo(HaveOccurred())
	Expect(k8sClient).NotTo(BeNil())

	// Start controllers
	k8sManager, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme: scheme.Scheme,
	})
	Expect(err).ToNot(HaveOccurred())

	err = (&SriovNetworkReconciler{
		Client: k8sManager.GetClient(),
		Scheme: k8sManager.GetScheme(),
	}).SetupWithManager(k8sManager)
	Expect(err).ToNot(HaveOccurred())

	err = (&SriovIBNetworkReconciler{
		Client: k8sManager.GetClient(),
		Scheme: k8sManager.GetScheme(),
	}).SetupWithManager(k8sManager)
	Expect(err).ToNot(HaveOccurred())

	err = (&SriovOperatorConfigReconciler{
		Client: k8sManager.GetClient(),
		Scheme: k8sManager.GetScheme(),
	}).SetupWithManager(k8sManager)
	Expect(err).ToNot(HaveOccurred())

	err = (&SriovNetworkPoolConfigReconciler{
		Client: k8sManager.GetClient(),
		Scheme: k8sManager.GetScheme(),
	}).SetupWithManager(k8sManager)
	Expect(err).ToNot(HaveOccurred())

	os.Setenv("RESOURCE_PREFIX", "openshift.io")
	os.Setenv("NAMESPACE", "openshift-sriov-network-operator")
	os.Setenv("ENABLE_ADMISSION_CONTROLLER", "true")
	os.Setenv("SRIOV_CNI_IMAGE", "mock-image")
	os.Setenv("SRIOV_INFINIBAND_CNI_IMAGE", "mock-image")
	os.Setenv("SRIOV_DEVICE_PLUGIN_IMAGE", "mock-image")
	os.Setenv("NETWORK_RESOURCES_INJECTOR_IMAGE", "mock-image")
	os.Setenv("SRIOV_NETWORK_CONFIG_DAEMON_IMAGE", "mock-image")
	os.Setenv("SRIOV_NETWORK_WEBHOOK_IMAGE", "mock-image")
	os.Setenv("RELEASE_VERSION", "4.7.0")
	os.Setenv("OPERATOR_NAME", "sriov-network-operator")

	ctx, cancel = context.WithCancel(ctrl.SetupSignalHandler())

	go func() {
		defer GinkgoRecover()
		err = k8sManager.Start(ctx)
		Expect(err).ToNot(HaveOccurred())
	}()

	// Create test namespace
	ns := &corev1.Namespace{
		TypeMeta: metav1.TypeMeta{},
		ObjectMeta: metav1.ObjectMeta{
			Name: testNamespace,
		},
		Spec:   corev1.NamespaceSpec{},
		Status: corev1.NamespaceStatus{},
	}
	Expect(k8sClient.Create(context.TODO(), ns)).Should(Succeed())

	config := &sriovnetworkv1.SriovOperatorConfig{}
	config.SetNamespace(testNamespace)
	config.SetName(constants.DEFAULT_CONFIG_NAME)
	config.Spec = sriovnetworkv1.SriovOperatorConfigSpec{
		EnableInjector:           func() *bool { b := true; return &b }(),
		EnableOperatorWebhook:    func() *bool { b := true; return &b }(),
		ConfigDaemonNodeSelector: map[string]string{},
		LogLevel:                 2,
	}
	Expect(k8sClient.Create(context.TODO(), config)).Should(Succeed())

	poolConfig := &sriovnetworkv1.SriovNetworkPoolConfig{}
	poolConfig.SetNamespace(testNamespace)
	poolConfig.SetName(constants.DEFAULT_CONFIG_NAME)
	poolConfig.Spec = sriovnetworkv1.SriovNetworkPoolConfigSpec{}
	Expect(k8sClient.Create(context.TODO(), poolConfig)).Should(Succeed())
	close(done)
}, 60)

var _ = AfterSuite(func() {
	By("tearing down the test environment")
	cancel()
	Eventually(func() error {
		return testEnv.Stop()
	}, timeout, time.Second).ShouldNot(HaveOccurred())
})

func TestAPIs(t *testing.T) {
	config.GinkgoConfig.ParallelTotal = 1

	RegisterFailHandler(Fail)

	RunSpecsWithDefaultAndCustomReporters(t,
		"Controller Suite",
		[]Reporter{printer.NewlineReporter{}})
}
