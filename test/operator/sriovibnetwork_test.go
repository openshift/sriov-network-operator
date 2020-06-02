package operator

import (
	goctx "context"
	// "encoding/json"
	"fmt"
	"io"
	// "reflect"
	"strings"
	// "testing"
	"time"

	// dptypes "github.com/intel/sriov-network-device-plugin/pkg/types"
	framework "github.com/operator-framework/operator-sdk/pkg/test"
	// "github.com/operator-framework/operator-sdk/pkg/test/e2eutil"
	// appsv1 "k8s.io/api/apps/v1"
	// corev1 "k8s.io/api/core/v1"
	// "k8s.io/apimachinery/pkg/api/errors"
	// metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	// "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	// "k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/util/retry"
	dynclient "sigs.k8s.io/controller-runtime/pkg/client"

	// "github.com/openshift/sriov-network-operator/pkg/apis"
	netattdefv1 "github.com/openshift/sriov-network-operator/pkg/apis/k8s/v1"
	sriovnetworkv1 "github.com/openshift/sriov-network-operator/pkg/apis/sriovnetwork/v1"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/ginkgo/extensions/table"
	. "github.com/onsi/gomega"

	"github.com/openshift/sriov-network-operator/test/util"
)

var _ = Describe("Operator", func() {

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
		sriovnets := util.GenerateSriovIBNetworkCRs(namespace, specs)
		DescribeTable("should be possible to create/delete net-att-def",
			func(cr sriovnetworkv1.SriovIBNetwork) {
				var err error
				expect := util.GenerateExpectedIBNetConfig(&cr)

				By("Create the SriovIBNetwork Custom Resource")
				// get global framework variables
				f := framework.Global
				err = f.Client.Create(goctx.TODO(), &cr, &framework.CleanupOptions{TestContext: &oprctx, Timeout: util.ApiTimeout, RetryInterval: util.RetryInterval})
				Expect(err).NotTo(HaveOccurred())
				ns := namespace
				if cr.Spec.NetworkNamespace != "" {
					ns = cr.Spec.NetworkNamespace
				}
				netAttDef := &netattdefv1.NetworkAttachmentDefinition{}
				err = util.WaitForNamespacedObject(netAttDef, f.Client, ns, cr.GetName(), util.RetryInterval, util.Timeout)
				Expect(err).NotTo(HaveOccurred())
				anno := netAttDef.GetAnnotations()

				Expect(anno["k8s.v1.cni.cncf.io/resourceName"]).To(Equal("openshift.io/" + cr.Spec.ResourceName))
				Expect(strings.TrimSpace(netAttDef.Spec.Config)).To(Equal(expect))

				By("Delete the SriovIBNetwork Custom Resource")
				found := &sriovnetworkv1.SriovIBNetwork{}
				err = f.Client.Get(goctx.TODO(), types.NamespacedName{Namespace: cr.GetNamespace(), Name: cr.GetName()}, found)
				Expect(err).NotTo(HaveOccurred())
				err = f.Client.Delete(goctx.TODO(), found, []dynclient.DeleteOption{}...)
				Expect(err).NotTo(HaveOccurred())

				netAttDef = &netattdefv1.NetworkAttachmentDefinition{}
				err = util.WaitForNamespacedObjectDeleted(netAttDef, f.Client, ns, cr.GetName(), util.RetryInterval, util.Timeout)
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
		newsriovnets := util.GenerateSriovIBNetworkCRs(namespace, newSpecs)

		DescribeTable("should be possible to update net-att-def",
			func(old, new sriovnetworkv1.SriovIBNetwork) {
				f := framework.Global
				old.Name = new.GetName()
				err := f.Client.Create(goctx.TODO(), &old, &framework.CleanupOptions{TestContext: &oprctx, Timeout: util.ApiTimeout, RetryInterval: util.RetryInterval})
				Expect(err).NotTo(HaveOccurred())
				found := &sriovnetworkv1.SriovIBNetwork{}
				expect := util.GenerateExpectedIBNetConfig(&new)

				retryErr := retry.RetryOnConflict(retry.DefaultRetry, func() error {
					// Retrieve the latest version of SriovIBNetwork before attempting update
					// RetryOnConflict uses exponential backoff to avoid exhausting the apiserver
					getErr := f.Client.Get(goctx.TODO(), types.NamespacedName{Namespace: old.GetNamespace(), Name: old.GetName()}, found)
					if getErr != nil {
						io.WriteString(GinkgoWriter, fmt.Sprintf("Failed to get latest version of SriovIBNetwork: %v", getErr))
					}
					found.Spec = new.Spec
					found.Annotations = new.Annotations
					updateErr := f.Client.Update(goctx.TODO(), found)
					if getErr != nil {
						io.WriteString(GinkgoWriter, fmt.Sprintf("Failed to update latest version of SriovIBNetwork: %v", getErr))
					}
					return updateErr
				})
				if retryErr != nil {
					Fail(fmt.Sprintf("Update failed: %v", retryErr))
				}

				ns := namespace
				if new.Spec.NetworkNamespace != "" {
					ns = new.Spec.NetworkNamespace
				}

				time.Sleep(time.Second * 2)
				netAttDef := &netattdefv1.NetworkAttachmentDefinition{}
				err = util.WaitForNamespacedObject(netAttDef, f.Client, ns, old.GetName(), util.RetryInterval, util.Timeout)
				Expect(err).NotTo(HaveOccurred())
				anno := netAttDef.GetAnnotations()

				Expect(anno["k8s.v1.cni.cncf.io/resourceName"]).To(Equal("openshift.io/" + new.Spec.ResourceName))
				Expect(strings.TrimSpace(netAttDef.Spec.Config)).To(Equal(expect))
			},
			Entry("with ipam updated", sriovnets["ib-test-4"], newsriovnets["ib-new-0"]),
			Entry("with networkNamespace flag", sriovnets["ib-test-4"], newsriovnets["ib-new-1"]),
		)
	})
})
