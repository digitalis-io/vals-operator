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

	SecretInfo = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "vals_operator_secret_info",
			Help: "Tracks secret, timestamp is when it was last updated",
		}, []string{"secret", "namespace"})
	DbSecretInfo = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "vals_operator_dbsecret_info",
			Help: "Tracks database secret, timestamp is when it was last updated",
		}, []string{"secret", "namespace"})
	DbSecretExpireTime = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "vals_operator_dbsecret_expire_time",
			Help: "Reports if the when the secret expired last",
		}, []string{"secret", "namespace"})
	VaultError = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "vals_operator_vault_error",
			Help: "Timestamp if Vault backend is used and fails",
		}, []string{"addr"})
)
