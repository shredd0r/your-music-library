// Package termimg draws images in a terminal grid. Two renderers exist:
//
//   - Kitty: the Kitty graphics protocol with Unicode placeholders. The image
//     is sent once as an escape sequence (Transmit); the screen then holds
//     ordinary placeholder characters (Placeholder), so a cell-based UI
//     can lay them out, move and redraw them like text. Supported by Kitty
//     and Ghostty.
//   - Half-block: each cell shows two pixels with '▀' (foreground = upper,
//     background = lower). Works in any terminal with colors; Bubble Tea
//     reduces the colors for terminals without 24-bit color (Apple Terminal).
//
// iTerm2 has its own image protocol, but it draws at the cursor and cannot
// be laid out as cells, so it gets half-blocks.
package termimg

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	"image/png"
	"strings"

	_ "image/jpeg" // decode JPEG covers
	_ "image/png"

	"golang.org/x/image/draw"
)

// Protocol is how images are drawn.
type Protocol string

const (
	Kitty     Protocol = "kitty"
	HalfBlock Protocol = "halfblock"
)

// Detect picks the protocol for the terminal described by getenv.
// YMLIB_IMAGES=kitty|halfblock overrides it. Inside tmux or screen,
// placeholders need passthrough that is usually off, so half-blocks are used.
func Detect(getenv func(string) string) Protocol {
	switch Protocol(getenv("YMLIB_IMAGES")) {
	case Kitty:
		return Kitty
	case HalfBlock:
		return HalfBlock
	}
	if getenv("TMUX") != "" || strings.HasPrefix(getenv("TERM"), "screen") {
		return HalfBlock
	}
	switch {
	case getenv("KITTY_WINDOW_ID") != "", getenv("TERM") == "xterm-kitty",
		getenv("TERM_PROGRAM") == "ghostty", getenv("TERM") == "xterm-ghostty":
		return Kitty
	}
	return HalfBlock
}

// Decode reads a JPEG or PNG image.
func Decode(data []byte) (image.Image, error) {
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("decoding image: %w", err)
	}
	return img, nil
}

// Rows is the number of cell rows a square image takes at cols columns:
// terminal cells are about twice as tall as wide.
func Rows(cols int) int { return max(1, cols/2) }

func scale(img image.Image, w, h int) *image.RGBA {
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.CatmullRom.Scale(dst, dst.Bounds(), img, img.Bounds(), draw.Over, nil)
	return dst
}

// HalfBlocks renders img into cols×rows cells, one line per row, using
// 24-bit color escape sequences.
func HalfBlocks(img image.Image, cols, rows int) string {
	if cols <= 0 || rows <= 0 {
		return ""
	}
	s := scale(img, cols, rows*2)
	var b strings.Builder
	for y := 0; y < rows*2; y += 2 {
		if y > 0 {
			b.WriteByte('\n')
		}
		for x := 0; x < cols; x++ {
			u := s.RGBAAt(x, y)
			l := s.RGBAAt(x, y+1)
			fmt.Fprintf(&b, "\x1b[38;2;%d;%d;%dm\x1b[48;2;%d;%d;%dm▀", u.R, u.G, u.B, l.R, l.G, l.B)
		}
		b.WriteString("\x1b[0m")
	}
	return b.String()
}

// transmitPx is the size images are sent at; the terminal scales them to
// the placement.
const transmitPx = 400

// chunk is the largest payload of one graphics escape sequence.
const chunk = 4096

// Transmit returns the escape sequences that send img to the terminal as
// image id and create a virtual placement of cols×rows cells for the
// placeholders. Responses are suppressed (q=2) so they don't arrive as
// keyboard input.
func Transmit(img image.Image, id uint32, cols, rows int) (string, error) {
	var buf bytes.Buffer
	if err := png.Encode(&buf, scale(img, transmitPx, transmitPx)); err != nil {
		return "", err
	}
	data := base64.StdEncoding.EncodeToString(buf.Bytes())
	var b strings.Builder
	for i := 0; i < len(data); i += chunk {
		end := min(i+chunk, len(data))
		more := 0
		if end < len(data) {
			more = 1
		}
		if i == 0 {
			fmt.Fprintf(&b, "\x1b_Ga=T,U=1,q=2,f=100,i=%d,c=%d,r=%d,m=%d;%s\x1b\\", id, cols, rows, more, data[i:end])
		} else {
			fmt.Fprintf(&b, "\x1b_Gm=%d;%s\x1b\\", more, data[i:end])
		}
	}
	return b.String(), nil
}

// Delete returns the escape sequence that frees image id in the terminal.
func Delete(id uint32) string { return fmt.Sprintf("\x1b_Ga=d,d=I,q=2,i=%d\x1b\\", id) }

// placeholder is the Unicode character Kitty replaces with image cells.
const placeholder = '\U0010EEEE'

// diacritics encode a placeholder cell's row and column (Kitty's
// rowcolumn-diacritics table); their count limits the image size in cells.
var diacritics = []rune{
	0x0305, 0x030D, 0x030E, 0x0310, 0x0312, 0x033D, 0x033E, 0x033F, 0x0346, 0x034A,
	0x034B, 0x034C, 0x0350, 0x0351, 0x0352, 0x0357, 0x035B, 0x0363, 0x0364, 0x0365,
	0x0366, 0x0367, 0x0368, 0x0369, 0x036A, 0x036B, 0x036C, 0x036D, 0x036E, 0x036F,
	0x0483, 0x0484, 0x0485, 0x0486, 0x0487, 0x0592, 0x0593, 0x0594, 0x0595, 0x0597,
	0x0598, 0x0599, 0x059C, 0x059D, 0x059E, 0x059F, 0x05A0, 0x05A1, 0x05A8, 0x05A9,
	0x05AB, 0x05AC, 0x05AF, 0x05C4, 0x0610, 0x0611, 0x0612, 0x0613, 0x0614, 0x0615,
	0x0616, 0x0617, 0x0657, 0x0658,
}

// MaxCells is the largest width or height a Kitty placement can have here.
var MaxCells = len(diacritics)

// Placeholder returns cols×rows placeholder cells for image id, one line
// per row. The image id is carried in the 24-bit foreground color.
func Placeholder(id uint32, cols, rows int) string {
	cols, rows = min(cols, MaxCells), min(rows, MaxCells)
	if cols <= 0 || rows <= 0 {
		return ""
	}
	color := fmt.Sprintf("\x1b[38;2;%d;%d;%dm", id>>16&0xff, id>>8&0xff, id&0xff)
	var b strings.Builder
	for y := range rows {
		if y > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(color)
		for x := range cols {
			b.WriteRune(placeholder)
			b.WriteRune(diacritics[y])
			b.WriteRune(diacritics[x])
		}
		b.WriteString("\x1b[39m")
	}
	return b.String()
}
