package service

import (
	"path"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"go.uber.org/mock/gomock"

	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/host/types"
	mock_utils "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/utils/mock"
)

var _ = Describe("Systemd", func() {
	var (
		utilsMock = &mock_utils.MockCmdInterface{}
		s         types.ServiceInterface
		srv       = types.Service{
			Name:    "test-service",
			Path:    "",
			Content: "",
		}
		t        FullGinkgoTInterface
		mockCtrl *gomock.Controller
	)

	BeforeEach(func() {
		t = GinkgoT()
		mockCtrl = gomock.NewController(t)
		utilsMock = mock_utils.NewMockCmdInterface(mockCtrl)
		s = New(utilsMock)
	})

	Context("Service manage", func() {
		It("should enable service", func() {
			// Set srv.Path so that path.Join(consts.Chroot, srv.Path) resolves into
			// a writable temp dir rather than the non-existent /host directory.
			tmpDir := GinkgoT().TempDir()
			srv.Path = ".." + path.Join(tmpDir, srv.Name+".service")
			srv.Content = "[Unit]\nDescription=Test Service\n"

			utilsMock.EXPECT().Chroot(gomock.Any()).Return(func() error { return nil }, nil)
			utilsMock.EXPECT().RunCommand("systemctl", "reenable", srv.Name).Return("", "", nil)
			err := s.EnableService(&srv)
			Expect(err).ToNot(HaveOccurred())
		})
	})
})
