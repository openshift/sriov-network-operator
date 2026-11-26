package webhook

import (
	"context"
	"fmt"
	"os"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	admissionv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	runtimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	. "github.com/k8snetworkplumbingwg/sriov-network-operator/api/v1"
	constants "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/consts"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/vars"
)

func TestMain(m *testing.M) {
	NicIDMap = []string{
		"8086 158a 154c", // I40e XXV710
		"8086 158b 154c", // I40e 25G SFP28
		"8086 1572 154c", // I40e 10G X710 SFP+
		"8086 0d58 154c", // I40e XXV710 N3000
		"8086 1581 154c", // I40e X710 Backplane
		"8086 15ff 154c", // I40e X710 Base T
		"8086 1583 154c", // I40e 40G XL710 QSFP+
		"8086 1592 1889", // Columbiaville E810-CQDA2/2CQDA2
		"8086 1593 1889", // Columbiaville E810-XXVDA4
		"8086 159b 1889", // Columbiaville E810-XXVDA2
		"15b3 1013 1014", // ConnectX-4
		"15b3 1015 1016", // ConnectX-4LX
		"15b3 1017 1018", // ConnectX-5, PCIe 3.0
		"15b3 1019 101a", // ConnectX-5 Ex
		"15b3 101b 101c", // ConnectX-6
		"15b3 101d 101e", // ConnectX-6 Dx
		"15b3 a2d6 101e", // MT42822 BlueField-2 integrated ConnectX-6 Dx
		"15b3 1021 101e", // Nvidia_mlx5_ConnectX-7
		"14e4 16d7 16dc", // BCM57414 2x25G
		"14e4 1750 1806", // BCM75508 2x100G
	}
	os.Exit(m.Run())
}

func TestWebhook(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Webhook Suite")
}

func newNodeState() *SriovNetworkNodeState {
	return &SriovNetworkNodeState{
		Spec: SriovNetworkNodeStateSpec{
			Interfaces: []Interface{
				{
					Name:       "ens803f1",
					NumVfs:     4,
					PciAddress: "0000:86:00.1",
					VfGroups: []VfGroup{
						{
							DeviceType:   "netdevice",
							ResourceName: "nic1",
							VfRange:      "0-3",
						},
					},
				},
			},
		},
		Status: SriovNetworkNodeStateStatus{
			Interfaces: []InterfaceExt{
				{
					VFs: []VirtualFunction{
						{},
					},
					DeviceID:   "158b",
					Driver:     "i40e",
					Mtu:        1500,
					Name:       "ens803f0",
					PciAddress: "0000:86:00.0",
					Vendor:     "8086",
					NumVfs:     4,
					TotalVfs:   64,
					LinkType:   "ETH",
				},
				{
					VFs: []VirtualFunction{
						{},
					},
					DeviceID:   "158b",
					Driver:     "i40e",
					Mtu:        1500,
					Name:       "ens803f1",
					PciAddress: "0000:86:00.1",
					Vendor:     "8086",
					NumVfs:     4,
					TotalVfs:   64,
					LinkType:   "ETH",
				},
				{
					VFs: []VirtualFunction{
						{},
					},
					DeviceID:   "1015",
					Driver:     "i40e",
					Mtu:        1500,
					Name:       "ens803f2",
					PciAddress: "0000:86:00.2",
					Vendor:     "8086",
					NumVfs:     4,
					TotalVfs:   64,
					LinkType:   "ETH",
				},
			},
		},
	}
}

func newNodePolicy() *SriovNetworkNodePolicy {
	return &SriovNetworkNodePolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name: "p1",
		},
		Spec: SriovNetworkNodePolicySpec{
			DeviceType: "netdevice",
			NicSelector: SriovNetworkNicSelector{
				PfNames:     []string{"ens803f1#0-2"},
				RootDevices: []string{"0000:86:00.1"},
				Vendor:      "8086",
			},
			NodeSelector: map[string]string{
				"feature.node.kubernetes.io/network-sriov.capable": "true",
			},
			NumVfs:       63,
			Priority:     99,
			ResourceName: "p1",
		},
	}
}

func NewNode() *corev1.Node {
	return &corev1.Node{Spec: corev1.NodeSpec{ProviderID: "openstack"}}
}

func newDefaultOperatorConfig() *SriovOperatorConfig {
	return &SriovOperatorConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "default",
			Namespace: vars.Namespace,
		},
		Spec: SriovOperatorConfigSpec{
			ConfigDaemonNodeSelector: map[string]string{},
			DisableDrain:             true,
			EnableInjector:           true,
			EnableOperatorWebhook:    true,
			LogLevel:                 2,
		},
	}
}

func newDefaultNetworkPoolConfig() *SriovNetworkPoolConfig {
	maxUn := intstr.FromInt32(1)
	return &SriovNetworkPoolConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name: "default",
		},
		Spec: SriovNetworkPoolConfigSpec{
			MaxUnavailable: &maxUn,
			NodeSelector:   &metav1.LabelSelector{MatchLabels: map[string]string{}},
		},
	}
}

func TestValidateSriovOperatorConfigWithDefaultOperatorConfig(t *testing.T) {
	g := NewGomegaWithT(t)

	config := newDefaultOperatorConfig()
	client = fake.NewClientBuilder().WithScheme(vars.Scheme).Build()

	ok, w, err := validateSriovOperatorConfig(config, "DELETE")
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(ok).To(Equal(true))
	g.Expect(w).To(BeEmpty())

	ok, _, err = validateSriovOperatorConfig(config, "UPDATE")
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(ok).To(Equal(true))

	ok, w, err = validateSriovOperatorConfig(config, "UPDATE")
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(ok).To(Equal(true))
	g.Expect(w[0]).To(ContainSubstring("Node draining is disabled"))

	ok, _, err = validateSriovOperatorConfig(config, "CREATE")
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(ok).To(Equal(true))
}

func TestValidateSriovOperatorConfigDisableDrain(t *testing.T) {
	g := NewGomegaWithT(t)

	config := newDefaultOperatorConfig()
	config.Spec.DisableDrain = false

	nodeState := &SriovNetworkNodeState{
		ObjectMeta: metav1.ObjectMeta{Name: "worker-1", Namespace: namespace},
		Status: SriovNetworkNodeStateStatus{
			SyncStatus: "InProgress",
		},
	}

	client = fake.NewClientBuilder().WithScheme(vars.Scheme).WithObjects(config, nodeState).Build()

	config.Spec.DisableDrain = true
	ok, _, err := validateSriovOperatorConfig(config, "UPDATE")
	g.Expect(err).To(MatchError("can't set Spec.DisableDrain = true while node[worker-1] is updating"))
	g.Expect(ok).To(Equal(false))

	// Simulate node update finished
	nodeState.Status.SyncStatus = "Succeeded"
	err = client.Update(context.Background(), nodeState)
	g.Expect(err).ToNot(HaveOccurred())

	ok, _, err = validateSriovOperatorConfig(config, "UPDATE")
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(ok).To(Equal(true))
}

func TestValidateSriovNetworkPoolConfigWithDefault(t *testing.T) {
	g := NewGomegaWithT(t)

	config := newDefaultNetworkPoolConfig()
	client = fake.NewClientBuilder().WithScheme(vars.Scheme).Build()

	ok, _, err := validateSriovNetworkPoolConfig(config, "DELETE")
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(ok).To(Equal(true))

	ok, _, err = validateSriovNetworkPoolConfig(config, "UPDATE")
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(ok).To(Equal(true))

	ok, _, err = validateSriovNetworkPoolConfig(config, "CREATE")
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(ok).To(Equal(true))
}

func TestValidateSriovNetworkPoolConfigWithParallelAndHWOffload(t *testing.T) {
	g := NewGomegaWithT(t)

	config := newDefaultNetworkPoolConfig()
	config.Spec.OvsHardwareOffloadConfig = &OvsHardwareOffloadConfig{Name: "test"}
	client = fake.NewClientBuilder().WithScheme(vars.Scheme).Build()

	ok, _, err := validateSriovNetworkPoolConfig(config, "UPDATE")
	g.Expect(err).To(HaveOccurred())
	g.Expect(ok).To(BeFalse())

	ok, _, err = validateSriovNetworkPoolConfig(config, "CREATE")
	g.Expect(err).To(HaveOccurred())
	g.Expect(ok).To(BeFalse())
}

func TestValidateSriovNetworkNodePolicyWithDefaultPolicy(t *testing.T) {
	var err error
	var ok bool
	policy := &SriovNetworkNodePolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "default",
			Namespace: "openshift-sriov-network-operator",
		},
		Spec: SriovNetworkNodePolicySpec{
			NicSelector:  SriovNetworkNicSelector{},
			NodeSelector: map[string]string{},
			NumVfs:       1,
			ResourceName: "p0",
		},
	}
	os.Setenv("NAMESPACE", "openshift-sriov-network-operator")
	vars.Namespace = "openshift-sriov-network-operator"
	g := NewGomegaWithT(t)
	ok, w, err := validateSriovNetworkNodePolicy(policy, "DELETE")
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(ok).To(Equal(true))
	g.Expect(w).To(BeEmpty())

	ok, _, err = validateSriovNetworkNodePolicy(policy, "UPDATE")
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(ok).To(Equal(true))

	ok, _, err = validateSriovNetworkNodePolicy(policy, "CREATE")
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(ok).To(Equal(true))
}

func TestValidatePolicyForNodeStateWithValidPolicy(t *testing.T) {
	state := newNodeState()
	policy := &SriovNetworkNodePolicy{
		Spec: SriovNetworkNodePolicySpec{
			DeviceType: "netdevice",
			NicSelector: SriovNetworkNicSelector{
				PfNames:     []string{"ens803f0"},
				RootDevices: []string{"0000:86:00.0"},
				Vendor:      "8086",
			},
			NodeSelector: map[string]string{
				"feature.node.kubernetes.io/network-sriov.capable": "true",
			},
			NumVfs:       63,
			Priority:     99,
			ResourceName: "p0",
		},
	}
	g := NewGomegaWithT(t)
	_, err := validatePolicyForNodeState(policy, state, NewNode())
	g.Expect(err).NotTo(HaveOccurred())
}

