// Package secrets stores API credentials in the OS keychain (macOS
// Keychain, Secret Service on Linux), with environment variables taking
// precedence so scripts and CI need no keychain.
package secrets

import (
	"errors"
	"os"

	"github.com/zalando/go-keyring"
)

const service = "ymlib"

// Secret names one credential.
type Secret struct {
	Key string // keychain account name
	Env string // environment variable that overrides the keychain
}

// DiscogsToken is the Discogs personal access token.
var DiscogsToken = Secret{Key: "discogs-token", Env: "YMLIB_DISCOGS_TOKEN"}

// Source says where a value came from.
type Source string

const (
	SourceNone     Source = ""
	SourceEnv      Source = "environment"
	SourceKeychain Source = "keychain"
)

// Get returns the secret from the environment or the keychain.
// A missing secret is not an error: the value is empty with SourceNone.
func Get(s Secret) (string, Source, error) {
	if v := os.Getenv(s.Env); v != "" {
		return v, SourceEnv, nil
	}
	v, err := keyring.Get(service, s.Key)
	if errors.Is(err, keyring.ErrNotFound) {
		return "", SourceNone, nil
	}
	if err != nil {
		return "", SourceNone, err
	}
	return v, SourceKeychain, nil
}

// Set stores the secret in the keychain.
func Set(s Secret, value string) error {
	if value == "" {
		return errors.New("empty value")
	}
	return keyring.Set(service, s.Key, value)
}

// Delete removes the secret from the keychain; missing is not an error.
func Delete(s Secret) error {
	err := keyring.Delete(service, s.Key)
	if errors.Is(err, keyring.ErrNotFound) {
		return nil
	}
	return err
}

// UseMockKeychain replaces the OS keychain with an in-memory one (tests).
func UseMockKeychain() { keyring.MockInit() }
