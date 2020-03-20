package operator

import (
	goctx "context"
	// "encoding/json"
	// "fmt"
	// "reflect"
	// "strings"
	// "testing"
	"time"

	// dptypes "github.com/intel/sriov-network-device-plugin/pkg/types"
	framework "github.com/operator-framework/operator-sdk/pkg/test"
	// "github.com/operator-framework/operator-sdk/pkg/test/e2eutil"
	admv1beta1 "k8s.io/api/admissionregistration/v1beta1"
	appsv1 "k8s.io/api/apps/v1"
	// corev1 "k8s.io/api/core/v1"
	// "k8s.io/apimachinery/pkg/api/errors"
	// metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	// "k8s.io/apimachinery/pkg/runtime"
	// "k8s.io/apimachinery/pkg/types"
	// "k8s.io/apimachinery/pkg/util/wait"
	// dynclient "sigs.k8s.io/controller-runtime/pkg/client"

	// "github.com/openshift/sriov-network-operator/pkg/apis"
	// netattdefv1 "github.com/openshift/sriov-network-operator/pkg/apis/k8s/v1"
	sriovnetworkv1 "github.com/openshift/sriov-network-operator/pkg/apis/sriovnetwork/v1"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/ginkgo/extensions/table"
	. "github.com/onsi/gomega"

	. "github.com/openshift/sriov-network-operator/test/util"
)

