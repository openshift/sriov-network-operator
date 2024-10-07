package tests

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	. "github.com/onsi/gomega/gstruct"

	netattdefv1 "github.com/k8snetworkplumbingwg/network-attachment-definition-client/pkg/apis/k8s.cni.cncf.io/v1"
	admission "k8s.io/api/admissionregistration/v1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/pointer"
	runtimeclient "sigs.k8s.io/controller-runtime/pkg/client"

	sriovv1 "github.com/k8snetworkplumbingwg/sriov-network-operator/api/v1"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/consts"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/test/util/clean"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/test/util/cluster"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/test/util/discovery"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/test/util/execute"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/test/util/namespaces"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/test/util/network"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/test/util/nodes"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/test/util/pod"
)

var waitingTime = 20 * time.Minute
var sriovNetworkName = "test-sriovnetwork"
var snoTimeoutMultiplier time.Duration = 0

const (
	operatorNetworkInjectorFlag = "network-resources-injector"
	operatorWebhookFlag         = "operator-webhook"
	on                          = "on"
	off                         = "off"
	testResourceName            = "testresource"
	testIpv6NetworkName         = "test-ipv6network"
	volumePodNetInfo            = "podnetinfo"
	ipamIpv6                    = `{"type": "host-local","ranges": [[{"subnet": "3ffe:ffff:0:01ff::/64"}]],"dataDir": "/run/my-orchestrator/container-ipam-state"}`
	ipamIpv4                    = `{"type": "host-local","ranges": [[{"subnet": "1.1.1.0/24"}]],"dataDir": "/run/my-orchestrator/container-ipam-state"}`
)

type patchBody struct {
	Op    string `json:"op"`
	Path  string `json:"path"`
	Value string `json:"value"`
}

func init() {
	waitingEnv := os.Getenv("SRIOV_WAITING_TIME")
	newTime, err := strconv.Atoi(waitingEnv)
	if err == nil && newTime != 0 {
		waitingTime = time.Duration(newTime) * time.Minute
	}
}

