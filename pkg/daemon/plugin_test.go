package daemon

import (
	gomock "github.com/golang/mock/gomock"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	v1 "github.com/k8snetworkplumbingwg/sriov-network-operator/api/v1"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/consts"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/helper"
	helperMocks "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/helper/mock"
	plugin "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/plugins"
	fakePlugin "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/plugins/fake"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/vars"
)

func validateVendorPlugins(loadedPlugins map[string]plugin.VendorPlugin, expectedPlugins []string) {
	var loadedPluginsList []string
	for k := range loadedPlugins {
		loadedPluginsList = append(loadedPluginsList, k)
	}
	ExpectWithOffset(-1, loadedPluginsList).To(HaveLen(len(expectedPlugins)))
	ExpectWithOffset(-1, loadedPluginsList).To(ContainElements(expectedPlugins))
}

var _ = Describe("config daemon plugin loading tests", func() {
	Context("loadPlugins", func() {
		var gmockController *gomock.Controller
		var helperMock *helperMocks.MockHostHelpersInterface

		BeforeEach(func() {
			prevK8sPluginFn := K8sPlugin
			prevClusterType := vars.ClusterType
			prevPlatformType := vars.PlatformType
			DeferCleanup(func() {
				vars.ClusterType = prevClusterType
				vars.PlatformType = prevPlatformType
				K8sPlugin = prevK8sPluginFn
			})

			vars.ClusterType = consts.ClusterTypeKubernetes
			gmockController = gomock.NewController(GinkgoT())
			helperMock = helperMocks.NewMockHostHelpersInterface(gmockController)
			// k8s plugin is ATM the only plugin which require mocking/faking, as its New method performs additional logic
			// other than simple plugin struct initialization
			K8sPlugin = func(_ helper.HostHelpersInterface) (plugin.VendorPlugin, error) {
				return &fakePlugin.FakePlugin{PluginName: "k8s"}, nil
			}
		})

		It("loads relevant plugins Baremetal platform, kubernetes cluster type", func() {
			ns := &v1.SriovNetworkNodeState{
				Status: v1.SriovNetworkNodeStateStatus{
					Interfaces: v1.InterfaceExts{
						v1.InterfaceExt{Vendor: "15b3"},
						v1.InterfaceExt{Vendor: "8086"}},
				},
			}
			vendorPlugins, err := loadPlugins(ns, helperMock, nil)

			Expect(err).ToNot(HaveOccurred())
			validateVendorPlugins(vendorPlugins, []string{"mellanox", "intel", "generic", "k8s"})
		})

		It("loads relevant plugins Baremetal platform, openshift cluster type", func() {
			vars.ClusterType = consts.ClusterTypeOpenshift
			ns := &v1.SriovNetworkNodeState{
				Status: v1.SriovNetworkNodeStateStatus{
					Interfaces: v1.InterfaceExts{
						v1.InterfaceExt{Vendor: "15b3"},
						v1.InterfaceExt{Vendor: "8086"}},
				},
			}
			vendorPlugins, err := loadPlugins(ns, helperMock, nil)

			Expect(err).ToNot(HaveOccurred())
			validateVendorPlugins(vendorPlugins, []string{"mellanox", "intel", "generic"})
		})

		It("loads only virtual plugin in VirtualOpenstack platform", func() {
			vars.PlatformType = consts.VirtualOpenStack
			ns := &v1.SriovNetworkNodeState{
				Status: v1.SriovNetworkNodeStateStatus{
					Interfaces: v1.InterfaceExts{
						v1.InterfaceExt{Vendor: "15b3"},
						v1.InterfaceExt{Vendor: "8086"}},
				},
			}
			vendorPlugins, err := loadPlugins(ns, helperMock, nil)

			Expect(err).ToNot(HaveOccurred())
			validateVendorPlugins(vendorPlugins, []string{"virtual"})
		})

		It("loads vendor plugin according to vendors present", func() {
			ns := &v1.SriovNetworkNodeState{
				Status: v1.SriovNetworkNodeStateStatus{
					Interfaces: v1.InterfaceExts{
						v1.InterfaceExt{Vendor: "8086"}},
				},
			}
			vendorPlugins, err := loadPlugins(ns, helperMock, nil)

			Expect(err).ToNot(HaveOccurred())
			validateVendorPlugins(vendorPlugins, []string{"intel", "generic", "k8s"})
		})

		It("does not load disabled vendor plugins", func() {
			ns := &v1.SriovNetworkNodeState{
				Status: v1.SriovNetworkNodeStateStatus{
					Interfaces: v1.InterfaceExts{
						v1.InterfaceExt{Vendor: "15b3"},
						v1.InterfaceExt{Vendor: "8086"}},
				},
			}
			vendorPlugins, err := loadPlugins(ns, helperMock, []string{"mellanox"})

			Expect(err).ToNot(HaveOccurred())
			validateVendorPlugins(vendorPlugins, []string{"intel", "generic", "k8s"})
		})

		It("does not load disabled non vendor plugins", func() {
			ns := &v1.SriovNetworkNodeState{
				Status: v1.SriovNetworkNodeStateStatus{
					Interfaces: v1.InterfaceExts{
						v1.InterfaceExt{Vendor: "15b3"},
						v1.InterfaceExt{Vendor: "8086"}},
				},
			}
			vendorPlugins, err := loadPlugins(ns, helperMock, []string{"generic"})

			Expect(err).ToNot(HaveOccurred())
			validateVendorPlugins(vendorPlugins, []string{"intel", "k8s", "mellanox"})
		})
	})
})
