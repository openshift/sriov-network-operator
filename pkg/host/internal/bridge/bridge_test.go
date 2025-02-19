package bridge

import (
	"fmt"

	"go.uber.org/mock/gomock"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	sriovnetworkv1 "github.com/k8snetworkplumbingwg/sriov-network-operator/api/v1"
	ovsMockPkg "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/host/internal/bridge/ovs/mock"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/host/types"
)

var _ = Describe("Bridge", func() {
	var (
		testCtrl *gomock.Controller
		br       types.BridgeInterface
		ovsMock  *ovsMockPkg.MockInterface
		testErr  = fmt.Errorf("test")
	)
	BeforeEach(func() {
		testCtrl = gomock.NewController(GinkgoT())
		ovsMock = ovsMockPkg.NewMockInterface(testCtrl)
		br = &bridge{ovs: ovsMock}
	})
	AfterEach(func() {
		testCtrl.Finish()
	})
	Context("DiscoverBridges", func() {
		It("succeed", func() {
			ovsMock.EXPECT().GetOVSBridges(gomock.Any()).Return([]sriovnetworkv1.OVSConfigExt{{Name: "test"}, {Name: "test2"}}, nil)
			ret, err := br.DiscoverBridges()
			Expect(err).NotTo(HaveOccurred())
			Expect(ret.OVS).To(HaveLen(2))
		})
		It("error", func() {
			ovsMock.EXPECT().GetOVSBridges(gomock.Any()).Return(nil, testErr)
			_, err := br.DiscoverBridges()
			Expect(err).To(MatchError(testErr))
		})
	})

	Context("ConfigureBridges", func() {
		It("succeed", func() {
			brCreate1 := sriovnetworkv1.OVSConfigExt{Name: "br-to-create-1"}
			brCreate2 := sriovnetworkv1.OVSConfigExt{Name: "br-to-create-2"}
			brDelete1 := sriovnetworkv1.OVSConfigExt{Name: "br-to-delete-1"}
			brDelete2 := sriovnetworkv1.OVSConfigExt{Name: "br-to-delete-2"}

			ovsMock.EXPECT().RemoveOVSBridge(gomock.Any(), brDelete1.Name).Return(nil)
			ovsMock.EXPECT().RemoveOVSBridge(gomock.Any(), brDelete2.Name).Return(nil)
			ovsMock.EXPECT().CreateOVSBridge(gomock.Any(), &brCreate1).Return(nil)
			ovsMock.EXPECT().CreateOVSBridge(gomock.Any(), &brCreate2).Return(nil)
			err := br.ConfigureBridges(
				sriovnetworkv1.Bridges{OVS: []sriovnetworkv1.OVSConfigExt{brCreate1, brCreate2}},
				sriovnetworkv1.Bridges{OVS: []sriovnetworkv1.OVSConfigExt{brCreate1, brDelete1, brDelete2}})
			Expect(err).NotTo(HaveOccurred())
		})
		It("empty spec and status", func() {
			err := br.ConfigureBridges(
				sriovnetworkv1.Bridges{OVS: []sriovnetworkv1.OVSConfigExt{}},
				sriovnetworkv1.Bridges{OVS: []sriovnetworkv1.OVSConfigExt{}})
			Expect(err).NotTo(HaveOccurred())
		})
		It("failed on creation", func() {
			brCreate1 := sriovnetworkv1.OVSConfigExt{Name: "br-to-create-1"}
			ovsMock.EXPECT().CreateOVSBridge(gomock.Any(), &brCreate1).Return(testErr)
			err := br.ConfigureBridges(
				sriovnetworkv1.Bridges{OVS: []sriovnetworkv1.OVSConfigExt{brCreate1}},
				sriovnetworkv1.Bridges{OVS: []sriovnetworkv1.OVSConfigExt{}})
			Expect(err).To(MatchError(testErr))
		})
		It("failed on removal", func() {
			brDelete1 := sriovnetworkv1.OVSConfigExt{Name: "br-to-delete-1"}
			ovsMock.EXPECT().RemoveOVSBridge(gomock.Any(), brDelete1.Name).Return(testErr)
			err := br.ConfigureBridges(
				sriovnetworkv1.Bridges{OVS: []sriovnetworkv1.OVSConfigExt{}},
				sriovnetworkv1.Bridges{OVS: []sriovnetworkv1.OVSConfigExt{brDelete1}})
			Expect(err).To(MatchError(testErr))
		})
	})

	Context("DetachInterfaceFromManagedBridge", func() {
		It("succeed", func() {
			ovsMock.EXPECT().RemoveInterfaceFromOVSBridge(gomock.Any(), "0000:d8:00.0").Return(nil)
			err := br.DetachInterfaceFromManagedBridge("0000:d8:00.0")
			Expect(err).NotTo(HaveOccurred())
		})
		It("error", func() {
			ovsMock.EXPECT().RemoveInterfaceFromOVSBridge(gomock.Any(), "0000:d8:00.0").Return(testErr)
			err := br.DetachInterfaceFromManagedBridge("0000:d8:00.0")
			Expect(err).To(MatchError(testErr))
		})
	})
})
