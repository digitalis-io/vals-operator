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
