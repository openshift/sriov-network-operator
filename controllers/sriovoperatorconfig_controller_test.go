package controllers

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	admv1 "k8s.io/api/admissionregistration/v1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/golang/mock/gomock"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	sriovnetworkv1 "github.com/k8snetworkplumbingwg/sriov-network-operator/api/v1"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/consts"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/featuregate"
	mock_platforms "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/platforms/mock"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/platforms/openshift"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/vars"
	util "github.com/k8snetworkplumbingwg/sriov-network-operator/test/util"
)

var _ = Describe("SriovOperatorConfig controller", Ordered, func() {
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

		somePolicy := &sriovnetworkv1.SriovNetworkNodePolicy{}
		somePolicy.SetNamespace(testNamespace)
		somePolicy.SetName("some-policy")
		somePolicy.Spec = sriovnetworkv1.SriovNetworkNodePolicySpec{
			NumVfs:       5,
			NodeSelector: map[string]string{"foo": "bar"},
			NicSelector:  sriovnetworkv1.SriovNetworkNicSelector{},
			Priority:     20,
		}
		Expect(k8sClient.Create(context.Background(), somePolicy)).ToNot(HaveOccurred())
		DeferCleanup(func() {
			err := k8sClient.Delete(context.Background(), somePolicy)
			Expect(err).ToNot(HaveOccurred())
		})

		// setup controller manager
		By("Setup controller manager")
		k8sManager, err := setupK8sManagerForTest()
		Expect(err).ToNot(HaveOccurred())

		t := GinkgoT()
		mockCtrl := gomock.NewController(t)
		platformHelper := mock_platforms.NewMockInterface(mockCtrl)
		platformHelper.EXPECT().GetFlavor().Return(openshift.OpenshiftFlavorDefault).AnyTimes()
		platformHelper.EXPECT().IsOpenshiftCluster().Return(false).AnyTimes()
		platformHelper.EXPECT().IsHypershift().Return(false).AnyTimes()

		err = (&SriovOperatorConfigReconciler{
			Client:         k8sManager.GetClient(),
			Scheme:         k8sManager.GetScheme(),
			PlatformHelper: platformHelper,
			FeatureGate:    featuregate.New(),
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

	Context("When is up", func() {
		BeforeEach(func() {
			config := &sriovnetworkv1.SriovOperatorConfig{}
			err := util.WaitForNamespacedObject(config, k8sClient, testNamespace, "default", util.RetryInterval, util.APITimeout)
			Expect(err).NotTo(HaveOccurred())
			config.Spec = sriovnetworkv1.SriovOperatorConfigSpec{
				EnableInjector:        true,
				EnableOperatorWebhook: true,
				LogLevel:              2,
				FeatureGates:          map[string]bool{},
			}
			err = k8sClient.Update(ctx, config)
			Expect(err).NotTo(HaveOccurred())
		})

		It("should have webhook enable", func() {
			mutateCfg := &admv1.MutatingWebhookConfiguration{}
			err := util.WaitForNamespacedObject(mutateCfg, k8sClient, testNamespace, "sriov-operator-webhook-config", util.RetryInterval, util.APITimeout*3)
			Expect(err).NotTo(HaveOccurred())

			validateCfg := &admv1.ValidatingWebhookConfiguration{}
			err = util.WaitForNamespacedObject(validateCfg, k8sClient, testNamespace, "sriov-operator-webhook-config", util.RetryInterval, util.APITimeout*3)
			Expect(err).NotTo(HaveOccurred())
		})

		DescribeTable("should have daemonset enabled by default",
			func(dsName string) {
				// wait for sriov-network-operator to be ready
				daemonSet := &appsv1.DaemonSet{}
				err := util.WaitForNamespacedObject(daemonSet, k8sClient, testNamespace, dsName, util.RetryInterval, util.APITimeout)
				Expect(err).NotTo(HaveOccurred())
				Expect(daemonSet.OwnerReferences).To(HaveLen(1))
				Expect(daemonSet.OwnerReferences[0].Kind).To(Equal("SriovOperatorConfig"))
				Expect(daemonSet.OwnerReferences[0].Name).To(Equal(consts.DefaultConfigName))
			},
			Entry("operator-webhook", "operator-webhook"),
			Entry("network-resources-injector", "network-resources-injector"),
			Entry("sriov-network-config-daemon", "sriov-network-config-daemon"),
			Entry("sriov-device-plugin", "sriov-device-plugin"),
		)

		It("should be able to turn network-resources-injector on/off", func() {
			By("set disable to enableInjector")
			config := &sriovnetworkv1.SriovOperatorConfig{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: testNamespace, Name: "default"}, config)).NotTo(HaveOccurred())

			config.Spec.EnableInjector = false
			err := k8sClient.Update(ctx, config)
			Expect(err).NotTo(HaveOccurred())

			daemonSet := &appsv1.DaemonSet{}
			err = util.WaitForNamespacedObjectDeleted(daemonSet, k8sClient, testNamespace, "network-resources-injector", util.RetryInterval, util.APITimeout)
			Expect(err).NotTo(HaveOccurred())

			mutateCfg := &admv1.MutatingWebhookConfiguration{}
			err = util.WaitForNamespacedObjectDeleted(mutateCfg, k8sClient, testNamespace, "network-resources-injector-config", util.RetryInterval, util.APITimeout)
			Expect(err).NotTo(HaveOccurred())

			By("set enable to enableInjector")
			err = util.WaitForNamespacedObject(config, k8sClient, testNamespace, "default", util.RetryInterval, util.APITimeout)
			Expect(err).NotTo(HaveOccurred())

			config.Spec.EnableInjector = true
			err = k8sClient.Update(ctx, config)
			Expect(err).NotTo(HaveOccurred())

			daemonSet = &appsv1.DaemonSet{}
			err = util.WaitForNamespacedObject(daemonSet, k8sClient, testNamespace, "network-resources-injector", util.RetryInterval, util.APITimeout)
			Expect(err).NotTo(HaveOccurred())

			mutateCfg = &admv1.MutatingWebhookConfiguration{}
			err = util.WaitForNamespacedObject(mutateCfg, k8sClient, testNamespace, "network-resources-injector-config", util.RetryInterval, util.APITimeout)
			Expect(err).NotTo(HaveOccurred())
		})

		It("should be able to turn operator-webhook on/off", func() {

			By("set disable to enableOperatorWebhook")
			config := &sriovnetworkv1.SriovOperatorConfig{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: testNamespace, Name: "default"}, config)).NotTo(HaveOccurred())

			config.Spec.EnableOperatorWebhook = false
			err := k8sClient.Update(ctx, config)
			Expect(err).NotTo(HaveOccurred())

			daemonSet := &appsv1.DaemonSet{}
			err = util.WaitForNamespacedObjectDeleted(daemonSet, k8sClient, testNamespace, "operator-webhook", util.RetryInterval, util.APITimeout)
			Expect(err).NotTo(HaveOccurred())

			mutateCfg := &admv1.MutatingWebhookConfiguration{}
			err = util.WaitForNamespacedObjectDeleted(mutateCfg, k8sClient, testNamespace, "sriov-operator-webhook-config", util.RetryInterval, util.APITimeout)
			Expect(err).NotTo(HaveOccurred())

			validateCfg := &admv1.ValidatingWebhookConfiguration{}
			err = util.WaitForNamespacedObjectDeleted(validateCfg, k8sClient, testNamespace, "sriov-operator-webhook-config", util.RetryInterval, util.APITimeout)
			Expect(err).NotTo(HaveOccurred())

			By("set disable to enableOperatorWebhook")
			Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: testNamespace, Name: "default"}, config)).NotTo(HaveOccurred())

			config.Spec.EnableOperatorWebhook = true
			err = k8sClient.Update(ctx, config)
			Expect(err).NotTo(HaveOccurred())

			daemonSet = &appsv1.DaemonSet{}
			err = util.WaitForNamespacedObject(daemonSet, k8sClient, testNamespace, "operator-webhook", util.RetryInterval, util.APITimeout)
			Expect(err).NotTo(HaveOccurred())

			mutateCfg = &admv1.MutatingWebhookConfiguration{}
			err = util.WaitForNamespacedObject(mutateCfg, k8sClient, testNamespace, "sriov-operator-webhook-config", util.RetryInterval, util.APITimeout)
			Expect(err).NotTo(HaveOccurred())

			validateCfg = &admv1.ValidatingWebhookConfiguration{}
			err = util.WaitForNamespacedObject(validateCfg, k8sClient, testNamespace, "sriov-operator-webhook-config", util.RetryInterval, util.APITimeout)
			Expect(err).NotTo(HaveOccurred())
		})

		// Namespaced resources are deleted via the `.ObjectMeta.OwnerReference` field. That logic can't be tested here because testenv doesn't have built-in controllers
		// (See https://book.kubebuilder.io/reference/envtest#testing-considerations). Since Service and DaemonSet are deleted when default/SriovOperatorConfig is no longer
		// present, it's important that webhook configurations are deleted as well.
		It("should delete the webhooks when SriovOperatorConfig/default is deleted", func() {
			DeferCleanup(k8sClient.Create, context.Background(), makeDefaultSriovOpConfig())

			err := k8sClient.Delete(context.Background(), &sriovnetworkv1.SriovOperatorConfig{
				ObjectMeta: metav1.ObjectMeta{Namespace: testNamespace, Name: "default"},
			})
			Expect(err).NotTo(HaveOccurred())

			assertResourceDoesNotExist(
				schema.GroupVersionKind{Group: "admissionregistration.k8s.io", Kind: "MutatingWebhookConfiguration", Version: "v1"},
				client.ObjectKey{Name: "sriov-operator-webhook-config"})
			assertResourceDoesNotExist(
				schema.GroupVersionKind{Group: "admissionregistration.k8s.io", Kind: "ValidatingWebhookConfiguration", Version: "v1"},
				client.ObjectKey{Name: "sriov-operator-webhook-config"})

			assertResourceDoesNotExist(
				schema.GroupVersionKind{Group: "admissionregistration.k8s.io", Kind: "MutatingWebhookConfiguration", Version: "v1"},
				client.ObjectKey{Name: "network-resources-injector-config"})
		})

		It("should be able to update the node selector of sriov-network-config-daemon", func() {
			By("specify the configDaemonNodeSelector")
			config := &sriovnetworkv1.SriovOperatorConfig{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: testNamespace, Name: "default"}, config)).NotTo(HaveOccurred())

			config.Spec.ConfigDaemonNodeSelector = map[string]string{"node-role.kubernetes.io/worker": ""}
			err := k8sClient.Update(ctx, config)
			Expect(err).NotTo(HaveOccurred())

			daemonSet := &appsv1.DaemonSet{}
			Eventually(func() map[string]string {
				// By("wait for DaemonSet NodeSelector")
				err := k8sClient.Get(ctx, types.NamespacedName{Name: "sriov-network-config-daemon", Namespace: testNamespace}, daemonSet)
				if err != nil {
					return nil
				}
				return daemonSet.Spec.Template.Spec.NodeSelector
			}, util.APITimeout, util.RetryInterval).Should(Equal(config.Spec.ConfigDaemonNodeSelector))
		})

		It("should be able to do multiple updates to the node selector of sriov-network-config-daemon", func() {
			By("changing the configDaemonNodeSelector")
			config := &sriovnetworkv1.SriovOperatorConfig{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: testNamespace, Name: "default"}, config)).NotTo(HaveOccurred())
			config.Spec.ConfigDaemonNodeSelector = map[string]string{"labelA": "", "labelB": "", "labelC": ""}
			err := k8sClient.Update(ctx, config)
			Expect(err).NotTo(HaveOccurred())
			config.Spec.ConfigDaemonNodeSelector = map[string]string{"labelA": "", "labelB": ""}
			err = k8sClient.Update(ctx, config)
			Expect(err).NotTo(HaveOccurred())

			daemonSet := &appsv1.DaemonSet{}
			Eventually(func() map[string]string {
				err := k8sClient.Get(ctx, types.NamespacedName{Name: "sriov-network-config-daemon", Namespace: testNamespace}, daemonSet)
				if err != nil {
					return nil
				}
				return daemonSet.Spec.Template.Spec.NodeSelector
			}, util.APITimeout, util.RetryInterval).Should(Equal(config.Spec.ConfigDaemonNodeSelector))
		})

		It("should not render disable-plugins cmdline flag of sriov-network-config-daemon if disablePlugin not provided in spec", func() {
			config := &sriovnetworkv1.SriovOperatorConfig{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: testNamespace, Name: "default"}, config)).NotTo(HaveOccurred())

			Eventually(func() string {
				daemonSet := &appsv1.DaemonSet{}
				err := k8sClient.Get(ctx, types.NamespacedName{Name: "sriov-network-config-daemon", Namespace: testNamespace}, daemonSet)
				if err != nil {
					return ""
				}
				return strings.Join(daemonSet.Spec.Template.Spec.Containers[0].Args, " ")
			}, util.APITimeout*10, util.RetryInterval).Should(And(Not(BeEmpty()), Not(ContainSubstring("disable-plugins"))))
		})

		It("should render disable-plugins cmdline flag of sriov-network-config-daemon if disablePlugin provided in spec", func() {
			config := &sriovnetworkv1.SriovOperatorConfig{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: testNamespace, Name: "default"}, config)).NotTo(HaveOccurred())

			config.Spec.DisablePlugins = sriovnetworkv1.PluginNameSlice{"mellanox"}
			err := k8sClient.Update(ctx, config)
			Expect(err).NotTo(HaveOccurred())

			Eventually(func() string {
				daemonSet := &appsv1.DaemonSet{}
				err := k8sClient.Get(ctx, types.NamespacedName{Name: "sriov-network-config-daemon", Namespace: testNamespace}, daemonSet)
				if err != nil {
					return ""
				}
				return strings.Join(daemonSet.Spec.Template.Spec.Containers[0].Args, " ")
			}, util.APITimeout*10, util.RetryInterval).Should(ContainSubstring("disable-plugins=mellanox"))
		})

		It("should render the resourceInjectorMatchCondition in the mutation if feature flag is enabled and block only pods with the networks annotation", func() {
			By("set the feature flag")
			config := &sriovnetworkv1.SriovOperatorConfig{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: testNamespace, Name: "default"}, config)).NotTo(HaveOccurred())

			config.Spec.FeatureGates = map[string]bool{}
			config.Spec.FeatureGates[consts.ResourceInjectorMatchConditionFeatureGate] = true
			err := k8sClient.Update(ctx, config)
			Expect(err).NotTo(HaveOccurred())

			By("checking the webhook have all the needed configuration")
			mutateCfg := &admv1.MutatingWebhookConfiguration{}
			err = wait.PollUntilContextTimeout(ctx, util.RetryInterval, util.APITimeout, true, func(ctx context.Context) (done bool, err error) {
				err = k8sClient.Get(ctx, types.NamespacedName{Name: "network-resources-injector-config", Namespace: testNamespace}, mutateCfg)
				if err != nil {
					if errors.IsNotFound(err) {
						return false, nil
					}
					return false, err
				}
				if len(mutateCfg.Webhooks) != 1 {
					return false, nil
				}
				if *mutateCfg.Webhooks[0].FailurePolicy != admv1.Fail {
					return false, nil
				}
				if len(mutateCfg.Webhooks[0].MatchConditions) != 1 {
					return false, nil
				}

				if mutateCfg.Webhooks[0].MatchConditions[0].Name != "include-networks-annotation" {
					return false, nil
				}

				return true, nil
			})
			Expect(err).ToNot(HaveOccurred())
		})

		Context("metricsExporter feature gate", func() {
			When("is disabled", func() {
				It("should not deploy the daemonset", func() {
					daemonSet := &appsv1.DaemonSet{}
					err := k8sClient.Get(ctx, types.NamespacedName{Name: "sriov-metrics-exporter", Namespace: testNamespace}, daemonSet)
					Expect(err).To(HaveOccurred())
					Expect(errors.IsNotFound(err)).To(BeTrue())
				})
			})

			When("is enabled", func() {
				BeforeEach(func() {
					config := &sriovnetworkv1.SriovOperatorConfig{}
					Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: testNamespace, Name: "default"}, config)).NotTo(HaveOccurred())

					By("Turn `metricsExporter` feature gate on")
					config.Spec.FeatureGates = map[string]bool{consts.MetricsExporterFeatureGate: true}
					err := k8sClient.Update(ctx, config)
					Expect(err).NotTo(HaveOccurred())
				})

				It("should deploy the sriov-network-metrics-exporter DaemonSet", func() {
					err := util.WaitForNamespacedObject(&appsv1.DaemonSet{}, k8sClient, testNamespace, "sriov-network-metrics-exporter", util.RetryInterval, util.APITimeout)
					Expect(err).NotTo(HaveOccurred())

					err = util.WaitForNamespacedObject(&corev1.Service{}, k8sClient, testNamespace, "sriov-network-metrics-exporter-service", util.RetryInterval, util.APITimeout)
					Expect(err).ToNot(HaveOccurred())
				})

				It("should deploy extra configuration when the Prometheus operator is installed", func() {
					DeferCleanup(os.Setenv, "METRICS_EXPORTER_PROMETHEUS_OPERATOR_ENABLED", os.Getenv("METRICS_EXPORTER_PROMETHEUS_OPERATOR_ENABLED"))
					os.Setenv("METRICS_EXPORTER_PROMETHEUS_OPERATOR_ENABLED", "true")

					err := util.WaitForNamespacedObject(&rbacv1.Role{}, k8sClient, testNamespace, "prometheus-k8s", util.RetryInterval, util.APITimeout)
					Expect(err).ToNot(HaveOccurred())

					err = util.WaitForNamespacedObject(&rbacv1.RoleBinding{}, k8sClient, testNamespace, "prometheus-k8s", util.RetryInterval, util.APITimeout)
					Expect(err).ToNot(HaveOccurred())

					assertResourceExists(
						schema.GroupVersionKind{
							Group:   "monitoring.coreos.com",
							Kind:    "ServiceMonitor",
							Version: "v1",
						},
						client.ObjectKey{Namespace: testNamespace, Name: "sriov-network-metrics-exporter"})
				})
			})
		})

		// This test verifies that the CABundle field in the webhook configuration  added by third party components is not
		// removed during the reconciliation loop. This is important when dealing with OpenShift certificate mangement:
		// https://docs.openshift.com/container-platform/4.15/security/certificates/service-serving-certificate.html
		// and when CertManager is used
		It("should not remove the field Spec.ClientConfig.CABundle from webhook configuration when reconciling", func() {
			validateCfg := &admv1.ValidatingWebhookConfiguration{}
			err := util.WaitForNamespacedObject(validateCfg, k8sClient, testNamespace, "sriov-operator-webhook-config", util.RetryInterval, util.APITimeout*3)
			Expect(err).NotTo(HaveOccurred())

			By("Simulate a third party component updating the webhook CABundle")
			validateCfg.Webhooks[0].ClientConfig.CABundle = []byte("some-base64-ca-bundle-value")

			err = k8sClient.Update(ctx, validateCfg)
			Expect(err).NotTo(HaveOccurred())

			By("Trigger a controller reconciliation")
			err = util.TriggerSriovOperatorConfigReconcile(k8sClient, testNamespace)
			Expect(err).NotTo(HaveOccurred())

			By("Verify the operator did not remove the CABundle from the webhook configuration")
			Consistently(func(g Gomega) {
				err = k8sClient.Get(ctx, types.NamespacedName{Namespace: testNamespace, Name: "sriov-operator-webhook-config"}, validateCfg)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(validateCfg.Webhooks[0].ClientConfig.CABundle).To(Equal([]byte("some-base64-ca-bundle-value")))
			}, "1s").Should(Succeed())
		})

		It("should update the webhook CABundle if `ADMISSION_CONTROLLERS_CERTIFICATES environment variable are set` ", func() {
			DeferCleanup(os.Setenv, "ADMISSION_CONTROLLERS_CERTIFICATES_OPERATOR_CA_CRT", os.Getenv("ADMISSION_CONTROLLERS_CERTIFICATES_OPERATOR_CA_CRT"))
			// echo "ca-bundle-1" | base64 -w 0
			os.Setenv("ADMISSION_CONTROLLERS_CERTIFICATES_OPERATOR_CA_CRT", "Y2EtYnVuZGxlLTEK")

			DeferCleanup(os.Setenv, "ADMISSION_CONTROLLERS_CERTIFICATES_INJECTOR_CA_CRT", os.Getenv("ADMISSION_CONTROLLERS_CERTIFICATES_INJECTOR_CA_CRT"))
			// echo "ca-bundle-2" | base64 -w 0
			os.Setenv("ADMISSION_CONTROLLERS_CERTIFICATES_INJECTOR_CA_CRT", "Y2EtYnVuZGxlLTIK")

			DeferCleanup(func(old string) { vars.ClusterType = old }, vars.ClusterType)
			vars.ClusterType = consts.ClusterTypeKubernetes

			err := util.TriggerSriovOperatorConfigReconcile(k8sClient, testNamespace)
			Expect(err).NotTo(HaveOccurred())

			Eventually(func(g Gomega) {
				validateCfg := &admv1.ValidatingWebhookConfiguration{}
				err = k8sClient.Get(ctx, types.NamespacedName{Namespace: testNamespace, Name: "sriov-operator-webhook-config"}, validateCfg)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(validateCfg.Webhooks[0].ClientConfig.CABundle).To(Equal([]byte("ca-bundle-1\n")))

				mutateCfg := &admv1.MutatingWebhookConfiguration{}
				err = k8sClient.Get(ctx, types.NamespacedName{Namespace: testNamespace, Name: "sriov-operator-webhook-config"}, mutateCfg)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(mutateCfg.Webhooks[0].ClientConfig.CABundle).To(Equal([]byte("ca-bundle-1\n")))

				injectorCfg := &admv1.MutatingWebhookConfiguration{}
				err = k8sClient.Get(ctx, types.NamespacedName{Namespace: testNamespace, Name: "network-resources-injector-config"}, injectorCfg)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(injectorCfg.Webhooks[0].ClientConfig.CABundle).To(Equal([]byte("ca-bundle-2\n")))
			}, "1s").Should(Succeed())
		})

		It("should reconcile to a converging state when multiple node policies are set", func() {
			By("Creating a consistent number of node policies")
			for i := 0; i < 30; i++ {
				p := &sriovnetworkv1.SriovNetworkNodePolicy{
					ObjectMeta: metav1.ObjectMeta{Namespace: testNamespace, Name: fmt.Sprintf("p%d", i)},
					Spec: sriovnetworkv1.SriovNetworkNodePolicySpec{
						Priority:     99,
						NodeSelector: map[string]string{"foo": fmt.Sprintf("v%d", i)},
					},
				}
				err := k8sClient.Create(context.Background(), p)
				Expect(err).NotTo(HaveOccurred())
			}

			By("Triggering a the reconcile loop")
			config := &sriovnetworkv1.SriovOperatorConfig{}
			err := k8sClient.Get(context.Background(), types.NamespacedName{Name: "default", Namespace: testNamespace}, config)
			Expect(err).NotTo(HaveOccurred())
			if config.ObjectMeta.Labels == nil {
				config.ObjectMeta.Labels = make(map[string]string)
			}
			config.ObjectMeta.Labels["trigger-test"] = "test-reconcile-daemonset"
			err = k8sClient.Update(context.Background(), config)
			Expect(err).NotTo(HaveOccurred())

			By("Wait until device-plugin Daemonset's affinity has been calculated")
			var expectedAffinity *corev1.Affinity

			Eventually(func(g Gomega) {
				daemonSet := &appsv1.DaemonSet{}
				err = k8sClient.Get(context.Background(), types.NamespacedName{Name: "sriov-device-plugin", Namespace: testNamespace}, daemonSet)
				g.Expect(err).NotTo(HaveOccurred())
				// Wait until the last policy (with NodeSelector foo=v29) has been considered at least one time
				g.Expect(daemonSet.Spec.Template.Spec.Affinity.String()).To(ContainSubstring("v29"))
				expectedAffinity = daemonSet.Spec.Template.Spec.Affinity
			}, "3s", "1s").Should(Succeed())

			By("Verify device-plugin Daemonset's affinity doesn't change over time")
			Consistently(func(g Gomega) {
				daemonSet := &appsv1.DaemonSet{}
				err = k8sClient.Get(context.Background(), types.NamespacedName{Name: "sriov-device-plugin", Namespace: testNamespace}, daemonSet)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(daemonSet.Spec.Template.Spec.Affinity).
					To(Equal(expectedAffinity))
			}, "3s", "1s").Should(Succeed())
		})
	})
})

