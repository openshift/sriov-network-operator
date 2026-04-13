package openshift

import (
	"context"
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/log"

	configv1 "github.com/openshift/api/config/v1"
	openshiftcrypto "github.com/openshift/library-go/pkg/crypto"

	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/consts"
	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/utils"
)

// GetTLSConfig retrieves the cluster-wide TLS security profile from the OpenShift APIServer resource.
// Returns nil if no TLSSecurityProfile is configured on the cluster, or if the tlsAdherence policy
// does not require this operator to honor it.
//
// The sriov-network-operator was NOT honoring the cluster TLS profile before the TLSAdherence
// feature was introduced. Per the OpenShift TLSAdherence contract, components that were not
// previously adhering should only start honoring the cluster profile when tlsAdherence is set
// to "StrictAllComponents". When unset or "LegacyAdheringComponentsOnly", this operator
// falls back to its own defaults (env-var based configuration on Kubernetes, or component defaults).
func (c *OpenshiftOrchestrator) GetTLSConfig(ctx context.Context) (*consts.TLSConfig, error) {
	apiServer := &configv1.APIServer{}
	err := c.kubeClient.Get(ctx, types.NamespacedName{Name: "cluster"}, apiServer)
	if err != nil {
		log.Log.WithName("GetTLSConfig").Error(err, "Failed to get config.openshift.io/v1 APIServer resource")
		return nil, fmt.Errorf("failed to get config.openshift.io/v1 APIServer resource: %w", err)
	}

	// Check if this operator should honor the cluster-wide TLS profile based on tlsAdherence.
	// ShouldHonorClusterTLSProfile returns true for StrictAllComponents and unknown values,
	// false for unset ("") and LegacyAdheringComponentsOnly.
	if !openshiftcrypto.ShouldHonorClusterTLSProfile(apiServer.Spec.TLSAdherence) {
		log.Log.WithName("GetTLSConfig").V(2).Info(
			"tlsAdherence does not require this operator to honor cluster TLS profile, skipping",
			"tlsAdherence", apiServer.Spec.TLSAdherence)
		return nil, nil
	}

	// If no TLSSecurityProfile is configured, return nil to preserve
	// existing component defaults (backward compatibility)
	if apiServer.Spec.TLSSecurityProfile == nil {
		return nil, nil
	}

	// Only resolve when explicitly configured
	profile := apiServer.Spec.TLSSecurityProfile
	var spec *configv1.TLSProfileSpec

	switch profile.Type {
	case configv1.TLSProfileCustomType:
		if profile.Custom != nil {
			spec = &profile.Custom.TLSProfileSpec
		}
	case configv1.TLSProfileOldType,
		configv1.TLSProfileIntermediateType,
		configv1.TLSProfileModernType:
		spec = configv1.TLSProfiles[profile.Type]
	}

	if spec == nil {
		return nil, nil // Unknown profile type or empty custom, use defaults
	}

	ciphers := strings.Join(spec.Ciphers, ",")
	return utils.BuildTLSConfig(ciphers, string(spec.MinTLSVersion))
}
