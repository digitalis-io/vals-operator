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
	"fmt"
	"strconv"
	"strings"
	"sync"
	"text/template"
	"time"

	sprig "github.com/Masterminds/sprig/v3"
	"github.com/go-logr/logr"
	v1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	digitalisiov1beta1 "digitalis.io/vals-operator/apis/digitalis.io/v1beta1"
	"digitalis.io/vals-operator/utils"
	"digitalis.io/vals-operator/vault"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// DbSecretReconciler reconciles a DbSecret object
type DbSecretReconciler struct {
	client.Client
	Scheme               *runtime.Scheme
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

//+kubebuilder:rbac:groups=digitalis.io,resources=dbsecrets,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=digitalis.io,resources=dbsecrets/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=digitalis.io,resources=dbsecrets/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the DbSecret object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.13.1/pkg/reconcile
func (r *DbSecretReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	_ = log.FromContext(ctx)

	var dbSecret digitalisiov1beta1.DbSecret

	err := r.Get(ctx, req.NamespacedName, &dbSecret)
	if err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if r.shouldExclude(dbSecret.Namespace) {
		r.Log.Info("Namespace requested is in the exclusion list, ignoring", "excluded_namespaces", r.ExcludeNamespaces)
		return ctrl.Result{}, nil
	}
	secretName := r.getSecretName(&dbSecret)
	currentSecret, err := r.getSecret(secretName, dbSecret.GetNamespace())
	if client.IgnoreNotFound(err) != nil {
		return ctrl.Result{}, err
	}

	//! [finalizer]
	valsDbSecretFinalizerName := "dbsecret.digitalis.io/finalizer"
	if dbSecret.ObjectMeta.DeletionTimestamp.IsZero() {
		if !utils.ContainsString(dbSecret.GetFinalizers(), valsDbSecretFinalizerName) {
			dbSecret.SetFinalizers(append(dbSecret.GetFinalizers(), valsDbSecretFinalizerName))
			if err := r.Update(context.Background(), &dbSecret); err != nil {
				return ctrl.Result{}, err
			}
		}
	} else {
		// The object is being deleted
		r.clearErrorCount(&dbSecret)
		if utils.ContainsString(dbSecret.GetFinalizers(), valsDbSecretFinalizerName) {
			err := r.revokeLease(&dbSecret, currentSecret)
			if err != nil {
				// log the error but continue
				r.Log.Error(err, "Lease cannot be revoked")
			}
			// our finalizer is present, so lets handle any external dependency
			if err := r.deleteSecret(ctx, &dbSecret); err != nil {
				r.Log.Error(err, "Error deleting from Vals-Secret", "name", dbSecret.Name, "namespace", dbSecret.Namespace)
				return ctrl.Result{}, client.IgnoreNotFound(err)
			}

			// remove our finalizer from the list and update it.
			dbSecret.SetFinalizers(utils.RemoveString(dbSecret.GetFinalizers(), valsDbSecretFinalizerName))
			if err := r.Update(context.Background(), &dbSecret); err != nil {
				return ctrl.Result{}, err
			}
		}

		// Stop reconciliation as the item is being deleted
		r.Log.Info("Secret deleted", "name", dbSecret.Name, "namespace", dbSecret.Namespace)
		return ctrl.Result{}, nil
	}
	//! [finalizer]

	if currentSecret != nil && currentSecret.Name != "" {
		shouldUpdate := false
		canRenew := true

		e, err := strconv.ParseInt(currentSecret.Annotations[expiresOnLabel], 10, 64)
		if err != nil {
			r.Log.Info("Updating secret due to invalid expire time", "name", dbSecret.Name, "namespace", dbSecret.Namespace)
			shouldUpdate = true
		} else {
			grace := int64(120) // if expires in less then 2 min, we'll update it
			if time.Now().Unix() >= e || time.Now().Unix()+grace >= e {
				shouldUpdate = true
				r.Log.Info(fmt.Sprintf("Credentials for secret %s expired on %s", currentSecret.Name, currentSecret.Annotations[expiresOnLabel]))
			}
		}
		if !r.isLeaseValid(&dbSecret, currentSecret) {
			shouldUpdate = true
			canRenew = false
			if r.recordingEnabled(&dbSecret) {
				r.Recorder.Event(&dbSecret, corev1.EventTypeNormal, "Update", "Invalid lease found")
			}
			r.Log.Info("Invalid lease", "name", dbSecret.Name, "namespace", dbSecret.Namespace)
		} else if currentSecret.ObjectMeta.Annotations[forceCreateAnnotation] == "true" {
			if r.recordingEnabled(&dbSecret) {
				r.Recorder.Event(&dbSecret, corev1.EventTypeNormal, "Update", "Lease could not be renewed. New credentials will be issued")
			}
			canRenew = false
		}

		/* If the new secret doesn't have a template anymore, make sure it's deleted from the secret */
		if len(dbSecret.Spec.Template) == 0 {
			for k, _ := range currentSecret.Data {
				if k != "username" && k != "password" {
					delete(currentSecret.Data, k)
				}
			}
		}

		newHash := utils.CreateFakeHash(dbSecret.Spec.Template)
		if newHash != "" && currentSecret.Annotations[templateHash] != "" {
			if newHash != currentSecret.Annotations[templateHash] {
				shouldUpdate = true
			}
		}

		if !shouldUpdate {
			return ctrl.Result{RequeueAfter: r.ReconciliationPeriod}, nil
		}
		if canRenew && dbSecret.Spec.Renew {
			err = r.renewLease(&dbSecret, currentSecret)
			if err != nil {
				r.Log.Error(err, "Lease could not be extended", "name", dbSecret.Name, "namespace", dbSecret.Namespace)
			}
			return ctrl.Result{RequeueAfter: r.ReconciliationPeriod}, err
		}
	}

	/* Because we're about to request a new credential, revoke any possible old ones */
	if currentSecret != nil && currentSecret.Name != "" && currentSecret.ObjectMeta.Annotations[leaseIdLabel] != "" {
		if err := r.revokeLease(&dbSecret, currentSecret); err != nil {
			r.Log.Error(err, "Old lease could not be revoked", "name", dbSecret.Name, "namespace", dbSecret.Namespace)
		}
	}
	creds, err := vault.GetDbCredentials(dbSecret.Spec.Vault.Role, dbSecret.Spec.Vault.Mount)
	if err != nil {
		r.Log.Error(err, "Failed to obtain credentials from Vault", "name", dbSecret.Name, "namespace", dbSecret.Namespace)
		DbSecretFailures.Inc()
		DbSecretError.WithLabelValues(dbSecret.Name, dbSecret.Namespace).SetToCurrentTime()
		return ctrl.Result{}, err
	}

	err = r.upsertSecret(&dbSecret, creds, currentSecret)
	if err != nil {
		r.Log.Error(err, "Failed to create secret", "name", dbSecret.Name, "namespace", dbSecret.Namespace)
		DbSecretFailures.Inc()
		DbSecretError.WithLabelValues(dbSecret.Name, dbSecret.Namespace).SetToCurrentTime()
		return ctrl.Result{}, nil
	}

	/* Patching resources to force a rollout if required */
	for target := range dbSecret.Spec.Rollout {
		if dbSecret.Spec.Rollout[target].Name != "" && dbSecret.Spec.Rollout[target].Kind != "" {
			if err := r.rollout(&dbSecret, dbSecret.Spec.Rollout[target]); err != nil {
				r.Log.Error(err, "Could not perform rollout",
					"name", dbSecret.Name,
					"namespace", dbSecret.Namespace,
					"kind", dbSecret.Spec.Rollout[target].Kind,
					"name", dbSecret.Spec.Rollout[target].Name)
			}
		}
	}
	return ctrl.Result{RequeueAfter: r.ReconciliationPeriod}, nil
}

func (r *DbSecretReconciler) revokeLease(sDef *digitalisiov1beta1.DbSecret, currentSecret *corev1.Secret) error {
	if currentSecret == nil || currentSecret.Name != "" {
		return nil
	}

	r.Log.Info(fmt.Sprintf("Revoking lease for %s", currentSecret.Name))

	if currentSecret.ObjectMeta.Annotations[leaseIdLabel] == "" {
		return fmt.Errorf("cannot revoke credentials without lease Id")
	}
	leaseId := fmt.Sprintf("%s/creds/%s/%s",
		sDef.Spec.Vault.Mount,
		sDef.Spec.Vault.Role,
		currentSecret.ObjectMeta.Annotations[leaseIdLabel])
	return vault.RevokeDbCredentials(leaseId)
}

// renewLease will ask vault to renew the lease
func (r *DbSecretReconciler) isLeaseValid(sDef *digitalisiov1beta1.DbSecret, currentSecret *corev1.Secret) bool {
	if currentSecret.ObjectMeta.Annotations[leaseIdLabel] == "" {
		return false
	}
	leaseId := fmt.Sprintf("%s/creds/%s/%s",
		sDef.Spec.Vault.Mount,
		sDef.Spec.Vault.Role,
		currentSecret.ObjectMeta.Annotations[leaseIdLabel])
	ok := vault.IsLeaseValid(leaseId)
	if !ok {
		r.Log.Info("Lease on secret no longer valid", "name", sDef.Name, "namespace", sDef.Namespace)
	}
	return ok
}

// renewLease will ask vault to renew the lease
func (r *DbSecretReconciler) renewLease(sDef *digitalisiov1beta1.DbSecret, currentSecret *corev1.Secret) error {
	var err error
	var leaseId string

	r.Log.Info("Renewing lease on secret", "name", sDef.Name, "namespace", sDef.Namespace)

	if currentSecret.ObjectMeta.Annotations[leaseIdLabel] == "" {
		return fmt.Errorf("cannot renew without lease Id")
	}
	leaseId = fmt.Sprintf("%s/creds/%s/%s",
		sDef.Spec.Vault.Mount,
		sDef.Spec.Vault.Role,
		currentSecret.ObjectMeta.Annotations[leaseIdLabel])

	var increment int
	increment, err = strconv.Atoi(currentSecret.ObjectMeta.Annotations[leaseDurationLabel])
	if err != nil {
		r.Log.Error(err, "Can't get increment")
		return err
	}
	err = vault.RenewDbCredentials(leaseId, increment)
	if err != nil {
		return err
	}

	currentSecret.ObjectMeta.Annotations[expiresOnLabel] = fmt.Sprintf("%d", time.Now().Unix()+int64(increment))
	currentSecret.ObjectMeta.Annotations[lastUpdatedAnnotation] = time.Now().UTC().Format(timeLayout)
	err = r.Update(r.Ctx, currentSecret)
	if err != nil {
		if r.recordingEnabled(sDef) {
			msg := fmt.Sprintf("Secret %s lease not renewed %v", currentSecret.Name, err)
			r.Recorder.Event(sDef, corev1.EventTypeNormal, "Failed", msg)
		}
		/* Force create new secret */
		currentSecret.ObjectMeta.Annotations[forceCreateAnnotation] = "true"
		return r.Update(r.Ctx, currentSecret)
	}

	if r.recordingEnabled(sDef) {
		r.Recorder.Event(sDef, corev1.EventTypeNormal, "Updated", "Database lease renewed")
	}

	return err
}

// upsertSecret will create or update a secret
func (r *DbSecretReconciler) upsertSecret(sDef *digitalisiov1beta1.DbSecret, creds vault.VaultDbSecret, secret *corev1.Secret) error {
	var err error

	secretName := r.getSecretName(sDef)

	if secret == nil {
		secret = &corev1.Secret{}
	}

	dataStr := make(map[string]string)
	dataStr["username"] = creds.Username
	dataStr["password"] = creds.Password
	if creds.ConnectionURL != "" {
		dataStr["connection_url"] = creds.ConnectionURL
	}
	if creds.Hosts != "" {
		dataStr["hosts"] = creds.Hosts
	}
	data := r.renderTemplate(sDef, dataStr)

	if len(data) < 1 {
		secret.StringData = dataStr
	} else {
		secret.Data = data
	}

	secret.Name = secretName
	secret.Namespace = sDef.Namespace
	secret.Type = corev1.SecretType("Opaque")
	secret.ResourceVersion = ""

	/* additional info */
	if secret.ObjectMeta.Labels == nil {
		secret.ObjectMeta.Labels = make(map[string]string)
	}
	if secret.ObjectMeta.Annotations == nil {
		secret.ObjectMeta.Annotations = make(map[string]string)
	}

	utils.MergeMap(secret.ObjectMeta.Labels, sDef.ObjectMeta.Labels)
	utils.MergeMap(secret.ObjectMeta.Annotations, sDef.ObjectMeta.Annotations)
	secret.ObjectMeta.Annotations[managedByLabel] = "vals-operator"
	secret.ObjectMeta.Annotations[leaseIdLabel] = strings.Split(creds.LeaseId, "/")[3]

	secret.ObjectMeta.Annotations[leaseDurationLabel] = fmt.Sprintf("%d", creds.LeaseDuration)
	secret.ObjectMeta.Annotations[lastUpdatedAnnotation] = time.Now().UTC().Format(timeLayout)
	secret.ObjectMeta.Annotations[expiresOnLabel] = fmt.Sprintf("%d", time.Now().Unix()+int64(creds.LeaseDuration))
	/* Hash to check for changes later on */
	secret.ObjectMeta.Annotations[templateHash] = utils.CreateFakeHash(sDef.Spec.Template)
	delete(secret.ObjectMeta.Annotations, forceCreateAnnotation)

	if err = controllerutil.SetControllerReference(sDef, secret, r.Scheme); err != nil {
		return err
	}

	r.Log.Info(fmt.Sprintf("Creating secret %s", secretName))

	err = r.Create(r.Ctx, secret)
	if errors.IsAlreadyExists(err) {
		err = r.Update(r.Ctx, secret)
	}

	if err != nil {
		if r.recordingEnabled(sDef) {
			msg := fmt.Sprintf("Secret %s not saved %v", secret.Name, err)
			r.Recorder.Event(sDef, corev1.EventTypeNormal, "Failed", msg)
		}
		return err
	}
	/* Prometheus */
	f, err := strconv.ParseFloat(secret.Annotations[expiresOnLabel], 10)
	if err != nil {
		f = float64(time.Now().UnixNano())
	}
	DbSecretExpireTime.WithLabelValues(secret.Name, secret.Namespace).Set(f)
	DbSecretInfo.WithLabelValues(secret.Name, secret.Namespace).SetToCurrentTime()

	if r.recordingEnabled(sDef) {
		r.Recorder.Event(sDef, corev1.EventTypeNormal, "Updated", "Secret created or updated")
	}
	r.Log.Info("Updated secret", "name", secretName)

	return err
}

// SetupWithManager sets up the controller with the Manager.
func (r *DbSecretReconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.Recorder = mgr.GetEventRecorderFor("Secrets")
	pred := predicate.GenerationChangedPredicate{}

	return ctrl.NewControllerManagedBy(mgr).
		For(&digitalisiov1beta1.DbSecret{}).
		Owns(&corev1.Secret{}).WithEventFilter(pred).
		Complete(r)
}

// shouldExclude will return true if the secretDefinition is in an excluded namespace
func (r *DbSecretReconciler) shouldExclude(sDefNamespace string) bool {
	if len(r.ExcludeNamespaces) > 0 {
		return r.ExcludeNamespaces[sDefNamespace]
	}
	return false
}

func (r *DbSecretReconciler) getSecret(secretName string, namespace string) (*corev1.Secret, error) {
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

// secretNeedsUpdate Checks if the secret data or definition has changed from the current secret
func (r *DbSecretReconciler) secretNeedsUpdate(sDef *digitalisiov1beta1.DbSecret, secret *corev1.Secret, newData map[string][]byte) bool {
	return false
}

// deleteSecret will delete a secret given its namespace and name
func (r *DbSecretReconciler) deleteSecret(ctx context.Context, sDef *digitalisiov1beta1.DbSecret) error {
	secretName := r.getSecretName(sDef)

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: sDef.Namespace,
			Name:      secretName,
		},
	}
	return client.IgnoreNotFound(r.Delete(ctx, secret))
}

