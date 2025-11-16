package drain_test

import (
	"context"
	"fmt"
	"os"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"go.uber.org/mock/gomock"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/pointer"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	sriovnetworkv1 "github.com/k8snetworkplumbingwg/sriov-network-operator/api/v1"
	constants "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/consts"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/drain"
	snolog "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/log"
	orchestratorMock "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/orchestrator/mock"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/vars"
)

var (
	cancel       context.CancelFunc
	ctx          context.Context
	drn          drain.DrainInterface
	t            FullGinkgoTInterface
	mockCtrl     *gomock.Controller
	orchestrator *orchestratorMock.MockInterface
)

var _ = Describe("Drainer", Ordered, func() {
	BeforeAll(func() {
		Expect(k8sClient.DeleteAllOf(context.Background(), &sriovnetworkv1.SriovOperatorConfig{}, client.InNamespace(testNamespace))).ToNot(HaveOccurred())
		soc := &sriovnetworkv1.SriovOperatorConfig{ObjectMeta: metav1.ObjectMeta{
			Name:      constants.DefaultConfigName,
			Namespace: testNamespace,
		},
			Spec: sriovnetworkv1.SriovOperatorConfigSpec{
				LogLevel: 2,
			},
		}
		err := k8sClient.Create(context.Background(), soc)
		Expect(err).ToNot(HaveOccurred())

		snolog.SetLogLevel(2)
		// Check if the environment variable CLUSTER_TYPE is set
		if clusterType, ok := os.LookupEnv("CLUSTER_TYPE"); ok && constants.ClusterType(clusterType) == constants.ClusterTypeOpenshift {
			vars.ClusterType = constants.ClusterTypeOpenshift
		} else {
			vars.ClusterType = constants.ClusterTypeKubernetes
		}
	})

	BeforeEach(func() {
		ctx, cancel = context.WithCancel(context.Background())
		ctx = context.WithValue(ctx, "logger", log.FromContext(ctx))

		t = GinkgoT()
		mockCtrl = gomock.NewController(t)
		orchestrator = orchestratorMock.NewMockInterface(mockCtrl)

		orchestrator.EXPECT().ClusterType().DoAndReturn(func() constants.ClusterType {
			if vars.ClusterType == constants.ClusterTypeOpenshift {
				return constants.ClusterTypeOpenshift
			}
			return constants.ClusterTypeKubernetes
		}).AnyTimes()

		// new drainer
		var err error
		drn, err = drain.NewDrainer(orchestrator)
		Expect(err).ToNot(HaveOccurred())
	})

	AfterEach(func() {
		Expect(k8sClient.DeleteAllOf(context.Background(), &sriovnetworkv1.SriovNetworkNodeState{}, client.InNamespace(testNamespace))).ToNot(HaveOccurred())
		Expect(k8sClient.DeleteAllOf(context.Background(), &corev1.Pod{}, client.InNamespace(testNamespace), &client.DeleteAllOfOptions{DeleteOptions: client.DeleteOptions{GracePeriodSeconds: pointer.Int64(0)}})).ToNot(HaveOccurred())
		Expect(k8sClient.DeleteAllOf(context.Background(), &corev1.Node{}, &client.DeleteAllOfOptions{DeleteOptions: client.DeleteOptions{GracePeriodSeconds: pointer.Int64(0)}})).ToNot(HaveOccurred())

		By("Shutdown controller manager")
		cancel()
	})

	AfterAll(func() {
		Expect(k8sClient.DeleteAllOf(context.Background(), &sriovnetworkv1.SriovOperatorConfig{}, client.InNamespace(testNamespace))).ToNot(HaveOccurred())
	})

	Context("DrainNode", func() {
		It("should failed if the platform is not able to drain", func() {
			n, _ := createNode("node0")
			orchestrator.EXPECT().BeforeDrainNode(ctx, n).Return(false, fmt.Errorf("failed"))

			completed, err := drn.DrainNode(ctx, n, false, false)
			Expect(err).To(HaveOccurred())
			Expect(completed).To(BeFalse())
		})

		It("should return not completed base on OpenshiftBeforeDrainNode call", func() {
			n, _ := createNode("node0")
			orchestrator.EXPECT().BeforeDrainNode(ctx, n).Return(false, nil)

			completed, err := drn.DrainNode(ctx, n, false, false)
			Expect(err).ToNot(HaveOccurred())
			Expect(completed).To(BeFalse())
		})

		It("should return error if not able to cordon the node", func() {
			n, _ := createNode("node0")
			nCopy := n.DeepCopy()
			nCopy.Name = "node1"
			originalDrainTimeOut := drain.DrainTimeOut
			drain.DrainTimeOut = 3 * time.Second
			defer func() {
				drain.DrainTimeOut = originalDrainTimeOut
			}()

			orchestrator.EXPECT().BeforeDrainNode(ctx, nCopy).Return(true, nil)

			_, err := drn.DrainNode(ctx, nCopy, false, false)
			Expect(err).To(HaveOccurred())
		})

		It("should return error if not able to drain the node", func() {
			n, _ := createNode("node0")
			orchestrator.EXPECT().BeforeDrainNode(ctx, n).Return(true, nil)
			createPodWithFinalizerOnNode(ctx, "test-node-0", "node0")
			originalDrainTimeOut := drain.DrainTimeOut
			drain.DrainTimeOut = 3 * time.Second
			defer func() {
				drain.DrainTimeOut = originalDrainTimeOut
			}()

			_, err := drn.DrainNode(ctx, n, true, false)
			Expect(err).To(HaveOccurred())
		})

		It("should remove pods with sriov devices only if a full drain is not requested", func() {
			n, _ := createNode("node1")
			resources := map[corev1.ResourceName]resource.Quantity{corev1.ResourceName(fmt.Sprintf("%s/test", vars.ResourcePrefix)): resource.MustParse("1")}
			n.Status.Allocatable = resources
			n.Status.Capacity = resources
			err := k8sClient.Status().Update(ctx, n)
			Expect(err).ToNot(HaveOccurred())
			orchestrator.EXPECT().BeforeDrainNode(ctx, n).Return(true, nil)
			createPodOnNode(ctx, "regular-pod", "node1")
			createPodWithSriovDeviceOnNode(ctx, "sriov-pod", "node1")

			go func() {
				Eventually(func(g Gomega) {
					podObj := &corev1.Pod{}
					err = k8sClient.Get(ctx, client.ObjectKey{Name: "sriov-pod", Namespace: testNamespace}, podObj)
					g.Expect(err).ToNot(HaveOccurred())
					g.Expect(podObj.DeletionTimestamp).ToNot(BeNil())
					err = k8sClient.Delete(ctx, podObj, &client.DeleteOptions{GracePeriodSeconds: pointer.Int64(0)})
					g.Expect(err).ToNot(HaveOccurred())
				}, 2*time.Minute, time.Second).Should(Succeed())
			}()

			_, err = drn.DrainNode(ctx, n, false, false)
			Expect(err).ToNot(HaveOccurred())
			pod := &corev1.Pod{}
			err = k8sClient.Get(ctx, client.ObjectKey{Name: "regular-pod", Namespace: testNamespace}, pod)
			Expect(err).ToNot(HaveOccurred())
			err = k8sClient.Get(ctx, client.ObjectKey{Name: "sriov-pod", Namespace: testNamespace}, pod)
			Expect(err).To(HaveOccurred())
			Expect(errors.IsNotFound(err)).To(BeTrue())

		})
	})

	Context("CompleteDrain", func() {
		It("should return error if the un cordon failed", func() {
			n, _ := createNode("node0")
			nCopy := n.DeepCopy()
			nCopy.Name = "node1"
			nCopy.Spec.Unschedulable = true

			completed, err := drn.CompleteDrainNode(ctx, nCopy)
			Expect(err).To(HaveOccurred())
			Expect(completed).To(BeFalse())
		})

		It("should return error if OpenshiftAfterCompleteDrainNode failed", func() {
			n, _ := createNode("node0")
			n.Spec.Unschedulable = true
			orchestrator.EXPECT().AfterCompleteDrainNode(ctx, n).Return(false, fmt.Errorf("test"))

			completed, err := drn.CompleteDrainNode(ctx, n)
			Expect(err).To(HaveOccurred())
			Expect(completed).To(BeFalse())
		})

		It("should return completed", func() {
			n, _ := createNode("node0")
			n.Spec.Unschedulable = true
			orchestrator.EXPECT().AfterCompleteDrainNode(ctx, n).Return(true, nil)

			completed, err := drn.CompleteDrainNode(ctx, n)
			Expect(err).ToNot(HaveOccurred())
			Expect(completed).To(BeTrue())
		})
	})
})

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
			Namespace: testNamespace,
			Annotations: map[string]string{
				constants.NodeStateDrainAnnotation:        constants.DrainIdle,
				constants.NodeStateDrainAnnotationCurrent: constants.DrainIdle,
			},
		},
	}

	Expect(k8sClient.Create(ctx, &node)).ToNot(HaveOccurred())
	Expect(k8sClient.Create(ctx, &nodeState)).ToNot(HaveOccurred())
	vars.NodeName = nodeName

	return &node, &nodeState
}

