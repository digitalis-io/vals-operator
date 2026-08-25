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
	"strings"
	"testing"
)

func mustAuthorizer(t *testing.T, spec string) *BackendAuthorizer {
	t.Helper()
	a, err := NewBackendAuthorizer(spec)
	if err != nil {
		t.Fatalf("NewBackendAuthorizer(%q) = %v, want nil", spec, err)
	}
	return a
}

func TestBackendAuthorizerDisabledByDefault(t *testing.T) {
	for _, spec := range []string{"", "   ", `""`} {
		a := mustAuthorizer(t, spec)
		if a.Enabled() {
			t.Errorf("Enabled() = true for spec %q, want false", spec)
		}
		if err := a.Authorize("any-namespace", "ref+vault://database/creds/anything#/username"); err != nil {
			t.Errorf("Authorize() = %v with no allowlist, want nil (must not change existing behaviour)", err)
		}
	}

	// A nil authorizer must behave the same, so a reconciler constructed without
	// one keeps working.
	var nilAuth *BackendAuthorizer
	if err := nilAuth.Authorize("any-namespace", "ref+vault://database/creds/anything"); err != nil {
		t.Errorf("Authorize() on a nil authorizer = %v, want nil", err)
	}
}

func TestBackendAuthorizerParseErrors(t *testing.T) {
	for _, spec := range []string{
		"no-equals-sign",
		"=ref+vault://database",
		"team-a=",
		"team-a=ref+vault://ok;broken",
	} {
		if _, err := NewBackendAuthorizer(spec); err == nil {
			t.Errorf("NewBackendAuthorizer(%q) = nil error, want a parse error", spec)
		}
	}
}

func TestBackendAuthorizerAuthorize(t *testing.T) {
	const spec = "team-a=ref+vault://database/creds/team-a,ref+k8s://team-a;" +
		"team-b=ref+vault://database/creds/team-b;" +
		"*=ref+awssecrets://shared"

	tests := []struct {
		name      string
		namespace string
		ref       string
		wantErr   bool
	}{
		{"own vault path permitted", "team-a", "ref+vault://database/creds/team-a#/username", false},
		{"own vault path with deeper segment permitted", "team-a", "ref+vault://database/creds/team-a/sub#/username", false},
		{"another tenant's vault path denied", "team-a", "ref+vault://database/creds/team-b#/username", true},

		// The bypass issue #102 identified: restricting a DbSecret's mount and role
		// is meaningless if the same dynamic credentials are reachable through a
		// ValsSecret reference.
		{"dynamic credential bypass denied", "team-a", "ref+vault://database/creds/other-tenant-role#/username", true},

		// A prefix must not match part-way through a path segment, or team-a's
		// prefix would cover team-abc's credentials.
		{"sibling namespace sharing a name prefix denied", "team-a", "ref+vault://database/creds/team-abc#/username", true},

		{"own k8s reference permitted", "team-a", "ref+k8s://team-a/some-secret#key", false},
		{"another namespace's k8s reference denied", "team-a", "ref+k8s://team-b/some-secret#key", true},

		{"wildcard entry permitted for any namespace", "team-b", "ref+awssecrets://shared/thing#key", false},
		{"wildcard does not permit unrelated paths", "team-b", "ref+awssecrets://private/thing#key", true},

		{"namespace absent from the allowlist is denied", "team-c", "ref+vault://database/creds/team-c#/username", true},

		{"cloud backend outside the allowlist denied", "team-a", "ref+gcpsecrets://project/secret#key", true},
	}

	a := mustAuthorizer(t, spec)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := a.Authorize(tt.namespace, tt.ref)
			if (err != nil) != tt.wantErr {
				t.Fatalf("Authorize(%q, %q) error = %v, wantErr %v", tt.namespace, tt.ref, err, tt.wantErr)
			}
			if err != nil {
				// The denial has to name both the namespace and the rejected path so
				// the event is actionable.
				if !strings.Contains(err.Error(), tt.namespace) || !strings.Contains(err.Error(), "denied") {
					t.Errorf("Authorize() error = %q, want it to name the namespace and say it was denied", err)
				}
			}
		})
	}
}

// A DbSecret's mount and role must be authorised as the equivalent ValsSecret
// reference, so one allowlist governs both resources.
func TestDbSecretAndValsSecretShareOneAllowlist(t *testing.T) {
	a := mustAuthorizer(t, "team-a=ref+vault://database/creds/team-a")

	permitted := canonicalDbSecretRef("database", "team-a")
	if err := a.Authorize("team-a", permitted); err != nil {
		t.Errorf("Authorize(%q) = %v, want nil", permitted, err)
	}

	denied := canonicalDbSecretRef("database", "team-b")
	if err := a.Authorize("team-a", denied); err == nil {
		t.Errorf("Authorize(%q) = nil, want a denial", denied)
	}

	// The DbSecret form and the ValsSecret form of the same credentials must reach
	// the same verdict, otherwise one is a way around the other.
	dbRef := canonicalDbSecretRef("database", "team-b")
	valsRef := "ref+vault://database/creds/team-b#/username"
	dbErr := a.Authorize("team-a", dbRef)
	valsErr := a.Authorize("team-a", valsRef)
	if (dbErr == nil) != (valsErr == nil) {
		t.Errorf("DbSecret form (%v) and ValsSecret form (%v) disagree for the same credentials", dbErr, valsErr)
	}
}

func TestRefHasPrefix(t *testing.T) {
	tests := []struct {
		ref    string
		prefix string
		want   bool
	}{
		{"ref+vault://db/creds/team-a", "ref+vault://db/creds/team-a", true},
		{"ref+vault://db/creds/team-a#/username", "ref+vault://db/creds/team-a", true},
		{"ref+vault://db/creds/team-a/x", "ref+vault://db/creds/team-a", true},
		{"ref+vault://db/creds/team-a", "ref+vault://db/creds/team-a/", true},
		{"ref+vault://db/creds/team-abc", "ref+vault://db/creds/team-a", false},
		{"ref+vault://db/creds/other", "ref+vault://db/creds/team-a", false},
		{"ref+vault://db", "ref+vault://db/creds", false},
		{"ref+vault://db/creds/x", "ref+vault://", true},
	}

	for _, tt := range tests {
		t.Run(tt.ref+"|"+tt.prefix, func(t *testing.T) {
			if got := refHasPrefix(tt.ref, tt.prefix); got != tt.want {
				t.Errorf("refHasPrefix(%q, %q) = %v, want %v", tt.ref, tt.prefix, got, tt.want)
			}
		})
	}
}
