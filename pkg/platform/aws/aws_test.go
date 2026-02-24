package aws

import (
	"errors"
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"go.uber.org/mock/gomock"

	sriovnetworkv1 "github.com/k8snetworkplumbingwg/sriov-network-operator/api/v1"
	mock_helper "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/helper/mock"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/vars"
)

var _ = Describe("Aws", func() {
	var (
		mockCtrl    *gomock.Controller
		awsPlatform *Aws
		hostHelper  *mock_helper.MockHostHelpersInterface
	)

	BeforeEach(func() {
		mockCtrl = gomock.NewController(GinkgoT())
		hostHelper = mock_helper.NewMockHostHelpersInterface(mockCtrl)
		var err error
		awsPlatform, err = New(hostHelper)
		Expect(err).NotTo(HaveOccurred())
		Expect(awsPlatform).NotTo(BeNil())
	})

	AfterEach(func() {
		mockCtrl.Finish()
	})

	Context("Init", func() {
		It("should fail if GetCheckPointNodeState fails", func() {
			hostHelper.EXPECT().GetCheckPointNodeState().Return(nil, errors.New("get checkpoint error"))
			err := awsPlatform.Init()
			Expect(err).To(MatchError("get checkpoint error"))
		})

		It("should create devices from metadata if node state is nil", func() {
			hostHelper.EXPECT().GetCheckPointNodeState().Return(nil, nil)
			// Mock the metadata call to avoid real HTTP requests
			// MAC addresses from metadata come with trailing "/"
			hostHelper.EXPECT().HTTPGetFetchData("http://169.254.169.254/latest/meta-data/network/interfaces/macs/").Return("0A:0B:0C:0D:0E:0F/", nil)
			hostHelper.EXPECT().HTTPGetFetchData("http://169.254.169.254/latest/meta-data/network/interfaces/macs/0A:0B:0C:0D:0E:0F/subnet-id").Return("subnet-090ab34e7af072e18", nil)
			hostHelper.EXPECT().DiscoverSriovVirtualDevices().Return([]sriovnetworkv1.InterfaceExt{{
				PciAddress: "0000:01:00.0",
				Mac:        "0A:0B:0C:0D:0E:0F"},
			}, nil)
			err := awsPlatform.Init()
			Expect(err).NotTo(HaveOccurred())
			Expect(len(awsPlatform.InitialDevicesInfo)).To(Equal(1))
			Expect(awsPlatform.InitialDevicesInfo[0].PciAddress).To(Equal("0000:01:00.0"))
			Expect(awsPlatform.InitialDevicesInfo[0].Mac).To(Equal("0A:0B:0C:0D:0E:0F"))
			Expect(awsPlatform.InitialDevicesInfo[0].NetFilter).To(Equal("aws/NetworkID:090ab34e7af072e18"))
			Expect(len(awsPlatform.InitialDevicesInfo[0].VFs)).To(Equal(1))
			Expect(awsPlatform.InitialDevicesInfo[0].VFs[0].PciAddress).To(Equal("0000:01:00.0"))
			Expect(awsPlatform.InitialDevicesInfo[0].VFs[0].Mac).To(Equal("0A:0B:0C:0D:0E:0F"))
		})

		It("should create devices from an existing node status", func() {
			nodeState := &sriovnetworkv1.SriovNetworkNodeState{
				Status: sriovnetworkv1.SriovNetworkNodeStateStatus{
					Interfaces: []sriovnetworkv1.InterfaceExt{{
						Name:       "eth0",
						PciAddress: "0000:01:00.0",
						Mac:        "0A:0B:0C:0D:0E:0F",
						NetFilter:  "aws/NetworkID:090ab34e7af072e18",
						VFs:        []sriovnetworkv1.VirtualFunction{{PciAddress: "0000:01:00.0", Mac: "0A:0B:0C:0D:0E:0F"}},
					}},
				},
			}
			hostHelper.EXPECT().GetCheckPointNodeState().Return(nodeState, nil)
			err := awsPlatform.Init()
			Expect(err).NotTo(HaveOccurred())

			hostHelper.EXPECT().GetNetdevMTU("0000:01:00.0").Return(1500)
			hostHelper.EXPECT().TryToGetVirtualInterfaceName("0000:01:00.0").Return("eth0")
			hostHelper.EXPECT().GetNetDevMac("eth0").Return("0A:0B:0C:0D:0E:0F")
			hostHelper.EXPECT().GetNetDevLinkSpeed("eth0").Return("10000")
			hostHelper.EXPECT().GetLinkType("eth0").Return("ether")
			hostHelper.EXPECT().HasDriver("0000:01:00.0").Return(true, "vfio-pci")

			ifs, err := awsPlatform.DiscoverSriovDevices()
			Expect(err).NotTo(HaveOccurred())
			Expect(ifs).To(HaveLen(1))
			Expect(ifs[0].PciAddress).To(Equal("0000:01:00.0"))
			Expect(ifs[0].Name).To(Equal("eth0"))
		})
	})

	Context("Plugin Management", func() {
		It("should return a virtual plugin", func() {
			main, additionals, err := awsPlatform.GetVendorPlugins(nil)
			Expect(err).NotTo(HaveOccurred())
			Expect(main).NotTo(BeNil())
			Expect(main.Name()).To(Equal("virtual"))
			Expect(additionals).To(BeEmpty())
		})

		It("should return an error for systemd plugin request", func() {
			_, err := awsPlatform.SystemdGetVendorPlugin("any-phase")
			Expect(err).To(MatchError(vars.ErrOperationNotSupportedByPlatform))
		})
	})

	Context("Bridge Discovery", func() {
		It("should always return an error as bridge discovery is not supported on AWS", func() {
			bridges, err := awsPlatform.DiscoverBridges()
			Expect(err).To(MatchError(vars.ErrOperationNotSupportedByPlatform))
			Expect(bridges).To(Equal(sriovnetworkv1.Bridges{}))
		})
	})

	Context("DiscoverSriovDevices", func() {
		BeforeEach(func() {
			// Pre-populate InitialDevicesInfo for tests
			nodeState := &sriovnetworkv1.SriovNetworkNodeState{
				Status: sriovnetworkv1.SriovNetworkNodeStateStatus{
					Interfaces: []sriovnetworkv1.InterfaceExt{
						{
							PciAddress: "0000:01:00.0", Vendor: "1d0f", DeviceID: "ec20",
							VFs: []sriovnetworkv1.VirtualFunction{{}},
						},
						{
							PciAddress: "0000:02:00.0", Vendor: "1d0f", DeviceID: "ec20",
							VFs: []sriovnetworkv1.VirtualFunction{{}},
						},
					},
				},
			}
			hostHelper.EXPECT().GetCheckPointNodeState().Return(nodeState, nil)
			Expect(awsPlatform.Init()).To(Succeed())
		})

		It("should discover devices and populate details correctly", func() {
			hostHelper.EXPECT().GetNetdevMTU("0000:01:00.0").Return(9001)
			hostHelper.EXPECT().TryToGetVirtualInterfaceName("0000:01:00.0").Return("eth0")
			hostHelper.EXPECT().GetNetDevMac("eth0").Return("0A:0B:0C:0D:0E:0F")
			hostHelper.EXPECT().GetNetDevLinkSpeed("eth0").Return("100000")
			hostHelper.EXPECT().GetLinkType("eth0").Return("ether")
			hostHelper.EXPECT().HasDriver("0000:01:00.0").Return(true, "vfio-pci")

			hostHelper.EXPECT().GetNetdevMTU("0000:02:00.0").Return(0)                  // No MTU
			hostHelper.EXPECT().TryToGetVirtualInterfaceName("0000:02:00.0").Return("") // No name
			hostHelper.EXPECT().HasDriver("0000:02:00.0").Return(true, "ena")

			devices, err := awsPlatform.DiscoverSriovDevices()
			Expect(err).NotTo(HaveOccurred())
			Expect(devices).To(HaveLen(2))

			// Assertions for the first, fully detailed device
			dev1 := devices[0]
			Expect(dev1.Driver).To(Equal("vfio-pci"))
			Expect(dev1.Mtu).To(Equal(9001))
			Expect(dev1.Name).To(Equal("eth0"))
			Expect(dev1.Mac).To(Equal("0A:0B:0C:0D:0E:0F"))
			Expect(dev1.LinkSpeed).To(Equal("100000"))
			Expect(dev1.LinkType).To(Equal("ether"))
			Expect(dev1.VFs).To(HaveLen(1))
			vf1 := dev1.VFs[0]
			Expect(vf1.PciAddress).To(Equal("0000:01:00.0"))
			Expect(vf1.Driver).To(Equal("vfio-pci"))
			Expect(vf1.VfID).To(Equal(0))
			Expect(vf1.Mtu).To(Equal(9001))
			Expect(vf1.Mac).To(Equal("0A:0B:0C:0D:0E:0F"))

			// Assertions for the second, partially detailed device
			dev2 := devices[1]
			Expect(dev2.Driver).To(Equal("ena"))
			Expect(dev2.Name).To(BeEmpty())
			Expect(dev2.Mtu).To(BeZero())
		})
	})

	Context("createDevicesInfo via Init", func() {
		BeforeEach(func() {
			hostHelper.EXPECT().GetCheckPointNodeState().Return(nil, nil)
		})

		It("should handle HTTP error when fetching MACs", func() {
			hostHelper.EXPECT().HTTPGetFetchData("http://169.254.169.254/latest/meta-data/network/interfaces/macs/").Return("", fmt.Errorf("refused"))
			err := awsPlatform.Init()
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("error getting aws meta_data"))
		})

		It("should handle error when fetching subnet ID", func() {
			macsResponse := "0A:0B:0C:0D:0E:0F/\n"
			subnetURL := "http://169.254.169.254/latest/meta-data/network/interfaces/macs/0A:0B:0C:0D:0E:0F/subnet-id"
			hostHelper.EXPECT().HTTPGetFetchData("http://169.254.169.254/latest/meta-data/network/interfaces/macs/").Return(macsResponse, nil)
			hostHelper.EXPECT().HTTPGetFetchData(subnetURL).Return("", errors.New("subnet timeout"))
			err := awsPlatform.Init()
			Expect(err).To(MatchError(fmt.Sprintf("error getting aws subnet_id from %s: %v", subnetURL, errors.New("subnet timeout"))))
		})

		It("should handle error for empty subnet ID", func() {
			macsResponse := "0A:0B:0C:0D:0E:0F/\n"
			subnetURL := "http://169.254.169.254/latest/meta-data/network/interfaces/macs/0A:0B:0C:0D:0E:0F/subnet-id"
			hostHelper.EXPECT().HTTPGetFetchData("http://169.254.169.254/latest/meta-data/network/interfaces/macs/").Return(macsResponse, nil)
			hostHelper.EXPECT().HTTPGetFetchData(subnetURL).Return("", nil)
			err := awsPlatform.Init()
			Expect(err).To(MatchError(fmt.Sprintf("empty subnet_id from %s", subnetURL)))
		})
	})
})
