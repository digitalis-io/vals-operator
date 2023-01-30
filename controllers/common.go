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
