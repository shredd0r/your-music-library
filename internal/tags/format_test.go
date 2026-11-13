package tags

import (
	"bytes"
	"image"
	"image/color"
	"reflect"
	"testing"

	"your-music-library/internal/testutil"
)

func TestFormatDropsTotalsBackAndGrouping(t *testing.T) {
	tr := sampleTrack(t)
	got, err := Format(tr)
	if err != nil {
		t.Fatal(err)
	}
	if got.TrackTotal != 0 || got.DiscTotal != 0 || got.Back != nil {
		t.Errorf("totals and back cover should be removed: %+v", got)
	}
	if got.IDs != tr.IDs || got.ReleaseType != tr.ReleaseType || got.Cover == nil {
		t.Errorf("IDs, release type and cover should be kept: %+v", got)
	}
	if tr.Back == nil || tr.TrackTotal == 0 {
		t.Error("Format mutated its input")
	}
}

func TestFormatRestoresGroupedSingle(t *testing.T) {
	tr := Track{
		Title: "Leave Me Alone", Album: "Michael Jackson Singles", TrackNumber: 4, DiscNumber: 1,
		OriginalAlbum: "Leave Me Alone (Single)", OriginalTrack: 2,
	}
	got, _ := Format(tr)
	if got.Album != "Leave Me Alone (Single)" || got.TrackNumber != 2 || got.DiscNumber != 0 {
		t.Errorf("not restored: %+v", got)
	}
	if got.OriginalAlbum != "" || got.OriginalTrack != 0 {
		t.Errorf("original fields should be cleared: %+v", got)
	}
	tr.OriginalTrack = 0
	if got, _ := Format(tr); got.TrackNumber != 1 {
		t.Errorf("missing original track should become 1, got %d", got.TrackNumber)
	}
}

func TestFormatASCII(t *testing.T) {
	tr := Track{
		Title: "Don’t Stop ’Til You Get Enough", Album: "“Off the Wall” – Special…",
		Artist: "Michael Jackson", Artists: []string{"Bj‘ork"}, AlbumArtist: "A — B", Genre: "R‘n’B",
	}
	got, _ := Format(tr)
	want := Track{
		Title: "Don't Stop 'Til You Get Enough", Album: `"Off the Wall" - Special...`,
		Artist: "Michael Jackson", Artists: []string{"Bj'ork"}, AlbumArtist: "A - B", Genre: "R'n'B",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got  %+v\nwant %+v", got, want)
	}
}

func TestFormatMovesFeatIntoTitle(t *testing.T) {
	cases := []struct{ title, artist, wantTitle, wantArtist string }{
		{"I Just Can't Stop Loving You", "Michael Jackson feat. Siedah Garrett",
			"I Just Can't Stop Loving You (feat. Siedah Garrett)", "Michael Jackson"},
		{"Song", "A ft. B & C", "Song (feat. B & C)", "A"},
		{"Song", "A Featuring B", "Song (feat. B)", "A"},
		{"Song", "A (feat. B)", "Song (feat. B)", "A"},
		{"Song", "A feat B", "Song (feat. B)", "A"},
		// the title already names the guest: only Artist is cleaned
		{"Song (feat. B)", "A feat. B", "Song (feat. B)", "A"},
		{"Song [ft. B]", "A feat. B", "Song [ft. B]", "A"},
		// never split on & or inside words
		{"The Boxer", "Simon & Garfunkel", "The Boxer", "Simon & Garfunkel"},
		{"Around the World", "Daft Punk", "Around the World", "Daft Punk"},
		{"Song", "Loft Feature", "Song", "Loft Feature"},
		// no title to move the guest into: leave it
		{"", "A feat. B", "", "A feat. B"},
	}
	for _, c := range cases {
		got, _ := Format(Track{Title: c.title, Artist: c.artist})
		if got.Title != c.wantTitle || got.Artist != c.wantArtist {
			t.Errorf("%q / %q: got %q / %q, want %q / %q", c.title, c.artist, got.Title, got.Artist, c.wantTitle, c.wantArtist)
		}
	}
}

func TestMainArtist(t *testing.T) {
	for in, want := range map[string]string{"A feat. B": "A", "A (ft. B)": "A", "Simon & Garfunkel": "Simon & Garfunkel", "": ""} {
		if got := MainArtist(in); got != want {
			t.Errorf("MainArtist(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestFormatShrinksCover(t *testing.T) {
	tr := Track{Cover: &Picture{MIME: "image/jpeg", Data: testutil.JPEG(t, 1200, 900, color.Black)}}
	got, err := Format(tr)
	if err != nil {
		t.Fatal(err)
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(got.Cover.Data))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Width != CoverMaxPx || cfg.Height != 450 {
		t.Errorf("cover is %dx%d, want %dx450", cfg.Width, cfg.Height, CoverMaxPx)
	}
	small := Track{Cover: &Picture{MIME: "image/jpeg", Data: testutil.JPEG(t, 20, 20, color.Black)}}
	if got, _ := Format(small); !bytes.Equal(got.Cover.Data, small.Cover.Data) {
		t.Error("a small cover should be kept as is")
	}
}

func TestFormatIsIdempotent(t *testing.T) {
	once, err := Format(sampleTrack(t))
	if err != nil {
		t.Fatal(err)
	}
	once.Title, once.Artist = "Bad", "Michael Jackson feat. Someone"
	once, _ = Format(once)
	twice, _ := Format(once)
	if d := Diff(once, twice); len(d) != 0 {
		t.Errorf("second Format changed %v", d)
	}
}
