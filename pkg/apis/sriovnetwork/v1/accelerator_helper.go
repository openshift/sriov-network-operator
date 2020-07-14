package v1

import (
	corev1 "k8s.io/api/core/v1"
	logf "sigs.k8s.io/controller-runtime/pkg/runtime/log"
)

var AcceleratorSriovPfVfMap = map[string](string){
	"5052": "5050",
}

var accelLog = logf.Log.WithName("sriovaccelerator")

type AcceleratorPolicyByPriority []SriovAcceleratorNodePolicy

func (a AcceleratorPolicyByPriority) Len() int {
	return len(a)
}

func (a AcceleratorPolicyByPriority) Less(i, j int) bool {
	if a[i].Spec.Priority != a[j].Spec.Priority {
		return a[i].Spec.Priority > a[j].Spec.Priority
	}
	return a[i].GetName() < a[j].GetName()
}

func (a AcceleratorPolicyByPriority) Swap(i, j int) {
	a[i], a[j] = a[j], a[i]
}

// Match check if node is selected by NodeSelector
func (p *SriovAcceleratorNodePolicy) Selected(node *corev1.Node) bool {
	for k, v := range p.Spec.NodeSelector {
		if nv, ok := node.Labels[k]; ok && nv == v {
			continue
		}
		return false
	}
	log.Info("Selected():", "node", node.Name)
	return true
}

// Apply policy to SriovAcceleratorNodeState CR
func (p *SriovAcceleratorNodePolicy) Apply(state *SriovAcceleratorNodeState) {
	s := p.Spec.AccelSelector
	if s.Vendor == "" && s.DeviceID == "" && len(s.RootDevices) == 0 {
		// Empty NicSelector match none
		return
	}
	for _, iface := range state.Status.Accelerators {
		if s.Selected(&iface) {
			log.Info("Update accelerator", "pci:", iface.PciAddress, "vendor:", iface.Vendor, "deviceID:", iface.DeviceID)
			result := Card{
				PciAddress: iface.PciAddress,
				Config:     p.Spec.Config,
				DeviceName: p.Spec.DeviceName,
			}
			if p.Spec.NumVfs > 0 {
				result.NumVfs = p.Spec.NumVfs
				found := false
				for i := range state.Spec.Cards {
					if state.Spec.Cards[i].PciAddress == result.PciAddress {
						found = true
						state.Spec.Cards[i] = result
						break
					}
				}
				if !found {
					state.Spec.Cards = append(state.Spec.Cards, result)
				}
			}
		}
	}
}

func (selector *SriovAcceleratorNicSelector) Selected(iface *AcceleratorExt) bool {
	if selector.Vendor != "" && selector.Vendor != iface.Vendor {
		return false
	}
	if selector.DeviceID != "" && selector.DeviceID != iface.DeviceID {
		return false
	}
	if len(selector.RootDevices) > 0 && !StringInArray(iface.PciAddress, selector.RootDevices) {
		return false
	}
	return true
}

func (s *SriovAcceleratorNodeState) GetAcceleratorStateByPciAddress(addr string) *AcceleratorExt {
	for _, iface := range s.Status.Accelerators {
		if addr == iface.PciAddress {
			return &iface
		}
	}
	return nil
}

func (s *SriovAcceleratorNodeState) GetDriverByPciAddress(addr string) string {
	for _, iface := range s.Status.Accelerators {
		if addr == iface.PciAddress {
			return iface.Driver
		}
	}
	return ""
}
