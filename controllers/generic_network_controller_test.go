package controllers

import (
	"context"
	"sync"
	"time"

	netattdefv1 "github.com/k8snetworkplumbingwg/network-attachment-definition-client/pkg/apis/k8s.cni.cncf.io/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	sriovnetworkv1 "github.com/k8snetworkplumbingwg/sriov-network-operator/api/v1"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/consts"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/test/util"
)

var _ = Describe("All Network Controllers", Ordered, func() {
	var cancel context.CancelFunc
	var ctx context.Context

	BeforeAll(func() {
		By("Setup controller manager")
		k8sManager, err := setupK8sManagerForTest()
		Expect(err).ToNot(HaveOccurred())

		err = (&SriovNetworkReconciler{
			Client: k8sManager.GetClient(),
			Scheme: k8sManager.GetScheme(),
		}).SetupWithManager(k8sManager)
		Expect(err).ToNot(HaveOccurred())

		err = (&SriovIBNetworkReconciler{
			Client: k8sManager.GetClient(),
			Scheme: k8sManager.GetScheme(),
		}).SetupWithManager(k8sManager)
		Expect(err).ToNot(HaveOccurred())

		err = (&OVSNetworkReconciler{
			Client: k8sManager.GetClient(),
			Scheme: k8sManager.GetScheme(),
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
			By("Shutdown controller manager")
			cancel()
			wg.Wait()
		})
	})

	Context("owner-reference annotations", func() {
		AfterEach(func() {
			cleanNetworksInNamespace(testNamespace)
			cleanNetworksInNamespace("default")
		})

		It("applies to new netattachdef", func() {
			cr := sriovnetworkv1.SriovNetwork{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "sriovnet-blue",
					Namespace: testNamespace,
				},
				Spec: sriovnetworkv1.SriovNetworkSpec{
					NetworkNamespace: "default",
				},
			}

			err := k8sClient.Create(ctx, &cr)
			Expect(err).NotTo(HaveOccurred())

			netAttDef := &netattdefv1.NetworkAttachmentDefinition{}
			err = util.WaitForNamespacedObject(netAttDef, k8sClient, "default", cr.GetName(), util.RetryInterval, util.Timeout)
			Expect(err).NotTo(HaveOccurred())

			Expect(netAttDef.GetAnnotations()).To(HaveKeyWithValue(consts.OwnerRefAnnotation, "SriovNetwork.sriovnetwork.openshift.io/openshift-sriov-network-operator/sriovnet-blue"))
		})

		It("should migrate existing NetAttachDef to have a value for Owner annotation", func() {
			netAttachDef := netattdefv1.NetworkAttachmentDefinition{
				ObjectMeta: metav1.ObjectMeta{Name: "netuser", Namespace: "default"},
				Spec: netattdefv1.NetworkAttachmentDefinitionSpec{
					Config: "user created configration, to be overridden",
				},
			}
			err := k8sClient.Create(ctx, &netAttachDef)
			Expect(err).NotTo(HaveOccurred())

			cr := sriovnetworkv1.SriovNetwork{
				ObjectMeta: metav1.ObjectMeta{Name: "netuser", Namespace: testNamespace},
				Spec:       sriovnetworkv1.SriovNetworkSpec{NetworkNamespace: "default"},
			}
			err = k8sClient.Create(ctx, &cr)
			Expect(err).NotTo(HaveOccurred())

			Eventually(func(g Gomega) {
				netAttDef := &netattdefv1.NetworkAttachmentDefinition{}
				err = util.WaitForNamespacedObject(netAttDef, k8sClient, "default", cr.GetName(), util.RetryInterval, util.Timeout)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(netAttDef.GetAnnotations()).
					To(HaveKeyWithValue(consts.OwnerRefAnnotation, "SriovNetwork.sriovnetwork.openshift.io/openshift-sriov-network-operator/netuser"))
				g.Expect(netAttDef.Spec.Config).To(Equal(generateExpectedNetConfig(&cr)))
			}).WithPolling(30 * time.Millisecond).WithTimeout(300 * time.Millisecond).Should(Succeed())
		})

		Context("does not override the NetAttachDefinition if the Owner annotation does not match", func() {
			It("when using different network type with the same name", func() {
				cr := sriovnetworkv1.SriovNetwork{
					ObjectMeta: metav1.ObjectMeta{Name: "net1", Namespace: testNamespace},
					Spec:       sriovnetworkv1.SriovNetworkSpec{NetworkNamespace: "default"},
				}
				err := k8sClient.Create(ctx, &cr)
				Expect(err).NotTo(HaveOccurred())

				netAttDef := &netattdefv1.NetworkAttachmentDefinition{}
				err = util.WaitForNamespacedObject(netAttDef, k8sClient, "default", cr.GetName(), util.RetryInterval, util.Timeout)
				Expect(err).NotTo(HaveOccurred())

				cr2 := sriovnetworkv1.SriovIBNetwork{
					ObjectMeta: metav1.ObjectMeta{Name: "net1", Namespace: testNamespace},
					Spec:       sriovnetworkv1.SriovIBNetworkSpec{NetworkNamespace: "default"},
				}
				err = k8sClient.Create(ctx, &cr2)
				Expect(err).NotTo(HaveOccurred())

				Consistently(func(g Gomega) {
					netAttDef := &netattdefv1.NetworkAttachmentDefinition{}
					err = util.WaitForNamespacedObject(netAttDef, k8sClient, "default", cr.GetName(), util.RetryInterval, util.Timeout)
					g.Expect(err).NotTo(HaveOccurred())
					g.Expect(netAttDef.Spec.Config).To(ContainSubstring(`"sriov"`))
					g.Expect(netAttDef.Spec.Config).ToNot(ContainSubstring(`"ib-sriov"`))
				}).WithPolling(30 * time.Millisecond).WithTimeout(300 * time.Millisecond).Should(Succeed())
			})

			It("when using the same network type with the same name, in different namespaces", func() {
				cr := sriovnetworkv1.SriovNetwork{
					ObjectMeta: metav1.ObjectMeta{Name: "net2", Namespace: testNamespace},
					Spec: sriovnetworkv1.SriovNetworkSpec{
						NetworkNamespace: "default",
						MinTxRate:        ptr.To(42),
					},
				}
				err := k8sClient.Create(ctx, &cr)
				Expect(err).NotTo(HaveOccurred())

				netAttDef := &netattdefv1.NetworkAttachmentDefinition{}
				err = util.WaitForNamespacedObject(netAttDef, k8sClient, "default", cr.GetName(), util.RetryInterval, util.Timeout)
				Expect(err).NotTo(HaveOccurred())

				cr2 := sriovnetworkv1.SriovNetwork{
					ObjectMeta: metav1.ObjectMeta{Name: "net2", Namespace: "default"},
					Spec: sriovnetworkv1.SriovNetworkSpec{
						MinTxRate: ptr.To(84),
					},
				}
				err = k8sClient.Create(ctx, &cr2)
				Expect(err).NotTo(HaveOccurred())

				Consistently(func(g Gomega) {
					netAttDef := &netattdefv1.NetworkAttachmentDefinition{}
					err = util.WaitForNamespacedObject(netAttDef, k8sClient, "default", cr.GetName(), util.RetryInterval, util.Timeout)
					g.Expect(err).NotTo(HaveOccurred())
					g.Expect(netAttDef.Spec.Config).To(ContainSubstring(`"min_tx_rate": 42`))
					g.Expect(netAttDef.Spec.Config).ToNot(ContainSubstring(`"min_tx_rate": 84`))
				}).WithPolling(30 * time.Millisecond).WithTimeout(300 * time.Millisecond).Should(Succeed())
			})
		})
	})

})

