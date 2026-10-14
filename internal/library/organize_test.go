package library

import (
	"context"
	"os"
	"reflect"
	"testing"

	"your-music-library/internal/tags"
)

func plan(t *testing.T, l *Library) OrganizePlan {
	t.Helper()
	p, err := l.PlanOrganize(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func apply(t *testing.T, l *Library, p OrganizePlan) ApplyReport {
	t.Helper()
	r, err := l.ApplyOrganize(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestOrganizeMovesFilesImagesAndCleansUp(t *testing.T) {
	l := newLib(t)
	addFile(t, l, "inbox/mj/a.flac", tags.Track{Title: "Bad", Album: "Bad", AlbumArtist: "Michael Jackson", TrackNumber: 1})
	addFile(t, l, "inbox/mj/b.mp3", tags.Track{Title: "Speed Demon", Album: "Bad", Artist: "Michael Jackson", TrackNumber: 3})
	os.WriteFile(l.abs("inbox/mj/cover.jpg"), []byte("img"), 0o644)
	os.WriteFile(l.abs("inbox/mj/.DS_Store"), []byte("x"), 0o644)
	scan(t, l)

	p := plan(t, l)
	want := []Move{
		{"inbox/mj/a.flac", "Michael Jackson/Bad/01 - Bad.flac"},
		{"inbox/mj/b.mp3", "Michael Jackson/Bad/03 - Speed Demon.mp3"},
	}
	if !reflect.DeepEqual(p.Moves, want) {
		t.Fatalf("Moves = %+v", p.Moves)
	}
	if !reflect.DeepEqual(p.Images, []Move{{"inbox/mj/cover.jpg", "Michael Jackson/Bad/cover.jpg"}}) {
		t.Errorf("Images = %+v", p.Images)
	}
	if !exists(l.abs("inbox/mj/a.flac")) {
		t.Fatal("planning must not move files")
	}

	r := apply(t, l, p)
	if r.Changed != 3 || len(r.Failed) != 0 {
		t.Errorf("report = %+v", r)
	}
	for _, m := range append(want, p.Images...) {
		if !exists(l.abs(m.To)) || exists(l.abs(m.From)) {
			t.Errorf("%s not moved to %s", m.From, m.To)
		}
	}
	if exists(l.abs("inbox")) {
		t.Error("empty source folders should be removed")
	}
	es := entries(t, l)
	if _, ok := es["Michael Jackson/Bad/01 - Bad.flac"]; !ok || len(es) != 2 {
		t.Errorf("index not updated: %v", es)
	}
	// running again changes nothing
	p = plan(t, l)
	if len(p.Moves) != 0 || p.Unchanged != 2 {
		t.Errorf("second plan: %+v", p)
	}
	if r := scan(t, l); r.Unchanged != 2 || r.Added+r.Removed != 0 {
		t.Errorf("index out of sync with disk after organize: %+v", r)
	}
}

func TestOrganizeMultiDisc(t *testing.T) {
	l := newLib(t)
	// only one track carries DISCTOTAL; the album is still multi-disc
	addFile(t, l, "a.flac", tags.Track{Title: "In the Flesh?", Album: "The Wall", AlbumArtist: "Pink Floyd", TrackNumber: 1, DiscNumber: 1})
	addFile(t, l, "b.flac", tags.Track{Title: "Hey You", Album: "The Wall", AlbumArtist: "Pink Floyd", TrackNumber: 1, DiscNumber: 2, DiscTotal: 2})
	scan(t, l)
	got := map[string]string{}
	for _, m := range plan(t, l).Moves {
		got[m.From] = m.To
	}
	if got["a.flac"] != "Pink Floyd/The Wall/1-01 - In the Flesh_.flac" || got["b.flac"] != "Pink Floyd/The Wall/2-01 - Hey You.flac" {
		t.Errorf("moves = %v", got)
	}
}

func TestOrganizeConflictsNeverOverwrite(t *testing.T) {
	l := newLib(t)
	same := tags.Track{Title: "T", Album: "A", AlbumArtist: "X", TrackNumber: 1}
	addFile(t, l, "one.flac", same)
	addFile(t, l, "two.flac", same)
	// a non-indexed file already sits at another track's target
	addFile(t, l, "three.mp3", tags.Track{Title: "U", Album: "A", AlbumArtist: "X", TrackNumber: 2})
	scan(t, l)
	os.MkdirAll(l.abs("X/A"), 0o755)
	if err := os.WriteFile(l.abs("X/A/02 - U.mp3"), []byte("someone else's"), 0o644); err != nil {
		t.Fatal(err)
	}

	p := plan(t, l)
	if len(p.Moves) != 1 || p.Moves[0].From != "one.flac" {
		t.Errorf("Moves = %+v", p.Moves)
	}
	if len(p.Conflicts) != 2 {
		t.Fatalf("Conflicts = %+v", p.Conflicts)
	}
	apply(t, l, p)
	if data, _ := os.ReadFile(l.abs("X/A/02 - U.mp3")); string(data) != "someone else's" {
		t.Error("existing file was overwritten")
	}
	if !exists(l.abs("two.flac")) || !exists(l.abs("three.mp3")) {
		t.Error("conflicting files must stay where they are")
	}
}

func TestOrganizeFixesLetterCase(t *testing.T) {
	l := newLib(t)
	// non-ASCII folder name guards the character-vs-byte path rewrite
	addFile(t, l, "michael jackson/bad/01 - bad.flac", tags.Track{Title: "Bad", Album: "Bad", AlbumArtist: "Michael Jackson", TrackNumber: 1})
	addFile(t, l, "michael jackson/bad/02 - smooth.flac", tags.Track{Title: "Smooth", Album: "Bad", AlbumArtist: "Michael Jackson", TrackNumber: 2})
	addFile(t, l, "beyoncé/lemonade/01 - pray you catch me.flac", tags.Track{Title: "Pray You Catch Me", Album: "Lemonade", AlbumArtist: "Beyoncé", TrackNumber: 1})
	scan(t, l)
	p := plan(t, l)
	if len(p.Moves) != 3 || len(p.Conflicts) != 0 {
		t.Fatalf("plan = %+v", p)
	}
	if r := apply(t, l, p); len(r.Failed) != 0 {
		t.Fatalf("failed: %+v", r.Failed)
	}
	names := func(dir string) []string {
		des, _ := os.ReadDir(l.abs(dir))
		var out []string
		for _, d := range des {
			out = append(out, d.Name())
		}
		return out
	}
	if got := names("."); !reflect.DeepEqual(got, []string{"Beyoncé", "Michael Jackson"}) {
		t.Errorf("root = %v", got)
	}
	if got := names("Michael Jackson/Bad"); !reflect.DeepEqual(got, []string{"01 - Bad.flac", "02 - Smooth.flac"}) {
		t.Errorf("album folder = %v", got)
	}
	if r := scan(t, l); r.Unchanged != 3 || r.Added+r.Removed != 0 {
		t.Errorf("index out of sync after case fix: %+v", r)
	}
}

func TestOrganizeMergingFoldersImagesConflictInPlan(t *testing.T) {
	l := newLib(t)
	addFile(t, l, "s1/a.flac", tags.Track{Title: "A", Album: "Ado Singles", AlbumArtist: "Ado", TrackNumber: 1})
	addFile(t, l, "s2/b.flac", tags.Track{Title: "B", Album: "Ado Singles", AlbumArtist: "Ado", TrackNumber: 2})
	os.WriteFile(l.abs("s1/cover.jpg"), []byte("one"), 0o644)
	os.WriteFile(l.abs("s2/cover.jpg"), []byte("two"), 0o644)
	scan(t, l)

	p := plan(t, l)
	if len(p.Images) != 1 || len(p.Conflicts) != 1 || p.Conflicts[0].From != "s2/cover.jpg" {
		t.Fatalf("plan = %+v", p)
	}
	if r := apply(t, l, p); len(r.Failed) != 0 {
		t.Errorf("apply failed: %+v", r.Failed)
	}
	if data, _ := os.ReadFile(l.abs("Ado/Ado Singles/cover.jpg")); string(data) != "one" {
		t.Errorf("cover = %q", data)
	}
	if !exists(l.abs("s2/cover.jpg")) {
		t.Error("conflicting image must stay in place")
	}
}
