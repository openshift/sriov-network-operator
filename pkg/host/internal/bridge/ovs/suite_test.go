package ovs

import (
	"testing"

	"github.com/go-logr/stdr"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"go.uber.org/zap/zapcore"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
)

func TestOVS(t *testing.T) {
	log.SetLogger(zap.New(
		zap.WriteTo(GinkgoWriter),
		zap.Level(zapcore.Level(-2)),
		zap.UseDevMode(true)))
	// to reduce verbosity of ovsdb server's logs
	stdr.SetVerbosity(0)
	RegisterFailHandler(Fail)
	RunSpecs(t, "Package OVS Suite")
}
