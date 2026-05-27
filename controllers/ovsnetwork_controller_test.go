package controllers

import (
	"context"
	"sync"
	"time"

	netattdefv1 "github.com/k8snetworkplumbingwg/network-attachment-definition-client/pkg/apis/k8s.cni.cncf.io/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	v1 "github.com/k8snetworkplumbingwg/sriov-network-operator/api/v1"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/status"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/test/util"
)

func getOvsNetworkCR() *v1.OVSNetwork {
	return &v1.OVSNetwork{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test",
			Namespace: testNamespace,
		},
		Spec: v1.OVSNetworkSpec{
			ResourceName: "test",
		},
	}
}

func removeOVSNetwork(ctx context.Context, cr *v1.OVSNetwork) {
	err := k8sClient.Delete(ctx, cr)
	if err != nil {
		ExpectWithOffset(1, errors.IsNotFound(err)).To(BeTrue())
	}
	EventuallyWithOffset(1, func(g Gomega) {
		g.Expect(errors.IsNotFound(
			k8sClient.Get(ctx, types.NamespacedName{
				Namespace: cr.Namespace,
				Name:      cr.Name}, &v1.OVSNetwork{}))).To(BeTrue())
	}, util.APITimeout, util.RetryInterval).Should(Succeed())
}

var _ = Describe("OVSNetwork Controller", Ordered, func() {
	var cancel context.CancelFunc
	var ctx context.Context

	BeforeAll(func() {
		By("Setup controller manager")
		k8sManager, err := setupK8sManagerForTest()
		Expect(err).NotTo(HaveOccurred())

		err = (&OVSNetworkReconciler{
			Client:        k8sManager.GetClient(),
			Scheme:        k8sManager.GetScheme(),
			StatusPatcher: status.NewPatcher(k8sManager.GetClient(), k8sManager.GetEventRecorder("test"), k8sManager.GetScheme(), "test"),
		}).SetupWithManager(k8sManager)
		Expect(err).NotTo(HaveOccurred())

		ctx, cancel = context.WithCancel(context.Background())

		wg := sync.WaitGroup{}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer GinkgoRecover()
			By("Start controller manager")
			err := k8sManager.Start(ctx)
			Expect(err).NotTo(HaveOccurred())
		}()

		DeferCleanup(func() {
			By("Shutdown controller manager")
			cancel()
			wg.Wait()
		})
	})

	Context("OVSNetwork", func() {
		It("create/delete net-att-def", func() {
			netCR := getOvsNetworkCR()

			By("Create OVSNetwork CR")
			Expect(k8sClient.Create(ctx, netCR)).NotTo(HaveOccurred())
			DeferCleanup(func() { removeOVSNetwork(ctx, netCR) })

			By("Check NetworkAttachmentDefinition is created")
			Eventually(func(g Gomega) {
				netAttDef := &netattdefv1.NetworkAttachmentDefinition{}
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "test", Namespace: testNamespace}, netAttDef)).NotTo(HaveOccurred())
				g.Expect(netAttDef.GetAnnotations()["k8s.v1.cni.cncf.io/resourceName"]).To(ContainSubstring("test"))
			}, util.APITimeout, util.RetryInterval).Should(Succeed())

			By("Remove OVSNetwork CR")
			Expect(k8sClient.Delete(ctx, netCR)).NotTo(HaveOccurred())

			By("Check NetworkAttachmentDefinition is removed")
			Eventually(func(g Gomega) {
				err := k8sClient.Get(ctx, types.NamespacedName{Name: "test",
					Namespace: testNamespace}, &netattdefv1.NetworkAttachmentDefinition{})
				g.Expect(errors.IsNotFound(err)).To(BeTrue())
			}, util.APITimeout, util.RetryInterval).Should(Succeed())
		})
		It("update net-att-def", func() {
			netCR := getOvsNetworkCR()

			By("Create OVSNetwork CR")
			Expect(k8sClient.Create(ctx, netCR)).NotTo(HaveOccurred())
			DeferCleanup(func() { removeOVSNetwork(ctx, netCR) })

			By("Check NetworkAttachmentDefinition is created")
			Eventually(func(g Gomega) {
				netAttDef := &netattdefv1.NetworkAttachmentDefinition{}
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "test",
					Namespace: testNamespace}, netAttDef)).NotTo(HaveOccurred())
			}, util.APITimeout, util.RetryInterval).Should(Succeed())

			By("Update OVSNetwork CR")
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: netCR.Name,
				Namespace: netCR.Namespace}, netCR)).NotTo(HaveOccurred())
			netCR.Spec.Vlan = 200
			Expect(k8sClient.Update(ctx, netCR)).NotTo(HaveOccurred())

			By("Check NetworkAttachmentDefinition is updated")
			Eventually(func(g Gomega) {
				netAttDef := &netattdefv1.NetworkAttachmentDefinition{}
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "test",
					Namespace: testNamespace}, netAttDef)).NotTo(HaveOccurred())
				g.Expect(netAttDef.Spec.Config).To(ContainSubstring(`"vlan": 200`))
			}, util.APITimeout, util.RetryInterval).Should(Succeed())
		})
		It("re-create net-att-def", func() {
			netCR := getOvsNetworkCR()

			By("Create OVSNetwork CR")
			Expect(k8sClient.Create(ctx, netCR)).NotTo(HaveOccurred())
			DeferCleanup(func() { removeOVSNetwork(ctx, netCR) })

			var origUID types.UID
			netAttDef := &netattdefv1.NetworkAttachmentDefinition{}
			By("Check NetworkAttachmentDefinition is created")
			Eventually(func(g Gomega) {
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "test",
					Namespace: testNamespace}, netAttDef)).NotTo(HaveOccurred())
				origUID = netAttDef.GetUID()
			}, util.APITimeout, util.RetryInterval).Should(Succeed())

			By("Remove NetworkAttachmentDefinition CR")
			Expect(k8sClient.Delete(ctx, netAttDef)).NotTo(HaveOccurred())

			By("Check NetworkAttachmentDefinition is recreated")
			Eventually(func(g Gomega) {
				netAttDef := &netattdefv1.NetworkAttachmentDefinition{}
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "test",
					Namespace: testNamespace}, netAttDef)).NotTo(HaveOccurred())
				g.Expect(netAttDef.GetUID()).NotTo(Equal(origUID))
			}, util.APITimeout, util.RetryInterval).Should(Succeed())
		})
		It("namespace is not yet created", func() {
			newNSName := "test-ns"
			netCR := getOvsNetworkCR()
			netCR.Spec.NetworkNamespace = newNSName

			By("Create OVSNetwork CR")
			Expect(k8sClient.Create(ctx, netCR)).NotTo(HaveOccurred())
			DeferCleanup(func() { removeOVSNetwork(ctx, netCR) })

			// Sleep 3 seconds to be sure the Reconcile loop has been invoked. This can be improved by exposing some information (e.g. the error)
			// in the SriovNetwork.Status field.
			time.Sleep(3 * time.Second)

			By("Create Namespace")
			nsObj := &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{Name: newNSName},
			}
			Expect(k8sClient.Create(ctx, nsObj)).NotTo(HaveOccurred())
			DeferCleanup(func() {
				Expect(k8sClient.Delete(ctx, nsObj)).NotTo(HaveOccurred())
			})

			By("Check NetworkAttachmentDefinition is created")
			Eventually(func(g Gomega) {
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "test", Namespace: newNSName},
					&netattdefv1.NetworkAttachmentDefinition{})).NotTo(HaveOccurred())
			}, util.APITimeout, util.RetryInterval).Should(Succeed())
		})
	})

	Context("conditions", func() {
		It("should set Ready=True when network is successfully provisioned", func() {
			netCR := &v1.OVSNetwork{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "ovs-cond-ready",
					Namespace: testNamespace,
				},
				Spec: v1.OVSNetworkSpec{
					ResourceName: "ovs_resource_cond",
				},
			}

			Expect(k8sClient.Create(ctx, netCR)).NotTo(HaveOccurred())
			DeferCleanup(func() { removeOVSNetwork(ctx, netCR) })

			Eventually(func(g Gomega) {
				network := &v1.OVSNetwork{}
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: netCR.Name, Namespace: testNamespace}, network)).To(Succeed())

				// Verify Ready condition
				readyCondition := findCondition(network.Status.Conditions, v1.ConditionReady)
				g.Expect(readyCondition).ToNot(BeNil())
				g.Expect(readyCondition.Status).To(Equal(metav1.ConditionTrue))
				g.Expect(readyCondition.Reason).To(Equal(v1.ReasonNetworkReady))
				g.Expect(readyCondition.Message).To(ContainSubstring("NetworkAttachmentDefinition is provisioned"))

			}, util.APITimeout, util.RetryInterval).Should(Succeed())
		})

		It("should set Ready=False when target namespace does not exist", func() {
			netCR := &v1.OVSNetwork{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "ovs-cond-degraded",
					Namespace: testNamespace,
				},
				Spec: v1.OVSNetworkSpec{
					NetworkNamespace: "ovs-non-existent-ns-degraded",
					ResourceName:     "ovs_resource_cond_degraded",
				},
			}

			Expect(k8sClient.Create(ctx, netCR)).NotTo(HaveOccurred())
			DeferCleanup(func() { removeOVSNetwork(ctx, netCR) })

			// Wait for conditions to be set
			time.Sleep(2 * time.Second)

			Eventually(func(g Gomega) {
				network := &v1.OVSNetwork{}
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: netCR.Name, Namespace: testNamespace}, network)).To(Succeed())

				// Verify Ready condition is False (waiting for namespace)
				readyCondition := findCondition(network.Status.Conditions, v1.ConditionReady)
				g.Expect(readyCondition).ToNot(BeNil())
				g.Expect(readyCondition.Status).To(Equal(metav1.ConditionFalse))
				g.Expect(readyCondition.Reason).To(Equal(v1.ReasonNamespaceNotFound))
				g.Expect(readyCondition.Message).To(ContainSubstring("does not exist"))
			}, util.APITimeout, util.RetryInterval).Should(Succeed())
		})

		It("should have consistent observedGeneration in conditions", func() {
			netCR := &v1.OVSNetwork{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "ovs-cond-gen",
					Namespace: testNamespace,
				},
				Spec: v1.OVSNetworkSpec{
					ResourceName: "ovs_resource_cond_gen",
				},
			}

			Expect(k8sClient.Create(ctx, netCR)).NotTo(HaveOccurred())
			DeferCleanup(func() { removeOVSNetwork(ctx, netCR) })

			Eventually(func(g Gomega) {
				network := &v1.OVSNetwork{}
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: netCR.Name, Namespace: testNamespace}, network)).To(Succeed())

				readyCondition := findCondition(network.Status.Conditions, v1.ConditionReady)
				g.Expect(readyCondition).ToNot(BeNil())
				// And it should match the current generation
				g.Expect(readyCondition.ObservedGeneration).To(Equal(network.Generation))
			}, util.APITimeout, util.RetryInterval).Should(Succeed())
		})

		It("should set Ready=False when app network sets NetworkNamespace from non-operator namespace", func() {
			appNs := "ovs-app-ns-cross"
			// Create application namespace
			nsObj := &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{Name: appNs},
			}
			Expect(k8sClient.Create(ctx, nsObj)).NotTo(HaveOccurred())
			DeferCleanup(func() {
				Expect(k8sClient.Delete(ctx, nsObj)).NotTo(HaveOccurred())
			})

			// Network in application namespace with cross-namespace target - should not be Ready
			netCR := &v1.OVSNetwork{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "ovs-app-cross",
					Namespace: appNs,
				},
				Spec: v1.OVSNetworkSpec{
					NetworkNamespace: "default", // Different from its own namespace
					ResourceName:     "ovs_resource_cross",
				},
			}

			Expect(k8sClient.Create(ctx, netCR)).NotTo(HaveOccurred())
			DeferCleanup(func() {
				Expect(k8sClient.Delete(ctx, netCR)).To(Succeed())
			})

			Eventually(func(g Gomega) {
				network := &v1.OVSNetwork{}
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: netCR.Name, Namespace: appNs}, network)).To(Succeed())

				readyCondition := findCondition(network.Status.Conditions, v1.ConditionReady)
				g.Expect(readyCondition).ToNot(BeNil())
				g.Expect(readyCondition.Status).To(Equal(metav1.ConditionFalse))
				g.Expect(readyCondition.Message).To(ContainSubstring("networkNamespace cannot be set"))
			}, util.APITimeout, util.RetryInterval).Should(Succeed())
		})

		It("should set Ready=False on app network when operator network already owns the NAD with same name", func() {
			targetNs := "ovs-target-ns-conflict"
			nadName := "ovs-test-nad-conflict"

			// Create target namespace
			nsObj := &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{Name: targetNs},
			}
			Expect(k8sClient.Create(ctx, nsObj)).NotTo(HaveOccurred())
			DeferCleanup(func() {
				Expect(k8sClient.Delete(ctx, nsObj)).NotTo(HaveOccurred())
			})

			// Network 1: In operator namespace, targeting target-ns
			netCROperator := &v1.OVSNetwork{
				ObjectMeta: metav1.ObjectMeta{
					Name:      nadName,
					Namespace: testNamespace,
				},
				Spec: v1.OVSNetworkSpec{
					NetworkNamespace: targetNs,
					ResourceName:     "ovs_resource_op",
				},
			}

			// Network 2: In target namespace with SAME NAME
			netCRApp := &v1.OVSNetwork{
				ObjectMeta: metav1.ObjectMeta{
					Name:      nadName,
					Namespace: targetNs,
				},
				Spec: v1.OVSNetworkSpec{
					ResourceName: "ovs_resource_app",
				},
			}

			// Create operator network first
			Expect(k8sClient.Create(ctx, netCROperator)).NotTo(HaveOccurred())
			DeferCleanup(func() { removeOVSNetwork(ctx, netCROperator) })

			// Wait for NAD to be created by operator network
			Eventually(func(g Gomega) {
				netAttDef := &netattdefv1.NetworkAttachmentDefinition{}
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: nadName, Namespace: targetNs}, netAttDef)).NotTo(HaveOccurred())
			}, util.APITimeout, util.RetryInterval).Should(Succeed())

			// Network from operator namespace should be Ready
			Eventually(func(g Gomega) {
				network := &v1.OVSNetwork{}
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: netCROperator.Name, Namespace: testNamespace}, network)).To(Succeed())

				readyCondition := findCondition(network.Status.Conditions, v1.ConditionReady)
				g.Expect(readyCondition).ToNot(BeNil())
				g.Expect(readyCondition.Status).To(Equal(metav1.ConditionTrue))
			}, util.APITimeout, util.RetryInterval).Should(Succeed())

			// Now create app network with same name - should not be Ready
			Expect(k8sClient.Create(ctx, netCRApp)).NotTo(HaveOccurred())
			DeferCleanup(func() {
				Expect(k8sClient.Delete(ctx, netCRApp)).To(Succeed())
			})

			// Network from app namespace should not be Ready (NAD already exists and is owned by another network)
			Eventually(func(g Gomega) {
				network := &v1.OVSNetwork{}
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: netCRApp.Name, Namespace: targetNs}, network)).To(Succeed())

				readyCondition := findCondition(network.Status.Conditions, v1.ConditionReady)
				g.Expect(readyCondition).ToNot(BeNil())
				g.Expect(readyCondition.Status).To(Equal(metav1.ConditionFalse))
			}, util.APITimeout, util.RetryInterval).Should(Succeed())

			// Verify NAD still exists and is owned by operator network (not overwritten)
			Eventually(func(g Gomega) {
				netAttDef := &netattdefv1.NetworkAttachmentDefinition{}
				g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: nadName, Namespace: targetNs}, netAttDef)).NotTo(HaveOccurred())
				g.Expect(netAttDef.GetAnnotations()["k8s.v1.cni.cncf.io/resourceName"]).To(ContainSubstring("ovs_resource_op"))
			}, util.APITimeout, util.RetryInterval).Should(Succeed())
		})
	})
})
