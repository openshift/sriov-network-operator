package netlink

import (
	"net"

	"github.com/vishvananda/netlink"
)

func New() NetlinkLib {
	return &libWrapper{}
}

type Link interface {
	netlink.Link
}

//go:generate ../../../../../bin/mockgen -destination mock/mock_netlink.go -source netlink.go
type NetlinkLib interface {
	// LinkSetVfNodeGUID sets the node GUID of a vf for the link.
	// Equivalent to: `ip link set dev $link vf $vf node_guid $nodeguid`
	LinkSetVfNodeGUID(link Link, vf int, nodeguid net.HardwareAddr) error
	// LinkSetVfPortGUID sets the port GUID of a vf for the link.
	// Equivalent to: `ip link set dev $link vf $vf port_guid $portguid`
	LinkSetVfPortGUID(link Link, vf int, portguid net.HardwareAddr) error
	// LinkByName finds a link by name and returns a pointer to the object.
	LinkByName(name string) (Link, error)
	// LinkByIndex finds a link by index and returns a pointer to the object.
	LinkByIndex(index int) (Link, error)
	// LinkList gets a list of link devices.
	// Equivalent to: `ip link show`
	LinkList() ([]Link, error)
	// LinkSetVfHardwareAddr sets the hardware address of a vf for the link.
	// Equivalent to: `ip link set $link vf $vf mac $hwaddr`
	LinkSetVfHardwareAddr(link Link, vf int, hwaddr net.HardwareAddr) error
	// LinkSetUp enables the link device.
	// Equivalent to: `ip link set $link up`
	LinkSetUp(link Link) error
	// LinkSetMTU sets the mtu of the link device.
	// Equivalent to: `ip link set $link mtu $mtu`
	LinkSetMTU(link Link, mtu int) error
	// DevlinkGetDeviceByName provides a pointer to devlink device and nil error,
	// otherwise returns an error code.
	DevLinkGetDeviceByName(bus string, device string) (*netlink.DevlinkDevice, error)
	// DevLinkSetEswitchMode sets eswitch mode if able to set successfully or
	// returns an error code.
	// Equivalent to: `devlink dev eswitch set $dev mode switchdev`
	// Equivalent to: `devlink dev eswitch set $dev mode legacy`
	DevLinkSetEswitchMode(dev *netlink.DevlinkDevice, newMode string) error
	// VDPAGetDevByName returns VDPA device selected by name
	// Equivalent to: `vdpa dev show <name>`
	VDPAGetDevByName(name string) (*netlink.VDPADev, error)
	// VDPADelDev removes VDPA device
	// Equivalent to: `vdpa dev del <name>`
	VDPADelDev(name string) error
	// VDPANewDev adds new VDPA device
	// Equivalent to: `vdpa dev add name <name> mgmtdev <mgmtBus>/mgmtName [params]`
	VDPANewDev(name, mgmtBus, mgmtName string, params netlink.VDPANewDevParams) error
	// DevlinkGetDeviceParamByName returns specific parameter for devlink device
	// Equivalent to: `devlink dev param show <bus>/<device> name <param>`
	DevlinkGetDeviceParamByName(bus string, device string, param string) (*netlink.DevlinkParam, error)
	// DevlinkSetDeviceParam set specific parameter for devlink device
	// Equivalent to: `devlink dev param set <bus>/<device> name <param> cmode <cmode> value <value>`
	// cmode argument should contain valid cmode value as uint8, modes are define in nl.DEVLINK_PARAM_CMODE_* constants
	// value argument should have one of the following types: uint8, uint16, uint32, string, bool
	DevlinkSetDeviceParam(bus string, device string, param string, cmode uint8, value interface{}) error
	// RdmaLinkByName finds a link by name and returns a pointer to the object if
	// found and nil error, otherwise returns error code.
	RdmaLinkByName(name string) (*netlink.RdmaLink, error)
	// IsLinkAdminStateUp checks if the admin state of a link is up
	IsLinkAdminStateUp(link Link) bool
	// RdmaSystemGetNetnsMode returns RDMA subsystem mode
	RdmaSystemGetNetnsMode() (string, error)
}

type libWrapper struct{}

// LinkSetVfNodeGUID sets the node GUID of a vf for the link.
// Equivalent to: `ip link set dev $link vf $vf node_guid $nodeguid`
func (w *libWrapper) LinkSetVfNodeGUID(link Link, vf int, nodeguid net.HardwareAddr) error {
	return netlink.LinkSetVfNodeGUID(link, vf, nodeguid)
}

// LinkSetVfPortGUID sets the port GUID of a vf for the link.
// Equivalent to: `ip link set dev $link vf $vf port_guid $portguid`
func (w *libWrapper) LinkSetVfPortGUID(link Link, vf int, portguid net.HardwareAddr) error {
	return netlink.LinkSetVfPortGUID(link, vf, portguid)
}