var _ = Describe("[sriov] operator", func() {
	var sriovInfos *cluster.EnabledNodes
	var initPassed bool
	execute.BeforeAll(func() {
		isSingleNode, err := cluster.IsSingleNode(clients)
		Expect(err).ToNot(HaveOccurred())
		if isSingleNode {
			disableDrainState, err := cluster.GetNodeDrainState(clients, operatorNamespace)
			Expect(err).ToNot(HaveOccurred())
			if discovery.Enabled() {
				if !disableDrainState {
					Skip("SriovOperatorConfig DisableDrain property must be enabled in a single node environment")
				}
			}
			snoTimeoutMultiplier = 1
			if !disableDrainState {
				err = cluster.SetDisableNodeDrainState(clients, operatorNamespace, true)
				Expect(err).ToNot(HaveOccurred())
				clean.RestoreNodeDrainState = true
			}
		}

		Expect(clients).NotTo(BeNil(), "Client misconfigured, check the $KUBECONFIG env variable")
		err = namespaces.Create(namespaces.Test, clients)
		Expect(err).ToNot(HaveOccurred())
		err = namespaces.Clean(operatorNamespace, namespaces.Test, clients, discovery.Enabled())
		Expect(err).ToNot(HaveOccurred())
		WaitForSRIOVStable()
		sriovInfos, err = cluster.DiscoverSriov(clients, operatorNamespace)
		Expect(err).ToNot(HaveOccurred())
		initPassed = true
	})

	BeforeEach(func() {
		Expect(initPassed).To(BeTrue(), "Global setup failed")
	})

	Describe("No SriovNetworkNodePolicy", func() {
		Context("SR-IOV network config daemon can be set by nodeselector", func() {
			// 26186
			It("Should schedule the config daemon on selected nodes", func() {
				if discovery.Enabled() {
					Skip("Test unsuitable to be run in discovery mode")
				}

				testStartingTime := time.Now()

				By("Checking that a daemon is scheduled on each worker node")
				Eventually(func() bool {
					return daemonsScheduledOnNodes("node-role.kubernetes.io/worker=")
				}, 3*time.Minute, 1*time.Second).Should(Equal(true))

				By("Labeling one worker node with the label needed for the daemon")
				allNodes, err := clients.CoreV1Interface.Nodes().List(context.Background(), metav1.ListOptions{
					LabelSelector: "node-role.kubernetes.io/worker",
				})
				Expect(err).ToNot(HaveOccurred())

				selectedNodes, err := nodes.MatchingOptionalSelector(clients, allNodes.Items)
				Expect(err).ToNot(HaveOccurred())

				Expect(len(selectedNodes)).To(BeNumerically(">", 0), "There must be at least one worker")
				candidate := selectedNodes[0]
				candidate.Labels["sriovenabled"] = "true"
				_, err = clients.CoreV1Interface.Nodes().Update(context.Background(), &candidate, metav1.UpdateOptions{})
				Expect(err).ToNot(HaveOccurred())

				By("Setting the node selector for each daemon")
				cfg := sriovv1.SriovOperatorConfig{}
				err = clients.Get(context.TODO(), runtimeclient.ObjectKey{
					Name:      "default",
					Namespace: operatorNamespace,
				}, &cfg)
				Expect(err).ToNot(HaveOccurred())
				cfg.Spec.ConfigDaemonNodeSelector = map[string]string{
					"sriovenabled": "true",
				}
				Eventually(func() error {
					return clients.Update(context.TODO(), &cfg)
				}, 1*time.Minute, 5*time.Second).ShouldNot(HaveOccurred())

				By("Checking that a daemon is scheduled only on selected node")
				Eventually(func() bool {
					return !daemonsScheduledOnNodes("sriovenabled!=true") &&
						daemonsScheduledOnNodes("sriovenabled=true")
				}, 1*time.Minute, 1*time.Second).Should(Equal(true))

				By("Restoring the node selector for daemons")
				err = clients.Get(context.TODO(), runtimeclient.ObjectKey{
					Name:      "default",
					Namespace: operatorNamespace,
				}, &cfg)
				Expect(err).ToNot(HaveOccurred())
				cfg.Spec.ConfigDaemonNodeSelector = map[string]string{}
				Eventually(func() error {
					return clients.Update(context.TODO(), &cfg)
				}, 1*time.Minute, 5*time.Second).ShouldNot(HaveOccurred())

				By("Checking that a daemon is scheduled on each worker node")
				Eventually(func() bool {
					return daemonsScheduledOnNodes("node-role.kubernetes.io/worker")
				}, 1*time.Minute, 1*time.Second).Should(Equal(true))

				By("Checking that a daemon is able to publish events")
				Eventually(func() bool {
					events, err := clients.Events(operatorNamespace).List(
						context.Background(), metav1.ListOptions{TypeMeta: sriovv1.SriovNetworkNodeState{}.TypeMeta})
					Expect(err).ToNot(HaveOccurred())

					for _, e := range events.Items {
						if e.Reason == "ConfigDaemonStart" &&
							e.CreationTimestamp.Time.After(testStartingTime) {
							return true
						}
					}
					return false
				}, 30*time.Second, 5*time.Second).Should(BeTrue(), "Config Daemon should record an event when starting")
			})
		})

		Context("LogLevel affects operator's logs", func() {
			It("when set to 0 no lifecycle logs are present", func() {
				if discovery.Enabled() {
					Skip("Test unsuitable to be run in discovery mode")
				}

				initialLogLevelValue := getOperatorConfigLogLevel()
				DeferCleanup(func() {
					By("Restore LogLevel to its initial value")
					setOperatorConfigLogLevel(initialLogLevelValue)
				})

				initialDisableDrain, err := cluster.GetNodeDrainState(clients, operatorNamespace)
				Expect(err).ToNot(HaveOccurred())

				DeferCleanup(func() {
					By("Restore DisableDrain to its initial value")
					Eventually(func() error {
						return cluster.SetDisableNodeDrainState(clients, operatorNamespace, initialDisableDrain)
					}, 1*time.Minute, 5*time.Second).ShouldNot(HaveOccurred())
				})

				By("Set operator LogLevel to 2")
				setOperatorConfigLogLevel(2)

				By("Flip DisableDrain to trigger operator activity")
				since := time.Now().Add(-10 * time.Second)
				Eventually(func() error {
					return cluster.SetDisableNodeDrainState(clients, operatorNamespace, !initialDisableDrain)
				}, 1*time.Minute, 5*time.Second).ShouldNot(HaveOccurred())

				By("Assert logs contains verbose output")
				Eventually(func(g Gomega) {
					logs := getOperatorLogs(since)
					g.Expect(logs).To(
						ContainElement(And(
							ContainSubstring("Reconciling SriovOperatorConfig"),
						)),
					)

					// Should contain verbose logging
					g.Expect(logs).To(
						ContainElement(
							ContainSubstring("Start to sync webhook objects"),
						),
					)
				}, 1*time.Minute, 5*time.Second).Should(Succeed())

				By("Reduce operator LogLevel to 0")
				setOperatorConfigLogLevel(0)

				By("Flip DisableDrain again to trigger operator activity")
				since = time.Now().Add(-10 * time.Second)
				Eventually(func() error {
					return cluster.SetDisableNodeDrainState(clients, operatorNamespace, initialDisableDrain)
				}, 1*time.Minute, 5*time.Second).ShouldNot(HaveOccurred())

				By("Assert logs contains less operator activity")
				Eventually(func(g Gomega) {
					logs := getOperatorLogs(since)

					// time only contains sec, but we can have race here that in the same sec there was a sync
					afterLogs := []string{}
					found := false
					for _, log := range logs {
						if found {
							afterLogs = append(afterLogs, log)
						}
						if strings.Contains(log, "{\"new-level\": 0, \"current-level\": 2}") {
							found = true
						}
					}
					g.Expect(found).To(BeTrue())
					g.Expect(afterLogs).To(
						ContainElement(And(
							ContainSubstring("Reconciling SriovOperatorConfig"),
						)),
					)

					// Should not contain verbose logging
					g.Expect(afterLogs).ToNot(
						ContainElement(
							ContainSubstring("Start to sync webhook objects"),
						),
					)
				}, 3*time.Minute, 5*time.Second).Should(Succeed())
			})
		})

		DescribeTable("should gracefully restart quickly", func(webookEnabled bool) {
			DeferCleanup(setSriovOperatorSpecFlag(operatorNetworkInjectorFlag, webookEnabled))
			DeferCleanup(setSriovOperatorSpecFlag(operatorWebhookFlag, webookEnabled))

			// This test case ensure leader election process runs smoothly when the operator's pod is restarted.
			oldLease, err := clients.CoordinationV1Interface.Leases(operatorNamespace).Get(context.Background(), consts.LeaderElectionID, metav1.GetOptions{})
			if k8serrors.IsNotFound(err) {
				Skip("Leader Election is not enabled on the cluster. Skipping")
			}
			Expect(err).ToNot(HaveOccurred())

			oldOperatorPod := getOperatorPod()

			By("Delete the operator's pod")
			deleteOperatorPod()

			By("Wait the new operator's pod to start")
			Eventually(func(g Gomega) {
				newOperatorPod := getOperatorPod()
				Expect(newOperatorPod.Name).ToNot(Equal(oldOperatorPod.Name))
				Expect(newOperatorPod.Status.Phase).To(Equal(corev1.PodRunning))
			}, 45*time.Second, 5*time.Second)

			By("Assert the new operator's pod acquire the lease before 30 seconds")
			Eventually(func(g Gomega) {
				newLease, err := clients.CoordinationV1Interface.Leases(operatorNamespace).Get(context.Background(), consts.LeaderElectionID, metav1.GetOptions{})
				g.Expect(err).ToNot(HaveOccurred())

				g.Expect(newLease.Spec.HolderIdentity).ToNot(Equal(oldLease.Spec.HolderIdentity))
			}, 30*time.Second, 5*time.Second).Should(Succeed())
		},
			Entry("webhooks enabled", true),
			Entry("webhooks disabled", true),
		)

		Context("SriovNetworkMetricsExporter", func() {
			BeforeEach(func() {
				if discovery.Enabled() {
					Skip("Test unsuitable to be run in discovery mode")
				}

				initialValue := isFeatureFlagEnabled("metricsExporter")
				DeferCleanup(func() {
					By("Restoring initial feature flag value")
					setFeatureFlag("metricsExporter", initialValue)
				})

				By("Enabling `metricsExporter` feature flag")
				setFeatureFlag("metricsExporter", true)
			})

			It("should be deployed if the feature gate is enabled", func() {
				By("Checking that a daemon is scheduled on selected node")
				Eventually(func() bool {
					return isDaemonsetScheduledOnNodes("node-role.kubernetes.io/worker", "app=sriov-network-metrics-exporter")
				}, 1*time.Minute, 1*time.Second).Should(Equal(true))
			})

			It("should deploy ServiceMonitor if the Promethueus operator is installed", func() {
				_, err := clients.ServiceMonitors(operatorNamespace).List(context.Background(), metav1.ListOptions{})
				if k8serrors.IsNotFound(err) {
					Skip("Prometheus operator not available in the cluster")
				}

				By("Checking ServiceMonitor is deployed if needed")
				Eventually(func(g Gomega) {
					_, err := clients.ServiceMonitors(operatorNamespace).Get(context.Background(), "sriov-network-metrics-exporter", metav1.GetOptions{})
					g.Expect(err).ToNot(HaveOccurred())
				}).Should(Succeed())
			})
		})
	})

	Describe("Generic SriovNetworkNodePolicy", func() {
		numVfs := 5
		resourceName := testResourceName
		var node string
		var sriovDevice *sriovv1.InterfaceExt
		var discoveryFailed bool

		execute.BeforeAll(func() {
			var err error

			err = namespaces.Clean(operatorNamespace, namespaces.Test, clients, discovery.Enabled())
			Expect(err).ToNot(HaveOccurred())
			if discovery.Enabled() {
				node, resourceName, numVfs, sriovDevice, err = discovery.DiscoveredResources(clients,
					sriovInfos, operatorNamespace, defaultFilterPolicy,
					func(node string, sriovDeviceList []*sriovv1.InterfaceExt) (*sriovv1.InterfaceExt, bool) {
						if len(sriovDeviceList) == 0 {
							return nil, false
						}
						return sriovDeviceList[0], true
					},
				)

				Expect(err).ToNot(HaveOccurred())
				discoveryFailed = node == "" || resourceName == "" || numVfs < 5
			} else {
				node = sriovInfos.Nodes[0]
				createVanillaNetworkPolicy(node, sriovInfos, numVfs, resourceName)
				WaitForSRIOVStable()

				// Update info
				sriovInfos, err = cluster.DiscoverSriov(clients, operatorNamespace)
				Expect(err).ToNot(HaveOccurred())
				sriovDevice = findInterface(sriovInfos, node)

				By("Using device " + sriovDevice.Name + " on node " + node)

				Eventually(func() int64 {
					testedNode, err := clients.CoreV1Interface.Nodes().Get(context.Background(), node, metav1.GetOptions{})
					Expect(err).ToNot(HaveOccurred())
					resNum := testedNode.Status.Allocatable[corev1.ResourceName("openshift.io/"+resourceName)]
					allocatable, _ := resNum.AsInt64()
					return allocatable
				}, 10*time.Minute, time.Second).Should(Equal(int64(numVfs)))
			}
		})

		BeforeEach(func() {
			if discovery.Enabled() && discoveryFailed {
				Skip("Insufficient resources to run tests in discovery mode")
			}
			err := namespaces.CleanPods(namespaces.Test, clients)
			Expect(err).ToNot(HaveOccurred())
			err = namespaces.CleanNetworks(operatorNamespace, clients)
			Expect(err).ToNot(HaveOccurred())
		})
		Context("Resource Injector", func() {
			// 25815
			It("Should inject downward api volume with no labels present", func() {
				sriovNetwork := &sriovv1.SriovNetwork{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-apivolnetwork",
						Namespace: operatorNamespace,
					},
					Spec: sriovv1.SriovNetworkSpec{
						ResourceName:     resourceName,
						IPAM:             `{"type":"host-local","subnet":"10.10.10.0/24","rangeStart":"10.10.10.171","rangeEnd":"10.10.10.181","routes":[{"dst":"0.0.0.0/0"}],"gateway":"10.10.10.1"}`,
						NetworkNamespace: namespaces.Test,
					}}
				err := clients.Create(context.Background(), sriovNetwork)
				Expect(err).ToNot(HaveOccurred())

				Eventually(func() error {
					netAttDef := &netattdefv1.NetworkAttachmentDefinition{}
					return clients.Get(context.Background(), runtimeclient.ObjectKey{Name: "test-apivolnetwork", Namespace: namespaces.Test}, netAttDef)
				}, (10+snoTimeoutMultiplier*110)*time.Second, 1*time.Second).ShouldNot(HaveOccurred())

				podDefinition := pod.DefineWithNetworks([]string{sriovNetwork.Name})
				created, err := clients.Pods(namespaces.Test).Create(context.Background(), podDefinition, metav1.CreateOptions{})
				Expect(err).ToNot(HaveOccurred())

				var runningPod *corev1.Pod
				Eventually(func() corev1.PodPhase {
					runningPod, err = clients.Pods(namespaces.Test).Get(context.Background(), created.Name, metav1.GetOptions{})
					if k8serrors.IsNotFound(err) {
						return corev1.PodUnknown
					}
					Expect(err).ToNot(HaveOccurred())

					return runningPod.Status.Phase
				}, 3*time.Minute, time.Second).Should(Equal(corev1.PodRunning))

				var downwardVolume *corev1.Volume
				for _, v := range runningPod.Spec.Volumes {
					if v.Name == volumePodNetInfo {
						downwardVolume = v.DeepCopy()
						break
					}
				}

				Expect(downwardVolume).ToNot(BeNil(), "Downward volume not found")
				Expect(downwardVolume.DownwardAPI).ToNot(BeNil(), "Downward api not found in volume")
				Expect(downwardVolume.DownwardAPI.Items).ToNot(
					ContainElement(corev1.DownwardAPIVolumeFile{
						Path: "labels",
						FieldRef: &corev1.ObjectFieldSelector{
							APIVersion: "v1",
							FieldPath:  "metadata.labels",
						},
					}))
				Expect(downwardVolume.DownwardAPI.Items).To(
					ContainElement(corev1.DownwardAPIVolumeFile{
						Path: "annotations",
						FieldRef: &corev1.ObjectFieldSelector{
							APIVersion: "v1",
							FieldPath:  "metadata.annotations",
						},
					}))
			})

			It("Should inject downward api volume with labels present", func() {
				sriovNetwork := &sriovv1.SriovNetwork{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-apivolnetwork",
						Namespace: operatorNamespace,
					},
					Spec: sriovv1.SriovNetworkSpec{
						ResourceName:     resourceName,
						IPAM:             `{"type":"host-local","subnet":"10.10.10.0/24","rangeStart":"10.10.10.171","rangeEnd":"10.10.10.181","routes":[{"dst":"0.0.0.0/0"}],"gateway":"10.10.10.1"}`,
						NetworkNamespace: namespaces.Test,
					}}
				err := clients.Create(context.Background(), sriovNetwork)
				Expect(err).ToNot(HaveOccurred())

				Eventually(func() error {
					netAttDef := &netattdefv1.NetworkAttachmentDefinition{}
					return clients.Get(context.Background(), runtimeclient.ObjectKey{Name: "test-apivolnetwork", Namespace: namespaces.Test}, netAttDef)
				}, (10+snoTimeoutMultiplier*110)*time.Second, 1*time.Second).ShouldNot(HaveOccurred())

				podDefinition := pod.DefineWithNetworks([]string{sriovNetwork.Name})
				podDefinition.ObjectMeta.Labels = map[string]string{"anyname": "anyvalue"}
				created, err := clients.Pods(namespaces.Test).Create(context.Background(), podDefinition, metav1.CreateOptions{})
				Expect(err).ToNot(HaveOccurred())

				var runningPod *corev1.Pod
				Eventually(func() corev1.PodPhase {
					runningPod, err = clients.Pods(namespaces.Test).Get(context.Background(), created.Name, metav1.GetOptions{})
					if k8serrors.IsNotFound(err) {
						return corev1.PodUnknown
					}
					Expect(err).ToNot(HaveOccurred())

					return runningPod.Status.Phase
				}, 3*time.Minute, time.Second).Should(Equal(corev1.PodRunning))

				var downwardVolume *corev1.Volume
				for _, v := range runningPod.Spec.Volumes {
					if v.Name == volumePodNetInfo {
						downwardVolume = v.DeepCopy()
						break
					}
				}

				Expect(downwardVolume).ToNot(BeNil(), "Downward volume not found")
				Expect(downwardVolume.DownwardAPI).ToNot(BeNil(), "Downward api not found in volume")
				Expect(downwardVolume.DownwardAPI.Items).To(SatisfyAll(
					ContainElement(corev1.DownwardAPIVolumeFile{
						Path: "labels",
						FieldRef: &corev1.ObjectFieldSelector{
							APIVersion: "v1",
							FieldPath:  "metadata.labels",
						},
					}), ContainElement(corev1.DownwardAPIVolumeFile{
						Path: "annotations",
						FieldRef: &corev1.ObjectFieldSelector{
							APIVersion: "v1",
							FieldPath:  "metadata.annotations",
						},
					})))
			})
		})

		Context("VF flags", func() {
			hostNetPod := &corev1.Pod{} // Initialized in BeforeEach
			intf := &sriovv1.InterfaceExt{}

			validationFunction := func(networks []string, containsFunc func(line string) bool) {
				podObj := pod.RedefineWithNodeSelector(pod.DefineWithNetworks(networks), node)
				err := clients.Create(context.Background(), podObj)
				Expect(err).ToNot(HaveOccurred())
				Eventually(func() corev1.PodPhase {
					podObj, err = clients.Pods(namespaces.Test).Get(context.Background(), podObj.Name, metav1.GetOptions{})
					Expect(err).ToNot(HaveOccurred())
					return podObj.Status.Phase
				}, 5*time.Minute, time.Second).Should(Equal(corev1.PodRunning))

				vfIndex, err := podVFIndexInHost(hostNetPod, podObj, "net1")
				Expect(err).ToNot(HaveOccurred())

				Eventually(func() bool {
					stdout, stderr, err := pod.ExecCommand(clients, hostNetPod, "ip", "link", "show")
					Expect(err).ToNot(HaveOccurred())
					Expect(stderr).To(Equal(""))

					found := false
					for _, line := range strings.Split(stdout, "\n") {
						if strings.Contains(line, fmt.Sprintf("vf %d ", vfIndex)) && containsFunc(line) {
							found = true
							break
						}
					}
					if !found {
						return found
					}

					err = clients.Pods(namespaces.Test).Delete(context.Background(), podObj.Name, metav1.DeleteOptions{
						GracePeriodSeconds: pointer.Int64Ptr(0)})
					Expect(err).ToNot(HaveOccurred())

					return found
				}, time.Minute, time.Second).Should(BeTrue())

			}

			validateNetworkFields := func(sriovNetwork *sriovv1.SriovNetwork, validationString string) {
				netAttDef := &netattdefv1.NetworkAttachmentDefinition{}
				Eventually(func() error {
					return clients.Get(context.Background(), runtimeclient.ObjectKey{Name: sriovNetwork.Name, Namespace: namespaces.Test}, netAttDef)
				}, (10+snoTimeoutMultiplier*110)*time.Second, 1*time.Second).ShouldNot(HaveOccurred())

				checkFunc := func(line string) bool {
					return strings.Contains(line, validationString)
				}

				validationFunction([]string{sriovNetwork.Name}, checkFunc)
			}

			BeforeEach(func() {
				Eventually(func() int64 {
					testedNode, err := clients.CoreV1Interface.Nodes().Get(context.Background(), node, metav1.GetOptions{})
					Expect(err).ToNot(HaveOccurred())
					resNum := testedNode.Status.Allocatable[corev1.ResourceName("openshift.io/"+resourceName)]
					allocatable, _ := resNum.AsInt64()
					return allocatable
				}, 3*time.Minute, time.Second).Should(Equal(int64(numVfs)))

				hostNetPod = pod.DefineWithHostNetwork(node)
				err := clients.Create(context.Background(), hostNetPod)
				Expect(err).ToNot(HaveOccurred())
				Eventually(func() corev1.PodPhase {
					hostNetPod, err = clients.Pods(namespaces.Test).Get(context.Background(), hostNetPod.Name, metav1.GetOptions{})
					Expect(err).ToNot(HaveOccurred())
					return hostNetPod.Status.Phase
				}, 3*time.Minute, time.Second).Should(Equal(corev1.PodRunning))
			})

			// 25959
			It("Should configure the spoofChk boolean variable", func() {
				sriovNetwork := &sriovv1.SriovNetwork{
					ObjectMeta: metav1.ObjectMeta{Name: "test-spoofnetwork", Namespace: operatorNamespace},
					Spec: sriovv1.SriovNetworkSpec{
						ResourceName: resourceName,
						IPAM: `{"type":"host-local",
								"subnet":"10.10.10.0/24",
								"rangeStart":"10.10.10.171",
								"rangeEnd":"10.10.10.181",
								"routes":[{"dst":"0.0.0.0/0"}],
								"gateway":"10.10.10.1"}`,
						NetworkNamespace: namespaces.Test,
					}}

				By("configuring spoofChk on")
				copyObj := sriovNetwork.DeepCopy()
				copyObj.Spec.SpoofChk = on
				spoofChkStatusValidation := "spoof checking on"
				err := clients.Create(context.Background(), copyObj)
				Expect(err).ToNot(HaveOccurred())

				validateNetworkFields(copyObj, spoofChkStatusValidation)

				By("removing sriov network")
				err = clients.Delete(context.Background(), sriovNetwork)
				Expect(err).ToNot(HaveOccurred())

				Eventually(func() bool {
					networkDef := &sriovv1.SriovNetwork{}
					err := clients.Get(context.Background(), runtimeclient.ObjectKey{Name: "test-spoofnetwork",
						Namespace: operatorNamespace}, networkDef)
					return k8serrors.IsNotFound(err)
				}, 10*time.Second, 1*time.Second).Should(BeTrue())

				By("configuring spoofChk off")
				copyObj = sriovNetwork.DeepCopy()
				copyObj.Spec.SpoofChk = off
				spoofChkStatusValidation = "spoof checking off"
				err = clients.Create(context.Background(), copyObj)
				Expect(err).ToNot(HaveOccurred())

				validateNetworkFields(copyObj, spoofChkStatusValidation)
			})

			// 25960
			It("Should configure the trust boolean variable", func() {
				sriovNetwork := &sriovv1.SriovNetwork{
					ObjectMeta: metav1.ObjectMeta{Name: "test-trustnetwork", Namespace: operatorNamespace},
					Spec: sriovv1.SriovNetworkSpec{
						ResourceName: resourceName,
						IPAM: `{"type":"host-local",
								"subnet":"10.10.10.0/24",
								"rangeStart":"10.10.10.171",
								"rangeEnd":"10.10.10.181",
								"routes":[{"dst":"0.0.0.0/0"}],
								"gateway":"10.10.10.1"}`,
						NetworkNamespace: namespaces.Test,
					}}

				By("configuring trust on")
				copyObj := sriovNetwork.DeepCopy()
				copyObj.Spec.Trust = on
				trustChkStatusValidation := "trust on"
				err := clients.Create(context.Background(), copyObj)
				Expect(err).ToNot(HaveOccurred())

				validateNetworkFields(copyObj, trustChkStatusValidation)

				By("removing sriov network")
				err = clients.Delete(context.Background(), sriovNetwork)
				Expect(err).ToNot(HaveOccurred())
				Eventually(func() bool {
					networkDef := &sriovv1.SriovNetwork{}
					err := clients.Get(context.Background(), runtimeclient.ObjectKey{Name: "test-trustnetwork",
						Namespace: operatorNamespace}, networkDef)
					return k8serrors.IsNotFound(err)
				}, 10*time.Second, 1*time.Second).Should(BeTrue())

				By("configuring trust off")
				copyObj = sriovNetwork.DeepCopy()
				copyObj.Spec.Trust = off
				trustChkStatusValidation = "trust off"
				err = clients.Create(context.Background(), copyObj)
				Expect(err).ToNot(HaveOccurred())

				validateNetworkFields(copyObj, trustChkStatusValidation)
			})

			// 25961
			It("Should configure the the link state variable", func() {
				if cluster.VirtualCluster() {
					// https://bugzilla.redhat.com/show_bug.cgi?id=2214976
					Skip("Bug in IGB driver")
				}

				sriovNetwork := &sriovv1.SriovNetwork{
					ObjectMeta: metav1.ObjectMeta{Name: "test-statenetwork", Namespace: operatorNamespace},
					Spec: sriovv1.SriovNetworkSpec{
						ResourceName: resourceName,
						IPAM: `{"type":"host-local",
								"subnet":"10.10.10.0/24",
								"rangeStart":"10.10.10.171",
								"rangeEnd":"10.10.10.181",
								"routes":[{"dst":"0.0.0.0/0"}],
								"gateway":"10.10.10.1"}`,
						NetworkNamespace: namespaces.Test,
					}}

				By("configuring link-state as enabled")
				enabledLinkNetwork := sriovNetwork.DeepCopy()
				enabledLinkNetwork.Spec.LinkState = "enable"
				linkStateChkStatusValidation := "link-state enable"
				err := clients.Create(context.Background(), enabledLinkNetwork)
				Expect(err).ToNot(HaveOccurred())

				validateNetworkFields(enabledLinkNetwork, linkStateChkStatusValidation)

				By("removing sriov network")
				err = clients.Delete(context.Background(), enabledLinkNetwork)
				Expect(err).ToNot(HaveOccurred())
				Eventually(func() bool {
					networkDef := &sriovv1.SriovNetwork{}
					err := clients.Get(context.Background(), runtimeclient.ObjectKey{Name: "test-statenetwork",
						Namespace: operatorNamespace}, networkDef)
					return k8serrors.IsNotFound(err)
				}, 10*time.Second, 1*time.Second).Should(BeTrue())

				By("configuring link-state as disable")
				disabledLinkNetwork := sriovNetwork.DeepCopy()
				disabledLinkNetwork.Spec.LinkState = "disable"
				linkStateChkStatusValidation = "link-state disable"
				err = clients.Create(context.Background(), disabledLinkNetwork)
				Expect(err).ToNot(HaveOccurred())

				validateNetworkFields(disabledLinkNetwork, linkStateChkStatusValidation)

				By("removing sriov network")
				err = clients.Delete(context.Background(), disabledLinkNetwork)
				Expect(err).ToNot(HaveOccurred())
				Eventually(func() bool {
					networkDef := &sriovv1.SriovNetwork{}
					err := clients.Get(context.Background(), runtimeclient.ObjectKey{Name: "test-statenetwork",
						Namespace: operatorNamespace}, networkDef)
					return k8serrors.IsNotFound(err)
				}, 10*time.Second, 1*time.Second).Should(BeTrue())

				By("configuring link-state as auto")
				autoLinkNetwork := sriovNetwork.DeepCopy()
				autoLinkNetwork.Spec.LinkState = "auto"
				linkStateChkStatusValidation = "link-state auto"
				err = clients.Create(context.Background(), autoLinkNetwork)
				Expect(err).ToNot(HaveOccurred())

				validateNetworkFields(autoLinkNetwork, linkStateChkStatusValidation)
			})

			// 25963
			Describe("rate limit", func() {
				It("Should configure the requested rate limit flags under the vf", func() {
					if intf.Driver != "mlx5_core" {
						// There is an issue with the intel cards both driver i40 and ixgbe
						// BZ 1772847
						// BZ 1772815
						// BZ 1236146
						Skip("Skip rate limit test no mellanox driver found")
					}

					var maxTxRate = 100
					var minTxRate = 40
					sriovNetwork := &sriovv1.SriovNetwork{ObjectMeta: metav1.ObjectMeta{Name: "test-ratenetwork", Namespace: operatorNamespace},
						Spec: sriovv1.SriovNetworkSpec{
							ResourceName: resourceName,
							IPAM: `{"type":"host-local",
								"subnet":"10.10.10.0/24",
								"rangeStart":"10.10.10.171",
								"rangeEnd":"10.10.10.181",
								"routes":[{"dst":"0.0.0.0/0"}],
								"gateway":"10.10.10.1"}`,
							MaxTxRate:        &maxTxRate,
							MinTxRate:        &minTxRate,
							NetworkNamespace: namespaces.Test,
						}}
					err := clients.Create(context.Background(), sriovNetwork)
					Expect(err).ToNot(HaveOccurred())

					netAttDef := &netattdefv1.NetworkAttachmentDefinition{}
					Eventually(func() error {
						return clients.Get(context.Background(), runtimeclient.ObjectKey{Name: "test-ratenetwork", Namespace: namespaces.Test}, netAttDef)
					}, (10+snoTimeoutMultiplier*110)*time.Second, 1*time.Second).ShouldNot(HaveOccurred())

					checkFunc := func(line string) bool {
						if strings.Contains(line, "max_tx_rate 100Mbps") &&
							strings.Contains(line, "min_tx_rate 40Mbps") {
							return true
						}
						return false
					}

					validationFunction([]string{"test-ratenetwork"}, checkFunc)
				})
			})

			// 25963
			Describe("vlan and Qos vlan", func() {
				It("Should configure the requested vlan and Qos vlan flags under the vf", func() {
					sriovNetwork := &sriovv1.SriovNetwork{ObjectMeta: metav1.ObjectMeta{Name: "test-quosnetwork", Namespace: operatorNamespace},
						Spec: sriovv1.SriovNetworkSpec{
							ResourceName: resourceName,
							IPAM: `{"type":"host-local",
								"subnet":"10.10.10.0/24",
								"rangeStart":"10.10.10.171",
								"rangeEnd":"10.10.10.181",
								"routes":[{"dst":"0.0.0.0/0"}],
								"gateway":"10.10.10.1"}`,
							Vlan:             1,
							VlanQoS:          2,
							NetworkNamespace: namespaces.Test,
						}}
					err := clients.Create(context.Background(), sriovNetwork)
					Expect(err).ToNot(HaveOccurred())

					netAttDef := &netattdefv1.NetworkAttachmentDefinition{}
					Eventually(func() error {
						return clients.Get(context.Background(), runtimeclient.ObjectKey{Name: "test-quosnetwork", Namespace: namespaces.Test}, netAttDef)
					}, (10+snoTimeoutMultiplier*110)*time.Second, 1*time.Second).ShouldNot(HaveOccurred())

					checkFunc := func(line string) bool {
						if strings.Contains(line, "vlan 1") &&
							strings.Contains(line, "qos 2") {
							return true
						}
						return false
					}

					validationFunction([]string{"test-quosnetwork"}, checkFunc)
				})
			})
		})

		Context("Multiple sriov device and attachment", func() {
			// 25834
			It("Should configure multiple network attachments", func() {
				ipam := ipamIpv4
				err := network.CreateSriovNetwork(clients, sriovDevice, sriovNetworkName, namespaces.Test, operatorNamespace, resourceName, ipam)
				Expect(err).ToNot(HaveOccurred())
				waitForNetAttachDef(sriovNetworkName, namespaces.Test)

				pod := createTestPod(node, []string{sriovNetworkName, sriovNetworkName})
				nics, err := network.GetNicsByPrefix(pod, "net")
				Expect(err).ToNot(HaveOccurred())
				Expect(len(nics)).To(Equal(2), "No sriov network interfaces found.")
			})
		})

		Context("IPv6 configured secondary interfaces on pods", func() {
			// 25874
			It("should be able to ping each other", func() {
				ipv6NetworkName := testIpv6NetworkName
				ipam := ipamIpv6
				err := network.CreateSriovNetwork(clients, sriovDevice, ipv6NetworkName, namespaces.Test, operatorNamespace, resourceName, ipam)
				Expect(err).ToNot(HaveOccurred())
				waitForNetAttachDef(ipv6NetworkName, namespaces.Test)

				pod := createTestPod(node, []string{ipv6NetworkName})
				ips, err := network.GetSriovNicIPs(pod, "net1")
				Expect(err).ToNot(HaveOccurred())
				Expect(ips).NotTo(BeNil(), "No sriov network interface found.")
				Expect(len(ips)).Should(Equal(1))
				for _, ip := range ips {
					pingPod(ip, node, ipv6NetworkName)
				}
			})
		})

		Context("NAD update", func() {
			// 24713
			It("NAD is updated when SriovNetwork spec/networkNamespace is changed", func() {
				ns1 := "test-z1"
				ns2 := "test-z2"
				defer namespaces.DeleteAndWait(clients, ns1, 1*time.Minute)
				defer namespaces.DeleteAndWait(clients, ns2, 1*time.Minute)
				err := namespaces.Create(ns1, clients)
				Expect(err).ToNot(HaveOccurred())
				err = namespaces.Create(ns2, clients)
				Expect(err).ToNot(HaveOccurred())

				ipam := ipamIpv4
				err = network.CreateSriovNetwork(clients, sriovDevice, sriovNetworkName, ns1, operatorNamespace, resourceName, ipam)
				Expect(err).ToNot(HaveOccurred())
				waitForNetAttachDef(sriovNetworkName, ns1)

				body, _ := json.Marshal([]patchBody{{
					Op:    "replace",
					Path:  "/spec/networkNamespace",
					Value: ns2,
				}})
				clients.SriovnetworkV1Interface.RESTClient().Patch(types.JSONPatchType).Namespace(operatorNamespace).Resource("sriovnetworks").Name(sriovNetworkName).Body(body).Do(context.Background())

				waitForNetAttachDef(sriovNetworkName, ns2)

				Consistently(func() error {
					netAttDef := &netattdefv1.NetworkAttachmentDefinition{}
					return clients.Get(context.Background(), runtimeclient.ObjectKey{Name: sriovNetworkName, Namespace: ns1}, netAttDef)
				}, 5*time.Second, 1*time.Second).Should(HaveOccurred())
			})
		})

		Context("NAD update", func() {
			// 24714
			It("NAD default gateway is updated when SriovNetwork ipam is changed", func() {

				ipam := `{
					"type": "host-local",
					"subnet": "10.11.11.0/24",
					"gateway": "%s"
				  }`
				err := network.CreateSriovNetwork(clients, sriovDevice, sriovNetworkName, namespaces.Test, operatorNamespace, resourceName, fmt.Sprintf(ipam, "10.11.11.1"))
				Expect(err).ToNot(HaveOccurred())

				Eventually(func() bool {
					netAttDef := &netattdefv1.NetworkAttachmentDefinition{}
					err := clients.Get(context.Background(), runtimeclient.ObjectKey{Name: sriovNetworkName, Namespace: namespaces.Test}, netAttDef)
					if k8serrors.IsNotFound(err) {
						return false
					}
					return strings.Contains(netAttDef.Spec.Config, "10.11.11.1")
				}, (30+snoTimeoutMultiplier*90)*time.Second, 1*time.Second).Should(BeTrue())

				sriovNetwork := &sriovv1.SriovNetwork{}
				err = clients.Get(context.TODO(), runtimeclient.ObjectKey{Name: sriovNetworkName, Namespace: operatorNamespace}, sriovNetwork)
				Expect(err).ToNot(HaveOccurred())
				sriovNetwork.Spec.IPAM = fmt.Sprintf(ipam, "10.11.11.100")
				err = clients.Update(context.Background(), sriovNetwork)
				Expect(err).ToNot(HaveOccurred())

				Eventually(func() bool {
					netAttDef := &netattdefv1.NetworkAttachmentDefinition{}
					clients.Get(context.Background(), runtimeclient.ObjectKey{Name: sriovNetworkName, Namespace: namespaces.Test}, netAttDef)
					return strings.Contains(netAttDef.Spec.Config, "10.11.11.100")
				}, (30+snoTimeoutMultiplier*90)*time.Second, 1*time.Second).Should(BeTrue())
			})
		})

		Context("SRIOV and macvlan", func() {
			// 25834
			It("Should be able to create a pod with both sriov and macvlan interfaces", func() {
				ipam := ipamIpv4
				err := network.CreateSriovNetwork(clients, sriovDevice, sriovNetworkName, namespaces.Test, operatorNamespace, resourceName, ipam)
				Expect(err).ToNot(HaveOccurred())
				waitForNetAttachDef(sriovNetworkName, namespaces.Test)

				macvlanNadName := "macvlan-nad"
				macvlanNad := network.CreateMacvlanNetworkAttachmentDefinition(macvlanNadName, namespaces.Test)
				err = clients.Create(context.Background(), &macvlanNad)
				Expect(err).ToNot(HaveOccurred())
				defer clients.Delete(context.Background(), &macvlanNad)
				Eventually(func() error {
					netAttDef := &netattdefv1.NetworkAttachmentDefinition{}
					return clients.Get(context.Background(), runtimeclient.ObjectKey{Name: macvlanNadName, Namespace: namespaces.Test}, netAttDef)
				}, (10+snoTimeoutMultiplier*110)*time.Second, 1*time.Second).ShouldNot(HaveOccurred())

				createdPod := createTestPod(node, []string{sriovNetworkName, macvlanNadName})

				nics, err := network.GetNicsByPrefix(createdPod, "net")
				Expect(err).ToNot(HaveOccurred())
				Expect(len(nics)).To(Equal(2), "Pod should have two multus nics.")

				stdout, _, err := pod.ExecCommand(clients, createdPod, "ethtool", "-i", "net1")
				Expect(err).ToNot(HaveOccurred())

				sriovVfDriver := getDriver(stdout)
				Expect(cluster.IsVFDriverSupported(sriovVfDriver)).To(BeTrue())

				stdout, _, err = pod.ExecCommand(clients, createdPod, "ethtool", "-i", "net2")
				macvlanDriver := getDriver(stdout)
				Expect(err).ToNot(HaveOccurred())
				Expect(macvlanDriver).To(Equal("macvlan"))

			})
		})

		Context("Meta Plugin Configuration", func() {
			It("Should be able to configure a metaplugin", func() {
				ipam := ipamIpv4
				config := func(network *sriovv1.SriovNetwork) {
					network.Spec.MetaPluginsConfig = `{ "type": "tuning", "sysctl": { "net.ipv4.conf.IFNAME.accept_redirects": "1"}}`
				}
				err := network.CreateSriovNetwork(clients, sriovDevice, sriovNetworkName, namespaces.Test, operatorNamespace, resourceName, ipam, []network.SriovNetworkOptions{config}...)
				Expect(err).ToNot(HaveOccurred())
				waitForNetAttachDef(sriovNetworkName, namespaces.Test)

				testPod := createTestPod(node, []string{sriovNetworkName})
				stdout, _, err := pod.ExecCommand(clients, testPod, "more", "/proc/sys/net/ipv4/conf/net1/accept_redirects")
				Expect(err).ToNot(HaveOccurred())

				Expect(strings.TrimSpace(stdout)).To(Equal("1"))
			})
		})

		Context("Virtual Functions", func() {
			// 21396
			It("should release the VFs once the pod deleted and same VFs can be used by the new created pods", func() {
				if discovery.Enabled() {
					Skip("Virtual functions allocation test consumes all the available vfs, not suitable for discovery mode")
					// TODO Split this so we check the allocation / unallocation but with a limited number of
					// resources.
				}

				By("Create first Pod which consumes all available VFs")
				sriovDevice, err := sriovInfos.FindOneSriovDevice(node)
				Expect(err).ToNot(HaveOccurred())
				By("Using device " + sriovDevice.Name + " on node " + node)

				ipam := ipamIpv6
				err = network.CreateSriovNetwork(clients, sriovDevice, sriovNetworkName, namespaces.Test, operatorNamespace, resourceName, ipam)
				Expect(err).ToNot(HaveOccurred())
				waitForNetAttachDef(sriovNetworkName, namespaces.Test)

				testPodA := pod.RedefineWithNodeSelector(
					pod.DefineWithNetworks([]string{sriovNetworkName, sriovNetworkName, sriovNetworkName, sriovNetworkName, sriovNetworkName}),
					node,
				)
				runningPodA, err := clients.Pods(testPodA.Namespace).Create(context.Background(), testPodA, metav1.CreateOptions{})
				Expect(err).ToNot(HaveOccurred(), fmt.Sprintf("Error to create pod %s", testPodA.Name))
				By("Checking that first Pod is in Running state")
				Eventually(func() corev1.PodPhase {
					runningPodA, err = clients.Pods(namespaces.Test).Get(context.Background(), runningPodA.Name, metav1.GetOptions{})
					Expect(err).ToNot(HaveOccurred())
					return runningPodA.Status.Phase
				}, 3*time.Minute, time.Second).Should(Equal(corev1.PodRunning))
				By("Create second Pod which consumes one more VF")

				testPodB := pod.RedefineWithNodeSelector(
					pod.DefineWithNetworks([]string{sriovNetworkName}),
					node,
				)
				runningPodB, err := clients.Pods(testPodB.Namespace).Create(context.Background(), testPodB, metav1.CreateOptions{})
				Expect(err).ToNot(HaveOccurred(), fmt.Sprintf("Error to create pod %s", testPodB.Name))
				By("Checking second that pod is in Pending state")
				Eventually(func() corev1.PodPhase {
					runningPodB, err = clients.Pods(namespaces.Test).Get(context.Background(), runningPodB.Name, metav1.GetOptions{})
					Expect(err).ToNot(HaveOccurred())
					return runningPodB.Status.Phase
				}, 3*time.Minute, time.Second).Should(Equal(corev1.PodPending))

				By("Checking that relevant error event was originated")
				Eventually(func() bool {
					events, err := clients.Events(namespaces.Test).List(context.Background(), metav1.ListOptions{})
					Expect(err).ToNot(HaveOccurred())

					for _, val := range events.Items {
						if val.InvolvedObject.Name == runningPodB.Name && strings.Contains(val.Message, fmt.Sprintf("Insufficient openshift.io/%s", resourceName)) {
							return true
						}
					}
					return false
				}, 2*time.Minute, 10*time.Second).Should(BeTrue(), "Error to detect Required Event")
				By("Delete first pod and release all VFs")
				err = clients.Pods(namespaces.Test).Delete(context.Background(), runningPodA.Name, metav1.DeleteOptions{
					GracePeriodSeconds: pointer.Int64Ptr(0),
				})
				Expect(err).ToNot(HaveOccurred(), fmt.Sprintf("Error to delete pod %s", runningPodA.Name))
				By("Checking that second pod is able to use released VF")
				Eventually(func() corev1.PodPhase {
					runningPodB, err = clients.Pods(namespaces.Test).Get(context.Background(), runningPodB.Name, metav1.GetOptions{})
					Expect(err).ToNot(HaveOccurred())
					return runningPodB.Status.Phase
				}, 3*time.Minute, time.Second).Should(Equal(corev1.PodRunning))
			})

			It("should reconcile managed VF if status is changed", func() {
				originalMtu := sriovDevice.Mtu
				lowerMtu := originalMtu - 500

				By("manually decreasing the MTU")
				_, errOutput, err := runCommandOnConfigDaemon(node, "/bin/bash", "-c", fmt.Sprintf("echo %d > /sys/bus/pci/devices/%s/net/%s/mtu", lowerMtu, sriovDevice.PciAddress, sriovDevice.Name))
				Expect(err).ToNot(HaveOccurred())
				Expect(errOutput).To(Equal(""))

				By("waiting for the mtu to be updated in the status")
				Eventually(func() sriovv1.InterfaceExts {
					nodeState, err := clients.SriovNetworkNodeStates(operatorNamespace).Get(context.Background(), node, metav1.GetOptions{})
					Expect(err).ToNot(HaveOccurred())
					return nodeState.Status.Interfaces
				}, 3*time.Minute, 1*time.Second).Should(ContainElement(MatchFields(
					IgnoreExtras,
					Fields{
						"Name":       Equal(sriovDevice.Name),
						"Mtu":        Equal(lowerMtu),
						"PciAddress": Equal(sriovDevice.PciAddress),
						"NumVfs":     Equal(sriovDevice.NumVfs),
					})))

				By("waiting for the mtu to be restored")
				Eventually(func() sriovv1.InterfaceExts {
					nodeState, err := clients.SriovNetworkNodeStates(operatorNamespace).Get(context.Background(), node, metav1.GetOptions{})
					Expect(err).ToNot(HaveOccurred())
					return nodeState.Status.Interfaces
				}, 3*time.Minute, 1*time.Second).Should(ContainElement(MatchFields(
					IgnoreExtras,
					Fields{
						"Name":       Equal(sriovDevice.Name),
						"Mtu":        Equal(originalMtu),
						"PciAddress": Equal(sriovDevice.PciAddress),
						"NumVfs":     Equal(sriovDevice.NumVfs),
					})))

				higherMtu := originalMtu + 500

				By("manually increasing the MTU")
				_, errOutput, err = runCommandOnConfigDaemon(node, "/bin/bash", "-c", fmt.Sprintf("echo %d > /sys/bus/pci/devices/%s/net/%s/mtu", higherMtu, sriovDevice.PciAddress, sriovDevice.Name))
				Expect(err).ToNot(HaveOccurred())
				Expect(errOutput).To(Equal(""))

				By("waiting for the mtu to be updated in the status")
				Eventually(func() sriovv1.InterfaceExts {
					nodeState, err := clients.SriovNetworkNodeStates(operatorNamespace).Get(context.Background(), node, metav1.GetOptions{})
					Expect(err).ToNot(HaveOccurred())
					return nodeState.Status.Interfaces
				}, 3*time.Minute, 1*time.Second).Should(ContainElement(MatchFields(
					IgnoreExtras,
					Fields{
						"Name":       Equal(sriovDevice.Name),
						"Mtu":        Equal(higherMtu),
						"PciAddress": Equal(sriovDevice.PciAddress),
						"NumVfs":     Equal(sriovDevice.NumVfs),
					})))

				By("expecting the mtu to consistently stay at the new higher level")
				Consistently(func() sriovv1.InterfaceExts {
					nodeState, err := clients.SriovNetworkNodeStates(operatorNamespace).Get(context.Background(), node, metav1.GetOptions{})
					Expect(err).ToNot(HaveOccurred())
					return nodeState.Status.Interfaces
				}, 3*time.Minute, 15*time.Second).Should(ContainElement(MatchFields(
					IgnoreExtras,
					Fields{
						"Name":       Equal(sriovDevice.Name),
						"Mtu":        Equal(higherMtu),
						"PciAddress": Equal(sriovDevice.PciAddress),
						"NumVfs":     Equal(sriovDevice.NumVfs),
					})))

				By("manually returning the MTU to the original level")
				_, errOutput, err = runCommandOnConfigDaemon(node, "/bin/bash", "-c", fmt.Sprintf("echo %d > /sys/bus/pci/devices/%s/net/%s/mtu", originalMtu, sriovDevice.PciAddress, sriovDevice.Name))
				Expect(err).ToNot(HaveOccurred())
				Expect(errOutput).To(Equal(""))

				By("waiting for the mtu to be updated in the status")
				Eventually(func() sriovv1.InterfaceExts {
					nodeState, err := clients.SriovNetworkNodeStates(operatorNamespace).Get(context.Background(), node, metav1.GetOptions{})
					Expect(err).ToNot(HaveOccurred())
					return nodeState.Status.Interfaces
				}, 3*time.Minute, 1*time.Second).Should(ContainElement(MatchFields(
					IgnoreExtras,
					Fields{
						"Name":       Equal(sriovDevice.Name),
						"Mtu":        Equal(originalMtu),
						"PciAddress": Equal(sriovDevice.PciAddress),
						"NumVfs":     Equal(sriovDevice.NumVfs),
					})))

				By("expecting the mtu to consistently stay at the original level")
				Consistently(func() sriovv1.InterfaceExts {
					nodeState, err := clients.SriovNetworkNodeStates(operatorNamespace).Get(context.Background(), node, metav1.GetOptions{})
					Expect(err).ToNot(HaveOccurred())
					return nodeState.Status.Interfaces
				}, 3*time.Minute, 15*time.Second).Should(ContainElement(MatchFields(
					IgnoreExtras,
					Fields{
						"Name":       Equal(sriovDevice.Name),
						"Mtu":        Equal(originalMtu),
						"PciAddress": Equal(sriovDevice.PciAddress),
						"NumVfs":     Equal(sriovDevice.NumVfs),
					})))
			})
		})

		Context("CNI Logging level", func() {
			It("Debug logging should be visible in multus pod", func() {
				sriovNetworkName := "test-log-level-debug-no-file"
				err := network.CreateSriovNetwork(clients, sriovDevice, sriovNetworkName,
					namespaces.Test, operatorNamespace, resourceName, ipamIpv4,
					func(sn *sriovv1.SriovNetwork) {
						sn.Spec.LogLevel = "debug"
					})
				Expect(err).ToNot(HaveOccurred())
				waitForNetAttachDef(sriovNetworkName, namespaces.Test)

				testPod := createTestPod(node, []string{sriovNetworkName})

				recentMultusLogs := getMultusPodLogs(testPod.Spec.NodeName, testPod.Status.StartTime.Time)

				Expect(recentMultusLogs).To(
					ContainElement(
						// Assert against multiple ContainSubstring condition because we can't make assumption on the order of the chunks
						And(
							ContainSubstring(`level="debug"`),
							ContainSubstring(`msg="function called"`),
							ContainSubstring(`func="cmdAdd"`),
							ContainSubstring(`cniName="sriov-cni"`),
							ContainSubstring(`ifname="net1"`),
						),
					))
			})
		})
	})

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
							IPAM:             `{"type":"host-local","subnet":"10.10.10.0/24","rangeStart":"10.10.10.171","rangeEnd":"10.10.10.181","routes":[{"dst":"0.0.0.0/0"}],"gateway":"10.10.10.1"}`,
							NetworkNamespace: namespaces.Test,
						}}

					err = clients.Create(context.Background(), sriovNetwork)
					Expect(err).ToNot(HaveOccurred())
					waitForNetAttachDef("test-vfionetwork", namespaces.Test)

					podDefinition := pod.DefineWithNetworks([]string{"test-vfionetwork"})
					firstPod, err := clients.Pods(namespaces.Test).Create(context.Background(), podDefinition, metav1.CreateOptions{})
					Expect(err).ToNot(HaveOccurred())

					Eventually(func() corev1.PodPhase {
						firstPod, _ = clients.Pods(namespaces.Test).Get(context.Background(), firstPod.Name, metav1.GetOptions{})
						return firstPod.Status.Phase
					}, 3*time.Minute, time.Second).Should(Equal(corev1.PodRunning))

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
						"rangeEnd":"10.10.10.181",
						"routes":[{"dst":"0.0.0.0/0"}],
						"gateway":"10.10.10.1"
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
							IPAM:             `{"type":"host-local","subnet":"10.10.10.0/24","rangeStart":"10.10.10.171","rangeEnd":"10.10.10.181","routes":[{"dst":"0.0.0.0/0"}],"gateway":"10.10.10.1"}`,
							NetworkNamespace: namespaces.Test,
						}}

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

					Eventually(func() corev1.PodPhase {
						firstPod, _ = clients.Pods(namespaces.Test).Get(context.Background(), firstPod.Name, metav1.GetOptions{})
						return firstPod.Status.Phase
					}, 3*time.Minute, time.Second).Should(Equal(corev1.PodRunning))

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

					Eventually(func() corev1.PodPhase {
						secondPod, _ = clients.Pods(namespaces.Test).Get(context.Background(), secondPod.Name, metav1.GetOptions{})
						return secondPod.Status.Phase
					}, 3*time.Minute, time.Second).Should(Equal(corev1.PodRunning))

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

		Context("Nic Validation", func() {
			numVfs := 5
			resourceName := testResourceName

			BeforeEach(func() {
				if discovery.Enabled() {
					Skip("Test unsuitable to be run in discovery mode")
				}
			})

			findSriovDevice := func(vendorID, deviceID string) (string, sriovv1.InterfaceExt) {
				for _, node := range sriovInfos.Nodes {
					for _, nic := range sriovInfos.States[node].Status.Interfaces {
						if vendorID != "" && deviceID != "" && nic.Vendor == vendorID && nic.DeviceID == deviceID {
							return node, nic
						}
					}
				}

				return "", sriovv1.InterfaceExt{}
			}

			DescribeTable("Test connectivity using the requested nic", func(vendorID, deviceID string) {
				By("searching for the requested network card")
				node, nic := findSriovDevice(vendorID, deviceID)
				if node == "" {
					Skip(fmt.Sprintf("skip nic validate as wasn't able to find a nic with vendorID %s and deviceID %s", vendorID, deviceID))
				}

				By("creating a network policy")
				config := &sriovv1.SriovNetworkNodePolicy{
					ObjectMeta: metav1.ObjectMeta{
						GenerateName: "test-policy",
						Namespace:    operatorNamespace,
					},

					Spec: sriovv1.SriovNetworkNodePolicySpec{
						NodeSelector: map[string]string{
							"kubernetes.io/hostname": node,
						},
						NumVfs:       numVfs,
						ResourceName: resourceName,
						Priority:     99,
						NicSelector: sriovv1.SriovNetworkNicSelector{
							PfNames: []string{nic.Name},
						},
						DeviceType: "netdevice",
					},
				}
				err := clients.Create(context.Background(), config)
				Expect(err).ToNot(HaveOccurred())

				By("waiting for the node state to be updated")
				Eventually(func() sriovv1.Interfaces {
					nodeState, err := clients.SriovNetworkNodeStates(operatorNamespace).Get(context.Background(), node, metav1.GetOptions{})
					Expect(err).ToNot(HaveOccurred())
					return nodeState.Spec.Interfaces
				}, 1*time.Minute, 1*time.Second).Should(ContainElement(MatchFields(
					IgnoreExtras,
					Fields{
						"Name":   Equal(nic.Name),
						"NumVfs": Equal(numVfs),
					})))

				By("waiting the sriov to be stable on the node")
				WaitForSRIOVStable()

				By("waiting for the resources to be available")
				Eventually(func() int64 {
					testedNode, err := clients.CoreV1Interface.Nodes().Get(context.Background(), node, metav1.GetOptions{})
					Expect(err).ToNot(HaveOccurred())
					resNum := testedNode.Status.Allocatable[corev1.ResourceName("openshift.io/"+resourceName)]
					allocatable, _ := resNum.AsInt64()
					return allocatable
				}, 10*time.Minute, time.Second).Should(Equal(int64(numVfs)))

				By("creating a network object")
				ipv6NetworkName := testIpv6NetworkName
				ipam := ipamIpv4
				err = network.CreateSriovNetwork(clients, &nic, ipv6NetworkName, namespaces.Test, operatorNamespace, resourceName, ipam)
				Expect(err).ToNot(HaveOccurred())
				waitForNetAttachDef(ipv6NetworkName, namespaces.Test)

				By("creating a pod")
				pod := createTestPod(node, []string{ipv6NetworkName})
				ips, err := network.GetSriovNicIPs(pod, "net1")
				Expect(err).ToNot(HaveOccurred())
				Expect(ips).NotTo(BeNil(), "No sriov network interface found.")
				Expect(len(ips)).Should(Equal(1))

				By("run pod from another pod")
				for _, ip := range ips {
					pingPod(ip, node, ipv6NetworkName)
				}
			},
				//25321
				Entry("Intel Corporation Ethernet Controller XXV710 for 25GbE SFP28", "8086", "158b"),
				Entry("Ethernet Controller XXV710 Intel(R) FPGA Programmable Acceleration Card N3000 for Networking", "8086", "0d58"))
		})

		Context("Resource Injector", func() {
			BeforeEach(func() {
				if discovery.Enabled() {
					Skip("Test unsuitable to be run in discovery mode")
				}
			})

			AfterEach(func() {
				if !discovery.Enabled() {
					setSriovOperatorSpecFlag(operatorNetworkInjectorFlag, true)
					setSriovOperatorSpecFlag(operatorWebhookFlag, true)
				}
			})

			It("SR-IOV Operator Config, disable Resource Injector", func() {

				setSriovOperatorSpecFlag(operatorNetworkInjectorFlag, false)

				assertObjectIsNotFound("network-resources-injector", &appsv1.DaemonSet{})

				Eventually(func() int {
					podsList, err := clients.Pods(operatorNamespace).
						List(context.Background(),
							metav1.ListOptions{LabelSelector: "app=network-resources-injector"})
					Expect(err).ToNot(HaveOccurred())
					return len(podsList.Items)
				}, 2*time.Minute, 10*time.Second).Should(BeZero())

				assertObjectIsNotFound("network-resources-injector-service", &corev1.Service{})
				assertObjectIsNotFound("network-resources-injector", &rbacv1.ClusterRole{})
				assertObjectIsNotFound("network-resources-injector-role-binding", &rbacv1.ClusterRoleBinding{})
				assertObjectIsNotFound("network-resources-injector-config", &admission.MutatingWebhookConfiguration{})
				assertObjectIsNotFound("nri-control-switches", &corev1.ConfigMap{})
			})

			It("SR-IOV Operator Config, disable Operator Webhook", func() {

				setSriovOperatorSpecFlag(operatorWebhookFlag, false)

				assertObjectIsNotFound("operator-webhook", &appsv1.DaemonSet{})

				Eventually(func() int {
					podsList, err := clients.Pods(operatorNamespace).
						List(context.Background(),
							metav1.ListOptions{LabelSelector: "app=operator-webhook"})
					Expect(err).ToNot(HaveOccurred())
					return len(podsList.Items)
				}, 2*time.Minute, 10*time.Second).Should(BeZero())

				assertObjectIsNotFound("operator-webhook-service", &corev1.Service{})
				assertObjectIsNotFound("operator-webhook", &rbacv1.ClusterRole{})
				assertObjectIsNotFound("operator-webhook-role-binding", &rbacv1.ClusterRoleBinding{})
				assertObjectIsNotFound("sriov-operator-webhook-config", &admission.MutatingWebhookConfiguration{})
			})

			It("SR-IOV Operator Config, disable Resource Injector and Operator Webhook", func() {

				setSriovOperatorSpecFlag(operatorNetworkInjectorFlag, false)
				setSriovOperatorSpecFlag(operatorWebhookFlag, false)

				// Assert disabling both flags works, no need to check every resource as above tests.
				assertObjectIsNotFound("operator-webhook", &appsv1.DaemonSet{})
				assertObjectIsNotFound("network-resources-injector", &appsv1.DaemonSet{})
			})

		})

		Context("vhost-net and tun devices Validation", func() {
			var node string
			resourceName := "vhostresource"
			vhostnetwork := "test-vhostnetwork"
			numVfs := 5
			var intf *sriovv1.InterfaceExt
			var err error

			execute.BeforeAll(func() {
				if discovery.Enabled() {
					node, resourceName, numVfs, intf, err = discovery.DiscoveredResources(clients,
						sriovInfos, operatorNamespace,
						func(policy sriovv1.SriovNetworkNodePolicy) bool {
							if !defaultFilterPolicy(policy) {
								return false
							}
							if !policy.Spec.NeedVhostNet {
								return false
							}
							return true
						},
						func(node string, sriovDeviceList []*sriovv1.InterfaceExt) (*sriovv1.InterfaceExt, bool) {
							if len(sriovDeviceList) == 0 {
								return nil, false
							}
							return sriovDeviceList[0], true
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
							GenerateName: "test-vhostpolicy",
							Namespace:    operatorNamespace,
						},

						Spec: sriovv1.SriovNetworkNodePolicySpec{
							NodeSelector: map[string]string{
								"kubernetes.io/hostname": node,
							},
							NumVfs:       5,
							ResourceName: resourceName,
							Priority:     99,
							NicSelector: sriovv1.SriovNetworkNicSelector{
								PfNames: []string{intf.Name},
							},
							DeviceType:   "netdevice",
							NeedVhostNet: true,
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
						Name:      vhostnetwork,
						Namespace: operatorNamespace,
					},
					Spec: sriovv1.SriovNetworkSpec{
						ResourceName:     resourceName,
						IPAM:             `{"type":"host-local","subnet":"10.10.10.0/24","rangeStart":"10.10.10.171","rangeEnd":"10.10.10.181","routes":[{"dst":"0.0.0.0/0"}],"gateway":"10.10.10.1"}`,
						NetworkNamespace: namespaces.Test,
					}}

				// We need this to be able to run the connectivity checks on Mellanox cards
				if intf.DeviceID == "1015" {
					sriovNetwork.Spec.SpoofChk = off
				}

				err = clients.Create(context.Background(), sriovNetwork)

				Expect(err).ToNot(HaveOccurred())

				Eventually(func() error {
					netAttDef := &netattdefv1.NetworkAttachmentDefinition{}
					return clients.Get(context.Background(), runtimeclient.ObjectKey{Name: vhostnetwork, Namespace: namespaces.Test}, netAttDef)
				}, (10+snoTimeoutMultiplier*110)*time.Second, 1*time.Second).ShouldNot(HaveOccurred())

			})

			It("Should have the vhost-net device inside the container", func() {
				By("creating a pod")
				podObj := createCustomTestPod(node, []string{vhostnetwork}, false, []corev1.Capability{"NET_ADMIN", "NET_RAW"})
				ips, err := network.GetSriovNicIPs(podObj, "net1")
				Expect(err).ToNot(HaveOccurred())
				Expect(ips).NotTo(BeNil(), "No sriov network interface found.")
				Expect(len(ips)).Should(Equal(1))

				By("checking the /dev/vhost device exist inside the container")
				output, errOutput, err := pod.ExecCommand(clients, podObj, "ls", "/dev/vhost-net")
				Expect(err).ToNot(HaveOccurred())
				Expect(errOutput).To(Equal(""))
				Expect(output).ToNot(ContainSubstring("cannot access"))

				By("checking the /dev/vhost device exist inside the container")
				output, errOutput, err = pod.ExecCommand(clients, podObj, "ls", "/dev/net/tun")
				Expect(err).ToNot(HaveOccurred())
				Expect(errOutput).To(Equal(""))
				Expect(output).ToNot(ContainSubstring("cannot access"))

				By("creating a tap device inside the container")
				output, errOutput, err = pod.ExecCommand(clients, podObj, "ip", "tuntap", "add", "tap23", "mode", "tap", "multi_queue")
				Expect(err).ToNot(HaveOccurred())
				Expect(errOutput).To(Equal(""))
				Expect(output).ToNot(ContainSubstring("No such file"))

				By("checking the tap device was created inside the container")
				output, errOutput, err = pod.ExecCommand(clients, podObj, "ip", "link", "show", "tap23")
				Expect(err).ToNot(HaveOccurred())
				Expect(errOutput).To(Equal(""))
				Expect(output).To(ContainSubstring("tap23: <BROADCAST,MULTICAST> mtu 1500"))

				err = clients.Delete(context.Background(), podObj)
				Expect(err).ToNot(HaveOccurred())
			})
		})

		Context("ExternallyManaged Validation", func() {
			numVfs := 5
			var node string
			var nic sriovv1.InterfaceExt
			externallyManage := func(policy *sriovv1.SriovNetworkNodePolicy) {
				policy.Spec.ExternallyManaged = true
			}

			execute.BeforeAll(func() {
				node, nic = sriovInfos.FindOneVfioSriovDevice()
			})

			BeforeEach(func() {
				if node == "" {
					Skip("not suitable device found for the test")
				}

				By("Using device " + nic.Name + " on node " + node)
			})

			It("Should not allow to create a policy if there are no vfs configured", func() {
				resourceName := "test"
				_, err := network.CreateSriovPolicy(clients, "test-policy-", operatorNamespace, nic.Name, node, numVfs, resourceName, "netdevice", externallyManage)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("is higher than the virtual functions allocated for the PF externally"))
			})

			It("Should create a policy if the number of requested vfs is equal", func() {
				resourceName := "testexternally" //nolint:goconst
				By("allocating the 5 virtual functions to the selected device")
				_, errOutput, err := runCommandOnConfigDaemon(node, "/bin/bash", "-c", fmt.Sprintf("echo 5 > /host/sys/class/net/%s/device/sriov_numvfs", nic.Name))
				Expect(err).ToNot(HaveOccurred())
				Expect(errOutput).To(Equal(""))

				By("creating the policy that will use the 5 virtual functions we create manually on the system")
				Eventually(func() error {
					_, err := network.CreateSriovPolicy(clients, "test-policy-", operatorNamespace, nic.Name, node, numVfs, resourceName, "netdevice", externallyManage)
					return err
				}, 1*time.Minute, time.Second).ShouldNot(HaveOccurred())

				Eventually(func() int64 {
					testedNode, err := clients.CoreV1Interface.Nodes().Get(context.Background(), node, metav1.GetOptions{})
					Expect(err).ToNot(HaveOccurred())
					resNum := testedNode.Status.Allocatable[corev1.ResourceName("openshift.io/"+resourceName)]
					allocatable, _ := resNum.AsInt64()
					return allocatable
				}, 2*time.Minute, time.Second).Should(Equal(int64(numVfs)))

				By("cleaning the manual sriov created")
				_, errOutput, err = runCommandOnConfigDaemon(node, "/bin/bash", "-c", fmt.Sprintf("echo 0 > /host/sys/class/net/%s/device/sriov_numvfs", nic.Name))
				Expect(err).ToNot(HaveOccurred())
				Expect(errOutput).To(Equal(""))
			})

			It("Should create a policy if the number of requested vfs is equal and not delete them when the policy is removed", func() {
				resourceName := "testexternally"
				var sriovPolicy *sriovv1.SriovNetworkNodePolicy
				By("allocating the 5 virtual functions to the selected device")
				_, errOutput, err := runCommandOnConfigDaemon(node, "/bin/bash", "-c", fmt.Sprintf("echo 5 > /host/sys/class/net/%s/device/sriov_numvfs", nic.Name))
				Expect(err).ToNot(HaveOccurred())
				Expect(errOutput).To(Equal(""))

				By("creating the policy that will use the 5 virtual functions we create manually on the system")
				Eventually(func() error {
					sriovPolicy, err = network.CreateSriovPolicy(clients, "test-policy-", operatorNamespace, nic.Name, node, numVfs, resourceName, "netdevice", externallyManage)
					return err
				}, 2*time.Minute, time.Second).ShouldNot(HaveOccurred())

				Eventually(func() int64 {
					testedNode, err := clients.CoreV1Interface.Nodes().Get(context.Background(), node, metav1.GetOptions{})
					Expect(err).ToNot(HaveOccurred())
					resNum := testedNode.Status.Allocatable[corev1.ResourceName("openshift.io/"+resourceName)]
					allocatable, _ := resNum.AsInt64()
					return allocatable
				}, 3*time.Minute, time.Second).Should(Equal(int64(numVfs)))

				By("validate the pf info exist on host")
				output, _, err := runCommandOnConfigDaemon(node, "/bin/bash", "-c", "ls /host/etc/sriov-operator/pci/ | wc -l")
				Expect(err).ToNot(HaveOccurred())
				Expect(output).ToNot(Equal("1"))

				By("deleting the policy")
				err = clients.Delete(context.Background(), sriovPolicy, &runtimeclient.DeleteOptions{})
				Expect(err).ToNot(HaveOccurred())
				WaitForSRIOVStable()

				Eventually(func() int64 {
					testedNode, err := clients.CoreV1Interface.Nodes().Get(context.Background(), node, metav1.GetOptions{})
					Expect(err).ToNot(HaveOccurred())
					resNum := testedNode.Status.Allocatable[corev1.ResourceName("openshift.io/"+resourceName)]
					allocatable, _ := resNum.AsInt64()
					return allocatable
				}, 2*time.Minute, time.Second).Should(Equal(int64(0)))

				By("checking the virtual functions are still on the host")
				output, errOutput, err = runCommandOnConfigDaemon(node, "/bin/bash", "-c", fmt.Sprintf("cat /host/sys/class/net/%s/device/sriov_numvfs", nic.Name))
				Expect(err).ToNot(HaveOccurred())
				Expect(errOutput).To(Equal(""))
				Expect(output).To(ContainSubstring("5"))

				By("validate the pf info doesn't exist on the host anymore")
				output, _, err = runCommandOnConfigDaemon(node, "/bin/bash", "-c", "ls /host/etc/sriov-operator/pci/ | wc -l")
				Expect(err).ToNot(HaveOccurred())
				Expect(output).ToNot(Equal("0"))

				By("cleaning the manual sriov created")
				_, errOutput, err = runCommandOnConfigDaemon(node, "/bin/bash", "-c", fmt.Sprintf("echo 0 > /host/sys/class/net/%s/device/sriov_numvfs", nic.Name))
				Expect(err).ToNot(HaveOccurred())
				Expect(errOutput).To(Equal(""))
			})

			It("should reset the virtual functions if externallyManaged is false", func() {
				resourceName := "testexternally" //nolint:goconst

				var sriovPolicy *sriovv1.SriovNetworkNodePolicy
				By("creating the policy for 5 virtual functions")
				sriovPolicy, err := network.CreateSriovPolicy(clients, "test-policy-", operatorNamespace, nic.Name, node, numVfs, resourceName, "netdevice")
				Expect(err).ToNot(HaveOccurred())

				Eventually(func() int64 {
					testedNode, err := clients.CoreV1Interface.Nodes().Get(context.Background(), node, metav1.GetOptions{})
					Expect(err).ToNot(HaveOccurred())
					resNum := testedNode.Status.Allocatable[corev1.ResourceName("openshift.io/"+resourceName)]
					allocatable, _ := resNum.AsInt64()
					return allocatable
				}, 3*time.Minute, time.Second).Should(Equal(int64(numVfs)))

				By("deleting the policy")
				err = clients.Delete(context.Background(), sriovPolicy, &runtimeclient.DeleteOptions{})
				Expect(err).ToNot(HaveOccurred())
				WaitForSRIOVStable()

				Eventually(func() int64 {
					testedNode, err := clients.CoreV1Interface.Nodes().Get(context.Background(), node, metav1.GetOptions{})
					Expect(err).ToNot(HaveOccurred())
					resNum := testedNode.Status.Allocatable[corev1.ResourceName("openshift.io/"+resourceName)]
					allocatable, _ := resNum.AsInt64()
					return allocatable
				}, 3*time.Minute, time.Second).Should(Equal(int64(0)))

				By("checking the virtual functions don't exist anymore on the system")
				output, errOutput, err := runCommandOnConfigDaemon(node, "/bin/bash", "-c", fmt.Sprintf("cat /host/sys/class/net/%s/device/sriov_numvfs", nic.Name))
				Expect(err).ToNot(HaveOccurred())
				Expect(errOutput).To(Equal(""))
				Expect(output).To(ContainSubstring("0"))
			})
		})
	})
})

