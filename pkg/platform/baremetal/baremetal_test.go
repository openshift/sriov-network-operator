package baremetal

import (
	"errors"
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"go.uber.org/mock/gomock"

	sriovnetworkv1 "github.com/k8snetworkplumbingwg/sriov-network-operator/api/v1"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/consts"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/helper"
	mock_helper "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/helper/mock"
	hosttypes "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/host/types"
	plugin "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/plugins"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/vars"
)

var _ = Describe("Baremetal", func() {
	var (
		mockCtrl   *gomock.Controller
		bm         *Baremetal
		hostHelper *mock_helper.MockHostHelpersInterface
	)

	BeforeEach(func() {
		mockCtrl = gomock.NewController(GinkgoT())
		hostHelper = mock_helper.NewMockHostHelpersInterface(mockCtrl)
		hostHelper.EXPECT().GetCurrentKernelArgs().Return("", nil).AnyTimes()
		hostHelper.EXPECT().IsKernelArgsSet(gomock.Any(), gomock.Any()).Return(true).AnyTimes()

		var err error
		bm, err = New(hostHelper)
		Expect(err).NotTo(HaveOccurred())
		Expect(bm).NotTo(BeNil())
	})

	AfterEach(func() {
		// Finish checks that all expected calls were made
		mockCtrl.Finish()
	})

	Context("Initialization and Basic Getters", func() {
		It("should return nil from Init", func() {
			Expect(bm.Init()).To(BeNil())
		})

		It("should return the host helper", func() {
			Expect(bm.GetHostHelpers()).To(Equal(hostHelper))
		})
	})

	Context("Device and Bridge Discovery", func() {
		It("should discover SR-IOV devices by calling the helper", func() {
			expectedDevices := []sriovnetworkv1.InterfaceExt{{PciAddress: "0000:01:00.0"}}
			hostHelper.EXPECT().DiscoverSriovDevices(hostHelper).Return(expectedDevices, nil)

			devices, err := bm.DiscoverSriovDevices()
			Expect(err).NotTo(HaveOccurred())
			Expect(devices).To(Equal(expectedDevices))
		})

		When("ManageSoftwareBridges is true", func() {
			It("should discover bridges by calling the helper", func() {
				vars.ManageSoftwareBridges = true
				expectedBridges := sriovnetworkv1.Bridges{}
				hostHelper.EXPECT().DiscoverBridges().Return(expectedBridges, nil)

				bridges, err := bm.DiscoverBridges()
				Expect(err).NotTo(HaveOccurred())
				Expect(bridges).To(Equal(expectedBridges))
			})
		})

		When("ManageSoftwareBridges is false", func() {
			It("should return empty bridges without calling the helper", func() {
				vars.ManageSoftwareBridges = false
				bridges, err := bm.DiscoverBridges()
				Expect(err).NotTo(HaveOccurred())
				Expect(bridges.OVS).To(BeNil())
			})
		})
	})

	Context("Plugin Loading", func() {
		var ns *sriovnetworkv1.SriovNetworkNodeState

		Context("kubernetes", func() {
			BeforeEach(func() {
				// Set cluster type to kubernetes for this context
				vars.ClusterType = consts.ClusterTypeKubernetes
				// Use DeferCleanup to reset the global variable after the tests in this context run
				DeferCleanup(func() {
					vars.ClusterType = "" // Reset to default
				})

				hostHelper.EXPECT().ReadServiceManifestFile(gomock.Any()).Return(&hosttypes.Service{Name: "test"}, nil).AnyTimes()
				hostHelper.EXPECT().ReadServiceInjectionManifestFile(gomock.Any()).Return(&hosttypes.Service{Name: "test"}, nil).AnyTimes()

				// Assuming default cluster type is Kubernetes for these tests
				// In a real-world scenario, you might need to mock orchestrator.New()
				// or set environment variables if it relies on them.
				ns = &sriovnetworkv1.SriovNetworkNodeState{
					Status: sriovnetworkv1.SriovNetworkNodeStateStatus{
						Interfaces: []sriovnetworkv1.InterfaceExt{
							{Vendor: "8086", PciAddress: "0000:01:00.0"}, // Intel
							{Vendor: "15b3", PciAddress: "0000:02:00.0"}, // Mellanox
							{Vendor: "9999", PciAddress: "0000:03:00.0"}, // Unknown
						},
					},
				}
			})

			It("should load generic, k8s, and all unique vendor plugins", func() {
				mainPlugin, addPlugins, err := bm.GetPlugins(ns)

				Expect(err).NotTo(HaveOccurred())
				Expect(mainPlugin.Name()).To(Equal("generic"))

				// Expect 3 additional plugins: intel, mellanox, k8s
				Expect(addPlugins).To(HaveLen(3))

				pluginNames := []string{}
				for _, p := range addPlugins {
					pluginNames = append(pluginNames, p.Name())
				}
				Expect(pluginNames).To(ContainElements("intel", "mellanox", "k8s"))
			})

			It("should not duplicate plugins for vendors with multiple interfaces", func() {
				ns.Status.Interfaces = append(ns.Status.Interfaces, sriovnetworkv1.InterfaceExt{
					Vendor: "8086", PciAddress: "0000:01:00.1", // Another Intel
				})

				_, addPlugins, err := bm.GetPlugins(ns)
				Expect(err).NotTo(HaveOccurred())

				// Still expect only 3 additional plugins
				Expect(addPlugins).To(HaveLen(3))
				pluginNames := []string{}
				for _, p := range addPlugins {
					pluginNames = append(pluginNames, p.Name())
				}
				Expect(pluginNames).To(ContainElements("intel", "mellanox", "k8s"))
			})

			It("should return an error if a vendor plugin fails to load", func() {
				// Temporarily replace the plugin factory with one that returns an error
				originalIntelPlugin := VendorPluginMap["8086"]
				defer func() { VendorPluginMap["8086"] = originalIntelPlugin }()

				VendorPluginMap["8086"] = func(h helper.HostHelpersInterface) (plugin.VendorPlugin, error) {
					return nil, errors.New("intel plugin load failure")
				}

				_, _, err := bm.GetPlugins(ns)
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("intel plugin load failure"))
			})
		})
		Context("Openshift", func() {
			BeforeEach(func() {
				// Set cluster type to Openshift for this context
				vars.ClusterType = consts.ClusterTypeOpenshift
				// Use DeferCleanup to reset the global variable after the tests in this context run
				DeferCleanup(func() {
					vars.ClusterType = "" // Reset to default
				})

				// Set up the same node state as the kubernetes tests
				ns = &sriovnetworkv1.SriovNetworkNodeState{
					Status: sriovnetworkv1.SriovNetworkNodeStateStatus{
						Interfaces: []sriovnetworkv1.InterfaceExt{
							{Vendor: "8086", PciAddress: "0000:01:00.0"}, // Intel
							{Vendor: "15b3", PciAddress: "0000:02:00.0"}, // Mellanox
						},
					},
				}
			})

			It("should load generic and vendor plugins, but not the k8s plugin", func() {
				mainPlugin, addPlugins, err := bm.GetPlugins(ns)

				Expect(err).NotTo(HaveOccurred())
				Expect(mainPlugin.Name()).To(Equal("generic"))

				// Expect 2 additional plugins: intel, mellanox. k8s_plugin should NOT be loaded.
				Expect(addPlugins).To(HaveLen(2))

				pluginNames := []string{}
				for _, p := range addPlugins {
					pluginNames = append(pluginNames, p.Name())
				}
				Expect(pluginNames).To(ContainElements("intel", "mellanox"))
				Expect(pluginNames).NotTo(ContainElement("k8s"))
			})
		})
	})

	Context("Systemd Plugin Retrieval", func() {
		It("should return a configured generic plugin for PhasePre", func() {
			p, err := bm.SystemdGetPlugin(consts.PhasePre)
			Expect(err).NotTo(HaveOccurred())
			Expect(p).NotTo(BeNil())
			Expect(p.Name()).To(Equal("generic"))
			// Note: Verifying the With... options requires either inspecting the
			// internal state of the plugin or having the plugin expose its config.
			// A simple type/name check is often sufficient for unit tests.
		})

		It("should return a standard generic plugin for PhasePost", func() {
			p, err := bm.SystemdGetPlugin(consts.PhasePost)
			Expect(err).NotTo(HaveOccurred())
			Expect(p).NotTo(BeNil())
			Expect(p.Name()).To(Equal("generic"))
		})

		It("should return an error for an invalid phase", func() {
			p, err := bm.SystemdGetPlugin("invalid-phase")
			Expect(err).To(HaveOccurred())
			Expect(p).To(BeNil())
			Expect(err.Error()).To(Equal(fmt.Sprintf("invalid phase %s", "invalid-phase")))
		})
	})
})
