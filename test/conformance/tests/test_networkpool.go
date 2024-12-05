package tests

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"golang.org/x/net/context"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	sriovv1 "github.com/k8snetworkplumbingwg/sriov-network-operator/api/v1"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/consts"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/test/util/cluster"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/test/util/discovery"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/test/util/namespaces"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/test/util/network"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/test/util/pod"
)

var _ = Describe("[sriov] NetworkPool", Ordered, func() {
	var testNode string
	var interfaces []*sriovv1.InterfaceExt

	BeforeAll(func() {
		err := namespaces.Create(namespaces.Test, clients)
		Expect(err).ToNot(HaveOccurred())
		err = namespaces.Clean(operatorNamespace, namespaces.Test, clients, discovery.Enabled())
		Expect(err).ToNot(HaveOccurred())

		sriovInfos, err := cluster.DiscoverSriov(clients, operatorNamespace)
		Expect(err).ToNot(HaveOccurred())
		Expect(len(sriovInfos.Nodes)).ToNot(BeZero())

		testNode, interfaces, err = sriovInfos.FindSriovDevicesAndNode()
		Expect(err).ToNot(HaveOccurred())

		By(fmt.Sprintf("Testing on node %s, %d devices found", testNode, len(interfaces)))
		WaitForSRIOVStable()
	})

	AfterEach(func() {
		err := namespaces.Clean(operatorNamespace, namespaces.Test, clients, discovery.Enabled())
		Expect(err).ToNot(HaveOccurred())

		err = clients.DeleteAllOf(context.Background(), &sriovv1.SriovNetworkPoolConfig{}, client.InNamespace(operatorNamespace))
		Expect(err).ToNot(HaveOccurred())
		WaitForSRIOVStable()
	})

	Context("Configure rdma namespace mode", func() {
		It("should switch rdma mode", func() {
			By("create a pool with only that node")
			networkPool := &sriovv1.SriovNetworkPoolConfig{
				ObjectMeta: metav1.ObjectMeta{Name: testNode, Namespace: operatorNamespace},
				Spec: sriovv1.SriovNetworkPoolConfigSpec{RdmaMode: consts.RdmaSubsystemModeExclusive,
					NodeSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"kubernetes.io/hostname": testNode}}}}

			By("configure rdma mode to exclusive")
			err := clients.Create(context.Background(), networkPool)
			Expect(err).ToNot(HaveOccurred())
			By("waiting for operator to finish the configuration")
			WaitForSRIOVStable()
			nodeState := &sriovv1.SriovNetworkNodeState{}
			err = clients.Get(context.Background(), client.ObjectKey{Name: testNode, Namespace: operatorNamespace}, nodeState)
			Expect(err).ToNot(HaveOccurred())
			Expect(nodeState.Spec.System.RdmaMode).To(Equal(consts.RdmaSubsystemModeExclusive))
			Expect(nodeState.Status.System.RdmaMode).To(Equal(consts.RdmaSubsystemModeExclusive))

			By("Checking rdma mode and kernel args")
			output, _, err := runCommandOnConfigDaemon(testNode, "/bin/bash", "-c", "cat /host/proc/cmdline | grep ib_core.netns_mode=0 | wc -l")
			Expect(err).ToNot(HaveOccurred())
			Expect(strings.HasPrefix(output, "1")).To(BeTrue())

			output, _, err = runCommandOnConfigDaemon(testNode, "/bin/bash", "-c", "cat /host/proc/cmdline | grep ib_core.netns_mode=1 | wc -l")
			Expect(err).ToNot(HaveOccurred())
			Expect(strings.HasPrefix(output, "0")).To(BeTrue())

			output, _, err = runCommandOnConfigDaemon(testNode, "/bin/bash", "-c", "cat /host/etc/modprobe.d/sriov_network_operator_modules_config.conf  | grep mode=0 | wc -l")
			Expect(err).ToNot(HaveOccurred())
			Expect(strings.HasPrefix(output, "1")).To(BeTrue())

			By("configure rdma mode to shared")
			networkPool.Spec.RdmaMode = consts.RdmaSubsystemModeShared
			err = clients.Update(context.Background(), networkPool)
			Expect(err).ToNot(HaveOccurred())
			WaitForSRIOVStable()
			err = clients.Get(context.Background(), client.ObjectKey{Name: testNode, Namespace: operatorNamespace}, nodeState)
			Expect(err).ToNot(HaveOccurred())
			Expect(nodeState.Spec.System.RdmaMode).To(Equal(consts.RdmaSubsystemModeShared))
			Expect(nodeState.Status.System.RdmaMode).To(Equal(consts.RdmaSubsystemModeShared))

			By("Checking rdma mode and kernel args")
			output, _, err = runCommandOnConfigDaemon(testNode, "/bin/bash", "-c", "cat /host/proc/cmdline | grep ib_core.netns_mode=0 | wc -l")
			Expect(err).ToNot(HaveOccurred())
			Expect(strings.HasPrefix(output, "0")).To(BeTrue())

			output, _, err = runCommandOnConfigDaemon(testNode, "/bin/bash", "-c", "cat /host/proc/cmdline | grep ib_core.netns_mode=1 | wc -l")
			Expect(err).ToNot(HaveOccurred())
			Expect(strings.HasPrefix(output, "1")).To(BeTrue())

			output, _, err = runCommandOnConfigDaemon(testNode, "/bin/bash", "-c", "cat /host/etc/modprobe.d/sriov_network_operator_modules_config.conf  | grep mode=1 | wc -l")
			Expect(err).ToNot(HaveOccurred())
			Expect(strings.HasPrefix(output, "1")).To(BeTrue())

			By("removing rdma mode configuration")
			err = clients.Delete(context.Background(), networkPool)
			Expect(err).ToNot(HaveOccurred())
			WaitForSRIOVStable()

			err = clients.Get(context.Background(), client.ObjectKey{Name: testNode, Namespace: operatorNamespace}, nodeState)
			Expect(err).ToNot(HaveOccurred())
			Expect(nodeState.Spec.System.RdmaMode).To(Equal(""))
			Expect(nodeState.Status.System.RdmaMode).To(Equal(consts.RdmaSubsystemModeShared))

			By("Checking rdma mode and kernel args")
			output, _, err = runCommandOnConfigDaemon(testNode, "/bin/bash", "-c", "cat /host/proc/cmdline | grep ib_core.netns_mode=0 | wc -l")
			Expect(err).ToNot(HaveOccurred())
			Expect(strings.HasPrefix(output, "0")).To(BeTrue())

			output, _, err = runCommandOnConfigDaemon(testNode, "/bin/bash", "-c", "cat /host/proc/cmdline | grep ib_core.netns_mode=1 | wc -l")
			Expect(err).ToNot(HaveOccurred())
			Expect(strings.HasPrefix(output, "0")).To(BeTrue())

			output, _, err = runCommandOnConfigDaemon(testNode, "/bin/bash", "-c", "ls /host/etc/modprobe.d | grep sriov_network_operator_modules_config.conf | wc -l")
			Expect(err).ToNot(HaveOccurred())
			Expect(strings.HasPrefix(output, "0")).To(BeTrue())
		})
	})

	Context("Check rdma metrics inside a pod in exclusive mode", func() {
		var iface *sriovv1.InterfaceExt

		BeforeAll(func() {
			sriovInfos, err := cluster.DiscoverSriov(clients, operatorNamespace)
			Expect(err).ToNot(HaveOccurred())
			Expect(len(sriovInfos.Nodes)).ToNot(BeZero())

			for _, node := range sriovInfos.Nodes {
				iface, err = sriovInfos.FindOneMellanoxSriovDevice(node)
				if err == nil {
					testNode = node
					break
				}
			}

			if iface == nil {
				Skip("no mellanox card available to test rdma")
			}

			networkPool := &sriovv1.SriovNetworkPoolConfig{
				ObjectMeta: metav1.ObjectMeta{Name: testNode, Namespace: operatorNamespace},
				Spec: sriovv1.SriovNetworkPoolConfigSpec{RdmaMode: consts.RdmaSubsystemModeExclusive,
					NodeSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"kubernetes.io/hostname": testNode}}}}

			err = clients.Create(context.Background(), networkPool)
			Expect(err).ToNot(HaveOccurred())
			By("waiting for operator to finish the configuration")
			WaitForSRIOVStable()
		})

		It("should run pod with RDMA cni and expose nic metrics and another one without rdma info", func() {
			By("creating a policy")
			resourceName := "testrdma"
			_, err := network.CreateSriovPolicy(clients, "test-policy-", operatorNamespace, iface.Name, testNode, 5, resourceName, "netdevice",
				func(policy *sriovv1.SriovNetworkNodePolicy) { policy.Spec.IsRdma = true })
			Expect(err).ToNot(HaveOccurred())
			WaitForSRIOVStable()

			By("Creating sriov network to use the rdma device")
			sriovNetwork := &sriovv1.SriovNetwork{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-rdmanetwork",
					Namespace: operatorNamespace,
				},
				Spec: sriovv1.SriovNetworkSpec{
					ResourceName:      resourceName,
					IPAM:              `{"type":"host-local","subnet":"10.10.10.0/24","rangeStart":"10.10.10.171","rangeEnd":"10.10.10.181"}`,
					NetworkNamespace:  namespaces.Test,
					MetaPluginsConfig: `{"type": "rdma"}`,
				}}

			err = clients.Create(context.Background(), sriovNetwork)
			Expect(err).ToNot(HaveOccurred())
			waitForNetAttachDef("test-rdmanetwork", namespaces.Test)

			sriovNetwork = &sriovv1.SriovNetwork{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-nordmanetwork",
					Namespace: operatorNamespace,
				},
				Spec: sriovv1.SriovNetworkSpec{
					ResourceName:     resourceName,
					IPAM:             `{"type":"host-local","subnet":"10.10.10.0/24","rangeStart":"10.10.10.171","rangeEnd":"10.10.10.181"}`,
					NetworkNamespace: namespaces.Test,
				}}

			err = clients.Create(context.Background(), sriovNetwork)
			Expect(err).ToNot(HaveOccurred())
			waitForNetAttachDef("test-nordmanetwork", namespaces.Test)

			podDefinition := pod.DefineWithNetworks([]string{"test-rdmanetwork"})
			firstPod, err := clients.Pods(namespaces.Test).Create(context.Background(), podDefinition, metav1.CreateOptions{})
			Expect(err).ToNot(HaveOccurred())

			podDefinition = pod.DefineWithNetworks([]string{"test-nordmanetwork"})
			secondPod, err := clients.Pods(namespaces.Test).Create(context.Background(), podDefinition, metav1.CreateOptions{})
			Expect(err).ToNot(HaveOccurred())

			firstPod = waitForPodRunning(firstPod)
			secondPod = waitForPodRunning(secondPod)

			testedNode := &corev1.Node{}
			err = clients.Get(context.Background(), client.ObjectKey{Name: testNode}, testedNode)
			Expect(err).ToNot(HaveOccurred())
			resNum := testedNode.Status.Allocatable[corev1.ResourceName("openshift.io/"+resourceName)]
			allocatable, _ := resNum.AsInt64()
			Expect(allocatable).ToNot(Equal(5))

			By("restart device plugin")
			pods, err := clients.Pods(operatorNamespace).List(context.Background(), metav1.ListOptions{
				LabelSelector: "app=sriov-device-plugin",
				FieldSelector: "spec.nodeName=" + testNode,
			})
			Expect(err).ToNot(HaveOccurred())

			for _, podObj := range pods.Items {
				err = clients.Delete(context.Background(), &podObj)
				Expect(err).ToNot(HaveOccurred())
				Eventually(func() bool {
					searchPod := &corev1.Pod{}
					err = clients.Get(context.Background(), client.ObjectKey{Name: podObj.Name, Namespace: podObj.Namespace}, searchPod)
					if err != nil && errors.IsNotFound(err) {
						return true
					}
					return false
				}, 2*time.Minute, time.Second).Should(BeTrue())
			}

			By("checking the amount of allocatable devices remains after device plugin reset")
			Consistently(func() int64 {
				err = clients.Get(context.Background(), client.ObjectKey{Name: testNode}, testedNode)
				Expect(err).ToNot(HaveOccurred())
				resNum := testedNode.Status.Allocatable[corev1.ResourceName("openshift.io/"+resourceName)]
				newAllocatable, _ := resNum.AsInt64()
				return newAllocatable
			}, 1*time.Minute, 5*time.Second).Should(Equal(allocatable))

			By("checking counters inside the pods")
			strOut, _, err := pod.ExecCommand(clients, firstPod, "/bin/bash", "-c", "ip link show net1 | grep net1 | wc -l")
			Expect(err).ToNot(HaveOccurred())
			Expect(strings.HasPrefix(strOut, "1")).To(BeTrue())
			strOut, _, err = pod.ExecCommand(clients, firstPod, "/bin/bash", "-c", "ls /sys/bus/pci/devices/${PCIDEVICE_OPENSHIFT_IO_TESTRDMA}/infiniband/*/ports/*/hw_counters | wc -l")
			strOut = strings.TrimSpace(strOut)
			Expect(err).ToNot(HaveOccurred())
			num, err := strconv.Atoi(strOut)
			Expect(err).ToNot(HaveOccurred())
			Expect(num).To(BeNumerically(">", 0))

			strOut, _, err = pod.ExecCommand(clients, secondPod, "/bin/bash", "-c", "ls /sys/bus/pci/devices/${PCIDEVICE_OPENSHIFT_IO_TESTRDMA}/infiniband/ | wc -l")
			Expect(err).ToNot(HaveOccurred())
			strOut = strings.TrimSpace(strOut)
			num, err = strconv.Atoi(strOut)
			Expect(err).ToNot(HaveOccurred())
			Expect(num).To(BeNumerically("==", 0))
		})
	})

	Context("Check rdma metrics inside a pod in shared mode not exist", func() {
		var iface *sriovv1.InterfaceExt
		BeforeAll(func() {
			sriovInfos, err := cluster.DiscoverSriov(clients, operatorNamespace)
			Expect(err).ToNot(HaveOccurred())
			Expect(len(sriovInfos.Nodes)).ToNot(BeZero())

			for _, node := range sriovInfos.Nodes {
				iface, err = sriovInfos.FindOneMellanoxSriovDevice(node)
				if err == nil {
					testNode = node
					break
				}
			}

			if iface == nil {
				Skip("no mellanox card available to test rdma")
			}

			networkPool := &sriovv1.SriovNetworkPoolConfig{
				ObjectMeta: metav1.ObjectMeta{Name: testNode, Namespace: operatorNamespace},
				Spec: sriovv1.SriovNetworkPoolConfigSpec{RdmaMode: consts.RdmaSubsystemModeShared,
					NodeSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"kubernetes.io/hostname": testNode}}}}

			err = clients.Create(context.Background(), networkPool)
			Expect(err).ToNot(HaveOccurred())
			By("waiting for operator to finish the configuration")
			WaitForSRIOVStable()
		})

		It("should run pod without RDMA cni and not expose nic metrics", func() {
			By("creating a policy")
			resourceName := "testrdma"
			_, err := network.CreateSriovPolicy(clients, "test-policy-", operatorNamespace, iface.Name, testNode, 5, resourceName, "netdevice",
				func(policy *sriovv1.SriovNetworkNodePolicy) { policy.Spec.IsRdma = true })
			Expect(err).ToNot(HaveOccurred())
			WaitForSRIOVStable()

			By("Creating sriov network to use the rdma device")
			sriovNetwork := &sriovv1.SriovNetwork{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-rdmanetwork",
					Namespace: operatorNamespace,
				},
				Spec: sriovv1.SriovNetworkSpec{
					ResourceName:     resourceName,
					IPAM:             `{"type":"host-local","subnet":"10.10.10.0/24","rangeStart":"10.10.10.171","rangeEnd":"10.10.10.181"}`,
					NetworkNamespace: namespaces.Test,
				}}

			err = clients.Create(context.Background(), sriovNetwork)
			Expect(err).ToNot(HaveOccurred())
			waitForNetAttachDef("test-rdmanetwork", namespaces.Test)

			podDefinition := pod.DefineWithNetworks([]string{"test-rdmanetwork"})
			firstPod, err := clients.Pods(namespaces.Test).Create(context.Background(), podDefinition, metav1.CreateOptions{})
			Expect(err).ToNot(HaveOccurred())
			firstPod = waitForPodRunning(firstPod)

			strOut, _, err := pod.ExecCommand(clients, firstPod, "/bin/bash", "-c", "ip link show net1 | grep net1 | wc -l")
			Expect(err).ToNot(HaveOccurred())
			Expect(strings.HasPrefix(strOut, "1")).To(BeTrue())
			strOut, _, err = pod.ExecCommand(clients, firstPod, "/bin/bash", "-c", "ls /sys/bus/pci/devices/${PCIDEVICE_OPENSHIFT_IO_TESTRDMA}/infiniband/*/ports/* | grep hw_counters | wc -l")
			strOut = strings.TrimSpace(strOut)
			Expect(err).ToNot(HaveOccurred())
			num, err := strconv.Atoi(strOut)
			Expect(err).ToNot(HaveOccurred())
			Expect(num).To(BeNumerically("==", 0))
		})
	})
})
