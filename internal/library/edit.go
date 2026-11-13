package library

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"regexp"
	"strconv"
	"strings"

	"your-music-library/internal/tags"
)

// EditableFields are the text fields a user can edit by hand, in display
// order. Pictures are not edited this way.
var EditableFields = []tags.Field{
	tags.FieldTitle, tags.FieldArtist, tags.FieldArtists, tags.FieldAlbum,
	tags.FieldAlbumArtist, tags.FieldDate, tags.FieldTrackNumber,
	tags.FieldDiscNumber, tags.FieldGenre,
}

// AlbumFields are the editable fields that are shared by an album's tracks.
var AlbumFields = []tags.Field{
	tags.FieldAlbum, tags.FieldAlbumArtist, tags.FieldDate, tags.FieldGenre,
}

// TagEdit maps fields to their new value as text; an empty value removes
// the tag. Fields not in the map are kept. Artists are separated by "; ".
// Numbers are plain ("3" or "03"): totals are not written.
type TagEdit map[tags.Field]string

// FieldValue returns field f of t as an editable string, the inverse of
// what an edit accepts.
func FieldValue(t tags.Track, f tags.Field) string {
	switch f {
	case tags.FieldTitle:
		return t.Title
	case tags.FieldAlbum:
		return t.Album
	case tags.FieldArtist:
		return t.Artist
	case tags.FieldArtists:
		return strings.Join(t.Artists, "; ")
	case tags.FieldAlbumArtist:
		return t.AlbumArtist
	case tags.FieldDate:
		return t.Date
	case tags.FieldTrackNumber:
		return tags.PadNumber(t.TrackNumber)
	case tags.FieldDiscNumber:
		return tags.PadNumber(t.DiscNumber)
	case tags.FieldGenre:
		return t.Genre
	}
	return ""
}

var datePattern = regexp.MustCompile(`^\d{4}(-\d{2}(-\d{2})?)?$`)

func parseNumber(f tags.Field, s string) (int, error) {
	if s == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 1 {
		return 0, fmt.Errorf("%s: %q is not a number like 3", f, s)
	}
	return n, nil
}

// Apply returns t with the edit applied.
func (e TagEdit) Apply(t tags.Track) (tags.Track, error) {
	for f, raw := range e {
		v := strings.TrimSpace(raw)
		switch f {
		case tags.FieldTitle:
			t.Title = v
		case tags.FieldAlbum:
			t.Album = v
		case tags.FieldArtist:
			t.Artist = v
		case tags.FieldArtists:
			t.Artists = nil
			for _, a := range strings.Split(v, ";") {
				if a = strings.TrimSpace(a); a != "" {
					t.Artists = append(t.Artists, a)
				}
			}
		case tags.FieldAlbumArtist:
			t.AlbumArtist = v
		case tags.FieldDate:
			if v != "" && !datePattern.MatchString(v) {
				return t, fmt.Errorf("date %q must be YYYY, YYYY-MM or YYYY-MM-DD", v)
			}
			t.Date = v
		case tags.FieldTrackNumber:
			n, err := parseNumber(f, v)
			if err != nil {
				return t, err
			}
			t.TrackNumber = n
		case tags.FieldDiscNumber:
			n, err := parseNumber(f, v)
			if err != nil {
				return t, err
			}
			t.DiscNumber = n
		case tags.FieldGenre:
			t.Genre = v
		default:
			return t, fmt.Errorf("%s cannot be edited", f)
		}
	}
	return t, nil
}

// EditItem is one file whose tags an edit changes.
type EditItem struct {
	Path    string
	Changes []tags.Change
}

// EditPlan is the preview of a tag edit.
type EditPlan struct {
	Items     []EditItem
	Unchanged int
	edit      TagEdit
}

// result is cur with the edit applied, in the fixed tag format (so the
// preview shows what is written: ASCII text, guests in the title, ...).
func (p EditPlan) result(cur tags.Track) (tags.Track, error) {
	want, err := p.edit.Apply(cur)
	if err != nil {
		return want, err
	}
	return tags.Format(want)
}

// PlanEdit previews applying edit to the files at paths (relative to the
// library). Invalid values are reported before anything is read.
func (l *Library) PlanEdit(ctx context.Context, paths []string, edit TagEdit) (EditPlan, error) {
	if _, err := edit.Apply(tags.Track{}); err != nil {
		return EditPlan{}, err
	}
	plan := EditPlan{edit: edit}
	for _, p := range paths {
		if err := ctx.Err(); err != nil {
			return plan, err
		}
		cur, err := tags.Read(l.abs(p))
		if err != nil {
			return plan, err
		}
		want, err := plan.result(cur)
		if err != nil {
			return plan, err
		}
		changes := tags.Diff(cur, want)
		if len(changes) == 0 {
			plan.Unchanged++
			continue
		}
		plan.Items = append(plan.Items, EditItem{Path: p, Changes: changes})
	}
	return plan, nil
}

// ApplyEdit writes a planned edit. Each file is read again, so tags that
// the edit does not touch (pictures, IDs, unknown tags) are kept.
func (l *Library) ApplyEdit(ctx context.Context, plan EditPlan) (ApplyReport, error) {
	var r ApplyReport
	for _, it := range plan.Items {
		if err := ctx.Err(); err != nil {
			return r, err
		}
		abs := l.abs(it.Path)
		cur, err := tags.Read(abs)
		if err != nil {
			r.Failed = append(r.Failed, FileError{it.Path, err})
			continue
		}
		want, err := plan.result(cur)
		if err == nil {
			err = tags.Write(abs, want)
		}
		if err != nil {
			r.Failed = append(r.Failed, FileError{it.Path, err})
			continue
		}
		if err := l.reindex(ctx, it.Path); err != nil {
			return r, err
		}
		r.Changed++
	}
	return r, nil
}

// Cover returns the cover image for the file at rel: the embedded front
// cover, or else an image in its album folder (cover.jpg, folder.jpg, ...).
// It returns nil data when there is none.
func (l *Library) Cover(rel string) ([]byte, error) {
	t, err := tags.Read(l.abs(rel))
	if err != nil {
		return nil, err
	}
	if t.Cover != nil {
		return t.Cover.Data, nil
	}
	dir := path.Dir(rel)
	for _, name := range folderImages {
		if strings.HasPrefix(name, "back.") {
			continue
		}
		data, err := os.ReadFile(l.abs(path.Join(dir, name)))
		if err == nil {
			return data, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
	}
	return nil, nil
}
