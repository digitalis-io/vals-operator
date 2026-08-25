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

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"

	secretv1 "digitalis.io/vals-operator/apis/digitalis.io/v1"
)

func TestNewTargetPolicy(t *testing.T) {
	tests := []struct {
		name    string
		list    string
		wantErr bool
		allowed []string
	}{
		{"empty", "", false, nil},
		{"core and group", "configmaps, flinkdeployments.flink.apache.org", false, []string{"configmaps", "flinkdeployments.flink.apache.org"}},
		{"uppercase", "ConfigMaps", true, nil},
		{"slash form", "flink.apache.org/flinkdeployments", true, nil},
		{"denied group", "roles.rbac.authorization.k8s.io", true, nil},
		{"denied core", "pods", true, nil},
		{"trailing comma", "configmaps,", false, []string{"configmaps"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, err := NewTargetPolicy(true, true, tt.list)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if err != nil {
				return
			}
			if got := strings.Join(p.AllowedResources(), ","); got != strings.Join(tt.allowed, ",") {
				t.Fatalf("allowed = %q, want %q", got, tt.allowed)
			}
		})
	}
}

func TestTargetPolicyAllowed(t *testing.T) {
	enabled, _ := NewTargetPolicy(true, true, "configmaps,testapps.test.digitalis.io")
	disabled, _ := NewTargetPolicy(false, true, "configmaps")
	var nilPolicy *TargetPolicy

	tests := []struct {
		name    string
		p       *TargetPolicy
		gr      schema.GroupResource
		wantErr string
	}{
		{"allowed core", enabled, schema.GroupResource{Resource: "configmaps"}, ""},
		{"allowed crd", enabled, schema.GroupResource{Group: "test.digitalis.io", Resource: "testapps"}, ""},
		{"not listed", enabled, schema.GroupResource{Resource: "secrets"}, "not in -allowed-target-resources"},
		{"denied even if listed", enabled, schema.GroupResource{Group: "rbac.authorization.k8s.io", Resource: "roles"}, "can never be a custom target"},
		{"disabled", disabled, schema.GroupResource{Resource: "configmaps"}, "disabled"},
		{"nil", nilPolicy, schema.GroupResource{Resource: "configmaps"}, "disabled"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.p.Allowed(tt.gr)
			if tt.wantErr == "" && err != nil {
				t.Fatalf("unexpected error %v", err)
			}
			if tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)) {
				t.Fatalf("err = %v, want containing %q", err, tt.wantErr)
			}
		})
	}
}

func TestBuildTemplateContext(t *testing.T) {
	vs := &secretv1.ValsSecret{ObjectMeta: metav1.ObjectMeta{Name: "n", Namespace: "ns"}}
	ctx, shadowed := buildTemplateContext(vs, map[string]string{"user": "u", "Secrets": "evil", "ValsSecret": "evil"})
	if ctx["user"] != "u" {
		t.Fatalf("flat key missing: %v", ctx)
	}
	if len(shadowed) != 2 {
		t.Fatalf("shadowed = %v", shadowed)
	}
	if _, ok := ctx["Secrets"].(map[string]string); !ok {
		t.Fatalf("Secrets overwritten: %T", ctx["Secrets"])
	}
	meta := ctx["ValsSecret"].(map[string]interface{})
	if meta["Name"] != "n" || meta["Namespace"] != "ns" {
		t.Fatalf("ValsSecret meta = %v", meta)
	}
	if ctx["Secrets"].(map[string]string)["Secrets"] != "evil" {
		t.Fatal("shadowed key must remain reachable via .Secrets")
	}
}

func TestRenderTargetBody(t *testing.T) {
	ctx, _ := buildTemplateContext(&secretv1.ValsSecret{ObjectMeta: metav1.ObjectMeta{Name: "n", Namespace: "ns"}},
		map[string]string{"key": "s3cr3t", "multi": "a: b\nc: d"})

	tests := []struct {
		name    string
		tmpl    string
		wantErr string
		check   func(map[string]interface{}) bool
	}{
		{"nested spec", "spec:\n  config:\n    api.key: {{ .key | quote }}\n", "",
			func(b map[string]interface{}) bool {
				return b["spec"].(map[string]interface{})["config"].(map[string]interface{})["api.key"] == "s3cr3t"
			}},
		{"configmap data via Secrets", "data:\n  k: {{ .Secrets.key }}\n", "",
			func(b map[string]interface{}) bool { return b["data"].(map[string]interface{})["k"] == "s3cr3t" }},
		{"valssecret meta", "data:\n  who: {{ .ValsSecret.Namespace }}/{{ .ValsSecret.Name }}\n", "",
			func(b map[string]interface{}) bool { return b["data"].(map[string]interface{})["who"] == "ns/n" }},
		{"sprig", "data:\n  k: {{ .key | upper }}\n", "",
			func(b map[string]interface{}) bool { return b["data"].(map[string]interface{})["k"] == "S3CR3T" }},
		{"parse error", "data: {{ .key", "cannot parse", nil},
		{"missing key", "data:\n  k: {{ .nope }}\n", "cannot render", nil},
		{"blocked env func", "data:\n  k: {{ env \"HOME\" }}\n", "cannot parse", nil},
		{"not a mapping", "- a\n- b\n", "not a YAML mapping", nil},
		{"empty", "# nothing\n", "empty", nil},
		{"metadata forbidden", "metadata:\n  name: x\n", `must not set "metadata"`, nil},
		{"kind forbidden", "kind: Pod\nspec: {}\n", `must not set "kind"`, nil},
		{"status forbidden", "status:\n  ok: true\n", `must not set "status"`, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body, err := renderTargetBody(tt.tmpl, ctx)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want containing %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !tt.check(body) {
				t.Fatalf("unexpected body: %v", body)
			}
		})
	}
}

func TestHashBodyStable(t *testing.T) {
	a := map[string]interface{}{"spec": map[string]interface{}{"b": "2", "a": "1"}}
	b := map[string]interface{}{"spec": map[string]interface{}{"a": "1", "b": "2"}}
	c := map[string]interface{}{"spec": map[string]interface{}{"a": "1", "b": "3"}}
	if hashBody(a) != hashBody(b) {
		t.Fatal("hash must not depend on key order")
	}
	if hashBody(a) == hashBody(c) {
		t.Fatal("hash must change with content")
	}
}

func TestTargetNameAndMode(t *testing.T) {
	vs := &secretv1.ValsSecret{ObjectMeta: metav1.ObjectMeta{Name: "vs"}, Spec: secretv1.ValsSecretSpec{Target: &secretv1.Target{}}}
	if targetName(vs) != "vs" || targetMode(vs) != secretv1.TargetModeCreate {
		t.Fatal("defaults")
	}
	vs.Spec.Target.Name = "other"
	vs.Spec.Target.Mode = secretv1.TargetModePatch
	if targetName(vs) != "other" || targetMode(vs) != secretv1.TargetModePatch {
		t.Fatal("explicit")
	}
}
