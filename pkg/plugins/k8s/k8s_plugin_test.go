package k8s

import (
	"os"
	"testing"

	"github.com/golang/mock/gomock"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/zap/zapcore"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	sriovnetworkv1 "github.com/k8snetworkplumbingwg/sriov-network-operator/api/v1"
	mock_helper "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/helper/mock"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/host"
	hostTypes "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/host/types"
	plugin "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/plugins"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/vars"
)

func TestK8sPlugin(t *testing.T) {
	log.SetLogger(zap.New(
		zap.WriteTo(GinkgoWriter),
		zap.Level(zapcore.Level(-2)),
		zap.UseDevMode(true)))
	RegisterFailHandler(Fail)
	RunSpecs(t, "Test K8s Plugin")
}

// changes current working dir before calling the real function
func registerCall(m *gomock.Call, realF interface{}) *gomock.Call {
	cur, _ := os.Getwd()
	return m.Do(func(_ ...interface{}) {
		os.Chdir("../../..")
	}).DoAndReturn(realF).Do(func(_ ...interface{}) {
		os.Chdir(cur)
	}).AnyTimes()
}

func setIsSystemdMode(val bool) {
	origUsingSystemdMode := vars.UsingSystemdMode
	DeferCleanup(func() {
		vars.UsingSystemdMode = origUsingSystemdMode
	})
	vars.UsingSystemdMode = val
}

func newServiceNameMatcher(name string) gomock.Matcher {
	return &serviceNameMatcher{name: name}
}

type serviceNameMatcher struct {
	name string
}

func (snm *serviceNameMatcher) Matches(x interface{}) bool {
	s, ok := x.(*hostTypes.Service)
	if !ok {
		return false
	}
	return snm.name == s.Name
}

func (snm *serviceNameMatcher) String() string {
	return "service name match: " + snm.name
}

