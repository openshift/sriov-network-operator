package webhook

import (
	"encoding/json"
	"testing"

	. "github.com/onsi/gomega"

	constants "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/consts"
)

func buildPolicyMap(name string, spec map[string]interface{}) map[string]interface{} {
	return map[string]interface{}{
		"metadata": map[string]interface{}{
			"name": name,
		},
		"spec": spec,
	}
}

func patchesFromResponse(g *GomegaWithT, resp []byte) []map[string]interface{} {
	var patches []map[string]interface{}
	err := json.Unmarshal(resp, &patches)
	g.Expect(err).ToNot(HaveOccurred())
	return patches
}

func hasPatch(patches []map[string]interface{}, path string, value interface{}) bool {
	for _, p := range patches {
		if p["path"] == path && p["value"] == value {
			return true
		}
	}
	return false
}

func TestMutateSriovNetworkNodePolicy_DefaultPolicy(t *testing.T) {
	g := NewGomegaWithT(t)
	cr := buildPolicyMap(constants.DefaultPolicyName, map[string]interface{}{})

	resp, err := mutateSriovNetworkNodePolicy(cr)
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(resp.Allowed).To(BeTrue())
	g.Expect(resp.Patch).To(BeNil())
}

func TestMutateSriovNetworkNodePolicy_DefaultValues(t *testing.T) {
	g := NewGomegaWithT(t)
	cr := buildPolicyMap("test-policy", map[string]interface{}{})

	resp, err := mutateSriovNetworkNodePolicy(cr)
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(resp.Allowed).To(BeTrue())

	patches := patchesFromResponse(g, resp.Patch)
	g.Expect(hasPatch(patches, "/spec/priority", float64(99))).To(BeTrue())
	g.Expect(hasPatch(patches, "/spec/isRdma", false)).To(BeTrue())
	g.Expect(hasPatch(patches, "/spec/isRdma", true)).To(BeFalse())
}

func TestMutateSriovNetworkNodePolicy_InfiniBandSetsIsRdma(t *testing.T) {
	g := NewGomegaWithT(t)
	cr := buildPolicyMap("ib-policy", map[string]interface{}{
		"linkType": constants.LinkTypeIB,
	})

	resp, err := mutateSriovNetworkNodePolicy(cr)
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(resp.Allowed).To(BeTrue())

	patches := patchesFromResponse(g, resp.Patch)
	g.Expect(hasPatch(patches, "/spec/isRdma", true)).To(BeTrue())
}

func TestMutateSriovNetworkNodePolicy_InfiniBandNetdeviceSetsIsRdma(t *testing.T) {
	g := NewGomegaWithT(t)
	cr := buildPolicyMap("ib-netdevice-policy", map[string]interface{}{
		"linkType":   constants.LinkTypeIB,
		"deviceType": constants.DeviceTypeNetDevice,
	})

	resp, err := mutateSriovNetworkNodePolicy(cr)
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(resp.Allowed).To(BeTrue())

	patches := patchesFromResponse(g, resp.Patch)
	g.Expect(hasPatch(patches, "/spec/isRdma", true)).To(BeTrue())
}

func TestMutateSriovNetworkNodePolicy_InfiniBandVfioPciDoesNotSetIsRdma(t *testing.T) {
	g := NewGomegaWithT(t)
	cr := buildPolicyMap("ib-vfio-policy", map[string]interface{}{
		"linkType":   constants.LinkTypeIB,
		"deviceType": constants.DeviceTypeVfioPci,
	})

	resp, err := mutateSriovNetworkNodePolicy(cr)
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(resp.Allowed).To(BeTrue())

	patches := patchesFromResponse(g, resp.Patch)
	g.Expect(hasPatch(patches, "/spec/isRdma", true)).To(BeFalse(),
		"isRdma should NOT be set to true when deviceType is vfio-pci")
	g.Expect(hasPatch(patches, "/spec/isRdma", false)).To(BeTrue(),
		"isRdma should default to false when deviceType is vfio-pci with IB linkType")
}

func TestMutateSriovNetworkNodePolicy_EthernetDoesNotSetIsRdma(t *testing.T) {
	g := NewGomegaWithT(t)
	cr := buildPolicyMap("eth-policy", map[string]interface{}{
		"linkType": constants.LinkTypeETH,
	})

	resp, err := mutateSriovNetworkNodePolicy(cr)
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(resp.Allowed).To(BeTrue())

	patches := patchesFromResponse(g, resp.Patch)
	g.Expect(hasPatch(patches, "/spec/isRdma", true)).To(BeFalse())
	g.Expect(hasPatch(patches, "/spec/isRdma", false)).To(BeTrue())
}

func TestMutateSriovNetworkNodePolicy_ExistingIsRdmaNotOverridden(t *testing.T) {
	g := NewGomegaWithT(t)
	cr := buildPolicyMap("existing-rdma-policy", map[string]interface{}{
		"isRdma": true,
	})

	resp, err := mutateSriovNetworkNodePolicy(cr)
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(resp.Allowed).To(BeTrue())

	patches := patchesFromResponse(g, resp.Patch)
	g.Expect(hasPatch(patches, "/spec/isRdma", false)).To(BeFalse(),
		"should not override existing isRdma value")
}

func TestMutateSriovNetworkNodePolicy_MissingSpecDoesNotPanic(t *testing.T) {
	g := NewGomegaWithT(t)
	cr := map[string]interface{}{
		"metadata": map[string]interface{}{
			"name": "no-spec-policy",
		},
	}

	resp, err := mutateSriovNetworkNodePolicy(cr)
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(resp.Allowed).To(BeTrue())
}

func TestMutateSriovNetworkNodePolicy_NilSpecDoesNotPanic(t *testing.T) {
	g := NewGomegaWithT(t)
	cr := map[string]interface{}{
		"metadata": map[string]interface{}{
			"name": "nil-spec-policy",
		},
		"spec": nil,
	}

	resp, err := mutateSriovNetworkNodePolicy(cr)
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(resp.Allowed).To(BeTrue())
}

func TestMutateSriovNetworkNodePolicy_ExistingPriorityNotOverridden(t *testing.T) {
	g := NewGomegaWithT(t)
	cr := buildPolicyMap("existing-priority-policy", map[string]interface{}{
		"priority": 10,
	})

	resp, err := mutateSriovNetworkNodePolicy(cr)
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(resp.Allowed).To(BeTrue())

	patches := patchesFromResponse(g, resp.Patch)
	g.Expect(hasPatch(patches, "/spec/priority", float64(99))).To(BeFalse(),
		"should not override existing priority value")
}
