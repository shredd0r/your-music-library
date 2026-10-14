package tags

import (
	"bytes"
	"fmt"
	"image"
	"image/jpeg"
	_ "image/png"
	"slices"
	"strconv"
	"strings"

	"golang.org/x/image/draw"

	"your-music-library/internal/config"
)

// ApplyPreset returns a copy of t keeping only the preset's fields and with
// front and back covers shrunk to the preset's CoverMaxPx. Identity and internal fields
// are always kept. t is not modified.
func ApplyPreset(t Track, p config.Preset) (Track, error) {
	out := t
	out.Artists = slices.Clone(t.Artists)
	clear := map[config.Field]func(){
		config.FieldTitle:        func() { out.Title = "" },
		config.FieldAlbum:        func() { out.Album = "" },
		config.FieldArtist:       func() { out.Artist = "" },
		config.FieldArtists:      func() { out.Artists = nil },
		config.FieldAlbumArtist:  func() { out.AlbumArtist = "" },
		config.FieldDate:         func() { out.Date = "" },
		config.FieldTrackNumber:  func() { out.TrackNumber, out.TrackTotal = 0, 0 },
		config.FieldDiscNumber:   func() { out.DiscNumber, out.DiscTotal = 0, 0 },
		config.FieldGenre:        func() { out.Genre = "" },
		config.FieldPictureCover: func() { out.Cover = nil },
		config.FieldPictureBack:  func() { out.Back = nil },
	}
	for _, f := range config.AllFields {
		if !p.Has(f) {
			clear[f]()
		}
	}
	if p.CoverMaxPx > 0 {
		for _, pic := range []**Picture{&out.Cover, &out.Back} {
			if *pic == nil {
				continue
			}
			c, err := ShrinkPicture(**pic, p.CoverMaxPx)
			if err != nil {
				return Track{}, fmt.Errorf("resizing cover: %w", err)
			}
			*pic = &c
		}
	}
	return out, nil
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
	Field    config.Field
	Old, New string
}

// Diff lists the preset-visible fields that differ between a and b, in
// config.AllFields order.
func Diff(a, b Track) []Change {
	var out []Change
	for _, f := range config.AllFields {
		if o, n := describe(a, f), describe(b, f); o != n {
			out = append(out, Change{Field: f, Old: o, New: n})
		}
	}
	return out
}

func describe(t Track, f config.Field) string {
	switch f {
	case config.FieldTitle:
		return t.Title
	case config.FieldAlbum:
		return t.Album
	case config.FieldArtist:
		return t.Artist
	case config.FieldArtists:
		return strings.Join(t.Artists, "; ")
	case config.FieldAlbumArtist:
		return t.AlbumArtist
	case config.FieldDate:
		return t.Date
	case config.FieldTrackNumber:
		return formatTrackPair(t.TrackNumber, t.TrackTotal)
	case config.FieldDiscNumber:
		return formatNumberPair(t.DiscNumber, t.DiscTotal)
	case config.FieldGenre:
		return t.Genre
	case config.FieldPictureCover:
		return describePicture(t.Cover)
	case config.FieldPictureBack:
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
