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
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	dynclient "sigs.k8s.io/controller-runtime/pkg/client"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	sriovnetworkv1 "github.com/k8snetworkplumbingwg/sriov-network-operator/api/v1"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/test/util"
)

var _ = Describe("SriovIBNetwork Controller", Ordered, func() {
	var cancel context.CancelFunc
	var ctx context.Context

	BeforeAll(func() {
		By("Setup controller manager")
		k8sManager, err := setupK8sManagerForTest()
		Expect(err).ToNot(HaveOccurred())

		err = (&SriovIBNetworkReconciler{
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

	Context("with SriovIBNetwork", func() {
		specs := map[string]sriovnetworkv1.SriovIBNetworkSpec{
			"ib-test-1": {
				ResourceName:     "resource_1",
				IPAM:             `{"type":"host-local","subnet":"10.56.217.0/24","rangeStart":"10.56.217.171","rangeEnd":"10.56.217.181","routes":[{"dst":"0.0.0.0/0"}],"gateway":"10.56.217.1"}`,
				NetworkNamespace: "default",
			},
			"ib-test-2": {
				ResourceName:     "resource_1",
				NetworkNamespace: "default",
			},
			"ib-test-3": {
				ResourceName:     "resource_1",
				NetworkNamespace: "default",
				LinkState:        "enable",
			},
			"ib-test-4": {
				ResourceName: "resource_1",
				IPAM:         `{"type":"host-local","subnet":"10.56.217.0/24","rangeStart":"10.56.217.171","rangeEnd":"10.56.217.181","routes":[{"dst":"0.0.0.0/0"}],"gateway":"10.56.217.1"}`,
			},
		}
		sriovnets := util.GenerateSriovIBNetworkCRs(testNamespace, specs)
		DescribeTable("should be possible to create/delete net-att-def",
			func(cr sriovnetworkv1.SriovIBNetwork) {
				var err error
				expect := generateExpectedIBNetConfig(&cr)

				By("Create the SriovIBNetwork Custom Resource")
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

				By("Delete the SriovIBNetwork Custom Resource")
				found := &sriovnetworkv1.SriovIBNetwork{}
				err = k8sClient.Get(ctx, types.NamespacedName{Namespace: cr.GetNamespace(), Name: cr.GetName()}, found)
				Expect(err).NotTo(HaveOccurred())
				err = k8sClient.Delete(ctx, found, []dynclient.DeleteOption{}...)
				Expect(err).NotTo(HaveOccurred())

				netAttDef = &netattdefv1.NetworkAttachmentDefinition{}
				err = util.WaitForNamespacedObjectDeleted(netAttDef, k8sClient, ns, cr.GetName(), util.RetryInterval, util.Timeout)
				Expect(err).NotTo(HaveOccurred())
			},
			Entry("with networkNamespace flag", sriovnets["ib-test-1"]),
			Entry("without IPAM", sriovnets["ib-test-2"]),
			Entry("with linkState on", sriovnets["ib-test-3"]),
			Entry("without networkNamespace flag", sriovnets["ib-test-4"]),
		)

		newSpecs := map[string]sriovnetworkv1.SriovIBNetworkSpec{
			"ib-new-0": {
				ResourceName: "resource_1",
				IPAM:         `{"type":"dhcp"}`,
			},
			"ib-new-1": {
				ResourceName: "resource_1",
				IPAM:         `{"type":"host-local","subnet":"10.56.217.0/24","rangeStart":"10.56.217.171","rangeEnd":"10.56.217.181","routes":[{"dst":"0.0.0.0/0"}],"gateway":"10.56.217.1"}`,
			},
		}
		newsriovnets := util.GenerateSriovIBNetworkCRs(testNamespace, newSpecs)

		DescribeTable("should be possible to update net-att-def",
			func(old, new sriovnetworkv1.SriovIBNetwork) {
				old.Name = new.GetName()
				err := k8sClient.Create(ctx, &old)
				Expect(err).NotTo(HaveOccurred())
				defer func() {
					// Cleanup the test resource
					Expect(k8sClient.Delete(ctx, &old)).To(Succeed())
				}()
				found := &sriovnetworkv1.SriovIBNetwork{}
				expect := generateExpectedIBNetConfig(&new)

				retryErr := retry.RetryOnConflict(retry.DefaultRetry, func() error {
					// Retrieve the latest version of SriovIBNetwork before attempting update
					// RetryOnConflict uses exponential backoff to avoid exhausting the apiserver
					getErr := k8sClient.Get(ctx, types.NamespacedName{Namespace: old.GetNamespace(), Name: old.GetName()}, found)
					if getErr != nil {
						io.WriteString(GinkgoWriter, fmt.Sprintf("Failed to get latest version of SriovIBNetwork: %v", getErr))
					}
					found.Spec = new.Spec
					found.Annotations = new.Annotations
					updateErr := k8sClient.Update(ctx, found)
					if getErr != nil {
						io.WriteString(GinkgoWriter, fmt.Sprintf("Failed to update latest version of SriovIBNetwork: %v", getErr))
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
			Entry("with ipam updated", sriovnets["ib-test-4"], newsriovnets["ib-new-0"]),
			Entry("with networkNamespace flag", sriovnets["ib-test-4"], newsriovnets["ib-new-1"]),
		)
	})
	Context("When a derived net-att-def CR is removed", func() {
		It("should regenerate the net-att-def CR", func() {
			cr := sriovnetworkv1.SriovIBNetwork{
				TypeMeta: metav1.TypeMeta{
					Kind:       "SriovNetwork",
					APIVersion: "sriovnetwork.openshift.io/v1",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-5",
					Namespace: testNamespace,
				},
				Spec: sriovnetworkv1.SriovIBNetworkSpec{
					NetworkNamespace: "default",
					ResourceName:     "resource_1",
					IPAM:             `{"type":"dhcp"}`,
				},
			}
			var err error
			expect := generateExpectedIBNetConfig(&cr)

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

			found := &sriovnetworkv1.SriovIBNetwork{}
			err = k8sClient.Get(ctx, types.NamespacedName{Namespace: cr.GetNamespace(), Name: cr.GetName()}, found)
			Expect(err).NotTo(HaveOccurred())
			err = k8sClient.Delete(ctx, found, []dynclient.DeleteOption{}...)
			Expect(err).NotTo(HaveOccurred())
		})
	})

	Context("When the target NetworkNamespace doesn't exists", func() {
		It("should create the NetAttachDef when the namespace is created", func() {
			cr := sriovnetworkv1.SriovIBNetwork{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-missing-namespace",
					Namespace: testNamespace,
				},
				Spec: sriovnetworkv1.SriovIBNetworkSpec{
					NetworkNamespace: "ib-ns-xxx",
					ResourceName:     "resource_missing_namespace",
					IPAM:             `{"type":"dhcp"}`,
				},
			}
			var err error
			expect := generateExpectedIBNetConfig(&cr)

			err = k8sClient.Create(ctx, &cr)
			Expect(err).NotTo(HaveOccurred())

			DeferCleanup(func() {
				err = k8sClient.Delete(ctx, &cr)
				Expect(err).NotTo(HaveOccurred())
			})

			// Sleep 3 seconds to be sure the Reconcile loop has been invoked. This can be improved by exposing some information (e.g. the error)
			// in the SriovIBNetwork.Status field.
			time.Sleep(3 * time.Second)

			netAttDef := &netattdefv1.NetworkAttachmentDefinition{}
			err = k8sClient.Get(ctx, types.NamespacedName{Name: cr.GetName(), Namespace: "ib-ns-xxx"}, netAttDef)
			Expect(err).To(HaveOccurred())

			// Create Namespace
			nsObj := &corev1.Namespace{
				ObjectMeta: metav1.ObjectMeta{Name: "ib-ns-xxx"},
			}
			err = k8sClient.Create(ctx, nsObj)
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(func() {
				err = k8sClient.Delete(ctx, nsObj)
				Expect(err).NotTo(HaveOccurred())
			})

			// Check that net-attach-def has been created
			err = util.WaitForNamespacedObject(netAttDef, k8sClient, "ib-ns-xxx", cr.GetName(), util.RetryInterval, util.Timeout)
			Expect(err).NotTo(HaveOccurred())

			anno := netAttDef.GetAnnotations()
			Expect(anno["k8s.v1.cni.cncf.io/resourceName"]).To(Equal("openshift.io/" + cr.Spec.ResourceName))
			Expect(strings.TrimSpace(netAttDef.Spec.Config)).To(Equal(expect))
		})
	})
})

func generateExpectedIBNetConfig(cr *sriovnetworkv1.SriovIBNetwork) string {
	ipam := emptyCurls
	state := getLinkState(cr.Spec.LinkState)

	if cr.Spec.IPAM != "" {
		ipam = cr.Spec.IPAM
	}
	configStr, err := formatJSON(fmt.Sprintf(`{ "cniVersion":"1.0.0", "name":"%s","type":"ib-sriov",%s"ipam":%s }`, cr.GetName(), state, ipam))
	if err != nil {
		panic(err)
	}
	return configStr
}

func getLinkState(state string) string {
	st := ""
	if state == sriovnetworkv1.SriovCniStateAuto {
		st = `"link_state":"auto",`
	} else if state == sriovnetworkv1.SriovCniStateEnable {
		st = `"link_state":"enable",`
	} else if state == sriovnetworkv1.SriovCniStateDisable {
		st = `"link_state":"disable",`
	}
	return st
}
