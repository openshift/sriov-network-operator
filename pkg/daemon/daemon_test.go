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
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/helper"
	mock_helper "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/helper/mock"
	hostTypes "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/host/types"
	snolog "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/log"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/platforms"
	mock_platforms "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/platforms/mock"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/vars"
)

var (
	cancel        context.CancelFunc
	ctx           context.Context
	k8sManager    manager.Manager
	kubeclient    *kubernetes.Clientset
	eventRecorder *daemon.EventRecorder
	wg            sync.WaitGroup
	startDaemon   func(dc *daemon.NodeReconciler)

	t              FullGinkgoTInterface
	mockCtrl       *gomock.Controller
	hostHelper     *mock_helper.MockHostHelpersInterface
	platformHelper *mock_platforms.MockInterface
)

const (
	waitTime  = 30 * time.Minute
	retryTime = 5 * time.Second
)

var _ = Describe("Daemon Controller", Ordered, func() {
	BeforeAll(func() {
		ctx, cancel = context.WithCancel(context.Background())
		wg = sync.WaitGroup{}
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
		if clusterType, ok := os.LookupEnv("CLUSTER_TYPE"); ok && clusterType == constants.ClusterTypeOpenshift {
			vars.ClusterType = constants.ClusterTypeOpenshift
		} else {
			vars.ClusterType = constants.ClusterTypeKubernetes
		}
	})

	BeforeEach(func() {
		t = GinkgoT()
		mockCtrl = gomock.NewController(t)
		hostHelper = mock_helper.NewMockHostHelpersInterface(mockCtrl)
		platformHelper = mock_platforms.NewMockInterface(mockCtrl)

		// daemon initialization default mocks
		hostHelper.EXPECT().CheckRDMAEnabled().Return(true, nil)
		hostHelper.EXPECT().CleanSriovFilesFromHost(vars.ClusterType == constants.ClusterTypeOpenshift).Return(nil)
		hostHelper.EXPECT().TryEnableTun()
		hostHelper.EXPECT().TryEnableVhostNet()
		hostHelper.EXPECT().PrepareNMUdevRule([]string{}).Return(nil)
		hostHelper.EXPECT().PrepareVFRepUdevRule().Return(nil)
		hostHelper.EXPECT().WriteCheckpointFile(gomock.Any()).Return(nil)

		// general
		hostHelper.EXPECT().Chroot(gomock.Any()).Return(func() error { return nil }, nil).AnyTimes()
		hostHelper.EXPECT().RunCommand("/bin/sh", gomock.Any(), gomock.Any(), gomock.Any()).Return("", "", nil).AnyTimes()

	})

	AfterEach(func() {
		Expect(k8sClient.DeleteAllOf(context.Background(), &sriovnetworkv1.SriovNetworkNodeState{}, client.InNamespace(testNamespace))).ToNot(HaveOccurred())

		By("Shutdown controller manager")
		cancel()
		wg.Wait()
	})

	AfterAll(func() {
		Expect(k8sClient.DeleteAllOf(context.Background(), &sriovnetworkv1.SriovOperatorConfig{}, client.InNamespace(testNamespace))).ToNot(HaveOccurred())
	})

	Context("Config Daemon generic flow", func() {
		BeforeEach(func() {
			// k8s plugin for k8s cluster type
			if vars.ClusterType == constants.ClusterTypeKubernetes {
				hostHelper.EXPECT().ReadServiceManifestFile(gomock.Any()).Return(&hostTypes.Service{Name: "test"}, nil).AnyTimes()
				hostHelper.EXPECT().ReadServiceInjectionManifestFile(gomock.Any()).Return(&hostTypes.Service{Name: "test"}, nil).AnyTimes()
			}
		})

		It("Should expose nodeState Status section", func() {
			By("Init mock functions")
			afterConfig := false
			hostHelper.EXPECT().DiscoverSriovDevices(hostHelper).DoAndReturn(func(helpersInterface helper.HostHelpersInterface) ([]sriovnetworkv1.InterfaceExt, error) {
				interfaceExtList := []sriovnetworkv1.InterfaceExt{
					{
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
					},
				}

				if afterConfig {
					interfaceExtList[0].NumVfs = 2
					interfaceExtList[0].VFs = []sriovnetworkv1.VirtualFunction{
						{
							Name:       "eno1f0",
							PciAddress: "0000:16:00.1",
							VfID:       0,
						},
						{
							Name:       "eno1f1",
							PciAddress: "0000:16:00.2",
							VfID:       1,
						}}
				}
				return interfaceExtList, nil
			}).AnyTimes()

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

			hostHelper.EXPECT().ConfigSriovInterfaces(gomock.Any(), gomock.Any(), gomock.Any(), false).Return(nil).AnyTimes()

			featureGates := featuregate.New()
			featureGates.Init(map[string]bool{})
			dc := createDaemon(hostHelper, platformHelper, featureGates, []string{})
			startDaemon(dc)

			_, nodeState := createNode("node1")
			By("waiting for state to be succeeded")
			EventuallyWithOffset(1, func(g Gomega) {
				g.Expect(k8sClient.Get(context.Background(), types.NamespacedName{Namespace: nodeState.Namespace, Name: nodeState.Name}, nodeState)).
					ToNot(HaveOccurred())

				g.Expect(nodeState.Status.SyncStatus).To(Equal(constants.SyncStatusSucceeded))
			}, waitTime, retryTime).Should(Succeed())

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
			afterConfig = true
			err = k8sClient.Update(ctx, nodeState)
			Expect(err).ToNot(HaveOccurred())
			By("waiting to require drain")
			EventuallyWithOffset(1, func(g Gomega) {
				g.Expect(k8sClient.Get(context.Background(), types.NamespacedName{Namespace: nodeState.Namespace, Name: nodeState.Name}, nodeState)).
					ToNot(HaveOccurred())
				g.Expect(dc.GetLastAppliedGeneration()).To(Equal(int64(2)))
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

		It("Should apply the reset configuration when disableDrain is true", func() {
			DeferCleanup(func(x bool) { vars.DisableDrain = x }, vars.DisableDrain)
			vars.DisableDrain = true

			By("Init mock functions")
			hostHelper.EXPECT().DiscoverSriovDevices(hostHelper).DoAndReturn(func(helpersInterface helper.HostHelpersInterface) ([]sriovnetworkv1.InterfaceExt, error) {
				interfaceExtList := []sriovnetworkv1.InterfaceExt{
					{
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
						NumVfs:         2,
						VFs: []sriovnetworkv1.VirtualFunction{
							{
								Name:       "eno1f0",
								PciAddress: "0000:16:00.1",
								VfID:       0,
							},
							{
								Name:       "eno1f1",
								PciAddress: "0000:16:00.2",
								VfID:       1,
							}},
					},
				}

				return interfaceExtList, nil
			}).AnyTimes()

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

			hostHelper.EXPECT().ConfigSriovInterfaces(gomock.Any(), gomock.Any(), gomock.Any(), false).Return(nil).AnyTimes()

			_, nodeState := createNode("node1")
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
			err := k8sClient.Update(ctx, nodeState)
			Expect(err).ToNot(HaveOccurred())

			featureGates := featuregate.New()
			featureGates.Init(map[string]bool{})
			dc := createDaemon(hostHelper, platformHelper, featureGates, []string{})
			startDaemon(dc)

			eventuallySyncStatusEqual(nodeState, constants.SyncStatusSucceeded)

			By("Simulate node policy removal")
			nodeState.Spec.Interfaces = []sriovnetworkv1.Interface{}
			err = k8sClient.Update(ctx, nodeState)
			Expect(err).ToNot(HaveOccurred())

			eventuallySyncStatusEqual(nodeState, constants.SyncStatusSucceeded)
			assertLastStatusTransitionsContains(nodeState, 2, constants.SyncStatusInProgress)
		})
	})
})

func patchAnnotation(nodeState *sriovnetworkv1.SriovNetworkNodeState, key, value string) {
	originalNodeState := nodeState.DeepCopy()
	nodeState.Annotations[key] = value
	err := k8sClient.Patch(ctx, nodeState, client.MergeFrom(originalNodeState))
	Expect(err).ToNot(HaveOccurred())
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

func createDaemon(
	hostHelper helper.HostHelpersInterface,
	platformHelper platforms.Interface,
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

	configController := daemon.New(kClient, hostHelper, platformHelper, eventRecorder, featureGates, disablePlugins)
	err = configController.Init()
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
