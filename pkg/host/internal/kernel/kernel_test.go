package kernel

import (
	"fmt"
	"path"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"go.uber.org/mock/gomock"

	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/consts"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/host/types"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/utils"
	mock_utils "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/utils/mock"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/vars"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/test/util/fakefilesystem"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/test/util/helpers"
)

var _ = Describe("Kernel", func() {
	Context("Drivers", func() {
		var (
			k        types.KernelInterface
			kMocked  types.KernelInterface
			u        *mock_utils.MockCmdInterface
			t        FullGinkgoTInterface
			mockCtrl *gomock.Controller
		)
		BeforeEach(func() {
			k = New(utils.New())

			t = GinkgoT()
			mockCtrl = gomock.NewController(t)
			u = mock_utils.NewMockCmdInterface(mockCtrl)
			kMocked = New(u)
		})

		AfterEach(func() {
			Expect(mockCtrl.Satisfied()).To(BeTrue())
		})

		Context("Unbind, UnbindDriverByBusAndDevice", func() {
			It("unknown device", func() {
				Expect(k.UnbindDriverByBusAndDevice(consts.BusPci, "unknown-dev")).NotTo(HaveOccurred())
			})
			It("known device, no driver", func() {
				helpers.GinkgoConfigureFakeFS(&fakefilesystem.FS{Dirs: []string{"/sys/bus/pci/devices/0000:d8:00.0"}})
				Expect(k.Unbind("0000:d8:00.0")).NotTo(HaveOccurred())
			})
			It("has driver, succeed", func() {
				helpers.GinkgoConfigureFakeFS(&fakefilesystem.FS{
					Dirs: []string{
						"/sys/bus/pci/devices/0000:d8:00.0",
						"/sys/bus/pci/drivers/test-driver"},
					Symlinks: map[string]string{
						"/sys/bus/pci/devices/0000:d8:00.0/driver": "../../../../bus/pci/drivers/test-driver"},
					Files: map[string][]byte{
						"/sys/bus/pci/drivers/test-driver/unbind": {}},
				})
				Expect(k.Unbind("0000:d8:00.0")).NotTo(HaveOccurred())
				// check that echo to unbind path was done
				helpers.GinkgoAssertFileContentsEquals("/sys/bus/pci/drivers/test-driver/unbind", "0000:d8:00.0")
			})
			It("has driver, failed to unbind", func() {
				helpers.GinkgoConfigureFakeFS(&fakefilesystem.FS{
					Dirs: []string{
						"/sys/bus/pci/devices/0000:d8:00.0"},
					Symlinks: map[string]string{
						"/sys/bus/pci/devices/0000:d8:00.0/driver": "../../../../bus/pci/drivers/test-driver"},
				})
				Expect(k.Unbind("0000:d8:00.0")).To(HaveOccurred())
			})
		})

		Context("LoadKernelModule", func() {
			It("should return still try to load the kernel module if not able to check if it's loaded", func() {
				u.EXPECT().RunCommand("/bin/sh", "-c", fmt.Sprintf("chroot %s lsmod | grep \"^tun\"", getHost())).Return("", "lsmod failed", fmt.Errorf("lsmod failed"))
				u.EXPECT().RunCommand("/bin/sh", "-c", fmt.Sprintf("chroot %s modprobe tun ", getHost())).Return("", "", nil)
				err := kMocked.LoadKernelModule("tun")
				Expect(err).ToNot(HaveOccurred())
			})

			It("should not try to to load the driver if it's already loaded", func() {
				u.EXPECT().RunCommand("/bin/sh", "-c", fmt.Sprintf("chroot %s lsmod | grep \"^tun\"", getHost())).Return("tun", "", nil)
				err := kMocked.LoadKernelModule("tun")
				Expect(err).ToNot(HaveOccurred())
			})

			It("should return error if not able to load kernel module", func() {
				u.EXPECT().RunCommand("/bin/sh", "-c", fmt.Sprintf("chroot %s lsmod | grep \"^tun\"", getHost())).Return("", "", nil)
				u.EXPECT().RunCommand("/bin/sh", "-c", fmt.Sprintf("chroot %s modprobe tun ", getHost())).Return("", "", fmt.Errorf("failed to run modprobe"))
				err := kMocked.LoadKernelModule("tun")
				Expect(err).To(HaveOccurred())
			})

			It("should pass the args to the modprobe command", func() {
				u.EXPECT().RunCommand("/bin/sh", "-c", fmt.Sprintf("chroot %s lsmod | grep \"^tun\"", getHost())).Return("", "", nil)
				u.EXPECT().RunCommand("/bin/sh", "-c", fmt.Sprintf("chroot %s modprobe tun test=ok", getHost())).Return("", "", nil)
				err := kMocked.LoadKernelModule("tun", "test=ok")
				Expect(err).ToNot(HaveOccurred())
			})
		})

		Context("IsKernelModuleLoaded", func() {
			It("should return error if not able to run lsmod command", func() {
				u.EXPECT().RunCommand("/bin/sh", "-c", fmt.Sprintf("chroot %s lsmod | grep \"^tun\"", getHost())).Return("", "lsmod failed", fmt.Errorf("lsmod failed"))
				enabled, err := kMocked.IsKernelModuleLoaded("tun")
				Expect(err).To(HaveOccurred())
				Expect(enabled).To(BeFalse())
			})

			It("should return error if stderr is not empty", func() {
				u.EXPECT().RunCommand("/bin/sh", "-c", fmt.Sprintf("chroot %s lsmod | grep \"^tun\"", getHost())).Return("", "lsmod failed", nil)
				enabled, err := kMocked.IsKernelModuleLoaded("tun")
				Expect(err).To(HaveOccurred())
				Expect(enabled).To(BeFalse())
			})

			It("should return false if std is empty", func() {
				u.EXPECT().RunCommand("/bin/sh", "-c", fmt.Sprintf("chroot %s lsmod | grep \"^tun\"", getHost())).Return("", "", nil)
				enabled, err := kMocked.IsKernelModuleLoaded("tun")
				Expect(err).ToNot(HaveOccurred())
				Expect(enabled).To(BeFalse())
			})

			It("should return true if std is not empty", func() {
				u.EXPECT().RunCommand("/bin/sh", "-c", fmt.Sprintf("chroot %s lsmod | grep \"^tun\"", getHost())).Return("tun", "", nil)
				enabled, err := kMocked.IsKernelModuleLoaded("tun")
				Expect(err).ToNot(HaveOccurred())
				Expect(enabled).To(BeTrue())
			})
		})

		Context("GetCurrentKernelArgs", func() {
			It("should return error if not able to read the cmdline file", func() {
				cmdline, err := k.GetCurrentKernelArgs()
				Expect(err).To(HaveOccurred())
				Expect(cmdline).To(Equal(""))
			})

			It("should return cmdline", func() {
				helpers.GinkgoConfigureFakeFS(&fakefilesystem.FS{
					Dirs: []string{
						"/host/proc"},
					Files: map[string][]byte{
						"/host/proc/cmdline": []byte("iommu=pt")},
				})

				cmdline, err := k.GetCurrentKernelArgs()
				Expect(err).ToNot(HaveOccurred())
				Expect(cmdline).To(Equal("iommu=pt"))
			})

			It("should read the file without the /host when running on systemd mode", func() {
				helpers.GinkgoConfigureFakeFS(&fakefilesystem.FS{
					Dirs: []string{
						"/proc"},
					Files: map[string][]byte{
						"/proc/cmdline": []byte("iommu=pt")},
				})
				vars.UsingSystemdMode = true
				defer func() {
					vars.UsingSystemdMode = false
				}()

				cmdline, err := k.GetCurrentKernelArgs()
				Expect(err).ToNot(HaveOccurred())
				Expect(cmdline).To(Equal("iommu=pt"))
			})
		})

		Context("IsKernelArgsSet", func() {
			It("should return false if the kernel arg does not exist is cmdline", func() {
				set := k.IsKernelArgsSet("iommu=pt", consts.KernelArgIntelIommu)
				Expect(set).To(BeFalse())
			})

			It("should return true if the kernel arg exist in the cmdline", func() {
				set := k.IsKernelArgsSet("iommu=pt", consts.KernelArgIommuPt)
				Expect(set).To(BeTrue())
			})
		})

		Context("RebindVfToDefaultDriver", func() {
			It("should failed to rebind if unbind failed the driver", func() {
				helpers.GinkgoConfigureFakeFS(&fakefilesystem.FS{
					Dirs: []string{
						"/sys/bus/pci/devices/0000:d8:00.0"},
					Symlinks: map[string]string{
						"/sys/bus/pci/devices/0000:d8:00.0/driver": "../../../../bus/pci/drivers/test-driver"},
				})

				err := k.RebindVfToDefaultDriver("0000:d8:00.0")
				Expect(err).To(HaveOccurred())
			})

			It("should be able to rebind the vf to the default driver", func() {
				helpers.GinkgoConfigureFakeFS(&fakefilesystem.FS{
					Dirs: []string{
						"/sys/bus/pci/devices/0000:d8:00.0",
						"/sys/bus/pci/drivers/test-driver"},
					Symlinks: map[string]string{},
					Files: map[string][]byte{
						"/sys/bus/pci/drivers/test-driver/unbind": {}},
				})

				err := k.RebindVfToDefaultDriver("0000:d8:00.0")
				Expect(err).ToNot(HaveOccurred())
			})
		})

		Context("UnbindDriverIfNeeded", func() {
			It("should always return nil for non rdma", func() {
				err := k.UnbindDriverIfNeeded("0000:d8:00.0", false)
				Expect(err).ToNot(HaveOccurred())
			})
			It("should called unbind functions", func() {
				helpers.GinkgoConfigureFakeFS(&fakefilesystem.FS{
					Dirs: []string{
						"/sys/bus/pci/devices/0000:d8:00.0",
						"/sys/bus/pci/drivers/test-driver"},
					Symlinks: map[string]string{
						"/sys/bus/pci/devices/0000:d8:00.0/driver": "../../../../bus/pci/drivers/test-driver"},
					Files: map[string][]byte{
						"/sys/bus/pci/drivers/test-driver/unbind": {}},
				})

				err := k.UnbindDriverIfNeeded("0000:d8:00.0", true)
				Expect(err).ToNot(HaveOccurred())

				// check that echo to unbind path was done
				helpers.GinkgoAssertFileContentsEquals("/sys/bus/pci/drivers/test-driver/unbind", "0000:d8:00.0")
			})
		})

		Context("HasDriver", func() {
			It("unknown device", func() {
				has, driver := k.HasDriver("unknown-dev")
				Expect(has).To(BeFalse())
				Expect(driver).To(BeEmpty())
			})
			It("known device, no driver", func() {
				helpers.GinkgoConfigureFakeFS(&fakefilesystem.FS{Dirs: []string{"/sys/bus/pci/devices/0000:d8:00.0"}})
				has, driver := k.HasDriver("0000:d8:00.0")
				Expect(has).To(BeFalse())
				Expect(driver).To(BeEmpty())
			})
			It("has driver", func() {
				helpers.GinkgoConfigureFakeFS(&fakefilesystem.FS{
					Dirs: []string{
						"/sys/bus/pci/devices/0000:d8:00.0",
						"/sys/bus/pci/drivers/test-driver"},
					Symlinks: map[string]string{
						"/sys/bus/pci/devices/0000:d8:00.0/driver": "../../../../bus/pci/drivers/test-driver"},
				})
				has, driver := k.HasDriver("0000:d8:00.0")
				Expect(has).To(BeTrue())
				Expect(driver).To(Equal("test-driver"))
			})
		})

		Context("BindDefaultDriver", func() {
			It("unknown device", func() {
				Expect(k.BindDefaultDriver("unknown-dev")).To(HaveOccurred())
			})
			It("no driver", func() {
				helpers.GinkgoConfigureFakeFS(&fakefilesystem.FS{
					Dirs: []string{
						"/sys/bus/pci/devices/0000:d8:00.0"},
					Files: map[string][]byte{
						"/sys/bus/pci/drivers_probe": {}, "/sys/bus/pci/devices/0000:d8:00.0/driver_override": {}},
				})
				Expect(k.BindDefaultDriver("0000:d8:00.0")).NotTo(HaveOccurred())
				// should probe driver for dev
				helpers.GinkgoAssertFileContentsEquals("/sys/bus/pci/drivers_probe", "0000:d8:00.0")
			})
			It("already bind to default driver", func() {
				helpers.GinkgoConfigureFakeFS(&fakefilesystem.FS{
					Dirs: []string{
						"/sys/bus/pci/devices/0000:d8:00.0"},
					Symlinks: map[string]string{
						"/sys/bus/pci/devices/0000:d8:00.0/driver": "../../../../bus/pci/drivers/test-driver"},
				})
				Expect(k.BindDefaultDriver("0000:d8:00.0")).NotTo(HaveOccurred())
			})
			It("bind to dpdk driver", func() {
				helpers.GinkgoConfigureFakeFS(&fakefilesystem.FS{
					Dirs: []string{
						"/sys/bus/pci/devices/0000:d8:00.0",
						"/sys/bus/pci/drivers/vfio-pci"},
					Symlinks: map[string]string{
						"/sys/bus/pci/devices/0000:d8:00.0/driver": "../../../../bus/pci/drivers/vfio-pci"},
					Files: map[string][]byte{
						"/sys/bus/pci/drivers_probe":           {},
						"/sys/bus/pci/drivers/vfio-pci/unbind": {}},
				})
				Expect(k.BindDefaultDriver("0000:d8:00.0")).NotTo(HaveOccurred())
				// should unbind from dpdk driver
				helpers.GinkgoAssertFileContentsEquals("/sys/bus/pci/drivers/vfio-pci/unbind", "0000:d8:00.0")
				// should probe driver for dev
				helpers.GinkgoAssertFileContentsEquals("/sys/bus/pci/drivers_probe", "0000:d8:00.0")
			})
		})

		Context("BindDpdkDriver", func() {
			It("unknown device", func() {
				Expect(k.BindDpdkDriver("unknown-dev", "vfio-pci")).To(HaveOccurred())
			})
			It("no driver", func() {
				helpers.GinkgoConfigureFakeFS(&fakefilesystem.FS{
					Dirs: []string{
						"/sys/bus/pci/devices/0000:d8:00.0",
						"/sys/bus/pci/drivers/vfio-pci"},
					Files: map[string][]byte{
						"/sys/bus/pci/devices/0000:d8:00.0/driver_override": {}},
				})
				Expect(k.BindDpdkDriver("0000:d8:00.0", "vfio-pci")).NotTo(HaveOccurred())
				// should reset driver override
				helpers.GinkgoAssertFileContentsEquals("/sys/bus/pci/devices/0000:d8:00.0/driver_override", "\x00")
			})
			It("already bind to required driver", func() {
				helpers.GinkgoConfigureFakeFS(&fakefilesystem.FS{
					Dirs: []string{
						"/sys/bus/pci/devices/0000:d8:00.0"},
					Symlinks: map[string]string{
						"/sys/bus/pci/devices/0000:d8:00.0/driver": "../../../../bus/pci/drivers/vfio-pci"},
				})
				Expect(k.BindDpdkDriver("0000:d8:00.0", "vfio-pci")).NotTo(HaveOccurred())
			})
			It("bind to wrong driver", func() {
				helpers.GinkgoConfigureFakeFS(&fakefilesystem.FS{
					Dirs: []string{
						"/sys/bus/pci/devices/0000:d8:00.0",
						"/sys/bus/pci/drivers/test-driver",
						"/sys/bus/pci/drivers/vfio-pci"},
					Symlinks: map[string]string{
						"/sys/bus/pci/devices/0000:d8:00.0/driver": "../../../../bus/pci/drivers/test-driver"},
					Files: map[string][]byte{
						"/sys/bus/pci/drivers/test-driver/unbind":           {},
						"/sys/bus/pci/drivers/vfio-pci/bind":                {},
						"/sys/bus/pci/devices/0000:d8:00.0/driver_override": {}},
				})
				Expect(k.BindDpdkDriver("0000:d8:00.0", "vfio-pci")).NotTo(HaveOccurred())
				// should unbind from driver1
				helpers.GinkgoAssertFileContentsEquals("/sys/bus/pci/drivers/test-driver/unbind", "0000:d8:00.0")
				// should bind to driver2
				helpers.GinkgoAssertFileContentsEquals("/sys/bus/pci/drivers/vfio-pci/bind", "0000:d8:00.0")
			})
			It("fail to bind", func() {
				helpers.GinkgoConfigureFakeFS(&fakefilesystem.FS{
					Dirs: []string{
						"/sys/bus/pci/devices/0000:d8:00.0",
						"/sys/bus/pci/drivers/test-driver"},
					Symlinks: map[string]string{
						"/sys/bus/pci/devices/0000:d8:00.0/driver": "../../../../bus/pci/drivers/test-driver"},
					Files: map[string][]byte{
						"/sys/bus/pci/drivers/test-driver/unbind":           {},
						"/sys/bus/pci/devices/0000:d8:00.0/driver_override": {}},
				})
				Expect(k.BindDpdkDriver("0000:d8:00.0", "vfio-pci")).To(HaveOccurred())
			})
		})

		Context("BindDriverByBusAndDevice", func() {
			It("device doesn't support driver_override", func() {
				helpers.GinkgoConfigureFakeFS(&fakefilesystem.FS{
					Dirs: []string{
						"/sys/bus/pci/devices/0000:d8:00.0",
						"/sys/bus/pci/drivers/test-driver",
						"/sys/bus/pci/drivers/vfio-pci"},
					Symlinks: map[string]string{
						"/sys/bus/pci/devices/0000:d8:00.0/driver": "../../../../bus/pci/drivers/test-driver"},
					Files: map[string][]byte{
						"/sys/bus/pci/drivers/test-driver/unbind": {},
						"/sys/bus/pci/drivers/vfio-pci/bind":      {}},
				})
				Expect(k.BindDriverByBusAndDevice(consts.BusPci, "0000:d8:00.0", "vfio-pci")).NotTo(HaveOccurred())
				// should unbind from driver1
				helpers.GinkgoAssertFileContentsEquals("/sys/bus/pci/drivers/test-driver/unbind", "0000:d8:00.0")
				// should bind to driver2
				helpers.GinkgoAssertFileContentsEquals("/sys/bus/pci/drivers/vfio-pci/bind", "0000:d8:00.0")
			})
		})

		Context("GetDriverByBusAndDevice", func() {
			It("device has driver", func() {
				helpers.GinkgoConfigureFakeFS(&fakefilesystem.FS{
					Dirs: []string{
						"/sys/bus/vdpa/devices/vdpa:0000:d8:00.3"},
					Symlinks: map[string]string{
						"/sys/bus/vdpa/devices/vdpa:0000:d8:00.3/driver": "../../../../../bus/vdpa/drivers/vhost_vdpa"},
				})
				driver, err := k.GetDriverByBusAndDevice(consts.BusVdpa, "vdpa:0000:d8:00.3")
				Expect(err).NotTo(HaveOccurred())
				Expect(driver).To(Equal("vhost_vdpa"))
			})
			It("device has no driver", func() {
				helpers.GinkgoConfigureFakeFS(&fakefilesystem.FS{
					Dirs: []string{
						"/sys/bus/vdpa/devices/vdpa:0000:d8:00.3"},
				})
				driver, err := k.GetDriverByBusAndDevice(consts.BusVdpa, "vdpa:0000:d8:00.3")
				Expect(err).NotTo(HaveOccurred())
				Expect(driver).To(BeEmpty())
			})
		})

		Context("IsKernelLockdownMode", func() {
			It("should return true when kernel boots in lockdown integrity", func() {
				helpers.GinkgoConfigureFakeFS(&fakefilesystem.FS{
					Dirs: []string{"/host/sys/kernel/security"},
					Files: map[string][]byte{
						"/host/sys/kernel/security/lockdown": []byte("none [integrity] confidentiality")},
				})

				Expect(k.IsKernelLockdownMode()).To(BeTrue())
			})

			It("should return false when kernel lockdown is none", func() {
				helpers.GinkgoConfigureFakeFS(&fakefilesystem.FS{
					Dirs: []string{"/host/sys/kernel/security"},
					Files: map[string][]byte{
						"/host/sys/kernel/security/lockdown": []byte("[none] integrity confidentiality")},
				})

				Expect(k.IsKernelLockdownMode()).To(BeFalse())
			})

			It("should return false if there is an error running the cat command", func() {
				u.EXPECT().RunCommand("cat", fmt.Sprintf("%s/host/sys/kernel/security/lockdown", vars.FilesystemRoot)).Return("", "", fmt.Errorf("file doesn't exist"))
				loaded := kMocked.IsKernelLockdownMode()
				Expect(loaded).To(BeFalse())
				Expect(mockCtrl.Satisfied()).To(BeTrue())
			})
		})

		Context("TryEnableTun", func() {
			It("should load tun kernel module", func() {
				u.EXPECT().RunCommand("/bin/sh", "-c", fmt.Sprintf("chroot %s lsmod | grep \"^tun\"", getHost())).Return("", "", nil)
				u.EXPECT().RunCommand("/bin/sh", "-c", fmt.Sprintf("chroot %s modprobe tun ", getHost())).Return("", "", nil)
				kMocked.TryEnableTun()
				Expect(mockCtrl.Satisfied()).To(BeTrue())
			})
		})

		Context("TryEnableVhostNet", func() {
			It("should load tun kernel module", func() {
				u.EXPECT().RunCommand("/bin/sh", "-c", fmt.Sprintf("chroot %s lsmod | grep \"^vhost_net\"", getHost())).Return("", "", nil)
				u.EXPECT().RunCommand("/bin/sh", "-c", fmt.Sprintf("chroot %s modprobe vhost_net ", getHost())).Return("", "", nil)
				kMocked.TryEnableVhostNet()
				Expect(mockCtrl.Satisfied()).To(BeTrue())
			})
		})

		Context("CheckRDMAEnabled", func() {
			It("should return error if lsmod command failed", func() {
				u.EXPECT().RunCommand("/bin/sh", "-c", fmt.Sprintf("chroot %s lsmod | grep --quiet 'mlx5_core'", getHost())).Return("", "lsmod failed", fmt.Errorf("lsmod failed"))
				enabled, err := kMocked.CheckRDMAEnabled()
				Expect(err).To(HaveOccurred())
				Expect(enabled).To(BeFalse())
			})

			It("should return false if no RDMA capable devices exist", func() {
				u.EXPECT().RunCommand("/bin/sh", "-c", fmt.Sprintf("chroot %s lsmod | grep --quiet 'mlx5_core'", getHost())).Return("", "", fmt.Errorf("lsmod failed"))
				enabled, err := kMocked.CheckRDMAEnabled()
				Expect(err).ToNot(HaveOccurred())
				Expect(enabled).To(BeFalse())
			})

			It("should return error if ib and rdma lsmod command failed", func() {
				u.EXPECT().RunCommand("/bin/sh", "-c", fmt.Sprintf("chroot %s lsmod | grep --quiet 'mlx5_core'", getHost())).Return("", "", nil)
				u.EXPECT().RunCommand("/bin/sh", "-c", fmt.Sprintf("chroot %s lsmod | grep --quiet '\\(^ib\\|^rdma\\)'", getHost())).Return("", "lsmod failed", fmt.Errorf("lsmod failed"))
				enabled, err := kMocked.CheckRDMAEnabled()
				Expect(err).To(HaveOccurred())
				Expect(enabled).To(BeFalse())
			})

			It("should return false if ib and rdma lsmod command return empty", func() {
				u.EXPECT().RunCommand("/bin/sh", "-c", fmt.Sprintf("chroot %s lsmod | grep --quiet 'mlx5_core'", getHost())).Return("", "", nil)
				u.EXPECT().RunCommand("/bin/sh", "-c", fmt.Sprintf("chroot %s lsmod | grep --quiet '\\(^ib\\|^rdma\\)'", getHost())).Return("", "", fmt.Errorf("lsmod failed"))
				enabled, err := kMocked.CheckRDMAEnabled()
				Expect(err).ToNot(HaveOccurred())
				Expect(enabled).To(BeFalse())
			})

			It("should return true if ib and rdma are loaded", func() {
				u.EXPECT().RunCommand("/bin/sh", "-c", fmt.Sprintf("chroot %s lsmod | grep --quiet 'mlx5_core'", getHost())).Return("", "", nil)
				u.EXPECT().RunCommand("/bin/sh", "-c", fmt.Sprintf("chroot %s lsmod | grep --quiet '\\(^ib\\|^rdma\\)'", getHost())).Return("", "", nil)
				enabled, err := kMocked.CheckRDMAEnabled()
				Expect(err).ToNot(HaveOccurred())
				Expect(enabled).To(BeTrue())
			})
		})
	})
})

func getHost() string {
	return path.Join(vars.FilesystemRoot, "/host")
}