func (r *DbSecretReconciler) clearErrorCount(valsSecret *digitalisiov1beta1.DbSecret) {
	r.errMu.Lock()
	defer r.errMu.Unlock()
	errKey := fmt.Sprintf("%s/%s", valsSecret.Namespace, valsSecret.Name)
	if len(r.errorCounts) < 1 {
		return
	}
	delete(r.errorCounts, errKey)
}

// recordingEnabled check if we want the event recorded
func (r *DbSecretReconciler) recordingEnabled(sDef *digitalisiov1beta1.DbSecret) bool {
	recordAnn := sDef.GetAnnotations()[recordingEnabledAnnotation]
	if recordAnn != "" && recordAnn != "true" {
		return false
	}
	return r.RecordChanges
}

// rollout is used to restart the Deployment or StatefulSet
func (r *DbSecretReconciler) rollout(sDef *digitalisiov1beta1.DbSecret, rolloutTarget digitalisiov1beta1.DbRolloutTarget) error {
	var err error

	clientObject := types.NamespacedName{
		Namespace: sDef.Namespace,
		Name:      rolloutTarget.Name,
	}
	r.Log.Info(fmt.Sprintf("Rolling restart %s/%s in namespace %s", rolloutTarget.Kind, rolloutTarget.Name, sDef.Namespace))

	if strings.ToLower(rolloutTarget.Kind) == "deployment" {
		var object v1.Deployment
		err = r.Get(r.Ctx, clientObject, &object)
		if errors.IsNotFound(err) {
			msg := fmt.Sprintf("%s/%s in namespace %s not found", rolloutTarget.Kind, rolloutTarget.Name, sDef.Namespace)
			r.Log.Error(err, msg)
			return nil
		}
		if err != nil {
			return err
		}

		if object.Status.ReadyReplicas > 0 {
			object.Spec.Template.Annotations[restartedAnnotation] = time.Now().UTC().Format(timeLayout)
			err = r.Update(r.Ctx, &object)
			if err != nil {
				return err
			}
		}
	} else if strings.ToLower(rolloutTarget.Kind) == "statefulset" {
		var object v1.StatefulSet
		err = r.Get(r.Ctx, clientObject, &object)
		if errors.IsNotFound(err) {
			r.Log.Error(err, fmt.Sprintf("%s/%s in namespace %s not found", rolloutTarget.Kind, rolloutTarget.Name, sDef.Namespace))
			return nil
		}
		if err != nil {
			return err
		}

		if object.Status.ReadyReplicas > 0 {
			object.Spec.Template.Annotations[restartedAnnotation] = time.Now().UTC().Format(timeLayout)
			err = r.Update(r.Ctx, &object)
			if err != nil {
				return err
			}
		}
	} else {
		return fmt.Errorf("%s kind is not supported", rolloutTarget.Kind)
	}

	return nil
}

