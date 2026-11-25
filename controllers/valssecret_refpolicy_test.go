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
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	secretv1 "digitalis.io/vals-operator/apis/digitalis.io/v1"
)

var _ = Describe("ValsSecret reference policy", func() {
	const ns = "ref-policy"
	var (
		ctx      context.Context
		rec      *ValsSecretReconciler
		recorder *record.FakeRecorder
	)

	BeforeEach(func() {
		ctx = context.Background()
		recorder = record.NewFakeRecorder(100)
		authz, err := NewBackendAuthorizer(ns + "=ref+vault://secret/team-a,ref+k8s://" + ns)
		Expect(err).NotTo(HaveOccurred())
		rec = &ValsSecretReconciler{
			Client:               k8sClient,
			APIReader:            k8sClient,
			Log:                  logf.Log.WithName("test"),
			Ctx:                  ctx,
			ReconciliationPeriod: time.Hour,
			DefaultTTL:           time.Hour,
			RecordChanges:        true,
			Recorder:             recorder,
			BackendAuthorizer:    authz,
		}
		_ = k8sClient.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: ns}})
	})

	reconcile := func(name, ref string) {
		vs := &secretv1.ValsSecret{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
			Spec:       secretv1.ValsSecretSpec{Data: map[string]secretv1.DataSource{"v": {Ref: ref}}},
		}
		ExpectWithOffset(1, k8sClient.Create(ctx, vs)).To(Succeed())
		_, err := rec.Reconcile(ctx, ctrl.Request{NamespacedName: types.NamespacedName{Name: name, Namespace: ns}})
		ExpectWithOffset(1, err).NotTo(HaveOccurred())
	}

	expectDenied := func(name string) {
		err := k8sClient.Get(ctx, types.NamespacedName{Name: name, Namespace: ns}, &corev1.Secret{})
		ExpectWithOffset(1, apierrors.IsNotFound(err)).To(BeTrue(), "no Secret may be written for a denied reference")
		ExpectWithOffset(1, recorder.Events).To(Receive(ContainSubstring("Denied")))
	}

	It("denies a second reference placed after an allowed one", func() {
		reconcile("vs-embedded-file",
			"ref+vault://secret/team-a/app#key+ ref+file:///var/run/secrets/kubernetes.io/serviceaccount/token+")
		expectDenied("vs-embedded-file")
	})

	It("denies ref+file even with no allowlist configured", func() {
		rec.BackendAuthorizer = nil
		reconcile("vs-file", "ref+file:///var/run/secrets/kubernetes.io/serviceaccount/token")
		expectDenied("vs-file")
	})

	It("denies a query parameter that redirects the backend read", func() {
		reconcile("vs-address", "ref+vault://secret/team-a/app?address=https://attacker.example#key")
		expectDenied("vs-address")
	})

	It("denies a k8s reference spelled to reach vals' own k8s provider", func() {
		rec.BackendAuthorizer = nil
		rec.DisableNamespaceSync = true
		reconcile("vs-k8s-case", "ref+K8S://v1/Secret/kube-system/admin/token")
		expectDenied("vs-k8s-case")
	})

	It("copies a ref+k8s value verbatim instead of expanding it", func() {
		src := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "src", Namespace: ns},
			Data:       map[string][]byte{"k": []byte("ref+echo://expanded")},
		}
		Expect(k8sClient.Create(ctx, src)).To(Succeed())

		reconcile("vs-k8s", "ref+k8s://"+ns+"/src#k")

		got := &corev1.Secret{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "vs-k8s", Namespace: ns}, got)).To(Succeed())
		Expect(got.Data).To(HaveKeyWithValue("v", []byte("ref+echo://expanded")))
	})
})
