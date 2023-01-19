package controllers

import (
	goctx "context"

	admv1 "k8s.io/api/admissionregistration/v1"
	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/types"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	sriovnetworkv1 "github.com/k8snetworkplumbingwg/sriov-network-operator/api/v1"
	util "github.com/k8snetworkplumbingwg/sriov-network-operator/test/util"
)

var _ = Describe("Operator", func() {
	var config *sriovnetworkv1.SriovOperatorConfig
	// BeforeEach(func() {
	// 	config = &sriovnetworkv1.SriovOperatorConfig{}
	// 	config.SetNamespace(testNamespace)
	// 	config.SetName(DEFAULT_CONFIG_NAME)
	// 	config.Spec = sriovnetworkv1.SriovOperatorConfigSpec{
	// 		EnableInjector:           func() *bool { b := true; return &b }(),
	// 		EnableOperatorWebhook:    func() *bool { b := true; return &b }(),
	// 		ConfigDaemonNodeSelector: map[string]string{},
	// 		LogLevel:                 2,
	// 	}
	// 	Expect(k8sClient.Create(goctx.TODO(), config)).Should(Succeed())
	// })
	// AfterEach(func() {
	// 	config := &sriovnetworkv1.SriovOperatorConfig{}
	// 	config.SetNamespace(testNamespace)
	// 	config.SetName(DEFAULT_CONFIG_NAME)
	// 	Expect(k8sClient.Delete(goctx.TODO(), config)).Should(Succeed())
	// })

	Context("When is up", func() {
		JustBeforeEach(func() {
			config = &sriovnetworkv1.SriovOperatorConfig{}
			err := util.WaitForNamespacedObject(config, k8sClient, testNamespace, "default", interval, timeout)
			Expect(err).NotTo(HaveOccurred())
			config.Spec = sriovnetworkv1.SriovOperatorConfigSpec{
				EnableInjector:        func() *bool { b := true; return &b }(),
				EnableOperatorWebhook: func() *bool { b := true; return &b }(),
				// ConfigDaemonNodeSelector: map[string]string{},
				LogLevel: 2,
			}
			err = k8sClient.Update(goctx.TODO(), config)
			Expect(err).NotTo(HaveOccurred())
		})

		It("should have webhook enable", func() {
			mutateCfg := &admv1.MutatingWebhookConfiguration{}
			err := util.WaitForNamespacedObject(mutateCfg, k8sClient, testNamespace, "sriov-operator-webhook-config", interval, timeout)
			Expect(err).NotTo(HaveOccurred())

			validateCfg := &admv1.ValidatingWebhookConfiguration{}
			err = util.WaitForNamespacedObject(validateCfg, k8sClient, testNamespace, "sriov-operator-webhook-config", interval, timeout)
			Expect(err).NotTo(HaveOccurred())
		})

		DescribeTable("should have daemonset enabled by default",
			func(dsName string) {
				// wait for sriov-network-operator to be ready
				daemonSet := &appsv1.DaemonSet{}
				err := util.WaitForNamespacedObject(daemonSet, k8sClient, testNamespace, dsName, interval, timeout)
				Expect(err).NotTo(HaveOccurred())
			},
			Entry("operator-webhook", "operator-webhook"),
			Entry("network-resources-injector", "network-resources-injector"),
			Entry("sriov-network-config-daemon", "sriov-network-config-daemon"),
		)

		It("should be able to turn network-resources-injector on/off", func() {
			By("set disable to enableInjector")
			config := &sriovnetworkv1.SriovOperatorConfig{}
			err := util.WaitForNamespacedObject(config, k8sClient, testNamespace, "default", interval, timeout)
			Expect(err).NotTo(HaveOccurred())

			*config.Spec.EnableInjector = false
			err = k8sClient.Update(goctx.TODO(), config)
			Expect(err).NotTo(HaveOccurred())

			daemonSet := &appsv1.DaemonSet{}
			err = util.WaitForNamespacedObjectDeleted(daemonSet, k8sClient, testNamespace, "network-resources-injector", interval, timeout)
			Expect(err).NotTo(HaveOccurred())

			mutateCfg := &admv1.MutatingWebhookConfiguration{}
			err = util.WaitForNamespacedObjectDeleted(mutateCfg, k8sClient, testNamespace, "network-resources-injector-config", interval, timeout)
			Expect(err).NotTo(HaveOccurred())

			By("set enable to enableInjector")
			err = util.WaitForNamespacedObject(config, k8sClient, testNamespace, "default", interval, timeout)
			Expect(err).NotTo(HaveOccurred())

			*config.Spec.EnableInjector = true
			err = k8sClient.Update(goctx.TODO(), config)
			Expect(err).NotTo(HaveOccurred())

			daemonSet = &appsv1.DaemonSet{}
			err = util.WaitForNamespacedObject(daemonSet, k8sClient, testNamespace, "network-resources-injector", interval, timeout)
			Expect(err).NotTo(HaveOccurred())

			mutateCfg = &admv1.MutatingWebhookConfiguration{}
			err = util.WaitForNamespacedObject(mutateCfg, k8sClient, testNamespace, "network-resources-injector-config", interval, timeout)
			Expect(err).NotTo(HaveOccurred())
		})

		It("should be able to turn operator-webhook on/off", func() {

			By("set disable to enableOperatorWebhook")
			config := &sriovnetworkv1.SriovOperatorConfig{}
			err := util.WaitForNamespacedObject(config, k8sClient, testNamespace, "default", interval, timeout)
			Expect(err).NotTo(HaveOccurred())

			*config.Spec.EnableOperatorWebhook = false
			err = k8sClient.Update(goctx.TODO(), config)
			Expect(err).NotTo(HaveOccurred())

			daemonSet := &appsv1.DaemonSet{}
			err = util.WaitForNamespacedObjectDeleted(daemonSet, k8sClient, testNamespace, "operator-webhook", interval, timeout)
			Expect(err).NotTo(HaveOccurred())

			mutateCfg := &admv1.MutatingWebhookConfiguration{}
			err = util.WaitForNamespacedObjectDeleted(mutateCfg, k8sClient, testNamespace, "sriov-operator-webhook-config", interval, timeout)
			Expect(err).NotTo(HaveOccurred())

			validateCfg := &admv1.ValidatingWebhookConfiguration{}
			err = util.WaitForNamespacedObjectDeleted(validateCfg, k8sClient, testNamespace, "sriov-operator-webhook-config", interval, timeout)
			Expect(err).NotTo(HaveOccurred())

			By("set disable to enableOperatorWebhook")
			err = util.WaitForNamespacedObject(config, k8sClient, testNamespace, "default", interval, timeout)
			Expect(err).NotTo(HaveOccurred())

			*config.Spec.EnableOperatorWebhook = true
			err = k8sClient.Update(goctx.TODO(), config)
			Expect(err).NotTo(HaveOccurred())

			daemonSet = &appsv1.DaemonSet{}
			err = util.WaitForNamespacedObject(daemonSet, k8sClient, testNamespace, "operator-webhook", interval, timeout)
			Expect(err).NotTo(HaveOccurred())

			mutateCfg = &admv1.MutatingWebhookConfiguration{}
			err = util.WaitForNamespacedObject(mutateCfg, k8sClient, testNamespace, "sriov-operator-webhook-config", interval, timeout)
			Expect(err).NotTo(HaveOccurred())

			validateCfg = &admv1.ValidatingWebhookConfiguration{}
			err = util.WaitForNamespacedObject(validateCfg, k8sClient, testNamespace, "sriov-operator-webhook-config", interval, timeout)
			Expect(err).NotTo(HaveOccurred())
		})

		PIt("should be able to update the node selector of sriov-network-config-daemon", func() {
			By("specify the configDaemonNodeSelector")
			config := &sriovnetworkv1.SriovOperatorConfig{}
			err := util.WaitForNamespacedObject(config, k8sClient, testNamespace, "default", interval, timeout)
			Expect(err).NotTo(HaveOccurred())
			config.Spec.ConfigDaemonNodeSelector = map[string]string{"node-role.kubernetes.io/worker": ""}
			err = k8sClient.Update(goctx.TODO(), config)
			Expect(err).NotTo(HaveOccurred())

			daemonSet := &appsv1.DaemonSet{}
			Eventually(func() map[string]string {
				// By("wait for DaemonSet NodeSelector")
				err := k8sClient.Get(goctx.TODO(), types.NamespacedName{Name: "sriov-network-config-daemon", Namespace: testNamespace}, daemonSet)
				if err != nil {
					return nil
				}
				return daemonSet.Spec.Template.Spec.NodeSelector
			}, timeout*10, interval).Should(Equal(config.Spec.ConfigDaemonNodeSelector))
		})

	})
})
