package config

import (
	"fmt"
	"strings"
)

// Field is one metadata field a preset can write. The string value is the
// name users write in config.yaml.
type Field string

const (
	FieldTitle        Field = "Title"
	FieldAlbum        Field = "Album"
	FieldArtist       Field = "Artist"
	FieldArtists      Field = "Artists"
	FieldAlbumArtist  Field = "Album Artist"
	FieldDate         Field = "Date"
	FieldTrackNumber  Field = "Track Number"
	FieldDiscNumber   Field = "Disc Number"
	FieldGenre        Field = "Genre"
	FieldPictureCover Field = "Picture Cover"
	FieldPictureBack  Field = "Picture Back"
)

// AllFields lists every supported field in display order.
var AllFields = []Field{
	FieldTitle, FieldAlbum, FieldArtist, FieldArtists, FieldAlbumArtist,
	FieldDate, FieldTrackNumber, FieldDiscNumber, FieldGenre,
	FieldPictureCover, FieldPictureBack,
}

func normalizeFieldName(s string) string {
	r := strings.NewReplacer(" ", "", "_", "", "-", "")
	return strings.ToLower(r.Replace(s))
}

var fieldsByKey = func() map[string]Field {
	m := make(map[string]Field, len(AllFields))
	for _, f := range AllFields {
		m[normalizeFieldName(string(f))] = f
	}
	return m
}()

// ParseField accepts a field name ignoring case, spaces, '_' and '-'.
func ParseField(s string) (Field, error) {
	if f, ok := fieldsByKey[normalizeFieldName(s)]; ok {
		return f, nil
	}
	names := make([]string, len(AllFields))
	for i, f := range AllFields {
		names[i] = string(f)
	}
	return "", fmt.Errorf("unknown field %q (valid: %s)", s, strings.Join(names, ", "))
}