func getDriver(ethtoolstdout string) string {
	lines := strings.Split(ethtoolstdout, "\n")
	Expect(len(lines)).To(BeNumerically(">", 0))
	for _, line := range lines {
		if strings.HasPrefix(line, "driver:") {
			return strings.TrimSpace(line[len("driver:"):])
		}
	}
	Fail("Could not find device driver")
	return ""
}

func changeNodeInterfaceState(testNode string, ifcName string, enable bool) {
	state := "up"
	if !enable {
		state = "down"
	}
	podDefinition := pod.RedefineAsPrivileged(
		pod.RedefineWithRestartPolicy(
			pod.RedefineWithCommand(
				pod.DefineWithHostNetwork(testNode),
				[]string{"ip", "link", "set", "dev", ifcName, state}, []string{},
			),
			corev1.RestartPolicyNever,
		),
	)
	createdPod, err := clients.Pods(namespaces.Test).Create(context.Background(), podDefinition, metav1.CreateOptions{})
	Expect(err).ToNot(HaveOccurred())
	Eventually(func() corev1.PodPhase {
		runningPod, err := clients.Pods(namespaces.Test).Get(context.Background(), createdPod.Name, metav1.GetOptions{})
		Expect(err).ToNot(HaveOccurred())
		return runningPod.Status.Phase
	}, 3*time.Minute, 1*time.Second).Should(Equal(corev1.PodSucceeded))
}

