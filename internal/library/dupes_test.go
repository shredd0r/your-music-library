package library

import (
	"context"
	"testing"

	"your-music-library/internal/tags"
)

func TestDupIndex(t *testing.T) {
	l := newLib(t)
	addFile(t, l, "mj/bad.flac", tags.Track{Title: "Bad", Artist: "Michael Jackson", Album: "Bad",
		IDs: tags.Identity{Source: "fake", SourceTrackID: "t9", ISRC: "USSM18700359"}})
	addFile(t, l, "mj/rwy.flac", tags.Track{Title: "Rock With You", Artist: "Michael Jackson", AlbumArtist: "Michael Jackson", Album: "Off the Wall"})
	addFile(t, l, "ado/u.flac", tags.Track{Title: "Usseewa", Artist: "Ado", Album: "Ado Singles", OriginalAlbum: "Usseewa"})
	scan(t, l)
	d, err := l.DupIndex(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name string
		q    DupQuery
		want DupStatus
		path string
	}{
		{"source id", DupQuery{Source: "fake", SourceTrackID: "t9", Title: "whatever"}, InLibrary, "mj/bad.flac"},
		{"isrc same album", DupQuery{ISRC: "ussm18700359", Album: "Bad", Title: "Bad"}, InLibrary, "mj/bad.flac"},
		{"isrc other album", DupQuery{ISRC: "USSM18700359", Album: "Number Ones", Title: "Bad"}, OtherEdition, "mj/bad.flac"},
		{"names, typographic quotes and case", DupQuery{Artist: "michael jackson", Album: "Off The Wall", Title: "Rock with You"}, InLibrary, "mj/rwy.flac"},
		{"remaster is another edition", DupQuery{Artist: "Michael Jackson", Album: "Off the Wall (2016 Remaster)", Title: "Rock With You - 2016 Remaster"}, OtherEdition, "mj/rwy.flac"},
		{"no album given", DupQuery{Artist: "Michael Jackson", Title: "Rock With You"}, InLibrary, "mj/rwy.flac"},
		{"grouped single by original album", DupQuery{Artist: "Ado", Album: "Usseewa", Title: "Usseewa"}, InLibrary, "ado/u.flac"},
		{"unknown", DupQuery{Artist: "Michael Jackson", Album: "Bad", Title: "Smooth Criminal"}, NotInLibrary, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, path := d.Check(c.q)
			if got != c.want || path != c.path {
				t.Errorf("Check = %v %q, want %v %q", got, path, c.want, c.path)
			}
		})
	}

	d.Add(Entry{Path: "new.flac", Track: tags.Track{Title: "Smooth Criminal", Artist: "Michael Jackson", Album: "Bad"}})
	if s, _ := d.Check(DupQuery{Artist: "Michael Jackson", Album: "Bad", Title: "Smooth Criminal"}); s != InLibrary {
		t.Error("Add not visible to Check")
	}
}
