/*
Copyright 2023 Digitalis.IO.

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
	b64 "encoding/base64"
	"fmt"
	"html/template"
	"math"
	"math/rand"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/go-logr/logr"
	"github.com/helmfile/vals"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/record"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	secretv1 "digitalis.io/vals-operator/apis/digitalis.io/v1"
	valsDb "digitalis.io/vals-operator/db"
	dbType "digitalis.io/vals-operator/db/types"
	"digitalis.io/vals-operator/utils"
	sprig "github.com/Masterminds/sprig/v3"
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
	DefaultTTL           time.Duration

	errorCounts map[string]int
	errMu       sync.Mutex
}

//+kubebuilder:rbac:groups=digitalis.io,resources=valssecrets,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=digitalis.io,resources=valssecrets/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=digitalis.io,resources=valssecrets/finalizers,verbs=update

// SetupWithManager sets up the controller with the Manager.
func (r *ValsSecretReconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.Recorder = mgr.GetEventRecorderFor("Secrets")
	pred := predicate.GenerationChangedPredicate{}

	return ctrl.NewControllerManagedBy(mgr).
		For(&secretv1.ValsSecret{}).
		Owns(&corev1.Secret{}).WithEventFilter(pred).
		Complete(r)
}

// Reconcile is the main function
func (r *ValsSecretReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var secret secretv1.ValsSecret

	err := r.Get(ctx, req.NamespacedName, &secret)
	if err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if r.shouldExclude(secret.Namespace) {
		r.Log.Info("Namespace requested is in the exclusion list, ignoring", "excluded_namespaces", r.ExcludeNamespaces)
		return ctrl.Result{}, nil
	}
	//! [finalizer]
	valsSecretFinalizerName := "vals.digitalis.io/finalizer"
	if secret.ObjectMeta.DeletionTimestamp.IsZero() {
		if !utils.ContainsString(secret.GetFinalizers(), valsSecretFinalizerName) {
			secret.SetFinalizers(append(secret.GetFinalizers(), valsSecretFinalizerName))
			if err := r.Update(context.Background(), &secret); err != nil {
				return ctrl.Result{}, err
			}
		}
	} else {
		// The object is being deleted
		r.clearErrorCount(&secret)
		if utils.ContainsString(secret.GetFinalizers(), valsSecretFinalizerName) {
			// our finalizer is present, so lets handle any external dependency
			if err := r.deleteSecret(ctx, &secret); err != nil {
				r.Log.Error(err, "Error deleting from Vals-Secret")
				return ctrl.Result{}, client.IgnoreNotFound(err)
			}

			// remove our finalizer from the list and update it.
			secret.SetFinalizers(utils.RemoveString(secret.GetFinalizers(), valsSecretFinalizerName))
			if err := r.Update(context.Background(), &secret); err != nil {
				return ctrl.Result{}, err
			}
		}

		// Stop reconciliation as the item is being deleted
		r.Log.Info(fmt.Sprintf("Secret %s deleted", secret.Name))
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

	if currentSecret != nil && currentSecret.Name != "" && !r.hasSecretExpired(secret, currentSecret) {
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
		r.Log.Error(err, "Failed to get secrets from secrets store", "name", secret.Name)
		if r.recordingEnabled(&secret) {
			msg := fmt.Sprintf("Failed to get secrets from secrets store %v", err)
			r.Recorder.Event(&secret, corev1.EventTypeNormal, "Failed", msg)
		}

		return r.errorBackoff(&secret)
	}

	data := make(map[string][]byte)
	dataStr := make(map[string]string)
	for k, v := range valsRendered {
		if secret.Spec.Data[k].Encoding == "base64" && !strings.HasPrefix(secret.Spec.Data[k].Ref, k8sSecretPrefix) {
			sDec, err := b64.StdEncoding.DecodeString(v.(string))
			if err != nil {
				r.Log.Error(err, "Cannot b64 decode secret. Please check encoding configuration. Requeuing.", "name", secret.Name)
				if r.recordingEnabled(&secret) {
					r.Recorder.Event(&secret, corev1.EventTypeNormal, "Failed", "Base64 decoding failed")
				}

				return r.errorBackoff(&secret)
			}
			data[k] = sDec
			dataStr[k] = string(sDec)
		} else {
			data[k] = []byte(v.(string))
			dataStr[k] = v.(string)
		}
	}

	/* Render any template given */
	for k, v := range secret.Spec.Template {
		b := bytes.NewBuffer(nil)
		t, err := template.New(k).Funcs(sprig.FuncMap()).Parse(v)
		if err != nil {
			r.Log.Error(err, "Cannot parse template")
			if r.recordingEnabled(&secret) {
				msg := fmt.Sprintf("Template could not be parsed: %v", err)
				r.Recorder.Event(&secret, corev1.EventTypeNormal, "Failed", msg)
			}
			continue
		}
		if err := t.Execute(b, &dataStr); err != nil {
			r.Log.Error(err, "Cannot render template")
			if r.recordingEnabled(&secret) {
				msg := fmt.Sprintf("Template could not be rendered: %v", err)
				r.Recorder.Event(&secret, corev1.EventTypeNormal, "Failed", msg)
			}
			continue
		}

		data[k] = b.Bytes()
	}

	err = r.upsertSecret(&secret, data)
	if err != nil {
		r.Log.Error(err, "Failed to create secret")
		return ctrl.Result{}, nil
	}

	r.clearErrorCount(&secret)
	return ctrl.Result{RequeueAfter: r.ReconciliationPeriod}, nil
}

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

	// Do nothing if the secret does not need updating
	if !r.secretNeedsUpdate(sDef, secret, data) {
		return nil
	}

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
	utils.MergeMap(secret.ObjectMeta.Labels, sDef.ObjectMeta.Labels)
	secret.ObjectMeta.Labels[managedByLabel] = "vals-operator"
	utils.MergeMap(secret.ObjectMeta.Annotations, sDef.ObjectMeta.Annotations)
	secret.ObjectMeta.Annotations[lastUpdatedAnnotation] = time.Now().UTC().Format(timeLayout)
	delete(secret.ObjectMeta.Annotations, corev1.LastAppliedConfigAnnotation)
	secret.ResourceVersion = ""

	if err = controllerutil.SetControllerReference(sDef, secret, r.Scheme()); err != nil {
		return err
	}
	err = r.Create(r.Ctx, secret)
	if errors.IsAlreadyExists(err) {
		err = r.Update(r.Ctx, secret)
	}

	if err != nil {
		if r.recordingEnabled(sDef) {
			msg := fmt.Sprintf("Secret %s not saved %v", secret.Name, err)
			r.Recorder.Event(sDef, corev1.EventTypeNormal, "Failed", msg)
		}
		SecretFailures.Inc()
		SecretError.WithLabelValues(secret.Name, secret.Namespace).SetToCurrentTime()
		return err
	}

	/* Prometheus */
	SecretInfo.WithLabelValues(secret.Name, secret.Namespace).SetToCurrentTime()

	if r.recordingEnabled(sDef) {
		r.Recorder.Event(sDef, corev1.EventTypeNormal, "Updated", "Secret created or updated")
	}
	r.Log.Info("Updated secret", "name", secretName)

	if len(sDef.Spec.Databases) > 0 {
		r.updateDatabases(sDef, secret)
	} // end DB section

	return err
}