func discoverResourceForMainSriov(nodes *cluster.EnabledNodes) (*sriovv1.InterfaceExt, string, string, bool) {
	for _, node := range nodes.Nodes {
		nodeDevices, err := nodes.FindSriovDevices(node)
		Expect(err).ToNot(HaveOccurred())
		if len(nodeDevices) == 0 {
			continue
		}

		executorPod := createCustomTestPod(node, []string{}, true, nil)
		mainDevice := findMainSriovDevice(executorPod, nodeDevices)
		if mainDevice == nil {
			return nil, "", "", false
		}

		nodeState, err := clients.SriovNetworkNodeStates(operatorNamespace).Get(context.Background(), node, metav1.GetOptions{})
		Expect(err).ToNot(HaveOccurred())
		resourceName, ok := findSuitableResourceForMain(mainDevice, nodeState)
		if ok {
			fmt.Printf("Using %s with resource %s for node %s", mainDevice.Name, resourceName, node)
			return mainDevice, resourceName, node, true
		}
	}
	return nil, "", "", false
}

func findSuitableResourceForMain(mainIntf *sriovv1.InterfaceExt, networkState *sriovv1.SriovNetworkNodeState) (string, bool) {
	for _, intf := range networkState.Spec.Interfaces {
		if intf.Name == mainIntf.Name {
			for _, vfGroup := range intf.VfGroups {
				// we want to make sure that selecting the resource name means
				// selecting the primary interface
				if resourceOnlyForInterface(networkState, intf.Name, vfGroup.ResourceName) {
					return vfGroup.ResourceName, true
				}
			}
		}
	}

	return "", false
}

