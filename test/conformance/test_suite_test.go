package conformance

import (
	"flag"
	"log"
	"path"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	kniK8sReporter "github.com/openshift-kni/k8sreporter"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	// Test files in this package must not end with `_test.go` suffix, as they are imported as go package
	_ "github.com/k8snetworkplumbingwg/sriov-network-operator/test/conformance/tests"

	"github.com/k8snetworkplumbingwg/sriov-network-operator/test/util/k8sreporter"
)

var (
	customReporter *kniK8sReporter.KubernetesReporter
	err            error
	reportPath     *string
)

func init() {
	reportPath = flag.String("report", "", "the path of the report directory containing details for failed tests")
}

func TestTest(t *testing.T) {
	// We want to collect logs before any resource is deleted in AfterEach, so we register the global fail handler
	// in a way such that the reporter's Dump is always called before the default Fail.
	RegisterFailHandler(
		func(message string, callerSkip ...int) {
			if customReporter != nil {
				customReporter.Dump(10*time.Minute, CurrentSpecReport().FullText())
			}

			// Ensure failing line location is not affected by this wrapper
			for i := range callerSkip {
				callerSkip[i]++
			}
			Fail(message, callerSkip...)
		})

	if *reportPath != "" {
		reportFile := path.Join(*reportPath, "sriov_failure_report.log")
		customReporter, err = k8sreporter.New(reportFile)
		if err != nil {
			log.Fatalf("Failed to create the k8s reporter %s", err)
		}
	}

	logf.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true)))
	RunSpecs(t, "SRIOV Operator conformance tests")
}

