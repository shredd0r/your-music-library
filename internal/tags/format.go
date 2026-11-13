package tags

import (
	"bytes"
	"fmt"
	"image"
	"image/jpeg"
	_ "image/png"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"golang.org/x/image/draw"
)

// CoverMaxPx is the longest side of the embedded front cover. The
// full-size image is kept as cover.jpg in the album folder.
const CoverMaxPx = 600

// Format returns t in ymlib's fixed tag format. t is not modified.
//   - no track or disc totals and no embedded back cover;
//   - a grouped single ("<Artist> Singles", an old ymlib mode) gets its
//     own album and track number back;
//   - typographic quotes, dashes and ellipses become ASCII;
//   - guests move from Artist into the title: "Title (feat. Guest)";
//   - the front cover is shrunk to CoverMaxPx.
//
// IDs and the release type are kept. Format is idempotent.
func Format(t Track) (Track, error) {
	out := t
	out.Artists = slices.Clone(t.Artists)
	if out.OriginalAlbum != "" {
		out.Album = out.OriginalAlbum
		out.TrackNumber = max(out.OriginalTrack, 1)
		out.DiscNumber = 0
	}
	out.OriginalAlbum, out.OriginalTrack = "", 0
	out.TrackTotal, out.DiscTotal = 0, 0
	out.Back = nil

	for _, s := range []*string{&out.Title, &out.Album, &out.Artist, &out.AlbumArtist, &out.Genre} {
		*s = ASCII(*s)
	}
	for i := range out.Artists {
		out.Artists[i] = ASCII(out.Artists[i])
	}
	out.Title, out.Artist = moveFeat(out.Title, out.Artist)

	if out.Cover != nil {
		c, err := ShrinkPicture(*out.Cover, CoverMaxPx)
		if err != nil {
			return Track{}, fmt.Errorf("resizing cover: %w", err)
		}
		out.Cover = &c
	}
	return out, nil
}

var asciiReplacer = strings.NewReplacer(
	"\u2019", "'", "\u2018", "'", "\u02bc", "'", "\u2032", "'",
	"\u201c", `"`, "\u201d", `"`, "\u201e", `"`, "\u2033", `"`,
	"\u2013", "-", "\u2014", "-", "\u2012", "-", "\u2015", "-",
	"\u2026", "...",
)

// ASCII replaces typographic quotes, dashes and ellipses with their ASCII
// forms: ’ → ', “ ” → ", – — → -, … → ...
func ASCII(s string) string { return asciiReplacer.Replace(s) }

// featArtist splits "Main feat. Guest" (also ft., featuring, and the
// guest in brackets). It needs whitespace before the marker, so names
// like "Daft Punk" are never split, and it never splits on "&".
var featArtist = regexp.MustCompile(`(?i)^(.*?\S)\s+[(\[]?(?:feat\.|feat|ft\.|ft|featuring)\s+(.+?)[)\]]?$`)

// featTitle finds a guest already named in a title.
var featTitle = regexp.MustCompile(`(?i)(^|[\s(\[])(feat\.?|ft\.|featuring)\s`)

// MainArtist returns artist without its guests: "A feat. B" → "A".
// Duplicate checks use it, since Format moves guests into the title.
func MainArtist(artist string) string {
	if m := featArtist.FindStringSubmatch(artist); m != nil {
		return m[1]
	}
	return artist
}

// moveFeat moves the guests of artist into title. If the title already
// names a guest, only the artist is cleaned. Without a title nothing
// changes, so the guest is never lost.
func moveFeat(title, artist string) (string, string) {
	m := featArtist.FindStringSubmatch(artist)
	if m == nil || strings.TrimSpace(title) == "" {
		return title, artist
	}
	if !featTitle.MatchString(title) {
		title += " (feat. " + m[2] + ")"
	}
	return title, m[1]
}

// ShrinkPicture scales the image so its longest side is at most maxPx,
// re-encoding as JPEG. Images already small enough are returned unchanged.
func ShrinkPicture(p Picture, maxPx int) (Picture, error) {
	src, _, err := image.Decode(bytes.NewReader(p.Data))
	if err != nil {
		return Picture{}, err
	}
	w, h := src.Bounds().Dx(), src.Bounds().Dy()
	if w <= maxPx && h <= maxPx {
		return p, nil
	}
	if w >= h {
		h = max(1, h*maxPx/w)
		w = maxPx
	} else {
		w = max(1, w*maxPx/h)
		h = maxPx
	}
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.CatmullRom.Scale(dst, dst.Bounds(), src, src.Bounds(), draw.Src, nil)
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, dst, &jpeg.Options{Quality: 90}); err != nil {
		return Picture{}, err
	}
	return Picture{MIME: "image/jpeg", Data: buf.Bytes()}, nil
}

// Change is one user-visible field difference between two tracks.
type Change struct {
	Field    Field
	Old, New string
}

// Diff lists the fields that differ between a and b, in Fields order,
// followed by Picture Back.
func Diff(a, b Track) []Change {
	var out []Change
	for _, f := range append(slices.Clone(Fields), FieldPictureBack) {
		if o, n := describe(a, f), describe(b, f); o != n {
			out = append(out, Change{Field: f, Old: o, New: n})
		}
	}
	return out
}

func describe(t Track, f Field) string {
	switch f {
	case FieldTitle:
		return t.Title
	case FieldAlbum:
		return t.Album
	case FieldArtist:
		return t.Artist
	case FieldArtists:
		return strings.Join(t.Artists, "; ")
	case FieldAlbumArtist:
		return t.AlbumArtist
	case FieldDate:
		return t.Date
	case FieldTrackNumber:
		return formatNumberPair(t.TrackNumber, t.TrackTotal)
	case FieldDiscNumber:
		return formatNumberPair(t.DiscNumber, t.DiscTotal)
	case FieldGenre:
		return t.Genre
	case FieldPictureCover:
		return describePicture(t.Cover)
	case FieldPictureBack:
		return describePicture(t.Back)
	}
	return ""
}

func describePicture(p *Picture) string {
	if p == nil {
		return ""
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(p.Data))
	if err != nil {
		return p.MIME + " " + strconv.Itoa(len(p.Data)) + " bytes"
	}
	return fmt.Sprintf("%s %dx%d", p.MIME, cfg.Width, cfg.Height)
}