func resourceOnlyForInterface(networkState *sriovv1.SriovNetworkNodeState, resourceName, interfaceName string) bool {
	for _, intf := range networkState.Spec.Interfaces {
		if intf.Name != interfaceName {
			for _, vfGroup := range intf.VfGroups {
				if vfGroup.ResourceName == resourceName {
					return false
				}
			}
		}
	}
	return true
}

func findMainSriovDevice(executorPod *corev1.Pod, sriovDevices []*sriovv1.InterfaceExt) *sriovv1.InterfaceExt {
	stdout, _, err := pod.ExecCommand(clients, executorPod, "ip", "route")
	Expect(err).ToNot(HaveOccurred())
	routes := strings.Split(stdout, "\n")

	for _, device := range sriovDevices {
		if isDefaultRouteInterface(device.Name, routes) {
			fmt.Println("Chosen ", device.Name, " as it is the default gw")
			return device
		}
		stdout, _, err = pod.ExecCommand(clients, executorPod, "ip", "link", "show", device.Name)
		Expect(err).ToNot(HaveOccurred())
		Expect(len(stdout)).Should(Not(Equal(0)), "Unable to query link state")
		if strings.Contains(stdout, "state DOWN") {
			continue // The interface is not active
		}
		if strings.Contains(stdout, "master ovs-system") {
			fmt.Println("Chosen ", device.Name, " as it is used by ovs")
			return device
		}
	}
	return nil
}

