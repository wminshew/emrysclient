package cmd

import (
	"crypto/tls"
	"crypto/x509"
	"io/ioutil"
	"log"
	"net/http"
)

func resolveClient() *http.Client {
	if appEnv == "dev" {
		CA_Pool := x509.NewCertPool()
		serverCert, err := ioutil.ReadFile("./devCert.crt")
		if err != nil {
			log.Fatalf("Could not load dev certificate: %v\n", err)
		}
		CA_Pool.AppendCertsFromPEM(serverCert)
		config := &tls.Config{RootCAs: CA_Pool}
		tr := &http.Transport{TLSClientConfig: config}
		return &http.Client{Transport: tr}
	} else {
		return &http.Client{}
	}
}
