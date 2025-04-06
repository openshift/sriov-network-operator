package mlxutils

import (
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"go.uber.org/mock/gomock"

	sriovnetworkv1 "github.com/k8snetworkplumbingwg/sriov-network-operator/api/v1"
	mock_utils "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/utils/mock"
)

var _ = Describe("SRIOV", func() {
	var (
		m        MellanoxInterface
		u        *mock_utils.MockCmdInterface
		testCtrl *gomock.Controller

		testError = fmt.Errorf("test")
	)
	BeforeEach(func() {
		testCtrl = gomock.NewController(GinkgoT())
		u = mock_utils.NewMockCmdInterface(testCtrl)
		m = New(u)
	})

	AfterEach(func() {
		testCtrl.Finish()
	})

	Context("MstConfigReadData", func() {
		It("it should error if not able to run the command", func() {
			u.EXPECT().RunCommand("mstconfig", "-e", "-d", "0000:d8:00.3", "q").Return("", "-E- Failed to open the device", testError)
			stdOut, stdErr, err := m.MstConfigReadData("0000:d8:00.3")
			Expect(err).To(HaveOccurred())
			Expect(stdErr).To(Equal("-E- Failed to open the device"))
			Expect(stdOut).To(BeEmpty())
		})

		It("should return mstconfig output", func() {
			u.EXPECT().RunCommand("mstconfig", "-e", "-d", "0000:d8:00.0", "q").Return(
				getMstconfigOutput(0, 0, "True", "True", "True", true, false, false),
				"", nil)
			stdOut, stdErr, err := m.MstConfigReadData("0000:d8:00.0")
			Expect(err).ToNot(HaveOccurred())
			Expect(stdErr).To(BeEmpty())
			Expect(stdOut).ToNot(BeEmpty())
		})
	})

	Context("MlxResetFW", func() {
		It("should return not error if is able to run mstfwreset", func() {
			u.EXPECT().RunCommand("mstfwreset", "-d", "0000:d8:00.0", "--skip_driver", "-l", "3", "-y", "reset").Return(
				"", "", nil)
			err := m.MlxResetFW([]string{"0000:d8:00.0"})
			Expect(err).ToNot(HaveOccurred())
		})
		It("should return error if one of the interfaces is not able to reset", func() {
			u.EXPECT().RunCommand("mstfwreset", "-d", "0000:d8:00.0", "--skip_driver", "-l", "3", "-y", "reset").Return(
				"", "", nil)
			u.EXPECT().RunCommand("mstfwreset", "-d", "0000:d8:00.1", "--skip_driver", "-l", "3", "-y", "reset").Return(
				"", "-E- Failed to open the device", testError)
			u.EXPECT().RunCommand("mstfwreset", "-d", "0000:d8:00.2", "--skip_driver", "-l", "3", "-y", "reset").Return(
				"", "-E- Failed to open the device", testError)
			err := m.MlxResetFW([]string{"0000:d8:00.0", "0000:d8:00.1", "0000:d8:00.2"})
			Expect(err).To(HaveOccurred())
		})
	})

	Context("GetMellanoxBlueFieldMode", func() {
		It("should return error if not able to run mstconfig", func() {
			u.EXPECT().RunCommand("mstconfig", "-e", "-d", "0000:d8:00.0", "q").Return("", "-E- Failed to open the device", testError)
			mode, err := m.GetMellanoxBlueFieldMode("0000:d8:00.0")
			Expect(err).To(HaveOccurred())
			Expect(int(mode)).To(Equal(-1))
		})

		It("should return -1 if the card is not a BF", func() {
			u.EXPECT().RunCommand("mstconfig", "-e", "-d", "0000:d8:00.0", "q").Return(
				getMstconfigOutput(0, 0, "True", "True", "True", true, false, false),
				"", nil)
			mode, err := m.GetMellanoxBlueFieldMode("0000:d8:00.0")
			Expect(err).To(HaveOccurred())
			Expect(int(mode)).To(Equal(-1))
		})

		It("should return that the card is on dpu mode", func() {
			u.EXPECT().RunCommand("mstconfig", "-e", "-d", "0000:d8:00.0", "q").Return(
				getBFMstconfigOutput(true, false),
				"", nil)
			mode, err := m.GetMellanoxBlueFieldMode("0000:d8:00.0")
			Expect(err).ToNot(HaveOccurred())
			Expect(int(mode)).To(Equal(0))
		})

		It("should return that the card is on connectX mode", func() {
			u.EXPECT().RunCommand("mstconfig", "-e", "-d", "0000:d8:00.0", "q").Return(
				getBFMstconfigOutput(false, false),
				"", nil)
			mode, err := m.GetMellanoxBlueFieldMode("0000:d8:00.0")
			Expect(err).ToNot(HaveOccurred())
			Expect(int(mode)).To(Equal(1))
		})

		It("should return unknow if the combination out the output is not expected", func() {
			u.EXPECT().RunCommand("mstconfig", "-e", "-d", "0000:d8:00.0", "q").Return(
				getBFMstconfigOutput(false, true),
				"", nil)
			mode, err := m.GetMellanoxBlueFieldMode("0000:d8:00.0")
			Expect(err).To(HaveOccurred())
			Expect(int(mode)).To(Equal(-1))
		})
	})

	Context("MlxConfigFW", func() {
		It("should return error if the card is on DPU mode", func() {
			u.EXPECT().RunCommand("mstconfig", "-e", "-d", "0000:d8:00.0", "q").Return(
				getBFMstconfigOutput(true, false),
				"", nil)

			err := m.MlxConfigFW(map[string]MlxNic{"0000:d8:00.0": {}})
			Expect(err).To(HaveOccurred())
		})

		It("should not run mstconfig if no configuration is needed", func() {
			u.EXPECT().RunCommand("mstconfig", "-e", "-d", "0000:d8:00.0", "q").Return(
				getBFMstconfigOutput(false, false),
				"", nil)
			err := m.MlxConfigFW(map[string]MlxNic{"0000:d8:00.0": {EnableSriov: false, TotalVfs: -1}})
			Expect(err).ToNot(HaveOccurred())
		})

		It("should enable all the args", func() {
			u.EXPECT().RunCommand("mstconfig", "-e", "-d", "0000:d8:00.0", "q").Return(
				getBFMstconfigOutput(false, false),
				"", nil)
			u.EXPECT().RunCommand("mstconfig", "-d", "0000:d8:00.0", "-y", "set", "SRIOV_EN=True", "NUM_OF_VFS=10", "LINK_TYPE_P1=ETH", "LINK_TYPE_P2=ETH").Return(
				"",
				"", nil)
			err := m.MlxConfigFW(map[string]MlxNic{"0000:d8:00.0": {EnableSriov: true, TotalVfs: 10, LinkTypeP1: "ETH", LinkTypeP2: "ETH"}})
			Expect(err).ToNot(HaveOccurred())
		})

		It("should return error if args is not right", func() {
			u.EXPECT().RunCommand("mstconfig", "-e", "-d", "0000:d8:00.0", "q").Return(
				getBFMstconfigOutput(false, false),
				"", nil)
			u.EXPECT().RunCommand("mstconfig", "-d", "0000:d8:00.0", "-y", "set", "SRIOV_EN=True", "NUM_OF_VFS=10", "LINK_TYPE_P1=ETH", "LINK_TYPE_P2=test").Return(
				"",
				"", testError)
			err := m.MlxConfigFW(map[string]MlxNic{"0000:d8:00.0": {EnableSriov: true, TotalVfs: 10, LinkTypeP1: "ETH", LinkTypeP2: "test"}})
			Expect(err).To(HaveOccurred())
		})
	})

	Context("GetMlxNicFwData", func() {
		It("should return error if not able to run mstconfig", func() {
			u.EXPECT().RunCommand("mstconfig", "-e", "-d", "0000:d8:00.0", "q").Return(
				"", "", testError)
			current, next, err := m.GetMlxNicFwData("0000:d8:00.0")
			Expect(err).To(HaveOccurred())
			Expect(current).To(BeNil())
			Expect(next).To(BeNil())
		})

		It("should return the current and next firmware configuration", func() {
			u.EXPECT().RunCommand("mstconfig", "-e", "-d", "0000:d8:00.0", "q").Return(
				getMstconfigOutput(5, 10, "True", "True", "True", true, false, false),
				"", nil)
			current, next, err := m.GetMlxNicFwData("0000:d8:00.0")
			Expect(err).ToNot(HaveOccurred())
			Expect(current.TotalVfs).To(Equal(5))
			Expect(current.EnableSriov).To(BeTrue())
			Expect(current.LinkTypeP1).To(Equal("ETH"))
			Expect(current.LinkTypeP2).To(Equal("ETH"))
			Expect(next.TotalVfs).To(Equal(10))
			Expect(next.EnableSriov).To(BeTrue())
			Expect(next.LinkTypeP1).To(Equal("ETH"))
			Expect(next.LinkTypeP2).To(Equal("ETH"))
		})

		It("should return the current and next firmware configuration without linkType", func() {
			u.EXPECT().RunCommand("mstconfig", "-e", "-d", "0000:d8:00.0", "q").Return(
				getMstconfigOutput(5, 10, "True", "True", "True", false, false, false),
				"", nil)
			current, next, err := m.GetMlxNicFwData("0000:d8:00.0")
			Expect(err).ToNot(HaveOccurred())
			Expect(current.TotalVfs).To(Equal(5))
			Expect(current.EnableSriov).To(BeTrue())
			Expect(current.LinkTypeP1).To(Equal("Preconfigured"))
			Expect(current.LinkTypeP2).To(Equal(""))
			Expect(next.TotalVfs).To(Equal(10))
			Expect(next.EnableSriov).To(BeTrue())
			Expect(next.LinkTypeP1).To(Equal("Preconfigured"))
			Expect(next.LinkTypeP2).To(Equal(""))
		})

		It("should return the current and next firmware configuration with IB", func() {
			u.EXPECT().RunCommand("mstconfig", "-e", "-d", "0000:d8:00.0", "q").Return(
				getMstconfigOutput(5, 10, "True", "True", "True", false, true, false),
				"", nil)
			current, next, err := m.GetMlxNicFwData("0000:d8:00.0")
			Expect(err).ToNot(HaveOccurred())
			Expect(current.TotalVfs).To(Equal(5))
			Expect(current.EnableSriov).To(BeTrue())
			Expect(current.LinkTypeP1).To(Equal("IB"))
			Expect(current.LinkTypeP2).To(Equal("IB"))
			Expect(next.TotalVfs).To(Equal(10))
			Expect(next.EnableSriov).To(BeTrue())
			Expect(next.LinkTypeP1).To(Equal("ETH"))
			Expect(next.LinkTypeP2).To(Equal("ETH"))
		})

		It("should return the current and next firmware configuration with unknow", func() {
			u.EXPECT().RunCommand("mstconfig", "-e", "-d", "0000:d8:00.0", "q").Return(
				getMstconfigOutput(5, 10, "True", "True", "True", false, false, true),
				"", nil)
			current, next, err := m.GetMlxNicFwData("0000:d8:00.0")
			Expect(err).ToNot(HaveOccurred())
			Expect(current.TotalVfs).To(Equal(5))
			Expect(current.EnableSriov).To(BeTrue())
			Expect(current.LinkTypeP1).To(Equal("Unknown"))
			Expect(current.LinkTypeP2).To(Equal("IB"))
			Expect(next.TotalVfs).To(Equal(10))
			Expect(next.EnableSriov).To(BeTrue())
			Expect(next.LinkTypeP1).To(Equal("Unknown"))
			Expect(next.LinkTypeP2).To(Equal("ETH"))
		})
	})

	Context("IsDualPort", func() {
		It("should return true if it's a dual port", func() {
			mellanoxNicsStatus := map[string]map[string]sriovnetworkv1.InterfaceExt{"0000:d8:00.": {"0000:d8:00.0": {}, "0000:d8:00.1": {}}}
			status := IsDualPort("0000:d8:00.0", mellanoxNicsStatus)
			Expect(status).To(BeTrue())
		})

		It("should return false if it's not a dual port", func() {
			mellanoxNicsStatus := map[string]map[string]sriovnetworkv1.InterfaceExt{"0000:d8:00.": {"0000:d8:00.0": {}}}
			status := IsDualPort("0000:d8:00.0", mellanoxNicsStatus)
			Expect(status).To(BeFalse())
		})
	})

	Context("HandleTotalVfs", func() {
		It("should require to reboot the system for a single port card", func() {
			fwCurrent := &MlxNic{TotalVfs: 0}
			fwNext := &MlxNic{TotalVfs: 0}
			ifaceSpec := sriovnetworkv1.Interface{PciAddress: "0000:d8:00.0", NumVfs: 10}
			mellanoxNicsSpec := map[string]sriovnetworkv1.Interface{"0000:d8:00.0": ifaceSpec}
			attrs := &MlxNic{}
			totalvfs, needReboot, changeWithoutReboot := HandleTotalVfs(fwCurrent, fwNext, attrs, ifaceSpec, false, mellanoxNicsSpec)
			Expect(totalvfs).To(Equal(10))
			Expect(needReboot).To(BeTrue())
			Expect(changeWithoutReboot).To(BeFalse())
			Expect(attrs.TotalVfs).To(Equal(10))
		})

		It("should use the higher number of vf for dual port", func() {
			fwCurrent := &MlxNic{TotalVfs: 0}
			fwNext := &MlxNic{TotalVfs: 0}
			ifaceSpec := sriovnetworkv1.Interface{PciAddress: "0000:d8:00.0", NumVfs: 10}
			mellanoxNicsSpec := map[string]sriovnetworkv1.Interface{"0000:d8:00.0": ifaceSpec, "0000:d8:00.1": {NumVfs: 20}}
			attrs := &MlxNic{}
			totalvfs, needReboot, changeWithoutReboot := HandleTotalVfs(fwCurrent, fwNext, attrs, ifaceSpec, true, mellanoxNicsSpec)
			Expect(totalvfs).To(Equal(20))
			Expect(needReboot).To(BeTrue())
			Expect(changeWithoutReboot).To(BeFalse())
			Expect(attrs.TotalVfs).To(Equal(20))
		})

		It("should return for externally manage if the number is higher that the current configured", func() {
			fwCurrent := &MlxNic{TotalVfs: 0}
			fwNext := &MlxNic{TotalVfs: 0}
			ifaceSpec := sriovnetworkv1.Interface{PciAddress: "0000:d8:00.0", NumVfs: 10, ExternallyManaged: true}
			mellanoxNicsSpec := map[string]sriovnetworkv1.Interface{"0000:d8:00.0": ifaceSpec}
			attrs := &MlxNic{}
			totalvfs, needReboot, changeWithoutReboot := HandleTotalVfs(fwCurrent, fwNext, attrs, ifaceSpec, false, mellanoxNicsSpec)
			Expect(totalvfs).To(Equal(10))
			Expect(needReboot).To(BeTrue())
			Expect(changeWithoutReboot).To(BeFalse())
			Expect(attrs.TotalVfs).To(Equal(10))
		})

		It("should not need to reboot if we remove a policy and re-apply it", func() {
			fwCurrent := &MlxNic{TotalVfs: 10}
			fwNext := &MlxNic{TotalVfs: 0}
			ifaceSpec := sriovnetworkv1.Interface{PciAddress: "0000:d8:00.0", NumVfs: 10}
			mellanoxNicsSpec := map[string]sriovnetworkv1.Interface{"0000:d8:00.0": ifaceSpec}
			attrs := &MlxNic{}
			totalvfs, needReboot, changeWithoutReboot := HandleTotalVfs(fwCurrent, fwNext, attrs, ifaceSpec, false, mellanoxNicsSpec)
			Expect(totalvfs).To(Equal(10))
			Expect(needReboot).To(BeFalse())
			Expect(changeWithoutReboot).To(BeTrue())
			Expect(attrs.TotalVfs).To(Equal(10))
		})
	})

	Context("HandleEnableSriov", func() {
		It("should disable sriov and return need to reboot", func() {
			fwCurrent := &MlxNic{TotalVfs: 10, EnableSriov: true}
			fwNext := &MlxNic{TotalVfs: 0}
			attrs := &MlxNic{}
			needReboot, changeWithoutReboot := HandleEnableSriov(0, fwCurrent, fwNext, attrs)
			Expect(needReboot).To(BeTrue())
			Expect(changeWithoutReboot).To(BeFalse())
			Expect(attrs.EnableSriov).To(BeFalse())
		})
		It("should enable sriov and return need to reboot", func() {
			fwCurrent := &MlxNic{TotalVfs: 0, EnableSriov: false}
			fwNext := &MlxNic{TotalVfs: 0}
			attrs := &MlxNic{}
			needReboot, changeWithoutReboot := HandleEnableSriov(10, fwCurrent, fwNext, attrs)
			Expect(needReboot).To(BeTrue())
			Expect(changeWithoutReboot).To(BeFalse())
			Expect(attrs.EnableSriov).To(BeTrue())
		})
		It("should enable sriov and return no need to reboot if fw next is enable", func() {
			fwCurrent := &MlxNic{TotalVfs: 0, EnableSriov: true}
			fwNext := &MlxNic{TotalVfs: 0, EnableSriov: false}
			attrs := &MlxNic{}
			needReboot, changeWithoutReboot := HandleEnableSriov(10, fwCurrent, fwNext, attrs)
			Expect(needReboot).To(BeFalse())
			Expect(changeWithoutReboot).To(BeTrue())
			Expect(attrs.EnableSriov).To(BeTrue())
		})
		It("should not enable sriov and return false to reboot in case the totalvf is 0 and sriov is not enabled in current fw", func() {
			fwCurrent := &MlxNic{TotalVfs: 0, EnableSriov: false}
			fwNext := &MlxNic{TotalVfs: 0, EnableSriov: false}
			attrs := &MlxNic{}
			needReboot, changeWithoutReboot := HandleEnableSriov(0, fwCurrent, fwNext, attrs)
			Expect(needReboot).To(BeFalse())
			Expect(changeWithoutReboot).To(BeFalse())
			Expect(attrs.EnableSriov).To(BeFalse())
		})
	})

	Context("HandleLinkType", func() {
		It("should return false if there is no need to change link type", func() {
			fwData := &MlxNic{LinkTypeP1: "ETH", LinkTypeP2: "ETH"}
			ifaceSpec := sriovnetworkv1.Interface{PciAddress: "0000:d8:00.0", NumVfs: 10}
			mellanoxNicsSpec := map[string]sriovnetworkv1.Interface{"0000:d8:00.0": ifaceSpec, "0000:d8:00.1": {NumVfs: 20}}
			mellanoxNicsExt := map[string]sriovnetworkv1.InterfaceExt{"0000:d8:00.0": {PciAddress: "0000:d8:00.0"}, "0000:d8:00.1": {NumVfs: 20}}
			mellanoxNicsStatus := map[string]map[string]sriovnetworkv1.InterfaceExt{"0000:d8:00.": mellanoxNicsExt}
			attrs := &MlxNic{}

			needChange, err := HandleLinkType("0000:d8:00.", fwData, attrs, mellanoxNicsSpec, mellanoxNicsStatus)
			Expect(err).ToNot(HaveOccurred())
			Expect(needChange).To(BeFalse())
		})

		It("should return true if there is a need to change link type", func() {
			fwData := &MlxNic{LinkTypeP1: "ETH", LinkTypeP2: "ETH"}
			ifaceSpec := sriovnetworkv1.Interface{PciAddress: "0000:d8:00.0", NumVfs: 10, LinkType: "IB"}
			mellanoxNicsSpec := map[string]sriovnetworkv1.Interface{"0000:d8:00.0": ifaceSpec, "0000:d8:00.1": {NumVfs: 20}}
			mellanoxNicsExt := map[string]sriovnetworkv1.InterfaceExt{"0000:d8:00.0": {PciAddress: "0000:d8:00.0", LinkType: "ETH"}, "0000:d8:00.1": {NumVfs: 20, LinkType: "ETH"}}
			mellanoxNicsStatus := map[string]map[string]sriovnetworkv1.InterfaceExt{"0000:d8:00.": mellanoxNicsExt}
			attrs := &MlxNic{}

			needChange, err := HandleLinkType("0000:d8:00.", fwData, attrs, mellanoxNicsSpec, mellanoxNicsStatus)
			Expect(err).ToNot(HaveOccurred())
			Expect(needChange).To(BeTrue())
			Expect(attrs.LinkTypeP1).To(Equal("IB"))
		})

		It("should return false and error if the link type is not supported", func() {
			fwData := &MlxNic{LinkTypeP1: "ETH", LinkTypeP2: "ETH"}
			ifaceSpec := sriovnetworkv1.Interface{PciAddress: "0000:d8:00.0", NumVfs: 10, LinkType: "TEST"}
			mellanoxNicsSpec := map[string]sriovnetworkv1.Interface{"0000:d8:00.0": ifaceSpec, "0000:d8:00.1": {NumVfs: 20}}
			mellanoxNicsExt := map[string]sriovnetworkv1.InterfaceExt{"0000:d8:00.0": {PciAddress: "0000:d8:00.0", LinkType: "ETH"}, "0000:d8:00.1": {NumVfs: 20, LinkType: "ETH"}}
			mellanoxNicsStatus := map[string]map[string]sriovnetworkv1.InterfaceExt{"0000:d8:00.": mellanoxNicsExt}
			attrs := &MlxNic{}

			_, err := HandleLinkType("0000:d8:00.", fwData, attrs, mellanoxNicsSpec, mellanoxNicsStatus)
			Expect(err).To(HaveOccurred())
		})

		It("should return false and error if the link type is Unknown", func() {
			fwData := &MlxNic{LinkTypeP1: "Unknown", LinkTypeP2: "ETH"}
			ifaceSpec := sriovnetworkv1.Interface{PciAddress: "0000:d8:00.0", NumVfs: 10, LinkType: "IB"}
			mellanoxNicsSpec := map[string]sriovnetworkv1.Interface{"0000:d8:00.0": ifaceSpec, "0000:d8:00.1": {NumVfs: 20}}
			mellanoxNicsExt := map[string]sriovnetworkv1.InterfaceExt{"0000:d8:00.0": {PciAddress: "0000:d8:00.0", LinkType: "ETH"}, "0000:d8:00.1": {NumVfs: 20, LinkType: "ETH"}}
			mellanoxNicsStatus := map[string]map[string]sriovnetworkv1.InterfaceExt{"0000:d8:00.": mellanoxNicsExt}
			attrs := &MlxNic{}

			_, err := HandleLinkType("0000:d8:00.", fwData, attrs, mellanoxNicsSpec, mellanoxNicsStatus)
			Expect(err).To(HaveOccurred())
		})

		It("should return false and error if the link type is Preconfigured", func() {
			fwData := &MlxNic{LinkTypeP1: "Unknown", LinkTypeP2: "ETH"}
			ifaceSpec := sriovnetworkv1.Interface{PciAddress: "0000:d8:00.0", NumVfs: 10, LinkType: "IB"}
			mellanoxNicsSpec := map[string]sriovnetworkv1.Interface{"0000:d8:00.0": ifaceSpec, "0000:d8:00.1": {NumVfs: 20}}
			mellanoxNicsExt := map[string]sriovnetworkv1.InterfaceExt{"0000:d8:00.0": {PciAddress: "0000:d8:00.0", LinkType: "ETH"}, "0000:d8:00.1": {NumVfs: 20, LinkType: "ETH"}}
			mellanoxNicsStatus := map[string]map[string]sriovnetworkv1.InterfaceExt{"0000:d8:00.": mellanoxNicsExt}
			attrs := &MlxNic{}

			_, err := HandleLinkType("0000:d8:00.", fwData, attrs, mellanoxNicsSpec, mellanoxNicsStatus)
			Expect(err).To(HaveOccurred())
		})

		It("should return true if there is a need to change link type for Port 2", func() {
			fwData := &MlxNic{LinkTypeP1: "ETH", LinkTypeP2: "ETH"}
			ifaceSpec1 := sriovnetworkv1.Interface{PciAddress: "0000:d8:00.0", NumVfs: 10, LinkType: "ETH"}
			ifaceSpec2 := sriovnetworkv1.Interface{PciAddress: "0000:d8:00.0", NumVfs: 10, LinkType: "IB"}
			mellanoxNicsSpec := map[string]sriovnetworkv1.Interface{"0000:d8:00.0": ifaceSpec1, "0000:d8:00.1": ifaceSpec2}
			mellanoxNicsExt := map[string]sriovnetworkv1.InterfaceExt{"0000:d8:00.0": {PciAddress: "0000:d8:00.0", LinkType: "ETH"}, "0000:d8:00.1": {NumVfs: 20, LinkType: "ETH"}}
			mellanoxNicsStatus := map[string]map[string]sriovnetworkv1.InterfaceExt{"0000:d8:00.": mellanoxNicsExt}
			attrs := &MlxNic{}

			needChange, err := HandleLinkType("0000:d8:00.", fwData, attrs, mellanoxNicsSpec, mellanoxNicsStatus)
			Expect(err).ToNot(HaveOccurred())
			Expect(needChange).To(BeTrue())
			Expect(attrs.LinkTypeP2).To(Equal("IB"))
		})

		It("should return true if there is a need to change both links", func() {
			fwData := &MlxNic{LinkTypeP1: "ETH", LinkTypeP2: "ETH"}
			ifaceSpec1 := sriovnetworkv1.Interface{PciAddress: "0000:d8:00.0", NumVfs: 10, LinkType: "IB"}
			ifaceSpec2 := sriovnetworkv1.Interface{PciAddress: "0000:d8:00.0", NumVfs: 10, LinkType: "IB"}
			mellanoxNicsSpec := map[string]sriovnetworkv1.Interface{"0000:d8:00.0": ifaceSpec1, "0000:d8:00.1": ifaceSpec2}
			mellanoxNicsExt := map[string]sriovnetworkv1.InterfaceExt{"0000:d8:00.0": {PciAddress: "0000:d8:00.0", LinkType: "ETH"}, "0000:d8:00.1": {NumVfs: 20, LinkType: "ETH"}}
			mellanoxNicsStatus := map[string]map[string]sriovnetworkv1.InterfaceExt{"0000:d8:00.": mellanoxNicsExt}
			attrs := &MlxNic{}

			needChange, err := HandleLinkType("0000:d8:00.", fwData, attrs, mellanoxNicsSpec, mellanoxNicsStatus)
			Expect(err).ToNot(HaveOccurred())
			Expect(needChange).To(BeTrue())
			Expect(attrs.LinkTypeP1).To(Equal("IB"))
			Expect(attrs.LinkTypeP2).To(Equal("IB"))
		})
	})
})

