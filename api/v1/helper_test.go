package v1_test

import (
	"bufio"
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	intstrutil "k8s.io/apimachinery/pkg/util/intstr"

	v1 "github.com/k8snetworkplumbingwg/sriov-network-operator/api/v1"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/consts"
)

var update = flag.Bool("updategolden", false, "update .golden files")

func init() {
	// when running go tests path is local to the file, overriding it.
	v1.ManifestsPath = "../../bindata/manifests/cni-config"
}

func newNodeState() *v1.SriovNetworkNodeState {
	return &v1.SriovNetworkNodeState{
		Spec: v1.SriovNetworkNodeStateSpec{},
		Status: v1.SriovNetworkNodeStateStatus{
			Interfaces: []v1.InterfaceExt{
				{
					VFs: []v1.VirtualFunction{
						{},
					},
					DeviceID:   "158b",
					Driver:     "i40e",
					Mtu:        1500,
					Name:       "ens803f0",
					PciAddress: "0000:86:00.0",
					Vendor:     "8086",
					NumVfs:     4,
					TotalVfs:   64,
				},
				{
					VFs: []v1.VirtualFunction{
						{},
					},
					DeviceID:   "158b",
					Driver:     "i40e",
					Mtu:        1500,
					Name:       "ens803f1",
					PciAddress: "0000:86:00.1",
					Vendor:     "8086",
					NumVfs:     4,
					TotalVfs:   64,
				},
				{
					VFs: []v1.VirtualFunction{
						{},
					},
					DeviceID:   "1015",
					Driver:     "i40e",
					Mtu:        1500,
					Name:       "ens803f2",
					PciAddress: "0000:86:00.2",
					Vendor:     "8086",
					NumVfs:     4,
					TotalVfs:   64,
				},
			},
		},
	}
}

func newNodePolicy() *v1.SriovNetworkNodePolicy {
	return &v1.SriovNetworkNodePolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name: "p1",
		},
		Spec: v1.SriovNetworkNodePolicySpec{
			DeviceType: consts.DeviceTypeNetDevice,
			NicSelector: v1.SriovNetworkNicSelector{
				PfNames:     []string{"ens803f1"},
				RootDevices: []string{"0000:86:00.1"},
				Vendor:      "8086",
			},
			NodeSelector: map[string]string{
				"feature.node.kubernetes.io/network-sriov.capable": "true",
			},
			NumVfs:       2,
			Priority:     99,
			ResourceName: "p1res",
		},
	}
}

func newVirtioVdpaNodePolicy() *v1.SriovNetworkNodePolicy {
	return &v1.SriovNetworkNodePolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name: "p1",
		},
		Spec: v1.SriovNetworkNodePolicySpec{
			DeviceType: consts.DeviceTypeNetDevice,
			VdpaType:   consts.VdpaTypeVirtio,
			NicSelector: v1.SriovNetworkNicSelector{
				PfNames:     []string{"ens803f1#2-3"},
				RootDevices: []string{"0000:86:00.1"},
				Vendor:      "8086",
			},
			NodeSelector: map[string]string{
				"feature.node.kubernetes.io/network-sriov.capable": "true",
			},
			NumVfs:       4,
			Priority:     99,
			ResourceName: "virtiovdpa",
		},
	}
}

func newVhostVdpaNodePolicy() *v1.SriovNetworkNodePolicy {
	return &v1.SriovNetworkNodePolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name: "p1",
		},
		Spec: v1.SriovNetworkNodePolicySpec{
			DeviceType: consts.DeviceTypeNetDevice,
			VdpaType:   consts.VdpaTypeVhost,
			NicSelector: v1.SriovNetworkNicSelector{
				PfNames:     []string{"ens803f1"},
				RootDevices: []string{"0000:86:00.1"},
				Vendor:      "8086",
			},
			NodeSelector: map[string]string{
				"feature.node.kubernetes.io/network-sriov.capable": "true",
			},
			NumVfs:       2,
			Priority:     99,
			ResourceName: "vhostvdpa",
		},
	}
}

func TestRendering(t *testing.T) {
	testtable := []struct {
		tname   string
		network v1.SriovNetwork
	}{
		{
			tname: "simple",
			network: v1.SriovNetwork{
				TypeMeta:   metav1.TypeMeta{APIVersion: v1.GroupVersion.String(), Kind: "SriovNetwork"},
				ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "test"},
				Spec: v1.SriovNetworkSpec{
					NetworkNamespace: "testnamespace",
					ResourceName:     "testresource",
				},
			},
		},
		{
			tname: "chained",
			network: v1.SriovNetwork{
				TypeMeta:   metav1.TypeMeta{APIVersion: v1.GroupVersion.String(), Kind: "SriovNetwork"},
				ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "test"},
				Spec: v1.SriovNetworkSpec{
					NetworkNamespace: "testnamespace",
					ResourceName:     "testresource",
					MetaPluginsConfig: `
					{
						"type": "vrf",
						"vrfname": "blue"
					}
					`,
				},
			},
		},
	}
	for _, tc := range testtable {
		t.Run(tc.tname, func(t *testing.T) {
			var b bytes.Buffer
			w := bufio.NewWriter(&b)
			rendered, err := tc.network.RenderNetAttDef()
			if err != nil {
				t.Fatal("failed rendering network attachment definition", err)
			}
			encoder := json.NewEncoder(w)
			encoder.SetIndent("", "  ")
			encoder.Encode(rendered)
			w.Flush()
			gp := filepath.Join("testdata", filepath.FromSlash(t.Name())+".golden")
			if *update {
				t.Log("update golden file")
				if err := os.WriteFile(gp, b.Bytes(), 0644); err != nil {
					t.Fatalf("failed to update golden file: %s", err)
				}
			}
			g, err := os.ReadFile(gp)
			if err != nil {
				t.Fatalf("failed reading .golden: %s", err)
			}

			assert.Equal(t, string(g), b.String(), "bytes do not match .golden file [%s]", gp)
		})
	}
}

