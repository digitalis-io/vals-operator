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
	"os"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	secretv1 "digitalis.io/vals-operator/apis/digitalis.io/v1"
)

// The refresh-requested-at annotation reads the backend again on the next
// reconcile, within the TTL and without a spec change, once per request value.
var _ = Describe("ValsSecret refresh request", func() {
	const ns = "refresh"
	var (
		ctx      context.Context
		rec      *ValsSecretReconciler
		recorder *record.FakeRecorder
		backend  string
	)

	BeforeEach(func() {
		ctx = context.Background()
		recorder = record.NewFakeRecorder(100)
		rec = &ValsSecretReconciler{
			Client:               k8sClient,
			APIReader:            k8sClient,
			Log:                  logf.Log.WithName("test"),
			Ctx:                  ctx,
			ReconciliationPeriod: time.Hour,
			DefaultTTL:           time.Hour,
			RecordChanges:        true,
			Recorder:             recorder,
		}
		_ = k8sClient.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: ns}})
		// A file stands in for the backend: ref+file:// is read by vals on
		// every evaluation, and its content can change between reconciles.
		backend = filepath.Join(GinkgoT().TempDir(), "backend")
	})

	setBackend := func(value string) {
		ExpectWithOffset(1, os.WriteFile(backend, []byte(value), 0o600)).To(Succeed())
	}

	reconcileVS := func(name string) ctrl.Result {
		res, err := rec.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: name, Namespace: ns}})
		ExpectWithOffset(1, err).NotTo(HaveOccurred())
		return res
	}

	getVS := func(name string) *secretv1.ValsSecret {
		vs := &secretv1.ValsSecret{}
		ExpectWithOffset(1, k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: ns}, vs)).To(Succeed())
		return vs
	}

	getSecret := func(name string) *corev1.Secret {
		s := &corev1.Secret{}
		ExpectWithOffset(1, k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: ns}, s)).To(Succeed())
		return s
	}

	newVS := func(name string, rollout []secretv1.RolloutTarget) {
		vs := &secretv1.ValsSecret{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
			Spec: secretv1.ValsSecretSpec{
				TTL:     3600,
				Data:    map[string]secretv1.DataSource{"a": {Ref: "ref+file://" + backend}},
				Rollout: rollout,
			},
		}
		ExpectWithOffset(1, k8sClient.Create(ctx, vs)).To(Succeed())
	}

	requestRefresh := func(name, value string) {
		vs := getVS(name)
		if vs.Annotations == nil {
			vs.Annotations = map[string]string{}
		}
		vs.Annotations[refreshRequestedAtAnnotation] = value
		ExpectWithOffset(1, k8sClient.Update(ctx, vs)).To(Succeed())
	}

	It("reads the backend once per request within the TTL", func() {
		const name = "vs-refresh"
		setBackend("v1")
		newVS(name, nil)
		reconcileVS(name)
		Expect(getSecret(name).Data).To(HaveKeyWithValue("a", []byte("v1")))

		setBackend("v2")
		reconcileVS(name)
		Expect(getSecret(name).Data).To(HaveKeyWithValue("a", []byte("v1")), "within the TTL without a request")

		requestRefresh(name, "2026-11-24T10:00:00Z")
		Expect(reconcileVS(name).RequeueAfter).To(Equal(rec.ReconciliationPeriod))
		s := getSecret(name)
		Expect(s.Data).To(HaveKeyWithValue("a", []byte("v2")))
		Expect(s.Annotations).NotTo(HaveKey(refreshRequestedAtAnnotation), "the request is not copied to the Secret")
		Expect(getVS(name).Status.LastHandledRefreshAt).To(Equal("2026-11-24T10:00:00Z"))

		setBackend("v3")
		reconcileVS(name)
		Expect(getSecret(name).Data).To(HaveKeyWithValue("a", []byte("v2")), "a handled request is not read again")

		requestRefresh(name, "2026-11-24T10:05:00Z")
		reconcileVS(name)
		Expect(getSecret(name).Data).To(HaveKeyWithValue("a", []byte("v3")))
		Expect(getVS(name).Status.LastHandledRefreshAt).To(Equal("2026-11-24T10:05:00Z"))
	})

	It("records a request that leaves the data unchanged without rewriting the Secret", func() {
		const name = "vs-refresh-unchanged"
		labels := map[string]string{"app": name}
		dep := &appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
			Spec: appsv1.DeploymentSpec{
				Selector: &metav1.LabelSelector{MatchLabels: labels},
				Template: corev1.PodTemplateSpec{
					ObjectMeta: metav1.ObjectMeta{Labels: labels},
					Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "app", Image: "busybox"}}},
				},
			},
		}
		Expect(k8sClient.Create(ctx, dep)).To(Succeed())
		setBackend("v1")
		newVS(name, []secretv1.RolloutTarget{{Kind: "Deployment", Name: name}})

		reconcileVS(name)
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: ns}, dep)).To(Succeed())
		delete(dep.Spec.Template.Annotations, restartedAnnotation)
		Expect(k8sClient.Update(ctx, dep)).To(Succeed())
		rv := getSecret(name).ResourceVersion

		requestRefresh(name, "first")
		reconcileVS(name)
		Expect(getVS(name).Status.LastHandledRefreshAt).To(Equal("first"))
		Expect(getSecret(name).ResourceVersion).To(Equal(rv), "the data did not change, so the Secret is not written")
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: ns}, dep)).To(Succeed())
		Expect(dep.Spec.Template.Annotations).NotTo(HaveKey(restartedAnnotation), "nothing is restarted")
	})

	It("keeps a failed request pending and retries it with errorBackoff", func() {
		const name = "vs-refresh-failure"
		setBackend("v1")
		newVS(name, nil)
		reconcileVS(name)

		Expect(os.Remove(backend)).To(Succeed())
		requestRefresh(name, "retry-me")
		res := reconcileVS(name)
		Expect(res.RequeueAfter).To(BeNumerically(">", 0), "a failed read must be requeued")
		Expect(res.RequeueAfter).To(BeNumerically("<", rec.ReconciliationPeriod), "the requeue must come from errorBackoff")
		Expect(getVS(name).Status.LastHandledRefreshAt).To(BeEmpty(), "a failed read does not handle the request")
		Expect(getSecret(name).Data).To(HaveKeyWithValue("a", []byte("v1")))

		setBackend("v2")
		Expect(reconcileVS(name).RequeueAfter).To(Equal(rec.ReconciliationPeriod))
		Expect(getSecret(name).Data).To(HaveKeyWithValue("a", []byte("v2")))
		Expect(getVS(name).Status.LastHandledRefreshAt).To(Equal("retry-me"))
		Expect(rec.errorCounts).NotTo(HaveKey(ns + "/" + name))
	})

	It("does not requeue a request whose reference the backend authoriser denies", func() {
		const name = "vs-refresh-denied"
		setBackend("v1")
		newVS(name, nil)
		reconcileVS(name)
		rv := getSecret(name).ResourceVersion

		authz, err := NewBackendAuthorizer("other-namespace=ref+vault://secret")
		Expect(err).NotTo(HaveOccurred())
		rec.BackendAuthorizer = authz
		setBackend("v2")
		requestRefresh(name, "denied")

		for range 2 {
			Expect(reconcileVS(name)).To(Equal(ctrl.Result{}), "a denied reference is not requeued")
		}
		var events []string
		for len(recorder.Events) > 0 {
			events = append(events, <-recorder.Events)
		}
		Expect(events).To(ContainElement(ContainSubstring("Denied")))
		Expect(getSecret(name).ResourceVersion).To(Equal(rv), "nothing is read or written")
		Expect(getVS(name).Status.LastHandledRefreshAt).To(BeEmpty())
	})

	It("reads the backend once per request when the CRD drops the status field", func() {
		rec.Client = prunedStatusClient{k8sClient}

		const name = "vs-refresh-old-crd"
		setBackend("v1")
		newVS(name, nil)
		reconcileVS(name)

		setBackend("v2")
		requestRefresh(name, "first")
		reconcileVS(name)
		Expect(getSecret(name).Data).To(HaveKeyWithValue("a", []byte("v2")))
		Expect(getVS(name).Status.LastHandledRefreshAt).To(BeEmpty(), "the field is dropped")

		setBackend("v3")
		for range 2 {
			Expect(reconcileVS(name).RequeueAfter).To(Equal(rec.ReconciliationPeriod))
		}
		Expect(getSecret(name).Data).To(HaveKeyWithValue("a", []byte("v2")), "the handled request is not read again")

		requestRefresh(name, "second")
		reconcileVS(name)
		Expect(getSecret(name).Data).To(HaveKeyWithValue("a", []byte("v3")), "a new request is still handled")
	})

	It("re-applies a custom target once per request when the CRD drops the status field", func() {
		policy, err := NewTargetPolicy(true, true, "configmaps")
		Expect(err).NotTo(HaveOccurred())
		rec.TargetPolicy = policy
		rec.Client = prunedStatusClient{k8sClient}

		const name = "vs-refresh-target-old-crd"
		setBackend("v1")
		vs := &secretv1.ValsSecret{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
			Spec: secretv1.ValsSecretSpec{
				TTL:  3600,
				Data: map[string]secretv1.DataSource{"a": {Ref: "ref+file://" + backend}},
				Target: &secretv1.Target{
					APIVersion: "v1", Kind: "ConfigMap", Name: name,
					Template: "data:\n  a: {{ .a | quote }}\n",
				},
			},
		}
		Expect(k8sClient.Create(ctx, vs)).To(Succeed())
		reconcileVS(name) // adds the finalizer
		reconcileVS(name)

		cm := &corev1.ConfigMap{}
		getCM := func() map[string]string {
			ExpectWithOffset(1, k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: ns}, cm)).To(Succeed())
			return cm.Data
		}

		setBackend("v2")
		requestRefresh(name, "first")
		reconcileVS(name)
		Expect(getCM()).To(HaveKeyWithValue("a", "v2"))

		setBackend("v3")
		reconcileVS(name)
		Expect(getCM()).To(HaveKeyWithValue("a", "v2"), "the handled request is not read again")
	})

	It("re-applies a custom target on a request within the TTL", func() {
		policy, err := NewTargetPolicy(true, true, "configmaps")
		Expect(err).NotTo(HaveOccurred())
		rec.TargetPolicy = policy

		const name = "vs-refresh-target"
		setBackend("v1")
		vs := &secretv1.ValsSecret{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
			Spec: secretv1.ValsSecretSpec{
				TTL:  3600,
				Data: map[string]secretv1.DataSource{"a": {Ref: "ref+file://" + backend}},
				Target: &secretv1.Target{
					APIVersion: "v1", Kind: "ConfigMap", Name: name,
					Template: "data:\n  a: {{ .a | quote }}\n",
				},
			},
		}
		Expect(k8sClient.Create(ctx, vs)).To(Succeed())
		reconcileVS(name) // adds the finalizer
		reconcileVS(name)

		cm := &corev1.ConfigMap{}
		getCM := func() map[string]string {
			ExpectWithOffset(1, k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: ns}, cm)).To(Succeed())
			return cm.Data
		}
		Expect(getCM()).To(HaveKeyWithValue("a", "v1"))

		setBackend("v2")
		reconcileVS(name)
		Expect(getCM()).To(HaveKeyWithValue("a", "v1"), "within the TTL without a request")

		requestRefresh(name, "target")
		reconcileVS(name)
		Expect(getCM()).To(HaveKeyWithValue("a", "v2"))
		Expect(getVS(name).Status.LastHandledRefreshAt).To(Equal("target"))

		setBackend("v3")
		reconcileVS(name)
		Expect(getCM()).To(HaveKeyWithValue("a", "v2"), "a handled request is not read again")
	})
})

