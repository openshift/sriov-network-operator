package controllers

import (
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	netattdefv1 "github.com/k8snetworkplumbingwg/network-attachment-definition-client/pkg/apis/k8s.cni.cncf.io/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	dynclient "sigs.k8s.io/controller-runtime/pkg/client"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	sriovnetworkv1 "github.com/k8snetworkplumbingwg/sriov-network-operator/api/v1"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/status"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/test/util"
)

const (
	on         = "on"
	emptyCurls = "{}"
)

var _ = Describe("SriovNetwork Controller", Ordered, func() {
	var cancel context.CancelFunc
	var ctx context.Context

	BeforeAll(func() {
		By("Setup controller manager")
		k8sManager, err := setupK8sManagerForTest()
		Expect(err).ToNot(HaveOccurred())

		err = (&SriovNetworkReconciler{
			Client:        k8sManager.GetClient(),
			Scheme:        k8sManager.GetScheme(),
			StatusPatcher: status.NewPatcher(k8sManager.GetClient(), k8sManager.GetEventRecorder("test"), k8sManager.GetScheme(), "test"),
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

	Context("with SriovNetwork", func() {
		specs := map[string]sriovnetworkv1.SriovNetworkSpec{
			"test-0": {
				ResourceName: "resource_1",
				IPAM:         `{"type":"host-local","subnet":"10.56.217.0/24","rangeStart":"10.56.217.171","rangeEnd":"10.56.217.181","routes":[{"dst":"0.0.0.0/0"}],"gateway":"10.56.217.1"}`,
				Vlan:         100,
				VlanQoS:      5,
				VlanProto:    "802.1ad",
			},
			"test-1": {
				ResourceName:     "resource_1",
				IPAM:             `{"type":"host-local","subnet":"10.56.217.0/24","rangeStart":"10.56.217.171","rangeEnd":"10.56.217.181","routes":[{"dst":"0.0.0.0/0"}],"gateway":"10.56.217.1"}`,
				NetworkNamespace: "default",
			},
			"test-2": {
				ResourceName: "resource_1",
				IPAM:         `{"type":"host-local","subnet":"10.56.217.0/24","rangeStart":"10.56.217.171","rangeEnd":"10.56.217.181","routes":[{"dst":"0.0.0.0/0"}],"gateway":"10.56.217.1"}`,
				SpoofChk:     on,
			},
			"test-3": {
				ResourceName: "resource_1",
				IPAM:         `{"type":"host-local","subnet":"10.56.217.0/24","rangeStart":"10.56.217.171","rangeEnd":"10.56.217.181","routes":[{"dst":"0.0.0.0/0"}],"gateway":"10.56.217.1"}`,
				Trust:        on,
			},
			"test-4": {
				ResourceName: "resource_1",
				IPAM:         `{"type":"host-local","subnet":"10.56.217.0/24","rangeStart":"10.56.217.171","rangeEnd":"10.56.217.181","routes":[{"dst":"0.0.0.0/0"}],"gateway":"10.56.217.1"}`,
			},
			"test-5": {
				ResourceName: "resource_1",
				IPAM:         `{"type":"host-local","subnet":"10.56.217.0/24","rangeStart":"10.56.217.171","rangeEnd":"10.56.217.181","routes":[{"dst":"0.0.0.0/0"}],"gateway":"10.56.217.1"}`,
				LogLevel:     "debug",
				LogFile:      "/tmp/tmpfile",
			},
		}
		sriovnets := util.GenerateSriovNetworkCRs(testNamespace, specs)
		DescribeTable("should be possible to create/delete net-att-def",
			func(cr sriovnetworkv1.SriovNetwork) {
				var err error
				expect := generateExpectedNetConfig(&cr)

				By("Create the SriovNetwork Custom Resource")
				// get global framework variables
				err = k8sClient.Create(ctx, &cr)
				Expect(err).NotTo(HaveOccurred())
				ns := testNamespace
				if cr.Spec.NetworkNamespace != "" {
					ns = cr.Spec.NetworkNamespace
				}
				netAttDef := &netattdefv1.NetworkAttachmentDefinition{}
				err = util.WaitForNamespacedObject(netAttDef, k8sClient, ns, cr.GetName(), util.RetryInterval, util.Timeout)
				Expect(err).NotTo(HaveOccurred())
				anno := netAttDef.GetAnnotations()

				Expect(anno["k8s.v1.cni.cncf.io/resourceName"]).To(Equal("openshift.io/" + cr.Spec.ResourceName))
				Expect(strings.TrimSpace(netAttDef.Spec.Config)).To(Equal(expect))

				By("Delete the SriovNetwork Custom Resource")
				found := &sriovnetworkv1.SriovNetwork{}
				err = k8sClient.Get(ctx, types.NamespacedName{Namespace: cr.GetNamespace(), Name: cr.GetName()}, found)
				Expect(err).NotTo(HaveOccurred())
				err = k8sClient.Delete(ctx, found, []dynclient.DeleteOption{}...)
				Expect(err).NotTo(HaveOccurred())

				netAttDef = &netattdefv1.NetworkAttachmentDefinition{}
				err = util.WaitForNamespacedObjectDeleted(netAttDef, k8sClient, ns, cr.GetName(), util.RetryInterval, util.Timeout)
				Expect(err).NotTo(HaveOccurred())
			},
			Entry("with vlan, vlanQoS and vlanProto flag", sriovnets["test-0"]),
			Entry("with networkNamespace flag", sriovnets["test-1"]),
			Entry("with SpoofChk flag on", sriovnets["test-2"]),
			Entry("with Trust flag on", sriovnets["test-3"]),
			Entry("with LogLevel and LogFile", sriovnets["test-5"]),
		)

		newSpecs := map[string]sriovnetworkv1.SriovNetworkSpec{
			"new-0": {
				ResourceName: "resource_1",
				IPAM:         `{"type":"dhcp"}`,
				Vlan:         200,
				VlanProto:    "802.1q",
			},
			"new-1": {
				ResourceName: "resource_1",
				IPAM:         `{"type":"host-local","subnet":"10.56.217.0/24","rangeStart":"10.56.217.171","rangeEnd":"10.56.217.181","routes":[{"dst":"0.0.0.0/0"}],"gateway":"10.56.217.1"}`,
			},
			"new-2": {
				ResourceName: "resource_1",
				IPAM:         `{"type":"host-local","subnet":"10.56.217.0/24","rangeStart":"10.56.217.171","rangeEnd":"10.56.217.181","routes":[{"dst":"0.0.0.0/0"}],"gateway":"10.56.217.1"}`,
				SpoofChk:     on,
			},
			"new-3": {
				ResourceName: "resource_1",
				IPAM:         `{"type":"host-local","subnet":"10.56.217.0/24","rangeStart":"10.56.217.171","rangeEnd":"10.56.217.181","routes":[{"dst":"0.0.0.0/0"}],"gateway":"10.56.217.1"}`,
				Trust:        on,
			},
		}
		newsriovnets := util.GenerateSriovNetworkCRs(testNamespace, newSpecs)

		DescribeTable("should be possible to update net-att-def",
			func(old, new sriovnetworkv1.SriovNetwork) {
				old.Name = new.GetName()
				err := k8sClient.Create(ctx, &old)
				defer func() {
					// Cleanup the test resource
					Expect(k8sClient.Delete(ctx, &old)).To(Succeed())
				}()
				Expect(err).NotTo(HaveOccurred())
				found := &sriovnetworkv1.SriovNetwork{}
				expect := generateExpectedNetConfig(&new)

				retryErr := retry.RetryOnConflict(retry.DefaultRetry, func() error {
					// Retrieve the latest version of SriovNetwork before attempting update
					// RetryOnConflict uses exponential backoff to avoid exhausting the apiserver
					getErr := k8sClient.Get(ctx, types.NamespacedName{Namespace: old.GetNamespace(), Name: old.GetName()}, found)
					if getErr != nil {
						io.WriteString(GinkgoWriter, fmt.Sprintf("Failed to get latest version of SriovNetwork: %v", getErr))
					}
					found.Spec = new.Spec
					found.Annotations = new.Annotations
					updateErr := k8sClient.Update(ctx, found)
					if getErr != nil {
						io.WriteString(GinkgoWriter, fmt.Sprintf("Failed to update latest version of SriovNetwork: %v", getErr))
					}
					return updateErr
				})
				if retryErr != nil {
					Fail(fmt.Sprintf("Update failed: %v", retryErr))
				}

				ns := testNamespace
				if new.Spec.NetworkNamespace != "" {
					ns = new.Spec.NetworkNamespace
				}

				time.Sleep(time.Second * 2)
				netAttDef := &netattdefv1.NetworkAttachmentDefinition{}
				err = util.WaitForNamespacedObject(netAttDef, k8sClient, ns, old.GetName(), util.RetryInterval, util.Timeout)
				Expect(err).NotTo(HaveOccurred())
				anno := netAttDef.GetAnnotations()

				Expect(anno["k8s.v1.cni.cncf.io/resourceName"]).To(Equal("openshift.io/" + new.Spec.ResourceName))
				Expect(strings.TrimSpace(netAttDef.Spec.Config)).To(Equal(expect))
			},
			Entry("with vlan and proto flag and ipam updated", sriovnets["test-4"], newsriovnets["new-0"]),
			Entry("with networkNamespace flag", sriovnets["test-4"], newsriovnets["new-1"]),
			Entry("with SpoofChk flag on", sriovnets["test-4"], newsriovnets["new-2"]),
			Entry("with Trust flag on", sriovnets["test-4"], newsriovnets["new-3"]),
		)

		Context("When a derived net-att-def CR is removed", func() {
			It("should regenerate the net-att-def CR", func() {
				cr := sriovnetworkv1.SriovNetwork{
					TypeMeta: metav1.TypeMeta{
						Kind:       "SriovNetwork",
						APIVersion: "sriovnetwork.openshift.io/v1",
					},
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-5",
						Namespace: testNamespace,
					},
					Spec: sriovnetworkv1.SriovNetworkSpec{
						NetworkNamespace: "default",
						ResourceName:     "resource_1",
						IPAM:             `{"type":"dhcp"}`,
						Vlan:             200,
					},
				}
				var err error
				expect := generateExpectedNetConfig(&cr)

				err = k8sClient.Create(ctx, &cr)
				Expect(err).NotTo(HaveOccurred())
				ns := testNamespace
				if cr.Spec.NetworkNamespace != "" {
					ns = cr.Spec.NetworkNamespace
				}
				netAttDef := &netattdefv1.NetworkAttachmentDefinition{}
				err = util.WaitForNamespacedObject(netAttDef, k8sClient, ns, cr.GetName(), util.RetryInterval, util.Timeout)
				Expect(err).NotTo(HaveOccurred())

				err = k8sClient.Delete(ctx, netAttDef)
				Expect(err).NotTo(HaveOccurred())
				time.Sleep(3 * time.Second)
				err = util.WaitForNamespacedObject(netAttDef, k8sClient, ns, cr.GetName(), util.RetryInterval, util.Timeout)
				Expect(err).NotTo(HaveOccurred())
				anno := netAttDef.GetAnnotations()
				Expect(anno["k8s.v1.cni.cncf.io/resourceName"]).To(Equal("openshift.io/" + cr.Spec.ResourceName))
				Expect(strings.TrimSpace(netAttDef.Spec.Config)).To(Equal(expect))

				found := &sriovnetworkv1.SriovNetwork{}
				err = k8sClient.Get(ctx, types.NamespacedName{Namespace: cr.GetNamespace(), Name: cr.GetName()}, found)
				Expect(err).NotTo(HaveOccurred())
				err = k8sClient.Delete(ctx, found, []dynclient.DeleteOption{}...)
				Expect(err).NotTo(HaveOccurred())
			})
		})

		Context("When the target NetworkNamespace doesn't exists", func() {
			It("should create the NetAttachDef when the namespace is created", func() {
				cr := sriovnetworkv1.SriovNetwork{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-missing-namespace",
						Namespace: testNamespace,
					},
					Spec: sriovnetworkv1.SriovNetworkSpec{
						NetworkNamespace: "ns-xxx",
						ResourceName:     "resource_missing_namespace",
						IPAM:             `{"type":"dhcp"}`,
						Vlan:             200,
					},
				}
				var err error
				expect := generateExpectedNetConfig(&cr)

				err = k8sClient.Create(ctx, &cr)
				Expect(err).NotTo(HaveOccurred())

				DeferCleanup(func() {
					err = k8sClient.Delete(ctx, &cr)
					Expect(err).NotTo(HaveOccurred())
				})

				// Sleep 3 seconds to be sure the Reconcile loop has been invoked. This can be improved by exposing some information (e.g. the error)
				// in the SriovNetwork.Status field.
				time.Sleep(3 * time.Second)

				netAttDef := &netattdefv1.NetworkAttachmentDefinition{}
				err = k8sClient.Get(ctx, types.NamespacedName{Name: cr.GetName(), Namespace: "ns-xxx"}, netAttDef)
				Expect(err).To(HaveOccurred())

				// Create Namespace
				nsObj := &corev1.Namespace{
					ObjectMeta: metav1.ObjectMeta{Name: "ns-xxx"},
				}
				err = k8sClient.Create(ctx, nsObj)
				Expect(err).NotTo(HaveOccurred())
				DeferCleanup(func() {
					err = k8sClient.Delete(ctx, nsObj)
					Expect(err).NotTo(HaveOccurred())
				})

				// Check that net-attach-def has been created
				err = util.WaitForNamespacedObject(netAttDef, k8sClient, "ns-xxx", cr.GetName(), util.RetryInterval, util.Timeout)
				Expect(err).NotTo(HaveOccurred())

				anno := netAttDef.GetAnnotations()
				Expect(anno["k8s.v1.cni.cncf.io/resourceName"]).To(Equal("openshift.io/" + cr.Spec.ResourceName))
				Expect(strings.TrimSpace(netAttDef.Spec.Config)).To(Equal(expect))
			})
		})

		It("should preserve user defined annotations", func() {
			cr := sriovnetworkv1.SriovNetwork{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "test-annotations",
					Namespace:   testNamespace,
					Annotations: map[string]string{"foo": "bar"},
				},
				Spec: sriovnetworkv1.SriovNetworkSpec{
					NetworkNamespace: "default",
				},
			}

			err := k8sClient.Create(ctx, &cr)
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(k8sClient.Delete, ctx, &cr)

			Eventually(func(g Gomega) {
				network := &sriovnetworkv1.SriovNetwork{}
				err = k8sClient.Get(ctx, types.NamespacedName{Name: cr.GetName(), Namespace: testNamespace}, network)
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(network.Annotations).To(HaveKeyWithValue("foo", "bar"))
				g.Expect(network.Annotations).To(HaveKeyWithValue("operator.sriovnetwork.openshift.io/last-network-namespace", "default"))
			}).
				WithPolling(100 * time.Millisecond).
				WithTimeout(5 * time.Second).
				MustPassRepeatedly(10).
				Should(Succeed())
		})

		Context("conditions", func() {
			It("should set Ready=True when network is successfully provisioned", func() {
				cr := sriovnetworkv1.SriovNetwork{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-cond-ready",
						Namespace: testNamespace,
					},
					Spec: sriovnetworkv1.SriovNetworkSpec{
						NetworkNamespace: "default",
						ResourceName:     "resource_cond",
					},
				}

				err := k8sClient.Create(ctx, &cr)
				Expect(err).NotTo(HaveOccurred())
				DeferCleanup(func() {
					Expect(k8sClient.Delete(ctx, &cr)).To(Succeed())
				})

				Eventually(func(g Gomega) {
					network := &sriovnetworkv1.SriovNetwork{}
					g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: cr.Name, Namespace: testNamespace}, network)).To(Succeed())

					// Verify Ready condition
					readyCondition := findCondition(network.Status.Conditions, sriovnetworkv1.ConditionReady)
					g.Expect(readyCondition).ToNot(BeNil())
					g.Expect(readyCondition.Status).To(Equal(metav1.ConditionTrue))
					g.Expect(readyCondition.Reason).To(Equal(sriovnetworkv1.ReasonNetworkReady))
					g.Expect(readyCondition.Message).To(ContainSubstring("NetworkAttachmentDefinition is provisioned"))

				}, util.APITimeout, util.RetryInterval).Should(Succeed())
			})

			It("should set Ready=False when target namespace does not exist", func() {
				cr := sriovnetworkv1.SriovNetwork{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-cond-degraded",
						Namespace: testNamespace,
					},
					Spec: sriovnetworkv1.SriovNetworkSpec{
						NetworkNamespace: "non-existent-ns-degraded",
						ResourceName:     "resource_cond_degraded",
					},
				}

				err := k8sClient.Create(ctx, &cr)
				Expect(err).NotTo(HaveOccurred())
				DeferCleanup(func() {
					Expect(k8sClient.Delete(ctx, &cr)).To(Succeed())
				})

				// Wait for conditions to be set
				time.Sleep(2 * time.Second)

				Eventually(func(g Gomega) {
					network := &sriovnetworkv1.SriovNetwork{}
					g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: cr.Name, Namespace: testNamespace}, network)).To(Succeed())

					// Verify Ready condition is False (waiting for namespace)
					readyCondition := findCondition(network.Status.Conditions, sriovnetworkv1.ConditionReady)
					g.Expect(readyCondition).ToNot(BeNil())
					g.Expect(readyCondition.Status).To(Equal(metav1.ConditionFalse))
					g.Expect(readyCondition.Reason).To(Equal(sriovnetworkv1.ReasonNamespaceNotFound))
					g.Expect(readyCondition.Message).To(ContainSubstring("does not exist"))
				}, util.APITimeout, util.RetryInterval).Should(Succeed())
			})

			It("should have consistent observedGeneration in conditions", func() {
				cr := sriovnetworkv1.SriovNetwork{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-cond-gen",
						Namespace: testNamespace,
					},
					Spec: sriovnetworkv1.SriovNetworkSpec{
						NetworkNamespace: "default",
						ResourceName:     "resource_cond_gen",
					},
				}

				err := k8sClient.Create(ctx, &cr)
				Expect(err).NotTo(HaveOccurred())
				DeferCleanup(func() {
					Expect(k8sClient.Delete(ctx, &cr)).To(Succeed())
				})

				Eventually(func(g Gomega) {
					network := &sriovnetworkv1.SriovNetwork{}
					g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: cr.Name, Namespace: testNamespace}, network)).To(Succeed())

					readyCondition := findCondition(network.Status.Conditions, sriovnetworkv1.ConditionReady)
					g.Expect(readyCondition).ToNot(BeNil())
					g.Expect(readyCondition.ObservedGeneration).To(Equal(network.Generation))
				}, util.APITimeout, util.RetryInterval).Should(Succeed())
			})

			It("should set Ready=True when SriovNetwork is in application namespace (same as NetworkNamespace)", func() {
				appNs := "app-ns-cond"
				// Create application namespace
				nsObj := &corev1.Namespace{
					ObjectMeta: metav1.ObjectMeta{Name: appNs},
				}
				Expect(k8sClient.Create(ctx, nsObj)).NotTo(HaveOccurred())
				DeferCleanup(func() {
					Expect(k8sClient.Delete(ctx, nsObj)).NotTo(HaveOccurred())
				})

				// Create SriovNetwork in application namespace (not operator namespace)
				cr := sriovnetworkv1.SriovNetwork{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "app-network",
						Namespace: appNs,
					},
					Spec: sriovnetworkv1.SriovNetworkSpec{
						ResourceName: "resource_app",
						// No NetworkNamespace specified - should use same namespace
					},
				}

				err := k8sClient.Create(ctx, &cr)
				Expect(err).NotTo(HaveOccurred())
				DeferCleanup(func() {
					Expect(k8sClient.Delete(ctx, &cr)).To(Succeed())
				})

				Eventually(func(g Gomega) {
					network := &sriovnetworkv1.SriovNetwork{}
					g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: cr.Name, Namespace: appNs}, network)).To(Succeed())

					// Verify Ready condition is True (network in same namespace)
					readyCondition := findCondition(network.Status.Conditions, sriovnetworkv1.ConditionReady)
					g.Expect(readyCondition).ToNot(BeNil())
					g.Expect(readyCondition.Status).To(Equal(metav1.ConditionTrue))
					g.Expect(readyCondition.Reason).To(Equal(sriovnetworkv1.ReasonNetworkReady))

				}, util.APITimeout, util.RetryInterval).Should(Succeed())

				// Verify NetworkAttachmentDefinition is created in the same namespace
				Eventually(func(g Gomega) {
					netAttDef := &netattdefv1.NetworkAttachmentDefinition{}
					g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: cr.Name, Namespace: appNs}, netAttDef)).NotTo(HaveOccurred())
				}, util.APITimeout, util.RetryInterval).Should(Succeed())
			})

			It("should show different conditions for networks in operator vs application namespace with cross-namespace target", func() {
				appNs := "app-ns-multi"
				// Create application namespace
				nsObj := &corev1.Namespace{
					ObjectMeta: metav1.ObjectMeta{Name: appNs},
				}
				Expect(k8sClient.Create(ctx, nsObj)).NotTo(HaveOccurred())
				DeferCleanup(func() {
					Expect(k8sClient.Delete(ctx, nsObj)).NotTo(HaveOccurred())
				})

				// Network 1: In operator namespace, targeting application namespace - should succeed
				crOperator := sriovnetworkv1.SriovNetwork{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "network-operator-ns",
						Namespace: testNamespace,
					},
					Spec: sriovnetworkv1.SriovNetworkSpec{
						NetworkNamespace: appNs,
						ResourceName:     "resource_multi_1",
					},
				}

				// Network 2: In application namespace, targeting different namespace - should not be Ready
				// (cross-namespace targeting from non-operator namespace is not allowed)
				crApp := sriovnetworkv1.SriovNetwork{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "network-app-ns",
						Namespace: appNs,
					},
					Spec: sriovnetworkv1.SriovNetworkSpec{
						NetworkNamespace: "default", // Different from its own namespace
						ResourceName:     "resource_multi_2",
					},
				}

				Expect(k8sClient.Create(ctx, &crOperator)).NotTo(HaveOccurred())
				Expect(k8sClient.Create(ctx, &crApp)).NotTo(HaveOccurred())
				DeferCleanup(func() {
					Expect(k8sClient.Delete(ctx, &crOperator)).To(Succeed())
					Expect(k8sClient.Delete(ctx, &crApp)).To(Succeed())
				})

				// Network in operator namespace should be Ready
				Eventually(func(g Gomega) {
					network := &sriovnetworkv1.SriovNetwork{}
					g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: crOperator.Name, Namespace: testNamespace}, network)).To(Succeed())

					readyCondition := findCondition(network.Status.Conditions, sriovnetworkv1.ConditionReady)
					g.Expect(readyCondition).ToNot(BeNil())
					g.Expect(readyCondition.Status).To(Equal(metav1.ConditionTrue))
				}, util.APITimeout, util.RetryInterval).Should(Succeed())

				// Network in application namespace with cross-namespace target should not be Ready
				Eventually(func(g Gomega) {
					network := &sriovnetworkv1.SriovNetwork{}
					g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: crApp.Name, Namespace: appNs}, network)).To(Succeed())

					readyCondition := findCondition(network.Status.Conditions, sriovnetworkv1.ConditionReady)
					g.Expect(readyCondition).ToNot(BeNil())
					g.Expect(readyCondition.Status).To(Equal(metav1.ConditionFalse))
					g.Expect(readyCondition.Reason).To(Equal(sriovnetworkv1.ReasonNetworkAttachmentDefInvalid))
					g.Expect(readyCondition.Message).To(ContainSubstring("networkNamespace cannot be set"))
				}, util.APITimeout, util.RetryInterval).Should(Succeed())

				// NAD should NOT be created in target namespace
				time.Sleep(2 * time.Second)
				Consistently(func(g Gomega) {
					netAttDef := &netattdefv1.NetworkAttachmentDefinition{}
					err := k8sClient.Get(ctx, types.NamespacedName{Name: crApp.Name, Namespace: "default"}, netAttDef)
					g.Expect(errors.IsNotFound(err)).To(BeTrue())
				}, 2*time.Second, 100*time.Millisecond).Should(Succeed())
			})

			It("should set Ready=False on app network when operator network already owns the NAD with same name", func() {
				targetNs := "target-ns-conflict"
				nadName := "test-nad-conflict"

				// Create target namespace
				nsObj := &corev1.Namespace{
					ObjectMeta: metav1.ObjectMeta{Name: targetNs},
				}
				Expect(k8sClient.Create(ctx, nsObj)).NotTo(HaveOccurred())
				DeferCleanup(func() {
					Expect(k8sClient.Delete(ctx, nsObj)).NotTo(HaveOccurred())
				})

				// Network 1: In operator namespace, targeting target-ns
				// Creates NAD named "test-nad-conflict" in target-ns
				crOperator := sriovnetworkv1.SriovNetwork{
					ObjectMeta: metav1.ObjectMeta{
						Name:      nadName,
						Namespace: testNamespace,
					},
					Spec: sriovnetworkv1.SriovNetworkSpec{
						NetworkNamespace: targetNs,
						ResourceName:     "resource_op",
					},
				}

				// Network 2: In target namespace with SAME NAME
				// Would try to create NAD with same name in same namespace - should not be Ready
				crApp := sriovnetworkv1.SriovNetwork{
					ObjectMeta: metav1.ObjectMeta{
						Name:      nadName, // Same name as operator network
						Namespace: targetNs,
					},
					Spec: sriovnetworkv1.SriovNetworkSpec{
						ResourceName: "resource_app",
						// No NetworkNamespace - uses same namespace (targetNs)
					},
				}

				// Create operator network first
				Expect(k8sClient.Create(ctx, &crOperator)).NotTo(HaveOccurred())
				DeferCleanup(func() {
					Expect(k8sClient.Delete(ctx, &crOperator)).To(Succeed())
				})

				// Wait for NAD to be created by operator network
				Eventually(func(g Gomega) {
					netAttDef := &netattdefv1.NetworkAttachmentDefinition{}
					g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: nadName, Namespace: targetNs}, netAttDef)).NotTo(HaveOccurred())
				}, util.APITimeout, util.RetryInterval).Should(Succeed())

				// Network from operator namespace should be Ready
				Eventually(func(g Gomega) {
					network := &sriovnetworkv1.SriovNetwork{}
					g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: crOperator.Name, Namespace: testNamespace}, network)).To(Succeed())

					readyCondition := findCondition(network.Status.Conditions, sriovnetworkv1.ConditionReady)
					g.Expect(readyCondition).ToNot(BeNil())
					g.Expect(readyCondition.Status).To(Equal(metav1.ConditionTrue))
				}, util.APITimeout, util.RetryInterval).Should(Succeed())

				// Now create app network with same name - should not be Ready
				Expect(k8sClient.Create(ctx, &crApp)).NotTo(HaveOccurred())
				DeferCleanup(func() {
					Expect(k8sClient.Delete(ctx, &crApp)).To(Succeed())
				})

				// Network from app namespace should not be Ready (NAD already exists and is owned by another network)
				Eventually(func(g Gomega) {
					network := &sriovnetworkv1.SriovNetwork{}
					g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: crApp.Name, Namespace: targetNs}, network)).To(Succeed())

					readyCondition := findCondition(network.Status.Conditions, sriovnetworkv1.ConditionReady)
					g.Expect(readyCondition).ToNot(BeNil())
					g.Expect(readyCondition.Status).To(Equal(metav1.ConditionFalse))
				}, util.APITimeout, util.RetryInterval).Should(Succeed())

				// Verify NAD still exists and is owned by operator network (not overwritten)
				Eventually(func(g Gomega) {
					netAttDef := &netattdefv1.NetworkAttachmentDefinition{}
					g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: nadName, Namespace: targetNs}, netAttDef)).NotTo(HaveOccurred())
					// The NAD should NOT have owner reference to app network (since they're in different namespaces)
					// It should still be controlled by the operator network's reconciler
					g.Expect(netAttDef.GetAnnotations()["k8s.v1.cni.cncf.io/resourceName"]).To(ContainSubstring("resource_op"))
				}, util.APITimeout, util.RetryInterval).Should(Succeed())
			})
		})

		Context("When the SriovNetwork namespace is not equal to the operator one", func() {
			BeforeAll(func() {
				nsBlue := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "ns-blue"}}
				Expect(k8sClient.Create(context.Background(), nsBlue)).ToNot(HaveOccurred())
			})

			AfterEach(func() {
				cleanNetworksInNamespace("ns-blue")
			})

			It("should create the NetAttachDefinition in the same namespace", func() {
				cr := sriovnetworkv1.SriovNetwork{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "sriovnet-blue",
						Namespace: "ns-blue",
					},
					Spec: sriovnetworkv1.SriovNetworkSpec{
						ResourceName: "resource_x",
					},
				}

				err := k8sClient.Create(ctx, &cr)
				Expect(err).NotTo(HaveOccurred())

				netAttDef := &netattdefv1.NetworkAttachmentDefinition{}
				err = util.WaitForNamespacedObject(netAttDef, k8sClient, "ns-blue", cr.GetName(), util.RetryInterval, util.Timeout)
				Expect(err).NotTo(HaveOccurred())
				expectedOwnerReference := metav1.OwnerReference{
					Kind:       "SriovNetwork",
					APIVersion: sriovnetworkv1.GroupVersion.String(),
					UID:        cr.UID,
					Name:       cr.Name,
				}
				Expect(netAttDef.GetAnnotations()["k8s.v1.cni.cncf.io/resourceName"]).To(Equal("openshift.io/resource_x"))

				Expect(netAttDef.ObjectMeta.OwnerReferences).To(ContainElement(expectedOwnerReference))

				// Patch the SriovNetwork
				original := cr.DeepCopy()
				cr.Spec.ResourceName = "resource_y"
				err = k8sClient.Patch(ctx, &cr, dynclient.MergeFrom(original))
				Expect(err).NotTo(HaveOccurred())

				// Check that the OwnerReference persists
				netAttDef = &netattdefv1.NetworkAttachmentDefinition{}

				Eventually(func(g Gomega) {
					netAttDef = &netattdefv1.NetworkAttachmentDefinition{}
					g.Expect(k8sClient.Get(ctx, types.NamespacedName{Name: cr.GetName(), Namespace: "ns-blue"}, netAttDef)).To(Succeed())
					g.Expect(netAttDef.GetAnnotations()["k8s.v1.cni.cncf.io/resourceName"]).To(Equal("openshift.io/resource_y"))
					g.Expect(netAttDef.ObjectMeta.OwnerReferences).To(ContainElement(expectedOwnerReference))
				}).WithPolling(100 * time.Millisecond).WithTimeout(5 * time.Second).Should(Succeed())

				// Delete the SriovNetwork
				err = k8sClient.Delete(ctx, &cr)
				Expect(err).NotTo(HaveOccurred())
			})

			It("should not create the NetAttachDefinition if the NetworkNamespace field is not empty", func() {
				cr := sriovnetworkv1.SriovNetwork{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "sriovnet-blue",
						Namespace: "ns-blue",
					},
					Spec: sriovnetworkv1.SriovNetworkSpec{
						NetworkNamespace: "default",
						ResourceName:     "resource_x",
					},
				}

				err := k8sClient.Create(ctx, &cr)
				Expect(err).NotTo(HaveOccurred())

				Consistently(func(g Gomega) {
					netAttDef := &netattdefv1.NetworkAttachmentDefinition{}
					err := k8sClient.Get(ctx, types.NamespacedName{Name: cr.GetName(), Namespace: "default"}, netAttDef)
					g.Expect(err).To(HaveOccurred())
					g.Expect(errors.IsNotFound(err)).To(BeTrue())
				}).WithPolling(100 * time.Millisecond).WithTimeout(1 * time.Second).Should(Succeed())
			})

		})
	})
})

