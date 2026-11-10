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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	secretv1 "digitalis.io/vals-operator/apis/digitalis.io/v1"
)

// A failed write of the target Secret must go through errorBackoff, like the
// other failure paths, so the ValsSecret is requeued and retried once the
// cause (admission webhook outage, API server error, ...) is gone.
var _ = Describe("ValsSecret Secret write failure", func() {
	const (
		ns   = "upsert-failure"
		name = "vs-upsert-failure"
	)
	var (
		ctx context.Context
		rec *ValsSecretReconciler
	)

	key := types.NamespacedName{Name: name, Namespace: ns}

	BeforeEach(func() {
		ctx = context.Background()
		rec = &ValsSecretReconciler{
			Client:               k8sClient,
			APIReader:            k8sClient,
			Log:                  logf.Log.WithName("test"),
			Ctx:                  ctx,
			ReconciliationPeriod: time.Hour,
			DefaultTTL:           time.Hour,
			RecordChanges:        true,
			Recorder:             record.NewFakeRecorder(100),
		}
		_ = k8sClient.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: ns}})
	})

	It("requeues with errorBackoff when upsertSecret fails, then recovers", func() {
		// An immutable Secret with stale data and no last-updated annotation:
		// the reconcile sees it as expired and the API server rejects the
		// update of its data, so upsertSecret returns an error.
		immutable := true
		stale := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
			Immutable:  &immutable,
			Data:       map[string][]byte{"password": []byte("old")},
		}
		Expect(k8sClient.Create(ctx, stale)).To(Succeed())

		vs := &secretv1.ValsSecret{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
			Spec: secretv1.ValsSecretSpec{
				// No ref+ prefix: vals.Eval passes the value through, no backend needed.
				Data: map[string]secretv1.DataSource{"password": {Ref: "new"}},
			},
		}
		Expect(k8sClient.Create(ctx, vs)).To(Succeed())

		res, err := rec.Reconcile(ctx, ctrl.Request{NamespacedName: key})
		Expect(err).NotTo(HaveOccurred())
		Expect(res.RequeueAfter).To(BeNumerically(">", 0), "a failed Secret write must be requeued")
		Expect(res.RequeueAfter).To(BeNumerically("<", rec.ReconciliationPeriod), "the requeue must come from errorBackoff")
		Expect(rec.errorCounts).To(HaveKeyWithValue(ns+"/"+name, 1))

		// Remove the cause: the next reconcile writes the Secret and clears the error count.
		Expect(k8sClient.Delete(ctx, stale)).To(Succeed())
		Eventually(func() bool {
			return k8sClient.Get(ctx, key, &corev1.Secret{}) != nil
		}).Should(BeTrue())

		res, err = rec.Reconcile(ctx, ctrl.Request{NamespacedName: key})
		Expect(err).NotTo(HaveOccurred())
		Expect(res.RequeueAfter).To(Equal(rec.ReconciliationPeriod))
		Expect(rec.errorCounts).NotTo(HaveKey(ns + "/" + name))

		got := &corev1.Secret{}
		Expect(k8sClient.Get(ctx, key, got)).To(Succeed())
		Expect(got.Data).To(HaveKeyWithValue("password", []byte("new")))
	})
})

