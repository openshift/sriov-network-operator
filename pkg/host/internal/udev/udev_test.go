package udev

import (
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/host/types"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/vars"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/test/util/fakefilesystem"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/test/util/helpers"
)

const (
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
		s types.UdevInterface
	)
	BeforeEach(func() {
		s = New(nil)
	})
	Context("AddUdevRule", func() {
		It("Created", func() {
			helpers.GinkgoConfigureFakeFS(&fakefilesystem.FS{})
			Expect(s.AddUdevRule("0000:d8:00.0")).To(BeNil())
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
			Expect(s.AddUdevRule("0000:d8:00.0")).To(BeNil())
			helpers.GinkgoAssertFileContentsEquals(
				"/etc/udev/rules.d/10-nm-disable-0000:d8:00.0.rules",
				testExpectedNMUdevRule)
		})
	})
	Context("RemoveUdevRule", func() {
		It("Exist", func() {
			helpers.GinkgoConfigureFakeFS(&fakefilesystem.FS{
				Dirs: []string{"/etc/udev/rules.d"},
				Files: map[string][]byte{
					"/etc/udev/rules.d/10-nm-disable-0000:d8:00.0.rules": []byte(testExpectedNMUdevRule),
				},
			})
			Expect(s.RemoveUdevRule("0000:d8:00.0")).To(BeNil())
			_, err := os.Stat(filepath.Join(vars.FilesystemRoot,
				"/etc/udev/rules.d/10-nm-disable-0000:d8:00.0.rules"))
			Expect(os.IsNotExist(err)).To(BeTrue())
		})
		It("Not found", func() {
			helpers.GinkgoConfigureFakeFS(&fakefilesystem.FS{
				Dirs: []string{"/etc/udev/rules.d"},
			})
			Expect(s.RemoveUdevRule("0000:d8:00.0")).To(BeNil())
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
})
