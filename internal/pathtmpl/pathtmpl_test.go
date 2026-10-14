package pathtmpl

import (
	"strings"
	"testing"
)

const def = "{albumartist}/{album}/{disc?}{track:02} - {title}.{ext}"

func mustParse(t *testing.T, s string) *Template {
	t.Helper()
	tp, err := Parse(s)
	if err != nil {
		t.Fatal(err)
	}
	return tp
}

func TestRenderDefault(t *testing.T) {
	tp := mustParse(t, def)
	cases := []struct {
		name string
		v    Values
		want string
	}{
		{"single disc", Values{AlbumArtist: "Michael Jackson", Album: "Bad", Track: 3, Title: "Speed Demon", Ext: "flac"},
			"Michael Jackson/Bad/03 - Speed Demon.flac"},
		{"multi disc", Values{AlbumArtist: "Pink Floyd", Album: "The Wall", Disc: 2, DiscTotal: 2, Track: 1, Title: "Hey You", Ext: "mp3"},
			"Pink Floyd/The Wall/2-01 - Hey You.mp3"},
		{"falls back to artist", Values{Artist: "Prince", Album: "1999", Track: 1, Title: "1999", Ext: "flac"},
			"Prince/1999/01 - 1999.flac"},
		{"unknowns", Values{Ext: "flac"},
			"Unknown Artist/Unknown Album/00 - Untitled.flac"},
		{"unsafe characters", Values{AlbumArtist: "AC/DC", Album: "Who Made Who?", Track: 1, Title: `Ride On: "Live" <1>|*`, Ext: "flac"},
			"AC_DC/Who Made Who_/01 - Ride On_ _Live_ _1___.flac"},
		{"trailing dots and spaces", Values{AlbumArtist: "R.E.M.", Album: "  Out of Time  ", Track: 1, Title: "Losing My Religion...", Ext: "flac"},
			"R.E.M/Out of Time/01 - Losing My Religion.flac"},
		{"leading dot", Values{AlbumArtist: ".hidden", Album: "x", Track: 1, Title: "t", Ext: "flac"},
			"_hidden/x/01 - t.flac"},
		{"reserved name", Values{AlbumArtist: "CON", Album: "aux", Track: 1, Title: "t", Ext: "flac"},
			"CON_/aux_/01 - t.flac"},
		{"three digit track pads only to 2", Values{AlbumArtist: "a", Album: "b", Track: 104, Title: "t", Ext: "flac"},
			"a/b/104 - t.flac"},
		{"NFD input becomes NFC", Values{AlbumArtist: "Beyoncé", Album: "b", Track: 1, Title: "t", Ext: "flac"},
			"Beyoncé/b/01 - t.flac"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := tp.Render(c.v)
			if err != nil {
				t.Fatal(err)
			}
			if got != c.want {
				t.Errorf("got  %q\nwant %q", got, c.want)
			}
		})
	}
}

func TestRenderOtherVariables(t *testing.T) {
	tp := mustParse(t, "{genre}/{year} - {album}/{track} {artist} - {title}.{ext}")
	got, err := tp.Render(Values{Genre: "Pop", Date: "1987-08-31", Album: "Bad", Track: 7, Artist: "MJ", Title: "x", Ext: "flac"})
	if err != nil || got != "Pop/1987 - Bad/7 MJ - x.flac" {
		t.Errorf("got %q, %v", got, err)
	}
	got, _ = tp.Render(Values{Ext: "flac"})
	if got != "Unknown Genre/Unknown Year - Unknown Album/0 Unknown Artist - Untitled.flac" {
		t.Errorf("fallbacks: %q", got)
	}
}

func TestLongNamesAreTruncated(t *testing.T) {
	tp := mustParse(t, def)
	got, err := tp.Render(Values{AlbumArtist: "a", Album: strings.Repeat("é", 300), Track: 1, Title: strings.Repeat("x", 400), Ext: "flac"})
	if err != nil {
		t.Fatal(err)
	}
	for _, seg := range strings.Split(got, "/") {
		if len(seg) > MaxSegmentBytes {
			t.Errorf("segment %d bytes > %d", len(seg), MaxSegmentBytes)
		}
	}
	if !strings.HasSuffix(got, ".flac") {
		t.Errorf("extension lost: %q", got)
	}
}

func TestParseErrors(t *testing.T) {
	for _, s := range []string{
		"",
		"{album",
		"album}",
		"{nope}.{ext}",
		"{title:02}.{ext}",
		"{track:x}.{ext}",
		"/{album}/{title}.{ext}",
		"{album}/../{title}.{ext}",
		"{album}//{title}.{ext}",
		"{album}/{title}",
		"{album}/{title}.{ext}/x",
	} {
		if _, err := Parse(s); err == nil {
			t.Errorf("Parse(%q) should fail", s)
		}
	}
}
