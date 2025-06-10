package main

import (
	"context"
	"sync"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/spf13/cobra"
	"go.uber.org/mock/gomock"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	sriovnetworkv1 "github.com/k8snetworkplumbingwg/sriov-network-operator/api/v1"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/controllers"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/consts"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/featuregate"
	orchestratorMock "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/orchestrator/mock"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/vars"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/test/util"
)

type configController struct {
	k8sManager manager.Manager
	ctx        context.Context
	cancel     context.CancelFunc
	wg         *sync.WaitGroup
}

var (
	controller               *configController
	testNamespace            = "sriov-network-operator"
	defaultSriovOperatorSpec = sriovnetworkv1.SriovOperatorConfigSpec{
		EnableInjector:        true,
		EnableOperatorWebhook: true,
		LogLevel:              2,
		FeatureGates:          nil,
	}
)

var _ = Describe("cleanup", Ordered, func() {
	BeforeAll(func() {
		By("Create SriovOperatorConfig controller k8s objs")
		config := getDefaultSriovOperatorConfig()
		Expect(k8sClient.Create(context.Background(), config)).Should(Succeed())

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

		controller = newConfigController()

	})

	It("test webhook cleanup flow", func() {
		controller.start()
		defer controller.stop()

		cmd := &cobra.Command{}
		namespace = testNamespace
		// verify that finalizer has been added, by controller, upon object creation
		config := &sriovnetworkv1.SriovOperatorConfig{}
		Eventually(func() []string {
			// wait for SriovOperatorConfig flags to get updated
			err := k8sClient.Get(context.Background(), types.NamespacedName{Name: "default", Namespace: testNamespace}, config)
			if err != nil {
				return nil
			}
			return config.Finalizers
		}, util.APITimeout, util.RetryInterval).Should(Equal([]string{sriovnetworkv1.OPERATORCONFIGFINALIZERNAME}))

		Expect(runCleanupCmd(cmd, []string{})).Should(Succeed())
		config = &sriovnetworkv1.SriovOperatorConfig{}
		err := util.WaitForNamespacedObjectDeleted(config, k8sClient, testNamespace, "default", util.RetryInterval, util.APITimeout)
		Expect(err).NotTo(HaveOccurred())

	})

	It("test 'default' config cleanup timeout", func() {
		// in this test case sriov-operator controller has been scaled down.
		// we are testing returned ctx timeout error, for not being able to delete 'default' config object
		config := getDefaultSriovOperatorConfig()
		config.Finalizers = []string{sriovnetworkv1.OPERATORCONFIGFINALIZERNAME}
		Expect(k8sClient.Create(context.Background(), config)).Should(Succeed())

		cmd := &cobra.Command{}
		namespace = testNamespace
		// verify that finalizer has been added, by controller, upon object creation
		config = &sriovnetworkv1.SriovOperatorConfig{}
		Eventually(func() []string {
			// wait for SriovOperatorConfig flags to get updated
			err := k8sClient.Get(context.Background(), types.NamespacedName{Name: "default", Namespace: testNamespace}, config)
			if err != nil {
				return nil
			}
			return config.Finalizers
		}, util.APITimeout, util.RetryInterval).Should(Equal([]string{sriovnetworkv1.OPERATORCONFIGFINALIZERNAME}))

		watchTO = 1
		err := runCleanupCmd(cmd, []string{})
		Expect(err.Error()).To(ContainSubstring("context deadline exceeded"))
	})
})

func getDefaultSriovOperatorConfig() *sriovnetworkv1.SriovOperatorConfig {
	return &sriovnetworkv1.SriovOperatorConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "default",
			Namespace: testNamespace,
		},
		Spec: defaultSriovOperatorSpec,
	}
}

func newConfigController() *configController {
	// setup controller manager
	By("Setup controller manager")
	k8sManager, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme: scheme.Scheme,
	})
	Expect(err).ToNot(HaveOccurred())

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
	orchestrator.EXPECT().Flavor().DoAndReturn(func() consts.ClusterFlavor {
		if vars.ClusterType == consts.ClusterTypeOpenshift {
			return consts.DefaultConfigName
		}
		return consts.ClusterFlavorVanillaK8s
	}).AnyTimes()

	err = (&controllers.SriovOperatorConfigReconciler{
		Client:            k8sManager.GetClient(),
		Scheme:            k8sManager.GetScheme(),
		Orchestrator:      orchestrator,
		FeatureGate:       featuregate.New(),
		UncachedAPIReader: k8sManager.GetAPIReader(),
	}).SetupWithManager(k8sManager)
	Expect(err).ToNot(HaveOccurred())

	ctx, cancel := context.WithCancel(context.Background())
	wg := sync.WaitGroup{}
	controller = &configController{
		k8sManager: k8sManager,
		ctx:        ctx,
		cancel:     cancel,
		wg:         &wg,
	}

	return controller
}

func (c *configController) start() {
	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		defer GinkgoRecover()
		By("Start controller manager")
		err := c.k8sManager.Start(c.ctx)
		Expect(err).ToNot(HaveOccurred())
	}()
}

func (c *configController) stop() {
	c.cancel()
	c.wg.Wait()
}