// rollout is used to restart the Deployment or StatefulSet
func (r *DbSecretReconciler) getSecretName(sDef *digitalisiov1beta1.DbSecret) string {
	var secretName string
	if sDef.Spec.SecretName != "" {
		secretName = sDef.Spec.SecretName
	} else {
		secretName = sDef.Name
	}
	return secretName
}

func (r *DbSecretReconciler) renderTemplate(sDef *digitalisiov1beta1.DbSecret, dataStr map[string]string) map[string][]byte {
	data := make(map[string][]byte)

	/* Render any template given */
	for k, v := range sDef.Spec.Template {
		b := bytes.NewBuffer(nil)
		t, err := template.New(k).Funcs(sprig.FuncMap()).Parse(v)
		if err != nil {
			r.Log.Error(err, "Cannot parse template")
			if r.recordingEnabled(sDef) {
				msg := fmt.Sprintf("Template could not be parsed: %v", err)
				r.Recorder.Event(sDef, corev1.EventTypeNormal, "Failed", msg)
			}
			return data
		}
		if err := t.Execute(b, &dataStr); err != nil {
			r.Log.Error(err, "Cannot render template")
			if r.recordingEnabled(sDef) {
				msg := fmt.Sprintf("Template could not be rendered: %v", err)
				r.Recorder.Event(sDef, corev1.EventTypeNormal, "Failed", msg)
			}
			return data
		}

		data[k] = b.Bytes()
	}
	return data
}
