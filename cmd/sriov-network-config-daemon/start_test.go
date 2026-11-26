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
package main

import (
	"os"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	sriovnetworkv1 "github.com/k8snetworkplumbingwg/sriov-network-operator/api/v1"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/consts"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/featuregate"
	snolog "github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/log"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/vars"
)

var _ = Describe("Start", func() {
	Context("stringList", func() {
		It("appends comma-separated values across repeated flags", func() {
			var plugins stringList
			Expect(plugins.String()).To(BeEmpty())
			Expect(plugins.Set("mellanox,intel")).To(Succeed())
			Expect(plugins.Set("virtual")).To(Succeed())
			Expect(plugins).To(Equal(stringList{"mellanox", "intel", "virtual"}))
			Expect(plugins.String()).To(Equal("mellanox,intel,virtual"))
			Expect(plugins.Type()).To(Equal("CommaSeparatedString"))
		})

		DescribeTable("rejects empty plugin names", func(value string) {
			var plugins stringList
			Expect(plugins.Set(value)).To(MatchError("empty plugin name"))
		},
			Entry("empty flag", ""),
			Entry("leading comma", ",mellanox"),
			Entry("trailing comma", "mellanox,"),
			Entry("consecutive commas", "mellanox,,intel"),
		)
	})

	Context("configGlobalVariables", func() {
		BeforeEach(func() {
			savedOpts := startOpts
			savedSystemd, savedParallel := vars.UsingSystemdMode, vars.ParallelNicConfig
			savedBridges, savedSocket := vars.ManageSoftwareBridges, vars.OVSDBSocketPath
			savedNode, savedScheme := vars.NodeName, vars.Scheme
			DeferCleanup(func() {
				startOpts = savedOpts
				vars.UsingSystemdMode, vars.ParallelNicConfig = savedSystemd, savedParallel
				vars.ManageSoftwareBridges, vars.OVSDBSocketPath = savedBridges, savedSocket
				vars.NodeName, vars.Scheme = savedNode, savedScheme
			})
			startOpts.nodeName = ""
			startOpts.systemd = false
			startOpts.parallelNicConfig = false
			startOpts.manageSoftwareBridges = false
			startOpts.disabledPlugins = nil
			startOpts.ovsSocketPath = "unix:///test/ovs.sock"
			GinkgoT().Setenv("NODE_NAME", "worker-env")
		})

		It("uses NODE_NAME when no node-name flag is supplied", func() {
			Expect(configGlobalVariables()).To(Succeed())
			Expect(vars.NodeName).To(Equal("worker-env"))
			Expect(startOpts.nodeName).To(Equal("worker-env"))
			Expect(vars.Scheme).To(BeIdenticalTo(scheme))
		})

		It("prefers the node-name flag over NODE_NAME", func() {
			startOpts.nodeName = "worker-flag"
			Expect(configGlobalVariables()).To(Succeed())
			Expect(vars.NodeName).To(Equal("worker-flag"))
		})

		DescribeTable("requires a node name", func(unset bool) {
			if unset {
				Expect(os.Unsetenv("NODE_NAME")).To(Succeed())
			} else {
				GinkgoT().Setenv("NODE_NAME", "")
			}
			Expect(configGlobalVariables()).To(MatchError("node-name is required"))
		},
			Entry("missing environment variable", true),
			Entry("empty environment variable", false),
		)

		DescribeTable("applies runtime flags and the OVS socket path", func(enabled bool) {
			vars.UsingSystemdMode = !enabled
			vars.ParallelNicConfig = !enabled
			vars.ManageSoftwareBridges = !enabled
			startOpts.systemd = enabled
			startOpts.parallelNicConfig = enabled
			startOpts.manageSoftwareBridges = enabled
			Expect(configGlobalVariables()).To(Succeed())
			Expect(vars.UsingSystemdMode).To(Equal(enabled))
			Expect(vars.ParallelNicConfig).To(Equal(enabled))
			Expect(vars.ManageSoftwareBridges).To(Equal(enabled))
			Expect(vars.OVSDBSocketPath).To(Equal(startOpts.ovsSocketPath))
		},
			Entry("enabled", true),
			Entry("disabled", false),
		)

		It("accepts a disableable plugin", func() {
			startOpts.disabledPlugins = stringList{"mellanox"}
			Expect(configGlobalVariables()).To(Succeed())
		})

		It("rejects a plugin that cannot be disabled", func() {
			startOpts.disabledPlugins = stringList{"generic"}
			Expect(configGlobalVariables()).To(MatchError("generic plugin cannot be disabled"))
		})
	})

	Context("initFeatureGates", func() {
		BeforeEach(func() {
			saved := vars.FeatureGate
			DeferCleanup(func() { vars.FeatureGate = saved })
			vars.FeatureGate = featuregate.New()
		})

		DescribeTable("uses the configured feature gate without changing the spec", func(draEnabled bool) {
			config := &sriovnetworkv1.SriovOperatorConfig{
				Spec: sriovnetworkv1.SriovOperatorConfigSpec{FeatureGates: map[string]bool{
					consts.DynamicResourceAllocationFeatureGate: draEnabled,
					consts.ParallelNicConfigFeatureGate:         true,
				}},
			}
			original := config.DeepCopy()
			sharedGates := vars.FeatureGate
			sharedGates.Set(map[string]bool{consts.MetricsExporterFeatureGate: true})
			initFeatureGates(config)
			Expect(vars.FeatureGate.IsEnabled(consts.DynamicResourceAllocationFeatureGate)).To(Equal(draEnabled))
			Expect(vars.FeatureGate.IsEnabled(consts.ParallelNicConfigFeatureGate)).To(BeTrue())
			Expect(vars.FeatureGate.IsEnabled(consts.BlockDevicePluginUntilConfiguredFeatureGate)).To(BeTrue())
			Expect(vars.FeatureGate.IsEnabled(consts.MetricsExporterFeatureGate)).To(BeFalse(), "removed overrides must reset to their defaults")
			Expect(vars.FeatureGate).To(BeIdenticalTo(sharedGates))
			Expect(config).To(Equal(original))
		},
			Entry("device plugin", false),
			Entry("DRA", true),
		)

		It("initializes defaults when no feature gates were requested", func() {
			config := &sriovnetworkv1.SriovOperatorConfig{}
			initFeatureGates(config)
			Expect(vars.FeatureGate.IsEnabled(consts.DynamicResourceAllocationFeatureGate)).To(BeFalse())
			Expect(vars.FeatureGate.IsEnabled(consts.BlockDevicePluginUntilConfiguredFeatureGate)).To(BeTrue())
			Expect(config.Spec.FeatureGates).To(BeNil())
		})
	})

	Context("initLogLevel", func() {
		BeforeEach(func() {
			saved := snolog.GetLogLevel()
			DeferCleanup(snolog.SetLogLevel, saved)
		})

		DescribeTable("applies the configured verbosity", func(level int) {
			config := &sriovnetworkv1.SriovOperatorConfig{Spec: sriovnetworkv1.SriovOperatorConfigSpec{LogLevel: level}}
			Expect(initLogLevel(config)).To(Succeed())
			Expect(snolog.GetLogLevel()).To(Equal(level))
		},
			Entry("normal", 0),
			Entry("verbose", 1),
			Entry("debug", 2),
		)
	})
})
