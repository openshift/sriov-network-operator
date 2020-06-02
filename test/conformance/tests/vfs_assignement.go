package tests

import (
	"context"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	. "github.com/onsi/gomega/gstruct"

	netattdefv1 "github.com/openshift/sriov-network-operator/pkg/apis/k8s/v1"
	sriovv1 "github.com/openshift/sriov-network-operator/pkg/apis/sriovnetwork/v1"
	"github.com/openshift/sriov-network-operator/test/util/cluster"
	"github.com/openshift/sriov-network-operator/test/util/execute"
	"github.com/openshift/sriov-network-operator/test/util/namespaces"
	"github.com/openshift/sriov-network-operator/test/util/network"
	"github.com/openshift/sriov-network-operator/test/util/pod"
	v1core "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/pointer"
	runtimeclient "sigs.k8s.io/controller-runtime/pkg/client"
)

var _ = Describe("[sriov] pod", func() {
	var sriovInfos *cluster.EnabledNodes
	execute.BeforeAll(func() {
		Expect(clients).NotTo(BeNil(), "Client misconfigured, check the $KUBECONFIG env variable")
		err := namespaces.Create(namespaces.Test, clients)
		Expect(err).ToNot(HaveOccurred())
		waitForSRIOVStable()
		sriovInfos, err = cluster.DiscoverSriov(clients, operatorNamespace)
		Expect(err).ToNot(HaveOccurred())
		err = namespaces.Clean(operatorNamespace, namespaces.Test, clients)
		Expect(err).ToNot(HaveOccurred())

		waitForSRIOVStable()
	})

	Describe("Configuration", func() {
		var testNode string
		resourceName := "testresource"
		networkName := "test-network"
		numVfs := 5

		BeforeEach(func() {
			testNode = sriovInfos.Nodes[0]
			intf, err := sriovInfos.FindOneSriovDevice(testNode)
			Expect(err).ToNot(HaveOccurred(), "No SR-IOV supported devices detected")
			Expect(intf.TotalVfs).To(BeNumerically(">=", numVfs), fmt.Sprintf("Cluster has less than %d VFs available.", numVfs))
			_, err = network.CreateSriovPolicy(clients, "test-vfreleasedpolicy", operatorNamespace, intf.Name, testNode, numVfs, resourceName)
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
				resNum, _ := testedNode.Status.Capacity["openshift.io/testresource"]
				capacity, _ := resNum.AsInt64()
				return capacity
			}, 5*time.Minute, time.Second).Should(Equal(int64(numVfs)), fmt.Sprintf("Error discovered less than %d VFs", numVfs))

			ipam := `{ "type": "host-local","subnet": "10.10.10.0/24","rangeStart": 
					"10.10.10.171","rangeEnd": "10.10.10.181","routes": [{ "dst": "0.0.0.0/0" }],"gateway": "10.10.10.1"}`
			err = network.CreateSriovNetwork(clients, intf, networkName, namespaces.Test, operatorNamespace, resourceName, ipam)
			Expect(err).ToNot(HaveOccurred(), "Error to create SriovNetwork")

			Eventually(func() error {
				netAttDef := &netattdefv1.NetworkAttachmentDefinition{}
				return clients.Get(context.TODO(), runtimeclient.ObjectKey{Name: networkName, Namespace: namespaces.Test}, netAttDef)
			}, 30*time.Second, 1*time.Second).ShouldNot(HaveOccurred(), "Error to detect NetworkAttachmentDefinition")
		})

		Context("Virtual Functions", func() {
			// 21396
			It("should release the VFs once the pod deleted and same VFs can be used by the new created pods", func() {
				By("Create first Pod which consumes all available VFs")

				testPodA := pod.RedefineWithNodeSelector(
					pod.DefineWithNetworks([]string{networkName, networkName, networkName, networkName, networkName}),
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
				By("Create second Pod which consumes one more VF")

				testPodB := pod.RedefineWithNodeSelector(
					pod.DefineWithNetworks([]string{networkName}),
					testNode,
				)
				runningPodB, err := clients.Pods(testPodB.Namespace).Create(testPodB)
				Expect(err).ToNot(HaveOccurred(), fmt.Sprintf("Error to create pod %s", testPodB.Name))
				By("Checking second that pod is in Pending state")
				Eventually(func() v1core.PodPhase {
					runningPodB, err = clients.Pods(namespaces.Test).Get(runningPodB.Name, metav1.GetOptions{})
					Expect(err).ToNot(HaveOccurred())
					return runningPodB.Status.Phase
				}, 3*time.Minute, time.Second).Should(Equal(v1core.PodPending))

				By("Checking that relevant error event was originated")
				Eventually(func() string {
					events, err := clients.Events(namespaces.Test).List(metav1.ListOptions{})
					Expect(err).ToNot(HaveOccurred())

					for _, val := range events.Items {
						if val.InvolvedObject.Name == runningPodB.Name {
							return val.Message
						}
					}
					return ""
				}, 2*time.Minute, 10*time.Second).Should(ContainSubstring("Insufficient openshift.io/%s", resourceName),
					"Error to detect Required Event")
				By("Delete first pod and release all VFs")
				err = clients.Pods(namespaces.Test).Delete(runningPodA.Name, &metav1.DeleteOptions{
					GracePeriodSeconds: pointer.Int64Ptr(0),
				})
				Expect(err).ToNot(HaveOccurred(), fmt.Sprintf("Error to delete pod %s", runningPodA.Name))
				By("Checking that second pod is able to use released VF")
				Eventually(func() v1core.PodPhase {
					runningPodB, err = clients.Pods(namespaces.Test).Get(runningPodB.Name, metav1.GetOptions{})
					Expect(err).ToNot(HaveOccurred())
					return runningPodB.Status.Phase
				}, 3*time.Minute, time.Second).Should(Equal(v1core.PodRunning))
			})
		})
	})
})
