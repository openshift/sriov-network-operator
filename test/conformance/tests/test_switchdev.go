package tests

import (
	"context"
	"fmt"
	"time"

	sriovv1 "github.com/k8snetworkplumbingwg/sriov-network-operator/api/v1"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/test/util/cluster"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/test/util/discovery"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/test/util/namespaces"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/test/util/network"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("[sriov] Switchdev", Ordered, func() {

	BeforeAll(func() {
		if cluster.VirtualCluster() {
			Skip("IGB driver does not support VF statistics")
		}

		err := namespaces.Create(namespaces.Test, clients)
		Expect(err).ToNot(HaveOccurred())

		err = namespaces.Clean(operatorNamespace, namespaces.Test, clients, discovery.Enabled())
		Expect(err).ToNot(HaveOccurred())

		WaitForSRIOVStable()
	})

	It("create switchdev policies on supported devices", func() {
		sriovInfos, err := cluster.DiscoverSriov(clients, operatorNamespace)
		Expect(err).ToNot(HaveOccurred())
		Expect(len(sriovInfos.Nodes)).ToNot(BeZero())

		testNode, interfaces, err := sriovInfos.FindSriovDevicesAndNode()
		Expect(err).ToNot(HaveOccurred())

		By(fmt.Sprintf("Testing on node %s, %d devices found", testNode, len(interfaces)))

		mainDevice := findMainSriovDevice(getConfigDaemonPod(testNode), interfaces)
		if mainDevice == nil {
			mainDevice = &sriovv1.InterfaceExt{}
			By("No SR-IOV device is the primary NIC of the node")
		} else {
			By("Skipping " + mainDevice.Name + " as it is the primary NIC")
		}

		for _, intf := range interfaces {
			if !doesInterfaceSupportSwitchdev(intf) {
				continue
			}

			if intf.Name == mainDevice.Name {
				// Avoid testing switchdev on main NIC
				continue
			}

			By("Testing device " + intf.Name + " on node " + testNode)
			resourceName := "swtichdev" + intf.Name
			_, err = network.CreateSriovPolicy(clients, "test-switchdev-policy-", operatorNamespace, intf.Name, testNode, 8, resourceName, "netdevice", func(snnp *sriovv1.SriovNetworkNodePolicy) {
				snnp.Spec.EswitchMode = "switchdev"
			})
			Expect(err).ToNot(HaveOccurred())

			WaitForSRIOVStable()

			Eventually(func() int64 {
				testedNode, err := clients.CoreV1Interface.Nodes().Get(context.Background(), testNode, metav1.GetOptions{})
				Expect(err).ToNot(HaveOccurred())
				resNum := testedNode.Status.Allocatable[corev1.ResourceName("openshift.io/"+resourceName)]
				capacity, _ := resNum.AsInt64()
				return capacity
			}, 10*time.Minute, time.Second).Should(Equal(int64(8)))
		}
	})
})

func doesInterfaceSupportSwitchdev(intf *sriovv1.InterfaceExt) bool {
	if intf.Driver == "mlx5_core" {
		return true
	}

	if intf.Driver == "ice" {
		return true
	}

	return false
}
