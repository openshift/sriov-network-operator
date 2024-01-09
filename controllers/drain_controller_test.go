package controllers

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	sriovnetworkv1 "github.com/k8snetworkplumbingwg/sriov-network-operator/api/v1"
	constants "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/consts"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/utils"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/vars"
)

var _ = Describe("Drain Controller", func() {
	BeforeEach(func() {
		Expect(k8sClient.DeleteAllOf(context.Background(), &corev1.Node{})).ToNot(HaveOccurred())
		Expect(k8sClient.DeleteAllOf(context.Background(), &sriovnetworkv1.SriovNetworkNodeState{}, client.InNamespace(vars.Namespace))).ToNot(HaveOccurred())

		poolConfig := &sriovnetworkv1.SriovNetworkPoolConfig{}
		poolConfig.SetNamespace(testNamespace)
		poolConfig.SetName("test-workers")
		err := k8sClient.Delete(context.Background(), poolConfig)
		if err != nil {
			Expect(errors.IsNotFound(err)).To(BeTrue())
		}
	})

	Context("when there is only one node", func() {

		It("should drain", func(ctx context.Context) {
			node, nodeState := createNode("node1")

			simulateDaemonSetAnnotation(node, constants.DrainRequired)

			expectNodeStateAnnotation(nodeState, constants.DrainComplete)
			expectNodeIsNotSchedulable(node)

			simulateDaemonSetAnnotation(node, constants.DrainIdle)

			expectNodeStateAnnotation(nodeState, constants.DrainIdle)
			expectNodeIsSchedulable(node)
		})
	})

	Context("when there are multiple nodes", func() {

		It("should drain nodes serially with default pool selector", func(ctx context.Context) {
			node1, nodeState1 := createNode("node1")
			node2, nodeState2 := createNode("node2")
			node3, nodeState3 := createNode("node3")

			// Two nodes require to drain at the same time
			simulateDaemonSetAnnotation(node1, constants.DrainRequired)
			simulateDaemonSetAnnotation(node2, constants.DrainRequired)

			// Only the first node drains
			expectNodeStateAnnotation(nodeState1, constants.DrainComplete)
			expectNodeStateAnnotation(nodeState2, constants.DrainIdle)
			expectNodeStateAnnotation(nodeState3, constants.DrainIdle)
			expectNodeIsNotSchedulable(node1)
			expectNodeIsSchedulable(node2)
			expectNodeIsSchedulable(node3)

			simulateDaemonSetAnnotation(node1, constants.DrainIdle)

			expectNodeStateAnnotation(nodeState1, constants.DrainIdle)
			expectNodeIsSchedulable(node1)

			// Second node starts draining
			expectNodeStateAnnotation(nodeState1, constants.DrainIdle)
			expectNodeStateAnnotation(nodeState2, constants.DrainComplete)
			expectNodeStateAnnotation(nodeState3, constants.DrainIdle)
			expectNodeIsSchedulable(node1)
			expectNodeIsNotSchedulable(node2)
			expectNodeIsSchedulable(node3)

			simulateDaemonSetAnnotation(node2, constants.DrainIdle)

			expectNodeStateAnnotation(nodeState1, constants.DrainIdle)
			expectNodeStateAnnotation(nodeState2, constants.DrainIdle)
			expectNodeStateAnnotation(nodeState3, constants.DrainIdle)
			expectNodeIsSchedulable(node1)
			expectNodeIsSchedulable(node2)
			expectNodeIsSchedulable(node3)
		})

		It("should drain nodes in parallel with a custom pool selector", func(ctx context.Context) {
			node1, nodeState1 := createNode("node1")
			node2, nodeState2 := createNode("node2")
			node3, nodeState3 := createNode("node3")

			maxun := intstr.Parse("2")
			poolConfig := &sriovnetworkv1.SriovNetworkPoolConfig{}
			poolConfig.SetNamespace(testNamespace)
			poolConfig.SetName("test-workers")
			poolConfig.Spec = sriovnetworkv1.SriovNetworkPoolConfigSpec{MaxUnavailable: &maxun, NodeSelector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"test": "",
				},
			}}
			Expect(k8sClient.Create(context.TODO(), poolConfig)).Should(Succeed())

			// Two nodes require to drain at the same time
			simulateDaemonSetAnnotation(node1, constants.DrainRequired)
			simulateDaemonSetAnnotation(node2, constants.DrainRequired)

			// Only the first node drains
			expectNodeStateAnnotation(nodeState1, constants.DrainComplete)
			expectNodeStateAnnotation(nodeState2, constants.DrainComplete)
			expectNodeStateAnnotation(nodeState3, constants.DrainIdle)
			expectNodeIsNotSchedulable(node1)
			expectNodeIsNotSchedulable(node2)
			expectNodeIsSchedulable(node3)

			simulateDaemonSetAnnotation(node1, constants.DrainIdle)

			expectNodeStateAnnotation(nodeState1, constants.DrainIdle)
			expectNodeIsSchedulable(node1)

			// Second node starts draining
			expectNodeStateAnnotation(nodeState1, constants.DrainIdle)
			expectNodeStateAnnotation(nodeState2, constants.DrainComplete)
			expectNodeStateAnnotation(nodeState3, constants.DrainIdle)
			expectNodeIsSchedulable(node1)
			expectNodeIsNotSchedulable(node2)
			expectNodeIsSchedulable(node3)

			simulateDaemonSetAnnotation(node2, constants.DrainIdle)

			expectNodeStateAnnotation(nodeState1, constants.DrainIdle)
			expectNodeStateAnnotation(nodeState2, constants.DrainIdle)
			expectNodeStateAnnotation(nodeState3, constants.DrainIdle)
			expectNodeIsSchedulable(node1)
			expectNodeIsSchedulable(node2)
			expectNodeIsSchedulable(node3)
		})

		It("should drain nodes in parallel with a custom pool selector and honor MaxUnavailable", func(ctx context.Context) {
			node1, nodeState1 := createNode("node1")
			node2, nodeState2 := createNode("node2")
			node3, nodeState3 := createNode("node3")

			maxun := intstr.Parse("2")
			poolConfig := &sriovnetworkv1.SriovNetworkPoolConfig{}
			poolConfig.SetNamespace(testNamespace)
			poolConfig.SetName("test-workers")
			poolConfig.Spec = sriovnetworkv1.SriovNetworkPoolConfigSpec{MaxUnavailable: &maxun, NodeSelector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"test": "",
				},
			}}
			Expect(k8sClient.Create(context.TODO(), poolConfig)).Should(Succeed())

			// Two nodes require to drain at the same time
			simulateDaemonSetAnnotation(node1, constants.DrainRequired)
			simulateDaemonSetAnnotation(node2, constants.DrainRequired)
			simulateDaemonSetAnnotation(node3, constants.DrainRequired)

			expectNumberOfDrainingNodes(2, nodeState1, nodeState2, nodeState3)
			ExpectDrainCompleteNodesHaveIsNotSchedule(nodeState1, nodeState2, nodeState3)
		})

		It("should drain all nodes in parallel with a custom pool using nil in max unavailable", func(ctx context.Context) {
			node1, nodeState1 := createNode("node1")
			node2, nodeState2 := createNode("node2")
			node3, nodeState3 := createNode("node3")

			poolConfig := &sriovnetworkv1.SriovNetworkPoolConfig{}
			poolConfig.SetNamespace(testNamespace)
			poolConfig.SetName("test-workers")
			poolConfig.Spec = sriovnetworkv1.SriovNetworkPoolConfigSpec{MaxUnavailable: nil, NodeSelector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"test": "",
				},
			}}
			Expect(k8sClient.Create(context.TODO(), poolConfig)).Should(Succeed())

			// Two nodes require to drain at the same time
			simulateDaemonSetAnnotation(node1, constants.DrainRequired)
			simulateDaemonSetAnnotation(node2, constants.DrainRequired)
			simulateDaemonSetAnnotation(node3, constants.DrainRequired)

			// Only the first node drains
			expectNodeStateAnnotation(nodeState1, constants.DrainComplete)
			expectNodeStateAnnotation(nodeState2, constants.DrainComplete)
			expectNodeStateAnnotation(nodeState3, constants.DrainComplete)
			expectNodeIsNotSchedulable(node1)
			expectNodeIsNotSchedulable(node2)
			expectNodeIsNotSchedulable(node3)
		})
	})
})

