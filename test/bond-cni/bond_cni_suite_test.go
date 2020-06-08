package bond_cni_test

import (
	"testing"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	_ "github.com/openshift/sriov-network-operator/test/conformance/tests"
)

func TestBondCni(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "BondCni Suite")
}
