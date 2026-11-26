/*
Copyright 2026.

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
	"fmt"
	"maps"
	"os"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	resourceapi "k8s.io/api/resource/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	k8sclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	sriovnetworkv1 "github.com/k8snetworkplumbingwg/sriov-network-operator/api/v1"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/consts"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/featuregate"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/render"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/vars"
)

func testRenderManifests(path string, data *render.RenderData) ([]*unstructured.Unstructured, error) {
	return render.RenderDir(path, data)
}

func testApplyManifest(ctx context.Context, c k8sclient.Client, obj *unstructured.Unstructured) error {
	return (&SriovOperatorConfigReconciler{}).applyManifest(ctx, c, obj)
}

func providerOwnerReferences(kind string) []metav1.OwnerReference {
	controller := true
	return []metav1.OwnerReference{{
		APIVersion: sriovnetworkv1.GroupVersion.String(),
		Kind:       kind,
		Name:       consts.DefaultConfigName,
		UID:        types.UID("operator-owner"),
		Controller: &controller,
	}}
}

var _ = Describe("Helper DRA", func() {
	Context("GetNodeSelectorForDRADriver", func() {
		It("returns ConfigDaemonNodeSelector when set", func() {
			dc := &sriovnetworkv1.SriovOperatorConfig{
				Spec: sriovnetworkv1.SriovOperatorConfigSpec{
					ConfigDaemonNodeSelector: map[string]string{
						"custom-label": "value",
						"foo":          "bar",
					},
				},
			}
			sel := GetNodeSelectorForDRADriver(dc)
			Expect(sel).To(HaveLen(2))
			Expect(sel).To(HaveKeyWithValue("custom-label", "value"))
			Expect(sel).To(HaveKeyWithValue("foo", "bar"))
		})

		It("returns default node selector when ConfigDaemonNodeSelector is empty", func() {
			dc := &sriovnetworkv1.SriovOperatorConfig{
				Spec: sriovnetworkv1.SriovOperatorConfigSpec{
					ConfigDaemonNodeSelector: map[string]string{},
				},
			}
			sel := GetNodeSelectorForDRADriver(dc)
			Expect(sel).To(Equal(GetDefaultNodeSelector()))
			Expect(sel).To(HaveKeyWithValue("node-role.kubernetes.io/worker", ""))
			Expect(sel).To(HaveKeyWithValue("kubernetes.io/os", "linux"))
		})

		It("returns default node selector when ConfigDaemonNodeSelector is nil", func() {
			dc := &sriovnetworkv1.SriovOperatorConfig{}
			sel := GetNodeSelectorForDRADriver(dc)
			Expect(sel).To(Equal(GetDefaultNodeSelector()))
		})
	})

	Context("cleanupDRADriverObjs", func() {
		var (
			ctx     context.Context
			scheme  *runtime.Scheme
			client  k8sclient.Client
			nsSaved string
		)

		BeforeEach(func() {
			ctx = context.Background()
			nsSaved = vars.Namespace
			vars.Namespace = testNamespace
			DeferCleanup(func() { vars.Namespace = nsSaved })
			scheme = runtime.NewScheme()
			utilruntime.Must(sriovnetworkv1.AddToScheme(scheme))
			utilruntime.Must(corev1.AddToScheme(scheme))
			utilruntime.Must(appsv1.AddToScheme(scheme))
			utilruntime.Must(rbacv1.AddToScheme(scheme))
			utilruntime.Must(resourceapi.AddToScheme(scheme))
		})

		It("deletes DRA driver DaemonSet, RBAC, ServiceAccount, and base DeviceClass when present", func() {
			ownerReferences := providerOwnerReferences("SriovOperatorConfig")
			ds := &appsv1.DaemonSet{
				ObjectMeta: metav1.ObjectMeta{
					Name:            consts.DRADriverDaemonSetName,
					Namespace:       testNamespace,
					OwnerReferences: ownerReferences,
				},
			}
			sa := &corev1.ServiceAccount{
				ObjectMeta: metav1.ObjectMeta{Name: consts.DRADriverServiceAccountName, Namespace: testNamespace, OwnerReferences: ownerReferences},
			}
			role := &rbacv1.Role{
				ObjectMeta: metav1.ObjectMeta{Name: consts.DRADriverPodAccessRoleName, Namespace: testNamespace, OwnerReferences: ownerReferences},
			}
			roleBinding := &rbacv1.RoleBinding{
				ObjectMeta: metav1.ObjectMeta{Name: consts.DRADriverPodAccessRoleBindingName, Namespace: testNamespace, OwnerReferences: ownerReferences},
			}
			sccRole := &rbacv1.Role{
				ObjectMeta: metav1.ObjectMeta{Name: consts.DRADriverSCCRoleName, Namespace: testNamespace, OwnerReferences: ownerReferences},
			}
			sccRoleBinding := &rbacv1.RoleBinding{
				ObjectMeta: metav1.ObjectMeta{Name: consts.DRADriverSCCRoleBindingName, Namespace: testNamespace, OwnerReferences: ownerReferences},
			}
			managedLabels := maps.Clone(consts.DRAOperatorLabels)
			clusterRole := &rbacv1.ClusterRole{
				ObjectMeta: metav1.ObjectMeta{Name: consts.DRADriverClusterRBACName, Labels: managedLabels},
			}
			clusterRoleBinding := &rbacv1.ClusterRoleBinding{
				ObjectMeta: metav1.ObjectMeta{Name: consts.DRADriverClusterRBACName, Labels: managedLabels},
			}
			dc := &resourceapi.DeviceClass{
				ObjectMeta: metav1.ObjectMeta{Name: consts.DRADriverBaseDeviceClassName, Labels: managedLabels},
			}

			client = fake.NewClientBuilder().WithScheme(scheme).WithObjects(
				ds, sa, role, roleBinding, sccRole, sccRoleBinding, clusterRole, clusterRoleBinding, dc,
			).Build()
			Expect(cleanupDRADriverObjs(ctx, client, &sriovnetworkv1.SriovOperatorConfig{}, testRenderManifests)).To(Succeed())

			Expect(client.Get(ctx, types.NamespacedName{Namespace: testNamespace, Name: consts.DRADriverDaemonSetName}, &appsv1.DaemonSet{})).To(MatchError(ContainSubstring("not found")))
			Expect(client.Get(ctx, types.NamespacedName{Namespace: testNamespace, Name: consts.DRADriverServiceAccountName}, &corev1.ServiceAccount{})).To(MatchError(ContainSubstring("not found")))
			Expect(client.Get(ctx, types.NamespacedName{Namespace: testNamespace, Name: consts.DRADriverPodAccessRoleName}, &rbacv1.Role{})).To(MatchError(ContainSubstring("not found")))
			Expect(client.Get(ctx, types.NamespacedName{Namespace: testNamespace, Name: consts.DRADriverPodAccessRoleBindingName}, &rbacv1.RoleBinding{})).To(MatchError(ContainSubstring("not found")))
			Expect(client.Get(ctx, types.NamespacedName{Namespace: testNamespace, Name: consts.DRADriverSCCRoleName}, &rbacv1.Role{})).To(MatchError(ContainSubstring("not found")))
			Expect(client.Get(ctx, types.NamespacedName{Namespace: testNamespace, Name: consts.DRADriverSCCRoleBindingName}, &rbacv1.RoleBinding{})).To(MatchError(ContainSubstring("not found")))
			Expect(client.Get(ctx, types.NamespacedName{Name: consts.DRADriverClusterRBACName}, &rbacv1.ClusterRole{})).To(MatchError(ContainSubstring("not found")))
			Expect(client.Get(ctx, types.NamespacedName{Name: consts.DRADriverClusterRBACName}, &rbacv1.ClusterRoleBinding{})).To(MatchError(ContainSubstring("not found")))
			Expect(client.Get(ctx, types.NamespacedName{Name: consts.DRADriverBaseDeviceClassName}, dc)).To(MatchError(ContainSubstring("not found")))
		})

		It("succeeds when DRA driver DaemonSet does not exist", func() {
			client = fake.NewClientBuilder().WithScheme(scheme).Build()
			Expect(cleanupDRADriverObjs(ctx, client, &sriovnetworkv1.SriovOperatorConfig{}, testRenderManifests)).To(Succeed())
		})

		It("does not delete cluster-scoped DRA objects without the operator management marker", func() {
			clusterRole := &rbacv1.ClusterRole{
				ObjectMeta: metav1.ObjectMeta{Name: consts.DRADriverClusterRBACName},
			}
			clusterRoleBinding := &rbacv1.ClusterRoleBinding{
				ObjectMeta: metav1.ObjectMeta{Name: consts.DRADriverClusterRBACName},
			}
			dc := &resourceapi.DeviceClass{
				ObjectMeta: metav1.ObjectMeta{Name: consts.DRADriverBaseDeviceClassName},
			}

			client = fake.NewClientBuilder().WithScheme(scheme).WithObjects(
				clusterRole, clusterRoleBinding, dc,
			).Build()
			Expect(cleanupDRADriverObjs(ctx, client, &sriovnetworkv1.SriovOperatorConfig{}, testRenderManifests)).To(Succeed())

			Expect(client.Get(ctx, types.NamespacedName{Name: consts.DRADriverClusterRBACName}, &rbacv1.ClusterRole{})).To(Succeed())
			Expect(client.Get(ctx, types.NamespacedName{Name: consts.DRADriverClusterRBACName}, &rbacv1.ClusterRoleBinding{})).To(Succeed())
			Expect(client.Get(ctx, types.NamespacedName{Name: consts.DRADriverBaseDeviceClassName}, dc)).To(Succeed())
		})
	})

	Context("cleanupDevicePluginObjs", func() {
		var (
			ctx     context.Context
			scheme  *runtime.Scheme
			client  k8sclient.Client
			nsSaved string
		)

		BeforeEach(func() {
			ctx = context.Background()
			nsSaved = vars.Namespace
			vars.Namespace = testNamespace
			DeferCleanup(func() { vars.Namespace = nsSaved })
			scheme = runtime.NewScheme()
			utilruntime.Must(sriovnetworkv1.AddToScheme(scheme))
			utilruntime.Must(corev1.AddToScheme(scheme))
			utilruntime.Must(appsv1.AddToScheme(scheme))
			utilruntime.Must(rbacv1.AddToScheme(scheme))
		})

		It("deletes device plugin DaemonSet, RBAC, and ServiceAccount when present", func() {
			ownerReferences := providerOwnerReferences("SriovOperatorConfig")
			ds := &appsv1.DaemonSet{
				ObjectMeta: metav1.ObjectMeta{Name: devicePluginDaemonSetName, Namespace: testNamespace, OwnerReferences: ownerReferences},
			}
			sa := &corev1.ServiceAccount{
				ObjectMeta: metav1.ObjectMeta{Name: devicePluginServiceAccountName, Namespace: testNamespace, OwnerReferences: ownerReferences},
			}
			sccRole := &rbacv1.Role{
				ObjectMeta: metav1.ObjectMeta{Name: devicePluginSCCRoleName, Namespace: testNamespace, OwnerReferences: ownerReferences},
			}
			sccRoleBinding := &rbacv1.RoleBinding{
				ObjectMeta: metav1.ObjectMeta{Name: devicePluginSCCRoleBindingName, Namespace: testNamespace, OwnerReferences: ownerReferences},
			}
			podAccessRole := &rbacv1.Role{
				ObjectMeta: metav1.ObjectMeta{Name: devicePluginPodAccessRoleName, Namespace: testNamespace, OwnerReferences: ownerReferences},
			}
			podAccessRoleBinding := &rbacv1.RoleBinding{
				ObjectMeta: metav1.ObjectMeta{Name: devicePluginPodAccessRoleBindingName, Namespace: testNamespace, OwnerReferences: ownerReferences},
			}

			client = fake.NewClientBuilder().WithScheme(scheme).WithObjects(
				ds, sa, sccRole, sccRoleBinding, podAccessRole, podAccessRoleBinding,
			).Build()
			Expect(cleanupDevicePluginObjs(ctx, client, &sriovnetworkv1.SriovOperatorConfig{}, featuregate.New(), testRenderManifests)).To(Succeed())

			Expect(client.Get(ctx, types.NamespacedName{Namespace: testNamespace, Name: devicePluginDaemonSetName}, &appsv1.DaemonSet{})).To(MatchError(ContainSubstring("not found")))
			Expect(client.Get(ctx, types.NamespacedName{Namespace: testNamespace, Name: devicePluginServiceAccountName}, &corev1.ServiceAccount{})).To(MatchError(ContainSubstring("not found")))
			Expect(client.Get(ctx, types.NamespacedName{Namespace: testNamespace, Name: devicePluginSCCRoleName}, &rbacv1.Role{})).To(MatchError(ContainSubstring("not found")))
			Expect(client.Get(ctx, types.NamespacedName{Namespace: testNamespace, Name: devicePluginSCCRoleBindingName}, &rbacv1.RoleBinding{})).To(MatchError(ContainSubstring("not found")))
			Expect(client.Get(ctx, types.NamespacedName{Namespace: testNamespace, Name: devicePluginPodAccessRoleName}, &rbacv1.Role{})).To(MatchError(ContainSubstring("not found")))
			Expect(client.Get(ctx, types.NamespacedName{Namespace: testNamespace, Name: devicePluginPodAccessRoleBindingName}, &rbacv1.RoleBinding{})).To(MatchError(ContainSubstring("not found")))
		})

		It("succeeds when device plugin objects do not exist", func() {
			client = fake.NewClientBuilder().WithScheme(scheme).Build()
			Expect(cleanupDevicePluginObjs(ctx, client, &sriovnetworkv1.SriovOperatorConfig{}, featuregate.New(), testRenderManifests)).To(Succeed())
		})
	})

	Context("manifest-based provider cleanup", func() {
		var (
			ctx    context.Context
			scheme *runtime.Scheme
			config *sriovnetworkv1.SriovOperatorConfig
		)

		BeforeEach(func() {
			ctx = context.Background()
			config = &sriovnetworkv1.SriovOperatorConfig{}
			scheme = runtime.NewScheme()
			utilruntime.Must(corev1.AddToScheme(scheme))
			utilruntime.Must(appsv1.AddToScheme(scheme))
			utilruntime.Must(rbacv1.AddToScheme(scheme))
			utilruntime.Must(resourceapi.AddToScheme(scheme))
		})

		DescribeTable("deletes an additional resource supplied by the manifests", func(draMode bool) {
			extra := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "extra-provider-config", Namespace: testNamespace, OwnerReferences: providerOwnerReferences("SriovOperatorConfig")}}
			client := fake.NewClientBuilder().WithScheme(scheme).WithObjects(extra).Build()
			renderFn := func(path string, data *render.RenderData) ([]*unstructured.Unstructured, error) {
				objs, err := testRenderManifests(path, data)
				if err != nil {
					return nil, err
				}
				extraManifest := &unstructured.Unstructured{}
				extraManifest.SetAPIVersion("v1")
				extraManifest.SetKind("ConfigMap")
				extraManifest.SetName(extra.Name)
				extraManifest.SetNamespace(extra.Namespace)
				return append(objs, extraManifest), nil
			}
			if draMode {
				Expect(cleanupDRADriverObjs(ctx, client, config, renderFn)).To(Succeed())
			} else {
				Expect(cleanupDevicePluginObjs(ctx, client, config, featuregate.New(), renderFn)).To(Succeed())
			}
			Expect(client.Get(ctx, k8sclient.ObjectKeyFromObject(extra), &corev1.ConfigMap{})).To(MatchError(ContainSubstring("not found")))
		}, Entry("DRA", true), Entry("device plugin", false))

		It("deletes DaemonSets first and protects provider objects by UID and resource version", func() {
			objs, err := renderDRADriverObjs(config, testRenderManifests)
			Expect(err).NotTo(HaveOccurred())
			objects := make([]k8sclient.Object, 0, len(objs))
			for i, obj := range objs {
				obj.SetUID(types.UID(fmt.Sprintf("provider-object-%d", i)))
				if obj.GetNamespace() != "" {
					obj.SetOwnerReferences(providerOwnerReferences("SriovOperatorConfig"))
				}
				objects = append(objects, obj)
			}
			var deletedKinds []string
			client := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).WithInterceptorFuncs(interceptor.Funcs{
				Delete: func(ctx context.Context, c k8sclient.WithWatch, obj k8sclient.Object, opts ...k8sclient.DeleteOption) error {
					kind := obj.GetObjectKind().GroupVersionKind().Kind
					if _, ok := obj.(*resourceapi.DeviceClass); ok {
						kind = deviceClassResourceName
					}
					deletedKinds = append(deletedKinds, kind)
					deleteOptions := &k8sclient.DeleteOptions{}
					for _, opt := range opts {
						opt.ApplyToDelete(deleteOptions)
					}
					Expect(deleteOptions.Preconditions).NotTo(BeNil())
					Expect(deleteOptions.Preconditions.UID).To(HaveValue(Equal(obj.GetUID())))
					Expect(deleteOptions.Preconditions.ResourceVersion).To(HaveValue(Equal(obj.GetResourceVersion())))
					return c.Delete(ctx, obj, opts...)
				},
			}).Build()
			Expect(cleanupProviderObjs(ctx, client, objs)).To(Succeed())
			Expect(deletedKinds[0]).To(Equal(consts.DaemonSet))
			Expect(deletedKinds).To(ConsistOf([]string{
				consts.DaemonSet, clusterRoleBindingResourceName, consts.RoleBinding, consts.RoleBinding,
				clusterRoleResourceName, consts.Role, consts.Role, consts.ServiceAccount, deviceClassResourceName,
			}))
		})

		It("skips unowned namespaced objects and removes legacy device-plugin objects", func() {
			manifest := &unstructured.Unstructured{}
			manifest.SetAPIVersion("apps/v1")
			manifest.SetKind(consts.DaemonSet)
			manifest.SetName("provider-daemon")
			manifest.SetNamespace(testNamespace)

			unowned := &appsv1.DaemonSet{ObjectMeta: metav1.ObjectMeta{Name: manifest.GetName(), Namespace: testNamespace}}
			client := fake.NewClientBuilder().WithScheme(scheme).WithObjects(unowned).Build()
			Expect(cleanupProviderObjs(ctx, client, []*unstructured.Unstructured{manifest})).To(Succeed())
			Expect(client.Get(ctx, k8sclient.ObjectKeyFromObject(unowned), &appsv1.DaemonSet{})).To(Succeed())

			legacy := unowned.DeepCopy()
			legacy.OwnerReferences = providerOwnerReferences("SriovNetworkNodePolicy")
			Expect(client.Update(ctx, legacy)).To(Succeed())
			Expect(cleanupProviderObjs(ctx, client, []*unstructured.Unstructured{manifest})).To(Succeed())
			Expect(client.Get(ctx, k8sclient.ObjectKeyFromObject(legacy), &appsv1.DaemonSet{})).To(MatchError(ContainSubstring("not found")))
		})

		It("skips an unavailable DeviceClass API during cleanup", func() {
			client := fake.NewClientBuilder().WithScheme(scheme).WithInterceptorFuncs(interceptor.Funcs{
				Get: func(ctx context.Context, c k8sclient.WithWatch, key k8sclient.ObjectKey, obj k8sclient.Object, opts ...k8sclient.GetOption) error {
					if _, ok := obj.(*resourceapi.DeviceClass); ok {
						return &meta.NoKindMatchError{GroupKind: schema.GroupKind{Group: "resource.k8s.io", Kind: deviceClassResourceName}}
					}
					return c.Get(ctx, key, obj, opts...)
				},
			}).Build()
			Expect(cleanupDRADriverObjs(ctx, client, config, testRenderManifests)).To(Succeed())
		})

		It("does not delete RBAC after a DaemonSet deletion fails", func() {
			var deletedKinds []string
			ds := &appsv1.DaemonSet{ObjectMeta: metav1.ObjectMeta{
				Name: consts.DRADriverDaemonSetName, Namespace: testNamespace,
				OwnerReferences: providerOwnerReferences("SriovOperatorConfig"),
			}}
			roleBinding := &rbacv1.RoleBinding{ObjectMeta: metav1.ObjectMeta{
				Name: consts.DRADriverPodAccessRoleBindingName, Namespace: testNamespace,
				OwnerReferences: providerOwnerReferences("SriovOperatorConfig"),
			}}
			client := fake.NewClientBuilder().WithScheme(scheme).WithObjects(ds, roleBinding).WithInterceptorFuncs(interceptor.Funcs{
				Delete: func(ctx context.Context, c k8sclient.WithWatch, obj k8sclient.Object, opts ...k8sclient.DeleteOption) error {
					kind := obj.GetObjectKind().GroupVersionKind().Kind
					deletedKinds = append(deletedKinds, kind)
					if kind == consts.DaemonSet {
						return fmt.Errorf("deletion denied")
					}
					return c.Delete(ctx, obj, opts...)
				},
			}).Build()
			Expect(cleanupDRADriverObjs(ctx, client, config, testRenderManifests)).To(MatchError(ContainSubstring("deletion denied")))
			Expect(deletedKinds).To(Equal([]string{consts.DaemonSet}))
			Expect(client.Get(ctx, k8sclient.ObjectKeyFromObject(roleBinding), &rbacv1.RoleBinding{})).To(Succeed())
		})

		DescribeTable("does not delete any objects when rendering fails", func(draMode bool) {
			deletes := 0
			client := fake.NewClientBuilder().WithScheme(scheme).WithInterceptorFuncs(interceptor.Funcs{
				Delete: func(context.Context, k8sclient.WithWatch, k8sclient.Object, ...k8sclient.DeleteOption) error {
					deletes++
					return nil
				},
			}).Build()
			renderFn := func(string, *render.RenderData) ([]*unstructured.Unstructured, error) {
				return nil, fmt.Errorf("render failed")
			}
			var err error
			if draMode {
				err = cleanupDRADriverObjs(ctx, client, config, renderFn)
			} else {
				err = cleanupDevicePluginObjs(ctx, client, config, featuregate.New(), renderFn)
			}
			Expect(err).To(MatchError(ContainSubstring("render failed")))
			Expect(deletes).To(BeZero())
		}, Entry("DRA", true), Entry("device plugin", false))
	})

	Context("syncDRADriverObjs", func() {
		var (
			ctx     context.Context
			scheme  *runtime.Scheme
			client  k8sclient.Client
			dc      *sriovnetworkv1.SriovOperatorConfig
			nsSaved string
		)

		BeforeEach(func() {
			ctx = context.Background()
			nsSaved = vars.Namespace
			vars.Namespace = testNamespace
			DeferCleanup(func() { vars.Namespace = nsSaved })
			DeferCleanup(os.Setenv, "SRIOV_DRA_DRIVER_IMAGE", os.Getenv("SRIOV_DRA_DRIVER_IMAGE"))
			DeferCleanup(os.Setenv, "SRIOV_NETWORK_CONFIG_DAEMON_IMAGE", os.Getenv("SRIOV_NETWORK_CONFIG_DAEMON_IMAGE"))
			Expect(os.Setenv("SRIOV_DRA_DRIVER_IMAGE", "dra-driver-image")).To(Succeed())
			Expect(os.Setenv("SRIOV_NETWORK_CONFIG_DAEMON_IMAGE", "config-daemon-image")).To(Succeed())
			scheme = runtime.NewScheme()
			utilruntime.Must(sriovnetworkv1.AddToScheme(scheme))
			utilruntime.Must(corev1.AddToScheme(scheme))
			utilruntime.Must(appsv1.AddToScheme(scheme))
			utilruntime.Must(rbacv1.AddToScheme(scheme))
			dc = &sriovnetworkv1.SriovOperatorConfig{
				ObjectMeta: metav1.ObjectMeta{Name: consts.DefaultConfigName, Namespace: testNamespace},
			}
			client = fake.NewClientBuilder().WithScheme(scheme).WithObjects(dc).Build()
		})

		It("creates DRA driver DaemonSet and other objects", func() {
			Expect(syncDRADriverObjs(ctx, client, scheme, dc, testRenderManifests, testApplyManifest)).To(Succeed())
			ds := &appsv1.DaemonSet{}
			Expect(client.Get(ctx, types.NamespacedName{Namespace: testNamespace, Name: "sriov-dra-driver"}, ds)).To(Succeed())
			Expect(ds.Namespace).To(Equal(testNamespace))
			Expect(ds.Name).To(Equal("sriov-dra-driver"))
		})

		It("uses a fixed container CDI path and omits standalone-mode mounts", func() {
			oldCDIRoot := os.Getenv("DRA_DRIVER_CDI_ROOT")
			DeferCleanup(os.Setenv, "DRA_DRIVER_CDI_ROOT", oldCDIRoot)
			Expect(os.Setenv("DRA_DRIVER_CDI_ROOT", "/host/cdi")).To(Succeed())

			Expect(syncDRADriverObjs(ctx, client, scheme, dc, testRenderManifests, testApplyManifest)).To(Succeed())
			ds := &appsv1.DaemonSet{}
			Expect(client.Get(ctx, types.NamespacedName{Namespace: testNamespace, Name: "sriov-dra-driver"}, ds)).To(Succeed())

			container := ds.Spec.Template.Spec.Containers[0]
			Expect(container.Env).To(ContainElement(SatisfyAll(
				HaveField("Name", "CDI_ROOT"),
				HaveField("Value", "/var/run/cdi"),
			)))
			Expect(container.VolumeMounts).To(ContainElement(SatisfyAll(
				HaveField("Name", "cdi-root"),
				HaveField("MountPath", "/var/run/cdi"),
			)))
			Expect(ds.Spec.Template.Spec.Volumes).To(ContainElement(SatisfyAll(
				HaveField("Name", "cdi-root"),
				HaveField("HostPath.Path", "/host/cdi"),
			)))
			for _, name := range []string{"netns", "cni-results", "cni-bin"} {
				Expect(container.VolumeMounts).NotTo(ContainElement(HaveField("Name", name)))
				Expect(ds.Spec.Template.Spec.Volumes).NotTo(ContainElement(HaveField("Name", name)))
			}
		})

		It("uses GetNodeSelectorForDRADriver for DaemonSet node selector", func() {
			dc.Spec.ConfigDaemonNodeSelector = map[string]string{"custom-label": "value"}
			Expect(syncDRADriverObjs(ctx, client, scheme, dc, testRenderManifests, testApplyManifest)).To(Succeed())
			ds := &appsv1.DaemonSet{}
			Expect(client.Get(ctx, types.NamespacedName{Namespace: testNamespace, Name: "sriov-dra-driver"}, ds)).To(Succeed())
			Expect(ds.Spec.Template.Spec.NodeSelector).To(Equal(dc.Spec.ConfigDaemonNodeSelector))
		})

		It("fails when DeviceClass API is unavailable", func() {
			applyFn := func(ctx context.Context, c k8sclient.Client, obj *unstructured.Unstructured) error {
				if obj.GetKind() == deviceClassResourceName {
					return &meta.NoKindMatchError{
						GroupKind: schema.GroupVersionKind{Group: "resource.k8s.io", Version: "v1", Kind: "DeviceClass"}.GroupKind(),
					}
				}
				return testApplyManifest(ctx, c, obj)
			}
			err := syncDRADriverObjs(ctx, client, scheme, dc, testRenderManifests, applyFn)
			Expect(err).To(MatchError(ContainSubstring("DeviceClass API is unavailable")))
		})
	})

	Context("syncDsObject DeviceClass apply", func() {
		var (
			ctx     context.Context
			scheme  *runtime.Scheme
			desired *unstructured.Unstructured
		)

		BeforeEach(func() {
			ctx = context.Background()
			scheme = runtime.NewScheme()
			utilruntime.Must(resourceapi.AddToScheme(scheme))
			desired = &unstructured.Unstructured{Object: map[string]interface{}{
				"apiVersion": "resource.k8s.io/v1",
				"kind":       deviceClassResourceName,
				"metadata":   map[string]interface{}{"name": consts.DRADriverBaseDeviceClassName},
				"spec": map[string]interface{}{"selectors": []interface{}{
					map[string]interface{}{"cel": map[string]interface{}{"expression": "true"}},
				}},
			}}
		})

		DescribeTable("reconciles the fixed-name object regardless of ownership labels", func(exists bool, labels map[string]string) {
			client := fake.NewClientBuilder().WithScheme(scheme).Build()
			existing := &resourceapi.DeviceClass{
				ObjectMeta: metav1.ObjectMeta{Name: desired.GetName(), Labels: labels},
				Spec: resourceapi.DeviceClassSpec{Selectors: []resourceapi.DeviceSelector{
					{CEL: &resourceapi.CELDeviceSelector{Expression: "false"}},
				}},
			}
			if exists {
				Expect(client.Create(ctx, existing)).To(Succeed())
			}
			err := syncDsObject(ctx, client, scheme, &sriovnetworkv1.SriovOperatorConfig{}, desired, testApplyManifest)
			Expect(err).NotTo(HaveOccurred())
			actual := &resourceapi.DeviceClass{}
			Expect(client.Get(ctx, k8sclient.ObjectKeyFromObject(existing), actual)).To(Succeed())
			Expect(objectHasOperatorManagedLabel(actual)).To(BeTrue())
			Expect(actual.Spec.Selectors[0].CEL.Expression).To(Equal("true"))
			if labels["custom-label"] != "" {
				Expect(actual.Labels["custom-label"]).To(Equal(labels["custom-label"]))
			}
		},
			Entry("creates an absent object", false, nil),
			Entry("updates an operator-managed object", true, maps.Clone(consts.DRAOperatorLabels)),
			Entry("adopts an unmarked object", true, nil),
			Entry("adopts an object marked for another manager", true, map[string]string{consts.DRAGeneratedByLabel: "other", "custom-label": "preserve"}),
		)

		DescribeTable("does not write when the apply read fails", func(readErr error, message string) {
			client := fake.NewClientBuilder().WithScheme(scheme).WithInterceptorFuncs(interceptor.Funcs{
				Get: func(context.Context, k8sclient.WithWatch, k8sclient.ObjectKey, k8sclient.Object, ...k8sclient.GetOption) error {
					return readErr
				},
				Create: func(context.Context, k8sclient.WithWatch, k8sclient.Object, ...k8sclient.CreateOption) error {
					Fail("create must not run after a failed read")
					return nil
				},
				Update: func(context.Context, k8sclient.WithWatch, k8sclient.Object, ...k8sclient.UpdateOption) error {
					Fail("update must not run after a failed read")
					return nil
				},
			}).Build()
			err := syncDsObject(ctx, client, scheme, &sriovnetworkv1.SriovOperatorConfig{}, desired, testApplyManifest)
			Expect(err).To(MatchError(ContainSubstring(message)))
			Expect(err).To(MatchError(readErr))
		},
			Entry("read error", fmt.Errorf("read denied"), "read denied"),
			Entry("unavailable API", &meta.NoKindMatchError{
				GroupKind: schema.GroupKind{Group: "resource.k8s.io", Kind: deviceClassResourceName},
			}, "DeviceClass API is unavailable"),
		)
	})

})
