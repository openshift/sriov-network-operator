package store

import (
	"os"
	"path"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	sriovnetworkv1 "github.com/k8snetworkplumbingwg/sriov-network-operator/api/v1"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/consts"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/utils"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/vars"
)

var _ = Describe("Store", func() {
	var (
		tempDir = "/tmp/sriov-test/"
		err     error
		m       ManagerInterface

		testInterface = &sriovnetworkv1.Interface{
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
		}

		testInterfaceData = []byte(`{
  "name": "enp216s0f0np0",
  "pciAddress": "0000:d8:00.0",
  "numVfs": 1,
  "linkType": "IB",
  "vfGroups": [
    {
      "vfRange": "0-0",
      "resourceName": "test-resource0",
      "policyName": "test-policy0"
    }
  ],
  "mtu": 2000
}`)

		testNodeState = &sriovnetworkv1.SriovNetworkNodeState{ObjectMeta: metav1.ObjectMeta{
			Name: "worker-0",
		},
			Status: sriovnetworkv1.SriovNetworkNodeStateStatus{
				Interfaces: sriovnetworkv1.InterfaceExts{
					{
						Name:       "eno1np0",
						PciAddress: "0000:8d:00.0",
					},
				},
			},
		}

		testNodeStateData = []byte(`{
  "apiVersion": "sriovnetwork.openshift.io/v1",
  "kind": "SriovNetworkNodeState",
  "metadata": {
    "annotations": {
      "sriovnetwork.openshift.io/current-state": "Idle",
      "sriovnetwork.openshift.io/desired-state": "Idle"
    },
    "creationTimestamp": "2024-10-10T13:40:32Z",
    "generation": 1,
    "name": "worker-0",
    "namespace": "sriov-network-operator",
    "ownerReferences": [
      {
        "apiVersion": "sriovnetwork.openshift.io/v1",
        "blockOwnerDeletion": true,
        "controller": true,
        "kind": "SriovOperatorConfig",
        "name": "default",
        "uid": "4bb72ddd-82be-4a8a-a91b-1cb24f500f77"
      }
    ],
    "resourceVersion": "96479400",
    "uid": "0d11adf3-dd52-4906-a15c-87bf6271c946"
  },
  "spec": {
    "bridges": {},
    "system": {}
  },
  "status": {
    "bridges": {},
    "interfaces": [
      {
        "deviceID": "1015",
        "driver": "mlx5_core",
        "linkAdminState": "up",
        "linkSpeed": "10000 Mb/s",
        "linkType": "ETH",
        "mac": "0c:42:a1:6c:ac:9c",
        "mtu": 1500,
        "name": "eno1np0",
        "pciAddress": "0000:8d:00.0",
        "vendor": "15b3"
      }
    ],
    "syncStatus": "Succeeded",
    "system": {
      "rdmaMode": "shared"
    }
  }
}`)
	)

	BeforeEach(func() {
		vars.InChroot = true
		vars.FilesystemRoot = tempDir
		vars.Destdir = tempDir
		sriovnetworkv1.NicIDMap = []string{}

		m, err = NewManager()
		Expect(err).ToNot(HaveOccurred())
	})

	AfterEach(func() {
		err := os.RemoveAll(tempDir)
		Expect(err).ToNot(HaveOccurred())
	})

	Context("NewManager", func() {
		It("should not return error if it's able to create the folder", func() {
			_, err := NewManager()
			Expect(err).ToNot(HaveOccurred())
		})
	})

	Context("createOperatorConfigFolderIfNeeded", func() {
		It("should have the folder created after getting an instance of the store manager", func() {
			_, err = os.Stat(utils.GetHostExtensionPath(consts.SriovConfBasePath))
			Expect(err).ToNot(HaveOccurred())

			_, err = os.Stat(utils.GetHostExtensionPath(consts.PfAppliedConfig))
			Expect(err).ToNot(HaveOccurred())
		})
	})

	Context("ClearPCIAddressFolder", func() {
		It("should return without error if the folder doesn't exist", func() {
			err = os.RemoveAll(utils.GetHostExtensionPath(consts.PfAppliedConfig))
			Expect(err).ToNot(HaveOccurred())

			err = m.ClearPCIAddressFolder()
			Expect(err).ToNot(HaveOccurred())
		})

		It("should remove the folder and recreate an empty new one", func() {
			err := os.WriteFile(path.Join(utils.GetHostExtensionPath(consts.PfAppliedConfig), "test"), []byte("test"), 0644)
			Expect(err).ToNot(HaveOccurred())

			err = m.ClearPCIAddressFolder()
			Expect(err).ToNot(HaveOccurred())

			_, err = os.Stat(path.Join(utils.GetHostExtensionPath(consts.PfAppliedConfig), "test"))
			Expect(err).To(HaveOccurred())
			Expect(os.IsNotExist(err)).To(BeTrue())
		})
	})

	Context("SaveLastPfAppliedStatus", func() {
		It("should failed to write the file if path doesn't exist", func() {
			err = os.RemoveAll(utils.GetHostExtensionPath(consts.PfAppliedConfig))
			Expect(err).ToNot(HaveOccurred())

			err = m.SaveLastPfAppliedStatus(testInterface)
			Expect(err).To(HaveOccurred())
		})

		It("should write the file", func() {
			err = m.SaveLastPfAppliedStatus(testInterface)
			Expect(err).ToNot(HaveOccurred())

			_, err = os.Stat(path.Join(utils.GetHostExtensionPath(consts.PfAppliedConfig), "0000:d8:00.0"))
			Expect(err).ToNot(HaveOccurred())
		})
	})

	Context("RemovePfAppliedStatus", func() {
		It("should remove the file", func() {
			err = m.SaveLastPfAppliedStatus(testInterface)
			Expect(err).ToNot(HaveOccurred())

			err = m.RemovePfAppliedStatus("0000:d8:00.0")
			Expect(err).ToNot(HaveOccurred())

			_, err = os.Stat(path.Join(utils.GetHostExtensionPath(consts.PfAppliedConfig), "0000:d8:00.0"))
			Expect(err).To(HaveOccurred())
			Expect(os.IsNotExist(err)).To(BeTrue())
		})

		It("should not return error if the file doesn't exist", func() {
			err = m.RemovePfAppliedStatus("0000:d8:00.0")
			Expect(err).ToNot(HaveOccurred())
		})
	})

	Context("LoadPfsStatus", func() {
		It("should return error if not able to parse the file", func() {
			err = os.WriteFile(path.Join(utils.GetHostExtensionPath(consts.PfAppliedConfig), "0000:d8:00.0"), []byte("test"), 0644)
			Expect(err).ToNot(HaveOccurred())

			_, _, err = m.LoadPfsStatus("0000:d8:00.0")
			Expect(err).To(HaveOccurred())
		})

		It("should not return error if the file doesn't exist and false", func() {
			_, exist, err := m.LoadPfsStatus("0000:d8:00.0")
			Expect(err).ToNot(HaveOccurred())
			Expect(exist).To(BeFalse())
		})

		It("should return a valid PFStatus", func() {
			err = os.WriteFile(path.Join(utils.GetHostExtensionPath(consts.PfAppliedConfig), "0000:d8:00.0"), testInterfaceData, 0644)
			Expect(err).ToNot(HaveOccurred())

			loaded, exist, err := m.LoadPfsStatus("0000:d8:00.0")
			Expect(err).ToNot(HaveOccurred())
			Expect(exist).To(BeTrue())
			Expect(loaded.PciAddress).To(Equal(testInterface.PciAddress))
			Expect(loaded.Name).To(Equal(testInterface.Name))
		})
	})

	Context("GetCheckPointNodeState", func() {
		It("should return not error and return empty struct if file doesn't exist", func() {
			ns, err := m.GetCheckPointNodeState()
			Expect(err).ToNot(HaveOccurred())
			Expect(ns).To(BeNil())
		})

		It("should return not error if not able to parse the file", func() {
			err = os.WriteFile(path.Join(vars.Destdir, consts.CheckpointFileName), []byte("test"), 0644)
			Expect(err).ToNot(HaveOccurred())

			_, err = m.GetCheckPointNodeState()
			Expect(err).To(HaveOccurred())
		})

		It("should be able to decode the file and also save a global variable", func() {
			Expect(sriovnetworkv1.InitialState.Name).To(Equal(""))
			err = os.WriteFile(path.Join(vars.Destdir, consts.CheckpointFileName), testNodeStateData, 0644)
			Expect(err).ToNot(HaveOccurred())

			ns, err := m.GetCheckPointNodeState()
			Expect(err).ToNot(HaveOccurred())
			Expect(ns).ToNot(BeNil())
			Expect(sriovnetworkv1.InitialState.Name).To(Equal("worker-0"))
			Expect(equality.Semantic.DeepEqual(*ns, sriovnetworkv1.InitialState)).To(BeTrue())
		})
	})

	Context("WriteCheckpointFile", func() {
		It("should return error if the path doesn't exist", func() {
			vars.Destdir = path.Join(vars.Destdir, "not-exist")
			defer func() {
				vars.Destdir = vars.FilesystemRoot
			}()

			err = m.WriteCheckpointFile(testNodeState)
			Expect(err).To(HaveOccurred())
		})

		It("should write the file and return no error", func() {
			err = m.WriteCheckpointFile(testNodeState)
			Expect(err).ToNot(HaveOccurred())

			_, err = os.Stat(path.Join(vars.Destdir, consts.CheckpointFileName))
			Expect(err).ToNot(HaveOccurred())
		})

		It("should not override the file if it already exist", func() {
			_, err = os.Stat(path.Join(vars.Destdir, consts.CheckpointFileName))
			Expect(err).To(HaveOccurred())
			Expect(os.IsNotExist(err)).To(BeTrue())

			err = m.WriteCheckpointFile(testNodeState)
			Expect(err).ToNot(HaveOccurred())

			_, err = os.Stat(path.Join(vars.Destdir, consts.CheckpointFileName))
			Expect(err).ToNot(HaveOccurred())

			copyTestNode := testNodeState.DeepCopy()
			copyTestNode.Name = "worker-1"
			err = m.WriteCheckpointFile(copyTestNode)
			Expect(err).ToNot(HaveOccurred())

			ns, err := m.GetCheckPointNodeState()
			Expect(err).ToNot(HaveOccurred())
			Expect(ns.Name).To(Equal("worker-0"))
		})

		It("should override the file if not able to decode it", func() {
			err = os.WriteFile(path.Join(vars.Destdir, consts.CheckpointFileName), []byte("test"), 0644)
			Expect(err).ToNot(HaveOccurred())

			err = m.WriteCheckpointFile(testNodeState)
			Expect(err).ToNot(HaveOccurred())

			ns, err := m.GetCheckPointNodeState()
			Expect(err).ToNot(HaveOccurred())
			Expect(ns.Name).To(Equal("worker-0"))
		})
	})
})
