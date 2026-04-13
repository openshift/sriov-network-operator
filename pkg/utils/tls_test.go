package utils

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("TLS Utilities", func() {
	Context("BuildTLSConfig", func() {
		It("should return nil when both inputs are empty", func() {
			config, err := BuildTLSConfig("", "")
			Expect(err).NotTo(HaveOccurred())
			Expect(config).To(BeNil())
		})

		It("should accept valid IANA cipher suites", func() {
			config, err := BuildTLSConfig("TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384", "")
			Expect(err).NotTo(HaveOccurred())
			Expect(config).NotTo(BeNil())
			Expect(config.CipherSuites).To(Equal("TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384"))
			Expect(config.MinTLSVersion).To(BeEmpty())
		})

		It("should accept valid OpenSSL cipher suites and convert to IANA", func() {
			config, err := BuildTLSConfig("ECDHE-RSA-AES128-GCM-SHA256", "")
			Expect(err).NotTo(HaveOccurred())
			Expect(config).NotTo(BeNil())
			Expect(config.CipherSuites).To(Equal("TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256"))
		})

		It("should accept valid TLS version VersionTLS12", func() {
			config, err := BuildTLSConfig("", "VersionTLS12")
			Expect(err).NotTo(HaveOccurred())
			Expect(config).NotTo(BeNil())
			Expect(config.MinTLSVersion).To(Equal("VersionTLS12"))
			Expect(config.CipherSuites).To(BeEmpty())
		})

		It("should accept valid TLS version VersionTLS13", func() {
			config, err := BuildTLSConfig("", "VersionTLS13")
			Expect(err).NotTo(HaveOccurred())
			Expect(config).NotTo(BeNil())
			Expect(config.MinTLSVersion).To(Equal("VersionTLS13"))
		})

		It("should accept both ciphers and version together", func() {
			config, err := BuildTLSConfig("TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256", "VersionTLS13")
			Expect(err).NotTo(HaveOccurred())
			Expect(config).NotTo(BeNil())
			Expect(config.CipherSuites).To(Equal("TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256"))
			Expect(config.MinTLSVersion).To(Equal("VersionTLS13"))
		})

		It("should return error for invalid cipher suite", func() {
			_, err := BuildTLSConfig("COMPLETELY-INVALID-CIPHER", "")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("failed to convert cipher suites to IANA"))
		})

		It("should return error for invalid TLS version string", func() {
			_, err := BuildTLSConfig("", "InvalidVersion")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("failed to parse TLS version"))
		})

		It("should return error for TLS version below 1.2", func() {
			_, err := BuildTLSConfig("", "VersionTLS11")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("minimum TLS version must be VersionTLS12 or higher"))
		})

		It("should filter out insecure ciphers", func() {
			config, err := BuildTLSConfig("TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,TLS_RSA_WITH_RC4_128_SHA", "")
			Expect(err).NotTo(HaveOccurred())
			Expect(config).NotTo(BeNil())
			Expect(config.CipherSuites).To(Equal("TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256"))
			Expect(config.CipherSuites).NotTo(ContainSubstring("RC4"))
		})

		It("should return empty CipherSuites when all ciphers are insecure", func() {
			config, err := BuildTLSConfig("TLS_RSA_WITH_RC4_128_SHA", "VersionTLS12")
			Expect(err).NotTo(HaveOccurred())
			Expect(config).NotTo(BeNil())
			Expect(config.CipherSuites).To(BeEmpty())
			Expect(config.MinTLSVersion).To(Equal("VersionTLS12"))
		})

		It("should handle mixed OpenSSL and IANA cipher formats", func() {
			config, err := BuildTLSConfig("ECDHE-RSA-AES128-GCM-SHA256,TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384", "")
			Expect(err).NotTo(HaveOccurred())
			Expect(config).NotTo(BeNil())
			Expect(config.CipherSuites).To(Equal("TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384"))
		})

		It("should handle TLS 1.3 ciphers that have same name in both formats", func() {
			config, err := BuildTLSConfig("TLS_AES_128_GCM_SHA256,TLS_AES_256_GCM_SHA384", "")
			Expect(err).NotTo(HaveOccurred())
			Expect(config).NotTo(BeNil())
			Expect(config.CipherSuites).To(Equal("TLS_AES_128_GCM_SHA256,TLS_AES_256_GCM_SHA384"))
		})
	})

	Context("CipherSuitesToIANAAndSecure", func() {
		It("should return empty list for empty input", func() {
			result, err := CipherSuitesToIANAAndSecure([]string{})
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(BeEmpty())
		})

		It("should pass through IANA cipher names", func() {
			result, err := CipherSuitesToIANAAndSecure([]string{
				"TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256",
				"TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384",
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal([]string{
				"TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256",
				"TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384",
			}))
		})

		It("should convert OpenSSL names to IANA", func() {
			result, err := CipherSuitesToIANAAndSecure([]string{
				"ECDHE-ECDSA-AES128-GCM-SHA256",
				"ECDHE-RSA-AES256-GCM-SHA384",
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal([]string{
				"TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256",
				"TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384",
			}))
		})

		It("should filter insecure IANA cipher", func() {
			result, err := CipherSuitesToIANAAndSecure([]string{
				"TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256",
				"TLS_RSA_WITH_RC4_128_SHA",
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal([]string{"TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256"}))
		})

		It("should filter insecure OpenSSL cipher", func() {
			result, err := CipherSuitesToIANAAndSecure([]string{
				"ECDHE-RSA-AES128-GCM-SHA256",
				"DES-CBC3-SHA",
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal([]string{"TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256"}))
		})

		It("should return error for unrecognized cipher", func() {
			_, err := CipherSuitesToIANAAndSecure([]string{"NOT-A-REAL-CIPHER"})
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("failed to convert cipher suite"))
		})

		It("should return empty list when all ciphers are insecure", func() {
			result, err := CipherSuitesToIANAAndSecure([]string{
				"TLS_RSA_WITH_RC4_128_SHA",
				"TLS_ECDHE_RSA_WITH_3DES_EDE_CBC_SHA",
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(BeEmpty())
		})

		It("should handle single secure cipher", func() {
			result, err := CipherSuitesToIANAAndSecure([]string{"TLS_AES_128_GCM_SHA256"})
			Expect(err).NotTo(HaveOccurred())
			Expect(result).To(Equal([]string{"TLS_AES_128_GCM_SHA256"}))
		})
	})

	Context("BuildTLSConfig edge cases", func() {
		It("should return non-nil with empty CipherSuites when all ciphers are insecure and no minVersion", func() {
			config, err := BuildTLSConfig("TLS_RSA_WITH_RC4_128_SHA", "")
			Expect(err).NotTo(HaveOccurred())
			Expect(config).NotTo(BeNil())
			Expect(config.CipherSuites).To(BeEmpty())
			Expect(config.MinTLSVersion).To(BeEmpty())
		})

		It("should accept only cipher suites without minVersion", func() {
			config, err := BuildTLSConfig("TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256", "")
			Expect(err).NotTo(HaveOccurred())
			Expect(config).NotTo(BeNil())
			Expect(config.CipherSuites).To(Equal("TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256"))
			Expect(config.MinTLSVersion).To(BeEmpty())
		})

		It("should accept only minVersion without cipher suites", func() {
			config, err := BuildTLSConfig("", "VersionTLS13")
			Expect(err).NotTo(HaveOccurred())
			Expect(config).NotTo(BeNil())
			Expect(config.CipherSuites).To(BeEmpty())
			Expect(config.MinTLSVersion).To(Equal("VersionTLS13"))
		})

		It("should return error for VersionTLS10", func() {
			_, err := BuildTLSConfig("", "VersionTLS10")
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("minimum TLS version must be VersionTLS12 or higher"))
		})
	})
})