func TestIBRendering(t *testing.T) {
	testtable := []struct {
		tname   string
		network v1.SriovIBNetwork
	}{
		{
			tname: "simpleib",
			network: v1.SriovIBNetwork{
				TypeMeta:   metav1.TypeMeta{APIVersion: v1.GroupVersion.String(), Kind: "SriovIBNetwork"},
				ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "test"},
				Spec: v1.SriovIBNetworkSpec{
					NetworkNamespace: "testnamespace",
					ResourceName:     "testresource",
					Capabilities:     "foo",
				},
			},
		},
	}
	for _, tc := range testtable {
		t.Run(tc.tname, func(t *testing.T) {
			var b bytes.Buffer
			w := bufio.NewWriter(&b)
			rendered, err := tc.network.RenderNetAttDef()
			if err != nil {
				t.Fatal("failed rendering network attachment definition", err)
			}
			encoder := json.NewEncoder(w)
			encoder.SetIndent("", "  ")
			encoder.Encode(rendered)
			w.Flush()
			gp := filepath.Join("testdata", filepath.FromSlash(t.Name())+".golden")
			if *update {
				t.Log("update golden file")
				if err := os.WriteFile(gp, b.Bytes(), 0644); err != nil {
					t.Fatalf("failed to update golden file: %s", err)
				}
			}
			g, err := os.ReadFile(gp)
			if err != nil {
				t.Fatalf("failed reading .golden: %s", err)
			}

			assert.Equal(t, string(g), b.String(), "bytes do not match .golden file [%s]", gp)
		})
	}
}

func TestOVSRendering(t *testing.T) {
	testtable := []struct {
		tname   string
		network v1.OVSNetwork
	}{
		{
			tname: "simpleovs",
			network: v1.OVSNetwork{
				TypeMeta:   metav1.TypeMeta{APIVersion: v1.GroupVersion.String(), Kind: "OVSNetwork"},
				ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "test"},
				Spec: v1.OVSNetworkSpec{
					NetworkNamespace: "testnamespace",
					ResourceName:     "testresource",
				},
			},
		},
		{
			tname: "chained",
			network: v1.OVSNetwork{
				TypeMeta:   metav1.TypeMeta{APIVersion: v1.GroupVersion.String(), Kind: "OVSNetwork"},
				ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "test"},
				Spec: v1.OVSNetworkSpec{
					NetworkNamespace: "testnamespace",
					ResourceName:     "testresource",
					MTU:              1500,
					MetaPluginsConfig: `
					{
						"type": "vrf",
						"vrfname": "blue"
					}
					`,
				},
			},
		},
		{
			tname: "complexconf",
			network: v1.OVSNetwork{
				TypeMeta:   metav1.TypeMeta{APIVersion: v1.GroupVersion.String(), Kind: "OVSNetwork"},
				ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "test"},
				Spec: v1.OVSNetworkSpec{
					NetworkNamespace: "testnamespace",
					ResourceName:     "testresource",
					Capabilities:     `{"foo": "bar"}`,
					Bridge:           "test",
					Vlan:             100,
					MTU:              1500,
					Trunk: []*v1.TrunkConfig{
						{
							ID: func(i uint) *uint { return &i }(120)},
						{
							MinID: func(i uint) *uint { return &i }(500),
							MaxID: func(i uint) *uint { return &i }(550)},
					},
					InterfaceType: "netdev",
					IPAM:          `{"type": "foo"}`,
				},
			},
		},
	}
	for _, tc := range testtable {
		t.Run(tc.tname, func(t *testing.T) {
			var b bytes.Buffer
			w := bufio.NewWriter(&b)
			rendered, err := tc.network.RenderNetAttDef()
			if err != nil {
				t.Fatal("failed rendering network attachment definition", err)
			}
			encoder := json.NewEncoder(w)
			encoder.SetIndent("", "  ")
			encoder.Encode(rendered)
			w.Flush()
			gp := filepath.Join("testdata", filepath.FromSlash(t.Name())+".golden")
			if *update {
				t.Log("update golden file")
				if err := os.WriteFile(gp, b.Bytes(), 0644); err != nil {
					t.Fatalf("failed to update golden file: %s", err)
				}
			}
			g, err := os.ReadFile(gp)
			if err != nil {
				t.Fatalf("failed reading .golden: %s", err)
			}

			assert.Equal(t, string(g), b.String(), "bytes do not match .golden file [%s]", gp)
		})
	}
}

