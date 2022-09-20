package controllers

import (
	goctx "context"

	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	sriovnetworkv1 "github.com/k8snetworkplumbingwg/sriov-network-operator/api/v1"
	constants "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/consts"
	mcfgv1 "github.com/openshift/machine-config-operator/pkg/apis/machineconfiguration.openshift.io/v1"
)

var _ = Describe("Operator", func() {
	Context("When is up", func() {
		It("should be able to create machine config for MachineConfigPool specified in sriov pool config", func() {
			config := &sriovnetworkv1.SriovNetworkPoolConfig{}
			config.SetNamespace(testNamespace)
			config.SetName("ovs-hw-offload-config")
			mcpName := "worker"
			mc := &mcfgv1.MachineConfig{}
			mcName := "00-" + mcpName + "-" + constants.OVSHWOLMachineConfigNameSuffix
			err := k8sClient.Get(goctx.TODO(), types.NamespacedName{Name: mcName, Namespace: testNamespace}, mc)
			Expect(errors.IsNotFound(err)).Should(BeTrue())

			mcp := &mcfgv1.MachineConfigPool{}
			err = k8sClient.Get(goctx.TODO(), types.NamespacedName{Name: mcpName, Namespace: testNamespace}, mcp)
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
			err = k8sClient.Create(goctx.TODO(), mcp)
			Expect(err).NotTo(HaveOccurred())

			config.Spec.OvsHardwareOffloadConfig = sriovnetworkv1.OvsHardwareOffloadConfig{
				Name: mcpName,
			}
			err = k8sClient.Create(goctx.TODO(), config)
			Expect(err).NotTo(HaveOccurred())
			Eventually(func() error {
				mc := &mcfgv1.MachineConfig{}
				err := k8sClient.Get(goctx.TODO(), types.NamespacedName{Name: mcName, Namespace: testNamespace}, mc)
				if err != nil {
					return err
				}
				return nil
			}, timeout*3, interval).Should(Succeed())
		})
	})
})
