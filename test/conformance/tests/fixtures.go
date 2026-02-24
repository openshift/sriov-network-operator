package tests

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/consts"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/test/util/clean"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/test/util/cluster"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/test/util/discovery"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/test/util/namespaces"
)

var sriovInfos *cluster.EnabledNodes
var platformType consts.PlatformTypes

var _ = BeforeSuite(func() {
	err := clean.All()
	Expect(err).NotTo(HaveOccurred())

	isSingleNode, err := cluster.IsSingleNode(clients)
	Expect(err).ToNot(HaveOccurred())
	if isSingleNode {
		disableDrainState, err := cluster.GetNodeDrainState(clients, operatorNamespace)
		Expect(err).ToNot(HaveOccurred())
		if discovery.Enabled() {
			if !disableDrainState {
				Fail("SriovOperatorConfig DisableDrain property must be enabled in a single node environment")
			}
		}
		snoTimeoutMultiplier = 1
		if !disableDrainState {
			err = cluster.SetDisableNodeDrainState(clients, operatorNamespace, true)
			Expect(err).ToNot(HaveOccurred())
			clean.RestoreNodeDrainState = true
		}
	}

	if !isFeatureFlagEnabled(consts.ResourceInjectorMatchConditionFeatureGate) {
		setFeatureFlag(consts.ResourceInjectorMatchConditionFeatureGate, true)
	}

	platformType, err = cluster.GetPlatformType(clients, operatorNamespace)
	Expect(err).ToNot(HaveOccurred())

	err = namespaces.Create(namespaces.Test, clients)
	Expect(err).ToNot(HaveOccurred())
	err = namespaces.Clean(operatorNamespace, namespaces.Test, clients, discovery.Enabled())
	Expect(err).ToNot(HaveOccurred())
	WaitForSRIOVStable()

	switch platformType {
	case consts.Baremetal:
		sriovInfos, err = cluster.DiscoverSriov(clients, operatorNamespace)
		Expect(err).ToNot(HaveOccurred())
	case consts.AWS:
		sriovInfos, err = cluster.DiscoverSriovForAws(clients, operatorNamespace)
		Expect(err).ToNot(HaveOccurred())
	case consts.VirtualOpenStack:
		Fail("VirtualOpenStack platform is not supported on e2e tests")
	default:
		Fail("Unknown platform type")
	}
})

var _ = AfterSuite(func() {
	err := clean.All()
	Expect(err).NotTo(HaveOccurred())
})
