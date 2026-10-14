package config

import "slices"

// Preset is a named set of fields written to files on import or retag.
type Preset struct {
	Name   string
	Fields []Field
	// CoverMaxPx is the longest side of the embedded cover; 0 keeps the
	// original size. The cover.jpg in the album folder is always full size.
	CoverMaxPx int
	Builtin    bool
}

// Has reports whether the preset writes field f.
func (p Preset) Has(f Field) bool { return slices.Contains(p.Fields, f) }

func builtinPresets() map[string]Preset {
	return map[string]Preset{
		"full": {
			Name:    "full",
			Fields:  slices.Clone(AllFields),
			Builtin: true,
		},
		"mp3-player": {
			Name: "mp3-player",
			Fields: []Field{
				FieldTitle, FieldAlbum, FieldArtist, FieldAlbumArtist, FieldDate,
				FieldTrackNumber, FieldDiscNumber, FieldPictureCover, FieldGenre,
			},
			// Many hardware players fail on large embedded images.
			CoverMaxPx: 600,
			Builtin:    true,
		},
	}
}