func expectNodeStateAnnotation(nodeState *sriovnetworkv1.SriovNetworkNodeState, expectedAnnotationValue string) {
	EventuallyWithOffset(1, func(g Gomega) {
		g.Expect(k8sClient.Get(context.Background(), types.NamespacedName{Namespace: nodeState.Namespace, Name: nodeState.Name}, nodeState)).
			ToNot(HaveOccurred())

		g.Expect(utils.ObjectHasAnnotation(nodeState, constants.NodeStateDrainAnnotationCurrent, expectedAnnotationValue)).
			To(BeTrue(),
				"Node[%s] annotation[%s] == '%s'. Expected '%s'", nodeState.Name, constants.NodeDrainAnnotation, nodeState.GetLabels()[constants.NodeStateDrainAnnotationCurrent], expectedAnnotationValue)
	}, "20s", "1s").Should(Succeed())
}

func expectNumberOfDrainingNodes(numbOfDrain int, nodesState ...*sriovnetworkv1.SriovNetworkNodeState) {
	EventuallyWithOffset(1, func(g Gomega) {
		drainingNodes := 0
		for _, nodeState := range nodesState {
			g.Expect(k8sClient.Get(context.Background(), types.NamespacedName{Namespace: nodeState.Namespace, Name: nodeState.Name}, nodeState)).
				ToNot(HaveOccurred())

			if utils.ObjectHasAnnotation(nodeState, constants.NodeStateDrainAnnotationCurrent, constants.DrainComplete) {
				drainingNodes++
			}
		}

		g.Expect(drainingNodes).To(Equal(numbOfDrain))
	}, "20s", "1s").Should(Succeed())
}

