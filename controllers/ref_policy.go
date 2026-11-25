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
	"net/url"
	"regexp"
	"sort"
	"strings"

	"github.com/helmfile/vals/pkg/expansion"
)

// defaultProviders are the vals schemes a reference may use without any
// configuration. Each reads from a secrets backend whose location is set by
// the operator's own configuration, so the path allowlist and the backend
// credential's policy bound what it can reach.
var defaultProviders = map[string]bool{
	"awskms":             true,
	"awssecrets":         true,
	"awsssm":             true,
	"azurekeyvault":      true,
	"bw":                 true,
	"conjur":             true,
	"doppler":            true,
	"gcpsecrets":         true,
	"gcs":                true,
	"gkms":               true,
	"googlesheets":       true,
	"hcpvaultsecrets":    true,
	"infisical":          true,
	"k8s":                true,
	"oci":                true,
	"onepasswordconnect": true,
	"op":                 true,
	"openbao":            true,
	"pulumistateapi":     true,
	"s3":                 true,
	"scw":                true,
	"servercore":         true,
	"tss":                true,
	"vault":              true,
	"yclockbox":          true,
}

// optInProviders are the vals schemes that are rejected unless listed in
// -allowed-extra-providers. They read the operator's own pod (its files,
// including the mounted ServiceAccount token, its environment, a command it
// runs) or fetch from a host or file named in the reference itself, using the
// operator's network position and credentials.
var optInProviders = map[string]bool{
	"echo":           true,
	"envsubst":       true,
	"exec":           true,
	"file":           true,
	"gitlab":         true,
	"httpjson":       true,
	"keychain":       true,
	"sops":           true,
	"tfstate":        true,
	"tfstateazurerm": true,
	"tfstategitlab":  true,
	"tfstategs":      true,
	"tfstateremote":  true,
	"tfstates3":      true,
}

// safeRefParams are the query parameters a reference may carry. They select or
// format the value being read. Every other parameter is rejected, because vals
// providers also accept parameters that point the read at another server
// (`address`, `host`, `proto`) or load credentials from the operator's pod
// (`token_file`, `token_env`, `password_file`, `credentials_file`, ...). Any of
// those lets a reference that passes the path allowlist send the operator's
// credential elsewhere or read local files.
var safeRefParams = map[string]bool{
	"alg":            true,
	"decode":         true,
	"encode":         true,
	"fallback_value": true,
	"format":         true,
	"generation":     true,
	"mode":           true,
	"optional":       true,
	"recursive":      true,
	"region":         true,
	"trim":           true,
	"trim_nl":        true,
	"version":        true,
	"version_id":     true,
	"version_stage":  true,
}

var (
	// regionValue keeps `region` to a region name, so it cannot steer an SDK
	// towards another endpoint host.
	regionValue = regexp.MustCompile(`^[a-z]{2}(-[a-z]+)+-[0-9]+$`)
	// paramValue bounds every other parameter except fallback_value, which is
	// returned as the value and never interpreted.
	paramValue = regexp.MustCompile(`^[A-Za-z0-9._-]*$`)
	// azureVaultHost keeps an azurekeyvault reference on Azure: vals connects to
	// the first path segment as given whenever it contains a '.'.
	azureVaultHost = regexp.MustCompile(`^[A-Za-z0-9-]{3,24}(\.(vault\.azure\.net|vault\.azure\.cn|vault\.usgovcloudapi\.net|managedhsm\.azure\.net))?$`)
)

// RefPolicy validates the shape of a ValsSecret reference before it is
// authorized or evaluated.
//
// It always applies, unlike BackendAuthorizer. vals expands every reference it
// finds in a string, including nested ones, so a `ref` holding more than one
// reference would have only its first one checked by the path allowlist. The
// policy therefore requires each `ref` to be exactly one reference with nothing
// around it.
//
// A nil RefPolicy behaves as one with no extra providers allowed.
type RefPolicy struct {
	allowedExtra map[string]bool
}

// NewRefPolicy parses a comma-separated list of opt-in providers to permit, for
// example `file,sops`. An empty list permits none.
func NewRefPolicy(allowedExtra string) (*RefPolicy, error) {
	p := &RefPolicy{allowedExtra: map[string]bool{}}
	for _, name := range strings.Split(allowedExtra, ",") {
		name = strings.ToLower(strings.TrimSpace(name))
		if name == "" {
			continue
		}
		if !optInProviders[name] {
			return nil, fmt.Errorf("invalid extra provider %q: must be one of %s", name, strings.Join(OptInProviderNames(), ", "))
		}
		p.allowedExtra[name] = true
	}
	return p, nil
}