func TestValidatePolicyForNodeStateWithInvalidNumVfsPolicy(t *testing.T) {
	state := newNodeState()
	policy := &SriovNetworkNodePolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name: "p1",
		},
		Spec: SriovNetworkNodePolicySpec{
			DeviceType: "netdevice",
			NicSelector: SriovNetworkNicSelector{
				PfNames:     []string{"ens803f0"},
				RootDevices: []string{"0000:86:00.0"},
				Vendor:      "8086",
			},
			NodeSelector: map[string]string{
				"feature.node.kubernetes.io/network-sriov.capable": "true",
			},
			NumVfs:       65,
			Priority:     99,
			ResourceName: "p0",
		},
	}
	g := NewGomegaWithT(t)
	_, err := validatePolicyForNodeState(policy, state, NewNode())
	g.Expect(err).To(MatchError("numVfs(65) in CR p1 exceed the maximum allowed value(64) interface(ens803f0)"))
}

func TestValidatePolicyForNodeStateWithInvalidNumVfsExternallyCreated(t *testing.T) {
	state := newNodeState()
	policy := &SriovNetworkNodePolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name: "p1",
		},
		Spec: SriovNetworkNodePolicySpec{
			DeviceType: "netdevice",
			NicSelector: SriovNetworkNicSelector{
				PfNames:     []string{"ens803f0"},
				RootDevices: []string{"0000:86:00.0"},
				Vendor:      "8086",
			},
			NodeSelector: map[string]string{
				"feature.node.kubernetes.io/network-sriov.capable": "true",
			},
			NumVfs:            5,
			Priority:          99,
			ResourceName:      "p0",
			ExternallyManaged: true,
		},
	}
	g := NewGomegaWithT(t)
	_, err := validatePolicyForNodeState(policy, state, NewNode())
	g.Expect(err).To(MatchError(ContainSubstring(fmt.Sprintf("numVfs(%d) in CR %s is higher than the virtual functions allocated for the PF externally value(%d)", policy.Spec.NumVfs, policy.GetName(), state.Status.Interfaces[0].NumVfs))))
}

func TestValidatePolicyForNodeStateWithValidNumVfsExternallyCreated(t *testing.T) {
	state := newNodeState()
	policy := &SriovNetworkNodePolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name: "p1",
		},
		Spec: SriovNetworkNodePolicySpec{
			DeviceType: "netdevice",
			NicSelector: SriovNetworkNicSelector{
				PfNames:     []string{"ens803f0"},
				RootDevices: []string{"0000:86:00.0"},
				Vendor:      "8086",
			},
			NodeSelector: map[string]string{
				"feature.node.kubernetes.io/network-sriov.capable": "true",
			},
			NumVfs:            4,
			Priority:          99,
			ResourceName:      "p0",
			ExternallyManaged: true,
		},
	}
	g := NewGomegaWithT(t)
	_, err := validatePolicyForNodeState(policy, state, NewNode())
	g.Expect(err).ToNot(HaveOccurred())
}

func TestValidatePolicyForNodeStateWithValidLowerNumVfsExternallyCreated(t *testing.T) {
	state := newNodeState()
	policy := &SriovNetworkNodePolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name: "p1",
		},
		Spec: SriovNetworkNodePolicySpec{
			DeviceType: "netdevice",
			NicSelector: SriovNetworkNicSelector{
				PfNames:     []string{"ens803f0"},
				RootDevices: []string{"0000:86:00.0"},
				Vendor:      "8086",
			},
			NodeSelector: map[string]string{
				"feature.node.kubernetes.io/network-sriov.capable": "true",
			},
			NumVfs:            3,
			Priority:          99,
			ResourceName:      "p0",
			ExternallyManaged: true,
		},
	}
	g := NewGomegaWithT(t)
	_, err := validatePolicyForNodeState(policy, state, NewNode())
	g.Expect(err).ToNot(HaveOccurred())
}

func TestValidatePolicyForNodePolicyWithOutExternallyManageConflict(t *testing.T) {
	appliedPolicy := newNodePolicy()
	appliedPolicy.Spec.ExternallyManaged = true
	policy := &SriovNetworkNodePolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name: "p0",
		},
		Spec: SriovNetworkNodePolicySpec{
			DeviceType: "netdevice",
			NicSelector: SriovNetworkNicSelector{
				PfNames: []string{"ens803f1#3-4"},
				Vendor:  "8086",
			},
			NodeSelector: map[string]string{
				"feature.node.kubernetes.io/network-sriov.capable": "true",
			},
			NumVfs:            63,
			Priority:          99,
			ResourceName:      "p0",
			ExternallyManaged: true,
		},
	}
	g := NewGomegaWithT(t)
	err := validatePolicyForNodePolicy(policy, appliedPolicy, nil)
	g.Expect(err).ToNot(HaveOccurred())
}

func TestValidatePolicyForNodePolicyWithExternallyManageConflict(t *testing.T) {
	appliedPolicy := newNodePolicy()
	policy := &SriovNetworkNodePolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name: "p0",
		},
		Spec: SriovNetworkNodePolicySpec{
			DeviceType: "netdevice",
			NicSelector: SriovNetworkNicSelector{
				PfNames: []string{"ens803f1#3-4"},
				Vendor:  "8086",
			},
			NodeSelector: map[string]string{
				"feature.node.kubernetes.io/network-sriov.capable": "true",
			},
			NumVfs:            63,
			Priority:          99,
			ResourceName:      "p0",
			ExternallyManaged: true,
		},
	}
	g := NewGomegaWithT(t)
	err := validatePolicyForNodePolicy(policy, appliedPolicy, nil)
	g.Expect(err).To(MatchError(ContainSubstring(fmt.Sprintf("externallyManage is inconsistent with existing policy %s", appliedPolicy.ObjectMeta.Name))))
}

func TestValidatePolicyForNodeStateWithExternallyManageAndMTU(t *testing.T) {
	state := newNodeState()
	policy := &SriovNetworkNodePolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name: "p1",
		},
		Spec: SriovNetworkNodePolicySpec{
			DeviceType: "netdevice",
			NicSelector: SriovNetworkNicSelector{
				PfNames:     []string{"ens803f0"},
				RootDevices: []string{"0000:86:00.0"},
				Vendor:      "8086",
			},
			NodeSelector: map[string]string{
				"feature.node.kubernetes.io/network-sriov.capable": "true",
			},
			NumVfs:            4,
			Priority:          99,
			ResourceName:      "p0",
			ExternallyManaged: true,
			Mtu:               1500,
		},
	}
	g := NewGomegaWithT(t)
	_, err := validatePolicyForNodeState(policy, state, NewNode())
	g.Expect(err).ToNot(HaveOccurred())
}

func TestValidatePolicyForNodeStateWithExternallyManageAndDifferentMTU(t *testing.T) {
	state := newNodeState()
	policy := &SriovNetworkNodePolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name: "p1",
		},
		Spec: SriovNetworkNodePolicySpec{
			DeviceType: "netdevice",
			NicSelector: SriovNetworkNicSelector{
				PfNames:     []string{"ens803f0"},
				RootDevices: []string{"0000:86:00.0"},
				Vendor:      "8086",
			},
			NodeSelector: map[string]string{
				"feature.node.kubernetes.io/network-sriov.capable": "true",
			},
			NumVfs:            4,
			Priority:          99,
			ResourceName:      "p0",
			ExternallyManaged: true,
			Mtu:               9000,
		},
	}
	g := NewGomegaWithT(t)
	_, err := validatePolicyForNodeState(policy, state, NewNode())
	g.Expect(err).To(HaveOccurred())
}

func TestValidatePolicyForNodeStateWithExternallyManageAndLinkType(t *testing.T) {
	state := newNodeState()
	policy := &SriovNetworkNodePolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name: "p1",
		},
		Spec: SriovNetworkNodePolicySpec{
			DeviceType: "netdevice",
			NicSelector: SriovNetworkNicSelector{
				PfNames:     []string{"ens803f0"},
				RootDevices: []string{"0000:86:00.0"},
				Vendor:      "8086",
			},
			NodeSelector: map[string]string{
				"feature.node.kubernetes.io/network-sriov.capable": "true",
			},
			NumVfs:            4,
			Priority:          99,
			ResourceName:      "p0",
			ExternallyManaged: true,
			LinkType:          "ETH",
		},
	}
	g := NewGomegaWithT(t)
	_, err := validatePolicyForNodeState(policy, state, NewNode())
	g.Expect(err).ToNot(HaveOccurred())

	policy.Spec.LinkType = "eth"
	_, err = validatePolicyForNodeState(policy, state, NewNode())
	g.Expect(err).ToNot(HaveOccurred())

	policy.Spec.LinkType = "ETH"
	state.Status.Interfaces[0].LinkType = "eth"
	_, err = validatePolicyForNodeState(policy, state, NewNode())
	g.Expect(err).ToNot(HaveOccurred())
}

func TestValidatePolicyForNodeStateWithExternallyManageAndDifferentLinkType(t *testing.T) {
	state := newNodeState()
	policy := &SriovNetworkNodePolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name: "p1",
		},
		Spec: SriovNetworkNodePolicySpec{
			DeviceType: "netdevice",
			NicSelector: SriovNetworkNicSelector{
				PfNames:     []string{"ens803f0"},
				RootDevices: []string{"0000:86:00.0"},
				Vendor:      "8086",
			},
			NodeSelector: map[string]string{
				"feature.node.kubernetes.io/network-sriov.capable": "true",
			},
			NumVfs:            4,
			Priority:          99,
			ResourceName:      "p0",
			ExternallyManaged: true,
			Mtu:               9000,
			LinkType:          "IB",
		},
	}
	g := NewGomegaWithT(t)
	_, err := validatePolicyForNodeState(policy, state, NewNode())
	g.Expect(err).To(HaveOccurred())
}

func TestValidatePolicyForNodePolicyWithOverlappedVfRange(t *testing.T) {
	appliedPolicy := newNodePolicy()
	policy := &SriovNetworkNodePolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name: "p0",
		},
		Spec: SriovNetworkNodePolicySpec{
			DeviceType: "netdevice",
			NicSelector: SriovNetworkNicSelector{
				PfNames: []string{"ens803f1#2-2"},
				Vendor:  "8086",
			},
			NodeSelector: map[string]string{
				"feature.node.kubernetes.io/network-sriov.capable": "true",
			},
			NumVfs:       63,
			Priority:     99,
			ResourceName: "p0",
		},
	}
	g := NewGomegaWithT(t)
	err := validatePolicyForNodePolicy(policy, appliedPolicy, nil)
	g.Expect(err).To(MatchError(ContainSubstring(fmt.Sprintf("VF index range in %s is overlapped with existing policy %s", policy.Spec.NicSelector.PfNames[0], appliedPolicy.ObjectMeta.Name))))
}

