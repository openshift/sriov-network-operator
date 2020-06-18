package e2e

import (
	// goctx "context"
	// "encoding/json"
	// "fmt"
	// "reflect"
	// "strings"
	"testing"
	// "time"

	// dptypes "github.com/intel/sriov-network-device-plugin/pkg/types"
	framework "github.com/operator-framework/operator-sdk/pkg/test"
	// "github.com/operator-framework/operator-sdk/pkg/test/e2eutil"
	appsv1 "k8s.io/api/apps/v1"
	// corev1 "k8s.io/api/core/v1"
	// "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	// "k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	// dynclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/openshift/sriov-network-operator/pkg/apis"
	// netattdefv1 "github.com/openshift/sriov-network-operator/pkg/apis/k8s/v1"
	sriovnetworkv1 "github.com/openshift/sriov-network-operator/pkg/apis/sriovnetwork/v1"

	. "github.com/onsi/ginkgo"
	"github.com/onsi/ginkgo/config"
	. "github.com/onsi/gomega"

	. "github.com/openshift/sriov-network-operator/test/util"
	testclient "github.com/openshift/sriov-network-operator/test/util/client"
	"github.com/openshift/sriov-network-operator/test/util/cluster"
)

var namespace = "openshift-sriov-network-operator"
var oprctx framework.TestCtx

func TestSriovTests(t *testing.T) {
	snetList := &sriovnetworkv1.SriovNetworkList{
		TypeMeta: metav1.TypeMeta{
			Kind:       "SriovNetwork",
			APIVersion: sriovnetworkv1.SchemeGroupVersion.String(),
		},
	}
	err := framework.AddToFrameworkScheme(apis.AddToScheme, snetList)
	if err != nil {
		t.Logf("failed to add custom resource scheme to framework: %v", err)
	}

	config.GinkgoConfig.ParallelTotal = 1
	RegisterFailHandler(Fail)
	RunSpecs(t, "OperatorTests Suite")
}

var sriovInfos *cluster.EnabledNodes
var sriovIface *sriovnetworkv1.InterfaceExt

var _ = BeforeSuite(func() {
	// get global framework variables
	f := framework.Global
	// wait for sriov-network-operator to be ready
	deploy := &appsv1.Deployment{}
	err := WaitForNamespacedObject(deploy, f.Client, namespace, "sriov-network-operator", RetryInterval, Timeout)
	Expect(err).NotTo(HaveOccurred())
	clients := testclient.New("")
	var sriovInfos *cluster.EnabledNodes
	err = wait.PollImmediate(RetryInterval, Timeout*15, func() (done bool, err error) {
		sriovInfos, err = cluster.DiscoverSriov(clients, namespace)
		if sriovInfos == nil {
			return false, nil
		}
		return true, nil
	})
	Expect(err).ToNot(HaveOccurred())
	Expect(len(sriovInfos.Nodes)).Should(BeNumerically(">=", 1))
	sriovIface, err = sriovInfos.FindOneSriovDevice(sriovInfos.Nodes[0])
	Expect(err).ToNot(HaveOccurred())
	Expect(sriovIface).ToNot(BeNil())
})

var _ = AfterSuite(func() {
	oprctx.Cleanup()
})
