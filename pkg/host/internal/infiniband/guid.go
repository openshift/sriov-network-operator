package infiniband

import (
	"fmt"
	"math/rand"
	"net"
)

// GUID address is an uint64 encapsulation for network hardware address
type GUID uint64

const (
	guidLength = 8
	byteBitLen = 8
	byteMask   = 0xff
)

// ParseGUID parses string only as GUID 64 bit
func ParseGUID(s string) (GUID, error) {
	ha, err := net.ParseMAC(s)
	if err != nil {
		return 0, err
	}
	if len(ha) != guidLength {
		return 0, fmt.Errorf("invalid GUID address %s", s)
	}
	var guid uint64
	for idx, octet := range ha {
		guid |= uint64(octet) << uint(byteBitLen*(guidLength-1-idx))
	}
	return GUID(guid), nil
}

// String returns the string representation of GUID
func (g GUID) String() string {
	return g.HardwareAddr().String()
}

// HardwareAddr returns GUID representation as net.HardwareAddr
func (g GUID) HardwareAddr() net.HardwareAddr {
	value := uint64(g)
	ha := make(net.HardwareAddr, guidLength)
	for idx := guidLength - 1; idx >= 0; idx-- {
		ha[idx] = byte(value & byteMask)
		value >>= byteBitLen
	}

	return ha
}

func generateRandomGUID() net.HardwareAddr {
	guid := make(net.HardwareAddr, 8)

	// First field is 0x01 - xfe to avoid all zero and all F invalid guids
	guid[0] = byte(1 + rand.Intn(0xfe))

	for i := 1; i < len(guid); i++ {
		guid[i] = byte(rand.Intn(0x100))
	}

	return guid
}
