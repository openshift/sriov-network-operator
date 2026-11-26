package featuregate

import (
	"sync"
	"sync/atomic"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/consts"
)

var _ = Describe("FeatureGate", func() {
	Context("IsEnabled", func() {
		It("return false for unknown feature", func() {
			Expect(New().IsEnabled("something")).To(BeFalse())
		})
	})
	Context("Init", func() {
		It("applies unchanged configuration only once across concurrent initializations", func() {
			f := New()
			features := map[string]bool{"extra": true}
			var updates atomic.Int32
			var wg sync.WaitGroup
			for range 20 {
				wg.Go(func() {
					defer GinkgoRecover()
					for range 100 {
						if f.Set(features) {
							updates.Add(1)
						}
						Expect(f.IsEnabled("extra")).To(BeTrue(), "feature is disabled after initialization")
						_ = f.String()
					}
				})
			}
			wg.Wait()
			Expect(updates.Load()).To(Equal(int32(1)))
		})

		It("skips equivalent effective states without allocating", func() {
			f := NewWithDefaultFeatures(map[string]bool{"enabled": true, "disabled": false})
			Expect(f.Set(nil)).To(BeTrue())
			Expect(f.Set(nil)).To(BeFalse())
			Expect(f.Set(map[string]bool{})).To(BeFalse())
			Expect(f.Set(map[string]bool{"enabled": true, "disabled": false})).To(BeFalse())

			overrides := map[string]bool{"enabled": false, "extra": true}
			Expect(f.Set(overrides)).To(BeTrue())
			Expect(f.Set(map[string]bool{"enabled": false, "extra": true})).To(BeFalse())
			Expect(testing.AllocsPerRun(100, func() { f.Set(overrides) })).To(BeZero())
		})

		It("detects mutated inputs and restores defaults when overrides are removed", func() {
			f := NewWithDefaultFeatures(map[string]bool{"enabled": true})
			overrides := map[string]bool{"enabled": false, "extra": true}
			Expect(f.Set(overrides)).To(BeTrue())
			overrides["extra"] = false
			Expect(f.IsEnabled("extra")).To(BeTrue(), "the active state must not alias the input map")
			Expect(f.Set(overrides)).To(BeTrue())
			Expect(f.IsEnabled("extra")).To(BeFalse())
			delete(overrides, "extra")
			Expect(f.Set(overrides)).To(BeTrue(), "removed false-valued features must also be detected")
			Expect(f.String()).NotTo(ContainSubstring("extra:"))
			Expect(f.Set(nil)).To(BeTrue())
			Expect(f.IsEnabled("enabled")).To(BeTrue())
			Expect(f.Set(nil)).To(BeFalse())
		})

		It("distinguishes an absent feature from an explicitly disabled feature", func() {
			f := NewWithDefaultFeatures(nil)
			Expect(f.Set(map[string]bool{"extra": false})).To(BeTrue())
			Expect(f.Set(map[string]bool{"other": false})).To(BeTrue())
			Expect(f.String()).To(Equal("other:false"))
			Expect(f.Set(nil)).To(BeTrue())
			Expect(f.Set(map[string]bool{})).To(BeFalse())
		})

		It("should update the state", func() {
			f := New()
			f.Set(map[string]bool{"feat1": true, "feat2": false})
			Expect(f.IsEnabled("feat1")).To(BeTrue())
			Expect(f.IsEnabled("feat2")).To(BeFalse())
		})
		It("should apply default feature state", func() {
			f := NewWithDefaultFeatures(map[string]bool{"default1": true, "default2": false})
			f.Set(nil)
			Expect(f.IsEnabled("default1")).To(BeTrue())
			Expect(f.IsEnabled("default2")).To(BeFalse())
		})
		It("should override default feature state", func() {
			f := NewWithDefaultFeatures(map[string]bool{"feat1": false, "feat2": true})
			f.Set(map[string]bool{"feat1": true})
			Expect(f.IsEnabled("feat1")).To(BeTrue())
			Expect(f.IsEnabled("feat2")).To(BeTrue())
		})
		It("should apply real default feature states", func() {
			f := New()
			f.Set(nil)
			Expect(f.IsEnabled(consts.ParallelNicConfigFeatureGate)).To(BeFalse())
			Expect(f.IsEnabled(consts.ResourceInjectorMatchConditionFeatureGate)).To(BeFalse())
			Expect(f.IsEnabled(consts.MetricsExporterFeatureGate)).To(BeFalse())
			Expect(f.IsEnabled(consts.ManageSoftwareBridgesFeatureGate)).To(BeFalse())
			Expect(f.IsEnabled(consts.BlockDevicePluginUntilConfiguredFeatureGate)).To(BeTrue())
			Expect(f.IsEnabled(consts.MellanoxFirmwareResetFeatureGate)).To(BeTrue())
		})
		It("should override real default feature state", func() {
			f := New()
			f.Set(map[string]bool{consts.BlockDevicePluginUntilConfiguredFeatureGate: false})
			Expect(f.IsEnabled(consts.BlockDevicePluginUntilConfiguredFeatureGate)).To(BeFalse())
		})
	})
	Context("String", func() {
		It("no features", func() {
			Expect(New().String()).To(Equal(""))
		})
		It("print feature state", func() {
			f := New()
			f.Set(map[string]bool{"feat1": true, "feat2": false})
			Expect(f.String()).To(And(ContainSubstring("feat1:true"), ContainSubstring("feat2:false")))
		})
	})
})
