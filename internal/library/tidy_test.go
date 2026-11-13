package library

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"your-music-library/internal/tags"
)

func TestTidyPreviewsMovesFromFormattedTags(t *testing.T) {
	l := newLib(t)
	addFile(t, l, "in/x.flac", tags.Track{Title: "Song", Artist: "A feat. B", AlbumArtist: "A", Album: "Rec", TrackNumber: 2})
	addFile(t, l, "A/Rec/01 - Done.flac", tags.Track{Title: "Done", Artist: "A", AlbumArtist: "A", Album: "Rec", TrackNumber: 1})
	scan(t, l)
	ctx := context.Background()

	p, err := l.PlanTidy(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Retag.Items) != 1 || p.Retag.Items[0].Path != "in/x.flac" {
		t.Fatalf("retag = %+v", p.Retag.Items)
	}
	// the move uses the title after Format: the guest is in it
	want := []Move{{"in/x.flac", "A/Rec/02 - Song (feat. B).flac"}}
	if !reflect.DeepEqual(p.Organize.Moves, want) {
		t.Errorf("moves = %+v, want %+v", p.Organize.Moves, want)
	}
	if exists(l.abs("A/Rec/02 - Song (feat. B).flac")) {
		t.Fatal("planning changed files")
	}

	r, err := l.ApplyTidy(ctx, p)
	if err != nil || r.Retag.Changed != 1 || r.Organize.Changed != 1 || len(r.Retag.Failed)+len(r.Organize.Failed) != 0 {
		t.Fatalf("apply = %+v, %v", r, err)
	}
	got, err := tags.Read(l.abs("A/Rec/02 - Song (feat. B).flac"))
	if err != nil || got.Artist != "A" {
		t.Errorf("moved file: %+v, %v", got, err)
	}
	if p, _ := l.PlanTidy(ctx); len(p.Retag.Items)+len(p.Organize.Moves) != 0 {
		t.Errorf("second tidy should be a no-op: %+v", p)
	}
}

func TestTidyNamesPlaylistsWithMovedFiles(t *testing.T) {
	l := newLib(t)
	addFile(t, l, "in/x.flac", tags.Track{Title: "Song", Artist: "A", Album: "Rec", TrackNumber: 1})
	addFile(t, l, "A/Rec/02 - Stay.flac", tags.Track{Title: "Stay", Artist: "A", Album: "Rec", TrackNumber: 2})
	scan(t, l)
	write := func(name, body string) {
		p := filepath.Join(l.Root(), "Playlists", name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("Road trip.m3u8", "#EXTM3U\n#EXTINF:1,A - Song\n../in/x.flac\n")
	write("Abs.m3u", filepath.Join(l.Root(), "IN", "X.flac")+"\r\n") // absolute, other letter case
	write("Calm.m3u8", "../A/Rec/02 - Stay.flac\n")
	write("notes.txt", "../in/x.flac\n")

	p, err := l.PlanTidy(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"Abs", "Road trip"}; !reflect.DeepEqual(p.Playlists, want) {
		t.Errorf("Playlists = %v, want %v", p.Playlists, want)
	}
}

func TestRebuildRereadsEveryFile(t *testing.T) {
	l := newLib(t)
	path := addFile(t, l, "a.flac", tags.Track{Title: "Old"})
	scan(t, l)
	info, _ := os.Stat(path)
	if err := tags.Write(path, tags.Track{Title: "New"}); err != nil {
		t.Fatal(err)
	}
	// same size and modification time: a normal scan can't see the change
	if err := os.Chtimes(path, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	if after, _ := os.Stat(path); after.Size() != info.Size() {
		t.Skip("tag rewrite changed the file size; the case needs equal sizes")
	}
	if r := scan(t, l); r.Updated != 0 || entries(t, l)["a.flac"].Title != "Old" {
		t.Fatalf("scan should skip the unchanged-looking file: %+v", r)
	}
	r, err := l.Rebuild(context.Background())
	if err != nil || r.Updated != 1 {
		t.Fatalf("rebuild = %+v, %v", r, err)
	}
	if e := entries(t, l)["a.flac"]; e.Title != "New" {
		t.Errorf("entry = %+v", e)
	}
}

func TestPlanImportRefusesFoldersInsideTheLibrary(t *testing.T) {
	l := newLib(t)
	_, err := l.PlanImport(context.Background(), filepath.Join(l.Root(), "inbox"), nil, IngestOptions{})
	if err == nil || !strings.Contains(err.Error(), "inside the library") {
		t.Errorf("err = %v", err)
	}
	out, _ := inbox(t, map[string]tags.Track{"rip/1.flac": {Title: "One", Artist: "A", Album: "R", TrackNumber: 1}})
	p, err := l.PlanImport(context.Background(), out, nil, IngestOptions{})
	if err != nil || len(p.Items) != 1 || p.Items[0].Display != "rip/1.flac" || p.Items[0].Target != "A/R/01 - One.flac" {
		t.Errorf("plan = %+v, %v", p, err)
	}
}
