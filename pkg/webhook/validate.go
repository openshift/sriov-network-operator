package webhook

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/golang/glog"
	"k8s.io/api/admission/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"

	sriovnetworkv1 "github.com/openshift/sriov-network-operator/api/v1"
)

const (
	IntelID    = "8086"
	MellanoxID = "15b3"
	MlxMaxVFs  = 128
)

var (
	nodesSelected     bool
	interfaceSelected bool
)

func validateSriovOperatorConfig(cr *sriovnetworkv1.SriovOperatorConfig, operation v1beta1.Operation) (bool, []string, error) {
	glog.V(2).Infof("validateSriovOperatorConfig: %v", cr)
	var warnings []string

	if cr.GetName() == "default" {
		if operation == "DELETE" {
			return false, warnings, fmt.Errorf("default SriovOperatorConfig shouldn't be deleted")
		}

		if cr.Spec.DisableDrain {
			warnings = append(warnings, "Node draining is disabled for applying SriovNetworkNodePolicy, it may result in workload interruption.")
		}
		return true, warnings, nil
	}
	return false, warnings, fmt.Errorf("only default SriovOperatorConfig is used")
}

func validateSriovNetworkNodePolicy(cr *sriovnetworkv1.SriovNetworkNodePolicy, operation v1beta1.Operation) (bool, []string, error) {
	glog.V(2).Infof("validateSriovNetworkNodePolicy: %v", cr)
	var warnings []string

	if cr.GetName() == "default" && cr.GetNamespace() == os.Getenv("NAMESPACE") {
		if operation == "DELETE" {
			// reject deletion of default policy
			return false, warnings, fmt.Errorf("default SriovNetworkNodePolicy shouldn't be deleted")
		} else {
			// skip validating default policy
			return true, warnings, nil
		}
	}

	if cr.GetNamespace() != os.Getenv("NAMESPACE") {
		warnings = append(warnings, cr.GetName()+
			" is created or updated but not used. Only policy in openshift-sriov-network-operator namespace is respected.")
	}

	admit, err := staticValidateSriovNetworkNodePolicy(cr)
	if err != nil {
		return admit, warnings, err

	}

	admit, err = dynamicValidateSriovNetworkNodePolicy(cr)
	if err != nil {
		return admit, warnings, err

	}

	return admit, warnings, nil
}

func staticValidateSriovNetworkNodePolicy(cr *sriovnetworkv1.SriovNetworkNodePolicy) (bool, error) {
	var validString = regexp.MustCompile(`^[a-zA-Z0-9_]+$`)
	if !validString.MatchString(cr.Spec.ResourceName) {
		return false, fmt.Errorf("resource name \"%s\" contains invalid characters, the accepted syntax of the regular expressions is: \"^[a-zA-Z0-9_]+$\"", cr.Spec.ResourceName)
	}

	if cr.Spec.NicSelector.Vendor == "" && cr.Spec.NicSelector.DeviceID == "" && len(cr.Spec.NicSelector.PfNames) == 0 && len(cr.Spec.NicSelector.RootDevices) == 0 {
		return false, fmt.Errorf("at least one of these parameters (vendor, deviceID, pfNames or rootDevices) has to be defined in nicSelector in CR %s", cr.GetName())
	}

	if cr.Spec.NicSelector.Vendor != "" {
		if !sriovnetworkv1.IsSupportedVendor(cr.Spec.NicSelector.Vendor) {
			return false, fmt.Errorf("vendor %s is not supported", cr.Spec.NicSelector.Vendor)
		}
		if cr.Spec.NicSelector.DeviceID != "" {
			if !sriovnetworkv1.IsSupportedModel(cr.Spec.NicSelector.Vendor, cr.Spec.NicSelector.DeviceID) {
				return false, fmt.Errorf("vendor/device %s/%s is not supported", cr.Spec.NicSelector.Vendor, cr.Spec.NicSelector.DeviceID)
			}
		}
	} else if cr.Spec.NicSelector.DeviceID != "" {
		if !sriovnetworkv1.IsSupportedDevice(cr.Spec.NicSelector.DeviceID) {
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
	if strings.ToLower(cr.Spec.LinkType) == "ib" && !cr.Spec.IsRdma {
		return false, fmt.Errorf("'linkType: ib or IB' requires 'isRdma: true'; Set 'isRdma' to (bool)'true'")
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
	npList, err := snclient.SriovnetworkV1().SriovNetworkNodePolicies(namespace).List(context.Background(), metav1.ListOptions{})
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
			// validate current policy against policies in API (may not be converted to SriovNetworkNodeState yet)
			for _, np := range npList.Items {
				if np.GetName() != cr.GetName() && np.Selected(&node) {
					if ok, err := validatePolicyForNodePolicy(cr, &np); err != nil || !ok {
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
			if policy.Spec.NumVfs > iface.TotalVfs && iface.Vendor == IntelID {
				return false, fmt.Errorf("numVfs(%d) in CR %s exceed the maximum allowed value(%d)", policy.Spec.NumVfs, policy.GetName(), iface.TotalVfs)
			}
			if policy.Spec.NumVfs > MlxMaxVFs && iface.Vendor == MellanoxID {
				return false, fmt.Errorf("numVfs(%d) in CR %s exceed the maximum allowed value(%d)", policy.Spec.NumVfs, policy.GetName(), MlxMaxVFs)
			}
		}
	}
	return true, nil
}

func validatePolicyForNodePolicy(
	current *sriovnetworkv1.SriovNetworkNodePolicy,
	previous *sriovnetworkv1.SriovNetworkNodePolicy,
) (bool, error) {
	glog.V(2).Infof("validateConflictPolicy(): validate policy %s against policy %s",
		current.GetName(), previous.GetName())

	if current.GetName() == previous.GetName() {
		return true, nil
	}

	for _, curPf := range current.Spec.NicSelector.PfNames {
		curName, curRngSt, curRngEnd, err := sriovnetworkv1.ParsePFName(curPf)
		if err != nil {
			return false, fmt.Errorf("invalid PF name: %s", curPf)
		}
		for _, prePf := range previous.Spec.NicSelector.PfNames {
			// Not validate return err of ParsePFName for previous PF
			// since it should already be evaluated in previous run.
			preName, preRngSt, preRngEnd, _ := sriovnetworkv1.ParsePFName(prePf)
			if curName == preName {
				if curRngEnd < preRngSt || curRngSt > preRngEnd {
					return true, nil
				} else {
					return false, fmt.Errorf("VF index range in %s is overlapped with existing policy %s", curPf, previous.GetName())
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
	if !sriovnetworkv1.IsSupportedModel(iface.Vendor, iface.DeviceID) {
		return false
	}
	return true
}
