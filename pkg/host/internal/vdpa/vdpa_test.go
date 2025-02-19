package vdpa

import (
	"fmt"
	"syscall"

	"github.com/vishvananda/netlink"
	"go.uber.org/mock/gomock"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/consts"
	constants "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/consts"
	netlinkMock "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/host/internal/lib/netlink/mock"
	hostMock "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/host/mock"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/host/types"
)

var _ = Describe("VDPA", func() {
	var (
		v          types.VdpaInterface
		libMock    *netlinkMock.MockNetlinkLib
		kernelMock *hostMock.MockHostManagerInterface

		testCtrl *gomock.Controller
		testErr  = fmt.Errorf("test-error")
	)
	BeforeEach(func() {
		testCtrl = gomock.NewController(GinkgoT())
		libMock = netlinkMock.NewMockNetlinkLib(testCtrl)
		kernelMock = hostMock.NewMockHostManagerInterface(testCtrl)
		v = New(kernelMock, libMock)
	})
	AfterEach(func() {
		testCtrl.Finish()
	})
	Context("CreateVDPADevice", func() {
		callFunc := func() error {
			return v.CreateVDPADevice("0000:d8:00.2", constants.VdpaTypeVhost)
		}
		It("Created", func() {
			libMock.EXPECT().VDPAGetDevByName("vdpa:0000:d8:00.2").Return(nil, syscall.ENODEV)
			libMock.EXPECT().VDPANewDev("vdpa:0000:d8:00.2", "pci", "0000:d8:00.2", netlink.VDPANewDevParams{MaxVQP: 32}).Return(nil)
			kernelMock.EXPECT().BindDriverByBusAndDevice(consts.BusVdpa, "vdpa:0000:d8:00.2", "vhost_vdpa").Return(nil)
			Expect(callFunc()).NotTo(HaveOccurred())
		})
		It("Created without MaxVQP", func() {
			libMock.EXPECT().VDPAGetDevByName("vdpa:0000:d8:00.2").Return(nil, syscall.ENODEV)
			libMock.EXPECT().VDPANewDev("vdpa:0000:d8:00.2", "pci", "0000:d8:00.2", netlink.VDPANewDevParams{MaxVQP: 32}).Return(syscall.ENOTSUP)
			libMock.EXPECT().VDPANewDev("vdpa:0000:d8:00.2", "pci", "0000:d8:00.2", netlink.VDPANewDevParams{}).Return(nil)
			kernelMock.EXPECT().BindDriverByBusAndDevice(consts.BusVdpa, "vdpa:0000:d8:00.2", "vhost_vdpa").Return(nil)
			Expect(callFunc()).NotTo(HaveOccurred())
		})
		It("Already exist", func() {
			libMock.EXPECT().VDPAGetDevByName("vdpa:0000:d8:00.2").Return(&netlink.VDPADev{}, nil)
			kernelMock.EXPECT().BindDriverByBusAndDevice(consts.BusVdpa, "vdpa:0000:d8:00.2", "vhost_vdpa").Return(nil)
			Expect(callFunc()).NotTo(HaveOccurred())
		})
		It("Fail to Get device", func() {
			libMock.EXPECT().VDPAGetDevByName("vdpa:0000:d8:00.2").Return(nil, testErr)
			Expect(callFunc()).To(MatchError(testErr))
		})
		It("Fail to Create device", func() {
			libMock.EXPECT().VDPAGetDevByName("vdpa:0000:d8:00.2").Return(nil, syscall.ENODEV)
			libMock.EXPECT().VDPANewDev("vdpa:0000:d8:00.2", "pci", "0000:d8:00.2", netlink.VDPANewDevParams{MaxVQP: 32}).Return(testErr)
			Expect(callFunc()).To(MatchError(testErr))
		})
		It("Fail to Bind device", func() {
			libMock.EXPECT().VDPAGetDevByName("vdpa:0000:d8:00.2").Return(&netlink.VDPADev{}, nil)
			kernelMock.EXPECT().BindDriverByBusAndDevice(consts.BusVdpa, "vdpa:0000:d8:00.2", "vhost_vdpa").Return(testErr)
			Expect(callFunc()).To(MatchError(testErr))
		})
	})
	Context("DeleteVDPADevice", func() {
		callFunc := func() error {
			return v.DeleteVDPADevice("0000:d8:00.2")
		}
		It("Removed", func() {
			libMock.EXPECT().VDPADelDev("vdpa:0000:d8:00.2").Return(nil)
			Expect(callFunc()).NotTo(HaveOccurred())
		})
		It("Not found", func() {
			libMock.EXPECT().VDPADelDev("vdpa:0000:d8:00.2").Return(syscall.ENODEV)
			Expect(callFunc()).NotTo(HaveOccurred())
		})
		It("Fail to delete device", func() {
			libMock.EXPECT().VDPADelDev("vdpa:0000:d8:00.2").Return(testErr)
			Expect(callFunc()).To(MatchError(testErr))
		})
	})
	Context("DiscoverVDPAType", func() {
		callFunc := func() string {
			return v.DiscoverVDPAType("0000:d8:00.2")
		}
		It("No device", func() {
			libMock.EXPECT().VDPAGetDevByName("vdpa:0000:d8:00.2").Return(nil, syscall.ENODEV)
			Expect(callFunc()).To(BeEmpty())
		})
		It("Fail to read device", func() {
			libMock.EXPECT().VDPAGetDevByName("vdpa:0000:d8:00.2").Return(nil, testErr)
			Expect(callFunc()).To(BeEmpty())
		})
		It("No driver", func() {
			libMock.EXPECT().VDPAGetDevByName("vdpa:0000:d8:00.2").Return(&netlink.VDPADev{}, nil)
			kernelMock.EXPECT().GetDriverByBusAndDevice(consts.BusVdpa, "vdpa:0000:d8:00.2").Return("", nil)
			Expect(callFunc()).To(BeEmpty())
		})
		It("Unknown driver", func() {
			libMock.EXPECT().VDPAGetDevByName("vdpa:0000:d8:00.2").Return(&netlink.VDPADev{}, nil)
			kernelMock.EXPECT().GetDriverByBusAndDevice(consts.BusVdpa, "vdpa:0000:d8:00.2").Return("something", nil)
			Expect(callFunc()).To(BeEmpty())
		})
		It("Vhost driver", func() {
			libMock.EXPECT().VDPAGetDevByName("vdpa:0000:d8:00.2").Return(&netlink.VDPADev{}, nil)
			kernelMock.EXPECT().GetDriverByBusAndDevice(consts.BusVdpa, "vdpa:0000:d8:00.2").Return("vhost_vdpa", nil)
			Expect(callFunc()).To(Equal("vhost"))
		})
		It("Virtio driver", func() {
			libMock.EXPECT().VDPAGetDevByName("vdpa:0000:d8:00.2").Return(&netlink.VDPADev{}, nil)
			kernelMock.EXPECT().GetDriverByBusAndDevice(consts.BusVdpa, "vdpa:0000:d8:00.2").Return("virtio_vdpa", nil)
			Expect(callFunc()).To(Equal("virtio"))
		})
		It("Fail to read driver", func() {
			libMock.EXPECT().VDPAGetDevByName("vdpa:0000:d8:00.2").Return(&netlink.VDPADev{}, nil)
			kernelMock.EXPECT().GetDriverByBusAndDevice(consts.BusVdpa, "vdpa:0000:d8:00.2").Return("", testErr)
			Expect(callFunc()).To(BeEmpty())
		})
	})
})
