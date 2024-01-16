package vdpa

import (
	"fmt"
	"syscall"

	"github.com/golang/mock/gomock"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/consts"
	constants "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/consts"
	govdpaMock "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/host/internal/lib/govdpa/mock"
	hostMock "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/host/mock"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/host/types"
)

var _ = Describe("VDPA", func() {
	var (
		v           types.VdpaInterface
		libMock     *govdpaMock.MockGoVdpaLib
		vdpaDevMock *govdpaMock.MockVdpaDevice
		kernelMock  *hostMock.MockHostManagerInterface

		testCtrl *gomock.Controller
		testErr  = fmt.Errorf("test-error")
	)
	BeforeEach(func() {
		testCtrl = gomock.NewController(GinkgoT())
		libMock = govdpaMock.NewMockGoVdpaLib(testCtrl)
		vdpaDevMock = govdpaMock.NewMockVdpaDevice(testCtrl)
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
			libMock.EXPECT().GetVdpaDevice("vdpa:0000:d8:00.2").Return(nil, syscall.ENODEV)
			libMock.EXPECT().AddVdpaDevice("pci/"+"0000:d8:00.2", "vdpa:0000:d8:00.2").Return(nil)
			kernelMock.EXPECT().BindDriverByBusAndDevice(consts.BusVdpa, "vdpa:0000:d8:00.2", "vhost_vdpa").Return(nil)
			Expect(callFunc()).NotTo(HaveOccurred())
		})
		It("Already exist", func() {
			libMock.EXPECT().GetVdpaDevice("vdpa:0000:d8:00.2").Return(vdpaDevMock, nil)
			kernelMock.EXPECT().BindDriverByBusAndDevice(consts.BusVdpa, "vdpa:0000:d8:00.2", "vhost_vdpa").Return(nil)
			Expect(callFunc()).NotTo(HaveOccurred())
		})
		It("Fail to Get device", func() {
			libMock.EXPECT().GetVdpaDevice("vdpa:0000:d8:00.2").Return(nil, testErr)
			Expect(callFunc()).To(MatchError(testErr))
		})
		It("Fail to Create device", func() {
			libMock.EXPECT().GetVdpaDevice("vdpa:0000:d8:00.2").Return(nil, syscall.ENODEV)
			libMock.EXPECT().AddVdpaDevice("pci/"+"0000:d8:00.2", "vdpa:0000:d8:00.2").Return(testErr)
			Expect(callFunc()).To(MatchError(testErr))
		})
		It("Fail to Bind device", func() {
			libMock.EXPECT().GetVdpaDevice("vdpa:0000:d8:00.2").Return(vdpaDevMock, nil)
			kernelMock.EXPECT().BindDriverByBusAndDevice(consts.BusVdpa, "vdpa:0000:d8:00.2", "vhost_vdpa").Return(testErr)
			Expect(callFunc()).To(MatchError(testErr))
		})
	})
	Context("DeleteVDPADevice", func() {
		callFunc := func() error {
			return v.DeleteVDPADevice("0000:d8:00.2")
		}
		It("Removed", func() {
			libMock.EXPECT().DeleteVdpaDevice("vdpa:0000:d8:00.2").Return(nil)
			Expect(callFunc()).NotTo(HaveOccurred())
		})
		It("Not found", func() {
			libMock.EXPECT().DeleteVdpaDevice("vdpa:0000:d8:00.2").Return(syscall.ENODEV)
			Expect(callFunc()).NotTo(HaveOccurred())
		})
		It("Fail to delete device", func() {
			libMock.EXPECT().DeleteVdpaDevice("vdpa:0000:d8:00.2").Return(testErr)
			Expect(callFunc()).To(MatchError(testErr))
		})
	})
	Context("DiscoverVDPAType", func() {
		callFunc := func() string {
			return v.DiscoverVDPAType("0000:d8:00.2")
		}
		It("No device", func() {
			libMock.EXPECT().GetVdpaDevice("vdpa:0000:d8:00.2").Return(nil, syscall.ENODEV)
			Expect(callFunc()).To(BeEmpty())
		})
		It("No driver", func() {
			vdpaDevMock.EXPECT().Driver().Return("")
			libMock.EXPECT().GetVdpaDevice("vdpa:0000:d8:00.2").Return(vdpaDevMock, nil)
			Expect(callFunc()).To(BeEmpty())
		})
		It("Unknown driver", func() {
			vdpaDevMock.EXPECT().Driver().Return("something")
			libMock.EXPECT().GetVdpaDevice("vdpa:0000:d8:00.2").Return(vdpaDevMock, nil)
			Expect(callFunc()).To(BeEmpty())
		})
		It("Vhost driver", func() {
			vdpaDevMock.EXPECT().Driver().Return("vhost_vdpa")
			libMock.EXPECT().GetVdpaDevice("vdpa:0000:d8:00.2").Return(vdpaDevMock, nil)
			Expect(callFunc()).To(Equal("vhost"))
		})
		It("Virtio driver", func() {
			vdpaDevMock.EXPECT().Driver().Return("virtio_vdpa")
			libMock.EXPECT().GetVdpaDevice("vdpa:0000:d8:00.2").Return(vdpaDevMock, nil)
			Expect(callFunc()).To(Equal("virtio"))
		})
	})
})
