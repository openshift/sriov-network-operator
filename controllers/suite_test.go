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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"go.uber.org/zap/zapcore"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/config"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/metrics/server"

	netattdefv1 "github.com/k8snetworkplumbingwg/network-attachment-definition-client/pkg/apis/k8s.cni.cncf.io/v1"
	openshiftconfigv1 "github.com/openshift/api/config/v1"
	mcfgv1 "github.com/openshift/api/machineconfiguration/v1"
	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"

	//+kubebuilder:scaffold:imports
	sriovnetworkv1 "github.com/k8snetworkplumbingwg/sriov-network-operator/api/v1"
	snolog "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/log"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/vars"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/test/util"
)

// These tests use Ginkgo (BDD-style Go testing framework). Refer to
// http://onsi.github.io/ginkgo/ to learn more about Ginkgo.

var (
	k8sClient client.Client
	testEnv   *envtest.Environment
	cfg       *rest.Config
)

// Define utility constants for object names and testing timeouts/durations and intervals.
const testNamespace = "openshift-sriov-network-operator"

func setupK8sManagerForTest() (manager.Manager, error) {
	k8sManager, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme:  scheme.Scheme,
		Metrics: server.Options{BindAddress: "0"}, // we don't need metrics server for tests
		Controller: config.Controller{
			SkipNameValidation: ptr.To(true),
		},
	})

	if err != nil {
		return nil, err
	}

	k8sManager.GetCache().IndexField(context.Background(), &sriovnetworkv1.SriovNetwork{}, "spec.networkNamespace", func(o client.Object) []string {
		return []string{o.(*sriovnetworkv1.SriovNetwork).Spec.NetworkNamespace}
	})

	k8sManager.GetCache().IndexField(context.Background(), &sriovnetworkv1.SriovIBNetwork{}, "spec.networkNamespace", func(o client.Object) []string {
		return []string{o.(*sriovnetworkv1.SriovIBNetwork).Spec.NetworkNamespace}
	})

	k8sManager.GetCache().IndexField(context.Background(), &sriovnetworkv1.OVSNetwork{}, "spec.networkNamespace", func(o client.Object) []string {
		return []string{o.(*sriovnetworkv1.OVSNetwork).Spec.NetworkNamespace}
	})

	return k8sManager, nil
}