func findUnusedSriovDevices(testNode string, sriovDevices []*sriovv1.InterfaceExt) ([]*sriovv1.InterfaceExt, error) {
	createdPod := createCustomTestPod(testNode, []string{}, true, nil)
	filteredDevices := []*sriovv1.InterfaceExt{}
	stdout, _, err := pod.ExecCommand(clients, createdPod, "ip", "route")
	Expect(err).ToNot(HaveOccurred())
	routes := strings.Split(stdout, "\n")

	for _, device := range sriovDevices {
		if isDefaultRouteInterface(device.Name, routes) {
			continue
		}
		stdout, _, err = pod.ExecCommand(clients, createdPod, "ip", "link", "show", device.Name)
		Expect(err).ToNot(HaveOccurred())
		Expect(len(stdout)).Should(Not(Equal(0)), "Unable to query link state")
		if strings.Contains(stdout, "master ovs-system") {
			continue // The interface is not active
		}

		filteredDevices = append(filteredDevices, device)
	}
	if len(filteredDevices) == 0 {
		return nil, fmt.Errorf("unused sriov devices not found")
	}
	return filteredDevices, nil
}

func isDefaultRouteInterface(intfName string, routes []string) bool {
	for _, route := range routes {
		if strings.HasPrefix(route, "default") && strings.Contains(route, "dev "+intfName) {
			return true
		}
	}
	return false
}

