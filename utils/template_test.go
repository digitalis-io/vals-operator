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

package utils

import (
	"bytes"
	"strings"
	"testing"
	"text/template"
)

// TestSafeTemplateFuncMapBlocksEnvironmentAccess is the regression guard for the
// operator's own credentials: the secrets backend token is kept in the process
// environment, and secret templates are supplied by whoever can create a
// ValsSecret or DbSecret in any namespace.
func TestSafeTemplateFuncMapBlocksEnvironmentAccess(t *testing.T) {
	t.Setenv("VAULT_TOKEN", "hvs.super-secret-operator-token")

	for _, tmpl := range []string{
		`{{ env "VAULT_TOKEN" }}`,
		`{{ expandenv "$VAULT_TOKEN" }}`,
		`{{ getHostByName "example.com" }}`,
	} {
		t.Run(tmpl, func(t *testing.T) {
			_, err := template.New("t").Funcs(SafeTemplateFuncMap()).Parse(tmpl)
			if err == nil {
				t.Fatalf("template %q parsed, want a 'function not defined' error", tmpl)
			}
			if !strings.Contains(err.Error(), "not defined") {
				t.Errorf("template %q failed with %v, want a 'function not defined' error", tmpl, err)
			}
		})
	}
}

func TestSafeTemplateFuncMapKeepsUsefulFunctions(t *testing.T) {
	data := map[string]string{"username": "v-root-readonly-abc", "password": "s3cr3t"}

	tests := []struct {
		tmpl string
		want string
	}{
		{`{{ .username }}`, "v-root-readonly-abc"},
		{`{{ .password | b64enc }}`, "czNjcjN0"},
		{`{{ .username | upper }}`, "V-ROOT-READONLY-ABC"},
		{`{{ printf "%s@db" .username }}`, "v-root-readonly-abc@db"},
	}

	for _, tt := range tests {
		t.Run(tt.tmpl, func(t *testing.T) {
			tpl, err := template.New("t").Funcs(SafeTemplateFuncMap()).Parse(tt.tmpl)
			if err != nil {
				t.Fatalf("Parse(%q) = %v, want nil", tt.tmpl, err)
			}
			b := bytes.NewBuffer(nil)
			if err := tpl.Execute(b, &data); err != nil {
				t.Fatalf("Execute(%q) = %v, want nil", tt.tmpl, err)
			}
			if got := b.String(); got != tt.want {
				t.Errorf("template %q rendered %q, want %q", tt.tmpl, got, tt.want)
			}
		})
	}
}

// CreateFakeHash renders the same user-supplied templates to build the change
// hash, so it must be sandboxed too.
func TestCreateFakeHashDoesNotRenderEnvironment(t *testing.T) {
	t.Setenv("VAULT_TOKEN", "hvs.super-secret-operator-token")

	if got := CreateFakeHash(map[string]string{"leak": `{{ env "VAULT_TOKEN" }}`}); got != "" {
		t.Errorf("CreateFakeHash() = %q for a template calling env, want an empty string (parse failure)", got)
	}
}
