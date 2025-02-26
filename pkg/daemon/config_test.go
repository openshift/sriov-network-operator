package daemon_test

import (
	"context"
	"sync"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	sriovnetworkv1 "github.com/k8snetworkplumbingwg/sriov-network-operator/api/v1"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/consts"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/daemon"
	snolog "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/log"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/vars"
)

var _ = Describe("Daemon OperatorConfig Controller", Ordered, func() {
	var cancel context.CancelFunc
	var ctx context.Context

	BeforeAll(func() {
		By("Setup controller manager")
		k8sManager, err := ctrl.NewManager(cfg, ctrl.Options{
			Scheme: scheme.Scheme,
		})
		Expect(err).ToNot(HaveOccurred())

		configController := daemon.NewOperatorConfigNodeReconcile(k8sClient)
		err = configController.SetupWithManager(k8sManager)
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

		err = k8sClient.Create(ctx, &corev1.ServiceAccount{ObjectMeta: metav1.ObjectMeta{Name: "default", Namespace: "default"}})
		Expect(err).ToNot(HaveOccurred())
	})

	AfterEach(func() {
		Expect(k8sClient.DeleteAllOf(context.Background(), &sriovnetworkv1.SriovOperatorConfig{}, client.InNamespace(testNamespace))).ToNot(HaveOccurred())
	})

	Context("LogLevel", func() {
		It("should configure the log level base on sriovOperatorConfig", func() {
			soc := &sriovnetworkv1.SriovOperatorConfig{ObjectMeta: metav1.ObjectMeta{
				Name:      consts.DefaultConfigName,
				Namespace: testNamespace,
			},
				Spec: sriovnetworkv1.SriovOperatorConfigSpec{
					LogLevel: 1,
				},
			}

			err := k8sClient.Create(ctx, soc)
			Expect(err).ToNot(HaveOccurred())
			validateExpectedLogLevel(1)

		})

		It("should update the log level in runtime", func() {
			soc := &sriovnetworkv1.SriovOperatorConfig{ObjectMeta: metav1.ObjectMeta{
				Name:      consts.DefaultConfigName,
				Namespace: testNamespace,
			},
				Spec: sriovnetworkv1.SriovOperatorConfigSpec{
					LogLevel: 1,
				},
			}

			err := k8sClient.Create(ctx, soc)
			Expect(err).ToNot(HaveOccurred())
			validateExpectedLogLevel(1)

			soc.Spec.LogLevel = 2
			err = k8sClient.Update(ctx, soc)
			Expect(err).ToNot(HaveOccurred())
			validateExpectedLogLevel(2)
		})
	})

	Context("Disable Drain", func() {
		It("should update the skip drain flag", func() {
			soc := &sriovnetworkv1.SriovOperatorConfig{ObjectMeta: metav1.ObjectMeta{
				Name:      consts.DefaultConfigName,
				Namespace: testNamespace,
			},
				Spec: sriovnetworkv1.SriovOperatorConfigSpec{
					DisableDrain: true,
				},
			}

			err := k8sClient.Create(ctx, soc)
			Expect(err).ToNot(HaveOccurred())
			validateExpectedDrain(true)

			soc.Spec.DisableDrain = false
			err = k8sClient.Update(ctx, soc)
			Expect(err).ToNot(HaveOccurred())
			validateExpectedDrain(false)
		})
	})

	Context("Feature gates", func() {
		It("should update the feature gates struct", func() {
			soc := &sriovnetworkv1.SriovOperatorConfig{ObjectMeta: metav1.ObjectMeta{
				Name:      consts.DefaultConfigName,
				Namespace: testNamespace,
			},
				Spec: sriovnetworkv1.SriovOperatorConfigSpec{
					FeatureGates: map[string]bool{
						"test": true,
						"bla":  true,
					},
				},
			}

			err := k8sClient.Create(ctx, soc)
			Expect(err).ToNot(HaveOccurred())
			EventuallyWithOffset(1, func(g Gomega) {
				g.Expect(vars.FeatureGate.IsEnabled("test")).To(BeTrue())
			}, "15s", "3s").Should(Succeed())
			EventuallyWithOffset(1, func(g Gomega) {
				g.Expect(vars.FeatureGate.IsEnabled("bla")).To(BeTrue())
			}, "15s", "3s").Should(Succeed())

			soc.Spec.FeatureGates["test"] = false
			err = k8sClient.Update(ctx, soc)
			Expect(err).ToNot(HaveOccurred())
			EventuallyWithOffset(1, func(g Gomega) {
				g.Expect(vars.FeatureGate.IsEnabled("test")).To(BeFalse())
			}, "15s", "3s").Should(Succeed())
			EventuallyWithOffset(1, func(g Gomega) {
				g.Expect(vars.FeatureGate.IsEnabled("bla")).To(BeTrue())
			}, "15s", "3s").Should(Succeed())
		})
	})
})

func validateExpectedLogLevel(level int) {
	EventuallyWithOffset(1, func(g Gomega) {
		g.Expect(snolog.GetLogLevel()).To(Equal(level))
	}, "15s", "3s").Should(Succeed())
}

func validateExpectedDrain(disableDrain bool) {
	EventuallyWithOffset(1, func(g Gomega) {
		g.Expect(vars.DisableDrain).To(Equal(disableDrain))
	}, "15s", "3s").Should(Succeed())
}
