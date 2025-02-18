package udev

import (
	"fmt"
	"os"
	"path/filepath"

	"go.uber.org/mock/gomock"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/host/types"
	utilsMockPkg "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/utils/mock"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/vars"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/test/util/fakefilesystem"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/test/util/helpers"
)

const (
	testExpectedPFUdevRule = `SUBSYSTEM=="net", ACTION=="add", DRIVERS=="?*", KERNELS=="0000:d8:00.0", NAME="enp129"`
	testExpectedNMUdevRule = `SUBSYSTEM=="net", ACTION=="add|change|move", ` +
		`ATTRS{device}=="0x1017|0x1018", ` +
		`IMPORT{program}="/etc/udev/disable-nm-sriov.sh $env{INTERFACE} 0000:d8:00.0"`
	testExpectedSwitchdevUdevRule = `SUBSYSTEM=="net", ACTION=="add|move", ` +
		`ATTRS{phys_switch_id}=="7cfe90ff2cc0", ` +
		`ATTR{phys_port_name}=="pf0vf*", IMPORT{program}="/etc/udev/switchdev-vf-link-name.sh $attr{phys_port_name}", ` +
		`NAME="enp216s0f0np0_$env{NUMBER}"`
)

var _ = Describe("UDEV", func() {
	var (
		s         types.UdevInterface
		testCtrl  *gomock.Controller
		utilsMock *utilsMockPkg.MockCmdInterface
		testError = fmt.Errorf("test")
	)

	BeforeEach(func() {
		testCtrl = gomock.NewController(GinkgoT())
		utilsMock = utilsMockPkg.NewMockCmdInterface(testCtrl)
		s = New(utilsMock)
	})

	AfterEach(func() {
		testCtrl.Finish()
	})

	Context("AddDisableNMUdevRule", func() {
		It("Created", func() {
			helpers.GinkgoConfigureFakeFS(&fakefilesystem.FS{})
			Expect(s.AddDisableNMUdevRule("0000:d8:00.0")).To(BeNil())
			helpers.GinkgoAssertFileContentsEquals(
				"/etc/udev/rules.d/10-nm-disable-0000:d8:00.0.rules",
				testExpectedNMUdevRule)
		})
		It("Overwrite", func() {
			helpers.GinkgoConfigureFakeFS(&fakefilesystem.FS{
				Dirs: []string{"/etc/udev/rules.d"},
				Files: map[string][]byte{
					"/etc/udev/rules.d/10-nm-disable-0000:d8:00.0.rules": []byte("something"),
				},
			})
			Expect(s.AddDisableNMUdevRule("0000:d8:00.0")).To(BeNil())
			helpers.GinkgoAssertFileContentsEquals(
				"/etc/udev/rules.d/10-nm-disable-0000:d8:00.0.rules",
				testExpectedNMUdevRule)
		})
	})
	Context("RemoveDisableNMUdevRule", func() {
		It("Exist", func() {
			helpers.GinkgoConfigureFakeFS(&fakefilesystem.FS{
				Dirs: []string{"/etc/udev/rules.d"},
				Files: map[string][]byte{
					"/etc/udev/rules.d/10-nm-disable-0000:d8:00.0.rules": []byte(testExpectedNMUdevRule),
				},
			})
			Expect(s.RemoveDisableNMUdevRule("0000:d8:00.0")).To(BeNil())
			_, err := os.Stat(filepath.Join(vars.FilesystemRoot,
				"/etc/udev/rules.d/10-nm-disable-0000:d8:00.0.rules"))
			Expect(os.IsNotExist(err)).To(BeTrue())
		})
		It("Not found", func() {
			helpers.GinkgoConfigureFakeFS(&fakefilesystem.FS{
				Dirs: []string{"/etc/udev/rules.d"},
			})
			Expect(s.RemoveDisableNMUdevRule("0000:d8:00.0")).To(BeNil())
		})
	})
	Context("AddPersistPFNameUdevRule", func() {
		It("Created", func() {
			helpers.GinkgoConfigureFakeFS(&fakefilesystem.FS{})
			Expect(s.AddPersistPFNameUdevRule("0000:d8:00.0", "enp129")).To(BeNil())
			helpers.GinkgoAssertFileContentsEquals(
				"/etc/udev/rules.d/10-pf-name-0000:d8:00.0.rules",
				testExpectedPFUdevRule)

		})
		It("Overwrite", func() {
			helpers.GinkgoConfigureFakeFS(&fakefilesystem.FS{
				Dirs: []string{"/etc/udev/rules.d"},
				Files: map[string][]byte{
					"etc/udev/rules.d/10-pf-name-0000:d8:00.0.rules": []byte("something"),
				},
			})
			Expect(s.AddPersistPFNameUdevRule("0000:d8:00.0", "enp129")).To(BeNil())
			helpers.GinkgoAssertFileContentsEquals(
				"/etc/udev/rules.d/10-pf-name-0000:d8:00.0.rules",
				testExpectedPFUdevRule)
		})
	})
	Context("RemovePersistPFNameUdevRule", func() {
		It("Exist", func() {
			helpers.GinkgoConfigureFakeFS(&fakefilesystem.FS{
				Dirs: []string{"/etc/udev/rules.d"},
				Files: map[string][]byte{
					"/etc/udev/rules.d/10-pf-name-0000:d8:00.0.rules": []byte(testExpectedPFUdevRule),
				},
			})
			Expect(s.RemovePersistPFNameUdevRule("0000:d8:00.0")).To(BeNil())
			_, err := os.Stat(filepath.Join(vars.FilesystemRoot,
				"/etc/udev/rules.d/10-pf-name-0000:d8:00.0.rules"))
			Expect(os.IsNotExist(err)).To(BeTrue())
		})
		It("Not found", func() {
			helpers.GinkgoConfigureFakeFS(&fakefilesystem.FS{
				Dirs: []string{"/etc/udev/rules.d"},
			})
			Expect(s.RemovePersistPFNameUdevRule("0000:d8:00.0")).To(BeNil())
		})
	})
	Context("AddVfRepresentorUdevRule", func() {
		It("Created", func() {
			helpers.GinkgoConfigureFakeFS(&fakefilesystem.FS{})
			Expect(s.AddVfRepresentorUdevRule("0000:d8:00.0",
				"enp216s0f0np0", "7cfe90ff2cc0", "p0")).To(BeNil())
			helpers.GinkgoAssertFileContentsEquals(
				"/etc/udev/rules.d/20-switchdev-0000:d8:00.0.rules",
				testExpectedSwitchdevUdevRule)
		})
		It("Overwrite", func() {
			helpers.GinkgoConfigureFakeFS(&fakefilesystem.FS{
				Dirs: []string{"/etc/udev/rules.d"},
				Files: map[string][]byte{
					"/etc/udev/rules.d/20-switchdev-0000:d8:00.0.rules": []byte("something"),
				},
			})
			Expect(s.AddVfRepresentorUdevRule("0000:d8:00.0",
				"enp216s0f0np0", "7cfe90ff2cc0", "p0")).To(BeNil())
			helpers.GinkgoAssertFileContentsEquals(
				"/etc/udev/rules.d/20-switchdev-0000:d8:00.0.rules",
				testExpectedSwitchdevUdevRule)
		})
	})
	Context("RemoveVfRepresentorUdevRule", func() {
		It("Exist", func() {
			helpers.GinkgoConfigureFakeFS(&fakefilesystem.FS{
				Dirs: []string{"/etc/udev/rules.d"},
				Files: map[string][]byte{
					"/etc/udev/rules.d/20-switchdev-0000:d8:00.0.rules": []byte(testExpectedSwitchdevUdevRule),
				},
			})
			Expect(s.RemoveVfRepresentorUdevRule("0000:d8:00.0")).To(BeNil())
			_, err := os.Stat(filepath.Join(vars.FilesystemRoot,
				"/etc/udev/rules.d/20-switchdev-0000:d8:00.0.rules"))
			Expect(os.IsNotExist(err)).To(BeTrue())
		})
		It("Not found", func() {
			helpers.GinkgoConfigureFakeFS(&fakefilesystem.FS{
				Dirs: []string{"/etc/udev/rules.d"},
			})
			Expect(s.RemoveVfRepresentorUdevRule("0000:d8:00.0")).To(BeNil())
		})
	})
	Context("PrepareVFRepUdevRule", func() {
		It("Already Exist", func() {
			helpers.GinkgoConfigureFakeFS(&fakefilesystem.FS{
				Dirs: []string{"/host/etc/udev", "/bindata/scripts"},
				Files: map[string][]byte{
					"/host/etc/udev/switchdev-vf-link-name.sh":   []byte("before"),
					"/bindata/scripts/switchdev-vf-link-name.sh": []byte("script"),
				},
			})
			Expect(s.PrepareVFRepUdevRule()).To(BeNil())
			helpers.GinkgoAssertFileContentsEquals("/host/etc/udev/switchdev-vf-link-name.sh", "script")
		})
		It("Fail - no folder", func() {
			helpers.GinkgoConfigureFakeFS(&fakefilesystem.FS{
				Dirs: []string{"/bindata/scripts"},
				Files: map[string][]byte{
					"/bindata/scripts/switchdev-vf-link-name.sh": []byte("script"),
				},
			})
			Expect(s.PrepareVFRepUdevRule()).NotTo(BeNil())
		})
	})
	Context("LoadUdevRules", func() {
		It("Succeed", func() {
			utilsMock.EXPECT().RunCommand("udevadm", "control", "--reload-rules").Return("", "", nil)
			utilsMock.EXPECT().RunCommand("udevadm", "trigger", "--action", "add", "--attr-match", "subsystem=net").Return("", "", nil)
			Expect(s.LoadUdevRules()).NotTo(HaveOccurred())
		})
		It("Failed to reload rules", func() {
			utilsMock.EXPECT().RunCommand("udevadm", "control", "--reload-rules").Return("", "", testError)
			Expect(s.LoadUdevRules()).To(MatchError(testError))
		})
		It("Failed to trigger rules", func() {
			utilsMock.EXPECT().RunCommand("udevadm", "control", "--reload-rules").Return("", "", nil)
			utilsMock.EXPECT().RunCommand("udevadm", "trigger", "--action", "add", "--attr-match", "subsystem=net").Return("", "", testError)
			Expect(s.LoadUdevRules()).To(MatchError(testError))
		})
	})
	Context("WaitUdevEventsProcessed", func() {
		It("Succeed", func() {
			utilsMock.EXPECT().RunCommand("udevadm", "settle", "-t", "10").Return("", "", nil)
			Expect(s.WaitUdevEventsProcessed(10)).NotTo(HaveOccurred())
		})
		It("Command Failed", func() {
			utilsMock.EXPECT().RunCommand("udevadm", "settle", "-t", "20").Return("", "", testError)
			Expect(s.WaitUdevEventsProcessed(20)).To(MatchError(testError))
		})
	})
})
