/*
Copyright 2021 Digitalis.IO.

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
	b64 "encoding/base64"
	"fmt"
	"math"
	"math/rand"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/go-logr/logr"
	"github.com/variantdev/vals"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/record"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	secretv1 "digitalis.io/vals-operator/api/v1"
)

const (
	timeLayout                 = "2006-01-02T15.04.05Z"
	lastUpdatedAnnotation      = "vals-operator.digitalis.io/last-updated"
	recordingEnabledAnnotation = "vals-operator.digitalis.io/record"
	managedByLabel             = "app.kubernetes.io/managed-by"
	k8sSecretPrefix            = "ref+k8s://"
)

// ValsSecretReconciler reconciles a ValsSecret object
type ValsSecretReconciler struct {
	client.Client
	Log                  logr.Logger
	Ctx                  context.Context
	APIReader            client.Reader
	ReconciliationPeriod time.Duration
	ExcludeNamespaces    map[string]bool
	RecordChanges        bool
	Recorder             record.EventRecorder
	SecretTTL            time.Duration

	errorCounts map[string]int
	errMu       sync.Mutex
}

//+kubebuilder:rbac:groups=digitalis.io,resources=valssecrets,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=digitalis.io,resources=valssecrets/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=digitalis.io,resources=valssecrets/finalizers,verbs=update

func (r *ValsSecretReconciler) getSecret(secretName string, namespace string) (*corev1.Secret, error) {
	var secret corev1.Secret

	err := r.Get(r.Ctx, client.ObjectKey{
		Namespace: namespace,
		Name:      secretName,
	}, &secret)
	if err != nil {
		return nil, err
	}

	return &secret, nil
}

// shouldExclude will return true if the secretDefinition is in an excluded namespace
func (r *ValsSecretReconciler) shouldExclude(sDefNamespace string) bool {
	if len(r.ExcludeNamespaces) > 0 {
		return r.ExcludeNamespaces[sDefNamespace]
	}
	return false
}

// upsertSecret will create or update a secret
func (r *ValsSecretReconciler) upsertSecret(sDef *secretv1.ValsSecret, data map[string][]byte) error {
	log := ctrl.Log.WithName("vals-operator")
	var secretName string
	if sDef.Spec.Name != "" {
		secretName = sDef.Spec.Name
	} else {
		secretName = sDef.Name
	}
	secret, err := r.getSecret(secretName, sDef.GetNamespace())
	if err != nil {
		if client.IgnoreNotFound(err) != nil {
			return err
		}
		// secret not found, create a new empty one
		secret = &corev1.Secret{}
	}

	if r.secretNeedsUpdate(sDef, secret, data) {
		if sDef.Spec.Name != "" {
			secret.Name = sDef.Spec.Name
		} else {
			secret.Name = sDef.Name
		}
		secret.Namespace = sDef.Namespace
		secret.Data = data
		secret.Type = corev1.SecretType(sDef.Spec.Type)
		if secret.ObjectMeta.Labels == nil {
			secret.ObjectMeta.Labels = make(map[string]string)
		}
		if secret.ObjectMeta.Annotations == nil {
			secret.ObjectMeta.Annotations = make(map[string]string)
		}
		// Replace all labels and annotations on the secret
		secret.ObjectMeta.Labels = make(map[string]string)
		mergeMap(secret.ObjectMeta.Labels, sDef.ObjectMeta.Labels)
		secret.ObjectMeta.Labels[managedByLabel] = "vals-operator"
		secret.ObjectMeta.Annotations = make(map[string]string)
		mergeMap(secret.ObjectMeta.Annotations, sDef.ObjectMeta.Annotations)
		secret.ObjectMeta.Annotations[lastUpdatedAnnotation] = time.Now().UTC().Format(timeLayout)
	} else {
		/* Secret already up to date */
		return nil
	}

	delete(secret.ObjectMeta.Annotations, corev1.LastAppliedConfigAnnotation)
	secret.ResourceVersion = ""

	err = r.Create(r.Ctx, secret)
	if errors.IsAlreadyExists(err) {
		err = r.Update(r.Ctx, secret)
	}
	if err == nil && r.recordingEnabled(sDef) {
		r.Recorder.Event(sDef, corev1.EventTypeNormal, "Updated", "Secret created or updated")
	} else if err != nil && r.recordingEnabled(sDef) {
		msg := fmt.Sprintf("Secret %s not saved %v", secret.Name, err)
		r.Recorder.Event(sDef, corev1.EventTypeNormal, "Failed", msg)
	}

	log.Info("Updated secret", "name", secretName)

	return err
}