func createPodOnNode(ctx context.Context, podName, nodeName string) {
	pod := corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: podName, Namespace: testNamespace},
		Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "test", Image: "test", Command: []string{"test"}}},
			NodeName: nodeName, TerminationGracePeriodSeconds: pointer.Int64(1)}}
	Expect(k8sClient.Create(ctx, &pod)).ToNot(HaveOccurred())
}

func createPodWithFinalizerOnNode(ctx context.Context, podName, nodeName string) {
	pod := corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: podName, Namespace: testNamespace, Finalizers: []string{"sriov-operator.io/test"}},
		Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "test", Image: "test", Command: []string{"test"}}},
			NodeName: nodeName, TerminationGracePeriodSeconds: pointer.Int64(1)}}
	Expect(k8sClient.Create(ctx, &pod)).ToNot(HaveOccurred())
}

func createPodWithSriovDeviceOnNode(ctx context.Context, podName, nodeName string) {
	resources := map[corev1.ResourceName]resource.Quantity{corev1.ResourceName(fmt.Sprintf("%s/test", vars.ResourcePrefix)): resource.MustParse("1")}
	pod := corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: podName, Namespace: testNamespace},
		Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "test", Image: "test", Command: []string{"test"},
			Resources: corev1.ResourceRequirements{Requests: resources, Limits: resources}}},
			NodeName: nodeName, TerminationGracePeriodSeconds: pointer.Int64(1)}}
	Expect(k8sClient.Create(ctx, &pod)).ToNot(HaveOccurred())
}
