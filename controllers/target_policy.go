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
	"fmt"
	"sort"
	"strings"

	"k8s.io/apimachinery/pkg/runtime/schema"
)

// TargetPolicy governs which resources a ValsSecret may write to through
// spec.target. It is built from the -enable-custom-targets and
// -allowed-target-resources flags.
type TargetPolicy struct {
	// Enabled is the feature gate. When false every ValsSecret with spec.target
	// is reported as FeatureDisabled and nothing is written.
	Enabled bool
	// DryRun performs a server-side dry-run apply before the real apply.
	DryRun  bool
	allowed map[schema.GroupResource]struct{}
}

// deniedTargetGroups can never be targeted, regardless of the allow-list.
var deniedTargetGroups = map[string]struct{}{
	"rbac.authorization.k8s.io":    {},
	"admissionregistration.k8s.io": {},
	"apiextensions.k8s.io":         {},
	"authentication.k8s.io":        {},
	"authorization.k8s.io":         {},
}

// deniedCoreResources can never be targeted, regardless of the allow-list.
var deniedCoreResources = map[string]struct{}{
	"serviceaccounts":   {},
	"pods":              {},
	"nodes":             {},
	"namespaces":        {},
	"persistentvolumes": {},
}

// NewTargetPolicy parses a comma separated list of "resource.group" entries
// (core group: just "configmaps"), e.g.
// "configmaps,flinkdeployments.flink.apache.org".
func NewTargetPolicy(enabled, dryRun bool, allowedList string) (*TargetPolicy, error) {
	p := &TargetPolicy{Enabled: enabled, DryRun: dryRun, allowed: map[schema.GroupResource]struct{}{}}
	for _, raw := range strings.Split(allowedList, ",") {
		entry := strings.TrimSpace(raw)
		if entry == "" {
			continue
		}
		gr := schema.ParseGroupResource(entry)
		if gr.Resource == "" || strings.ContainsAny(entry, " /") || gr.Resource != strings.ToLower(gr.Resource) {
			return nil, fmt.Errorf("invalid -allowed-target-resources entry %q: expected lowercase plural resource[.group]", entry)
		}
		if err := denied(gr); err != nil {
			return nil, fmt.Errorf("invalid -allowed-target-resources entry %q: %w", entry, err)
		}
		p.allowed[gr] = struct{}{}
	}
	return p, nil
}

func denied(gr schema.GroupResource) error {
	if _, ok := deniedTargetGroups[gr.Group]; ok {
		return fmt.Errorf("group %q can never be a custom target", gr.Group)
	}
	if gr.Group == "" {
		if _, ok := deniedCoreResources[gr.Resource]; ok {
			return fmt.Errorf("core resource %q can never be a custom target", gr.Resource)
		}
	}
	return nil
}

// Allowed returns nil when gr may be targeted.
func (p *TargetPolicy) Allowed(gr schema.GroupResource) error {
	if p == nil || !p.Enabled {
		return fmt.Errorf("custom targets are disabled (-enable-custom-targets)")
	}
	if err := denied(gr); err != nil {
		return err
	}
	if _, ok := p.allowed[gr]; !ok {
		return fmt.Errorf("%s is not in -allowed-target-resources", gr.String())
	}
	return nil
}

// AllowedResources lists the configured allow-list, sorted, for logging.
func (p *TargetPolicy) AllowedResources() []string {
	out := make([]string, 0, len(p.allowed))
	for gr := range p.allowed {
		out = append(out, gr.String())
	}
	sort.Strings(out)
	return out
}
