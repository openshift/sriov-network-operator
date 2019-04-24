package v1

import (
	corev1 "k8s.io/api/core/v1"
)

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
	for _, iface := range state.Status.Interfaces {
		if in_array(iface.PciAddress, s.RootDevices) && iface.LinkSpeed == s.LinkSpeed && iface.Vendor == s.Vendor {
			iface.Mtu = p.Spec.Mtu
			iface.NumVfs = p.Spec.NumVfs
			iface.ResourceName = p.Spec.ResourceName
		}
	}
}
