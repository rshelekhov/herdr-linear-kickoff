// Package secret stores the Linear API keys, one per workspace: in the OS
// keyring (macOS Keychain, Linux Secret Service), or a 0600 file when there
// is no keyring.
package secret

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/zalando/go-keyring"
)

const (
	service = "herdr-linear-kickoff"
	legacy  = "linear-api-key" // the one key saved before workspaces
)

// Store keeps the keys; Dir is the plugin config dir for the file fallback.
type Store struct{ Dir string }

// A workspace ID is Linear's organization UUID: safe in a file name.
var reWS = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

func account(ws string) string { return "workspace-" + ws }

func (s Store) file(name string) string { return filepath.Join(s.Dir, name) }

// Get returns workspace ws's saved key and where it is: "keyring" or "file".
func (s Store) Get(ws string) (string, string) {
	if !reWS.MatchString(ws) {
		return "", ""
	}
	return s.get(account(ws), "api-key-"+ws)
}

// Set saves workspace ws's key in the keyring, or in a 0600 file if that fails.
func (s Store) Set(ws, key string) (string, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return "", errors.New("empty key")
	}
	if !reWS.MatchString(ws) {
		return "", errors.New("bad workspace ID")
	}
	if err := keyring.Set(service, account(ws), key); err == nil {
		_ = os.Remove(s.file("api-key-" + ws))
		return "keyring", nil
	}
	if err := os.MkdirAll(s.Dir, 0o700); err != nil {
		return "", err
	}
	f := s.file("api-key-" + ws)
	if err := os.WriteFile(f+".tmp", []byte(key+"\n"), 0o600); err != nil {
		return "", err
	}
	return "file", os.Rename(f+".tmp", f)
}

// Delete forgets workspace ws's key.
func (s Store) Delete(ws string) error {
	if !reWS.MatchString(ws) {
		return nil
	}
	return s.delete(account(ws), "api-key-"+ws)
}

// Legacy is the key saved before there were workspaces, else LINEAR_API_KEY
// from the environment; the source is "keyring", "file", "env" or "".
func (s Store) Legacy() (string, string) {
	if k, src := s.get(legacy, "api-key"); k != "" {
		return k, src
	}
	if k := strings.TrimSpace(os.Getenv("LINEAR_API_KEY")); k != "" {
		return k, "env"
	}
	return "", ""
}

// DeleteLegacy forgets the pre-workspace key once it's saved per workspace.
func (s Store) DeleteLegacy() error { return s.delete(legacy, "api-key") }

func (s Store) get(acct, file string) (string, string) {
	if k, err := keyring.Get(service, acct); err == nil && strings.TrimSpace(k) != "" {
		return strings.TrimSpace(k), "keyring"
	}
	if b, err := os.ReadFile(s.file(file)); err == nil && strings.TrimSpace(string(b)) != "" {
		return strings.TrimSpace(string(b)), "file"
	}
	return "", ""
}

// delete removes what there is; nothing to remove isn't an error, also where
// there is no keyring at all (no Secret Service).
func (s Store) delete(acct, file string) error {
	var kerr error
	if _, err := keyring.Get(service, acct); err == nil {
		if kerr = keyring.Delete(service, acct); errors.Is(kerr, keyring.ErrNotFound) {
			kerr = nil
		}
	}
	ferr := os.Remove(s.file(file))
	if os.IsNotExist(ferr) {
		ferr = nil
	}
	return errors.Join(kerr, ferr)
}
