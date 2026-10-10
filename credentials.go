package main

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// credential is one MultiPaaS's developer tokens.
type credential struct {
	AccessToken  string    `json:"accessToken"`
	RefreshToken string    `json:"refreshToken"`
	ExpiresAt    time.Time `json:"expiresAt"`
}

// credentialFile is ~/.config/multipaas/credentials.json: one credential per api origin, readable by the user only.
type credentialFile map[string]credential

// refreshMargin refreshes an access token this long before it expires, so it never expires in flight.
const refreshMargin = time.Minute

// token returns the access token, refreshing first when it is near expiry or is the one the
// server just rejected. A refresh the server refuses means the chain is dead: the entry is removed.
func (s *session) token(ctx context.Context, rejected string) (string, error) {
	token := ""
	err := updateCredentials(func(creds credentialFile) error {
		current, ok := creds[s.origins.api]
		if !ok {
			return nil
		}
		if current.AccessToken != rejected && time.Until(current.ExpiresAt) > refreshMargin {
			token = current.AccessToken
			return nil
		}
		refreshed, err := s.refresh(ctx, current.RefreshToken)
		var refusal *oauthError
		if errors.As(err, &refusal) && refusal.Code == "invalid_grant" {
			delete(creds, s.origins.api)
			return nil
		}
		if err != nil {
			return err
		}
		creds[s.origins.api] = refreshed
		token = refreshed.AccessToken
		return nil
	})
	if err == nil && token == "" {
		return "", &cliError{exit: exitSignedOut, Code: "NOT_SIGNED_IN", Message: "not signed in; run: multipaas login"}
	}
	return token, err
}

// updateCredentials reads the file, applies change, and writes it back, holding an exclusive lock
// throughout. The lock matters: two commands refreshing the same token at once would present it
// twice, and the server revokes the whole chain on reuse.
func updateCredentials(change func(credentialFile) error) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	dir := filepath.Join(home, ".config", "multipaas")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	lock, err := os.OpenFile(filepath.Join(dir, "credentials.lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}

	path := filepath.Join(dir, "credentials.json")
	creds := credentialFile{}
	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if len(data) > 0 {
		if err := json.Unmarshal(data, &creds); err != nil {
			return err
		}
	}
	if err := change(creds); err != nil {
		return err
	}
	return writeFileAtomically(path, creds)
}

// writeFileAtomically writes through a 0600 temp file and a rename, so a crash never leaves half a file.
func writeFileAtomically(path string, creds credentialFile) error {
	data, err := json.MarshalIndent(creds, "", "  ")
	if err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".credentials-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(temp.Name()) }()
	if _, err := temp.Write(data); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(temp.Name(), path)
}