func TestSriovNetworkNodePolicyApply(t *testing.T) {
	testtable := []struct {
		tname              string
		currentState       *v1.SriovNetworkNodeState
		policy             *v1.SriovNetworkNodePolicy
		expectedInterfaces v1.Interfaces
		equalP             bool
		expectedErr        bool
	}{
		{
			tname:        "starting config",
			currentState: newNodeState(),
			policy:       newNodePolicy(),
			equalP:       false,
			expectedInterfaces: []v1.Interface{
				{
					Name:       "ens803f1",
					NumVfs:     2,
					PciAddress: "0000:86:00.1",
					VfGroups: []v1.VfGroup{
						{
							DeviceType:   consts.DeviceTypeNetDevice,
							ResourceName: "p1res",
							VfRange:      "0-1",
							PolicyName:   "p1",
						},
					},
				},
			},
		},
		{
			tname: "one policy present different pf",
			currentState: func() *v1.SriovNetworkNodeState {
				st := newNodeState()
				st.Spec.Interfaces = []v1.Interface{
					{
						Name:       "ens803f0",
						NumVfs:     2,
						PciAddress: "0000:86:00.0",
						VfGroups: []v1.VfGroup{
							{
								DeviceType:   consts.DeviceTypeNetDevice,
								ResourceName: "prevres",
								VfRange:      "0-1",
								PolicyName:   "p2",
							},
						},
					},
				}
				return st
			}(),
			policy: newNodePolicy(),
			equalP: false,
			expectedInterfaces: []v1.Interface{
				{
					Name:       "ens803f0",
					NumVfs:     2,
					PciAddress: "0000:86:00.0",
					VfGroups: []v1.VfGroup{
						{
							DeviceType:   consts.DeviceTypeNetDevice,
							ResourceName: "prevres",
							VfRange:      "0-1",
							PolicyName:   "p2",
						},
					},
				},
				{
					Name:       "ens803f1",
					NumVfs:     2,
					PciAddress: "0000:86:00.1",
					VfGroups: []v1.VfGroup{
						{
							DeviceType:   consts.DeviceTypeNetDevice,
							ResourceName: "p1res",
							VfRange:      "0-1",
							PolicyName:   "p1",
						},
					},
				},
			},
		},
		{
			// policy overwrites (applied last has higher priority) what is inside the
			// SriovNetworkNodeState
			tname: "one policy present same pf different priority",
			currentState: func() *v1.SriovNetworkNodeState {
				st := newNodeState()
				st.Spec.Interfaces = []v1.Interface{
					{
						Name:       "ens803f1",
						NumVfs:     3,
						PciAddress: "0000:86:00.1",
						VfGroups: []v1.VfGroup{
							{
								DeviceType:   consts.DeviceTypeVfioPci,
								ResourceName: "vfiores",
								VfRange:      "0-1",
								PolicyName:   "p2",
							},
						},
					},
				}
				return st
			}(),
			policy: newNodePolicy(),
			equalP: false,
			expectedInterfaces: []v1.Interface{
				{
					Name:       "ens803f1",
					NumVfs:     2,
					PciAddress: "0000:86:00.1",
					VfGroups: []v1.VfGroup{
						{
							DeviceType:   consts.DeviceTypeNetDevice,
							ResourceName: "p1res",
							VfRange:      "0-1",
							PolicyName:   "p1",
						},
					},
				},
			},
		},
		{
			// policy with same priority, but there is VfRange overlap so
			// only the last applied stays, NumVfs and MTU is still merged
			tname: "one policy present same pf same priority",
			currentState: func() *v1.SriovNetworkNodeState {
				st := newNodeState()
				st.Spec.Interfaces = []v1.Interface{
					{
						Name:       "ens803f1",
						NumVfs:     3,
						PciAddress: "0000:86:00.1",
						Mtu:        2000,
						VfGroups: []v1.VfGroup{
							{
								DeviceType:   consts.DeviceTypeVfioPci,
								ResourceName: "vfiores",
								VfRange:      "0-1",
								PolicyName:   "p2",
							},
						},
					},
				}
				return st
			}(),
			policy: newNodePolicy(),
			equalP: true,
			expectedInterfaces: []v1.Interface{
				{
					Mtu:        2000,
					Name:       "ens803f1",
					NumVfs:     3,
					PciAddress: "0000:86:00.1",
					VfGroups: []v1.VfGroup{
						{
							DeviceType:   consts.DeviceTypeNetDevice,
							ResourceName: "p1res",
							VfRange:      "0-1",
							PolicyName:   "p1",
						},
					},
				},
			},
		},
		{
			// policy with same priority, VfRange's do not overlap so all is merged
			tname: "one policy present same pf same priority partitioning",
			currentState: func() *v1.SriovNetworkNodeState {
				st := newNodeState()
				st.Spec.Interfaces = []v1.Interface{
					{
						Name:       "ens803f1",
						NumVfs:     5,
						PciAddress: "0000:86:00.1",
						VfGroups: []v1.VfGroup{
							{
								DeviceType:   consts.DeviceTypeVfioPci,
								ResourceName: "vfiores",
								VfRange:      "2-4",
								PolicyName:   "p2",
							},
						},
					},
				}
				return st
			}(),
			policy: newNodePolicy(),
			equalP: true,
			expectedInterfaces: []v1.Interface{
				{
					Name:       "ens803f1",
					NumVfs:     5,
					PciAddress: "0000:86:00.1",
					VfGroups: []v1.VfGroup{
						{
							DeviceType:   consts.DeviceTypeNetDevice,
							ResourceName: "p1res",
							VfRange:      "0-1",
							PolicyName:   "p1",
						},
						{
							DeviceType:   consts.DeviceTypeVfioPci,
							ResourceName: "vfiores",
							VfRange:      "2-4",
							PolicyName:   "p2",
						},
					},
				},
			},
		},
		{
			// vdpa policy with same priority (both virtio and vhost), VfRange's do not overlap so all is merged
			tname: "one vdpa policy present same pf same priority partitioning",
			currentState: func() *v1.SriovNetworkNodeState {
				st := newNodeState()
				st.Spec.Interfaces = []v1.Interface{
					{
						Name:       "ens803f1",
						NumVfs:     4,
						PciAddress: "0000:86:00.1",
						VfGroups: []v1.VfGroup{
							{
								DeviceType:   consts.DeviceTypeNetDevice,
								VdpaType:     consts.VdpaTypeVhost,
								ResourceName: "vhostvdpa",
								VfRange:      "0-1",
								PolicyName:   "p2",
							},
						},
					},
				}
				return st
			}(),
			policy: newVirtioVdpaNodePolicy(),
			equalP: true,
			expectedInterfaces: []v1.Interface{
				{
					Name:       "ens803f1",
					NumVfs:     4,
					PciAddress: "0000:86:00.1",
					VfGroups: []v1.VfGroup{
						{
							DeviceType:   consts.DeviceTypeNetDevice,
							VdpaType:     consts.VdpaTypeVirtio,
							ResourceName: "virtiovdpa",
							VfRange:      "2-3",
							PolicyName:   "p1",
						},
						{
							DeviceType:   consts.DeviceTypeNetDevice,
							VdpaType:     consts.VdpaTypeVhost,
							ResourceName: "vhostvdpa",
							VfRange:      "0-1",
							PolicyName:   "p2",
						},
					},
				},
			},
		},
		{
			// policy with same priority that overwrites the 2 present groups in
			// SriovNetworkNodeState because they overlap VfRange
			tname: "two policy present same pf same priority overlap",
			currentState: func() *v1.SriovNetworkNodeState {
				st := newNodeState()
				st.Spec.Interfaces = []v1.Interface{
					{
						Name:       "ens803f1",
						NumVfs:     2,
						PciAddress: "0000:86:00.1",
						VfGroups: []v1.VfGroup{
							{
								DeviceType:   consts.DeviceTypeVfioPci,
								ResourceName: "vfiores1",
								VfRange:      "0-0",
								PolicyName:   "p2",
							},
							{
								DeviceType:   consts.DeviceTypeVfioPci,
								ResourceName: "vfiores2",
								VfRange:      "1-1",
								PolicyName:   "p3",
							},
						},
					},
				}
				return st
			}(),
			policy: newNodePolicy(),
			equalP: true,
			expectedInterfaces: []v1.Interface{
				{
					Name:       "ens803f1",
					NumVfs:     2,
					PciAddress: "0000:86:00.1",
					VfGroups: []v1.VfGroup{
						{
							DeviceType:   consts.DeviceTypeNetDevice,
							ResourceName: "p1res",
							VfRange:      "0-1",
							PolicyName:   "p1",
						},
					},
				},
			},
		},
		{
			// policy with same priority that overwrites the present group in
			// SriovNetworkNodeState because of same ResourceName
			tname: "one policy present same pf same priority same ResourceName",
			currentState: func() *v1.SriovNetworkNodeState {
				st := newNodeState()
				st.Spec.Interfaces = []v1.Interface{
					{
						Name:       "ens803f1",
						NumVfs:     4,
						PciAddress: "0000:86:00.1",
						VfGroups: []v1.VfGroup{
							{
								DeviceType:   consts.DeviceTypeVfioPci,
								ResourceName: "p1res",
								VfRange:      "2-3",
								PolicyName:   "p2",
							},
						},
					},
				}
				return st
			}(),
			policy: newNodePolicy(),
			equalP: true,
			expectedInterfaces: []v1.Interface{
				{
					Name:       "ens803f1",
					NumVfs:     4,
					PciAddress: "0000:86:00.1",
					VfGroups: []v1.VfGroup{
						{
							DeviceType:   consts.DeviceTypeNetDevice,
							ResourceName: "p1res",
							VfRange:      "0-1",
							PolicyName:   "p1",
						},
					},
				},
			},
		},
		{
			// policy with diff priority that have non-overlapping VF groups will be
			// merged
			tname: "one policy present same pf diff priority no overlap VFs",
			currentState: func() *v1.SriovNetworkNodeState {
				st := newNodeState()
				st.Spec.Interfaces = []v1.Interface{
					{
						Name:       "ens803f1",
						NumVfs:     4,
						PciAddress: "0000:86:00.1",
						VfGroups: []v1.VfGroup{
							{
								DeviceType:   consts.DeviceTypeVfioPci,
								ResourceName: "p2res",
								VfRange:      "2-3",
								PolicyName:   "p2",
							},
						},
					},
				}
				return st
			}(),
			policy: newNodePolicy(),
			equalP: false,
			expectedInterfaces: []v1.Interface{
				{
					Name:       "ens803f1",
					NumVfs:     4,
					PciAddress: "0000:86:00.1",
					VfGroups: []v1.VfGroup{
						{
							DeviceType:   consts.DeviceTypeNetDevice,
							ResourceName: "p1res",
							VfRange:      "0-1",
							PolicyName:   "p1",
						},
						{
							DeviceType:   consts.DeviceTypeVfioPci,
							ResourceName: "p2res",
							VfRange:      "2-3",
							PolicyName:   "p2",
						},
					},
				},
			},
		},
		{
			tname:        "no selectors",
			currentState: newNodeState(),
			policy: &v1.SriovNetworkNodePolicy{
				ObjectMeta: metav1.ObjectMeta{
					Name: "p1",
				},
				Spec: v1.SriovNetworkNodePolicySpec{
					DeviceType: consts.DeviceTypeNetDevice,
					NicSelector: v1.SriovNetworkNicSelector{
						PfNames:     []string{},
						RootDevices: []string{},
					},
					NodeSelector: map[string]string{
						"feature.node.kubernetes.io/network-sriov.capable": "true",
					},
					NumVfs:       2,
					Priority:     99,
					ResourceName: "p1res",
				},
			},
			equalP:             false,
			expectedInterfaces: nil,
		},
		{
			tname:        "bad pf partition",
			currentState: newNodeState(),
			policy: &v1.SriovNetworkNodePolicy{
				ObjectMeta: metav1.ObjectMeta{
					Name: "p1",
				},
				Spec: v1.SriovNetworkNodePolicySpec{
					DeviceType: consts.DeviceTypeNetDevice,
					NicSelector: v1.SriovNetworkNicSelector{
						PfNames:     []string{"ens803f0#a-c"},
						RootDevices: []string{},
					},
					NodeSelector: map[string]string{
						"feature.node.kubernetes.io/network-sriov.capable": "true",
					},
					NumVfs:       2,
					Priority:     99,
					ResourceName: "p1res",
				},
			},
			equalP:             false,
			expectedInterfaces: nil,
			expectedErr:        true,
		},
	}
	for _, tc := range testtable {
		t.Run(tc.tname, func(t *testing.T) {
			err := tc.policy.Apply(tc.currentState, tc.equalP)
			if tc.expectedErr && err == nil {
				t.Errorf("Apply expecting error.")
			} else if !tc.expectedErr && err != nil {
				t.Errorf("Apply error:\n%s", err)
			}
			if diff := cmp.Diff(tc.expectedInterfaces, tc.currentState.Spec.Interfaces); diff != "" {
				t.Errorf("SriovNetworkNodeState spec diff (-want +got):\n%s", diff)
			}
		})
	}
}