// secretNeedsUpdate Checks if the secret data or definition has changed from the current secret
func (r *ValsSecretReconciler) secretNeedsUpdate(sDef *secretv1.ValsSecret, secret *corev1.Secret, newData map[string][]byte) bool {
	return secret == nil || secret.Name == "" ||
		!r.byteMapsMatch(secret.Data, newData) ||
		!r.stringMapsMatch(
			secret.ObjectMeta.Annotations,
			sDef.ObjectMeta.Annotations,
			[]string{"kubectl.kubernetes.io/last-applied-configuration", "vals-operator.digitalis.io/last-updated"}) ||
		!r.stringMapsMatch(
			secret.ObjectMeta.Labels,
			sDef.ObjectMeta.Labels,
			[]string{"app.kubernetes.io/managed-by"})
}

// stringMapsMatch returns true if the provided maps contain the same keys and values, otherwise false
func (r *ValsSecretReconciler) stringMapsMatch(m1, m2 map[string]string, ignoreKeys []string) bool {
	// if both are empty then they must match
	if (m1 == nil || len(m1) == 0) && (m2 == nil || len(m2) == 0) {
		return true
	}

	ignoreMap := make(map[string]struct{})
	for _, k := range ignoreKeys {
		ignoreMap[k] = struct{}{}
	}

	for k, v := range m1 {
		if _, ignore := ignoreMap[k]; ignore {
			continue
		}
		v2, ok := m2[k]
		if !ok || v2 != v {
			return false
		}
	}
	for k, v := range m2 {
		if _, ignore := ignoreMap[k]; ignore {
			continue
		}
		v1, ok := m1[k]
		if !ok || v1 != v {
			return false
		}
	}
	return true
}

// byteMapsMatch is like stringMapsMatch but for maps of byte arrays
func (r *ValsSecretReconciler) byteMapsMatch(m1, m2 map[string][]byte) bool {
	if len(m1) != len(m2) {
		return false
	}
	for k, v := range m1 {
		v2, ok := m2[k]
		if !ok {
			return false
		}
		if len(v2) != len(v) {
			return false
		}
		for i, c := range v {
			if v2[i] != c {
				return false
			}
		}
	}
	return true
}

// recordingEnabled check if we want the event recorded
func (r *ValsSecretReconciler) recordingEnabled(sDef *secretv1.ValsSecret) bool {
	recordAnn := sDef.GetAnnotations()[recordingEnabledAnnotation]
	if recordAnn != "" && recordAnn != "true" {
		return false
	}

	return r.RecordChanges
}

// deleteSecret will delete a secret given its namespace and name
func (r *ValsSecretReconciler) deleteSecret(ctx context.Context, sDef *secretv1.ValsSecret) error {
	var name string

	if sDef.Spec.Name != "" {
		name = sDef.Spec.Name
	} else {
		name = sDef.Name
	}
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: sDef.Namespace,
			Name:      name,
		},
	}

	return client.IgnoreNotFound(r.Delete(ctx, secret))
}

func (r *ValsSecretReconciler) getKeyFromK8sSecret(secretRef string) (string, error) {
	re := regexp.MustCompile(`ref\+k8s://(?P<namespace>\S+)/(?P<secretName>\S+)#(?P<key>\S+)`)
	matchMap := FindAllGroups(re, secretRef)

	if !k8sSecretFound(matchMap) {
		return "", fmt.Errorf("The ref+k8s secret '%s' did not match the regular expression for ref+k8s://namespace/secret-name#key", secretRef)
	}
	secret, err := r.getSecret(matchMap["secretName"], matchMap["namespace"])
	if err != nil {
		return "", err
	}

	v := string(secret.Data[matchMap["key"]])

	return v, nil
}