var _ = BeforeSuite(func() {
	var err error

	logf.SetLogger(zap.New(
		zap.WriteTo(GinkgoWriter),
		zap.Level(zapcore.Level(-2)),
		zap.UseDevMode(true),
		func(o *zap.Options) {
			o.TimeEncoder = zapcore.RFC3339NanoTimeEncoder
		}))
	snolog.InitLog()

	// Go to project root directory
	err = os.Chdir("..")

	By("setting up env variables for tests")
	Expect(err).NotTo(HaveOccurred())
	err = os.Setenv("RESOURCE_PREFIX", "openshift.io")
	Expect(err).NotTo(HaveOccurred())
	err = os.Setenv("NAMESPACE", "openshift-sriov-network-operator")
	Expect(err).NotTo(HaveOccurred())
	err = os.Setenv("ADMISSION_CONTROLLERS_ENABLED", "true")
	Expect(err).NotTo(HaveOccurred())
	err = os.Setenv("ADMISSION_CONTROLLERS_CERTIFICATES_OPERATOR_SECRET_NAME", "operator-webhook-cert")
	Expect(err).NotTo(HaveOccurred())
	err = os.Setenv("ADMISSION_CONTROLLERS_CERTIFICATES_INJECTOR_SECRET_NAME", "network-resources-injector-cert")
	Expect(err).NotTo(HaveOccurred())
	err = os.Setenv("OPERATOR_WEBHOOK_NETWORK_POLICY_PORT", "6443")
	Expect(err).NotTo(HaveOccurred())
	err = os.Setenv("INJECTOR_WEBHOOK_NETWORK_POLICY_PORT", "6443")
	Expect(err).NotTo(HaveOccurred())
	err = os.Setenv("SRIOV_CNI_IMAGE", "mock-image")
	Expect(err).NotTo(HaveOccurred())
	err = os.Setenv("SRIOV_INFINIBAND_CNI_IMAGE", "mock-image")
	Expect(err).NotTo(HaveOccurred())
	err = os.Setenv("OVS_CNI_IMAGE", "mock-image")
	Expect(err).NotTo(HaveOccurred())
	err = os.Setenv("RDMA_CNI_IMAGE", "mock-image")
	Expect(err).NotTo(HaveOccurred())
	err = os.Setenv("SRIOV_DEVICE_PLUGIN_IMAGE", "mock-image")
	Expect(err).NotTo(HaveOccurred())
	err = os.Setenv("NETWORK_RESOURCES_INJECTOR_IMAGE", "mock-image")
	Expect(err).NotTo(HaveOccurred())
	err = os.Setenv("SRIOV_NETWORK_CONFIG_DAEMON_IMAGE", "mock-image")
	Expect(err).NotTo(HaveOccurred())
	err = os.Setenv("SRIOV_NETWORK_WEBHOOK_IMAGE", "mock-image")
	Expect(err).NotTo(HaveOccurred())
	err = os.Setenv("RELEASE_VERSION", "4.7.0")
	Expect(err).NotTo(HaveOccurred())
	err = os.Setenv("OPERATOR_NAME", "sriov-network-operator")
	Expect(err).NotTo(HaveOccurred())
	err = os.Setenv("METRICS_EXPORTER_IMAGE", "mock-image")
	Expect(err).NotTo(HaveOccurred())
	err = os.Setenv("METRICS_EXPORTER_SECRET_NAME", "metrics-exporter-cert")
	Expect(err).NotTo(HaveOccurred())
	err = os.Setenv("METRICS_EXPORTER_PORT", "9110")
	Expect(err).NotTo(HaveOccurred())
	err = os.Setenv("METRICS_EXPORTER_KUBE_RBAC_PROXY_IMAGE", "mock-image")
	Expect(err).NotTo(HaveOccurred())
	err = os.Setenv("METRICS_EXPORTER_PROMETHEUS_OPERATOR_SERVICE_ACCOUNT", "k8s-prometheus")
	Expect(err).NotTo(HaveOccurred())
	err = os.Setenv("METRICS_EXPORTER_PROMETHEUS_OPERATOR_NAMESPACE", "default")
	Expect(err).NotTo(HaveOccurred())

	By("bootstrapping test environment")
	testEnv = &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("config", "crd", "bases"), filepath.Join("test", "util", "crds")},
		ErrorIfCRDPathMissing: true,
	}

	testEnv.ControlPlane.GetAPIServer().Configure().Set("disable-admission-plugins", "MutatingAdmissionWebhook", "ValidatingAdmissionWebhook")

	cfg, err = testEnv.Start()
	Expect(err).NotTo(HaveOccurred())
	Expect(cfg).NotTo(BeNil())

	By("registering schemes")
	err = sriovnetworkv1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())
	err = netattdefv1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())
	err = mcfgv1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())
	err = openshiftconfigv1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())
	err = monitoringv1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	vars.Config = cfg
	vars.Scheme = scheme.Scheme
	vars.Namespace = testNamespace

	By("creating K8s client")
	k8sClient, err = client.New(cfg, client.Options{Scheme: scheme.Scheme})
	Expect(err).NotTo(HaveOccurred())
	Expect(k8sClient).NotTo(BeNil())

	By("creating default/common k8s objects for tests")
	// Create test namespace
	ns := &corev1.Namespace{
		TypeMeta: metav1.TypeMeta{},
		ObjectMeta: metav1.ObjectMeta{
			Name: testNamespace,
		},
		Spec:   corev1.NamespaceSpec{},
		Status: corev1.NamespaceStatus{},
	}
	Expect(k8sClient.Create(context.Background(), ns)).Should(Succeed())

	sa := &corev1.ServiceAccount{TypeMeta: metav1.TypeMeta{},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "default",
			Namespace: testNamespace,
		}}
	Expect(k8sClient.Create(context.Background(), sa)).Should(Succeed())

	// Create openshift Infrastructure
	infra := &openshiftconfigv1.Infrastructure{
		ObjectMeta: metav1.ObjectMeta{
			Name: "cluster",
		},
		Spec: openshiftconfigv1.InfrastructureSpec{},
		Status: openshiftconfigv1.InfrastructureStatus{
			ControlPlaneTopology: openshiftconfigv1.HighlyAvailableTopologyMode,
		},
	}
	Expect(k8sClient.Create(context.Background(), infra)).Should(Succeed())
})

var _ = AfterSuite(func() {
	By("tearing down the test environment")
	if testEnv != nil {
		Eventually(func() error {
			return testEnv.Stop()
		}, util.APITimeout, time.Second).ShouldNot(HaveOccurred())
	}
})

func TestAPIs(t *testing.T) {
	_, reporterConfig := GinkgoConfiguration()

	RegisterFailHandler(Fail)

	RunSpecs(t, "Controller Suite", reporterConfig)
}
