package utils

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/jaypipes/ghw"
	"github.com/jaypipes/ghw/pkg/net"
	"github.com/jaypipes/ghw/pkg/option"
	"k8s.io/utils/pointer"
)

func TestUtilsVirtual(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Utils")
}

var _ = Describe("Virtual", func() {

	Context("GetOpenstackData", func() {
		It("PCI address replacement based on MAC address", func() {
			ospNetworkDataFile = "./testdata/network_data.json"
			ospMetaDataFile = "./testdata/meta_data.json"
			DeferCleanup(func() {
				ospNetworkDataFile = ospMetaDataDir + "/network_data.json"
				ospMetaDataFile = ospMetaDataDir + "/meta_data.json"
			})

			ghw.Network = func(opts ...*option.Option) (*net.Info, error) {
				return &net.Info{
					NICs: []*net.NIC{{
						MacAddress: "fa:16:3e:00:00:00",
						PCIAddress: pointer.String("0000:04:00.0"),
					}, {
						MacAddress: "fa:16:3e:11:11:11",
						PCIAddress: pointer.String("0000:99:99.9"),
					}},
				}, nil
			}

			DeferCleanup(func() {
				ghw.Network = net.New
			})

			metaData, _, err := GetOpenstackData(false)
			Expect(err).ToNot(HaveOccurred())

			Expect(metaData.Devices).To(HaveLen(2))
			Expect(metaData.Devices[0].Mac).To(Equal("fa:16:3e:00:00:00"))
			Expect(metaData.Devices[0].Address).To(Equal("0000:04:00.0"))
			Expect(metaData.Devices[1].Mac).To(Equal("fa:16:3e:11:11:11"))
			Expect(metaData.Devices[1].Address).To(Equal("0000:99:99.9"))

		})
	})
})
