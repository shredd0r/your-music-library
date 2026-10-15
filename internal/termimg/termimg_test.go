package termimg

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/png"
	"regexp"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
)

func solid(c color.RGBA, w, h int) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.Set(x, y, c)
		}
	}
	return img
}

func TestDetect(t *testing.T) {
	cases := []struct {
		env  map[string]string
		want Protocol
	}{
		{map[string]string{"TERM_PROGRAM": "ghostty"}, Kitty},
		{map[string]string{"TERM": "xterm-kitty"}, Kitty},
		{map[string]string{"KITTY_WINDOW_ID": "1"}, Kitty},
		{map[string]string{"TERM_PROGRAM": "iTerm.app"}, HalfBlock},
		{map[string]string{"TERM_PROGRAM": "Apple_Terminal"}, HalfBlock},
		{map[string]string{"TERM_PROGRAM": "ghostty", "TMUX": "/tmp/tmux"}, HalfBlock},
		{map[string]string{"TERM_PROGRAM": "Apple_Terminal", "YMLIB_IMAGES": "kitty"}, Kitty},
		{map[string]string{"TERM_PROGRAM": "ghostty", "YMLIB_IMAGES": "halfblock"}, HalfBlock},
	}
	for _, c := range cases {
		if got := Detect(func(k string) string { return c.env[k] }); got != c.want {
			t.Errorf("Detect(%v) = %s, want %s", c.env, got, c.want)
		}
	}
}

func TestHalfBlocksSize(t *testing.T) {
	out := HalfBlocks(solid(color.RGBA{200, 10, 10, 255}, 50, 50), 12, 6)
	w, h := lipgloss.Size(out)
	if w != 12 || h != 6 {
		t.Fatalf("size = %dx%d, want 12x6", w, h)
	}
	if !strings.Contains(out, "38;2;200;10;10") {
		t.Errorf("color missing from %q", out[:60])
	}
}

func TestHalfBlocksTopAndBottomPixels(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 1, 2))
	img.Set(0, 0, color.RGBA{255, 0, 0, 255})
	img.Set(0, 1, color.RGBA{0, 0, 255, 255})
	out := HalfBlocks(img, 1, 1)
	if !strings.Contains(out, "\x1b[38;2;255;0;0m\x1b[48;2;0;0;255m▀") {
		t.Errorf("got %q, want red over blue", out)
	}
}

func TestPlaceholderLaysOutAsCells(t *testing.T) {
	out := Placeholder(0x010203, 8, 4)
	w, h := lipgloss.Size(out)
	if w != 8 || h != 4 {
		t.Fatalf("size = %dx%d, want 8x4", w, h)
	}
	if !strings.HasPrefix(out, "\x1b[38;2;1;2;3m") {
		t.Errorf("image id not in the foreground color: %q", out[:20])
	}
	lines := strings.Split(out, "\n")
	// row 2, column 3
	cell := string([]rune{placeholder, diacritics[2], diacritics[3]})
	if !strings.Contains(lines[2], cell) {
		t.Errorf("row 2 has no cell for column 3")
	}
}

func TestPlaceholderClampsToTable(t *testing.T) {
	_, h := lipgloss.Size(Placeholder(1, 500, 500))
	if h != MaxCells {
		t.Errorf("height = %d, want %d", h, MaxCells)
	}
}

func TestTransmitChunks(t *testing.T) {
	// a noisy image so the PNG is larger than one chunk
	img := image.NewRGBA(image.Rect(0, 0, 64, 64))
	for i := range img.Pix {
		img.Pix[i] = byte(i * 7919 % 251)
	}
	out, err := Transmit(img, 42, 10, 5)
	if err != nil {
		t.Fatal(err)
	}
	re := regexp.MustCompile("\x1b_G([^;]*);([^\x1b]*)\x1b\\\\")
	parts := re.FindAllStringSubmatch(out, -1)
	if len(parts) < 2 {
		t.Fatalf("got %d chunks, want several", len(parts))
	}
	if !strings.Contains(parts[0][1], "a=T,U=1,q=2,f=100,i=42,c=10,r=5,m=1") {
		t.Errorf("first chunk header = %q", parts[0][1])
	}
	if last := parts[len(parts)-1][1]; last != "m=0" {
		t.Errorf("last chunk header = %q, want m=0", last)
	}
	var data strings.Builder
	for _, p := range parts {
		if len(p[2]) > chunk {
			t.Errorf("chunk of %d bytes", len(p[2]))
		}
		data.WriteString(p[2])
	}
	raw, err := base64.StdEncoding.DecodeString(data.String())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := png.Decode(bytes.NewReader(raw)); err != nil {
		t.Errorf("payload is not a PNG: %v", err)
	}
}

func TestDecode(t *testing.T) {
	var buf bytes.Buffer
	if err := png.Encode(&buf, solid(color.RGBA{1, 2, 3, 255}, 4, 4)); err != nil {
		t.Fatal(err)
	}
	if _, err := Decode(buf.Bytes()); err != nil {
		t.Fatal(err)
	}
	if _, err := Decode([]byte("not an image")); err == nil {
		t.Error("garbage decoded")
	}
}
