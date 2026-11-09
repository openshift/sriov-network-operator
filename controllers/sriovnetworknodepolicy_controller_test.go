package controllers

import (
	"context"
	"encoding/json"
	"os"
	"slices"
	"sync"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/google/go-cmp/cmp"
	dptypes "github.com/k8snetworkplumbingwg/sriov-network-device-plugin/pkg/types"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	k8sclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	sriovnetworkv1 "github.com/k8snetworkplumbingwg/sriov-network-operator/api/v1"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/consts"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/featuregate"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/vars"
)

func mustMarshallSelector(t *testing.T, input *dptypes.NetDeviceSelectors) *json.RawMessage {
	out, err := json.Marshal(input)
	if err != nil {
		t.Error(err)
		t.FailNow()
		return nil
	}
	ret := json.RawMessage(out)
	return &ret
}

func mustUnmarshallSelector(input *json.RawMessage) *dptypes.NetDeviceSelectors {
	ret := dptypes.NetDeviceSelectors{}
	err := json.Unmarshal(*input, &ret)
	ExpectWithOffset(1, err).ToNot(HaveOccurred())
	return &ret
}

func TestResolvePfNames(t *testing.T) {
	testCases := []struct {
		name           string
		pfNames        []string
		nodeState      *sriovnetworkv1.SriovNetworkNodeState
		expectedResult []string
	}{
		{
			name:           "empty pfNames returns empty slice",
			pfNames:        []string{},
			nodeState:      &sriovnetworkv1.SriovNetworkNodeState{},
			expectedResult: []string{},
		},
		{
			name:           "nil nodeState returns pfNames as-is",
			pfNames:        []string{"eth0", "eth1"},
			nodeState:      nil,
			expectedResult: []string{"eth0", "eth1"},
		},
		{
			name:    "pfNames with actual interface names",
			pfNames: []string{"ens803f0", "ens803f1"},
			nodeState: &sriovnetworkv1.SriovNetworkNodeState{
				Status: sriovnetworkv1.SriovNetworkNodeStateStatus{
					Interfaces: []sriovnetworkv1.InterfaceExt{
						{Name: "ens803f0", AltNames: []string{"alt1", "alt2"}},
						{Name: "ens803f1", AltNames: []string{"alt3"}},
					},
				},
			},
			expectedResult: []string{"ens803f0", "ens803f1"},
		},
		{
			name:    "pfNames with alternative names resolve to actual names",
			pfNames: []string{"alt1", "alt3"},
			nodeState: &sriovnetworkv1.SriovNetworkNodeState{
				Status: sriovnetworkv1.SriovNetworkNodeStateStatus{
					Interfaces: []sriovnetworkv1.InterfaceExt{
						{Name: "ens803f0", AltNames: []string{"alt1", "alt2"}},
						{Name: "ens803f1", AltNames: []string{"alt3"}},
					},
				},
			},
			expectedResult: []string{"ens803f0", "ens803f1"},
		},
		{
			name:    "mixed pfNames with actual and alternative names",
			pfNames: []string{"ens803f0", "alt3", "alt2"},
			nodeState: &sriovnetworkv1.SriovNetworkNodeState{
				Status: sriovnetworkv1.SriovNetworkNodeStateStatus{
					Interfaces: []sriovnetworkv1.InterfaceExt{
						{Name: "ens803f0", AltNames: []string{"alt1", "alt2"}},
						{Name: "ens803f1", AltNames: []string{"alt3"}},
					},
				},
			},
			expectedResult: []string{"ens803f0", "ens803f1", "ens803f0"},
		},
		{
			name:    "pfName not found in nodeState returns unchanged",
			pfNames: []string{"notfound", "ens803f0"},
			nodeState: &sriovnetworkv1.SriovNetworkNodeState{
				Status: sriovnetworkv1.SriovNetworkNodeStateStatus{
					Interfaces: []sriovnetworkv1.InterfaceExt{
						{Name: "ens803f0", AltNames: []string{"alt1"}},
					},
				},
			},
			expectedResult: []string{"notfound", "ens803f0"},
		},
		{
			name:    "pfNames with ranges preserved",
			pfNames: []string{"ens803f0#0-9", "alt1#10-19"},
			nodeState: &sriovnetworkv1.SriovNetworkNodeState{
				Status: sriovnetworkv1.SriovNetworkNodeStateStatus{
					Interfaces: []sriovnetworkv1.InterfaceExt{
						{Name: "ens803f0", AltNames: []string{"alt1"}},
					},
				},
			},
			expectedResult: []string{"ens803f0#0-9", "ens803f0#10-19"},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := resolvePfNames(tc.pfNames, tc.nodeState)
			if len(result) != len(tc.expectedResult) {
				t.Errorf("resolvePfNames() returned slice of length %d, want %d", len(result), len(tc.expectedResult))
				return
			}
			for i := range result {
				if result[i] != tc.expectedResult[i] {
					t.Errorf("resolvePfNames()[%d] = %q, want %q", i, result[i], tc.expectedResult[i])
				}
			}
		})
	}
}