func (r *ValsSecretReconciler) updateDatabases(sDef *secretv1.ValsSecret, secret *corev1.Secret) {
	r.Log.Info("Syncing credentials to databases")
	for db := range sDef.Spec.Databases {
		if sDef.Spec.Databases[db].LoginCredentials.SecretName != "" {
			namespace := sDef.Spec.Databases[db].LoginCredentials.Namespace
			if sDef.Spec.Databases[db].LoginCredentials.Namespace == "" {
				namespace = sDef.Namespace
			}
			dbSecret, err := r.getSecret(sDef.Spec.Databases[db].LoginCredentials.SecretName, namespace)
			if err != nil {
				msg := fmt.Sprintf("Could not get secret %s", sDef.Spec.Databases[db].LoginCredentials.SecretName)
				r.Log.Error(err, msg)
				if r.recordingEnabled(sDef) {
					r.Recorder.Event(sDef, corev1.EventTypeNormal, "Failed", msg)
				}
				// don't give up just yet, there may be other databases
				continue
			}
			loginUsername := ""
			if sDef.Spec.Databases[db].LoginCredentials.UsernameKey != "" {
				loginUsername = string(dbSecret.Data[sDef.Spec.Databases[db].LoginCredentials.UsernameKey])
			}

			username := string(secret.Data[sDef.Spec.Databases[db].UsernameKey])
			password := string(secret.Data[sDef.Spec.Databases[db].PasswordKey])

			if username == "" || password == "" {
				msg := fmt.Sprintf("'%s' or '%s' keys do not point to a valid username or password",
					sDef.Spec.Databases[db].UsernameKey, sDef.Spec.Databases[db].PasswordKey)
				r.Log.Error(err, msg)
				return
			}

			dbQuery := dbType.DatabaseBackend{
				Username:      username,
				Password:      password,
				UserHost:      string(dbSecret.Data[sDef.Spec.Databases[db].UserHost]),
				LoginUsername: loginUsername,
				LoginPassword: string(dbSecret.Data[sDef.Spec.Databases[db].LoginCredentials.PasswordKey]),
				Driver:        sDef.Spec.Databases[db].Driver,
				Hosts:         sDef.Spec.Databases[db].Hosts,
				Port:          sDef.Spec.Databases[db].Port,
			}
			if err := valsDb.UpdateUserPassword(dbQuery); err != nil {
				r.Log.Error(err, "Cannot update DB password")
				if r.recordingEnabled(sDef) {
					r.Recorder.Event(sDef, corev1.EventTypeNormal, "Failed", "Cannot update database password")
				}
			}
		}
	}
}

