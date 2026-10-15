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

	"your-music-library/internal/config"
	"your-music-library/internal/tags"
)

// EditableFields are the text fields a user can edit by hand, in display
// order. Pictures are not edited this way.
var EditableFields = []config.Field{
	config.FieldTitle, config.FieldArtist, config.FieldArtists, config.FieldAlbum,
	config.FieldAlbumArtist, config.FieldDate, config.FieldTrackNumber,
	config.FieldDiscNumber, config.FieldGenre,
}

// AlbumFields are the editable fields that are shared by an album's tracks.
var AlbumFields = []config.Field{
	config.FieldAlbum, config.FieldAlbumArtist, config.FieldDate, config.FieldGenre,
}

// TagEdit maps fields to their new value as text; an empty value removes
// the tag. Fields not in the map are kept. Artists are separated by "; ",
// and numbers may carry a total ("3/12").
type TagEdit map[config.Field]string

// FieldValue returns field f of t as an editable string, the inverse of
// what an edit accepts.
func FieldValue(t tags.Track, f config.Field) string {
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
		return numberPair(t.TrackNumber, t.TrackTotal)
	case config.FieldDiscNumber:
		return numberPair(t.DiscNumber, t.DiscTotal)
	case config.FieldGenre:
		return t.Genre
	}
	return ""
}

func numberPair(n, total int) string {
	switch {
	case n == 0:
		return ""
	case total == 0:
		return strconv.Itoa(n)
	}
	return fmt.Sprintf("%d/%d", n, total)
}

var datePattern = regexp.MustCompile(`^\d{4}(-\d{2}(-\d{2})?)?$`)

func parseNumberPair(f config.Field, s string) (n, total int, err error) {
	if s == "" {
		return 0, 0, nil
	}
	a, b, hasTotal := strings.Cut(s, "/")
	n, err = strconv.Atoi(strings.TrimSpace(a))
	if err == nil && hasTotal {
		total, err = strconv.Atoi(strings.TrimSpace(b))
	}
	if err != nil || n < 1 || total < 0 || (total > 0 && n > total) {
		return 0, 0, fmt.Errorf("%s: %q is not a number like 3 or 3/12", f, s)
	}
	return n, total, nil
}

// Apply returns t with the edit applied.
func (e TagEdit) Apply(t tags.Track) (tags.Track, error) {
	for f, raw := range e {
		v := strings.TrimSpace(raw)
		switch f {
		case config.FieldTitle:
			t.Title = v
		case config.FieldAlbum:
			t.Album = v
		case config.FieldArtist:
			t.Artist = v
		case config.FieldArtists:
			t.Artists = nil
			for _, a := range strings.Split(v, ";") {
				if a = strings.TrimSpace(a); a != "" {
					t.Artists = append(t.Artists, a)
				}
			}
		case config.FieldAlbumArtist:
			t.AlbumArtist = v
		case config.FieldDate:
			if v != "" && !datePattern.MatchString(v) {
				return t, fmt.Errorf("date %q must be YYYY, YYYY-MM or YYYY-MM-DD", v)
			}
			t.Date = v
		case config.FieldTrackNumber:
			n, total, err := parseNumberPair(f, v)
			if err != nil {
				return t, err
			}
			t.TrackNumber, t.TrackTotal = n, total
		case config.FieldDiscNumber:
			n, total, err := parseNumberPair(f, v)
			if err != nil {
				return t, err
			}
			t.DiscNumber, t.DiscTotal = n, total
		case config.FieldGenre:
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
		want, err := edit.Apply(cur)
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
		want, err := plan.edit.Apply(cur)
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
