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
)

// wildcardNamespace is the namespace key applying to every namespace.
const wildcardNamespace = "*"

// BackendAuthorizer decides whether a resource in a given namespace may read a
// given backend path.
//
// The operator authenticates to the secrets backend once at startup and reuses
// that one credential for every reconcile in every namespace, so the credential's
// own policy is the real security boundary. Anybody able to create a ValsSecret
// or a DbSecret in any watched namespace can therefore reach anything that
// credential can read.
//
// This authorizer narrows that reach per namespace. It has to cover both
// resources and every backend to be worth anything: restricting only a
// DbSecret's mount and role is bypassed by a ValsSecret naming the same dynamic
// credential path, for example
// `ref+vault://database/creds/other-tenant-role#/username`.
//
// It is opt-in. A zero or unconfigured authorizer permits everything, so
// existing deployments are unaffected until they configure it.
type BackendAuthorizer struct {
	// allowed maps a namespace to the path prefixes it may read. The
	// wildcardNamespace key applies to every namespace, in addition to any
	// namespace-specific entry.
	allowed map[string][]string
}

// NewBackendAuthorizer parses the allowlist specification.
//
// The format is a semicolon-separated list of `namespace=prefix[,prefix...]`
// entries, where a namespace of `*` applies to every namespace:
//
//	tenant-a=ref+vault://database/creds/tenant-a,ref+k8s://tenant-a
//	tenant-b=ref+vault://database/creds/tenant-b
//	*=ref+awssecrets://shared
//
// An empty specification disables authorization entirely.
func NewBackendAuthorizer(spec string) (*BackendAuthorizer, error) {
	a := &BackendAuthorizer{allowed: map[string][]string{}}

	spec = strings.TrimSpace(strings.Trim(strings.TrimSpace(spec), `"`))
	if spec == "" {
		return a, nil
	}

	for _, entry := range strings.Split(spec, ";") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}

		namespace, prefixList, found := strings.Cut(entry, "=")
		if !found {
			return nil, fmt.Errorf("invalid entry %q: expected namespace=prefix[,prefix...]", entry)
		}
		namespace = strings.TrimSpace(namespace)
		if namespace == "" {
			return nil, fmt.Errorf("invalid entry %q: empty namespace", entry)
		}

		var prefixes []string
		for _, prefix := range strings.Split(prefixList, ",") {
			prefix = strings.TrimSpace(prefix)
			if prefix == "" {
				continue
			}
			prefixes = append(prefixes, prefix)
		}
		if len(prefixes) == 0 {
			return nil, fmt.Errorf("invalid entry %q: no prefixes given for namespace %q", entry, namespace)
		}

		a.allowed[namespace] = append(a.allowed[namespace], prefixes...)
	}

	return a, nil
}

// Enabled reports whether any allowlist was configured. When it is false every
// reference is permitted.
func (a *BackendAuthorizer) Enabled() bool {
	return a != nil && len(a.allowed) > 0
}

// Authorize returns nil if a resource in namespace may read ref.
//
// Callers must invoke this before contacting the backend, so that a denied
// reference produces no backend read at all.
func (a *BackendAuthorizer) Authorize(namespace, ref string) error {
	if !a.Enabled() {
		return nil
	}

	prefixes := append(append([]string{}, a.allowed[namespace]...), a.allowed[wildcardNamespace]...)
	if len(prefixes) == 0 {
		return fmt.Errorf("backend reference %q denied: namespace %q has no allowed backend paths", ref, namespace)
	}

	for _, prefix := range prefixes {
		if refHasPrefix(ref, prefix) {
			return nil
		}
	}

	return fmt.Errorf("backend reference %q denied: not covered by the allowed backend paths for namespace %q (%s)",
		ref, namespace, strings.Join(prefixes, ", "))
}

// AllowedFor returns the prefixes configured for a namespace, sorted. Used for
// logging and tests.
func (a *BackendAuthorizer) AllowedFor(namespace string) []string {
	if !a.Enabled() {
		return nil
	}
	prefixes := append(append([]string{}, a.allowed[namespace]...), a.allowed[wildcardNamespace]...)
	sort.Strings(prefixes)
	return prefixes
}

// refHasPrefix reports whether ref is covered by prefix, matching only on
// separator boundaries.
//
// A plain strings.HasPrefix would let a prefix of `.../creds/tenant-a` cover
// `.../creds/tenant-abc`, handing one tenant another tenant's credentials.
func refHasPrefix(ref, prefix string) bool {
	// The fragment selects a key within the secret, not the path being read.
	if hash := strings.IndexByte(ref, '#'); hash >= 0 {
		ref = ref[:hash]
	}
	prefix = strings.TrimRight(prefix, "/")

	if ref == prefix {
		return true
	}
	if !strings.HasPrefix(ref, prefix) {
		return false
	}

	// The character right after the prefix has to be a separator, otherwise the
	// prefix stopped in the middle of a path segment.
	next := ref[len(prefix)]
	return next == '/' || next == ':'
}

// canonicalDbSecretRef renders a DbSecret's mount and role as the backend
// reference a ValsSecret would use for the same credentials, so that one
// allowlist governs both resources.
func canonicalDbSecretRef(mount, role string) string {
	return fmt.Sprintf("ref+vault://%s/creds/%s", mount, role)
}
