// Code generated by MockGen. DO NOT EDIT.
// Source: netlink.go
//
// Generated by this command:
//
//	mockgen -destination mock/mock_netlink.go -source netlink.go
//

// Package mock_netlink is a generated GoMock package.
package mock_netlink

import (
	net "net"
	reflect "reflect"

	netlink "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/host/internal/lib/netlink"
	netlink0 "github.com/vishvananda/netlink"
	gomock "go.uber.org/mock/gomock"
)

// MockLink is a mock of Link interface.
type MockLink struct {
	ctrl     *gomock.Controller
	recorder *MockLinkMockRecorder
	isgomock struct{}
}

// MockLinkMockRecorder is the mock recorder for MockLink.
type MockLinkMockRecorder struct {
	mock *MockLink
}

// NewMockLink creates a new mock instance.
func NewMockLink(ctrl *gomock.Controller) *MockLink {
	mock := &MockLink{ctrl: ctrl}
	mock.recorder = &MockLinkMockRecorder{mock}
	return mock
}

// EXPECT returns an object that allows the caller to indicate expected use.
func (m *MockLink) EXPECT() *MockLinkMockRecorder {
	return m.recorder
}

// Attrs mocks base method.
func (m *MockLink) Attrs() *netlink0.LinkAttrs {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "Attrs")
	ret0, _ := ret[0].(*netlink0.LinkAttrs)
	return ret0
}

// Attrs indicates an expected call of Attrs.
func (mr *MockLinkMockRecorder) Attrs() *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "Attrs", reflect.TypeOf((*MockLink)(nil).Attrs))
}

// Type mocks base method.
func (m *MockLink) Type() string {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "Type")
	ret0, _ := ret[0].(string)
	return ret0
}

// Type indicates an expected call of Type.
func (mr *MockLinkMockRecorder) Type() *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "Type", reflect.TypeOf((*MockLink)(nil).Type))
}

// MockNetlinkLib is a mock of NetlinkLib interface.
type MockNetlinkLib struct {
	ctrl     *gomock.Controller
	recorder *MockNetlinkLibMockRecorder
	isgomock struct{}
}

// MockNetlinkLibMockRecorder is the mock recorder for MockNetlinkLib.
type MockNetlinkLibMockRecorder struct {
	mock *MockNetlinkLib
}

// NewMockNetlinkLib creates a new mock instance.
func NewMockNetlinkLib(ctrl *gomock.Controller) *MockNetlinkLib {
	mock := &MockNetlinkLib{ctrl: ctrl}
	mock.recorder = &MockNetlinkLibMockRecorder{mock}
	return mock
}

// EXPECT returns an object that allows the caller to indicate expected use.
func (m *MockNetlinkLib) EXPECT() *MockNetlinkLibMockRecorder {
	return m.recorder
}

// DevLinkGetDeviceByName mocks base method.
func (m *MockNetlinkLib) DevLinkGetDeviceByName(bus, device string) (*netlink0.DevlinkDevice, error) {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "DevLinkGetDeviceByName", bus, device)
	ret0, _ := ret[0].(*netlink0.DevlinkDevice)
	ret1, _ := ret[1].(error)
	return ret0, ret1
}

// DevLinkGetDeviceByName indicates an expected call of DevLinkGetDeviceByName.
func (mr *MockNetlinkLibMockRecorder) DevLinkGetDeviceByName(bus, device any) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "DevLinkGetDeviceByName", reflect.TypeOf((*MockNetlinkLib)(nil).DevLinkGetDeviceByName), bus, device)
}

// DevLinkSetEswitchMode mocks base method.
func (m *MockNetlinkLib) DevLinkSetEswitchMode(dev *netlink0.DevlinkDevice, newMode string) error {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "DevLinkSetEswitchMode", dev, newMode)
	ret0, _ := ret[0].(error)
	return ret0
}

// DevLinkSetEswitchMode indicates an expected call of DevLinkSetEswitchMode.
func (mr *MockNetlinkLibMockRecorder) DevLinkSetEswitchMode(dev, newMode any) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "DevLinkSetEswitchMode", reflect.TypeOf((*MockNetlinkLib)(nil).DevLinkSetEswitchMode), dev, newMode)
}

// DevlinkGetDeviceParamByName mocks base method.
func (m *MockNetlinkLib) DevlinkGetDeviceParamByName(bus, device, param string) (*netlink0.DevlinkParam, error) {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "DevlinkGetDeviceParamByName", bus, device, param)
	ret0, _ := ret[0].(*netlink0.DevlinkParam)
	ret1, _ := ret[1].(error)
	return ret0, ret1
}

