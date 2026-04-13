package openshift_test

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	openshiftconfigv1 "github.com/openshift/api/config/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/orchestrator/openshift"
)

// createAPIServerWithTLS creates an APIServer CR with the given TLS profile and adherence policy,
// and registers a DeferCleanup to delete it after the test.
func createAPIServerWithTLS(ctx context.Context, profile *openshiftconfigv1.TLSSecurityProfile, adherence openshiftconfigv1.TLSAdherencePolicy) {
	apiServer := &openshiftconfigv1.APIServer{
		ObjectMeta: metav1.ObjectMeta{
			Name: "cluster",
		},
		Spec: openshiftconfigv1.APIServerSpec{
			TLSSecurityProfile: profile,
			TLSAdherence:       adherence,
		},
	}
	err := k8sClient.Create(ctx, apiServer)
	ExpectWithOffset(1, err).NotTo(HaveOccurred())

	DeferCleanup(func() {
		err := k8sClient.Delete(ctx, apiServer)
		ExpectWithOffset(1, err).NotTo(HaveOccurred())
	})
}

var _ = Describe("TLS Profile", func() {
	var orchestrator *openshift.OpenshiftOrchestrator
	var ctx context.Context

	BeforeEach(func() {
		var err error
		ctx = context.Background()
		orchestrator, err = openshift.New()
		Expect(err).NotTo(HaveOccurred())
	})

	Context("when APIServer resource does not exist", func() {
		It("should return an error", func() {
			tlsConfig, err := orchestrator.GetTLSConfig(ctx)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("failed to get config.openshift.io/v1 APIServer resource"))
			Expect(tlsConfig).To(BeNil())
		})
	})

	Context("when APIServer has no tlsSecurityProfile", func() {
		BeforeEach(func() {
			apiServer := &openshiftconfigv1.APIServer{
				ObjectMeta: metav1.ObjectMeta{
					Name: "cluster",
				},
				Spec: openshiftconfigv1.APIServerSpec{
					Audit: openshiftconfigv1.Audit{
						Profile: openshiftconfigv1.DefaultAuditProfileType,
					},
				},
			}
			err := k8sClient.Create(ctx, apiServer)
			Expect(err).NotTo(HaveOccurred())

			DeferCleanup(func() {
				err := k8sClient.Delete(ctx, apiServer)
				Expect(err).NotTo(HaveOccurred())
			})
		})

		It("should return nil TLSConfig (use component defaults)", func() {
			tlsConfig, err := orchestrator.GetTLSConfig(ctx)
			Expect(err).NotTo(HaveOccurred())
			Expect(tlsConfig).To(BeNil())
		})
	})

	Context("when APIServer has Intermediate profile with StrictAllComponents", func() {
		BeforeEach(func() {
			createAPIServerWithTLS(ctx, &openshiftconfigv1.TLSSecurityProfile{
				Type: openshiftconfigv1.TLSProfileIntermediateType,
			}, openshiftconfigv1.TLSAdherencePolicyStrictAllComponents)
		})

		It("should return resolved Intermediate profile", func() {
			tlsConfig, err := orchestrator.GetTLSConfig(ctx)
			Expect(err).NotTo(HaveOccurred())
			Expect(tlsConfig).NotTo(BeNil())
			Expect(tlsConfig.MinTLSVersion).To(Equal("VersionTLS12"))
			Expect(tlsConfig.CipherSuites).NotTo(BeEmpty())
		})
	})

	Context("when APIServer has Modern profile with StrictAllComponents", func() {
		BeforeEach(func() {
			createAPIServerWithTLS(ctx, &openshiftconfigv1.TLSSecurityProfile{
				Type: openshiftconfigv1.TLSProfileModernType,
			}, openshiftconfigv1.TLSAdherencePolicyStrictAllComponents)
		})

		It("should return resolved Modern profile with TLS 1.3", func() {
			tlsConfig, err := orchestrator.GetTLSConfig(ctx)
			Expect(err).NotTo(HaveOccurred())
			Expect(tlsConfig).NotTo(BeNil())
			Expect(tlsConfig.MinTLSVersion).To(Equal("VersionTLS13"))
		})
	})

	Context("when APIServer has Old profile with StrictAllComponents", func() {
		BeforeEach(func() {
			createAPIServerWithTLS(ctx, &openshiftconfigv1.TLSSecurityProfile{
				Type: openshiftconfigv1.TLSProfileOldType,
			}, openshiftconfigv1.TLSAdherencePolicyStrictAllComponents)
		})

		It("should return an error because Old profile uses TLS version below 1.2", func() {
			tlsConfig, err := orchestrator.GetTLSConfig(ctx)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("minimum TLS version must be VersionTLS12 or higher"))
			Expect(tlsConfig).To(BeNil())
		})
	})

	Context("when APIServer has Custom profile with full config (OpenSSL ciphers)", func() {
		BeforeEach(func() {
			createAPIServerWithTLS(ctx, &openshiftconfigv1.TLSSecurityProfile{
				Type: openshiftconfigv1.TLSProfileCustomType,
				Custom: &openshiftconfigv1.CustomTLSProfile{
					TLSProfileSpec: openshiftconfigv1.TLSProfileSpec{
						Ciphers:       []string{"ECDHE-ECDSA-AES128-GCM-SHA256", "ECDHE-RSA-AES128-GCM-SHA256"},
						MinTLSVersion: openshiftconfigv1.VersionTLS12,
					},
				},
			}, openshiftconfigv1.TLSAdherencePolicyStrictAllComponents)
		})

		It("should convert OpenSSL ciphers to IANA format and set minVersion", func() {
			tlsConfig, err := orchestrator.GetTLSConfig(ctx)
			Expect(err).NotTo(HaveOccurred())
			Expect(tlsConfig).NotTo(BeNil())
			Expect(tlsConfig.CipherSuites).To(Equal("TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256"))
			Expect(tlsConfig.MinTLSVersion).To(Equal("VersionTLS12"))
		})
	})

	Context("when APIServer has Custom profile with IANA-format ciphers", func() {
		BeforeEach(func() {
			createAPIServerWithTLS(ctx, &openshiftconfigv1.TLSSecurityProfile{
				Type: openshiftconfigv1.TLSProfileCustomType,
				Custom: &openshiftconfigv1.CustomTLSProfile{
					TLSProfileSpec: openshiftconfigv1.TLSProfileSpec{
						Ciphers:       []string{"TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256", "TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384"},
						MinTLSVersion: openshiftconfigv1.VersionTLS12,
					},
				},
			}, openshiftconfigv1.TLSAdherencePolicyStrictAllComponents)
		})

		It("should pass through IANA ciphers directly", func() {
			tlsConfig, err := orchestrator.GetTLSConfig(ctx)
			Expect(err).NotTo(HaveOccurred())
			Expect(tlsConfig).NotTo(BeNil())
			Expect(tlsConfig.CipherSuites).To(Equal("TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384"))
			Expect(tlsConfig.MinTLSVersion).To(Equal("VersionTLS12"))
		})
	})

	Context("when APIServer has Custom profile with mixed OpenSSL and IANA ciphers", func() {
		BeforeEach(func() {
			createAPIServerWithTLS(ctx, &openshiftconfigv1.TLSSecurityProfile{
				Type: openshiftconfigv1.TLSProfileCustomType,
				Custom: &openshiftconfigv1.CustomTLSProfile{
					TLSProfileSpec: openshiftconfigv1.TLSProfileSpec{
						Ciphers: []string{
							"ECDHE-RSA-AES128-GCM-SHA256",
							"TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384",
						},
						MinTLSVersion: openshiftconfigv1.VersionTLS13,
					},
				},
			}, openshiftconfigv1.TLSAdherencePolicyStrictAllComponents)
		})

		It("should handle both formats correctly", func() {
			tlsConfig, err := orchestrator.GetTLSConfig(ctx)
			Expect(err).NotTo(HaveOccurred())
			Expect(tlsConfig).NotTo(BeNil())
			Expect(tlsConfig.CipherSuites).To(Equal("TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384"))
			Expect(tlsConfig.MinTLSVersion).To(Equal("VersionTLS13"))
		})
	})

	Context("when APIServer has Custom profile with TLS 1.3 ciphers (same name in both formats)", func() {
		BeforeEach(func() {
			createAPIServerWithTLS(ctx, &openshiftconfigv1.TLSSecurityProfile{
				Type: openshiftconfigv1.TLSProfileCustomType,
				Custom: &openshiftconfigv1.CustomTLSProfile{
					TLSProfileSpec: openshiftconfigv1.TLSProfileSpec{
						Ciphers: []string{"TLS_AES_128_GCM_SHA256", "TLS_AES_256_GCM_SHA384"},
					},
				},
			}, openshiftconfigv1.TLSAdherencePolicyStrictAllComponents)
		})

		It("should return TLS 1.3 ciphers correctly", func() {
			tlsConfig, err := orchestrator.GetTLSConfig(ctx)
			Expect(err).NotTo(HaveOccurred())
			Expect(tlsConfig).NotTo(BeNil())
			Expect(tlsConfig.CipherSuites).To(Equal("TLS_AES_128_GCM_SHA256,TLS_AES_256_GCM_SHA384"))
			Expect(tlsConfig.MinTLSVersion).To(BeEmpty())
		})
	})

	Context("when APIServer has Custom profile with ciphers only (no minTLSVersion)", func() {
		BeforeEach(func() {
			createAPIServerWithTLS(ctx, &openshiftconfigv1.TLSSecurityProfile{
				Type: openshiftconfigv1.TLSProfileCustomType,
				Custom: &openshiftconfigv1.CustomTLSProfile{
					TLSProfileSpec: openshiftconfigv1.TLSProfileSpec{
						Ciphers: []string{"ECDHE-RSA-AES256-GCM-SHA384"},
					},
				},
			}, openshiftconfigv1.TLSAdherencePolicyStrictAllComponents)
		})

		It("should return only CipherSuites set, MinTLSVersion empty", func() {
			tlsConfig, err := orchestrator.GetTLSConfig(ctx)
			Expect(err).NotTo(HaveOccurred())
			Expect(tlsConfig).NotTo(BeNil())
			Expect(tlsConfig.CipherSuites).To(Equal("TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384"))
			Expect(tlsConfig.MinTLSVersion).To(BeEmpty())
		})
	})

	Context("when APIServer has Custom profile with minTLSVersion only (no ciphers)", func() {
		BeforeEach(func() {
			createAPIServerWithTLS(ctx, &openshiftconfigv1.TLSSecurityProfile{
				Type: openshiftconfigv1.TLSProfileCustomType,
				Custom: &openshiftconfigv1.CustomTLSProfile{
					TLSProfileSpec: openshiftconfigv1.TLSProfileSpec{
						MinTLSVersion: openshiftconfigv1.VersionTLS13,
					},
				},
			}, openshiftconfigv1.TLSAdherencePolicyStrictAllComponents)
		})

		It("should return only MinTLSVersion set, CipherSuites empty", func() {
			tlsConfig, err := orchestrator.GetTLSConfig(ctx)
			Expect(err).NotTo(HaveOccurred())
			Expect(tlsConfig).NotTo(BeNil())
			Expect(tlsConfig.CipherSuites).To(BeEmpty())
			Expect(tlsConfig.MinTLSVersion).To(Equal("VersionTLS13"))
		})
	})

	Context("when APIServer custom profile requests weak TLS minimum (below TLS 1.2)", func() {
		BeforeEach(func() {
			createAPIServerWithTLS(ctx, &openshiftconfigv1.TLSSecurityProfile{
				Type: openshiftconfigv1.TLSProfileCustomType,
				Custom: &openshiftconfigv1.CustomTLSProfile{
					TLSProfileSpec: openshiftconfigv1.TLSProfileSpec{
						MinTLSVersion: openshiftconfigv1.VersionTLS11,
					},
				},
			}, openshiftconfigv1.TLSAdherencePolicyStrictAllComponents)
		})

		It("should return an error for TLS version below 1.2", func() {
			tlsConfig, err := orchestrator.GetTLSConfig(ctx)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("minimum TLS version must be VersionTLS12 or higher"))
			Expect(tlsConfig).To(BeNil())
		})
	})

	Context("when APIServer custom profile requests invalid TLS version string", func() {
		BeforeEach(func() {
			createAPIServerWithTLS(ctx, &openshiftconfigv1.TLSSecurityProfile{
				Type: openshiftconfigv1.TLSProfileCustomType,
				Custom: &openshiftconfigv1.CustomTLSProfile{
					TLSProfileSpec: openshiftconfigv1.TLSProfileSpec{
						MinTLSVersion: "InvalidVersion",
					},
				},
			}, openshiftconfigv1.TLSAdherencePolicyStrictAllComponents)
		})

		It("should return an error for invalid TLS version", func() {
			tlsConfig, err := orchestrator.GetTLSConfig(ctx)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("failed to parse TLS version"))
			Expect(tlsConfig).To(BeNil())
		})
	})

	Context("when APIServer has Custom profile type with nil Custom field", func() {
		BeforeEach(func() {
			createAPIServerWithTLS(ctx, &openshiftconfigv1.TLSSecurityProfile{
				Type:   openshiftconfigv1.TLSProfileCustomType,
				Custom: nil,
			}, openshiftconfigv1.TLSAdherencePolicyStrictAllComponents)
		})

		It("should return nil TLSConfig (use component defaults)", func() {
			tlsConfig, err := orchestrator.GetTLSConfig(ctx)
			Expect(err).NotTo(HaveOccurred())
			Expect(tlsConfig).To(BeNil())
		})
	})

	Context("when APIServer has an unknown profile type", func() {
		BeforeEach(func() {
			createAPIServerWithTLS(ctx, &openshiftconfigv1.TLSSecurityProfile{
				Type: "UnknownType",
			}, openshiftconfigv1.TLSAdherencePolicyStrictAllComponents)
		})

		It("should return nil TLSConfig (use component defaults)", func() {
			tlsConfig, err := orchestrator.GetTLSConfig(ctx)
			Expect(err).NotTo(HaveOccurred())
			Expect(tlsConfig).To(BeNil())
		})
	})

	Context("when APIServer has Custom profile with insecure ciphers", func() {
		BeforeEach(func() {
			createAPIServerWithTLS(ctx, &openshiftconfigv1.TLSSecurityProfile{
				Type: openshiftconfigv1.TLSProfileCustomType,
				Custom: &openshiftconfigv1.CustomTLSProfile{
					TLSProfileSpec: openshiftconfigv1.TLSProfileSpec{
						Ciphers: []string{
							"TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256",
							"TLS_RSA_WITH_RC4_128_SHA",
							"TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384",
						},
						MinTLSVersion: openshiftconfigv1.VersionTLS12,
					},
				},
			}, openshiftconfigv1.TLSAdherencePolicyStrictAllComponents)
		})

		It("should filter out insecure ciphers", func() {
			tlsConfig, err := orchestrator.GetTLSConfig(ctx)
			Expect(err).NotTo(HaveOccurred())
			Expect(tlsConfig).NotTo(BeNil())
			Expect(tlsConfig.CipherSuites).To(Equal("TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384"))
			Expect(tlsConfig.CipherSuites).NotTo(ContainSubstring("RC4"))
		})
	})

	Context("when APIServer has Custom profile with insecure OpenSSL-format cipher", func() {
		BeforeEach(func() {
			createAPIServerWithTLS(ctx, &openshiftconfigv1.TLSSecurityProfile{
				Type: openshiftconfigv1.TLSProfileCustomType,
				Custom: &openshiftconfigv1.CustomTLSProfile{
					TLSProfileSpec: openshiftconfigv1.TLSProfileSpec{
						Ciphers: []string{
							"ECDHE-RSA-AES128-GCM-SHA256",
							"DES-CBC3-SHA",
						},
						MinTLSVersion: openshiftconfigv1.VersionTLS12,
					},
				},
			}, openshiftconfigv1.TLSAdherencePolicyStrictAllComponents)
		})

		It("should filter out insecure OpenSSL-format cipher and keep secure ones", func() {
			tlsConfig, err := orchestrator.GetTLSConfig(ctx)
			Expect(err).NotTo(HaveOccurred())
			Expect(tlsConfig).NotTo(BeNil())
			Expect(tlsConfig.CipherSuites).To(Equal("TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256"))
			Expect(tlsConfig.CipherSuites).NotTo(ContainSubstring("3DES"))
		})
	})

	Context("when APIServer has Custom profile with all insecure ciphers", func() {
		BeforeEach(func() {
			createAPIServerWithTLS(ctx, &openshiftconfigv1.TLSSecurityProfile{
				Type: openshiftconfigv1.TLSProfileCustomType,
				Custom: &openshiftconfigv1.CustomTLSProfile{
					TLSProfileSpec: openshiftconfigv1.TLSProfileSpec{
						Ciphers:       []string{"TLS_RSA_WITH_RC4_128_SHA", "TLS_ECDHE_RSA_WITH_3DES_EDE_CBC_SHA"},
						MinTLSVersion: openshiftconfigv1.VersionTLS12,
					},
				},
			}, openshiftconfigv1.TLSAdherencePolicyStrictAllComponents)
		})

		It("should return empty CipherSuites when all ciphers are insecure", func() {
			tlsConfig, err := orchestrator.GetTLSConfig(ctx)
			Expect(err).NotTo(HaveOccurred())
			Expect(tlsConfig).NotTo(BeNil())
			Expect(tlsConfig.CipherSuites).To(BeEmpty())
			Expect(tlsConfig.MinTLSVersion).To(Equal("VersionTLS12"))
		})
	})

	Context("when APIServer has Custom profile with unrecognized cipher", func() {
		BeforeEach(func() {
			createAPIServerWithTLS(ctx, &openshiftconfigv1.TLSSecurityProfile{
				Type: openshiftconfigv1.TLSProfileCustomType,
				Custom: &openshiftconfigv1.CustomTLSProfile{
					TLSProfileSpec: openshiftconfigv1.TLSProfileSpec{
						Ciphers:       []string{"COMPLETELY-INVALID-CIPHER"},
						MinTLSVersion: openshiftconfigv1.VersionTLS12,
					},
				},
			}, openshiftconfigv1.TLSAdherencePolicyStrictAllComponents)
		})

		It("should return an error for unrecognized cipher", func() {
			tlsConfig, err := orchestrator.GetTLSConfig(ctx)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("failed to convert cipher suite"))
			Expect(tlsConfig).To(BeNil())
		})
	})

	Context("when APIServer has Custom profile with empty ciphers and empty version", func() {
		BeforeEach(func() {
			createAPIServerWithTLS(ctx, &openshiftconfigv1.TLSSecurityProfile{
				Type: openshiftconfigv1.TLSProfileCustomType,
				Custom: &openshiftconfigv1.CustomTLSProfile{
					TLSProfileSpec: openshiftconfigv1.TLSProfileSpec{},
				},
			}, openshiftconfigv1.TLSAdherencePolicyStrictAllComponents)
		})

		It("should return nil TLSConfig (no configuration to apply)", func() {
			tlsConfig, err := orchestrator.GetTLSConfig(ctx)
			Expect(err).NotTo(HaveOccurred())
			Expect(tlsConfig).To(BeNil())
		})
	})

	Context("tlsAdherence behavior", func() {
		It("should return nil when tlsAdherence is unset (empty string)", func() {
			createAPIServerWithTLS(ctx, &openshiftconfigv1.TLSSecurityProfile{
				Type: openshiftconfigv1.TLSProfileIntermediateType,
			}, "")

			tlsConfig, err := orchestrator.GetTLSConfig(ctx)
			Expect(err).NotTo(HaveOccurred())
			Expect(tlsConfig).To(BeNil())
		})

		It("should return nil when tlsAdherence is LegacyAdheringComponentsOnly", func() {
			createAPIServerWithTLS(ctx, &openshiftconfigv1.TLSSecurityProfile{
				Type: openshiftconfigv1.TLSProfileIntermediateType,
			}, openshiftconfigv1.TLSAdherencePolicyLegacyAdheringComponentsOnly)

			tlsConfig, err := orchestrator.GetTLSConfig(ctx)
			Expect(err).NotTo(HaveOccurred())
			Expect(tlsConfig).To(BeNil())
		})

		It("should honor the TLS profile when tlsAdherence is StrictAllComponents", func() {
			createAPIServerWithTLS(ctx, &openshiftconfigv1.TLSSecurityProfile{
				Type: openshiftconfigv1.TLSProfileIntermediateType,
			}, openshiftconfigv1.TLSAdherencePolicyStrictAllComponents)

			tlsConfig, err := orchestrator.GetTLSConfig(ctx)
			Expect(err).NotTo(HaveOccurred())
			Expect(tlsConfig).NotTo(BeNil())
			Expect(tlsConfig.MinTLSVersion).To(Equal("VersionTLS12"))
		})

		It("should honor the TLS profile when tlsAdherence has an unknown value (forward compatibility)", func() {
			createAPIServerWithTLS(ctx, &openshiftconfigv1.TLSSecurityProfile{
				Type: openshiftconfigv1.TLSProfileIntermediateType,
			}, openshiftconfigv1.TLSAdherencePolicy("FutureUnknownValue"))

			tlsConfig, err := orchestrator.GetTLSConfig(ctx)
			Expect(err).NotTo(HaveOccurred())
			Expect(tlsConfig).NotTo(BeNil())
			Expect(tlsConfig.MinTLSVersion).To(Equal("VersionTLS12"))
		})
	})
})
