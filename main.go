// Command multipaas signs a developer in to MultiPaaS and runs the webapp-api operations the web UI uses.
package main

import "os"

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

func main() {
	os.Exit(run(os.Args[1:], environment{
		stdout:      os.Stdout,
		stderr:      os.Stderr,
		version:     version,
		apiURL:      os.Getenv("MULTIPAAS_API_URL"),
		origins:     originsFor,
		openBrowser: openBrowser,
	}))
}
