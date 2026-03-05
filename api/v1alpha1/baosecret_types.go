/*
Copyright 2024 KubeBao Authors.

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

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// BaoSecretSpec defines the desired state of BaoSecret
type BaoSecretSpec struct {
	// SecretPath is the path in OpenBao where the secret is stored
	// +kubebuilder:validation:Required
	SecretPath string `json:"secretPath"`

	// SecretKey is the specific key to extract from the secret (optional)
	// If not specified, all keys will be synced
	// +optional
	SecretKey string `json:"secretKey,omitempty"`

	// SecretEngine is the type of secret engine (kv, database, pki, etc.)
	// +kubebuilder:default=kv
	// +optional
	SecretEngine string `json:"secretEngine,omitempty"`

	// Target defines where to sync the secret
	// +kubebuilder:validation:Required
	Target SecretTarget `json:"target"`

	// RefreshInterval is the interval at which to refresh the secret
	// +kubebuilder:default="1h"
	// +optional
	RefreshInterval string `json:"refreshInterval,omitempty"`

	// OpenBaoRef references the OpenBao connection to use
	// +optional
	OpenBaoRef *OpenBaoReference `json:"openbaoRef,omitempty"`

	// RoleName is the role to use for authentication (if different from default)
	// +optional
	RoleName string `json:"roleName,omitempty"`

	// SecretArgs are additional arguments for dynamic secrets (database, pki)
	// +optional
	SecretArgs map[string]string `json:"secretArgs,omitempty"`

	// Template allows transforming the secret data before syncing
	// +optional
	Template *SecretTemplate `json:"template,omitempty"`

	// SuspendSync suspends the synchronization of the secret
	// +optional
	SuspendSync bool `json:"suspendSync,omitempty"`
}

// SecretTarget defines where to sync the secret
type SecretTarget struct {
	// Name is the name of the target Kubernetes Secret
	// +kubebuilder:validation:Required
	Name string `json:"name"`

	// Namespace is the namespace of the target Secret
	// If not specified, uses the same namespace as the BaoSecret
	// +optional
	Namespace string `json:"namespace,omitempty"`

	// Type is the type of the Kubernetes Secret
	// +kubebuilder:default=Opaque
	// +optional
	Type string `json:"type,omitempty"`

	// Labels to add to the target Secret
	// +optional
	Labels map[string]string `json:"labels,omitempty"`

	// Annotations to add to the target Secret
	// +optional
	Annotations map[string]string `json:"annotations,omitempty"`

	// CreationPolicy defines what to do when the target doesn't exist
	// +kubebuilder:default=Owner
	// +kubebuilder:validation:Enum=Owner;Orphan;Merge
	// +optional
	CreationPolicy string `json:"creationPolicy,omitempty"`
}

// OpenBaoReference references an OpenBao connection
type OpenBaoReference struct {
	// Address is the address of the OpenBao server
	// +optional
	Address string `json:"address,omitempty"`

	// Namespace is the OpenBao namespace
	// +optional
	Namespace string `json:"namespace,omitempty"`

	// AuthMethod is the authentication method to use
	// +kubebuilder:default=kubernetes
	// +optional
	AuthMethod string `json:"authMethod,omitempty"`

	// AuthMountPath is the mount path for the auth method
	// +optional
	AuthMountPath string `json:"authMountPath,omitempty"`

	// ServiceAccountRef references a ServiceAccount to use for authentication
	// +optional
	ServiceAccountRef *ServiceAccountReference `json:"serviceAccountRef,omitempty"`
}

// ServiceAccountReference references a ServiceAccount
type ServiceAccountReference struct {
	// Name is the name of the ServiceAccount
	Name string `json:"name"`

	// Namespace is the namespace of the ServiceAccount
	// +optional
	Namespace string `json:"namespace,omitempty"`
}

// SecretTemplate allows transforming secret data
type SecretTemplate struct {
	// Data is a map of template strings
	// Keys are the target secret data keys
	// Values are Go templates that can reference source data with {{ .Data.key }}
	// +optional
	Data map[string]string `json:"data,omitempty"`

	// StringData is a map of template strings for string data
	// +optional
	StringData map[string]string `json:"stringData,omitempty"`
}

// BaoSecretStatus defines the observed state of BaoSecret
type BaoSecretStatus struct {
	// Conditions represent the latest available observations of the BaoSecret's state
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// LastSyncTime is the last time the secret was synced
	// +optional
	LastSyncTime *metav1.Time `json:"lastSyncTime,omitempty"`

	// SecretVersion is the version of the secret in OpenBao
	// +optional
	SecretVersion string `json:"secretVersion,omitempty"`

	// SyncedSecretName is the name of the synced Kubernetes Secret
	// +optional
	SyncedSecretName string `json:"syncedSecretName,omitempty"`

	// SyncedSecretNamespace is the namespace of the synced Kubernetes Secret
	// +optional
	SyncedSecretNamespace string `json:"syncedSecretNamespace,omitempty"`

	// ObservedGeneration is the last observed generation
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Secret Path",type=string,JSONPath=`.spec.secretPath`
// +kubebuilder:printcolumn:name="Target",type=string,JSONPath=`.spec.target.name`
// +kubebuilder:printcolumn:name="Last Sync",type=date,JSONPath=`.status.lastSyncTime`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// BaoSecret is the Schema for the baosecrets API
type BaoSecret struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   BaoSecretSpec   `json:"spec,omitempty"`
	Status BaoSecretStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// BaoSecretList contains a list of BaoSecret
type BaoSecretList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []BaoSecret `json:"items"`
}

func init() {
	SchemeBuilder.Register(&BaoSecret{}, &BaoSecretList{})
}

// Condition types for BaoSecret
const (
	// ConditionTypeReady indicates the secret is ready and synced
	ConditionTypeReady = "Ready"

	// ConditionTypeSynced indicates the secret was synced successfully
	ConditionTypeSynced = "Synced"

	// ConditionTypeAuthenticated indicates authentication to OpenBao was successful
	ConditionTypeAuthenticated = "Authenticated"
)

// Condition reasons
const (
	ReasonSuccess            = "Success"
	ReasonFailed             = "Failed"
	ReasonAuthenticationFailed = "AuthenticationFailed"
	ReasonSecretNotFound     = "SecretNotFound"
	ReasonSyncSuspended      = "SyncSuspended"
)