func makeDefaultSriovOpConfig() *sriovnetworkv1.SriovOperatorConfig {
	config := &sriovnetworkv1.SriovOperatorConfig{}
	config.SetNamespace(testNamespace)
	config.SetName(consts.DefaultConfigName)
	config.Spec = sriovnetworkv1.SriovOperatorConfigSpec{
		EnableInjector:           true,
		EnableOperatorWebhook:    true,
		ConfigDaemonNodeSelector: map[string]string{},
		LogLevel:                 2,
	}
	return config
}

func assertResourceExists(gvk schema.GroupVersionKind, key client.ObjectKey) {
	u := &unstructured.Unstructured{}
	u.SetGroupVersionKind(gvk)
	err := k8sClient.Get(context.Background(), key, u)
	Expect(err).NotTo(HaveOccurred())
}

func assertResourceDoesNotExist(gvk schema.GroupVersionKind, key client.ObjectKey) {
	Eventually(func(g Gomega) {
		u := &unstructured.Unstructured{}
		u.SetGroupVersionKind(gvk)
		err := k8sClient.Get(context.Background(), key, u)
		g.Expect(err).To(HaveOccurred())
		g.Expect(errors.IsNotFound(err)).To(BeTrue())
	}).
		WithOffset(1).
		WithPolling(100*time.Millisecond).
		WithTimeout(2*time.Second).
		Should(Succeed(), "Resource type[%s] name[%s] still present in the cluster", gvk.String(), key.String())
}
