package drain

import (
	"context"
	stderrors "errors"
	"fmt"
	"os"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/go-logr/logr"
	"go.uber.org/mock/gomock"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	resourceapi "k8s.io/api/resource/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes/fake"
	clienttesting "k8s.io/client-go/testing"
	kubedrain "k8s.io/kubectl/pkg/drain"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	draconsts "github.com/k8snetworkplumbingwg/dra-driver-sriov/pkg/consts"

	sriovnetworkv1 "github.com/k8snetworkplumbingwg/sriov-network-operator/api/v1"
	constants "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/consts"
	snolog "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/log"
	orchestratorMock "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/orchestrator/mock"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/vars"
)

var (
	cancel           context.CancelFunc
	ctx              context.Context
	drn              DrainInterface
	t                FullGinkgoTInterface
	mockCtrl         *gomock.Controller
	mockOrchestrator *orchestratorMock.MockInterface
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
		ctx = context.WithValue(ctx, constants.LoggerContextKey, log.FromContext(ctx))

		t = GinkgoT()
		mockCtrl = gomock.NewController(t)
		mockOrchestrator = orchestratorMock.NewMockInterface(mockCtrl)

		mockOrchestrator.EXPECT().ClusterType().DoAndReturn(func() constants.ClusterType {
			if vars.ClusterType == constants.ClusterTypeOpenshift {
				return constants.ClusterTypeOpenshift
			}
			return constants.ClusterTypeKubernetes
		}).AnyTimes()

		// new drainer
		var err error
		drn, err = NewDrainer(mockOrchestrator)
		Expect(err).ToNot(HaveOccurred())
	})

	AfterEach(func() {
		Expect(k8sClient.DeleteAllOf(context.Background(), &sriovnetworkv1.SriovNetworkNodeState{}, client.InNamespace(testNamespace))).ToNot(HaveOccurred())
		Expect(k8sClient.DeleteAllOf(context.Background(), &corev1.Pod{}, client.InNamespace(testNamespace), &client.DeleteAllOfOptions{DeleteOptions: client.DeleteOptions{GracePeriodSeconds: ptr.To[int64](0)}})).ToNot(HaveOccurred())
		Expect(k8sClient.DeleteAllOf(context.Background(), &corev1.Node{}, &client.DeleteAllOfOptions{DeleteOptions: client.DeleteOptions{GracePeriodSeconds: ptr.To[int64](0)}})).ToNot(HaveOccurred())

		By("Shutdown controller manager")
		cancel()
	})

	AfterAll(func() {
		Expect(k8sClient.DeleteAllOf(context.Background(), &sriovnetworkv1.SriovOperatorConfig{}, client.InNamespace(testNamespace))).ToNot(HaveOccurred())
	})

	Context("DrainNode", func() {
		It("should failed if the platform is not able to drain", func() {
			n, _ := createNode("node0")
			mockOrchestrator.EXPECT().BeforeDrainNode(ctx, n).Return(false, fmt.Errorf("failed"))

			completed, err := drn.DrainNode(ctx, n, false, false, DrainErrorNoop)
			Expect(err).To(HaveOccurred())
			Expect(completed).To(BeFalse())
		})

		It("should call the error callback when BeforeDrainNode fails", func() {
			n, _ := createNode("node0")
			expectedErr := fmt.Errorf("orchestrator failed")
			mockOrchestrator.EXPECT().BeforeDrainNode(ctx, n).Return(false, expectedErr)

			var callbackCalled bool
			var callbackErr error
			onError := func(err error) {
				callbackCalled = true
				callbackErr = err
			}

			completed, err := drn.DrainNode(ctx, n, false, false, onError)
			Expect(err).To(HaveOccurred())
			Expect(completed).To(BeFalse())
			Expect(callbackCalled).To(BeTrue())
			Expect(stderrors.Is(callbackErr, expectedErr)).To(BeTrue())
			Expect(callbackErr.Error()).To(ContainSubstring("BeforeDrainNode"))
		})

		It("should call the error callback when cordon fails", func() {
			n, _ := createNode("node0")
			nCopy := n.DeepCopy()
			nCopy.Name = "non-existent-node"
			originalDrainTimeOut := DrainTimeOut
			DrainTimeOut = 3 * time.Second
			defer func() {
				DrainTimeOut = originalDrainTimeOut
			}()

			mockOrchestrator.EXPECT().BeforeDrainNode(ctx, nCopy).Return(true, nil)

			var callbackCalled bool
			onError := func(err error) {
				callbackCalled = true
			}

			_, err := drn.DrainNode(ctx, nCopy, false, false, onError)
			Expect(err).To(HaveOccurred())
			Expect(callbackCalled).To(BeTrue())
		})

		It("should return not completed base on OpenshiftBeforeDrainNode call", func() {
			n, _ := createNode("node0")
			mockOrchestrator.EXPECT().BeforeDrainNode(ctx, n).Return(false, nil)

			completed, err := drn.DrainNode(ctx, n, false, false, DrainErrorNoop)
			Expect(err).ToNot(HaveOccurred())
			Expect(completed).To(BeFalse())
		})

		It("should return error if not able to cordon the node", func() {
			n, _ := createNode("node0")
			nCopy := n.DeepCopy()
			nCopy.Name = "node1"
			originalDrainTimeOut := DrainTimeOut
			DrainTimeOut = 3 * time.Second
			defer func() {
				DrainTimeOut = originalDrainTimeOut
			}()

			mockOrchestrator.EXPECT().BeforeDrainNode(ctx, nCopy).Return(true, nil)

			_, err := drn.DrainNode(ctx, nCopy, false, false, DrainErrorNoop)
			Expect(err).To(HaveOccurred())
		})

		It("should return error if not able to drain the node", func() {
			n, _ := createNode("node0")
			mockOrchestrator.EXPECT().BeforeDrainNode(ctx, n).Return(true, nil)
			createPodWithFinalizerOnNode(ctx, "test-node-0", "node0")
			originalDrainTimeOut := DrainTimeOut
			DrainTimeOut = 3 * time.Second
			defer func() {
				DrainTimeOut = originalDrainTimeOut
			}()

			_, err := drn.DrainNode(ctx, n, true, false, DrainErrorNoop)
			Expect(err).To(HaveOccurred())
		})

		DescribeTable("should select device consumers and wait for termination during a partial drain",
			func(claim *corev1.PodResourceClaim, driver, extendedResource string, daemonSet, wantDrain bool) {
				n, _ := createNode("node-dra")
				pod := &corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name: "consumer", Namespace: testNamespace,
						Finalizers: []string{"sriov-operator.io/test"},
					},
					Spec: corev1.PodSpec{
						NodeName: n.Name, TerminationGracePeriodSeconds: ptr.To[int64](0),
						Containers: []corev1.Container{{Name: "app", Image: "test"}},
					},
				}
				if claim != nil {
					pod.Spec.ResourceClaims = []corev1.PodResourceClaim{*claim}
					pod.Spec.Containers[0].Resources.Claims = []corev1.ResourceClaim{{Name: claim.Name}}
				}
				if claim != nil {
					claimName := "generated-vf-claim"
					if claim.ResourceClaimName != nil {
						claimName = *claim.ResourceClaimName
					}
					rc := &resourceapi.ResourceClaim{
						ObjectMeta: metav1.ObjectMeta{Name: claimName, Namespace: testNamespace},
						Spec: resourceapi.ResourceClaimSpec{Devices: resourceapi.DeviceClaim{
							Requests: []resourceapi.DeviceRequest{{Name: "vf", Exactly: &resourceapi.ExactDeviceRequest{
								DeviceClassName: "custom-device-class", AllocationMode: resourceapi.DeviceAllocationModeExactCount, Count: 1,
							}}},
						}},
					}
					Expect(k8sClient.Create(ctx, rc)).To(Succeed())
					DeferCleanup(func() { Expect(k8sClient.Delete(context.Background(), rc)).To(Succeed()) })
					rc.Status.Allocation = &resourceapi.AllocationResult{Devices: resourceapi.DeviceAllocationResult{
						Results: []resourceapi.DeviceRequestAllocationResult{{Request: "vf", Driver: driver, Pool: "pool", Device: "vf0"}},
					}}
					Expect(k8sClient.Status().Update(ctx, rc)).To(Succeed())
				}
				if extendedResource != "" {
					if extendedResource == "sriov" {
						extendedResource = vars.ResourcePrefix + "/vf"
					}
					resources := corev1.ResourceList{corev1.ResourceName(extendedResource): resource.MustParse("1")}
					pod.Spec.Containers[0].Resources.Requests = resources
					pod.Spec.Containers[0].Resources.Limits = resources
				}
				if daemonSet {
					ds := &appsv1.DaemonSet{
						ObjectMeta: metav1.ObjectMeta{Name: "dra-daemon", Namespace: testNamespace},
						Spec: appsv1.DaemonSetSpec{
							Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "dra-daemon"}},
							Template: corev1.PodTemplateSpec{
								ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"app": "dra-daemon"}},
								Spec:       pod.Spec,
							},
						},
					}
					Expect(k8sClient.Create(ctx, ds)).To(Succeed())
					DeferCleanup(func() { Expect(k8sClient.Delete(context.Background(), ds)).To(Succeed()) })
					pod.OwnerReferences = []metav1.OwnerReference{*metav1.NewControllerRef(ds, appsv1.SchemeGroupVersion.WithKind("DaemonSet"))}
				}
				Expect(k8sClient.Create(ctx, pod)).To(Succeed())
				key := client.ObjectKeyFromObject(pod)
				DeferCleanup(func() {
					remaining := &corev1.Pod{}
					if err := k8sClient.Get(context.Background(), key, remaining); !errors.IsNotFound(err) {
						Expect(err).NotTo(HaveOccurred())
						remaining.Finalizers = nil
						Expect(k8sClient.Update(context.Background(), remaining)).To(Succeed())
					}
				})
				if claim != nil && claim.ResourceClaimTemplateName != nil {
					pod.Status.ResourceClaimStatuses = []corev1.PodResourceClaimStatus{{
						Name: claim.Name, ResourceClaimName: ptr.To("generated-vf-claim"),
					}}
				}
				pod.Status.Phase = corev1.PodRunning
				Expect(k8sClient.Status().Update(ctx, pod)).To(Succeed())

				originalTimeout := DrainTimeOut
				DrainTimeOut = time.Second
				DeferCleanup(func() { DrainTimeOut = originalTimeout })
				mockOrchestrator.EXPECT().BeforeDrainNode(ctx, n).Return(true, nil)
				completed, err := drn.DrainNode(ctx, n, false, false, DrainErrorNoop)
				if !wantDrain {
					Expect(err).NotTo(HaveOccurred())
					Expect(completed).To(BeTrue())
					Expect(k8sClient.Get(ctx, key, pod)).To(Succeed())
					Expect(pod.DeletionTimestamp).To(BeNil())
					return
				}

				// Eviction alone must not allow provider restart or VF reconfiguration.
				Expect(err).To(HaveOccurred())
				Expect(completed).To(BeFalse())
				Expect(k8sClient.Get(ctx, key, pod)).To(Succeed())
				Expect(pod.DeletionTimestamp).NotTo(BeNil())

				// Simulate termination; envtest has no kubelet to finish pod deletion.
				pod.Finalizers = nil
				Expect(k8sClient.Update(ctx, pod)).To(Succeed())
				Expect(k8sClient.Delete(ctx, pod, &client.DeleteOptions{GracePeriodSeconds: ptr.To[int64](0)})).To(SatisfyAny(Succeed(), WithTransform(errors.IsNotFound, BeTrue())))
				Expect(errors.IsNotFound(k8sClient.Get(ctx, key, &corev1.Pod{}))).To(BeTrue())
				mockOrchestrator.EXPECT().BeforeDrainNode(ctx, n).Return(true, nil)
				completed, err = drn.DrainNode(ctx, n, false, false, DrainErrorNoop)
				Expect(err).NotTo(HaveOccurred())
				Expect(completed).To(BeTrue())
			},
			Entry("named DRA claim", &corev1.PodResourceClaim{Name: "vf", ResourceClaimName: ptr.To("vf-claim")}, draconsts.DriverName, "", false, true),
			Entry("DRA claim template", &corev1.PodResourceClaim{Name: "vf", ResourceClaimTemplateName: ptr.To("vf-template")}, draconsts.DriverName, "", false, true),
			Entry("unrelated DRA claim", &corev1.PodResourceClaim{Name: "vf", ResourceClaimName: ptr.To("vf-claim")}, "gpu.example.com", "", false, false),
			Entry("unrelated DRA template", &corev1.PodResourceClaim{Name: "vf", ResourceClaimTemplateName: ptr.To("vf-template")}, "gpu.example.com", "", false, false),
			Entry("SR-IOV extended resource", nil, "", "sriov", false, true),
			Entry("ordinary pod", nil, "", "", false, false),
			Entry("unrelated extended resource", nil, "", "other.example.com/device", false, false),
			Entry("DRA DaemonSet", &corev1.PodResourceClaim{Name: "vf", ResourceClaimName: ptr.To("vf-claim")}, draconsts.DriverName, "", true, false),
		)

		It("should remove pods with sriov devices only if a full drain is not requested", func() {
			n, _ := createNode("node1")
			resources := map[corev1.ResourceName]resource.Quantity{corev1.ResourceName(fmt.Sprintf("%s/test", vars.ResourcePrefix)): resource.MustParse("1")}
			n.Status.Allocatable = resources
			n.Status.Capacity = resources
			err := k8sClient.Status().Update(ctx, n)
			Expect(err).ToNot(HaveOccurred())
			mockOrchestrator.EXPECT().BeforeDrainNode(ctx, n).Return(true, nil)
			createPodOnNode(ctx, "regular-pod", "node1")
			createPodWithSriovDeviceOnNode(ctx, "sriov-pod", "node1")

			go func() {
				Eventually(func(g Gomega) {
					podObj := &corev1.Pod{}
					err = k8sClient.Get(ctx, client.ObjectKey{Name: "sriov-pod", Namespace: testNamespace}, podObj)
					g.Expect(err).ToNot(HaveOccurred())
					g.Expect(podObj.DeletionTimestamp).ToNot(BeNil())
					err = k8sClient.Delete(ctx, podObj, &client.DeleteOptions{GracePeriodSeconds: ptr.To[int64](0)})
					g.Expect(err).ToNot(HaveOccurred())
				}, 2*time.Minute, time.Second).Should(Succeed())
			}()

			_, err = drn.DrainNode(ctx, n, false, false, DrainErrorNoop)
			Expect(err).ToNot(HaveOccurred())
			pod := &corev1.Pod{}
			err = k8sClient.Get(ctx, client.ObjectKey{Name: "regular-pod", Namespace: testNamespace}, pod)
			Expect(err).ToNot(HaveOccurred())
			err = k8sClient.Get(ctx, client.ObjectKey{Name: "sriov-pod", Namespace: testNamespace}, pod)
			Expect(err).To(HaveOccurred())
			Expect(errors.IsNotFound(err)).To(BeTrue())

		})
	})

	Context("IsInformationalDrainErrOutMessage", func() {
		DescribeTable("classifies kubectl drain ErrOut messages",
			func(msg string, informational bool) {
				Expect(IsInformationalDrainErrOutMessage(msg)).To(Equal(informational))
			},
			Entry("empty message", "", true),
			Entry("pod deletion warning from RunNodeDrain",
				"WARNING: ignoring DaemonSet-managed Pods: kube-system/kube-proxy-abc\n", true),
			Entry("pdb eviction retry from evictPods",
				`error when evicting pods/"my-pod" -n "default" (will retry after 5s): Too many requests`, true),
			Entry("terminating namespace eviction retry from evictPods",
				`error when evicting pod "my-pod" from terminating namespace "default" (will retry after 5s): forbidden`, true),
			Entry("terminal eviction failure returned by RunNodeDrain not ErrOut",
				`error when evicting pods/"my-pod" -n "default": pods "my-pod" is forbidden`, false),
			Entry("global timeout failure returned by RunNodeDrain not ErrOut",
				`error when evicting pods/"my-pod" -n "default": global timeout reached: 1m30s`, false),
			Entry("wait for delete failure returned by RunNodeDrain not ErrOut",
				`error when waiting for pod "my-pod" in namespace "default" to terminate: timed out`, false),
		)
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
			mockOrchestrator.EXPECT().AfterCompleteDrainNode(ctx, n).Return(false, fmt.Errorf("test"))

			completed, err := drn.CompleteDrainNode(ctx, n)
			Expect(err).To(HaveOccurred())
			Expect(completed).To(BeFalse())
		})

		It("should return completed", func() {
			n, _ := createNode("node0")
			n.Spec.Unschedulable = true
			mockOrchestrator.EXPECT().AfterCompleteDrainNode(ctx, n).Return(true, nil)

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
			NodeName: nodeName, TerminationGracePeriodSeconds: ptr.To[int64](1)}}
	Expect(k8sClient.Create(ctx, &pod)).ToNot(HaveOccurred())
}

