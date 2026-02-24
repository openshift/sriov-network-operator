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

		It("should return needDrain when interface is removed and was created by operator", func() {
			sriovNetworkNodeState.Status.Interfaces = sriovnetworkv1.InterfaceExts{
				{
					Name:       "eno1",
					PciAddress: "0000:d8:00.0",
					NumVfs:     1,
				},
			}
			sriovNetworkNodeState.Spec.Interfaces = sriovnetworkv1.Interfaces{}

			h.EXPECT().LoadPfsStatus("0000:d8:00.0").Return(&sriovnetworkv1.Interface{
				PciAddress:        "0000:d8:00.0",
				NumVfs:            1,
				ExternallyManaged: false,
			}, true, nil)

			needDrain, needReboot, err := v.OnNodeStateChange(sriovNetworkNodeState)
			Expect(err).ToNot(HaveOccurred())
			Expect(needDrain).To(BeTrue())
			Expect(needReboot).To(BeFalse())
		})

		It("should not return needDrain when interface is removed but doesn't exist in store", func() {
			sriovNetworkNodeState.Status.Interfaces = sriovnetworkv1.InterfaceExts{
				{
					Name:       "eno1",
					PciAddress: "0000:d8:00.0",
					NumVfs:     1,
				},
			}
			sriovNetworkNodeState.Spec.Interfaces = sriovnetworkv1.Interfaces{}

			h.EXPECT().LoadPfsStatus("0000:d8:00.0").Return(nil, false, nil)

			needDrain, needReboot, err := v.OnNodeStateChange(sriovNetworkNodeState)
			Expect(err).ToNot(HaveOccurred())
			Expect(needDrain).To(BeFalse())
			Expect(needReboot).To(BeFalse())
		})

		It("should not return needDrain when interface is removed but was externally managed", func() {
			sriovNetworkNodeState.Status.Interfaces = sriovnetworkv1.InterfaceExts{
				{
					Name:       "eno1",
					PciAddress: "0000:d8:00.0",
					NumVfs:     1,
				},
			}
			sriovNetworkNodeState.Spec.Interfaces = sriovnetworkv1.Interfaces{}

			h.EXPECT().LoadPfsStatus("0000:d8:00.0").Return(&sriovnetworkv1.Interface{
				PciAddress:        "0000:d8:00.0",
				NumVfs:            1,
				ExternallyManaged: true,
			}, true, nil)

			needDrain, needReboot, err := v.OnNodeStateChange(sriovNetworkNodeState)
			Expect(err).ToNot(HaveOccurred())
			Expect(needDrain).To(BeFalse())
			Expect(needReboot).To(BeFalse())
		})

		It("should not return needDrain when all current interfaces are in desired spec", func() {
			sriovNetworkNodeState.Status.Interfaces = sriovnetworkv1.InterfaceExts{
				{
					Name:       "eno1",
					PciAddress: "0000:d8:00.0",
					NumVfs:     1,
				},
			}
			sriovNetworkNodeState.Spec.Interfaces = sriovnetworkv1.Interfaces{
				{
					Name:       "eno1",
					PciAddress: "0000:d8:00.0",
					NumVfs:     1,
					VfGroups: []sriovnetworkv1.VfGroup{{
						ResourceName: "test",
						PolicyName:   "test",
						VfRange:      "0-0",
						DeviceType:   consts.DeviceTypeVfioPci,
					}},
				},
			}

			needDrain, needReboot, err := v.OnNodeStateChange(sriovNetworkNodeState)
			Expect(err).ToNot(HaveOccurred())
			Expect(needDrain).To(BeFalse())
			Expect(needReboot).To(BeFalse())
		})

		It("should continue checking other interfaces when LoadPfsStatus fails", func() {
			sriovNetworkNodeState.Status.Interfaces = sriovnetworkv1.InterfaceExts{
				{
					Name:       "eno1",
					PciAddress: "0000:d8:00.0",
					NumVfs:     1,
				},
				{
					Name:       "eno2",
					PciAddress: "0000:d8:00.1",
					NumVfs:     1,
				},
			}
			sriovNetworkNodeState.Spec.Interfaces = sriovnetworkv1.Interfaces{}

			h.EXPECT().LoadPfsStatus("0000:d8:00.0").Return(nil, false, fmt.Errorf("failed to load"))
			h.EXPECT().LoadPfsStatus("0000:d8:00.1").Return(&sriovnetworkv1.Interface{
				PciAddress:        "0000:d8:00.1",
				NumVfs:            1,
				ExternallyManaged: false,
			}, true, nil)

			needDrain, needReboot, err := v.OnNodeStateChange(sriovNetworkNodeState)
			Expect(err).ToNot(HaveOccurred())
			Expect(needDrain).To(BeTrue())
			Expect(needReboot).To(BeFalse())
		})

		It("should return needDrain for first interface that needs reset in multiple interfaces", func() {
			sriovNetworkNodeState.Status.Interfaces = sriovnetworkv1.InterfaceExts{
				{
					Name:       "eno1",
					PciAddress: "0000:d8:00.0",
					NumVfs:     1,
				},
				{
					Name:       "eno2",
					PciAddress: "0000:d8:00.1",
					NumVfs:     1,
				},
				{
					Name:       "eno3",
					PciAddress: "0000:d8:00.2",
					NumVfs:     1,
				},
			}
			sriovNetworkNodeState.Spec.Interfaces = sriovnetworkv1.Interfaces{
				{
					Name:       "eno2",
					PciAddress: "0000:d8:00.1",
					NumVfs:     1,
					VfGroups: []sriovnetworkv1.VfGroup{{
						ResourceName: "test",
						PolicyName:   "test",
						VfRange:      "0-0",
					}},
				},
			}

			h.EXPECT().LoadPfsStatus("0000:d8:00.0").Return(&sriovnetworkv1.Interface{
				PciAddress:        "0000:d8:00.0",
				NumVfs:            1,
				ExternallyManaged: false,
			}, true, nil)

			needDrain, needReboot, err := v.OnNodeStateChange(sriovNetworkNodeState)
			Expect(err).ToNot(HaveOccurred())
			Expect(needDrain).To(BeTrue())
			Expect(needReboot).To(BeFalse())
		})

		It("should not return needDrain when multiple interfaces are removed but none were created by operator", func() {
			sriovNetworkNodeState.Status.Interfaces = sriovnetworkv1.InterfaceExts{
				{
					Name:       "eno1",
					PciAddress: "0000:d8:00.0",
					NumVfs:     1,
				},
				{
					Name:       "eno2",
					PciAddress: "0000:d8:00.1",
					NumVfs:     1,
				},
			}
			sriovNetworkNodeState.Spec.Interfaces = sriovnetworkv1.Interfaces{}

			h.EXPECT().LoadPfsStatus("0000:d8:00.0").Return(nil, false, nil)
			h.EXPECT().LoadPfsStatus("0000:d8:00.1").Return(nil, false, nil)

			needDrain, needReboot, err := v.OnNodeStateChange(sriovNetworkNodeState)
			Expect(err).ToNot(HaveOccurred())
			Expect(needDrain).To(BeFalse())
			Expect(needReboot).To(BeFalse())
		})

		It("should not return needDrain when no interfaces in status", func() {
			sriovNetworkNodeState.Status.Interfaces = sriovnetworkv1.InterfaceExts{}
			sriovNetworkNodeState.Spec.Interfaces = sriovnetworkv1.Interfaces{
				{
					Name:       "eno1",
					PciAddress: "0000:d8:00.0",
					NumVfs:     1,
					VfGroups: []sriovnetworkv1.VfGroup{{
						ResourceName: "test",
						PolicyName:   "test",
						VfRange:      "0-0",
					}},
				},
			}

			needDrain, needReboot, err := v.OnNodeStateChange(sriovNetworkNodeState)
			Expect(err).ToNot(HaveOccurred())
			Expect(needDrain).To(BeFalse())
			Expect(needReboot).To(BeFalse())
		})

		It("should handle mixed scenario with some interfaces needing drain and some not", func() {
			sriovNetworkNodeState.Status.Interfaces = sriovnetworkv1.InterfaceExts{
				{
					Name:       "eno1",
					PciAddress: "0000:d8:00.0",
					NumVfs:     1,
				},
				{
					Name:       "eno2",
					PciAddress: "0000:d8:00.1",
					NumVfs:     1,
				},
				{
					Name:       "eno3",
					PciAddress: "0000:d8:00.2",
					NumVfs:     1,
				},
			}
			sriovNetworkNodeState.Spec.Interfaces = sriovnetworkv1.Interfaces{
				{
					Name:       "eno1",
					PciAddress: "0000:d8:00.0",
					NumVfs:     1,
					VfGroups: []sriovnetworkv1.VfGroup{{
						ResourceName: "test",
						PolicyName:   "test",
						VfRange:      "0-0",
					}},
				},
			}

			// eno2 - not created by operator (doesn't exist)
			h.EXPECT().LoadPfsStatus("0000:d8:00.1").Return(nil, false, nil)
			// eno3 - externally managed
			h.EXPECT().LoadPfsStatus("0000:d8:00.2").Return(&sriovnetworkv1.Interface{
				PciAddress:        "0000:d8:00.2",
				NumVfs:            1,
				ExternallyManaged: true,
			}, true, nil)

			needDrain, needReboot, err := v.OnNodeStateChange(sriovNetworkNodeState)
			Expect(err).ToNot(HaveOccurred())
			Expect(needDrain).To(BeFalse())
			Expect(needReboot).To(BeFalse())
		})

		It("should combine vfio driver loading and drain node checks", func() {
			sriovNetworkNodeState.Status.Interfaces = sriovnetworkv1.InterfaceExts{
				{
					Name:       "eno1",
					PciAddress: "0000:d8:00.0",
					NumVfs:     1,
				},
			}
			sriovNetworkNodeState.Spec.Interfaces = sriovnetworkv1.Interfaces{
				{
					Name:       "eno2",
					PciAddress: "0000:d8:00.1",
					NumVfs:     1,
					VfGroups: []sriovnetworkv1.VfGroup{{
						ResourceName: "test",
						PolicyName:   "test",
						VfRange:      "0-0",
						DeviceType:   consts.DeviceTypeVfioPci,
					}},
				},
			}

			h.EXPECT().LoadPfsStatus("0000:d8:00.0").Return(&sriovnetworkv1.Interface{
				PciAddress:        "0000:d8:00.0",
				NumVfs:            1,
				ExternallyManaged: false,
			}, true, nil)

			Expect(v.LoadVfioDriver).To(Equal(uint(unloaded)))
			needDrain, needReboot, err := v.OnNodeStateChange(sriovNetworkNodeState)
			Expect(err).ToNot(HaveOccurred())
			Expect(needDrain).To(BeTrue())
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
			h.EXPECT().ConfigSriovDevicesVirtual(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
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
