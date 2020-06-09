package tests

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"time"

	netattdefv1 "github.com/openshift/sriov-network-operator/pkg/apis/k8s/v1"
	sriovv1 "github.com/openshift/sriov-network-operator/pkg/apis/sriovnetwork/v1"
	v1core "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	runtimeclient "sigs.k8s.io/controller-runtime/pkg/client"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	. "github.com/onsi/gomega/gstruct"

	"github.com/openshift/sriov-network-operator/test/util/cluster"
	"github.com/openshift/sriov-network-operator/test/util/execute"
	"github.com/openshift/sriov-network-operator/test/util/namespaces"
	"github.com/openshift/sriov-network-operator/test/util/network"
	"github.com/openshift/sriov-network-operator/test/util/pod"
)

var waitingTime time.Duration = 20 * time.Minute

const (
	onlinePingableAddress = "8.8.8.8"
)

func init() {
	waitingEnv := os.Getenv("SRIOV_WAITING_TIME")
	newTime, err := strconv.Atoi(waitingEnv)
	if err == nil && newTime != 0 {
		waitingTime = time.Duration(newTime) * time.Minute
	}
}

var _ = Describe("Bond-CNI", func() {
	var sriovInfos *cluster.EnabledNodes

	execute.BeforeAll(func() {
		Expect(clients).NotTo(BeNil(), "Client misconfigured, check the $KUBECONFIG env variable")

		err := namespaces.Create(namespaces.Test, clients)
		Expect(err).ToNot(HaveOccurred())

		waitForSRIOVStable()
		sriovInfos, err = cluster.DiscoverSriov(clients, operatorNamespace)
		Expect(err).ToNot(HaveOccurred())
	})

	BeforeEach(func() {
		err := namespaces.Clean(operatorNamespace, namespaces.Test, clients)
		Expect(err).ToNot(HaveOccurred())

		waitForSRIOVStable()
	})

	Describe("Bond CNI Configuration", func() {
		var testNode string
		resourceName := "testBondResource"
		networkName := "testNetwork"
		numVfs := 5

		BeforeEach(func() {
			testNode = sriovInfos.Nodes[0]
			intf, err := sriovInfos.FindOneSriovDevice(testNode)

			Expect(err).ToNot(HaveOccurred(), "No SR-IOV supported devices detected")
			Expect(intf.TotalVfs).To(BeNumerically(">=", numVfs), fmt.Sprintf("Cluster has less than %d VFs available.", numVfs))

			_, err = network.CreateSriovPolicy(clients, "testBondPolicy", operatorNamespace, intf.Name, testNode, numVfs, resourceName)
			Expect(err).ToNot(HaveOccurred(), "Error to create SriovNetworkNodePolicy")

			Eventually(func() sriovv1.Interfaces {
				nodeState, err := clients.SriovNetworkNodeStates(operatorNamespace).Get(testNode, metav1.GetOptions{})
				Expect(err).ToNot(HaveOccurred())
				return nodeState.Spec.Interfaces
			}, 1*time.Minute, 1*time.Second).Should(ContainElement(MatchFields(
				IgnoreExtras,
				Fields{
					"Name":   Equal(intf.Name),
					"NumVfs": Equal(numVfs),
					"VfGroups": ContainElement(
						MatchFields(
							IgnoreExtras,
							Fields{
								"ResourceName": Equal(resourceName),
								"DeviceType":   Equal("netdevice"),
								"VfRange":      Equal("0-4"),
							})),
				})), "Error SriovNetworkNodeState doesn't contain required elements")

			waitForSRIOVStable()

			Eventually(func() int64 {
				testedNode, err := clients.Nodes().Get(testNode, metav1.GetOptions{})
				Expect(err).ToNot(HaveOccurred())
				resNum, _ := testedNode.Status.Capacity["openshift.io/testbondresource"]
				capacity, _ := resNum.AsInt64()
				return capacity
			}, 5*time.Minute, time.Second).Should(Equal(int64(numVfs)), fmt.Sprintf("Error discovered less than %d VFs", numVfs))

			err = network.CreateSriovNetwork(clients, intf, networkName, namespaces.Test, operatorNamespace, resourceName, "")
			Expect(err).ToNot(HaveOccurred(), "Error to create SriovNetwork")

			Eventually(func() error {
				netAttDef := &netattdefv1.NetworkAttachmentDefinition{}
				return clients.Get(context.Background(), runtimeclient.ObjectKey{Name: networkName, Namespace: namespaces.Test}, netAttDef)
			}, 30*time.Second, 1*time.Second).ShouldNot(HaveOccurred(), "Error to detect NetworkAttachmentDefinition")

			netAttDef := &netattdefv1.NetworkAttachmentDefinition{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-bondnetwork",
					Namespace: namespaces.Test,
				},
				Spec: netattdefv1.NetworkAttachmentDefinitionSpec{
					Config: `{
								"type": "bond",
								"cniVersion": "0.3.1",
								"name": "bond-net1",
								"ifname": "bond0",
								"mode": "active-backup",
								"failOverMac": 1,
								"linksInContainer": true,
								"miimon": "100",
								"links": [
									{"name": "net1"},
									{"name": "net2"}
								],
								"ipam": {
									"type": "host-local",
									"subnet": "10.56.217.0/24",
									"routes": [{
									"dst": "0.0.0.0/0"
									}],
									"gateway": "10.56.217.1"
								}
							}`,
				},
			}

			err = clients.Create(context.Background(), netAttDef)
			Expect(err).ToNot(HaveOccurred())
		})

		It("Should start a pod with a bond interface", func() {
			testPodA := pod.RedefineWithNodeSelector(
				pod.DefineWithNetworks([]string{networkName, networkName, "test-bondnetwork"}),
				testNode,
			)

			runningPodA, err := clients.Pods(testPodA.Namespace).Create(testPodA)
			Expect(err).ToNot(HaveOccurred(), fmt.Sprintf("Error to create pod %s", testPodA.Name))

			By("Checking that first Pod is in Running state")
			Eventually(func() v1core.PodPhase {
				runningPodA, err = clients.Pods(namespaces.Test).Get(runningPodA.Name, metav1.GetOptions{})
				Expect(err).ToNot(HaveOccurred())
				return runningPodA.Status.Phase
			}, 3*time.Minute, time.Second).Should(Equal(v1core.PodRunning))

			stdout, stderr, err := pod.ExecCommand(clients, runningPodA, "ip", "link", "show")
			Expect(err).ToNot(HaveOccurred())
			Expect(stderr).To(Equal(""))

			fmt.Println(stdout)
		})

		It("Should have active backup capacity", func() {
			// Create a pod with bond interface
			testPodA := pod.RedefineWithNodeSelector(
				pod.DefineWithNetworks([]string{networkName, networkName, "test-bondnetwork"}),
				testNode,
			)

			runningPodA, err := clients.Pods(testPodA.Namespace).Create(testPodA)
			Expect(err).ToNot(HaveOccurred(), fmt.Sprintf("Error to create pod %s", testPodA.Name))

			By("Checking that first Pod is in Running state")
			Eventually(func() v1core.PodPhase {
				runningPodA, err = clients.Pods(namespaces.Test).Get(runningPodA.Name, metav1.GetOptions{})
				Expect(err).ToNot(HaveOccurred())
				return runningPodA.Status.Phase
			}, 3*time.Minute, time.Second).Should(Equal(v1core.PodRunning))

			By("Turning net1 off")
			_, stderr, err := pod.ExecCommand(clients, runningPodA, "ifdown", "net1")
			Expect(err).ToNot(HaveOccurred())
			Expect(stderr).To(Equal(""))

			By("Checking the pod communication")
			checkPodCommunication(runningPodA, 4, onlinePingableAddress)

			By("Turning net1 on after communication check")
			_, stderr, err = pod.ExecCommand(clients, runningPodA, "ifup", "net1")
			Expect(err).ToNot(HaveOccurred())
			Expect(stderr).To(Equal(""))

			By("Turning net2 off")
			_, stderr, err = pod.ExecCommand(clients, runningPodA, "ifdown", "net2")
			Expect(err).ToNot(HaveOccurred())
			Expect(stderr).To(Equal(""))

			By("Checking the pod communication")
			checkPodCommunication(runningPodA, 4, onlinePingableAddress)

			By("Turning net1 on after communication check")
			_, stderr, err = pod.ExecCommand(clients, runningPodA, "ifup", "net2")
			Expect(err).ToNot(HaveOccurred())
			Expect(stderr).To(Equal(""))
		})
	})
})

