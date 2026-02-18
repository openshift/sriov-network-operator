package daemon_test

import (
	"context"
	"os"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"
	corev1 "k8s.io/api/core/v1"
	apiErrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	sriovnetworkv1 "github.com/k8snetworkplumbingwg/sriov-network-operator/api/v1"
	constants "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/consts"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/daemon"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/featuregate"
	mock_helper "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/helper/mock"
	hostTypes "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/host/types"
	snolog "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/log"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/platform"
	mock_platform "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/platform/mock"
	plugin "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/plugins"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/plugins/generic"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/vars"
)

var (
	k8sManager    manager.Manager
	kubeclient    *kubernetes.Clientset
	eventRecorder *daemon.EventRecorder
	wg            sync.WaitGroup
	startDaemon   func(dc *daemon.NodeReconciler)

	t                   FullGinkgoTInterface
	mockCtrl            *gomock.Controller
	hostHelper          *mock_helper.MockHostHelpersInterface
	genericPlugin       plugin.VendorPlugin
	platformMock        *mock_platform.MockInterface
	discoverSriovReturn *sriovDiscoverReturn
	nodeState           *sriovnetworkv1.SriovNetworkNodeState

	daemonReconciler *daemon.NodeReconciler
)

const (
	waitTime            = 30 * time.Minute
	retryTime           = 5 * time.Second
	nodeName            = "node1"
	devicePluginPodName = "sriov-device-plugin-test"
)

// newSriovDiscoverReturn creates a new sriovDiscoverReturn instance.
func newSriovDiscoverReturn() *sriovDiscoverReturn {
	return &sriovDiscoverReturn{
		mu:                 sync.RWMutex{},
		original:           []sriovnetworkv1.InterfaceExt{},
		afterConfiguration: []sriovnetworkv1.InterfaceExt{},
	}
}

// sriovDiscoverReturn is a helper struct that lets you configure what
// DiscoverSriovDevices should return both on initial start and after configuration (i.e., after ConfigSriovInterfaces has been called).
type sriovDiscoverReturn struct {
	mu                 sync.RWMutex
	original           []sriovnetworkv1.InterfaceExt
	afterConfiguration []sriovnetworkv1.InterfaceExt
	replaced           bool
}

func (s *sriovDiscoverReturn) SetOriginal(interfaces []sriovnetworkv1.InterfaceExt) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.original = interfaces
	s.replaced = false
}

func (s *sriovDiscoverReturn) SetAfter(interfaces []sriovnetworkv1.InterfaceExt) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.afterConfiguration = interfaces
}

func (s *sriovDiscoverReturn) ReplaceOriginalWithAfter() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.replaced = true
}

func (s *sriovDiscoverReturn) GetCurrent() []sriovnetworkv1.InterfaceExt {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.replaced {
		return s.afterConfiguration
	}
	return s.original
}

