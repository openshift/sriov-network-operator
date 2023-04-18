package controllers

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/go-cmp/cmp"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"

	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	dptypes "github.com/k8snetworkplumbingwg/sriov-network-device-plugin/pkg/types"

	sriovnetworkv1 "github.com/k8snetworkplumbingwg/sriov-network-operator/api/v1"
	v1 "github.com/k8snetworkplumbingwg/sriov-network-operator/api/v1"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/consts"
)

func TestNodeSelectorMerge(t *testing.T) {
	table := []struct {
		tname    string
		policies []sriovnetworkv1.SriovNetworkNodePolicy
		expected []corev1.NodeSelectorTerm
	}{
		{
			tname: "testoneselector",
			policies: []sriovnetworkv1.SriovNetworkNodePolicy{
				{
					Spec: sriovnetworkv1.SriovNetworkNodePolicySpec{
						NodeSelector: map[string]string{
							"foo": "bar",
						},
					},
				},
				{
					Spec: sriovnetworkv1.SriovNetworkNodePolicySpec{
						NodeSelector: map[string]string{
							"bb": "cc",
						},
					},
				},
			},
			expected: []corev1.NodeSelectorTerm{
				{
					MatchExpressions: []corev1.NodeSelectorRequirement{
						{
							Operator: corev1.NodeSelectorOpIn,
							Key:      "foo",
							Values:   []string{"bar"},
						},
					},
				},
				{
					MatchExpressions: []corev1.NodeSelectorRequirement{
						{
							Operator: corev1.NodeSelectorOpIn,
							Key:      "bb",
							Values:   []string{"cc"},
						},
					},
				},
			},
		},
		{
			tname: "testtwoselectors",
			policies: []sriovnetworkv1.SriovNetworkNodePolicy{
				{
					Spec: sriovnetworkv1.SriovNetworkNodePolicySpec{
						NodeSelector: map[string]string{
							"foo":  "bar",
							"foo1": "bar1",
						},
					},
				},
				{
					Spec: sriovnetworkv1.SriovNetworkNodePolicySpec{
						NodeSelector: map[string]string{
							"bb":  "cc",
							"bb1": "cc1",
							"bb2": "cc2",
						},
					},
				},
			},
			expected: []corev1.NodeSelectorTerm{
				{
					MatchExpressions: []corev1.NodeSelectorRequirement{
						{
							Operator: corev1.NodeSelectorOpIn,
							Key:      "foo",
							Values:   []string{"bar"},
						},
						{
							Operator: corev1.NodeSelectorOpIn,
							Key:      "foo1",
							Values:   []string{"bar1"},
						},
					},
				},
				{
					MatchExpressions: []corev1.NodeSelectorRequirement{
						{
							Operator: corev1.NodeSelectorOpIn,
							Key:      "bb",
							Values:   []string{"cc"},
						},
						{
							Operator: corev1.NodeSelectorOpIn,
							Key:      "bb1",
							Values:   []string{"cc1"},
						},
						{
							Operator: corev1.NodeSelectorOpIn,
							Key:      "bb2",
							Values:   []string{"cc2"},
						},
					},
				},
			},
		},
	}

	for _, tc := range table {
		t.Run(tc.tname, func(t *testing.T) {
			selectors := nodeSelectorTermsForPolicyList(tc.policies)
			if !cmp.Equal(selectors, tc.expected) {
				t.Error(tc.tname, "Selectors not as expected", cmp.Diff(selectors, tc.expected))
			}
		})
	}
}

func mustMarshallSelector(t *testing.T, input *dptypes.NetDeviceSelectors) *json.RawMessage {
	out, err := json.Marshal(input)
	if err != nil {
		t.Error(err)
		t.FailNow()
		return nil
	}
	ret := json.RawMessage(out)
	return &ret
}

func TestRenderDevicePluginConfigData(t *testing.T) {
	table := []struct {
		tname       string
		policy      sriovnetworkv1.SriovNetworkNodePolicy
		expResource dptypes.ResourceConfList
	}{
		{
			tname: "testVdpaVirtio",
			policy: sriovnetworkv1.SriovNetworkNodePolicy{
				Spec: v1.SriovNetworkNodePolicySpec{
					ResourceName: "resourceName",
					DeviceType:   consts.DeviceTypeNetDevice,
					VdpaType:     consts.VdpaTypeVirtio,
				},
			},
			expResource: dptypes.ResourceConfList{
				ResourceList: []dptypes.ResourceConfig{
					{
						ResourceName: "resourceName",
						Selectors: mustMarshallSelector(t, &dptypes.NetDeviceSelectors{
							VdpaType: dptypes.VdpaType(consts.VdpaTypeVirtio),
						}),
					},
				},
			},
		},
	}

	reconciler := SriovNetworkNodePolicyReconciler{}

	node := corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "node1"}}
	nodeState := sriovnetworkv1.SriovNetworkNodeState{ObjectMeta: metav1.ObjectMeta{Name: node.Name, Namespace: namespace}}

	scheme := runtime.NewScheme()
	utilruntime.Must(sriovnetworkv1.AddToScheme(scheme))
	reconciler.Client = fake.NewClientBuilder().
		WithScheme(scheme).WithObjects(&nodeState).
		Build()

	for _, tc := range table {
		policyList := sriovnetworkv1.SriovNetworkNodePolicyList{Items: []sriovnetworkv1.SriovNetworkNodePolicy{tc.policy}}

		t.Run(tc.tname, func(t *testing.T) {
			resourceList, err := reconciler.renderDevicePluginConfigData(context.TODO(), &policyList, &node)
			if err != nil {
				t.Error(tc.tname, "renderDevicePluginConfigData has failed")
			}

			if !cmp.Equal(resourceList, tc.expResource) {
				t.Error(tc.tname, "ResourceConfList not as expected", cmp.Diff(resourceList, tc.expResource))
			}
		})
	}
}
