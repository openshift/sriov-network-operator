package v1

import (
	corev1 "k8s.io/api/core/v1"
	logf "sigs.k8s.io/controller-runtime/pkg/runtime/log"
)

var SriovPfVfMap = map[string](string){
	"1583": "154c",
	"158b": "154c",
	"10fb": "10ed",
	"1015": "1016",
	"1017": "1018",
}

var VfIds = []string{}

func init() {
	for _, v := range SriovPfVfMap {
		id := "0x" + v
		if !StringInArray(id, VfIds) {
			VfIds = append(VfIds, id)
		}
	}
}

var log = logf.Log.WithName("sriovnetwork")

type ByPriority []SriovNetworkNodePolicy

func (a ByPriority) Len() int {
	return len(a)
}

func (a ByPriority) Less(i, j int) bool {
	if a[i].Spec.Priority != a[j].Spec.Priority {
		return a[i].Spec.Priority > a[j].Spec.Priority
	}
	return a[i].GetName() < a[j].GetName()
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
	s := p.Spec
	if s.NicSelector.Vendor == "" && s.NicSelector.DeviceID == "" && len(s.NicSelector.RootDevices) == 0 && len(s.NicSelector.PfNames) == 0 {
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

func (s *SriovNetworkNodePolicySpec) Selected(iface *InterfaceExt) bool {
	if s.NicSelector.Vendor != "" && s.NicSelector.Vendor != iface.Vendor {
		return false
	}
	if s.NicSelector.DeviceID != "" && s.NicSelector.DeviceID != iface.DeviceID {
		return false
	}
	if len(s.NicSelector.RootDevices) > 0 && !StringInArray(iface.PciAddress, s.NicSelector.RootDevices) {
		return false
	}
	if len(s.NicSelector.PfNames) > 0 && !StringInArray(iface.Name, s.NicSelector.PfNames) {
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
