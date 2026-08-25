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
	"context"
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	secretv1 "digitalis.io/vals-operator/apis/digitalis.io/v1"
)

var testAppGVK = schema.GroupVersionKind{Group: "test.digitalis.io", Version: "v1", Kind: "TestApp"}

// Custom targets are exercised by calling Reconcile directly against envtest.
// Data refs are literal strings: the controller passes anything without a
// ref+ prefix through vals.Eval unchanged, so no backend is needed.
var _ = Describe("ValsSecret custom targets", func() {
	const ns = "targets"
	var (
		ctx context.Context
		rec *ValsSecretReconciler
		seq int
	)

	newReconciler := func(policy *TargetPolicy) *ValsSecretReconciler {
		return &ValsSecretReconciler{
			Client:               k8sClient,
			APIReader:            k8sClient,
			Log:                  logf.Log.WithName("test"),
			Ctx:                  ctx,
			ReconciliationPeriod: time.Second,
			DefaultTTL:           time.Hour,
			RecordChanges:        true,
			Recorder:             record.NewFakeRecorder(100),
			TargetPolicy:         policy,
		}
	}

	reconcile := func(name string) {
		_, err := rec.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: name, Namespace: ns}})
		ExpectWithOffset(1, err).NotTo(HaveOccurred())
	}

	getVS := func(name string) *secretv1.ValsSecret {
		vs := &secretv1.ValsSecret{}
		ExpectWithOffset(1, k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: ns}, vs)).To(Succeed())
		return vs
	}

	readyReason := func(name string) string {
		c := meta.FindStatusCondition(getVS(name).Status.Conditions, secretv1.ConditionReady)
		if c == nil {
			return ""
		}
		return c.Reason
	}

	newVS := func(target secretv1.Target, data map[string]string) *secretv1.ValsSecret {
		seq++
		d := map[string]secretv1.DataSource{}
		for k, v := range data {
			d[k] = secretv1.DataSource{Ref: v}
		}
		vs := &secretv1.ValsSecret{
			ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("vs-%d", seq), Namespace: ns},
			Spec:       secretv1.ValsSecretSpec{Data: d, Target: &target},
		}
		Expect(k8sClient.Create(ctx, vs)).To(Succeed())
		return vs
	}

	getTestApp := func(name string) *unstructured.Unstructured {
		u := &unstructured.Unstructured{}
		u.SetGroupVersionKind(testAppGVK)
		ExpectWithOffset(1, k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: ns}, u)).To(Succeed())
		return u
	}

	BeforeEach(func() {
		ctx = context.Background()
		policy, err := NewTargetPolicy(true, true, "configmaps,testapps.test.digitalis.io")
		Expect(err).NotTo(HaveOccurred())
		rec = newReconciler(policy)
		nsObj := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: ns}}
		_ = k8sClient.Create(ctx, nsObj)
	})

	Context("CRD validation", func() {
		It("rejects target together with template", func() {
			vs := &secretv1.ValsSecret{
				ObjectMeta: metav1.ObjectMeta{Name: "invalid-template", Namespace: ns},
				Spec: secretv1.ValsSecretSpec{
					Data:     map[string]secretv1.DataSource{"k": {Ref: "v"}},
					Template: map[string]string{"x": "y"},
					Target:   &secretv1.Target{APIVersion: "v1", Kind: "ConfigMap", Template: "data: {}"},
				},
			}
			err := k8sClient.Create(ctx, vs)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("spec.template cannot be used with spec.target"))
		})

		It("rejects target together with databases", func() {
			vs := &secretv1.ValsSecret{
				ObjectMeta: metav1.ObjectMeta{Name: "invalid-db", Namespace: ns},
				Spec: secretv1.ValsSecretSpec{
					Data:      map[string]secretv1.DataSource{"k": {Ref: "v"}},
					Databases: []secretv1.Database{{Driver: "postgres", PasswordKey: "k", Hosts: []string{"h"}}},
					Target:    &secretv1.Target{APIVersion: "v1", Kind: "ConfigMap", Template: "data: {}"},
				},
			}
			Expect(k8sClient.Create(ctx, vs)).NotTo(Succeed())
		})

		It("rejects an unknown mode", func() {
			vs := &secretv1.ValsSecret{
				ObjectMeta: metav1.ObjectMeta{Name: "invalid-mode", Namespace: ns},
				Spec: secretv1.ValsSecretSpec{
					Data:   map[string]secretv1.DataSource{"k": {Ref: "v"}},
					Target: &secretv1.Target{APIVersion: "v1", Kind: "ConfigMap", Mode: "merge", Template: "data: {}"},
				},
			}
			Expect(k8sClient.Create(ctx, vs)).NotTo(Succeed())
		})

		It("still accepts a legacy ValsSecret without target", func() {
			vs := &secretv1.ValsSecret{
				ObjectMeta: metav1.ObjectMeta{Name: "legacy", Namespace: ns},
				Spec: secretv1.ValsSecretSpec{
					Data:     map[string]secretv1.DataSource{"k": {Ref: "v"}},
					Template: map[string]string{"x": "{{ .k }}"},
				},
			}
			Expect(k8sClient.Create(ctx, vs)).To(Succeed())
		})
	})

	Context("feature gate", func() {
		It("reports FeatureDisabled and writes nothing when the gate is off", func() {
			rec = newReconciler(&TargetPolicy{Enabled: false})
			vs := newVS(secretv1.Target{APIVersion: "v1", Kind: "ConfigMap", Template: "data:\n  k: {{ .k }}\n"},
				map[string]string{"k": "v"})
			reconcile(vs.Name)
			Expect(readyReason(vs.Name)).To(Equal(secretv1.ReasonFeatureDisabled))
			cm := &corev1.ConfigMap{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: vs.Name, Namespace: ns}, cm)).NotTo(Succeed())
		})

		It("reports TargetNotAllowed for a resource outside the allow-list", func() {
			vs := newVS(secretv1.Target{APIVersion: "v1", Kind: "Secret", Template: "stringData:\n  k: {{ .k }}\n"},
				map[string]string{"k": "v"})
			reconcile(vs.Name)
			Expect(readyReason(vs.Name)).To(Equal(secretv1.ReasonTargetNotAllowed))
		})

		It("reports TargetNotAllowed for an unknown kind", func() {
			vs := newVS(secretv1.Target{APIVersion: "nope.io/v1", Kind: "Nothing", Template: "spec: {}\n"},
				map[string]string{"k": "v"})
			reconcile(vs.Name)
			Expect(readyReason(vs.Name)).To(Equal(secretv1.ReasonTargetNotAllowed))
		})
	})

	Context("create mode", func() {
		It("creates a ConfigMap owned by the ValsSecret and deletes it with the finalizer", func() {
			vs := newVS(secretv1.Target{
				APIVersion: "v1", Kind: "ConfigMap", Name: "app-config",
				Labels:   map[string]string{"app": "demo"},
				Template: "data:\n  application.yaml: |\n    db:\n      host: {{ .db_host }}\n  region: {{ .region }}\n",
			}, map[string]string{"db_host": "db.internal", "region": "eu-west-1"})
			reconcile(vs.Name) // adds finalizer
			reconcile(vs.Name)

			Expect(readyReason(vs.Name)).To(Equal(secretv1.ReasonApplied))
			cm := &corev1.ConfigMap{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "app-config", Namespace: ns}, cm)).To(Succeed())
			Expect(cm.Data["region"]).To(Equal("eu-west-1"))
			Expect(cm.Data["application.yaml"]).To(ContainSubstring("host: db.internal"))
			Expect(cm.Labels["app"]).To(Equal("demo"))
			Expect(cm.Labels[managedByLabel]).To(Equal("vals-operator"))
			Expect(cm.OwnerReferences).To(HaveLen(1))
			Expect(cm.OwnerReferences[0].Name).To(Equal(vs.Name))

			st := getVS(vs.Name).Status.Target
			Expect(st).NotTo(BeNil())
			Expect(st.Kind).To(Equal("ConfigMap"))
			Expect(st.Mode).To(Equal(secretv1.TargetModeCreate))
			Expect(st.Hash).NotTo(BeEmpty())

			Expect(k8sClient.Delete(ctx, getVS(vs.Name))).To(Succeed())
			reconcile(vs.Name) // finalizer runs
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "app-config", Namespace: ns}, cm)).NotTo(Succeed())
		})

		It("reports RenderError for a template that sets metadata", func() {
			vs := newVS(secretv1.Target{APIVersion: "v1", Kind: "ConfigMap", Template: "metadata:\n  name: x\ndata: {}\n"},
				map[string]string{"k": "v"})
			reconcile(vs.Name)
			reconcile(vs.Name)
			Expect(readyReason(vs.Name)).To(Equal(secretv1.ReasonRenderError))
		})

		It("reports ApplyError when the rendered body fails schema validation", func() {
			vs := newVS(secretv1.Target{APIVersion: "test.digitalis.io/v1", Kind: "TestApp", Template: "spec:\n  replicas: {{ .n | quote }}\n"},
				map[string]string{"n": "three-is-secret"})
			reconcile(vs.Name)
			reconcile(vs.Name)
			Expect(readyReason(vs.Name)).To(Equal(secretv1.ReasonApplyError))
			c := meta.FindStatusCondition(getVS(vs.Name).Status.Conditions, secretv1.ConditionReady)
			Expect(c.Message).NotTo(ContainSubstring("three-is-secret"), "API server errors echo values; they must not reach status")
			u := &unstructured.Unstructured{}
			u.SetGroupVersionKind(testAppGVK)
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: vs.Name, Namespace: ns}, u)).NotTo(Succeed(), "dry-run must prevent the write")
		})
	})

	Context("patch mode", func() {
		var app *unstructured.Unstructured

		BeforeEach(func() {
			seq++
			app = &unstructured.Unstructured{}
			app.SetGroupVersionKind(testAppGVK)
			app.SetName(fmt.Sprintf("app-%d", seq))
			app.SetNamespace(ns)
			app.Object["spec"] = map[string]interface{}{
				"replicas": int64(3),
				"config":   map[string]interface{}{"existing": "keep-me"},
				"containers": []interface{}{
					map[string]interface{}{"name": "main", "image": "app:v1",
						"env": []interface{}{map[string]interface{}{"name": "KEEP", "value": "yes"}}},
					map[string]interface{}{"name": "sidecar", "image": "proxy:v1"},
				},
			}
			// Simulate a GitOps tool owning the object with its own field manager.
			Expect(k8sClient.Apply(ctx, client.ApplyConfigurationFromUnstructured(app), client.FieldOwner("argocd"))).To(Succeed())
		})

		It("injects only the rendered fields into an existing CR and keeps everything else", func() {
			vs := newVS(secretv1.Target{
				APIVersion: "test.digitalis.io/v1", Kind: "TestApp", Name: app.GetName(), Mode: secretv1.TargetModePatch,
				Template: "spec:\n  config:\n    metrics.reporter.dghttp.apikey: {{ .dd | quote }}\n" +
					"  containers:\n    - name: main\n      env:\n        - name: DD_API_KEY\n          value: {{ .dd | quote }}\n",
			}, map[string]string{"dd": "dd-secret"})
			reconcile(vs.Name)
			reconcile(vs.Name)
			Expect(readyReason(vs.Name)).To(Equal(secretv1.ReasonApplied))

			got := getTestApp(app.GetName())
			spec := got.Object["spec"].(map[string]interface{})
			Expect(spec["replicas"]).To(Equal(int64(3)))
			cfg := spec["config"].(map[string]interface{})
			Expect(cfg["existing"]).To(Equal("keep-me"))
			Expect(cfg["metrics.reporter.dghttp.apikey"]).To(Equal("dd-secret"))
			containers := spec["containers"].([]interface{})
			Expect(containers).To(HaveLen(2))
			main := containers[0].(map[string]interface{})
			Expect(main["image"]).To(Equal("app:v1"))
			Expect(main["env"]).To(HaveLen(2))
			Expect(got.GetOwnerReferences()).To(BeEmpty())
			Expect(got.GetLabels()).NotTo(HaveKey(managedByLabel))

			managers := map[string]bool{}
			for _, mf := range got.GetManagedFields() {
				managers[mf.Manager] = true
			}
			Expect(managers).To(HaveKey(targetFieldManager))
			Expect(managers).To(HaveKey("argocd"))

			By("removing the injected fields, but not the object, on deletion")
			Expect(k8sClient.Delete(ctx, getVS(vs.Name))).To(Succeed())
			reconcile(vs.Name)
			got = getTestApp(app.GetName())
			spec = got.Object["spec"].(map[string]interface{})
			cfg = spec["config"].(map[string]interface{})
			Expect(cfg).NotTo(HaveKey("metrics.reporter.dghttp.apikey"))
			Expect(cfg["existing"]).To(Equal("keep-me"))
			Expect(spec["containers"].([]interface{})[0].(map[string]interface{})["env"]).To(HaveLen(1))
		})

		It("re-applies when the template changes and drops fields no longer rendered", func() {
			vs := newVS(secretv1.Target{
				APIVersion: "test.digitalis.io/v1", Kind: "TestApp", Name: app.GetName(), Mode: secretv1.TargetModePatch,
				Template: "spec:\n  config:\n    a: {{ .v | quote }}\n    b: {{ .v | quote }}\n",
			}, map[string]string{"v": "1"})
			reconcile(vs.Name)
			reconcile(vs.Name)
			Expect(getTestApp(app.GetName()).Object["spec"].(map[string]interface{})["config"]).To(HaveKey("b"))

			cur := getVS(vs.Name)
			cur.Spec.Target.Template = "spec:\n  config:\n    a: {{ .v | quote }}\n"
			Expect(k8sClient.Update(ctx, cur)).To(Succeed())
			reconcile(vs.Name)
			cfg := getTestApp(app.GetName()).Object["spec"].(map[string]interface{})["config"].(map[string]interface{})
			Expect(cfg).To(HaveKey("a"))
			Expect(cfg).NotTo(HaveKey("b"))
			Expect(cfg["existing"]).To(Equal("keep-me"))
		})

		It("reports TargetNotFound when the object does not exist", func() {
			vs := newVS(secretv1.Target{
				APIVersion: "test.digitalis.io/v1", Kind: "TestApp", Name: "missing", Mode: secretv1.TargetModePatch,
				Template: "spec:\n  config:\n    a: {{ .v | quote }}\n",
			}, map[string]string{"v": "1"})
			reconcile(vs.Name)
			reconcile(vs.Name)
			Expect(readyReason(vs.Name)).To(Equal(secretv1.ReasonTargetNotFound))
			u := &unstructured.Unstructured{}
			u.SetGroupVersionKind(testAppGVK)
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "missing", Namespace: ns}, u)).NotTo(Succeed())
		})

		It("restores an owned field edited by hand once the TTL elapses", func() {
			vs := newVS(secretv1.Target{
				APIVersion: "test.digitalis.io/v1", Kind: "TestApp", Name: app.GetName(), Mode: secretv1.TargetModePatch,
				Template: "spec:\n  config:\n    key: {{ .v }}\n",
			}, map[string]string{"v": "wanted"})
			cur := getVS(vs.Name)
			cur.Spec.TTL = 1
			Expect(k8sClient.Update(ctx, cur)).To(Succeed())
			reconcile(vs.Name)
			reconcile(vs.Name)

			edited := getTestApp(app.GetName())
			edited.Object["spec"].(map[string]interface{})["config"].(map[string]interface{})["key"] = "tampered"
			Expect(k8sClient.Update(ctx, edited)).To(Succeed())

			// Within TTL: no apply, no backend read.
			reconcile(vs.Name)
			Expect(getTestApp(app.GetName()).Object["spec"].(map[string]interface{})["config"].(map[string]interface{})["key"]).To(Equal("tampered"))

			time.Sleep(1100 * time.Millisecond)
			reconcile(vs.Name)
			Expect(getTestApp(app.GetName()).Object["spec"].(map[string]interface{})["config"].(map[string]interface{})["key"]).To(Equal("wanted"))
		})
	})
})
