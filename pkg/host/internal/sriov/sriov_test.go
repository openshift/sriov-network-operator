package sriov

import (
	"fmt"
	"net"
	"strconv"
	"syscall"

	"github.com/golang/mock/gomock"
	"github.com/jaypipes/ghw"
	"github.com/jaypipes/pcidb"
	"github.com/vishvananda/netlink"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	sriovnetworkv1 "github.com/k8snetworkplumbingwg/sriov-network-operator/api/v1"
	dputilsMockPkg "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/host/internal/lib/dputils/mock"
	ghwMockPkg "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/host/internal/lib/ghw/mock"
	netlinkMockPkg "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/host/internal/lib/netlink/mock"
	sriovnetMockPkg "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/host/internal/lib/sriovnet/mock"
	hostMockPkg "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/host/mock"
	hostStoreMockPkg "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/host/store/mock"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/host/types"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/test/util/fakefilesystem"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/test/util/helpers"
)

var _ = Describe("SRIOV", func() {
	var (
		s                types.SriovInterface
		netlinkLibMock   *netlinkMockPkg.MockNetlinkLib
		dputilsLibMock   *dputilsMockPkg.MockDPUtilsLib
		sriovnetLibMock  *sriovnetMockPkg.MockSriovnetLib
		ghwLibMock       *ghwMockPkg.MockGHWLib
		hostMock         *hostMockPkg.MockHostManagerInterface
		storeManagerMode *hostStoreMockPkg.MockManagerInterface

		testCtrl *gomock.Controller

		testError = fmt.Errorf("test")
	)
	BeforeEach(func() {
		testCtrl = gomock.NewController(GinkgoT())
		netlinkLibMock = netlinkMockPkg.NewMockNetlinkLib(testCtrl)
		dputilsLibMock = dputilsMockPkg.NewMockDPUtilsLib(testCtrl)
		sriovnetLibMock = sriovnetMockPkg.NewMockSriovnetLib(testCtrl)
		ghwLibMock = ghwMockPkg.NewMockGHWLib(testCtrl)

		hostMock = hostMockPkg.NewMockHostManagerInterface(testCtrl)
		storeManagerMode = hostStoreMockPkg.NewMockManagerInterface(testCtrl)

		s = New(nil, hostMock, hostMock, hostMock, hostMock, netlinkLibMock, dputilsLibMock, sriovnetLibMock, ghwLibMock)
	})

	AfterEach(func() {
		testCtrl.Finish()
	})

	Context("DiscoverSriovDevices", func() {
		var (
			ghwInfoMock *ghwMockPkg.MockInfo
		)
		BeforeEach(func() {
			ghwInfoMock = ghwMockPkg.NewMockInfo(testCtrl)
			ghwLibMock.EXPECT().PCI().Return(ghwInfoMock, nil)
			origNicMap := sriovnetworkv1.NicIDMap
			sriovnetworkv1.InitNicIDMapFromList([]string{
				"15b3 101d 101e",
			})
			DeferCleanup(func() {
				sriovnetworkv1.NicIDMap = origNicMap
			})
		})

		It("discovered", func() {
			ghwInfoMock.EXPECT().ListDevices().Return(getTestPCIDevices())
			dputilsLibMock.EXPECT().IsSriovVF("0000:d8:00.0").Return(false)
			dputilsLibMock.EXPECT().IsSriovVF("0000:d8:00.2").Return(true)
			dputilsLibMock.EXPECT().IsSriovVF("0000:3b:00.0").Return(false)
			dputilsLibMock.EXPECT().GetDriverName("0000:d8:00.0").Return("mlx5_core", nil)
			hostMock.EXPECT().TryGetInterfaceName("0000:d8:00.0").Return("enp216s0f0np0")

			pfLinkMock := netlinkMockPkg.NewMockLink(testCtrl)
			netlinkLibMock.EXPECT().LinkByName("enp216s0f0np0").Return(pfLinkMock, nil)

			mac, _ := net.ParseMAC("08:c0:eb:70:74:4e")
			pfLinkMock.EXPECT().Attrs().Return(&netlink.LinkAttrs{
				MTU:          1500,
				HardwareAddr: mac,
				EncapType:    "ether",
			}).MinTimes(1)
			hostMock.EXPECT().GetNetDevLinkSpeed("enp216s0f0np0").Return("100000 Mb/s")
			storeManagerMode.EXPECT().LoadPfsStatus("0000:d8:00.0").Return(nil, false, nil)

			dputilsLibMock.EXPECT().IsSriovPF("0000:d8:00.0").Return(true)
			dputilsLibMock.EXPECT().GetSriovVFcapacity("0000:d8:00.0").Return(1)
			dputilsLibMock.EXPECT().GetVFconfigured("0000:d8:00.0").Return(1)
			netlinkLibMock.EXPECT().DevLinkGetDeviceByName("pci", "0000:d8:00.0").Return(
				&netlink.DevlinkDevice{Attrs: netlink.DevlinkDevAttrs{Eswitch: netlink.DevlinkDevEswitchAttr{Mode: "switchdev"}}}, nil)
			dputilsLibMock.EXPECT().SriovConfigured("0000:d8:00.0").Return(true)
			dputilsLibMock.EXPECT().GetVFList("0000:d8:00.0").Return([]string{"0000:d8:00.2"}, nil)
			dputilsLibMock.EXPECT().GetDriverName("0000:d8:00.2").Return("mlx5_core", nil)
			dputilsLibMock.EXPECT().GetVFID("0000:d8:00.2").Return(0, nil)
			hostMock.EXPECT().DiscoverVDPAType("0000:d8:00.2").Return("")

			hostMock.EXPECT().TryGetInterfaceName("0000:d8:00.2").Return("enp216s0f0v0")
			vfLinkMock := netlinkMockPkg.NewMockLink(testCtrl)
			netlinkLibMock.EXPECT().LinkByName("enp216s0f0v0").Return(vfLinkMock, nil)

			mac, _ = net.ParseMAC("4e:fd:3d:08:59:b1")
			vfLinkMock.EXPECT().Attrs().Return(&netlink.LinkAttrs{
				MTU:          1500,
				HardwareAddr: mac,
			}).MinTimes(1)

			sriovnetLibMock.EXPECT().GetVfRepresentor("enp216s0f0np0", 0).Return("enp216s0f0np0_0", nil)

			ret, err := s.DiscoverSriovDevices(storeManagerMode)
			Expect(err).NotTo(HaveOccurred())
			Expect(ret).To(HaveLen(1))
			Expect(ret[0]).To(Equal(sriovnetworkv1.InterfaceExt{
				Name:              "enp216s0f0np0",
				Mac:               "08:c0:eb:70:74:4e",
				Driver:            "mlx5_core",
				PciAddress:        "0000:d8:00.0",
				Vendor:            "15b3",
				DeviceID:          "101d",
				Mtu:               1500,
				NumVfs:            1,
				LinkSpeed:         "100000 Mb/s",
				LinkType:          "ETH",
				EswitchMode:       "switchdev",
				ExternallyManaged: false,
				TotalVfs:          1,
				VFs: []sriovnetworkv1.VirtualFunction{{
					Name:            "enp216s0f0v0",
					Mac:             "4e:fd:3d:08:59:b1",
					Driver:          "mlx5_core",
					PciAddress:      "0000:d8:00.2",
					Vendor:          "15b3",
					DeviceID:        "101e",
					Mtu:             1500,
					VfID:            0,
					RepresentorName: "enp216s0f0np0_0",
				}},
			}))
		})
	})

	Context("SetSriovNumVfs", func() {
		It("set", func() {
			helpers.GinkgoConfigureFakeFS(&fakefilesystem.FS{
				Dirs:  []string{"/sys/bus/pci/devices/0000:d8:00.0"},
				Files: map[string][]byte{"/sys/bus/pci/devices/0000:d8:00.0/sriov_numvfs": {}},
			})
			Expect(s.SetSriovNumVfs("0000:d8:00.0", 5)).NotTo(HaveOccurred())
			helpers.GinkgoAssertFileContentsEquals("/sys/bus/pci/devices/0000:d8:00.0/sriov_numvfs", strconv.Itoa(5))
		})
		It("fail - no such device", func() {
			Expect(s.SetSriovNumVfs("0000:d8:00.0", 5)).To(HaveOccurred())
		})
	})

	Context("GetNicSriovMode", func() {
		It("devlink returns info", func() {
			netlinkLibMock.EXPECT().DevLinkGetDeviceByName("pci", "0000:d8:00.0").Return(
				&netlink.DevlinkDevice{Attrs: netlink.DevlinkDevAttrs{Eswitch: netlink.DevlinkDevEswitchAttr{Mode: "switchdev"}}},
				nil)
			mode := s.GetNicSriovMode("0000:d8:00.0")
			Expect(mode).To(Equal("switchdev"))
		})
		It("devlink returns error", func() {
			netlinkLibMock.EXPECT().DevLinkGetDeviceByName("pci", "0000:d8:00.0").Return(nil, testError)
			mode := s.GetNicSriovMode("0000:d8:00.0")

			Expect(mode).To(Equal("legacy"))
		})
		It("devlink not supported - fail to get name", func() {
			netlinkLibMock.EXPECT().DevLinkGetDeviceByName("pci", "0000:d8:00.0").Return(nil, syscall.ENODEV)
			mode := s.GetNicSriovMode("0000:d8:00.0")
			Expect(mode).To(Equal("legacy"))
		})
	})

	Context("SetNicSriovMode", func() {
		It("set", func() {
			testDev := &netlink.DevlinkDevice{}
			netlinkLibMock.EXPECT().DevLinkGetDeviceByName("pci", "0000:d8:00.0").Return(&netlink.DevlinkDevice{}, nil)
			netlinkLibMock.EXPECT().DevLinkSetEswitchMode(testDev, "legacy").Return(nil)
			Expect(s.SetNicSriovMode("0000:d8:00.0", "legacy")).NotTo(HaveOccurred())
		})
		It("fail to get dev", func() {
			netlinkLibMock.EXPECT().DevLinkGetDeviceByName("pci", "0000:d8:00.0").Return(nil, testError)
			Expect(s.SetNicSriovMode("0000:d8:00.0", "legacy")).To(MatchError(testError))
		})
		It("fail to set mode", func() {
			testDev := &netlink.DevlinkDevice{}
			netlinkLibMock.EXPECT().DevLinkGetDeviceByName("pci", "0000:d8:00.0").Return(&netlink.DevlinkDevice{}, nil)
			netlinkLibMock.EXPECT().DevLinkSetEswitchMode(testDev, "legacy").Return(testError)
			Expect(s.SetNicSriovMode("0000:d8:00.0", "legacy")).To(MatchError(testError))
		})
	})

	Context("ConfigSriovInterfaces", func() {
		It("should configure", func() {
			helpers.GinkgoConfigureFakeFS(&fakefilesystem.FS{
				Dirs:  []string{"/sys/bus/pci/devices/0000:d8:00.0"},
				Files: map[string][]byte{"/sys/bus/pci/devices/0000:d8:00.0/sriov_numvfs": {}},
			})

			dputilsLibMock.EXPECT().GetSriovVFcapacity("0000:d8:00.0").Return(2)
			dputilsLibMock.EXPECT().GetVFconfigured("0000:d8:00.0").Return(0)
			netlinkLibMock.EXPECT().DevLinkGetDeviceByName("pci", "0000:d8:00.0").Return(&netlink.DevlinkDevice{
				Attrs: netlink.DevlinkDevAttrs{Eswitch: netlink.DevlinkDevEswitchAttr{Mode: "legacy"}}}, nil)
			hostMock.EXPECT().RemoveDisableNMUdevRule("0000:d8:00.0").Return(nil)
			hostMock.EXPECT().RemovePersistPFNameUdevRule("0000:d8:00.0").Return(nil)
			hostMock.EXPECT().RemoveVfRepresentorUdevRule("0000:d8:00.0").Return(nil)
			hostMock.EXPECT().AddDisableNMUdevRule("0000:d8:00.0").Return(nil)
			dputilsLibMock.EXPECT().GetVFList("0000:d8:00.0").Return([]string{"0000:d8:00.2", "0000:d8:00.3"}, nil)
			pfLinkMock := netlinkMockPkg.NewMockLink(testCtrl)
			netlinkLibMock.EXPECT().LinkByName("enp216s0f0np0").Return(pfLinkMock, nil).Times(3)
			pfLinkMock.EXPECT().Attrs().Return(&netlink.LinkAttrs{OperState: netlink.OperDown, EncapType: "ether"}).Times(2)
			netlinkLibMock.EXPECT().LinkSetUp(pfLinkMock).Return(nil)

			dputilsLibMock.EXPECT().GetVFID("0000:d8:00.2").Return(0, nil).Times(2)
			hostMock.EXPECT().HasDriver("0000:d8:00.2").Return(false, "")
			hostMock.EXPECT().BindDefaultDriver("0000:d8:00.2").Return(nil)
			hostMock.EXPECT().HasDriver("0000:d8:00.2").Return(true, "test")
			hostMock.EXPECT().UnbindDriverIfNeeded("0000:d8:00.2", true).Return(nil)
			hostMock.EXPECT().BindDefaultDriver("0000:d8:00.2").Return(nil)
			hostMock.EXPECT().SetNetdevMTU("0000:d8:00.2", 2000).Return(nil)
			hostMock.EXPECT().TryGetInterfaceName("0000:d8:00.2").Return("enp216s0f0_0")
			vf0LinkMock := netlinkMockPkg.NewMockLink(testCtrl)
			vf0Mac, _ := net.ParseMAC("02:42:19:51:2f:af")
			vf0LinkMock.EXPECT().Attrs().Return(&netlink.LinkAttrs{HardwareAddr: vf0Mac})
			netlinkLibMock.EXPECT().LinkByName("enp216s0f0_0").Return(vf0LinkMock, nil)
			netlinkLibMock.EXPECT().LinkSetVfHardwareAddr(vf0LinkMock, 0, vf0Mac).Return(nil)

			dputilsLibMock.EXPECT().GetVFID("0000:d8:00.3").Return(1, nil)
			hostMock.EXPECT().HasDriver("0000:d8:00.3").Return(true, "vfio-pci").Times(2)
			hostMock.EXPECT().UnbindDriverIfNeeded("0000:d8:00.3", false).Return(nil)
			hostMock.EXPECT().BindDpdkDriver("0000:d8:00.3", "vfio-pci").Return(nil)

			storeManagerMode.EXPECT().SaveLastPfAppliedStatus(gomock.Any()).Return(nil)

			Expect(s.ConfigSriovInterfaces(storeManagerMode,
				[]sriovnetworkv1.Interface{{
					Name:       "enp216s0f0np0",
					PciAddress: "0000:d8:00.0",
					NumVfs:     2,
					VfGroups: []sriovnetworkv1.VfGroup{
						{
							VfRange:      "0-0",
							ResourceName: "test-resource0",
							PolicyName:   "test-policy0",
							Mtu:          2000,
							IsRdma:       true,
						},
						{
							VfRange:      "1-1",
							ResourceName: "test-resource1",
							PolicyName:   "test-policy1",
							Mtu:          1600,
							IsRdma:       false,
							DeviceType:   "vfio-pci",
						}},
				}},
				[]sriovnetworkv1.InterfaceExt{{PciAddress: "0000:d8:00.0"}, {PciAddress: "0000:d8:00.1"}},
				false)).NotTo(HaveOccurred())
			helpers.GinkgoAssertFileContentsEquals("/sys/bus/pci/devices/0000:d8:00.0/sriov_numvfs", "2")
		})
		It("should configure IB", func() {
			helpers.GinkgoConfigureFakeFS(&fakefilesystem.FS{
				Dirs:  []string{"/sys/bus/pci/devices/0000:d8:00.0"},
				Files: map[string][]byte{"/sys/bus/pci/devices/0000:d8:00.0/sriov_numvfs": {}},
			})

			dputilsLibMock.EXPECT().GetSriovVFcapacity("0000:d8:00.0").Return(1)
			dputilsLibMock.EXPECT().GetVFconfigured("0000:d8:00.0").Return(0)
			netlinkLibMock.EXPECT().DevLinkGetDeviceByName("pci", "0000:d8:00.0").Return(&netlink.DevlinkDevice{
				Attrs: netlink.DevlinkDevAttrs{Eswitch: netlink.DevlinkDevEswitchAttr{Mode: "legacy"}}}, nil)
			hostMock.EXPECT().RemoveDisableNMUdevRule("0000:d8:00.0").Return(nil)
			hostMock.EXPECT().RemovePersistPFNameUdevRule("0000:d8:00.0").Return(nil)
			hostMock.EXPECT().RemoveVfRepresentorUdevRule("0000:d8:00.0").Return(nil)
			hostMock.EXPECT().AddDisableNMUdevRule("0000:d8:00.0").Return(nil)
			dputilsLibMock.EXPECT().GetVFList("0000:d8:00.0").Return([]string{"0000:d8:00.2"}, nil)
			pfLinkMock := netlinkMockPkg.NewMockLink(testCtrl)
			netlinkLibMock.EXPECT().LinkByName("enp216s0f0np0").Return(pfLinkMock, nil).Times(2)
			pfLinkMock.EXPECT().Attrs().Return(&netlink.LinkAttrs{OperState: netlink.OperDown})
			netlinkLibMock.EXPECT().LinkSetUp(pfLinkMock).Return(nil)

			dputilsLibMock.EXPECT().GetVFID("0000:d8:00.2").Return(0, nil).Times(2)
			hostMock.EXPECT().Unbind("0000:d8:00.2").Return(nil)
			hostMock.EXPECT().HasDriver("0000:d8:00.2").Return(true, "test").Times(2)
			hostMock.EXPECT().UnbindDriverIfNeeded("0000:d8:00.2", true).Return(nil)
			hostMock.EXPECT().BindDefaultDriver("0000:d8:00.2").Return(nil)
			hostMock.EXPECT().SetNetdevMTU("0000:d8:00.2", 2000).Return(nil)
			vf0LinkMock := netlinkMockPkg.NewMockLink(testCtrl)
			netlinkLibMock.EXPECT().LinkSetVfNodeGUID(vf0LinkMock, 0, gomock.Any()).Return(nil)
			netlinkLibMock.EXPECT().LinkSetVfPortGUID(vf0LinkMock, 0, gomock.Any()).Return(nil)

			storeManagerMode.EXPECT().SaveLastPfAppliedStatus(gomock.Any()).Return(nil)

			Expect(s.ConfigSriovInterfaces(storeManagerMode,
				[]sriovnetworkv1.Interface{{
					Name:       "enp216s0f0np0",
					PciAddress: "0000:d8:00.0",
					NumVfs:     1,
					LinkType:   "IB",
					VfGroups: []sriovnetworkv1.VfGroup{
						{
							VfRange:      "0-0",
							ResourceName: "test-resource0",
							PolicyName:   "test-policy0",
							Mtu:          2000,
							IsRdma:       true,
						}},
				}},
				[]sriovnetworkv1.InterfaceExt{{PciAddress: "0000:d8:00.0"}},
				false)).NotTo(HaveOccurred())
			helpers.GinkgoAssertFileContentsEquals("/sys/bus/pci/devices/0000:d8:00.0/sriov_numvfs", "1")
		})

		It("should configure switchdev", func() {
			helpers.GinkgoConfigureFakeFS(&fakefilesystem.FS{
				Dirs:  []string{"/sys/bus/pci/devices/0000:d8:00.0"},
				Files: map[string][]byte{"/sys/bus/pci/devices/0000:d8:00.0/sriov_numvfs": {}},
			})

			dputilsLibMock.EXPECT().GetSriovVFcapacity("0000:d8:00.0").Return(1)
			dputilsLibMock.EXPECT().GetVFconfigured("0000:d8:00.0").Return(0)
			hostMock.EXPECT().RemoveDisableNMUdevRule("0000:d8:00.0").Return(nil)
			hostMock.EXPECT().RemovePersistPFNameUdevRule("0000:d8:00.0").Return(nil)
			hostMock.EXPECT().RemoveVfRepresentorUdevRule("0000:d8:00.0").Return(nil)
			hostMock.EXPECT().AddDisableNMUdevRule("0000:d8:00.0").Return(nil)
			hostMock.EXPECT().AddPersistPFNameUdevRule("0000:d8:00.0", "enp216s0f0np0").Return(nil)
			hostMock.EXPECT().EnableHwTcOffload("enp216s0f0np0").Return(nil)
			hostMock.EXPECT().GetDevlinkDeviceParam("0000:d8:00.0", "flow_steering_mode").Return("", syscall.EINVAL)
			dputilsLibMock.EXPECT().GetVFList("0000:d8:00.0").Return([]string{"0000:d8:00.2"}, nil).Times(2)
			pfLinkMock := netlinkMockPkg.NewMockLink(testCtrl)
			netlinkLibMock.EXPECT().LinkByName("enp216s0f0np0").Return(pfLinkMock, nil).Times(2)
			pfLinkMock.EXPECT().Attrs().Return(&netlink.LinkAttrs{OperState: netlink.OperDown})
			netlinkLibMock.EXPECT().LinkSetUp(pfLinkMock).Return(nil)
			netlinkLibMock.EXPECT().DevLinkGetDeviceByName("pci", "0000:d8:00.0").Return(&netlink.DevlinkDevice{
				Attrs: netlink.DevlinkDevAttrs{Eswitch: netlink.DevlinkDevEswitchAttr{Mode: "legacy"}}}, nil).Times(2)
			netlinkLibMock.EXPECT().DevLinkSetEswitchMode(gomock.Any(), "switchdev").Return(nil)

			dputilsLibMock.EXPECT().GetVFID("0000:d8:00.2").Return(0, nil).Times(2)
			hostMock.EXPECT().Unbind("0000:d8:00.2").Return(nil)
			hostMock.EXPECT().HasDriver("0000:d8:00.2").Return(false, "")
			hostMock.EXPECT().BindDefaultDriver("0000:d8:00.2").Return(nil)
			hostMock.EXPECT().HasDriver("0000:d8:00.2").Return(true, "test")
			hostMock.EXPECT().UnbindDriverIfNeeded("0000:d8:00.2", true).Return(nil)
			hostMock.EXPECT().BindDefaultDriver("0000:d8:00.2").Return(nil)
			hostMock.EXPECT().SetNetdevMTU("0000:d8:00.2", 2000).Return(nil)
			hostMock.EXPECT().TryGetInterfaceName("0000:d8:00.2").Return("enp216s0f0_0")
			vf0LinkMock := netlinkMockPkg.NewMockLink(testCtrl)
			vf0Mac, _ := net.ParseMAC("02:42:19:51:2f:af")
			vf0LinkMock.EXPECT().Attrs().Return(&netlink.LinkAttrs{HardwareAddr: vf0Mac})
			netlinkLibMock.EXPECT().LinkByName("enp216s0f0_0").Return(vf0LinkMock, nil)
			netlinkLibMock.EXPECT().LinkSetVfHardwareAddr(vf0LinkMock, 0, vf0Mac).Return(nil)
			hostMock.EXPECT().GetPhysPortName("enp216s0f0np0").Return("p0", nil)
			hostMock.EXPECT().GetPhysSwitchID("enp216s0f0np0").Return("7cfe90ff2cc0", nil)
			hostMock.EXPECT().AddVfRepresentorUdevRule("0000:d8:00.0", "enp216s0f0np0", "7cfe90ff2cc0", "p0").Return(nil)
			hostMock.EXPECT().CreateVDPADevice("0000:d8:00.2", "vhost_vdpa")
			hostMock.EXPECT().LoadUdevRules().Return(nil)

			storeManagerMode.EXPECT().SaveLastPfAppliedStatus(gomock.Any()).Return(nil)

			Expect(s.ConfigSriovInterfaces(storeManagerMode,
				[]sriovnetworkv1.Interface{{
					Name:        "enp216s0f0np0",
					PciAddress:  "0000:d8:00.0",
					NumVfs:      1,
					LinkType:    "ETH",
					EswitchMode: "switchdev",
					VfGroups: []sriovnetworkv1.VfGroup{
						{
							VfRange:      "0-0",
							ResourceName: "test-resource0",
							PolicyName:   "test-policy0",
							Mtu:          2000,
							IsRdma:       true,
							VdpaType:     "vhost_vdpa",
						}},
				}},
				[]sriovnetworkv1.InterfaceExt{{PciAddress: "0000:d8:00.0"}},
				false)).NotTo(HaveOccurred())
			helpers.GinkgoAssertFileContentsEquals("/sys/bus/pci/devices/0000:d8:00.0/sriov_numvfs", "1")
		})

		It("externally managed - wrong VF count", func() {
			dputilsLibMock.EXPECT().GetVFconfigured("0000:d8:00.0").Return(0)
			Expect(s.ConfigSriovInterfaces(storeManagerMode,
				[]sriovnetworkv1.Interface{{
					Name:              "enp216s0f0np0",
					PciAddress:        "0000:d8:00.0",
					NumVfs:            1,
					ExternallyManaged: true,
					VfGroups: []sriovnetworkv1.VfGroup{
						{
							VfRange:      "0-0",
							ResourceName: "test-resource0",
							PolicyName:   "test-policy0",
							Mtu:          2000,
							IsRdma:       true,
						}},
				}},
				[]sriovnetworkv1.InterfaceExt{{PciAddress: "0000:d8:00.0"}},
				false)).To(HaveOccurred())
		})

		It("externally managed - wrong MTU", func() {
			dputilsLibMock.EXPECT().GetVFconfigured("0000:d8:00.0").Return(1)
			netlinkLibMock.EXPECT().DevLinkGetDeviceByName("pci", "0000:d8:00.0").Return(
				&netlink.DevlinkDevice{Attrs: netlink.DevlinkDevAttrs{Eswitch: netlink.DevlinkDevEswitchAttr{Mode: "legacy"}}},
				nil)
			hostMock.EXPECT().GetNetdevMTU("0000:d8:00.0")
			Expect(s.ConfigSriovInterfaces(storeManagerMode,
				[]sriovnetworkv1.Interface{{
					Name:              "enp216s0f0np0",
					PciAddress:        "0000:d8:00.0",
					NumVfs:            1,
					Mtu:               2000,
					ExternallyManaged: true,
					VfGroups: []sriovnetworkv1.VfGroup{
						{
							VfRange:      "0-0",
							ResourceName: "test-resource0",
							PolicyName:   "test-policy0",
							IsRdma:       true,
						}},
				}},
				[]sriovnetworkv1.InterfaceExt{{PciAddress: "0000:d8:00.0"}},
				false)).To(HaveOccurred())
		})

		It("reset device", func() {
			helpers.GinkgoConfigureFakeFS(&fakefilesystem.FS{
				Dirs:  []string{"/sys/bus/pci/devices/0000:d8:00.0"},
				Files: map[string][]byte{"/sys/bus/pci/devices/0000:d8:00.0/sriov_numvfs": {}},
			})

			storeManagerMode.EXPECT().LoadPfsStatus("0000:d8:00.0").Return(&sriovnetworkv1.Interface{
				Name:       "enp216s0f0np0",
				PciAddress: "0000:d8:00.0",
				NumVfs:     2,
			}, true, nil)
			netlinkLibMock.EXPECT().DevLinkGetDeviceByName("pci", "0000:d8:00.0").Return(
				&netlink.DevlinkDevice{Attrs: netlink.DevlinkDevAttrs{Eswitch: netlink.DevlinkDevEswitchAttr{Mode: "legacy"}}},
				nil)
			hostMock.EXPECT().RemoveDisableNMUdevRule("0000:d8:00.0").Return(nil)
			hostMock.EXPECT().RemovePersistPFNameUdevRule("0000:d8:00.0").Return(nil)
			hostMock.EXPECT().RemoveVfRepresentorUdevRule("0000:d8:00.0").Return(nil)
			hostMock.EXPECT().SetNetdevMTU("0000:d8:00.0", 1500).Return(nil)

			Expect(s.ConfigSriovInterfaces(storeManagerMode,
				[]sriovnetworkv1.Interface{},
				[]sriovnetworkv1.InterfaceExt{
					{
						Name:       "enp216s0f0np0",
						PciAddress: "0000:d8:00.0",
						LinkType:   "ETH",
						NumVfs:     2,
						TotalVfs:   2,
					}}, false)).NotTo(HaveOccurred())
			helpers.GinkgoAssertFileContentsEquals("/sys/bus/pci/devices/0000:d8:00.0/sriov_numvfs", "0")
		})
		It("reset device - skip external", func() {
			storeManagerMode.EXPECT().LoadPfsStatus("0000:d8:00.0").Return(&sriovnetworkv1.Interface{
				Name:              "enp216s0f0np0",
				PciAddress:        "0000:d8:00.0",
				NumVfs:            2,
				ExternallyManaged: true,
			}, true, nil)
			Expect(s.ConfigSriovInterfaces(storeManagerMode,
				[]sriovnetworkv1.Interface{},
				[]sriovnetworkv1.InterfaceExt{
					{
						Name:       "enp216s0f0np0",
						PciAddress: "0000:d8:00.0",
						NumVfs:     2,
						TotalVfs:   2,
					}}, false)).NotTo(HaveOccurred())
		})
		It("should configure - skipVFConfiguration is true", func() {
			helpers.GinkgoConfigureFakeFS(&fakefilesystem.FS{
				Dirs:  []string{"/sys/bus/pci/devices/0000:d8:00.0"},
				Files: map[string][]byte{"/sys/bus/pci/devices/0000:d8:00.0/sriov_numvfs": {}},
			})

			dputilsLibMock.EXPECT().GetSriovVFcapacity("0000:d8:00.0").Return(2)
			dputilsLibMock.EXPECT().GetVFconfigured("0000:d8:00.0").Return(0)
			netlinkLibMock.EXPECT().DevLinkGetDeviceByName("pci", "0000:d8:00.0").Return(
				&netlink.DevlinkDevice{Attrs: netlink.DevlinkDevAttrs{Eswitch: netlink.DevlinkDevEswitchAttr{Mode: "legacy"}}},
				nil)
			hostMock.EXPECT().RemoveDisableNMUdevRule("0000:d8:00.0").Return(nil)
			hostMock.EXPECT().RemovePersistPFNameUdevRule("0000:d8:00.0").Return(nil)
			hostMock.EXPECT().RemoveVfRepresentorUdevRule("0000:d8:00.0").Return(nil)
			hostMock.EXPECT().AddDisableNMUdevRule("0000:d8:00.0").Return(nil)
			dputilsLibMock.EXPECT().GetVFList("0000:d8:00.0").Return([]string{"0000:d8:00.2", "0000:d8:00.3"}, nil)
			hostMock.EXPECT().Unbind("0000:d8:00.2").Return(nil)
			hostMock.EXPECT().Unbind("0000:d8:00.3").Return(nil)

			storeManagerMode.EXPECT().SaveLastPfAppliedStatus(gomock.Any()).Return(nil)

			Expect(s.ConfigSriovInterfaces(storeManagerMode,
				[]sriovnetworkv1.Interface{{
					Name:       "enp216s0f0np0",
					PciAddress: "0000:d8:00.0",
					NumVfs:     2,
					VfGroups: []sriovnetworkv1.VfGroup{
						{
							VfRange:      "0-0",
							ResourceName: "test-resource0",
							PolicyName:   "test-policy0",
							Mtu:          2000,
							IsRdma:       true,
						},
						{
							VfRange:      "1-1",
							ResourceName: "test-resource1",
							PolicyName:   "test-policy1",
							Mtu:          1600,
							IsRdma:       false,
							DeviceType:   "vfio-pci",
						}},
				}},
				[]sriovnetworkv1.InterfaceExt{{PciAddress: "0000:d8:00.0"}},
				true)).NotTo(HaveOccurred())
			helpers.GinkgoAssertFileContentsEquals("/sys/bus/pci/devices/0000:d8:00.0/sriov_numvfs", "2")
		})
	})
})

