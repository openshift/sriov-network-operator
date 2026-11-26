package v1

import (
	"fmt"
	"strings"

	"github.com/k8snetworkplumbingwg/sriov-network-operator/pkg/consts"
)

const defaultDeviceClassName = "sriov"

// PolicyResourceValidator checks resource names, supported selectors, and
// class-wide device types. Callers supply only policies in the operator namespace
// and exclude the old version of a policy being updated.
// +kubebuilder:object:generate=false
type PolicyResourceValidator struct {
	draEnabled bool
	classes    map[string]*policyResourceClassState
	issues     map[string]error
}

type policyResourceClassState struct {
	resourceName string
	driver       string
	owner        string
	policyNames  []string
	issue        error
}

// NewPolicyResourceValidator creates a validator for the provider selected by the feature gate.
func (cr *SriovOperatorConfig) NewPolicyResourceValidator() *PolicyResourceValidator {
	return newPolicyResourceValidator(cr.Spec.FeatureGates[consts.DynamicResourceAllocationFeatureGate])
}

func newPolicyResourceValidator(draEnabled bool) *PolicyResourceValidator {
	return &PolicyResourceValidator{
		draEnabled: draEnabled,
		classes:    make(map[string]*policyResourceClassState),
		issues:     make(map[string]error),
	}
}

// Add validates a policy before adding its resource to the shared configuration.
// Each policy name must be added at most once, except that a policy rejected by
// policy-local validation may be corrected and retried. Shared conflicts are
// recorded against every policy participating in the affected DeviceClass.
func (v *PolicyResourceValidator) Add(policy *SriovNetworkNodePolicy) error {
	// The deprecated default policy is ignored by both resource renderers.
	if policy.Name == consts.DefaultPolicyName {
		return nil
	}
	delete(v.issues, policy.Name)
	resourceName := policy.Spec.ResourceName
	if err := ValidateResourceName(resourceName); err != nil {
		return v.recordPolicyIssue(policy.Name, fmt.Errorf("policy %q: %w", policy.Name, err))
	}

	// Selector constraints describe the generated DeviceClass and therefore apply
	// only while the DRA feature gate is enabled.
	if v.draEnabled {
		if policy.Spec.NicSelector.NetFilter != "" {
			return v.recordPolicyIssue(policy.Name, fmt.Errorf("policy %q: netFilter selector is not supported in DRA mode; use device-plugin mode", policy.Name))
		}
		for _, pfName := range policy.Spec.NicSelector.PfNames {
			if strings.Contains(pfName, "#") {
				return v.recordPolicyIssue(policy.Name, fmt.Errorf("policy %q: VF range in pfNames selector %q is not supported in DRA mode; use a whole PF name or device-plugin mode", policy.Name, pfName))
			}
		}
	}

	if !v.draEnabled {
		return nil
	}

	// DRA renders a DeviceClass from the resource name, so normalized names
	// must be unique.
	className := ResourceNameToDeviceClassName(resourceName)
	// An empty allocation driver preserves the kernel driver configured by
	// the config daemon, allowing different vendors' netdevices to share a resource.
	driver := ""
	if policy.Spec.DeviceType == consts.DeviceTypeVfioPci {
		driver = consts.DeviceTypeVfioPci
	}
	class, found := v.classes[className]
	if found {
		if class.issue != nil {
			v.issues[policy.Name] = class.issue
			class.policyNames = append(class.policyNames, policy.Name)
			return class.issue
		}
		if class.resourceName != resourceName {
			err := fmt.Errorf("resourceName %q in policy %q normalizes to the same DRA device class name %q as policy %q resourceName %q",
				resourceName, policy.Name, className, class.owner, class.resourceName)
			v.recordClassIssue(class, policy.Name, err)
			return err
		}
		if class.driver != driver {
			err := fmt.Errorf("policies %q and %q have incompatible deviceType settings for DRA resource %q: vfio-pci and netdevice cannot share a DeviceClass",
				class.owner, policy.Name, resourceName)
			v.recordClassIssue(class, policy.Name, err)
			return err
		}
	} else {
		class = &policyResourceClassState{resourceName: resourceName, driver: driver}
		v.classes[className] = class
	}
	class.owner = policy.Name
	class.policyNames = append(class.policyNames, policy.Name)
	return nil
}

// recordPolicyIssue records an issue that affects only one policy.
func (v *PolicyResourceValidator) recordPolicyIssue(policyName string, err error) error {
	v.issues[policyName] = err
	return err
}

// recordClassIssue records an issue for a DeviceClass and all policies sharing it.
func (v *PolicyResourceValidator) recordClassIssue(class *policyResourceClassState, policyName string, err error) {
	for _, relatedPolicyName := range class.policyNames {
		v.issues[relatedPolicyName] = err
	}
	v.issues[policyName] = err
	class.issue = err
	class.policyNames = append(class.policyNames, policyName)
}

// Issue returns the validation issue recorded for a policy after all related
// policies have been added. It returns nil when the policy is valid.
func (v *PolicyResourceValidator) Issue(policyName string) error {
	return v.issues[policyName]
}

// resourceDrivers returns the allocation drivers for all DRA resources.
func (v *PolicyResourceValidator) resourceDrivers() map[string]string {
	drivers := make(map[string]string, len(v.classes))
	for _, class := range v.classes {
		drivers[class.resourceName] = class.driver
	}
	return drivers
}

// PolicyResourceDrivers validates all DRA resources and collects their allocation
// drivers. No partial configuration is returned when policies conflict.
func PolicyResourceDrivers(policies []SriovNetworkNodePolicy) (map[string]string, error) {
	validator := newPolicyResourceValidator(true)
	for i := range policies {
		if err := validator.Add(&policies[i]); err != nil {
			return nil, err
		}
	}
	return validator.resourceDrivers(), nil
}

// ResourceNameToDeviceClassName converts a policy resourceName to a DNS-subdomain-safe
// DeviceClass metadata.name (lowercase alnum + hyphens; leading/trailing hyphens stripped;
// empty falls back to "sriov").
func ResourceNameToDeviceClassName(resourceName string) string {
	s := strings.ReplaceAll(resourceName, "_", "-")
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			b.WriteRune(r)
		} else if r >= 'A' && r <= 'Z' {
			b.WriteRune(r + 32)
		}
	}
	name := strings.Trim(b.String(), "-")
	if name == "" {
		name = defaultDeviceClassName
	}
	return name
}
