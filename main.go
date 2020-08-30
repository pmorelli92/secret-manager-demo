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

// getEnv returns the value for an environment value, or a fallback if not found
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

// GetSecret gets a secret either from environment variable or from GCP Secret Manager
func (sg SecretGetter) GetSecret(name string, fallback string) string {
	// If GCP project is not present, get value from environment variables
	if sg.GoogleCloudProject == "" {
		return getEnv(name, fallback)
	}

	// Get the token for the service account that runs the node pool
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

	// Get the secret value using the access_token that we fetched above
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

	// In case there is an error because of privileges or oauth scopes, return the fallback
	if secretResponse.Error != 0 {
		fmt.Println(fmt.Sprintf("error %d - status %s", secretResponse.Error, secretResponse.Status))
		return fallback
	}

	// Secret Manager returns the secret on base64
	data, err := base64.StdEncoding.DecodeString(secretResponse.Payload.Data)
	if err != nil {
		fmt.Println(err)
		return fallback
	}

	return string(data)
}

func main() {

	// Get GCP Project to know if we use environment variables or Secret Manager
	googleCloudProject := getEnv("GCP_PROJECT", "")
	secretGetter := SecretGetter{googleCloudProject}

	// Set up the HTTP server for getting secrets
	routes := http.NewServeMux()
	routes.HandleFunc("/get-secret", getSecretHandler(secretGetter))
	err := http.ListenAndServe(":8080", routes)
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}

// getSecretHandler gets the secret value according to the name sent on the header
func getSecretHandler(secretGetter SecretGetter) http.HandlerFunc {
	return func(w http.ResponseWriter, rq *http.Request) {
		// Only work with GET requests
		if rq.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}

		// Fetch the secret name on the header
		secretName := rq.Header.Get("secret")
		if secretName == "" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		// Create the struct definition for the response
		bytes, err := json.Marshal(struct {
			Name  string `json:"name"`
			Value string `json:"value"`
		}{
			Name: secretName,
			// Use the secret getter to get the secret or the fallback
			Value: secretGetter.GetSecret(secretName, fmt.Sprintf("default-for-%s", secretName)),
		})
		if err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		// Return the secret value
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(bytes)
	}
}
