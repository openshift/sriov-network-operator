package udev

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"go.uber.org/zap/zapcore"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/vars"
)

func TestUdev(t *testing.T) {
	log.SetLogger(zap.New(
		zap.WriteTo(GinkgoWriter),
		zap.Level(zapcore.Level(-2)),
		zap.UseDevMode(true)))
	RegisterFailHandler(Fail)
	RunSpecs(t, "Package Udev Suite")
}

var _ = BeforeSuite(func() {
	vars.SupportedVfIds = []string{"0x1017", "0x1018"}
	DeferCleanup(func() {
		vars.SupportedVfIds = []string{}
	})
})
