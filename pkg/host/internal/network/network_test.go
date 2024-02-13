package network

import (
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/vishvananda/netlink"
	"github.com/vishvananda/netlink/nl"

	"github.com/golang/mock/gomock"

	hostMockPkg "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/helper/mock"
	dputilsMockPkg "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/host/internal/lib/dputils/mock"
	netlinkMockPkg "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/host/internal/lib/netlink/mock"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/host/types"
)

func getDevlinkParam(t uint8, value interface{}) *netlink.DevlinkParam {
	return &netlink.DevlinkParam{
		Name: "test_param",
		Type: t,
		Values: []netlink.DevlinkParamValue{
			{Data: value, CMODE: nl.DEVLINK_PARAM_CMODE_DRIVERINIT}},
	}
}

var _ = Describe("Network", func() {
	var (
		n              types.NetworkInterface
		netlinkLibMock *netlinkMockPkg.MockNetlinkLib
		dputilsLibMock *dputilsMockPkg.MockDPUtilsLib
		hostMock       *hostMockPkg.MockHostHelpersInterface

		testCtrl *gomock.Controller
		testErr  = fmt.Errorf("test")
	)
	BeforeEach(func() {
		testCtrl = gomock.NewController(GinkgoT())
		netlinkLibMock = netlinkMockPkg.NewMockNetlinkLib(testCtrl)
		dputilsLibMock = dputilsMockPkg.NewMockDPUtilsLib(testCtrl)
		hostMock = hostMockPkg.NewMockHostHelpersInterface(testCtrl)

		n = New(hostMock, dputilsLibMock, netlinkLibMock)
	})

	AfterEach(func() {
		testCtrl.Finish()
	})
	Context("GetDevlinkDeviceParam", func() {
		It("get - string", func() {
			netlinkLibMock.EXPECT().DevlinkGetDeviceParamByName("pci", "0000:d8:00.1", "param_name").Return(
				getDevlinkParam(nl.DEVLINK_PARAM_TYPE_STRING, "test_value"), nil)
			result, err := n.GetDevlinkDeviceParam("0000:d8:00.1", "param_name")
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal("test_value"))
		})
		It("get - uint8", func() {
			netlinkLibMock.EXPECT().DevlinkGetDeviceParamByName("pci", "0000:d8:00.1", "param_name").Return(
				getDevlinkParam(nl.DEVLINK_PARAM_TYPE_U8, uint8(8)), nil)
			result, err := n.GetDevlinkDeviceParam("0000:d8:00.1", "param_name")
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal("8"))
		})
		It("get - uint16", func() {
			netlinkLibMock.EXPECT().DevlinkGetDeviceParamByName("pci", "0000:d8:00.1", "param_name").Return(
				getDevlinkParam(nl.DEVLINK_PARAM_TYPE_U16, uint16(16)), nil)
			result, err := n.GetDevlinkDeviceParam("0000:d8:00.1", "param_name")
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal("16"))
		})
		It("get - uint32", func() {
			netlinkLibMock.EXPECT().DevlinkGetDeviceParamByName("pci", "0000:d8:00.1", "param_name").Return(
				getDevlinkParam(nl.DEVLINK_PARAM_TYPE_U32, uint32(32)), nil)
			result, err := n.GetDevlinkDeviceParam("0000:d8:00.1", "param_name")
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal("32"))
		})
		It("get - bool", func() {
			netlinkLibMock.EXPECT().DevlinkGetDeviceParamByName("pci", "0000:d8:00.1", "param_name").Return(
				getDevlinkParam(nl.DEVLINK_PARAM_TYPE_BOOL, false), nil)
			result, err := n.GetDevlinkDeviceParam("0000:d8:00.1", "param_name")
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal("false"))
		})
		It("failed", func() {
			netlinkLibMock.EXPECT().DevlinkGetDeviceParamByName("pci", "0000:d8:00.1", "param_name").Return(nil, testErr)
			_, err := n.GetDevlinkDeviceParam("0000:d8:00.1", "param_name")
			Expect(err).To(HaveOccurred())
		})
	})
	Context("SetDevlinkDeviceParam", func() {
		It("set - string", func() {
			netlinkLibMock.EXPECT().DevlinkGetDeviceParamByName("pci", "0000:d8:00.1", "param_name").Return(
				getDevlinkParam(nl.DEVLINK_PARAM_TYPE_STRING, "test_value"), nil)
			netlinkLibMock.EXPECT().DevlinkSetDeviceParam("pci", "0000:d8:00.1", "param_name",
				uint8(nl.DEVLINK_PARAM_CMODE_DRIVERINIT), "test_value").Return(nil)
			err := n.SetDevlinkDeviceParam("0000:d8:00.1", "param_name", "test_value")
			Expect(err).NotTo(HaveOccurred())
		})
		It("set - uint8", func() {
			netlinkLibMock.EXPECT().DevlinkGetDeviceParamByName("pci", "0000:d8:00.1", "param_name").Return(
				getDevlinkParam(nl.DEVLINK_PARAM_TYPE_U8, uint8(8)), nil)
			netlinkLibMock.EXPECT().DevlinkSetDeviceParam("pci", "0000:d8:00.1", "param_name",
				uint8(nl.DEVLINK_PARAM_CMODE_DRIVERINIT), uint8(100)).Return(nil)
			err := n.SetDevlinkDeviceParam("0000:d8:00.1", "param_name", "100")
			Expect(err).NotTo(HaveOccurred())
		})
		It("set - uint16", func() {
			netlinkLibMock.EXPECT().DevlinkGetDeviceParamByName("pci", "0000:d8:00.1", "param_name").Return(
				getDevlinkParam(nl.DEVLINK_PARAM_TYPE_U16, uint16(16)), nil)
			netlinkLibMock.EXPECT().DevlinkSetDeviceParam("pci", "0000:d8:00.1", "param_name",
				uint8(nl.DEVLINK_PARAM_CMODE_DRIVERINIT), uint16(100)).Return(nil)
			err := n.SetDevlinkDeviceParam("0000:d8:00.1", "param_name", "100")
			Expect(err).NotTo(HaveOccurred())
		})
		It("set - uint32", func() {
			netlinkLibMock.EXPECT().DevlinkGetDeviceParamByName("pci", "0000:d8:00.1", "param_name").Return(
				getDevlinkParam(nl.DEVLINK_PARAM_TYPE_U32, uint32(32)), nil)
			netlinkLibMock.EXPECT().DevlinkSetDeviceParam("pci", "0000:d8:00.1", "param_name",
				uint8(nl.DEVLINK_PARAM_CMODE_DRIVERINIT), uint32(100)).Return(nil)
			err := n.SetDevlinkDeviceParam("0000:d8:00.1", "param_name", "100")
			Expect(err).NotTo(HaveOccurred())
		})
		It("set - bool", func() {
			netlinkLibMock.EXPECT().DevlinkGetDeviceParamByName("pci", "0000:d8:00.1", "param_name").Return(
				getDevlinkParam(nl.DEVLINK_PARAM_TYPE_BOOL, false), nil)
			netlinkLibMock.EXPECT().DevlinkSetDeviceParam("pci", "0000:d8:00.1", "param_name",
				uint8(nl.DEVLINK_PARAM_CMODE_DRIVERINIT), true).Return(nil)
			err := n.SetDevlinkDeviceParam("0000:d8:00.1", "param_name", "true")
			Expect(err).NotTo(HaveOccurred())
		})
		It("failed to get", func() {
			netlinkLibMock.EXPECT().DevlinkGetDeviceParamByName("pci", "0000:d8:00.1", "param_name").Return(
				nil, testErr)
			err := n.SetDevlinkDeviceParam("0000:d8:00.1", "param_name", "true")
			Expect(err).To(HaveOccurred())
		})
		It("failed to set", func() {
			netlinkLibMock.EXPECT().DevlinkGetDeviceParamByName("pci", "0000:d8:00.1", "param_name").Return(
				getDevlinkParam(nl.DEVLINK_PARAM_TYPE_BOOL, false), nil)
			netlinkLibMock.EXPECT().DevlinkSetDeviceParam("pci", "0000:d8:00.1", "param_name",
				uint8(nl.DEVLINK_PARAM_CMODE_DRIVERINIT), true).Return(testErr)
			err := n.SetDevlinkDeviceParam("0000:d8:00.1", "param_name", "true")
			Expect(err).To(HaveOccurred())
		})
		It("failed to convert type on set", func() {
			netlinkLibMock.EXPECT().DevlinkGetDeviceParamByName("pci", "0000:d8:00.1", "param_name").Return(
				getDevlinkParam(nl.DEVLINK_PARAM_TYPE_U8, 10), nil)
			// uint8 overflow
			err := n.SetDevlinkDeviceParam("0000:d8:00.1", "param_name", "10000")
			Expect(err).To(HaveOccurred())
		})
	})
})
