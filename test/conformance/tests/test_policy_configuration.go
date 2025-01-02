package tests

import (
	"context"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	. "github.com/onsi/gomega/gstruct"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	runtimeclient "sigs.k8s.io/controller-runtime/pkg/client"

	netattdefv1 "github.com/k8snetworkplumbingwg/network-attachment-definition-client/pkg/apis/k8s.cni.cncf.io/v1"

	sriovv1 "github.com/k8snetworkplumbingwg/sriov-network-operator/api/v1"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/test/util/cluster"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/test/util/discovery"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/test/util/namespaces"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/test/util/network"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/test/util/pod"
)

var _ = Describe("[sriov] operator", Ordered, func() {
	Describe("Custom SriovNetworkNodePolicy", func() {
		BeforeEach(func() {
			err := namespaces.Clean(operatorNamespace, namespaces.Test, clients, discovery.Enabled())
			Expect(err).ToNot(HaveOccurred())
			WaitForSRIOVStable()
		})

		Describe("Configuration", func() {
			Context("Create vfio-pci node policy", func() {
				var vfioNode string
				var vfioNic sriovv1.InterfaceExt

				BeforeEach(func() {
					if discovery.Enabled() {
						Skip("Test unsuitable to be run in discovery mode")
					}

					vfioNode, vfioNic = sriovInfos.FindOneVfioSriovDevice()
					if vfioNode == "" {
						Skip("skip test as no vfio-pci capable PF was found")
					}
					By("Using device " + vfioNic.Name + " on node " + vfioNode)
				})

				It("Should be possible to create a vfio-pci resource and allocate to a pod", func() {
					By("creating a vfio-pci node policy")
					resourceName := "testvfio"
					vfioPolicy, err := network.CreateSriovPolicy(clients, "test-policy-", operatorNamespace, vfioNic.Name, vfioNode, 5, resourceName, "vfio-pci")
					Expect(err).ToNot(HaveOccurred())

					By("waiting for the node state to be updated")
					Eventually(func() sriovv1.Interfaces {
						nodeState, err := clients.SriovNetworkNodeStates(operatorNamespace).Get(context.Background(), vfioNode, metav1.GetOptions{})
						Expect(err).ToNot(HaveOccurred())
						return nodeState.Spec.Interfaces
					}, 1*time.Minute, 1*time.Second).Should(ContainElement(MatchFields(
						IgnoreExtras,
						Fields{
							"Name":   Equal(vfioNic.Name),
							"NumVfs": Equal(5),
						})))

					By("waiting the sriov to be stable on the node")
					WaitForSRIOVStable()

					By("waiting for the resources to be available")
					Eventually(func() int64 {
						testedNode, err := clients.CoreV1Interface.Nodes().Get(context.Background(), vfioNode, metav1.GetOptions{})
						Expect(err).ToNot(HaveOccurred())
						resNum := testedNode.Status.Allocatable[corev1.ResourceName("openshift.io/"+resourceName)]
						allocatable, _ := resNum.AsInt64()
						return allocatable
					}, 10*time.Minute, time.Second).Should(Equal(int64(5)))

					By("validate the pf info exist on host")
					output, _, err := runCommandOnConfigDaemon(vfioNode, "/bin/bash", "-c", "ls /host/etc/sriov-operator/pci/ | wc -l")
					Expect(err).ToNot(HaveOccurred())
					Expect(output).ToNot(Equal("1"))

					By("Creating sriov network to use the vfio device")
					sriovNetwork := &sriovv1.SriovNetwork{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "test-vfionetwork",
							Namespace: operatorNamespace,
						},
						Spec: sriovv1.SriovNetworkSpec{
							ResourceName:     resourceName,
							IPAM:             `{"type":"host-local","subnet":"10.10.10.0/24","rangeStart":"10.10.10.171","rangeEnd":"10.10.10.181"}`,
							NetworkNamespace: namespaces.Test,
						}}

					err = clients.Create(context.Background(), sriovNetwork)
					Expect(err).ToNot(HaveOccurred())
					waitForNetAttachDef("test-vfionetwork", namespaces.Test)

					podDefinition := pod.DefineWithNetworks([]string{"test-vfionetwork"})
					firstPod, err := clients.Pods(namespaces.Test).Create(context.Background(), podDefinition, metav1.CreateOptions{})
					Expect(err).ToNot(HaveOccurred())

					firstPod = waitForPodRunning(firstPod)

					By("Checking MTU in pod network status annotation")
					networkStatusJSON, exist := firstPod.Annotations["k8s.v1.cni.cncf.io/network-status"]
					Expect(exist).To(BeTrue())
					Expect(networkStatusJSON).To(ContainSubstring(fmt.Sprintf("\"mtu\": %d", vfioNic.Mtu)))

					By("deleting the policy")
					err = clients.Delete(context.Background(), vfioPolicy, &runtimeclient.DeleteOptions{})
					Expect(err).ToNot(HaveOccurred())
					WaitForSRIOVStable()

					Eventually(func() int64 {
						testedNode, err := clients.CoreV1Interface.Nodes().Get(context.Background(), vfioNode, metav1.GetOptions{})
						Expect(err).ToNot(HaveOccurred())
						resNum := testedNode.Status.Allocatable[corev1.ResourceName("openshift.io/"+resourceName)]
						allocatable, _ := resNum.AsInt64()
						return allocatable
					}, 2*time.Minute, time.Second).Should(Equal(int64(0)))

					By("validate the pf info doesn't exist on the host anymore")
					output, _, err = runCommandOnConfigDaemon(vfioNode, "/bin/bash", "-c", "ls /host/etc/sriov-operator/pci/ | wc -l")
					Expect(err).ToNot(HaveOccurred())
					Expect(output).ToNot(Equal("0"))
				})
			})

			Context("PF Partitioning", func() {
				// 27633
				BeforeEach(func() {
					if discovery.Enabled() {
						Skip("Test unsuitable to be run in discovery mode")
					}
				})

				It("Should be possible to partition the pf's vfs", func() {
					vfioNode, vfioNic := sriovInfos.FindOneVfioSriovDevice()
					if vfioNode == "" {
						Skip("skip test as no vfio-pci capable PF was found")
					}
					By("Using device " + vfioNic.Name + " on node " + vfioNode)

					_, err := network.CreateSriovPolicy(clients, "test-policy-", operatorNamespace, vfioNic.Name+"#2-4", vfioNode, 5, testResourceName, "netdevice")
					Expect(err).ToNot(HaveOccurred())

					Eventually(func() sriovv1.Interfaces {
						nodeState, err := clients.SriovNetworkNodeStates(operatorNamespace).Get(context.Background(), vfioNode, metav1.GetOptions{})
						Expect(err).ToNot(HaveOccurred())
						return nodeState.Spec.Interfaces
					}, 3*time.Minute, 1*time.Second).Should(ContainElement(MatchFields(
						IgnoreExtras,
						Fields{
							"Name":   Equal(vfioNic.Name),
							"NumVfs": Equal(5),
							"VfGroups": ContainElement(
								MatchFields(
									IgnoreExtras,
									Fields{
										"ResourceName": Equal(testResourceName),
										"DeviceType":   Equal("netdevice"),
										"VfRange":      Equal("2-4"),
									})),
						})))

					WaitForSRIOVStable()

					Eventually(func() int64 {
						testedNode, err := clients.CoreV1Interface.Nodes().Get(context.Background(), vfioNode, metav1.GetOptions{})
						Expect(err).ToNot(HaveOccurred())
						resNum := testedNode.Status.Allocatable["openshift.io/testresource"]
						capacity, _ := resNum.AsInt64()
						return capacity
					}, 3*time.Minute, time.Second).Should(Equal(int64(3)))

					_, err = network.CreateSriovPolicy(clients, "test-policy-", operatorNamespace, vfioNic.Name+"#0-1", vfioNode, 5, "testresource1", "vfio-pci")
					Expect(err).ToNot(HaveOccurred())

					Eventually(func() sriovv1.Interfaces {
						nodeState, err := clients.SriovNetworkNodeStates(operatorNamespace).Get(context.Background(), vfioNode, metav1.GetOptions{})
						Expect(err).ToNot(HaveOccurred())
						return nodeState.Spec.Interfaces
					}, 3*time.Minute, 1*time.Second).Should(ContainElement(MatchFields(
						IgnoreExtras,
						Fields{
							"Name":   Equal(vfioNic.Name),
							"NumVfs": Equal(5),
							"VfGroups": SatisfyAll(
								ContainElement(
									MatchFields(
										IgnoreExtras,
										Fields{
											"ResourceName": Equal(testResourceName),
											"DeviceType":   Equal("netdevice"),
											"VfRange":      Equal("2-4"),
										})),
								ContainElement(
									MatchFields(
										IgnoreExtras,
										Fields{
											"ResourceName": Equal("testresource1"),
											"DeviceType":   Equal("vfio-pci"),
											"VfRange":      Equal("0-1"),
										})),
							),
						},
					)))

					// The node may reset here so we put a larger timeout here
					Eventually(func() bool {
						res, err := cluster.SriovStable(operatorNamespace, clients)
						Expect(err).ToNot(HaveOccurred())
						return res
					}, 15*time.Minute, 5*time.Second).Should(BeTrue())

					Eventually(func() map[string]int64 {
						testedNode, err := clients.CoreV1Interface.Nodes().Get(context.Background(), vfioNode, metav1.GetOptions{})
						Expect(err).ToNot(HaveOccurred())
						resNum := testedNode.Status.Allocatable["openshift.io/testresource"]
						capacity, _ := resNum.AsInt64()
						res := make(map[string]int64)
						res["openshift.io/testresource"] = capacity
						resNum = testedNode.Status.Allocatable["openshift.io/testresource1"]
						capacity, _ = resNum.AsInt64()
						res["openshift.io/testresource1"] = capacity
						return res
					}, 15*time.Minute, time.Second).Should(Equal(map[string]int64{
						"openshift.io/testresource":  int64(3),
						"openshift.io/testresource1": int64(2),
					}))
				})

				It("Should configure the mtu only for vfs which are part of the partition", func() {
					defaultMtu := 1500
					newMtu := 2000

					node := sriovInfos.Nodes[0]
					intf, err := sriovInfos.FindOneSriovDevice(node)
					Expect(err).ToNot(HaveOccurred())
					By("Using device " + intf.Name + " on node " + node)

					_, err = network.CreateSriovPolicy(clients, "test-policy-", operatorNamespace, intf.Name+"#0-1", node, 5, testResourceName, "netdevice", func(policy *sriovv1.SriovNetworkNodePolicy) {
						policy.Spec.Mtu = newMtu
					})
					Expect(err).ToNot(HaveOccurred())

					Eventually(func() sriovv1.Interfaces {
						nodeState, err := clients.SriovNetworkNodeStates(operatorNamespace).Get(context.Background(), node, metav1.GetOptions{})
						Expect(err).ToNot(HaveOccurred())
						return nodeState.Spec.Interfaces
					}, 3*time.Minute, 1*time.Second).Should(ContainElement(MatchFields(
						IgnoreExtras,
						Fields{
							"Name":   Equal(intf.Name),
							"NumVfs": Equal(5),
							"Mtu":    Equal(newMtu),
							"VfGroups": ContainElement(
								MatchFields(
									IgnoreExtras,
									Fields{
										"ResourceName": Equal(testResourceName),
										"DeviceType":   Equal("netdevice"),
										"VfRange":      Equal("0-1"),
									})),
						})))

					WaitForSRIOVStable()

					Eventually(func() int64 {
						testedNode, err := clients.CoreV1Interface.Nodes().Get(context.Background(), node, metav1.GetOptions{})
						Expect(err).ToNot(HaveOccurred())
						resNum := testedNode.Status.Allocatable["openshift.io/testresource"]
						capacity, _ := resNum.AsInt64()
						return capacity
					}, 3*time.Minute, time.Second).Should(Equal(int64(2)))

					By(fmt.Sprintf("verifying that only VF 0 and 1 have mtu set to %d", newMtu))
					Eventually(func() sriovv1.InterfaceExts {
						nodeState, err := clients.SriovNetworkNodeStates(operatorNamespace).Get(context.Background(), node, metav1.GetOptions{})
						Expect(err).ToNot(HaveOccurred())
						return nodeState.Status.Interfaces
					}, 3*time.Minute, 1*time.Second).Should(ContainElement(MatchFields(
						IgnoreExtras,
						Fields{
							"VFs": SatisfyAll(
								ContainElement(
									MatchFields(
										IgnoreExtras,
										Fields{
											"VfID": Equal(0),
											"Mtu":  Equal(newMtu),
										})),
								ContainElement(
									MatchFields(
										IgnoreExtras,
										Fields{
											"VfID": Equal(1),
											"Mtu":  Equal(newMtu),
										})),
								ContainElement(
									MatchFields(
										IgnoreExtras,
										Fields{
											"VfID": Equal(2),
											"Mtu":  Equal(defaultMtu),
										})),
							),
						})))
				})

				// 27630
				It("Should not be possible to have overlapping pf ranges", func() {
					node := sriovInfos.Nodes[0]
					intf, err := sriovInfos.FindOneSriovDevice(node)
					Expect(err).ToNot(HaveOccurred())
					By("Using device " + intf.Name + " on node " + node)

					firstConfig := &sriovv1.SriovNetworkNodePolicy{
						ObjectMeta: metav1.ObjectMeta{
							GenerateName: "test-policy",
							Namespace:    operatorNamespace,
						},

						Spec: sriovv1.SriovNetworkNodePolicySpec{
							NodeSelector: map[string]string{
								"kubernetes.io/hostname": node,
							},
							NumVfs:       5,
							ResourceName: testResourceName,
							Priority:     99,
							NicSelector: sriovv1.SriovNetworkNicSelector{
								PfNames: []string{intf.Name + "#1-4"},
							},
							DeviceType: "netdevice",
						},
					}

					err = clients.Create(context.Background(), firstConfig)
					Expect(err).ToNot(HaveOccurred())

					Eventually(func() sriovv1.Interfaces {
						nodeState, err := clients.SriovNetworkNodeStates(operatorNamespace).Get(context.Background(), node, metav1.GetOptions{})
						Expect(err).ToNot(HaveOccurred())
						return nodeState.Spec.Interfaces
					}, 3*time.Minute, 1*time.Second).Should(ContainElement(MatchFields(
						IgnoreExtras,
						Fields{
							"Name":     Equal(intf.Name),
							"NumVfs":   Equal(5),
							"VfGroups": ContainElement(sriovv1.VfGroup{ResourceName: testResourceName, DeviceType: "netdevice", VfRange: "1-4", PolicyName: firstConfig.Name}),
						})))

					secondConfig := &sriovv1.SriovNetworkNodePolicy{
						ObjectMeta: metav1.ObjectMeta{
							GenerateName: "test-policy",
							Namespace:    operatorNamespace,
						},

						Spec: sriovv1.SriovNetworkNodePolicySpec{
							NodeSelector: map[string]string{
								"kubernetes.io/hostname": node,
							},
							NumVfs:       5,
							ResourceName: "testresource1",
							Priority:     99,
							NicSelector: sriovv1.SriovNetworkNicSelector{
								PfNames: []string{intf.Name + "#0-2"},
							},
							DeviceType: "vfio-pci",
						},
					}

					err = clients.Create(context.Background(), secondConfig)
					Expect(err).To(HaveOccurred())
				})

				// https://issues.redhat.com/browse/OCPBUGS-34934
				It("Should be possible to delete a vfio-pci policy", func() {
					vfioNode, vfioNic := sriovInfos.FindOneVfioSriovDevice()
					if vfioNode == "" {
						Skip("skip test as no vfio-pci capable PF was found")
					}
					By("Using device " + vfioNic.Name + " on node " + vfioNode)

					By("Creating a vfio-pci policy")
					vfiopolicy, err := network.CreateSriovPolicy(clients, "test-policy-", operatorNamespace, vfioNic.Name+"#0-1", vfioNode, 5, "resvfiopci", "vfio-pci")
					Expect(err).ToNot(HaveOccurred())

					By("Creating a netdevice policy")
					_, err = network.CreateSriovPolicy(clients, "test-policy-", operatorNamespace, vfioNic.Name+"#2-4", vfioNode, 5, "resnetdevice", "netdevice")
					Expect(err).ToNot(HaveOccurred())

					By("Checking the SriovNetworkNodeState is correctly configured")
					assertNodeStateHasVFMatching(vfioNode,
						Fields{"VfID": Equal(0), "Driver": Equal("vfio-pci")})
					assertNodeStateHasVFMatching(vfioNode,
						Fields{"VfID": Equal(1), "Driver": Equal("vfio-pci")})
					assertNodeStateHasVFMatching(vfioNode,
						Fields{"VfID": Equal(2), "Name": Not(BeEmpty())})

					By("Deleting the vfio-pci policy")
					err = clients.Delete(context.Background(), vfiopolicy)
					Expect(err).ToNot(HaveOccurred())

					By("Checking the SriovNetworkNodeState is consistently stable")
					Eventually(cluster.SriovStable).
						WithTimeout(10*time.Second).
						WithPolling(1*time.Second).
						WithArguments(operatorNamespace, clients).
						Should(BeTrue())

					Eventually(cluster.SriovStable).
						WithTimeout(5*time.Second).
						WithPolling(1*time.Second).
						WithArguments(operatorNamespace, clients).
						Should(BeTrue())
				})
			})

			Context("Main PF", func() {
				It("should work when vfs are used by pods", func() {
					if !discovery.Enabled() {
						testNode := sriovInfos.Nodes[0]
						resourceName := "mainpfresource"
						sriovDeviceList, err := sriovInfos.FindSriovDevices(testNode)
						Expect(err).ToNot(HaveOccurred())
						executorPod := createCustomTestPod(testNode, []string{}, true, nil)
						mainDeviceForNode := findMainSriovDevice(executorPod, sriovDeviceList)
						if mainDeviceForNode == nil {
							Skip("Could not find pf used as gateway")
						}
						By("Using device " + mainDeviceForNode.Name + " on node " + testNode)

						createSriovPolicy(mainDeviceForNode.Name, testNode, 2, resourceName)
					}

					mainDevice, resourceName, nodeToTest, ok := discoverResourceForMainSriov(sriovInfos)
					if !ok {
						Skip("Could not find a policy configured to use the main pf")
					}
					ipam := ipamIpv4
					err := network.CreateSriovNetwork(clients, mainDevice, sriovNetworkName, namespaces.Test, operatorNamespace, resourceName, ipam)
					Expect(err).ToNot(HaveOccurred())
					waitForNetAttachDef(sriovNetworkName, namespaces.Test)
					createTestPod(nodeToTest, []string{sriovNetworkName})
				})
			})
			Context("PF shutdown", func() {
				// 29398
				It("Should be able to create pods successfully if PF is down.Pods are able to communicate with each other on the same node", func() {
					if cluster.VirtualCluster() {
						// https://bugzilla.redhat.com/show_bug.cgi?id=2214976
						Skip("Bug in IGB driver")
					}

					resourceName := testResourceName
					var testNode string
					var unusedSriovDevice *sriovv1.InterfaceExt

					if discovery.Enabled() {
						Skip("PF Shutdown test not enabled in discovery mode")
					}

					testNode = sriovInfos.Nodes[0]
					sriovDeviceList, err := sriovInfos.FindSriovDevices(testNode)
					Expect(err).ToNot(HaveOccurred())
					unusedSriovDevices, err := findUnusedSriovDevices(testNode, sriovDeviceList)
					if err != nil {
						Skip(err.Error())
					}
					unusedSriovDevice = unusedSriovDevices[0]

					By("Using device " + unusedSriovDevice.Name + " on node " + testNode)

					defer changeNodeInterfaceState(testNode, unusedSriovDevices[0].Name, true)
					Expect(err).ToNot(HaveOccurred())
					createSriovPolicy(unusedSriovDevice.Name, testNode, 2, resourceName)

					ipam := `{
						"type":"host-local",
						"subnet":"10.10.10.0/24",
						"rangeStart":"10.10.10.171",
						"rangeEnd":"10.10.10.181"
						}`

					err = network.CreateSriovNetwork(clients, unusedSriovDevice, sriovNetworkName, namespaces.Test, operatorNamespace, resourceName, ipam)
					Expect(err).ToNot(HaveOccurred())
					waitForNetAttachDef(sriovNetworkName, namespaces.Test)
					changeNodeInterfaceState(testNode, unusedSriovDevice.Name, false)
					pod := createTestPod(testNode, []string{sriovNetworkName})
					ips, err := network.GetSriovNicIPs(pod, "net1")
					Expect(err).ToNot(HaveOccurred())
					Expect(ips).NotTo(BeNil(), "No sriov network interface found.")
					Expect(len(ips)).Should(Equal(1))
					for _, ip := range ips {
						pingPod(ip, testNode, sriovNetworkName)
					}
				})
			})

			Context("MTU", func() {
				BeforeEach(func() {

					var node string
					resourceName := "mturesource"
					var numVfs int
					var intf *sriovv1.InterfaceExt
					var err error

					if discovery.Enabled() {
						node, resourceName, numVfs, intf, err = discovery.DiscoveredResources(clients,
							sriovInfos, operatorNamespace,
							func(policy sriovv1.SriovNetworkNodePolicy) bool {
								if !defaultFilterPolicy(policy) {
									return false
								}
								if policy.Spec.Mtu != 9000 {
									return false
								}
								return true
							},
							func(node string, sriovDeviceList []*sriovv1.InterfaceExt) (*sriovv1.InterfaceExt, bool) {
								if len(sriovDeviceList) == 0 {
									return nil, false
								}
								nodeState, err := clients.SriovNetworkNodeStates(operatorNamespace).Get(context.Background(), node, metav1.GetOptions{})
								Expect(err).ToNot(HaveOccurred())

								for _, ifc := range nodeState.Spec.Interfaces {
									if ifc.Mtu == 9000 && ifc.NumVfs > 0 {
										for _, device := range sriovDeviceList {
											if device.Name == ifc.Name {
												return device, true
											}
										}
									}
								}
								return nil, false
							},
						)
						Expect(err).ToNot(HaveOccurred())
						if node == "" || resourceName == "" || numVfs < 5 || intf == nil {
							Skip("Insufficient resources to run test in discovery mode")
						}
					} else {
						node = sriovInfos.Nodes[0]
						sriovDeviceList, err := sriovInfos.FindSriovDevices(node)
						Expect(err).ToNot(HaveOccurred())
						unusedSriovDevices, err := findUnusedSriovDevices(node, sriovDeviceList)
						if err != nil {
							Skip(err.Error())
						}
						intf = unusedSriovDevices[0]
						By("Using device " + intf.Name + " on node " + node)

						mtuPolicy := &sriovv1.SriovNetworkNodePolicy{
							ObjectMeta: metav1.ObjectMeta{
								GenerateName: "test-mtupolicy",
								Namespace:    operatorNamespace,
							},

							Spec: sriovv1.SriovNetworkNodePolicySpec{
								NodeSelector: map[string]string{
									"kubernetes.io/hostname": node,
								},
								Mtu:          9000,
								NumVfs:       5,
								ResourceName: resourceName,
								Priority:     99,
								NicSelector: sriovv1.SriovNetworkNicSelector{
									PfNames: []string{intf.Name},
								},
								DeviceType: "netdevice",
							},
						}

						err = clients.Create(context.Background(), mtuPolicy)
						Expect(err).ToNot(HaveOccurred())

						WaitForSRIOVStable()
						By("waiting for the resources to be available")
						Eventually(func() int64 {
							testedNode, err := clients.CoreV1Interface.Nodes().Get(context.Background(), node, metav1.GetOptions{})
							Expect(err).ToNot(HaveOccurred())
							resNum := testedNode.Status.Allocatable[corev1.ResourceName("openshift.io/"+resourceName)]
							allocatable, _ := resNum.AsInt64()
							return allocatable
						}, 10*time.Minute, time.Second).Should(Equal(int64(5)))
					}

					sriovNetwork := &sriovv1.SriovNetwork{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "test-mtuvolnetwork",
							Namespace: operatorNamespace,
						},
						Spec: sriovv1.SriovNetworkSpec{
							ResourceName:     resourceName,
							IPAM:             `{"type":"host-local","subnet":"10.10.10.0/24","rangeStart":"10.10.10.171","rangeEnd":"10.10.10.181"}`,
							NetworkNamespace: namespaces.Test,
						}}

					if !cluster.VirtualCluster() {
						sriovNetwork.Spec.LinkState = "enable"
					}

					// We need this to be able to run the connectivity checks on Mellanox cards
					if intf.DeviceID == "1015" {
						sriovNetwork.Spec.SpoofChk = off
					}

					err = clients.Create(context.Background(), sriovNetwork)

					Expect(err).ToNot(HaveOccurred())

					Eventually(func() error {
						netAttDef := &netattdefv1.NetworkAttachmentDefinition{}
						return clients.Get(context.Background(), runtimeclient.ObjectKey{Name: "test-mtuvolnetwork", Namespace: namespaces.Test}, netAttDef)
					}, (10+snoTimeoutMultiplier*110)*time.Second, 1*time.Second).ShouldNot(HaveOccurred())

				})

				// 27662
				It("Should support jumbo frames", func() {
					podDefinition := pod.DefineWithNetworks([]string{"test-mtuvolnetwork"})
					firstPod, err := clients.Pods(namespaces.Test).Create(context.Background(), podDefinition, metav1.CreateOptions{})
					Expect(err).ToNot(HaveOccurred())

					firstPod = waitForPodRunning(firstPod)

					By("Checking MTU in pod network status annotation")
					networkStatusJSON, exist := firstPod.Annotations["k8s.v1.cni.cncf.io/network-status"]
					Expect(exist).To(BeTrue())
					Expect(networkStatusJSON).To(ContainSubstring("\"mtu\": 9000"))

					var stdout, stderr string
					Eventually(func() error {
						stdout, stderr, err = pod.ExecCommand(clients, firstPod, "ip", "link", "show", "net1")
						if stdout == "" {
							return fmt.Errorf("empty response from pod exec")
						}

						if err != nil {
							return fmt.Errorf("failed to show net1")
						}

						return nil
					}, 1*time.Minute, 5*time.Second).ShouldNot(HaveOccurred())
					Expect(stdout).To(ContainSubstring("mtu 9000"))
					firstPodIPs, err := network.GetSriovNicIPs(firstPod, "net1")
					Expect(err).ToNot(HaveOccurred())
					Expect(len(firstPodIPs)).To(Equal(1))

					podDefinition = pod.DefineWithNetworks([]string{"test-mtuvolnetwork"})
					secondPod, err := clients.Pods(namespaces.Test).Create(context.Background(), podDefinition, metav1.CreateOptions{})
					Expect(err).ToNot(HaveOccurred())

					secondPod = waitForPodRunning(secondPod)

					stdout, stderr, err = pod.ExecCommand(clients, secondPod,
						"ping", firstPodIPs[0], "-s", "8972", "-M", "do", "-c", "2")
					Expect(err).ToNot(HaveOccurred(), "Failed to ping first pod", stderr)
					Expect(stdout).To(ContainSubstring("2 packets transmitted, 2 received, 0% packet loss"))
				})
			})

			Context("ExcludeTopology", func() {

				var excludeTopologyTrueResourceXXX, excludeTopologyFalseResourceXXX, excludeTopologyFalseResourceYYY *sriovv1.SriovNetworkNodePolicy
				var node string
				var intf *sriovv1.InterfaceExt

				BeforeEach(func() {
					if discovery.Enabled() {
						Skip("Test unsuitable to be run in discovery mode")
					}

					node = sriovInfos.Nodes[0]
					sriovDeviceList, err := sriovInfos.FindSriovDevices(node)
					Expect(err).ToNot(HaveOccurred())
					unusedSriovDevices, err := findUnusedSriovDevices(node, sriovDeviceList)
					Expect(err).ToNot(HaveOccurred())

					intf = unusedSriovDevices[0]
					By("Using device " + intf.Name + " on node " + node)

					excludeTopologyTrueResourceXXX = &sriovv1.SriovNetworkNodePolicy{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "test-exclude-topology-true-res-xxx",
							Namespace: operatorNamespace,
						},

						Spec: sriovv1.SriovNetworkNodePolicySpec{
							NumVfs:       7,
							ResourceName: "resourceXXX",
							NodeSelector: map[string]string{"kubernetes.io/hostname": node},
							NicSelector: sriovv1.SriovNetworkNicSelector{
								PfNames: []string{intf.Name + "#0-3"},
							},
							ExcludeTopology: true,
						},
					}

					excludeTopologyFalseResourceXXX = &sriovv1.SriovNetworkNodePolicy{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "test-exclude-topology-false-res-xxx",
							Namespace: operatorNamespace,
						},

						Spec: sriovv1.SriovNetworkNodePolicySpec{
							NumVfs:       7,
							ResourceName: "resourceXXX",
							NodeSelector: map[string]string{"kubernetes.io/hostname": node},
							NicSelector: sriovv1.SriovNetworkNicSelector{
								PfNames: []string{intf.Name + "#4-6"},
							},
							ExcludeTopology: false,
						},
					}

					excludeTopologyFalseResourceYYY = &sriovv1.SriovNetworkNodePolicy{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "test-exclude-topology-false-res-yyy",
							Namespace: operatorNamespace,
						},

						Spec: sriovv1.SriovNetworkNodePolicySpec{
							NumVfs:       7,
							ResourceName: "resourceYYY",
							NodeSelector: map[string]string{"kubernetes.io/hostname": node},
							NicSelector: sriovv1.SriovNetworkNicSelector{
								PfNames: []string{intf.Name + "#4-6"},
							},
							ExcludeTopology: false,
						},
					}

				})

				It("field is forwarded to the device plugin configuration", func() {

					err := clients.Create(context.Background(), excludeTopologyTrueResourceXXX)
					Expect(err).ToNot(HaveOccurred())

					assertDevicePluginConfigurationContains(node,
						fmt.Sprintf(`{"resourceName":"resourceXXX","excludeTopology":true,"selectors":{"pfNames":["%s#0-3"],"IsRdma":false,"NeedVhostNet":false},"SelectorObj":null}`, intf.Name))

					err = clients.Create(context.Background(), excludeTopologyFalseResourceYYY)
					Expect(err).ToNot(HaveOccurred())

					assertDevicePluginConfigurationContains(node,
						fmt.Sprintf(`{"resourceName":"resourceXXX","excludeTopology":true,"selectors":{"pfNames":["%s#0-3"],"IsRdma":false,"NeedVhostNet":false},"SelectorObj":null}`, intf.Name))
					assertDevicePluginConfigurationContains(node,
						fmt.Sprintf(`{"resourceName":"resourceYYY","selectors":{"pfNames":["%s#4-6"],"IsRdma":false,"NeedVhostNet":false},"SelectorObj":null}`, intf.Name))
				})

				It("multiple values for the same resource should not be allowed", func() {

					err := clients.Create(context.Background(), excludeTopologyTrueResourceXXX)
					Expect(err).ToNot(HaveOccurred())

					err = clients.Create(context.Background(), excludeTopologyFalseResourceXXX)
					Expect(err).To(HaveOccurred())

					Expect(err.Error()).To(ContainSubstring(
						"excludeTopology[false] field conflicts with policy [test-exclude-topology-true-res-xxx].ExcludeTopology[true]" +
							" as they target the same resource[resourceXXX]"))
				})
			})
		})

	})

})
