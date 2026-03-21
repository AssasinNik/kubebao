// API types для BaoPolicy — CRD синхронизации политик OpenBao.
package v1alpha1

import (
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// BaoPolicySpec defines the desired state of BaoPolicy
type BaoPolicySpec struct {
	// PolicyName is the name of the policy in OpenBao
	// If not specified, the BaoPolicy name will be used
	// +optional
	PolicyName string `json:"policyName,omitempty"`

	// Rules defines the policy rules
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinItems=1
	Rules []PolicyRule `json:"rules"`

	// OpenBaoRef references the OpenBao connection to use
	// +optional
	OpenBaoRef *OpenBaoReference `json:"openbaoRef,omitempty"`
}

// PolicyRule defines a single policy rule
type PolicyRule struct {
	// Path is the path pattern for this rule (supports wildcards)
	// +kubebuilder:validation:Required
	Path string `json:"path"`

	// Capabilities are the operations allowed on the path
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinItems=1
	Capabilities []Capability `json:"capabilities"`

	// AllowedParameters restricts which keys and values can be set
	// +optional
	AllowedParameters map[string][]string `json:"allowedParameters,omitempty"`

	// DeniedParameters specifies keys that cannot be set
	// +optional
	DeniedParameters []string `json:"deniedParameters,omitempty"`

	// RequiredParameters specifies keys that must be set
	// +optional
	RequiredParameters []string `json:"requiredParameters,omitempty"`

	// MinWrappingTTL specifies minimum wrapping TTL
	// +optional
	MinWrappingTTL string `json:"minWrappingTTL,omitempty"`

	// MaxWrappingTTL specifies maximum wrapping TTL
	// +optional
	MaxWrappingTTL string `json:"maxWrappingTTL,omitempty"`
}

// Capability represents an operation capability
// +kubebuilder:validation:Enum=create;read;update;delete;list;sudo;deny;patch
type Capability string

const (
	CapabilityCreate Capability = "create"
	CapabilityRead   Capability = "read"
	CapabilityUpdate Capability = "update"
	CapabilityDelete Capability = "delete"
	CapabilityList   Capability = "list"
	CapabilitySudo   Capability = "sudo"
	CapabilityDeny   Capability = "deny"
	CapabilityPatch  Capability = "patch"
)

// BaoPolicyStatus defines the observed state of BaoPolicy
type BaoPolicyStatus struct {
	// Conditions represent the latest available observations of the BaoPolicy's state
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// LastSyncTime is the last time the policy was synced to OpenBao
	// +optional
	LastSyncTime *metav1.Time `json:"lastSyncTime,omitempty"`

	// PolicyVersion is a hash of the policy content
	// +optional
	PolicyVersion string `json:"policyVersion,omitempty"`

	// AppliedPolicyName is the name of the policy as it appears in OpenBao
	// +optional
	AppliedPolicyName string `json:"appliedPolicyName,omitempty"`

	// ObservedGeneration is the last observed generation
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Policy Name",type=string,JSONPath=`.status.appliedPolicyName`
// +kubebuilder:printcolumn:name="Last Sync",type=date,JSONPath=`.status.lastSyncTime`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// BaoPolicy is the Schema for the baopolicies API
type BaoPolicy struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   BaoPolicySpec   `json:"spec,omitempty"`
	Status BaoPolicyStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// BaoPolicyList contains a list of BaoPolicy
type BaoPolicyList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []BaoPolicy `json:"items"`
}

func init() {
	SchemeBuilder.Register(&BaoPolicy{}, &BaoPolicyList{})
}

// ToHCL converts the policy rules to HCL format for OpenBao.
// All string values are escaped to prevent HCL injection.
func (p *BaoPolicy) ToHCL() string {
	var b strings.Builder

	for _, rule := range p.Spec.Rules {
		b.WriteString("path \"")
		b.WriteString(escapeHCLString(rule.Path))
		b.WriteString("\" {\n")
		b.WriteString("  capabilities = [")

		for i, cap := range rule.Capabilities {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString("\"")
			b.WriteString(escapeHCLString(string(cap)))
			b.WriteString("\"")
		}
		b.WriteString("]\n")

		if len(rule.AllowedParameters) > 0 {
			b.WriteString("  allowed_parameters = {\n")
			for key, values := range rule.AllowedParameters {
				b.WriteString("    \"")
				b.WriteString(escapeHCLString(key))
				b.WriteString("\" = [")
				for i, v := range values {
					if i > 0 {
						b.WriteString(", ")
					}
					b.WriteString("\"")
					b.WriteString(escapeHCLString(v))
					b.WriteString("\"")
				}
				b.WriteString("]\n")
			}
			b.WriteString("  }\n")
		}

		if len(rule.DeniedParameters) > 0 {
			b.WriteString("  denied_parameters = {\n")
			for _, key := range rule.DeniedParameters {
				b.WriteString("    \"")
				b.WriteString(escapeHCLString(key))
				b.WriteString("\" = []\n")
			}
			b.WriteString("  }\n")
		}

		if len(rule.RequiredParameters) > 0 {
			b.WriteString("  required_parameters = [")
			for i, key := range rule.RequiredParameters {
				if i > 0 {
					b.WriteString(", ")
				}
				b.WriteString("\"")
				b.WriteString(escapeHCLString(key))
				b.WriteString("\"")
			}
			b.WriteString("]\n")
		}

		if rule.MinWrappingTTL != "" {
			b.WriteString("  min_wrapping_ttl = \"")
			b.WriteString(escapeHCLString(rule.MinWrappingTTL))
			b.WriteString("\"\n")
		}

		if rule.MaxWrappingTTL != "" {
			b.WriteString("  max_wrapping_ttl = \"")
			b.WriteString(escapeHCLString(rule.MaxWrappingTTL))
			b.WriteString("\"\n")
		}

		b.WriteString("}\n\n")
	}

	return b.String()
}

// escapeHCLString экранирует спецсимволы для безопасной вставки в HCL-строку.
func escapeHCLString(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "\"", "\\\"")
	s = strings.ReplaceAll(s, "\n", "\\n")
	s = strings.ReplaceAll(s, "\r", "\\r")
	s = strings.ReplaceAll(s, "\t", "\\t")
	return s
}

// GetPolicyName returns the policy name to use in OpenBao
func (p *BaoPolicy) GetPolicyName() string {
	if p.Spec.PolicyName != "" {
		return p.Spec.PolicyName
	}
	return p.Name
}
