package webhook

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/golang/glog"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"

	sriovnetworkv1 "github.com/openshift/sriov-network-operator/pkg/apis/sriovnetwork/v1"
)

const (
	IntelID    = "8086"
	MellanoxID = "15b3"
	MlxMaxVFs  = 128
)

// SupportedModels holds the NIC models officially supported
var SupportedModels = map[string]([]string){
	IntelID:    []string{"158b"},
	MellanoxID: []string{"1015", "1017"},
}

var (
	nodesSelected     bool
	interfaceSelected bool
)

func validateSriovNetworkNodePolicy(cr *sriovnetworkv1.SriovNetworkNodePolicy) (bool, error) {
	glog.V(2).Infof("validateSriovNetworkNodePolicy: %v", cr)

	if cr.GetName() == "default" {
		// skip the default policy
		return true, nil
	}

	admit, err := staticValidateSriovNetworkNodePolicy(cr)
	if err != nil {
		return admit, err

	}

	admit, err = dynamicValidateSriovNetworkNodePolicy(cr)
	if err != nil {
		return admit, err

	}
	return admit, nil
}

func staticValidateSriovNetworkNodePolicy(cr *sriovnetworkv1.SriovNetworkNodePolicy) (bool, error) {
	var validString = regexp.MustCompile(`^[a-zA-Z0-9_]+$`)
	if !validString.MatchString(cr.Spec.ResourceName) {
		return false, fmt.Errorf("resource name \"%s\" contains invalid characters, the accepted syntax of the regular expressions is: \"^[a-zA-Z0-9_]+$\"", cr.Spec.ResourceName)
	}

	if cr.Spec.NicSelector.Vendor == "" && cr.Spec.NicSelector.DeviceID == "" && len(cr.Spec.NicSelector.PfNames) == 0 {
		return false, fmt.Errorf("at least one of these parameters (Vendor, DeviceID or PfNames) has to be defined in nicSelector in CR %s", cr.GetName())
	}

	if cr.Spec.NicSelector.Vendor != "" {
		if !sriovnetworkv1.StringInArray(cr.Spec.NicSelector.Vendor, keys(SupportedModels)) {
			return false, fmt.Errorf("vendor %s is not supported", cr.Spec.NicSelector.Vendor)
		}
		if cr.Spec.NicSelector.DeviceID != "" {
			if (cr.Spec.NicSelector.Vendor == IntelID && !sriovnetworkv1.StringInArray(cr.Spec.NicSelector.DeviceID, SupportedModels[IntelID])) || (cr.Spec.NicSelector.Vendor == MellanoxID && !sriovnetworkv1.StringInArray(cr.Spec.NicSelector.DeviceID, SupportedModels[MellanoxID])) {
				return false, fmt.Errorf("vendor/device %s/%s is not supported", cr.Spec.NicSelector.Vendor, cr.Spec.NicSelector.DeviceID)
			}
		}
	} else if cr.Spec.NicSelector.DeviceID != "" {
		if !sriovnetworkv1.StringInArray(cr.Spec.NicSelector.DeviceID, append(SupportedModels[IntelID], SupportedModels[MellanoxID]...)) {
			return false, fmt.Errorf("device %s is not supported", cr.Spec.NicSelector.DeviceID)
		}
	}

	if len(cr.Spec.NicSelector.PfNames) > 0 {
		for _, pf := range cr.Spec.NicSelector.PfNames {
			if strings.Contains(pf, "#") {
				fields := strings.Split(pf, "#")
				if len(fields) != 2 {
					return false, fmt.Errorf("failed to parse %s PF name in nicSelector, probably incorrect separator character usage", pf)
				}
				rng := strings.Split(fields[1], "-")
				if len(rng) != 2 {
					return false, fmt.Errorf("failed to parse %s PF name nicSelector, probably incorrect range character usage", pf)
				}
				rngSt, err := strconv.Atoi(rng[0])
				if err != nil {
					return false, fmt.Errorf("failed to parse %s PF name nicSelector, start range is incorrect", pf)
				}
				rngEnd, err := strconv.Atoi(rng[1])
				if err != nil {
					return false, fmt.Errorf("failed to parse %s PF name nicSelector, end range is incorrect", pf)
				}
				if rngEnd < rngSt {
					return false, fmt.Errorf("failed to parse %s PF name nicSelector, end range shall not be smaller than start range", pf)
				}
				if !(rngEnd < cr.Spec.NumVfs) {
					return false, fmt.Errorf("failed to parse %s PF name nicSelector, end range exceeds the maximum VF index ", pf)
				}
			}
		}
	}

	// To configure RoCE on baremetal or virtual machine:
	// BM: DeviceType = netdevice && isRdma = true
	// VM: DeviceType = vfio-pci && isRdma = false
	if cr.Spec.DeviceType == "vfio-pci" && cr.Spec.IsRdma {
		return false, fmt.Errorf("'deviceType: vfio-pci' conflicts with 'isRdma: true'; Set 'deviceType' to (string)'netdevice' Or Set 'isRdma' to (bool)'false'")
	}
	return true, nil
}

