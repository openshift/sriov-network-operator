package generic

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	sriovnetworkv1 "github.com/k8snetworkplumbingwg/sriov-network-operator/api/v1"
	plugin "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/plugins"
)

func TestGenericPlugin(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Test Generic Plugin")
}

var _ = Describe("Generic plugin", func() {
	var genericPlugin plugin.VendorPlugin
	var err error
	BeforeEach(func() {
		genericPlugin, err = NewGenericPlugin(false)
		Expect(err).ToNot(HaveOccurred())
	})

	Context("OnNodeStateChange", func() {
		It("should not drain", func() {
			networkNodeState := &sriovnetworkv1.SriovNetworkNodeState{
				Spec: sriovnetworkv1.SriovNetworkNodeStateSpec{
					Interfaces: sriovnetworkv1.Interfaces{{
						PciAddress: "0000:00:00.0",
						NumVfs:     1,
						VfGroups: []sriovnetworkv1.VfGroup{{
							DeviceType:   "netdevice",
							PolicyName:   "policy-1",
							ResourceName: "resource-1",
							VfRange:      "0-0",
						}}}},
				},
				Status: sriovnetworkv1.SriovNetworkNodeStateStatus{
					Interfaces: sriovnetworkv1.InterfaceExts{{
						PciAddress:  "0000:00:00.0",
						NumVfs:      1,
						TotalVfs:    1,
						DeviceID:    "1015",
						Vendor:      "15b3",
						Name:        "sriovif1",
						Mtu:         1500,
						Mac:         "0c:42:a1:55:ee:46",
						Driver:      "mlx5_core",
						EswitchMode: "legacy",
						LinkSpeed:   "25000 Mb/s",
						LinkType:    "ETH",
						VFs: []sriovnetworkv1.VirtualFunction{{
							PciAddress: "0000:00:00.1",
							DeviceID:   "1016",
							Vendor:     "15b3",
							VfID:       0,
							Name:       "sriovif1v0",
							Mtu:        1500,
							Mac:        "8e:d6:2c:62:87:1b",
							Driver:     "mlx5_core",
						}},
					}},
				},
			}

			needDrain, needReboot, err := genericPlugin.OnNodeStateChange(networkNodeState)
			Expect(err).ToNot(HaveOccurred())
			Expect(needReboot).To(BeFalse())
			Expect(needDrain).To(BeFalse())
		})

		It("should drain", func() {
			networkNodeState := &sriovnetworkv1.SriovNetworkNodeState{
				Spec: sriovnetworkv1.SriovNetworkNodeStateSpec{
					Interfaces: sriovnetworkv1.Interfaces{{
						PciAddress: "0000:00:00.0",
						NumVfs:     2,
						Mtu:        1500,
						VfGroups: []sriovnetworkv1.VfGroup{{
							DeviceType:   "netdevice",
							PolicyName:   "policy-1",
							ResourceName: "resource-1",
							VfRange:      "0-1",
							Mtu:          1500,
						}}}},
				},
				Status: sriovnetworkv1.SriovNetworkNodeStateStatus{
					Interfaces: sriovnetworkv1.InterfaceExts{{
						PciAddress:  "0000:00:00.0",
						NumVfs:      2,
						TotalVfs:    2,
						DeviceID:    "1015",
						Vendor:      "15b3",
						Name:        "sriovif1",
						Mtu:         1500,
						Mac:         "0c:42:a1:55:ee:46",
						Driver:      "mlx5_core",
						EswitchMode: "legacy",
						LinkSpeed:   "25000 Mb/s",
						LinkType:    "ETH",
						VFs: []sriovnetworkv1.VirtualFunction{{
							PciAddress: "0000:00:00.1",
							DeviceID:   "1016",
							Vendor:     "15b3",
							VfID:       0,
							Driver:     "mlx5_core",
							Name:       "sriovif1v0",
							Mtu:        999, // Bad MTU value, changed by the user application
							Mac:        "8e:d6:2c:62:87:1b",
						}, {
							PciAddress: "0000:00:00.2",
							DeviceID:   "1016",
							Vendor:     "15b3",
							VfID:       0,
							Driver:     "mlx5_core",
						}},
					}},
				},
			}

			needDrain, needReboot, err := genericPlugin.OnNodeStateChange(networkNodeState)
			Expect(err).ToNot(HaveOccurred())
			Expect(needReboot).To(BeFalse())
			Expect(needDrain).To(BeTrue())
		})

		It("should load vfio_pci driver", func() {
			networkNodeState := &sriovnetworkv1.SriovNetworkNodeState{
				Spec: sriovnetworkv1.SriovNetworkNodeStateSpec{
					Interfaces: sriovnetworkv1.Interfaces{{
						PciAddress: "0000:00:00.0",
						NumVfs:     2,
						Mtu:        1500,
						VfGroups: []sriovnetworkv1.VfGroup{{
							DeviceType:   "vfio-pci",
							PolicyName:   "policy-1",
							ResourceName: "resource-1",
							VfRange:      "0-1",
							Mtu:          1500,
						}}}},
				},
				Status: sriovnetworkv1.SriovNetworkNodeStateStatus{
					Interfaces: sriovnetworkv1.InterfaceExts{{
						PciAddress:  "0000:00:00.0",
						NumVfs:      2,
						TotalVfs:    2,
						DeviceID:    "1015",
						Vendor:      "15b3",
						Name:        "sriovif1",
						Mtu:         1500,
						Mac:         "0c:42:a1:55:ee:46",
						Driver:      "mlx5_core",
						EswitchMode: "legacy",
						LinkSpeed:   "25000 Mb/s",
						LinkType:    "ETH",
						VFs: []sriovnetworkv1.VirtualFunction{{
							PciAddress: "0000:00:00.1",
							DeviceID:   "1016",
							Vendor:     "15b3",
							VfID:       0,
							Driver:     "mlx5_core",
							Name:       "sriovif1v0",
							Mtu:        1500,
							Mac:        "8e:d6:2c:62:87:1b",
						}, {
							PciAddress: "0000:00:00.2",
							DeviceID:   "1016",
							Vendor:     "15b3",
							VfID:       0,
							Driver:     "mlx5_core",
						}},
					}},
				},
			}

			concretePlugin := genericPlugin.(*GenericPlugin)
			driverStateMap := concretePlugin.getDriverStateMap()
			driverState := driverStateMap[Vfio]
			concretePlugin.loadDriverForTests(networkNodeState)
			Expect(driverState.DriverLoaded).To(BeTrue())
		})

		It("should load virtio_vdpa driver", func() {
			networkNodeState := &sriovnetworkv1.SriovNetworkNodeState{
				Spec: sriovnetworkv1.SriovNetworkNodeStateSpec{
					Interfaces: sriovnetworkv1.Interfaces{{
						PciAddress: "0000:00:00.0",
						NumVfs:     2,
						Mtu:        1500,
						VfGroups: []sriovnetworkv1.VfGroup{{
							DeviceType:   "netdevice",
							VdpaType:     "virtio",
							PolicyName:   "policy-1",
							ResourceName: "resource-1",
							VfRange:      "0-1",
							Mtu:          1500,
						}}}},
				},
				Status: sriovnetworkv1.SriovNetworkNodeStateStatus{
					Interfaces: sriovnetworkv1.InterfaceExts{{
						PciAddress:  "0000:00:00.0",
						NumVfs:      2,
						TotalVfs:    2,
						DeviceID:    "1015",
						Vendor:      "15b3",
						Name:        "sriovif1",
						Mtu:         1500,
						Mac:         "0c:42:a1:55:ee:46",
						Driver:      "mlx5_core",
						EswitchMode: "legacy",
						LinkSpeed:   "25000 Mb/s",
						LinkType:    "ETH",
						VFs: []sriovnetworkv1.VirtualFunction{{
							PciAddress: "0000:00:00.1",
							DeviceID:   "1016",
							Vendor:     "15b3",
							VfID:       0,
							Driver:     "mlx5_core",
							Name:       "sriovif1v0",
							Mtu:        1500,
							Mac:        "8e:d6:2c:62:87:1b",
						}, {
							PciAddress: "0000:00:00.2",
							DeviceID:   "1016",
							Vendor:     "15b3",
							VfID:       0,
							Driver:     "mlx5_core",
						}},
					}},
				},
			}

			concretePlugin := genericPlugin.(*GenericPlugin)
			driverStateMap := concretePlugin.getDriverStateMap()
			driverState := driverStateMap[VirtioVdpa]
			concretePlugin.loadDriverForTests(networkNodeState)
			Expect(driverState.DriverLoaded).To(BeTrue())
		})
	})

})
