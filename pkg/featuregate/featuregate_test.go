package featuregate

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("FeatureGate", func() {
	Context("IsEnabled", func() {
		It("return false for unknown feature", func() {
			Expect(New().IsEnabled("something")).To(BeFalse())
		})
	})
	Context("Init", func() {
		It("should update the state", func() {
			f := New()
			f.Init(map[string]bool{"feat1": true, "feat2": false})
			Expect(f.IsEnabled("feat1")).To(BeTrue())
			Expect(f.IsEnabled("feat2")).To(BeFalse())
		})
	})
	Context("String", func() {
		It("no features", func() {
			Expect(New().String()).To(Equal(""))
		})
		It("print feature state", func() {
			f := New()
			f.Init(map[string]bool{"feat1": true, "feat2": false})
			Expect(f.String()).To(And(ContainSubstring("feat1:true"), ContainSubstring("feat2:false")))
		})
	})
})