// LinkByName finds a link by name and returns a pointer to the object.
func (w *libWrapper) LinkByName(name string) (Link, error) {
	return netlink.LinkByName(name)
}

// LinkByIndex finds a link by index and returns a pointer to the object.
func (w *libWrapper) LinkByIndex(index int) (Link, error) {
	return netlink.LinkByIndex(index)
}

// LinkList gets a list of link devices.
// Equivalent to: `ip link show`
func (w *libWrapper) LinkList() ([]Link, error) {
	links, err := netlink.LinkList()
	if err != nil {
		return nil, err
	}

	// Convert each netlink.Link to the custom Link interface
	customLinks := make([]Link, len(links))
	for i, link := range links {
		customLinks[i] = link
	}

	return customLinks, nil
}

// LinkSetVfHardwareAddr sets the hardware address of a vf for the link.
// Equivalent to: `ip link set $link vf $vf mac $hwaddr`
func (w *libWrapper) LinkSetVfHardwareAddr(link Link, vf int, hwaddr net.HardwareAddr) error {
	return netlink.LinkSetVfHardwareAddr(link, vf, hwaddr)
}

// LinkSetUp enables the link device.
// Equivalent to: `ip link set $link up`
func (w *libWrapper) LinkSetUp(link Link) error {
	return netlink.LinkSetUp(link)
}

// LinkSetMTU sets the mtu of the link device.
// Equivalent to: `ip link set $link mtu $mtu`
func (w *libWrapper) LinkSetMTU(link Link, mtu int) error {
	return netlink.LinkSetMTU(link, mtu)
}

// DevlinkGetDeviceByName provides a pointer to devlink device and nil error,
// otherwise returns an error code.
func (w *libWrapper) DevLinkGetDeviceByName(bus string, device string) (*netlink.DevlinkDevice, error) {
	return netlink.DevLinkGetDeviceByName(bus, device)
}

// DevLinkSetEswitchMode sets eswitch mode if able to set successfully or
// returns an error code.
// Equivalent to: `devlink dev eswitch set $dev mode switchdev`
// Equivalent to: `devlink dev eswitch set $dev mode legacy`
func (w *libWrapper) DevLinkSetEswitchMode(dev *netlink.DevlinkDevice, newMode string) error {
	return netlink.DevLinkSetEswitchMode(dev, newMode)
}

// VDPAGetDevByName returns VDPA device selected by name
// Equivalent to: `vdpa dev show <name>`
func (w *libWrapper) VDPAGetDevByName(name string) (*netlink.VDPADev, error) {
	return netlink.VDPAGetDevByName(name)
}

// VDPADelDev removes VDPA device
// Equivalent to: `vdpa dev del <name>`
func (w *libWrapper) VDPADelDev(name string) error {
	return netlink.VDPADelDev(name)
}

// VDPANewDev adds new VDPA device
// Equivalent to: `vdpa dev add name <name> mgmtdev <mgmtBus>/mgmtName [params]`
func (w *libWrapper) VDPANewDev(name, mgmtBus, mgmtName string, params netlink.VDPANewDevParams) error {
	return netlink.VDPANewDev(name, mgmtBus, mgmtName, params)
}

// DevlinkGetDeviceParamByName returns specific parameter for devlink device
// Equivalent to: `devlink dev param show <bus>/<device> name <param>`
func (w *libWrapper) DevlinkGetDeviceParamByName(bus string, device string, param string) (*netlink.DevlinkParam, error) {
	return netlink.DevlinkGetDeviceParamByName(bus, device, param)
}

// DevlinkSetDeviceParam set specific parameter for devlink device
// Equivalent to: `devlink dev param set <bus>/<device> name <param> cmode <cmode> value <value>`
// cmode argument should contain valid cmode value as uint8, modes are define in nl.DEVLINK_PARAM_CMODE_* constants
// value argument should have one of the following types: uint8, uint16, uint32, string, bool
func (w *libWrapper) DevlinkSetDeviceParam(bus string, device string, param string, cmode uint8, value interface{}) error {
	return netlink.DevlinkSetDeviceParam(bus, device, param, cmode, value)
}

// RdmaLinkByName finds a link by name and returns a pointer to the object if
// found and nil error, otherwise returns error code.
func (w *libWrapper) RdmaLinkByName(name string) (*netlink.RdmaLink, error) {
	return netlink.RdmaLinkByName(name)
}

// IsLinkAdminStateUp checks if the admin state of a link is up
func (w *libWrapper) IsLinkAdminStateUp(link Link) bool {
	return link.Attrs().Flags&net.FlagUp == 1
}

// RdmaSystemGetNetnsMode returns RDMA subsystem mode
func (w *libWrapper) RdmaSystemGetNetnsMode() (string, error) {
	return netlink.RdmaSystemGetNetnsMode()
}