func TestRenderDevicePluginConfigData(t *testing.T) {
	table := []struct {
		tname       string
		policy      sriovnetworkv1.SriovNetworkNodePolicy
		expResource dptypes.ResourceConfList
	}{
		{
			tname: "testVirtioVdpaVirtio",
			policy: sriovnetworkv1.SriovNetworkNodePolicy{
				Spec: sriovnetworkv1.SriovNetworkNodePolicySpec{
					ResourceName: "resourceName",
					DeviceType:   consts.DeviceTypeNetDevice,
					VdpaType:     consts.VdpaTypeVirtio,
				},
			},
			expResource: dptypes.ResourceConfList{
				ResourceList: []dptypes.ResourceConfig{
					{
						ResourceName: "resourceName",
						Selectors: mustMarshallSelector(t, &dptypes.NetDeviceSelectors{
							VdpaType: dptypes.VdpaType(consts.VdpaTypeVirtio),
						}),
					},
				},
			},
		}, {
			tname: "testVhostVdpaVirtio",
			policy: sriovnetworkv1.SriovNetworkNodePolicy{
				Spec: sriovnetworkv1.SriovNetworkNodePolicySpec{
					ResourceName: "resourceName",
					DeviceType:   consts.DeviceTypeNetDevice,
					VdpaType:     consts.VdpaTypeVhost,
				},
			},
			expResource: dptypes.ResourceConfList{
				ResourceList: []dptypes.ResourceConfig{
					{
						ResourceName: "resourceName",
						Selectors: mustMarshallSelector(t, &dptypes.NetDeviceSelectors{
							VdpaType: dptypes.VdpaType(consts.VdpaTypeVhost),
						}),
					},
				},
			},
		},
		{
			tname: "testExcludeTopology",
			policy: sriovnetworkv1.SriovNetworkNodePolicy{
				Spec: sriovnetworkv1.SriovNetworkNodePolicySpec{
					ResourceName:    "resourceName",
					ExcludeTopology: true,
				},
			},
			expResource: dptypes.ResourceConfList{
				ResourceList: []dptypes.ResourceConfig{
					{
						ResourceName:    "resourceName",
						Selectors:       mustMarshallSelector(t, &dptypes.NetDeviceSelectors{}),
						ExcludeTopology: true,
					},
				},
			},
		},
	}

	reconciler := SriovNetworkNodePolicyReconciler{
		FeatureGate: featuregate.New(),
	}

	node := corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "node1"}}
	nodeState := sriovnetworkv1.SriovNetworkNodeState{ObjectMeta: metav1.ObjectMeta{Name: node.Name, Namespace: vars.Namespace}}

	scheme := runtime.NewScheme()
	utilruntime.Must(sriovnetworkv1.AddToScheme(scheme))
	reconciler.Client = fake.NewClientBuilder().
		WithScheme(scheme).WithObjects(&nodeState).
		Build()

	for _, tc := range table {
		policyList := sriovnetworkv1.SriovNetworkNodePolicyList{Items: []sriovnetworkv1.SriovNetworkNodePolicy{tc.policy}}

		t.Run(tc.tname, func(t *testing.T) {
			resourceList, err := reconciler.renderDevicePluginConfigData(context.TODO(), &policyList, &node)
			if err != nil {
				t.Error(tc.tname, "renderDevicePluginConfigData has failed")
			}

			if !cmp.Equal(resourceList, tc.expResource) {
				t.Error(tc.tname, "ResourceConfList not as expected", cmp.Diff(resourceList, tc.expResource))
			}
		})
	}
}