// A change to a ValsSecret's spec must reach its Secret on the next reconcile,
// not when the Secret's TTL runs out. An unchanged ValsSecret still waits for
// the TTL.
var _ = Describe("ValsSecret spec change before the TTL expires", func() {
	const ns = "spec-change"
	var (
		ctx context.Context
		rec *ValsSecretReconciler
	)

	BeforeEach(func() {
		ctx = context.Background()
		rec = &ValsSecretReconciler{
			Client:               k8sClient,
			APIReader:            k8sClient,
			Log:                  logf.Log.WithName("test"),
			Ctx:                  ctx,
			ReconciliationPeriod: time.Hour,
			DefaultTTL:           time.Hour,
			RecordChanges:        true,
			Recorder:             record.NewFakeRecorder(100),
		}
		_ = k8sClient.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: ns}})
	})

	reconcileVS := func(name string) {
		res, err := rec.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: name, Namespace: ns}})
		ExpectWithOffset(1, err).NotTo(HaveOccurred())
		ExpectWithOffset(1, res.RequeueAfter).To(Equal(rec.ReconciliationPeriod))
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

	// No ref+ prefix: vals.Eval passes the value through, no backend needed.
	newVS := func(name string, data map[string]string, rollout []secretv1.RolloutTarget) {
		d := map[string]secretv1.DataSource{}
		for k, v := range data {
			d[k] = secretv1.DataSource{Ref: v}
		}
		vs := &secretv1.ValsSecret{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
			Spec:       secretv1.ValsSecretSpec{TTL: 3600, Data: d, Rollout: rollout},
		}
		ExpectWithOffset(1, k8sClient.Create(ctx, vs)).To(Succeed())
	}

	expectGenerationRecorded := func(name string) {
		vs := getVS(name)
		ExpectWithOffset(1, vs.Status.SecretGeneration).To(Equal(vs.Generation))
	}

	It("adds a new key on the first reconcile after the change", func() {
		const name = "vs-new-key"
		newVS(name, map[string]string{"a": "1"}, nil)
		reconcileVS(name)
		Expect(getSecret(name).Data).To(Equal(map[string][]byte{"a": []byte("1")}))

		vs := getVS(name)
		vs.Spec.Data["b"] = secretv1.DataSource{Ref: "2"}
		Expect(k8sClient.Update(ctx, vs)).To(Succeed())

		reconcileVS(name)
		Expect(getSecret(name).Data).To(Equal(map[string][]byte{"a": []byte("1"), "b": []byte("2")}))
		expectGenerationRecorded(name)
	})

	It("does not re-read an unchanged ValsSecret within the TTL", func() {
		const name = "vs-unchanged"
		newVS(name, map[string]string{"a": "1"}, nil)
		reconcileVS(name)

		// Edit the Secret behind the operator's back: a backend read would put "1" back.
		s := getSecret(name)
		s.Data["a"] = []byte("edited")
		Expect(k8sClient.Update(ctx, s)).To(Succeed())

		reconcileVS(name)
		Expect(getSecret(name).Data).To(HaveKeyWithValue("a", []byte("edited")))
	})

	It("records a spec change that leaves the data unchanged without rewriting the Secret", func() {
		const name = "vs-ttl-change"
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
		newVS(name, map[string]string{"a": "1"}, []secretv1.RolloutTarget{{Kind: "Deployment", Name: name}})

		// Creating the Secret restarts the Deployment.
		reconcileVS(name)
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: ns}, dep)).To(Succeed())
		Expect(dep.Spec.Template.Annotations).To(HaveKey(restartedAnnotation))
		delete(dep.Spec.Template.Annotations, restartedAnnotation)
		Expect(k8sClient.Update(ctx, dep)).To(Succeed())
		rv := getSecret(name).ResourceVersion

		vs := getVS(name)
		vs.Spec.TTL = 7200
		Expect(k8sClient.Update(ctx, vs)).To(Succeed())

		reconcileVS(name)
		expectGenerationRecorded(name)
		Expect(getSecret(name).ResourceVersion).To(Equal(rv), "the data did not change, so the Secret is not written")
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: ns}, dep)).To(Succeed())
		Expect(dep.Spec.Template.Annotations).NotTo(HaveKey(restartedAnnotation), "nothing is restarted")
	})

	It("keeps the TTL rule until the generation is first recorded", func() {
		// A ValsSecret last applied by an earlier version has no
		// status.secretGeneration. It waits for the TTL once, so an operator
		// upgrade does not re-read every backend at once.
		const name = "vs-upgrade"
		old := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:        name,
				Namespace:   ns,
				Annotations: map[string]string{lastUpdatedAnnotation: time.Now().UTC().Format(timeLayout)},
			},
			Data: map[string][]byte{"a": []byte("old")},
		}
		Expect(k8sClient.Create(ctx, old)).To(Succeed())
		newVS(name, map[string]string{"a": "new"}, nil)

		reconcileVS(name)
		Expect(getSecret(name).Data).To(HaveKeyWithValue("a", []byte("old")))
		Expect(getVS(name).Status.SecretGeneration).To(BeZero())

		expired := getSecret(name)
		expired.Annotations[lastUpdatedAnnotation] = time.Now().Add(-2 * time.Hour).UTC().Format(timeLayout)
		Expect(k8sClient.Update(ctx, expired)).To(Succeed())

		reconcileVS(name)
		Expect(getSecret(name).Data).To(HaveKeyWithValue("a", []byte("new")))
		expectGenerationRecorded(name)
	})
})
