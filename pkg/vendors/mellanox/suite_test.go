package mlxutils

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"go.uber.org/zap/zapcore"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	snolog "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/log"
)

func TestSriov(t *testing.T) {
	log.SetLogger(zap.New(
		zap.WriteTo(GinkgoWriter),
		zap.Level(zapcore.Level(-2)),
		zap.UseDevMode(true)))
	snolog.InitLog()
	RegisterFailHandler(Fail)
	RunSpecs(t, "Package Mellanox Vendor Suite")
}
