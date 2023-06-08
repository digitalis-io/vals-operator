package controllers

import "github.com/prometheus/client_golang/prometheus"

const (
	leaseIdLabel               = "vals-operator.digitalis.io/lease-id"
	leaseDurationLabel         = "vals-operator.digitalis.io/lease-duration"
	expiresOnLabel             = "vals-operator.digitalis.io/expires-on"
	restartedAnnotation        = "vals-operator.digitalis.io/restartedAt"
	timeLayout                 = "2006-01-02T15.04.05Z"
	lastUpdatedAnnotation      = "vals-operator.digitalis.io/last-updated"
	recordingEnabledAnnotation = "vals-operator.digitalis.io/record"
	forceCreateAnnotation      = "vals-operator.digitalis.io/force"
	templateHash               = "vals-operator.digitalis.io/hash"
	managedByLabel             = "app.kubernetes.io/managed-by"
	k8sSecretPrefix            = "ref+k8s://"
)

var (
	SecretFailures = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "vals_operator_secret_failures",
			Help: "Number of errors generating secrets",
		},
	)
	DbSecretFailures = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "vals_operator_dbsecret_failures",
			Help: "Number of errors generating DB secrets",
		},
	)
	SecretError = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "vals_operator_secret_error",
			Help: "Reports timestamp from when a secret last failed to be updated",
		}, []string{"secret", "namespace"})
	DbSecretError = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "vals_operator_dbsecret_error",
			Help: "Reports timestamp from when a DB secret last failed to be updated",
		}, []string{"secret", "namespace"})
)
