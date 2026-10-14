// Package pathtmpl renders library paths such as
// "{albumartist}/{album}/{disc?}{track:02} - {title}.{ext}".
//
// Variables: albumartist (falls back to artist), artist, album, title,
// genre, year, date, track, disc, ext. Numeric variables (track, disc,
// year) accept a zero-pad width: {track:02}. {disc?} renders "<disc>-" only
// for multi-disc albums. Every value is cleaned to be safe on FAT32 and
// exFAT, so files can be copied to SD cards and players.
package pathtmpl

import (
	"fmt"
	"path"
	"strconv"
	"strings"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

// MaxSegmentBytes is the longest file or folder name produced (FAT32/exFAT
// allow 255 UTF-16 units; bytes are the stricter bound for non-ASCII).
const MaxSegmentBytes = 255

// maxValueBytes limits a single variable so literal text still fits.
const maxValueBytes = 180

// Values are the inputs to a template.
type Values struct {
	AlbumArtist            string
	Artist                 string
	Album                  string
	Title                  string
	Genre                  string
	Date                   string // year is taken from it
	Track, Disc, DiscTotal int
	Ext                    string // without dot
}

type part struct {
	literal string
	name    string // variable name; empty for literals
	pad     int
}

// Template is a parsed path template.
type Template struct {
	src   string
	parts []part
}

func (t *Template) String() string { return t.src }

var textVars = map[string]bool{
	"albumartist": true,
	"artist":      true,
	"album":       true,
	"title":       true,
	"genre":       true,
	"date":        true,
	"ext":         true,
}
var numVars = map[string]bool{
	"track": true,
	"disc":  true,
	"year":  true,
}

// Parse validates and compiles a template.
func Parse(s string) (*Template, error) {
	if s == "" {
		return nil, fmt.Errorf("path template is empty")
	}
	t := &Template{src: s}
	rest := s
	for rest != "" {
		open := strings.IndexByte(rest, '{')
		if close := strings.IndexByte(rest, '}'); close >= 0 && (open < 0 || close < open) {
			return nil, fmt.Errorf("path template %q: unexpected '}'", s)
		}
		if open < 0 {
			t.parts = append(t.parts, part{literal: rest})
			break
		}
		if open > 0 {
			t.parts = append(t.parts, part{literal: rest[:open]})
		}
		end := strings.IndexByte(rest[open:], '}')
		if end < 0 {
			return nil, fmt.Errorf("path template %q: missing '}'", s)
		}
		p, err := parseVar(rest[open+1 : open+end])
		if err != nil {
			return nil, fmt.Errorf("path template %q: %w", s, err)
		}
		t.parts = append(t.parts, p)
		rest = rest[open+end+1:]
	}
	return t, t.checkShape()
}

func parseVar(spec string) (part, error) {
	if spec == "disc?" {
		return part{name: "disc?"}, nil
	}
	name, format, hasFormat := strings.Cut(spec, ":")
	if !textVars[name] && !numVars[name] {
		return part{}, fmt.Errorf("unknown variable {%s}", name)
	}
	p := part{name: name}
	if hasFormat {
		if !numVars[name] {
			return part{}, fmt.Errorf("{%s} does not take a format", name)
		}
		n, err := strconv.Atoi(format)
		if err != nil || n < 1 || n > 4 || format[0] != '0' {
			return part{}, fmt.Errorf("{%s}: format must be like 02", spec)
		}
		p.pad = n
	}
	return p, nil
}

// checkShape validates the folder structure using placeholder values.
func (t *Template) checkShape() error {
	var b strings.Builder
	for _, p := range t.parts {
		if p.name == "" {
			b.WriteString(p.literal)
		} else {
			b.WriteString("X")
		}
	}
	shape := b.String()
	if strings.HasPrefix(shape, "/") {
		return fmt.Errorf("path template %q must be relative to the library", t.src)
	}
	for _, seg := range strings.Split(shape, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return fmt.Errorf("path template %q has an empty, '.' or '..' folder", t.src)
		}
	}
	last := t.parts[len(t.parts)-1]
	if last.name != "ext" || !strings.HasSuffix(shape, ".X") {
		return fmt.Errorf("path template %q must end with .{ext}", t.src)
	}
	return nil
}

