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
	// Key in the secret containing the database username
	PasswordKey string `json:"passwordKey"`
	// Used for MySQL only, the host part for the username
	UserHost string `json:"userHost,omitempty"`
	// List of hosts to connect to, they'll be tried in sequence until one succeeds
	Hosts []string `json:"hosts"`
}

// ValsSecretSpec defines the desired state of ValsSecret
type ValsSecretSpec struct {
	Name      string                `json:"name,omitempty"`
	Data      map[string]DataSource `json:"data"`
	TTL       int64                 `json:"ttl,omitempty"`
	Type      string                `json:"type,omitempty"`
	Databases []Database            `json:"databases,omitempty"`
	Template  map[string]string     `json:"template,omitempty"`
}

// ValsSecretStatus defines the observed state of ValsSecret
type ValsSecretStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status

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
