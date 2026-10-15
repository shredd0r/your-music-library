package library

import (
	"bytes"
	"context"
	"image/color"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"your-music-library/internal/config"
	"your-music-library/internal/metadata"
	"your-music-library/internal/tags"
	"your-music-library/internal/testutil"
)

// inbox creates tagged files outside the library and returns their paths.
func inbox(t *testing.T, files map[string]tags.Track) (string, []string) {
	t.Helper()
	dir := t.TempDir()
	var out []string
	for name, tr := range files {
		p := testutil.CopyAudio(t, filepath.Ext(name)[1:], filepath.Join(dir, name))
		if err := tags.Write(p, tr); err != nil {
			t.Fatal(err)
		}
		out = append(out, p)
	}
	sort.Strings(out)
	return dir, out
}

func badIdentifier() *metadata.Identifier {
	return &metadata.Identifier{Providers: []metadata.Provider{&fakeProvider{releases: map[string]metadata.Release{"mb-bad": badRelease()}}}, MinScore: 0.85}
}

func TestIngestIdentifiesCopiesAndSkipsDuplicates(t *testing.T) {
	l := newLib(t)
	ctx := context.Background()
	dir, files := inbox(t, map[string]tags.Track{
		"rip/1.flac": {Title: "bad", Artist: "Michael Jackson", Album: "Bad"},
		"rip/3.mp3":  {Title: "Speed Demon", Artist: "Michael Jackson", Album: "Bad"},
	})
	opts := IngestOptions{Base: dir}

	plan, err := l.PlanIngest(ctx, files, badIdentifier(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Items) != 2 || len(plan.Matches) != 1 {
		t.Fatalf("plan = %+v", plan)
	}
	targets := []string{plan.Items[0].Target, plan.Items[1].Target}
	sort.Strings(targets)
	if targets[0] != "Michael Jackson/Bad/01 - Bad.flac" || targets[1] != "Michael Jackson/Bad/03 - Speed Demon.mp3" {
		t.Errorf("targets = %v", targets)
	}
	if entries, _ := os.ReadDir(l.Root()); len(entries) != 0 {
		t.Fatal("planning wrote into the library")
	}

	cover := testutil.JPEG(t, 900, 900, color.White)
	r, err := l.ApplyIngest(ctx, plan, config.Default().Presets["full"], fakeImages{data: cover}, opts)
	if err != nil || len(r.Imported) != 2 || len(r.Failed) != 0 {
		t.Fatalf("apply = %+v %v", r, err)
	}
	got, err := tags.Read(l.abs("Michael Jackson/Bad/03 - Speed Demon.mp3"))
	if err != nil || got.TrackNumber != 3 || got.IDs.MBReleaseID != "mb-bad" || got.Cover == nil {
		t.Errorf("imported tags = %+v %v", got, err)
	}
	if saved, _ := os.ReadFile(l.abs("Michael Jackson/Bad/cover.jpg")); !bytes.Equal(saved, cover) {
		t.Error("cover.jpg missing from the album folder")
	}
	if exists(l.abs(StagingDir)) {
		t.Error("staging folder left behind")
	}
	for _, f := range files {
		if !exists(f) {
			t.Errorf("copy mode removed source %s", f)
		}
	}
	if es := entries(t, l); len(es) != 2 {
		t.Errorf("index = %v", es)
	}
	if r := scan(t, l); r.Unchanged != 2 || r.Added+r.Removed != 0 {
		t.Errorf("index out of sync: %+v", r)
	}

	// the same files again are recognized as duplicates
	plan, _ = l.PlanIngest(ctx, files, badIdentifier(), opts)
	if len(plan.Items) != 0 || len(plan.Duplicates) != 2 {
		t.Errorf("second plan = %+v", plan)
	}
}

func TestIngestMoveWithoutLookup(t *testing.T) {
	l := newLib(t)
	ctx := context.Background()
	dir, files := inbox(t, map[string]tags.Track{
		"a.flac": {Title: "Song", Artist: "Someone", Album: "Record", TrackNumber: 4},
	})
	opts := IngestOptions{Base: dir, Move: true}
	plan, err := l.PlanIngest(ctx, files, nil, opts)
	if err != nil || len(plan.Items) != 1 || plan.Items[0].Target != "Someone/Record/04 - Song.flac" {
		t.Fatalf("plan = %+v %v", plan, err)
	}
	if _, err := l.ApplyIngest(ctx, plan, config.Default().Presets["full"], nil, opts); err != nil {
		t.Fatal(err)
	}
	if exists(files[0]) || !exists(l.abs("Someone/Record/04 - Song.flac")) {
		t.Error("move mode should delete the source after import")
	}
}

func TestIngestReviewConflictsAndFailures(t *testing.T) {
	l := newLib(t)
	ctx := context.Background()
	dir, files := inbox(t, map[string]tags.Track{
		"live/1.flac": {Title: "Bad", Artist: "Michael Jackson", Album: "Bad (Live)", TrackNumber: 1},
		"x/a.flac":    {Title: "Same", Artist: "A", Album: "B", TrackNumber: 1},
		"x/b.flac":    {Title: "Same", Artist: "A", Album: "B", TrackNumber: 1},
	})
	broken := filepath.Join(dir, "x", "broken.flac")
	os.WriteFile(broken, []byte("nope"), 0o644)
	m4a := filepath.Join(dir, "x", "c.m4a")
	os.WriteFile(m4a, []byte("nope"), 0o644)
	all, _ := CollectAudio(dir)
	if len(all) != 5 {
		t.Fatalf("CollectAudio = %v", all)
	}
	_ = files

	strict := &metadata.Identifier{Providers: badIdentifier().Providers, MinScore: 0.99}
	opts := IngestOptions{Base: dir}
	plan, err := l.PlanIngest(ctx, all, strict, opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Unsupported) != 1 || len(plan.Failed) != 1 || len(plan.Conflicts) != 1 || len(plan.Reviews) == 0 {
		t.Fatalf("plan = %+v", plan)
	}
	r, err := l.ApplyIngest(ctx, plan, config.Default().Presets["full"], nil, opts)
	if err != nil || len(r.Imported) != 2 {
		t.Fatalf("apply = %+v %v", r, err)
	}
	reviews, _ := l.Reviews(ctx)
	found := false
	for _, rv := range reviews {
		for _, p := range rv.Paths {
			found = found || p == "Michael Jackson/Bad (Live)/01 - Bad.flac"
			if !exists(l.abs(p)) {
				t.Errorf("review points to missing %s", p)
			}
		}
	}
	if !found {
		t.Errorf("review for the live album should point to its library path: %+v", reviews)
	}
}
