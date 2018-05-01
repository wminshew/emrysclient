package cmd

import (
	"crypto/tls"
	"crypto/x509"
	"io/ioutil"
	"log"
)

func resolveTLSConfig() *tls.Config {
	if appEnv == "dev" {
		CAPool := x509.NewCertPool()
		serverCert, err := ioutil.ReadFile("./devCert.crt")
		if err != nil {
			log.Fatalf("Could not load dev certificate: %v\n", err)
		}
		CAPool.AppendCertsFromPEM(serverCert)
		return &tls.Config{RootCAs: CAPool}
	}
	return &tls.Config{}
}