func (r *ValsSecretReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var secret secretv1.ValsSecret

	log := ctrl.Log.WithName("vals-operator")

	err := r.Get(ctx, req.NamespacedName, &secret)
	if err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if r.shouldExclude(secret.Namespace) {
		log.Info("Namespace requested is in the exclusion list, ignoring", "excluded_namespaces", r.ExcludeNamespaces)
		return ctrl.Result{}, nil
	}
	//! [finalizer]
	valsSecretFinalizerName := "vals.digitalis.io/finalizer"
	if secret.ObjectMeta.DeletionTimestamp.IsZero() {
		if !containsString(secret.GetFinalizers(), valsSecretFinalizerName) {
			secret.SetFinalizers(append(secret.GetFinalizers(), valsSecretFinalizerName))
			if err := r.Update(context.Background(), &secret); err != nil {
				return ctrl.Result{}, err
			}
		}
	} else {
		// The object is being deleted
		r.clearErrorCount(&secret)
		if containsString(secret.GetFinalizers(), valsSecretFinalizerName) {
			// our finalizer is present, so lets handle any external dependency
			if err := r.deleteSecret(ctx, &secret); err != nil {
				log.Error(err, "Error deleting from Vals-Secret")
				return ctrl.Result{}, client.IgnoreNotFound(err)
			}

			// remove our finalizer from the list and update it.
			secret.SetFinalizers(removeString(secret.GetFinalizers(), valsSecretFinalizerName))
			if err := r.Update(context.Background(), &secret); err != nil {
				return ctrl.Result{}, err
			}
		}

		// Stop reconciliation as the item is being deleted
		return ctrl.Result{}, nil
	}
	//! [finalizer]

	var secretName string
	if secret.Spec.Name != "" {
		secretName = secret.Spec.Name
	} else {
		secretName = secret.Name
	}
	currentSecret, err := r.getSecret(secretName, secret.GetNamespace())
	if client.IgnoreNotFound(err) != nil {
		return ctrl.Result{}, err
	}

	if currentSecret != nil && currentSecret.Name != "" && !hasSecretExpired(secret, currentSecret) {
		return ctrl.Result{RequeueAfter: r.ReconciliationPeriod}, nil
	}

	secretYaml := make(map[string]interface{})
	for k, v := range secret.Spec.Data {
		if strings.HasPrefix(v.Ref, k8sSecretPrefix) {
			secretYaml[k], err = r.getKeyFromK8sSecret(v.Ref)
			if err != nil {
				if r.recordingEnabled(&secret) {
					msg := fmt.Sprintf("Failed to get key from existing k8s secret %v", err)
					r.Recorder.Event(&secret, corev1.EventTypeNormal, "Failed", msg)
				}
				return r.errorBackoff(&secret)
			}
		} else {
			secretYaml[k] = v.Ref
		}
	}
	valsRendered, err := vals.Eval(secretYaml, vals.Options{})
	if err != nil {
		log.Error(err, "Failed to get secrets from secrets store", "name", secret.Name)
		if r.recordingEnabled(&secret) {
			msg := fmt.Sprintf("Failed to get secrets from secrets store %v", err)
			r.Recorder.Event(&secret, corev1.EventTypeNormal, "Failed", msg)
		}

		return r.errorBackoff(&secret)
	}

	data := make(map[string][]byte)
	for k, v := range valsRendered {
		if secret.Spec.Data[k].Encoding == "base64" && !strings.HasPrefix(secret.Spec.Data[k].Ref, k8sSecretPrefix) {
			sDec, err := b64.StdEncoding.DecodeString(v.(string))
			if err != nil {
				log.Error(err, "Cannot b64 decode secret. Please check encoding configuration. Requeuing.", "name", secret.Name)
				if r.recordingEnabled(&secret) {
					r.Recorder.Event(&secret, corev1.EventTypeNormal, "Failed", "Base64 decoding failed")
				}

				return r.errorBackoff(&secret)
			}
			data[k] = sDec
		} else {
			data[k] = []byte(v.(string))
		}
	}
	err = r.upsertSecret(&secret, data)
	if err != nil {
		log.Error(err, "Failed to create secret")
		return ctrl.Result{}, nil
	}

	log.WithValues("vals-operator", fmt.Sprintf("secret %s created or updated successfully", secret.Name))
	r.clearErrorCount(&secret)
	return ctrl.Result{RequeueAfter: r.ReconciliationPeriod}, nil
}

func mergeMap(dst map[string]string, srcMap map[string]string) {
	for k, v := range srcMap {
		dst[k] = v
	}
}

// Helper functions to check and remove string from a slice of strings.
func containsString(slice []string, s string) bool {
	for _, item := range slice {
		if item == s {
			return true
		}
	}
	return false
}

