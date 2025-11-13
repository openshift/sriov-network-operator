package openstack

// OSPMetaDataDevice -- Device structure within meta_data.json
type OSPMetaDataDevice struct {
	Vlan      int      `json:"vlan,omitempty"`
	VfTrusted bool     `json:"vf_trusted,omitempty"`
	Type      string   `json:"type,omitempty"`
	Mac       string   `json:"mac,omitempty"`
	Bus       string   `json:"bus,omitempty"`
	Address   string   `json:"address,omitempty"`
	Tags      []string `json:"tags,omitempty"`
}

// OSPMetaData -- Openstack meta_data.json format
type OSPMetaData struct {
	UUID             string              `json:"uuid,omitempty"`
	AdminPass        string              `json:"admin_pass,omitempty"`
	Name             string              `json:"name,omitempty"`
	LaunchIndex      int                 `json:"launch_index,omitempty"`
	AvailabilityZone string              `json:"availability_zone,omitempty"`
	ProjectID        string              `json:"project_id,omitempty"`
	Devices          []OSPMetaDataDevice `json:"devices,omitempty"`
}

// OSPNetworkLink OSP Link metadata
type OSPNetworkLink struct {
	ID          string `json:"id"`
	VifID       string `json:"vif_id,omitempty"`
	Type        string `json:"type"`
	Mtu         int    `json:"mtu,omitempty"`
	EthernetMac string `json:"ethernet_mac_address"`
}

// OSPNetwork OSP Network metadata
type OSPNetwork struct {
	ID        string `json:"id"`
	Type      string `json:"type"`
	Link      string `json:"link"`
	NetworkID string `json:"network_id"`
}

// OSPNetworkData OSP Network metadata
type OSPNetworkData struct {
	Links    []OSPNetworkLink `json:"links,omitempty"`
	Networks []OSPNetwork     `json:"networks,omitempty"`
	// Omit Services
}

type OSPDevicesInfo map[string]*OSPDeviceInfo

type OSPDeviceInfo struct {
	MacAddress string
	NetworkID  string
}
