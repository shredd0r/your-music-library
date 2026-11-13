package secrets

import (
	"testing"

	"github.com/zalando/go-keyring"
)

func TestGet(t *testing.T) {
	UseMockKeychain()
	t.Setenv(DiscogsToken.Env, "")

	if v, src, err := Get(DiscogsToken); err != nil || v != "" || src != SourceNone {
		t.Fatalf("empty: %q %q %v", v, src, err)
	}
	// ymlib never writes the keychain; the user adds the token with
	// `security add-generic-password -s ymlib -a discogs-token -w <token>`.
	if err := keyring.Set(service, DiscogsToken.Key, "abc"); err != nil {
		t.Fatal(err)
	}
	if v, src, _ := Get(DiscogsToken); v != "abc" || src != SourceKeychain {
		t.Errorf("keychain: %q %q", v, src)
	}
	t.Setenv(DiscogsToken.Env, "from-env")
	if v, src, _ := Get(DiscogsToken); v != "from-env" || src != SourceEnv {
		t.Errorf("env should win: %q %q", v, src)
	}
}
