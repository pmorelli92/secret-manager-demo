package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"syscall"
)

func getEnv(name string, fallback string) string {
	value, ok := syscall.Getenv(name)
	if !ok {
		return fallback
	}
	return value
}

type SecretGetter struct {
	GoogleCloudProject string
}

func (sg SecretGetter) GetSecret(name string, fallback string) string {
	if sg.GoogleCloudProject == "" {
		return getEnv(name, fallback)
	}

	tokenUrl := "http://metadata.google.internal/computeMetadata/v1/instance/service-accounts/default/token"

	rq, err := http.NewRequest(http.MethodGet, tokenUrl, nil)
	if err != nil {
		fmt.Println(err)
		return fallback
	}

	rq.Header.Add("Metadata-Flavor", "Google")
	rs, err := http.DefaultClient.Do(rq)
	if err != nil {
		fmt.Println(err)
		return fallback
	}

	tokenResponse := struct {
		AccessToken string `json:"access_token"`
	}{}

	bytes, err := ioutil.ReadAll(rs.Body)
	if err != nil {
		fmt.Println(err)
		return fallback
	}

	err = json.Unmarshal(bytes, &tokenResponse)
	if err != nil {
		fmt.Println(err)
		return fallback
	}

	secretUrl := fmt.Sprintf(
		"https://content-secretmanager.googleapis.com/v1beta1/projects/%s/secrets/%s/versions/latest:access",
		sg.GoogleCloudProject, name)

	rq, err = http.NewRequest(http.MethodGet, secretUrl, nil)
	if err != nil {
		fmt.Println(err)
		return fallback
	}

	rq.Header.Add("Authorization", fmt.Sprintf("Bearer %s", tokenResponse.AccessToken))
	rs, err = http.DefaultClient.Do(rq)
	if err != nil {
		fmt.Println(err)
		return fallback
	}

	secretResponse := struct {
		Error   int    `json:"error"`
		Status  string `json:"status"`
		Payload struct {
			Data string `json:"data"`
		} `json:"payload"`
	}{}

	bytes, err = ioutil.ReadAll(rs.Body)
	if err != nil {
		fmt.Println(err)
		return fallback
	}

	err = json.Unmarshal(bytes, &secretResponse)
	if err != nil {
		fmt.Println(err)
		return fallback
	}

	if secretResponse.Error != 0 {
		fmt.Println(fmt.Sprintf("error %d - status %s", secretResponse.Error, secretResponse.Status))
		return fallback
	}

	data, err := base64.StdEncoding.DecodeString(secretResponse.Payload.Data)
	if err != nil {
		fmt.Println(err)
		return fallback
	}

	return string(data)
}

func main() {

	googleCloudProject := getEnv("GCP_PROJECT", "")
	secretGetter := SecretGetter{googleCloudProject}

	routes := http.NewServeMux()
	routes.HandleFunc("/get-secret", getSecretHandler(secretGetter))
	err := http.ListenAndServe(":8080", routes)
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}

func getSecretHandler(secretGetter SecretGetter) http.HandlerFunc {
	return func(w http.ResponseWriter, rq *http.Request) {
		if rq.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}

		secretName := rq.Header.Get("secret")
		if secretName == "" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		bytes, err := json.Marshal(struct {
			Name  string `json:"name"`
			Value string `json:"value"`
		}{
			Name:  secretName,
			Value: secretGetter.GetSecret(secretName, "default"),
		})
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(bytes)
	}
}
