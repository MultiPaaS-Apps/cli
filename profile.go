package main

import (
	"fmt"
	"regexp"
	"strings"
)

// profileName is what a profile may be called: safe to join into a credentials key, easy to read in a message.
var profileName = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,31}$`)

// chooseProfile is the developer sign-in a command runs as: --profile, else MULTIPAAS_PROFILE, else ""
// (the unnamed sign-in). An empty value counts as unset.
func chooseProfile(flag, fromEnv string) (string, error) {
	profile := flag
	if profile == "" {
		profile = fromEnv
	}
	if profile != "" && !profileName.MatchString(profile) {
		return "", &cliError{exit: exitRefused, Code: "INVALID_PROFILE", Message: fmt.Sprintf(`profile %q must be lowercase letters, digits, "-" or "_", up to 32 characters`, profile)}
	}
	return profile, nil
}

// credentialKey is where the session's sign-in is saved: the api origin, plus "#<profile>" for a named one.
// An origin never contains "#" (originsFor refuses fragments), so the two never collide.
func (s *session) credentialKey() string {
	if s.profile == "" {
		return s.origins.api
	}
	return s.origins.api + "#" + s.profile
}

// notSignedIn tells the developer how to sign in as the profile the command ran as.
func (s *session) notSignedIn() error {
	message := "not signed in; run: multipaas login"
	if s.profile != "" {
		message = fmt.Sprintf("not signed in with profile %q; run: multipaas login --profile %s", s.profile, s.profile)
	}
	return &cliError{exit: exitSignedOut, Code: "NOT_SIGNED_IN", Message: message}
}

// profileJSON is the profile as --json reports it: its name, or null for the unnamed sign-in.
func (s *session) profileJSON() *string {
	if s.profile == "" {
		return nil
	}
	return &s.profile
}

// profileSuffix is the profile as a human line ends with it: " (profile acme)", or nothing for the unnamed sign-in.
func (s *session) profileSuffix() string {
	if s.profile == "" {
		return ""
	}
	return " (profile " + s.profile + ")"
}

// withProfileHint adds to a NOT_FOUND which sign-in the command ran as. Any other failure, or one before a session existed, is unchanged.
func withProfileHint(s *session, err error) error {
	if s == nil || err == nil {
		return err
	}
	failure := asCLIError(err)
	if failure.Code != "NOT_FOUND" {
		return err
	}
	hint := s.profileHint()
	if hint == "" {
		return err
	}
	hinted := *failure
	hinted.Message += hint
	return &hinted
}

// profileHint names the profile the session ran as, or, with none chosen, says to choose one when this
// MultiPaaS has other profiles signed in. It reads the store without writing it and makes no server call.
func (s *session) profileHint() string {
	if s.profile != "" {
		return fmt.Sprintf(" (signed in with profile %q)", s.profile)
	}
	creds, err := loadCredentials()
	if err != nil {
		return ""
	}
	for key := range creds {
		if strings.HasPrefix(key, s.origins.api+"#") {
			return " (no profile chosen: set MULTIPAAS_PROFILE or pass --profile)"
		}
	}
	return ""
}