func TestValidatePolicyForNodeStateWithUpdatedExistingVfRange(t *testing.T) {
	appliedPolicy := newNodePolicy()
	policy := &SriovNetworkNodePolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name: "p1",
		},
		Spec: SriovNetworkNodePolicySpec{
			DeviceType: "netdevice",
			NicSelector: SriovNetworkNicSelector{
				PfNames:     []string{"ens803f1#1-2"},
				RootDevices: []string{"0000:86:00.1"},
				Vendor:      "8086",
			},
			NodeSelector: map[string]string{
				"feature.node.kubernetes.io/network-sriov.capable": "true",
			},
			NumVfs:       63,
			Priority:     99,
			ResourceName: "p1",
		},
	}
	g := NewGomegaWithT(t)
	err := validatePolicyForNodePolicy(policy, appliedPolicy, nil)
	g.Expect(err).NotTo(HaveOccurred())
}

func TestValidatePoliciesWithDifferentExcludeTopologyForTheSameResource(t *testing.T) {
	current := &SriovNetworkNodePolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "currentPolicy"},
		Spec: SriovNetworkNodePolicySpec{
			ResourceName:    "resourceX",
			ExcludeTopology: true,
		},
	}

	previous := &SriovNetworkNodePolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "previousPolicy"},
		Spec: SriovNetworkNodePolicySpec{
			ResourceName:    "resourceX",
			ExcludeTopology: false,
		},
	}

	err := validatePolicyForNodePolicy(current, previous, nil)

	g := NewGomegaWithT(t)
	g.Expect(err).To(MatchError("excludeTopology[true] field conflicts with policy [previousPolicy].ExcludeTopology[false] as they target the same resource[resourceX]"))
}

func TestValidatePoliciesWithDifferentExcludeTopologyForTheSameResourceAndTheSamePF(t *testing.T) {
	current := &SriovNetworkNodePolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "currentPolicy"},
		Spec: SriovNetworkNodePolicySpec{
			ResourceName:    "resourceX",
			NumVfs:          10,
			NicSelector:     SriovNetworkNicSelector{PfNames: []string{"eno1#0-4"}},
			ExcludeTopology: true,
		},
	}

	previous := &SriovNetworkNodePolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "previousPolicy"},
		Spec: SriovNetworkNodePolicySpec{
			ResourceName:    "resourceX",
			NumVfs:          10,
			NicSelector:     SriovNetworkNicSelector{PfNames: []string{"eno1#5-9"}},
			ExcludeTopology: false,
		},
	}

	err := validatePolicyForNodePolicy(current, previous, nil)

	g := NewGomegaWithT(t)
	g.Expect(err).To(MatchError("excludeTopology[true] field conflicts with policy [previousPolicy].ExcludeTopology[false] as they target the same resource[resourceX]"))
}

func TestValidatePoliciesWithSameExcludeTopologyForTheSameResource(t *testing.T) {
	current := &SriovNetworkNodePolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "currentPolicy"},
		Spec: SriovNetworkNodePolicySpec{
			ResourceName:    "resourceX",
			ExcludeTopology: true,
		},
	}

	previous := &SriovNetworkNodePolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "previousPolicy"},
		Spec: SriovNetworkNodePolicySpec{
			ResourceName:    "resourceX",
			ExcludeTopology: true,
		},
	}

	err := validatePolicyForNodePolicy(current, previous, nil)

	g := NewGomegaWithT(t)
	g.Expect(err).NotTo(HaveOccurred())
}

func TestValidatePoliciesWithDifferentNumVfForTheSameResourceAndTheSameRootDevice(t *testing.T) {
	current := &SriovNetworkNodePolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "currentPolicy"},
		Spec: SriovNetworkNodePolicySpec{
			ResourceName: "resourceX",
			NumVfs:       10,
			NicSelector:  SriovNetworkNicSelector{RootDevices: []string{"0000:86:00.1"}},
		},
	}

	previous := &SriovNetworkNodePolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "previousPolicy"},
		Spec: SriovNetworkNodePolicySpec{
			ResourceName: "resourceX",
			NumVfs:       5,
			NicSelector:  SriovNetworkNicSelector{RootDevices: []string{"0000:86:00.1"}},
		},
	}

	err := validatePolicyForNodePolicy(current, previous, nil)

	g := NewGomegaWithT(t)
	g.Expect(err).To(MatchError("root device 0000:86:00.1 is overlapped with existing policy previousPolicy"))
}

func TestValidateDRAResourceNameCollision(t *testing.T) {
	for _, draEnabled := range []bool{false, true} {
		t.Run(fmt.Sprintf("DRA enabled=%t", draEnabled), func(t *testing.T) {
			config, _ := setupDRANetFilterValidation(t)
			config.Spec.FeatureGates[constants.DynamicResourceAllocationFeatureGate] = draEnabled
			client = fake.NewClientBuilder().WithScheme(vars.Scheme).WithObjects(config).Build()
			g := NewGomegaWithT(t)

			existing := &SriovNetworkNodePolicy{
				ObjectMeta: metav1.ObjectMeta{Name: "policy-a", Namespace: vars.Namespace},
				Spec:       SriovNetworkNodePolicySpec{ResourceName: "intel_nic"},
			}
			npList := &SriovNetworkNodePolicyList{Items: []SriovNetworkNodePolicy{*existing}}

			colliding := &SriovNetworkNodePolicy{
				ObjectMeta: metav1.ObjectMeta{Name: "policy-b", Namespace: vars.Namespace},
				Spec:       SriovNetworkNodePolicySpec{ResourceName: "INTEL_NIC"},
			}
			err := validateDRAResourcePolicies(colliding, npList)
			if draEnabled {
				g.Expect(err).To(MatchError(ContainSubstring("normalizes to the same DRA device class name")))
			} else {
				g.Expect(err).NotTo(HaveOccurred())
			}

			unique := &SriovNetworkNodePolicy{
				ObjectMeta: metav1.ObjectMeta{Name: "policy-c", Namespace: vars.Namespace},
				Spec:       SriovNetworkNodePolicySpec{ResourceName: "mlx_nic"},
			}
			err = validateDRAResourcePolicies(unique, npList)
			g.Expect(err).NotTo(HaveOccurred())

			invalidPeer := &SriovNetworkNodePolicy{
				ObjectMeta: metav1.ObjectMeta{Name: "policy-invalid", Namespace: vars.Namespace},
			}
			err = validateDRAResourcePolicies(unique, &SriovNetworkNodePolicyList{Items: []SriovNetworkNodePolicy{*existing, *invalidPeer}})
			g.Expect(err).NotTo(HaveOccurred())

			outOfNamespace := &SriovNetworkNodePolicy{
				ObjectMeta: metav1.ObjectMeta{Name: "policy-other-ns", Namespace: "other-namespace"},
				Spec:       SriovNetworkNodePolicySpec{ResourceName: "INTEL_NIC"},
			}
			err = validateDRAResourcePolicies(outOfNamespace, npList)
			g.Expect(err).NotTo(HaveOccurred())
		})
	}
}

func TestValidateResourceName(t *testing.T) {
	cases := []struct {
		name      string
		input     string
		expectErr bool
		errSubstr string
	}{
		{name: "valid simple", input: "myresource"},
		{name: "valid with hyphen", input: "my-resource"},
		{name: "valid uppercase", input: "MyResource"},
		{name: "valid single char", input: "a"},
		{name: "underscore in name", input: "my_resource"},
		{name: "underscore with hyphen", input: "net_device-1"},
		{name: "underscore only", input: "_", expectErr: true, errSubstr: "invalid"},
		{name: "valid 63 chars", input: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
		{name: "empty", input: "", expectErr: true, errSubstr: "must not be empty"},
		{name: "leading hyphen", input: "-resource", expectErr: true, errSubstr: "invalid"},
		{name: "trailing hyphen", input: "resource-", expectErr: true, errSubstr: "invalid"},
		{name: "only hyphen", input: "-", expectErr: true, errSubstr: "invalid"},
		{name: "64 chars", input: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", expectErr: true, errSubstr: "no more than 63"},
		{name: "invalid char space", input: "my resource", expectErr: true, errSubstr: "invalid"},
		{name: "invalid char dot", input: "my.resource", expectErr: true, errSubstr: "invalid"},
		{name: "invalid char slash", input: "my/resource", expectErr: true, errSubstr: "invalid"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			err := ValidateResourceName(tc.input)
			if tc.expectErr {
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error()).To(ContainSubstring(tc.errSubstr))
			} else {
				g.Expect(err).NotTo(HaveOccurred())
			}
		})
	}
}

func TestStaticValidateSriovNetworkNodePolicyWithValidVendorDevice(t *testing.T) {
	policy := &SriovNetworkNodePolicy{
		Spec: SriovNetworkNodePolicySpec{
			DeviceType: "netdevice",
			NicSelector: SriovNetworkNicSelector{
				Vendor:   "8086",
				DeviceID: "158b",
			},
			NodeSelector: map[string]string{
				"feature.node.kubernetes.io/network-sriov.capable": "true",
			},
			NumVfs:       63,
			Priority:     99,
			ResourceName: "p0",
		},
	}
	g := NewGomegaWithT(t)
	ok, err := staticValidateSriovNetworkNodePolicy(policy)
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(ok).To(Equal(true))
}

func TestStaticValidateSriovNetworkNodePolicyWithInvalidVendor(t *testing.T) {
	policy := &SriovNetworkNodePolicy{
		Spec: SriovNetworkNodePolicySpec{
			DeviceType: "netdevice",
			NicSelector: SriovNetworkNicSelector{
				Vendor: "8087",
			},
			NodeSelector: map[string]string{
				"feature.node.kubernetes.io/network-sriov.capable": "true",
			},
			NumVfs:       63,
			Priority:     99,
			ResourceName: "p0",
		},
	}
	g := NewGomegaWithT(t)
	ok, err := staticValidateSriovNetworkNodePolicy(policy)
	g.Expect(err).To(MatchError(ContainSubstring("vendor %s is not supported", policy.Spec.NicSelector.Vendor)))
	g.Expect(ok).To(Equal(false))
}

func TestStaticValidateSriovNetworkNodePolicyWithInvalidVendorDevMode(t *testing.T) {
	t.Setenv("DEV_MODE", "TRUE")
	policy := &SriovNetworkNodePolicy{
		Spec: SriovNetworkNodePolicySpec{
			DeviceType: "netdevice",
			NicSelector: SriovNetworkNicSelector{
				Vendor: "8087",
			},
			NodeSelector: map[string]string{
				"feature.node.kubernetes.io/network-sriov.capable": "true",
			},
			NumVfs:       63,
			Priority:     99,
			ResourceName: "p0",
		},
	}
	g := NewGomegaWithT(t)
	ok, err := staticValidateSriovNetworkNodePolicy(policy)
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(ok).To(Equal(true))
}