// OptInProviderNames returns the providers -allowed-extra-providers accepts,
// sorted.
func OptInProviderNames() []string {
	return sortedKeys(optInProviders)
}

// AllowedExtra returns the opt-in providers this policy permits, sorted.
func (p *RefPolicy) AllowedExtra() []string {
	if p == nil {
		return nil
	}
	return sortedKeys(p.allowedExtra)
}

func sortedKeys(m map[string]bool) []string {
	names := make([]string, 0, len(m))
	for name := range m {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// normalizeRef trims surrounding whitespace from a value vals would expand,
// such as one written as a YAML block scalar. Literals are returned unchanged.
func normalizeRef(ref string) string {
	if trimmed := strings.TrimSpace(ref); expansion.DefaultRefRegexp.MatchString(trimmed) {
		return trimmed
	}
	return ref
}

// Validate returns nil if ref is a literal value or a single reference vals may
// evaluate.
func (p *RefPolicy) Validate(ref string) error {
	/* Nothing vals would expand: a literal, returned unchanged. vals only
	   expands, and only resolves nested references for, matches of this
	   regexp. */
	if !expansion.DefaultRefRegexp.MatchString(ref) {
		return nil
	}

	/* "secretref+" contains "ref+", so this counts both forms. More than one
	   means a second or nested reference that the allowlist would never see. */
	if n := strings.Count(ref, "ref+"); n != 1 {
		return fmt.Errorf("reference %q rejected: it must contain exactly one ref+ reference, found %d", ref, n)
	}

	ixs := expansion.DefaultRefRegexp.FindStringSubmatchIndex(ref)
	if ixs[0] != 0 || ixs[1] != len(ref) {
		return fmt.Errorf("reference %q rejected: it must be a single ref+<backend>://... reference with no other text", ref)
	}

	uri := ref[ixs[6]:ixs[7]]
	rawScheme, rest, _ := strings.Cut(uri, "://")
	scheme := strings.ToLower(rawScheme)

	switch {
	case scheme == "k8s":
		/* The controller reads ref+k8s:// itself, applying the namespace-sync
		   checks. Any other spelling would reach vals' own k8s provider and
		   skip them. */
		if !strings.HasPrefix(ref, k8sSecretPrefix) {
			return fmt.Errorf("reference %q rejected: Kubernetes references must be written as %s<namespace>/<secret>#<key>", ref, k8sSecretPrefix)
		}
	case defaultProviders[scheme]:
	case optInProviders[scheme]:
		if p == nil || !p.allowedExtra[scheme] {
			return fmt.Errorf("reference %q rejected: the %s provider is disabled; see -allowed-extra-providers", ref, scheme)
		}
	default:
		return fmt.Errorf("reference %q rejected: %q is not a supported provider", ref, scheme)
	}

	/* Same split as net/url, which vals uses: the fragment first, then the
	   query. The fragment selects a key in the value and is not checked. */
	rest, _, _ = strings.Cut(rest, "#")
	path, query, _ := strings.Cut(rest, "?")

	if strings.ContainsAny(path, `%\`) {
		return fmt.Errorf("reference %q rejected: '%%' and '\\' are not allowed in the path", ref)
	}

	/* '@' in the first segment would make net/url read what comes before it as
	   user information, so the host vals reads from is not the one the
	   allowlist matched. vals passes an AWS ARN through as a path instead, and
	   AWS names may contain '@'. */
	authority, _, _ := strings.Cut(path, "/")
	isARN := strings.HasPrefix(path, "arn:aws:") || strings.HasPrefix(path, "arn:aws-")
	if !isARN && strings.Contains(authority, "@") {
		return fmt.Errorf("reference %q rejected: '@' is not allowed before the first '/'", ref)
	}
	if scheme == "azurekeyvault" && !azureVaultHost.MatchString(authority) {
		return fmt.Errorf("reference %q rejected: %q is not an Azure Key Vault name or host", ref, authority)
	}
	for _, segment := range strings.Split(path, "/") {
		if segment == "." || segment == ".." {
			return fmt.Errorf("reference %q rejected: relative path segments are not allowed", ref)
		}
	}

	if query == "" {
		return nil
	}
	params, err := url.ParseQuery(query)
	if err != nil {
		return fmt.Errorf("reference %q rejected: invalid query string: %v", ref, err)
	}
	for name, values := range params {
		if !safeRefParams[name] {
			return fmt.Errorf("reference %q rejected: query parameter %q is not allowed", ref, name)
		}
		for _, value := range values {
			switch {
			case name == "fallback_value":
			case name == "region" && !regionValue.MatchString(value),
				name != "region" && !paramValue.MatchString(value):
				return fmt.Errorf("reference %q rejected: invalid value for query parameter %q", ref, name)
			}
		}
	}
	return nil
}