// prunedStatusClient stands in for an API server whose ValsSecret CRD predates
// status.lastHandledRefreshAt: the field is dropped from every status write.
type prunedStatusClient struct{ client.Client }

func (c prunedStatusClient) Status() client.SubResourceWriter {
	return prunedStatusWriter{c.Client.Status()}
}

type prunedStatusWriter struct{ client.SubResourceWriter }

func prune(obj client.Object) {
	if vs, ok := obj.(*secretv1.ValsSecret); ok {
		vs.Status.LastHandledRefreshAt = ""
	}
}

func (w prunedStatusWriter) Update(ctx context.Context, obj client.Object, opts ...client.SubResourceUpdateOption) error {
	prune(obj)
	return w.SubResourceWriter.Update(ctx, obj, opts...)
}

func (w prunedStatusWriter) Patch(ctx context.Context, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
	prune(obj)
	return w.SubResourceWriter.Patch(ctx, obj, patch, opts...)
}

var _ = Describe("refreshRequestedPredicate", func() {
	withAnnotations := func(a map[string]string) *secretv1.ValsSecret {
		return &secretv1.ValsSecret{ObjectMeta: metav1.ObjectMeta{Annotations: a}}
	}
	p := refreshRequestedPredicate()

	It("passes an update that changes the request", func() {
		Expect(p.Update(event.UpdateEvent{
			ObjectOld: withAnnotations(nil),
			ObjectNew: withAnnotations(map[string]string{refreshRequestedAtAnnotation: "now"}),
		})).To(BeTrue())
		Expect(p.Update(event.UpdateEvent{
			ObjectOld: withAnnotations(map[string]string{refreshRequestedAtAnnotation: "then"}),
			ObjectNew: withAnnotations(map[string]string{refreshRequestedAtAnnotation: "now"}),
		})).To(BeTrue())
	})

	It("filters an update of any other annotation", func() {
		Expect(p.Update(event.UpdateEvent{
			ObjectOld: withAnnotations(map[string]string{refreshRequestedAtAnnotation: "now"}),
			ObjectNew: withAnnotations(map[string]string{refreshRequestedAtAnnotation: "now", "other": "x"}),
		})).To(BeFalse())
	})

	It("leaves create, delete and generic events to the generation predicate", func() {
		obj := withAnnotations(map[string]string{refreshRequestedAtAnnotation: "now"})
		Expect(p.Create(event.CreateEvent{Object: obj})).To(BeFalse())
		Expect(p.Delete(event.DeleteEvent{Object: obj})).To(BeFalse())
		Expect(p.Generic(event.GenericEvent{Object: obj})).To(BeFalse())
	})
})
