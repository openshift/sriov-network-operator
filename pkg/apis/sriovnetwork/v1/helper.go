package v1

import (
	corev1 "k8s.io/api/core/v1"
	logf "sigs.k8s.io/controller-runtime/pkg/runtime/log"
)

var DeviceDriverMap = map[string](map[string]string){
	"PF": {
		"1572": "i40e",
		"1583": "i40e",
		"158b": "i40e",
		"37d2": "i40e",
	},
	"VF": {
		"1572": "iavf",
		"1583": "iavf",
		"158b": "iavf",
		"37d2": "iavf",
		"154c": "iavf",
	},
}

var SriovPfVfMap = map[string](string){
	"1583": "154c",
}

var log = logf.Log.WithName("sriovnetwork")

type ByPriority []SriovNetworkNodePolicy

func (a ByPriority) Len() int {
	return len(a)
}

func (a ByPriority) Less(i, j int) bool {
	return a[i].Spec.Priority > a[j].Spec.Priority
}

func (a ByPriority) Swap(i, j int) {
	a[i], a[j] = a[j], a[i]
}

// Match check if node is selected by NodeSelector
func (p *SriovNetworkNodePolicy) Selected(node *corev1.Node) bool {
	for k, v := range p.Spec.NodeSelector {
		if nv, ok := node.Labels[k]; ok && nv == v {
			continue
		}
		return false
	}
	log.Info("Selected():", "node", node.Name)
	return true
}

func StringInArray(val string, array []string) bool {
	for i := range array {
		if array[i] == val {
			return true
		}
	}
	return false
}

func UniqueAppend(inSlice []string, strings ...string) []string {
	for _, s := range strings {
		if !StringInArray(s, inSlice) {
			inSlice = append(inSlice, s)
		}
	}
	return inSlice
}

// Apply policy to SriovNetworkNodeState CR
func (p *SriovNetworkNodePolicy) Apply(state *SriovNetworkNodeState) {
	s := p.Spec.NicSelector
	if s.Vendor == "" && s.DeviceID == "" && len(s.RootDevices) == 0 && len(s.PfNames) == 0 {
		// Empty NicSelector match none
		return
	}
	interfaces := []Interface{}
	for _, iface := range state.Status.Interfaces {
		if s.Selected(&iface) {
			log.Info("Update interface", "name:", iface.Name)
			interfaces = append(interfaces, Interface{
				PciAddress: iface.PciAddress,
				Mtu:        p.Spec.Mtu,
				NumVfs:     p.Spec.NumVfs,
				DeviceType: p.Spec.DeviceType,
			})
		}
	}
	state.Spec.Interfaces = append(state.Spec.Interfaces, interfaces...)
}

func (s *SriovNetworkNicSelector) Selected(iface *InterfaceExt) bool {
	if s.Vendor != "" && s.Vendor != iface.Vendor {
		return false
	}
	if s.DeviceID != "" && s.DeviceID != iface.DeviceID {
		return false
	}
	if len(s.RootDevices) > 0 && !StringInArray(iface.PciAddress, s.RootDevices) {
		return false
	}
	if len(s.PfNames) > 0 && !StringInArray(iface.Name, s.PfNames) {
		return false
	}
	return true
}

func (s *SriovNetworkNodeState) GetInterfaceStateByPciAddress(addr string) *InterfaceExt {
	for _, iface := range s.Status.Interfaces {
		if addr == iface.PciAddress {
			return &iface
		}
	}
	return nil
}

func (s *SriovNetworkNodeState) GetDriverByPciAddress(addr string) string {
	for _, iface := range s.Status.Interfaces {
		if addr == iface.PciAddress {
			return iface.Driver
		}
	}
	return ""
}
