package e2e

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	netattdefv1 "github.com/k8snetworkplumbingwg/network-attachment-definition-client/pkg/apis/k8s.cni.cncf.io/v1"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/utils/pointer"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/controller-runtime/pkg/envtest/printer"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	sriovnetworkv1 "github.com/k8snetworkplumbingwg/sriov-network-operator/api/v1"
	// +kubebuilder:scaffold:imports

	"github.com/k8snetworkplumbingwg/sriov-network-operator/test/util/cluster"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/test/util/netns"
)

// These tests use Ginkgo (BDD-style Go testing framework). Refer to
// http://onsi.github.io/ginkgo/ to learn more about Ginkgo.

var cfg *rest.Config
var k8sClient client.Client
var testEnv *envtest.Environment
var testNamespace string
var quitNetNsSet chan bool
var doneNetNsSet chan error

// Define utility constants for object names and testing timeouts/durations and intervals.
const (
	timeout         = time.Second * 30
	interval        = time.Second * 1
	devPollInterval = time.Millisecond * 400
)

func TestSriovTests(t *testing.T) {
	RegisterFailHandler(Fail)

	testNamespace = os.Getenv("NAMESPACE")
	Expect(testNamespace).NotTo(Equal(""))

	RunSpecsWithDefaultAndCustomReporters(t,
		"E2E Suite",
		[]Reporter{printer.NewlineReporter{}})
}

var sriovInfos *cluster.EnabledNodes
var sriovIface *sriovnetworkv1.InterfaceExt

var _ = BeforeSuite(func(done Done) {
	logf.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true)))

	// Go to project root directory
	os.Chdir("..")

	By("bootstrapping test environment")
	testEnv = &envtest.Environment{
		CRDDirectoryPaths:  []string{filepath.Join("config", "crd", "bases"), filepath.Join("test", "util", "crds")},
		UseExistingCluster: pointer.BoolPtr(true),
	}

	var err error
	cfg, err = testEnv.Start()
	Expect(err).ToNot(HaveOccurred())
	Expect(cfg).ToNot(BeNil())

	err = sriovnetworkv1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())
	err = netattdefv1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	err = sriovnetworkv1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	err = sriovnetworkv1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	err = sriovnetworkv1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	// +kubebuilder:scaffold:scheme

	// A client is created for our test CRUD operations.
	k8sClient, err = client.New(cfg, client.Options{Scheme: scheme.Scheme})
	Expect(err).ToNot(HaveOccurred())
	Expect(k8sClient).ToNot(BeNil())

	// get test device PCI address & target netns path. This path is the target netns for the PCI device.
	testPciDev := os.Getenv("TEST_PCI_DEVICE")
	testNsPath := os.Getenv("TEST_NETNS_PATH")
	if testPciDev != "" && testNsPath != "" {
		By("Moving test PCI device '" + testPciDev + "' to netns defined by path '" + testNsPath + "'")
		quitNetNsSet = make(chan bool, 1)
		doneNetNsSet = make(chan error, 1)
		go netns.SetPfVfLinkNetNs(testPciDev, testNsPath, devPollInterval, quitNetNsSet, doneNetNsSet)
	}

	close(done)
}, 60)

var _ = AfterSuite(func() {
	By("tearing down the test environment")
	err := testEnv.Stop()
	Expect(err).ToNot(HaveOccurred())
	if quitNetNsSet != nil && doneNetNsSet != nil {
		By("closing PF/VF link netns watcher")
		quitNetNsSet <- true            // non-blocking
		for msg := range doneNetNsSet { // blocking until goroutine exists
			if msg != nil {
				fmt.Fprintf(GinkgoWriter, "Messages from goroutine during execution of SetPfVfLinkNetNs(): '%s'",
					msg.Error())
			}
		}
		close(quitNetNsSet)
	}
})