var _ = Describe("SriovnetworkNodePolicy controller", Ordered, func() {
	var cancel context.CancelFunc
	var ctx context.Context

	BeforeAll(func() {
		// disable stale state cleanup delay to check that the controller can cleanup state objects
		DeferCleanup(os.Setenv, "STALE_NODE_STATE_CLEANUP_DELAY_MINUTES", os.Getenv("STALE_NODE_STATE_CLEANUP_DELAY_MINUTES"))
		os.Setenv("STALE_NODE_STATE_CLEANUP_DELAY_MINUTES", "0")

		By("Create SriovOperatorConfig controller k8s objs")
		config := makeDefaultSriovOpConfig()
		Expect(k8sClient.Create(context.Background(), config)).Should(Succeed())
		DeferCleanup(func() {
			err := k8sClient.Delete(context.Background(), config)
			Expect(err).ToNot(HaveOccurred())
		})

		// setup controller manager
		By("Setup controller manager")
		k8sManager, err := setupK8sManagerForTest()
		Expect(err).ToNot(HaveOccurred())

		err = (&SriovNetworkNodePolicyReconciler{
			Client:      k8sManager.GetClient(),
			Scheme:      k8sManager.GetScheme(),
			FeatureGate: featuregate.New(),
		}).SetupWithManager(k8sManager)
		Expect(err).ToNot(HaveOccurred())

		ctx, cancel = context.WithCancel(context.Background())

		wg := sync.WaitGroup{}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer GinkgoRecover()
			By("Start controller manager")
			err := k8sManager.Start(ctx)
			Expect(err).ToNot(HaveOccurred())
		}()

		DeferCleanup(func() {
			By("Shut down manager")
			cancel()
			wg.Wait()
		})
	})
	AfterEach(func() {
		err := k8sClient.DeleteAllOf(context.Background(), &corev1.Node{}, k8sclient.GracePeriodSeconds(0))
		Expect(err).ToNot(HaveOccurred())

		err = k8sClient.DeleteAllOf(context.Background(), &sriovnetworkv1.SriovNetworkNodePolicy{}, k8sclient.InNamespace(vars.Namespace), k8sclient.GracePeriodSeconds(0))
		Expect(err).ToNot(HaveOccurred())

		err = k8sClient.DeleteAllOf(context.Background(), &sriovnetworkv1.SriovNetworkNodeState{}, k8sclient.InNamespace(vars.Namespace), k8sclient.GracePeriodSeconds(0))
		Expect(err).ToNot(HaveOccurred())
	})
	DescribeTable("renders policy revisions promptly after interface discovery", func(initialInterfaces sriovnetworkv1.InterfaceExts) {
		policy := &sriovnetworkv1.SriovNetworkNodePolicy{
			ObjectMeta: metav1.ObjectMeta{Name: "discovery-policy", Namespace: vars.Namespace},
			Spec: sriovnetworkv1.SriovNetworkNodePolicySpec{
				ResourceName: "resource", NumVfs: 2,
				NodeSelector: map[string]string{"node-role.kubernetes.io/worker": ""},
				NicSelector:  sriovnetworkv1.SriovNetworkNicSelector{PfNames: []string{"ens1"}},
			},
		}
		node := testManagedNode("discovery-worker")
		Expect(k8sClient.Create(ctx, policy)).To(Succeed())
		Expect(k8sClient.Create(ctx, node)).To(Succeed())
		state := &sriovnetworkv1.SriovNetworkNodeState{}
		key := k8sclient.ObjectKey{Name: node.Name, Namespace: vars.Namespace}
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, key, state)).To(Succeed())
			g.Expect(k8sClient.Get(ctx, k8sclient.ObjectKeyFromObject(node), node)).To(Succeed())
			g.Expect(node.Labels).To(HaveKeyWithValue(consts.SriovDevicePluginLabel, consts.SriovDevicePluginLabelEnabled))
		}, 30*time.Second, 100*time.Millisecond).Should(Succeed())
		state.Status.Interfaces = initialInterfaces
		Expect(k8sClient.Status().Update(ctx, state)).To(Succeed())
		// Let discovery and node-label reconciles settle before publishing a matching NIC.
		Consistently(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, key, state)).To(Succeed())
			g.Expect(state.Status.Interfaces).To(Equal(initialInterfaces))
			g.Expect(policy.RevisionMatchesInNodeState(state)).To(BeFalse())
		}, 3*time.Second, 100*time.Millisecond).Should(Succeed())
		state.Status.Interfaces = append(initialInterfaces.DeepCopy(), sriovnetworkv1.InterfaceExt{
			Name: "ens1", PciAddress: "0000:01:00.0", TotalVfs: 8,
		})
		Expect(k8sClient.Status().Update(ctx, state)).To(Succeed())
		Eventually(func(g Gomega) {
			g.Expect(k8sClient.Get(ctx, key, state)).To(Succeed())
			g.Expect(policy.RevisionMatchesInNodeState(state)).To(BeTrue())
			g.Expect(state.Spec.Interfaces).To(HaveLen(1))
			g.Expect(state.Spec.Interfaces[0].NumVfs).To(Equal(2))
		}, 15*time.Second, 100*time.Millisecond).Should(Succeed())
	},
		Entry("initial discovery", sriovnetworkv1.InterfaceExts(nil)),
		Entry("additional discovery", sriovnetworkv1.InterfaceExts{{Name: "ens2", PciAddress: "0000:02:00.0", TotalVfs: 8}}),
	)

	Context("device plugin labels", func() {
		It("Should add the right labels to the nodes", func() {
			node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{
				Name: "node0",
				Labels: map[string]string{"kubernetes.io/os": "linux",
					"node-role.kubernetes.io/worker": ""},
			}}
			Expect(k8sClient.Create(ctx, node)).To(Succeed())

			nodeState := &sriovnetworkv1.SriovNetworkNodeState{}
			Eventually(func(g Gomega) {
				err := k8sClient.Get(context.TODO(), k8sclient.ObjectKey{Name: "node0", Namespace: testNamespace}, nodeState)
				g.Expect(err).ToNot(HaveOccurred())
			}, time.Minute, time.Second).Should(Succeed())

			Eventually(func(g Gomega) {
				err := k8sClient.Get(context.Background(), k8sclient.ObjectKey{Name: node.Name}, node)
				g.Expect(err).ToNot(HaveOccurred())
				value, exist := node.Labels[consts.SriovDevicePluginLabel]
				g.Expect(exist).To(BeTrue())
				g.Expect(value).To(Equal(consts.SriovDevicePluginLabelDisabled))
			}, time.Minute, time.Second).Should(Succeed())

			nodeState.Status.Interfaces = sriovnetworkv1.InterfaceExts{
				sriovnetworkv1.InterfaceExt{
					Vendor:     "8086",
					Driver:     "i40e",
					Mtu:        1500,
					Name:       "ens803f0",
					PciAddress: "0000:86:00.0",
					NumVfs:     0,
					TotalVfs:   64,
				},
			}
			err := k8sClient.Status().Update(context.Background(), nodeState)
			Expect(err).ToNot(HaveOccurred())

			somePolicy := &sriovnetworkv1.SriovNetworkNodePolicy{}
			somePolicy.SetNamespace(testNamespace)
			somePolicy.SetName("some-policy")
			somePolicy.Spec = sriovnetworkv1.SriovNetworkNodePolicySpec{
				NumVfs:       5,
				NodeSelector: map[string]string{"node-role.kubernetes.io/worker": ""},
				NicSelector:  sriovnetworkv1.SriovNetworkNicSelector{Vendor: "8086"},
				Priority:     20,
			}
			Expect(k8sClient.Create(context.Background(), somePolicy)).ToNot(HaveOccurred())

			Eventually(func(g Gomega) {
				err := k8sClient.Get(context.Background(), k8sclient.ObjectKey{Name: node.Name}, node)
				g.Expect(err).ToNot(HaveOccurred())
				value, exist := node.Labels[consts.SriovDevicePluginLabel]
				g.Expect(exist).To(BeTrue())
				g.Expect(value).To(Equal(consts.SriovDevicePluginLabelEnabled))
			}, time.Minute, time.Second).Should(Succeed())

			delete(node.Labels, "node-role.kubernetes.io/worker")
			err = k8sClient.Update(context.Background(), node)
			Expect(err).ToNot(HaveOccurred())

			Eventually(func(g Gomega) {
				err := k8sClient.Get(context.Background(), k8sclient.ObjectKey{Name: node.Name}, node)
				g.Expect(err).ToNot(HaveOccurred())
				_, exist := node.Labels[consts.SriovDevicePluginLabel]
				g.Expect(exist).To(BeFalse())
			}, time.Minute, time.Second).Should(Succeed())

			Eventually(func(g Gomega) {
				err := k8sClient.Get(context.Background(), k8sclient.ObjectKey{Name: node.Name, Namespace: testNamespace}, nodeState)
				Expect(err).To(HaveOccurred())
				Expect(errors.IsNotFound(err)).To(BeTrue())
			}, time.Minute, time.Second).Should(Succeed())
		})

		It("should skip label removal for nodes that doesn't exist with no stale timer", func() {
			node0 := &corev1.Node{ObjectMeta: metav1.ObjectMeta{
				Name: "node0",
				Labels: map[string]string{"kubernetes.io/os": "linux",
					"node-role.kubernetes.io/worker": ""},
			}}
			Expect(k8sClient.Create(ctx, node0)).To(Succeed())

			node1 := &corev1.Node{ObjectMeta: metav1.ObjectMeta{
				Name: "node1",
				Labels: map[string]string{"kubernetes.io/os": "linux",
					"node-role.kubernetes.io/worker": ""},
			}}
			Expect(k8sClient.Create(ctx, node1)).To(Succeed())

			nodeState := &sriovnetworkv1.SriovNetworkNodeState{}
			node := &corev1.Node{}
			for _, nodeName := range []string{"node0", "node1"} {
				Eventually(func(g Gomega) {
					err := k8sClient.Get(context.TODO(), k8sclient.ObjectKey{Name: nodeName, Namespace: testNamespace}, nodeState)
					g.Expect(err).ToNot(HaveOccurred())
				}, time.Minute, time.Second).Should(Succeed())

				Eventually(func(g Gomega) {
					err := k8sClient.Get(context.Background(), k8sclient.ObjectKey{Name: nodeName}, node)
					g.Expect(err).ToNot(HaveOccurred())
					value, exist := node.Labels[consts.SriovDevicePluginLabel]
					g.Expect(exist).To(BeTrue())
					g.Expect(value).To(Equal(consts.SriovDevicePluginLabelDisabled))
				}, time.Minute, time.Second).Should(Succeed())

				nodeState.Status.Interfaces = sriovnetworkv1.InterfaceExts{
					sriovnetworkv1.InterfaceExt{
						Vendor:     "8086",
						Driver:     "i40e",
						Mtu:        1500,
						Name:       "ens803f0",
						PciAddress: "0000:86:00.0",
						NumVfs:     0,
						TotalVfs:   64,
					},
				}
				err := k8sClient.Status().Update(context.Background(), nodeState)
				Expect(err).ToNot(HaveOccurred())
			}

			err := k8sClient.Delete(context.Background(), node1, k8sclient.GracePeriodSeconds(0))
			Expect(err).ToNot(HaveOccurred())

			Eventually(func(g Gomega) {
				err := k8sClient.Get(context.TODO(), k8sclient.ObjectKey{Name: "node1", Namespace: testNamespace}, nodeState)
				g.Expect(err).To(HaveOccurred())
				g.Expect(errors.IsNotFound(err)).To(BeTrue())
			}, 30*time.Second, time.Second).Should(Succeed())

			somePolicy := &sriovnetworkv1.SriovNetworkNodePolicy{}
			somePolicy.SetNamespace(testNamespace)
			somePolicy.SetName("some-policy")
			somePolicy.Spec = sriovnetworkv1.SriovNetworkNodePolicySpec{
				NumVfs:       5,
				NodeSelector: map[string]string{"node-role.kubernetes.io/worker": ""},
				NicSelector:  sriovnetworkv1.SriovNetworkNicSelector{Vendor: "8086"},
				Priority:     20,
			}
			Expect(k8sClient.Create(context.Background(), somePolicy)).ToNot(HaveOccurred())

			Eventually(func(g Gomega) {
				err := k8sClient.Get(context.Background(), k8sclient.ObjectKey{Name: node0.Name}, node0)
				g.Expect(err).ToNot(HaveOccurred())
				value, exist := node0.Labels[consts.SriovDevicePluginLabel]
				g.Expect(exist).To(BeTrue())
				g.Expect(value).To(Equal(consts.SriovDevicePluginLabelEnabled))
			}, time.Minute, time.Second).Should(Succeed())
		})

		It("should skip label removal for nodes that doesn't exist with stale timer", func() {
			err := os.Setenv("STALE_NODE_STATE_CLEANUP_DELAY_MINUTES", "5")
			Expect(err).ToNot(HaveOccurred())
			defer func() {
				err = os.Unsetenv("STALE_NODE_STATE_CLEANUP_DELAY_MINUTES")
				Expect(err).ToNot(HaveOccurred())
			}()

			node0 := &corev1.Node{ObjectMeta: metav1.ObjectMeta{
				Name: "node0",
				Labels: map[string]string{"kubernetes.io/os": "linux",
					"node-role.kubernetes.io/worker": ""},
			}}
			Expect(k8sClient.Create(ctx, node0)).To(Succeed())

			node1 := &corev1.Node{ObjectMeta: metav1.ObjectMeta{
				Name: "node1",
				Labels: map[string]string{"kubernetes.io/os": "linux",
					"node-role.kubernetes.io/worker": ""},
			}}
			Expect(k8sClient.Create(ctx, node1)).To(Succeed())

			nodeState := &sriovnetworkv1.SriovNetworkNodeState{}
			node := &corev1.Node{}
			for _, nodeName := range []string{"node0", "node1"} {
				Eventually(func(g Gomega) {
					err := k8sClient.Get(context.TODO(), k8sclient.ObjectKey{Name: nodeName, Namespace: testNamespace}, nodeState)
					g.Expect(err).ToNot(HaveOccurred())
				}, time.Minute, time.Second).Should(Succeed())

				Eventually(func(g Gomega) {
					err := k8sClient.Get(context.Background(), k8sclient.ObjectKey{Name: nodeName}, node)
					g.Expect(err).ToNot(HaveOccurred())
					value, exist := node.Labels[consts.SriovDevicePluginLabel]
					g.Expect(exist).To(BeTrue())
					g.Expect(value).To(Equal(consts.SriovDevicePluginLabelDisabled))
				}, time.Minute, time.Second).Should(Succeed())

				nodeState.Status.Interfaces = sriovnetworkv1.InterfaceExts{
					sriovnetworkv1.InterfaceExt{
						Vendor:     "8086",
						Driver:     "i40e",
						Mtu:        1500,
						Name:       "ens803f0",
						PciAddress: "0000:86:00.0",
						NumVfs:     0,
						TotalVfs:   64,
					},
				}
				err := k8sClient.Status().Update(context.Background(), nodeState)
				Expect(err).ToNot(HaveOccurred())
			}

			err = k8sClient.Delete(context.Background(), node1, k8sclient.GracePeriodSeconds(0))
			Expect(err).ToNot(HaveOccurred())

			Consistently(func(g Gomega) {
				err := k8sClient.Get(context.TODO(), k8sclient.ObjectKey{Name: "node1", Namespace: testNamespace}, nodeState)
				g.Expect(err).ToNot(HaveOccurred())
			}, 10*time.Second, time.Second).Should(Succeed())

			somePolicy := &sriovnetworkv1.SriovNetworkNodePolicy{}
			somePolicy.SetNamespace(testNamespace)
			somePolicy.SetName("some-policy")
			somePolicy.Spec = sriovnetworkv1.SriovNetworkNodePolicySpec{
				NumVfs:       5,
				NodeSelector: map[string]string{"node-role.kubernetes.io/worker": ""},
				NicSelector:  sriovnetworkv1.SriovNetworkNicSelector{Vendor: "8086"},
				Priority:     20,
			}
			Expect(k8sClient.Create(context.Background(), somePolicy)).ToNot(HaveOccurred())

			Eventually(func(g Gomega) {
				err := k8sClient.Get(context.Background(), k8sclient.ObjectKey{Name: node0.Name}, node0)
				g.Expect(err).ToNot(HaveOccurred())
				value, exist := node0.Labels[consts.SriovDevicePluginLabel]
				g.Expect(exist).To(BeTrue())
				g.Expect(value).To(Equal(consts.SriovDevicePluginLabelEnabled))
			}, time.Minute, time.Second).Should(Succeed())
		})
	})

	Context("RdmaMode", func() {
		BeforeEach(func() {
			Expect(
				k8sClient.DeleteAllOf(context.Background(), &sriovnetworkv1.SriovNetworkPoolConfig{}, k8sclient.InNamespace(vars.Namespace)),
			).ToNot(HaveOccurred())
		})

		It("field is correctly written to the SriovNetworkNodeState", func() {
			node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{
				Name: "node0",
				Labels: map[string]string{
					"node-role.kubernetes.io/worker": "",
					"kubernetes.io/os":               "linux",
					"test":                           "",
				},
			}}
			Expect(k8sClient.Create(ctx, node)).To(Succeed())

			nodeState := &sriovnetworkv1.SriovNetworkNodeState{}
			Eventually(func(g Gomega) {
				err := k8sClient.Get(context.TODO(), k8sclient.ObjectKey{Name: "node0", Namespace: testNamespace}, nodeState)
				g.Expect(err).ToNot(HaveOccurred())
			}, time.Minute, time.Second).Should(Succeed())

			nodeState.Status.Interfaces = sriovnetworkv1.InterfaceExts{
				sriovnetworkv1.InterfaceExt{
					Vendor:     "8086",
					Driver:     "i40e",
					Mtu:        1500,
					Name:       "ens803f0",
					PciAddress: "0000:86:00.0",
					NumVfs:     0,
					TotalVfs:   64,
				},
			}
			err := k8sClient.Status().Update(context.Background(), nodeState)
			Expect(err).ToNot(HaveOccurred())

			poolConfig := &sriovnetworkv1.SriovNetworkPoolConfig{}
			poolConfig.SetNamespace(testNamespace)
			poolConfig.SetName("test-workers")
			poolConfig.Spec = sriovnetworkv1.SriovNetworkPoolConfigSpec{
				NodeSelector: &metav1.LabelSelector{
					MatchLabels: map[string]string{
						"test": "",
					},
				},
				RdmaMode: "exclusive",
			}
			Expect(k8sClient.Create(ctx, poolConfig)).To(Succeed())

			Eventually(func(g Gomega) {
				err := k8sClient.Get(context.Background(), k8sclient.ObjectKey{Name: node.Name, Namespace: testNamespace}, nodeState)
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(nodeState.Spec.System.RdmaMode).To(Equal("exclusive"))
			}).WithPolling(time.Second).WithTimeout(time.Minute).Should(Succeed())

		})
	})
})