// secretNeedsUpdate Checks if the secret data or definition has changed from the current secret
func (r *ValsSecretReconciler) secretNeedsUpdate(sDef *secretv1.ValsSecret, secret *corev1.Secret, newData map[string][]byte) bool {
	return secret == nil || secret.Name == "" ||
		!utils.ByteMapsMatch(secret.Data, newData) ||
		!utils.StringMapsMatch(
			secret.ObjectMeta.Annotations,
			sDef.ObjectMeta.Annotations,
			[]string{"kubectl.kubernetes.io/last-applied-configuration", "vals-operator.digitalis.io/last-updated"}) ||
		!utils.StringMapsMatch(
			secret.ObjectMeta.Labels,
			sDef.ObjectMeta.Labels,
			[]string{"app.kubernetes.io/managed-by"})
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
	matchMap := utils.FindAllGroups(re, secretRef)

	if !utils.K8sSecretFound(matchMap) {
		return "", fmt.Errorf("The ref+k8s secret '%s' did not match the regular expression for ref+k8s://namespace/secret-name#key", secretRef)
	}
	secret, err := r.getSecret(matchMap["secretName"], matchMap["namespace"])
	if err != nil {
		return "", err
	}
	return string(secret.Data[matchMap["key"]]), nil
}

func (r *ValsSecretReconciler) hasSecretExpired(sDef secretv1.ValsSecret, secret *corev1.Secret) bool {
	/* if no TTL, apply a sensible default */
	if sDef.Spec.TTL <= 0 {
		sDef.Spec.TTL = int64(r.DefaultTTL.Seconds())
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

	if int64(now.Sub(s).Seconds()) > sDef.Spec.TTL {
		return true
	}

	return false
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
