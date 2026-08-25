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
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	goerrors "errors"
	"fmt"
	"text/template"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/yaml"

	secretv1 "digitalis.io/vals-operator/apis/digitalis.io/v1"
	dmetrics "digitalis.io/vals-operator/metrics"
	"digitalis.io/vals-operator/utils"
)

// targetFieldManager is the Server-Side Apply field manager. Only fields
// asserted under this manager are owned (and later removed) by the operator.
const targetFieldManager = "vals-operator"

// targetError carries the Ready condition reason alongside the error.
//
// safe is the message that may be exposed on the Ready condition and in
// events. Render and API server errors can echo rendered values — which are
// secrets — so those carry a generic safe message and the full error goes to
// the controller log only.
type targetError struct {
	reason string
	err    error
	safe   string
}

func (e *targetError) Error() string { return e.err.Error() }
func (e *targetError) Unwrap() error { return e.err }

func newTargetError(reason string, err error) error {
	return &targetError{reason: reason, err: err, safe: err.Error()}
}

// newUnsafeTargetError is for errors whose text may contain rendered data.
func newUnsafeTargetError(reason, safe string, err error) error {
	return &targetError{reason: reason, err: err, safe: safe + " (see operator log)"}
}

// reservedTemplateKeys cannot be shadowed by data keys in the template context.
var reservedTemplateKeys = map[string]struct{}{"Secrets": {}, "ValsSecret": {}}

// forbiddenBodyKeys must not appear in the rendered target body.
var forbiddenBodyKeys = []string{"apiVersion", "kind", "metadata", "status"}

// targetName returns the target object name.
func targetName(vs *secretv1.ValsSecret) string {
	if vs.Spec.Target != nil && vs.Spec.Target.Name != "" {
		return vs.Spec.Target.Name
	}
	return vs.Name
}

func targetMode(vs *secretv1.ValsSecret) secretv1.TargetMode {
	if vs.Spec.Target != nil && vs.Spec.Target.Mode == secretv1.TargetModePatch {
		return secretv1.TargetModePatch
	}
	return secretv1.TargetModeCreate
}

// buildTemplateContext flattens the resolved data into the root (matching
// spec.template) and exposes the same data under .Secrets plus ValsSecret
// metadata. Data keys colliding with reserved names are only reachable through
// .Secrets.
func buildTemplateContext(vs *secretv1.ValsSecret, data map[string]string) (map[string]interface{}, []string) {
	ctx := make(map[string]interface{}, len(data)+2)
	var shadowed []string
	for k, v := range data {
		if _, reserved := reservedTemplateKeys[k]; reserved {
			shadowed = append(shadowed, k)
			continue
		}
		ctx[k] = v
	}
	ctx["Secrets"] = data
	ctx["ValsSecret"] = map[string]interface{}{
		"Name":      vs.Name,
		"Namespace": vs.Namespace,
	}
	return ctx, shadowed
}

// renderTargetBody renders tmpl and parses it as a YAML mapping.
func renderTargetBody(tmpl string, ctx map[string]interface{}) (map[string]interface{}, error) {
	t, err := template.New("target").Funcs(utils.SafeTemplateFuncMap()).Option("missingkey=error").Parse(tmpl)
	if err != nil {
		return nil, fmt.Errorf("cannot parse target template: %w", err)
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, ctx); err != nil {
		return nil, fmt.Errorf("cannot render target template: %w", err)
	}
	body := map[string]interface{}{}
	if err := yaml.Unmarshal(buf.Bytes(), &body); err != nil {
		return nil, fmt.Errorf("rendered target template is not a YAML mapping: %w", err)
	}
	if len(body) == 0 {
		return nil, fmt.Errorf("rendered target template is empty")
	}
	for _, k := range forbiddenBodyKeys {
		if _, ok := body[k]; ok {
			return nil, fmt.Errorf("rendered target template must not set %q", k)
		}
	}
	return body, nil
}

