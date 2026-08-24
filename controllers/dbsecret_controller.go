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
	"regexp"
	"strconv"
	"strings"
	"sync"
	"text/template"
	"time"

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
	dmetrics "digitalis.io/vals-operator/metrics"
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
	// AllowedVaultMounts restricts which database mounts a DbSecret may request
	// credentials from. Entries are either `mount` (allowed in any namespace) or
	// `namespace/mount` (allowed in that namespace only). Empty = all allowed.
	AllowedVaultMounts map[string]bool
	// AllowedVaultRoles restricts which roles a DbSecret may request. Entries are
	// either `role` or `namespace/role`. Empty = all allowed.
	AllowedVaultRoles map[string]bool

	errorCounts map[string]int
	errMu       sync.Mutex
}

// vaultPathSegment matches a single mount or role name. A segment must not
// contain a path separator: the mount and role are interpolated into the Vault
// path (`<mount>/creds/<role>`) and into lease IDs, so a value containing `/`
// would let a DbSecret reach a different path than the one it names, and would
// break lease ID parsing.
var vaultPathSegment = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

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
				r.Log.Error(err, "Lease cannot be revoked", "name", dbSecret.Name, "namespace", dbSecret.Namespace)
				dmetrics.DbSecretRevokationError.WithLabelValues(dbSecret.Name, dbSecret.Namespace).SetToCurrentTime()
			}
			// our finalizer is present, so lets handle any external dependency
			if err := r.deleteSecret(ctx, &dbSecret); err != nil {
				r.Log.Error(err, "Error deleting from database secret", "name", dbSecret.Name, "namespace", dbSecret.Namespace)
				dmetrics.DbSecretDeletionError.WithLabelValues(dbSecret.Name, dbSecret.Namespace).SetToCurrentTime()
				return ctrl.Result{}, client.IgnoreNotFound(err)
			}

			// remove our finalizer from the list and update it.
			dbSecret.SetFinalizers(utils.RemoveString(dbSecret.GetFinalizers(), valsDbSecretFinalizerName))
			if err := r.Update(context.Background(), &dbSecret); err != nil {
				dmetrics.DbSecretDeletionError.WithLabelValues(dbSecret.Name, dbSecret.Namespace).SetToCurrentTime()
				return ctrl.Result{}, err
			}
			/* mark as deleted in prom */
			dmetrics.DbSecretExpireTime.WithLabelValues(dbSecret.Name, dbSecret.Namespace).Set(0)
			dmetrics.DbSecretInfo.WithLabelValues(dbSecret.Name, dbSecret.Namespace).Set(0)
		}

		// Stop reconciliation as the item is being deleted
		r.Log.Info("Secret deleted", "name", dbSecret.Name, "namespace", dbSecret.Namespace)
		return ctrl.Result{}, nil
	}
	//! [finalizer]

	/* Refuse to talk to the backend at all for a mount/role this DbSecret may not use.
	   Checked after the finalizer block so an existing resource can still be deleted. */
	if err := r.isVaultRefAllowed(&dbSecret); err != nil {
		r.Log.Error(err, "Refusing to request credentials", "name", dbSecret.Name, "namespace", dbSecret.Namespace)
		if r.recordingEnabled(&dbSecret) {
			r.Recorder.Event(&dbSecret, corev1.EventTypeWarning, "Denied", err.Error())
		}
		dmetrics.DbSecretError.WithLabelValues(dbSecret.Name, dbSecret.Namespace).SetToCurrentTime()
		return ctrl.Result{}, nil
	}

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
			for k := range currentSecret.Data {
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
		dmetrics.DbSecretFailures.Inc()
		dmetrics.DbSecretError.WithLabelValues(dbSecret.Name, dbSecret.Namespace).SetToCurrentTime()
		return ctrl.Result{}, err
	}

	err = r.upsertSecret(&dbSecret, creds, currentSecret)
	if err != nil {
		r.Log.Error(err, "Failed to create secret", "name", dbSecret.Name, "namespace", dbSecret.Namespace)
		dmetrics.DbSecretFailures.Inc()
		dmetrics.DbSecretError.WithLabelValues(dbSecret.Name, dbSecret.Namespace).SetToCurrentTime()
		/* Returned so the workqueue retries with backoff: swallowing this left the
		   DbSecret wedged until its spec changed. */
		return ctrl.Result{}, err
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

// isVaultRefAllowed returns nil if the DbSecret may request credentials for the
// mount and role it names.
//
// A DbSecret is namespaced but the operator holds a single, cluster-wide token
// for the secrets backend. Without a restriction, anybody able to create a
// DbSecret in any namespace can mint credentials for every database mount and
// role that token can read. The allowlists are opt-in so existing deployments
// keep working, but the format check always applies.
func (r *DbSecretReconciler) isVaultRefAllowed(sDef *digitalisiov1beta1.DbSecret) error {
	mount := sDef.Spec.Vault.Mount
	role := sDef.Spec.Vault.Role

	if !vaultPathSegment.MatchString(mount) {
		return fmt.Errorf("invalid vault mount %q: must match %s", mount, vaultPathSegment)
	}
	if !vaultPathSegment.MatchString(role) {
		return fmt.Errorf("invalid vault role %q: must match %s", role, vaultPathSegment)
	}

	if len(r.AllowedVaultMounts) > 0 &&
		!r.AllowedVaultMounts[mount] &&
		!r.AllowedVaultMounts[fmt.Sprintf("%s/%s", sDef.Namespace, mount)] {
		return fmt.Errorf("vault mount %q is not allowed in namespace %s", mount, sDef.Namespace)
	}
	if len(r.AllowedVaultRoles) > 0 &&
		!r.AllowedVaultRoles[role] &&
		!r.AllowedVaultRoles[fmt.Sprintf("%s/%s", sDef.Namespace, role)] {
		return fmt.Errorf("vault role %q is not allowed in namespace %s", role, sDef.Namespace)
	}

	return nil
}

// leaseIdSuffix extracts the trailing identifier from a lease ID returned by
// the secrets backend, which has the form `<mount>/creds/<role>/<id>`. Only the
// last segment is stored on the secret; the mount and role are taken from the
// DbSecret spec when the lease ID is rebuilt.
//
// The backend response is not trusted to have that exact shape: a mount path
// with extra segments, or any other backend quirk, must not panic the operator.
func leaseIdSuffix(leaseId string) (string, error) {
	if leaseId == "" {
		return "", fmt.Errorf("backend returned an empty lease id")
	}
	parts := strings.Split(leaseId, "/")
	suffix := parts[len(parts)-1]
	if len(parts) < 4 || suffix == "" {
		return "", fmt.Errorf("backend returned a lease id in an unexpected format: %d segments", len(parts))
	}
	return suffix, nil
}

// leaseIdFor rebuilds the full lease ID for a secret from the mount and role in
// the DbSecret spec plus the suffix stored on the secret. It returns an empty
// string when the secret carries no lease.
func leaseIdFor(sDef *digitalisiov1beta1.DbSecret, currentSecret *corev1.Secret) string {
	if currentSecret == nil || currentSecret.Annotations[leaseIdLabel] == "" {
		return ""
	}
	return fmt.Sprintf("%s/creds/%s/%s",
		sDef.Spec.Vault.Mount,
		sDef.Spec.Vault.Role,
		currentSecret.Annotations[leaseIdLabel])
}

func (r *DbSecretReconciler) revokeLease(sDef *digitalisiov1beta1.DbSecret, currentSecret *corev1.Secret) error {
	if currentSecret == nil || currentSecret.Name == "" {
		return nil
	}

	r.Log.Info(fmt.Sprintf("Revoking lease for %s in namespace %s", currentSecret.Name, currentSecret.Namespace))

	leaseId := leaseIdFor(sDef, currentSecret)
	if leaseId == "" {
		return fmt.Errorf("cannot revoke credentials without lease Id: secret %s in namespace %s",
			currentSecret.Name, currentSecret.Namespace)
	}
	return vault.RevokeDbCredentials(leaseId)
}

// renewLease will ask vault to renew the lease
func (r *DbSecretReconciler) isLeaseValid(sDef *digitalisiov1beta1.DbSecret, currentSecret *corev1.Secret) bool {
	leaseId := leaseIdFor(sDef, currentSecret)
	if leaseId == "" {
		return false
	}
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

	leaseId = leaseIdFor(sDef, currentSecret)
	if leaseId == "" {
		return fmt.Errorf("cannot renew without lease Id")
	}

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

// ownedByDbSecret returns nil if the existing secret was created by this
// DbSecret, and an error if it belongs to anything else.
//
// upsertSecret has always set a controller reference, so every secret this
// controller manages carries one; anything else is a secret we did not create.
func ownedByDbSecret(sDef *digitalisiov1beta1.DbSecret, secret *corev1.Secret) error {
	for _, ref := range secret.GetOwnerReferences() {
		if ref.UID == sDef.GetUID() {
			return nil
		}
	}
	return fmt.Errorf("secret %s in namespace %s already exists and is not owned by DbSecret %s: refusing to overwrite it",
		secret.Name, secret.Namespace, sDef.Name)
}

// upsertSecret will create or update a secret
func (r *DbSecretReconciler) upsertSecret(sDef *digitalisiov1beta1.DbSecret, creds vault.VaultDbSecret, secret *corev1.Secret) error {
	var err error

	secretName := r.getSecretName(sDef)

	if secret == nil {
		secret = &corev1.Secret{}
	} else if err := ownedByDbSecret(sDef, secret); err != nil {
		/* Never take over a secret this DbSecret doesn't already own: spec.secretName
		   is free-form, so otherwise naming an unrelated secret would overwrite it and
		   adopt it for deletion. */
		if r.recordingEnabled(sDef) {
			r.Recorder.Event(sDef, corev1.EventTypeWarning, "Denied", err.Error())
		}
		return err
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

	leaseSuffix, err := leaseIdSuffix(creds.LeaseId)
	if err != nil {
		return err
	}

	utils.MergeMap(secret.ObjectMeta.Labels, sDef.ObjectMeta.Labels)
	utils.MergeMap(secret.ObjectMeta.Annotations, sDef.ObjectMeta.Annotations)
	secret.ObjectMeta.Annotations[managedByLabel] = "vals-operator"
	secret.ObjectMeta.Annotations[leaseIdLabel] = leaseSuffix

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
	f, err := strconv.ParseFloat(secret.Annotations[expiresOnLabel], 64)
	if err != nil {
		f = float64(time.Now().UnixNano())
	}
	dmetrics.DbSecretExpireTime.WithLabelValues(secret.Name, secret.Namespace).Set(f)
	dmetrics.DbSecretInfo.WithLabelValues(secret.Name, secret.Namespace).SetToCurrentTime()

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

		if object.Spec.Template.Annotations == nil {
			object.Spec.Template.Annotations = make(map[string]string)
		}
		object.Spec.Template.Annotations[restartedAnnotation] = time.Now().UTC().Format(timeLayout)
		err = r.Update(r.Ctx, &object)
		if err != nil {
			return err
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

		if object.Spec.Template.Annotations == nil {
			object.Spec.Template.Annotations = make(map[string]string)
		}
		object.Spec.Template.Annotations[restartedAnnotation] = time.Now().UTC().Format(timeLayout)
		err = r.Update(r.Ctx, &object)
		if err != nil {
			return err
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
		t, err := template.New(k).Funcs(utils.SafeTemplateFuncMap()).Parse(v)
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
