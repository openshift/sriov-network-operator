package infiniband

import (
	"net"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"

	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/consts"
	netlinkLibPkg "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/host/internal/lib/netlink"
	netlinkMockPkg "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/host/internal/lib/netlink/mock"
	hostMockPkg "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/host/mock"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/test/util/fakefilesystem"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/test/util/helpers"
)

var _ = Describe("infiniband interface implementation", func() {
	var (
		testCtrl       *gomock.Controller
		netlinkLibMock *netlinkMockPkg.MockNetlinkLib
		hostMock       *hostMockPkg.MockHostManagerInterface
	)
	BeforeEach(func() {
		testCtrl = gomock.NewController(GinkgoT())
		netlinkLibMock = netlinkMockPkg.NewMockNetlinkLib(testCtrl)
		hostMock = hostMockPkg.NewMockHostManagerInterface(testCtrl)
	})
	AfterEach(func() {
		testCtrl.Finish()
	})
	It("should create infiniband helper if guid config path is empty", func() {
		netlinkLibMock.EXPECT().LinkList().Return([]netlinkLibPkg.Link{}, nil)
		_, err := New(netlinkLibMock, hostMock, hostMock)
		Expect(err).NotTo(HaveOccurred())
	})
	It("should assign guids if guid pool is nil", func() {
		netlinkLibMock.EXPECT().LinkList().Return([]netlinkLibPkg.Link{}, nil)
		var generatedGUID string
		pfLinkMock := netlinkMockPkg.NewMockLink(testCtrl)
		netlinkLibMock.EXPECT().LinkSetVfNodeGUID(pfLinkMock, 0, gomock.Any()).DoAndReturn(
			func(link netlinkLibPkg.Link, vf int, nodeguid net.HardwareAddr) error {
				// save generated GUID to validate that it is valid
				generatedGUID = nodeguid.String()
				return nil
			})
		netlinkLibMock.EXPECT().LinkSetVfPortGUID(pfLinkMock, 0, gomock.Any()).Return(nil)
		ib, err := New(netlinkLibMock, hostMock, hostMock)
		Expect(err).NotTo(HaveOccurred())
		err = ib.ConfigureVfGUID("0000:d8:00.2", "0000:d8:00.0", 0, pfLinkMock)
		Expect(err).NotTo(HaveOccurred())
		// validate that generated GUID is valid
		_, err = ParseGUID(generatedGUID)
		Expect(err).NotTo(HaveOccurred())
	})
	It("should assign guids if guid pool is not nil", func() {
		var assignedGUID string
		pfLinkMock := netlinkMockPkg.NewMockLink(testCtrl)
		netlinkLibMock.EXPECT().LinkSetVfNodeGUID(pfLinkMock, 0, gomock.Any()).DoAndReturn(
			func(link netlinkLibPkg.Link, vf int, nodeguid net.HardwareAddr) error {
				// save generated GUID to validate that it is valid
				assignedGUID = nodeguid.String()
				return nil
			})
		netlinkLibMock.EXPECT().LinkSetVfPortGUID(pfLinkMock, 0, gomock.Any()).Return(nil)

		guid, _ := ParseGUID("00:00:00:00:00:00:00:01")
		pool := &ibGUIDPoolImpl{guidConfigs: map[string]ibPfGUIDConfig{"0000:d8:00.0": {GUIDs: []GUID{guid}}}}

		ib := &infiniband{guidPool: pool, netlinkLib: netlinkLibMock, kernelHelper: hostMock}

		err := ib.ConfigureVfGUID("0000:d8:00.2", "0000:d8:00.0", 0, pfLinkMock)
		Expect(err).NotTo(HaveOccurred())
		// validate that generated GUID is valid
		resultGUID, err := ParseGUID(assignedGUID)
		Expect(err).NotTo(HaveOccurred())
		Expect(resultGUID).To(Equal(guid))
	})
	It("should read guids from the file", func() {
		var assignedGUID string
		netlinkLibMock.EXPECT().LinkList().Return([]netlinkLibPkg.Link{}, nil)
		pfLinkMock := netlinkMockPkg.NewMockLink(testCtrl)
		netlinkLibMock.EXPECT().LinkSetVfNodeGUID(pfLinkMock, 0, gomock.Any()).DoAndReturn(
			func(link netlinkLibPkg.Link, vf int, nodeguid net.HardwareAddr) error {
				// save generated GUID to validate that it is valid
				assignedGUID = nodeguid.String()
				return nil
			})
		netlinkLibMock.EXPECT().LinkSetVfPortGUID(pfLinkMock, 0, gomock.Any()).Return(nil)

		mockJsonConfig := `[{"pciAddress":"0000:d8:00.0","guids":["00:01:02:03:04:05:06:07"]}]`
		helpers.GinkgoConfigureFakeFS(&fakefilesystem.FS{
			Dirs:  []string{"/host" + consts.SriovConfBasePath + "/infiniband"},
			Files: map[string][]byte{"/host" + consts.InfinibandGUIDConfigFilePath: []byte(mockJsonConfig)},
		})

		ib, err := New(netlinkLibMock, hostMock, hostMock)
		Expect(err).NotTo(HaveOccurred())
		err = ib.ConfigureVfGUID("0000:d8:00.2", "0000:d8:00.0", 0, pfLinkMock)
		Expect(err).NotTo(HaveOccurred())
		// validate that generated GUID is valid
		resultGUID, err := ParseGUID(assignedGUID)
		Expect(err).NotTo(HaveOccurred())
		Expect(resultGUID.String()).To(Equal("00:01:02:03:04:05:06:07"))
	})
})
