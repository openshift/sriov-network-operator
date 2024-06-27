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
	ethtoolMockPkg "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/host/internal/lib/ethtool/mock"
	netlinkMockPkg "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/host/internal/lib/netlink/mock"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/host/types"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/test/util/fakefilesystem"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/test/util/helpers"
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
		ethtoolLibMock *ethtoolMockPkg.MockEthtoolLib
		dputilsLibMock *dputilsMockPkg.MockDPUtilsLib
		hostMock       *hostMockPkg.MockHostHelpersInterface

		testCtrl *gomock.Controller
		testErr  = fmt.Errorf("test")
	)
	BeforeEach(func() {
		testCtrl = gomock.NewController(GinkgoT())
		netlinkLibMock = netlinkMockPkg.NewMockNetlinkLib(testCtrl)
		ethtoolLibMock = ethtoolMockPkg.NewMockEthtoolLib(testCtrl)
		dputilsLibMock = dputilsMockPkg.NewMockDPUtilsLib(testCtrl)
		hostMock = hostMockPkg.NewMockHostHelpersInterface(testCtrl)

		n = New(hostMock, dputilsLibMock, netlinkLibMock, ethtoolLibMock)
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
	Context("EnableHwTcOffload", func() {
		It("Enabled", func() {
			ethtoolLibMock.EXPECT().FeatureNames("enp216s0f0np0").Return(map[string]uint{"hw-tc-offload": 42}, nil)
			ethtoolLibMock.EXPECT().Features("enp216s0f0np0").Return(map[string]bool{"hw-tc-offload": false}, nil)
			ethtoolLibMock.EXPECT().Change("enp216s0f0np0", map[string]bool{"hw-tc-offload": true}).Return(nil)
			ethtoolLibMock.EXPECT().Features("enp216s0f0np0").Return(map[string]bool{"hw-tc-offload": true}, nil)
			Expect(n.EnableHwTcOffload("enp216s0f0np0")).NotTo(HaveOccurred())
		})
		It("Feature unknown", func() {
			ethtoolLibMock.EXPECT().FeatureNames("enp216s0f0np0").Return(map[string]uint{}, nil)
			Expect(n.EnableHwTcOffload("enp216s0f0np0")).NotTo(HaveOccurred())
		})
		It("Already enabled", func() {
			ethtoolLibMock.EXPECT().FeatureNames("enp216s0f0np0").Return(map[string]uint{"hw-tc-offload": 42}, nil)
			ethtoolLibMock.EXPECT().Features("enp216s0f0np0").Return(map[string]bool{"hw-tc-offload": true}, nil)
			Expect(n.EnableHwTcOffload("enp216s0f0np0")).NotTo(HaveOccurred())
		})
		It("not supported", func() {
			ethtoolLibMock.EXPECT().FeatureNames("enp216s0f0np0").Return(map[string]uint{"hw-tc-offload": 42}, nil)
			ethtoolLibMock.EXPECT().Features("enp216s0f0np0").Return(map[string]bool{"hw-tc-offload": false}, nil)
			ethtoolLibMock.EXPECT().Change("enp216s0f0np0", map[string]bool{"hw-tc-offload": true}).Return(nil)
			ethtoolLibMock.EXPECT().Features("enp216s0f0np0").Return(map[string]bool{"hw-tc-offload": false}, nil)
			Expect(n.EnableHwTcOffload("enp216s0f0np0")).NotTo(HaveOccurred())
		})
		It("fail - can't list supported", func() {
			ethtoolLibMock.EXPECT().FeatureNames("enp216s0f0np0").Return(nil, testErr)
			Expect(n.EnableHwTcOffload("enp216s0f0np0")).To(MatchError(testErr))
		})
		It("fail - can't get features", func() {
			ethtoolLibMock.EXPECT().FeatureNames("enp216s0f0np0").Return(map[string]uint{"hw-tc-offload": 42}, nil)
			ethtoolLibMock.EXPECT().Features("enp216s0f0np0").Return(nil, testErr)
			Expect(n.EnableHwTcOffload("enp216s0f0np0")).To(MatchError(testErr))
		})
		It("fail - can't change features", func() {
			ethtoolLibMock.EXPECT().FeatureNames("enp216s0f0np0").Return(map[string]uint{"hw-tc-offload": 42}, nil)
			ethtoolLibMock.EXPECT().Features("enp216s0f0np0").Return(map[string]bool{"hw-tc-offload": false}, nil)
			ethtoolLibMock.EXPECT().Change("enp216s0f0np0", map[string]bool{"hw-tc-offload": true}).Return(testErr)
			Expect(n.EnableHwTcOffload("enp216s0f0np0")).To(MatchError(testErr))
		})
		It("fail - can't reread features", func() {
			ethtoolLibMock.EXPECT().FeatureNames("enp216s0f0np0").Return(map[string]uint{"hw-tc-offload": 42}, nil)
			ethtoolLibMock.EXPECT().Features("enp216s0f0np0").Return(map[string]bool{"hw-tc-offload": false}, nil)
			ethtoolLibMock.EXPECT().Change("enp216s0f0np0", map[string]bool{"hw-tc-offload": true}).Return(nil)
			ethtoolLibMock.EXPECT().Features("enp216s0f0np0").Return(nil, testErr)
			Expect(n.EnableHwTcOffload("enp216s0f0np0")).To(MatchError(testErr))
		})
	})
	Context("GetNetDevNodeGUID", func() {
		It("Returns empty when pciAddr is empty", func() {
			Expect(n.GetNetDevNodeGUID("")).To(Equal(""))
		})
		It("Returns empty when infiniband directory can't be read", func() {
			helpers.GinkgoConfigureFakeFS(&fakefilesystem.FS{
				Dirs: []string{"/sys/bus/pci/devices/0000:4b:00.3/"},
			})
			Expect(n.GetNetDevNodeGUID("0000:4b:00.3")).To(Equal(""))
		})
		It("Returns empty when more than one RDMA devices are detected for pciAddr", func() {
			helpers.GinkgoConfigureFakeFS(&fakefilesystem.FS{
				Dirs: []string{
					"/sys/bus/pci/devices/0000:4b:00.3/infiniband/mlx5_2",
					"/sys/bus/pci/devices/0000:4b:00.3/infiniband/mlx5_3",
				},
			})
			Expect(n.GetNetDevNodeGUID("0000:4b:00.3")).To(Equal(""))
		})
		It("Returns empty when it fails to read RDMA link", func() {
			helpers.GinkgoConfigureFakeFS(&fakefilesystem.FS{
				Dirs: []string{"/sys/bus/pci/devices/0000:4b:00.3/infiniband/mlx5_2"},
			})
			netlinkLibMock.EXPECT().RdmaLinkByName("mlx5_2").Return(nil, fmt.Errorf("some-error"))
			Expect(n.GetNetDevNodeGUID("0000:4b:00.3")).To(Equal(""))
		})
		It("Returns populated node GUID on correct setup", func() {
			helpers.GinkgoConfigureFakeFS(&fakefilesystem.FS{
				Dirs: []string{"/sys/bus/pci/devices/0000:4b:00.3/infiniband/mlx5_2"},
			})
			netlinkLibMock.EXPECT().RdmaLinkByName("mlx5_2").Return(&netlink.RdmaLink{Attrs: netlink.RdmaLinkAttrs{NodeGuid: "1122:3344:5566:7788"}}, nil)
			Expect(n.GetNetDevNodeGUID("0000:4b:00.3")).To(Equal("1122:3344:5566:7788"))
		})
	})
	Context("GetInterfaceIndex", func() {
		It("should return valid index", func() {
			helpers.GinkgoConfigureFakeFS(&fakefilesystem.FS{
				Dirs: []string{
					"/sys/bus/pci/devices/0000:4b:00.3/net/eth0/",
					"/sys/class/net/eth0/",
				},
				Files: map[string][]byte{
					"/sys/bus/pci/devices/0000:4b:00.3/net/eth0/ifindex": []byte("42"),
					"/sys/class/net/eth0/phys_switch_id":                 {},
				},
			})
			dputilsLibMock.EXPECT().GetNetNames("0000:4b:00.3").Return([]string{"eth0"}, nil)
			index, err := n.GetInterfaceIndex("0000:4b:00.3")
			Expect(err).ToNot(HaveOccurred())
			Expect(index).To(Equal(42))
		})
		It("should return invalid index", func() {
			helpers.GinkgoConfigureFakeFS(&fakefilesystem.FS{
				Dirs: []string{
					"/sys/bus/pci/devices/0000:4b:00.3/net/eth0",
					"/sys/class/net/eth0/",
				},
				Files: map[string][]byte{
					"/sys/class/net/eth0/phys_switch_id": {},
				},
			})
			dputilsLibMock.EXPECT().GetNetNames("0000:4b:00.3").Return([]string{"eth0"}, nil)
			index, err := n.GetInterfaceIndex("0000:4b:00.3")
			Expect(err).To(HaveOccurred())
			Expect(index).To(Equal(-1))
		})
	})
})