// podVFIndexInHost retrieves the vf index on the host network namespace related to the given
// interface that was passed to the pod, using the name in the pod's namespace.
func podVFIndexInHost(hostNetPod *corev1.Pod, targetPod *corev1.Pod, interfaceName string) (int, error) {
	var stdout, stderr string
	var err error
	Eventually(func() error {
		stdout, stderr, err = pod.ExecCommand(clients, targetPod, "readlink", "-f", fmt.Sprintf("/sys/class/net/%s", interfaceName))
		if stdout == "" {
			return fmt.Errorf("empty response from pod exec")
		}

		if err != nil {
			return fmt.Errorf("failed to find %s interface address %v - %s", interfaceName, err, stderr)
		}

		return nil
	}, 1*time.Minute, 5*time.Second).ShouldNot(HaveOccurred())

	// sysfs address looks like: /sys/devices/pci0000:17/0000:17:02.0/0000:19:00.5/net/net1
	pathSegments := strings.Split(stdout, "/")
	segNum := len(pathSegments)

	if !strings.HasPrefix(pathSegments[segNum-1], "net1") { // not checking equality because of rubbish like new line
		return 0, fmt.Errorf("expecting net1 as last segment of %s", stdout)
	}

	podVFAddr := pathSegments[segNum-3] // 0000:19:00.5

	devicePath := strings.Join(pathSegments[0:segNum-2], "/") // /sys/devices/pci0000:17/0000:17:02.0/0000:19:00.5/
	findAllSiblingVfs := strings.Split(fmt.Sprintf("ls -gG %s/physfn/", devicePath), " ")

	res := 0
	Eventually(func() error {
		stdout, stderr, err = pod.ExecCommand(clients, hostNetPod, findAllSiblingVfs...)
		if stdout == "" {
			return fmt.Errorf("empty response from pod exec")
		}

		if err != nil {
			return fmt.Errorf("failed to find %s siblings %v - %s", devicePath, err, stderr)
		}

		// lines of the format of
		// lrwxrwxrwx. 1        0 Mar  6 15:15 virtfn3 -> ../0000:19:00.5
		scanner := bufio.NewScanner(strings.NewReader(stdout))
		for scanner.Scan() {
			line := scanner.Text()
			if !strings.Contains(line, "virtfn") {
				continue
			}

			columns := strings.Fields(line)

			if len(columns) != 9 {
				return fmt.Errorf("expecting 9 columns in %s, found %d", line, len(columns))
			}

			vfAddr := strings.TrimPrefix(columns[8], "../") // ../0000:19:00.2

			if vfAddr == podVFAddr { // Found!
				vfName := columns[6] // virtfn0
				vfNumber := strings.TrimPrefix(vfName, "virtfn")
				res, err = strconv.Atoi(vfNumber)
				if err != nil {
					return fmt.Errorf("could not get vf number from vfname %s", vfName)
				}
				return nil
			}
		}
		return fmt.Errorf("could not find %s index in %s", podVFAddr, stdout)
	}, 1*time.Minute, 5*time.Second).ShouldNot(HaveOccurred())

	return res, nil
}

func daemonsScheduledOnNodes(selector string) bool {
	return isDaemonsetScheduledOnNodes(selector, "app=sriov-network-config-daemon")
}

func isDaemonsetScheduledOnNodes(nodeSelector, daemonsetLabelSelector string) bool {
	nn, err := clients.CoreV1Interface.Nodes().List(context.Background(), metav1.ListOptions{
		LabelSelector: nodeSelector,
	})
	Expect(err).ToNot(HaveOccurred())
	nodes := nn.Items

	daemons, err := clients.Pods(operatorNamespace).List(context.Background(), metav1.ListOptions{LabelSelector: daemonsetLabelSelector})
	Expect(err).ToNot(HaveOccurred())
	for _, d := range daemons.Items {
		foundNode := false
		for i, n := range nodes {
			if d.Spec.NodeName == n.Name {
				foundNode = true
				// Removing the element from the list as we want to make sure
				// the daemons are running on different nodes
				nodes = append(nodes[:i], nodes[i+1:]...)
				break
			}
		}
		if !foundNode {
			return false
		}
	}
	return true
}

func createSriovPolicy(sriovDevice string, testNode string, numVfs int, resourceName string) {
	_, err := network.CreateSriovPolicy(clients, "test-policy-", operatorNamespace, sriovDevice, testNode, numVfs, resourceName, "netdevice")
	Expect(err).ToNot(HaveOccurred())
	WaitForSRIOVStable()

	Eventually(func() int64 {
		testedNode, err := clients.CoreV1Interface.Nodes().Get(context.Background(), testNode, metav1.GetOptions{})
		Expect(err).ToNot(HaveOccurred())
		resNum := testedNode.Status.Allocatable[corev1.ResourceName("openshift.io/"+resourceName)]
		capacity, _ := resNum.AsInt64()
		return capacity
	}, 10*time.Minute, time.Second).Should(Equal(int64(numVfs)))
}

func createTestPod(node string, networks []string) *corev1.Pod {
	return createCustomTestPod(node, networks, false, nil)
}

func createCustomTestPod(node string, networks []string, hostNetwork bool, podCapabilities []corev1.Capability) *corev1.Pod {
	var podDefinition *corev1.Pod
	if hostNetwork {
		podDefinition = pod.DefineWithHostNetwork(node)
	} else {
		podDefinition = pod.RedefineWithNodeSelector(
			pod.DefineWithNetworks(networks),
			node,
		)
	}

	if len(podCapabilities) != 0 {
		if podDefinition.Spec.Containers[0].SecurityContext == nil {
			podDefinition.Spec.Containers[0].SecurityContext = &corev1.SecurityContext{}
		}
		if podDefinition.Spec.Containers[0].SecurityContext.Capabilities == nil {
			podDefinition.Spec.Containers[0].SecurityContext.Capabilities = &corev1.Capabilities{}
		}
		podDefinition.Spec.Containers[0].SecurityContext.Capabilities.Add = podCapabilities
	}

	createdPod, err := clients.Pods(namespaces.Test).Create(context.Background(), podDefinition, metav1.CreateOptions{})
	Expect(err).ToNot(HaveOccurred())

	Eventually(func() corev1.PodPhase {
		runningPod, err := clients.Pods(namespaces.Test).Get(context.Background(), createdPod.Name, metav1.GetOptions{})
		Expect(err).ToNot(HaveOccurred())
		return runningPod.Status.Phase
	}, 5*time.Minute, 1*time.Second).Should(Equal(corev1.PodRunning))
	podObj, err := clients.Pods(namespaces.Test).Get(context.Background(), createdPod.Name, metav1.GetOptions{})
	Expect(err).ToNot(HaveOccurred())
	return podObj
}

