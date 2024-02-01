package e2e

import (
	goctx "context"
	"strconv"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	sriovnetworkv1 "github.com/k8snetworkplumbingwg/sriov-network-operator/api/v1"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	. "github.com/k8snetworkplumbingwg/sriov-network-operator/test/util"
	testclient "github.com/k8snetworkplumbingwg/sriov-network-operator/test/util/client"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/test/util/cluster"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/test/util/execute"
)

var _ = Describe("Operator", func() {
	var sriovInfos *cluster.EnabledNodes
	var err error
	var nodeState *sriovnetworkv1.SriovNetworkNodeState
	var name string

	execute.BeforeAll(func() {
		clients := testclient.New("")
		Expect(clients).ToNot(BeNil())

		Eventually(func() *cluster.EnabledNodes {
			sriovInfos, _ = cluster.DiscoverSriov(clients, testNamespace)
			return sriovInfos
		}, timeout, interval).ShouldNot(BeNil())
		Expect(len(sriovInfos.Nodes)).To(BeNumerically(">", 0))

		sriovIface, err = sriovInfos.FindOneSriovDevice(sriovInfos.Nodes[0])
		Expect(err).ToNot(HaveOccurred())
		Expect(sriovIface).ToNot(BeNil())
	})

	Context("with single policy", func() {
		policy1 := &sriovnetworkv1.SriovNetworkNodePolicy{
			TypeMeta: metav1.TypeMeta{
				Kind:       "SriovNetworkNodePolicy",
				APIVersion: "sriovnetwork.openshift.io/v1",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      "policy-1",
				Namespace: testNamespace,
			},
			Spec: sriovnetworkv1.SriovNetworkNodePolicySpec{
				ResourceName: "resource_1",
				NodeSelector: map[string]string{
					"feature.node.kubernetes.io/network-sriov.capable": "true",
				},
				Priority:    99,
				Mtu:         9000,
				NumVfs:      6,
				NicSelector: sriovnetworkv1.SriovNetworkNicSelector{},
				DeviceType:  "vfio-pci",
			},
		}
		policy2 := &sriovnetworkv1.SriovNetworkNodePolicy{
			TypeMeta: metav1.TypeMeta{
				Kind:       "SriovNetworkNodePolicy",
				APIVersion: "sriovnetwork.openshift.io/v1",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      "policy-2",
				Namespace: testNamespace,
			},
			Spec: sriovnetworkv1.SriovNetworkNodePolicySpec{
				ResourceName: "resource_2",
				NodeSelector: map[string]string{
					"feature.node.kubernetes.io/network-sriov.capable": "true",
				},
				Priority:    99,
				Mtu:         9000,
				NumVfs:      6,
				NicSelector: sriovnetworkv1.SriovNetworkNicSelector{},
			},
		}

		JustBeforeEach(func() {
			By("wait for the node state ready")
			name = sriovInfos.Nodes[0]
			nodeState = &sriovnetworkv1.SriovNetworkNodeState{}
			err = WaitForSriovNetworkNodeStateReady(nodeState, k8sClient, testNamespace, name, RetryInterval, Timeout*15)
			Expect(err).NotTo(HaveOccurred())
		})

		DescribeTable("should config sriov",
			func(policy *sriovnetworkv1.SriovNetworkNodePolicy) {
				policy.Spec.NicSelector.RootDevices = []string{sriovIface.PciAddress}
				policy.Spec.NicSelector.PfNames = []string{sriovIface.Name}

				By("apply node policy CR")
				err = k8sClient.Create(goctx.TODO(), policy)
				Expect(err).NotTo(HaveOccurred())
				defer func() {
					// Cleanup the test resource
					Expect(k8sClient.Delete(goctx.TODO(), policy)).To(Succeed())
				}()

				By("generate the config for device plugin")
				time.Sleep(30 * time.Second)
				config := &corev1.ConfigMap{}
				err = WaitForNamespacedObject(config, k8sClient, testNamespace, "device-plugin-config", RetryInterval, Timeout)
				Expect(err).NotTo(HaveOccurred())
				err = ValidateDevicePluginConfig([]*sriovnetworkv1.SriovNetworkNodePolicy{policy}, config.Data["config.json"])

				By("wait for the node state ready")
				err = WaitForSriovNetworkNodeStateReady(nodeState, k8sClient, testNamespace, name, RetryInterval, Timeout*15)
				Expect(err).NotTo(HaveOccurred())

				By("provision the cni and device plugin daemonsets")
				cniDaemonSet := &appsv1.DaemonSet{}
				err = WaitForDaemonSetReady(cniDaemonSet, k8sClient, testNamespace, "sriov-network-config-daemon", RetryInterval, Timeout)
				Expect(err).NotTo(HaveOccurred())

				dpDaemonSet := &appsv1.DaemonSet{}
				err = WaitForDaemonSetReady(dpDaemonSet, k8sClient, testNamespace, "sriov-device-plugin", RetryInterval, Timeout)
				Expect(err).NotTo(HaveOccurred())

				By("update the spec of SriovNetworkNodeState CR")
				found := false
				for _, address := range policy.Spec.NicSelector.RootDevices {
					for _, iface := range nodeState.Spec.Interfaces {
						if iface.PciAddress == address {
							found = true
							Expect(iface.NumVfs).To(Equal(policy.Spec.NumVfs))
							Expect(iface.Mtu).To(Equal(policy.Spec.Mtu))
							Expect(iface.VfGroups[0].DeviceType).To(Equal(policy.Spec.DeviceType))
							Expect(iface.VfGroups[0].ResourceName).To(Equal(policy.Spec.ResourceName))
							Expect(iface.VfGroups[0].VfRange).To(Equal("0-" + strconv.Itoa(policy.Spec.NumVfs-1)))
						}
					}
				}
				Expect(found).To(BeTrue())

				By("update the status of SriovNetworkNodeState CR")
				found = false
				for _, address := range policy.Spec.NicSelector.RootDevices {
					for _, iface := range nodeState.Status.Interfaces {
						if iface.PciAddress == address {
							found = true
							Expect(iface.NumVfs).To(Equal(policy.Spec.NumVfs))
							Expect(iface.Mtu).To(Equal(policy.Spec.Mtu))
							Expect(len(iface.VFs)).To(Equal(policy.Spec.NumVfs))
							for _, vf := range iface.VFs {
								if policy.Spec.DeviceType == "netdevice" || policy.Spec.DeviceType == "" {
									Expect(vf.Mtu).To(Equal(policy.Spec.Mtu))
								}
								if policy.Spec.DeviceType == "vfio" {
									Expect(vf.Driver).To(Equal(policy.Spec.DeviceType))
								}
							}
							break
						}
					}
				}
				Expect(found).To(BeTrue())
			},
			Entry("Set MTU and vfio driver", policy1),
			Entry("Set MTU and default driver", policy2),
		)
	})

	Context("with single vf index policy", func() {
		policy1 := &sriovnetworkv1.SriovNetworkNodePolicy{
			TypeMeta: metav1.TypeMeta{
				Kind:       "SriovNetworkNodePolicy",
				APIVersion: "sriovnetwork.openshift.io/v1",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      "policy-1",
				Namespace: testNamespace,
			},
			Spec: sriovnetworkv1.SriovNetworkNodePolicySpec{
				ResourceName: "resource_1",
				NodeSelector: map[string]string{
					"feature.node.kubernetes.io/network-sriov.capable": "true",
				},
				Priority: 99,
				Mtu:      9000,
				NumVfs:   6,
				NicSelector: sriovnetworkv1.SriovNetworkNicSelector{
					PfNames: []string{"#0-5"},
				},
				DeviceType: "vfio-pci",
			},
		}
		policy2 := &sriovnetworkv1.SriovNetworkNodePolicy{
			TypeMeta: metav1.TypeMeta{
				Kind:       "SriovNetworkNodePolicy",
				APIVersion: "sriovnetwork.openshift.io/v1",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      "policy-1",
				Namespace: testNamespace,
			},
			Spec: sriovnetworkv1.SriovNetworkNodePolicySpec{
				ResourceName: "resource_1",
				NodeSelector: map[string]string{
					"feature.node.kubernetes.io/network-sriov.capable": "true",
				},
				Priority: 99,
				Mtu:      9000,
				NumVfs:   6,
				NicSelector: sriovnetworkv1.SriovNetworkNicSelector{
					PfNames: []string{"#0-0"},
				},
			},
		}

		JustBeforeEach(func() {
			By("wait for the node state ready")
			name = sriovInfos.Nodes[0]
			nodeState = &sriovnetworkv1.SriovNetworkNodeState{}
			err = WaitForSriovNetworkNodeStateReady(nodeState, k8sClient, testNamespace, name, RetryInterval, Timeout*15)
			Expect(err).NotTo(HaveOccurred())
		})

		DescribeTable("should config sriov",
			func(policy *sriovnetworkv1.SriovNetworkNodePolicy) {
				policy.Spec.NicSelector.RootDevices = []string{sriovIface.PciAddress}
				policy.Spec.NicSelector.PfNames[0] = sriovIface.Name + policy.Spec.NicSelector.PfNames[0]

				By("apply node policy CR")
				err = k8sClient.Create(goctx.TODO(), policy)
				Expect(err).NotTo(HaveOccurred())
				defer func() {
					// Cleanup the test resource
					Expect(k8sClient.Delete(goctx.TODO(), policy)).To(Succeed())
				}()

				By("generate the config for device plugin")
				time.Sleep(30 * time.Second)
				config := &corev1.ConfigMap{}
				err = WaitForNamespacedObject(config, k8sClient, testNamespace, "device-plugin-config", RetryInterval, Timeout)
				Expect(err).NotTo(HaveOccurred())
				err = ValidateDevicePluginConfig([]*sriovnetworkv1.SriovNetworkNodePolicy{policy}, config.Data["config.json"])

				By("wait for the node state ready")
				err = WaitForSriovNetworkNodeStateReady(nodeState, k8sClient, testNamespace, name, RetryInterval, Timeout*15)
				Expect(err).NotTo(HaveOccurred())

				By("provision the cni and device plugin daemonsets")
				cniDaemonSet := &appsv1.DaemonSet{}
				err = WaitForDaemonSetReady(cniDaemonSet, k8sClient, testNamespace, "sriov-network-config-daemon", RetryInterval, Timeout)
				Expect(err).NotTo(HaveOccurred())

				dpDaemonSet := &appsv1.DaemonSet{}
				err = WaitForDaemonSetReady(dpDaemonSet, k8sClient, testNamespace, "sriov-device-plugin", RetryInterval, Timeout)
				Expect(err).NotTo(HaveOccurred())

				By("update the spec of SriovNetworkNodeState CR")
				found := false
				for _, address := range policy.Spec.NicSelector.RootDevices {
					for _, iface := range nodeState.Spec.Interfaces {
						if iface.PciAddress == address {
							found = true
							Expect(iface.NumVfs).To(Equal(policy.Spec.NumVfs))
							Expect(iface.Mtu).To(Equal(policy.Spec.Mtu))
							Expect(iface.VfGroups[0].DeviceType).To(Equal(policy.Spec.DeviceType))
							Expect(iface.VfGroups[0].ResourceName).To(Equal(policy.Spec.ResourceName))

							pfName, rngStart, rngEnd, err := sriovnetworkv1.ParsePFName(policy.Spec.NicSelector.PfNames[0])
							Expect(err).NotTo(HaveOccurred())
							rng := strconv.Itoa(rngStart) + "-" + strconv.Itoa(rngEnd)
							Expect(iface.Name).To(Equal(pfName))
							Expect(iface.VfGroups[0].VfRange).To(Equal(rng))
						}
					}
				}
				Expect(found).To(BeTrue())

				By("update the status of SriovNetworkNodeState CR")
				found = false
				for _, address := range policy.Spec.NicSelector.RootDevices {
					for _, iface := range nodeState.Status.Interfaces {
						if iface.PciAddress == address {
							found = true
							Expect(iface.NumVfs).To(Equal(policy.Spec.NumVfs))
							Expect(iface.Mtu).To(Equal(policy.Spec.Mtu))
							Expect(len(iface.VFs)).To(Equal(policy.Spec.NumVfs))
							for _, vf := range iface.VFs {
								if policy.Spec.DeviceType == "netdevice" || policy.Spec.DeviceType == "" {
									Expect(vf.Mtu).To(Equal(policy.Spec.Mtu))
								}
								if policy.Spec.DeviceType == "vfio" {
									Expect(vf.Driver).To(Equal(policy.Spec.DeviceType))
								}
							}
							break
						}
					}
				}
				Expect(found).To(BeTrue())
			},
			Entry("Set one PF with VF range", policy1),
			Entry("Set one PF with VF range", policy2),
		)
	})

	Context("with multiple vf index policies", func() {
		policy1 := &sriovnetworkv1.SriovNetworkNodePolicy{
			TypeMeta: metav1.TypeMeta{
				Kind:       "SriovNetworkNodePolicy",
				APIVersion: "sriovnetwork.openshift.io/v1",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      "policy1",
				Namespace: testNamespace,
			},
			Spec: sriovnetworkv1.SriovNetworkNodePolicySpec{
				ResourceName: "resource1",
				NodeSelector: map[string]string{
					"feature.node.kubernetes.io/network-sriov.capable": "true",
				},
				NumVfs:     6,
				DeviceType: "netdevice",
				NicSelector: sriovnetworkv1.SriovNetworkNicSelector{
					PfNames: []string{"#0-1"},
				},
			},
		}
		policy2 := &sriovnetworkv1.SriovNetworkNodePolicy{
			TypeMeta: metav1.TypeMeta{
				Kind:       "SriovNetworkNodePolicy",
				APIVersion: "sriovnetwork.openshift.io/v1",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      "policy2",
				Namespace: testNamespace,
			},
			Spec: sriovnetworkv1.SriovNetworkNodePolicySpec{
				ResourceName: "resource2",
				NodeSelector: map[string]string{
					"feature.node.kubernetes.io/network-sriov.capable": "true",
				},
				NumVfs:     6,
				DeviceType: "netdevice",
				NicSelector: sriovnetworkv1.SriovNetworkNicSelector{
					PfNames: []string{"#2-3"},
				},
			},
		}

		JustBeforeEach(func() {
			By("wait for the node state ready")
			name = sriovInfos.Nodes[0]
			nodeState = &sriovnetworkv1.SriovNetworkNodeState{}
			err = WaitForSriovNetworkNodeStateReady(nodeState, k8sClient, testNamespace, name, RetryInterval, Timeout*15)
			Expect(err).NotTo(HaveOccurred())
		})

		It("should config sriov",
			func() {
				policy1.Spec.NicSelector.RootDevices = []string{sriovIface.PciAddress}
				policy1.Spec.NicSelector.PfNames[0] = sriovIface.Name + policy1.Spec.NicSelector.PfNames[0]
				policy2.Spec.NicSelector.RootDevices = []string{sriovIface.PciAddress}
				policy2.Spec.NicSelector.PfNames[0] = sriovIface.Name + policy2.Spec.NicSelector.PfNames[0]
				policies := []*sriovnetworkv1.SriovNetworkNodePolicy{policy1, policy2}

				By("apply node policy CRs")
				err = k8sClient.Create(goctx.TODO(), policy1)
				Expect(err).NotTo(HaveOccurred())
				defer func() {
					// Cleanup the test resource
					Expect(k8sClient.Delete(goctx.TODO(), policy1)).To(Succeed())
				}()

				err = k8sClient.Create(goctx.TODO(), policy2)
				Expect(err).NotTo(HaveOccurred())
				defer func() {
					// Cleanup the test resource
					Expect(k8sClient.Delete(goctx.TODO(), policy2)).To(Succeed())
				}()

				By("generate the config for device plugin")
				time.Sleep(30 * time.Second)
				config := &corev1.ConfigMap{}
				err = WaitForNamespacedObject(config, k8sClient, testNamespace, "device-plugin-config", RetryInterval, Timeout)
				Expect(err).NotTo(HaveOccurred())
				err = ValidateDevicePluginConfig(policies, config.Data["config.json"])

				By("wait for the node state ready")
				err = WaitForSriovNetworkNodeStateReady(nodeState, k8sClient, testNamespace, name, RetryInterval, Timeout*15)
				Expect(err).NotTo(HaveOccurred())

				By("provision the cni and device plugin daemonsets")
				cniDaemonSet := &appsv1.DaemonSet{}
				err = WaitForDaemonSetReady(cniDaemonSet, k8sClient, testNamespace, "sriov-network-config-daemon", RetryInterval, Timeout)
				Expect(err).NotTo(HaveOccurred())

				dpDaemonSet := &appsv1.DaemonSet{}
				err = WaitForDaemonSetReady(dpDaemonSet, k8sClient, testNamespace, "sriov-device-plugin", RetryInterval, Timeout)
				Expect(err).NotTo(HaveOccurred())

				By("update the spec of SriovNetworkNodeState CR")
				found := false
				for _, address := range policy1.Spec.NicSelector.RootDevices {
					for _, iface := range nodeState.Spec.Interfaces {
						if iface.PciAddress == address {
							found = true
							Expect(iface.NumVfs).To(Equal(policy1.Spec.NumVfs))
							Expect(len(iface.VfGroups)).To(Equal(2))
							vg1 := sriovnetworkv1.VfGroup{
								ResourceName: policy1.Spec.ResourceName,
								DeviceType:   "netdevice",
								VfRange:      "0-1",
								PolicyName:   policy1.GetName(),
							}
							Expect(vg1).To(BeElementOf(iface.VfGroups))
							vg2 := sriovnetworkv1.VfGroup{
								ResourceName: policy2.Spec.ResourceName,
								DeviceType:   "netdevice",
								VfRange:      "2-3",
								PolicyName:   policy2.GetName(),
							}
							Expect(vg2).To(BeElementOf(iface.VfGroups))
						}
					}
				}
				Expect(found).To(BeTrue())

				By("update the status of SriovNetworkNodeState CR")
				found = false
				for _, address := range policy1.Spec.NicSelector.RootDevices {
					for _, iface := range nodeState.Status.Interfaces {
						if iface.PciAddress == address {
							found = true
							Expect(iface.NumVfs).To(Equal(policy1.Spec.NumVfs))
							Expect(len(iface.VFs)).To(Equal(policy1.Spec.NumVfs))
							break
						}
					}
				}
				Expect(found).To(BeTrue())
			})
	})
})