func TestVirtioVdpaNodePolicyApply(t *testing.T) {
	testtable := []struct {
		tname              string
		currentState       *v1.SriovNetworkNodeState
		policy             *v1.SriovNetworkNodePolicy
		expectedInterfaces v1.Interfaces
		equalP             bool
		expectedErr        bool
	}{
		{
			tname:        "virtio/vdpa configuration",
			currentState: newNodeState(),
			policy:       newVirtioVdpaNodePolicy(),
			equalP:       false,
			expectedInterfaces: []v1.Interface{
				{
					Name:       "ens803f1",
					NumVfs:     4,
					PciAddress: "0000:86:00.1",
					VfGroups: []v1.VfGroup{
						{
							DeviceType:   consts.DeviceTypeNetDevice,
							VdpaType:     consts.VdpaTypeVirtio,
							ResourceName: "virtiovdpa",
							VfRange:      "2-3",
							PolicyName:   "p1",
						},
					},
				},
			},
		},
	}
	for _, tc := range testtable {
		t.Run(tc.tname, func(t *testing.T) {
			err := tc.policy.Apply(tc.currentState, tc.equalP)
			if tc.expectedErr && err == nil {
				t.Errorf("Apply expecting error.")
			} else if !tc.expectedErr && err != nil {
				t.Errorf("Apply error:\n%s", err)
			}
			if diff := cmp.Diff(tc.expectedInterfaces, tc.currentState.Spec.Interfaces); diff != "" {
				t.Errorf("SriovNetworkNodeState spec diff (-want +got):\n%s", diff)
			}
		})
	}
}

