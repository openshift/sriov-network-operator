package sriov

import (
	"fmt"
	"strconv"
	"syscall"

	"github.com/golang/mock/gomock"
	"github.com/vishvananda/netlink"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	dputilsMockPkg "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/host/internal/lib/dputils/mock"
	netlinkMockPkg "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/host/internal/lib/netlink/mock"
	hostMockPkg "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/host/mock"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/host/types"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/test/util/fakefilesystem"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/test/util/helpers"
)

var _ = Describe("SRIOV", func() {
	var (
		s              types.SriovInterface
		netlinkLibMock *netlinkMockPkg.MockNetlinkLib
		dputilsLibMock *dputilsMockPkg.MockDPUtilsLib
		hostMock       *hostMockPkg.MockHostManagerInterface

		testCtrl *gomock.Controller

		testError = fmt.Errorf("test")
	)
	BeforeEach(func() {
		testCtrl = gomock.NewController(GinkgoT())
		netlinkLibMock = netlinkMockPkg.NewMockNetlinkLib(testCtrl)
		dputilsLibMock = dputilsMockPkg.NewMockDPUtilsLib(testCtrl)
		hostMock = hostMockPkg.NewMockHostManagerInterface(testCtrl)
		s = New(nil, hostMock, hostMock, hostMock, netlinkLibMock, dputilsLibMock)
	})

	AfterEach(func() {
		testCtrl.Finish()
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
			mode, err := s.GetNicSriovMode("0000:d8:00.0")
			Expect(err).NotTo(HaveOccurred())
			Expect(mode).To(Equal("switchdev"))
		})
		It("devlink returns error", func() {
			netlinkLibMock.EXPECT().DevLinkGetDeviceByName("pci", "0000:d8:00.0").Return(nil, testError)
			_, err := s.GetNicSriovMode("0000:d8:00.0")
			Expect(err).To(MatchError(testError))
		})
		It("devlink not supported - fail to get name", func() {
			netlinkLibMock.EXPECT().DevLinkGetDeviceByName("pci", "0000:d8:00.0").Return(nil, syscall.ENODEV)
			mode, err := s.GetNicSriovMode("0000:d8:00.0")
			Expect(err).NotTo(HaveOccurred())
			Expect(mode).To(BeEmpty())
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
})
