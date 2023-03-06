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

func defineSriovPolicy(generatedName string, operatorNamespace string, sriovDevice string, testNode string, numVfs int, resourceName string, deviceType string, options ...func(*sriovv1.SriovNetworkNodePolicy)) *sriovv1.SriovNetworkNodePolicy {
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
			DeviceType: deviceType,
		},
	}
	for _, o := range options {
		o(nodePolicy)
	}
	return nodePolicy
}

// CreateSriovPolicy creates a SriovNetworkNodePolicy and returns it
func CreateSriovPolicy(clientSet *testclient.ClientSet, generatedName string, operatorNamespace string, sriovDevice string, testNode string, numVfs int, resourceName string, deviceType string, options ...func(*sriovv1.SriovNetworkNodePolicy)) (*sriovv1.SriovNetworkNodePolicy, error) {
	nodePolicy := defineSriovPolicy(generatedName, operatorNamespace, sriovDevice, testNode, numVfs, resourceName, deviceType, options...)
	err := clientSet.Create(context.Background(), nodePolicy)
	return nodePolicy, err
}

// GetNicsByPrefix returns a list of pod nic names, filtered by the given
// nic name prefix ifcPrefix
func GetNicsByPrefix(pod *k8sv1.Pod, ifcPrefix string) ([]string, error) {
	var nets []Network
	nics := []string{}
	err := json.Unmarshal([]byte(pod.ObjectMeta.Annotations[netattdefv1.NetworkStatusAnnot]), &nets)
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
	networksStatus, ok := pod.ObjectMeta.Annotations[netattdefv1.NetworkStatusAnnot]
	if !ok {
		return nil, fmt.Errorf("pod [%s] has no annotation `%s`", netattdefv1.NetworkStatusAnnot, pod.Name)
	}

	var nets []Network
	err := json.Unmarshal([]byte(networksStatus), &nets)
	if err != nil {
		return nil, fmt.Errorf("can't unmarshal annotation `%s`: %w", netattdefv1.NetworkStatusAnnot, err)
	}
	for _, net := range nets {
		if net.Interface != ifcName {
			continue
		}
		return net.Ips, nil
	}

	return nil, fmt.Errorf("interface [%s] not found in pod annotation", ifcName)
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
			Config: `{
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
			  }`,
		},
	}
}