func TestVhostVdpaNodePolicyApply(t *testing.T) {
	testtable := []struct {
		tname              string
		currentState       *v1.SriovNetworkNodeState
		policy             *v1.SriovNetworkNodePolicy
		expectedInterfaces v1.Interfaces
		equalP             bool
		expectedErr        bool
	}{
		{
			tname:        "vhost/vdpa configuration",
			currentState: newNodeState(),
			policy:       newVhostVdpaNodePolicy(),
			equalP:       false,
			expectedInterfaces: []v1.Interface{
				{
					Name:       "ens803f1",
					NumVfs:     2,
					PciAddress: "0000:86:00.1",
					VfGroups: []v1.VfGroup{
						{
							DeviceType:   consts.DeviceTypeNetDevice,
							VdpaType:     consts.VdpaTypeVhost,
							ResourceName: "vhostvdpa",
							VfRange:      "0-1",
							PolicyName:   "p1",
						},
					},
				},
			},
		},
	}
	for _, tc := range testtable {
		t.Run(tc.tname, func(t *testing.T) {
			err := tc.policy.Apply(tc.currentState, tc.equalP)
			if tc.expectedErr && err == nil {
				t.Errorf("Apply expecting error.")
			} else if !tc.expectedErr && err != nil {
				t.Errorf("Apply error:\n%s", err)
			}
			if diff := cmp.Diff(tc.expectedInterfaces, tc.currentState.Spec.Interfaces); diff != "" {
				t.Errorf("SriovNetworkNodeState spec diff (-want +got):\n%s", diff)
			}
		})
	}
}

func TestGetEswitchModeFromSpec(t *testing.T) {
	testtable := []struct {
		tname          string
		spec           *v1.Interface
		expectedResult string
	}{
		{
			tname:          "set to legacy",
			spec:           &v1.Interface{EswitchMode: v1.ESwithModeLegacy},
			expectedResult: v1.ESwithModeLegacy,
		},
		{
			tname:          "set to switchdev",
			spec:           &v1.Interface{EswitchMode: v1.ESwithModeSwitchDev},
			expectedResult: v1.ESwithModeSwitchDev,
		},
		{
			tname:          "not set",
			spec:           &v1.Interface{},
			expectedResult: v1.ESwithModeLegacy,
		},
	}
	for _, tc := range testtable {
		t.Run(tc.tname, func(t *testing.T) {
			result := v1.GetEswitchModeFromSpec(tc.spec)
			if diff := cmp.Diff(tc.expectedResult, result); diff != "" {
				t.Errorf("unexpected result (-want +got):\n%s", diff)
			}
		})
	}
}

func TestGetEswitchModeFromStatus(t *testing.T) {
	testtable := []struct {
		tname          string
		spec           *v1.InterfaceExt
		expectedResult string
	}{
		{
			tname:          "set to legacy",
			spec:           &v1.InterfaceExt{EswitchMode: v1.ESwithModeLegacy},
			expectedResult: v1.ESwithModeLegacy,
		},
		{
			tname:          "set to switchdev",
			spec:           &v1.InterfaceExt{EswitchMode: v1.ESwithModeSwitchDev},
			expectedResult: v1.ESwithModeSwitchDev,
		},
		{
			tname:          "not set",
			spec:           &v1.InterfaceExt{},
			expectedResult: v1.ESwithModeLegacy,
		},
	}
	for _, tc := range testtable {
		t.Run(tc.tname, func(t *testing.T) {
			result := v1.GetEswitchModeFromStatus(tc.spec)
			if diff := cmp.Diff(tc.expectedResult, result); diff != "" {
				t.Errorf("unexpected result (-want +got):\n%s", diff)
			}
		})
	}
}