func cleanNetworksInNamespace(namespace string) {
	ctx := context.Background()
	EventuallyWithOffset(1, func(g Gomega) {
		err := k8sClient.DeleteAllOf(ctx, &sriovnetworkv1.SriovNetwork{}, client.InNamespace(namespace))
		g.Expect(err).NotTo(HaveOccurred())

		err = k8sClient.DeleteAllOf(ctx, &sriovnetworkv1.SriovIBNetwork{}, client.InNamespace(namespace))
		g.Expect(err).NotTo(HaveOccurred())

		err = k8sClient.DeleteAllOf(ctx, &sriovnetworkv1.OVSNetwork{}, client.InNamespace(namespace))
		g.Expect(err).NotTo(HaveOccurred())

		k8sClient.DeleteAllOf(ctx, &netattdefv1.NetworkAttachmentDefinition{}, client.InNamespace(namespace))
		g.Expect(err).NotTo(HaveOccurred())

		sriovNetworks := &sriovnetworkv1.SriovNetworkList{}
		err = k8sClient.List(ctx, sriovNetworks, client.InNamespace(namespace))
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(sriovNetworks.Items).To(BeEmpty())

		sriovIBNetworks := &sriovnetworkv1.SriovIBNetworkList{}
		err = k8sClient.List(ctx, sriovIBNetworks, client.InNamespace(namespace))
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(sriovIBNetworks.Items).To(BeEmpty())

		ovsNetworks := &sriovnetworkv1.OVSNetworkList{}
		err = k8sClient.List(ctx, ovsNetworks, client.InNamespace(namespace))
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(ovsNetworks.Items).To(BeEmpty())

		netAttachDefs := &netattdefv1.NetworkAttachmentDefinitionList{}
		err = k8sClient.List(ctx, netAttachDefs, client.InNamespace(namespace))
		g.Expect(err).NotTo(HaveOccurred())
		g.Expect(netAttachDefs.Items).To(BeEmpty())
	}).WithPolling(100 * time.Millisecond).WithTimeout(10 * time.Second).Should(Succeed())
}
