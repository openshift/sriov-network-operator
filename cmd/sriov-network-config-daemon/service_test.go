package main

import (
	"fmt"

	"github.com/go-logr/logr"
	"github.com/spf13/cobra"
	"go.uber.org/mock/gomock"
	"gopkg.in/yaml.v3"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	sriovnetworkv1 "github.com/k8snetworkplumbingwg/sriov-network-operator/api/v1"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/helper"
	helperMock "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/helper/mock"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/platforms"
	platformsMock "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/platforms/mock"
	plugin "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/plugins"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/plugins/generic"
	pluginsMock "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/plugins/mock"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/systemd"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/test/util/fakefilesystem"
	testHelpers "github.com/k8snetworkplumbingwg/sriov-network-operator/test/util/helpers"
)

func restoreOrigFuncs() {
	origNewGenericPluginFunc := newGenericPluginFunc
	origNewVirtualPluginFunc := newVirtualPluginFunc
	origNewHostHelpersFunc := newHostHelpersFunc
	origNewPlatformHelperFunc := newPlatformHelperFunc
	DeferCleanup(func() {
		newGenericPluginFunc = origNewGenericPluginFunc
		newVirtualPluginFunc = origNewVirtualPluginFunc
		newHostHelpersFunc = origNewHostHelpersFunc
		newPlatformHelperFunc = origNewPlatformHelperFunc
	})
}

func getTestSriovInterfaceConfig(platform int) []byte {
	return []byte(fmt.Sprintf(`spec:
    interfaces:
        - pciaddress: 0000:d8:00.0
          numvfs: 4
          mtu: 1500
          name: enp216s0f0np0
          linktype: ""
          eswitchmode: legacy
          vfgroups:
            - resourcename: legacy
              devicetype: ""
              vfrange: 0-3
              policyname: test-legacy
              mtu: 1500
              isrdma: false
              vdpatype: ""
          externallymanaged: false
unsupportedNics: false
platformType: %d
manageSoftwareBridges: true
`, platform))
}

var testSriovSupportedNicIDs = `8086 1583 154c
8086 0d58 154c
8086 10c9 10ca
`

func getTestResultFileContent(syncStatus, errMsg string) []byte {
	data, err := yaml.Marshal(systemd.SriovResult{SyncStatus: syncStatus, LastSyncError: errMsg})
	ExpectWithOffset(1, err).NotTo(HaveOccurred())
	return data
}

// checks if NodeState contains deviceName in spec and status fields
func newNodeStateContainsDeviceMatcher(deviceName string) gomock.Matcher {
	return &nodeStateContainsDeviceMatcher{deviceName: deviceName}
}

type nodeStateContainsDeviceMatcher struct {
	deviceName string
}

func (ns *nodeStateContainsDeviceMatcher) Matches(x interface{}) bool {
	s, ok := x.(*sriovnetworkv1.SriovNetworkNodeState)
	if !ok {
		return false
	}
	specFound := false
	for _, i := range s.Spec.Interfaces {
		if i.Name == ns.deviceName {
			specFound = true
			break
		}
	}
	if !specFound {
		return false
	}
	statusFound := false
	for _, i := range s.Status.Interfaces {
		if i.Name == ns.deviceName {
			statusFound = true
			break
		}
	}
	return statusFound
}

func (ns *nodeStateContainsDeviceMatcher) String() string {
	return "node state contains match: " + ns.deviceName
}

