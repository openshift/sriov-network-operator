package controllers

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/google/go-cmp/cmp"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	k8sclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	dptypes "github.com/k8snetworkplumbingwg/sriov-network-device-plugin/pkg/types"

	sriovnetworkv1 "github.com/k8snetworkplumbingwg/sriov-network-operator/api/v1"
	v1 "github.com/k8snetworkplumbingwg/sriov-network-operator/api/v1"
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
				Spec: v1.SriovNetworkNodePolicySpec{
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
				Spec: v1.SriovNetworkNodePolicySpec{
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
				Spec: v1.SriovNetworkNodePolicySpec{
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
		err := k8sClient.DeleteAllOf(context.Background(), &corev1.Node{})
		Expect(err).ToNot(HaveOccurred())

		err = k8sClient.DeleteAllOf(context.Background(), &sriovnetworkv1.SriovNetworkNodePolicy{}, k8sclient.InNamespace(vars.Namespace))
		Expect(err).ToNot(HaveOccurred())

		err = k8sClient.DeleteAllOf(context.Background(), &sriovnetworkv1.SriovNetworkNodeState{}, k8sclient.InNamespace(vars.Namespace))
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
	})
})
