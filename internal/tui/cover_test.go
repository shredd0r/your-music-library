package tui

import (
	"errors"
	"image/color"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"your-music-library/internal/tags"
	"your-music-library/internal/termimg"
	"your-music-library/internal/testutil"
)

func TestCoversKitty(t *testing.T) {
	c := newCovers(termimg.Kitty, 7)
	img := testutil.JPEG(t, 32, 32, color.RGBA{200, 0, 0, 255})
	cmd := c.want("url:x", func() ([]byte, error) { return img, nil })
	if c.want("url:x", nil) != nil {
		t.Error("loading the same key twice")
	}
	if got := c.view("url:x"); !strings.Contains(strings.Join(got, ""), "loading cover") {
		t.Errorf("while loading: %q", got)
	}
	send := c.update(cmd().(coverMsg))
	raw, ok := send().(tea.RawMsg)
	if !ok || !strings.Contains(raw.Msg.(string), "a=T,U=1,q=2,f=100,i=7,") {
		t.Fatalf("transmit = %#v", raw)
	}
	lines := c.view("url:x")
	if w, h := lipgloss.Size(strings.Join(lines, "\n")); w != coverCols || h != coverRows {
		t.Errorf("placeholder size %dx%d", w, h)
	}
	if !strings.Contains(c.cleanup(), "a=d,d=I,q=2,i=7") {
		t.Errorf("cleanup = %q", c.cleanup())
	}
}

func TestCoversHalfBlockAndMissing(t *testing.T) {
	c := newCovers(termimg.HalfBlock, 1)
	img := testutil.JPEG(t, 16, 16, color.White)
	if cmd := c.update(coverMsg{key: "a", data: img}); cmd != nil {
		t.Error("half-blocks need no terminal command")
	}
	if got := strings.Join(c.view("a"), "\n"); !strings.Contains(got, "▀") || lipgloss.Height(got) != coverRows {
		t.Errorf("half-block cover: %q", got)
	}
	c.update(coverMsg{key: "none"})
	c.update(coverMsg{key: "bad", data: []byte("x")})
	c.update(coverMsg{key: "err", err: errors.New("offline")})
	for key, want := range map[string]string{"none": "no cover", "bad": "cover unavailable", "err": "cover unavailable"} {
		if got := strings.Join(c.view(key), ""); !strings.Contains(got, want) {
			t.Errorf("%s: %q", key, got)
		}
	}
	if c.view("") != nil || c.cleanup() != "" {
		t.Error("empty key or cleanup without Kitty images")
	}
}

func TestLibraryShowsEmbeddedCover(t *testing.T) {
	h := newHarness(t, func(h *harness) {
		h.addTrack("A/X/01.flac", tags.Track{Title: "T", Album: "X", Artist: "A",
			Cover: &tags.Picture{MIME: "image/jpeg", Data: testutil.JPEG(t, 20, 20, color.RGBA{0, 0, 255, 255})}})
	})
	h.keys("3")
	if !strings.Contains(h.screen(), strings.Repeat("▀", coverCols)) {
		t.Errorf("no cover drawn:\n%s", h.screen())
	}
}
