/*
Copyright 2026 Digitalis.IO.

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

package controllers

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	digitalisiov1beta1 "digitalis.io/vals-operator/apis/digitalis.io/v1beta1"
)

func dbSecretFixture(namespace, mount, role string) *digitalisiov1beta1.DbSecret {
	return &digitalisiov1beta1.DbSecret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test",
			Namespace: namespace,
			UID:       types.UID("db-secret-uid"),
		},
		Spec: digitalisiov1beta1.DbSecretSpec{
			Vault: digitalisiov1beta1.DbVaultConfig{Mount: mount, Role: role},
		},
	}
}

func TestLeaseIdSuffix(t *testing.T) {
	tests := []struct {
		name    string
		leaseId string
		want    string
		wantErr bool
	}{
		{"vault format", "cass000/creds/readonly/AbCdEf123", "AbCdEf123", false},
		{"extra segments", "team/cass000/creds/readonly/AbCdEf123", "AbCdEf123", false},
		{"too few segments", "creds/readonly", "", true},
		{"no separator", "AbCdEf123", "", true},
		{"trailing separator", "cass000/creds/readonly/", "", true},
		{"empty", "", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := leaseIdSuffix(tt.leaseId)
			if (err != nil) != tt.wantErr {
				t.Fatalf("leaseIdSuffix(%q) error = %v, wantErr %v", tt.leaseId, err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("leaseIdSuffix(%q) = %q, want %q", tt.leaseId, got, tt.want)
			}
		})
	}
}

func TestLeaseIdFor(t *testing.T) {
	sDef := dbSecretFixture("apps", "cass000", "readonly")

	t.Run("rebuilds the full lease id", func(t *testing.T) {
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Annotations: map[string]string{leaseIdLabel: "AbCdEf123"},
			},
		}
		want := "cass000/creds/readonly/AbCdEf123"
		if got := leaseIdFor(sDef, secret); got != want {
			t.Errorf("leaseIdFor() = %q, want %q", got, want)
		}
	})

	t.Run("empty when the secret carries no lease", func(t *testing.T) {
		secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{}}}
		if got := leaseIdFor(sDef, secret); got != "" {
			t.Errorf("leaseIdFor() = %q, want empty", got)
		}
	})

	t.Run("empty when the secret is nil", func(t *testing.T) {
		if got := leaseIdFor(sDef, nil); got != "" {
			t.Errorf("leaseIdFor() = %q, want empty", got)
		}
	})
}

// TestRevokeLeaseGuard covers the guard clause only: a named secret must get
// past it and reach the backend call. The inverted form of this condition meant
// leases were never revoked on delete or on rotation.
func TestRevokeLeaseGuard(t *testing.T) {
	r := &DbSecretReconciler{}
	sDef := dbSecretFixture("apps", "cass000", "readonly")

	t.Run("returns nil for a nil secret", func(t *testing.T) {
		if err := r.revokeLease(sDef, nil); err != nil {
			t.Errorf("revokeLease() with nil secret = %v, want nil", err)
		}
	})

	t.Run("returns nil for an unnamed secret", func(t *testing.T) {
		if err := r.revokeLease(sDef, &corev1.Secret{}); err != nil {
			t.Errorf("revokeLease() with unnamed secret = %v, want nil", err)
		}
	})

	t.Run("does not skip a named secret without a lease id", func(t *testing.T) {
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "creds", Namespace: "apps"},
		}
		// Past the guard, a named secret with no lease id is an error rather than a
		// silent no-op. Anything else means the guard swallowed it again.
		if err := r.revokeLease(sDef, secret); err == nil {
			t.Error("revokeLease() with a named secret returned nil, want an error: the guard clause skipped it")
		}
	})
}

func TestOwnedByDbSecret(t *testing.T) {
	sDef := dbSecretFixture("apps", "cass000", "readonly")

	t.Run("accepts a secret it owns", func(t *testing.T) {
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:            "creds",
				Namespace:       "apps",
				OwnerReferences: []metav1.OwnerReference{{UID: sDef.GetUID()}},
			},
		}
		if err := ownedByDbSecret(sDef, secret); err != nil {
			t.Errorf("ownedByDbSecret() = %v, want nil", err)
		}
	})

	t.Run("rejects an unowned secret", func(t *testing.T) {
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "someone-elses", Namespace: "apps"},
		}
		if err := ownedByDbSecret(sDef, secret); err == nil {
			t.Error("ownedByDbSecret() = nil, want an error for a secret owned by nobody")
		}
	})

	t.Run("rejects a secret owned by something else", func(t *testing.T) {
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:            "someone-elses",
				Namespace:       "apps",
				OwnerReferences: []metav1.OwnerReference{{UID: types.UID("another-uid")}},
			},
		}
		if err := ownedByDbSecret(sDef, secret); err == nil {
			t.Error("ownedByDbSecret() = nil, want an error for a secret owned by another resource")
		}
	})
}

func TestIsVaultRefAllowed(t *testing.T) {
	tests := []struct {
		name      string
		namespace string
		mount     string
		role      string
		mounts    map[string]bool
		roles     map[string]bool
		wantErr   bool
	}{
		{
			name: "no allowlist permits anything well formed", namespace: "apps",
			mount: "cass000", role: "readonly", wantErr: false,
		},
		{
			name: "path separator in mount is rejected", namespace: "apps",
			mount: "cass000/creds/admin", role: "readonly", wantErr: true,
		},
		{
			name: "path separator in role is rejected", namespace: "apps",
			mount: "cass000", role: "../admin", wantErr: true,
		},
		{
			name: "empty mount is rejected", namespace: "apps",
			mount: "", role: "readonly", wantErr: true,
		},
		{
			name: "mount on the allowlist is permitted", namespace: "apps",
			mount: "cass000", role: "readonly",
			mounts: map[string]bool{"cass000": true}, wantErr: false,
		},
		{
			name: "mount off the allowlist is rejected", namespace: "apps",
			mount: "prod-db", role: "readonly",
			mounts: map[string]bool{"cass000": true}, wantErr: true,
		},
		{
			name: "namespace qualified entry permits its own namespace", namespace: "team-a",
			mount: "pg", role: "readonly",
			mounts: map[string]bool{"team-a/pg": true}, wantErr: false,
		},
		{
			name: "namespace qualified entry rejects another namespace", namespace: "team-b",
			mount: "pg", role: "readonly",
			mounts: map[string]bool{"team-a/pg": true}, wantErr: true,
		},
		{
			name: "role off the allowlist is rejected", namespace: "apps",
			mount: "cass000", role: "admin",
			roles: map[string]bool{"readonly": true}, wantErr: true,
		},
		{
			name: "role on the allowlist is permitted", namespace: "apps",
			mount: "cass000", role: "readonly",
			roles: map[string]bool{"readonly": true}, wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &DbSecretReconciler{
				AllowedVaultMounts: tt.mounts,
				AllowedVaultRoles:  tt.roles,
			}
			err := r.isVaultRefAllowed(dbSecretFixture(tt.namespace, tt.mount, tt.role))
			if (err != nil) != tt.wantErr {
				t.Errorf("isVaultRefAllowed(%s/%s in %s) error = %v, wantErr %v",
					tt.mount, tt.role, tt.namespace, err, tt.wantErr)
			}
		})
	}
}
