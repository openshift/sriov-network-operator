package controllers

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/go-cmp/cmp"
	corev1 "k8s.io/api/core/v1"

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

func buildSelector(vdpaType dptypes.VdpaType) (json.RawMessage, error) {
	netDeviceSelectors := dptypes.NetDeviceSelectors{}
	netDeviceSelectors.IsRdma = false
	netDeviceSelectors.NeedVhostNet = false
	netDeviceSelectors.VdpaType = vdpaType

	netDeviceSelectorsMarshal, err := json.Marshal(netDeviceSelectors)
	if err != nil {
		return nil, err
	}
	rawNetDeviceSelectors := json.RawMessage(netDeviceSelectorsMarshal)
	return rawNetDeviceSelectors, nil
}

func TestRenderDevicePluginConfigData(t *testing.T) {
	rawNetDeviceSelectors, err := buildSelector(dptypes.VdpaType(consts.VdpaTypeVirtio))
	if err != nil {
		t.Error(err)
		return
	}

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
						Selectors:    &rawNetDeviceSelectors,
					},
				},
			},
		},
	}

	reconciler := SriovNetworkNodePolicyReconciler{}

	for _, tc := range table {
		policyList := sriovnetworkv1.SriovNetworkNodePolicyList{Items: []sriovnetworkv1.SriovNetworkNodePolicy{tc.policy}}
		node := corev1.Node{}
		t.Run(tc.tname, func(t *testing.T) {
			resourceList, err := reconciler.renderDevicePluginConfigData(context.TODO(), &policyList, &node)
			if err != nil {
				t.Error(tc.tname, "renderDevicePluginConfigData has failed")
			}

			t.Logf("SelectorObj: %v", resourceList.ResourceList[0].SelectorObj)

			if !cmp.Equal(resourceList, tc.expResource) {
				t.Error(tc.tname, "ResourceConfList not as expected", cmp.Diff(resourceList, tc.expResource))
			}
		})
	}
}