var _ = Describe("SriovNetworkNodePolicyReconciler", Ordered, func() {
	Context("handleStaleNodeState", func() {
		var (
			ctx       context.Context
			r         *SriovNetworkNodePolicyReconciler
			nodeState *sriovnetworkv1.SriovNetworkNodeState
		)

		BeforeEach(func() {
			ctx = context.Background()
			scheme := runtime.NewScheme()
			utilruntime.Must(sriovnetworkv1.AddToScheme(scheme))
			nodeState = &sriovnetworkv1.SriovNetworkNodeState{ObjectMeta: metav1.ObjectMeta{Name: "node1"}}
			r = &SriovNetworkNodePolicyReconciler{Client: fake.NewClientBuilder().WithObjects(nodeState).Build()}
		})
		It("should set default delay", func() {
			nodeState := nodeState.DeepCopy()
			Expect(r.handleStaleNodeState(ctx, nodeState)).NotTo(HaveOccurred())
			Expect(r.Get(ctx, types.NamespacedName{Name: nodeState.Name}, nodeState)).NotTo(HaveOccurred())
			Expect(time.Now().UTC().Before(nodeState.GetKeepUntilTime())).To(BeTrue())
		})
		It("should remove CR if wait time expired", func() {
			nodeState := nodeState.DeepCopy()
			nodeState.SetKeepUntilTime(time.Now().UTC().Add(-time.Minute))
			Expect(r.handleStaleNodeState(ctx, nodeState)).NotTo(HaveOccurred())
			Expect(errors.IsNotFound(r.Get(ctx, types.NamespacedName{Name: nodeState.Name}, nodeState))).To(BeTrue())
		})
		It("should keep existing wait time if already set", func() {
			nodeState := nodeState.DeepCopy()
			nodeState.SetKeepUntilTime(time.Now().UTC().Add(time.Minute))
			testTime := nodeState.GetKeepUntilTime()
			r.Update(ctx, nodeState)
			Expect(r.handleStaleNodeState(ctx, nodeState)).NotTo(HaveOccurred())
			Expect(r.Get(ctx, types.NamespacedName{Name: nodeState.Name}, nodeState)).NotTo(HaveOccurred())
			Expect(nodeState.GetKeepUntilTime()).To(Equal(testTime))
		})
		It("non default dealy", func() {
			DeferCleanup(os.Setenv, "STALE_NODE_STATE_CLEANUP_DELAY_MINUTES", os.Getenv("STALE_NODE_STATE_CLEANUP_DELAY_MINUTES"))
			os.Setenv("STALE_NODE_STATE_CLEANUP_DELAY_MINUTES", "60")
			nodeState := nodeState.DeepCopy()
			Expect(r.handleStaleNodeState(ctx, nodeState)).NotTo(HaveOccurred())
			Expect(r.Get(ctx, types.NamespacedName{Name: nodeState.Name}, nodeState)).NotTo(HaveOccurred())
			Expect(time.Until(nodeState.GetKeepUntilTime()) > 30*time.Minute).To(BeTrue())
		})
		It("invalid non default delay - should use default", func() {
			DeferCleanup(os.Setenv, "STALE_NODE_STATE_CLEANUP_DELAY_MINUTES", os.Getenv("STALE_NODE_STATE_CLEANUP_DELAY_MINUTES"))
			os.Setenv("STALE_NODE_STATE_CLEANUP_DELAY_MINUTES", "-20")
			nodeState := nodeState.DeepCopy()
			Expect(r.handleStaleNodeState(ctx, nodeState)).NotTo(HaveOccurred())
			Expect(r.Get(ctx, types.NamespacedName{Name: nodeState.Name}, nodeState)).NotTo(HaveOccurred())
			Expect(time.Until(nodeState.GetKeepUntilTime()) > 20*time.Minute).To(BeTrue())
		})
		It("should remove CR if delay is zero", func() {
			DeferCleanup(os.Setenv, "STALE_NODE_STATE_CLEANUP_DELAY_MINUTES", os.Getenv("STALE_NODE_STATE_CLEANUP_DELAY_MINUTES"))
			os.Setenv("STALE_NODE_STATE_CLEANUP_DELAY_MINUTES", "0")
			nodeState := nodeState.DeepCopy()
			Expect(r.handleStaleNodeState(ctx, nodeState)).NotTo(HaveOccurred())
			Expect(errors.IsNotFound(r.Get(ctx, types.NamespacedName{Name: nodeState.Name}, nodeState))).To(BeTrue())
		})
	})

	Context("renderDevicePluginConfigData", func() {
		It("should render device plugin config data when policies with the same resource name target different devices", func() {

			intelNode := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "intelNode", Labels: map[string]string{"node-role.kubernetes.io/worker": ""}}}
			mlxNode := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "mlxNode", Labels: map[string]string{"node-role.kubernetes.io/worker": ""}}}

			objs := []k8sclient.Object{
				intelNode,
				&sriovnetworkv1.SriovNetworkNodeState{ObjectMeta: metav1.ObjectMeta{Name: "intelNode", Namespace: testNamespace}, Status: sriovnetworkv1.SriovNetworkNodeStateStatus{
					Interfaces: sriovnetworkv1.InterfaceExts{
						{Driver: "ice", DeviceID: "159b", Vendor: "8086", PciAddress: "0000:31:00.0"},
					},
				}},
				mlxNode,
				&sriovnetworkv1.SriovNetworkNodeState{ObjectMeta: metav1.ObjectMeta{Name: "mlxNode", Namespace: testNamespace}, Status: sriovnetworkv1.SriovNetworkNodeStateStatus{
					Interfaces: sriovnetworkv1.InterfaceExts{
						{Driver: "mlx5_core", DeviceID: "101d", Vendor: "15b3", PciAddress: "0000:ca:00.0"},
					},
				}},
			}

			r := &SriovNetworkNodePolicyReconciler{Client: fake.NewClientBuilder().WithObjects(objs...).Build()}

			pl := &sriovnetworkv1.SriovNetworkNodePolicyList{
				Items: []sriovnetworkv1.SriovNetworkNodePolicy{
					{
						ObjectMeta: metav1.ObjectMeta{Name: "intel-vfio-pci"},
						Spec: sriovnetworkv1.SriovNetworkNodePolicySpec{
							ResourceName: "resource1",
							DeviceType:   "vfio-pci",
							NicSelector: sriovnetworkv1.SriovNetworkNicSelector{
								Vendor:      "8086",
								DeviceID:    "159b",
								RootDevices: []string{"0000:31:00.0"},
							},
							NumVfs:       128,
							NodeSelector: map[string]string{"node-role.kubernetes.io/worker": ""},
						},
					},
					{
						ObjectMeta: metav1.ObjectMeta{Name: "mellanox-rdma"},
						Spec: sriovnetworkv1.SriovNetworkNodePolicySpec{
							ResourceName: "resource1",
							DeviceType:   "netdevice",
							IsRdma:       true,
							NicSelector: sriovnetworkv1.SriovNetworkNicSelector{
								Vendor:      "15b3",
								DeviceID:    "101d",
								RootDevices: []string{"0000:ca:00.0"},
							},
							NumVfs:       128,
							NodeSelector: map[string]string{"node-role.kubernetes.io/worker": ""},
						},
					},
				},
			}
			rcl, err := r.renderDevicePluginConfigData(context.Background(), pl, mlxNode)
			Expect(err).ToNot(HaveOccurred())
			Expect(len(rcl.ResourceList)).To(Equal(1))
			Expect(rcl.ResourceList[0].ResourceName).To(Equal("resource1"))
			selectors := mustUnmarshallSelector(rcl.ResourceList[0].Selectors)
			Expect(selectors.Vendors).To(ContainElements("8086", "15b3"))
			Expect(selectors.RootDevices).To(ContainElements("0000:31:00.0", "0000:ca:00.0"))

			// Having drivers in the selector cause the device plugin to fail to select the mellanox devices in the mlxNode
			Expect(selectors.Drivers).To(BeEmpty())
		})

		It("should render device plugin config data when policies configure vfio-pci and netdevice", func() {
			node1 := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "node1", Labels: map[string]string{"node-role.kubernetes.io/worker": ""}}}
			objs := []k8sclient.Object{
				node1,
				&sriovnetworkv1.SriovNetworkNodeState{ObjectMeta: metav1.ObjectMeta{Name: "node1", Namespace: testNamespace}, Status: sriovnetworkv1.SriovNetworkNodeStateStatus{
					Interfaces: sriovnetworkv1.InterfaceExts{
						{Driver: "ice", DeviceID: "159b", Vendor: "8086", PciAddress: "0000:31:00.0", Name: "ens0"},
					},
				}},
			}

			r := &SriovNetworkNodePolicyReconciler{Client: fake.NewClientBuilder().WithObjects(objs...).Build()}

			pl := &sriovnetworkv1.SriovNetworkNodePolicyList{
				Items: []sriovnetworkv1.SriovNetworkNodePolicy{
					{
						ObjectMeta: metav1.ObjectMeta{Name: "intel-vfio-pci"},
						Spec: sriovnetworkv1.SriovNetworkNodePolicySpec{
							ResourceName: "resvfiopci",
							DeviceType:   "vfio-pci",
							NicSelector: sriovnetworkv1.SriovNetworkNicSelector{
								Vendor:   "8086",
								DeviceID: "159b",
								PfNames:  []string{"ens0#0-9"},
							},
							NumVfs:       128,
							NodeSelector: map[string]string{"node-role.kubernetes.io/worker": ""},
						},
					},
					{
						ObjectMeta: metav1.ObjectMeta{Name: "mellanox-rdma"},
						Spec: sriovnetworkv1.SriovNetworkNodePolicySpec{
							ResourceName: "resnetdevice",
							DeviceType:   "netdevice",
							NicSelector: sriovnetworkv1.SriovNetworkNicSelector{
								Vendor:   "8086",
								DeviceID: "159b",
								PfNames:  []string{"ens0#10-19"},
							},
							NumVfs:       128,
							NodeSelector: map[string]string{"node-role.kubernetes.io/worker": ""},
						},
					},
				},
			}
			rcl, err := r.renderDevicePluginConfigData(context.Background(), pl, node1)
			Expect(err).ToNot(HaveOccurred())
			Expect(len(rcl.ResourceList)).To(Equal(2))
			selectors := map[string]string{
				rcl.ResourceList[0].ResourceName: string(*rcl.ResourceList[0].Selectors),
				rcl.ResourceList[1].ResourceName: string(*rcl.ResourceList[1].Selectors),
			}
			Expect(selectors).To(HaveKeyWithValue("resvfiopci", `{"vendors":["8086"],"pfNames":["ens0#0-9"],"IsRdma":false,"NeedVhostNet":false}`))
			Expect(selectors).To(HaveKeyWithValue("resnetdevice", `{"vendors":["8086"],"pfNames":["ens0#10-19"],"IsRdma":false,"NeedVhostNet":false}`))
		})
	})
	Context("NodeState source revisions", func() {
		It("preserves NodeState configuration when pools conflict", func() {
			ctx := context.Background()
			node := testManagedNode("worker1")
			pool := &sriovnetworkv1.SriovNetworkPoolConfig{ObjectMeta: metav1.ObjectMeta{
				Name: "first-pool", Namespace: vars.Namespace,
			}}
			other := pool.DeepCopy()
			other.Name = "second-pool"
			state := &sriovnetworkv1.SriovNetworkNodeState{
				ObjectMeta: metav1.ObjectMeta{Name: node.Name, Namespace: vars.Namespace},
				Spec: sriovnetworkv1.SriovNetworkNodeStateSpec{System: sriovnetworkv1.System{
					RdmaMode: "shared", OvsConfig: map[string]string{"hw-offload": "true"},
				}},
				Status: sriovnetworkv1.SriovNetworkNodeStateStatus{
					Interfaces: sriovnetworkv1.InterfaceExts{{Name: "ens1", PciAddress: "0000:01:00.0"}},
				},
			}
			Expect(pool.SetRevisionOnObject(state)).To(Succeed())
			c := fake.NewClientBuilder().WithScheme(k8sClient.Scheme()).WithObjects(node, pool, other, state).Build()
			Expect(c.Get(ctx, k8sclient.ObjectKeyFromObject(state), state)).To(Succeed())
			before := state.DeepCopy()
			r := &SriovNetworkNodePolicyReconciler{Client: c, Scheme: c.Scheme()}
			err := r.syncAllSriovNetworkNodeStates(ctx, testOperatorConfig(),
				&sriovnetworkv1.SriovNetworkNodePolicyList{}, &corev1.NodeList{Items: []corev1.Node{*node}})
			Expect(c.Get(ctx, k8sclient.ObjectKeyFromObject(state), state)).To(Succeed())
			Expect(state).To(Equal(before))
			Expect(err).To(MatchError(ContainSubstring("failed to get SriovNetworkPoolConfig for node worker1")))
		})

		It("records current source revisions even when the rendered spec is unchanged", func() {
			ctx := context.Background()
			node := testManagedNode("worker1")
			policy := sriovnetworkv1.SriovNetworkNodePolicy{
				ObjectMeta: metav1.ObjectMeta{Name: "policy", Namespace: vars.Namespace},
				Spec: sriovnetworkv1.SriovNetworkNodePolicySpec{
					ResourceName: "resource", NumVfs: 2, NodeSelector: map[string]string{},
					NicSelector: sriovnetworkv1.SriovNetworkNicSelector{PfNames: []string{"ens1"}},
				},
			}
			pool := &sriovnetworkv1.SriovNetworkPoolConfig{ObjectMeta: metav1.ObjectMeta{
				Name: "pool", Namespace: vars.Namespace,
			}}
			state := &sriovnetworkv1.SriovNetworkNodeState{ObjectMeta: metav1.ObjectMeta{
				Name: node.Name, Namespace: vars.Namespace,
			}}
			state.Status.Interfaces = sriovnetworkv1.InterfaceExts{{Name: "ens1", PciAddress: "0000:01:00.0"}}
			config := testOperatorConfig()
			createAggregateObjects(node, config, &policy)
			createDrainOnlyPool(pool)
			status := state.Status
			createAggregateObjects(state)
			state.Status = status
			Expect(k8sClient.Status().Update(ctx, state)).To(Succeed())
			c := k8sClient
			r := &SriovNetworkNodePolicyReconciler{Client: c, Scheme: c.Scheme()}
			ns := &sriovnetworkv1.SriovNetworkNodeState{ObjectMeta: metav1.ObjectMeta{Name: node.Name, Namespace: vars.Namespace}}
			Expect(pool.SetRevisionOnObject(ns)).To(Succeed())
			policies := &sriovnetworkv1.SriovNetworkNodePolicyList{Items: []sriovnetworkv1.SriovNetworkNodePolicy{policy}}
			Expect(r.syncSriovNetworkNodeState(ctx, config, policies, ns, node)).To(Succeed())
			latest := &sriovnetworkv1.SriovNetworkNodeState{}
			Expect(c.Get(ctx, types.NamespacedName{Name: node.Name, Namespace: vars.Namespace}, latest)).To(Succeed())
			Expect(policy.RevisionMatchesInNodeState(latest)).To(BeTrue())
			Expect(pool.RevisionMatchesInObject(latest)).To(BeTrue())
			previousSpec := latest.DeepCopy().Spec
			previousGeneration := latest.Generation
			policy.Spec.Priority++
			Expect(c.Update(ctx, &policy)).To(Succeed())
			policies.Items[0] = policy
			Expect(r.syncSriovNetworkNodeState(ctx, config, policies, ns, node)).To(Succeed())
			Expect(c.Get(ctx, types.NamespacedName{Name: node.Name, Namespace: vars.Namespace}, latest)).To(Succeed())
			Expect(policy.RevisionMatchesInNodeState(latest)).To(BeTrue())
			Expect(latest.Spec).To(Equal(previousSpec))
			Expect(latest.Generation).To(Equal(previousGeneration))
		})

		It("replaces policy and pool membership", func() {
			ctx := context.Background()
			node := testManagedNode("worker1")
			first := sriovnetworkv1.SriovNetworkNodePolicy{
				ObjectMeta: metav1.ObjectMeta{Name: "first", Namespace: vars.Namespace},
				Spec: sriovnetworkv1.SriovNetworkNodePolicySpec{
					ResourceName: "first", NumVfs: 2, Priority: 99,
					NodeSelector: map[string]string{"policy-group": "a"},
					NicSelector:  sriovnetworkv1.SriovNetworkNicSelector{PfNames: []string{"ens1#0-0"}},
				},
			}
			second := *first.DeepCopy()
			second.Name, second.Spec.ResourceName = "second", "second"
			second.Spec.NicSelector.PfNames = []string{"ens1#1-1"}
			firstPool := &sriovnetworkv1.SriovNetworkPoolConfig{ObjectMeta: metav1.ObjectMeta{
				Name: "first-pool", Namespace: vars.Namespace,
			}}
			secondPool := firstPool.DeepCopy()
			secondPool.Name = "second-pool"
			state := &sriovnetworkv1.SriovNetworkNodeState{
				ObjectMeta: metav1.ObjectMeta{Name: node.Name, Namespace: vars.Namespace,
					Annotations: map[string]string{"unrelated": "preserved"}},
				Status: sriovnetworkv1.SriovNetworkNodeStateStatus{
					Interfaces: sriovnetworkv1.InterfaceExts{{Name: "ens1", PciAddress: "0000:01:00.0"}},
					Conditions: []metav1.Condition{{Type: sriovnetworkv1.ConditionReady, Status: metav1.ConditionTrue,
						Reason: sriovnetworkv1.ReasonNodeReady, ObservedGeneration: 1, LastTransitionTime: metav1.Now()}},
				},
			}
			config := testOperatorConfig()
			createAggregateObjects(node, config, &first, &second)
			createDrainOnlyPool(firstPool)
			createDrainOnlyPool(secondPool)
			status := state.Status
			createAggregateObjects(state)
			state.Status = status
			Expect(k8sClient.Status().Update(ctx, state)).To(Succeed())
			moved := *second.DeepCopy()
			moved.Spec.NodeSelector["policy-group"] = "b"
			c := k8sClient
			r := &SriovNetworkNodePolicyReconciler{Client: c, Scheme: c.Scheme()}
			steps := []struct {
				name      string
				group     string
				policies  []sriovnetworkv1.SriovNetworkNodePolicy
				pool      *sriovnetworkv1.SriovNetworkPoolConfig
				resources []string
			}{
				{"add first policy", "a", []sriovnetworkv1.SriovNetworkNodePolicy{first}, firstPool, []string{"first"}},
				{"add partition policy", "a", []sriovnetworkv1.SriovNetworkNodePolicy{first, second}, firstPool, []string{"first", "second"}},
				{"remove first policy", "a", []sriovnetworkv1.SriovNetworkNodePolicy{second}, firstPool, []string{"second"}},
				{"move node out of policy", "b", []sriovnetworkv1.SriovNetworkNodePolicy{second}, secondPool, nil},
				{"move to another policy and pool", "b", []sriovnetworkv1.SriovNetworkNodePolicy{first, moved}, secondPool, []string{"second"}},
				{"remove all policies and pool", "b", nil, nil, nil},
			}
			for _, step := range steps {
				By(step.name)
				if step.name == "move to another policy and pool" {
					Expect(c.Update(ctx, &moved)).To(Succeed())
					step.policies[1] = moved
				}
				node.Labels["policy-group"] = step.group
				Expect(c.Update(ctx, node)).To(Succeed())
				previousSpec := state.DeepCopy().Spec
				desired := &sriovnetworkv1.SriovNetworkNodeState{ObjectMeta: metav1.ObjectMeta{
					Name: node.Name, Namespace: vars.Namespace,
				}}
				Expect(step.pool.SetRevisionOnObject(desired)).To(Succeed())
				Expect(r.syncSriovNetworkNodeState(ctx, config,
					&sriovnetworkv1.SriovNetworkNodePolicyList{Items: step.policies}, desired, node)).To(Succeed())
				Expect(c.Get(ctx, types.NamespacedName{Name: node.Name, Namespace: vars.Namespace}, state)).To(Succeed())
				var resources []string
				for _, iface := range state.Spec.Interfaces {
					for _, group := range iface.VfGroups {
						resources = append(resources, group.ResourceName)
					}
				}
				slices.Sort(resources)
				Expect(resources).To(Equal(step.resources))
				for _, policy := range []sriovnetworkv1.SriovNetworkNodePolicy{first, second, moved} {
					want := slices.ContainsFunc(step.policies, func(current sriovnetworkv1.SriovNetworkNodePolicy) bool {
						return current.Name == policy.Name && current.Generation == policy.Generation && current.Selected(node)
					})
					Expect(policy.RevisionMatchesInNodeState(state)).To(Equal(want), "policy %s generation %d", policy.Name, policy.Generation)
				}
				if step.pool == nil {
					Expect(state.Annotations).NotTo(HaveKey(consts.PoolRevisionAnnotation))
				} else {
					Expect(step.pool.RevisionMatchesInObject(state)).To(BeTrue())
				}
				Expect(state.Annotations).To(HaveKeyWithValue("unrelated", "preserved"))
				specChanged := !equality.Semantic.DeepEqual(previousSpec, state.Spec)
				assertPolicies := func(applied bool) {
					for i := range step.policies {
						policy := &step.policies[i]
						var matched, ready int32
						wantStatus, reason := metav1.ConditionFalse, sriovnetworkv1.ReasonNoMatchingNodes
						if policy.Selected(node) {
							matched, reason = 1, sriovnetworkv1.ReasonApplyingConfiguration
							if applied {
								ready, wantStatus, reason = 1, metav1.ConditionTrue, sriovnetworkv1.ReasonPolicyReady
							}
						}
						result := policyStatus(ctx, c, policy)
						Expect(result.matched).To(Equal(&matched))
						Expect(result.ready).To(Equal(&ready))
						Expect(result.condition.Status).To(Equal(wantStatus))
						Expect(result.condition.Reason).To(Equal(reason))
						Expect(result.condition.ObservedGeneration).To(Equal(policy.Generation))
					}
				}
				assertPolicies(!specChanged)
				state.Status.Conditions[0].ObservedGeneration = state.Generation
				Expect(c.Status().Update(ctx, state)).To(Succeed())
				assertPolicies(true)
			}
		})

	})
})
