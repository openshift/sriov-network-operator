package sriovnetworknodepolicy

import (
	"testing"

	sriovnetworkv1 "github.com/openshift/sriov-network-operator/pkg/apis/sriovnetwork/v1"
	corev1 "k8s.io/api/core/v1"

	"github.com/google/go-cmp/cmp"
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