func TestStaticValidateSriovNetworkNodePolicyWithInvalidDevice(t *testing.T) {
	policy := &SriovNetworkNodePolicy{
		Spec: SriovNetworkNodePolicySpec{
			DeviceType: "netdevice",
			NicSelector: SriovNetworkNicSelector{
				DeviceID: "1234",
			},
			NodeSelector: map[string]string{
				"feature.node.kubernetes.io/network-sriov.capable": "true",
			},
			NumVfs:       63,
			Priority:     99,
			ResourceName: "p0",
		},
	}
	g := NewGomegaWithT(t)
	ok, err := staticValidateSriovNetworkNodePolicy(policy)
	g.Expect(err).To(MatchError(ContainSubstring("device %s is not supported", policy.Spec.NicSelector.DeviceID)))
	g.Expect(ok).To(Equal(false))
}

func TestStaticValidateSriovNetworkNodePolicyWithInvalidVendorDevice(t *testing.T) {
	policy := &SriovNetworkNodePolicy{
		Spec: SriovNetworkNodePolicySpec{
			DeviceType: "netdevice",
			NicSelector: SriovNetworkNicSelector{
				Vendor:   "8086",
				DeviceID: "1015",
			},
			NodeSelector: map[string]string{
				"feature.node.kubernetes.io/network-sriov.capable": "true",
			},
			NumVfs:       63,
			Priority:     99,
			ResourceName: "p0",
		},
	}
	g := NewGomegaWithT(t)
	ok, err := staticValidateSriovNetworkNodePolicy(policy)
	g.Expect(err).To(MatchError(ContainSubstring("vendor/device %s/%s is not supported", policy.Spec.NicSelector.Vendor, policy.Spec.NicSelector.DeviceID)))
	g.Expect(ok).To(Equal(false))
}

func TestStaticValidateSriovNetworkNodePolicyWithConflictIsRdmaAndDeviceType(t *testing.T) {
	policy := &SriovNetworkNodePolicy{
		Spec: SriovNetworkNodePolicySpec{
			DeviceType: constants.DeviceTypeVfioPci,
			NicSelector: SriovNetworkNicSelector{
				Vendor:   "8086",
				DeviceID: "158b",
			},
			NodeSelector: map[string]string{
				"feature.node.kubernetes.io/network-sriov.capable": "true",
			},
			NumVfs:       1,
			Priority:     99,
			ResourceName: "p0",
			IsRdma:       true,
		},
	}
	g := NewGomegaWithT(t)
	ok, err := staticValidateSriovNetworkNodePolicy(policy)
	g.Expect(err).To(MatchError(ContainSubstring("'deviceType: vfio-pci' conflicts with 'isRdma: true'")))
	g.Expect(ok).To(Equal(false))
}

func TestStaticValidateSriovNetworkNodePolicyWithConflictDeviceTypeAndVirtioVdpaType(t *testing.T) {
	policy := &SriovNetworkNodePolicy{
		Spec: SriovNetworkNodePolicySpec{
			DeviceType: constants.DeviceTypeVfioPci,
			NicSelector: SriovNetworkNicSelector{
				Vendor:   "15b3",
				DeviceID: "101d",
			},
			NodeSelector: map[string]string{
				"feature.node.kubernetes.io/network-sriov.capable": "true",
			},
			NumVfs:       1,
			Priority:     99,
			ResourceName: "p0",
			VdpaType:     constants.VdpaTypeVirtio,
			EswitchMode:  "switchdev",
		},
	}
	g := NewGomegaWithT(t)
	ok, err := staticValidateSriovNetworkNodePolicy(policy)
	g.Expect(err).To(MatchError(ContainSubstring("'deviceType: vfio-pci' conflicts with 'virtio'")))
	g.Expect(ok).To(Equal(false))
}

func TestStaticValidateSriovNetworkNodePolicyWithConflictDeviceTypeAndVhostVdpaType(t *testing.T) {
	policy := &SriovNetworkNodePolicy{
		Spec: SriovNetworkNodePolicySpec{
			DeviceType: constants.DeviceTypeVfioPci,
			NicSelector: SriovNetworkNicSelector{
				Vendor:   "15b3",
				DeviceID: "101d",
			},
			NodeSelector: map[string]string{
				"feature.node.kubernetes.io/network-sriov.capable": "true",
			},
			NumVfs:       1,
			Priority:     99,
			ResourceName: "p0",
			VdpaType:     constants.VdpaTypeVhost,
			EswitchMode:  "switchdev",
		},
	}
	g := NewGomegaWithT(t)
	ok, err := staticValidateSriovNetworkNodePolicy(policy)
	g.Expect(err).To(MatchError(ContainSubstring("'deviceType: vfio-pci' conflicts with 'vhost'")))
	g.Expect(ok).To(Equal(false))
}

func TestStaticValidateSriovNetworkNodePolicyVirtioVdpaMustSpecifySwitchDev(t *testing.T) {
	policy := &SriovNetworkNodePolicy{
		Spec: SriovNetworkNodePolicySpec{
			DeviceType: "netdevice",
			NicSelector: SriovNetworkNicSelector{
				Vendor:   "15b3",
				DeviceID: "101d",
			},
			NodeSelector: map[string]string{
				"feature.node.kubernetes.io/network-sriov.capable": "true",
			},
			NumVfs:       1,
			Priority:     99,
			ResourceName: "p0",
			VdpaType:     constants.VdpaTypeVirtio,
		},
	}
	g := NewGomegaWithT(t)
	ok, err := staticValidateSriovNetworkNodePolicy(policy)
	g.Expect(err).To(MatchError(ContainSubstring("vdpa requires the device to be configured in switchdev mode")))
	g.Expect(ok).To(Equal(false))
}

func TestStaticValidateSriovNetworkNodePolicyVhostVdpaMustSpecifySwitchDev(t *testing.T) {
	policy := &SriovNetworkNodePolicy{
		Spec: SriovNetworkNodePolicySpec{
			DeviceType: "netdevice",
			NicSelector: SriovNetworkNicSelector{
				Vendor:   "15b3",
				DeviceID: "101d",
			},
			NodeSelector: map[string]string{
				"feature.node.kubernetes.io/network-sriov.capable": "true",
			},
			NumVfs:       1,
			Priority:     99,
			ResourceName: "p0",
			VdpaType:     constants.VdpaTypeVhost,
		},
	}
	g := NewGomegaWithT(t)
	ok, err := staticValidateSriovNetworkNodePolicy(policy)
	g.Expect(err).To(MatchError(ContainSubstring("vdpa requires the device to be configured in switchdev mode")))
	g.Expect(ok).To(Equal(false))
}

func TestValidatePolicyForNodeStateVirtioVdpaWithNotSupportedVendor(t *testing.T) {
	state := newNodeState()
	policy := &SriovNetworkNodePolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name: "p1",
		},
		Spec: SriovNetworkNodePolicySpec{
			DeviceType: "netdevice",
			VdpaType:   "virtio",
			NicSelector: SriovNetworkNicSelector{
				PfNames:     []string{"ens803f0"},
				RootDevices: []string{"0000:86:00.0"},
				Vendor:      "8086",
			},
			NodeSelector: map[string]string{
				"feature.node.kubernetes.io/network-sriov.capable": "true",
			},
			NumVfs:       4,
			Priority:     99,
			ResourceName: "p0",
		},
	}
	g := NewGomegaWithT(t)
	_, err := validatePolicyForNodeState(policy, state, NewNode())
	g.Expect(err).To(MatchError("vendor(8086) in CR p1 not supported for vdpa interface(ens803f0)"))
}

func TestValidatePolicyForNodeStateVhostVdpaWithNotSupportedVendor(t *testing.T) {
	state := newNodeState()
	policy := &SriovNetworkNodePolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name: "p1",
		},
		Spec: SriovNetworkNodePolicySpec{
			DeviceType: "netdevice",
			VdpaType:   "vhost",
			NicSelector: SriovNetworkNicSelector{
				PfNames:     []string{"ens803f0"},
				RootDevices: []string{"0000:86:00.0"},
				Vendor:      "8086",
			},
			NodeSelector: map[string]string{
				"feature.node.kubernetes.io/network-sriov.capable": "true",
			},
			NumVfs:       4,
			Priority:     99,
			ResourceName: "p0",
		},
	}
	g := NewGomegaWithT(t)
	_, err := validatePolicyForNodeState(policy, state, NewNode())
	g.Expect(err).To(MatchError("vendor(8086) in CR p1 not supported for vdpa interface(ens803f0)"))
}

func TestValidatePolicyForNodeStateWithInvalidDevice(t *testing.T) {
	state := newNodeState()
	policy := &SriovNetworkNodePolicy{
		Spec: SriovNetworkNodePolicySpec{
			DeviceType: "netdevice",
			NicSelector: SriovNetworkNicSelector{
				DeviceID: "1015",
			},
			NodeSelector: map[string]string{
				"feature.node.kubernetes.io/network-sriov.capable": "true",
			},
			NumVfs:       63,
			Priority:     99,
			ResourceName: "p0",
		},
	}
	g := NewGomegaWithT(t)

	_, err := validatePolicyForNodeState(policy, state, NewNode())
	g.Expect(err).NotTo(HaveOccurred())
}

func TestValidatePolicyForNodeStateWithInvalidPfName(t *testing.T) {
	interfaceSelected = false
	state := newNodeState()
	policy := &SriovNetworkNodePolicy{
		Spec: SriovNetworkNodePolicySpec{
			DeviceType: "netdevice",
			NicSelector: SriovNetworkNicSelector{
				PfNames: []string{"ens803f2"},
			},
			NodeSelector: map[string]string{
				"feature.node.kubernetes.io/network-sriov.capable": "true",
			},
			NumVfs:       63,
			Priority:     99,
			ResourceName: "p0",
		},
	}
	g := NewGomegaWithT(t)
	_, err := validatePolicyForNodeState(policy, state, NewNode())
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(interfaceSelected).To(Equal(false))
}

