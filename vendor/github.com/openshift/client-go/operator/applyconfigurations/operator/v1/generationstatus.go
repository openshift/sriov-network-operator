// Code generated by applyconfiguration-gen. DO NOT EDIT.

package v1

// GenerationStatusApplyConfiguration represents a declarative configuration of the GenerationStatus type for use
// with apply.
type GenerationStatusApplyConfiguration struct {
	Group          *string `json:"group,omitempty"`
	Resource       *string `json:"resource,omitempty"`
	Namespace      *string `json:"namespace,omitempty"`
	Name           *string `json:"name,omitempty"`
	LastGeneration *int64  `json:"lastGeneration,omitempty"`
	Hash           *string `json:"hash,omitempty"`
}

// GenerationStatusApplyConfiguration constructs a declarative configuration of the GenerationStatus type for use with
// apply.
func GenerationStatus() *GenerationStatusApplyConfiguration {
	return &GenerationStatusApplyConfiguration{}
}

// WithGroup sets the Group field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the Group field is set to the value of the last call.
func (b *GenerationStatusApplyConfiguration) WithGroup(value string) *GenerationStatusApplyConfiguration {
	b.Group = &value
	return b
}

// WithResource sets the Resource field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the Resource field is set to the value of the last call.
func (b *GenerationStatusApplyConfiguration) WithResource(value string) *GenerationStatusApplyConfiguration {
	b.Resource = &value
	return b
}

// WithNamespace sets the Namespace field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the Namespace field is set to the value of the last call.
func (b *GenerationStatusApplyConfiguration) WithNamespace(value string) *GenerationStatusApplyConfiguration {
	b.Namespace = &value
	return b
}

// WithName sets the Name field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the Name field is set to the value of the last call.
func (b *GenerationStatusApplyConfiguration) WithName(value string) *GenerationStatusApplyConfiguration {
	b.Name = &value
	return b
}

// WithLastGeneration sets the LastGeneration field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the LastGeneration field is set to the value of the last call.
func (b *GenerationStatusApplyConfiguration) WithLastGeneration(value int64) *GenerationStatusApplyConfiguration {
	b.LastGeneration = &value
	return b
}

// WithHash sets the Hash field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the Hash field is set to the value of the last call.
func (b *GenerationStatusApplyConfiguration) WithHash(value string) *GenerationStatusApplyConfiguration {
	b.Hash = &value
	return b
}