var _ = Describe("K8s plugin", func() {
	var (
		k8sPlugin  plugin.VendorPlugin
		err        error
		testCtrl   *gomock.Controller
		hostHelper *mock_helper.MockHostHelpersInterface
	)

	BeforeEach(func() {
		testCtrl = gomock.NewController(GinkgoT())

		hostHelper = mock_helper.NewMockHostHelpersInterface(testCtrl)
		realHostMgr := host.NewHostManager(hostHelper)

		// proxy some functions to real host manager to simplify testing and to additionally validate manifests
		for _, f := range []string{
			"bindata/manifests/switchdev-config/files/switchdev-configuration-before-nm.sh.yaml",
			"bindata/manifests/switchdev-config/files/switchdev-configuration-after-nm.sh.yaml",
			"bindata/manifests/switchdev-config/files/switchdev-vf-link-name.sh.yaml",
		} {
			registerCall(hostHelper.EXPECT().ReadScriptManifestFile(f), realHostMgr.ReadScriptManifestFile)
		}
		for _, f := range []string{
			"bindata/manifests/switchdev-config/switchdev-units/switchdev-configuration-before-nm.yaml",
			"bindata/manifests/switchdev-config/switchdev-units/switchdev-configuration-after-nm.yaml",
			"bindata/manifests/sriov-config-service/kubernetes/sriov-config-service.yaml",
			"bindata/manifests/sriov-config-service/kubernetes/sriov-config-post-network-service.yaml",
		} {
			registerCall(hostHelper.EXPECT().ReadServiceManifestFile(f), realHostMgr.ReadServiceManifestFile)
		}
		for _, s := range []string{
			"switchdev-configuration-before-nm.service",
			"switchdev-configuration-after-nm.service",
		} {
			registerCall(hostHelper.EXPECT().RemoveFromService(newServiceNameMatcher(s), gomock.Any()), realHostMgr.RemoveFromService)
		}
		for _, s := range []string{
			"bindata/manifests/switchdev-config/switchdev-units/NetworkManager.service.yaml",
			"bindata/manifests/switchdev-config/ovs-units/ovs-vswitchd.service.yaml",
		} {
			registerCall(hostHelper.EXPECT().ReadServiceInjectionManifestFile(s), realHostMgr.ReadServiceInjectionManifestFile)
		}
		k8sPlugin, err = NewK8sPlugin(hostHelper)
		Expect(err).ToNot(HaveOccurred())
	})

	AfterEach(func() {
		testCtrl.Finish()
	})

	It("no switchdev, no systemd", func() {
		setIsSystemdMode(false)
		needDrain, needReboot, err := k8sPlugin.OnNodeStateChange(&sriovnetworkv1.SriovNetworkNodeState{})
		Expect(err).ToNot(HaveOccurred())
		Expect(needReboot).To(BeFalse())
		Expect(needDrain).To(BeFalse())
		Expect(k8sPlugin.Apply()).NotTo(HaveOccurred())
	})

	It("systemd, created", func() {
		setIsSystemdMode(true)

		hostHelper.EXPECT().IsServiceEnabled("/etc/systemd/system/sriov-config.service").Return(false, nil)
		hostHelper.EXPECT().IsServiceEnabled("/etc/systemd/system/sriov-config-post-network.service").Return(false, nil)
		hostHelper.EXPECT().EnableService(newServiceNameMatcher("sriov-config.service")).Return(nil)
		hostHelper.EXPECT().EnableService(newServiceNameMatcher("sriov-config-post-network.service")).Return(nil)
		hostHelper.EXPECT().UpdateSystemService(newServiceNameMatcher("sriov-config.service")).Return(nil)
		hostHelper.EXPECT().UpdateSystemService(newServiceNameMatcher("sriov-config-post-network.service")).Return(nil)

		needDrain, needReboot, err := k8sPlugin.OnNodeStateChange(&sriovnetworkv1.SriovNetworkNodeState{})
		Expect(err).ToNot(HaveOccurred())
		Expect(needReboot).To(BeTrue())
		Expect(needDrain).To(BeTrue())
		Expect(k8sPlugin.Apply()).NotTo(HaveOccurred())
	})
	It("systemd, already configured", func() {
		setIsSystemdMode(true)

		hostHelper.EXPECT().IsServiceEnabled("/etc/systemd/system/sriov-config.service").Return(true, nil)
		hostHelper.EXPECT().ReadService("/etc/systemd/system/sriov-config.service").Return(
			&hostTypes.Service{Name: "sriov-config.service"}, nil)
		hostHelper.EXPECT().CompareServices(
			&hostTypes.Service{Name: "sriov-config.service"},
			newServiceNameMatcher("sriov-config.service"),
		).Return(false, nil)

		hostHelper.EXPECT().IsServiceEnabled("/etc/systemd/system/sriov-config-post-network.service").Return(true, nil)
		hostHelper.EXPECT().ReadService("/etc/systemd/system/sriov-config-post-network.service").Return(
			&hostTypes.Service{Name: "sriov-config-post-network.service"}, nil)
		hostHelper.EXPECT().CompareServices(&hostTypes.Service{Name: "sriov-config-post-network.service"},
			newServiceNameMatcher("sriov-config-post-network.service"),
		).Return(false, nil)

		needDrain, needReboot, err := k8sPlugin.OnNodeStateChange(&sriovnetworkv1.SriovNetworkNodeState{})
		Expect(err).ToNot(HaveOccurred())
		Expect(needReboot).To(BeFalse())
		Expect(needDrain).To(BeFalse())
		Expect(k8sPlugin.Apply()).NotTo(HaveOccurred())
	})
	It("systemd, update required", func() {
		setIsSystemdMode(true)

		hostHelper.EXPECT().IsServiceEnabled("/etc/systemd/system/sriov-config.service").Return(true, nil)
		hostHelper.EXPECT().ReadService("/etc/systemd/system/sriov-config.service").Return(
			&hostTypes.Service{Name: "sriov-config.service"}, nil)
		hostHelper.EXPECT().CompareServices(
			&hostTypes.Service{Name: "sriov-config.service"},
			newServiceNameMatcher("sriov-config.service"),
		).Return(true, nil)
		hostHelper.EXPECT().EnableService(newServiceNameMatcher("sriov-config.service")).Return(nil)
		hostHelper.EXPECT().UpdateSystemService(newServiceNameMatcher("sriov-config.service")).Return(nil)

		hostHelper.EXPECT().IsServiceEnabled("/etc/systemd/system/sriov-config-post-network.service").Return(true, nil)
		hostHelper.EXPECT().ReadService("/etc/systemd/system/sriov-config-post-network.service").Return(
			&hostTypes.Service{Name: "sriov-config-post-network.service"}, nil)
		hostHelper.EXPECT().CompareServices(&hostTypes.Service{Name: "sriov-config-post-network.service"},
			newServiceNameMatcher("sriov-config-post-network.service"),
		).Return(false, nil)

		needDrain, needReboot, err := k8sPlugin.OnNodeStateChange(&sriovnetworkv1.SriovNetworkNodeState{})
		Expect(err).ToNot(HaveOccurred())
		Expect(needReboot).To(BeTrue())
		Expect(needDrain).To(BeTrue())
		Expect(k8sPlugin.Apply()).NotTo(HaveOccurred())
	})
})
