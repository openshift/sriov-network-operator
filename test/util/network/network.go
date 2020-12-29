package network

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	netattdefv1 "github.com/k8snetworkplumbingwg/network-attachment-definition-client/pkg/apis/k8s.cni.cncf.io/v1"
	sriovv1 "github.com/k8snetworkplumbingwg/sriov-network-operator/api/v1"
	testclient "github.com/k8snetworkplumbingwg/sriov-network-operator/test/util/client"
	k8sv1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Needed for parsing of podinfo
type Network struct {
	Interface string
	Ips       []string
}

type SriovNetworkOptions func(*sriovv1.SriovNetwork)

func CreateSriovNetwork(clientSet *testclient.ClientSet, intf *sriovv1.InterfaceExt, name string, namespace string, operatorNamespace string, resourceName string, ipam string, options ...SriovNetworkOptions) error {
	sriovNetwork := &sriovv1.SriovNetwork{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: operatorNamespace,
		},
		Spec: sriovv1.SriovNetworkSpec{
			ResourceName:     resourceName,
			IPAM:             ipam,
			NetworkNamespace: namespace,
			// Enable the linkState instead of auto so even if the PF is down we can still use the VF
			// for pod to pod connectivity tests in the same host
			LinkState: "enable",
		}}

	for _, o := range options {
		o(sriovNetwork)
	}

	// We need this to be able to run the connectivity checks on Mellanox cards
	if intf.DeviceID == "1015" {
		sriovNetwork.Spec.SpoofChk = "off"
	}

	err := clientSet.Create(context.Background(), sriovNetwork)
	return err
}

func CreateSriovPolicy(clientSet *testclient.ClientSet, generatedName string, operatorNamespace string, sriovDevice string, testNode string, numVfs int, resourceName string) (*sriovv1.SriovNetworkNodePolicy, error) {
	nodePolicy := &sriovv1.SriovNetworkNodePolicy{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: generatedName,
			Namespace:    operatorNamespace,
		},
		Spec: sriovv1.SriovNetworkNodePolicySpec{
			NodeSelector: map[string]string{
				"kubernetes.io/hostname": testNode,
			},
			NumVfs:       numVfs,
			ResourceName: resourceName,
			Priority:     99,
			NicSelector: sriovv1.SriovNetworkNicSelector{
				PfNames: []string{sriovDevice},
			},
			DeviceType: "netdevice",
		},
	}
	err := clientSet.Create(context.Background(), nodePolicy)
	return nodePolicy, err
}

// GetNicsByPrefix returns a list of pod nic names, filtered by the given
// nic name prefix ifcPrefix
func GetNicsByPrefix(pod *k8sv1.Pod, ifcPrefix string) ([]string, error) {
	var nets []Network
	nics := []string{}
	err := json.Unmarshal([]byte(pod.ObjectMeta.Annotations["k8s.v1.cni.cncf.io/networks-status"]), &nets)
	if err != nil {
		return nil, err
	}
	for _, net := range nets {
		if strings.Index(net.Interface, ifcPrefix) == 0 {
			nics = append(nics, net.Interface)
		}
	}
	return nics, nil
}

// GetSriovNicIPs returns the list of ip addresses related to the given
// interface name for the given pod.
func GetSriovNicIPs(pod *k8sv1.Pod, ifcName string) ([]string, error) {
	var nets []Network
	err := json.Unmarshal([]byte(pod.ObjectMeta.Annotations["k8s.v1.cni.cncf.io/networks-status"]), &nets)
	if err != nil {
		return nil, err
	}
	for _, net := range nets {
		if net.Interface != ifcName {
			continue
		}
		return net.Ips, nil
	}
	return nil, nil
}

// Return a definition of a macvlan NetworkAttachmentDefinition
// name name
// namespace namespace
func CreateMacvlanNetworkAttachmentDefinition(name string, namespace string) netattdefv1.NetworkAttachmentDefinition {
	return netattdefv1.NetworkAttachmentDefinition{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: netattdefv1.NetworkAttachmentDefinitionSpec{
			Config: fmt.Sprintf(`{
				"cniVersion": "0.3.0",
				"type": "macvlan",
				"mode": "bridge",
				"ipam": {
				  "type": "host-local",
				  "subnet": "10.1.1.0/24",
				  "rangeStart": "10.1.1.100",
				  "rangeEnd": "10.1.1.200",
				  "routes": [
					{ "dst": "0.0.0.0/0" }
				  ],
				  "gateway": "10.1.1.1"
				}
			  }`),
		},
	}
}
