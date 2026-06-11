package openshift_test

import (
	"context"
	"strings"

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

		It("should include curve preferences from the Intermediate profile defaults", func() {
			intermediateSpec := openshiftconfigv1.TLSProfiles[openshiftconfigv1.TLSProfileIntermediateType]
			if len(intermediateSpec.Groups) == 0 {
				Skip("Intermediate profile does not have default groups in this openshift/api version")
			}

			tlsConfig, err := orchestrator.GetTLSConfig(ctx)
			Expect(err).NotTo(HaveOccurred())
			Expect(tlsConfig).NotTo(BeNil())
			Expect(tlsConfig.CurvePreferences).NotTo(BeEmpty())

			expectedGroups := make([]string, len(intermediateSpec.Groups))
			for i, g := range intermediateSpec.Groups {
				expectedGroups[i] = string(g)
			}
			Expect(tlsConfig.CurvePreferences).To(Equal(strings.Join(expectedGroups, ",")))
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

		It("should include curve preferences from the Modern profile defaults", func() {
			modernSpec := openshiftconfigv1.TLSProfiles[openshiftconfigv1.TLSProfileModernType]
			if len(modernSpec.Groups) == 0 {
				Skip("Modern profile does not have default groups in this openshift/api version")
			}

			tlsConfig, err := orchestrator.GetTLSConfig(ctx)
			Expect(err).NotTo(HaveOccurred())
			Expect(tlsConfig).NotTo(BeNil())
			Expect(tlsConfig.CurvePreferences).NotTo(BeEmpty())
		})
	})

	Context("when APIServer has Old profile with StrictAllComponents", func() {
		BeforeEach(func() {
			createAPIServerWithTLS(ctx, &openshiftconfigv1.TLSSecurityProfile{
				Type: openshiftconfigv1.TLSProfileOldType,
			}, openshiftconfigv1.TLSAdherencePolicyStrictAllComponents)
		})

		It("should return an error because Old profile has incompatible settings", func() {
			tlsConfig, err := orchestrator.GetTLSConfig(ctx)
			Expect(err).To(HaveOccurred())
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
						Ciphers:       []string{"TLS_AES_128_GCM_SHA256", "TLS_AES_256_GCM_SHA384"},
						MinTLSVersion: openshiftconfigv1.VersionTLS13,
					},
				},
			}, openshiftconfigv1.TLSAdherencePolicyStrictAllComponents)
		})

		It("should return TLS 1.3 ciphers correctly", func() {
			tlsConfig, err := orchestrator.GetTLSConfig(ctx)
			Expect(err).NotTo(HaveOccurred())
			Expect(tlsConfig).NotTo(BeNil())
			Expect(tlsConfig.CipherSuites).To(Equal("TLS_AES_128_GCM_SHA256,TLS_AES_256_GCM_SHA384"))
			Expect(tlsConfig.MinTLSVersion).To(Equal("VersionTLS13"))
		})
	})

	Context("when APIServer has Custom profile with ciphers and minTLSVersion", func() {
		BeforeEach(func() {
			createAPIServerWithTLS(ctx, &openshiftconfigv1.TLSSecurityProfile{
				Type: openshiftconfigv1.TLSProfileCustomType,
				Custom: &openshiftconfigv1.CustomTLSProfile{
					TLSProfileSpec: openshiftconfigv1.TLSProfileSpec{
						Ciphers:       []string{"ECDHE-RSA-AES256-GCM-SHA384"},
						MinTLSVersion: openshiftconfigv1.VersionTLS12,
					},
				},
			}, openshiftconfigv1.TLSAdherencePolicyStrictAllComponents)
		})

		It("should return CipherSuites and MinTLSVersion", func() {
			tlsConfig, err := orchestrator.GetTLSConfig(ctx)
			Expect(err).NotTo(HaveOccurred())
			Expect(tlsConfig).NotTo(BeNil())
			Expect(tlsConfig.CipherSuites).To(Equal("TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384"))
			Expect(tlsConfig.MinTLSVersion).To(Equal("VersionTLS12"))
		})

		It("should return empty CurvePreferences when no groups are specified", func() {
			tlsConfig, err := orchestrator.GetTLSConfig(ctx)
			Expect(err).NotTo(HaveOccurred())
			Expect(tlsConfig).NotTo(BeNil())
			Expect(tlsConfig.CurvePreferences).To(BeEmpty())
		})
	})

	Context("when APIServer has Custom profile with ciphers, minTLSVersion, and groups", func() {
		BeforeEach(func() {
			createAPIServerWithTLS(ctx, &openshiftconfigv1.TLSSecurityProfile{
				Type: openshiftconfigv1.TLSProfileCustomType,
				Custom: &openshiftconfigv1.CustomTLSProfile{
					TLSProfileSpec: openshiftconfigv1.TLSProfileSpec{
						Ciphers:       []string{"TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256"},
						MinTLSVersion: openshiftconfigv1.VersionTLS12,
						Groups: []openshiftconfigv1.TLSGroup{
							openshiftconfigv1.TLSGroupX25519,
							openshiftconfigv1.TLSGroupSecP256r1,
						},
					},
				},
			}, openshiftconfigv1.TLSAdherencePolicyStrictAllComponents)
		})

		It("should return CurvePreferences from the custom groups", func() {
			tlsConfig, err := orchestrator.GetTLSConfig(ctx)
			Expect(err).NotTo(HaveOccurred())
			Expect(tlsConfig).NotTo(BeNil())
			Expect(tlsConfig.CipherSuites).To(Equal("TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256"))
			Expect(tlsConfig.MinTLSVersion).To(Equal("VersionTLS12"))
			Expect(tlsConfig.CurvePreferences).To(Equal("X25519,secp256r1"))
		})
	})

	Context("when APIServer has Custom profile with single group", func() {
		BeforeEach(func() {
			createAPIServerWithTLS(ctx, &openshiftconfigv1.TLSSecurityProfile{
				Type: openshiftconfigv1.TLSProfileCustomType,
				Custom: &openshiftconfigv1.CustomTLSProfile{
					TLSProfileSpec: openshiftconfigv1.TLSProfileSpec{
						Ciphers:       []string{"TLS_AES_128_GCM_SHA256"},
						MinTLSVersion: openshiftconfigv1.VersionTLS13,
						Groups: []openshiftconfigv1.TLSGroup{
							openshiftconfigv1.TLSGroupX25519MLKEM768,
						},
					},
				},
			}, openshiftconfigv1.TLSAdherencePolicyStrictAllComponents)
		})

		It("should return single curve preference for PQC group", func() {
			tlsConfig, err := orchestrator.GetTLSConfig(ctx)
			Expect(err).NotTo(HaveOccurred())
			Expect(tlsConfig).NotTo(BeNil())
			Expect(tlsConfig.CurvePreferences).To(Equal("X25519MLKEM768"))
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
		It("should be rejected by the CRD validation", func() {
			apiServer := &openshiftconfigv1.APIServer{
				ObjectMeta: metav1.ObjectMeta{
					Name: "cluster",
				},
				Spec: openshiftconfigv1.APIServerSpec{
					TLSSecurityProfile: &openshiftconfigv1.TLSSecurityProfile{
						Type: openshiftconfigv1.TLSProfileCustomType,
						Custom: &openshiftconfigv1.CustomTLSProfile{
							TLSProfileSpec: openshiftconfigv1.TLSProfileSpec{
								MinTLSVersion: "InvalidVersion",
							},
						},
					},
					TLSAdherence: openshiftconfigv1.TLSAdherencePolicyStrictAllComponents,
				},
			}
			err := k8sClient.Create(ctx, apiServer)
			Expect(err).To(HaveOccurred())
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
		It("should be rejected by the CRD validation", func() {
			apiServer := &openshiftconfigv1.APIServer{
				ObjectMeta: metav1.ObjectMeta{
					Name: "cluster",
				},
				Spec: openshiftconfigv1.APIServerSpec{
					TLSSecurityProfile: &openshiftconfigv1.TLSSecurityProfile{
						Type: "UnknownType",
					},
					TLSAdherence: openshiftconfigv1.TLSAdherencePolicyStrictAllComponents,
				},
			}
			err := k8sClient.Create(ctx, apiServer)
			Expect(err).To(HaveOccurred())
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
		It("should be rejected by the CRD validation (minTLSVersion is required for Custom type)", func() {
			apiServer := &openshiftconfigv1.APIServer{
				ObjectMeta: metav1.ObjectMeta{
					Name: "cluster",
				},
				Spec: openshiftconfigv1.APIServerSpec{
					TLSSecurityProfile: &openshiftconfigv1.TLSSecurityProfile{
						Type: openshiftconfigv1.TLSProfileCustomType,
						Custom: &openshiftconfigv1.CustomTLSProfile{
							TLSProfileSpec: openshiftconfigv1.TLSProfileSpec{},
						},
					},
					TLSAdherence: openshiftconfigv1.TLSAdherencePolicyStrictAllComponents,
				},
			}
			err := k8sClient.Create(ctx, apiServer)
			Expect(err).To(HaveOccurred())
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

		It("should reject unknown tlsAdherence values via CRD validation (forward compatibility handled by library-go)", func() {
			apiServer := &openshiftconfigv1.APIServer{
				ObjectMeta: metav1.ObjectMeta{
					Name: "cluster",
				},
				Spec: openshiftconfigv1.APIServerSpec{
					TLSSecurityProfile: &openshiftconfigv1.TLSSecurityProfile{
						Type: openshiftconfigv1.TLSProfileIntermediateType,
					},
					TLSAdherence: openshiftconfigv1.TLSAdherencePolicy("FutureUnknownValue"),
				},
			}
			err := k8sClient.Create(ctx, apiServer)
			Expect(err).To(HaveOccurred())
		})
	})
})