func getMstconfigOutput(numOfVfsCurrent, numofVfsNextBoot int, sriovEnableDefault, sriovEnableCurrent, sriovEnableNextBoot string, withETHLinkType, withIBLinkType, withUnknowLinkType bool) string {
	mstconfigOutput := `
Device #1:
----------

Device type:        ConnectX4LX         
Name:               0R887V              
Description:        MCX422A-ACAA ConnectX-4 Lx EN Dual Port SFP28; 25GbE for Dell rack NDC
Device:             19:00.0             

Configurations:                                     Default              Current              Next Boot
%s
        MEMIC_BAR_SIZE                              0                    0                    0                   
        MEMIC_SIZE_LIMIT                            _256KB(1)            _256KB(1)            _256KB(1)           
        FLEX_PARSER_PROFILE_ENABLE                  0                    0                    0                   
        FLEX_IPV4_OVER_VXLAN_PORT                   0                    0                    0                   
        ROCE_NEXT_PROTOCOL                          254                  254                  254                 
        PF_NUM_OF_VF_VALID                          False(0)             False(0)             False(0)            
        NON_PREFETCHABLE_PF_BAR                     False(0)             False(0)             False(0)            
        VF_VPD_ENABLE                               False(0)             False(0)             False(0)            
        STRICT_VF_MSIX_NUM                          False(0)             False(0)             False(0)            
        VF_NODNIC_ENABLE                            False(0)             False(0)             False(0)            
        NUM_PF_MSIX_VALID                           True(1)              True(1)              True(1)             
*       NUM_OF_VFS                                  8                    %d                   %d                   
        NUM_OF_PF                                   2                    2                    2                   
*       SRIOV_EN                                    %s(0)                %s(1)                  %s(1)             
        PF_LOG_BAR_SIZE                             5                    5                    5                   
        VF_LOG_BAR_SIZE                             0                    0                    0                   
        NUM_PF_MSIX                                 63                   63                   63                  
        NUM_VF_MSIX                                 11                   11                   11                  
        INT_LOG_MAX_PAYLOAD_SIZE                    AUTOMATIC(0)         AUTOMATIC(0)         AUTOMATIC(0)        
        PCIE_CREDIT_TOKEN_TIMEOUT                   0                    0                    0                   
        ACCURATE_TX_SCHEDULER                       False(0)             False(0)             False(0)            
        PARTIAL_RESET_EN                            False(0)             False(0)             False(0)            
        SW_RECOVERY_ON_ERRORS                       False(0)             False(0)             False(0)            
        RESET_WITH_HOST_ON_ERRORS                   False(0)             False(0)             False(0)            
        PCI_BUS0_RESTRICT_SPEED                     PCI_GEN_1(0)         PCI_GEN_1(0)         PCI_GEN_1(0)        
        PCI_BUS0_RESTRICT_ASPM                      False(0)             False(0)             False(0)            
        PCI_BUS0_RESTRICT_WIDTH                     PCI_X1(0)            PCI_X1(0)            PCI_X1(0)           
        PCI_BUS0_RESTRICT                           False(0)             False(0)             False(0)            
        PCI_DOWNSTREAM_PORT_OWNER                   Array[0..15]         Array[0..15]         Array[0..15]        
        CQE_COMPRESSION                             BALANCED(0)          BALANCED(0)          BALANCED(0)         
        IP_OVER_VXLAN_EN                            False(0)             False(0)             False(0)            
        MKEY_BY_NAME                                False(0)             False(0)             False(0)            
        UCTX_EN                                     True(1)              True(1)              True(1)             
        PCI_ATOMIC_MODE                             PCI_ATOMIC_DISABLED_EXT_ATOMIC_ENABLED(0) PCI_ATOMIC_DISABLED_EXT_ATOMIC_ENABLED(0) PCI_ATOMIC_DISABLED_EXT_ATOMIC_ENABLED(0)
        TUNNEL_ECN_COPY_DISABLE                     False(0)             False(0)             False(0)            
        LRO_LOG_TIMEOUT0                            6                    6                    6                   
        LRO_LOG_TIMEOUT1                            7                    7                    7                   
        LRO_LOG_TIMEOUT2                            8                    8                    8                   
        LRO_LOG_TIMEOUT3                            13                   13                   13                  
        ICM_CACHE_MODE                              DEVICE_DEFAULT(0)    DEVICE_DEFAULT(0)    DEVICE_DEFAULT(0)   
        TX_SCHEDULER_BURST                          0                    0                    0                   
        LOG_DCR_HASH_TABLE_SIZE                     14                   14                   14                  
        MAX_PACKET_LIFETIME                         0                    0                    0                   
        DCR_LIFO_SIZE                               16384                16384                16384               
        ROCE_CC_PRIO_MASK_P1                        255                  255                  255                 
        ROCE_CC_CNP_MODERATION_P1                   DEVICE_DEFAULT(0)    DEVICE_DEFAULT(0)    DEVICE_DEFAULT(0)   
        ROCE_CC_PRIO_MASK_P2                        255                  255                  255                 
        ROCE_CC_CNP_MODERATION_P2                   DEVICE_DEFAULT(0)    DEVICE_DEFAULT(0)    DEVICE_DEFAULT(0)   
        CLAMP_TGT_RATE_AFTER_TIME_INC_P1            True(1)              True(1)              True(1)             
        CLAMP_TGT_RATE_P1                           False(0)             False(0)             False(0)            
        RPG_TIME_RESET_P1                           300                  300                  300                 
        RPG_BYTE_RESET_P1                           32767                32767                32767               
        RPG_THRESHOLD_P1                            1                    1                    1                   
        RPG_MAX_RATE_P1                             0                    0                    0                   
        RPG_AI_RATE_P1                              5                    5                    5                   
        RPG_HAI_RATE_P1                             50                   50                   50                  
        RPG_GD_P1                                   11                   11                   11                  
        RPG_MIN_DEC_FAC_P1                          50                   50                   50                  
        RPG_MIN_RATE_P1                             1                    1                    1                   
        RATE_TO_SET_ON_FIRST_CNP_P1                 0                    0                    0                   
        DCE_TCP_G_P1                                1019                 1019                 1019                
        DCE_TCP_RTT_P1                              1                    1                    1                   
        RATE_REDUCE_MONITOR_PERIOD_P1               4                    4                    4                   
        INITIAL_ALPHA_VALUE_P1                      1023                 1023                 1023                
        MIN_TIME_BETWEEN_CNPS_P1                    4                    4                    4                   
        CNP_802P_PRIO_P1                            6                    6                    6                   
        CNP_DSCP_P1                                 48                   48                   48                  
        CLAMP_TGT_RATE_AFTER_TIME_INC_P2            True(1)              True(1)              True(1)             
        CLAMP_TGT_RATE_P2                           False(0)             False(0)             False(0)            
        RPG_TIME_RESET_P2                           300                  300                  300                 
        RPG_BYTE_RESET_P2                           32767                32767                32767               
        RPG_THRESHOLD_P2                            1                    1                    1                   
        RPG_MAX_RATE_P2                             0                    0                    0                   
        RPG_AI_RATE_P2                              5                    5                    5                   
        RPG_HAI_RATE_P2                             50                   50                   50                  
        RPG_GD_P2                                   11                   11                   11                  
        RPG_MIN_DEC_FAC_P2                          50                   50                   50                  
        RPG_MIN_RATE_P2                             1                    1                    1                   
        RATE_TO_SET_ON_FIRST_CNP_P2                 0                    0                    0                   
        DCE_TCP_G_P2                                1019                 1019                 1019                
        DCE_TCP_RTT_P2                              1                    1                    1                   
        RATE_REDUCE_MONITOR_PERIOD_P2               4                    4                    4                   
        INITIAL_ALPHA_VALUE_P2                      1023                 1023                 1023                
        MIN_TIME_BETWEEN_CNPS_P2                    4                    4                    4                   
        CNP_802P_PRIO_P2                            6                    6                    6                   
        CNP_DSCP_P2                                 48                   48                   48                  
        LLDP_NB_DCBX_P1                             False(0)             False(0)             False(0)            
        LLDP_NB_RX_MODE_P1                          ALL(2)               ALL(2)               ALL(2)              
        LLDP_NB_TX_MODE_P1                          ALL(2)               ALL(2)               ALL(2)              
        LLDP_NB_DCBX_P2                             False(0)             False(0)             False(0)            
        LLDP_NB_RX_MODE_P2                          ALL(2)               ALL(2)               ALL(2)              
        LLDP_NB_TX_MODE_P2                          ALL(2)               ALL(2)               ALL(2)              
        ROCE_RTT_RESP_DSCP_P1                       0                    0                    0                   
        ROCE_RTT_RESP_DSCP_MODE_P1                  DEVICE_DEFAULT(0)    DEVICE_DEFAULT(0)    DEVICE_DEFAULT(0)   
        ROCE_RTT_RESP_DSCP_P2                       0                    0                    0                   
        ROCE_RTT_RESP_DSCP_MODE_P2                  DEVICE_DEFAULT(0)    DEVICE_DEFAULT(0)    DEVICE_DEFAULT(0)   
        DCBX_IEEE_P1                                True(1)              True(1)              True(1)             
        DCBX_CEE_P1                                 True(1)              True(1)              True(1)             
        DCBX_WILLING_P1                             True(1)              True(1)              True(1)             
        DCBX_IEEE_P2                                True(1)              True(1)              True(1)             
        DCBX_CEE_P2                                 True(1)              True(1)              True(1)             
        DCBX_WILLING_P2                             True(1)              True(1)              True(1)             
        KEEP_ETH_LINK_UP_P1                         True(1)              True(1)              True(1)             
        KEEP_IB_LINK_UP_P1                          False(0)             False(0)             False(0)            
        KEEP_LINK_UP_ON_BOOT_P1                     False(0)             False(0)             False(0)            
        KEEP_LINK_UP_ON_STANDBY_P1                  False(0)             False(0)             False(0)            
        DO_NOT_CLEAR_PORT_STATS_P1                  False(0)             False(0)             False(0)            
        AUTO_POWER_SAVE_LINK_DOWN_P1                False(0)             False(0)             False(0)            
        KEEP_ETH_LINK_UP_P2                         True(1)              True(1)              True(1)             
        KEEP_IB_LINK_UP_P2                          False(0)             False(0)             False(0)            
        KEEP_LINK_UP_ON_BOOT_P2                     False(0)             False(0)             False(0)            
        KEEP_LINK_UP_ON_STANDBY_P2                  False(0)             False(0)             False(0)            
        DO_NOT_CLEAR_PORT_STATS_P2                  False(0)             False(0)             False(0)            
        AUTO_POWER_SAVE_LINK_DOWN_P2                False(0)             False(0)             False(0)            
        NUM_OF_VL_P1                                _4_VLs(3)            _4_VLs(3)            _4_VLs(3)           
        NUM_OF_TC_P1                                _8_TCs(0)            _8_TCs(0)            _8_TCs(0)           
        NUM_OF_PFC_P1                               8                    8                    8                   
        VL15_BUFFER_SIZE_P1                         0                    0                    0                   
        NUM_OF_VL_P2                                _4_VLs(3)            _4_VLs(3)            _4_VLs(3)           
        NUM_OF_TC_P2                                _8_TCs(0)            _8_TCs(0)            _8_TCs(0)           
        NUM_OF_PFC_P2                               8                    8                    8                   
        VL15_BUFFER_SIZE_P2                         0                    0                    0                   
        DUP_MAC_ACTION_P1                           LAST_CFG(0)          LAST_CFG(0)          LAST_CFG(0)         
        SRIOV_IB_ROUTING_MODE_P1                    LID(1)               LID(1)               LID(1)              
        IB_ROUTING_MODE_P1                          LID(1)               LID(1)               LID(1)              
        DUP_MAC_ACTION_P2                           LAST_CFG(0)          LAST_CFG(0)          LAST_CFG(0)         
        SRIOV_IB_ROUTING_MODE_P2                    LID(1)               LID(1)               LID(1)              
        IB_ROUTING_MODE_P2                          LID(1)               LID(1)               LID(1)              
        PHY_FEC_OVERRIDE_P1                         DEVICE_DEFAULT(0)    DEVICE_DEFAULT(0)    DEVICE_DEFAULT(0)   
        PHY_FEC_OVERRIDE_P2                         DEVICE_DEFAULT(0)    DEVICE_DEFAULT(0)    DEVICE_DEFAULT(0)   
        WOL_MAGIC_EN                                False(0)             False(0)             False(0)            
        PF_SD_GROUP                                 0                    0                    0                   
*       ROCE_CONTROL                                ROCE_ENABLE(2)       DEVICE_DEFAULT(0)    ROCE_ENABLE(2)      
        PCI_WR_ORDERING                             per_mkey(0)          per_mkey(0)          per_mkey(0)         
        MULTI_PORT_VHCA_EN                          False(0)             False(0)             False(0)            
        PORT_OWNER                                  True(1)              True(1)              True(1)             
        ALLOW_RD_COUNTERS                           True(1)              True(1)              True(1)             
        RENEG_ON_CHANGE                             True(1)              True(1)              True(1)             
        TRACER_ENABLE                               True(1)              True(1)              True(1)             
        BOOT_UNDI_NETWORK_WAIT                      0                    0                    0                   
        UEFI_HII_EN                                 True(1)              True(1)              True(1)             
        BOOT_DBG_LOG                                False(0)             False(0)             False(0)            
        UEFI_LOGS                                   DISABLED(0)          DISABLED(0)          DISABLED(0)         
        BOOT_VLAN                                   1                    1                    1                   
*       LEGACY_BOOT_PROTOCOL                        PXE(1)               PXE(1)               NONE(0)             
        BOOT_INTERRUPT_DIS                          False(0)             False(0)             False(0)            
        BOOT_LACP_DIS                               True(1)              True(1)              True(1)             
        BOOT_VLAN_EN                                False(0)             False(0)             False(0)            
        BOOT_PKEY                                   0                    0                    0                   
        DYNAMIC_VF_MSIX_TABLE                       False(0)             False(0)             False(0)            
        EXP_ROM_UEFI_x86_ENABLE                     True(1)              True(1)              True(1)             
        EXP_ROM_PXE_ENABLE                          True(1)              True(1)              True(1)             
        ADVANCED_PCI_SETTINGS                       False(0)             False(0)             False(0)            
        SAFE_MODE_THRESHOLD                         10                   10                   10                  
        SAFE_MODE_ENABLE                            True(1)              True(1)              True(1)             
The '*' shows parameters with next value different from default/current value.`

	if withETHLinkType {
		linkType := `        LINK_TYPE_P1                                ETH                  ETH                  ETH
        LINK_TYPE_P2                                ETH                  ETH                  ETH`
		return fmt.Sprintf(mstconfigOutput, linkType, numOfVfsCurrent, numofVfsNextBoot, sriovEnableDefault, sriovEnableCurrent, sriovEnableNextBoot)
	}

	if withIBLinkType {
		linkType := `        LINK_TYPE_P1                                IB                  IB                  ETH
        LINK_TYPE_P2                                IB                  IB                  ETH`
		return fmt.Sprintf(mstconfigOutput, linkType, numOfVfsCurrent, numofVfsNextBoot, sriovEnableDefault, sriovEnableCurrent, sriovEnableNextBoot)
	}

	if withUnknowLinkType {
		linkType := `        LINK_TYPE_P1                                TEST                  TEST                  TEST
        LINK_TYPE_P2                                IB                  IB                  ETH`
		return fmt.Sprintf(mstconfigOutput, linkType, numOfVfsCurrent, numofVfsNextBoot, sriovEnableDefault, sriovEnableCurrent, sriovEnableNextBoot)
	}

	return fmt.Sprintf(mstconfigOutput, "", numOfVfsCurrent, numofVfsNextBoot, sriovEnableDefault, sriovEnableCurrent, sriovEnableNextBoot)
}

