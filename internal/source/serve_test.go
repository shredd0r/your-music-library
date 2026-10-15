package source

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// served returns the fake plugin in protocol 2 mode; state is where it
// marks a clean close.
func served(t *testing.T) (p *Plugin, state string) {
	t.Helper()
	t.Setenv("YMLIB_FAKE_SERVE", "1")
	state = t.TempDir()
	t.Setenv("YMLIB_FAKE_STATE", state)
	p = fake(t)
	t.Cleanup(p.Close)
	return p, state
}

// whoami returns the plugin's process id and how many requests that
// process has served.
func whoami(t *testing.T, p *Plugin) (pid, served string) {
	t.Helper()
	res, err := p.Search(context.Background(), "whoami", KindTrack, 1)
	if err != nil || len(res) != 1 {
		t.Fatalf("whoami = %+v, %v", res, err)
	}
	return res[0].ID, res[0].Title
}

func setTimeouts(t *testing.T, idle, closing time.Duration) {
	t.Helper()
	oldIdle, oldClose := IdleTimeout, CloseTimeout
	IdleTimeout, CloseTimeout = idle, closing
	t.Cleanup(func() { IdleTimeout, CloseTimeout = oldIdle, oldClose })
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		if cond() {
			return
		}
	}
	t.Fatalf("timed out waiting for %s", what)
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func TestServeKeepsOneProcess(t *testing.T) {
	p, state := served(t)
	ctx := context.Background()
	if info, err := p.Info(ctx); err != nil || info.Protocol != 2 {
		t.Fatalf("Info = %+v, %v", info, err)
	}
	pid, n := whoami(t, p)
	if pid2, n2 := whoami(t, p); pid2 != pid || n != "1" || n2 != "2" {
		t.Errorf("second request: pid %s→%s, served %s→%s; want one process keeping its count", pid, pid2, n, n2)
	}

	// the protocol 1 commands work unchanged over the connection
	if _, tracks, err := p.Resolve(ctx, "a1", KindAlbum); err != nil || len(tracks) != 2 {
		t.Errorf("Resolve = %+v, %v", tracks, err)
	}
	if _, _, err := p.Resolve(ctx, "nope", KindAlbum); err == nil || !strings.Contains(err.Error(), "fake resolve: unknown id nope") {
		t.Errorf("error answer: %v", err)
	}
	dest := t.TempDir()
	if f, err := p.Download(ctx, "t1", dest); err != nil || filepath.Base(f) != "t1.flac" {
		t.Errorf("Download = %q, %v", f, err)
	}
	if _, err := p.Download(ctx, "a1", dest); err == nil || !strings.Contains(err.Error(), "cannot download a1") {
		t.Errorf("failed download: %v", err)
	}

	p.Close()
	if !exists(filepath.Join(state, "closed-"+pid)) {
		t.Error("plugin did not get close")
	}
	if pid2, n2 := whoami(t, p); pid2 == pid || n2 != "1" {
		t.Errorf("after Close: pid %s, served %s; want a fresh process", pid2, n2)
	}
}

func TestServeConcurrentRequests(t *testing.T) {
	p, _ := served(t)
	pid, _ := whoami(t, p)
	done := make(chan string, 2)
	go func() {
		_, err := p.Search(context.Background(), "slow", KindTrack, 1)
		done <- "slow " + errString(err)
	}()
	time.Sleep(50 * time.Millisecond) // let the slow request go first
	go func() {
		_, err := p.Search(context.Background(), "rock", KindTrack, 1)
		done <- "fast " + errString(err)
	}()
	if first, second := <-done, <-done; first != "fast ok" || second != "slow ok" {
		t.Errorf("answers in order %q, %q; want the fast one first", first, second)
	}
	if pid2, n := whoami(t, p); pid2 != pid || n != "4" {
		t.Errorf("pid %s→%s, served %s: requests did not share one process", pid, pid2, n)
	}
}

func errString(err error) string {
	if err != nil {
		return err.Error()
	}
	return "ok"
}

func TestServeIdleClose(t *testing.T) {
	setTimeouts(t, 50*time.Millisecond, time.Second)
	p, state := served(t)
	pid, _ := whoami(t, p)
	waitFor(t, "idle close", func() bool { return exists(filepath.Join(state, "closed-"+pid)) })
	if pid2, _ := whoami(t, p); pid2 == pid {
		t.Error("request after idle close used the old process")
	}
}

func TestServeKillsPluginIgnoringClose(t *testing.T) {
	setTimeouts(t, 0, 100*time.Millisecond)
	t.Setenv("YMLIB_FAKE_IGNORE_CLOSE", "1")
	p, _ := served(t)
	pid, _ := whoami(t, p)
	start := time.Now()
	p.Close()
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("Close took %s", d)
	}
	var n int
	for _, c := range pid {
		n = n*10 + int(c-'0')
	}
	if err := syscall.Kill(n, 0); err == nil {
		t.Errorf("process %d still running", n)
	}
}

func TestServeCrashRestarts(t *testing.T) {
	p, _ := served(t)
	pid, _ := whoami(t, p)
	_, err := p.Search(context.Background(), "crash", KindTrack, 1)
	if err == nil || !strings.Contains(err.Error(), "fake search: plugin exited: fake crashed") {
		t.Fatalf("crash error = %v", err)
	}
	if pid2, n := whoami(t, p); pid2 == pid || n != "1" {
		t.Errorf("after a crash: pid %s, served %s; want a new process", pid2, n)
	}
}

func TestProtocol1StartsPerRequest(t *testing.T) {
	p := fake(t)
	pid, _ := whoami(t, p)
	if pid2, _ := whoami(t, p); pid2 == pid {
		t.Error("protocol 1 plugin reused a process")
	}
	p.Close() // nothing running: a no-op
}

func TestRegistryTimeouts(t *testing.T) {
	p, state := served(t) // defaults: idle 5 min
	NewRegistry([]*Plugin{p}).SetTimeouts(50*time.Millisecond, time.Second)
	pid, _ := whoami(t, p)
	waitFor(t, "idle close with the configured timeout", func() bool { return exists(filepath.Join(state, "closed-"+pid)) })
}

func TestRegistryClose(t *testing.T) {
	p, state := served(t)
	pid, _ := whoami(t, p)
	NewRegistry([]*Plugin{p}).Close()
	if !exists(filepath.Join(state, "closed-"+pid)) {
		t.Error("Registry.Close did not close the plugin")
	}
}
