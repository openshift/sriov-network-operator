package store

import (
	"sync"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	sriovnetworkv1 "github.com/k8snetworkplumbingwg/sriov-network-operator/api/v1"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/consts"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/test/util/fakefilesystem"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/test/util/helpers"
)

func getStore() Store {
	s := New()
	Expect(s).NotTo(BeNil())
	return s
}

var _ = Describe("OVS store", func() {
	It("load data from disk", func() {
		helpers.GinkgoConfigureFakeFS(&fakefilesystem.FS{
			Dirs:  []string{"/host/etc/sriov-operator/"},
			Files: map[string][]byte{"/host" + consts.ManagedOVSBridgesPath: []byte(`{"test": {"name": "test"}}`)}})
		s := getStore()
		b, err := s.GetManagedOVSBridge("test")
		Expect(err).NotTo(HaveOccurred())
		Expect(b).NotTo(BeNil())
		Expect(b.Name).To(Equal("test"))
	})
	It("should read saved data", func() {
		helpers.GinkgoConfigureFakeFS(&fakefilesystem.FS{})
		s := getStore()
		testObj := &sriovnetworkv1.OVSConfigExt{Name: "test", Bridge: sriovnetworkv1.OVSBridgeConfig{DatapathType: "test"}}
		Expect(s.AddManagedOVSBridge(testObj)).NotTo(HaveOccurred())
		ret, err := s.GetManagedOVSBridge("test")
		Expect(err).NotTo(HaveOccurred())
		Expect(ret).To(Equal(testObj))
		retMap, err := s.GetManagedOVSBridges()
		Expect(err).NotTo(HaveOccurred())
		Expect(retMap["test"]).To(Equal(testObj))
	})
	It("should persist writes on disk", func() {
		helpers.GinkgoConfigureFakeFS(&fakefilesystem.FS{})
		s := getStore()
		testObj := &sriovnetworkv1.OVSConfigExt{Name: "test", Bridge: sriovnetworkv1.OVSBridgeConfig{DatapathType: "test"}}
		Expect(s.AddManagedOVSBridge(testObj)).NotTo(HaveOccurred())
		helpers.GinkgoAssertFileContentsEquals("/host"+consts.ManagedOVSBridgesPath,
			`{"test":{"name":"test","bridge":{"datapathType":"test"}}}`)
		Expect(s.RemoveManagedOVSBridge("test")).NotTo(HaveOccurred())
		helpers.GinkgoAssertFileContentsEquals("/host"+consts.ManagedOVSBridgesPath, "{}")
	})
	It("stash/restore", func() {
		s := &ovsStore{
			lock:  &sync.RWMutex{},
			cache: make(map[string]sriovnetworkv1.OVSConfigExt),
		}
		s.cache["a"] = sriovnetworkv1.OVSConfigExt{Name: "a"}
		s.cache["b"] = sriovnetworkv1.OVSConfigExt{Name: "b"}
		aRestore := s.putCacheEntryToStash("a")
		bRestore := s.putCacheEntryToStash("b")
		cRestore := s.putCacheEntryToStash("c")
		s.cache["a"] = sriovnetworkv1.OVSConfigExt{Name: "replaced"}
		delete(s.cache, "b")
		s.cache["c"] = sriovnetworkv1.OVSConfigExt{Name: "created"}

		aRestore()
		bRestore()
		cRestore()
		Expect(s.cache["a"].Name).To(Equal("a"))
		Expect(s.cache["b"].Name).To(Equal("b"))
		Expect(s.cache).NotTo(HaveKey("c"))
	})
})