func getBFMstconfigOutput(DPUMode, unExpected bool) string {
	mstconfigOutput := `
Device #1:
----------

Device type:        ConnectX4LX         
Name:               0R887V              
Description:        MCX422A-ACAA ConnectX-4 Lx EN Dual Port SFP28; 25GbE for Dell rack NDC
Device:             19:00.0             

Configurations:                                     Default              Current              Next Boot
        INTERNAL_CPU_PAGE_SUPPLIER                  %s                   %s                   %s
        INTERNAL_CPU_ESWITCH_MANAGER                %s                   %s                   %s
        INTERNAL_CPU_IB_VPORT0                      %s                   %s                   %s
        INTERNAL_CPU_OFFLOAD_ENGINE                 %s                   %s                   %s
        INTERNAL_CPU_MODEL                          %s                   %s                   %s
        MEMIC_BAR_SIZE                              0                    0                    0                   
        MEMIC_SIZE_LIMIT                            _256KB(1)            _256KB(1)            _256KB(1)           
        FLEX_PARSER_PROFILE_ENABLE                  0                    0                    0                   
        FLEX_IPV4_OVER_VXLAN_PORT                   0                    0                    0                   
        ROCE_NEXT_PROTOCOL                          254                  254                  254                 
        PF_NUM_OF_VF_VALID                          False(0)             False(0)             False(0)            
        NON_PREFETCHABLE_PF_BAR                     False(0)             False(0)             False(0)            
        VF_VPD_ENABLE                               False(0)             False(0)             False(0)            
        STRICT_VF_MSIX_NUM                          False(0)             False(0)             False(0)            
        VF_NODNIC_ENABLE                            False(0)             False(0)             False(0)            
        NUM_PF_MSIX_VALID                           True(1)              True(1)              True(1)             
*       NUM_OF_VFS                                  8                    0                    0                  
        NUM_OF_PF                                   2                    2                    2                   
*       SRIOV_EN                                    True(0)              True(1)              True(1)             
        PF_LOG_BAR_SIZE                             5                    5                    5                   
        VF_LOG_BAR_SIZE                             0                    0                    0                   
        NUM_PF_MSIX                                 63                   63                   63                  
        NUM_VF_MSIX                                 11                   11                   11                  
        INT_LOG_MAX_PAYLOAD_SIZE                    AUTOMATIC(0)         AUTOMATIC(0)         AUTOMATIC(0)        
        PCIE_CREDIT_TOKEN_TIMEOUT                   0                    0                    0                   
        ACCURATE_TX_SCHEDULER                       False(0)             False(0)             False(0)            
        PARTIAL_RESET_EN                            False(0)             False(0)             False(0)            
        SW_RECOVERY_ON_ERRORS                       False(0)             False(0)             False(0)            
        RESET_WITH_HOST_ON_ERRORS                   False(0)             False(0)             False(0)            
        PCI_BUS0_RESTRICT_SPEED                     PCI_GEN_1(0)         PCI_GEN_1(0)         PCI_GEN_1(0)        
        PCI_BUS0_RESTRICT_ASPM                      False(0)             False(0)             False(0)            
        PCI_BUS0_RESTRICT_WIDTH                     PCI_X1(0)            PCI_X1(0)            PCI_X1(0)           
        PCI_BUS0_RESTRICT                           False(0)             False(0)             False(0)            
        PCI_DOWNSTREAM_PORT_OWNER                   Array[0..15]         Array[0..15]         Array[0..15]        
        CQE_COMPRESSION                             BALANCED(0)          BALANCED(0)          BALANCED(0)         
        IP_OVER_VXLAN_EN                            False(0)             False(0)             False(0)            
        MKEY_BY_NAME                                False(0)             False(0)             False(0)            
        UCTX_EN                                     True(1)              True(1)              True(1)             
        PCI_ATOMIC_MODE                             PCI_ATOMIC_DISABLED_EXT_ATOMIC_ENABLED(0) PCI_ATOMIC_DISABLED_EXT_ATOMIC_ENABLED(0) PCI_ATOMIC_DISABLED_EXT_ATOMIC_ENABLED(0)
        TUNNEL_ECN_COPY_DISABLE                     False(0)             False(0)             False(0)            
        LRO_LOG_TIMEOUT0                            6                    6                    6                   
        LRO_LOG_TIMEOUT1                            7                    7                    7                   
        LRO_LOG_TIMEOUT2                            8                    8                    8                   
        LRO_LOG_TIMEOUT3                            13                   13                   13                  
        ICM_CACHE_MODE                              DEVICE_DEFAULT(0)    DEVICE_DEFAULT(0)    DEVICE_DEFAULT(0)   
        TX_SCHEDULER_BURST                          0                    0                    0                   
        LOG_DCR_HASH_TABLE_SIZE                     14                   14                   14                  
        MAX_PACKET_LIFETIME                         0                    0                    0                   
        DCR_LIFO_SIZE                               16384                16384                16384               
        ROCE_CC_PRIO_MASK_P1                        255                  255                  255                 
        ROCE_CC_CNP_MODERATION_P1                   DEVICE_DEFAULT(0)    DEVICE_DEFAULT(0)    DEVICE_DEFAULT(0)   
        ROCE_CC_PRIO_MASK_P2                        255                  255                  255                 
        ROCE_CC_CNP_MODERATION_P2                   DEVICE_DEFAULT(0)    DEVICE_DEFAULT(0)    DEVICE_DEFAULT(0)   
        CLAMP_TGT_RATE_AFTER_TIME_INC_P1            True(1)              True(1)              True(1)             
        CLAMP_TGT_RATE_P1                           False(0)             False(0)             False(0)            
        RPG_TIME_RESET_P1                           300                  300                  300                 
        RPG_BYTE_RESET_P1                           32767                32767                32767               
        RPG_THRESHOLD_P1                            1                    1                    1                   
        RPG_MAX_RATE_P1                             0                    0                    0                   
        RPG_AI_RATE_P1                              5                    5                    5                   
        RPG_HAI_RATE_P1                             50                   50                   50                  
        RPG_GD_P1                                   11                   11                   11                  
        RPG_MIN_DEC_FAC_P1                          50                   50                   50                  
        RPG_MIN_RATE_P1                             1                    1                    1                   
        RATE_TO_SET_ON_FIRST_CNP_P1                 0                    0                    0                   
        DCE_TCP_G_P1                                1019                 1019                 1019                
        DCE_TCP_RTT_P1                              1                    1                    1                   
        RATE_REDUCE_MONITOR_PERIOD_P1               4                    4                    4                   
        INITIAL_ALPHA_VALUE_P1                      1023                 1023                 1023                
        MIN_TIME_BETWEEN_CNPS_P1                    4                    4                    4                   
        CNP_802P_PRIO_P1                            6                    6                    6                   
        CNP_DSCP_P1                                 48                   48                   48                  
        CLAMP_TGT_RATE_AFTER_TIME_INC_P2            True(1)              True(1)              True(1)             
        CLAMP_TGT_RATE_P2                           False(0)             False(0)             False(0)            
        RPG_TIME_RESET_P2                           300                  300                  300                 
        RPG_BYTE_RESET_P2                           32767                32767                32767               
        RPG_THRESHOLD_P2                            1                    1                    1                   
        RPG_MAX_RATE_P2                             0                    0                    0                   
        RPG_AI_RATE_P2                              5                    5                    5                   
        RPG_HAI_RATE_P2                             50                   50                   50                  
        RPG_GD_P2                                   11                   11                   11                  
        RPG_MIN_DEC_FAC_P2                          50                   50                   50                  
        RPG_MIN_RATE_P2                             1                    1                    1                   
        RATE_TO_SET_ON_FIRST_CNP_P2                 0                    0                    0                   
        DCE_TCP_G_P2                                1019                 1019                 1019                
        DCE_TCP_RTT_P2                              1                    1                    1                   
        RATE_REDUCE_MONITOR_PERIOD_P2               4                    4                    4                   
        INITIAL_ALPHA_VALUE_P2                      1023                 1023                 1023                
        MIN_TIME_BETWEEN_CNPS_P2                    4                    4                    4                   
        CNP_802P_PRIO_P2                            6                    6                    6                   
        CNP_DSCP_P2                                 48                   48                   48                  
        LLDP_NB_DCBX_P1                             False(0)             False(0)             False(0)            
        LLDP_NB_RX_MODE_P1                          ALL(2)               ALL(2)               ALL(2)              
        LLDP_NB_TX_MODE_P1                          ALL(2)               ALL(2)               ALL(2)              
        LLDP_NB_DCBX_P2                             False(0)             False(0)             False(0)            
        LLDP_NB_RX_MODE_P2                          ALL(2)               ALL(2)               ALL(2)              
        LLDP_NB_TX_MODE_P2                          ALL(2)               ALL(2)               ALL(2)              
        ROCE_RTT_RESP_DSCP_P1                       0                    0                    0                   
        ROCE_RTT_RESP_DSCP_MODE_P1                  DEVICE_DEFAULT(0)    DEVICE_DEFAULT(0)    DEVICE_DEFAULT(0)   
        ROCE_RTT_RESP_DSCP_P2                       0                    0                    0                   
        ROCE_RTT_RESP_DSCP_MODE_P2                  DEVICE_DEFAULT(0)    DEVICE_DEFAULT(0)    DEVICE_DEFAULT(0)   
        DCBX_IEEE_P1                                True(1)              True(1)              True(1)             
        DCBX_CEE_P1                                 True(1)              True(1)              True(1)             
        DCBX_WILLING_P1                             True(1)              True(1)              True(1)             
        DCBX_IEEE_P2                                True(1)              True(1)              True(1)             
        DCBX_CEE_P2                                 True(1)              True(1)              True(1)             
        DCBX_WILLING_P2                             True(1)              True(1)              True(1)             
        KEEP_ETH_LINK_UP_P1                         True(1)              True(1)              True(1)             
        KEEP_IB_LINK_UP_P1                          False(0)             False(0)             False(0)            
        KEEP_LINK_UP_ON_BOOT_P1                     False(0)             False(0)             False(0)            
        KEEP_LINK_UP_ON_STANDBY_P1                  False(0)             False(0)             False(0)            
        DO_NOT_CLEAR_PORT_STATS_P1                  False(0)             False(0)             False(0)            
        AUTO_POWER_SAVE_LINK_DOWN_P1                False(0)             False(0)             False(0)            
        KEEP_ETH_LINK_UP_P2                         True(1)              True(1)              True(1)             
        KEEP_IB_LINK_UP_P2                          False(0)             False(0)             False(0)            
        KEEP_LINK_UP_ON_BOOT_P2                     False(0)             False(0)             False(0)            
        KEEP_LINK_UP_ON_STANDBY_P2                  False(0)             False(0)             False(0)            
        DO_NOT_CLEAR_PORT_STATS_P2                  False(0)             False(0)             False(0)            
        AUTO_POWER_SAVE_LINK_DOWN_P2                False(0)             False(0)             False(0)            
        NUM_OF_VL_P1                                _4_VLs(3)            _4_VLs(3)            _4_VLs(3)           
        NUM_OF_TC_P1                                _8_TCs(0)            _8_TCs(0)            _8_TCs(0)           
        NUM_OF_PFC_P1                               8                    8                    8                   
        VL15_BUFFER_SIZE_P1                         0                    0                    0                   
        NUM_OF_VL_P2                                _4_VLs(3)            _4_VLs(3)            _4_VLs(3)           
        NUM_OF_TC_P2                                _8_TCs(0)            _8_TCs(0)            _8_TCs(0)           
        NUM_OF_PFC_P2                               8                    8                    8                   
        VL15_BUFFER_SIZE_P2                         0                    0                    0                   
        DUP_MAC_ACTION_P1                           LAST_CFG(0)          LAST_CFG(0)          LAST_CFG(0)         
        SRIOV_IB_ROUTING_MODE_P1                    LID(1)               LID(1)               LID(1)              
        IB_ROUTING_MODE_P1                          LID(1)               LID(1)               LID(1)              
        DUP_MAC_ACTION_P2                           LAST_CFG(0)          LAST_CFG(0)          LAST_CFG(0)         
        SRIOV_IB_ROUTING_MODE_P2                    LID(1)               LID(1)               LID(1)              
        IB_ROUTING_MODE_P2                          LID(1)               LID(1)               LID(1)              
        PHY_FEC_OVERRIDE_P1                         DEVICE_DEFAULT(0)    DEVICE_DEFAULT(0)    DEVICE_DEFAULT(0)   
        PHY_FEC_OVERRIDE_P2                         DEVICE_DEFAULT(0)    DEVICE_DEFAULT(0)    DEVICE_DEFAULT(0)   
        WOL_MAGIC_EN                                False(0)             False(0)             False(0)            
        PF_SD_GROUP                                 0                    0                    0                   
*       ROCE_CONTROL                                ROCE_ENABLE(2)       DEVICE_DEFAULT(0)    ROCE_ENABLE(2)      
        PCI_WR_ORDERING                             per_mkey(0)          per_mkey(0)          per_mkey(0)         
        MULTI_PORT_VHCA_EN                          False(0)             False(0)             False(0)            
        PORT_OWNER                                  True(1)              True(1)              True(1)             
        ALLOW_RD_COUNTERS                           True(1)              True(1)              True(1)             
        RENEG_ON_CHANGE                             True(1)              True(1)              True(1)             
        TRACER_ENABLE                               True(1)              True(1)              True(1)             
        BOOT_UNDI_NETWORK_WAIT                      0                    0                    0                   
        UEFI_HII_EN                                 True(1)              True(1)              True(1)             
        BOOT_DBG_LOG                                False(0)             False(0)             False(0)            
        UEFI_LOGS                                   DISABLED(0)          DISABLED(0)          DISABLED(0)         
        BOOT_VLAN                                   1                    1                    1                   
*       LEGACY_BOOT_PROTOCOL                        PXE(1)               PXE(1)               NONE(0)             
        BOOT_INTERRUPT_DIS                          False(0)             False(0)             False(0)            
        BOOT_LACP_DIS                               True(1)              True(1)              True(1)             
        BOOT_VLAN_EN                                False(0)             False(0)             False(0)            
        BOOT_PKEY                                   0                    0                    0                   
        DYNAMIC_VF_MSIX_TABLE                       False(0)             False(0)             False(0)            
        EXP_ROM_UEFI_x86_ENABLE                     True(1)              True(1)              True(1)             
        EXP_ROM_PXE_ENABLE                          True(1)              True(1)              True(1)             
        ADVANCED_PCI_SETTINGS                       False(0)             False(0)             False(0)            
        SAFE_MODE_THRESHOLD                         10                   10                   10                  
        SAFE_MODE_ENABLE                            True(1)              True(1)              True(1)             
The '*' shows parameters with next value different from default/current value.`
	if DPUMode {
		return fmt.Sprintf(mstconfigOutput, ecpf, ecpf, ecpf, ecpf, ecpf, ecpf, ecpf, ecpf, ecpf, enabled, enabled, enabled, embeddedCPU, embeddedCPU, embeddedCPU)
	}

	if unExpected {
		return fmt.Sprintf(mstconfigOutput, extHostPf, extHostPf, extHostPf, ecpf, ecpf, ecpf, ecpf, ecpf, ecpf, enabled, enabled, enabled, embeddedCPU, embeddedCPU, embeddedCPU)
	}

	return fmt.Sprintf(mstconfigOutput, extHostPf, extHostPf, extHostPf, extHostPf, extHostPf, extHostPf, extHostPf, extHostPf, extHostPf, disabled, disabled, disabled, embeddedCPU, embeddedCPU, embeddedCPU)
}
