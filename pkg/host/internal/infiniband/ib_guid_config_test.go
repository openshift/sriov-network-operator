package infiniband

import (
	"fmt"

	"github.com/golang/mock/gomock"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/vishvananda/netlink"

	netlinkLibPkg "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/host/internal/lib/netlink"
	netlinkMockPkg "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/host/internal/lib/netlink/mock"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/host/internal/network"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/host/types"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/test/util/fakefilesystem"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/test/util/helpers"
)

var _ = Describe("IbGuidConfig", func() {
	Describe("readJSONConfig", Ordered, func() {
		var (
			createJsonConfig func(string) string
		)

		BeforeEach(func() {
			createJsonConfig = func(content string) string {
				configPath := "/config.json"
				helpers.GinkgoConfigureFakeFS(&fakefilesystem.FS{
					Dirs:  []string{"/host"},
					Files: map[string][]byte{"/host" + configPath: []byte(content)},
				})

				return configPath
			}
		})

		It("should correctly decode a JSON configuration file with all fields present", func() {
			mockJsonConfig := `[{"pciAddress":"0000:00:00.0","pfGuid":"00:00:00:00:00:00:00:00","guids":["00:01:02:03:04:05:06:07", "00:01:02:03:04:05:06:08"],"guidsRange":{"start":"00:01:02:03:04:05:06:08","end":"00:01:02:03:04:05:06:FF"}}]`

			configPath := createJsonConfig(mockJsonConfig)

			configs, err := readJSONConfig(configPath)
			Expect(err).NotTo(HaveOccurred())
			Expect(configs).To(HaveLen(1))
			Expect(configs[0].PciAddress).To(Equal("0000:00:00.0"))
			Expect(configs[0].GUIDs).To(ContainElement("00:01:02:03:04:05:06:07"))
			Expect(configs[0].GUIDs).To(ContainElement("00:01:02:03:04:05:06:08"))
			Expect(configs[0].GUIDsRange.Start).To(Equal("00:01:02:03:04:05:06:08"))
			Expect(configs[0].GUIDsRange.End).To(Equal("00:01:02:03:04:05:06:FF"))

		})
		It("should correctly decode a JSON configuration file with one field present", func() {
			mockJsonConfig := `[{"pciAddress":"0000:00:00.0"}]`

			configPath := createJsonConfig(mockJsonConfig)

			configs, err := readJSONConfig(configPath)
			Expect(err).NotTo(HaveOccurred())
			Expect(configs).To(HaveLen(1))
			Expect(configs[0].PciAddress).To(Equal("0000:00:00.0"))
		})
		It("should correctly decode a JSON array with several elements", func() {
			mockJsonConfig := `[{"pciAddress":"0000:00:00.0","guids":["00:01:02:03:04:05:06:07"]},{"pfGuid":"00:00:00:00:00:00:00:00","guidsRange":{"start":"00:01:02:03:04:05:06:08","end":"00:01:02:03:04:05:06:FF"}}]`

			configPath := createJsonConfig(mockJsonConfig)

			configs, err := readJSONConfig(configPath)
			Expect(err).NotTo(HaveOccurred())
			Expect(configs).To(HaveLen(2))
			Expect(configs[0].PciAddress).To(Equal("0000:00:00.0"))
			Expect(configs[1].PfGUID).To(Equal("00:00:00:00:00:00:00:00"))
			Expect(configs[1].GUIDsRange.Start).To(Equal("00:01:02:03:04:05:06:08"))
			Expect(configs[1].GUIDsRange.End).To(Equal("00:01:02:03:04:05:06:FF"))
		})
		It("should fail on a non-array JSON", func() {
			mockJsonConfig := `{"pciAddress":"0000:00:00.0", "newField": "newValue"}`

			configPath := createJsonConfig(mockJsonConfig)

			_, err := readJSONConfig(configPath)
			Expect(err).To(HaveOccurred())
		})
	})

	Describe("getPfPciAddressFromRawConfig", func() {
		var (
			networkHelper types.NetworkInterface
		)
		BeforeEach(func() {
			networkHelper = network.New(nil, nil, nil, nil)
		})
		It("should return same pci address when pci address is provided", func() {
			pci, err := getPfPciAddressFromRawConfig(ibPfGUIDJSONConfig{PciAddress: "pciAddress"}, nil, networkHelper)
			Expect(err).NotTo(HaveOccurred())
			Expect(pci).To(Equal("pciAddress"))
		})
		It("should find correct pci address when pf guid is given", func() {
			pfGuid := generateRandomGUID()

			testCtrl := gomock.NewController(GinkgoT())
			pfLinkMock := netlinkMockPkg.NewMockLink(testCtrl)
			pfLinkMock.EXPECT().Attrs().Return(&netlink.LinkAttrs{Name: "ib216s0f0", HardwareAddr: pfGuid}).Times(2)

			helpers.GinkgoConfigureFakeFS(&fakefilesystem.FS{
				Dirs:     []string{"/sys/bus/pci/0000:3b:00.0", "/sys/class/net/ib216s0f0"},
				Symlinks: map[string]string{"/sys/class/net/ib216s0f0/device": "/sys/bus/pci/0000:3b:00.0"},
			})

			pci, err := getPfPciAddressFromRawConfig(ibPfGUIDJSONConfig{PfGUID: pfGuid.String()}, []netlinkLibPkg.Link{pfLinkMock}, networkHelper)
			Expect(err).NotTo(HaveOccurred())
			Expect(pci).To(Equal("0000:3b:00.0"))

			testCtrl.Finish()
		})
		It("should return an error when no matching link is found", func() {
			pfGuidDesired := generateRandomGUID()
			pfGuidActual := generateRandomGUID()

			testCtrl := gomock.NewController(GinkgoT())
			pfLinkMock := netlinkMockPkg.NewMockLink(testCtrl)
			pfLinkMock.EXPECT().Attrs().Return(&netlink.LinkAttrs{Name: "ib216s0f0", HardwareAddr: pfGuidActual}).Times(1)

			helpers.GinkgoConfigureFakeFS(&fakefilesystem.FS{
				Dirs:     []string{"/sys/bus/pci/0000:3b:00.0", "/sys/class/net/ib216s0f0"},
				Symlinks: map[string]string{"/sys/class/net/ib216s0f0/device": "/sys/bus/pci/0000:3b:00.0"},
			})

			_, err := getPfPciAddressFromRawConfig(ibPfGUIDJSONConfig{PfGUID: pfGuidDesired.String()}, []netlinkLibPkg.Link{pfLinkMock}, networkHelper)
			Expect(err).To(HaveOccurred())
			Expect(err).To(MatchError(MatchRegexp(`no matching link found for pf guid:.*`)))

			testCtrl.Finish()
		})
		It("should return an error when too many parameters are provided", func() {
			_, err := getPfPciAddressFromRawConfig(ibPfGUIDJSONConfig{PfGUID: "pfGuid", PciAddress: "pciAddress"}, nil, networkHelper)
			Expect(err).To(HaveOccurred())
			Expect(err).To(MatchError("either PCI address or PF GUID required to describe an interface, both provided"))
		})
		It("should return an error when too few parameters are provided", func() {
			_, err := getPfPciAddressFromRawConfig(ibPfGUIDJSONConfig{}, nil, networkHelper)
			Expect(err).To(HaveOccurred())
			Expect(err).To(MatchError("either PCI address or PF GUID required to describe an interface, none provided"))
		})
	})

	Describe("getIbGUIDConfig", func() {
		Describe("Tests without common mocks", func() {
			It("should parse correct json config and return a map", func() {
				pfGuid, _ := ParseGUID(generateRandomGUID().String())
				vfGuid1, _ := ParseGUID(generateRandomGUID().String())
				vfGuid2, _ := ParseGUID(generateRandomGUID().String())
				rangeStart, err := ParseGUID("00:01:02:03:04:05:06:08")
				Expect(err).NotTo(HaveOccurred())
				rangeEnd, err := ParseGUID("00:01:02:03:04:05:06:FF")
				Expect(err).NotTo(HaveOccurred())

				configPath := "/config.json"
				configStr := fmt.Sprintf(`[{"pciAddress":"0000:3b:00.1","guids":["%s", "%s"]},{"pfGuid":"%s","guidsRange":{"start":"%s","end":"%s"}}]`, vfGuid1.String(), vfGuid2.String(), pfGuid.String(), rangeStart.String(), rangeEnd.String())

				helpers.GinkgoConfigureFakeFS(&fakefilesystem.FS{
					Dirs:     []string{"/sys/bus/pci/0000:3b:00.0", "/sys/class/net/ib216s0f0", "/host"},
					Symlinks: map[string]string{"/sys/class/net/ib216s0f0/device": "/sys/bus/pci/0000:3b:00.0"},
					Files:    map[string][]byte{"/host" + configPath: []byte(configStr)},
				})

				testCtrl := gomock.NewController(GinkgoT())

				pfLinkMock := netlinkMockPkg.NewMockLink(testCtrl)
				pfLinkMock.EXPECT().Attrs().Return(&netlink.LinkAttrs{Name: "ib216s0f0", HardwareAddr: pfGuid.HardwareAddr()}).Times(2)

				netlinkLibMock := netlinkMockPkg.NewMockNetlinkLib(testCtrl)
				netlinkLibMock.EXPECT().LinkList().Return([]netlinkLibPkg.Link{pfLinkMock}, nil).Times(1)

				networkHelper := network.New(nil, nil, nil, nil)

				config, err := getIbGUIDConfig(configPath, netlinkLibMock, networkHelper)
				Expect(err).NotTo(HaveOccurred())
				Expect(config["0000:3b:00.1"].GUIDs[0]).To(Equal(vfGuid1))
				Expect(config["0000:3b:00.1"].GUIDs[1]).To(Equal(vfGuid2))
				Expect(config["0000:3b:00.0"].GUIDRange).To(Not(BeNil()))
				Expect(config["0000:3b:00.0"].GUIDRange.Start).To(Equal(rangeStart))
				Expect(config["0000:3b:00.0"].GUIDRange.End).To(Equal(rangeEnd))

				testCtrl.Finish()
			})
		})

		Describe("Tests with common mocks", func() {
			var (
				netlinkLibMock *netlinkMockPkg.MockNetlinkLib
				testCtrl       *gomock.Controller

				createJsonConfig func(string) string

				networkHelper types.NetworkInterface
			)

			BeforeEach(func() {
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

				networkHelper = network.New(nil, nil, nil, nil)
			})

			AfterEach(func() {
				testCtrl.Finish()
			})

			It("should return an error when invalid json config is provided", func() {
				configPath := createJsonConfig(`[invalid file]`)

				_, err := getIbGUIDConfig(configPath, netlinkLibMock, networkHelper)
				Expect(err).To(HaveOccurred())
				Expect(err).To(MatchError(MatchRegexp("failed to unmarshal content of ib guid config.*")))
			})
			It("should return an error when failed to determine pf's pci address", func() {
				configPath := createJsonConfig(`[{"guids":["00:01:02:03:04:05:06:07"]}]`)

				_, err := getIbGUIDConfig(configPath, netlinkLibMock, networkHelper)
				Expect(err).To(HaveOccurred())
				Expect(err).To(MatchError(MatchRegexp("failed to extract pci address from ib guid config.*")))
			})
			It("should return an error when both guids and rangeStart are provided", func() {
				configPath := createJsonConfig(`[{"pciAddress": "someaddress", "guids":["00:01:02:03:04:05:06:07"], "guidsRange":{"start": "00:01:02:03:04:05:06:AA"}}]`)

				_, err := getIbGUIDConfig(configPath, netlinkLibMock, networkHelper)
				Expect(err).To(HaveOccurred())
				Expect(err).To(MatchError(MatchRegexp("either guid list or guid range should be provided, got both.*")))
			})
			It("should return an error when both guids and rangeEnd are provided", func() {
				configPath := createJsonConfig(`[{"pciAddress": "someaddress", "guids":["00:01:02:03:04:05:06:07"], "guidsRange":{"end": "00:01:02:03:04:05:06:AA"}}]`)

				_, err := getIbGUIDConfig(configPath, netlinkLibMock, networkHelper)
				Expect(err).To(HaveOccurred())
				Expect(err).To(MatchError(MatchRegexp("either guid list or guid range should be provided, got both.*")))
			})
			It("should return an error when neither guids nor range are provided", func() {
				configPath := createJsonConfig(`[{"pciAddress": "someaddress"}]`)

				_, err := getIbGUIDConfig(configPath, netlinkLibMock, networkHelper)
				Expect(err).To(HaveOccurred())
				Expect(err).To(MatchError(MatchRegexp("either guid list or guid range should be provided, got none.*")))
			})
			It("should return an error when invalid guid list is provided", func() {
				configPath := createJsonConfig(`[{"pciAddress": "someaddress", "guids":["invalid_guid"]}]`)

				_, err := getIbGUIDConfig(configPath, netlinkLibMock, networkHelper)
				Expect(err).To(HaveOccurred())
				Expect(err).To(MatchError(MatchRegexp("failed to parse ib guid invalid_guid.*")))
			})
			It("should return an error when invalid guid range start is provided", func() {
				configPath := createJsonConfig(`[{"pciAddress": "someaddress", "guidsRange": {"start":"invalid range start", "end":"00:01:02:03:04:05:06:FF"}}]`)

				_, err := getIbGUIDConfig(configPath, netlinkLibMock, networkHelper)
				Expect(err).To(HaveOccurred())
				Expect(err).To(MatchError(MatchRegexp("failed to parse ib guid range start.*")))
			})
			It("should return an error when invalid guid range end is provided", func() {
				configPath := createJsonConfig(`[{"pciAddress": "someaddress", "guidsRange": {"start":"00:01:02:03:04:05:06:08", "end":"invalid range end"}}]`)

				_, err := getIbGUIDConfig(configPath, netlinkLibMock, networkHelper)
				Expect(err).To(HaveOccurred())
				Expect(err).To(MatchError(MatchRegexp("failed to parse ib guid range end.*")))
			})
			It("should return an error when guid range end is less than range start", func() {
				configPath := createJsonConfig(`[{"pciAddress": "someaddress", "guidsRange": {"start":"00:01:02:03:04:05:06:FF", "end":"00:01:02:03:04:05:06:AA"}}]`)

				_, err := getIbGUIDConfig(configPath, netlinkLibMock, networkHelper)
				Expect(err).To(HaveOccurred())
				Expect(err).To(MatchError(MatchRegexp("range end cannot be less then range start.*")))
			})
		})
	})
})
