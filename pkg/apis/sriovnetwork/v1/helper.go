package v1

import (
	"strconv"
	"strings"

	corev1 "k8s.io/api/core/v1"
	logf "sigs.k8s.io/controller-runtime/pkg/runtime/log"
)

const invalidVfIndex = -1

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

func RemoveString(s string, slice []string) (result []string) {
	for _, item := range slice {
		if item == s {
			continue
		}
		result = append(result, item)
	}
	return
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
	for _, iface := range state.Status.Interfaces {
		if s.Selected(&iface) {
			log.Info("Update interface", "name:", iface.Name)
			result := Interface{
				PciAddress: iface.PciAddress,
				Mtu:        p.Spec.Mtu,
				Name:       iface.Name,
			}
			var group *VfGroup
			if p.Spec.NumVfs > 0 {
				result.NumVfs = p.Spec.NumVfs
				group, _ = generateVfGroup(&p.Spec, &iface)
			}
			found := false
			for i := range state.Spec.Interfaces {
				if state.Spec.Interfaces[i].PciAddress == result.PciAddress {
					found = true
					result.VfGroups = state.Spec.Interfaces[i].mergeVfGroups(group)
					state.Spec.Interfaces[i] = result
					break
				}
			}
			if !found {
				result.VfGroups = []VfGroup{*group}
				state.Spec.Interfaces = append(state.Spec.Interfaces, result)
			}
		}
	}
}

func (iface Interface) mergeVfGroups(input *VfGroup) []VfGroup {
	groups := iface.VfGroups
	for i := range groups {
		if groups[i].ResourceName == input.ResourceName {
			groups[i] = *input
			return groups
		}
	}
	groups = append(groups, *input)
	return groups
}

func generateVfGroup(ps *SriovNetworkNodePolicySpec, iface *InterfaceExt) (*VfGroup, error) {
	var err error
	pfName := ""
	var rngStart, rngEnd int
	for _, selector := range ps.NicSelector.PfNames {
		pfName, rngStart, rngEnd, err = ParsePFName(selector)
		if err != nil {
			return nil, err
		}
		if pfName == iface.Name {
			if rngStart == invalidVfIndex && rngEnd == invalidVfIndex {
				rngStart, rngEnd = 0, ps.NumVfs-1
			}
			break
		}
	}
	rng := strconv.Itoa(rngStart) + "-" + strconv.Itoa(rngEnd)
	return &VfGroup{
		ResourceName: ps.ResourceName,
		DeviceType:   ps.DeviceType,
		VfRange:      rng,
	}, nil
}

func IndexInRange(i int, r string) bool {
	rngSt, rngEnd, err := parseRange(r)
	if err != nil {
		return false
	}
	if i <= rngEnd && i >= rngSt {
		return true
	}
	return false
}

func parseRange(r string) (rngSt, rngEnd int, err error) {
	rng := strings.Split(r, "-")
	rngSt, err = strconv.Atoi(rng[0])
	if err != nil {
		return
	}
	rngEnd, err = strconv.Atoi(rng[1])
	if err != nil {
		return
	}
	return
}

// Parse PF name with VF range
func ParsePFName(name string) (ifName string, rngSt, rngEnd int, err error) {
	rngSt, rngEnd = invalidVfIndex, invalidVfIndex
	if strings.Contains(name, "#") {
		fields := strings.Split(name, "#")
		ifName = fields[0]
		rngSt, rngEnd, err = parseRange(fields[1])
	} else {
		ifName = name
	}
	return
}

func (selector *SriovNetworkNicSelector) Selected(iface *InterfaceExt) bool {
	if selector.Vendor != "" && selector.Vendor != iface.Vendor {
		return false
	}
	if selector.DeviceID != "" && selector.DeviceID != iface.DeviceID {
		return false
	}
	if len(selector.RootDevices) > 0 && !StringInArray(iface.PciAddress, selector.RootDevices) {
		return false
	}
	if len(selector.PfNames) > 0 {
		var pfNames []string
		for _, p := range selector.PfNames {
			if strings.Contains(p, "#") {
				fields := strings.Split(p, "#")
				pfNames = append(pfNames, fields[0])
			} else {
				pfNames = append(pfNames, p)
			}
		}
		if !StringInArray(iface.Name, pfNames) {
			return false
		}
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
