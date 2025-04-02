package controllers

import (
	"context"
	"encoding/json"
	"os"
	"sync"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/google/go-cmp/cmp"
	dptypes "github.com/k8snetworkplumbingwg/sriov-network-device-plugin/pkg/types"
	corev1 "k8s.io/api/core/v1"
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
})
