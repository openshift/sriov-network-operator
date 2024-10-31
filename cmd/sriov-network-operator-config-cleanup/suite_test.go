package main

import (
	"context"
	"io/fs"
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
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	//+kubebuilder:scaffold:imports
	sriovnetworkv1 "github.com/k8snetworkplumbingwg/sriov-network-operator/api/v1"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/vars"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/test/util"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

var (
	k8sClient   client.Client
	testEnv     *envtest.Environment
	cfg         *rest.Config
	kubecfgPath string
)

var _ = BeforeSuite(func() {

	logf.SetLogger(zap.New(
		zap.WriteTo(GinkgoWriter),
		zap.UseDevMode(true),
		func(o *zap.Options) {
			o.TimeEncoder = zapcore.RFC3339NanoTimeEncoder
		}))

	// Go to project root directory
	err := os.Chdir("../..")
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

	apiserverDir := testEnv.ControlPlane.GetAPIServer().CertDir
	kubecfgPath = findKubecfg(apiserverDir, ".kubecfg")
	err = os.Setenv("KUBECONFIG", kubecfgPath)
	Expect(err).NotTo(HaveOccurred())

	By("registering schemes")
	err = sriovnetworkv1.AddToScheme(scheme.Scheme)
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
	ctx := context.Background()
	Expect(k8sClient.Create(ctx, ns)).Should(Succeed())
})

var _ = AfterSuite(func() {
	By("tearing down the test environment")
	if testEnv != nil {
		Eventually(func() error {
			return testEnv.Stop()
		}, util.APITimeout, time.Second).ShouldNot(HaveOccurred())
	}
})

func findKubecfg(path, ext string) string {
	var cfg string
	filepath.WalkDir(path, func(s string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if filepath.Ext(d.Name()) == ext {
			cfg = s
		}
		return nil
	})
	return cfg
}

func TestAPIs(t *testing.T) {
	_, reporterConfig := GinkgoConfiguration()

	RegisterFailHandler(Fail)

	RunSpecs(t, "operator-webhook Suite", reporterConfig)
}