// DevlinkGetDeviceParamByName indicates an expected call of DevlinkGetDeviceParamByName.
func (mr *MockNetlinkLibMockRecorder) DevlinkGetDeviceParamByName(bus, device, param any) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "DevlinkGetDeviceParamByName", reflect.TypeOf((*MockNetlinkLib)(nil).DevlinkGetDeviceParamByName), bus, device, param)
}

// DevlinkSetDeviceParam mocks base method.
func (m *MockNetlinkLib) DevlinkSetDeviceParam(bus, device, param string, cmode uint8, value any) error {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "DevlinkSetDeviceParam", bus, device, param, cmode, value)
	ret0, _ := ret[0].(error)
	return ret0
}

// DevlinkSetDeviceParam indicates an expected call of DevlinkSetDeviceParam.
func (mr *MockNetlinkLibMockRecorder) DevlinkSetDeviceParam(bus, device, param, cmode, value any) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "DevlinkSetDeviceParam", reflect.TypeOf((*MockNetlinkLib)(nil).DevlinkSetDeviceParam), bus, device, param, cmode, value)
}

// IsLinkAdminStateUp mocks base method.
func (m *MockNetlinkLib) IsLinkAdminStateUp(link netlink.Link) bool {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "IsLinkAdminStateUp", link)
	ret0, _ := ret[0].(bool)
	return ret0
}

// IsLinkAdminStateUp indicates an expected call of IsLinkAdminStateUp.
func (mr *MockNetlinkLibMockRecorder) IsLinkAdminStateUp(link any) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "IsLinkAdminStateUp", reflect.TypeOf((*MockNetlinkLib)(nil).IsLinkAdminStateUp), link)
}

// LinkByIndex mocks base method.
func (m *MockNetlinkLib) LinkByIndex(index int) (netlink.Link, error) {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "LinkByIndex", index)
	ret0, _ := ret[0].(netlink.Link)
	ret1, _ := ret[1].(error)
	return ret0, ret1
}

// LinkByIndex indicates an expected call of LinkByIndex.
func (mr *MockNetlinkLibMockRecorder) LinkByIndex(index any) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "LinkByIndex", reflect.TypeOf((*MockNetlinkLib)(nil).LinkByIndex), index)
}

// LinkByName mocks base method.
func (m *MockNetlinkLib) LinkByName(name string) (netlink.Link, error) {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "LinkByName", name)
	ret0, _ := ret[0].(netlink.Link)
	ret1, _ := ret[1].(error)
	return ret0, ret1
}

// LinkByName indicates an expected call of LinkByName.
func (mr *MockNetlinkLibMockRecorder) LinkByName(name any) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "LinkByName", reflect.TypeOf((*MockNetlinkLib)(nil).LinkByName), name)
}

// LinkList mocks base method.
func (m *MockNetlinkLib) LinkList() ([]netlink.Link, error) {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "LinkList")
	ret0, _ := ret[0].([]netlink.Link)
	ret1, _ := ret[1].(error)
	return ret0, ret1
}

// LinkList indicates an expected call of LinkList.
func (mr *MockNetlinkLibMockRecorder) LinkList() *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "LinkList", reflect.TypeOf((*MockNetlinkLib)(nil).LinkList))
}

// LinkSetMTU mocks base method.
func (m *MockNetlinkLib) LinkSetMTU(link netlink.Link, mtu int) error {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "LinkSetMTU", link, mtu)
	ret0, _ := ret[0].(error)
	return ret0
}

// LinkSetMTU indicates an expected call of LinkSetMTU.
func (mr *MockNetlinkLibMockRecorder) LinkSetMTU(link, mtu any) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "LinkSetMTU", reflect.TypeOf((*MockNetlinkLib)(nil).LinkSetMTU), link, mtu)
}

// LinkSetUp mocks base method.
func (m *MockNetlinkLib) LinkSetUp(link netlink.Link) error {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "LinkSetUp", link)
	ret0, _ := ret[0].(error)
	return ret0
}

// LinkSetUp indicates an expected call of LinkSetUp.
func (mr *MockNetlinkLibMockRecorder) LinkSetUp(link any) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "LinkSetUp", reflect.TypeOf((*MockNetlinkLib)(nil).LinkSetUp), link)
}

// LinkSetVfHardwareAddr mocks base method.
func (m *MockNetlinkLib) LinkSetVfHardwareAddr(link netlink.Link, vf int, hwaddr net.HardwareAddr) error {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "LinkSetVfHardwareAddr", link, vf, hwaddr)
	ret0, _ := ret[0].(error)
	return ret0
}

// LinkSetVfHardwareAddr indicates an expected call of LinkSetVfHardwareAddr.
func (mr *MockNetlinkLibMockRecorder) LinkSetVfHardwareAddr(link, vf, hwaddr any) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "LinkSetVfHardwareAddr", reflect.TypeOf((*MockNetlinkLib)(nil).LinkSetVfHardwareAddr), link, vf, hwaddr)
}