func ExpectDrainCompleteNodesHaveIsNotSchedule(nodesState ...*sriovnetworkv1.SriovNetworkNodeState) {
	for _, nodeState := range nodesState {
		if utils.ObjectHasAnnotation(nodeState, constants.NodeStateDrainAnnotationCurrent, constants.DrainComplete) {
			node := &corev1.Node{}
			Expect(k8sClient.Get(context.Background(), types.NamespacedName{Name: nodeState.Name}, node)).
				ToNot(HaveOccurred())
			expectNodeIsNotSchedulable(node)
		}
	}
}

func expectNodeIsNotSchedulable(node *corev1.Node) {
	EventuallyWithOffset(1, func(g Gomega) {
		g.Expect(k8sClient.Get(context.Background(), types.NamespacedName{Name: node.Name}, node)).
			ToNot(HaveOccurred())

		g.Expect(node.Spec.Unschedulable).To(BeTrue())
	}, "20s", "1s").Should(Succeed())
}

func expectNodeIsSchedulable(node *corev1.Node) {
	EventuallyWithOffset(1, func(g Gomega) {
		g.Expect(k8sClient.Get(context.Background(), types.NamespacedName{Name: node.Name}, node)).
			ToNot(HaveOccurred())

		g.Expect(node.Spec.Unschedulable).To(BeFalse())
	}, "20s", "1s").Should(Succeed())
}

func simulateDaemonSetAnnotation(node *corev1.Node, drainAnnotationValue string) {
	ExpectWithOffset(1,
		utils.AnnotateObject(node, constants.NodeDrainAnnotation, drainAnnotationValue, k8sClient)).
		ToNot(HaveOccurred())
}

func createNode(nodeName string) (*corev1.Node, *sriovnetworkv1.SriovNetworkNodeState) {
	node := corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: nodeName,
			Annotations: map[string]string{
				constants.NodeDrainAnnotation:                     constants.DrainIdle,
				"machineconfiguration.openshift.io/desiredConfig": "worker-1",
			},
			Labels: map[string]string{
				"test": "",
			},
		},
	}

	nodeState := sriovnetworkv1.SriovNetworkNodeState{
		ObjectMeta: metav1.ObjectMeta{
			Name:      nodeName,
			Namespace: vars.Namespace,
			Labels: map[string]string{
				constants.NodeStateDrainAnnotationCurrent: constants.DrainIdle,
			},
		},
	}

	Expect(k8sClient.Create(ctx, &node)).ToNot(HaveOccurred())
	Expect(k8sClient.Create(ctx, &nodeState)).ToNot(HaveOccurred())

	return &node, &nodeState
}
