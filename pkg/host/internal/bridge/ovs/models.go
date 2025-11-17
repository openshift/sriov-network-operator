package ovs

import (
	"slices"

	"github.com/ovn-kubernetes/libovsdb/model"
)

// OpenvSwitchEntry represents some fields of the object in the Open_vSwitch table
type OpenvSwitchEntry struct {
	UUID    string   `ovsdb:"_uuid"`
	Bridges []string `ovsdb:"bridges"`
}

// BridgeEntry represents some fields of the object in the Bridge table
type BridgeEntry struct {
	UUID         string            `ovsdb:"_uuid"`
	Name         string            `ovsdb:"name"`
	DatapathType string            `ovsdb:"datapath_type"`
	ExternalIDs  map[string]string `ovsdb:"external_ids"`
	OtherConfig  map[string]string `ovsdb:"other_config"`
	Ports        []string          `ovsdb:"ports"`
	FailMode     *string           `ovsdb:"fail_mode"`
}

// HasPort returns true if portUUID is found in Ports slice
func (b *BridgeEntry) HasPort(portUUID string) bool {
	return slices.Contains(b.Ports, portUUID)
}

// InterfaceEntry represents some fields of the object in the Interface table
type InterfaceEntry struct {
	UUID        string            `ovsdb:"_uuid"`
	Name        string            `ovsdb:"name"`
	Type        string            `ovsdb:"type"`
	Error       *string           `ovsdb:"error"`
	Options     map[string]string `ovsdb:"options"`
	ExternalIDs map[string]string `ovsdb:"external_ids"`
	OtherConfig map[string]string `ovsdb:"other_config"`
	MTURequest  *int              `ovsdb:"mtu_request"`
}

// PortEntry represents some fields of the object in the Port table
type PortEntry struct {
	UUID       string   `ovsdb:"_uuid"`
	Name       string   `ovsdb:"name"`
	Interfaces []string `ovsdb:"interfaces"`
}

// DatabaseModel returns the DatabaseModel object to be used in libovsdb
func DatabaseModel() (model.ClientDBModel, error) {
	return model.NewClientDBModel("Open_vSwitch", map[string]model.Model{
		"Bridge":       &BridgeEntry{},
		"Interface":    &InterfaceEntry{},
		"Open_vSwitch": &OpenvSwitchEntry{},
		"Port":         &PortEntry{},
	})
}