var _ = Describe("Service", func() {
	var (
		hostHelpers    *helperMock.MockHostHelpersInterface
		platformHelper *platformsMock.MockInterface
		genericPlugin  *pluginsMock.MockVendorPlugin
		virtualPlugin  *pluginsMock.MockVendorPlugin

		testCtrl  *gomock.Controller
		testError = fmt.Errorf("test")
	)

	BeforeEach(func() {
		restoreOrigFuncs()

		testCtrl = gomock.NewController(GinkgoT())
		hostHelpers = helperMock.NewMockHostHelpersInterface(testCtrl)
		platformHelper = platformsMock.NewMockInterface(testCtrl)
		genericPlugin = pluginsMock.NewMockVendorPlugin(testCtrl)
		virtualPlugin = pluginsMock.NewMockVendorPlugin(testCtrl)

		newGenericPluginFunc = func(_ helper.HostHelpersInterface, _ ...generic.Option) (plugin.VendorPlugin, error) {
			return genericPlugin, nil
		}
		newVirtualPluginFunc = func(_ helper.HostHelpersInterface) (plugin.VendorPlugin, error) {
			return virtualPlugin, nil
		}
		newHostHelpersFunc = func() (helper.HostHelpersInterface, error) {
			return hostHelpers, nil
		}
		newPlatformHelperFunc = func() (platforms.Interface, error) {
			return platformHelper, nil
		}

	})
	AfterEach(func() {
		phaseArg = ""
		testCtrl.Finish()
	})

	It("Pre phase - baremetal cluster", func() {
		phaseArg = PhasePre
		testHelpers.GinkgoConfigureFakeFS(&fakefilesystem.FS{
			Dirs: []string{"/etc/sriov-operator"},
			Files: map[string][]byte{
				"/etc/sriov-operator/sriov-supported-nics-ids.yaml": []byte(testSriovSupportedNicIDs),
				"/etc/sriov-operator/sriov-interface-config.yaml":   getTestSriovInterfaceConfig(0),
				"/etc/sriov-operator/sriov-interface-result.yaml":   []byte("something"),
			},
		})
		hostHelpers.EXPECT().TryGetInterfaceName("0000:d8:00.0").Return("enp216s0f0np0")
		hostHelpers.EXPECT().WaitUdevEventsProcessed(60).Return(nil)
		hostHelpers.EXPECT().CheckRDMAEnabled().Return(true, nil)
		hostHelpers.EXPECT().TryEnableTun().Return()
		hostHelpers.EXPECT().TryEnableVhostNet().Return()
		hostHelpers.EXPECT().DiscoverSriovDevices(hostHelpers).Return([]sriovnetworkv1.InterfaceExt{{
			Name: "enp216s0f0np0",
		}}, nil)
		genericPlugin.EXPECT().OnNodeStateChange(newNodeStateContainsDeviceMatcher("enp216s0f0np0")).Return(true, false, nil)
		genericPlugin.EXPECT().Apply().Return(nil)

		Expect(runServiceCmd(&cobra.Command{}, []string{})).NotTo(HaveOccurred())

		testHelpers.GinkgoAssertFileContentsEquals("/etc/sriov-operator/sriov-interface-result.yaml",
			string(getTestResultFileContent("InProgress", "")))
	})

	It("Pre phase - virtual cluster", func() {
		phaseArg = PhasePre
		testHelpers.GinkgoConfigureFakeFS(&fakefilesystem.FS{
			Dirs: []string{"/etc/sriov-operator"},
			Files: map[string][]byte{
				"/etc/sriov-operator/sriov-supported-nics-ids.yaml": []byte(testSriovSupportedNicIDs),
				"/etc/sriov-operator/sriov-interface-config.yaml":   getTestSriovInterfaceConfig(1),
				"/etc/sriov-operator/sriov-interface-result.yaml":   []byte("something"),
			},
		})
		hostHelpers.EXPECT().CheckRDMAEnabled().Return(true, nil)
		hostHelpers.EXPECT().TryEnableTun().Return()
		hostHelpers.EXPECT().TryEnableVhostNet().Return()

		platformHelper.EXPECT().CreateOpenstackDevicesInfo().Return(nil)
		platformHelper.EXPECT().DiscoverSriovDevicesVirtual().Return([]sriovnetworkv1.InterfaceExt{{
			Name: "enp216s0f0np0",
		}}, nil)

		virtualPlugin.EXPECT().OnNodeStateChange(newNodeStateContainsDeviceMatcher("enp216s0f0np0")).Return(true, false, nil)
		virtualPlugin.EXPECT().Apply().Return(nil)

		Expect(runServiceCmd(&cobra.Command{}, []string{})).NotTo(HaveOccurred())

		testHelpers.GinkgoAssertFileContentsEquals("/etc/sriov-operator/sriov-interface-result.yaml",
			string(getTestResultFileContent("InProgress", "")))
	})

	It("Pre phase - apply failed", func() {
		phaseArg = PhasePre
		testHelpers.GinkgoConfigureFakeFS(&fakefilesystem.FS{
			Dirs: []string{"/etc/sriov-operator"},
			Files: map[string][]byte{
				"/etc/sriov-operator/sriov-supported-nics-ids.yaml": []byte(testSriovSupportedNicIDs),
				"/etc/sriov-operator/sriov-interface-config.yaml":   getTestSriovInterfaceConfig(0),
				"/etc/sriov-operator/sriov-interface-result.yaml":   []byte("something"),
			},
		})
		hostHelpers.EXPECT().TryGetInterfaceName("0000:d8:00.0").Return("enp216s0f0np0")
		hostHelpers.EXPECT().WaitUdevEventsProcessed(60).Return(nil)
		hostHelpers.EXPECT().CheckRDMAEnabled().Return(true, nil)
		hostHelpers.EXPECT().TryEnableTun().Return()
		hostHelpers.EXPECT().TryEnableVhostNet().Return()
		hostHelpers.EXPECT().DiscoverSriovDevices(hostHelpers).Return([]sriovnetworkv1.InterfaceExt{{
			Name: "enp216s0f0np0",
		}}, nil)
		genericPlugin.EXPECT().OnNodeStateChange(newNodeStateContainsDeviceMatcher("enp216s0f0np0")).Return(true, false, nil)
		genericPlugin.EXPECT().Apply().Return(testError)

		Expect(runServiceCmd(&cobra.Command{}, []string{})).To(MatchError(ContainSubstring("test")))

		testHelpers.GinkgoAssertFileContentsEquals("/etc/sriov-operator/sriov-interface-result.yaml",
			string(getTestResultFileContent("Failed", "pre: failed to apply configuration: test")))
	})

	It("Post phase - baremetal cluster", func() {
		phaseArg = PhasePost
		testHelpers.GinkgoConfigureFakeFS(&fakefilesystem.FS{
			Dirs: []string{"/etc/sriov-operator"},
			Files: map[string][]byte{
				"/etc/sriov-operator/sriov-supported-nics-ids.yaml": []byte(testSriovSupportedNicIDs),
				"/etc/sriov-operator/sriov-interface-config.yaml":   getTestSriovInterfaceConfig(0),
				"/etc/sriov-operator/sriov-interface-result.yaml":   getTestResultFileContent("InProgress", ""),
			},
		})
		hostHelpers.EXPECT().TryGetInterfaceName("0000:d8:00.0").Return("enp216s0f0np0")
		hostHelpers.EXPECT().WaitUdevEventsProcessed(60).Return(nil)
		hostHelpers.EXPECT().DiscoverSriovDevices(hostHelpers).Return([]sriovnetworkv1.InterfaceExt{{
			Name: "enp216s0f0np0",
		}}, nil)
		hostHelpers.EXPECT().DiscoverBridges().Return(sriovnetworkv1.Bridges{}, nil)
		genericPlugin.EXPECT().OnNodeStateChange(newNodeStateContainsDeviceMatcher("enp216s0f0np0")).Return(true, false, nil)
		genericPlugin.EXPECT().Apply().Return(nil)
		Expect(runServiceCmd(&cobra.Command{}, []string{})).NotTo(HaveOccurred())
		testHelpers.GinkgoAssertFileContentsEquals("/etc/sriov-operator/sriov-interface-result.yaml",
			string(getTestResultFileContent("Succeeded", "")))
	})

	It("Post phase - virtual cluster", func() {
		phaseArg = PhasePost
		testHelpers.GinkgoConfigureFakeFS(&fakefilesystem.FS{
			Dirs: []string{"/etc/sriov-operator"},
			Files: map[string][]byte{
				"/etc/sriov-operator/sriov-supported-nics-ids.yaml": []byte(testSriovSupportedNicIDs),
				"/etc/sriov-operator/sriov-interface-config.yaml":   getTestSriovInterfaceConfig(1),
				"/etc/sriov-operator/sriov-interface-result.yaml":   getTestResultFileContent("InProgress", ""),
			},
		})
		Expect(runServiceCmd(&cobra.Command{}, []string{})).NotTo(HaveOccurred())
		testHelpers.GinkgoAssertFileContentsEquals("/etc/sriov-operator/sriov-interface-result.yaml",
			string(getTestResultFileContent("Succeeded", "")))
	})

	It("Post phase - wrong result of the pre phase", func() {
		phaseArg = PhasePost
		testHelpers.GinkgoConfigureFakeFS(&fakefilesystem.FS{
			Dirs: []string{"/etc/sriov-operator"},
			Files: map[string][]byte{
				"/etc/sriov-operator/sriov-supported-nics-ids.yaml": []byte(testSriovSupportedNicIDs),
				"/etc/sriov-operator/sriov-interface-config.yaml":   getTestSriovInterfaceConfig(1),
				"/etc/sriov-operator/sriov-interface-result.yaml":   getTestResultFileContent("Failed", "pretest"),
			},
		})
		Expect(runServiceCmd(&cobra.Command{}, []string{})).To(HaveOccurred())
		testHelpers.GinkgoAssertFileContentsEquals("/etc/sriov-operator/sriov-interface-result.yaml",
			string(getTestResultFileContent("Failed", "post: unexpected result of the pre phase: Failed, syncError: pretest")))
	})
	It("waitForDevicesInitialization", func() {
		cfg := &systemd.SriovConfig{Spec: sriovnetworkv1.SriovNetworkNodeStateSpec{
			Interfaces: []sriovnetworkv1.Interface{
				{Name: "name1", PciAddress: "0000:d8:00.0"},
				{Name: "name2", PciAddress: "0000:d8:00.1"}}}}
		hostHelpers.EXPECT().TryGetInterfaceName("0000:d8:00.0").Return("other")
		hostHelpers.EXPECT().TryGetInterfaceName("0000:d8:00.1").Return("")
		hostHelpers.EXPECT().TryGetInterfaceName("0000:d8:00.0").Return("name1")
		hostHelpers.EXPECT().TryGetInterfaceName("0000:d8:00.1").Return("")
		hostHelpers.EXPECT().TryGetInterfaceName("0000:d8:00.1").Return("name2")
		hostHelpers.EXPECT().WaitUdevEventsProcessed(60).Return(nil)
		waitForDevicesInitialization(logr.Discard(), cfg, hostHelpers)
	})
})
