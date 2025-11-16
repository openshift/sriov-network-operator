package systemd

import (
	"os"
	"path"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	sriovnetworkv1 "github.com/k8snetworkplumbingwg/sriov-network-operator/api/v1"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/consts"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/host/types"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/utils"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/vars"
)

var _ = Describe("Systemd", func() {
	var (
		tempDir              = "/tmp/sriov-systemd-test/"
		validTestContentJson = []byte(`{
  "spec": {
    "interfaces": [
      {
        "name": "enp216s0f0np0",
        "pciAddress": "0000:d8:00.0",
        "numVfs": 1,
        "linkType": "IB",
        "vfGroups": [
          {
            "vfRange": "0-0",
            "resourceName": "test-resource0"
          }
        ]
      }
    ],
    "policyName": "test-policy0"
  },
  "mtu": 2000,
  "isRdma": true,
  "unsupportedNics": false,
  "platformType": "Baremetal",
  "manageSoftwareBridges": false,
  "ovsdbSocketPath": ""
}`)

		validTestContentYaml = []byte(`spec:
  interfaces:
    - name: "enp216s0f0np0"
      pciAddress: "0000:d8:00.0"
      numVfs: 1
      linkType: "IB"
      vfGroups:
        - vfRange: "0-0"
          resourceName: "test-resource0"
          policyName: "test-policy0"
      mtu: 2000
      isRdma: true
unsupportedNics: false
platformType: "Baremetal"
manageSoftwareBridges: false
ovsdbSocketPath: ""`)

		testNodeStateData = &sriovnetworkv1.SriovNetworkNodeState{
			Spec: sriovnetworkv1.SriovNetworkNodeStateSpec{
				Interfaces: []sriovnetworkv1.Interface{
					{
						Name:       "enp216s0f0np0",
						PciAddress: "0000:d8:00.0",
						NumVfs:     1,
						LinkType:   "IB",
						VfGroups: []sriovnetworkv1.VfGroup{
							{
								VfRange:      "0-0",
								ResourceName: "test-resource0",
							},
						},
					},
				},
			},
		}

		resultData      = &types.SriovResult{LastSyncError: "", SyncStatus: consts.SyncStatusSucceeded}
		validResultData = []byte(`syncStatus: Succeeded
lastSyncError: ""`)

		testSriovSupportedNicIDs = []byte(`8086 1583 154c
8086 0d58 154c
8086 10c9 10ca
`)

		s types.SystemdInterface
	)

	BeforeEach(func() {
		err := os.MkdirAll(path.Join(tempDir, consts.SriovConfBasePath), 0777)
		Expect(err).ToNot(HaveOccurred())

		err = os.MkdirAll(path.Join(tempDir, consts.SriovServiceBasePath), 0777)
		Expect(err).ToNot(HaveOccurred())

		err = os.MkdirAll(path.Join(tempDir, "/var/lib/sriov/"), 0777)
		Expect(err).ToNot(HaveOccurred())

		vars.InChroot = true
		vars.FilesystemRoot = tempDir
		vars.PlatformType = consts.Baremetal
		sriovnetworkv1.NicIDMap = []string{}

		s = &systemd{}
	})

	AfterEach(func() {
		err := os.RemoveAll(tempDir)
		Expect(err).ToNot(HaveOccurred())
	})

	Context("ReadConfFile", func() {
		It("should read the content of the file as a json", func() {
			err := os.WriteFile(path.Join(tempDir, consts.SriovSystemdConfigPath), validTestContentJson, 0644)
			Expect(err).ToNot(HaveOccurred())

			sr, err := s.ReadConfFile()
			Expect(err).ToNot(HaveOccurred())
			Expect(len(sr.Spec.Interfaces)).To(Equal(1))
		})

		It("should read the content of the file as a yaml", func() {
			err := os.WriteFile(path.Join(tempDir, consts.SriovSystemdConfigPath), validTestContentYaml, 0644)
			Expect(err).ToNot(HaveOccurred())

			sr, err := s.ReadConfFile()
			Expect(err).ToNot(HaveOccurred())
			Expect(len(sr.Spec.Interfaces)).To(Equal(1))
		})

		It("should return error if the file doesn't exist ", func() {
			_, err := s.ReadConfFile()
			Expect(err).To(HaveOccurred())
		})

		It("should return error if not able to parse the content of the file as a yaml", func() {
			err := os.WriteFile(path.Join(tempDir, consts.SriovSystemdConfigPath), []byte("test"), 0644)
			Expect(err).ToNot(HaveOccurred())

			_, err = s.ReadConfFile()
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(Equal("yaml: unmarshal errors:\n  line 1: cannot unmarshal !!str `test` into types.SriovConfig"))
		})
	})

	Context("WriteConfFile", func() {
		It("should create the sriov-operator folder if it doesn't exist", func() {
			err := os.Remove(path.Join(tempDir, consts.SriovConfBasePath))
			Expect(err).ToNot(HaveOccurred())

			updated, err := s.WriteConfFile(testNodeStateData)
			Expect(err).ToNot(HaveOccurred())
			Expect(updated).To(BeTrue())
			_, err = os.Stat(path.Join(tempDir, consts.SriovSystemdConfigPath))
			Expect(err).ToNot(HaveOccurred())
		})

		It("should return false if the file was not updated", func() {
			updated, err := s.WriteConfFile(testNodeStateData)
			Expect(err).ToNot(HaveOccurred())
			Expect(updated).To(BeTrue())
			updated, err = s.WriteConfFile(testNodeStateData)
			Expect(err).ToNot(HaveOccurred())
			Expect(updated).To(BeFalse())
		})

		It("should write the content of the file as a yaml", func() {
			updated, err := s.WriteConfFile(testNodeStateData)
			Expect(err).ToNot(HaveOccurred())
			Expect(updated).To(BeTrue())

			content, err := s.ReadConfFile()
			Expect(err).ToNot(HaveOccurred())
			Expect(content.PlatformType).To(Equal(consts.Baremetal))

			_, err = os.Stat(utils.GetHostExtensionPath(consts.SriovSystemdConfigPath))
			Expect(err).ToNot(HaveOccurred())
		})

		It("should write the content of the file as a yaml and return false if the file didn't exist and no interfaces exist", func() {
			testNodeStateData := &sriovnetworkv1.SriovNetworkNodeState{
				Spec: sriovnetworkv1.SriovNetworkNodeStateSpec{},
			}

			updated, err := s.WriteConfFile(testNodeStateData)
			Expect(err).ToNot(HaveOccurred())
			Expect(updated).To(BeFalse())

			content, err := s.ReadConfFile()
			Expect(err).ToNot(HaveOccurred())
			Expect(content.PlatformType).To(Equal(consts.Baremetal))

			_, err = os.Stat(utils.GetHostExtensionPath(consts.SriovSystemdConfigPath))
			Expect(err).ToNot(HaveOccurred())
		})

		It("should update the existing config file", func() {
			emptyTestNodeStateData := &sriovnetworkv1.SriovNetworkNodeState{
				Spec: sriovnetworkv1.SriovNetworkNodeStateSpec{},
			}

			updated, err := s.WriteConfFile(emptyTestNodeStateData)
			Expect(err).ToNot(HaveOccurred())
			Expect(updated).To(BeFalse())

			content, err := s.ReadConfFile()
			Expect(err).ToNot(HaveOccurred())
			Expect(len(content.Spec.Interfaces)).To(Equal(0))

			updated, err = s.WriteConfFile(testNodeStateData)
			Expect(err).ToNot(HaveOccurred())
			Expect(updated).To(BeTrue())

			content, err = s.ReadConfFile()
			Expect(err).ToNot(HaveOccurred())
			Expect(len(content.Spec.Interfaces)).To(Equal(1))
		})
	})

	Context("WriteSriovResult", func() {
		It("should write the result file", func() {
			err := s.WriteSriovResult(resultData)
			Expect(err).ToNot(HaveOccurred())

			_, err = os.Stat(utils.GetHostExtensionPath(consts.SriovSystemdResultPath))
			Expect(err).ToNot(HaveOccurred())

			result, err := s.ReadSriovResult()
			Expect(err).ToNot(HaveOccurred())
			Expect(result.SyncStatus).To(Equal(resultData.SyncStatus))
		})

		It("should create the folder if doesn't exist", func() {
			err := os.Remove(path.Join(tempDir, consts.SriovConfBasePath))
			Expect(err).ToNot(HaveOccurred())

			err = s.WriteSriovResult(resultData)
			Expect(err).ToNot(HaveOccurred())

			result, err := s.ReadSriovResult()
			Expect(err).ToNot(HaveOccurred())
			Expect(result.SyncStatus).To(Equal(resultData.SyncStatus))
		})
	})

	Context("ReadSriovResult", func() {
		It("should read the content of the file as a yaml", func() {
			err := os.WriteFile(path.Join(tempDir, consts.SriovSystemdResultPath), validResultData, 0644)
			Expect(err).ToNot(HaveOccurred())

			sr, err := s.ReadSriovResult()
			Expect(err).ToNot(HaveOccurred())
			Expect(sr.SyncStatus).To(Equal(consts.SyncStatusSucceeded))
		})

		It("should return error if not able to parse the content of the file as a yaml", func() {
			err := os.WriteFile(path.Join(tempDir, consts.SriovSystemdResultPath), []byte("test"), 0644)
			Expect(err).ToNot(HaveOccurred())

			_, err = s.ReadSriovResult()
			Expect(err).To(HaveOccurred())
		})
	})

	Context("RemoveSriovResult", func() {
		It("should not return an error if the file doesn't exist", func() {
			err := s.RemoveSriovResult()
			Expect(err).ToNot(HaveOccurred())
		})

		It("should not return", func() {
			err := os.WriteFile(path.Join(tempDir, consts.SriovSystemdResultPath), []byte("test"), 0644)
			Expect(err).ToNot(HaveOccurred())

			err = s.RemoveSriovResult()
			Expect(err).ToNot(HaveOccurred())
		})
	})

	Context("WriteSriovSupportedNics", func() {
		It("should write the nic map", func() {
			sriovnetworkv1.NicIDMap = []string{"test", "test1"}
			err := s.WriteSriovSupportedNics()
			Expect(err).ToNot(HaveOccurred())

			_, err = os.Stat(utils.GetHostExtensionPath(consts.SriovSystemdSupportedNicPath))
			Expect(err).ToNot(HaveOccurred())

			result, err := s.ReadSriovSupportedNics()
			Expect(err).ToNot(HaveOccurred())
			Expect(len(result)).To(Equal(3))
			Expect(result[0]).To(Equal("test"))
		})

		It("should create the folder if doesn't exist", func() {
			err := os.Remove(path.Join(tempDir, consts.SriovConfBasePath))
			Expect(err).ToNot(HaveOccurred())

			err = s.WriteSriovSupportedNics()
			Expect(err).ToNot(HaveOccurred())

			result, err := s.ReadSriovSupportedNics()
			Expect(err).ToNot(HaveOccurred())
			Expect(len(result)).To(Equal(1))
			Expect(result[0]).To(Equal(""))
		})
	})

	Context("ReadSriovSupportedNics", func() {
		It("should read the content of the file", func() {
			err := os.WriteFile(path.Join(tempDir, consts.SriovSystemdSupportedNicPath), testSriovSupportedNicIDs, 0644)
			Expect(err).ToNot(HaveOccurred())

			sr, err := s.ReadSriovSupportedNics()
			Expect(err).ToNot(HaveOccurred())
			Expect(len(sr)).To(Equal(4))
			Expect(sr[0]).To(Equal("8086 1583 154c"))
		})

		It("should return error if the files doesn't exist", func() {
			_, err := s.ReadSriovSupportedNics()
			Expect(err).To(HaveOccurred())
		})
	})

	Context("CleanSriovFilesFromHost", func() {
		It("should remove the files that exist", func() {
			err := os.WriteFile(path.Join(tempDir, consts.SriovSystemdConfigPath), []byte("test"), 0644)
			Expect(err).ToNot(HaveOccurred())

			err = os.WriteFile(path.Join(tempDir, consts.SriovSystemdResultPath), []byte("test"), 0644)
			Expect(err).ToNot(HaveOccurred())

			err = os.WriteFile(path.Join(tempDir, consts.SriovSystemdSupportedNicPath), []byte("test"), 0644)
			Expect(err).ToNot(HaveOccurred())

			err = os.WriteFile(path.Join(tempDir, consts.SriovSystemdServiceBinaryPath), []byte("test"), 0644)
			Expect(err).ToNot(HaveOccurred())

			err = os.WriteFile(path.Join(tempDir, consts.SriovSystemdSupportedNicPath), []byte("test"), 0644)
			Expect(err).ToNot(HaveOccurred())

			err = os.WriteFile(path.Join(tempDir, consts.SriovServicePath), []byte("test"), 0644)
			Expect(err).ToNot(HaveOccurred())

			err = os.WriteFile(path.Join(tempDir, consts.SriovPostNetworkServicePath), []byte("test"), 0644)
			Expect(err).ToNot(HaveOccurred())

			err = s.CleanSriovFilesFromHost(false)
			Expect(err).ToNot(HaveOccurred())
		})

		It("should not return error if the files don't exist", func() {
			err := s.CleanSriovFilesFromHost(false)
			Expect(err).ToNot(HaveOccurred())
		})
	})
})
