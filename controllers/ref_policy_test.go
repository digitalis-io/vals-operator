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
)

func TestRefPolicyAccepts(t *testing.T) {
	var p *RefPolicy
	for _, ref := range []string{
		"ref+vault://secret/team-a/app#key",
		"ref+vault://secret/data/team-a/app#/key",
		"ref+vault://database/creds/team-a#/username",
		"ref+vault://secret/team-a/app#key+",
		"secretref+vault://secret/team-a/app#key",
		"ref+awssecrets://myteam/mydoc?region=eu-west-1#/foo",
		"ref+awssecrets://arn:aws:secretsmanager:eu-west-1:123456789012:secret:myteam-AbCdEf#/foo",
		"ref+awsssm://myteam/mydoc?version=2",
		"ref+gcpsecrets://project/secret?version=latest",
		"ref+azurekeyvault://my-vault.vault.azure.net/secret",
		"ref+azurekeyvault://my-vault/secret",
		"ref+awssecrets://team/user@corp",
		"ref+awssecrets://arn:aws:secretsmanager:eu-west-1:123456789012:secret:user@corp-AbCdEf",
		"ref+k8s://tenant-a/my-secret#password",
		"ref+awsssm://myteam/params?mode=singleparam&recursive=true",
		"ref+awssecrets://myteam/mydoc?region=us-gov-west-1",
		"ref+vault://secret/team-a/app?fallback_value=not:set,yet#key",
		// Literals pass through vals unchanged.
		"plain-value",
		"",
		"ref+novalue",
		"a password with ref+ inside",
	} {
		if err := p.Validate(ref); err != nil {
			t.Errorf("Validate(%q) = %v, want nil", ref, err)
		}
	}
}

func TestRefPolicyRejects(t *testing.T) {
	var p *RefPolicy
	for name, ref := range map[string]string{
		// Advisory proof of concept: a second reference after an allowed one.
		"trailing file ref":  "ref+vault://secret/team-a/app#key+ ref+file:///var/run/secrets/kubernetes.io/serviceaccount/token+",
		"trailing vault ref": "ref+vault://secret/team-a/app#key+ ref+vault://secret/platform/crown-jewels#token+",
		"adjacent refs":      "ref+vault://secret/team-a/app#key+ref+vault://secret/platform/x#y+",
		"nested ref":         "ref+vault://ref+envsubst://$VAULT_PATH/x#y",
		"ref in fragment":    "ref+vault://secret/team-a/app#ref+file:///etc/passwd",
		"leading text":       "prefix ref+vault://secret/team-a/app#key",
		"leading k8s text":   " ref+k8s://kube-system/admin#token",
		"trailing text":      "ref+vault://secret/team-a/app#key suffix",
		"text before ref":    "pref+vault://secret/team-a/app#key",

		// Local providers are disabled by default.
		"file":             "ref+file:///var/run/secrets/kubernetes.io/serviceaccount/token",
		"file environ":     "ref+file:///proc/self/environ",
		"envsubst":         "ref+envsubst://$VAULT_ROLE_ID",
		"exec":             "ref+exec://cat/etc/passwd",
		"echo":             "ref+echo://foo",
		"upper-case local": "ref+FILE:///etc/passwd",

		// Providers that fetch from a host or file named in the reference.
		"httpjson":      "ref+httpjson://internal.svc.cluster.local/x#/field",
		"sops":          "ref+sops:///path/to/file.yaml#key",
		"tfstate":       "ref+tfstate:///path/file.json/res.attr",
		"tfstateremote": "ref+tfstateremote://app.terraform.io/org/ws/res.attr",
		"gitlab":        "ref+gitlab://attacker.example/project/var",
		"unknown":       "ref+madeup://foo/bar",

		// vals' own k8s provider would skip the namespace-sync checks.
		"upper-case k8s": "ref+K8S://v1/Secret/other-ns/name/key",
		"secretref k8s":  "secretref+k8s://v1/Secret/other-ns/name/key",

		// User information changes the host vals reads from.
		"userinfo":     "ref+vault://secret:x@other-mount/path/y#key",
		"aws userinfo": "ref+awssecrets://shared:x@other-secret",

		// azurekeyvault connects to the first segment when it contains a '.'.
		"azure foreign host":  "ref+azurekeyvault://attacker.example.com/secretname",
		"azure internal host": "ref+azurekeyvault://internal-svc.corp.example/secretname",
		"azure suffix trick":  "ref+azurekeyvault://x.vault.azure.net.attacker.example/secret",

		// Query parameters that redirect the read or load local credentials.
		"vault address":    "ref+vault://secret/team-a/app?address=https://attacker.example#key",
		"vault proto host": "ref+vault://secret/team-a/app?proto=http&host=attacker.example#key",
		"vault token_file": "ref+vault://secret/team-a/app?token_file=/var/run/secrets/kubernetes.io/serviceaccount/token#key",
		"vault auth":       "ref+vault://secret/team-a/app?auth_method=approle&role_id=x#key",
		"aws profile":      "ref+awssecrets://myteam/mydoc?profile=admin&role_arn=arn:aws:iam::1:role/x",
		"bad query":        "ref+vault://secret/team-a/app?%zz=1#key",
		"encoded name":     "ref+vault://secret/team-a/app?%61ddress=https://attacker.example#key",
		"bad region":       "ref+awssecrets://myteam/mydoc?region=x.attacker.example",
		"bad version":      "ref+awsssm://myteam/mydoc?version=1/../2",

		// Path tricks that could leave an allowed prefix.
		"dot-dot":         "ref+vault://secret/team-a/../platform/crown-jewels#token",
		"dot":             "ref+vault://secret/team-a/./app#key",
		"encoded dot-dot": "ref+vault://secret/team-a/%2e%2e/platform/crown-jewels#token",
		"backslash":       `ref+vault://secret/team-a\..\platform#token`,
	} {
		if err := p.Validate(ref); err == nil {
			t.Errorf("%s: Validate(%q) = nil, want a rejection", name, ref)
		}
	}
}

func TestRefPolicyAllowedExtra(t *testing.T) {
	p, err := NewRefPolicy(" file , ECHO ")
	if err != nil {
		t.Fatalf("NewRefPolicy() = %v, want nil", err)
	}
	if got := p.AllowedExtra(); len(got) != 2 || got[0] != "echo" || got[1] != "file" {
		t.Errorf("AllowedExtra() = %v, want [echo file]", got)
	}
	for _, ref := range []string{"ref+file:///etc/config/value", "ref+echo://foo"} {
		if err := p.Validate(ref); err != nil {
			t.Errorf("Validate(%q) = %v with the provider allowed, want nil", ref, err)
		}
	}
	for _, ref := range []string{
		"ref+exec://cat/etc/passwd",
		"ref+envsubst://$HOME",
		// Allowing a provider does not relax the single-reference rule.
		"ref+echo://foo+ ref+file:///etc/passwd+",
	} {
		if err := p.Validate(ref); err == nil {
			t.Errorf("Validate(%q) = nil, want a rejection", ref)
		}
	}
}

func TestNewRefPolicyErrors(t *testing.T) {
	for _, spec := range []string{"vault", "file,k8s", "files", "madeup"} {
		if _, err := NewRefPolicy(spec); err == nil {
			t.Errorf("NewRefPolicy(%q) = nil error, want an error", spec)
		}
	}
	p, err := NewRefPolicy("")
	if err != nil || len(p.AllowedExtra()) != 0 {
		t.Errorf("NewRefPolicy(\"\") = %v, %v; want an empty policy", p.AllowedExtra(), err)
	}
}
