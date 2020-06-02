package images

import (
	"fmt"
	"os"
)

var (
	registry      string
	cnfTestsImage string
)

func init() {
	registry = os.Getenv("IMAGE_REGISTRY")
	if registry == "" {
		registry = "quay.io/openshift-kni/"
	}

	cnfTestsImage = os.Getenv("CNF_TESTS_IMAGE")
	if cnfTestsImage == "" {
		cnfTestsImage = "cnf-tests:4.5"
	}
}

// Test returns the test image to be used
func Test() string {
	return fmt.Sprintf("%s%s", registry, cnfTestsImage)
}
