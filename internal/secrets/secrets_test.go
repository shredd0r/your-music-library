package secrets

import "testing"

func TestGetSetDelete(t *testing.T) {
	UseMockKeychain()
	t.Setenv(DiscogsToken.Env, "")

	if v, src, err := Get(DiscogsToken); err != nil || v != "" || src != SourceNone {
		t.Fatalf("empty: %q %q %v", v, src, err)
	}
	if err := Set(DiscogsToken, "abc"); err != nil {
		t.Fatal(err)
	}
	if v, src, _ := Get(DiscogsToken); v != "abc" || src != SourceKeychain {
		t.Errorf("keychain: %q %q", v, src)
	}
	t.Setenv(DiscogsToken.Env, "from-env")
	if v, src, _ := Get(DiscogsToken); v != "from-env" || src != SourceEnv {
		t.Errorf("env should win: %q %q", v, src)
	}
	t.Setenv(DiscogsToken.Env, "")
	if err := Delete(DiscogsToken); err != nil {
		t.Fatal(err)
	}
	if err := Delete(DiscogsToken); err != nil {
		t.Errorf("deleting twice: %v", err)
	}
	if v, _, _ := Get(DiscogsToken); v != "" {
		t.Errorf("after delete: %q", v)
	}
	if err := Set(DiscogsToken, ""); err == nil {
		t.Error("empty token accepted")
	}
}
