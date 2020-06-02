package conformance

import (
	"flag"
	"fmt"
	"os"
	"testing"

	. "github.com/onsi/ginkgo"
	"github.com/onsi/ginkgo/reporters"
	. "github.com/onsi/gomega"

	sriovv1 "github.com/openshift/sriov-network-operator/pkg/apis/sriovnetwork/v1"
	testclient "github.com/openshift/sriov-network-operator/test/util/client"
	"k8s.io/apimachinery/pkg/runtime"

	_ "github.com/openshift/sriov-network-operator/test/conformance/tests"
	"github.com/openshift/sriov-network-operator/test/util/k8sreporter"
)

var (
	junitPath  *string
	dumpOutput *bool
)

func init() {
	junitPath = flag.String("junit", "junit.xml", "the path for the junit format report")
	dumpOutput = flag.Bool("dump", false, "dump informations for failed tests")
}

func TestTest(t *testing.T) {
	RegisterFailHandler(Fail)

	rr := []Reporter{}
	if junitPath != nil {
		rr = append(rr, reporters.NewJUnitReporter(*junitPath))
	}

	reporterFile := os.Getenv("REPORTER_OUTPUT")

	clients := testclient.New("", func(scheme *runtime.Scheme) {
		sriovv1.AddToScheme(scheme)
	})

	if reporterFile != "" {
		f, err := os.OpenFile(reporterFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err != nil {
			fmt.Fprintf(os.Stderr, "failed to open the file: %v\n", err)
			return
		}
		defer f.Close()
		rr = append(rr, k8sreporter.New(clients, f))

	} else if *dumpOutput {
		rr = append(rr, k8sreporter.New(clients, os.Stdout))
	}

	RunSpecsWithDefaultAndCustomReporters(t, "SRIOV Operator conformance tests", rr)
}
