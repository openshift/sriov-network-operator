/*
Copyright 2021.

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

package v1_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	v1 "github.com/k8snetworkplumbingwg/sriov-network-operator/api/v1"
)

var _ = Describe("NetworkStatus", func() {
	It("should have empty conditions by default", func() {
		status := v1.NetworkStatus{}
		Expect(status.Conditions).To(BeNil())
	})

	It("should store conditions", func() {
		status := v1.NetworkStatus{
			Conditions: []metav1.Condition{
				{
					Type:    v1.ConditionReady,
					Status:  metav1.ConditionTrue,
					Reason:  v1.ReasonNetworkReady,
					Message: "ready",
				},
			},
		}
		Expect(status.Conditions).To(HaveLen(1))
		Expect(status.Conditions[0].Type).To(Equal(v1.ConditionReady))
		Expect(status.Conditions[0].Reason).To(Equal(v1.ReasonNetworkReady))
	})
})

var _ = Describe("Condition constants", func() {
	It("should have the expected condition type values", func() {
		Expect(v1.ConditionReady).To(Equal("Ready"))
	})

	It("should have the expected reason values", func() {
		Expect(v1.ReasonNetworkReady).To(Equal("NetworkReady"))
		Expect(v1.ReasonNetworkAttachmentDefNotFound).To(Equal("NetworkAttachmentDefinitionNotFound"))
		Expect(v1.ReasonNetworkAttachmentDefInvalid).To(Equal("NetworkAttachmentDefinitionInvalid"))
		Expect(v1.ReasonNamespaceNotFound).To(Equal("NamespaceNotFound"))
	})
})
