package cmd

import (
	"os"
)

var (
	appEnv = os.Getenv("APP_ENV")
)

//
// func resolveHost() string {
// if appEnv == "dev" {
// 	return "localhost:4430"
// }
// return "wmdlserver.ddns.net:4430"
// }
