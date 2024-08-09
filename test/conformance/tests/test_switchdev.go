package tests

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	sriovv1 "github.com/k8snetworkplumbingwg/sriov-network-operator/api/v1"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/test/util/cluster"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/test/util/discovery"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/test/util/namespaces"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/test/util/network"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("[sriov] Switchdev", Ordered, func() {

	BeforeAll(func() {
		if cluster.VirtualCluster() {
			Skip("IGB driver does not support switchdev driver model")
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

		// Avoid testing against primary NIC
		interfaces, err = findUnusedSriovDevices(testNode, interfaces)
		Expect(err).ToNot(HaveOccurred())

		// Avoid testing the same NIC model more than once
		interfaces = filterDuplicateNICModels(interfaces)

		By(fmt.Sprintf("Testing on node %s, %d devices found", testNode, len(interfaces)))

		for _, intf := range interfaces {
			if !doesInterfaceSupportSwitchdev(intf) {
				continue
			}

			By("Testing device " + nameVendorDeviceID(intf) + " on node " + testNode)
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

func filterDuplicateNICModels(devices []*sriovv1.InterfaceExt) []*sriovv1.InterfaceExt {
	foundVendorAndModels := map[string]bool{}
	ret := []*sriovv1.InterfaceExt{}

	for _, device := range devices {
		vendorAndDevice := device.Vendor + "_" + device.DeviceID
		if _, value := foundVendorAndModels[vendorAndDevice]; !value {
			ret = append(ret, device)
			foundVendorAndModels[vendorAndDevice] = true
		}
	}
	return ret
}

func nameVendorDeviceID(intf *sriovv1.InterfaceExt) string {
	return fmt.Sprintf("%s (%s:%s)", intf.Name, intf.Vendor, intf.DeviceID)
}
