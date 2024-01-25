package helpers

import (
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/vars"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/test/util/fakefilesystem"
)

// GinkgoConfigureFakeFS configure fake filesystem by setting vars.FilesystemRoot
// and register Ginkgo DeferCleanup handler to clean the fs when test completed
func GinkgoConfigureFakeFS(f *fakefilesystem.FS) {
	var (
		cleanFakeFs func()
		err         error
	)
	vars.FilesystemRoot, cleanFakeFs, err = f.Use()
	Expect(err).ToNot(HaveOccurred())
	DeferCleanup(cleanFakeFs)
}

// GinkgoAssertFileContentsEquals check that content of the file
// match the expected value.
// prepends vars.FilesystemRoot to the file path to be compatible with
// GinkgoConfigureFakeFS function
func GinkgoAssertFileContentsEquals(path, expectedContent string) {
	d, err := os.ReadFile(filepath.Join(vars.FilesystemRoot, path))
	ExpectWithOffset(1, err).NotTo(HaveOccurred())
	ExpectWithOffset(1, string(d)).To(Equal(expectedContent))
}
