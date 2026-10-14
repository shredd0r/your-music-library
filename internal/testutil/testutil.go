// Package testutil provides fixtures shared by tests in several packages.
package testutil

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// fixtureDir is resolved from this file's location so tests in any package
// can find the shared audio fixtures.
func fixtureDir() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "testdata")
}

// CopyAudio copies the tiny untagged fixture for ext ("flac" or "mp3") to
// dst, creating parent directories, and returns dst.
func CopyAudio(t testing.TB, ext, dst string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(fixtureDir(), "tiny."+ext))
	if err != nil {
		t.Fatalf("fixture tiny.%s: %v", ext, err)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return dst
}

// JPEG returns a solid-color JPEG of the given size.
func JPEG(t testing.TB, w, h int, c color.Color) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, c)
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, nil); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}
