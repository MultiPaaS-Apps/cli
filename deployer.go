package main

import (
	"context"
	"net/url"
)

// deployer reports whether MULTIPAAS_CLIENT_ID and MULTIPAAS_CLIENT_SECRET name a deployer to act as; one without
// the other is refused, so a CI secret that failed to load never falls back to a developer's sign-in.
func (s *session) deployer() (bool, error) {
	if (s.env.clientID == "") != (s.env.clientSecret == "") {
		return false, &cliError{exit: exitRefused, Code: "DEPLOYER_CREDENTIALS_INCOMPLETE", Message: "set both MULTIPAAS_CLIENT_ID and MULTIPAAS_CLIENT_SECRET, or neither"}
	}
	return s.env.clientID != "", nil
}

// deployerAccessToken mints the deployer's token by the client-credentials grant on first use, and again when the
// server rejected it. It is kept in memory only.
func (s *session) deployerAccessToken(ctx context.Context, rejected string) (string, error) {
	if s.deployerToken != "" && s.deployerToken != rejected {
		return s.deployerToken, nil
	}
	form := url.Values{"grant_type": {"client_credentials"}, "client_id": {s.env.clientID}, "client_secret": {s.env.clientSecret}}
	var minted tokenResponse
	if err := s.postOAuth(ctx, "/oauth/token", form, &minted); err != nil {
		return "", err
	}
	s.deployerToken = minted.AccessToken
	return minted.AccessToken, nil
}

// refuseAsDeployer refuses login and logout while the CLI acts as a deployer.
func (s *session) refuseAsDeployer() error {
	deployer, err := s.deployer()
	if err != nil || !deployer {
		return err
	}
	return &cliError{exit: exitRefused, Code: "DEPLOYER", Message: "MULTIPAAS_CLIENT_ID is set, so the CLI acts as that deployer; unset MULTIPAAS_CLIENT_ID and MULTIPAAS_CLIENT_SECRET to sign in as yourself"}
}

// deployerScope is a deployer's named scope: the app-tenant when one is given, else the application.
func deployerScope(appID, tenantID string) string {
	if tenantID != "" {
		return "tenant:" + tenantID
	}
	return "app:" + appID
}
