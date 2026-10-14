package library

import (
	"os"
	"reflect"
	"testing"
	"time"

	"your-music-library/internal/tags"
)

func TestScanIndexesAndTracksChanges(t *testing.T) {
	l := newLib(t)
	a := addFile(t, l, "x/a.flac", tags.Track{Title: "A", Artist: "Art", Artists: []string{"Art", "Feat"}, DiscNumber: 1, DiscTotal: 2})
	addFile(t, l, "x/b.mp3", tags.Track{Title: "B"})
	os.WriteFile(l.abs("x/notes.txt"), []byte("hi"), 0o644)
	os.WriteFile(l.abs("x/c.m4a"), []byte("fake"), 0o644)
	os.WriteFile(l.abs("x/._a.flac"), []byte("apple double"), 0o644)
	os.MkdirAll(l.abs(".hidden"), 0o755)
	os.WriteFile(l.abs(".hidden/z.flac"), []byte("x"), 0o644)
	os.WriteFile(l.abs("x/broken.flac"), []byte("not flac"), 0o644)

	before, _ := os.Stat(a)
	r := scan(t, l)
	if r.Added != 2 || r.Updated != 0 || r.Unchanged != 0 || r.Removed != 0 {
		t.Errorf("first scan: %+v", r)
	}
	if !reflect.DeepEqual(r.Unsupported, []string{"x/c.m4a"}) {
		t.Errorf("Unsupported = %v", r.Unsupported)
	}
	if len(r.Failed) != 1 || r.Failed[0].Path != "x/broken.flac" {
		t.Errorf("Failed = %v", r.Failed)
	}
	after, _ := os.Stat(a)
	if !before.ModTime().Equal(after.ModTime()) {
		t.Error("scan modified a file")
	}

	es := entries(t, l)
	e := es["x/a.flac"]
	if e.Title != "A" || !reflect.DeepEqual(e.Artists, []string{"Art", "Feat"}) || e.DiscTotal != 2 {
		t.Errorf("entry = %+v", e)
	}

	// second scan: nothing re-read
	if r := scan(t, l); r.Unchanged != 2 || r.Added+r.Updated+r.Removed != 0 {
		t.Errorf("rescan: %+v", r)
	}

	// change one file, delete another
	if err := tags.Write(a, tags.Track{Title: "A2"}); err != nil {
		t.Fatal(err)
	}
	os.Chtimes(a, time.Now(), time.Now().Add(time.Hour))
	os.Remove(l.abs("x/b.mp3"))
	r = scan(t, l)
	if r.Updated != 1 || r.Removed != 1 || r.Unchanged != 0 {
		t.Errorf("after changes: %+v", r)
	}
	if got := entries(t, l)["x/a.flac"].Title; got != "A2" {
		t.Errorf("title not updated: %q", got)
	}
}
