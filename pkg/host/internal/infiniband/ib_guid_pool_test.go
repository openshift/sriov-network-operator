package infiniband

import (
	"github.com/golang/mock/gomock"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	netlinkLibPkg "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/host/internal/lib/netlink"
	netlinkMockPkg "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/host/internal/lib/netlink/mock"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/host/internal/network"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/test/util/fakefilesystem"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/test/util/helpers"
)

var _ = Describe("ibGUIDPool", Ordered, func() {
	var (
		netlinkLibMock *netlinkMockPkg.MockNetlinkLib
		testCtrl       *gomock.Controller

		createJsonConfig func(string) string

		guidPool ibGUIDPool
	)

	BeforeAll(func() {
		var err error

		createJsonConfig = func(content string) string {
			configPath := "/config.json"
			helpers.GinkgoConfigureFakeFS(&fakefilesystem.FS{
				Dirs:  []string{"/host"},
				Files: map[string][]byte{"/host" + configPath: []byte(content)},
			})

			return configPath
		}

		testCtrl = gomock.NewController(GinkgoT())
		netlinkLibMock = netlinkMockPkg.NewMockNetlinkLib(testCtrl)
		netlinkLibMock.EXPECT().LinkList().Return([]netlinkLibPkg.Link{}, nil).Times(1)

		configPath := createJsonConfig(
			`[{"pciAddress":"0000:3b:00.0","guids":["00:00:00:00:00:00:00:00", "00:00:00:00:00:00:00:01"]},
			{"pciAddress":"0000:3b:00.1","guidsRange":{"start":"00:00:00:00:00:00:01:00","end":"00:00:00:00:00:00:01:02"}}]`)

		guidPool, err = newIbGUIDPool(configPath, netlinkLibMock, network.New(nil, nil, nil, nil))
		Expect(err).NotTo(HaveOccurred())
	})

	DescribeTable("check GetVFGUID function",
		func(pfAddr string, vfID int, expectedError string, expectedGUID string) {
			guid, err := guidPool.GetVFGUID(pfAddr, vfID)
			if expectedError != "" {
				Expect(err).To(MatchError(expectedError))
			} else {
				Expect(err).NotTo(HaveOccurred())
			}
			if expectedGUID != "" {
				Expect(guid.String()).To(Equal(expectedGUID))
			}
		},

		Entry("Should get the first GUID out of the array", "0000:3b:00.0", 0, "", "00:00:00:00:00:00:00:00"),
		Entry("Should get the last GUID out of the array", "0000:3b:00.0", 1, "", "00:00:00:00:00:00:00:01"),
		Entry("Should get the same result when called again for an array GUID", "0000:3b:00.0", 0, "", "00:00:00:00:00:00:00:00"),
		Entry("Should return a pool exhausted error when given a VF ID immediately out of bounds of a GUID array", "0000:3b:00.0", 2, "no guid allocation found for VF id: 2 on pf 0000:3b:00.0", ""),
		Entry("Should return a pool exhausted error when given a VF ID, grossly out of bounds of a GUID array", "0000:3b:00.0", 5, "no guid allocation found for VF id: 5 on pf 0000:3b:00.0", ""),
		Entry("Should get a correct GUID from the GUID range #1", "0000:3b:00.1", 0, "", "00:00:00:00:00:00:01:00"),
		Entry("Should get a correct GUID from the GUID range #2", "0000:3b:00.1", 1, "", "00:00:00:00:00:00:01:01"),
		Entry("Should get a correct GUID from the GUID range #3", "0000:3b:00.1", 2, "", "00:00:00:00:00:00:01:02"),
		Entry("Should return a pool exhausted error when given a VF ID immediately out of bounds of a GUID range", "0000:3b:00.1", 3, "no guid allocation found for VF id: 3 on pf 0000:3b:00.1", ""),
		Entry("Should return a pool exhausted error when given a VF ID, grossly out of bounds of a GUID range", "0000:3b:00.1", 5, "no guid allocation found for VF id: 5 on pf 0000:3b:00.1", ""),
		Entry("Should get the same result when called again for an array GUID", "0000:3b:00.1", 1, "", "00:00:00:00:00:00:01:01"),
	)

	AfterAll(func() {
		testCtrl.Finish()
	})
})