func (r *ValsSecretReconciler) hasSecretExpired(sDef secretv1.ValsSecret, secret *corev1.Secret) bool {
	/* if no TTL, apply a sensible default */
	if sDef.Spec.Ttl <= 0 {
		sDef.Spec.Ttl = int64(r.SecretTTL.Seconds())
	}

	lastUpdated := secret.GetAnnotations()[lastUpdatedAnnotation]

	if lastUpdated == "" {
		return true
	}

	now := time.Now().UTC()
	s, err := time.Parse(timeLayout, lastUpdated)
	if err != nil {
		return true
	}

	if int64(now.Sub(s).Seconds()) > sDef.Spec.Ttl {
		return true
	}

	return false
}

func removeString(slice []string, s string) (result []string) {
	for _, item := range slice {
		if item == s {
			continue
		}
		result = append(result, item)
	}
	return
}

// SetupWithManager sets up the controller with the Manager.
func (r *ValsSecretReconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.Recorder = mgr.GetEventRecorderFor("Secrets")

	return ctrl.NewControllerManagedBy(mgr).
		For(&secretv1.ValsSecret{}).
		Complete(r)
}

// errorBackoff Increments the error count annotation and uses it to calculate the backoff time
func (r *ValsSecretReconciler) errorBackoff(valsSecret *secretv1.ValsSecret) (ctrl.Result, error) {
	const maxBackoff = 120 * time.Second
	const minBackoff = 3 * time.Second
	const backoffFactor = 1.5
	// fraction of the backoff time to use as random jitter. This is applied + or - so a jitter of 0.1 allows the backoff time to be changed +/- 10%
	const jitterFraction = 0.1

	errCount := r.incErrorCount(valsSecret)

	// Calculate backoff time as minBackoff + (backoffFactor ^ errorCount)
	backoffTime := minBackoff + (time.Second * time.Duration(math.Round(math.Pow(backoffFactor, float64(errCount)))))
	if backoffTime < minBackoff {
		backoffTime = minBackoff
	} else if backoffTime > maxBackoff {
		backoffTime = maxBackoff
	}

	// Add jitter to backoff time (allow going past the maxBackoff with this as it will only be +/-10%)
	jitter := math.Round((rand.Float64() - 0.5) * float64(backoffTime) * (jitterFraction * 2))
	backoffTime += time.Duration(jitter)

	r.Log.Info(fmt.Sprintf("errorBackoff: %s  (jitter=%s)", backoffTime.String(), time.Duration(jitter).String()))
	err := r.Update(r.Ctx, valsSecret)
	if err != nil {
		r.Log.Error(err, "Error updating error count annotation")
	}
	return ctrl.Result{RequeueAfter: backoffTime}, nil
}

func (r *ValsSecretReconciler) incErrorCount(valsSecret *secretv1.ValsSecret) int {
	r.errMu.Lock()
	defer r.errMu.Unlock()
	errKey := fmt.Sprintf("%s/%s", valsSecret.Namespace, valsSecret.Name)
	if r.errorCounts == nil {
		r.errorCounts = make(map[string]int)
	}
	r.errorCounts[errKey]++
	return r.errorCounts[errKey]
}

func (r *ValsSecretReconciler) clearErrorCount(valsSecret *secretv1.ValsSecret) {
	r.errMu.Lock()
	defer r.errMu.Unlock()
	errKey := fmt.Sprintf("%s/%s", valsSecret.Namespace, valsSecret.Name)
	if len(r.errorCounts) < 1 {
		return
	}
	delete(r.errorCounts, errKey)
}

// FindAllGroups returns a map with each match group. The map key corresponds to the match group name.
// A nil return value indicates no matches.
func FindAllGroups(re *regexp.Regexp, s string) map[string]string {
	matches := re.FindStringSubmatch(s)
	subnames := re.SubexpNames()
	if matches == nil || len(matches) != len(subnames) {
		return nil
	}

	matchMap := map[string]string{}
	for i := 1; i < len(matches); i++ {
		matchMap[subnames[i]] = matches[i]
	}
	return matchMap
}

func k8sSecretFound(m map[string]string) bool {
	for _, k := range []string{"namespace", "secretName", "key"} {
		if _, ok := m[k]; !ok {
			return false
		}
	}
	return true
}