func generateExpectedNetConfig(cr *sriovnetworkv1.SriovNetwork) string {
	spoofchk := ""
	trust := ""
	vlanProto := ""
	logLevel := `"logLevel":"info",`
	logFile := ""
	ipam := emptyCurls

	if cr.Spec.Trust == sriovnetworkv1.SriovCniStateOn {
		trust = `"trust":"on",`
	} else if cr.Spec.Trust == sriovnetworkv1.SriovCniStateOff {
		trust = `"trust":"off",`
	}

	if cr.Spec.SpoofChk == sriovnetworkv1.SriovCniStateOn {
		spoofchk = `"spoofchk":"on",`
	} else if cr.Spec.SpoofChk == sriovnetworkv1.SriovCniStateOff {
		spoofchk = `"spoofchk":"off",`
	}

	state := getLinkState(cr.Spec.LinkState)

	if cr.Spec.IPAM != "" {
		ipam = cr.Spec.IPAM
	}
	vlanQoS := cr.Spec.VlanQoS

	if cr.Spec.VlanProto != "" {
		vlanProto = fmt.Sprintf(`"vlanProto": "%s",`, cr.Spec.VlanProto)
	}
	if cr.Spec.LogLevel != "" {
		logLevel = fmt.Sprintf(`"logLevel":"%s",`, cr.Spec.LogLevel)
	}
	if cr.Spec.LogFile != "" {
		logFile = fmt.Sprintf(`"logFile":"%s",`, cr.Spec.LogFile)
	}

	configStr, err := formatJSON(fmt.Sprintf(
		`{ "cniVersion":"1.0.0", "name":"%s","type":"sriov","vlan":%d,%s%s"vlanQoS":%d,%s%s%s%s"ipam":%s }`,
		cr.GetName(), cr.Spec.Vlan, spoofchk, trust, vlanQoS, vlanProto, state, logLevel, logFile, ipam))
	if err != nil {
		panic(err)
	}
	return configStr
}