var _ = Describe("Daemon Controller", Ordered, func() {
	BeforeAll(func() {
		wg = sync.WaitGroup{}
		DeferCleanup(wg.Wait)

		ctx, cancel := context.WithCancel(context.Background())
		DeferCleanup(cancel)

		startDaemon = func(dc *daemon.NodeReconciler) {
			By("start controller manager")
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer GinkgoRecover()
				By("Start controller manager")
				err := k8sManager.Start(ctx)
				Expect(err).ToNot(HaveOccurred())
			}()
		}

		Expect(k8sClient.DeleteAllOf(context.Background(), &sriovnetworkv1.SriovOperatorConfig{}, client.InNamespace(testNamespace))).ToNot(HaveOccurred())
		soc := &sriovnetworkv1.SriovOperatorConfig{ObjectMeta: metav1.ObjectMeta{
			Name:      constants.DefaultConfigName,
			Namespace: testNamespace,
		},
			Spec: sriovnetworkv1.SriovOperatorConfigSpec{
				LogLevel: 2,
			},
		}
		err := k8sClient.Create(ctx, soc)
		Expect(err).ToNot(HaveOccurred())

		kubeclient = kubernetes.NewForConfigOrDie(cfg)
		eventRecorder = daemon.NewEventRecorder(k8sClient, kubeclient, scheme.Scheme)
		DeferCleanup(func() {
			eventRecorder.Shutdown()
		})

		snolog.SetLogLevel(2)
		// Check if the environment variable CLUSTER_TYPE is set
		if clusterType, ok := os.LookupEnv("CLUSTER_TYPE"); ok && constants.ClusterType(clusterType) == constants.ClusterTypeOpenshift {
			vars.ClusterType = constants.ClusterTypeOpenshift
		} else {
			vars.ClusterType = constants.ClusterTypeKubernetes
		}

		devicePluginPod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      devicePluginPodName,
				Namespace: testNamespace,
				Labels: map[string]string{
					"app": "sriov-device-plugin",
				},
				Annotations: map[string]string{
					constants.DevicePluginWaitConfigAnnotation: "true",
				},
			},
			Spec: corev1.PodSpec{
				NodeName: "node1",
				Containers: []corev1.Container{
					{
						Name:  "device-plugin",
						Image: "test-image",
					},
				},
			},
		}
		pr := newPodRecreator(k8sClient, devicePluginPod, 100*time.Millisecond)
		pr.Start(ctx)
		DeferCleanup(pr.Stop)

		By("Init mock functions")
		t = GinkgoT()
		mockCtrl = gomock.NewController(t)
		hostHelper = mock_helper.NewMockHostHelpersInterface(mockCtrl)
		platformMock = mock_platform.NewMockInterface(mockCtrl)

		// daemon initialization default mocks
		hostHelper.EXPECT().CheckRDMAEnabled().Return(true, nil)
		hostHelper.EXPECT().CleanSriovFilesFromHost(vars.ClusterType == constants.ClusterTypeOpenshift).Return(nil)
		hostHelper.EXPECT().TryEnableTun()
		hostHelper.EXPECT().TryEnableVhostNet()
		hostHelper.EXPECT().PrepareNMUdevRule().Return(nil)
		hostHelper.EXPECT().PrepareVFRepUdevRule().Return(nil)
		hostHelper.EXPECT().WriteCheckpointFile(gomock.Any()).Return(nil)

		// general
		hostHelper.EXPECT().Chroot(gomock.Any()).Return(func() error { return nil }, nil).AnyTimes()
		hostHelper.EXPECT().RunCommand("/bin/sh", gomock.Any(), gomock.Any(), gomock.Any()).Return("", "", nil).AnyTimes()

		discoverSriovReturn = newSriovDiscoverReturn()

		hostHelper.EXPECT().LoadPfsStatus("0000:16:00.0").Return(&sriovnetworkv1.Interface{ExternallyManaged: false}, true, nil).AnyTimes()
		hostHelper.EXPECT().ClearPCIAddressFolder().Return(nil).AnyTimes()
		hostHelper.EXPECT().DiscoverRDMASubsystem().Return("shared", nil).AnyTimes()
		hostHelper.EXPECT().GetCurrentKernelArgs().Return("", nil).AnyTimes()
		hostHelper.EXPECT().IsKernelArgsSet("", constants.KernelArgPciRealloc).Return(true).AnyTimes()
		hostHelper.EXPECT().IsKernelArgsSet("", constants.KernelArgIntelIommu).Return(true).AnyTimes()
		hostHelper.EXPECT().IsKernelArgsSet("", constants.KernelArgIommuPt).Return(true).AnyTimes()
		hostHelper.EXPECT().IsKernelArgsSet("", constants.KernelArgRdmaExclusive).Return(false).AnyTimes()
		hostHelper.EXPECT().IsKernelArgsSet("", constants.KernelArgRdmaShared).Return(false).AnyTimes()
		hostHelper.EXPECT().SetRDMASubsystem("").Return(nil).AnyTimes()

		hostHelper.EXPECT().ConfigSriovInterfaces(gomock.Any(), gomock.Any(), gomock.Any(), false).Do(
			func(_, _, _, _ any) { discoverSriovReturn.ReplaceOriginalWithAfter() }).AnyTimes()
		// k8s plugin for k8s cluster type
		if vars.ClusterType == constants.ClusterTypeKubernetes {
			hostHelper.EXPECT().ReadServiceManifestFile(gomock.Any()).Return(&hostTypes.Service{Name: "test"}, nil).AnyTimes()
			hostHelper.EXPECT().ReadServiceInjectionManifestFile(gomock.Any()).Return(&hostTypes.Service{Name: "test"}, nil).AnyTimes()
		}

		platformMock.EXPECT().Init().Return(nil)
		// TODO: remove this when adding unit tests for switchdev
		platformMock.EXPECT().DiscoverBridges().Return(sriovnetworkv1.Bridges{}, nil).AnyTimes()
		platformMock.EXPECT().DiscoverSriovDevices().DoAndReturn(func() ([]sriovnetworkv1.InterfaceExt, error) {
			return discoverSriovReturn.GetCurrent(), nil
		}).AnyTimes()

		genericPlugin, err = generic.NewGenericPlugin(hostHelper)
		Expect(err).ToNot(HaveOccurred())
		platformMock.EXPECT().GetVendorPlugins(gomock.Any()).Return(genericPlugin, []plugin.VendorPlugin{}, nil)

		featureGates := featuregate.New()
		featureGates.Init(map[string]bool{})
		vars.FeatureGate = featureGates
		daemonReconciler = createDaemon(platformMock, featureGates, []string{})
		startDaemon(daemonReconciler)
		createNode(nodeName)
	})

	AfterAll(func() {
		Expect(k8sClient.DeleteAllOf(context.Background(), &corev1.Node{})).ToNot(HaveOccurred())

		Expect(k8sClient.DeleteAllOf(context.Background(), &sriovnetworkv1.SriovNetworkNodeState{}, client.InNamespace(testNamespace))).ToNot(HaveOccurred())
		Expect(k8sClient.DeleteAllOf(context.Background(), &sriovnetworkv1.SriovOperatorConfig{}, client.InNamespace(testNamespace))).ToNot(HaveOccurred())
	})

	// make sure that we are starting each test from the predefined initial state
	BeforeEach(func() {
		discoverSriovReturn.SetOriginal([]sriovnetworkv1.InterfaceExt{})
		discoverSriovReturn.SetAfter([]sriovnetworkv1.InterfaceExt{})
		nodeState = ensureEmptyNodeState(nodeName)
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(context.Background(), client.ObjectKeyFromObject(nodeState), nodeState)).
				ToNot(HaveOccurred())
			g.Expect(nodeState.Status.SyncStatus).To(Equal(constants.SyncStatusSucceeded))
		}, waitTime, retryTime).Should(Succeed())
	})

	Context("Config Daemon generic flow", func() {
		It("Should expose nodeState Status section", func(ctx context.Context) {
			originalInterface := sriovnetworkv1.InterfaceExt{
				Name:           "eno1",
				Driver:         "ice",
				PciAddress:     "0000:16:00.0",
				DeviceID:       "1593",
				Vendor:         "8086",
				EswitchMode:    "legacy",
				LinkAdminState: "up",
				LinkSpeed:      "10000 Mb/s",
				LinkType:       "ETH",
				Mac:            "aa:bb:cc:dd:ee:ff",
				Mtu:            1500,
				TotalVfs:       2,
				NumVfs:         0,
			}

			discoverSriovReturn.SetOriginal([]sriovnetworkv1.InterfaceExt{originalInterface})
			afterInterface := *originalInterface.DeepCopy()
			afterInterface.NumVfs = 2
			afterInterface.VFs = []sriovnetworkv1.VirtualFunction{
				{
					Name:       "eno1f0",
					PciAddress: "0000:16:00.1",
					VfID:       0,
					Driver:     "iavf",
				},
				{
					Name:       "eno1f1",
					PciAddress: "0000:16:00.2",
					VfID:       1,
					Driver:     "iavf",
				}}
			discoverSriovReturn.SetAfter([]sriovnetworkv1.InterfaceExt{afterInterface})

			By("waiting for state to be succeeded")
			eventuallySyncStatusEqual(nodeState, constants.SyncStatusSucceeded)

			By("add spec to node state")
			err := k8sClient.Get(ctx, types.NamespacedName{Namespace: nodeState.Namespace, Name: nodeState.Name}, nodeState)
			Expect(err).ToNot(HaveOccurred())

			nodeState.Spec.Interfaces = []sriovnetworkv1.Interface{
				{Name: "eno1",
					PciAddress: "0000:16:00.0",
					LinkType:   "eth",
					NumVfs:     2,
					VfGroups: []sriovnetworkv1.VfGroup{
						{ResourceName: "test",
							DeviceType: "netdevice",
							PolicyName: "test-policy",
							VfRange:    "eno1#0-1"},
					}},
			}

			err = k8sClient.Update(ctx, nodeState)
			Expect(err).ToNot(HaveOccurred())

			By("waiting to require drain")
			EventuallyWithOffset(1, func(g Gomega) {
				g.Expect(k8sClient.Get(context.Background(), types.NamespacedName{Namespace: nodeState.Namespace, Name: nodeState.Name}, nodeState)).
					ToNot(HaveOccurred())
				g.Expect(daemonReconciler.GetLastAppliedGeneration()).To(Equal(int64(2)))
			}, waitTime, retryTime).Should(Succeed())

			err = k8sClient.Get(ctx, types.NamespacedName{Namespace: nodeState.Namespace, Name: nodeState.Name}, nodeState)
			Expect(err).ToNot(HaveOccurred())
			nodeState.Spec.Interfaces = []sriovnetworkv1.Interface{}
			err = k8sClient.Update(ctx, nodeState)
			Expect(err).ToNot(HaveOccurred())

			EventuallyWithOffset(1, func(g Gomega) {
				g.Expect(k8sClient.Get(context.Background(), types.NamespacedName{Namespace: nodeState.Namespace, Name: nodeState.Name}, nodeState)).
					ToNot(HaveOccurred())

				g.Expect(nodeState.Annotations[constants.NodeStateDrainAnnotation]).To(Equal(constants.DrainRequired))
				// verify that external drainer annotation doesn't exist
				node := &corev1.Node{}
				g.Expect(k8sClient.Get(context.Background(), types.NamespacedName{Name: nodeName}, node)).
					ToNot(HaveOccurred())
				g.Expect(nodeState.Annotations).NotTo(ContainElement(constants.NodeStateExternalDrainerAnnotation))
			}, waitTime, retryTime).Should(Succeed())

			patchAnnotation(nodeState, constants.NodeStateDrainAnnotationCurrent, constants.DrainComplete)
			// Validate status
			EventuallyWithOffset(1, func(g Gomega) {
				g.Expect(k8sClient.Get(context.Background(), types.NamespacedName{Namespace: nodeState.Namespace, Name: nodeState.Name}, nodeState)).
					ToNot(HaveOccurred())

				g.Expect(nodeState.Annotations[constants.NodeStateDrainAnnotation]).To(Equal(constants.DrainIdle))
			}, waitTime, retryTime).Should(Succeed())
			patchAnnotation(nodeState, constants.NodeStateDrainAnnotationCurrent, constants.DrainIdle)

			// Validate status
			EventuallyWithOffset(1, func(g Gomega) {
				g.Expect(k8sClient.Get(context.Background(), types.NamespacedName{Namespace: nodeState.Namespace, Name: nodeState.Name}, nodeState)).
					ToNot(HaveOccurred())

				g.Expect(nodeState.Status.SyncStatus).To(Equal(constants.SyncStatusSucceeded))
			}, waitTime, retryTime).Should(Succeed())

			Expect(nodeState.Status.LastSyncError).To(Equal(""))
		})

		It("Should apply external drainer annotation when useExternalDrainer is true", func(ctx context.Context) {
			DeferCleanup(func(x bool) { vars.UseExternalDrainer = x }, vars.UseExternalDrainer)
			vars.UseExternalDrainer = true

			By("waiting for drain idle states")
			EventuallyWithOffset(1, func(g Gomega) {
				g.Expect(k8sClient.Get(context.Background(), types.NamespacedName{Namespace: nodeState.Namespace, Name: nodeState.Name}, nodeState)).
					ToNot(HaveOccurred())

				g.Expect(nodeState.Annotations[constants.NodeStateDrainAnnotation]).To(Equal(constants.DrainIdle))
				g.Expect(nodeState.Annotations[constants.NodeStateDrainAnnotationCurrent]).To(Equal(constants.DrainIdle))
			}, waitTime, retryTime).Should(Succeed())

			// trigger reconcile to add external drainer annotation
			patchAnnotation(nodeState, "foo", "bar")
			By("waiting for external drainer annotation to be added")
			EventuallyWithOffset(1, func(g Gomega) {
				g.Expect(k8sClient.Get(context.Background(), types.NamespacedName{Namespace: nodeState.Namespace, Name: nodeState.Name}, nodeState)).
					ToNot(HaveOccurred())

				// verify that external drainer annotation exist
				g.Expect(nodeState.Annotations[constants.NodeStateExternalDrainerAnnotation]).To(Equal("true"))
			}, waitTime, retryTime).Should(Succeed())

			originalInterface := sriovnetworkv1.InterfaceExt{
				Name:           "eno1",
				Driver:         "ice",
				PciAddress:     "0000:16:00.0",
				DeviceID:       "1593",
				Vendor:         "8086",
				EswitchMode:    "legacy",
				LinkAdminState: "up",
				LinkSpeed:      "10000 Mb/s",
				LinkType:       "ETH",
				Mac:            "aa:bb:cc:dd:ee:ff",
				Mtu:            1500,
				TotalVfs:       3,
				NumVfs:         1, // in case if VF num is zero the drain is not required
				VFs: []sriovnetworkv1.VirtualFunction{
					{
						Name:       "eno1f0",
						PciAddress: "0000:16:00.1",
						VfID:       0,
						Driver:     "iavf",
					},
				},
			}
			discoverSriovReturn.SetOriginal([]sriovnetworkv1.InterfaceExt{originalInterface})
			afterInterface := *originalInterface.DeepCopy()
			afterInterface.NumVfs = 3
			afterInterface.VFs = []sriovnetworkv1.VirtualFunction{
				{
					Name:       "eno1f0",
					PciAddress: "0000:16:00.1",
					VfID:       0,
					Driver:     "iavf",
				},
				{
					Name:       "eno1f1",
					PciAddress: "0000:16:00.2",
					VfID:       1,
					Driver:     "iavf",
				},
				{
					Name:       "eno1f2",
					PciAddress: "0000:16:00.3",
					VfID:       2,
					Driver:     "iavf",
				},
			}
			discoverSriovReturn.SetAfter([]sriovnetworkv1.InterfaceExt{afterInterface})

			EventuallyWithOffset(1, func(g Gomega) {
				err := k8sClient.Get(ctx, types.NamespacedName{Namespace: nodeState.Namespace, Name: nodeState.Name}, nodeState)
				g.Expect(err).ToNot(HaveOccurred())

				nodeState.Spec.Interfaces = []sriovnetworkv1.Interface{
					{Name: "eno1",
						PciAddress: "0000:16:00.0",
						LinkType:   "eth",
						NumVfs:     3,
						VfGroups: []sriovnetworkv1.VfGroup{
							{ResourceName: "test",
								DeviceType: "netdevice",
								PolicyName: "test-policy",
								VfRange:    "0-2"},
						}},
				}
				err = k8sClient.Update(ctx, nodeState)
				g.Expect(err).ToNot(HaveOccurred())
			}, waitTime, retryTime).Should(Succeed())

			By("waiting for drain required and external drainer annotation to be added")
			EventuallyWithOffset(1, func(g Gomega) {
				g.Expect(k8sClient.Get(context.Background(), types.NamespacedName{Namespace: nodeState.Namespace, Name: nodeState.Name}, nodeState)).
					ToNot(HaveOccurred())

				g.Expect(nodeState.Annotations[constants.NodeStateDrainAnnotation]).To(Equal(constants.DrainRequired))
				// verify that external drainer annotation exist
				g.Expect(nodeState.Annotations[constants.NodeStateExternalDrainerAnnotation]).To(Equal("true"))
			}, waitTime, retryTime).Should(Succeed())

			patchAnnotation(nodeState, constants.NodeStateDrainAnnotationCurrent, constants.DrainComplete)
			// Validate status
			EventuallyWithOffset(1, func(g Gomega) {
				g.Expect(k8sClient.Get(context.Background(), types.NamespacedName{Namespace: nodeState.Namespace, Name: nodeState.Name}, nodeState)).
					ToNot(HaveOccurred())

				g.Expect(nodeState.Annotations[constants.NodeStateDrainAnnotation]).To(Equal(constants.DrainIdle))
			}, waitTime, retryTime).Should(Succeed())
			patchAnnotation(nodeState, constants.NodeStateDrainAnnotationCurrent, constants.DrainIdle)

			// Validate status
			EventuallyWithOffset(1, func(g Gomega) {
				g.Expect(k8sClient.Get(context.Background(), types.NamespacedName{Namespace: nodeState.Namespace, Name: nodeState.Name}, nodeState)).
					ToNot(HaveOccurred())

				g.Expect(nodeState.Status.SyncStatus).To(Equal(constants.SyncStatusSucceeded))
				g.Expect(nodeState.Annotations[constants.NodeStateDrainAnnotation]).To(Equal(constants.DrainIdle))
			}, waitTime, retryTime).Should(Succeed())

			// disable external drainer flag
			vars.UseExternalDrainer = false
			// remove 'foo' annotation to trigger reconcile that should remove external drainer annotation
			original := nodeState.DeepCopy()
			delete(nodeState.Annotations, "foo")
			Expect(k8sClient.Patch(context.Background(), nodeState, client.MergeFrom(original))).ToNot(HaveOccurred())

			By("waiting for external drainer annotation to be removed")
			EventuallyWithOffset(1, func(g Gomega) {
				g.Expect(k8sClient.Get(context.Background(), types.NamespacedName{Namespace: nodeState.Namespace, Name: nodeState.Name}, nodeState)).
					ToNot(HaveOccurred())

				// verify that external drainer annotation does not exist
				g.Expect(nodeState.Annotations).ToNot(ContainElement(constants.NodeStateExternalDrainerAnnotation))
			}, waitTime, retryTime).Should(Succeed())

			Expect(nodeState.Status.LastSyncError).To(Equal(""))
		})

		It("Should apply the reset configuration when disableDrain is true", func(ctx context.Context) {
			DeferCleanup(func(x bool) { vars.DisableDrain = x }, vars.DisableDrain)
			vars.DisableDrain = true

			By("waiting for drain idle states")
			EventuallyWithOffset(1, func(g Gomega) {
				g.Expect(k8sClient.Get(context.Background(), types.NamespacedName{Namespace: nodeState.Namespace, Name: nodeState.Name}, nodeState)).
					ToNot(HaveOccurred())

				g.Expect(nodeState.Annotations[constants.NodeStateDrainAnnotation]).To(Equal(constants.DrainIdle))
				g.Expect(nodeState.Annotations[constants.NodeStateDrainAnnotationCurrent]).To(Equal(constants.DrainIdle))
			}, waitTime, retryTime).Should(Succeed())

			originalInterface := sriovnetworkv1.InterfaceExt{
				Name:           "eno1",
				Driver:         "ice",
				PciAddress:     "0000:16:00.0",
				DeviceID:       "1593",
				Vendor:         "8086",
				EswitchMode:    "legacy",
				LinkAdminState: "up",
				LinkSpeed:      "10000 Mb/s",
				LinkType:       "ETH",
				Mac:            "aa:bb:cc:dd:ee:ff",
				Mtu:            1500,
				TotalVfs:       2,
				NumVfs:         0,
			}
			discoverSriovReturn.SetOriginal([]sriovnetworkv1.InterfaceExt{originalInterface})
			afterInterface := *originalInterface.DeepCopy()
			afterInterface.NumVfs = 2
			afterInterface.VFs = []sriovnetworkv1.VirtualFunction{
				{
					Name:       "eno1f0",
					PciAddress: "0000:16:00.1",
					VfID:       0,
					Driver:     "iavf",
				},
				{
					Name:       "eno1f1",
					PciAddress: "0000:16:00.2",
					VfID:       1,
					Driver:     "iavf",
				},
			}
			discoverSriovReturn.SetAfter([]sriovnetworkv1.InterfaceExt{afterInterface})

			EventuallyWithOffset(1, func(g Gomega) {
				err := k8sClient.Get(ctx, types.NamespacedName{Namespace: nodeState.Namespace, Name: nodeState.Name}, nodeState)
				g.Expect(err).ToNot(HaveOccurred())

				nodeState.Spec.Interfaces = []sriovnetworkv1.Interface{
					{Name: "eno1",
						PciAddress: "0000:16:00.0",
						LinkType:   "eth",
						NumVfs:     2,
						VfGroups: []sriovnetworkv1.VfGroup{
							{ResourceName: "test",
								DeviceType: "netdevice",
								PolicyName: "test-policy",
								VfRange:    "eno1#0-1"},
						}},
				}
				err = k8sClient.Update(ctx, nodeState)
				g.Expect(err).ToNot(HaveOccurred())
			}, waitTime, retryTime).Should(Succeed())

			eventuallySyncStatusEqual(nodeState, constants.SyncStatusSucceeded)

			By("Simulate node policy removal")
			EventuallyWithOffset(1, func(g Gomega) {
				err := k8sClient.Get(ctx, types.NamespacedName{Namespace: nodeState.Namespace, Name: nodeState.Name}, nodeState)
				g.Expect(err).ToNot(HaveOccurred())

				nodeState.Spec.Interfaces = []sriovnetworkv1.Interface{}
				err = k8sClient.Update(ctx, nodeState)
				g.Expect(err).ToNot(HaveOccurred())
			}, waitTime, retryTime).Should(Succeed())

			eventuallySyncStatusEqual(nodeState, constants.SyncStatusSucceeded)
			assertLastStatusTransitionsContains(nodeState, 2, constants.SyncStatusInProgress)

			By("waiting for drain idle")
			EventuallyWithOffset(1, func(g Gomega) {
				g.Expect(k8sClient.Get(context.Background(), types.NamespacedName{Namespace: nodeState.Namespace, Name: nodeState.Name}, nodeState)).
					ToNot(HaveOccurred())

				g.Expect(nodeState.Annotations[constants.NodeStateDrainAnnotation]).To(Equal(constants.DrainIdle))

			}, waitTime, retryTime).Should(Succeed())
		})

		It("Should not wait for device plugin pod when there are no interfaces and blockDevicePluginUntilConfigured is enabled", func(ctx context.Context) {
			DeferCleanup(func(x bool) { vars.DisableDrain = x }, vars.DisableDrain)
			vars.DisableDrain = true

			By("waiting for drain idle states")
			EventuallyWithOffset(1, func(g Gomega) {
				g.Expect(k8sClient.Get(context.Background(), types.NamespacedName{Namespace: nodeState.Namespace, Name: nodeState.Name}, nodeState)).
					ToNot(HaveOccurred())

				g.Expect(nodeState.Annotations[constants.NodeStateDrainAnnotation]).To(Equal(constants.DrainIdle))
				g.Expect(nodeState.Annotations[constants.NodeStateDrainAnnotationCurrent]).To(Equal(constants.DrainIdle))
			}, waitTime, retryTime).Should(Succeed())

			By("setting an empty interfaces spec (no policies)")
			EventuallyWithOffset(1, func(g Gomega) {
				err := k8sClient.Get(ctx, types.NamespacedName{Namespace: nodeState.Namespace, Name: nodeState.Name}, nodeState)
				g.Expect(err).ToNot(HaveOccurred())

				nodeState.Spec.Interfaces = []sriovnetworkv1.Interface{}
				err = k8sClient.Update(ctx, nodeState)
				g.Expect(err).ToNot(HaveOccurred())
			}, waitTime, retryTime).Should(Succeed())

			By("verifying sync status reaches Succeeded without stalling on device plugin wait")
			eventuallySyncStatusEqual(nodeState, constants.SyncStatusSucceeded)

			By("verifying the device plugin pod still has the wait-for-config annotation (was not unblocked)")
			devicePluginPod := &corev1.Pod{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: devicePluginPodName, Namespace: testNamespace},
				devicePluginPod)).ToNot(HaveOccurred())
			Expect(devicePluginPod.Annotations).To(HaveKey(constants.DevicePluginWaitConfigAnnotation))
		})

		It("Should unblock the device plugin pod when configuration is finished", func(ctx context.Context) {
			DeferCleanup(func(x bool) { vars.DisableDrain = x }, vars.DisableDrain)
			vars.DisableDrain = true

			originalInterface := sriovnetworkv1.InterfaceExt{
				Name:           "eno1",
				Driver:         "ice",
				PciAddress:     "0000:16:00.0",
				DeviceID:       "1593",
				Vendor:         "8086",
				EswitchMode:    "legacy",
				LinkAdminState: "up",
				LinkSpeed:      "10000 Mb/s",
				LinkType:       "ETH",
				Mac:            "aa:bb:cc:dd:ee:ff",
				Mtu:            1500,
				TotalVfs:       1,
				NumVfs:         0,
			}
			discoverSriovReturn.SetOriginal([]sriovnetworkv1.InterfaceExt{originalInterface})
			afterInterface := *originalInterface.DeepCopy()
			afterInterface.NumVfs = 1
			afterInterface.VFs = []sriovnetworkv1.VirtualFunction{
				{
					Name:       "eno1f0",
					PciAddress: "0000:16:00.1",
					VfID:       0,
					Driver:     "iavf",
				},
			}
			discoverSriovReturn.SetAfter([]sriovnetworkv1.InterfaceExt{afterInterface})

			EventuallyWithOffset(1, func(g Gomega) {
				err := k8sClient.Get(ctx, types.NamespacedName{Namespace: nodeState.Namespace, Name: nodeState.Name}, nodeState)
				g.Expect(err).ToNot(HaveOccurred())

				nodeState.Spec.Interfaces = []sriovnetworkv1.Interface{
					{Name: "eno1",
						PciAddress: "0000:16:00.0",
						LinkType:   "eth",
						NumVfs:     12,
						VfGroups: []sriovnetworkv1.VfGroup{
							{ResourceName: "test",
								DeviceType: "netdevice",
								PolicyName: "test-policy",
								VfRange:    "eno1#0-0"},
						}},
				}
				err = k8sClient.Update(ctx, nodeState)
				g.Expect(err).ToNot(HaveOccurred())
			}, waitTime, retryTime).Should(Succeed())

			eventuallySyncStatusEqual(nodeState, constants.SyncStatusSucceeded)
			assertLastStatusTransitionsContains(nodeState, 2, constants.SyncStatusInProgress)

			// Verify that the device plugin pod is present and the 'wait-for-config' annotation has been removed upon completion of configuration
			Eventually(func(g Gomega) {
				devicePluginPod := &corev1.Pod{}
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: devicePluginPodName, Namespace: testNamespace},
					devicePluginPod)).ToNot(HaveOccurred())
				g.Expect(devicePluginPod.Annotations).ToNot(HaveKey(constants.DevicePluginWaitConfigAnnotation))
			}, waitTime, retryTime).Should(Succeed())
		})
	})
})