func TestValidatePolicyForNodeStateWithValidPfName(t *testing.T) {
	interfaceSelected = false
	state := newNodeState()
	policy := &SriovNetworkNodePolicy{
		Spec: SriovNetworkNodePolicySpec{
			DeviceType: "netdevice",
			NicSelector: SriovNetworkNicSelector{
				PfNames: []string{"ens803f1"},
			},
			NodeSelector: map[string]string{
				"feature.node.kubernetes.io/network-sriov.capable": "true",
			},
			NumVfs:       63,
			Priority:     99,
			ResourceName: "p0",
		},
	}
	g := NewGomegaWithT(t)
	_, err := validatePolicyForNodeState(policy, state, NewNode())
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(interfaceSelected).To(Equal(true))
}

func TestStaticValidateSriovNetworkNodePolicyWithInvalidNicSelector(t *testing.T) {
	policy := &SriovNetworkNodePolicy{
		Spec: SriovNetworkNodePolicySpec{
			DeviceType:  "netdevice",
			NicSelector: SriovNetworkNicSelector{},
			NodeSelector: map[string]string{
				"feature.node.kubernetes.io/network-sriov.capable": "true",
			},
			ResourceName: "p0",
		},
	}
	g := NewGomegaWithT(t)
	ok, err := staticValidateSriovNetworkNodePolicy(policy)
	g.Expect(err).To(HaveOccurred())
	g.Expect(ok).To(Equal(false))
}

func TestStaticValidateSriovNetworkNodePolicyWithInvalidLinkTypeForSwitchdev(t *testing.T) {
	policy := &SriovNetworkNodePolicy{
		Spec: SriovNetworkNodePolicySpec{
			DeviceType:  "netdevice",
			LinkType:    "ib",
			IsRdma:      true,
			EswitchMode: "switchdev",
			NicSelector: SriovNetworkNicSelector{
				PfNames: []string{"ens803f1"},
			},
			NodeSelector: map[string]string{
				"feature.node.kubernetes.io/network-sriov.capable": "true",
			},
			ResourceName: "p0",
		},
	}
	g := NewGomegaWithT(t)
	ok, err := staticValidateSriovNetworkNodePolicy(policy)
	g.Expect(err).To(HaveOccurred())
	g.Expect(ok).To(Equal(false))
}

func TestStaticValidateSriovNetworkNodePolicyWithBridgeConfigWithoutSwitchdev(t *testing.T) {
	policy := &SriovNetworkNodePolicy{
		Spec: SriovNetworkNodePolicySpec{
			DeviceType: "netdevice",
			Bridge:     Bridge{OVS: &OVSConfig{}},
			NicSelector: SriovNetworkNicSelector{
				PfNames: []string{"ens803f1"},
			},
			NodeSelector: map[string]string{
				"feature.node.kubernetes.io/network-sriov.capable": "true",
			},
			ResourceName: "p0",
		},
	}
	g := NewGomegaWithT(t)
	ok, err := staticValidateSriovNetworkNodePolicy(policy)
	g.Expect(err).To(HaveOccurred())
	g.Expect(ok).To(Equal(false))
}

func TestStaticValidateSriovNetworkNodePolicyWithBridgeConfigWithExternallyManaged(t *testing.T) {
	policy := &SriovNetworkNodePolicy{
		Spec: SriovNetworkNodePolicySpec{
			DeviceType:        "netdevice",
			Bridge:            Bridge{OVS: &OVSConfig{}},
			EswitchMode:       "switchdev",
			ExternallyManaged: true,
			NicSelector: SriovNetworkNicSelector{
				PfNames: []string{"ens803f1"},
			},
			NodeSelector: map[string]string{
				"feature.node.kubernetes.io/network-sriov.capable": "true",
			},
			ResourceName: "p0",
		},
	}
	g := NewGomegaWithT(t)
	ok, err := staticValidateSriovNetworkNodePolicy(policy)
	g.Expect(err).To(HaveOccurred())
	g.Expect(ok).To(Equal(false))
}

func TestValidatePolicyForNodeStateWithValidNetFilter(t *testing.T) {
	interfaceSelected = false
	state := newNodeState()
	policy := &SriovNetworkNodePolicy{
		Spec: SriovNetworkNodePolicySpec{
			DeviceType: "netdevice",
			NicSelector: SriovNetworkNicSelector{
				NetFilter: "openstack/NetworkID:ada9ec67-2c97-467c-b674-c47200e2f5da",
			},
			NodeSelector: map[string]string{
				"feature.node.kubernetes.io/network-sriov.capable": "true",
			},
			NumVfs:       63,
			Priority:     99,
			ResourceName: "p0",
		},
	}
	g := NewGomegaWithT(t)
	_, err := validatePolicyForNodeState(policy, state, NewNode())
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(interfaceSelected).To(Equal(true))
}

func TestStaticValidateSriovNetworkNodePolicyWithNetFilterAndOtherNicSelectors(t *testing.T) {
	testCases := []struct {
		name        string
		nicSelector SriovNetworkNicSelector
		expectError string
	}{
		{
			name: "netFilter with vendor",
			nicSelector: SriovNetworkNicSelector{
				NetFilter: "openstack/NetworkID:ada9ec67-2c97-467c-b674-c47200e2f5da",
				Vendor:    "8086",
			},
			expectError: "nicSelector fields vendor, deviceID, pfNames, and rootDevices are not allowed when netFilter is specified",
		},
		{
			name: "netFilter with deviceID",
			nicSelector: SriovNetworkNicSelector{
				NetFilter: "openstack/NetworkID:ada9ec67-2c97-467c-b674-c47200e2f5da",
				DeviceID:  "158b",
			},
			expectError: "nicSelector fields vendor, deviceID, pfNames, and rootDevices are not allowed when netFilter is specified",
		},
		{
			name: "netFilter with pfNames",
			nicSelector: SriovNetworkNicSelector{
				NetFilter: "openstack/NetworkID:ada9ec67-2c97-467c-b674-c47200e2f5da",
				PfNames:   []string{"ens803f0"},
			},
			expectError: "nicSelector fields vendor, deviceID, pfNames, and rootDevices are not allowed when netFilter is specified",
		},
		{
			name: "netFilter with rootDevices",
			nicSelector: SriovNetworkNicSelector{
				NetFilter:   "openstack/NetworkID:ada9ec67-2c97-467c-b674-c47200e2f5da",
				RootDevices: []string{"0000:86:00.0"},
			},
			expectError: "nicSelector fields vendor, deviceID, pfNames, and rootDevices are not allowed when netFilter is specified",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			policy := &SriovNetworkNodePolicy{
				Spec: SriovNetworkNodePolicySpec{
					DeviceType:   "netdevice",
					NicSelector:  tc.nicSelector,
					NumVfs:       1,
					ResourceName: "p0",
				},
			}
			ok, err := staticValidateSriovNetworkNodePolicy(policy)
			g.Expect(err).To(MatchError(ContainSubstring(tc.expectError)))
			g.Expect(ok).To(Equal(false))
		})
	}
}

func TestStaticValidateSriovNetworkNodePolicyWithNetFilterAndEswitchMode(t *testing.T) {
	g := NewGomegaWithT(t)
	policy := &SriovNetworkNodePolicy{
		Spec: SriovNetworkNodePolicySpec{
			DeviceType: "netdevice",
			NicSelector: SriovNetworkNicSelector{
				NetFilter: "openstack/NetworkID:ada9ec67-2c97-467c-b674-c47200e2f5da",
			},
			EswitchMode:  "switchdev",
			NumVfs:       1,
			ResourceName: "p0",
		},
	}
	ok, err := staticValidateSriovNetworkNodePolicy(policy)
	g.Expect(err).To(MatchError(ContainSubstring("eSwitchMode is not supported when netFilter is specified")))
	g.Expect(ok).To(Equal(false))
}

func TestStaticValidateSriovNetworkNodePolicyWithNetFilterAndBridge(t *testing.T) {
	g := NewGomegaWithT(t)
	policy := &SriovNetworkNodePolicy{
		Spec: SriovNetworkNodePolicySpec{
			DeviceType: "netdevice",
			NicSelector: SriovNetworkNicSelector{
				NetFilter: "openstack/NetworkID:ada9ec67-2c97-467c-b674-c47200e2f5da",
			},
			Bridge:       Bridge{OVS: &OVSConfig{}},
			NumVfs:       1,
			ResourceName: "p0",
		},
	}
	ok, err := staticValidateSriovNetworkNodePolicy(policy)
	g.Expect(err).To(MatchError(ContainSubstring("bridge configuration is not supported when netFilter is specified")))
	g.Expect(ok).To(Equal(false))
}

func TestStaticValidateSriovNetworkNodePolicyWithNetFilterAndInvalidLinkType(t *testing.T) {
	testCases := []struct {
		name        string
		linkType    string
		expectError bool
	}{
		{
			name:        "netFilter with linkType ib",
			linkType:    "ib",
			expectError: true,
		},
		{
			name:        "netFilter with linkType IB",
			linkType:    "IB",
			expectError: true,
		},
		{
			name:        "netFilter with linkType eth",
			linkType:    "eth",
			expectError: false,
		},
		{
			name:        "netFilter with linkType ETH",
			linkType:    "ETH",
			expectError: false,
		},
		{
			name:        "netFilter with empty linkType",
			linkType:    "",
			expectError: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			policy := &SriovNetworkNodePolicy{
				Spec: SriovNetworkNodePolicySpec{
					DeviceType: "netdevice",
					NicSelector: SriovNetworkNicSelector{
						NetFilter: "openstack/NetworkID:ada9ec67-2c97-467c-b674-c47200e2f5da",
					},
					LinkType:     tc.linkType,
					NumVfs:       1,
					ResourceName: "p0",
				},
			}
			ok, err := staticValidateSriovNetworkNodePolicy(policy)
			if tc.expectError {
				g.Expect(err).To(MatchError(ContainSubstring("linkType")))
				g.Expect(err).To(MatchError(ContainSubstring("is not allowed when netFilter is specified")))
				g.Expect(ok).To(Equal(false))
			} else {
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(ok).To(Equal(true))
			}
		})
	}
}

func TestStaticValidateSriovNetworkNodePolicyWithNetFilterOnly(t *testing.T) {
	g := NewGomegaWithT(t)
	policy := &SriovNetworkNodePolicy{
		Spec: SriovNetworkNodePolicySpec{
			DeviceType: "netdevice",
			NicSelector: SriovNetworkNicSelector{
				NetFilter: "openstack/NetworkID:ada9ec67-2c97-467c-b674-c47200e2f5da",
			},
			NumVfs:       1,
			ResourceName: "p0",
		},
	}
	ok, err := staticValidateSriovNetworkNodePolicy(policy)
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(ok).To(Equal(true))
}

