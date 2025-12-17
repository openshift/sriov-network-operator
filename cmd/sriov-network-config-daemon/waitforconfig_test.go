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
package main

import (
	"context"
	"os"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"

	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/consts"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/test/util"
)

var _ = Describe("WaitForConfig", func() {
	Context("validateWaitForConfigOpts", func() {
		BeforeEach(func() {
			waitForConfigOpts.podName = ""
			waitForConfigOpts.podNamespace = ""
		})
		It("should return error when pod-name is not provided", func() {
			waitForConfigOpts.podNamespace = "test-namespace"
			err := validateWaitForConfigOpts()
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("--pod-name is required"))
		})
		It("should return error when pod-namespace is not provided", func() {
			waitForConfigOpts.podName = "test-pod"
			err := validateWaitForConfigOpts()
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("--pod-namespace is required"))
		})
		It("should return nil when both options are provided", func() {
			waitForConfigOpts.podName = "test-pod"
			waitForConfigOpts.podNamespace = "test-namespace"
			err := validateWaitForConfigOpts()
			Expect(err).NotTo(HaveOccurred())
		})
	})
	Context("startWaitForConfigManager", Ordered, func() {
		const testNamespace = "test-namespace"
		var (
			testEnv   *envtest.Environment
			cfg       *rest.Config
			k8sClient client.Client
		)
		BeforeAll(func() {
			By("changing to project root directory")
			err := os.Chdir("../..")
			Expect(err).NotTo(HaveOccurred())

			By("bootstrapping test environment")
			testEnv = &envtest.Environment{}

			cfg, err = testEnv.Start()
			Expect(err).NotTo(HaveOccurred())
			Expect(cfg).NotTo(BeNil())

			By("creating K8s client")
			k8sClient, err = client.New(cfg, client.Options{Scheme: k8sscheme.Scheme})
			Expect(err).NotTo(HaveOccurred())

			By("creating test namespace")
			ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: testNamespace}}
			Expect(k8sClient.Create(context.Background(), ns)).To(Succeed())
		})
		AfterAll(func() {
			By("tearing down the test environment")
			if testEnv != nil {
				Eventually(func() error {
					return testEnv.Stop()
				}, util.APITimeout, time.Second).ShouldNot(HaveOccurred())
			}
		})
		It("should set annotation and exit when annotation is removed", func() {
			pod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					GenerateName: "test-pod-",
					Namespace:    testNamespace,
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{Name: "test", Image: "test"}},
				},
			}
			Expect(k8sClient.Create(context.Background(), pod)).To(Succeed())
			podName := client.ObjectKeyFromObject(pod)
			DeferCleanup(func() {
				_ = k8sClient.Delete(context.Background(), pod)
			})

			wg := sync.WaitGroup{}
			wg.Add(1)
			var managerErr error
			go func() {
				defer wg.Done()
				defer GinkgoRecover()
				managerErr = startWaitForConfigManager(
					GinkgoLogr.WithName("wait-for-config"), cfg, podName)
			}()

			By("waiting for annotation to be set")
			Eventually(func(g Gomega) {
				p := &corev1.Pod{}
				g.Expect(k8sClient.Get(context.Background(), podName, p)).To(Succeed())
				g.Expect(p.Annotations).To(HaveKeyWithValue(
					consts.DevicePluginWaitConfigAnnotation, "true"))
			}, 30*time.Second, 100*time.Millisecond).Should(Succeed())

			By("removing annotation to signal config is applied")
			p := &corev1.Pod{}
			Expect(k8sClient.Get(context.Background(), podName, p)).To(Succeed())
			originalPod := p.DeepCopy()
			delete(p.Annotations, consts.DevicePluginWaitConfigAnnotation)
			Expect(k8sClient.Patch(context.Background(), p, client.MergeFrom(originalPod))).To(Succeed())

			By("waiting for manager to exit")
			done := make(chan struct{})
			go func() {
				wg.Wait()
				close(done)
			}()
			Eventually(done, 30*time.Second).Should(BeClosed())
			Expect(managerErr).NotTo(HaveOccurred())
		})
	})
})
