package main

import (
	"fmt"
	"net/url"
	"slices"
	"strings"
)

// defaultAPIURL is the OAuth server origin unless MULTIPAAS_API_URL says otherwise.
const defaultAPIURL = "https://api.multipaas.com"

// origins are the OAuth server (api) and webapp-api hosts.
type origins struct {
	api       string
	webappAPI string
}

// originsFor takes the OAuth server's origin (empty means the default) and derives webapp-api's by
// swapping the host's leading "api." label for "webapp-api.". Only https://api.<labels>, with no port
// and at most one trailing "/", is accepted, so a token can only go to an api host.
func originsFor(apiURL string) (origins, error) {
	if apiURL == "" {
		apiURL = defaultAPIURL
	}
	parsed, err := url.Parse(apiURL)
	if err != nil || parsed.Scheme != "https" || parsed.Opaque != "" || parsed.User != nil ||
		(parsed.Path != "" && parsed.Path != "/") || parsed.RawQuery != "" || parsed.ForceQuery || strings.Contains(apiURL, "#") ||
		parsed.Host != parsed.Hostname() || !strings.HasPrefix(parsed.Host, "api.") || slices.Contains(strings.Split(parsed.Host, "."), "") {
		return origins{}, &cliError{exit: exitRefused, Code: "INVALID_API_URL", Message: fmt.Sprintf("MULTIPAAS_API_URL %q must be https://api.<host> with no port, path, query or fragment", apiURL)}
	}
	return origins{api: "https://" + parsed.Host, webappAPI: "https://webapp-api." + strings.TrimPrefix(parsed.Host, "api.")}, nil
}
