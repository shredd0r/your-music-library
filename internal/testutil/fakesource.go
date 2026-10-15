package testutil

import (
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
)

var (
	fakeOnce sync.Once
	fakeDir  string
	fakeErr  error
)

// FakeSourceDir builds the test source plugin (internal/source/testdata/
// fakesource) once per test binary and returns a sources directory that
// contains it as "fake". It also points the plugin at the audio fixtures.
func FakeSourceDir(t testing.TB) string {
	t.Helper()
	fakeOnce.Do(func() {
		fakeDir, fakeErr = os.MkdirTemp("", "ymlib-fakesource-")
		if fakeErr != nil {
			return
		}
		root := filepath.Join(fixtureDir(), "..", "..", "..")
		cmd := exec.Command("go", "build", "-o", filepath.Join(fakeDir, "fake"), "./internal/source/testdata/fakesource")
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			fakeErr = &buildError{err: err, out: string(out)}
		}
	})
	if fakeErr != nil {
		t.Fatalf("building fake source plugin: %v", fakeErr)
	}
	t.Setenv("YMLIB_FAKE_AUDIO", fixtureDir())
	return fakeDir
}

type buildError struct {
	err error
	out string
}

func (e *buildError) Error() string { return e.err.Error() + "\n" + e.out }