func TestSriovNetworkPoolConfig_MaxUnavailable(t *testing.T) {
	testtable := []struct {
		tname       string
		maxUn       intstrutil.IntOrString
		maxUnNil    bool
		numOfNodes  int
		expectedNum int
		expectedErr bool
	}{
		{
			tname:       "valid int MaxUnavailable",
			maxUn:       intstrutil.FromInt32(1),
			numOfNodes:  1,
			expectedNum: 1,
			expectedErr: false,
		},
		{
			tname:       "invalid string MaxUnavailable",
			maxUn:       intstrutil.FromString("bla"),
			numOfNodes:  1,
			expectedNum: 0,
			expectedErr: true,
		},
		{
			tname:       "valid string percentage MaxUnavailable",
			maxUn:       intstrutil.FromString("33%"),
			numOfNodes:  10,
			expectedNum: 3,
			expectedErr: false,
		},
		{
			tname:       "negative int MaxUnavailable",
			maxUn:       intstrutil.FromInt32(-1),
			numOfNodes:  10,
			expectedNum: 0,
			expectedErr: true,
		},
		{
			tname:       "out of range int MaxUnavailable",
			maxUn:       intstrutil.FromString("99999999999999999."),
			numOfNodes:  10,
			expectedNum: 0,
			expectedErr: true,
		},
		{
			tname:       "over 100%",
			maxUn:       intstrutil.FromString("10000%"),
			numOfNodes:  10,
			expectedNum: 0,
			expectedErr: true,
		},
		{
			tname:       "parallel",
			maxUn:       intstrutil.FromInt32(-1),
			maxUnNil:    true,
			numOfNodes:  10,
			expectedNum: -1,
			expectedErr: false,
		},
		{
			tname:       "zero",
			maxUn:       intstrutil.FromString("30%"),
			maxUnNil:    false,
			numOfNodes:  1,
			expectedNum: 0,
			expectedErr: false,
		},
	}
	for _, tc := range testtable {
		t.Run(tc.tname, func(t *testing.T) {
			pool := v1.SriovNetworkPoolConfig{
				Spec: v1.SriovNetworkPoolConfigSpec{
					MaxUnavailable: &tc.maxUn,
				},
			}

			if tc.maxUnNil {
				pool.Spec.MaxUnavailable = nil
			}

			num, err := pool.MaxUnavailable(tc.numOfNodes)
			if tc.expectedErr && err == nil {
				t.Errorf("MaxUnavailable expecting error.")
			} else if !tc.expectedErr && err != nil {
				t.Errorf("MaxUnavailable error:\n%s", err)
			}

			if tc.expectedNum != num {
				t.Errorf("unexpected number of MaxUnavailable.")
			}
		})
	}
}

