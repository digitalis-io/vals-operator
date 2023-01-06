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

package digitalisio

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
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
	"digitalis.io/vals-operator/utils"
	"digitalis.io/vals-operator/vault"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	managedByLabel             = "app.kubernetes.io/managed-by"
	lastUpdatedAnnotation      = "vals-operator.digitalis.io/last-updated"
	timeLayout                 = "2006-01-02T15.04.05Z"
	leaseIdLabel               = "vals-operator.digitalis.io/lease-id"
	leaseDurationLabel         = "vals-operator.digitalis.io/lease-duration"
	expiresOnLabel             = "vals-operator.digitalis.io/expires-on"
	recordingEnabledAnnotation = "vals-operator.digitalis.io/record"
	restartedAnnotation        = "vals-operator.digitalis.io/restartedAt"
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
			// our finalizer is present, so lets handle any external dependency
			if err := r.deleteSecret(ctx, &dbSecret); err != nil {
				r.Log.Error(err, "Error deleting from Vals-Secret")
				return ctrl.Result{}, client.IgnoreNotFound(err)
			}

			// remove our finalizer from the list and update it.
			dbSecret.SetFinalizers(utils.RemoveString(dbSecret.GetFinalizers(), valsDbSecretFinalizerName))
			if err := r.Update(context.Background(), &dbSecret); err != nil {
				return ctrl.Result{}, err
			}
		}

		// Stop reconciliation as the item is being deleted
		r.Log.Info(fmt.Sprintf("Secret %s deleted", dbSecret.Name))
		return ctrl.Result{}, nil
	}
	//! [finalizer]

	var secretName string
	if dbSecret.Spec.SecretName != "" {
		secretName = dbSecret.Spec.SecretName
	} else {
		secretName = dbSecret.Name
	}

	currentSecret, err := r.getSecret(secretName, dbSecret.GetNamespace())
	if client.IgnoreNotFound(err) != nil {
		return ctrl.Result{}, err
	}

	if currentSecret != nil && currentSecret.Name != "" {
		shouldUpdate := false
		e, err := strconv.ParseInt(currentSecret.Annotations[expiresOnLabel], 10, 64)
		if err != nil {
			r.Log.Info(fmt.Sprintf("Updating secret %s", currentSecret.Name))
			shouldUpdate = true
		} else {
			margin := int64(120) // if expires in less then 2 min, we'll update it
			if time.Now().Unix() >= e || time.Now().Unix() >= e+margin {
				shouldUpdate = true
				r.Log.Info(fmt.Sprintf("Updating credentials %s expired on %s", currentSecret.Name, currentSecret.Annotations[expiresOnLabel]))
			}
		}
		if !shouldUpdate {
			return ctrl.Result{RequeueAfter: r.ReconciliationPeriod}, nil
		}
	}

	creds, err := vault.GetDbCredentials(dbSecret.Spec.Vault.Role, dbSecret.Spec.Vault.Mount)
	if err != nil {
		r.Log.Error(err, "Failed to obtain credentials from Vault")
		return ctrl.Result{}, err
	}

	err = r.upsertSecret(&dbSecret, creds, currentSecret)
	if err != nil {
		r.Log.Error(err, "Failed to create secret")
		return ctrl.Result{}, nil
	}
	/* Patching resources to force a rollout if required */
	if dbSecret.Spec.Rollout.Name != "" && dbSecret.Spec.Rollout.Kind != "" {
		if err := r.rollout(&dbSecret); err != nil {
			r.Log.Error(err, "Could not perform rollout")
		}
	}
	return ctrl.Result{RequeueAfter: r.ReconciliationPeriod}, nil
}

// upsertSecret will create or update a secret
func (r *DbSecretReconciler) upsertSecret(sDef *digitalisiov1beta1.DbSecret, creds vault.VaultDbSecret, secret *corev1.Secret) error {
	var err error
	var secretName string
	if sDef.Spec.SecretName != "" {
		secretName = sDef.Spec.SecretName
	} else {
		secretName = sDef.Name
	}

	if secret == nil {
		secret = &corev1.Secret{}
	}

	usernameKey := "username"
	passwordKey := "password"
	if sDef.Spec.Secret["username"] != "" {
		usernameKey = sDef.Spec.Secret["username"]
	}
	if sDef.Spec.Secret["password"] != "" {
		passwordKey = sDef.Spec.Secret["password"]
	}
	// if I use StringData I can avoid base64encoding the data
	data := make(map[string]string)
	data[usernameKey] = creds.Username
	data[passwordKey] = creds.Password
	/* Any other values are literals to add to the secret */
	for k, v := range sDef.Spec.Secret {
		if k != "username" && k != "password" {
			data[k] = v
		}
	}
	secret.StringData = data
	secret.Data = nil

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
	//secret.ObjectMeta.Annotations[leaseIdLabel] = strings.Split(creds.LeaseId, "/")[3]
	secret.ObjectMeta.Annotations[leaseDurationLabel] = fmt.Sprintf("%d", creds.LeaseDuration)
	secret.ObjectMeta.Annotations[lastUpdatedAnnotation] = time.Now().UTC().Format(timeLayout)
	secret.ObjectMeta.Annotations[expiresOnLabel] = fmt.Sprintf("%d", time.Now().Unix()+int64(creds.LeaseDuration))

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
	var name string
	if sDef.Spec.SecretName != "" {
		name = sDef.Spec.SecretName
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
func (r *DbSecretReconciler) rollout(sDef *digitalisiov1beta1.DbSecret) error {
	var err error

	clientObject := types.NamespacedName{
		Namespace: sDef.Namespace,
		Name:      sDef.Spec.Rollout.Name,
	}
	r.Log.Info(fmt.Sprintf("Rolling restart %s/%s in namespace %s", sDef.Spec.Rollout.Kind, sDef.Spec.Rollout.Name, sDef.Namespace))

	if strings.ToLower(sDef.Spec.Rollout.Kind) == "deployment" {
		var object v1.Deployment
		err = r.Get(r.Ctx, clientObject, &object)
		if errors.IsNotFound(err) {
			msg := fmt.Sprintf("%s/%s in namespace %s not found", sDef.Spec.Rollout.Kind, sDef.Spec.Rollout.Name, sDef.Namespace)
			r.Log.Error(err, msg)
			return nil
		}
		if err != nil {
			return err
		}

		object.Spec.Template.Annotations[restartedAnnotation] = time.Now().UTC().Format(timeLayout)
		err = r.Update(r.Ctx, &object)
		if err != nil {
			return err
		}
	} else if strings.ToLower(sDef.Spec.Rollout.Kind) == "statefulset" {
		var object v1.StatefulSet
		err = r.Get(r.Ctx, clientObject, &object)
		if errors.IsNotFound(err) {
			r.Log.Error(err, fmt.Sprintf("%s/%s in namespace %s not found", sDef.Spec.Rollout.Kind, sDef.Spec.Rollout.Name, sDef.Namespace))
			return nil
		}
		if err != nil {
			return err
		}

		object.Spec.Template.Annotations[restartedAnnotation] = time.Now().UTC().Format(timeLayout)
		err = r.Update(r.Ctx, &object)
		if err != nil {
			return err
		}
	} else {
		return fmt.Errorf("%s kind is not supported", sDef.Spec.Rollout.Kind)
	}

	return nil
}
