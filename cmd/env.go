package cmd

import (
	"net/url"
	"os"
)

var (
	appEnv = os.Getenv("APP_ENV")
)

func resolveBase() *url.URL {
	var base *url.URL
	if appEnv == "dev" {
		// TODO: test different ports and http vs https
		base, _ = url.Parse("https://localhost:4430")
	} else {
		// TODO: need new certificate for this URL
		base, _ = url.Parse("https://wmdlserver.ddns.net:4430")
	}
	return base
}
