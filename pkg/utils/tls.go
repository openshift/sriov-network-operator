package utils

import (
	"crypto/tls"
	"fmt"
	"strconv"
	"strings"

	configv1 "github.com/openshift/api/config/v1"
	openshiftcrypto "github.com/openshift/library-go/pkg/crypto"
	cliflag "k8s.io/component-base/cli/flag"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/consts"
)

// BuildTLSConfig builds a validated TLSConfig from a comma-separated cipher suites string,
// a minimum TLS version string, and a comma-separated curve preferences string.
// It converts cipher names to IANA format, filters insecure ciphers, and enforces a minimum of TLS 1.2.
// Returns nil if all parameters are empty.
// Returns an error if the cipher suites cannot be converted, the TLS version is invalid,
// or the curve preferences contain unrecognized group names.
func BuildTLSConfig(cipherSuites, minVersion, curvePreferences string) (*consts.TLSConfig, error) {
	if cipherSuites == "" && minVersion == "" && curvePreferences == "" {
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

	if curvePreferences != "" {
		numericIDs, err := CurveNamesToIDs(curvePreferences)
		if err != nil {
			return nil, fmt.Errorf("failed to parse curve preferences: %w", err)
		}
		if numericIDs != "" {
			result.CurvePreferences = curvePreferences
		}
	}

	return result, nil
}

// ParseTLSMinVersion converts a TLS version string (e.g. "VersionTLS12", "VersionTLS13")
// to its corresponding uint16 constant value using the openshift library.
// Returns tls.VersionTLS12 if the input is empty.
func ParseTLSMinVersion(version string) (uint16, error) {
	if version == "" {
		return tls.VersionTLS12, nil
	}
	return openshiftcrypto.TLSVersion(version)
}

// ParseCipherSuites converts a comma-separated list of cipher suite names
// to their corresponding uint16 IDs using the openshift library.
// Returns nil if the input is empty.
func ParseCipherSuites(cipherSuites string) ([]uint16, error) {
	if cipherSuites == "" {
		return nil, nil
	}

	cipherList := strings.Split(cipherSuites, ",")
	result := make([]uint16, 0, len(cipherList))
	for _, cipher := range cipherList {
		id, err := openshiftcrypto.CipherSuite(strings.TrimSpace(cipher))
		if err != nil {
			return nil, fmt.Errorf("failed to parse cipher suite %q: %w", cipher, err)
		}
		result = append(result, id)
	}
	return result, nil
}

// curveNameToID maps TLS group/curve name strings to their corresponding tls.CurveID.
// Names follow IANA's "TLS Supported Groups" registry and match the openshift/api TLSGroup enum values.
var curveNameToID = map[string]tls.CurveID{
	string(configv1.TLSGroupX25519):         tls.X25519,
	string(configv1.TLSGroupSecP256r1):      tls.CurveP256,
	string(configv1.TLSGroupSecP384r1):      tls.CurveP384,
	string(configv1.TLSGroupSecP521r1):      tls.CurveP521,
	string(configv1.TLSGroupX25519MLKEM768): tls.X25519MLKEM768,
}

// ParseCurvePreferencesFromIDs parses a comma-separated list of numeric TLS CurveID values
// (as defined in crypto/tls) and returns them as []tls.CurveID.
// The supported values depend on the Go version used.
// See https://pkg.go.dev/crypto/tls#CurveID for values supported for each Go version.
// Returns nil if the input is empty.
func ParseCurvePreferencesFromIDs(curveIDs string) ([]tls.CurveID, error) {
	if curveIDs == "" {
		return nil, nil
	}

	parts := strings.Split(curveIDs, ",")
	result := make([]tls.CurveID, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		val, err := strconv.ParseUint(part, 10, 16)
		if err != nil {
			return nil, fmt.Errorf("invalid numeric CurveID %q: %w", part, err)
		}
		result = append(result, tls.CurveID(val))
	}
	return result, nil
}

// CurveNamesToIDs converts a comma-separated list of TLS curve/group names
// (e.g. "X25519,secp256r1") to a comma-separated list of their numeric CurveID values
// (e.g. "29,23"). This is used by the operator to convert names to IDs before
// passing them to the webhooks.
func CurveNamesToIDs(curveNames string) (string, error) {
	if curveNames == "" {
		return "", nil
	}

	names := strings.Split(curveNames, ",")
	ids := make([]string, 0, len(names))
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		id, ok := curveNameToID[name]
		if !ok {
			return "", fmt.Errorf("unrecognized TLS group/curve name %q", name)
		}
		ids = append(ids, strconv.Itoa(int(id)))
	}
	return strings.Join(ids, ","), nil
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