func TestValidatePolicyForNodeStateWithValidVFAndNetFilter(t *testing.T) {
	interfaceSelected = false
	state := &SriovNetworkNodeState{
		Spec: SriovNetworkNodeStateSpec{
			Interfaces: []Interface{
				{
					Name:       "ens803f1",
					NumVfs:     1,
					PciAddress: "0000:86:00.1",
					VfGroups: []VfGroup{
						{
							DeviceType:   "netdevice",
							ResourceName: "nic1",
						},
					},
				},
			},
		},
		Status: SriovNetworkNodeStateStatus{
			Interfaces: []InterfaceExt{
				{
					VFs: []VirtualFunction{
						{
							DeviceID:   "158b",
							Driver:     "iavf",
							PciAddress: "0000:86:00.1",
							Mtu:        1500,
							VfID:       0,
						},
					},
					DeviceID:   "158b",
					Driver:     "i40e",
					Mtu:        1500,
					Name:       "ens803f0",
					PciAddress: "0000:86:00.0",
					Vendor:     "8086",
					NumVfs:     1,
					TotalVfs:   64,
					NetFilter:  "openstack/NetworkID:e48c7670-bcb4-4f9c-8038-012b6571501d",
				},
			},
		},
	}
	policy := &SriovNetworkNodePolicy{
		Spec: SriovNetworkNodePolicySpec{
			DeviceType: "netdevice",
			NicSelector: SriovNetworkNicSelector{
				PfNames:   []string{"ens803f0"},
				NetFilter: "openstack/NetworkID:e48c7670-bcb4-4f9c-8038-012b6571501d",
			},
			NodeSelector: map[string]string{
				"feature.node.kubernetes.io/network-sriov.capable": "true",
			},
			NumVfs:       1,
			Priority:     99,
			ResourceName: "p0",
		},
	}
	g := NewGomegaWithT(t)
	_, err := validatePolicyForNodeState(policy, state, NewNode())
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(interfaceSelected).To(Equal(true))
}

func TestValidatePolicyForNodeStateWithExternallyManageAndSwitchdev(t *testing.T) {
	state := newNodeState()
	policy := &SriovNetworkNodePolicy{
		Spec: SriovNetworkNodePolicySpec{
			DeviceType: "netdevice",
			NicSelector: SriovNetworkNicSelector{
				PfNames:     []string{"ens803f0"},
				RootDevices: []string{"0000:86:00.0"},
				Vendor:      "8086",
			},
			NodeSelector: map[string]string{
				"feature.node.kubernetes.io/network-sriov.capable": "true",
			},
			NumVfs:            4,
			ResourceName:      "p0",
			EswitchMode:       "switchdev",
			ExternallyManaged: true,
		},
	}
	g := NewGomegaWithT(t)
	_, err := validatePolicyForNodeState(policy, state, NewNode())
	g.Expect(err).NotTo(HaveOccurred())
}

func TestValidatePolicyForNodeStateWithExternallyManageAndSwitchdevAndWrongVFCount(t *testing.T) {
	state := newNodeState()
	policy := &SriovNetworkNodePolicy{
		Spec: SriovNetworkNodePolicySpec{
			DeviceType: "netdevice",
			NicSelector: SriovNetworkNicSelector{
				PfNames:     []string{"ens803f0"},
				RootDevices: []string{"0000:86:00.0"},
				Vendor:      "8086",
			},
			NodeSelector: map[string]string{
				"feature.node.kubernetes.io/network-sriov.capable": "true",
			},
			NumVfs:            30,
			ResourceName:      "p0",
			EswitchMode:       "switchdev",
			ExternallyManaged: true,
		},
	}
	g := NewGomegaWithT(t)
	_, err := validatePolicyForNodeState(policy, state, NewNode())
	g.Expect(err).To(MatchError(ContainSubstring("is higher than the virtual functions allocated for the PF externally value")))
}

func TestValidatePolicyForNodePolicyAllowSwitchdevWithExternallyManage(t *testing.T) {
	appliedPolicy := newNodePolicy()
	appliedPolicy.Spec.ExternallyManaged = true

	policy := &SriovNetworkNodePolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name: "p0",
		},
		Spec: SriovNetworkNodePolicySpec{
			DeviceType: "netdevice",
			NicSelector: SriovNetworkNicSelector{
				PfNames: []string{"ens803f1#3-4"},
				Vendor:  "8086",
			},
			NodeSelector: map[string]string{
				"feature.node.kubernetes.io/network-sriov.capable": "true",
			},
			NumVfs:            63,
			Priority:          99,
			ResourceName:      "p0",
			ExternallyManaged: true,
			EswitchMode:       ESwithModeSwitchDev,
		},
	}
	g := NewGomegaWithT(t)
	err := validatePolicyForNodePolicy(policy, appliedPolicy, nil)
	g.Expect(err).NotTo(HaveOccurred())
}

func TestValidatePolicyForNodeStateWithPfNameMatchingInterfaceName(t *testing.T) {
	interfaceSelected = false
	state := newNodeState()
	policy := &SriovNetworkNodePolicy{
		Spec: SriovNetworkNodePolicySpec{
			DeviceType: "netdevice",
			NicSelector: SriovNetworkNicSelector{
				PfNames: []string{"ens803f1"},
				Vendor:  "8086",
			},
			NodeSelector: map[string]string{
				"feature.node.kubernetes.io/network-sriov.capable": "true",
			},
			NumVfs:       4,
			Priority:     99,
			ResourceName: "p0",
		},
	}
	g := NewGomegaWithT(t)
	_, err := validatePolicyForNodeState(policy, state, NewNode())
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(interfaceSelected).To(Equal(true))
}

func TestValidatePolicyForNodeStateWithPfNameMatchingAlternativeName(t *testing.T) {
	interfaceSelected = false
	state := newNodeState()
	// Add alternative names to the interface
	state.Status.Interfaces[1].AltNames = []string{"eth0", "net1", "oldname"}

	policy := &SriovNetworkNodePolicy{
		Spec: SriovNetworkNodePolicySpec{
			DeviceType: "netdevice",
			NicSelector: SriovNetworkNicSelector{
				PfNames: []string{"eth0"}, // matches alternative name, not primary name
				Vendor:  "8086",
			},
			NodeSelector: map[string]string{
				"feature.node.kubernetes.io/network-sriov.capable": "true",
			},
			NumVfs:       4,
			Priority:     99,
			ResourceName: "p0",
		},
	}
	g := NewGomegaWithT(t)
	_, err := validatePolicyForNodeState(policy, state, NewNode())
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(interfaceSelected).To(Equal(true))
}

func TestValidatePolicyForNodeStateWithPfNameNotMatchingEitherNameOrAltNames(t *testing.T) {
	interfaceSelected = false
	state := newNodeState()
	// Add alternative names but none match
	state.Status.Interfaces[1].AltNames = []string{"eth0", "net1", "oldname"}

	policy := &SriovNetworkNodePolicy{
		Spec: SriovNetworkNodePolicySpec{
			DeviceType: "netdevice",
			NicSelector: SriovNetworkNicSelector{
				PfNames: []string{"nonexistent"}, // doesn't match name or alt names
				Vendor:  "8086",
			},
			NodeSelector: map[string]string{
				"feature.node.kubernetes.io/network-sriov.capable": "true",
			},
			NumVfs:       4,
			Priority:     99,
			ResourceName: "p0",
		},
	}
	g := NewGomegaWithT(t)
	_, err := validatePolicyForNodeState(policy, state, NewNode())
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(interfaceSelected).To(Equal(false))
}

func TestValidatePolicyForNodeStateWithEmptyAlternativeNames(t *testing.T) {
	interfaceSelected = false
	state := newNodeState()
	// Explicitly set empty alternative names
	state.Status.Interfaces[1].AltNames = []string{}

	policy := &SriovNetworkNodePolicy{
		Spec: SriovNetworkNodePolicySpec{
			DeviceType: "netdevice",
			NicSelector: SriovNetworkNicSelector{
				PfNames: []string{"ens803f1"}, // matches primary name
				Vendor:  "8086",
			},
			NodeSelector: map[string]string{
				"feature.node.kubernetes.io/network-sriov.capable": "true",
			},
			NumVfs:       4,
			Priority:     99,
			ResourceName: "p0",
		},
	}
	g := NewGomegaWithT(t)
	_, err := validatePolicyForNodeState(policy, state, NewNode())
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(interfaceSelected).To(Equal(true))
}

func TestValidatePolicyForNodeStateWithNilAlternativeNames(t *testing.T) {
	interfaceSelected = false
	state := newNodeState()
	// Alternative names are nil by default in newNodeState()

	policy := &SriovNetworkNodePolicy{
		Spec: SriovNetworkNodePolicySpec{
			DeviceType: "netdevice",
			NicSelector: SriovNetworkNicSelector{
				PfNames: []string{"ens803f1"}, // matches primary name
				Vendor:  "8086",
			},
			NodeSelector: map[string]string{
				"feature.node.kubernetes.io/network-sriov.capable": "true",
			},
			NumVfs:       4,
			Priority:     99,
			ResourceName: "p0",
		},
	}
	g := NewGomegaWithT(t)
	_, err := validatePolicyForNodeState(policy, state, NewNode())
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(interfaceSelected).To(Equal(true))
}

func TestValidatePolicyForNodeStateWithMultipleAlternativeNamesOneMatches(t *testing.T) {
	interfaceSelected = false
	state := newNodeState()
	// Add multiple alternative names
	state.Status.Interfaces[1].AltNames = []string{"eth0", "net1", "oldname", "legacy"}

	policy := &SriovNetworkNodePolicy{
		Spec: SriovNetworkNodePolicySpec{
			DeviceType: "netdevice",
			NicSelector: SriovNetworkNicSelector{
				PfNames: []string{"legacy"}, // matches one of the alt names
				Vendor:  "8086",
			},
			NodeSelector: map[string]string{
				"feature.node.kubernetes.io/network-sriov.capable": "true",
			},
			NumVfs:       4,
			Priority:     99,
			ResourceName: "p0",
		},
	}
	g := NewGomegaWithT(t)
	_, err := validatePolicyForNodeState(policy, state, NewNode())
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(interfaceSelected).To(Equal(true))
}