func pingPod(ip string, nodeSelector string, sriovNetworkAttachment string) {
	ipProtocolVersion := "6"
	if len(strings.Split(ip, ".")) == 4 {
		ipProtocolVersion = "4"
	}
	podDefinition := pod.RedefineWithNodeSelector(
		pod.RedefineWithCapabilities(
			pod.RedefineWithRestartPolicy(
				pod.RedefineWithCommand(
					pod.DefineWithNetworks([]string{sriovNetworkAttachment}),
					[]string{"sh", "-c", fmt.Sprintf("ping -%s -c 3 %s", ipProtocolVersion, ip)}, []string{},
				),
				corev1.RestartPolicyNever,
			),
			[]corev1.Capability{"NET_RAW"},
		),
		nodeSelector,
	)

	createdPod, err := clients.Pods(namespaces.Test).Create(context.Background(), podDefinition, metav1.CreateOptions{})
	Expect(err).ToNot(HaveOccurred())

	Eventually(func() corev1.PodPhase {
		runningPod, err := clients.Pods(namespaces.Test).Get(context.Background(), createdPod.Name, metav1.GetOptions{})
		Expect(err).ToNot(HaveOccurred())
		return runningPod.Status.Phase
	}, 3*time.Minute, 1*time.Second).Should(Equal(corev1.PodSucceeded))
}

func WaitForSRIOVStable() {
	// This used to be to check for sriov not to be stable first,
	// then stable. The issue is that if no configuration is applied, then
	// the status won't never go to not stable and the test will fail.
	// TODO: find a better way to handle this scenario

	time.Sleep((10 + snoTimeoutMultiplier*20) * time.Second)

	fmt.Println("Waiting for the sriov state to stable")
	Eventually(func() bool {
		// ignoring the error. This can eventually be executed against a single node cluster,
		// and if a reconfiguration triggers a reboot then the api calls will return an error
		res, _ := cluster.SriovStable(operatorNamespace, clients)
		return res
	}, waitingTime, 1*time.Second).Should(BeTrue())
	fmt.Println("Sriov state is stable")

	Eventually(func() bool {
		isClusterReady, err := cluster.IsClusterStable(clients)
		Expect(err).ToNot(HaveOccurred())
		return isClusterReady
	}, waitingTime, 1*time.Second).Should(BeTrue())
}

func findInterface(sriovInfos *cluster.EnabledNodes, node string) *sriovv1.InterfaceExt { // For the context of tests is better to use a Mellanox card
	// as they support all the virtual function flags
	// if we don't find a Mellanox card we fall back to any sriov
	// capability interface and skip the rate limit test.
	intf, err := sriovInfos.FindOneMellanoxSriovDevice(node)
	if err != nil {
		intf, err = sriovInfos.FindOneSriovDevice(node)
		Expect(err).ToNot(HaveOccurred())
	}

	return intf
}

func createVanillaNetworkPolicy(node string, sriovInfos *cluster.EnabledNodes, numVfs int, resourceName string) {
	intf := findInterface(sriovInfos, node)

	config := &sriovv1.SriovNetworkNodePolicy{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "test-policy",
			Namespace:    operatorNamespace,
		},

		Spec: sriovv1.SriovNetworkNodePolicySpec{
			NodeSelector: map[string]string{
				"kubernetes.io/hostname": node,
			},
			NumVfs:       numVfs,
			ResourceName: resourceName,
			Mtu:          1500,
			Priority:     99,
			NicSelector: sriovv1.SriovNetworkNicSelector{
				PfNames: []string{intf.Name},
			},
			DeviceType: "netdevice",
		},
	}
	Eventually(func() error {
		return clients.Create(context.Background(), config)
	}, 1*time.Minute, 5*time.Second).ShouldNot(HaveOccurred())

	Eventually(func() sriovv1.Interfaces {
		nodeState, err := clients.SriovNetworkNodeStates(operatorNamespace).Get(context.Background(), node, metav1.GetOptions{})
		Expect(err).ToNot(HaveOccurred())
		return nodeState.Spec.Interfaces
	}, 1*time.Minute, 1*time.Second).Should(ContainElement(MatchFields(
		IgnoreExtras,
		Fields{
			"Name":   Equal(intf.Name),
			"NumVfs": Equal(numVfs),
		})))
}

func runCommandOnConfigDaemon(nodeName string, command ...string) (string, string, error) {
	pods := &corev1.PodList{}
	label, err := labels.Parse("app=sriov-network-config-daemon")
	Expect(err).ToNot(HaveOccurred())
	field, err := fields.ParseSelector(fmt.Sprintf("spec.nodeName=%s", nodeName))
	Expect(err).ToNot(HaveOccurred())
	err = clients.List(context.Background(), pods, &runtimeclient.ListOptions{Namespace: operatorNamespace, LabelSelector: label, FieldSelector: field})
	Expect(err).ToNot(HaveOccurred())
	Expect(len(pods.Items)).To(Equal(1))

	output, errOutput, err := pod.ExecCommand(clients, &pods.Items[0], command...)
	return output, errOutput, err
}

func defaultFilterPolicy(policy sriovv1.SriovNetworkNodePolicy) bool {
	return policy.Spec.DeviceType == "netdevice"
}

func setSriovOperatorSpecFlag(flagName string, flagValue bool) func() {
	cfg := sriovv1.SriovOperatorConfig{}
	err := clients.Get(context.TODO(), runtimeclient.ObjectKey{
		Name:      "default",
		Namespace: operatorNamespace,
	}, &cfg)

	ret := func() {}

	Expect(err).ToNot(HaveOccurred())
	if flagName == operatorNetworkInjectorFlag && cfg.Spec.EnableInjector != flagValue {
		cfg.Spec.EnableInjector = flagValue
		err = clients.Update(context.TODO(), &cfg)
		Expect(err).ToNot(HaveOccurred())
		Expect(cfg.Spec.EnableInjector).To(Equal(flagValue))
		ret = func() {
			setSriovOperatorSpecFlag(flagName, !flagValue)
		}
	}

	if flagName == operatorWebhookFlag && cfg.Spec.EnableOperatorWebhook != flagValue {
		cfg.Spec.EnableOperatorWebhook = flagValue
		clients.Update(context.TODO(), &cfg)
		Expect(err).ToNot(HaveOccurred())
		Expect(cfg.Spec.EnableOperatorWebhook).To(Equal(flagValue))
		ret = func() {
			setSriovOperatorSpecFlag(flagName, !flagValue)
		}
	}

	if flagValue {
		Eventually(func(g Gomega) {
			podsList, err := clients.Pods(operatorNamespace).List(context.Background(), metav1.ListOptions{
				LabelSelector: fmt.Sprintf("app=%s", flagName)})
			g.Expect(err).ToNot(HaveOccurred())

			g.Expect(len(podsList.Items)).To(BeNumerically(">", 0))

			for _, pod := range podsList.Items {
				g.Expect(pod.Status.Phase).To(Equal(corev1.PodRunning))
			}
		}, 1*time.Minute, 10*time.Second).WithOffset(1).Should(Succeed())
	}

	return ret
}

func setOperatorConfigLogLevel(level int) {
	instantBeforeSettingLogLevel := time.Now()

	Eventually(func(g Gomega) {
		cfg := sriovv1.SriovOperatorConfig{}
		err := clients.Get(context.TODO(), runtimeclient.ObjectKey{
			Name:      "default",
			Namespace: operatorNamespace,
		}, &cfg)
		g.Expect(err).ToNot(HaveOccurred())

		if cfg.Spec.LogLevel == level {
			return
		}

		cfg.Spec.LogLevel = level

		err = clients.Update(context.TODO(), &cfg)
		g.Expect(err).ToNot(HaveOccurred())

		logs := getOperatorLogs(instantBeforeSettingLogLevel)
		g.Expect(logs).To(
			ContainElement(
				ContainSubstring(fmt.Sprintf(`"new-level": %d`, level)),
			),
		)
	}, 1*time.Minute, 5*time.Second).Should(Succeed())
}

func getOperatorConfigLogLevel() int {
	cfg := sriovv1.SriovOperatorConfig{}
	err := clients.Get(context.TODO(), runtimeclient.ObjectKey{
		Name:      "default",
		Namespace: operatorNamespace,
	}, &cfg)
	Expect(err).ToNot(HaveOccurred())

	return cfg.Spec.LogLevel
}

func isFeatureFlagEnabled(featureFlag string) bool {
	cfg := sriovv1.SriovOperatorConfig{}
	err := clients.Get(context.TODO(), runtimeclient.ObjectKey{
		Name:      "default",
		Namespace: operatorNamespace,
	}, &cfg)
	Expect(err).ToNot(HaveOccurred())

	ret, ok := cfg.Spec.FeatureGates[featureFlag]
	return ok && ret
}

func setFeatureFlag(featureFlag string, value bool) {
	Eventually(func(g Gomega) {
		cfg := sriovv1.SriovOperatorConfig{}
		err := clients.Get(context.TODO(), runtimeclient.ObjectKey{
			Name:      "default",
			Namespace: operatorNamespace,
		}, &cfg)
		g.Expect(err).ToNot(HaveOccurred())

		if cfg.Spec.FeatureGates == nil {
			cfg.Spec.FeatureGates = make(map[string]bool)
		}

		previousValue, ok := cfg.Spec.FeatureGates[featureFlag]
		if ok && previousValue == value {
			return
		}

		cfg.Spec.FeatureGates[featureFlag] = value

		err = clients.Update(context.TODO(), &cfg)
		g.Expect(err).ToNot(HaveOccurred())
	}, 1*time.Minute, 5*time.Second).Should(Succeed())
}

func getOperatorPod() corev1.Pod {
	podList, err := clients.Pods(operatorNamespace).List(context.Background(), metav1.ListOptions{
		LabelSelector: "name=sriov-network-operator",
	})
	ExpectWithOffset(1, err).ToNot(HaveOccurred())
	ExpectWithOffset(1, podList.Items).ToNot(HaveLen(0), "At least one operator pod expected")
	return podList.Items[0]
}

func getOperatorLogs(since time.Time) []string {
	pod := getOperatorPod()
	logStart := metav1.NewTime(since)
	rawLogs, err := clients.Pods(pod.Namespace).
		GetLogs(pod.Name, &corev1.PodLogOptions{
			Container: pod.Spec.Containers[0].Name,
			SinceTime: &logStart,
		}).
		DoRaw(context.Background())
	ExpectWithOffset(1, err).ToNot(HaveOccurred())

	return strings.Split(string(rawLogs), "\n")
}

func deleteOperatorPod() {
	pod := getOperatorPod()

	err := clients.Pods(operatorNamespace).Delete(context.Background(), pod.Name, metav1.DeleteOptions{})
	ExpectWithOffset(1, err).ToNot(HaveOccurred())
}

func assertObjectIsNotFound(name string, obj runtimeclient.Object) {
	Eventually(func() bool {
		err := clients.Get(context.Background(), runtimeclient.ObjectKey{Name: name, Namespace: operatorNamespace}, obj)
		return err != nil && k8serrors.IsNotFound(err)
	}, 2*time.Minute, 10*time.Second).Should(BeTrue())
}

func assertDevicePluginConfigurationContains(node, configuration string) {
	Eventually(func(g Gomega) map[string]string {
		cfg := corev1.ConfigMap{}
		err := clients.Get(context.Background(), runtimeclient.ObjectKey{
			Name:      "device-plugin-config",
			Namespace: operatorNamespace,
		}, &cfg)
		g.Expect(err).ToNot(HaveOccurred())

		return cfg.Data
	}, 30*time.Second, 2*time.Second).Should(
		HaveKeyWithValue(node, ContainSubstring(configuration)),
	)
}

func getMultusPodLogs(nodeName string, since time.Time) []string {
	podList, err := clients.Pods("").List(context.Background(), metav1.ListOptions{
		LabelSelector: "app=multus",
		FieldSelector: "spec.nodeName=" + nodeName,
	})
	ExpectWithOffset(1, err).ToNot(HaveOccurred())
	ExpectWithOffset(1, podList.Items).To(HaveLen(1), "One multus pod expected")

	multusPod := podList.Items[0]
	logStart := metav1.NewTime(since)
	rawLogs, err := clients.Pods(multusPod.Namespace).
		GetLogs(multusPod.Name, &corev1.PodLogOptions{
			Container: multusPod.Spec.Containers[0].Name,
			SinceTime: &logStart,
		}).
		DoRaw(context.Background())
	ExpectWithOffset(1, err).ToNot(HaveOccurred())

	return strings.Split(string(rawLogs), "\n")
}

func waitForNetAttachDef(name, namespace string) {
	Eventually(func() error {
		netAttDef := &netattdefv1.NetworkAttachmentDefinition{}
		return clients.Get(context.Background(), runtimeclient.ObjectKey{Name: name, Namespace: namespace}, netAttDef)
	}, (10+snoTimeoutMultiplier*110)*time.Second, 1*time.Second).ShouldNot(HaveOccurred())
}
