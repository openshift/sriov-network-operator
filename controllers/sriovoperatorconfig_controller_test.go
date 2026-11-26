package controllers

import (
	"context"
	"fmt"
	"maps"
	"os"
	"strings"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/go-logr/logr"
	"go.uber.org/mock/gomock"
	admv1 "k8s.io/api/admissionregistration/v1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkv1 "k8s.io/api/networking/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	resourceapi "k8s.io/api/resource/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	sriovnetworkv1 "github.com/k8snetworkplumbingwg/sriov-network-operator/api/v1"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/consts"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/featuregate"
	orchestratorMock "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/orchestrator/mock"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/render"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/status"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/vars"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/test/util"
)

type countingFeatureGate struct {
	featuregate.FeatureGate
	initCalls int
}

func (g *countingFeatureGate) Set(features map[string]bool) bool {
	if !g.FeatureGate.Set(features) {
		return false
	}
	g.initCalls++
	return true
}

var _ = Describe("SriovOperatorConfig controller with a fake client", func() {
	BeforeEach(func() {
		savedGate := vars.FeatureGate
		DeferCleanup(func() { vars.FeatureGate = savedGate })
		vars.FeatureGate = featuregate.New()
	})

	It("should initialize feature gates only when the effective configuration changes", func() {
		ctx := context.Background()
		scheme := runtime.NewScheme()
		Expect(sriovnetworkv1.AddToScheme(scheme)).To(Succeed())
		config := &sriovnetworkv1.SriovOperatorConfig{
			ObjectMeta: metav1.ObjectMeta{Name: consts.DefaultConfigName, Namespace: vars.Namespace},
		}
		policy := &sriovnetworkv1.SriovNetworkNodePolicy{
			ObjectMeta: metav1.ObjectMeta{Name: "existing", Namespace: vars.Namespace},
		}
		cl := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(config).WithObjects(config, policy).Build()
		gate := &countingFeatureGate{FeatureGate: vars.FeatureGate}
		vars.FeatureGate = gate
		orch := orchestratorMock.NewMockInterface(gomock.NewController(GinkgoT()))
		orch.EXPECT().ClusterType().Return(consts.ClusterTypeKubernetes).AnyTimes()
		orch.EXPECT().GetTLSConfig(gomock.Any()).Return(nil, nil).AnyTimes()
		renderErr := fmt.Errorf("render failed after feature gates were applied")
		r := &SriovOperatorConfigReconciler{
			Client: cl, Scheme: scheme,
			Orchestrator: orch, StatusPatcher: &fakeStatusPatcher{},
			renderManifestFn: func(string, *render.RenderData) ([]*unstructured.Unstructured, error) {
				return nil, renderErr
			},
		}

		steps := []struct {
			name      string
			change    func()
			initCalls int
			dra       bool
			parallel  bool
			block     bool
		}{
			{name: "initial defaults", initCalls: 1, block: true},
			{name: "retry after downstream failure", initCalls: 1, block: true},
			{name: "unrelated spec and status changes", initCalls: 1, block: true, change: func() {
				config.Spec.DisableDrain = true
				config.Status.Conditions = []metav1.Condition{{Type: "Ready", Status: metav1.ConditionFalse, Reason: "Retrying"}}
			}},
			{name: "changed overrides", initCalls: 2, parallel: true, change: func() {
				config.Spec.FeatureGates = map[string]bool{
					consts.ParallelNicConfigFeatureGate:                true,
					consts.BlockDevicePluginUntilConfiguredFeatureGate: false,
				}
			}},
			{name: "enable DRA", initCalls: 3, dra: true, parallel: true, change: func() {
				config.Spec.FeatureGates[consts.DynamicResourceAllocationFeatureGate] = true
			}},
			{name: "unchanged DRA", initCalls: 3, dra: true, parallel: true},
			{name: "removed overrides restore defaults", initCalls: 4, block: true, change: func() {
				config.Spec.FeatureGates = nil
			}},
			{name: "unchanged defaults", initCalls: 4, block: true},
		}
		for _, step := range steps {
			By(step.name, func() {
				Expect(cl.Get(ctx, client.ObjectKeyFromObject(config), config)).To(Succeed())
				if step.change != nil {
					step.change()
					updatedStatus := config.Status
					Expect(cl.Update(ctx, config)).To(Succeed())
					config.Status = updatedStatus
					Expect(cl.Status().Update(ctx, config)).To(Succeed())
				}
				_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(config)})
				Expect(err).To(MatchError(renderErr))
				Expect(gate.initCalls).To(Equal(step.initCalls))
				Expect(gate.IsEnabled(consts.DynamicResourceAllocationFeatureGate)).To(Equal(step.dra))
				Expect(gate.IsEnabled(consts.ParallelNicConfigFeatureGate)).To(Equal(step.parallel))
				Expect(gate.IsEnabled(consts.BlockDevicePluginUntilConfiguredFeatureGate)).To(Equal(step.block))
			})
		}
	})

	DescribeTable("should clean generated device classes before finalizing operator config deletion", func(failure string) {
		ctx := context.Background()
		scheme := runtime.NewScheme()
		for _, add := range []func(*runtime.Scheme) error{
			sriovnetworkv1.AddToScheme, admv1.AddToScheme, resourceapi.AddToScheme,
		} {
			Expect(add(scheme)).To(Succeed())
		}
		config := &sriovnetworkv1.SriovOperatorConfig{ObjectMeta: metav1.ObjectMeta{
			Name: consts.DefaultConfigName, Namespace: testNamespace,
			Finalizers:        []string{sriovnetworkv1.OPERATORCONFIGFINALIZERNAME},
			DeletionTimestamp: &metav1.Time{Time: metav1.Now().Time},
		}}
		generated := &resourceapi.DeviceClass{ObjectMeta: metav1.ObjectMeta{
			Name: "intel-nic", Labels: maps.Clone(consts.DRAOperatorLabels),
		}}
		base := &resourceapi.DeviceClass{ObjectMeta: metav1.ObjectMeta{
			Name: consts.DRADriverBaseDeviceClassName, Labels: maps.Clone(consts.DRAOperatorLabels),
		}}
		foreign := &resourceapi.DeviceClass{ObjectMeta: metav1.ObjectMeta{Name: "user-managed"}}
		cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(config, generated, base, foreign).
			WithInterceptorFuncs(interceptor.Funcs{
				List: func(ctx context.Context, c client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
					if _, ok := list.(*resourceapi.DeviceClassList); ok && failure == "list" {
						return fmt.Errorf("DeviceClass list failed")
					}
					return c.List(ctx, list, opts...)
				},
				Delete: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.DeleteOption) error {
					if obj.GetName() == generated.Name && failure == "delete" {
						return fmt.Errorf("DeviceClass delete failed")
					}
					return c.Delete(ctx, obj, opts...)
				},
			}).Build()
		r := &SriovOperatorConfigReconciler{Client: cl, Scheme: scheme,
			renderManifestFn: func(_ string, _ *render.RenderData) ([]*unstructured.Unstructured, error) {
				// The driver inventory contains the base class, not generated classes.
				return []*unstructured.Unstructured{{Object: map[string]interface{}{
					"apiVersion": "resource.k8s.io/v1", "kind": "DeviceClass",
					"metadata": map[string]interface{}{"name": base.Name},
				}}}, nil
			},
		}
		_, err := r.handleSriovOperatorConfigDeletion(ctx, config.DeepCopy(), logr.Discard())
		if failure != "" {
			Expect(err).To(HaveOccurred(), "expected cleanup failure to block finalization")
			found := &sriovnetworkv1.SriovOperatorConfig{}
			Expect(cl.Get(ctx, client.ObjectKeyFromObject(config), found)).To(Succeed())
			Expect(sriovnetworkv1.StringInArray(sriovnetworkv1.OPERATORCONFIGFINALIZERNAME, found.Finalizers)).To(BeTrue(), "cleanup failure removed config finalizer")
			// A retry must finish cleanup once the API recovers.
			failure = ""
			_, err = r.handleSriovOperatorConfigDeletion(ctx, found, logr.Discard())
		}
		Expect(err).NotTo(HaveOccurred())
		for _, obj := range []client.Object{config, generated, base} {
			err := cl.Get(ctx, client.ObjectKeyFromObject(obj), obj)
			Expect(errors.IsNotFound(err)).To(BeTrue(), "expected %T %s deleted, got %v", obj, obj.GetName(), err)
		}
		Expect(cl.Get(ctx, client.ObjectKeyFromObject(foreign), foreign)).To(Succeed(), "unrelated DeviceClass must be retained")
	},
		Entry("when cleanup succeeds", ""),
		Entry("after retrying a list failure", "list"),
		Entry("after retrying a delete failure", "delete"),
	)

	Context("when retaining the device plugin ConfigMap", func() {
		DescribeTable("should reconcile the ConfigMap with the provider transition", func(draEnabled, pluginExists, configMapExists bool, failure string) {
			ctx := context.Background()
			scheme := runtime.NewScheme()
			for _, add := range []func(*runtime.Scheme) error{
				sriovnetworkv1.AddToScheme, corev1.AddToScheme, appsv1.AddToScheme,
				rbacv1.AddToScheme, resourceapi.AddToScheme,
			} {
				Expect(add(scheme)).To(Succeed())
			}
			config := makeDefaultSriovOpConfig()
			config.Spec.FeatureGates = map[string]bool{consts.DynamicResourceAllocationFeatureGate: draEnabled}
			cm := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{Name: consts.ConfigMapName, Namespace: testNamespace},
				Data:       map[string]string{"worker-0": `{"resourceList":[]}`},
			}
			objects := []client.Object{config}
			if configMapExists {
				objects = append(objects, cm)
			}
			if pluginExists {
				objects = append(objects, &appsv1.DaemonSet{
					ObjectMeta: metav1.ObjectMeta{
						Name: devicePluginDaemonSetName, Namespace: testNamespace,
						OwnerReferences: providerOwnerReferences("SriovOperatorConfig"),
					},
				})
			}
			configMapDeleteAttempts := 0
			cl := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(config).WithObjects(objects...).WithInterceptorFuncs(interceptor.Funcs{
				Delete: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.DeleteOption) error {
					if failure == "plugin cleanup" && obj.GetName() == devicePluginDaemonSetName {
						return fmt.Errorf("plugin cleanup failed")
					}
					if obj.GetName() == consts.ConfigMapName {
						configMapDeleteAttempts++
						if failure == "ConfigMap cleanup" {
							return fmt.Errorf("ConfigMap cleanup failed")
						}
					}
					return c.Delete(ctx, obj, opts...)
				},
			}).Build()
			orchestrator := orchestratorMock.NewMockInterface(gomock.NewController(GinkgoT()))
			orchestrator.EXPECT().ClusterType().Return(consts.ClusterTypeKubernetes).AnyTimes()
			orchestrator.EXPECT().GetTLSConfig(gomock.Any()).Return(nil, nil).AnyTimes()
			r := &SriovOperatorConfigReconciler{
				Client: cl, Scheme: scheme,
				Orchestrator: orchestrator, StatusPatcher: &fakeStatusPatcher{},
				renderManifestFn: func(path string, data *render.RenderData) ([]*unstructured.Unstructured, error) {
					// Exercise the real provider manifests; unrelated components need no objects here.
					if path == consts.PluginPath || path == consts.DRADriverPath {
						return testRenderManifests(path, data)
					}
					return nil, nil
				},
				applyManifestFn: func(ctx context.Context, c client.Client, obj *unstructured.Unstructured) error {
					if failure == "DRA deployment" {
						return fmt.Errorf("DRA deployment failed")
					}
					return testApplyManifest(ctx, c, obj)
				},
			}
			_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(config)})
			Expect(configMapDeleteAttempts).To(BeZero(), "reconciliation must not attempt to delete the device-plugin ConfigMap")
			if failure != "" && failure != "ConfigMap cleanup" {
				Expect(err).To(MatchError(ContainSubstring(failure + " failed")))
			} else {
				Expect(err).NotTo(HaveOccurred())
			}
			found := &corev1.ConfigMap{}
			err = cl.Get(ctx, client.ObjectKeyFromObject(cm), found)
			if configMapExists {
				Expect(err).NotTo(HaveOccurred())
				Expect(found.Data).To(Equal(cm.Data))
			} else {
				Expect(errors.IsNotFound(err)).To(BeTrue())
			}
			if failure == "" {
				if draEnabled {
					Expect(errors.IsNotFound(cl.Get(ctx, client.ObjectKey{Namespace: testNamespace, Name: devicePluginDaemonSetName}, &appsv1.DaemonSet{}))).To(BeTrue())
				}
				_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(config)})
				Expect(err).NotTo(HaveOccurred())
				err = cl.Get(ctx, client.ObjectKeyFromObject(cm), found)
				if configMapExists {
					Expect(err).NotTo(HaveOccurred())
					Expect(found.Data).To(Equal(cm.Data))
				} else {
					Expect(errors.IsNotFound(err)).To(BeTrue())
				}
			}
		},
			Entry("retains configuration after a successful switch", true, true, true, ""),
			Entry("retains configuration when deployment fails", true, true, true, "DRA deployment"),
			Entry("retains configuration when plugin cleanup fails", true, true, true, "plugin cleanup"),
			Entry("never attempts ConfigMap deletion", true, true, true, "ConfigMap cleanup"),
			Entry("retains configuration after the plugin is gone", true, false, true, ""),
			Entry("succeeds when configuration is already absent", true, false, false, ""),
			Entry("retains configuration in device-plugin mode", false, true, true, ""),
		)
	})

	Context("when deploying a resource provider", func() {
		DescribeTable("should deploy the selected provider before removing the previous one without waiting for readiness", func(draEnabled, failInstall bool) {
			ctx := context.Background()
			savedNamespace := vars.Namespace
			vars.Namespace = testNamespace
			DeferCleanup(func() { vars.Namespace = savedNamespace })
			scheme := runtime.NewScheme()
			for _, add := range []func(*runtime.Scheme) error{sriovnetworkv1.AddToScheme, appsv1.AddToScheme, corev1.AddToScheme, rbacv1.AddToScheme, resourceapi.AddToScheme} {
				Expect(add(scheme)).To(Succeed())
			}
			config := makeDefaultSriovOpConfig()
			config.Spec.FeatureGates = map[string]bool{consts.DynamicResourceAllocationFeatureGate: draEnabled}
			replacement, previous := devicePluginDaemonSetName, consts.DRADriverDaemonSetName
			if draEnabled {
				replacement, previous = previous, replacement
			}
			old := &appsv1.DaemonSet{ObjectMeta: metav1.ObjectMeta{Name: previous, Namespace: vars.Namespace, OwnerReferences: providerOwnerReferences("SriovOperatorConfig")}}
			cl := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&appsv1.DaemonSet{}).WithObjects(old).
				WithInterceptorFuncs(interceptor.Funcs{
					Create: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
						ds, ok := obj.(*appsv1.DaemonSet)
						if !ok || ds.Name != replacement {
							return c.Create(ctx, obj, opts...)
						}
						Expect(c.Get(ctx, client.ObjectKeyFromObject(old), &appsv1.DaemonSet{})).To(Succeed(), "old provider must remain until deployment succeeds")
						if failInstall {
							return fmt.Errorf("deployment failed")
						}
						if err := c.Create(ctx, ds, opts...); err != nil {
							return err
						}
						ds.Status.DesiredNumberScheduled = 1
						return c.Status().Update(ctx, ds)
					},
				}).Build()
			r := &SriovOperatorConfigReconciler{
				Client: cl, Scheme: scheme,
				renderManifestFn: testRenderManifests, applyManifestFn: testApplyManifest,
			}
			vars.FeatureGate.Set(config.Spec.FeatureGates)
			err := r.syncResourceProvider(ctx, config)
			if failInstall {
				Expect(err).To(MatchError(ContainSubstring("deployment failed")))
				Expect(cl.Get(ctx, client.ObjectKeyFromObject(old), old)).To(Succeed())
				return
			}
			Expect(err).NotTo(HaveOccurred())
			ds := &appsv1.DaemonSet{}
			Expect(cl.Get(ctx, client.ObjectKey{Namespace: vars.Namespace, Name: replacement}, ds)).To(Succeed())
			Expect(ds.Status.DesiredNumberScheduled).To(Equal(int32(1)))
			Expect(ds.Status.NumberReady).To(BeZero(), "the fake client has no DaemonSet controller")
			Expect(errors.IsNotFound(cl.Get(ctx, client.ObjectKeyFromObject(old), old))).To(BeTrue())
			Expect(r.syncResourceProvider(ctx, config)).To(Succeed(), "repeated reconciliation should converge")
		},
			Entry("device plugin to DRA", true, false),
			Entry("DRA to device plugin", false, false),
			Entry("failed DRA deployment retains device plugin", true, true),
			Entry("failed device-plugin deployment retains DRA", false, true),
		)
	})
})