func createPodWithFinalizerOnNode(ctx context.Context, podName, nodeName string) {
	pod := corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: podName, Namespace: testNamespace, Finalizers: []string{"sriov-operator.io/test"}},
		Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "test", Image: "test", Command: []string{"test"}}},
			NodeName: nodeName, TerminationGracePeriodSeconds: ptr.To[int64](1)}}
	Expect(k8sClient.Create(ctx, &pod)).ToNot(HaveOccurred())
}

func createPodWithSriovDeviceOnNode(ctx context.Context, podName, nodeName string) {
	resources := map[corev1.ResourceName]resource.Quantity{corev1.ResourceName(fmt.Sprintf("%s/test", vars.ResourcePrefix)): resource.MustParse("1")}
	pod := corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: podName, Namespace: testNamespace},
		Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "test", Image: "test", Command: []string{"test"},
			Resources: corev1.ResourceRequirements{Requests: resources, Limits: resources}}},
			NodeName: nodeName, TerminationGracePeriodSeconds: ptr.To[int64](1)}}
	Expect(k8sClient.Create(ctx, &pod)).ToNot(HaveOccurred())
}

var _ = Describe("SR-IOV DRA drain filter", func() {
	named := []corev1.PodResourceClaim{{Name: "device", ResourceClaimName: ptr.To("claim")}}
	template := []corev1.PodResourceClaim{{Name: "device", ResourceClaimTemplateName: ptr.To("template")}}
	resolved := []corev1.PodResourceClaimStatus{{Name: "device", ResourceClaimName: ptr.To("claim")}}
	for _, tt := range []struct {
		name                          string
		claims                        []corev1.PodResourceClaim
		statuses                      []corev1.PodResourceClaimStatus
		extended                      bool
		drivers                       []string
		missing, forbidden, fullDrain bool
		wantDelete, wantError         bool
	}{
		{name: "ordinary pod"},
		{name: "named SR-IOV claim", claims: named, drivers: []string{draconsts.DriverName}, wantDelete: true},
		{name: "unrelated driver", claims: named, drivers: []string{"gpu.example.com"}},
		{name: "mixed drivers", claims: named, drivers: []string{"gpu.example.com", draconsts.DriverName}, wantDelete: true},
		{name: "multiple claims", claims: append(append([]corev1.PodResourceClaim{}, named...), corev1.PodResourceClaim{Name: "second", ResourceClaimName: ptr.To("second")}), drivers: []string{draconsts.DriverName}, wantDelete: true},
		{name: "template SR-IOV claim", claims: template, statuses: resolved, drivers: []string{draconsts.DriverName}, wantDelete: true},
		{name: "template unrelated driver", claims: template, statuses: resolved, drivers: []string{"gpu.example.com"}},
		{name: "template not created", claims: template},
		{name: "template status for another claim", claims: template, statuses: []corev1.PodResourceClaimStatus{{Name: "other", ResourceClaimName: ptr.To("claim")}}, drivers: []string{draconsts.DriverName}},
		{name: "unallocated claim", claims: named},
		{name: "missing claim blocks drain", claims: named, missing: true, wantError: true},
		{name: "forbidden claim blocks drain", claims: named, forbidden: true, wantError: true},
		{name: "full drain bypasses claim lookup", claims: named, forbidden: true, fullDrain: true, wantDelete: true},
		{name: "extended resource backed by SR-IOV DRA", extended: true, drivers: []string{draconsts.DriverName}, wantDelete: true},
		{name: "extended resource backed by another driver", extended: true, drivers: []string{"gpu.example.com"}},
	} {
		It(tt.name, func() {
			claim := &resourceapi.ResourceClaim{ObjectMeta: metav1.ObjectMeta{Name: "claim", Namespace: "test"}}
			if tt.drivers != nil {
				claim.Status.Allocation = &resourceapi.AllocationResult{}
				for _, driver := range tt.drivers {
					claim.Status.Allocation.Devices.Results = append(claim.Status.Allocation.Devices.Results, resourceapi.DeviceRequestAllocationResult{Driver: driver})
				}
			}
			client := fake.NewClientset()
			if !tt.missing {
				_, err := client.ResourceV1().ResourceClaims("test").Create(context.Background(), claim, metav1.CreateOptions{})
				Expect(err).NotTo(HaveOccurred())
				other := &resourceapi.ResourceClaim{ObjectMeta: metav1.ObjectMeta{Name: "second", Namespace: "test"}}
				_, err = client.ResourceV1().ResourceClaims("test").Create(context.Background(), other, metav1.CreateOptions{})
				Expect(err).NotTo(HaveOccurred())
			}
			client.ClearActions()
			if tt.forbidden {
				client.PrependReactor("get", "resourceclaims", func(action clienttesting.Action) (bool, runtime.Object, error) {
					return true, nil, errors.NewForbidden(schema.GroupResource{Group: "resource.k8s.io", Resource: "resourceclaims"}, "claim", fmt.Errorf("denied"))
				})
			}
			pod := corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "pod", Namespace: "test"}, Spec: corev1.PodSpec{ResourceClaims: tt.claims}, Status: corev1.PodStatus{ResourceClaimStatuses: tt.statuses}}
			if tt.extended {
				pod.Status.ExtendedResourceClaimStatus = &corev1.PodExtendedResourceClaimStatus{ResourceClaimName: "claim"}
			}
			ctx := context.WithValue(context.Background(), constants.LoggerContextKey, logr.Discard())
			var callbackErr error
			helper := createDrainHelper(client, ctx, tt.fullDrain, func(err error) { callbackErr = err })
			status := kubedrain.PodDeleteStatus{Delete: true}
			for _, filter := range helper.AdditionalFilters {
				status = filter(pod)
			}
			Expect(status.Delete).To(Equal(tt.wantDelete))
			if tt.wantError {
				Expect(status.Reason).To(Equal(kubedrain.PodDeleteStatusTypeError))
				Expect(callbackErr).To(HaveOccurred())
			} else {
				Expect(status.Reason).NotTo(Equal(kubedrain.PodDeleteStatusTypeError))
				Expect(callbackErr).NotTo(HaveOccurred())
			}
			if tt.fullDrain || (tt.claims == nil && !tt.extended) {
				Expect(client.Actions()).To(BeEmpty(), "unexpected claim lookup")
			}
		})
	}
})
