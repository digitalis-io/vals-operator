/*
Copyright 2021 Digitalis.IO.

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

package v1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// DataSource defines a secret
type DataSource struct {
	// Ref value to the secret in the format ref+backend://path
	// https://github.com/helmfile/vals
	Ref string `json:"ref"`
	// Encoding type for the secret. Only base64 supported. Optional
	Encoding string `json:"encoding,omitempty"`
}

// DatabaseLoginCredentials holds the access details for the DB
type DatabaseLoginCredentials struct {
	// Name of the secret containing the credentials to be able to log in to the database
	SecretName string `json:"secretName"`
	// Optional namespace of the secret, default current namespace
	Namespace string `json:"namespace,omitempty"`
	// Key in the secret containing the database username
	UsernameKey string `json:"usernameKey,omitempty"`
	// Key in the secret containing the database username
	PasswordKey string `json:"passwordKey"`
}

// Database defines a DB connection
type Database struct {
	// Defines the database type
	Driver string `json:"driver"`
	// Credentials to access the database
	LoginCredentials DatabaseLoginCredentials `json:"loginCredentials,omitempty"`
	// Database port number
	Port int `json:"port,omitempty"`
	// Key in the secret containing the database username
	UsernameKey string `json:"usernameKey,omitempty"`
	// Key in the secret containing the database password
	PasswordKey string `json:"passwordKey"`
	// Used for MySQL only, the host part for the username
	UserHost string `json:"userHost,omitempty"`
	// Used for ClickHouse only, the wire protocol to use: native (also
	// accepted as tcp or clickhouse) or http. Defaults to native. A scheme
	// on a host entry overrides this.
	// +kubebuilder:validation:Enum=native;tcp;clickhouse;http
	Protocol string `json:"protocol,omitempty"`
	// Used for ClickHouse only, the TLS mode: preferred (default, TLS with
	// a plaintext fallback), disable, require or skip-verify. A scheme on a
	// host entry overrides this.
	// +kubebuilder:validation:Enum=preferred;disable;require;skip-verify
	TLS string `json:"tls,omitempty"`
	// List of hosts to connect to, they'll be tried in sequence until one succeeds
	Hosts []string `json:"hosts"`
}

// ValsSecretSpec defines the desired state of ValsSecret
// +kubebuilder:validation:XValidation:rule="!has(self.target) || !has(self.template)",message="spec.template cannot be used with spec.target; put the template in spec.target.template"
// +kubebuilder:validation:XValidation:rule="!has(self.target) || !has(self.databases) || size(self.databases) == 0",message="spec.databases cannot be used with spec.target"
// +kubebuilder:validation:XValidation:rule="!has(self.target) || !has(self.rollout) || size(self.rollout) == 0",message="spec.rollout cannot be used with spec.target"
type ValsSecretSpec struct {
	Name      string                `json:"name,omitempty"`
	Data      map[string]DataSource `json:"data"`
	TTL       int64                 `json:"ttl,omitempty"`
	Type      string                `json:"type,omitempty"`
	Databases []Database            `json:"databases,omitempty"`
	Template  map[string]string     `json:"template,omitempty"`
	Rollout   []RolloutTarget       `json:"rollout,omitempty"`

	// Target renders the resolved data into an arbitrary namespaced resource
	// (ConfigMap, a CRD, ...) instead of a Secret. Requires the operator to run
	// with -enable-custom-targets and the target resource to be listed in
	// -allowed-target-resources.
	// +optional
	Target *Target `json:"target,omitempty"`
}

// TargetMode selects how the target object is written.
// +kubebuilder:validation:Enum=create;patch
type TargetMode string

const (
	// TargetModeCreate creates the target object, sets an owner reference and
	// deletes it together with the ValsSecret.
	TargetModeCreate TargetMode = "create"
	// TargetModePatch requires the target object to already exist. Only the
	// fields rendered from the template are applied (Server-Side Apply) and the
	// object is never deleted; on ValsSecret deletion the rendered fields are
	// removed.
	TargetModePatch TargetMode = "patch"
)

// Target describes a non-Secret resource to render the data into.
type Target struct {
	// APIVersion of the target object, e.g. "v1" or "flink.apache.org/v1beta1".
	// Not templatable.
	// +kubebuilder:validation:MinLength=1
	APIVersion string `json:"apiVersion"`
	// Kind of the target object, e.g. "ConfigMap". Not templatable.
	// +kubebuilder:validation:MinLength=1
	Kind string `json:"kind"`
	// Name of the target object. Defaults to the ValsSecret name. The target
	// always lives in the ValsSecret namespace.
	// +kubebuilder:validation:MaxLength=253
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([-a-z0-9.]*[a-z0-9])?$`
	// +optional
	Name string `json:"name,omitempty"`
	// Mode is "create" (default) or "patch".
	// +kubebuilder:default=create
	// +optional
	Mode TargetMode `json:"mode,omitempty"`
	// Template is a Go template (sprig functions available) rendering to a YAML
	// document with the object body below metadata, e.g.
	//
	//   spec:
	//     flinkConfiguration:
	//       metrics.reporter.dghttp.apikey: {{ .datadog_api_key | quote }}
	//
	// Resolved data keys are available as {{ .<key> }} and {{ .Secrets.<key> }};
	// the ValsSecret name and namespace as {{ .ValsSecret.Name }} /
	// {{ .ValsSecret.Namespace }}. The rendered document must not contain
	// apiVersion, kind, metadata or status.
	// +kubebuilder:validation:MinLength=1
	Template string `json:"template"`
	// Labels to set on the target (create mode only). Not templated.
	// +optional
	Labels map[string]string `json:"labels,omitempty"`
	// Annotations to set on the target (create mode only). Not templated.
	// +optional
	Annotations map[string]string `json:"annotations,omitempty"`
}

// TargetStatus records the last target apply.
type TargetStatus struct {
	APIVersion string     `json:"apiVersion"`
	Kind       string     `json:"kind"`
	Name       string     `json:"name"`
	Namespace  string     `json:"namespace"`
	Mode       TargetMode `json:"mode"`
	// Hash is the sha256 of the rendered body; used to skip no-op applies.
	// +optional
	Hash string `json:"hash,omitempty"`
	// +optional
	LastApplied metav1.Time `json:"lastApplied,omitempty"`
}

// Condition reasons used on the Ready condition.
const (
	ConditionReady         = "Ready"
	ReasonApplied          = "Applied"
	ReasonFeatureDisabled  = "FeatureDisabled"
	ReasonTargetNotAllowed = "TargetNotAllowed"
	ReasonTargetNotFound   = "TargetNotFound"
	ReasonRenderError      = "RenderError"
	ReasonApplyError       = "ApplyError"
	ReasonBackendError     = "BackendError"
)

// RolloutTarget sets up what deployment or sts to restart
type RolloutTarget struct {
	// Kind is either Deployment or StatefulSet
	Kind string `json:"kind"`
	// Name is the object name
	Name string `json:"name"`
}

// ValsSecretStatus defines the observed state of ValsSecret
type ValsSecretStatus struct {
	// Conditions of the ValsSecret. Currently only populated when spec.target is set.
	// +optional
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`
	// Target records the last custom target apply.
	// +optional
	Target *TargetStatus `json:"target,omitempty"`
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status
//+kubebuilder:printcolumn:name="Target",type=string,JSONPath=`.status.target.kind`
//+kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
//+kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// ValsSecret is the Schema for the valssecrets API
type ValsSecret struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ValsSecretSpec   `json:"spec,omitempty"`
	Status ValsSecretStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// ValsSecretList contains a list of ValsSecret
type ValsSecretList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ValsSecret `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ValsSecret{}, &ValsSecretList{})
}