func dynamicValidateSriovNetworkNodePolicy(cr *sriovnetworkv1.SriovNetworkNodePolicy) (bool, error) {
	nodesSelected = false
	interfaceSelected = false

	nodeList, err := kubeclient.CoreV1().Nodes().List(context.Background(), metav1.ListOptions{
		LabelSelector: labels.Set(cr.Spec.NodeSelector).String(),
	})
	nsList, err := snclient.SriovnetworkV1().SriovNetworkNodeStates(namespace).List(context.Background(), metav1.ListOptions{})
	if err != nil {
		return false, err
	}
	for _, node := range nodeList.Items {
		if cr.Selected(&node) {
			nodesSelected = true
			for _, ns := range nsList.Items {
				if ns.GetName() == node.GetName() {
					if ok, err := validatePolicyForNodeState(cr, &ns); err != nil || !ok {
						return false, err
					}
				}
			}
		}
	}

	if !nodesSelected {
		return false, fmt.Errorf("no matched node is selected by the nodeSelector in CR %s", cr.GetName())
	}
	if !interfaceSelected {
		return false, fmt.Errorf("no supported NIC is selected by the nicSelector in CR %s", cr.GetName())
	}

	return true, nil
}

func validatePolicyForNodeState(policy *sriovnetworkv1.SriovNetworkNodePolicy, state *sriovnetworkv1.SriovNetworkNodeState) (bool, error) {
	glog.V(2).Infof("validatePolicyForNodeState(): validate policy %s for node %s.", policy.GetName(), state.GetName())
	for _, iface := range state.Status.Interfaces {
		if validateNicModel(&policy.Spec.NicSelector, &iface) {
			interfaceSelected = true
			if policy.GetName() != "default" && policy.Spec.NumVfs == 0 {
				return false, fmt.Errorf("numVfs(%d) in CR %s is not allowed", policy.Spec.NumVfs, policy.GetName())
			}
			if policy.Spec.NumVfs > iface.TotalVfs && iface.Vendor == IntelID {
				return false, fmt.Errorf("numVfs(%d) in CR %s exceed the maximum allowed value(%d)", policy.Spec.NumVfs, policy.GetName(), iface.TotalVfs)
			}
			if policy.Spec.NumVfs > MlxMaxVFs && iface.Vendor == MellanoxID {
				return false, fmt.Errorf("numVfs(%d) in CR %s exceed the maximum allowed value(%d)", policy.Spec.NumVfs, policy.GetName(), MlxMaxVFs)
			}
		}
	}

	for _, iface := range state.Spec.Interfaces {
		if len(policy.Spec.NicSelector.PfNames) > 0 {
			for _, pf := range policy.Spec.NicSelector.PfNames {
				name, rngSt, rngEnd, err := sriovnetworkv1.ParsePFName(pf)
				if err != nil {
					return false, fmt.Errorf("invalid PF name: %s", pf)
				}
				if name == iface.Name {
					for _, group := range iface.VfGroups {
						if sriovnetworkv1.IndexInRange(rngSt, group.VfRange) || sriovnetworkv1.IndexInRange(rngEnd, group.VfRange) {
							if group.PolicyName != policy.GetName() {
								return false, fmt.Errorf("Vf index range in %s is overlapped with existing policies", pf)
							}
						}
					}
				}
			}
		}
	}
	return true, nil
}

func keys(m map[string]([]string)) []string {
	keys := make([]string, len(m))
	i := 0
	for k := range m {
		keys[i] = k
		i++
	}
	return keys
}

func validateNicModel(selector *sriovnetworkv1.SriovNetworkNicSelector, iface *sriovnetworkv1.InterfaceExt) bool {
	if selector.Vendor != "" && selector.Vendor != iface.Vendor {
		return false
	}
	if selector.DeviceID != "" && selector.DeviceID != iface.DeviceID {
		return false
	}
	if len(selector.RootDevices) > 0 && !sriovnetworkv1.StringInArray(iface.PciAddress, selector.RootDevices) {
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
		if !sriovnetworkv1.StringInArray(iface.Name, pfNames) {
			return false
		}
	}
	// check the vendor/device ID to make sure only devices in supported list are allowed.
	for k, v := range SupportedModels {
		if k == iface.Vendor && !sriovnetworkv1.StringInArray(iface.DeviceID, v) {
			return false
		}
	}
	return true
}
