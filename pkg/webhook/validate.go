package webhook

import (
	"fmt"

	"github.com/golang/glog"
	"k8s.io/apimachinery/pkg/labels"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	sriovnetworkv1 "github.com/openshift/sriov-network-operator/pkg/apis/sriovnetwork/v1"

)
const (
	IntelID = "8086"
)

var (
	nodesSelected bool
	interfaceSelected  bool
)

func validateSriovNetworkNodePolicy(cr *sriovnetworkv1.SriovNetworkNodePolicy) (bool, error) {
	glog.V(2).Infof("validateSriovNetworkNodePolicy: %v", cr)
	admit := true 

	if cr.GetName() == "default" {
		// skip the default policy
		return admit, nil
	}
	if cr.Spec.NicSelector.Vendor == "" && cr.Spec.NicSelector.DeviceID == "" && len(cr.Spec.NicSelector.PfNames) == 0{
		return false, fmt.Errorf("at least one of these parameters (Vendor, DeviceID or PfNames) has to be defined in nicSelector in CR %s", cr.GetName())
	}

	admit, err := dynamicValidateSriovNetworkNodePolicy(cr)
	if err != nil {
		return admit, err

	}
	return admit, nil
}

func dynamicValidateSriovNetworkNodePolicy(cr *sriovnetworkv1.SriovNetworkNodePolicy) (bool, error) {
	nodesSelected = false
	interfaceSelected = false

	nodeList, err := kubeclient.CoreV1().Nodes().List(metav1.ListOptions{
		LabelSelector: labels.Set(cr.Spec.NodeSelector).String(),
	})
	nsList, err := snclient.SriovnetworkV1().SriovNetworkNodeStates(namespace).List(metav1.ListOptions{})
	if err != nil {
		return false, err
	}
	for _, node := range nodeList.Items {
		if cr.Selected(&node) {
			nodesSelected = true
			for _, ns := range nsList.Items {
				if ns.GetName() == node.GetName() {
					if ok, err := validatePolicyForNodeState(cr, &ns); err != nil || !ok{
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
		return false, fmt.Errorf("no matched NIC is selected by the nicSelector in CR %s", cr.GetName())
	}

	return true, nil
}

func validatePolicyForNodeState(policy *sriovnetworkv1.SriovNetworkNodePolicy, state *sriovnetworkv1.SriovNetworkNodeState) (bool, error) {
	glog.V(2).Infof("validatePolicyForNodeState(): validate policy %s for node %s.", policy.GetName(), state.GetName())
	for _, iface := range state.Status.Interfaces {
		if policy.Spec.Selected(&iface) {
			interfaceSelected = true
			if policy.Spec.NumVfs > iface.TotalVfs && iface.Vendor == IntelID{
				return false, fmt.Errorf("numVfs(%d) in CR %s exceed the maximum allowed value", policy.Spec.NumVfs, policy.GetName())
			}
		}
	}
	return true, nil
}
