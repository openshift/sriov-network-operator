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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

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
				Spec: v1.SriovNetworkSpec{
					NetworkNamespace: "testnamespace",
					ResourceName:     "testresource",
				},
			},
		},
		{
			tname: "chained",
			network: v1.SriovNetwork{
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
			t.Log(b.String())
			if !bytes.Equal(b.Bytes(), g) {
				t.Errorf("bytes do not match .golden file")
			}
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
			t.Log(b.String())
			if !bytes.Equal(b.Bytes(), g) {
				t.Errorf("bytes do not match .golden file")
			}
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
