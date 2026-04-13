package utils

import (
	"crypto/tls"
	"fmt"
	"strings"

	openshiftcrypto "github.com/openshift/library-go/pkg/crypto"
	cliflag "k8s.io/component-base/cli/flag"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/consts"
)

// BuildTLSConfig builds a validated TLSConfig from a comma-separated cipher suites string
// and a minimum TLS version string. It converts cipher names to IANA format, filters
// insecure ciphers, and enforces a minimum of TLS 1.2.
// Returns nil if both cipherSuites and minVersion are empty.
// Returns an error if the cipher suites cannot be converted or the TLS version is invalid.
func BuildTLSConfig(cipherSuites, minVersion string) (*consts.TLSConfig, error) {
	if cipherSuites == "" && minVersion == "" {
		return nil, nil
	}

	result := &consts.TLSConfig{}

	if cipherSuites != "" {
		cipherList := strings.Split(cipherSuites, ",")
		secureCiphers, err := CipherSuitesToIANAAndSecure(cipherList)
		if err != nil {
			return nil, fmt.Errorf("failed to convert cipher suites to IANA: %w", err)
		}
		if len(secureCiphers) > 0 {
			result.CipherSuites = strings.Join(secureCiphers, ",")
		}
	}

	if minVersion != "" {
		tlsVersion, err := cliflag.TLSVersion(minVersion)
		if err != nil {
			return nil, fmt.Errorf("failed to parse TLS version %q: %w", minVersion, err)
		}
		if tlsVersion < tls.VersionTLS12 {
			return nil, fmt.Errorf("minimum TLS version must be VersionTLS12 or higher, got %q", minVersion)
		}
		result.MinTLSVersion = minVersion
	}

	return result, nil
}

// CipherSuitesToIANAAndSecure converts a list of cipher suite names (OpenSSL or IANA format)
// to IANA names and filters out insecure cipher suites.
// Returns a list of secure cipher suite names in IANA format.
func CipherSuitesToIANAAndSecure(cipherSuiteList []string) ([]string, error) {
	logger := log.Log.WithName("CipherSuitesToIANAAndSecure")

	insecureCipherIDs := map[uint16]struct{}{}
	for _, insecureCipher := range tls.InsecureCipherSuites() {
		insecureCipherIDs[insecureCipher.ID] = struct{}{}
	}

	result := make([]string, 0, len(cipherSuiteList))
	for idx := range cipherSuiteList {
		var ianaName string
		var cipherID uint16

		cipherID, err := openshiftcrypto.CipherSuite(cipherSuiteList[idx])
		if err == nil {
			ianaName = cipherSuiteList[idx]
		} else {
			converted := openshiftcrypto.OpenSSLToIANACipherSuites([]string{cipherSuiteList[idx]})
			if len(converted) != 1 {
				return nil, fmt.Errorf("failed to convert cipher suite %q to IANA format: %w", cipherSuiteList[idx], err)
			}
			ianaName = converted[0]

			cipherID, err = openshiftcrypto.CipherSuite(ianaName)
			if err != nil {
				return nil, fmt.Errorf("failed to resolve cipher suite %q (converted to %q) to ID: %w", cipherSuiteList[idx], ianaName, err)
			}
		}

		if _, found := insecureCipherIDs[cipherID]; found {
			logger.V(1).Info("Skipping insecure cipher suite", "cipherSuite", cipherSuiteList[idx])
			continue
		}

		result = append(result, ianaName)
	}
	return result, nil
}
