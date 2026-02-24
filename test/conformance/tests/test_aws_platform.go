package tests

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	. "github.com/onsi/gomega/gstruct"

	netattdefv1 "github.com/k8snetworkplumbingwg/network-attachment-definition-client/pkg/apis/k8s.cni.cncf.io/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	runtimeclient "sigs.k8s.io/controller-runtime/pkg/client"

	sriovv1 "github.com/k8snetworkplumbingwg/sriov-network-operator/api/v1"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/consts"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/test/util/discovery"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/test/util/namespaces"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/test/util/network"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/test/util/pod"
)

var _ = Describe("[sriov] aws platform", Ordered, func() {
	BeforeAll(func() {
		if platformType != consts.AWS {
			Skip("AWS platform is not supported on non-AWS platforms")
		}
		Expect(len(sriovInfos.Nodes)).ToNot(BeZero())
		Expect(len(sriovInfos.States)).ToNot(BeZero())
		Expect(sriovInfos.States[sriovInfos.Nodes[0]].Status.Interfaces).ToNot(BeEmpty())
		Expect(sriovInfos.States[sriovInfos.Nodes[0]].Status.Interfaces[0].NetFilter).To(ContainSubstring("aws/NetworkID:"))
	})

	AfterEach(func() {
		err := namespaces.Clean(operatorNamespace, namespaces.Test, clients, discovery.Enabled())
		Expect(err).ToNot(HaveOccurred())
		WaitForSRIOVStable()
	})

	Describe("Generic SriovNetworkNodePolicy", func() {
		It("should configure vf in netdevice mode using host-device CNI", func() {
			node := sriovInfos.Nodes[0]
			nic, err := sriovInfos.FindOneSriovDevice(node)
			Expect(err).ToNot(HaveOccurred())
			Expect(nic).ToNot(BeNil())

			By("creating a netdevice node policy")
			resourceName := "testnetdevice"
			_, err = network.CreateSriovPolicyWithNetfilter(clients, "test-policy-", operatorNamespace, nic.NetFilter, node, 1, resourceName, "netdevice")
			Expect(err).ToNot(HaveOccurred())

			By("waiting for the node state to be updated")
			Eventually(func() sriovv1.Interfaces {
				nodeState := &sriovv1.SriovNetworkNodeState{}
				err := clients.Get(context.Background(), runtimeclient.ObjectKey{Namespace: operatorNamespace, Name: node}, nodeState)
				Expect(err).ToNot(HaveOccurred())
				return nodeState.Spec.Interfaces
			}, 1*time.Minute, 1*time.Second).Should(ContainElement(MatchFields(
				IgnoreExtras,
				Fields{
					"Name":   Equal(nic.Name),
					"NumVfs": Equal(1),
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
			}, 10*time.Minute, time.Second).Should(Equal(int64(1)))

			By("validate the pf info exist on host")
			output, _, err := runCommandOnConfigDaemon(node, "/bin/bash", "-c", "ls /host/etc/sriov-operator/pci/ | wc -l")
			Expect(err).ToNot(HaveOccurred())
			Expect(strings.TrimSpace(output)).To(Equal("1"))

			By("creating a network attachment definition for host-device CNI")
			nad := GetNetworkAttachmentDefinition(resourceName, namespaces.Test)
			err = clients.Create(context.Background(), nad)
			Expect(err).ToNot(HaveOccurred())

			podDefinition := pod.GetDefinition()
			podDefinition.Annotations = map[string]string{"k8s.v1.cni.cncf.io/networks": resourceName}
			podDefinition.Spec.Containers[0].Resources = corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceName("openshift.io/" + resourceName): resource.MustParse("1"),
				},
				Limits: corev1.ResourceList{
					corev1.ResourceName("openshift.io/" + resourceName): resource.MustParse("1"),
				},
			}
			runningPod, err := clients.Pods(namespaces.Test).Create(context.Background(), podDefinition, metav1.CreateOptions{})
			Expect(err).ToNot(HaveOccurred())

			By("waiting for the pod to be running")
			runningPod = waitForPodRunning(runningPod)

			By("checking the netdevice exist inside the pod")
			output, errOutput, err := pod.ExecCommand(clients, runningPod, "sh", "-c", "ip link show net1 | grep link | wc -l")
			Expect(err).ToNot(HaveOccurred())
			Expect(errOutput).To(Equal(""))
			Expect(strings.TrimSpace(output)).To(Equal("1"))
		})

		It("should configure vf in vfio-pci mode", func() {
			node := sriovInfos.Nodes[0]
			nic, err := sriovInfos.FindOneSriovDevice(node)
			Expect(err).ToNot(HaveOccurred())
			Expect(nic).ToNot(BeNil())

			By("creating a vfio-pci node policy")
			resourceName := "testvfio"
			vfioPolicy, err := network.CreateSriovPolicyWithNetfilter(clients, "test-policy-", operatorNamespace, nic.NetFilter, node, 1, resourceName, "vfio-pci")
			Expect(err).ToNot(HaveOccurred())

			By("waiting for the node state to be updated")
			Eventually(func() sriovv1.Interfaces {
				nodeState := &sriovv1.SriovNetworkNodeState{}
				err := clients.Get(context.Background(), runtimeclient.ObjectKey{Namespace: operatorNamespace, Name: node}, nodeState)
				Expect(err).ToNot(HaveOccurred())
				return nodeState.Spec.Interfaces
			}, 1*time.Minute, 1*time.Second).Should(ContainElement(MatchFields(
				IgnoreExtras,
				Fields{
					"Name":   Equal(nic.Name),
					"NumVfs": Equal(1),
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
			}, 10*time.Minute, time.Second).Should(Equal(int64(1)))

			By("validate the pf info exist on host")
			output, _, err := runCommandOnConfigDaemon(node, "/bin/bash", "-c", "ls /host/etc/sriov-operator/pci/ | wc -l")
			Expect(err).ToNot(HaveOccurred())
			Expect(strings.TrimSpace(output)).To(Equal("1"))

			podDefinition := pod.GetDefinition()
			podDefinition.Spec.Containers[0].Resources = corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceName("openshift.io/" + resourceName): resource.MustParse("1"),
				},
				Limits: corev1.ResourceList{
					corev1.ResourceName("openshift.io/" + resourceName): resource.MustParse("1"),
				},
			}

			firstPod, err := clients.Pods(namespaces.Test).Create(context.Background(), podDefinition, metav1.CreateOptions{})
			Expect(err).ToNot(HaveOccurred())

			firstPod = waitForPodRunning(firstPod)

			By("Checking the vfio device exist inside the pod")
			output, errOutput, err := pod.ExecCommand(clients, firstPod, "sh", "-c", "ls /dev/vfio/ | wc -l")
			Expect(err).ToNot(HaveOccurred())
			Expect(errOutput).To(Equal(""))
			Expect(strings.TrimSpace(output)).To(Equal("2"))

			By("get latest sriov network node state")
			nodeState := &sriovv1.SriovNetworkNodeState{}
			err = clients.Get(context.Background(), runtimeclient.ObjectKey{Namespace: operatorNamespace, Name: node}, nodeState)
			Expect(err).ToNot(HaveOccurred())
			savedStatus := nodeState.Status

			By("delete config daemon pod on the node")
			configDaemonPod := getConfigDaemonPod(node)
			oldPodName := configDaemonPod.Name
			err = clients.Pods(operatorNamespace).Delete(context.Background(), configDaemonPod.Name, metav1.DeleteOptions{
				GracePeriodSeconds: ptr.To(int64(0)),
			})
			Expect(err).ToNot(HaveOccurred())

			By("waiting for the new config daemon pod to be running")
			Eventually(func() bool {
				newPod := getConfigDaemonPod(node)
				// Make sure we got a different pod (new one)
				if newPod.Name == oldPodName {
					return false
				}
				return newPod.Status.Phase == corev1.PodRunning
			}, 3*time.Minute, 5*time.Second).Should(BeTrue(), "new config daemon pod should be running")

			By("consistently checking that the node state status remains unchanged for 1 minute")
			Consistently(func() bool {
				currentNodeState := &sriovv1.SriovNetworkNodeState{}
				err := clients.Get(context.Background(), runtimeclient.ObjectKey{Namespace: operatorNamespace, Name: node}, currentNodeState)
				Expect(err).ToNot(HaveOccurred())
				return reflect.DeepEqual(savedStatus, currentNodeState.Status)
			}, 1*time.Minute, 5*time.Second).Should(BeTrue(), "node state status should remain unchanged after config daemon restart")

			By("deleting the policy")
			err = clients.Delete(context.Background(), vfioPolicy, &runtimeclient.DeleteOptions{})
			Expect(err).ToNot(HaveOccurred())
			WaitForSRIOVStable()

			Eventually(func() int64 {
				testedNode, err := clients.CoreV1Interface.Nodes().Get(context.Background(), node, metav1.GetOptions{})
				Expect(err).ToNot(HaveOccurred())
				resNum := testedNode.Status.Allocatable[corev1.ResourceName("openshift.io/"+resourceName)]
				allocatable, _ := resNum.AsInt64()
				return allocatable
			}, 2*time.Minute, time.Second).Should(Equal(int64(0)))

			By("validate the pf info doesn't exist on the host anymore")
			output, _, err = runCommandOnConfigDaemon(node, "/bin/bash", "-c", "ls /host/etc/sriov-operator/pci/ | wc -l")
			Expect(err).ToNot(HaveOccurred())
			Expect(strings.TrimSpace(output)).To(Equal("0"))

			By("checking the driver was reset to default")
			nodeState = &sriovv1.SriovNetworkNodeState{}
			err = clients.Get(context.Background(), runtimeclient.ObjectKey{Namespace: operatorNamespace, Name: node}, nodeState)
			Expect(err).ToNot(HaveOccurred())
			Expect(nodeState.Status.Interfaces).To(ContainElement(MatchFields(
				IgnoreExtras,
				Fields{
					"Name":   Equal(nic.Name),
					"Driver": Equal("ena"),
				})))
		})
	})
})

func GetNetworkAttachmentDefinition(resourceName, namespace string) *netattdefv1.NetworkAttachmentDefinition {
	return &netattdefv1.NetworkAttachmentDefinition{
		ObjectMeta: metav1.ObjectMeta{
			Name:      resourceName,
			Namespace: namespace,
			Annotations: map[string]string{
				"k8s.v1.cni.cncf.io/resourceName": fmt.Sprintf("openshift.io/%s", resourceName),
			},
		},
		Spec: netattdefv1.NetworkAttachmentDefinitionSpec{
			Config: fmt.Sprintf(`{"cniVersion": "1.0.0", "name": "%s", "type": "host-device","capabilities": {"deviceID": true}}`, resourceName),
		},
	}
}
