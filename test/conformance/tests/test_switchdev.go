package tests

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	sriovv1 "github.com/k8snetworkplumbingwg/sriov-network-operator/api/v1"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/consts"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/test/util/cluster"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/test/util/discovery"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/test/util/namespaces"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/test/util/network"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/test/util/pod"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("[sriov] Switchdev", Ordered, func() {

	BeforeAll(func() {
		if platformType != consts.Baremetal {
			Skip("Switchdev is not supported on non-baremetal platforms")
		}

		err := namespaces.Create(namespaces.Test, clients)
		Expect(err).ToNot(HaveOccurred())

		err = namespaces.Clean(operatorNamespace, namespaces.Test, clients, discovery.Enabled())
		Expect(err).ToNot(HaveOccurred())

		WaitForSRIOVStable()
	})

	AfterAll(func() {
		err := namespaces.Clean(operatorNamespace, namespaces.Test, clients, discovery.Enabled())
		Expect(err).ToNot(HaveOccurred())
		WaitForSRIOVStable()
	})

	It("create switchdev policies on supported devices", func() {
		sriovInfos, err := cluster.DiscoverSriov(clients, operatorNamespace)
		Expect(err).ToNot(HaveOccurred())
		Expect(len(sriovInfos.Nodes)).ToNot(BeZero())

		testNode, interfaces, err := sriovInfos.FindSwitchdevDevicesAndNode(clients, namespaces.Test)
		Expect(err).ToNot(HaveOccurred())
		if len(interfaces) == 0 {
			Skip("No unused device with a switchdev-capable driver found")
		}

		// Avoid testing the same NIC model more than once
		interfaces = filterDuplicateNICModels(interfaces)

		By(fmt.Sprintf("Testing on node %s, %d devices found", testNode, len(interfaces)))

		previous := isFeatureFlagEnabled(consts.ManageSoftwareBridgesFeatureGate)
		setFeatureFlag(consts.ManageSoftwareBridgesFeatureGate, true)
		DeferCleanup(setFeatureFlag, consts.ManageSoftwareBridgesFeatureGate, previous)
		DeferCleanup(func() {
			Expect(namespaces.Clean(operatorNamespace, namespaces.Test, clients, discovery.Enabled())).To(Succeed())
			WaitForSRIOVStable()
		})
		pool := &sriovv1.SriovNetworkPoolConfig{
			ObjectMeta: metav1.ObjectMeta{Name: "test-switchdev-pool", Namespace: operatorNamespace},
			Spec: sriovv1.SriovNetworkPoolConfigSpec{
				NodeSelector:             &metav1.LabelSelector{MatchLabels: map[string]string{"kubernetes.io/hostname": testNode}},
				OvsHardwareOffloadConfig: sriovv1.OvsHardwareOffloadConfig{OvsConfig: map[string]string{"hw-offload": "true", "tc-policy": "none"}},
			},
		}
		if os.Getenv("CLUSTER_TYPE") == string(consts.ClusterTypeOpenshift) {
			pool.Spec.NodeSelector = nil
			node, err := clients.CoreV1Interface.Nodes().Get(context.Background(), testNode, metav1.GetOptions{})
			Expect(err).ToNot(HaveOccurred())
			desiredConfig := node.Annotations[consts.DesiredMachineConfigAnnotation]
			Expect(desiredConfig).ToNot(BeEmpty(), "The selected node must have a desired MachineConfig")
			machineConfig, err := clients.MachineConfigs().Get(context.Background(), desiredConfig, metav1.GetOptions{})
			Expect(err).ToNot(HaveOccurred())
			for _, owner := range machineConfig.OwnerReferences {
				if owner.Kind == "MachineConfigPool" {
					pool.Spec.OvsHardwareOffloadConfig.Name = owner.Name
					break
				}
			}
			Expect(pool.Spec.OvsHardwareOffloadConfig.Name).ToNot(BeEmpty(), "The selected node's MachineConfig must belong to a MachineConfigPool")
			By("Enabling OVS hardware offload for MachineConfigPool " + pool.Spec.OvsHardwareOffloadConfig.Name)
		}
		Expect(clients.Create(context.Background(), pool)).To(Succeed())
		for _, intf := range interfaces {
			By("Testing device " + nameVendorDeviceID(intf) + " on node " + testNode)
			numVfs := 5
			resourceName := "switchdev" + intf.Name
			policy, err := network.CreateSriovPolicy(clients, "test-switchdev-policy-", operatorNamespace, intf.Name, testNode, numVfs, resourceName, "netdevice", func(snnp *sriovv1.SriovNetworkNodePolicy) {
				snnp.Spec.EswitchMode = "switchdev"
				snnp.Spec.Bridge.OVS = &sriovv1.OVSConfig{}
			})
			Expect(err).ToNot(HaveOccurred())
			Eventually(func(g Gomega) bool {
				state := &sriovv1.SriovNetworkNodeState{}
				g.Expect(clients.Get(context.Background(), client.ObjectKey{Namespace: operatorNamespace, Name: testNode}, state)).To(Succeed())
				for _, iface := range state.Spec.Interfaces {
					for _, group := range iface.VfGroups {
						if iface.PciAddress == intf.PciAddress && iface.EswitchMode == "switchdev" && group.PolicyName == policy.Name {
							return true
						}
					}
				}
				return false
			}, 2*time.Minute, time.Second).Should(BeTrue(), "Switchdev policy must be reconciled before waiting for readiness")

			WaitForSRIOVStable()
			Eventually(func(g Gomega) {
				for key, value := range pool.Spec.OvsHardwareOffloadConfig.OvsConfig {
					output, stderr, err := runCommandOnConfigDaemon(testNode, "chroot", "/host", "ovs-vsctl", "get", "Open_vSwitch", ".", "other_config:"+key)
					g.Expect(err).ToNot(HaveOccurred(), stderr)
					g.Expect(strings.Trim(strings.TrimSpace(output), `"`)).To(Equal(value))
				}
			}, waitingTime, time.Second).Should(Succeed(), "OVS must apply the pool's hardware offload configuration")

			Eventually(func() int64 {
				testedNode, err := clients.CoreV1Interface.Nodes().Get(context.Background(), testNode, metav1.GetOptions{})
				Expect(err).ToNot(HaveOccurred())
				resNum := testedNode.Status.Allocatable[corev1.ResourceName("openshift.io/"+resourceName)]
				capacity, _ := resNum.AsInt64()
				return capacity
			}, 10*time.Minute, time.Second).Should(Equal(int64(numVfs)))
			Eventually(func(g Gomega) {
				state := &sriovv1.SriovNetworkNodeState{}
				g.Expect(clients.Get(context.Background(), client.ObjectKey{Namespace: operatorNamespace, Name: testNode}, state)).To(Succeed())
				g.Expect(state.Status.Interfaces).To(ContainElement(And(
					HaveField("PciAddress", intf.PciAddress), HaveField("EswitchMode", "switchdev"), HaveField("NumVfs", numVfs))))
				g.Expect(state.Status.Bridges.OVS).To(ContainElement(HaveField("Uplinks", ContainElement(HaveField("PciAddress", intf.PciAddress)))))
			}, 5*time.Minute, time.Second).Should(Succeed(), "The daemon must report the configured PF and managed bridge")
			WaitForSRIOVStable()
			verifySwitchdevTraffic(testNode, intf, resourceName, numVfs)
		}
	})
})

