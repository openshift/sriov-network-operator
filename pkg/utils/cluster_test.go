/*
Copyright 2025.

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
package utils

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

var _ = Describe("Cluster Utils", func() {
	Context("ObjectHasAnnotationKey", func() {
		DescribeTable("should check if annotation key exists",
			func(annotations map[string]string, key string, expected bool) {
				obj := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Annotations: annotations}}
				Expect(ObjectHasAnnotationKey(obj, key)).To(Equal(expected))
			},
			Entry("key exists", map[string]string{"test-key": "test-value"}, "test-key", true),
			Entry("key does not exist", map[string]string{"other-key": "test-value"}, "test-key", false),
			Entry("annotations is nil", nil, "test-key", false),
		)
	})
	Context("ObjectHasAnnotation", func() {
		DescribeTable("should check if annotation key and value match",
			func(annotations map[string]string, key, value string, expected bool) {
				obj := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Annotations: annotations}}
				Expect(ObjectHasAnnotation(obj, key, value)).To(Equal(expected))
			},
			Entry("key and value match", map[string]string{"test-key": "test-value"}, "test-key", "test-value", true),
			Entry("value does not match", map[string]string{"test-key": "other-value"}, "test-key", "test-value", false),
			Entry("key does not exist", map[string]string{}, "test-key", "test-value", false),
		)
	})
	Context("RemoveAnnotationFromObject", func() {
		var (
			fakeClient client.Client
			ctx        context.Context
		)
		BeforeEach(func() {
			ctx = context.Background()
		})
		It("should remove annotation from object", func() {
			pod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pod",
					Namespace: "test-namespace",
					Annotations: map[string]string{
						"to-remove":   "value",
						"to-preserve": "value",
					},
				},
			}
			fakeClient = fake.NewClientBuilder().WithObjects(pod).Build()

			err := RemoveAnnotationFromObject(ctx, pod, "to-remove", fakeClient)
			Expect(err).NotTo(HaveOccurred())

			// Verify annotation was removed
			updatedPod := &corev1.Pod{}
			err = fakeClient.Get(ctx,
				types.NamespacedName{Name: "test-pod", Namespace: "test-namespace"}, updatedPod)
			Expect(err).NotTo(HaveOccurred())
			Expect(updatedPod.Annotations).ToNot(HaveKey("to-remove"))
			Expect(updatedPod.Annotations).To(HaveKeyWithValue("to-preserve", "value"))
		})
		It("should handle nil annotations gracefully", func() {
			pod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pod",
					Namespace: "test-namespace",
				},
			}
			fakeClient = fake.NewClientBuilder().WithObjects(pod).Build()

			err := RemoveAnnotationFromObject(ctx, pod, "any-key", fakeClient)
			Expect(err).NotTo(HaveOccurred())
		})
	})
	Context("AnnotateObject", func() {
		var (
			fakeClient client.Client
			ctx        context.Context
		)
		BeforeEach(func() {
			ctx = context.Background()
		})
		It("should add annotation to object without annotations", func() {
			pod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pod",
					Namespace: "test-namespace",
				},
			}
			fakeClient = fake.NewClientBuilder().WithObjects(pod).Build()

			err := AnnotateObject(ctx, pod, "new-key", "new-value", fakeClient)
			Expect(err).NotTo(HaveOccurred())

			// Verify annotation was added
			updatedPod := &corev1.Pod{}
			err = fakeClient.Get(ctx,
				types.NamespacedName{Name: "test-pod", Namespace: "test-namespace"}, updatedPod)
			Expect(err).NotTo(HaveOccurred())
			Expect(updatedPod.Annotations).To(HaveKeyWithValue("new-key", "new-value"))
		})
		It("should add annotation preserving existing annotations", func() {
			pod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pod",
					Namespace: "test-namespace",
					Annotations: map[string]string{
						"existing-key": "existing-value",
					},
				},
			}
			fakeClient = fake.NewClientBuilder().WithObjects(pod).Build()

			err := AnnotateObject(ctx, pod, "new-key", "new-value", fakeClient)
			Expect(err).NotTo(HaveOccurred())

			// Verify both annotations exist
			updatedPod := &corev1.Pod{}
			err = fakeClient.Get(ctx,
				types.NamespacedName{Name: "test-pod", Namespace: "test-namespace"}, updatedPod)
			Expect(err).NotTo(HaveOccurred())
			Expect(updatedPod.Annotations).To(HaveKeyWithValue("existing-key", "existing-value"))
			Expect(updatedPod.Annotations).To(HaveKeyWithValue("new-key", "new-value"))
		})
		It("should not patch if annotation already has same value", func() {
			pod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-pod",
					Namespace: "test-namespace",
					Annotations: map[string]string{
						"same-key": "same-value",
					},
				},
			}
			patchCalled := false
			fakeClient = fake.NewClientBuilder().WithObjects(pod).WithInterceptorFuncs(
				interceptor.Funcs{
					Patch: func(ctx context.Context, c client.WithWatch,
						obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
						patchCalled = true
						return c.Patch(ctx, obj, patch, opts...)
					},
				}).Build()

			err := AnnotateObject(ctx, pod, "same-key", "same-value", fakeClient)
			Expect(err).NotTo(HaveOccurred())
			Expect(patchCalled).To(BeFalse())
		})
	})
})