// LinkSetVfNodeGUID mocks base method.
func (m *MockNetlinkLib) LinkSetVfNodeGUID(link netlink.Link, vf int, nodeguid net.HardwareAddr) error {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "LinkSetVfNodeGUID", link, vf, nodeguid)
	ret0, _ := ret[0].(error)
	return ret0
}

// LinkSetVfNodeGUID indicates an expected call of LinkSetVfNodeGUID.
func (mr *MockNetlinkLibMockRecorder) LinkSetVfNodeGUID(link, vf, nodeguid any) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "LinkSetVfNodeGUID", reflect.TypeOf((*MockNetlinkLib)(nil).LinkSetVfNodeGUID), link, vf, nodeguid)
}

// LinkSetVfPortGUID mocks base method.
func (m *MockNetlinkLib) LinkSetVfPortGUID(link netlink.Link, vf int, portguid net.HardwareAddr) error {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "LinkSetVfPortGUID", link, vf, portguid)
	ret0, _ := ret[0].(error)
	return ret0
}

// LinkSetVfPortGUID indicates an expected call of LinkSetVfPortGUID.
func (mr *MockNetlinkLibMockRecorder) LinkSetVfPortGUID(link, vf, portguid any) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "LinkSetVfPortGUID", reflect.TypeOf((*MockNetlinkLib)(nil).LinkSetVfPortGUID), link, vf, portguid)
}

// RdmaLinkByName mocks base method.
func (m *MockNetlinkLib) RdmaLinkByName(name string) (*netlink0.RdmaLink, error) {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "RdmaLinkByName", name)
	ret0, _ := ret[0].(*netlink0.RdmaLink)
	ret1, _ := ret[1].(error)
	return ret0, ret1
}

// RdmaLinkByName indicates an expected call of RdmaLinkByName.
func (mr *MockNetlinkLibMockRecorder) RdmaLinkByName(name any) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "RdmaLinkByName", reflect.TypeOf((*MockNetlinkLib)(nil).RdmaLinkByName), name)
}

// RdmaSystemGetNetnsMode mocks base method.
func (m *MockNetlinkLib) RdmaSystemGetNetnsMode() (string, error) {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "RdmaSystemGetNetnsMode")
	ret0, _ := ret[0].(string)
	ret1, _ := ret[1].(error)
	return ret0, ret1
}

// RdmaSystemGetNetnsMode indicates an expected call of RdmaSystemGetNetnsMode.
func (mr *MockNetlinkLibMockRecorder) RdmaSystemGetNetnsMode() *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "RdmaSystemGetNetnsMode", reflect.TypeOf((*MockNetlinkLib)(nil).RdmaSystemGetNetnsMode))
}

// VDPADelDev mocks base method.
func (m *MockNetlinkLib) VDPADelDev(name string) error {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "VDPADelDev", name)
	ret0, _ := ret[0].(error)
	return ret0
}

// VDPADelDev indicates an expected call of VDPADelDev.
func (mr *MockNetlinkLibMockRecorder) VDPADelDev(name any) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "VDPADelDev", reflect.TypeOf((*MockNetlinkLib)(nil).VDPADelDev), name)
}

// VDPAGetDevByName mocks base method.
func (m *MockNetlinkLib) VDPAGetDevByName(name string) (*netlink0.VDPADev, error) {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "VDPAGetDevByName", name)
	ret0, _ := ret[0].(*netlink0.VDPADev)
	ret1, _ := ret[1].(error)
	return ret0, ret1
}

// VDPAGetDevByName indicates an expected call of VDPAGetDevByName.
func (mr *MockNetlinkLibMockRecorder) VDPAGetDevByName(name any) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "VDPAGetDevByName", reflect.TypeOf((*MockNetlinkLib)(nil).VDPAGetDevByName), name)
}

// VDPANewDev mocks base method.
func (m *MockNetlinkLib) VDPANewDev(name, mgmtBus, mgmtName string, params netlink0.VDPANewDevParams) error {
	m.ctrl.T.Helper()
	ret := m.ctrl.Call(m, "VDPANewDev", name, mgmtBus, mgmtName, params)
	ret0, _ := ret[0].(error)
	return ret0
}

// VDPANewDev indicates an expected call of VDPANewDev.
func (mr *MockNetlinkLibMockRecorder) VDPANewDev(name, mgmtBus, mgmtName, params any) *gomock.Call {
	mr.mock.ctrl.T.Helper()
	return mr.mock.ctrl.RecordCallWithMethodType(mr.mock, "VDPANewDev", reflect.TypeOf((*MockNetlinkLib)(nil).VDPANewDev), name, mgmtBus, mgmtName, params)
}