// hashBody returns a stable sha256 of the rendered body.
func hashBody(body map[string]interface{}) string {
	b, _ := json.Marshal(body) // map keys are sorted by encoding/json
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// resolveTarget validates the GVK against the RESTMapper and the policy and
// returns the mapping.
func (r *ValsSecretReconciler) resolveTarget(t *secretv1.Target) (*meta.RESTMapping, error) {
	gvk := schema.FromAPIVersionAndKind(t.APIVersion, t.Kind)
	mapping, err := r.RESTMapper().RESTMapping(gvk.GroupKind(), gvk.Version)
	if err != nil {
		return nil, newTargetError(secretv1.ReasonTargetNotAllowed, fmt.Errorf("unknown target %s: %w", gvk, err))
	}
	if mapping.Scope.Name() != meta.RESTScopeNameNamespace {
		return nil, newTargetError(secretv1.ReasonTargetNotAllowed, fmt.Errorf("cluster-scoped target %s is not supported", gvk))
	}
	if err := r.TargetPolicy.Allowed(mapping.Resource.GroupResource()); err != nil {
		reason := secretv1.ReasonTargetNotAllowed
		if r.TargetPolicy == nil || !r.TargetPolicy.Enabled {
			reason = secretv1.ReasonFeatureDisabled
		}
		return nil, newTargetError(reason, err)
	}
	return mapping, nil
}

// applyTarget renders spec.target.template with data and applies it to the
// target object with Server-Side Apply.
func (r *ValsSecretReconciler) applyTarget(ctx context.Context, vs *secretv1.ValsSecret, data map[string]string) error {
	t := vs.Spec.Target
	mode := targetMode(vs)

	mapping, err := r.resolveTarget(t)
	if err != nil {
		return err
	}

	tmplCtx, shadowed := buildTemplateContext(vs, data)
	for _, k := range shadowed {
		r.Log.Info("data key shadows a reserved template name; use .Secrets.<key>",
			"name", vs.Name, "namespace", vs.Namespace, "key", k)
	}
	body, err := renderTargetBody(t.Template, tmplCtx)
	if err != nil {
		return newUnsafeTargetError(secretv1.ReasonRenderError, "target template failed to render or is not a valid body", err)
	}

	obj := &unstructured.Unstructured{Object: body}
	obj.SetGroupVersionKind(mapping.GroupVersionKind)
	obj.SetName(targetName(vs))
	obj.SetNamespace(vs.Namespace)

	hash := hashBody(body)

	switch mode {
	case secretv1.TargetModePatch:
		existing := &unstructured.Unstructured{}
		existing.SetGroupVersionKind(mapping.GroupVersionKind)
		if err := r.APIReader.Get(ctx, client.ObjectKeyFromObject(obj), existing); err != nil {
			if errors.IsNotFound(err) {
				return newTargetError(secretv1.ReasonTargetNotFound,
					fmt.Errorf("target %s %s/%s does not exist and mode is patch", t.Kind, vs.Namespace, obj.GetName()))
			}
			return newUnsafeTargetError(secretv1.ReasonApplyError, "cannot read target object", err)
		}
	default:
		labels := map[string]string{}
		for k, v := range t.Labels {
			labels[k] = v
		}
		labels[managedByLabel] = "vals-operator"
		obj.SetLabels(labels)
		if len(t.Annotations) > 0 {
			obj.SetAnnotations(t.Annotations)
		}
		if err := controllerutil.SetControllerReference(vs, obj, r.Scheme()); err != nil {
			return newTargetError(secretv1.ReasonApplyError, err)
		}
	}

	// resolveTarget above already required TargetPolicy != nil.
	if r.TargetPolicy.DryRun {
		dryRunOpts := []client.ApplyOption{client.FieldOwner(targetFieldManager), client.ForceOwnership, client.DryRunAll}
		if err := r.Apply(ctx, client.ApplyConfigurationFromUnstructured(obj.DeepCopy()), dryRunOpts...); err != nil {
			return newUnsafeTargetError(secretv1.ReasonApplyError, "dry-run apply rejected by the API server; nothing was written",
				fmt.Errorf("dry-run apply failed: %w", err))
		}
	}
	applyOpts := []client.ApplyOption{client.FieldOwner(targetFieldManager), client.ForceOwnership}
	if err := r.Apply(ctx, client.ApplyConfigurationFromUnstructured(obj), applyOpts...); err != nil {
		return newUnsafeTargetError(secretv1.ReasonApplyError, "apply rejected by the API server", err)
	}

	r.Log.Info("Applied custom target", "name", vs.Name, "namespace", vs.Namespace,
		"targetKind", t.Kind, "targetName", obj.GetName(), "mode", mode)
	if r.recordingEnabled(vs) {
		r.Recorder.Event(vs, corev1.EventTypeNormal, "TargetApplied",
			fmt.Sprintf("Applied %s %s/%s (%s)", t.Kind, vs.Namespace, obj.GetName(), mode))
	}

	vs.Status.Target = &secretv1.TargetStatus{
		APIVersion:  obj.GetAPIVersion(),
		Kind:        obj.GetKind(),
		Name:        obj.GetName(),
		Namespace:   obj.GetNamespace(),
		Mode:        mode,
		Hash:        hash,
		LastApplied: metav1.Now(),
	}
	return nil
}

// releaseTarget is the finalizer action. In create mode the target is deleted;
// in patch mode the operator's fields are removed by applying an empty body
// under the same field manager.
//
// It deliberately does not consult TargetPolicy: cleanup of something the
// operator previously wrote must still work after the allow-list is narrowed
// or the feature is switched off, otherwise the ValsSecret can never be
// deleted. It writes nothing but the removal of its own fields.
func (r *ValsSecretReconciler) releaseTarget(ctx context.Context, vs *secretv1.ValsSecret) error {
	t := vs.Spec.Target
	gvk := schema.FromAPIVersionAndKind(t.APIVersion, t.Kind)
	obj := &unstructured.Unstructured{}
	obj.SetGroupVersionKind(gvk)
	obj.SetName(targetName(vs))
	obj.SetNamespace(vs.Namespace)

	if targetMode(vs) == secretv1.TargetModeCreate {
		return client.IgnoreNotFound(r.Delete(ctx, obj))
	}
	err := r.Apply(ctx, client.ApplyConfigurationFromUnstructured(obj), client.FieldOwner(targetFieldManager), client.ForceOwnership)
	if errors.IsNotFound(err) || meta.IsNoMatchError(err) {
		return nil
	}
	return err
}

// reconcileTarget drives applyTarget and reflects the result in status,
// metrics and events. It never returns an error for policy/render failures:
// those are surfaced on the Ready condition and retried on the next period.
//
// When preErr is non-nil (a policy failure detected before the backend was
// read) it is reported as-is and applyTarget is not called.
func (r *ValsSecretReconciler) reconcileTarget(ctx context.Context, vs *secretv1.ValsSecret, data map[string]string, preErr error) error {
	kind := vs.Spec.Target.Kind
	mode := string(targetMode(vs))

	err := preErr
	if err == nil {
		err = r.applyTarget(ctx, vs, data)
	}
	if err != nil {
		reason := secretv1.ReasonApplyError
		message := "custom target failed (see operator log)"
		var te *targetError
		if goerrors.As(err, &te) {
			reason = te.reason
			message = te.safe
		}
		r.Log.Error(err, "Custom target apply failed", "name", vs.Name, "namespace", vs.Namespace, "reason", reason)
		dmetrics.TargetError.WithLabelValues(vs.Name, vs.Namespace).SetToCurrentTime()
		dmetrics.TargetApplyTotal.WithLabelValues(vs.Name, vs.Namespace, kind, mode, "error").Inc()
		if r.recordingEnabled(vs) {
			r.Recorder.Event(vs, corev1.EventTypeWarning, reason, message)
		}
		meta.SetStatusCondition(&vs.Status.Conditions, metav1.Condition{
			Type:               secretv1.ConditionReady,
			Status:             metav1.ConditionFalse,
			Reason:             reason,
			Message:            message,
			ObservedGeneration: vs.Generation,
		})
	} else {
		dmetrics.TargetError.WithLabelValues(vs.Name, vs.Namespace).Set(0)
		if vs.Status.Target != nil {
			dmetrics.TargetLastApplied.WithLabelValues(vs.Name, vs.Namespace, kind).Set(float64(vs.Status.Target.LastApplied.Unix()))
		}
		meta.SetStatusCondition(&vs.Status.Conditions, metav1.Condition{
			Type:               secretv1.ConditionReady,
			Status:             metav1.ConditionTrue,
			Reason:             secretv1.ReasonApplied,
			Message:            fmt.Sprintf("%s %s/%s applied", kind, vs.Namespace, targetName(vs)),
			ObservedGeneration: vs.Generation,
		})
	}
	if serr := r.Status().Update(ctx, vs); serr != nil {
		r.Log.Error(serr, "Cannot update ValsSecret status", "name", vs.Name, "namespace", vs.Namespace)
		return serr
	}
	if err == nil && vs.Status.Target != nil {
		dmetrics.TargetApplyTotal.WithLabelValues(vs.Name, vs.Namespace, kind, mode, "success").Inc()
	}
	return err
}
