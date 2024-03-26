package infiniband

import (
	"net"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("GUID", func() {
	It("should parse and process GUIDs correctly", func() {
		guidStr := "00:01:02:03:04:05:06:08"
		nextGuidStr := "00:01:02:03:04:05:06:09"

		guid, err := ParseGUID(guidStr)
		Expect(err).NotTo(HaveOccurred())

		Expect(guid.String()).To(Equal(guidStr))
		Expect((guid + 1).String()).To(Equal(nextGuidStr))
	})
	It("should represent GUID as HW address", func() {
		guidStr := "00:01:02:03:04:05:06:08"

		guid, err := ParseGUID(guidStr)
		Expect(err).NotTo(HaveOccurred())

		Expect(guid.HardwareAddr()).To(Equal(net.HardwareAddr{0x00, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x08}))
	})
})
