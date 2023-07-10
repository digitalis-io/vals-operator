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
package metrics

import "github.com/prometheus/client_golang/prometheus"

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
	VaultTokenError = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "vals_operator_vault_token_error",
			Help: "Timestamp if Vault token is invalid or expired",
		}, []string{"addr"})
	SecretRetrieveTime = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "vals_operator_secret_retrieve_time",
			Help: "Time in ms it took to get the secret",
		}, []string{"secret", "namespace"})
	SecretCreationTime = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "vals_operator_secret_creation_time",
			Help: "Time in ms it took to create the secret",
		}, []string{"secret", "namespace"})
	DbSecretRevokationError = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "vals_operator_dbsecret_revokation_error",
			Help: "Timestamp of when the lease could not be revoked",
		}, []string{"secret", "namespace"})
	DbSecretDeletionError = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "vals_operator_dbsecret_deletion_error",
			Help: "Timestamp of when the secret could not be deleted",
		}, []string{"secret", "namespace"})
)
