package v1

import (
	corev1 "k8s.io/api/core/v1"
	logf "sigs.k8s.io/controller-runtime/pkg/runtime/log"
)

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
func (p *SriovNetworkNodePolicy) Selected(node *corev1.Node) bool{
	for k, v := range p.Spec.NodeSelector {
		if nv, ok := node.Labels[k]; ok && nv == v{
			continue
		}
		return false
	}
	log.Info("Selectd ", "node", node.Name)
	return true
}

func in_array(val string, array []string) bool{
    for i := range array {
        if array[i] == val{
            return true
        }
    }
    return false
}

// Apply policy to SriovNetworkNodeState CR
func (p *SriovNetworkNodePolicy) Apply(state *SriovNetworkNodeState) {
	s := p.Spec.NicSelector
	if s.Name == "" && s.Vendor == "" && s.LinkSpeed == "" && len(s.RootDevices) == 0 {
		// Empty NicSelector match none
		return
	}
	interfaces := []Interface{}
	for _, iface := range state.Status.Interfaces {
		if  s.Selected(&iface){
			log.Info("Update interface", "name", iface.Name)
			interfaces = append(interfaces, Interface{
				PciAddress: iface.PciAddress,
				Mtu: p.Spec.Mtu,
				NumVfs: p.Spec.NumVfs,
			})
		}
	}
	state.Spec.Interfaces = interfaces
}

func (s *SriovNetworkNicSelector) Selected(iface *InterfaceExt) bool{
	if s.Name != "" && s.Name != iface.Name {
		return false
	}
	if s.Vendor != "" && s.Vendor != iface.Vendor {
		return false
	}
	if s.LinkSpeed !="" && s.LinkSpeed != iface.LinkSpeed {
		return false
	}
	if len(s.RootDevices)>0 && !in_array(iface.PciAddress, s.RootDevices) {
		return false
	}
	return true
}