func TestValidatePolicyForNodeStateWithPfNameAndVfRangeMatchingAlternativeName(t *testing.T) {
	interfaceSelected = false
	state := newNodeState()
	// Add alternative names to the interface
	state.Status.Interfaces[1].AltNames = []string{"eth0", "net1"}

	policy := &SriovNetworkNodePolicy{
		Spec: SriovNetworkNodePolicySpec{
			DeviceType: "netdevice",
			NicSelector: SriovNetworkNicSelector{
				PfNames: []string{"eth0#0-1"}, // matches alternative name with VF range
				Vendor:  "8086",
			},
			NodeSelector: map[string]string{
				"feature.node.kubernetes.io/network-sriov.capable": "true",
			},
			NumVfs:       4,
			Priority:     99,
			ResourceName: "p0",
		},
	}
	g := NewGomegaWithT(t)
	_, err := validatePolicyForNodeState(policy, state, NewNode())
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(interfaceSelected).To(Equal(true))
}

func TestValidatePolicyForNodePolicyWithAltNameConflict(t *testing.T) {
	// Test that policies using different names (actual vs altName) for the same interface
	// are properly detected as conflicting when nodeState is available
	current := &SriovNetworkNodePolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name: "policy1",
		},
		Spec: SriovNetworkNodePolicySpec{
			DeviceType: "netdevice",
			NicSelector: SriovNetworkNicSelector{
				PfNames: []string{"eno2np1"}, // actual interface name
			},
			NodeSelector: map[string]string{
				"kubernetes.io/hostname": "node1",
			},
			NumVfs:       2,
			ResourceName: "mlx_sriov1",
		},
	}

	previous := &SriovNetworkNodePolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name: "policy2",
		},
		Spec: SriovNetworkNodePolicySpec{
			DeviceType: "netdevice",
			NicSelector: SriovNetworkNicSelector{
				PfNames: []string{"sriov1"}, // alternative name for same interface
			},
			NodeSelector: map[string]string{
				"kubernetes.io/hostname": "node1",
			},
			NumVfs:       2,
			ResourceName: "mlx_sriov1",
		},
	}

	// Create a mock node state with altNames
	nodeState := &SriovNetworkNodeState{
		Status: SriovNetworkNodeStateStatus{
			Interfaces: []InterfaceExt{
				{
					Name:       "eno2np1",
					AltNames:   []string{"sriov1", "enp25s0f1np1"},
					PciAddress: "0000:19:00.1",
					NumVfs:     2,
					TotalVfs:   2,
				},
			},
		},
	}

	g := NewGomegaWithT(t)
	// With nodeState provided, this should detect the conflict
	err := validatePolicyForNodePolicy(current, previous, nodeState)
	g.Expect(err).To(HaveOccurred())
	g.Expect(err.Error()).To(ContainSubstring("VF index range in eno2np1 is overlapped"))

	// Without nodeState, this should not detect the conflict (old behavior)
	err = validatePolicyForNodePolicy(current, previous, nil)
	g.Expect(err).NotTo(HaveOccurred())
}

var _ = Describe("policy admission during provider deployment", func() {
	DescribeTable("uses the feature gate without requiring provider status or deployed DaemonSets", func(draEnabled, noConfig bool) {
		config, policy := setupDRANetFilterValidation()
		config.Spec.FeatureGates[constants.DynamicResourceAllocationFeatureGate] = draEnabled
		config.Status.Conditions = []metav1.Condition{{Type: ConditionReady, Status: metav1.ConditionFalse, Reason: "OperatorConfigSyncFailed"}}
		policy.Spec.NicSelector = SriovNetworkNicSelector{PfNames: []string{"ens803f1"}}
		builder := fake.NewClientBuilder().WithScheme(vars.Scheme)
		if !noConfig {
			builder.WithObjects(config)
		}
		client = builder.Build()
		err := validateDRAResourcePolicies(policy, &SriovNetworkNodePolicyList{})
		if noConfig {
			Expect(err).To(MatchError(ContainSubstring("not found")))
		} else {
			Expect(err).NotTo(HaveOccurred())
		}
	},
		Entry("DRA", true, false),
		Entry("device plugin", false, false),
		Entry("missing operator config", false, true),
	)
})

var _ = Describe("policy deletion during a provider switch", func() {
	It("validates the expected behavior", func() {
		for _, name := range []string{"pending switch", "missing config"} {
			By(name)
			config, policy := setupDRANetFilterValidation()
			builder := fake.NewClientBuilder().WithScheme(vars.Scheme)
			if name == "pending switch" {
				builder.WithObjects(config)
			}
			client = builder.Build()
			allowed, _, err := validateSriovNetworkNodePolicy(policy, admissionv1.Delete)
			Expect(err).NotTo(HaveOccurred())
			Expect(allowed).To(BeTrue())
		}
	})
})

var _ = Describe("DRA resource drivers", func() {
	DescribeTable("validates allocation driver compatibility", func(prepare func(*SriovOperatorConfig, *SriovNetworkNodePolicy, *SriovNetworkNodePolicy), wantError bool) {
		config, policy := setupDRANetFilterValidation()
		policy.Spec.DeviceType = constants.DeviceTypeVfioPci
		policy.Spec.ResourceName = "shared"
		policy.Spec.NicSelector = SriovNetworkNicSelector{PfNames: []string{"ens803f1"}}
		policy.Spec.NodeSelector = map[string]string{"kubernetes.io/hostname": "worker-a"}
		other := &SriovNetworkNodePolicy{
			ObjectMeta: metav1.ObjectMeta{Name: "other-policy", Namespace: vars.Namespace},
			Spec: SriovNetworkNodePolicySpec{ResourceName: "shared", DeviceType: constants.DeviceTypeNetDevice,
				NodeSelector: map[string]string{"kubernetes.io/hostname": "worker-b"}},
		}
		if prepare != nil {
			prepare(config, policy, other)
		}
		client = fake.NewClientBuilder().WithScheme(vars.Scheme).WithObjects(config).Build()
		err := validateDRAResourcePolicies(policy, &SriovNetworkNodePolicyList{Items: []SriovNetworkNodePolicy{*other}})
		if wantError {
			Expect(err).To(MatchError(ContainSubstring("incompatible deviceType")))
		} else {
			Expect(err).NotTo(HaveOccurred())
		}
	},
		Entry("reject mixed drivers across nodes", nil, true),
		Entry("allow shared VFIO", func(_ *SriovOperatorConfig, _, other *SriovNetworkNodePolicy) {
			other.Spec.DeviceType = constants.DeviceTypeVfioPci
		}, false),
		Entry("allow updating own device type", func(_ *SriovOperatorConfig, p, other *SriovNetworkNodePolicy) {
			other.Name = p.Name
		}, false),
		Entry("ignore other resources", func(_ *SriovOperatorConfig, _, other *SriovNetworkNodePolicy) {
			other.Spec.ResourceName = "other"
		}, false),
		Entry("ignore other namespace", func(_ *SriovOperatorConfig, _, other *SriovNetworkNodePolicy) {
			other.Namespace = "other"
		}, false),
		Entry("ignore deprecated default with incompatible driver", func(_ *SriovOperatorConfig, _, other *SriovNetworkNodePolicy) {
			other.Name = constants.DefaultPolicyName
		}, false),
		Entry("ignore deprecated default with empty resource", func(_ *SriovOperatorConfig, _, other *SriovNetworkNodePolicy) {
			other.Name = constants.DefaultPolicyName
			other.Spec.ResourceName = ""
		}, false),
		Entry("ignore deprecated default in device plugin mode", func(c *SriovOperatorConfig, _, other *SriovNetworkNodePolicy) {
			c.Spec.FeatureGates = nil
			other.Name = constants.DefaultPolicyName
			other.Spec.ResourceName = ""
		}, false),
		Entry("ignore unused candidate", func(_ *SriovOperatorConfig, p, _ *SriovNetworkNodePolicy) {
			p.Namespace = "other"
		}, false),
		Entry("allow device plugin mode", func(c *SriovOperatorConfig, _, _ *SriovNetworkNodePolicy) {
			c.Spec.FeatureGates = nil
		}, false),
	)
})

func setupDRANetFilterValidation(test ...*testing.T) (*SriovOperatorConfig, *SriovNetworkNodePolicy) {
	previousClient, previousNamespace := client, vars.Namespace
	cleanup := func() {
		client, vars.Namespace = previousClient, previousNamespace
	}
	if len(test) > 0 {
		test[0].Helper()
		test[0].Cleanup(cleanup)
	} else {
		DeferCleanup(cleanup)
	}
	vars.Namespace = "sriov-network-operator"
	config := newDefaultOperatorConfig()
	config.Spec.DisableDrain = false
	config.Spec.FeatureGates = map[string]bool{constants.DynamicResourceAllocationFeatureGate: true}
	policy := newNodePolicy()
	policy.Namespace = vars.Namespace
	policy.Spec.NicSelector = SriovNetworkNicSelector{
		NetFilter: "openstack/NetworkID:ada9ec67-2c97-467c-b674-c47200e2f5da",
	}
	return config, policy
}

