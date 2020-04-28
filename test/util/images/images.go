package images

import (
	"fmt"
	"os"

	gomega "github.com/onsi/gomega"
)

var registry string
var images map[string]imageLocation

const (
	// TestUtils is the image name to be used to retrieve the test utils image
	TestUtils = "testutils"
)

func init() {
	registry = os.Getenv("IMAGE_REGISTRY")

	images = map[string]imageLocation{
		TestUtils: {
			name:    "cnftest-utils",
			registy: "quay.io/openshift-kni/",
			version: "4.5",
		},
	}
}

type imageLocation struct {
	name    string
	registy string
	version string
}

// For returns the image to be used for the given key
func For(name string) string {
	img, ok := images[name]
	gomega.Expect(ok).To(gomega.BeTrue(), "Image not found")

	if registry != "" {
		return fmt.Sprintf("%s%s:%s", registry, img.name, img.version)
	}
	return fmt.Sprintf("%s%s:%s", img.registy, img.name, img.version)
}