var _ = Describe("SriovOperatorConfig controller", Ordered, func() {
	var cancel context.CancelFunc
	var ctx context.Context
	var mockedTLSConfig *consts.TLSConfig
	var mockedTLSError error

	BeforeAll(func() {
		savedGate := vars.FeatureGate
		// Registered before manager cleanup so the manager stops before restoration.
		DeferCleanup(func() { vars.FeatureGate = savedGate })
		vars.FeatureGate = featuregate.New()

		By("Create SriovOperatorConfig controller k8s objs")
		config := makeDefaultSriovOpConfig()
		Expect(k8sClient.Create(context.Background(), config)).Should(Succeed())

		somePolicy := &sriovnetworkv1.SriovNetworkNodePolicy{}
		somePolicy.SetNamespace(testNamespace)
		somePolicy.SetName("some-policy")
		somePolicy.Spec = sriovnetworkv1.SriovNetworkNodePolicySpec{
			ResourceName: "sriov",
			NumVfs:       5,
			NodeSelector: map[string]string{"foo": "bar"},
			NicSelector:  sriovnetworkv1.SriovNetworkNicSelector{},
			Priority:     20,
		}
		Expect(k8sClient.Create(context.Background(), somePolicy)).ToNot(HaveOccurred())

		// setup controller manager
		By("Setup controller manager")
		k8sManager, err := setupK8sManagerForTest()
		Expect(err).ToNot(HaveOccurred())
		statusPatcher := status.NewPatcher(k8sManager.GetClient(), k8sManager.GetEventRecorder("test"), k8sManager.GetScheme(), "test")

		t := GinkgoT()
		mockCtrl := gomock.NewController(t)
		orchestrator := orchestratorMock.NewMockInterface(mockCtrl)

		orchestrator.EXPECT().ClusterType().DoAndReturn(func() consts.ClusterType {
			if vars.ClusterType == consts.ClusterTypeOpenshift {
				return consts.ClusterTypeOpenshift
			}
			return consts.ClusterTypeKubernetes
		}).AnyTimes()

		// TODO: Change this to add tests for hypershift
		orchestrator.EXPECT().Flavor().Return(consts.ClusterFlavorDefault).AnyTimes()

		// Mock GetTLSConfig to return dynamic values controlled by each test.
		orchestrator.EXPECT().GetTLSConfig(gomock.Any()).DoAndReturn(
			func(_ context.Context) (*consts.TLSConfig, error) {
				return mockedTLSConfig, mockedTLSError
			}).AnyTimes()

		err = (&SriovOperatorConfigReconciler{
			Client:            k8sManager.GetClient(),
			Scheme:            k8sManager.GetScheme(),
			Orchestrator:      orchestrator,
			StatusPatcher:     statusPatcher,
			UncachedAPIReader: k8sManager.GetAPIReader(),
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
		AfterAll(func() {
			err := k8sClient.DeleteAllOf(context.Background(), &corev1.Node{})
			Expect(err).ToNot(HaveOccurred())

			err = k8sClient.DeleteAllOf(context.Background(), &sriovnetworkv1.SriovNetworkNodePolicy{}, client.InNamespace(vars.Namespace))
			Expect(err).ToNot(HaveOccurred())

			err = k8sClient.DeleteAllOf(context.Background(), &sriovnetworkv1.SriovNetworkNodeState{}, client.InNamespace(vars.Namespace))
			Expect(err).ToNot(HaveOccurred())

			err = k8sClient.DeleteAllOf(context.Background(), &sriovnetworkv1.SriovOperatorConfig{}, client.InNamespace(vars.Namespace))
			Expect(err).ToNot(HaveOccurred())

			operatorConfigList := &sriovnetworkv1.SriovOperatorConfigList{}
			Eventually(func(g Gomega) {
				err = k8sClient.List(context.Background(), operatorConfigList, &client.ListOptions{Namespace: vars.Namespace})
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(len(operatorConfigList.Items)).To(Equal(0))
			}, time.Minute, time.Second).Should(Succeed())
		})

		BeforeEach(func() {
			mockedTLSConfig = nil
			mockedTLSError = nil

			var err error
			config := &sriovnetworkv1.SriovOperatorConfig{}

			Eventually(func(g Gomega) {
				err = util.WaitForNamespacedObject(config, k8sClient, testNamespace, "default", util.RetryInterval, util.APITimeout)
				g.Expect(err).NotTo(HaveOccurred())
				// in case controller yet to add object's finalizer (e.g whenever test deferCleanup is creating new 'default' config object)
				g.Expect(config.Finalizers).ToNot(BeEmpty())

				config.Spec = sriovnetworkv1.SriovOperatorConfigSpec{
					EnableInjector:        true,
					EnableOperatorWebhook: true,
					LogLevel:              2,
					FeatureGates:          map[string]bool{},
				}
				err = k8sClient.Update(ctx, config)
				g.Expect(err).NotTo(HaveOccurred())
			}, util.APITimeout, util.RetryInterval).Should(Succeed())

		})

		It("should have webhook enable", func() {
			mutateCfg := &admv1.MutatingWebhookConfiguration{}
			err := util.WaitForNamespacedObject(mutateCfg, k8sClient, testNamespace, "sriov-operator-webhook-config", util.RetryInterval, util.APITimeout*3)
			Expect(err).NotTo(HaveOccurred())

			validateCfg := &admv1.ValidatingWebhookConfiguration{}
			err = util.WaitForNamespacedObject(validateCfg, k8sClient, testNamespace, "sriov-operator-webhook-config", util.RetryInterval, util.APITimeout*3)
			Expect(err).NotTo(HaveOccurred())
		})

		It("should configure TLS profile flags for webhook operands", func() {
			mockedTLSConfig = &consts.TLSConfig{
				CipherSuites:     "TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256",
				MinTLSVersion:    "VersionTLS13",
				CurvePreferences: "X25519,secp256r1,secp384r1",
			}

			err := util.TriggerSriovOperatorConfigReconcile(k8sClient, testNamespace)
			Expect(err).NotTo(HaveOccurred())

			Eventually(func(g Gomega) {
				operatorWebhookDS := &appsv1.DaemonSet{}
				err := k8sClient.Get(ctx, types.NamespacedName{Name: "operator-webhook", Namespace: testNamespace}, operatorWebhookDS)
				g.Expect(err).NotTo(HaveOccurred())
				operatorArgs := strings.Join(operatorWebhookDS.Spec.Template.Spec.Containers[0].Args, " ")
				g.Expect(operatorArgs).To(ContainSubstring("--tls-cipher-suites=TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256"))
				g.Expect(operatorArgs).To(ContainSubstring("--tls-min-version=VersionTLS13"))
				g.Expect(operatorArgs).To(ContainSubstring("--tls-curve-preferences=29,23,24"))

				injectorDS := &appsv1.DaemonSet{}
				err = k8sClient.Get(ctx, types.NamespacedName{Name: "network-resources-injector", Namespace: testNamespace}, injectorDS)
				g.Expect(err).NotTo(HaveOccurred())
				injectorArgs := strings.Join(injectorDS.Spec.Template.Spec.Containers[0].Args, " ")
				g.Expect(injectorArgs).To(ContainSubstring("-tls-cipher-suites=TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256"))
				g.Expect(injectorArgs).To(ContainSubstring("-tls-min-version=VersionTLS13"))
				g.Expect(injectorArgs).To(ContainSubstring("-tls-curve-preferences=29,23,24"))
			}, util.APITimeout, util.RetryInterval).Should(Succeed())
		})

		It("should remove TLS profile flags when no TLS config is provided", func() {
			mockedTLSConfig = &consts.TLSConfig{
				CipherSuites:     "TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256",
				MinTLSVersion:    "VersionTLS13",
				CurvePreferences: "X25519,secp256r1",
			}

			err := util.TriggerSriovOperatorConfigReconcile(k8sClient, testNamespace)
			Expect(err).NotTo(HaveOccurred())

			Eventually(func(g Gomega) {
				operatorWebhookDS := &appsv1.DaemonSet{}
				err := k8sClient.Get(ctx, types.NamespacedName{Name: "operator-webhook", Namespace: testNamespace}, operatorWebhookDS)
				g.Expect(err).NotTo(HaveOccurred())
				operatorArgs := strings.Join(operatorWebhookDS.Spec.Template.Spec.Containers[0].Args, " ")
				g.Expect(operatorArgs).To(ContainSubstring("--tls-cipher-suites=TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256"))
				g.Expect(operatorArgs).To(ContainSubstring("--tls-min-version=VersionTLS13"))
			}, util.APITimeout, util.RetryInterval).Should(Succeed())

			mockedTLSConfig = nil
			err = util.TriggerSriovOperatorConfigReconcile(k8sClient, testNamespace)
			Expect(err).NotTo(HaveOccurred())

			Eventually(func(g Gomega) {
				operatorWebhookDS := &appsv1.DaemonSet{}
				err := k8sClient.Get(ctx, types.NamespacedName{Name: "operator-webhook", Namespace: testNamespace}, operatorWebhookDS)
				g.Expect(err).NotTo(HaveOccurred())
				operatorArgs := strings.Join(operatorWebhookDS.Spec.Template.Spec.Containers[0].Args, " ")
				g.Expect(operatorArgs).NotTo(ContainSubstring("--tls-cipher-suites="))
				g.Expect(operatorArgs).NotTo(ContainSubstring("--tls-min-version="))
				g.Expect(operatorArgs).NotTo(ContainSubstring("--tls-curve-preferences="))

				injectorDS := &appsv1.DaemonSet{}
				err = k8sClient.Get(ctx, types.NamespacedName{Name: "network-resources-injector", Namespace: testNamespace}, injectorDS)
				g.Expect(err).NotTo(HaveOccurred())
				injectorArgs := strings.Join(injectorDS.Spec.Template.Spec.Containers[0].Args, " ")
				g.Expect(injectorArgs).NotTo(ContainSubstring("-tls-cipher-suites="))
				g.Expect(injectorArgs).NotTo(ContainSubstring("-tls-min-version="))
				g.Expect(injectorArgs).NotTo(ContainSubstring("-tls-curve-preferences="))
			}, util.APITimeout, util.RetryInterval).Should(Succeed())
		})

		It("should configure curve preferences flags via TLS_CURVE_PREFERENCES env var", func() {
			DeferCleanup(os.Setenv, "TLS_CURVE_PREFERENCES", os.Getenv("TLS_CURVE_PREFERENCES"))
			os.Setenv("TLS_CURVE_PREFERENCES", "X25519,secp256r1")

			mockedTLSConfig = nil

			err := util.TriggerSriovOperatorConfigReconcile(k8sClient, testNamespace)
			Expect(err).NotTo(HaveOccurred())

			Eventually(func(g Gomega) {
				operatorWebhookDS := &appsv1.DaemonSet{}
				err := k8sClient.Get(ctx, types.NamespacedName{Name: "operator-webhook", Namespace: testNamespace}, operatorWebhookDS)
				g.Expect(err).NotTo(HaveOccurred())
				operatorArgs := strings.Join(operatorWebhookDS.Spec.Template.Spec.Containers[0].Args, " ")
				g.Expect(operatorArgs).To(ContainSubstring("--tls-curve-preferences=29,23"))

				injectorDS := &appsv1.DaemonSet{}
				err = k8sClient.Get(ctx, types.NamespacedName{Name: "network-resources-injector", Namespace: testNamespace}, injectorDS)
				g.Expect(err).NotTo(HaveOccurred())
				injectorArgs := strings.Join(injectorDS.Spec.Template.Spec.Containers[0].Args, " ")
				g.Expect(injectorArgs).To(ContainSubstring("-tls-curve-preferences=29,23"))
			}, util.APITimeout, util.RetryInterval).Should(Succeed())
		})

		It("should fall back to webhook defaults when TLS config retrieval fails", func() {
			mockedTLSError = errors.NewServiceUnavailable("unable to read TLS profile")

			err := util.TriggerSriovOperatorConfigReconcile(k8sClient, testNamespace)
			Expect(err).NotTo(HaveOccurred())

			Eventually(func(g Gomega) {
				operatorWebhookDS := &appsv1.DaemonSet{}
				err := k8sClient.Get(ctx, types.NamespacedName{Name: "operator-webhook", Namespace: testNamespace}, operatorWebhookDS)
				g.Expect(err).NotTo(HaveOccurred())
				operatorArgs := strings.Join(operatorWebhookDS.Spec.Template.Spec.Containers[0].Args, " ")
				g.Expect(operatorArgs).NotTo(ContainSubstring("--tls-cipher-suites="))
				g.Expect(operatorArgs).NotTo(ContainSubstring("--tls-min-version="))

				injectorDS := &appsv1.DaemonSet{}
				err = k8sClient.Get(ctx, types.NamespacedName{Name: "network-resources-injector", Namespace: testNamespace}, injectorDS)
				g.Expect(err).NotTo(HaveOccurred())
				injectorArgs := strings.Join(injectorDS.Spec.Template.Spec.Containers[0].Args, " ")
				g.Expect(injectorArgs).NotTo(ContainSubstring("-tls-cipher-suites="))
				g.Expect(injectorArgs).NotTo(ContainSubstring("-tls-min-version="))
			}, util.APITimeout, util.RetryInterval).Should(Succeed())
		})

		It("should use TLS environment variables when orchestrator returns nil", func() {
			DeferCleanup(os.Setenv, "TLS_CIPHER_SUITES", os.Getenv("TLS_CIPHER_SUITES"))
			DeferCleanup(os.Setenv, "TLS_MIN_VERSION", os.Getenv("TLS_MIN_VERSION"))
			os.Setenv("TLS_CIPHER_SUITES", "TLS_AES_128_GCM_SHA256,TLS_AES_256_GCM_SHA384")
			os.Setenv("TLS_MIN_VERSION", "VersionTLS12")

			mockedTLSConfig = nil

			err := util.TriggerSriovOperatorConfigReconcile(k8sClient, testNamespace)
			Expect(err).NotTo(HaveOccurred())

			Eventually(func(g Gomega) {
				operatorWebhookDS := &appsv1.DaemonSet{}
				err := k8sClient.Get(ctx, types.NamespacedName{Name: "operator-webhook", Namespace: testNamespace}, operatorWebhookDS)
				g.Expect(err).NotTo(HaveOccurred())
				operatorArgs := strings.Join(operatorWebhookDS.Spec.Template.Spec.Containers[0].Args, " ")
				g.Expect(operatorArgs).To(ContainSubstring("--tls-cipher-suites=TLS_AES_128_GCM_SHA256,TLS_AES_256_GCM_SHA384"))
				g.Expect(operatorArgs).To(ContainSubstring("--tls-min-version=VersionTLS12"))

				injectorDS := &appsv1.DaemonSet{}
				err = k8sClient.Get(ctx, types.NamespacedName{Name: "network-resources-injector", Namespace: testNamespace}, injectorDS)
				g.Expect(err).NotTo(HaveOccurred())
				injectorArgs := strings.Join(injectorDS.Spec.Template.Spec.Containers[0].Args, " ")
				g.Expect(injectorArgs).To(ContainSubstring("-tls-cipher-suites=TLS_AES_128_GCM_SHA256,TLS_AES_256_GCM_SHA384"))
				g.Expect(injectorArgs).To(ContainSubstring("-tls-min-version=VersionTLS12"))
			}, util.APITimeout, util.RetryInterval).Should(Succeed())
		})

		It("should override TLS environment variables when orchestrator returns non-nil config", func() {
			DeferCleanup(os.Setenv, "TLS_CIPHER_SUITES", os.Getenv("TLS_CIPHER_SUITES"))
			DeferCleanup(os.Setenv, "TLS_MIN_VERSION", os.Getenv("TLS_MIN_VERSION"))
			os.Setenv("TLS_CIPHER_SUITES", "TLS_AES_128_GCM_SHA256")
			os.Setenv("TLS_MIN_VERSION", "VersionTLS12")

			mockedTLSConfig = &consts.TLSConfig{
				CipherSuites:  "TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384",
				MinTLSVersion: "VersionTLS13",
			}

			err := util.TriggerSriovOperatorConfigReconcile(k8sClient, testNamespace)
			Expect(err).NotTo(HaveOccurred())

			Eventually(func(g Gomega) {
				operatorWebhookDS := &appsv1.DaemonSet{}
				err := k8sClient.Get(ctx, types.NamespacedName{Name: "operator-webhook", Namespace: testNamespace}, operatorWebhookDS)
				g.Expect(err).NotTo(HaveOccurred())
				operatorArgs := strings.Join(operatorWebhookDS.Spec.Template.Spec.Containers[0].Args, " ")
				g.Expect(operatorArgs).To(ContainSubstring("--tls-cipher-suites=TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384"))
				g.Expect(operatorArgs).To(ContainSubstring("--tls-min-version=VersionTLS13"))
				g.Expect(operatorArgs).NotTo(ContainSubstring("TLS_AES_128_GCM_SHA256"))
				g.Expect(operatorArgs).NotTo(ContainSubstring("VersionTLS12"))

				injectorDS := &appsv1.DaemonSet{}
				err = k8sClient.Get(ctx, types.NamespacedName{Name: "network-resources-injector", Namespace: testNamespace}, injectorDS)
				g.Expect(err).NotTo(HaveOccurred())
				injectorArgs := strings.Join(injectorDS.Spec.Template.Spec.Containers[0].Args, " ")
				g.Expect(injectorArgs).To(ContainSubstring("-tls-cipher-suites=TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384"))
				g.Expect(injectorArgs).To(ContainSubstring("-tls-min-version=VersionTLS13"))
				g.Expect(injectorArgs).NotTo(ContainSubstring("TLS_AES_128_GCM_SHA256"))
				g.Expect(injectorArgs).NotTo(ContainSubstring("VersionTLS12"))
			}, util.APITimeout, util.RetryInterval).Should(Succeed())
		})

		It("should fail reconciliation when orchestrator returns error so it requeues", func() {
			DeferCleanup(os.Setenv, "TLS_CIPHER_SUITES", os.Getenv("TLS_CIPHER_SUITES"))
			DeferCleanup(os.Setenv, "TLS_MIN_VERSION", os.Getenv("TLS_MIN_VERSION"))
			os.Setenv("TLS_CIPHER_SUITES", "TLS_AES_256_GCM_SHA384")
			os.Setenv("TLS_MIN_VERSION", "VersionTLS13")

			mockedTLSError = errors.NewServiceUnavailable("unable to read TLS profile")

			err := util.TriggerSriovOperatorConfigReconcile(k8sClient, testNamespace)
			Expect(err).NotTo(HaveOccurred())

			Consistently(func(g Gomega) {
				config := &sriovnetworkv1.SriovOperatorConfig{}
				err := k8sClient.Get(ctx, types.NamespacedName{Namespace: testNamespace, Name: "default"}, config)
				g.Expect(err).NotTo(HaveOccurred())
			}, "2s", "200ms").Should(Succeed())
		})

		It("should fail reconciliation when TLS environment variables are invalid", func() {
			DeferCleanup(os.Setenv, "TLS_CIPHER_SUITES", os.Getenv("TLS_CIPHER_SUITES"))
			DeferCleanup(os.Setenv, "TLS_MIN_VERSION", os.Getenv("TLS_MIN_VERSION"))
			os.Setenv("TLS_CIPHER_SUITES", "COMPLETELY-INVALID-CIPHER")
			os.Setenv("TLS_MIN_VERSION", "VersionTLS12")

			mockedTLSConfig = nil

			err := util.TriggerSriovOperatorConfigReconcile(k8sClient, testNamespace)
			Expect(err).NotTo(HaveOccurred())

			Consistently(func(g Gomega) {
				config := &sriovnetworkv1.SriovOperatorConfig{}
				err := k8sClient.Get(ctx, types.NamespacedName{Namespace: testNamespace, Name: "default"}, config)
				g.Expect(err).NotTo(HaveOccurred())
			}, "2s", "200ms").Should(Succeed())
		})

		It("should convert OpenSSL env var cipher names to IANA format in rendered manifests", func() {
			DeferCleanup(os.Setenv, "TLS_CIPHER_SUITES", os.Getenv("TLS_CIPHER_SUITES"))
			DeferCleanup(os.Setenv, "TLS_MIN_VERSION", os.Getenv("TLS_MIN_VERSION"))
			os.Setenv("TLS_CIPHER_SUITES", "ECDHE-RSA-AES128-GCM-SHA256")
			os.Setenv("TLS_MIN_VERSION", "VersionTLS12")

			mockedTLSConfig = nil

			err := util.TriggerSriovOperatorConfigReconcile(k8sClient, testNamespace)
			Expect(err).NotTo(HaveOccurred())

			Eventually(func(g Gomega) {
				operatorWebhookDS := &appsv1.DaemonSet{}
				err := k8sClient.Get(ctx, types.NamespacedName{Name: "operator-webhook", Namespace: testNamespace}, operatorWebhookDS)
				g.Expect(err).NotTo(HaveOccurred())
				operatorArgs := strings.Join(operatorWebhookDS.Spec.Template.Spec.Containers[0].Args, " ")
				g.Expect(operatorArgs).To(ContainSubstring("--tls-cipher-suites=TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256"))
				g.Expect(operatorArgs).To(ContainSubstring("--tls-min-version=VersionTLS12"))

				injectorDS := &appsv1.DaemonSet{}
				err = k8sClient.Get(ctx, types.NamespacedName{Name: "network-resources-injector", Namespace: testNamespace}, injectorDS)
				g.Expect(err).NotTo(HaveOccurred())
				injectorArgs := strings.Join(injectorDS.Spec.Template.Spec.Containers[0].Args, " ")
				g.Expect(injectorArgs).To(ContainSubstring("-tls-cipher-suites=TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256"))
				g.Expect(injectorArgs).To(ContainSubstring("-tls-min-version=VersionTLS12"))
			}, util.APITimeout, util.RetryInterval).Should(Succeed())
		})

		It("should set Ready condition when reconcile succeeds", func() {
			Eventually(func(g Gomega) {
				config := &sriovnetworkv1.SriovOperatorConfig{}
				err := k8sClient.Get(ctx, types.NamespacedName{Namespace: testNamespace, Name: consts.DefaultConfigName}, config)
				g.Expect(err).NotTo(HaveOccurred())

				readyCondition := findCondition(config.Status.Conditions, sriovnetworkv1.ConditionReady)
				g.Expect(readyCondition).NotTo(BeNil())
				g.Expect(readyCondition.Status).To(Equal(metav1.ConditionTrue))
				g.Expect(readyCondition.Reason).To(Equal(sriovnetworkv1.ReasonOperatorConfigReady))
				g.Expect(readyCondition.ObservedGeneration).To(Equal(config.Generation))
			}, util.APITimeout, util.RetryInterval).Should(Succeed())
		})

		It("should update Ready observedGeneration after spec changes", func() {
			config := &sriovnetworkv1.SriovOperatorConfig{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: testNamespace, Name: consts.DefaultConfigName}, config)).NotTo(HaveOccurred())

			config.Spec.DisableDrain = !config.Spec.DisableDrain
			Expect(k8sClient.Update(ctx, config)).NotTo(HaveOccurred())

			Eventually(func(g Gomega) {
				updatedConfig := &sriovnetworkv1.SriovOperatorConfig{}
				err := k8sClient.Get(ctx, types.NamespacedName{Namespace: testNamespace, Name: consts.DefaultConfigName}, updatedConfig)
				g.Expect(err).NotTo(HaveOccurred())

				readyCondition := findCondition(updatedConfig.Status.Conditions, sriovnetworkv1.ConditionReady)
				g.Expect(readyCondition).NotTo(BeNil())
				g.Expect(readyCondition.Status).To(Equal(metav1.ConditionTrue))
				g.Expect(readyCondition.ObservedGeneration).To(Equal(updatedConfig.Generation))
			}, util.APITimeout, util.RetryInterval).Should(Succeed())
		})

		It("should map unsupported configuration errors to Ready=False", func() {
			config := makeDefaultSriovOpConfig()
			config.Generation = 7
			patcher := &fakeStatusPatcher{}

			reconciler := &SriovOperatorConfigReconciler{
				StatusPatcher: patcher,
			}

			err := reconciler.applyReadyCondition(ctx, config, errSystemdModeOnHypershift)
			Expect(err).NotTo(HaveOccurred())
			Expect(patcher.conditions).To(HaveLen(1))

			readyCondition := patcher.conditions[0]
			Expect(readyCondition.Type).To(Equal(sriovnetworkv1.ConditionReady))
			Expect(readyCondition.Status).To(Equal(metav1.ConditionFalse))
			Expect(readyCondition.Reason).To(Equal(sriovnetworkv1.ReasonUnsupportedConfiguration))
			Expect(readyCondition.ObservedGeneration).To(Equal(config.Generation))
		})

		It("should set Ready=False for HyperShift systemd configuration when reconciled directly", func() {
			config := makeDefaultSriovOpConfig()
			config.SetName("hypershift-systemd-config")
			config.Spec.ConfigurationMode = sriovnetworkv1.SystemdConfigurationMode
			Expect(k8sClient.Create(ctx, config)).To(Succeed())
			DeferCleanup(cleanupSriovOperatorConfig, config.Namespace, config.Name)

			mockCtrl := gomock.NewController(GinkgoT())
			orchestrator := orchestratorMock.NewMockInterface(mockCtrl)
			orchestrator.EXPECT().ClusterType().Return(consts.ClusterTypeOpenshift).AnyTimes()
			orchestrator.EXPECT().Flavor().Return(consts.ClusterFlavorHypershift).AnyTimes()
			orchestrator.EXPECT().GetTLSConfig(gomock.Any()).Return(nil, nil).AnyTimes()

			reconciler := newDirectSriovOperatorConfigReconciler(orchestrator)
			reconciler.renderManifestFn = func(string, *render.RenderData) ([]*unstructured.Unstructured, error) {
				return nil, nil
			}
			_, err := reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{
				Namespace: config.Namespace,
				Name:      config.Name,
			}})
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(Equal(errSystemdModeOnHypershift.Error()))

			Eventually(func(g Gomega) {
				updatedConfig := &sriovnetworkv1.SriovOperatorConfig{}
				err := k8sClient.Get(ctx, types.NamespacedName{Namespace: config.Namespace, Name: config.Name}, updatedConfig)
				g.Expect(err).NotTo(HaveOccurred())

				readyCondition := findCondition(updatedConfig.Status.Conditions, sriovnetworkv1.ConditionReady)
				g.Expect(readyCondition).NotTo(BeNil())
				g.Expect(readyCondition.Status).To(Equal(metav1.ConditionFalse))
				g.Expect(readyCondition.Reason).To(Equal(sriovnetworkv1.ReasonUnsupportedConfiguration))
				g.Expect(readyCondition.Message).To(Equal(errSystemdModeOnHypershift.Error()))
			}, util.APITimeout, util.RetryInterval).Should(Succeed())
		})

		It("should set Ready=False when apply fails during direct reconcile", func() {
			config := makeDefaultSriovOpConfig()
			config.SetName("apply-failure-config")
			Expect(k8sClient.Create(ctx, config)).To(Succeed())
			DeferCleanup(cleanupSriovOperatorConfig, config.Namespace, config.Name)

			mockCtrl := gomock.NewController(GinkgoT())
			orchestrator := orchestratorMock.NewMockInterface(mockCtrl)
			orchestrator.EXPECT().ClusterType().Return(consts.ClusterTypeKubernetes).AnyTimes()
			orchestrator.EXPECT().GetTLSConfig(gomock.Any()).Return(nil, nil).AnyTimes()

			reconciler := newDirectSriovOperatorConfigReconciler(orchestrator)
			reconciler.renderManifestFn = func(string, *render.RenderData) ([]*unstructured.Unstructured, error) {
				return []*unstructured.Unstructured{{
					Object: map[string]interface{}{
						"apiVersion": "v1",
						"kind":       "ConfigMap",
						"metadata": map[string]interface{}{
							"name":      "forced-apply-failure",
							"namespace": testNamespace,
						},
					},
				}}, nil
			}
			reconciler.applyManifestFn = func(context.Context, client.Client, *unstructured.Unstructured) error {
				return fmt.Errorf("forced apply failure")
			}
			_, err := reconciler.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{
				Namespace: config.Namespace,
				Name:      config.Name,
			}})
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("forced apply failure"))

			Eventually(func(g Gomega) {
				updatedConfig := &sriovnetworkv1.SriovOperatorConfig{}
				err := k8sClient.Get(ctx, types.NamespacedName{Namespace: config.Namespace, Name: config.Name}, updatedConfig)
				g.Expect(err).NotTo(HaveOccurred())

				readyCondition := findCondition(updatedConfig.Status.Conditions, sriovnetworkv1.ConditionReady)
				g.Expect(readyCondition).NotTo(BeNil())
				g.Expect(readyCondition.Status).To(Equal(metav1.ConditionFalse))
				g.Expect(readyCondition.Reason).To(Equal(sriovnetworkv1.ReasonOperatorConfigSyncFailed))
				g.Expect(readyCondition.Message).To(ContainSubstring("forced apply failure"))
			}, util.APITimeout, util.RetryInterval).Should(Succeed())
		})

		It("should return a wrapped error when delete fails via the reconciler hook", func() {
			reconciler := &SriovOperatorConfigReconciler{
				Client: k8sClient,
				deleteManifestFn: func(context.Context, client.Client, *unstructured.Unstructured) error {
					return fmt.Errorf("forced delete failure")
				},
			}

			obj := &unstructured.Unstructured{
				Object: map[string]interface{}{
					"apiVersion": "v1",
					"kind":       "ConfigMap",
					"metadata": map[string]interface{}{
						"name":      "forced-delete-failure",
						"namespace": testNamespace,
					},
				},
			}

			err := reconciler.deleteK8sResource(ctx, obj)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("forced delete failure"))
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

			networkPolicy := &networkv1.NetworkPolicy{}
			err = util.WaitForNamespacedObjectDeleted(networkPolicy, k8sClient, testNamespace, "network-resources-injector-allow-traffic-api-server", util.RetryInterval, util.APITimeout)
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

			networkPolicy = &networkv1.NetworkPolicy{}
			err = util.WaitForNamespacedObject(networkPolicy, k8sClient, testNamespace, "network-resources-injector-allow-traffic-api-server", util.RetryInterval, util.APITimeout)
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

			networkPolicy := &networkv1.NetworkPolicy{}
			err = util.WaitForNamespacedObjectDeleted(networkPolicy, k8sClient, testNamespace, "operator-webhook-allow-traffic-api-server", util.RetryInterval, util.APITimeout)
			Expect(err).NotTo(HaveOccurred())

			mutateCfg := &admv1.MutatingWebhookConfiguration{}
			err = util.WaitForNamespacedObjectDeleted(mutateCfg, k8sClient, testNamespace, "sriov-operator-webhook-config", util.RetryInterval, util.APITimeout)
			Expect(err).NotTo(HaveOccurred())

			validateCfg := &admv1.ValidatingWebhookConfiguration{}
			err = util.WaitForNamespacedObjectDeleted(validateCfg, k8sClient, testNamespace, "sriov-operator-webhook-config", util.RetryInterval, util.APITimeout)
			Expect(err).NotTo(HaveOccurred())

			By("set enable to enableOperatorWebhook")
			Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: testNamespace, Name: "default"}, config)).NotTo(HaveOccurred())

			config.Spec.EnableOperatorWebhook = true
			err = k8sClient.Update(ctx, config)
			Expect(err).NotTo(HaveOccurred())

			daemonSet = &appsv1.DaemonSet{}
			err = util.WaitForNamespacedObject(daemonSet, k8sClient, testNamespace, "operator-webhook", util.RetryInterval, util.APITimeout)
			Expect(err).NotTo(HaveOccurred())

			networkPolicy = &networkv1.NetworkPolicy{}
			err = util.WaitForNamespacedObject(networkPolicy, k8sClient, testNamespace, "operator-webhook-allow-traffic-api-server", util.RetryInterval, util.APITimeout)
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

		It("should add/delete finalizer 'operatorconfig' when SriovOperatorConfig/default is added/deleted", func() {
			DeferCleanup(k8sClient.Create, context.Background(), makeDefaultSriovOpConfig())

			// verify that finalizer has been added upon object creation
			config := &sriovnetworkv1.SriovOperatorConfig{}
			Eventually(func() []string {
				// wait for SriovOperatorConfig flags to get updated
				err := k8sClient.Get(context.Background(), types.NamespacedName{Name: "default", Namespace: testNamespace}, config)
				if err != nil {
					return nil
				}
				return config.Finalizers
			}, util.APITimeout, util.RetryInterval).Should(Equal([]string{sriovnetworkv1.OPERATORCONFIGFINALIZERNAME}))

			err := k8sClient.Delete(context.Background(), &sriovnetworkv1.SriovOperatorConfig{
				ObjectMeta: metav1.ObjectMeta{Namespace: testNamespace, Name: "default"},
			})
			Expect(err).NotTo(HaveOccurred())

			// verify that finalizer has been removed
			var empty []string
			config = &sriovnetworkv1.SriovOperatorConfig{}
			Eventually(func() []string {
				// wait for SriovOperatorConfig flags to get updated
				err := k8sClient.Get(context.Background(), types.NamespacedName{Name: "default", Namespace: testNamespace}, config)
				if err != nil {
					return nil
				}
				return config.Finalizers
			}, util.APITimeout, util.RetryInterval).Should(Equal(empty))
		})

		It("should not remove fields with default values when SriovOperatorConfig is created", func() {
			err := k8sClient.Delete(context.Background(), &sriovnetworkv1.SriovOperatorConfig{
				ObjectMeta: metav1.ObjectMeta{Namespace: testNamespace, Name: consts.DefaultConfigName},
			})
			Expect(err).NotTo(HaveOccurred())

			config := &unstructured.Unstructured{}
			config.SetGroupVersionKind(sriovnetworkv1.GroupVersion.WithKind("SriovOperatorConfig"))
			config.SetName(consts.DefaultConfigName)
			config.SetNamespace(testNamespace)
			config.Object["spec"] = map[string]interface{}{
				"enableInjector":        false,
				"enableOperatorWebhook": false,
				"logLevel":              0,
				"disableDrain":          false,
			}

			Eventually(func() error {
				return k8sClient.Create(context.Background(), config)
			}).Should(Succeed())

			By("Wait for the operator to reconcile the object")
			Eventually(func(g Gomega) {
				err := k8sClient.Get(context.Background(), types.NamespacedName{Namespace: testNamespace, Name: consts.DefaultConfigName}, config)
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(config.GetFinalizers()).To(ContainElement(sriovnetworkv1.OPERATORCONFIGFINALIZERNAME))
			}, util.APITimeout, util.RetryInterval).Should(Succeed())

			By("Verify default values have not been omitted")
			obj := &unstructured.Unstructured{}
			obj.SetGroupVersionKind(sriovnetworkv1.GroupVersion.WithKind("SriovOperatorConfig"))
			err = k8sClient.Get(context.Background(), types.NamespacedName{Namespace: testNamespace, Name: consts.DefaultConfigName}, obj)
			Expect(err).NotTo(HaveOccurred())

			Expect(obj.Object["spec"]).To(HaveKeyWithValue("enableInjector", false))
			Expect(obj.Object["spec"]).To(HaveKeyWithValue("enableOperatorWebhook", false))
			Expect(obj.Object["spec"]).To(HaveKeyWithValue("logLevel", int64(0)))
			Expect(obj.Object["spec"]).To(HaveKeyWithValue("disableDrain", false))
		})

		It("should be able to update the node selector of sriov-network-config-daemon", func() {
			By("specify the configDaemonNodeSelector")
			nodeSelector := map[string]string{"node-role.kubernetes.io/worker": ""}
			restore := updateConfigDaemonNodeSelector(nodeSelector)
			DeferCleanup(restore)

			daemonSet := &appsv1.DaemonSet{}
			Eventually(func() map[string]string {
				err := k8sClient.Get(ctx, types.NamespacedName{Name: "sriov-network-config-daemon", Namespace: testNamespace}, daemonSet)
				if err != nil {
					return nil
				}
				return daemonSet.Spec.Template.Spec.NodeSelector
			}, util.APITimeout, util.RetryInterval).Should(Equal(nodeSelector))
		})

		It("should be able to update the node selector of sriov-network-device-plugin", func() {
			By("specify the configDaemonNodeSelector")
			daemonSet := &appsv1.DaemonSet{}
			Eventually(func(g Gomega) {
				err := k8sClient.Get(ctx, types.NamespacedName{Name: "sriov-device-plugin", Namespace: testNamespace}, daemonSet)
				g.Expect(err).ToNot(HaveOccurred())
				_, exist := daemonSet.Spec.Template.Spec.NodeSelector["node-role.kubernetes.io/worker"]
				g.Expect(exist).To(BeFalse())
				_, exist = daemonSet.Spec.Template.Spec.NodeSelector[consts.SriovDevicePluginLabel]
				g.Expect(exist).To(BeTrue())
			}, util.APITimeout, util.RetryInterval).Should(Succeed())

			nodeSelector := map[string]string{"node-role.kubernetes.io/worker": ""}
			restore := updateConfigDaemonNodeSelector(nodeSelector)
			DeferCleanup(restore)

			Eventually(func(g Gomega) {
				err := k8sClient.Get(ctx, types.NamespacedName{Name: "sriov-device-plugin", Namespace: testNamespace}, daemonSet)
				g.Expect(err).ToNot(HaveOccurred())
				_, exist := daemonSet.Spec.Template.Spec.NodeSelector["node-role.kubernetes.io/worker"]
				g.Expect(exist).To(BeTrue())
				_, exist = daemonSet.Spec.Template.Spec.NodeSelector[consts.SriovDevicePluginLabel]
				g.Expect(exist).To(BeTrue())
			}, util.APITimeout, util.RetryInterval).Should(Succeed())
		})

		It("should be able to do multiple updates to the node selector of sriov-network-config-daemon", func() {
			By("changing the configDaemonNodeSelector")
			firstNodeSelector := map[string]string{"labelA": "", "labelB": "", "labelC": ""}
			restore := updateConfigDaemonNodeSelector(firstNodeSelector)
			DeferCleanup(restore)

			secondNodeSelector := map[string]string{"labelA": "", "labelB": ""}
			updateConfigDaemonNodeSelector(secondNodeSelector)

			daemonSet := &appsv1.DaemonSet{}
			Eventually(func() map[string]string {
				err := k8sClient.Get(ctx, types.NamespacedName{Name: "sriov-network-config-daemon", Namespace: testNamespace}, daemonSet)
				if err != nil {
					return nil
				}
				return daemonSet.Spec.Template.Spec.NodeSelector
			}, util.APITimeout, util.RetryInterval).Should(Equal(secondNodeSelector))
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
		It("should render configDaemonEnvVars in sriov-network-config-daemon if provided in spec", func() {
			config := &sriovnetworkv1.SriovOperatorConfig{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Namespace: testNamespace, Name: "default"}, config)).NotTo(HaveOccurred())

			config.Spec.ConfigDaemonEnvVars = map[string]string{"TEST_ENV_VAR": "test_value"}
			err := k8sClient.Update(ctx, config)
			Expect(err).NotTo(HaveOccurred())

			Eventually(func(g Gomega) {
				daemonSet := &appsv1.DaemonSet{}
				err := k8sClient.Get(ctx, types.NamespacedName{Name: "sriov-network-config-daemon", Namespace: testNamespace}, daemonSet)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(daemonSet.Spec.Template.Spec.Containers[0].Env).To(ContainElement(
					corev1.EnvVar{Name: "TEST_ENV_VAR", Value: "test_value"}))
			}, util.APITimeout*10, util.RetryInterval).Should(Succeed())
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

				It("should render tls profile flags for metrics exporter kube-rbac-proxy", func() {
					mockedTLSConfig = &consts.TLSConfig{
						CipherSuites:     "TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256",
						MinTLSVersion:    "VersionTLS13",
						CurvePreferences: "X25519,secp256r1",
					}

					err := util.TriggerSriovOperatorConfigReconcile(k8sClient, testNamespace)
					Expect(err).NotTo(HaveOccurred())

					Eventually(func(g Gomega) {
						daemonSet := &appsv1.DaemonSet{}
						err := k8sClient.Get(ctx, types.NamespacedName{Name: "sriov-network-metrics-exporter", Namespace: testNamespace}, daemonSet)
						g.Expect(err).NotTo(HaveOccurred())

						proxyArgs := ""
						for _, container := range daemonSet.Spec.Template.Spec.Containers {
							if container.Name == "kube-rbac-proxy" {
								proxyArgs = strings.Join(container.Args, " ")
								break
							}
						}

						g.Expect(proxyArgs).NotTo(BeEmpty())
						g.Expect(proxyArgs).To(ContainSubstring("--tls-cipher-suites=TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256"))
						g.Expect(proxyArgs).To(ContainSubstring("--tls-min-version=VersionTLS13"))
						g.Expect(proxyArgs).To(ContainSubstring("--tls-curve-preferences=29,23"))
					}, util.APITimeout, util.RetryInterval).Should(Succeed())
				})

				It("should deploy the sriov-network-metrics-exporter DaemonSet", func() {
					assertDaemonSetFields := func() {
						daemonSet := &appsv1.DaemonSet{}
						err := util.WaitForNamespacedObject(daemonSet, k8sClient, testNamespace, "sriov-network-metrics-exporter", util.RetryInterval, util.APITimeout)
						Expect(err).NotTo(HaveOccurred())
						Expect(daemonSet.Spec.UpdateStrategy.Type).To(Equal(appsv1.RollingUpdateDaemonSetStrategyType), "metrics exporter DaemonSet should use RollingUpdate")
						rollingUpdate := daemonSet.Spec.UpdateStrategy.RollingUpdate
						Expect(rollingUpdate).NotTo(BeNil(), "metrics exporter DaemonSet should define rolling update settings")
						if rollingUpdate != nil {
							Expect(rollingUpdate.MaxUnavailable.String()).To(Equal("33%"), "metrics exporter DaemonSet should set maxUnavailable to 33%%")
						}
						Expect(daemonSet.Spec.Template.Spec.Tolerations).To(Equal([]corev1.Toleration{{Operator: corev1.TolerationOpExists}}), "metrics exporter DaemonSet should tolerate all taints")
						Expect(daemonSet.Spec.Template.Spec.PriorityClassName).To(Equal("system-node-critical"), "metrics exporter DaemonSet should use the system-node-critical priority class")
					}

					assertDaemonSetFields()
					err := util.TriggerSriovOperatorConfigReconcile(k8sClient, testNamespace)
					Expect(err).NotTo(HaveOccurred())
					assertDaemonSetFields()

					err = util.WaitForNamespacedObject(&corev1.Service{}, k8sClient, testNamespace, "sriov-network-metrics-exporter-service", util.RetryInterval, util.APITimeout)
					Expect(err).ToNot(HaveOccurred())
				})

				It("should deploy the sriov-network-metrics-exporter using the Spec.ConfigDaemonNodeSelector field", func() {
					nodeSelector := map[string]string{
						"node-role.kubernetes.io/worker": "",
						"bool-key":                       "true",
					}

					restore := updateConfigDaemonNodeSelector(nodeSelector)
					DeferCleanup(restore)

					Eventually(func(g Gomega) {
						metricsDaemonset := appsv1.DaemonSet{}
						err := util.WaitForNamespacedObject(&metricsDaemonset, k8sClient, testNamespace, "sriov-network-metrics-exporter", util.RetryInterval, util.APITimeout)
						g.Expect(err).NotTo(HaveOccurred())
						g.Expect(metricsDaemonset.Spec.Template.Spec.NodeSelector).To(Equal(nodeSelector))
					}, time.Minute, time.Second).Should(Succeed())
				})

				It("should deploy extra configuration when the Prometheus operator is installed", func() {
					DeferCleanup(os.Setenv, "METRICS_EXPORTER_PROMETHEUS_OPERATOR_ENABLED", os.Getenv("METRICS_EXPORTER_PROMETHEUS_OPERATOR_ENABLED"))
					os.Setenv("METRICS_EXPORTER_PROMETHEUS_OPERATOR_ENABLED", "true")
					DeferCleanup(os.Setenv, "METRICS_EXPORTER_PROMETHEUS_DEPLOY_RULES", os.Getenv("METRICS_EXPORTER_PROMETHEUS_DEPLOY_RULES"))
					os.Setenv("METRICS_EXPORTER_PROMETHEUS_DEPLOY_RULES", "true")

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

					assertResourceExists(
						schema.GroupVersionKind{
							Group:   "monitoring.coreos.com",
							Kind:    "PrometheusRule",
							Version: "v1",
						},
						client.ObjectKey{Namespace: testNamespace, Name: "sriov-vf-rules"})
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

			DeferCleanup(func(old consts.ClusterType) { vars.ClusterType = old }, vars.ClusterType)
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

type fakeStatusPatcher struct {
	conditions []metav1.Condition
}

// ApplyCondition records the conditions passed to the fake status patcher.
func (f *fakeStatusPatcher) ApplyCondition(_ context.Context, _ client.Object, conditions ...metav1.Condition) error {
	f.conditions = append([]metav1.Condition(nil), conditions...)
	return nil
}

// ApplyStatus is a no-op for the fake status patcher used in operator config tests.
func (f *fakeStatusPatcher) ApplyStatus(_ context.Context, _ client.Object, _ map[string]interface{}, conditions []metav1.Condition) error {
	f.conditions = append([]metav1.Condition(nil), conditions...)
	return nil
}

// newDirectSriovOperatorConfigReconciler returns a reconciler wired for direct
// Reconcile invocations against the envtest API.
func newDirectSriovOperatorConfigReconciler(orchestrator *orchestratorMock.MockInterface) *SriovOperatorConfigReconciler {
	return &SriovOperatorConfigReconciler{
		Client:            k8sClient,
		Scheme:            vars.Scheme,
		Orchestrator:      orchestrator,
		StatusPatcher:     status.NewPatcher(k8sClient, nil, vars.Scheme, "direct-test"),
		UncachedAPIReader: k8sClient,
	}
}

// cleanupSriovOperatorConfig removes finalizers from a test SriovOperatorConfig
// before deleting it so direct-reconcile test fixtures do not leak.
func cleanupSriovOperatorConfig(namespace, name string) {
	config := &sriovnetworkv1.SriovOperatorConfig{}
	err := k8sClient.Get(context.Background(), types.NamespacedName{Namespace: namespace, Name: name}, config)
	if err != nil {
		if errors.IsNotFound(err) {
			return
		}
		Expect(err).NotTo(HaveOccurred())
	}

	if len(config.Finalizers) > 0 {
		config.Finalizers = nil
		err = k8sClient.Update(context.Background(), config)
		Expect(err).NotTo(HaveOccurred())
	}

	err = k8sClient.Delete(context.Background(), config)
	if err != nil && !errors.IsNotFound(err) {
		Expect(err).NotTo(HaveOccurred())
	}
}

func updateConfigDaemonNodeSelector(newValue map[string]string) func() {
	config := &sriovnetworkv1.SriovOperatorConfig{}
	err := k8sClient.Get(context.Background(), types.NamespacedName{Namespace: testNamespace, Name: "default"}, config)
	Expect(err).NotTo(HaveOccurred())

	previousValue := config.Spec.ConfigDaemonNodeSelector
	ret := func() {
		updateConfigDaemonNodeSelector(previousValue)
	}

	config.Spec.ConfigDaemonNodeSelector = newValue
	err = k8sClient.Update(context.Background(), config)
	Expect(err).NotTo(HaveOccurred())

	return ret
}