// Render produces a relative, '/'-separated path.
func (t *Template) Render(v Values) (string, error) {
	var b strings.Builder
	for _, p := range t.parts {
		if p.name == "" {
			b.WriteString(p.literal)
			continue
		}
		b.WriteString(value(p, v))
	}
	segs := strings.Split(b.String(), "/")
	for i, seg := range segs {
		seg = cleanSegment(seg)
		if seg == "" {
			return "", fmt.Errorf("rendering %q produced an empty folder name", t.src)
		}
		segs[i] = seg
	}
	return path.Join(segs...), nil
}

func value(p part, v Values) string {
	switch p.name {
	case "disc?":
		if v.DiscTotal > 1 && v.Disc > 0 {
			return strconv.Itoa(v.Disc) + "-"
		}
		return ""
	case "track":
		return pad(v.Track, p.pad)
	case "disc":
		return pad(v.Disc, p.pad)
	case "year":
		if y := year(v.Date); y != "" {
			if n, _ := strconv.Atoi(y); p.pad > 0 {
				return pad(n, p.pad)
			}
			return y
		}
		return "Unknown Year"
	case "ext":
		return Clean(strings.TrimPrefix(v.Ext, "."), "")
	}
	text := map[string]string{
		"albumartist": firstNonEmpty(v.AlbumArtist, v.Artist),
		"artist":      v.Artist,
		"album":       v.Album,
		"title":       v.Title,
		"genre":       v.Genre,
		"date":        v.Date,
	}[p.name]
	fallback := map[string]string{
		"albumartist": "Unknown Artist",
		"artist":      "Unknown Artist",
		"album":       "Unknown Album",
		"title":       "Untitled",
		"genre":       "Unknown Genre",
		"date":        "Unknown Date",
	}[p.name]
	return Clean(text, fallback)
}

func pad(n, width int) string { return fmt.Sprintf("%0*d", width, n) }

func year(date string) string {
	if len(date) >= 4 {
		if _, err := strconv.Atoi(date[:4]); err == nil {
			return date[:4]
		}
	}
	return ""
}

func firstNonEmpty(xs ...string) string {
	for _, x := range xs {
		if strings.TrimSpace(x) != "" {
			return x
		}
	}
	return ""
}

// Clean makes a single tag value safe inside a file or folder name: NFC,
// no characters FAT32 rejects (including '/'), no leading dot, no
// leading/trailing spaces or trailing dots, bounded length. An empty
// result becomes fallback.
func Clean(s, fallback string) string {
	s = norm.NFC.String(s)
	var b strings.Builder
	for _, r := range s {
		switch {
		case r < 0x20 || r == 0x7f:
			b.WriteByte('_')
		case strings.ContainsRune(`/\:*?"<>|`, r):
			b.WriteByte('_')
		default:
			b.WriteRune(r)
		}
	}
	s = truncate(trimEdges(b.String()), maxValueBytes)
	s = trimEdges(s)
	if s == "" {
		return fallback
	}
	if strings.HasPrefix(s, ".") {
		s = "_" + s[1:]
	}
	return s
}

func trimEdges(s string) string {
	s = strings.TrimSpace(s)
	return strings.TrimRight(s, ". ")
}

// cleanSegment applies whole-name rules after literals and values are
// joined: reserved device names, trailing dots/spaces, length.
func cleanSegment(seg string) string {
	ext := ""
	if i := strings.LastIndexByte(seg, '.'); i > 0 && len(seg)-i <= 6 {
		ext = seg[i:]
		seg = seg[:i]
	}
	seg = trimEdges(truncate(trimEdges(seg), MaxSegmentBytes-len(ext)))
	if seg == "" {
		return ""
	}
	if isReserved(seg) {
		seg += "_"
	}
	return seg + ext
}

// isReserved reports DOS device names, which FAT and Windows refuse even
// with an extension.
func isReserved(name string) bool {
	base, _, _ := strings.Cut(strings.ToUpper(name), ".")
	switch base {
	case "CON", "PRN", "AUX", "NUL":
		return true
	}
	if len(base) == 4 && (strings.HasPrefix(base, "COM") || strings.HasPrefix(base, "LPT")) && base[3] >= '1' && base[3] <= '9' {
		return true
	}
	return false
}

func truncate(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	s = s[:maxBytes]
	for !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}
	return s
}