var _ = Describe("Operator", func() {

	AfterEach(func() {
		// get global framework variables
		f := framework.Global
		// wait for sriov-network-operator to be ready
		config := &sriovnetworkv1.SriovOperatorConfig{}
		err := WaitForNamespacedObject(config, f.Client, namespace, "default", RetryInterval, Timeout)
		Expect(err).NotTo(HaveOccurred())

		*config.Spec.EnableOperatorWebhook = true
		*config.Spec.EnableInjector = true
		config.Spec.ConfigDaemonNodeSelector = nil

		err = f.Client.Update(goctx.TODO(), config)
		Expect(err).NotTo(HaveOccurred())
	})

	Context("When is up", func() {
		It("should have default operator config", func() {
			// get global framework variables
			f := framework.Global
			config := &sriovnetworkv1.SriovOperatorConfig{}
			err := WaitForNamespacedObject(config, f.Client, namespace, "default", RetryInterval, Timeout)
			Expect(err).NotTo(HaveOccurred())

			Expect(*config.Spec.EnableOperatorWebhook).To(Equal(true))
			Expect(*config.Spec.EnableInjector).To(Equal(true))
			Expect(config.Spec.ConfigDaemonNodeSelector).Should(BeNil())

			mutateCfg := &admv1beta1.MutatingWebhookConfiguration{}
			err = WaitForNamespacedObject(mutateCfg, f.Client, namespace, "operator-webhook-config", RetryInterval, Timeout)
			Expect(err).NotTo(HaveOccurred())

			validateCfg := &admv1beta1.ValidatingWebhookConfiguration{}
			err = WaitForNamespacedObject(validateCfg, f.Client, namespace, "operator-webhook-config", RetryInterval, Timeout)
			Expect(err).NotTo(HaveOccurred())
		})

		DescribeTable("should have daemonset enabled by default",
			func(dsName string) {
				// get global framework variables
				f := framework.Global
				// wait for sriov-network-operator to be ready
				daemonSet := &appsv1.DaemonSet{}
				err := WaitForNamespacedObject(daemonSet, f.Client, namespace, dsName, RetryInterval, Timeout)
				Expect(err).NotTo(HaveOccurred())
			},
			Entry("operator-webhook", "operator-webhook"),
			Entry("network-resources-injector", "network-resources-injector"),
			Entry("sriov-network-config-daemon", "sriov-network-config-daemon"),
		)
	})

	Context("With SriovOperatorConfig", func() {

		It("should be able to turn network-resources-injector on/off", func() {
			By("set disable to enableInjector")
			f := framework.Global
			config := &sriovnetworkv1.SriovOperatorConfig{}
			err := WaitForNamespacedObject(config, f.Client, namespace, "default", RetryInterval, Timeout)
			Expect(err).NotTo(HaveOccurred())

			*config.Spec.EnableInjector = false
			err = f.Client.Update(goctx.TODO(), config)
			Expect(err).NotTo(HaveOccurred())

			daemonSet := &appsv1.DaemonSet{}
			err = WaitForNamespacedObjectDeleted(daemonSet, f.Client, namespace, "network-resources-injector", RetryInterval, Timeout)
			Expect(err).NotTo(HaveOccurred())

			mutateCfg := &admv1beta1.MutatingWebhookConfiguration{}
			err = WaitForNamespacedObjectDeleted(mutateCfg, f.Client, namespace, "network-resources-injector-config", RetryInterval, Timeout)
			Expect(err).NotTo(HaveOccurred())

			By("set enable to enableInjector")
			err = WaitForNamespacedObject(config, f.Client, namespace, "default", RetryInterval, Timeout)
			Expect(err).NotTo(HaveOccurred())

			*config.Spec.EnableInjector = true
			err = f.Client.Update(goctx.TODO(), config)
			Expect(err).NotTo(HaveOccurred())

			daemonSet = &appsv1.DaemonSet{}
			err = WaitForNamespacedObject(daemonSet, f.Client, namespace, "network-resources-injector", RetryInterval, Timeout)
			Expect(err).NotTo(HaveOccurred())

			mutateCfg = &admv1beta1.MutatingWebhookConfiguration{}
			err = WaitForNamespacedObject(mutateCfg, f.Client, namespace, "network-resources-injector-config", RetryInterval, Timeout)
			Expect(err).NotTo(HaveOccurred())
		})

		It("should be able to turn operator-webhook on/off", func() {

			By("set disable to enableOperatorWebhook")
			f := framework.Global
			config := &sriovnetworkv1.SriovOperatorConfig{}
			err := WaitForNamespacedObject(config, f.Client, namespace, "default", RetryInterval, Timeout)
			Expect(err).NotTo(HaveOccurred())

			*config.Spec.EnableOperatorWebhook = false
			err = f.Client.Update(goctx.TODO(), config)
			Expect(err).NotTo(HaveOccurred())

			daemonSet := &appsv1.DaemonSet{}
			err = WaitForNamespacedObjectDeleted(daemonSet, f.Client, namespace, "operator-webhook", RetryInterval, Timeout)
			Expect(err).NotTo(HaveOccurred())

			mutateCfg := &admv1beta1.MutatingWebhookConfiguration{}
			err = WaitForNamespacedObjectDeleted(mutateCfg, f.Client, namespace, "operator-webhook-config", RetryInterval, Timeout)
			Expect(err).NotTo(HaveOccurred())

			validateCfg := &admv1beta1.ValidatingWebhookConfiguration{}
			err = WaitForNamespacedObjectDeleted(validateCfg, f.Client, namespace, "operator-webhook-config", RetryInterval, Timeout)
			Expect(err).NotTo(HaveOccurred())

			By("set disable to enableOperatorWebhook")
			err = WaitForNamespacedObject(config, f.Client, namespace, "default", RetryInterval, Timeout)
			Expect(err).NotTo(HaveOccurred())

			*config.Spec.EnableOperatorWebhook = true
			err = f.Client.Update(goctx.TODO(), config)
			Expect(err).NotTo(HaveOccurred())

			daemonSet = &appsv1.DaemonSet{}
			err = WaitForNamespacedObject(daemonSet, f.Client, namespace, "operator-webhook", RetryInterval, Timeout)
			Expect(err).NotTo(HaveOccurred())

			mutateCfg = &admv1beta1.MutatingWebhookConfiguration{}
			err = WaitForNamespacedObject(mutateCfg, f.Client, namespace, "operator-webhook-config", RetryInterval, Timeout)
			Expect(err).NotTo(HaveOccurred())

			validateCfg = &admv1beta1.ValidatingWebhookConfiguration{}
			err = WaitForNamespacedObject(validateCfg, f.Client, namespace, "operator-webhook-config", RetryInterval, Timeout)
			Expect(err).NotTo(HaveOccurred())
		})
		It("should be able to update the node selector of sriov-network-config-daemon", func() {

			By("specify the configDaemonNodeSelector")
			f := framework.Global
			config := &sriovnetworkv1.SriovOperatorConfig{}
			err := WaitForNamespacedObject(config, f.Client, namespace, "default", RetryInterval, Timeout)
			Expect(err).NotTo(HaveOccurred())
			config.Spec.ConfigDaemonNodeSelector = map[string]string{"node-role.kubernetes.io/worker": ""}
			err = f.Client.Update(goctx.TODO(), config)
			Expect(err).NotTo(HaveOccurred())

			time.Sleep(3 * time.Second)
			daemonSet := &appsv1.DaemonSet{}
			err = WaitForDaemonSetReady(daemonSet, f.Client, namespace, "sriov-network-config-daemon", RetryInterval, Timeout)
			Expect(err).NotTo(HaveOccurred())
			Expect(daemonSet.Spec.Template.Spec.NodeSelector).To(Equal(config.Spec.ConfigDaemonNodeSelector))
		})
	})
})