func checkPodCommunication(runningPod *v1core.Pod, numberOfPings int, pingAddress string) {
	_, stderr, err := pod.ExecCommand(clients, runningPod, "ping", "-c", strconv.Itoa(numberOfPings), pingAddress)
	Expect(err).ToNot(HaveOccurred())
	Expect(stderr).To(Equal(""))
	stdin, stderr, err := pod.ExecCommand(clients, runningPod, "echo", "$?")
	Expect(err).ToNot(HaveOccurred())
	Expect(stderr).To(Equal(""))
	Expect(stdin).To(Equal("0"))
}

func waitForSRIOVStable() {
	// This used to be to check for sriov not to be stable first,
	// then stable. The issue is that if no configuration is applied, then
	// the status won't never go to not stable and the test will fail.
	// TODO: find a better way to handle this scenario

	time.Sleep(5 * time.Second)
	Eventually(func() bool {
		res, err := cluster.SriovStable(operatorNamespace, clients)
		Expect(err).ToNot(HaveOccurred())
		return res
	}, waitingTime, 1*time.Second).Should(BeTrue())

	Eventually(func() bool {
		isClusterReady, err := cluster.IsClusterStable(clients)
		Expect(err).ToNot(HaveOccurred())
		return isClusterReady
	}, waitingTime, 1*time.Second).Should(BeTrue())
}