func verifySwitchdevTraffic(node string, intf *sriovv1.InterfaceExt, resourceName string, numVfs int) {
	By("Connecting two pods through the operator-managed OVS network")
	net := &sriovv1.OVSNetwork{
		ObjectMeta: metav1.ObjectMeta{GenerateName: "test-switchdev-ovs-", Namespace: operatorNamespace},
		Spec: sriovv1.OVSNetworkSpec{ResourceName: resourceName, NetworkNamespace: namespaces.Test,
			IPAM: `{"type":"host-local","subnet":"198.19.0.0/24"}`},
	}
	Expect(clients.Create(context.Background(), net)).To(Succeed())
	DeferCleanup(func() { Expect(clients.Delete(context.Background(), net)).To(Succeed()) })
	assertCondition(net, sriovv1.ConditionReady, metav1.ConditionTrue)
	a := createTestPod(node, []string{net.Name})
	b := createTestPod(node, []string{net.Name})
	for _, p := range []*corev1.Pod{a, b} {
		Eventually(func(g Gomega) {
			current, err := clients.Pods(p.Namespace).Get(context.Background(), p.Name, metav1.GetOptions{})
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(current.Status.Phase).To(Equal(corev1.PodRunning))
			g.Expect(current.Status.Conditions).To(ContainElement(And(
				HaveField("Type", corev1.PodReady), HaveField("Status", corev1.ConditionTrue))))
		}, time.Minute, time.Second).Should(Succeed())
	}
	ipsA, err := network.GetSriovNicIPs(a, "net1")
	Expect(err).ToNot(HaveOccurred())
	ipsB, err := network.GetSriovNicIPs(b, "net1")
	Expect(err).ToNot(HaveOccurred())
	Expect(ipsA).To(HaveLen(1))
	Expect(ipsB).To(HaveLen(1))
	vfIndex := func(p *corev1.Pod) (int, string) {
		var status []struct {
			Interface  string `json:"interface"`
			MAC        string `json:"mac"`
			DeviceInfo struct {
				PCI struct {
					Address string `json:"pci-address"`
				} `json:"pci"`
			} `json:"device-info"`
		}
		Expect(json.Unmarshal([]byte(p.Annotations["k8s.v1.cni.cncf.io/network-status"]), &status)).To(Succeed())
		for _, net := range status {
			if net.Interface != "net1" {
				continue
			}
			Expect(net.DeviceInfo.PCI.Address).ToNot(BeEmpty())
			Expect(net.MAC).ToNot(BeEmpty())
			for i := 0; i < numVfs; i++ {
				output, stderr, err := runCommandOnConfigDaemon(node, "readlink", fmt.Sprintf("/host/sys/bus/pci/devices/%s/virtfn%d", intf.PciAddress, i))
				Expect(err).ToNot(HaveOccurred(), stderr)
				if filepath.Base(strings.TrimSpace(output)) == net.DeviceInfo.PCI.Address {
					return i, net.MAC
				}
			}
		}
		Fail("Pod VF does not belong to the selected PF")
		return -1, ""
	}
	vfA, macA := vfIndex(a)
	vfB, macB := vfIndex(b)
	Expect(vfA).ToNot(Equal(vfB))
	output, stderr, err := runCommandOnConfigDaemon(node, "chroot", "/host", "devlink", "-j", "port", "show")
	Expect(err).ToNot(HaveOccurred(), stderr)
	var ports struct {
		Port map[string]struct {
			Flavor string `json:"flavour"` //nolint:misspell // devlink uses this JSON field name.
			Netdev string `json:"netdev"`
			VF     int    `json:"vfnum"`
		} `json:"port"`
	}
	Expect(json.Unmarshal([]byte(output), &ports)).To(Succeed())
	representors := map[int]string{}
	for name, port := range ports.Port {
		if strings.HasPrefix(name, "pci/"+intf.PciAddress+"/") && port.Flavor == "pcivf" {
			representors[port.VF] = port.Netdev
		}
	}
	Expect(representors[vfA]).ToNot(BeEmpty())
	Expect(representors[vfB]).ToNot(BeEmpty())
	packets := func(inPort, outPort, srcMAC, dstMAC string) uint64 {
		output, stderr, err := runCommandOnConfigDaemon(node, "chroot", "/host", "ovs-appctl", "dpctl/dump-flows", "--names", "type=offloaded")
		Expect(err).ToNot(HaveOccurred(), stderr)
		count, err := network.OffloadedPackets(output, inPort, outPort, srcMAC, dstMAC)
		Expect(err).ToNot(HaveOccurred())
		GinkgoWriter.Println(output)
		return count
	}
	directions := []struct {
		from                                *corev1.Pod
		to, inPort, outPort, srcMAC, dstMAC string
	}{
		{a, ipsB[0], representors[vfA], representors[vfB], macA, macB},
		{b, ipsA[0], representors[vfB], representors[vfA], macB, macA},
	}
	By("Checking bidirectional ping and increasing OVS offloaded IPv4 packet counters")
	for _, direction := range directions {
		before := packets(direction.inPort, direction.outPort, direction.srcMAC, direction.dstMAC)
		Eventually(func(g Gomega) {
			output, stderr, err := pod.ExecCommand(clients, direction.from, "ping", "-I", "net1", "-c", "5", "-W", "2", direction.to)
			g.Expect(err).ToNot(HaveOccurred(), output+stderr)
			g.Expect(output).To(ContainSubstring("5 packets transmitted, 5 received, 0% packet loss"))
			GinkgoWriter.Println(output)
			g.Expect(packets(direction.inPort, direction.outPort, direction.srcMAC, direction.dstMAC)).To(BeNumerically(">", before), "Traffic must increase the selected VF pair's offloaded packet counters")
		}, time.Minute, time.Second).Should(Succeed())
	}
	// Keep datapath flows alive while exec calls collect TC and OVS evidence.
	for _, direction := range directions {
		_, stderr, err := pod.ExecCommand(clients, direction.from, "sh", "-c",
			`ping -I net1 -i 0.2 -w 90 "$1" >/tmp/switchdev-keepalive.log 2>&1 &`, "--", direction.to)
		Expect(err).ToNot(HaveOccurred(), stderr)
	}
	By("Checking kernel TC in_hw and hardware packet counters on the selected VF pair")
	for _, direction := range directions {
		Eventually(func(g Gomega) {
			output, stderr, err := runCommandOnConfigDaemon(node, "chroot", "/host", "tc", "-s", "-j", "filter", "show", "dev", direction.inPort, "ingress")
			g.Expect(err).ToNot(HaveOccurred(), stderr)
			var filters []struct {
				Options struct {
					InHW bool `json:"in_hw"`
					Keys struct {
						EthType string `json:"eth_type"`
						SrcMAC  string `json:"src_mac"`
						DstMAC  string `json:"dst_mac"`
					} `json:"keys"`
					Actions []struct {
						ToDev string `json:"to_dev"`
						Stats struct {
							HWPackets uint64 `json:"hw_packets"`
						} `json:"stats"`
					} `json:"actions"`
				} `json:"options"`
			}
			g.Expect(json.Unmarshal([]byte(output), &filters)).To(Succeed())
			var count uint64
			for _, filter := range filters {
				opts := filter.Options
				if opts.InHW && opts.Keys.EthType == "ipv4" && opts.Keys.SrcMAC == direction.srcMAC && opts.Keys.DstMAC == direction.dstMAC {
					for _, action := range opts.Actions {
						if action.ToDev == direction.outPort {
							count += action.Stats.HWPackets
						}
					}
				}
			}
			GinkgoWriter.Println(output)
			g.Expect(count).To(BeNumerically(">", 0), "The matching TC redirect must execute in hardware")
		}, time.Minute, time.Second).Should(Succeed())
	}
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
