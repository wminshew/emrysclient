package cmd

import (
	"net/http"
)

func resolveClient() *http.Client {
	config := resolveTLSConfig()
	tr := &http.Transport{TLSClientConfig: config}
	return &http.Client{Transport: tr}
}
