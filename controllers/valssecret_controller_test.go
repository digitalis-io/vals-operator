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
