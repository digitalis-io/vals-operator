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
	"sync"
	"text/template"

	sprig "github.com/Masterminds/sprig/v3"
)

// blockedTemplateFuncs lists the sprig functions that must never be available
// to a user-supplied template.
//
// Secret templates come from ValsSecret and DbSecret resources, which are
// namespaced: anybody able to create one of those resources in their own
// namespace can control the template. The operator renders it in-process, so a
// function able to read the environment or reach the network lets that user
// read the operator's own credentials - including the live Vault/OpenBao token,
// which the token renewer keeps in VAULT_TOKEN/BAO_TOKEN - or exfiltrate the
// rendered secret over DNS.
//
// These are the functions sprig itself documents as unsafe in shared
// environments (its "OS" and "Network" groups).
var blockedTemplateFuncs = []string{
	"env",
	"expandenv",
	"getHostByName",
}

var (
	safeFuncMapOnce sync.Once
	safeFuncMap     template.FuncMap
)

// SafeTemplateFuncMap returns the sprig function map with the functions that
// can read the operator's environment or reach the network removed. Use it
// instead of sprig.FuncMap() anywhere a user-supplied template is rendered.
//
// A template calling a blocked function fails to parse with
// `function "env" not defined`, which the caller surfaces as a normal template
// error.
func SafeTemplateFuncMap() template.FuncMap {
	safeFuncMapOnce.Do(func() {
		fm := sprig.TxtFuncMap()
		for _, name := range blockedTemplateFuncs {
			delete(fm, name)
		}
		safeFuncMap = fm
	})
	return safeFuncMap
}