var _ = Describe("DRA mode changes", func() {
	DescribeTable("validates provider changes against existing policies", func(operation admissionv1.Operation, prepare func(*SriovOperatorConfig, *SriovNetworkNodePolicy), noPolicy bool, alreadyEnabled bool, noConfig bool, wantError bool) {
		config, policy := setupDRANetFilterValidation()
		if prepare != nil {
			prepare(config, policy)
		}
		builder := fake.NewClientBuilder().WithScheme(vars.Scheme)
		if operation == admissionv1.Update && !noConfig {
			previousConfig := config.DeepCopy()
			previousConfig.Spec.FeatureGates = map[string]bool{constants.DynamicResourceAllocationFeatureGate: alreadyEnabled}
			builder.WithObjects(previousConfig)
		}
		if !noPolicy {
			builder.WithObjects(policy)
		}
		client = builder.Build()
		allowed, _, err := validateSriovOperatorConfig(config, operation)
		Expect(allowed).To(Equal(!wantError))
		if wantError {
			Expect(err).To(MatchError(ContainSubstring("SriovNetworkNodePolicy " + policy.Namespace + "/" + policy.Name + " exists")))
		} else {
			Expect(err).NotTo(HaveOccurred())
		}
	},
		Entry("reject enabling DRA", admissionv1.Update, nil, false, false, false, true),
		Entry("reject enabling with missing config", admissionv1.Update, nil, false, false, true, true),
		Entry("reject creating enabled config", admissionv1.Create, nil, false, false, false, true),
		Entry("allow updates with DRA already enabled", admissionv1.Update, nil, false, true, false, false),
		Entry("allow deleting config", admissionv1.Delete, nil, false, false, false, false),
		Entry("reject disabling DRA", admissionv1.Update, func(c *SriovOperatorConfig, _ *SriovNetworkNodePolicy) {
			c.Spec.FeatureGates[constants.DynamicResourceAllocationFeatureGate] = false
		}, false, true, false, true),
		Entry("reject removing DRA gate", admissionv1.Update, func(c *SriovOperatorConfig, _ *SriovNetworkNodePolicy) {
			delete(c.Spec.FeatureGates, constants.DynamicResourceAllocationFeatureGate)
		}, false, true, false, true),
		Entry("reject removing feature gates map", admissionv1.Update, func(c *SriovOperatorConfig, _ *SriovNetworkNodePolicy) {
			c.Spec.FeatureGates = nil
		}, false, true, false, true),
		Entry("allow disabling without policies", admissionv1.Update, func(c *SriovOperatorConfig, _ *SriovNetworkNodePolicy) {
			c.Spec.FeatureGates[constants.DynamicResourceAllocationFeatureGate] = false
		}, true, true, false, false),
		Entry("allow removing gate without policies", admissionv1.Update, func(c *SriovOperatorConfig, _ *SriovNetworkNodePolicy) {
			c.Spec.FeatureGates = nil
		}, true, true, false, false),
		Entry("allow updates with DRA already disabled", admissionv1.Update, func(c *SriovOperatorConfig, _ *SriovNetworkNodePolicy) {
			c.Spec.FeatureGates[constants.DynamicResourceAllocationFeatureGate] = false
		}, false, false, false, false),
		Entry("allow creating disabled config", admissionv1.Create, func(c *SriovOperatorConfig, _ *SriovNetworkNodePolicy) {
			c.Spec.FeatureGates = nil
		}, false, false, false, false),
		Entry("allow omitted gate", admissionv1.Update, func(c *SriovOperatorConfig, _ *SriovNetworkNodePolicy) {
			c.Spec.FeatureGates = nil
		}, false, false, false, false),
		Entry("allow no policies", admissionv1.Update, nil, true, false, false, false),
		Entry("allow creating enabled config without policies", admissionv1.Create, nil, true, false, false, false),
		Entry("reject enabling even with supported policy", admissionv1.Update, func(_ *SriovOperatorConfig, p *SriovNetworkNodePolicy) {
			p.Spec.NicSelector = SriovNetworkNicSelector{PfNames: []string{"ens803f1"}}
		}, false, false, false, true),
		Entry("allow updates with existing supported DRA policy", admissionv1.Update, func(_ *SriovOperatorConfig, p *SriovNetworkNodePolicy) {
			p.Spec.NicSelector = SriovNetworkNicSelector{PfNames: []string{"ens803f1"}}
		}, false, true, false, false),
		Entry("ignore other namespace", admissionv1.Update, func(_ *SriovOperatorConfig, p *SriovNetworkNodePolicy) {
			p.Namespace = "other-namespace"
		}, false, false, false, false),
		Entry("ignore other namespace when disabling", admissionv1.Update, func(c *SriovOperatorConfig, p *SriovNetworkNodePolicy) {
			c.Spec.FeatureGates = nil
			p.Namespace = "other-namespace"
		}, false, true, false, false),
	)
})

var _ = Describe("DRA disablement read errors", func() {
	It("validates the expected behavior", func() {
		for _, failGet := range []bool{true, false} {
			By(fmt.Sprintf("config lookup fails=%t", failGet))
			config, _ := setupDRANetFilterValidation()
			previousConfig := config.DeepCopy()
			config.Spec.FeatureGates = nil
			readError := fmt.Errorf("API unavailable")
			listError := fmt.Errorf("policy list unavailable")
			client = fake.NewClientBuilder().WithScheme(vars.Scheme).WithObjects(previousConfig).WithInterceptorFuncs(interceptor.Funcs{
				Get: func(ctx context.Context, c runtimeclient.WithWatch, key runtimeclient.ObjectKey, obj runtimeclient.Object, opts ...runtimeclient.GetOption) error {
					if failGet {
						return readError
					}
					return c.Get(ctx, key, obj, opts...)
				},
				List: func(context.Context, runtimeclient.WithWatch, runtimeclient.ObjectList, ...runtimeclient.ListOption) error {
					if failGet {
						Fail("List must not be called after Get fails")
					}
					return listError
				},
			}).Build()
			allowed, _, err := validateSriovOperatorConfig(config, admissionv1.Update)
			Expect(allowed).To(BeFalse())
			if failGet {
				Expect(err).To(MatchError(readError))
			} else {
				Expect(err).To(MatchError(listError))
			}
		}
	})
})

var _ = Describe("DRA policy net filters", func() {
	DescribeTable("validates net filters for the selected provider", func(prepare func(*SriovOperatorConfig, *SriovNetworkNodePolicy), noConfig bool) {
		config, policy := setupDRANetFilterValidation()
		if prepare != nil {
			prepare(config, policy)
		}
		builder := fake.NewClientBuilder().WithScheme(vars.Scheme)
		if !noConfig {
			builder.WithObjects(config)
		}
		client = builder.Build()
		err := validateDRAResourcePolicies(policy, &SriovNetworkNodePolicyList{})
		if noConfig {
			Expect(err).To(MatchError(ContainSubstring("not found")))
		} else {
			Expect(err).NotTo(HaveOccurred())
		}
	},
		Entry("DRA disabled", func(c *SriovOperatorConfig, _ *SriovNetworkNodePolicy) {
			c.Spec.FeatureGates[constants.DynamicResourceAllocationFeatureGate] = false
		}, false),
		Entry("gate omitted", func(c *SriovOperatorConfig, _ *SriovNetworkNodePolicy) {
			c.Spec.FeatureGates = nil
		}, false),
		Entry("config missing", nil, true),
		Entry("netFilter removed", func(_ *SriovOperatorConfig, p *SriovNetworkNodePolicy) {
			p.Spec.NicSelector = SriovNetworkNicSelector{PfNames: []string{"ens803f1"}}
		}, false),
		Entry("other namespace", func(_ *SriovOperatorConfig, p *SriovNetworkNodePolicy) {
			p.Namespace = "other-namespace"
		}, false),
	)
})

var _ = Describe("DRA policy admission", func() {
	It("validates the expected behavior", func() {
		for _, operation := range []admissionv1.Operation{admissionv1.Create, admissionv1.Update, admissionv1.Delete} {
			By(string(operation))
			config, policy := setupDRANetFilterValidation()
			client = fake.NewClientBuilder().WithScheme(vars.Scheme).WithObjects(config).Build()
			allowed, _, err := validateSriovNetworkNodePolicy(policy, operation)
			if operation == admissionv1.Delete {
				Expect(err).NotTo(HaveOccurred())
				Expect(allowed).To(BeTrue())
			} else {
				Expect(allowed).To(BeFalse())
				Expect(err).To(MatchError(ContainSubstring("netFilter selector is not supported in DRA mode")))
			}
		}
	})
})

var _ = Describe("DRA net-filter read errors", func() {
	It("validates the expected behavior", func() {
		config, policy := setupDRANetFilterValidation()
		readError := fmt.Errorf("API unavailable")
		client = fake.NewClientBuilder().WithScheme(vars.Scheme).WithInterceptorFuncs(interceptor.Funcs{
			Get: func(context.Context, runtimeclient.WithWatch, runtimeclient.ObjectKey, runtimeclient.Object, ...runtimeclient.GetOption) error {
				return readError
			},
			List: func(context.Context, runtimeclient.WithWatch, runtimeclient.ObjectList, ...runtimeclient.ListOption) error {
				return readError
			},
		}).Build()
		allowed, _, err := validateSriovOperatorConfig(config, admissionv1.Update)
		Expect(allowed).To(BeFalse())
		Expect(err).To(MatchError(ContainSubstring(readError.Error())))
		// CREATE skips the previous-config lookup, so this exercises a failed policy list.
		allowed, _, err = validateSriovOperatorConfig(config, admissionv1.Create)
		Expect(allowed).To(BeFalse())
		Expect(err).To(MatchError(ContainSubstring(readError.Error())))
		allowed, _, err = validateSriovNetworkNodePolicy(policy, admissionv1.Create)
		Expect(allowed).To(BeFalse())
		Expect(err).To(MatchError(ContainSubstring(readError.Error())))
	})
})

var _ = Describe("policy namespace admission", func() {
	It("validates the expected behavior", func() {
		previousNamespace := vars.Namespace
		vars.Namespace = "operator"
		DeferCleanup(func() { vars.Namespace = previousNamespace })
		for _, operation := range []admissionv1.Operation{admissionv1.Create, admissionv1.Update, admissionv1.Delete} {
			By(string(operation))
			policy := &SriovNetworkNodePolicy{ObjectMeta: metav1.ObjectMeta{Name: "misplaced", Namespace: "other"}}
			admitted, _, err := validateSriovNetworkNodePolicy(policy, operation)
			if operation == admissionv1.Delete {
				Expect(err).NotTo(HaveOccurred())
				Expect(admitted).To(BeTrue())
				continue
			}
			Expect(admitted).To(BeFalse())
			Expect(err).To(MatchError(ContainSubstring("operator namespace")))
		}
	})
})

var _ = Describe("DRA policy VF ranges", func() {
	DescribeTable("validates VF ranges for the selected provider", func(pfNames []string, draEnabled bool, wantError bool) {
		config, policy := setupDRANetFilterValidation()
		config.Spec.FeatureGates[constants.DynamicResourceAllocationFeatureGate] = draEnabled
		policy.Spec.NicSelector = SriovNetworkNicSelector{PfNames: pfNames}
		policy.Spec.NumVfs = 8
		client = fake.NewClientBuilder().WithScheme(vars.Scheme).WithObjects(config).Build()
		err := validateDRAResourcePolicies(policy, &SriovNetworkNodePolicyList{})
		if wantError {
			Expect(err).To(MatchError(ContainSubstring("VF range")))
			Expect(err.Error()).To(ContainSubstring(policy.Name))
		} else {
			Expect(err).NotTo(HaveOccurred())
		}
	},
		Entry("DRA whole PF", []string{"ens1f0"}, true, false),
		Entry("DRA VF range", []string{"ens1f0#2-5"}, true, true),
		Entry("DRA single VF", []string{"ens1f0#2-2"}, true, true),
		Entry("DRA mixed selectors", []string{"ens1f0", "enp3s0np0#0-3"}, true, true),
		Entry("device plugin VF range", []string{"ens1f0#2-5"}, false, false),
	)
})
