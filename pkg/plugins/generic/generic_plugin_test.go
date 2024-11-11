package generic

import (
	"testing"

	"github.com/golang/mock/gomock"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	sriovnetworkv1 "github.com/k8snetworkplumbingwg/sriov-network-operator/api/v1"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/consts"
	mock_helper "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/helper/mock"
	hostTypes "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/host/types"
	plugin "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/plugins"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/vars"
)

func TestGenericPlugin(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Test Generic Plugin")
}

var _ = Describe("Generic plugin", func() {
	var (
		t             GinkgoTInterface
		genericPlugin plugin.VendorPlugin
		err           error
		ctrl          *gomock.Controller
		hostHelper    *mock_helper.MockHostHelpersInterface
	)

	BeforeEach(func() {
		t = GinkgoT()
		ctrl = gomock.NewController(t)

		hostHelper = mock_helper.NewMockHostHelpersInterface(ctrl)
		hostHelper.EXPECT().SetRDMASubsystem("").Return(nil).AnyTimes()
		hostHelper.EXPECT().GetCurrentKernelArgs().Return("", nil).AnyTimes()
		hostHelper.EXPECT().IsKernelArgsSet("", consts.KernelArgIntelIommu).Return(false).AnyTimes()
		hostHelper.EXPECT().IsKernelArgsSet("", consts.KernelArgIommuPt).Return(false).AnyTimes()
		hostHelper.EXPECT().IsKernelArgsSet("", consts.KernelArgPciRealloc).Return(false).AnyTimes()
		hostHelper.EXPECT().IsKernelArgsSet("", consts.KernelArgRdmaExclusive).Return(false).AnyTimes()
		hostHelper.EXPECT().IsKernelArgsSet("", consts.KernelArgRdmaShared).Return(false).AnyTimes()
		hostHelper.EXPECT().IsKernelArgsSet("", consts.KernelArgIommuPassthrough).Return(false).AnyTimes()

		hostHelper.EXPECT().RunCommand(gomock.Any(), gomock.Any()).Return("", "", nil).AnyTimes()

		genericPlugin, err = NewGenericPlugin(hostHelper)
		Expect(err).ToNot(HaveOccurred())

		manageSoftwareBridgesOrigValue := vars.ManageSoftwareBridges
		vars.ManageSoftwareBridges = true
		DeferCleanup(func() { vars.ManageSoftwareBridges = manageSoftwareBridgesOrigValue })
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
						PciAddress:     "0000:00:00.0",
						NumVfs:         1,
						TotalVfs:       1,
						DeviceID:       "1015",
						Vendor:         "15b3",
						Name:           "sriovif1",
						Mtu:            1500,
						Mac:            "0c:42:a1:55:ee:46",
						Driver:         "mlx5_core",
						EswitchMode:    "legacy",
						LinkSpeed:      "25000 Mb/s",
						LinkType:       "ETH",
						LinkAdminState: "up",
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

		It("should drain because MTU value has changed on PF", func() {
			networkNodeState := &sriovnetworkv1.SriovNetworkNodeState{
				Spec: sriovnetworkv1.SriovNetworkNodeStateSpec{
					Interfaces: sriovnetworkv1.Interfaces{{
						PciAddress: "0000:00:00.0",
						NumVfs:     1,
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
						PciAddress:     "0000:00:00.0",
						NumVfs:         1,
						TotalVfs:       1,
						DeviceID:       "1015",
						Vendor:         "15b3",
						Name:           "sriovif1",
						Mtu:            300, // Bad MTU value, changed by the user application
						Mac:            "0c:42:a1:55:ee:46",
						Driver:         "mlx5_core",
						EswitchMode:    "legacy",
						LinkSpeed:      "25000 Mb/s",
						LinkType:       "ETH",
						LinkAdminState: "up",
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
			Expect(needDrain).To(BeTrue())
		})

		It("should drain because numVFs value has changed on PF", func() {
			networkNodeState := &sriovnetworkv1.SriovNetworkNodeState{
				Spec: sriovnetworkv1.SriovNetworkNodeStateSpec{
					Interfaces: sriovnetworkv1.Interfaces{{
						PciAddress: "0000:00:00.0",
						NumVfs:     1,
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
						PciAddress:     "0000:00:00.0",
						NumVfs:         2, // Bad numVFs value, changed by the user application
						TotalVfs:       1,
						DeviceID:       "1015",
						Vendor:         "15b3",
						Name:           "sriovif1",
						Mtu:            1500,
						Mac:            "0c:42:a1:55:ee:46",
						Driver:         "mlx5_core",
						EswitchMode:    "legacy",
						LinkSpeed:      "25000 Mb/s",
						LinkType:       "ETH",
						LinkAdminState: "up",
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
			Expect(needDrain).To(BeTrue())
		})

		It("should drain because PF link is down", func() {
			networkNodeState := &sriovnetworkv1.SriovNetworkNodeState{
				Spec: sriovnetworkv1.SriovNetworkNodeStateSpec{
					Interfaces: sriovnetworkv1.Interfaces{{
						PciAddress: "0000:00:00.0",
						NumVfs:     1,
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
						PciAddress:     "0000:00:00.0",
						NumVfs:         1,
						TotalVfs:       1,
						DeviceID:       "1015",
						Vendor:         "15b3",
						Name:           "sriovif1",
						Mtu:            1500,
						Mac:            "0c:42:a1:55:ee:46",
						Driver:         "mlx5_core",
						EswitchMode:    "legacy",
						LinkSpeed:      "25000 Mb/s",
						LinkType:       "IB",
						LinkAdminState: "down",
						VFs: []sriovnetworkv1.VirtualFunction{{
							PciAddress: "0000:00:00.1",
							DeviceID:   "1016",
							Vendor:     "15b3",
							VfID:       0,
							Name:       "sriovif1v0",
							Mtu:        1500,
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

		It("should not drain if PF link is not down", func() {
			networkNodeState := &sriovnetworkv1.SriovNetworkNodeState{
				Spec: sriovnetworkv1.SriovNetworkNodeStateSpec{
					Interfaces: sriovnetworkv1.Interfaces{{
						PciAddress: "0000:00:00.0",
						NumVfs:     1,
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
						PciAddress:     "0000:00:00.0",
						NumVfs:         1,
						TotalVfs:       1,
						DeviceID:       "1015",
						Vendor:         "15b3",
						Name:           "sriovif1",
						Mtu:            1500,
						Mac:            "0c:42:a1:55:ee:46",
						Driver:         "mlx5_core",
						EswitchMode:    "legacy",
						LinkSpeed:      "25000 Mb/s",
						LinkType:       "IB",
						LinkAdminState: "",
						VFs: []sriovnetworkv1.VirtualFunction{{
							PciAddress: "0000:00:00.1",
							DeviceID:   "1016",
							Vendor:     "15b3",
							VfID:       0,
							Name:       "sriovif1v0",
							Mtu:        1500,
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

		It("should drain because driver has changed on VF of type netdevice", func() {
			networkNodeState := &sriovnetworkv1.SriovNetworkNodeState{
				Spec: sriovnetworkv1.SriovNetworkNodeStateSpec{
					Interfaces: sriovnetworkv1.Interfaces{{
						PciAddress: "0000:00:00.0",
						NumVfs:     1,
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
						PciAddress:     "0000:00:00.0",
						NumVfs:         1,
						TotalVfs:       1,
						DeviceID:       "1015",
						Vendor:         "15b3",
						Name:           "sriovif1",
						Mtu:            1500,
						Mac:            "0c:42:a1:55:ee:46",
						Driver:         "mlx5_core",
						EswitchMode:    "legacy",
						LinkSpeed:      "25000 Mb/s",
						LinkType:       "ETH",
						LinkAdminState: "up",
						VFs: []sriovnetworkv1.VirtualFunction{{
							PciAddress: "0000:00:00.1",
							DeviceID:   "1016",
							Vendor:     "15b3",
							VfID:       0,
							Name:       "sriovif1v0",
							Mtu:        1500,
							Mac:        "8e:d6:2c:62:87:1b",
							Driver:     "igb_uio", // The user has requested netdevice == mlx5_core
						}},
					}},
				},
			}

			needDrain, needReboot, err := genericPlugin.OnNodeStateChange(networkNodeState)
			Expect(err).ToNot(HaveOccurred())
			Expect(needReboot).To(BeFalse())
			Expect(needDrain).To(BeTrue())
		})

		It("should drain because MTU value has changed on VF of type netdevice", func() {
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
						PciAddress:     "0000:00:00.0",
						NumVfs:         2,
						TotalVfs:       2,
						DeviceID:       "1015",
						Vendor:         "15b3",
						Name:           "sriovif1",
						Mtu:            1500,
						Mac:            "0c:42:a1:55:ee:46",
						Driver:         "mlx5_core",
						EswitchMode:    "legacy",
						LinkSpeed:      "25000 Mb/s",
						LinkType:       "ETH",
						LinkAdminState: "up",
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
							Mtu:        1500,
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

		It("should drain because GUID address value is the default one on VF of type netdevice, rdma enabled and link type ETH", func() {
			networkNodeState := &sriovnetworkv1.SriovNetworkNodeState{
				Spec: sriovnetworkv1.SriovNetworkNodeStateSpec{
					Interfaces: sriovnetworkv1.Interfaces{{
						PciAddress: "0000:00:00.0",
						NumVfs:     1,
						Mtu:        1500,
						VfGroups: []sriovnetworkv1.VfGroup{{
							DeviceType:   "netdevice",
							PolicyName:   "policy-1",
							ResourceName: "resource-1",
							VfRange:      "0-1",
							Mtu:          1500,
							IsRdma:       true,
						}}}},
				},
				Status: sriovnetworkv1.SriovNetworkNodeStateStatus{
					Interfaces: sriovnetworkv1.InterfaceExts{{
						PciAddress:     "0000:00:00.0",
						NumVfs:         1,
						TotalVfs:       1,
						DeviceID:       "1015",
						Vendor:         "15b3",
						Name:           "sriovif1",
						Mtu:            1500,
						Mac:            "0c:42:a1:55:ee:46",
						Driver:         "mlx5_core",
						EswitchMode:    "legacy",
						LinkSpeed:      "25000 Mb/s",
						LinkType:       "ETH",
						LinkAdminState: "up",
						VFs: []sriovnetworkv1.VirtualFunction{{
							PciAddress: "0000:00:00.1",
							DeviceID:   "1016",
							Vendor:     "15b3",
							VfID:       0,
							Name:       "sriovif1v0",
							Mtu:        1500,
							Driver:     "mlx5_core",
							Mac:        "0c:42:a1:55:ee:46",
							GUID:       "0000:0000:0000:0000",
						}},
					}},
				},
			}

			needDrain, needReboot, err := genericPlugin.OnNodeStateChange(networkNodeState)
			Expect(err).ToNot(HaveOccurred())
			Expect(needReboot).To(BeFalse())
			Expect(needDrain).To(BeTrue())
		})

		It("should not drain if GUID address value is not set on VF of type netdevice, rdma enabled and link type ETH", func() {
			networkNodeState := &sriovnetworkv1.SriovNetworkNodeState{
				Spec: sriovnetworkv1.SriovNetworkNodeStateSpec{
					Interfaces: sriovnetworkv1.Interfaces{{
						PciAddress: "0000:00:00.0",
						NumVfs:     1,
						Mtu:        1500,
						VfGroups: []sriovnetworkv1.VfGroup{{
							DeviceType:   "netdevice",
							PolicyName:   "policy-1",
							ResourceName: "resource-1",
							VfRange:      "0-1",
							Mtu:          1500,
							IsRdma:       true,
						}}}},
				},
				Status: sriovnetworkv1.SriovNetworkNodeStateStatus{
					Interfaces: sriovnetworkv1.InterfaceExts{{
						PciAddress:     "0000:00:00.0",
						NumVfs:         1,
						TotalVfs:       1,
						DeviceID:       "1015",
						Vendor:         "15b3",
						Name:           "sriovif1",
						Mtu:            1500,
						Mac:            "0c:42:a1:55:ee:46",
						Driver:         "mlx5_core",
						EswitchMode:    "legacy",
						LinkSpeed:      "25000 Mb/s",
						LinkType:       "ETH",
						LinkAdminState: "up",
						VFs: []sriovnetworkv1.VirtualFunction{{
							PciAddress: "0000:00:00.1",
							DeviceID:   "1016",
							Vendor:     "15b3",
							VfID:       0,
							Name:       "sriovif1v0",
							Mtu:        1500,
							Driver:     "mlx5_core",
							Mac:        "0c:42:a1:55:ee:46",
						}},
					}},
				},
			}

			needDrain, needReboot, err := genericPlugin.OnNodeStateChange(networkNodeState)
			Expect(err).ToNot(HaveOccurred())
			Expect(needReboot).To(BeFalse())
			Expect(needDrain).To(BeFalse())
		})

		It("should not drain if GUID address value is the default one on VF of type netdevice, rdma disabled and link type ETH", func() {
			networkNodeState := &sriovnetworkv1.SriovNetworkNodeState{
				Spec: sriovnetworkv1.SriovNetworkNodeStateSpec{
					Interfaces: sriovnetworkv1.Interfaces{{
						PciAddress: "0000:00:00.0",
						NumVfs:     1,
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
						PciAddress:     "0000:00:00.0",
						NumVfs:         1,
						TotalVfs:       1,
						DeviceID:       "1015",
						Vendor:         "15b3",
						Name:           "sriovif1",
						Mtu:            1500,
						Mac:            "0c:42:a1:55:ee:46",
						Driver:         "mlx5_core",
						EswitchMode:    "legacy",
						LinkSpeed:      "25000 Mb/s",
						LinkType:       "ETH",
						LinkAdminState: "up",
						VFs: []sriovnetworkv1.VirtualFunction{{
							PciAddress: "0000:00:00.1",
							DeviceID:   "1016",
							Vendor:     "15b3",
							VfID:       0,
							Name:       "sriovif1v0",
							Mtu:        1500,
							Driver:     "mlx5_core",
							Mac:        "0c:42:a1:55:ee:46",
							GUID:       "0000:0000:0000:0000",
						}},
					}},
				},
			}

			needDrain, needReboot, err := genericPlugin.OnNodeStateChange(networkNodeState)
			Expect(err).ToNot(HaveOccurred())
			Expect(needReboot).To(BeFalse())
			Expect(needDrain).To(BeFalse())
		})

		It("should drain because GUID address value is the default one on VF of type netdevice and link type IB", func() {
			networkNodeState := &sriovnetworkv1.SriovNetworkNodeState{
				Spec: sriovnetworkv1.SriovNetworkNodeStateSpec{
					Interfaces: sriovnetworkv1.Interfaces{{
						PciAddress: "0000:00:00.0",
						NumVfs:     1,
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
						PciAddress:     "0000:00:00.0",
						NumVfs:         1,
						TotalVfs:       1,
						DeviceID:       "1015",
						Vendor:         "15b3",
						Name:           "sriovif1",
						Mtu:            1500,
						Mac:            "0c:42:a1:55:ee:46",
						Driver:         "mlx5_core",
						EswitchMode:    "legacy",
						LinkSpeed:      "25000 Mb/s",
						LinkType:       "IB",
						LinkAdminState: "up",
						VFs: []sriovnetworkv1.VirtualFunction{{
							PciAddress: "0000:00:00.1",
							DeviceID:   "1016",
							Vendor:     "15b3",
							VfID:       0,
							Name:       "sriovif1v0",
							Mtu:        1500,
							Driver:     "mlx5_core",
							GUID:       "0000:0000:0000:0000",
						}},
					}},
				},
			}

			needDrain, needReboot, err := genericPlugin.OnNodeStateChange(networkNodeState)
			Expect(err).ToNot(HaveOccurred())
			Expect(needReboot).To(BeFalse())
			Expect(needDrain).To(BeTrue())
		})

		It("should not drain if GUID address value is not set on VF of type netdevice and link type IB", func() {
			networkNodeState := &sriovnetworkv1.SriovNetworkNodeState{
				Spec: sriovnetworkv1.SriovNetworkNodeStateSpec{
					Interfaces: sriovnetworkv1.Interfaces{{
						PciAddress: "0000:00:00.0",
						NumVfs:     1,
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
						PciAddress:     "0000:00:00.0",
						NumVfs:         1,
						TotalVfs:       1,
						DeviceID:       "1015",
						Vendor:         "15b3",
						Name:           "sriovif1",
						Mtu:            1500,
						Mac:            "0c:42:a1:55:ee:46",
						Driver:         "mlx5_core",
						EswitchMode:    "legacy",
						LinkSpeed:      "25000 Mb/s",
						LinkType:       "IB",
						LinkAdminState: "up",
						VFs: []sriovnetworkv1.VirtualFunction{{
							PciAddress: "0000:00:00.1",
							DeviceID:   "1016",
							Vendor:     "15b3",
							VfID:       0,
							Name:       "sriovif1v0",
							Mtu:        1500,
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

		It("should drain because GUID address value is the default one on VF of type netdevice, rdma enabled and link type ETH", func() {
			networkNodeState := &sriovnetworkv1.SriovNetworkNodeState{
				Spec: sriovnetworkv1.SriovNetworkNodeStateSpec{
					Interfaces: sriovnetworkv1.Interfaces{{
						PciAddress: "0000:00:00.0",
						NumVfs:     1,
						Mtu:        1500,
						VfGroups: []sriovnetworkv1.VfGroup{{
							DeviceType:   "netdevice",
							PolicyName:   "policy-1",
							ResourceName: "resource-1",
							VfRange:      "0-1",
							Mtu:          1500,
							IsRdma:       true,
						}}}},
				},
				Status: sriovnetworkv1.SriovNetworkNodeStateStatus{
					Interfaces: sriovnetworkv1.InterfaceExts{{
						PciAddress:     "0000:00:00.0",
						NumVfs:         1,
						TotalVfs:       1,
						DeviceID:       "1015",
						Vendor:         "15b3",
						Name:           "sriovif1",
						Mtu:            1500,
						Mac:            "0c:42:a1:55:ee:46",
						Driver:         "mlx5_core",
						EswitchMode:    "legacy",
						LinkSpeed:      "25000 Mb/s",
						LinkType:       "ETH",
						LinkAdminState: "up",
						VFs: []sriovnetworkv1.VirtualFunction{{
							PciAddress: "0000:00:00.1",
							DeviceID:   "1016",
							Vendor:     "15b3",
							VfID:       0,
							Name:       "sriovif1v0",
							Mtu:        1500,
							Driver:     "mlx5_core",
							Mac:        "0c:42:a1:55:ee:46",
							GUID:       "0000:0000:0000:0000",
						}},
					}},
				},
			}

			needDrain, needReboot, err := genericPlugin.OnNodeStateChange(networkNodeState)
			Expect(err).ToNot(HaveOccurred())
			Expect(needReboot).To(BeFalse())
			Expect(needDrain).To(BeTrue())
		})

		It("should not drain if GUID address value is not set on VF of type netdevice, rdma enabled and link type ETH", func() {
			networkNodeState := &sriovnetworkv1.SriovNetworkNodeState{
				Spec: sriovnetworkv1.SriovNetworkNodeStateSpec{
					Interfaces: sriovnetworkv1.Interfaces{{
						PciAddress: "0000:00:00.0",
						NumVfs:     1,
						Mtu:        1500,
						VfGroups: []sriovnetworkv1.VfGroup{{
							DeviceType:   "netdevice",
							PolicyName:   "policy-1",
							ResourceName: "resource-1",
							VfRange:      "0-1",
							Mtu:          1500,
							IsRdma:       true,
						}}}},
				},
				Status: sriovnetworkv1.SriovNetworkNodeStateStatus{
					Interfaces: sriovnetworkv1.InterfaceExts{{
						PciAddress:     "0000:00:00.0",
						NumVfs:         1,
						TotalVfs:       1,
						DeviceID:       "1015",
						Vendor:         "15b3",
						Name:           "sriovif1",
						Mtu:            1500,
						Mac:            "0c:42:a1:55:ee:46",
						Driver:         "mlx5_core",
						EswitchMode:    "legacy",
						LinkSpeed:      "25000 Mb/s",
						LinkType:       "ETH",
						LinkAdminState: "up",
						VFs: []sriovnetworkv1.VirtualFunction{{
							PciAddress: "0000:00:00.1",
							DeviceID:   "1016",
							Vendor:     "15b3",
							VfID:       0,
							Name:       "sriovif1v0",
							Mtu:        1500,
							Driver:     "mlx5_core",
							Mac:        "0c:42:a1:55:ee:46",
						}},
					}},
				},
			}

			needDrain, needReboot, err := genericPlugin.OnNodeStateChange(networkNodeState)
			Expect(err).ToNot(HaveOccurred())
			Expect(needReboot).To(BeFalse())
			Expect(needDrain).To(BeFalse())
		})

		It("should not detect changes on status", func() {
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

			changed, err := genericPlugin.CheckStatusChanges(networkNodeState)
			Expect(err).ToNot(HaveOccurred())
			Expect(changed).To(BeFalse())
		})

		It("should detect changes on status due to spec mismatch", func() {
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

			changed, err := genericPlugin.CheckStatusChanges(networkNodeState)
			Expect(err).ToNot(HaveOccurred())
			Expect(changed).To(BeTrue())
		})

		Context("Kernel Args", func() {

			vfioNetworkNodeState := &sriovnetworkv1.SriovNetworkNodeState{
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
						DeviceID:    "159b",
						Vendor:      "8086",
						Name:        "sriovif1",
						Mtu:         1500,
						Mac:         "0c:42:a1:55:ee:46",
						Driver:      "ice",
						EswitchMode: "legacy",
						LinkSpeed:   "25000 Mb/s",
						LinkType:    "ETH",
						VFs: []sriovnetworkv1.VirtualFunction{{
							PciAddress: "0000:00:00.1",
							DeviceID:   "1889",
							Vendor:     "8086",
							VfID:       0,
							Driver:     "vfio-pci",
						}, {
							PciAddress: "0000:00:00.2",
							DeviceID:   "1889",
							Vendor:     "8086",
							VfID:       1,
							Driver:     "vfio-pci",
						}},
					}},
				},
			}

			rdmaState := &sriovnetworkv1.SriovNetworkNodeState{
				Spec: sriovnetworkv1.SriovNetworkNodeStateSpec{System: sriovnetworkv1.System{
					RdmaMode: consts.RdmaSubsystemModeShared,
				}},
				Status: sriovnetworkv1.SriovNetworkNodeStateStatus{},
			}

			It("should detect changes on status due to missing kernel args", func() {
				hostHelper.EXPECT().GetCPUVendor().Return(hostTypes.CPUVendorIntel, nil)

				// Load required kernel args.
				genericPlugin.(*GenericPlugin).addVfioDesiredKernelArg(vfioNetworkNodeState)

				Expect(genericPlugin.(*GenericPlugin).DesiredKernelArgs[consts.KernelArgIntelIommu]).To(BeTrue())
				Expect(genericPlugin.(*GenericPlugin).DesiredKernelArgs[consts.KernelArgIommuPt]).To(BeTrue())

				changed, err := genericPlugin.CheckStatusChanges(vfioNetworkNodeState)
				Expect(err).ToNot(HaveOccurred())
				Expect(changed).To(BeTrue())
			})

			It("should set the correct kernel args on AMD CPUs", func() {
				hostHelper.EXPECT().GetCPUVendor().Return(hostTypes.CPUVendorAMD, nil)
				genericPlugin.(*GenericPlugin).addVfioDesiredKernelArg(vfioNetworkNodeState)
				Expect(genericPlugin.(*GenericPlugin).DesiredKernelArgs[consts.KernelArgIommuPt]).To(BeTrue())
			})

			It("should set the correct kernel args on ARM CPUs", func() {
				hostHelper.EXPECT().GetCPUVendor().Return(hostTypes.CPUVendorARM, nil)
				genericPlugin.(*GenericPlugin).addVfioDesiredKernelArg(vfioNetworkNodeState)
				Expect(genericPlugin.(*GenericPlugin).DesiredKernelArgs[consts.KernelArgIommuPassthrough]).To(BeTrue())
			})

			It("should enable rdma shared mode", func() {
				hostHelper.EXPECT().SetRDMASubsystem(consts.RdmaSubsystemModeShared).Return(nil)
				err := genericPlugin.(*GenericPlugin).configRdmaKernelArg(rdmaState)
				Expect(err).ToNot(HaveOccurred())

				Expect(genericPlugin.(*GenericPlugin).DesiredKernelArgs[consts.KernelArgRdmaShared]).To(BeTrue())
				Expect(genericPlugin.(*GenericPlugin).DesiredKernelArgs[consts.KernelArgRdmaExclusive]).To(BeFalse())

				changed, err := genericPlugin.CheckStatusChanges(rdmaState)
				Expect(err).ToNot(HaveOccurred())
				Expect(changed).To(BeTrue())
			})
			It("should enable rdma exclusive mode", func() {
				hostHelper.EXPECT().SetRDMASubsystem(consts.RdmaSubsystemModeExclusive).Return(nil)
				rdmaState.Spec.System.RdmaMode = consts.RdmaSubsystemModeExclusive
				err := genericPlugin.(*GenericPlugin).configRdmaKernelArg(rdmaState)
				Expect(err).ToNot(HaveOccurred())

				Expect(genericPlugin.(*GenericPlugin).DesiredKernelArgs[consts.KernelArgRdmaShared]).To(BeFalse())
				Expect(genericPlugin.(*GenericPlugin).DesiredKernelArgs[consts.KernelArgRdmaExclusive]).To(BeTrue())

				changed, err := genericPlugin.CheckStatusChanges(rdmaState)
				Expect(err).ToNot(HaveOccurred())
				Expect(changed).To(BeTrue())
			})
			It("should not configure RDMA kernel args", func() {
				hostHelper.EXPECT().SetRDMASubsystem("").Return(nil)
				rdmaState.Spec.System = sriovnetworkv1.System{}
				err := genericPlugin.(*GenericPlugin).configRdmaKernelArg(rdmaState)
				Expect(err).ToNot(HaveOccurred())

				Expect(genericPlugin.(*GenericPlugin).DesiredKernelArgs[consts.KernelArgRdmaShared]).To(BeFalse())
				Expect(genericPlugin.(*GenericPlugin).DesiredKernelArgs[consts.KernelArgRdmaExclusive]).To(BeFalse())

				changed, err := genericPlugin.CheckStatusChanges(rdmaState)
				Expect(err).ToNot(HaveOccurred())
				Expect(changed).To(BeFalse())
			})
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

		It("should load vhost_vdpa driver", func() {
			networkNodeState := &sriovnetworkv1.SriovNetworkNodeState{
				Spec: sriovnetworkv1.SriovNetworkNodeStateSpec{
					Interfaces: sriovnetworkv1.Interfaces{{
						PciAddress: "0000:00:00.0",
						NumVfs:     2,
						Mtu:        1500,
						VfGroups: []sriovnetworkv1.VfGroup{{
							DeviceType:   "netdevice",
							VdpaType:     "vhost",
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
			driverState := driverStateMap[VhostVdpa]
			concretePlugin.loadDriverForTests(networkNodeState)
			Expect(driverState.DriverLoaded).To(BeTrue())
		})
	})
	It("should not drain - bridge config", func() {
		networkNodeState := &sriovnetworkv1.SriovNetworkNodeState{
			Spec: sriovnetworkv1.SriovNetworkNodeStateSpec{
				Interfaces: sriovnetworkv1.Interfaces{{
					PciAddress:  "0000:d8:00.0",
					NumVfs:      1,
					Name:        "enp216s0f0np0",
					EswitchMode: "switchdev",
					VfGroups: []sriovnetworkv1.VfGroup{{
						DeviceType:   "netdevice",
						PolicyName:   "policy-1",
						ResourceName: "resource-1",
						VfRange:      "0-0",
					}}}},
				Bridges: sriovnetworkv1.Bridges{
					OVS: []sriovnetworkv1.OVSConfigExt{{
						Name: "br-0000_d8_00.0",
						Uplinks: []sriovnetworkv1.OVSUplinkConfigExt{{
							PciAddress: "0000:d8:00.0",
							Name:       "enp216s0f0np0",
						}},
					}},
				}},
			Status: sriovnetworkv1.SriovNetworkNodeStateStatus{
				Interfaces: sriovnetworkv1.InterfaceExts{{
					PciAddress:     "0000:d8:00.0",
					NumVfs:         1,
					TotalVfs:       1,
					DeviceID:       "a2d6",
					Vendor:         "15b3",
					Name:           "enp216s0f0np0",
					Mtu:            1500,
					Mac:            "0c:42:a1:55:ee:46",
					Driver:         "mlx5_core",
					EswitchMode:    "switchdev",
					LinkSpeed:      "25000 Mb/s",
					LinkType:       "ETH",
					LinkAdminState: "up",
					VFs: []sriovnetworkv1.VirtualFunction{{
						PciAddress: "0000:d8:00.2",
						DeviceID:   "101e",
						Vendor:     "15b3",
						VfID:       0,
						Name:       "enp216s0f0v0",
						Mtu:        1500,
						Mac:        "8e:d6:2c:62:87:1b",
						Driver:     "mlx5_core",
					}},
				}},
				Bridges: sriovnetworkv1.Bridges{
					OVS: []sriovnetworkv1.OVSConfigExt{{
						Name: "br-0000_d8_00.0",
						Uplinks: []sriovnetworkv1.OVSUplinkConfigExt{{
							PciAddress: "0000:d8:00.0",
							Name:       "enp216s0f0np0",
						}},
					}},
				},
			}}
		needDrain, needReboot, err := genericPlugin.OnNodeStateChange(networkNodeState)
		Expect(err).ToNot(HaveOccurred())
		Expect(needReboot).To(BeFalse())
		Expect(needDrain).To(BeFalse())
	})
	It("should drain - bridge config mismatch", func() {
		networkNodeState := &sriovnetworkv1.SriovNetworkNodeState{
			Spec: sriovnetworkv1.SriovNetworkNodeStateSpec{
				Interfaces: sriovnetworkv1.Interfaces{{
					PciAddress:  "0000:d8:00.0",
					NumVfs:      1,
					Name:        "enp216s0f0np0",
					EswitchMode: "switchdev",
					VfGroups: []sriovnetworkv1.VfGroup{{
						DeviceType:   "netdevice",
						PolicyName:   "policy-1",
						ResourceName: "resource-1",
						VfRange:      "0-0",
					}}}},
				Bridges: sriovnetworkv1.Bridges{
					OVS: []sriovnetworkv1.OVSConfigExt{{
						Name: "br-0000_d8_00.0",
						Bridge: sriovnetworkv1.OVSBridgeConfig{
							DatapathType: "netdev",
						},
						Uplinks: []sriovnetworkv1.OVSUplinkConfigExt{{
							PciAddress: "0000:d8:00.0",
							Name:       "enp216s0f0np0",
							Interface: sriovnetworkv1.OVSInterfaceConfig{
								Type: "dpdk",
							},
						}},
					}},
				}},
			Status: sriovnetworkv1.SriovNetworkNodeStateStatus{
				Interfaces: sriovnetworkv1.InterfaceExts{{
					PciAddress:     "0000:d8:00.0",
					NumVfs:         1,
					TotalVfs:       1,
					DeviceID:       "a2d6",
					Vendor:         "15b3",
					Name:           "enp216s0f0np0",
					Mtu:            1500,
					Mac:            "0c:42:a1:55:ee:46",
					Driver:         "mlx5_core",
					EswitchMode:    "switchdev",
					LinkSpeed:      "25000 Mb/s",
					LinkType:       "ETH",
					LinkAdminState: "up",
					VFs: []sriovnetworkv1.VirtualFunction{{
						PciAddress: "0000:d8:00.2",
						DeviceID:   "101e",
						Vendor:     "15b3",
						VfID:       0,
						Name:       "enp216s0f0v0",
						Mtu:        1500,
						Mac:        "8e:d6:2c:62:87:1b",
						Driver:     "mlx5_core",
					}},
				}},
				Bridges: sriovnetworkv1.Bridges{
					OVS: []sriovnetworkv1.OVSConfigExt{{
						Name: "br-0000_d8_00.0",
						Uplinks: []sriovnetworkv1.OVSUplinkConfigExt{{
							PciAddress: "0000:d8:00.0",
							Name:       "enp216s0f0np0",
						}},
					}},
				},
			}}
		needDrain, needReboot, err := genericPlugin.OnNodeStateChange(networkNodeState)
		Expect(err).ToNot(HaveOccurred())
		Expect(needReboot).To(BeFalse())
		Expect(needDrain).To(BeTrue())
	})
	It("check status - bridge config mismatch", func() {
		networkNodeState := &sriovnetworkv1.SriovNetworkNodeState{
			Spec: sriovnetworkv1.SriovNetworkNodeStateSpec{
				Interfaces: sriovnetworkv1.Interfaces{{
					PciAddress:  "0000:d8:00.0",
					NumVfs:      1,
					Name:        "enp216s0f0np0",
					EswitchMode: "switchdev",
					VfGroups: []sriovnetworkv1.VfGroup{{
						DeviceType:   "netdevice",
						PolicyName:   "policy-1",
						ResourceName: "resource-1",
						VfRange:      "0-0",
					}}}},
				Bridges: sriovnetworkv1.Bridges{
					OVS: []sriovnetworkv1.OVSConfigExt{{
						Name: "br-0000_d8_00.0",
						Bridge: sriovnetworkv1.OVSBridgeConfig{
							DatapathType: "netdev",
						},
						Uplinks: []sriovnetworkv1.OVSUplinkConfigExt{{
							PciAddress: "0000:d8:00.0",
							Name:       "enp216s0f0np0",
							Interface: sriovnetworkv1.OVSInterfaceConfig{
								Type: "dpdk",
							},
						}},
					}},
				}},
			Status: sriovnetworkv1.SriovNetworkNodeStateStatus{
				Interfaces: sriovnetworkv1.InterfaceExts{{
					PciAddress:     "0000:d8:00.0",
					NumVfs:         1,
					TotalVfs:       1,
					DeviceID:       "a2d6",
					Vendor:         "15b3",
					Name:           "enp216s0f0np0",
					Mtu:            1500,
					Mac:            "0c:42:a1:55:ee:46",
					Driver:         "mlx5_core",
					EswitchMode:    "switchdev",
					LinkSpeed:      "25000 Mb/s",
					LinkType:       "ETH",
					LinkAdminState: "up",
					VFs: []sriovnetworkv1.VirtualFunction{{
						PciAddress: "0000:d8:00.2",
						DeviceID:   "101e",
						Vendor:     "15b3",
						VfID:       0,
						Name:       "enp216s0f0v0",
						Mtu:        1500,
						Mac:        "8e:d6:2c:62:87:1b",
						Driver:     "mlx5_core",
					}},
				}},
				Bridges: sriovnetworkv1.Bridges{
					OVS: []sriovnetworkv1.OVSConfigExt{{
						Name: "br-0000_d8_00.0",
						Uplinks: []sriovnetworkv1.OVSUplinkConfigExt{{
							PciAddress: "0000:d8:00.0",
							Name:       "enp216s0f0np0",
						}},
					}},
				},
			}}
		updated, err := genericPlugin.CheckStatusChanges(networkNodeState)
		Expect(err).ToNot(HaveOccurred())
		Expect(updated).To(BeTrue())
	})
})
