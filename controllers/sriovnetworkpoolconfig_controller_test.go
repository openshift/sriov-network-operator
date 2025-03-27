package controllers

import (
	"context"
	"sync"

	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"

	mcfgv1 "github.com/openshift/api/machineconfiguration/v1"

	sriovnetworkv1 "github.com/k8snetworkplumbingwg/sriov-network-operator/api/v1"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/consts"
	constants "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/consts"
	mock_platforms "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/platforms/mock"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/platforms/openshift"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/vars"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/test/util"
)

var _ = Describe("SriovNetworkPoolConfig controller", Ordered, func() {
	var cancel context.CancelFunc
	var ctx context.Context

	BeforeAll(func() {
		By("Create default SriovNetworkPoolConfig k8s objs")
		poolConfig := &sriovnetworkv1.SriovNetworkPoolConfig{}
		poolConfig.SetNamespace(testNamespace)
		poolConfig.SetName(constants.DefaultConfigName)
		poolConfig.Spec = sriovnetworkv1.SriovNetworkPoolConfigSpec{}
		Expect(k8sClient.Create(context.Background(), poolConfig)).Should(Succeed())
		DeferCleanup(func() {
			err := k8sClient.Delete(context.Background(), poolConfig)
			Expect(err).ToNot(HaveOccurred())
		})

		By("Setup controller manager")
		k8sManager, err := setupK8sManagerForTest()
		Expect(err).ToNot(HaveOccurred())

		t := GinkgoT()
		mockCtrl := gomock.NewController(t)
		platformHelper := mock_platforms.NewMockInterface(mockCtrl)
		platformHelper.EXPECT().GetFlavor().Return(openshift.OpenshiftFlavorDefault).AnyTimes()
		platformHelper.EXPECT().IsOpenshiftCluster().Return(false).AnyTimes()
		platformHelper.EXPECT().IsHypershift().Return(false).AnyTimes()

		err = (&SriovNetworkPoolConfigReconciler{
			Client:         k8sManager.GetClient(),
			Scheme:         k8sManager.GetScheme(),
			PlatformHelper: platformHelper,
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

	Context("When is up", func() {
		It("should be able to create machine config for MachineConfigPool specified in sriov pool config", func() {
			if vars.ClusterType != consts.ClusterTypeOpenshift {
				Skip("test should only be executed with openshift cluster type")
			}

			config := &sriovnetworkv1.SriovNetworkPoolConfig{}
			config.SetNamespace(testNamespace)
			config.SetName("ovs-hw-offload-config")
			mcpName := "worker-hwoffload"
			mc := &mcfgv1.MachineConfig{}
			mcName := "00-" + mcpName + "-" + constants.OVSHWOLMachineConfigNameSuffix
			err := k8sClient.Get(ctx, types.NamespacedName{Name: mcName, Namespace: testNamespace}, mc)
			Expect(errors.IsNotFound(err)).Should(BeTrue())

			mcp := &mcfgv1.MachineConfigPool{}
			err = k8sClient.Get(ctx, types.NamespacedName{Name: mcpName, Namespace: testNamespace}, mcp)
			Expect(errors.IsNotFound(err)).Should(BeTrue())

			mcp = &mcfgv1.MachineConfigPool{
				ObjectMeta: metav1.ObjectMeta{
					Name: mcpName,
				},
				Spec: mcfgv1.MachineConfigPoolSpec{
					NodeSelector: &metav1.LabelSelector{
						MatchLabels: map[string]string{
							"node-role.kubernetes.io/worker": "",
						},
					},
				},
			}
			err = k8sClient.Create(ctx, mcp)
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(func() {
				err = k8sClient.Delete(ctx, mcp)
				Expect(err).ToNot(HaveOccurred())
			})

			config.Spec.OvsHardwareOffloadConfig = sriovnetworkv1.OvsHardwareOffloadConfig{
				Name: mcpName,
			}
			err = k8sClient.Create(ctx, config)
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(func() {
				err = k8sClient.Delete(ctx, config)
				Expect(err).ToNot(HaveOccurred())
			})

			Eventually(func() error {
				mc := &mcfgv1.MachineConfig{}
				err := k8sClient.Get(ctx, types.NamespacedName{Name: mcName, Namespace: testNamespace}, mc)
				if err != nil {
					return err
				}
				return nil
			}, util.APITimeout, util.RetryInterval).Should(Succeed())
		})
	})
})