func TestNeedToUpdateSriov(t *testing.T) {
	type args struct {
		ifaceSpec   *v1.Interface
		ifaceStatus *v1.InterfaceExt
	}
	tests := []struct {
		name string
		args args
		want bool
	}{
		{
			name: "number of VFs changed",
			args: args{
				ifaceSpec:   &v1.Interface{NumVfs: 1},
				ifaceStatus: &v1.InterfaceExt{NumVfs: 0},
			},
			want: true,
		},
		{
			name: "no update",
			args: args{
				ifaceSpec:   &v1.Interface{NumVfs: 1},
				ifaceStatus: &v1.InterfaceExt{NumVfs: 1},
			},
			want: false,
		},
		{
			name: "vfio-pci VF is not configured for any group",
			args: args{
				ifaceSpec: &v1.Interface{
					NumVfs: 3,
					VfGroups: []v1.VfGroup{
						{
							VfRange:    "1-2",
							DeviceType: consts.DeviceTypeNetDevice,
						},
					},
				},
				ifaceStatus: &v1.InterfaceExt{
					NumVfs: 3,
					VFs: []v1.VirtualFunction{
						{
							VfID:   0,
							Driver: "vfio-pci",
						},
						{
							VfID:   1,
							Driver: "iavf",
						},
						{
							VfID:   2,
							Driver: "iavf",
						},
					},
				},
			},
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := v1.NeedToUpdateSriov(tt.args.ifaceSpec, tt.args.ifaceStatus); got != tt.want {
				t.Errorf("NeedToUpdateSriov() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSriovNetworkNodePolicyApplyBridgeConfig(t *testing.T) {
	testtable := []struct {
		tname           string
		currentState    *v1.SriovNetworkNodeState
		policy          *v1.SriovNetworkNodePolicy
		expectedBridges v1.Bridges
		expectedErr     bool
	}{
		{
			tname:        "no selectors",
			currentState: newNodeState(),
			policy: &v1.SriovNetworkNodePolicy{
				ObjectMeta: metav1.ObjectMeta{
					Name: "p1",
				},
				Spec: v1.SriovNetworkNodePolicySpec{
					DeviceType:  consts.DeviceTypeNetDevice,
					NicSelector: v1.SriovNetworkNicSelector{},
					NodeSelector: map[string]string{
						"feature.node.kubernetes.io/network-sriov.capable": "true",
					},
					NumVfs:       2,
					Priority:     99,
					EswitchMode:  "switchdev",
					ResourceName: "p1res",
					Bridge:       v1.Bridge{OVS: &v1.OVSConfig{}},
				},
			},
			expectedBridges: v1.Bridges{},
		},
		{
			tname:        "not switchdev config",
			currentState: newNodeState(),
			policy: &v1.SriovNetworkNodePolicy{
				ObjectMeta: metav1.ObjectMeta{
					Name: "p1",
				},
				Spec: v1.SriovNetworkNodePolicySpec{
					DeviceType: consts.DeviceTypeNetDevice,
					NicSelector: v1.SriovNetworkNicSelector{
						RootDevices: []string{"0000:86:00.0"},
					},
					NodeSelector: map[string]string{
						"feature.node.kubernetes.io/network-sriov.capable": "true",
					},
					NumVfs:       2,
					Priority:     99,
					EswitchMode:  "legacy",
					ResourceName: "p1res",
					Bridge:       v1.Bridge{OVS: &v1.OVSConfig{}},
				},
			},
			expectedBridges: v1.Bridges{},
			expectedErr:     true,
		},
		{
			tname:        "bad linkType",
			currentState: newNodeState(),
			policy: &v1.SriovNetworkNodePolicy{
				ObjectMeta: metav1.ObjectMeta{
					Name: "p1",
				},
				Spec: v1.SriovNetworkNodePolicySpec{
					DeviceType: consts.DeviceTypeNetDevice,
					NicSelector: v1.SriovNetworkNicSelector{
						RootDevices: []string{"0000:86:00.0"},
					},
					NodeSelector: map[string]string{
						"feature.node.kubernetes.io/network-sriov.capable": "true",
					},
					NumVfs:       2,
					Priority:     99,
					EswitchMode:  "switchdev",
					LinkType:     "ib",
					ResourceName: "p1res",
					Bridge:       v1.Bridge{OVS: &v1.OVSConfig{}},
				},
			},
			expectedBridges: v1.Bridges{},
			expectedErr:     true,
		},
		{
			tname:        "externally managed",
			currentState: newNodeState(),
			policy: &v1.SriovNetworkNodePolicy{
				ObjectMeta: metav1.ObjectMeta{
					Name: "p1",
				},
				Spec: v1.SriovNetworkNodePolicySpec{
					DeviceType: consts.DeviceTypeNetDevice,
					NicSelector: v1.SriovNetworkNicSelector{
						RootDevices: []string{"0000:86:00.0"},
					},
					NodeSelector: map[string]string{
						"feature.node.kubernetes.io/network-sriov.capable": "true",
					},
					NumVfs:            2,
					Priority:          99,
					EswitchMode:       "switchdev",
					ExternallyManaged: true,
					ResourceName:      "p1res",
					Bridge:            v1.Bridge{OVS: &v1.OVSConfig{}},
				},
			},
			expectedBridges: v1.Bridges{},
			expectedErr:     true,
		},
		{
			tname:        "single policy multi match",
			currentState: newNodeState(),
			policy: &v1.SriovNetworkNodePolicy{
				ObjectMeta: metav1.ObjectMeta{
					Name: "p1",
				},
				Spec: v1.SriovNetworkNodePolicySpec{
					DeviceType: consts.DeviceTypeNetDevice,
					NicSelector: v1.SriovNetworkNicSelector{
						RootDevices: []string{"0000:86:00.0", "0000:86:00.2"},
					},
					NodeSelector: map[string]string{
						"feature.node.kubernetes.io/network-sriov.capable": "true",
					},
					NumVfs:       2,
					Priority:     99,
					EswitchMode:  "switchdev",
					ResourceName: "p1res",
					Bridge: v1.Bridge{OVS: &v1.OVSConfig{
						Bridge: v1.OVSBridgeConfig{DatapathType: "test"},
						Uplink: v1.OVSUplinkConfig{
							Interface: v1.OVSInterfaceConfig{
								Type: "test",
							}},
					}},
				},
			},
			expectedBridges: v1.Bridges{OVS: []v1.OVSConfigExt{
				{
					Name:   "br-0000_86_00.0",
					Bridge: v1.OVSBridgeConfig{DatapathType: "test"},
					Uplinks: []v1.OVSUplinkConfigExt{{
						Name:       "ens803f0",
						PciAddress: "0000:86:00.0",
						Interface:  v1.OVSInterfaceConfig{Type: "test"},
					}},
				},
				{
					Name:   "br-0000_86_00.2",
					Bridge: v1.OVSBridgeConfig{DatapathType: "test"},
					Uplinks: []v1.OVSUplinkConfigExt{{
						Name:       "ens803f2",
						PciAddress: "0000:86:00.2",
						Interface:  v1.OVSInterfaceConfig{Type: "test"},
					}},
				},
			}},
		},
		{
			tname: "update bridge set by policy with lover priority",
			currentState: &v1.SriovNetworkNodeState{
				Spec: v1.SriovNetworkNodeStateSpec{
					Bridges: v1.Bridges{OVS: []v1.OVSConfigExt{
						{
							Name:   "br-0000_86_00.0",
							Bridge: v1.OVSBridgeConfig{DatapathType: "foo"},
							Uplinks: []v1.OVSUplinkConfigExt{{
								Name:       "ens803f0",
								PciAddress: "0000:86:00.0",
								Interface:  v1.OVSInterfaceConfig{Type: "test"},
							}},
						},
					}},
				},
				Status: v1.SriovNetworkNodeStateStatus{
					Interfaces: []v1.InterfaceExt{
						{
							VFs: []v1.VirtualFunction{
								{},
							},
							DeviceID:   "158b",
							Driver:     "i40e",
							Mtu:        1500,
							Name:       "ens803f0",
							PciAddress: "0000:86:00.0",
							Vendor:     "8086",
							NumVfs:     4,
							TotalVfs:   64,
						},
					},
				}},
			policy: &v1.SriovNetworkNodePolicy{
				ObjectMeta: metav1.ObjectMeta{
					Name: "p1",
				},
				Spec: v1.SriovNetworkNodePolicySpec{
					DeviceType: consts.DeviceTypeNetDevice,
					NicSelector: v1.SriovNetworkNicSelector{
						RootDevices: []string{"0000:86:00.0"},
					},
					NodeSelector: map[string]string{
						"feature.node.kubernetes.io/network-sriov.capable": "true",
					},
					NumVfs:       2,
					Priority:     99,
					EswitchMode:  "switchdev",
					ResourceName: "p1res",
					Bridge: v1.Bridge{OVS: &v1.OVSConfig{
						Bridge: v1.OVSBridgeConfig{DatapathType: "test"},
						Uplink: v1.OVSUplinkConfig{
							Interface: v1.OVSInterfaceConfig{
								Type: "test",
							}},
					}},
				},
			},
			expectedBridges: v1.Bridges{OVS: []v1.OVSConfigExt{
				{
					Name:   "br-0000_86_00.0",
					Bridge: v1.OVSBridgeConfig{DatapathType: "test"},
					Uplinks: []v1.OVSUplinkConfigExt{{
						Name:       "ens803f0",
						PciAddress: "0000:86:00.0",
						Interface:  v1.OVSInterfaceConfig{Type: "test"},
					}},
				},
			}},
		},
		{
			tname: "remove bridge set by policy with lover priority",
			currentState: &v1.SriovNetworkNodeState{
				Spec: v1.SriovNetworkNodeStateSpec{
					Bridges: v1.Bridges{OVS: []v1.OVSConfigExt{
						{
							Name:   "br-0000_86_00.0",
							Bridge: v1.OVSBridgeConfig{DatapathType: "foo"},
							Uplinks: []v1.OVSUplinkConfigExt{{
								Name:       "ens803f0",
								PciAddress: "0000:86:00.0",
								Interface:  v1.OVSInterfaceConfig{Type: "test"},
							}},
						},
					}},
				},
				Status: v1.SriovNetworkNodeStateStatus{
					Interfaces: []v1.InterfaceExt{
						{
							VFs: []v1.VirtualFunction{
								{},
							},
							DeviceID:   "158b",
							Driver:     "i40e",
							Mtu:        1500,
							Name:       "ens803f0",
							PciAddress: "0000:86:00.0",
							Vendor:     "8086",
							NumVfs:     4,
							TotalVfs:   64,
						},
					},
				}},
			policy: &v1.SriovNetworkNodePolicy{
				ObjectMeta: metav1.ObjectMeta{
					Name: "p1",
				},
				Spec: v1.SriovNetworkNodePolicySpec{
					DeviceType: consts.DeviceTypeNetDevice,
					NicSelector: v1.SriovNetworkNicSelector{
						RootDevices: []string{"0000:86:00.0"},
					},
					NodeSelector: map[string]string{
						"feature.node.kubernetes.io/network-sriov.capable": "true",
					},
					NumVfs:       2,
					Priority:     99,
					EswitchMode:  "switchdev",
					ResourceName: "p1res",
				},
			},
			expectedBridges: v1.Bridges{},
		},
		{
			tname: "keep bridge set by other policy",
			currentState: &v1.SriovNetworkNodeState{
				Spec: v1.SriovNetworkNodeStateSpec{
					Bridges: v1.Bridges{OVS: []v1.OVSConfigExt{
						{
							Name:   "br-0000_86_00.2",
							Bridge: v1.OVSBridgeConfig{DatapathType: "foo"},
							Uplinks: []v1.OVSUplinkConfigExt{{
								Name:       "ens803f2",
								PciAddress: "0000:86:00.2",
								Interface:  v1.OVSInterfaceConfig{Type: "bar"},
							}},
						},
					},
					}},
				Status: v1.SriovNetworkNodeStateStatus{
					Interfaces: []v1.InterfaceExt{
						{
							VFs: []v1.VirtualFunction{
								{},
							},
							DeviceID:   "158b",
							Driver:     "i40e",
							Mtu:        1500,
							Name:       "ens803f0",
							PciAddress: "0000:86:00.0",
							Vendor:     "8086",
							NumVfs:     4,
							TotalVfs:   64,
						},
					},
				}},
			policy: &v1.SriovNetworkNodePolicy{
				ObjectMeta: metav1.ObjectMeta{
					Name: "p1",
				},
				Spec: v1.SriovNetworkNodePolicySpec{
					DeviceType: consts.DeviceTypeNetDevice,
					NicSelector: v1.SriovNetworkNicSelector{
						RootDevices: []string{"0000:86:00.0"},
					},
					NodeSelector: map[string]string{
						"feature.node.kubernetes.io/network-sriov.capable": "true",
					},
					NumVfs:       2,
					Priority:     99,
					EswitchMode:  "switchdev",
					ResourceName: "p1res",
					Bridge: v1.Bridge{OVS: &v1.OVSConfig{
						Bridge: v1.OVSBridgeConfig{DatapathType: "test"},
						Uplink: v1.OVSUplinkConfig{
							Interface: v1.OVSInterfaceConfig{
								Type: "test",
							}},
					}},
				},
			},
			expectedBridges: v1.Bridges{OVS: []v1.OVSConfigExt{
				{
					Name:   "br-0000_86_00.0",
					Bridge: v1.OVSBridgeConfig{DatapathType: "test"},
					Uplinks: []v1.OVSUplinkConfigExt{{
						Name:       "ens803f0",
						PciAddress: "0000:86:00.0",
						Interface:  v1.OVSInterfaceConfig{Type: "test"},
					}},
				},
				{
					Name:   "br-0000_86_00.2",
					Bridge: v1.OVSBridgeConfig{DatapathType: "foo"},
					Uplinks: []v1.OVSUplinkConfigExt{{
						Name:       "ens803f2",
						PciAddress: "0000:86:00.2",
						Interface:  v1.OVSInterfaceConfig{Type: "bar"},
					}},
				},
			}},
		},
	}
	for _, tc := range testtable {
		t.Run(tc.tname, func(t *testing.T) {
			err := tc.policy.ApplyBridgeConfig(tc.currentState)
			if tc.expectedErr && err == nil {
				t.Errorf("ApplyBridgeConfig expecting error.")
			} else if !tc.expectedErr && err != nil {
				t.Errorf("ApplyBridgeConfig error:\n%s", err)
			}
			if diff := cmp.Diff(tc.expectedBridges, tc.currentState.Spec.Bridges); diff != "" {
				t.Errorf("SriovNetworkNodeState spec diff (-want +got):\n%s", diff)
			}
		})
	}
}

func TestGenerateBridgeName(t *testing.T) {
	result := v1.GenerateBridgeName(&v1.InterfaceExt{PciAddress: "0000:86:00.2"})
	expected := "br-0000_86_00.2"
	if result != expected {
		t.Errorf("GenerateBridgeName unexpected result, expected: %s, actual: %s", expected, result)
	}
}

func TestNeedToUpdateBridges(t *testing.T) {
	testtable := []struct {
		tname          string
		specBridge     *v1.Bridges
		statusBridge   *v1.Bridges
		expectedResult bool
	}{
		{
			tname:          "no update required",
			specBridge:     &v1.Bridges{OVS: []v1.OVSConfigExt{{Bridge: v1.OVSBridgeConfig{DatapathType: "test"}}}},
			statusBridge:   &v1.Bridges{OVS: []v1.OVSConfigExt{{Bridge: v1.OVSBridgeConfig{DatapathType: "test"}}}},
			expectedResult: false,
		},
		{
			tname:          "update required",
			specBridge:     &v1.Bridges{OVS: []v1.OVSConfigExt{{Bridge: v1.OVSBridgeConfig{DatapathType: "test"}}}},
			statusBridge:   &v1.Bridges{OVS: []v1.OVSConfigExt{}},
			expectedResult: true,
		},
	}
	for _, tc := range testtable {
		t.Run(tc.tname, func(t *testing.T) {
			result := v1.NeedToUpdateBridges(tc.specBridge, tc.statusBridge)
			if result != tc.expectedResult {
				t.Errorf("unexpected result want: %t got: %t", tc.expectedResult, result)
			}
		})
	}
}
