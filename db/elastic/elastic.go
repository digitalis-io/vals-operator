package elastic

import (
	"bytes"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	dbType "digitalis.io/vals-operator/db/types"
	ctrl "sigs.k8s.io/controller-runtime"
)

func basicAuth(username, password string) string {
	auth := username + ":" + password
	return base64.StdEncoding.EncodeToString([]byte(auth))
}

func UpdateUserPassword(dbQuery dbType.DatabaseBackend) error {
	var err error
	log := ctrl.Log.WithName("elastic")

	/* Default user */
	if dbQuery.LoginUsername == "" {
		dbQuery.LoginUsername = "elastic"
	}

	payload := map[string]string{"password": dbQuery.Password}
	json_data, err := json.Marshal(payload)

	for _, host := range dbQuery.Hosts {
		var url string
		var client *http.Client
		if strings.HasPrefix(host, "https://") || strings.HasPrefix(host, "http://") {
			url = fmt.Sprintf("%s/_security/user/%s/_password?pretty", host, dbQuery.Username)
		} else {
			url = fmt.Sprintf("http://%s:%d/_security/user/%s/_password", host, dbQuery.Port, dbQuery.Username)
		}

		// FIXME: we don't yet have support for SSL certs
		if strings.HasPrefix(url, "https://") {
			tr := &http.Transport{
				TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
			}
			client = &http.Client{Transport: tr}
		} else {
			client = &http.Client{}
		}

		req, err := http.NewRequest("POST", url, bytes.NewBuffer(json_data))
		if err != nil {
			log.Error(err, "Something went wrong initializing http client")
			return err
		}
		req.Header.Add("Authorization", "Basic "+basicAuth(dbQuery.LoginUsername, dbQuery.LoginPassword))
		req.Header.Add("Content-type", "application/json")
		resp, err := client.Do(req)

		if err != nil {
			log.Error(err, fmt.Sprintf("Cannot update password on %s", host))
			log.Error(err, fmt.Sprintf("%v", resp.Body))
		} else if resp.StatusCode != 200 {
			log.Error(err, fmt.Sprintf("ElasticSearch on %s returned error code %d", url, resp.StatusCode))
		} else {
			log.Info("ElasticSearch password successfully updated")
			return nil
		}
	}

	log.Error(err, "ElasticSearch password not updated")
	return err
}