func getTestPCIDevices() []*ghw.PCIDevice {
	return []*ghw.PCIDevice{{
		Driver:  "mlx5_core",
		Address: "0000:d8:00.0",
		Vendor: &pcidb.Vendor{
			ID:   "15b3",
			Name: "Mellanox Technologies",
		},
		Product: &pcidb.Product{
			ID:   "101d",
			Name: "MT2892 Family [ConnectX-6 Dx]",
		},
		Revision: "0x00",
		Subsystem: &pcidb.Product{
			ID:   "0083",
			Name: "unknown",
		},
		Class: &pcidb.Class{
			ID:   "02",
			Name: "Network controller",
		},
		Subclass: &pcidb.Subclass{
			ID:   "00",
			Name: "Ethernet controller",
		},
		ProgrammingInterface: &pcidb.ProgrammingInterface{
			ID:   "00",
			Name: "unknonw",
		},
	},
		{
			Driver:  "mlx5_core",
			Address: "0000:d8:00.2",
			Vendor: &pcidb.Vendor{
				ID:   "15b3",
				Name: "Mellanox Technologies",
			},
			Product: &pcidb.Product{
				ID:   "101e",
				Name: "ConnectX Family mlx5Gen Virtual Function",
			},
			Revision: "0x00",
			Subsystem: &pcidb.Product{
				ID:   "0083",
				Name: "unknown",
			},
			Class: &pcidb.Class{
				ID:   "02",
				Name: "Network controller",
			},
			Subclass: &pcidb.Subclass{
				ID:   "00",
				Name: "Ethernet controller",
			},
			ProgrammingInterface: &pcidb.ProgrammingInterface{
				ID:   "00",
				Name: "unknonw",
			},
		},
		{
			Driver:  "mlx5_core",
			Address: "0000:3b:00.0",
			Vendor: &pcidb.Vendor{
				ID:   "15b3",
				Name: "Mellanox Technologies",
			},
			Product: &pcidb.Product{
				ID:   "aaaa", // not supported
				Name: "not supported",
			},
			Class: &pcidb.Class{
				ID:   "02",
				Name: "Network controller",
			},
		},
		{
			Driver:  "test",
			Address: "0000:d7:16.5",
			Vendor: &pcidb.Vendor{
				ID:   "8086",
				Name: "Intel Corporation",
			},
			Class: &pcidb.Class{
				ID:   "11", // not network device
				Name: "Signal processing controller",
			},
		},
	}
}
