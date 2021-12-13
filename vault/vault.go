package vault

import (
	"crypto/tls"
	"errors"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"time"

	"github.com/go-logr/logr"
	"github.com/hashicorp/vault/api"
	ctrl "sigs.k8s.io/controller-runtime"
)

const (
	kubernetesJwtTokenPath = "/var/run/secrets/kubernetes.io/serviceaccount/token"
	kubernetesAuthUrl      = "auth/kubernetes/login"
)

var log logr.Logger

func fileExists(name string) (bool, error) {
	_, err := os.Stat(name)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return false, err
}

func vaultClient() (*api.Client, error) {
	var vaultSkipVerify bool = false

	vaultUrl := os.Getenv("VAULT_ADDR")
	if os.Getenv("VAULT_SKIP_VERIFY") != "" && os.Getenv("VAULT_SKIP_VERIFY") == "true" {
		vaultSkipVerify = true
	}
	if vaultUrl == "" {
		return nil, fmt.Errorf("VAULT_ADDR is not set")
	}

	tr := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: vaultSkipVerify},
	}

	httpClient := &http.Client{Transport: tr}

	// create a vault client
	return api.NewClient(&api.Config{Address: vaultUrl, HttpClient: httpClient})
}

func renewToken() (float64, error) {
	var tokenTTL float64
	client, err := vaultClient()
	if err != nil {
		return -1, err
	}

	r, err := client.Logical().Read("auth/token/lookup-self")
	if err != nil {
		return -1, err
	}

	t, err := r.TokenTTL()
	if err != nil {
		return -1, err
	}
	tokenTTL = t.Seconds()

	/* FIXME: is this a sensible minimum? */
	if tokenTTL > 120 {
		return tokenTTL, nil
	}

	if ok, err := r.TokenIsRenewable(); !ok {
		return tokenTTL, err
	}

	r, err = client.Logical().Write("/auth/token/renew-self", map[string]interface{}{
		"token": os.Getenv("VAULT_TOKEN"),
	})
	if err != nil {
		return tokenTTL, err
	}

	tokenTTL = float64(r.Auth.LeaseDuration)
	log.Info(fmt.Sprintf("Vault token renewed. New TTL is %v seconds", tokenTTL))

	return tokenTTL, nil
}

func kubeLogin() error {
	if ok, err := fileExists(kubernetesJwtTokenPath); !ok {
		return err
	}
	if os.Getenv("VAULT_ROLE_ID") == "" {
		return fmt.Errorf("VAULT_ROLE_ID not defined")
	}
	client, err := vaultClient()
	if err != nil {
		return err
	}
	fd, err := os.Open(kubernetesJwtTokenPath)
	if err != nil {
		return err
	}
	defer fd.Close()

	jwt, err := ioutil.ReadAll(fd)
	if err != nil {
		return err
	}

	params := map[string]interface{}{
		"jwt":  string(jwt),
		"role": os.Getenv("VAULT_ROLE_ID"),
	}

	var loginUrl string
	if os.Getenv("VAULT_KUBERNETES_PATH") == "" {
		loginUrl = kubernetesAuthUrl
	} else {
		loginUrl = fmt.Sprintf("auth/%s/login", os.Getenv("VAULT_KUBERNETES_PATH"))
	}

	r, err := client.Logical().Write(loginUrl, params)
	if err != nil {
		return err
	}

	client.SetToken(r.Auth.ClientToken)

	err = os.Setenv("VAULT_TOKEN", r.Auth.ClientToken)
	if err != nil {
		return err
	}

	return nil
}

func Start() error {
	log = ctrl.Log.WithName("vault")

	if os.Getenv("VAULT_AUTH_METHOD") == "kubernetes" {
		err := kubeLogin()
		if err != nil {
			return err
		}
	}
	go func() {
		for {
			tokenTTL, err := renewToken()
			if err != nil {
				log.Error(err, "Error renewing vault token")
				time.Sleep(time.Second * 60)
				continue
			}

			/* Wait for near the time when token expires */
			if tokenTTL > 0 {
				var sleepTime float64 = tokenTTL
				if tokenTTL > 120 {
					sleepTime = sleepTime - 120
				}
				time.Sleep(time.Second * time.Duration(sleepTime))
			} else if tokenTTL == 0 {
				log.Info("Vault token TTL is 0. It is a root token or set to not expire.")
				return
			} else {
				time.Sleep(time.Second * 60)
			}
		}
	}()

	return nil
}
