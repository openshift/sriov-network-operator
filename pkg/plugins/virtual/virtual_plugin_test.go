package virtual

import (
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"go.uber.org/mock/gomock"
	corev1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	sriovnetworkv1 "github.com/k8snetworkplumbingwg/sriov-network-operator/api/v1"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/consts"
	mock_helper "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/helper/mock"
)

var _ = Describe("SRIOV", Ordered, func() {
	var (
		v        *VirtualPlugin
		h        *mock_helper.MockHostHelpersInterface
		testCtrl *gomock.Controller

		sriovNetworkNodeState *sriovnetworkv1.SriovNetworkNodeState
	)

	BeforeAll(func() {
		sriovnetworkv1.NicIDMap = []string{"15b3 1013 1014"}
	})

	BeforeEach(func() {
		testCtrl = gomock.NewController(GinkgoT())
		h = mock_helper.NewMockHostHelpersInterface(testCtrl)
		t, err := NewVirtualPlugin(h)
		Expect(err).ToNot(HaveOccurred())

		v = t.(*VirtualPlugin)

		sriovNetworkNodeState = &sriovnetworkv1.SriovNetworkNodeState{ObjectMeta: corev1.ObjectMeta{Name: "worker-0", Namespace: "test"},
			Spec:   sriovnetworkv1.SriovNetworkNodeStateSpec{},
			Status: sriovnetworkv1.SriovNetworkNodeStateStatus{},
		}
	})

	AfterEach(func() {
		testCtrl.Finish()
	})

	Context("CheckStatusChanges", func() {
		It("should always return false", func() {
			drift, err := v.CheckStatusChanges(sriovNetworkNodeState)
			Expect(err).ToNot(HaveOccurred())
			Expect(drift).To(BeFalse())
		})
	})

	Context("OnNodeStateChange", func() {
		It("should mark the vfio driver as loading if needed", func() {
			sriovNetworkNodeState.Spec.Interfaces = sriovnetworkv1.Interfaces{
				{Name: "eno1",
					NumVfs:     10,
					PciAddress: "0000:d8:00.0", VfGroups: []sriovnetworkv1.VfGroup{
						{
							ResourceName: "test",
							PolicyName:   "test",
							VfRange:      "eno1#0-9",
							DeviceType:   consts.DeviceTypeVfioPci,
						},
					},
				},
			}
			Expect(v.LoadVfioDriver).To(Equal(uint(0)))
			needDrain, needReboot, err := v.OnNodeStateChange(sriovNetworkNodeState)
			Expect(err).ToNot(HaveOccurred())
			Expect(needDrain).To(BeFalse())
			Expect(needReboot).To(BeFalse())
			Expect(v.LoadVfioDriver).To(Equal(uint(1)))
		})

		It("should remain loading even if no vfio exist", func() {
			sriovNetworkNodeState.Spec.Interfaces = sriovnetworkv1.Interfaces{
				{Name: "eno1",
					NumVfs:     10,
					PciAddress: "0000:d8:00.0", VfGroups: []sriovnetworkv1.VfGroup{
						{
							ResourceName: "test",
							PolicyName:   "test",
							VfRange:      "eno1#0-9",
							DeviceType:   consts.DeviceTypeVfioPci,
						},
					},
				},
			}
			Expect(v.LoadVfioDriver).To(Equal(uint(unloaded)))
			needDrain, needReboot, err := v.OnNodeStateChange(sriovNetworkNodeState)
			Expect(err).ToNot(HaveOccurred())
			Expect(needDrain).To(BeFalse())
			Expect(needReboot).To(BeFalse())
			Expect(v.LoadVfioDriver).To(Equal(uint(loading)))

			sriovNetworkNodeState.Spec.Interfaces = sriovnetworkv1.Interfaces{}
			needDrain, needReboot, err = v.OnNodeStateChange(sriovNetworkNodeState)
			Expect(err).ToNot(HaveOccurred())
			Expect(needDrain).To(BeFalse())
			Expect(needReboot).To(BeFalse())
			Expect(v.LoadVfioDriver).To(Equal(uint(loading)))
		})
	})

	Context("Apply", func() {
		It("should not apply any configure if there is no change in the node state and the kernel modules are loaded", func() {
			v.LoadVfioDriver = loaded
			sriovNetworkNodeState.Spec.Interfaces = sriovnetworkv1.Interfaces{
				{Name: "eno1",
					NumVfs:     10,
					PciAddress: "0000:d8:00.0", VfGroups: []sriovnetworkv1.VfGroup{
						{
							ResourceName: "test",
							PolicyName:   "test",
							VfRange:      "eno1#0-9",
							DeviceType:   consts.DeviceTypeVfioPci,
						},
					},
				},
			}

			v.LastState = sriovNetworkNodeState
			v.DesireState = sriovNetworkNodeState
			err := v.Apply()
			Expect(err).ToNot(HaveOccurred())
		})

		It("should load the kernel modules if they are not loaded", func() {
			h.EXPECT().LoadKernelModule("vfio", "enable_unsafe_noiommu_mode=1").Return(nil)
			h.EXPECT().LoadKernelModule("vfio_pci").Return(nil)
			v.LoadVfioDriver = loading
			sriovNetworkNodeState.Spec.Interfaces = sriovnetworkv1.Interfaces{
				{Name: "eno1",
					NumVfs:     10,
					PciAddress: "0000:d8:00.0", VfGroups: []sriovnetworkv1.VfGroup{
						{
							ResourceName: "test",
							PolicyName:   "test",
							VfRange:      "eno1#0-9",
							DeviceType:   consts.DeviceTypeVfioPci,
						},
					},
				},
			}

			v.LastState = sriovNetworkNodeState
			v.DesireState = sriovNetworkNodeState
			err := v.Apply()
			Expect(err).ToNot(HaveOccurred())
			Expect(v.LoadVfioDriver).To(Equal(uint(loaded)))
		})

		It("should return error if not able to load the vfio kernel module", func() {
			h.EXPECT().LoadKernelModule("vfio", "enable_unsafe_noiommu_mode=1").Return(fmt.Errorf("failed to load kernel module"))
			v.LoadVfioDriver = loading
			sriovNetworkNodeState.Spec.Interfaces = sriovnetworkv1.Interfaces{
				{Name: "eno1",
					NumVfs:     10,
					PciAddress: "0000:d8:00.0", VfGroups: []sriovnetworkv1.VfGroup{
						{
							ResourceName: "test",
							PolicyName:   "test",
							VfRange:      "eno1#0-9",
							DeviceType:   consts.DeviceTypeVfioPci,
						},
					},
				},
			}
			v.DesireState = sriovNetworkNodeState

			err := v.Apply()
			Expect(err).To(HaveOccurred())
		})

		It("should return error if not able to load the vfio_pci kernel module", func() {
			h.EXPECT().LoadKernelModule("vfio", "enable_unsafe_noiommu_mode=1").Return(nil)
			h.EXPECT().LoadKernelModule("vfio_pci").Return(fmt.Errorf("failed to load kernel module"))
			v.LoadVfioDriver = loading
			sriovNetworkNodeState.Spec.Interfaces = sriovnetworkv1.Interfaces{
				{Name: "eno1",
					NumVfs:     10,
					PciAddress: "0000:d8:00.0", VfGroups: []sriovnetworkv1.VfGroup{
						{
							ResourceName: "test",
							PolicyName:   "test",
							VfRange:      "eno1#0-9",
							DeviceType:   consts.DeviceTypeVfioPci,
						},
					},
				},
			}
			v.DesireState = sriovNetworkNodeState
			err := v.Apply()
			Expect(err).To(HaveOccurred())
		})

		It("should configure the device", func() {
			v.LoadVfioDriver = loaded
			h.EXPECT().Chroot(gomock.Any()).Return(func() error { return nil }, nil)
			h.EXPECT().ConfigSriovDeviceVirtual(gomock.Any()).Return(nil)
			sriovNetworkNodeState.Spec.Interfaces = sriovnetworkv1.Interfaces{
				{Name: "eno1",
					NumVfs:     1,
					PciAddress: "0000:d8:00.0", VfGroups: []sriovnetworkv1.VfGroup{
						{
							ResourceName: "test",
							PolicyName:   "test",
							VfRange:      "0-0",
							DeviceType:   consts.DeviceTypeVfioPci},
					},
				},
			}
			sriovNetworkNodeState.Status.Interfaces = sriovnetworkv1.InterfaceExts{
				{
					Name:       "eno1",
					NumVfs:     1,
					PciAddress: "0000:d8:00.0",
					VFs: []sriovnetworkv1.VirtualFunction{
						{
							Name:       "eno1",
							PciAddress: "0000:d8:00.0",
							Driver:     "virtio-net",
						},
					},
				},
			}

			v.DesireState = sriovNetworkNodeState
			err := v.Apply()
			Expect(err).ToNot(HaveOccurred())
		})
	})
})
