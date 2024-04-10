/*


Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controllers

import (
	"context"
	"sync"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/google/go-cmp/cmp"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	controllerruntime "sigs.k8s.io/controller-runtime"

	sriovnetworkv1 "github.com/k8snetworkplumbingwg/sriov-network-operator/api/v1"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/vars"
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
		{
			tname: "testemptyselector",
			policies: []sriovnetworkv1.SriovNetworkNodePolicy{
				{
					Spec: sriovnetworkv1.SriovNetworkNodePolicySpec{
						NodeSelector: map[string]string{},
					},
				},
			},
			expected: []corev1.NodeSelectorTerm{},
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

var _ = Describe("Helper Validation", Ordered, func() {

	var cancel context.CancelFunc
	var ctx context.Context
	var dc *sriovnetworkv1.SriovOperatorConfig
	var in *appsv1.DaemonSet

	BeforeAll(func() {
		By("Setup controller manager")
		k8sManager, err := setupK8sManagerForTest()
		Expect(err).ToNot(HaveOccurred())

		ctx, cancel = context.WithCancel(context.Background())

		wg := sync.WaitGroup{}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer GinkgoRecover()
			By("Start controller manager")
			err := k8sManager.Start(ctx)
			Expect(err).ToNot(HaveOccurred())
		}()

		DeferCleanup(func() {
			By("Shutdown controller manager")
			cancel()
			wg.Wait()
		})
	})

	BeforeEach(func() {
		dc = &sriovnetworkv1.SriovOperatorConfig{
			ObjectMeta: controllerruntime.ObjectMeta{
				Name:      "default",
				Namespace: vars.Namespace,
				UID:       "12312312"}}
		in = &appsv1.DaemonSet{
			ObjectMeta: controllerruntime.ObjectMeta{
				Name:      "sriov-device-plugin",
				Namespace: vars.Namespace},
			Spec: appsv1.DaemonSetSpec{
				Selector: &metav1.LabelSelector{
					MatchLabels: map[string]string{"app": "sriov-device-plugin"}},
				Template: corev1.PodTemplateSpec{
					ObjectMeta: controllerruntime.ObjectMeta{
						Labels: map[string]string{"app": "sriov-device-plugin"}},
					Spec: corev1.PodSpec{
						Containers: []corev1.Container{
							{
								Image: "test:latest",
								Name:  "test",
							},
						},
					},
				}}}

		err := k8sClient.Delete(ctx, in)
		if err != nil {
			Expect(errors.IsNotFound(err)).To(BeTrue())
		}
	})

	Context("syncDaemonSet", func() {
		It("should create a new daemon", func() {
			pl := &sriovnetworkv1.SriovNetworkNodePolicyList{Items: []sriovnetworkv1.SriovNetworkNodePolicy{
				{ObjectMeta: controllerruntime.ObjectMeta{Name: "test", Namespace: vars.Namespace}},
			}}
			err := syncDaemonSet(ctx, k8sClient, vars.Scheme, dc, pl, in)
			Expect(err).ToNot(HaveOccurred())
			Expect(in.Spec.Template.Spec.Affinity).To(BeNil())
		})
		It("should update affinity", func() {
			pl := &sriovnetworkv1.SriovNetworkNodePolicyList{Items: []sriovnetworkv1.SriovNetworkNodePolicy{
				{
					ObjectMeta: controllerruntime.ObjectMeta{
						Name:      "test",
						Namespace: vars.Namespace,
					},
					Spec: sriovnetworkv1.SriovNetworkNodePolicySpec{
						NodeSelector: map[string]string{"test": "test"},
					},
				},
			}}

			err := k8sClient.Create(ctx, in)
			Expect(err).ToNot(HaveOccurred())

			err = syncDaemonSet(ctx, k8sClient, vars.Scheme, dc, pl, in)
			Expect(err).ToNot(HaveOccurred())
			Expect(in.Spec.Template.Spec.Affinity).ToNot(BeNil())
			Expect(in.Spec.Template.Spec.Affinity.NodeAffinity).ToNot(BeNil())
			Expect(in.Spec.Template.Spec.Affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution).ToNot(BeNil())
			Expect(len(in.Spec.Template.Spec.Affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution.NodeSelectorTerms)).To(Equal(1))
		})
		It("should update affinity with multiple", func() {
			pl := &sriovnetworkv1.SriovNetworkNodePolicyList{Items: []sriovnetworkv1.SriovNetworkNodePolicy{
				{
					ObjectMeta: controllerruntime.ObjectMeta{
						Name:      "test",
						Namespace: vars.Namespace,
					},
					Spec: sriovnetworkv1.SriovNetworkNodePolicySpec{
						NodeSelector: map[string]string{"test": "test"},
					},
				},
				{
					ObjectMeta: controllerruntime.ObjectMeta{
						Name:      "test1",
						Namespace: vars.Namespace,
					},
					Spec: sriovnetworkv1.SriovNetworkNodePolicySpec{
						NodeSelector: map[string]string{"test1": "test"},
					},
				},
			}}

			err := k8sClient.Create(ctx, in)
			Expect(err).ToNot(HaveOccurred())

			err = syncDaemonSet(ctx, k8sClient, vars.Scheme, dc, pl, in)
			Expect(err).ToNot(HaveOccurred())
			Expect(in.Spec.Template.Spec.Affinity).ToNot(BeNil())
			Expect(in.Spec.Template.Spec.Affinity.NodeAffinity).ToNot(BeNil())
			Expect(in.Spec.Template.Spec.Affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution).ToNot(BeNil())
			Expect(len(in.Spec.Template.Spec.Affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution.NodeSelectorTerms)).To(Equal(2))
		})
		It("should switch affinity", func() {
			pl := &sriovnetworkv1.SriovNetworkNodePolicyList{Items: []sriovnetworkv1.SriovNetworkNodePolicy{
				{
					ObjectMeta: controllerruntime.ObjectMeta{
						Name:      "test1",
						Namespace: vars.Namespace,
					},
					Spec: sriovnetworkv1.SriovNetworkNodePolicySpec{
						NodeSelector: map[string]string{"test1": "test"},
					},
				},
			}}

			in.Spec.Template.Spec.Affinity = &corev1.Affinity{
				NodeAffinity: &corev1.NodeAffinity{
					RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{
						NodeSelectorTerms: []corev1.NodeSelectorTerm{{
							MatchExpressions: []corev1.NodeSelectorRequirement{{
								Operator: corev1.NodeSelectorOpIn,
								Key:      "test",
								Values:   []string{"test"},
							}},
						}},
					},
				},
			}

			err := k8sClient.Create(ctx, in)
			Expect(err).ToNot(HaveOccurred())

			err = syncDaemonSet(ctx, k8sClient, vars.Scheme, dc, pl, in)
			Expect(err).ToNot(HaveOccurred())
			Expect(in.Spec.Template.Spec.Affinity).ToNot(BeNil())
			Expect(in.Spec.Template.Spec.Affinity.NodeAffinity).ToNot(BeNil())
			Expect(in.Spec.Template.Spec.Affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution).ToNot(BeNil())
			Expect(len(in.Spec.Template.Spec.Affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution.NodeSelectorTerms)).To(Equal(1))
			Expect(len(in.Spec.Template.Spec.Affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution.NodeSelectorTerms[0].MatchExpressions)).To(Equal(1))
			Expect(in.Spec.Template.Spec.Affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution.NodeSelectorTerms[0].MatchExpressions[0].Key).To(Equal("test1"))
		})
	})
})
