package webhook

import (
	"context"
	"fmt"
	"os"
	"testing"

	corev1 "k8s.io/api/core/v1"

	. "github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"sigs.k8s.io/controller-runtime/pkg/envtest"

	. "github.com/k8snetworkplumbingwg/sriov-network-operator/api/v1"
	constants "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/consts"

	fakesnclientset "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/client/clientset/versioned/fake"
)

func TestMain(m *testing.M) {
	NicIDMap = []string{
		"8086 158a 154c", // I40e XXV710
		"8086 158b 154c", // I40e 25G SFP28
		"8086 1572 154c", // I40e 10G X710 SFP+
		"8086 0d58 154c", // I40e XXV710 N3000
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
		"14e4 16d7 16dc", // BCM57414 2x25G
		"14e4 1750 1806", // BCM75508 2x100G
	}
	os.Exit(m.Run())
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
			Name: "default",
		},
		Spec: SriovOperatorConfigSpec{
			ConfigDaemonNodeSelector: map[string]string{},
			DisableDrain:             true,
			EnableInjector:           func() *bool { b := true; return &b }(),
			EnableOperatorWebhook:    func() *bool { b := true; return &b }(),
			LogLevel:                 2,
		},
	}
}

func TestValidateSriovOperatorConfigWithDefaultOperatorConfig(t *testing.T) {
	g := NewGomegaWithT(t)

	config := newDefaultOperatorConfig()
	snclient = fakesnclientset.NewSimpleClientset()

	ok, _, err := validateSriovOperatorConfig(config, "DELETE")
	g.Expect(err).To(HaveOccurred())
	g.Expect(ok).To(Equal(false))

	ok, _, err = validateSriovOperatorConfig(config, "UPDATE")
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(ok).To(Equal(true))

	ok, w, err := validateSriovOperatorConfig(config, "UPDATE")
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

	snclient = fakesnclientset.NewSimpleClientset(
		config,
		nodeState,
	)

	config.Spec.DisableDrain = true
	ok, _, err := validateSriovOperatorConfig(config, "UPDATE")
	g.Expect(err).To(MatchError("can't set Spec.DisableDrain = true while node[worker-1] is updating"))
	g.Expect(ok).To(Equal(false))

	// Simulate node update finished
	nodeState.Status.SyncStatus = "Succeeded"
	snclient.SriovnetworkV1().SriovNetworkNodeStates(namespace).
		Update(context.Background(), nodeState, metav1.UpdateOptions{})

	ok, _, err = validateSriovOperatorConfig(config, "UPDATE")
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(ok).To(Equal(true))
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
	g := NewGomegaWithT(t)
	ok, _, err = validateSriovNetworkNodePolicy(policy, "DELETE")
	g.Expect(err).To(HaveOccurred())
	g.Expect(ok).To(Equal(false))

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
	err := validatePolicyForNodePolicy(policy, appliedPolicy)
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
	err := validatePolicyForNodePolicy(policy, appliedPolicy)
	g.Expect(err).To(MatchError(ContainSubstring(fmt.Sprintf("externallyManage is inconsistent with existing policy %s", appliedPolicy.ObjectMeta.Name))))
}

func TestValidatePolicyForNodePolicyWithExternallyManageConflictWithSwitchDev(t *testing.T) {
	appliedPolicy := newNodePolicy()
	appliedPolicy.Spec.EswitchMode = ESwithModeSwitchDev

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
	err := validatePolicyForNodePolicy(policy, appliedPolicy)
	g.Expect(err).To(HaveOccurred())
}

func TestValidatePolicyForNodePolicyWithSwitchDevConflictWithExternallyManage(t *testing.T) {
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
			NumVfs:       63,
			Priority:     99,
			ResourceName: "p0",
			EswitchMode:  ESwithModeSwitchDev,
		},
	}
	g := NewGomegaWithT(t)
	err := validatePolicyForNodePolicy(policy, appliedPolicy)
	g.Expect(err).To(HaveOccurred())
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
	err := validatePolicyForNodePolicy(policy, appliedPolicy)
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
	err := validatePolicyForNodePolicy(policy, appliedPolicy)
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

	err := validatePolicyForNodePolicy(current, previous)

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

	err := validatePolicyForNodePolicy(current, previous)

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

	err := validatePolicyForNodePolicy(current, previous)

	g := NewGomegaWithT(t)
	g.Expect(err).NotTo(HaveOccurred())
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

func TestStaticValidateSriovNetworkNodePolicyWithExternallyCreatedAndSwitchDev(t *testing.T) {
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
			NumVfs:            63,
			Priority:          99,
			ResourceName:      "p0",
			EswitchMode:       "switchdev",
			ExternallyManaged: true,
		},
	}
	g := NewGomegaWithT(t)
	ok, err := staticValidateSriovNetworkNodePolicy(policy)
	g.Expect(err).To(HaveOccurred())
	g.Expect(ok).To(BeFalse())
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
	var testEnv = &envtest.Environment{}

	cfg, err := testEnv.Start()
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(cfg).ToNot(BeNil())
	kubeclient = kubernetes.NewForConfigOrDie(cfg)
	_, err = validatePolicyForNodeState(policy, state, NewNode())
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
							DeviceID:   "154c",
							Driver:     "iavf",
							PciAddress: "0000:86:00.1",
							Mtu:        1500,
							VfID:       0,
						},
					},
					DeviceID:   "154c",
					Driver:     "iavf",
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
