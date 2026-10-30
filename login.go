package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

// developerClientID is the first-party developer OAuth client. A public client: the id is not a secret.
const developerClientID = "4b0285b8-29b9-468f-b31d-333f0bb128c8"

// signInTimeout bounds how long login waits for the browser to come back.
var signInTimeout = 5 * time.Minute

// session is one command's view of one MultiPaaS as one developer: its api origin and profile key the saved credentials.
type session struct {
	origins origins
	profile string // "" = the unnamed sign-in
	env     environment
	http    *http.Client
}

// login signs in through the browser: a PKCE authorization code delivered to a loopback listener on
// any free port (RFC 8252 §7.3), exchanged for developer tokens and saved for the api origin.
func (s *session) login(ctx context.Context) error {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	redirectURI := fmt.Sprintf("http://%s/callback", listener.Addr())
	verifier, state := randomToken(), randomToken()
	authorize := s.origins.api + "/oauth/authorize?" + url.Values{
		"response_type":         {"code"},
		"client_id":             {developerClientID},
		"redirect_uri":          {redirectURI},
		"code_challenge":        {challengeFor(verifier)},
		"code_challenge_method": {"S256"},
		"state":                 {state},
	}.Encode()
	fmt.Fprintf(s.env.stderr, "Sign in to MultiPaaS (%s) in your browser. If it did not open, visit:\n%s\n", s.origins.api, authorize)
	_ = s.env.openBrowser(authorize)

	code, err := awaitCallback(ctx, listener, state, s.loginCommand())
	if err != nil {
		return err
	}
	var tokens tokenResponse
	form := url.Values{"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {redirectURI}, "code_verifier": {verifier}}
	if err := s.postForm(ctx, "/oauth/token", form, &tokens); err != nil {
		return err
	}
	return updateCredentials(func(creds credentialFile) error {
		creds[s.credentialKey()] = tokens.credential()
		return nil
	})
}

// logout forgets the saved tokens and asks the server to revoke their chain. The tokens are forgotten
// even when the server can't be reached, and then the error says the chain was not revoked.
// Not signed in is not an error.
func (s *session) logout(ctx context.Context) (bool, error) {
	signedOut := false
	var revokeErr error
	err := updateCredentials(func(creds credentialFile) error {
		current, ok := creds[s.credentialKey()]
		if !ok {
			return nil
		}
		revokeErr = s.postForm(ctx, "/oauth/revoke", url.Values{"token": {current.RefreshToken}}, nil)
		delete(creds, s.credentialKey())
		signedOut = true
		return nil
	})
	if err == nil && revokeErr != nil {
		err = &cliError{exit: exitRefused, Code: "REVOKE_FAILED", Message: "signed out on this machine, but the server did not confirm revoking the tokens (" + revokeErr.Error() + "); they lapse on their own"}
	}
	return signedOut, err
}

// refresh rotates a refresh token into a new pair.
func (s *session) refresh(ctx context.Context, refreshToken string) (credential, error) {
	var tokens tokenResponse
	err := s.postForm(ctx, "/oauth/token", url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refreshToken}}, &tokens)
	return tokens.credential(), err
}

// awaitCallback serves the loopback redirect until one carries this sign-in's state, and returns its code.
func awaitCallback(ctx context.Context, listener net.Listener, state, loginCommand string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, signInTimeout)
	defer cancel()
	type callback struct {
		code string
		err  error
	}
	callbacks := make(chan callback, 1)
	server := &http.Server{ReadHeaderTimeout: 10 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query()
		if r.URL.Path != "/callback" || query.Get("state") != state {
			http.NotFound(w, r)
			return
		}
		got := callback{code: query.Get("code")}
		page := "Signed in to MultiPaaS. You can close this tab."
		if refusal := query.Get("error"); refusal != "" || got.code == "" {
			got = callback{err: &cliError{exit: exitSignedOut, Code: "SIGN_IN_REFUSED", Message: "sign-in was not completed: " + refusal}}
			page = "Sign-in was not completed. You can close this tab."
		}
		fmt.Fprintln(w, page)
		select {
		case callbacks <- got:
		default:
		}
	})}
	go func() { _ = server.Serve(listener) }()
	defer func() {
		shutdown, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		_ = server.Shutdown(shutdown)
	}()

	select {
	case got := <-callbacks:
		return got.code, got.err
	case <-ctx.Done():
		return "", &cliError{exit: exitSignedOut, Code: "SIGN_IN_TIMEOUT", Message: "sign-in timed out; run " + loginCommand + " again"}
	}
}

// postForm posts an OAuth form as the developer client and decodes the JSON answer into into (nil discards it).
func (s *session) postForm(ctx context.Context, path string, form url.Values, into any) error {
	form.Set("client_id", developerClientID)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, s.origins.api+path, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := s.http.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		refusal := &oauthError{}
		if json.NewDecoder(response.Body).Decode(refusal) != nil || refusal.Code == "" {
			return fmt.Errorf("%s answered HTTP %d", path, response.StatusCode)
		}
		return refusal
	}
	if into == nil {
		return nil
	}
	return json.NewDecoder(response.Body).Decode(into)
}

// oauthError is the OAuth server's {error, error_description} refusal.
type oauthError struct {
	Code        string `json:"error"`
	Description string `json:"error_description"`
}

func (e *oauthError) Error() string { return e.Code + ": " + e.Description }

type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
}

func (t tokenResponse) credential() credential {
	return credential{
		AccessToken:  t.AccessToken,
		RefreshToken: t.RefreshToken,
		ExpiresAt:    time.Now().Add(time.Duration(t.ExpiresIn) * time.Second).UTC(),
	}
}

// openBrowser asks the OS to open url. login prints the URL first, so a failure here is not fatal.
func openBrowser(url string) error {
	opener := "xdg-open"
	if runtime.GOOS == "darwin" {
		opener = "open"
	}
	return exec.Command(opener, url).Start()
}

// randomToken is 32 random bytes, base64url: a PKCE verifier (RFC 7636 §4.1) or a state.
func randomToken() string {
	buf := make([]byte, 32)
	_, _ = rand.Read(buf) // crypto/rand.Read never returns an error.
	return base64.RawURLEncoding.EncodeToString(buf)
}

func challengeFor(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}
