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

package v1beta1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!
// NOTE: json tags are required.  Any new fields you add must have json tags for the fields to be serialized.

// DbSecretSpec defines the desired state of DbSecret
type DbSecretSpec struct {
	// Name can override the secret name, defaults to manifests.name
	SecretName string            `json:"secretName,omitempty"`
	Vault      DbVaultConfig     `json:"vault"`
	Secret     map[string]string `json:"secret,omitempty"`
	Renew      bool              `json:"renew,omitempty"`
	Rollout    []DbRolloutTarget `json:"rollout,omitempty"`
}

/*
apiVersion: digitalis.io/v1
kind: DbSecret
metadata:
  name: cassandra
spec:
  secretName: another-name
  renew: true
  vault:
    role: application_role
    mount: which_database
  secret: # optional: vault returns the values as `username` and `password` but you may need different variable names
    username: "CASSANDRA_USERNAME"
    password: "CASSANDRA_PASSWORD"
	host: localhost
	dc: europe
  rollout: # optional: run a `rollout` to make the pods use new credentials
    - kind: Deployment
      name: my-cass-client
*/

type DbRolloutTarget struct {
	// Kind is either Deployment, Pod or StatefulSet
	Kind string `json:"kind"`
	// Name is the object name
	Name string `json:"name"`
}

type DbVaultConfig struct {
	// Role is the vault role used to connect to the database
	Role string `json:"role"`
	// Mount is the vault database
	Mount string `json:"mount"`
}

type DbSecretRollout struct {
	// Kind if the object kind such as Deployment
	Kind string `json:"kind"`
	// Name is the object name
	Name string `json:"name"`
}

// DbSecretStatus defines the observed state of DbSecret
type DbSecretStatus struct {
	// INSERT ADDITIONAL STATUS FIELD - define observed state of cluster
	// Important: Run "make" to regenerate code after modifying this file
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status

// DbSecret is the Schema for the dbsecrets API
type DbSecret struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   DbSecretSpec   `json:"spec,omitempty"`
	Status DbSecretStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// DbSecretList contains a list of DbSecret
type DbSecretList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []DbSecret `json:"items"`
}

func init() {
	SchemeBuilder.Register(&DbSecret{}, &DbSecretList{})
}
