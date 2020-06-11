package webhook

import (
	"fmt"
	"testing"

	. "github.com/onsi/gomega"
	. "github.com/openshift/sriov-network-operator/pkg/apis/sriovnetwork/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

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
				},
			},
		},
	}
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
	ok, err := validatePolicyForNodeState(policy, state)
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(ok).To(Equal(true))
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
	ok, err := validatePolicyForNodeState(policy, state)
	g.Expect(err).To(MatchError(ContainSubstring(fmt.Sprintf("numVfs(%d) in CR %s exceed the maximum allowed value(%d)", policy.Spec.NumVfs, policy.GetName(), state.Status.Interfaces[0].TotalVfs))))
	g.Expect(ok).To(Equal(false))
}

func TestValidatePolicyForNodeStateWithOverlappedVfRange(t *testing.T) {
	state := newNodeState()
	policy := &SriovNetworkNodePolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name: "p0",
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
			ResourceName: "p0",
		},
	}
	state.Spec.Interfaces[0].VfGroups[0].PolicyName = "p1"
	g := NewGomegaWithT(t)
	ok, err := validatePolicyForNodeState(policy, state)
	g.Expect(err).To(MatchError(ContainSubstring(fmt.Sprintf("Vf index range in %s is overlapped with existing policies", policy.Spec.NicSelector.PfNames[0]))))
	g.Expect(ok).To(Equal(false))
}

func TestValidatePolicyForNodeStateWithUpdatedExistingVfRange(t *testing.T) {
	state := newNodeState()
	policy := &SriovNetworkNodePolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name: "p0",
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
			ResourceName: "p0",
		},
	}
	state.Spec.Interfaces[0].VfGroups[0].PolicyName = "p0"
	g := NewGomegaWithT(t)
	ok, err := validatePolicyForNodeState(policy, state)
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(ok).To(Equal(true))
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
			DeviceType: "vfio-pci",
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
	ok, err := validatePolicyForNodeState(policy, state)
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(ok).To(Equal(true))
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
	ok, err := validatePolicyForNodeState(policy, state)
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(ok).To(Equal(true))
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
	ok, err := validatePolicyForNodeState(policy, state)
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(ok).To(Equal(true))
	g.Expect(interfaceSelected).To(Equal(true))
}