func patchAnnotation(nodeState *sriovnetworkv1.SriovNetworkNodeState, key, value string) {
	originalNodeState := nodeState.DeepCopy()
	nodeState.Annotations[key] = value
	err := k8sClient.Patch(context.Background(), nodeState, client.MergeFrom(originalNodeState))
	Expect(err).ToNot(HaveOccurred())
}

func createNode(nodeName string) *corev1.Node {
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

	vars.NodeName = nodeName
	Expect(k8sClient.Create(context.Background(), &node)).ToNot(HaveOccurred())
	return &node
}

func ensureEmptyNodeState(nodeName string) *sriovnetworkv1.SriovNetworkNodeState {
	existingNodeState := &sriovnetworkv1.SriovNetworkNodeState{}
	err := k8sClient.Get(context.Background(), types.NamespacedName{
		Name:      nodeName,
		Namespace: testNamespace,
	}, existingNodeState)
	if err == nil {
		Expect(k8sClient.Delete(context.Background(), existingNodeState)).ToNot(HaveOccurred())
	} else {
		Expect(apiErrors.IsNotFound(err)).To(BeTrue())
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
	Expect(k8sClient.Create(context.Background(), &nodeState)).ToNot(HaveOccurred())
	return &nodeState
}

func createDaemon(
	platformInterface platform.Interface,
	featureGates featuregate.FeatureGate,
	disablePlugins []string) *daemon.NodeReconciler {
	kClient, err := client.New(
		cfg,
		client.Options{
			Scheme: scheme.Scheme})
	Expect(err).ToNot(HaveOccurred())

	By("Setup controller manager")
	k8sManager, err = ctrl.NewManager(cfg, ctrl.Options{
		Scheme: scheme.Scheme,
	})
	Expect(err).ToNot(HaveOccurred())

	configController := daemon.New(kClient, hostHelper, platformInterface, eventRecorder, featureGates)
	err = configController.Init(disablePlugins)
	Expect(err).ToNot(HaveOccurred())
	err = configController.SetupWithManager(k8sManager)
	Expect(err).ToNot(HaveOccurred())

	return configController
}

func eventuallySyncStatusEqual(nodeState *sriovnetworkv1.SriovNetworkNodeState, expectedState string) {
	EventuallyWithOffset(1, func(g Gomega) {
		g.Expect(k8sClient.Get(context.Background(), types.NamespacedName{Namespace: nodeState.Namespace, Name: nodeState.Name}, nodeState)).
			ToNot(HaveOccurred())
		g.Expect(nodeState.Status.SyncStatus).To(Equal(expectedState))
	}, 10*time.Second, 100*time.Millisecond).Should(Succeed())
}

func assertLastStatusTransitionsContains(nodeState *sriovnetworkv1.SriovNetworkNodeState, numberOfTransitions int, status string) {
	events := &corev1.EventList{}
	err := k8sClient.List(
		context.Background(),
		events,
		client.MatchingFields{
			"involvedObject.name": nodeState.Name,
			"reason":              "SyncStatusChanged",
		},
		client.Limit(numberOfTransitions),
	)
	Expect(err).ToNot(HaveOccurred())

	// Status transition events are in the form
	// `Status changed from: Succeed to: InProgress`
	Expect(events.Items).To(ContainElement(
		HaveField("Message", ContainSubstring("to: "+status))))
}

// podRecreator manages a pod lifecycle in the background, ensuring the pod
// is recreated if it gets deleted until explicitly stopped.
type podRecreator struct {
	client       client.Client
	podSpec      *corev1.Pod
	pollInterval time.Duration

	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// newPodRecreator creates a new podRecreator instance.
// podSpec must have Name and Namespace set.
func newPodRecreator(c client.Client, podSpec *corev1.Pod, pollInterval time.Duration) *podRecreator {
	return &podRecreator{
		client:       c,
		podSpec:      podSpec,
		pollInterval: pollInterval,
	}
}

// Start begins the background loop that ensures the pod exists.
// It creates the pod if it doesn't exist and periodically checks
// if the pod was removed, recreating it if necessary.
func (pr *podRecreator) Start(ctx context.Context) {
	ctx, pr.cancel = context.WithCancel(ctx)
	pr.ensurePodExists(ctx)
	pr.wg.Add(1)
	go func() {
		defer pr.wg.Done()
		pr.ensurePodExists(ctx)
		ticker := time.NewTicker(pr.pollInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				pr.ensurePodExists(ctx)
			}
		}
	}()
}

// Stop stops the background loop and waits for it to finish.
func (pr *podRecreator) Stop() {
	if pr.cancel != nil {
		pr.cancel()
	}
	pr.wg.Wait()
	pr.client.Delete(context.Background(), pr.podSpec)
}

// ensurePodExists checks if the pod exists and creates it if not found.
func (pr *podRecreator) ensurePodExists(ctx context.Context) {
	pod := &corev1.Pod{}
	err := pr.client.Get(ctx, types.NamespacedName{
		Name:      pr.podSpec.Name,
		Namespace: pr.podSpec.Namespace,
	}, pod)
	if apiErrors.IsNotFound(err) {
		// Pod not found, create it
		newPod := pr.podSpec.DeepCopy()
		_ = pr.client.Create(ctx, newPod)
		return
	}
	if err == nil && pod.DeletionTimestamp != nil {
		// the pod has nodeName set, we need to force delete it to simulate kubelet behavior
		_ = pr.client.Delete(ctx, pod, client.GracePeriodSeconds(0))
	}
}
