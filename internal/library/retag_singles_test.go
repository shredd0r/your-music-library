package library

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"os"
	"reflect"
	"testing"

	"github.com/go-flac/flacvorbis/v2"
	flac "github.com/go-flac/go-flac/v2"

	"your-music-library/internal/config"
	"your-music-library/internal/tags"
	"your-music-library/internal/testutil"
)

func TestRetagMP3PresetSavesFullCoverFirst(t *testing.T) {
	l := newLib(t)
	big := testutil.JPEG(t, 1200, 1200, color.RGBA{200, 0, 0, 255})
	full := tags.Track{
		Title: "Bad", Album: "Bad", Artist: "MJ", Artists: []string{"MJ", "X"}, TrackNumber: 1,
		Cover: &tags.Picture{MIME: "image/jpeg", Data: big},
		Back:  &tags.Picture{MIME: "image/jpeg", Data: testutil.JPEG(t, 50, 50, color.Black)},
		IDs:   tags.Identity{ISRC: "X1"},
	}
	addFile(t, l, "MJ/Bad/01.flac", full)
	addFile(t, l, "MJ/Bad/02.flac", full)
	scan(t, l)
	preset := config.Default().Presets["mp3-player"]

	ctx := context.Background()
	p, err := l.PlanRetag(ctx, preset)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Items) != 2 {
		t.Fatalf("items = %+v", p.Items)
	}
	var fields []config.Field
	for _, c := range p.Items[0].Changes {
		fields = append(fields, c.Field)
	}
	if !reflect.DeepEqual(fields, []config.Field{config.FieldArtists, config.FieldPictureCover, config.FieldPictureBack}) {
		t.Errorf("changed fields = %v", fields)
	}
	if !reflect.DeepEqual(p.Items[0].SaveImages, []string{"cover.jpg", "back.jpg"}) || p.Items[1].SaveImages != nil {
		t.Errorf("SaveImages = %v / %v", p.Items[0].SaveImages, p.Items[1].SaveImages)
	}
	if exists(l.abs("MJ/Bad/cover.jpg")) {
		t.Fatal("planning wrote files")
	}

	if r, err := l.ApplyRetag(ctx, p, preset); err != nil || r.Changed != 2 || len(r.Failed) != 0 {
		t.Fatalf("apply: %+v %v", r, err)
	}
	saved, _ := os.ReadFile(l.abs("MJ/Bad/cover.jpg"))
	if !bytes.Equal(saved, big) {
		t.Error("cover.jpg must be the original full-size image")
	}
	got, _ := tags.Read(l.abs("MJ/Bad/01.flac"))
	cfg, _, _ := image.DecodeConfig(bytes.NewReader(got.Cover.Data))
	if cfg.Width != 600 || got.Artists != nil || got.Back != nil || got.IDs.ISRC != "X1" {
		t.Errorf("after retag: cover %dpx, %+v", cfg.Width, got)
	}
	if p, _ := l.PlanRetag(ctx, preset); len(p.Items) != 0 || p.Unchanged != 2 {
		t.Errorf("retag should be idempotent: %+v", p)
	}
}

func TestSinglesRoundTrip(t *testing.T) {
	l := newLib(t)
	addFile(t, l, "a.flac", tags.Track{Title: "Later", Album: "Later", Artist: "Ado", Date: "2023-05-01", TrackNumber: 1})
	addFile(t, l, "b.flac", tags.Track{Title: "Usseewa", Album: "Usseewa - Single", Artist: "Ado", Date: "2020-10-23", TrackNumber: 1})
	addFile(t, l, "c.mp3", tags.Track{Title: "B-side", Album: "Show", Artist: "Ado", Date: "2023-01-01", TrackNumber: 2, ReleaseType: "single"})
	// album tracks are left alone
	addFile(t, l, "d.flac", tags.Track{Title: "Kyogen", Album: "Kyogen", Artist: "Ado", TrackNumber: 1, ReleaseType: "album"})
	addFile(t, l, "e.flac", tags.Track{Title: "Odo", Album: "Kyogen", Artist: "Ado", TrackNumber: 2})
	scan(t, l)
	ctx := context.Background()

	items, err := l.PlanSingles(ctx, config.SinglesGrouped)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 3 {
		t.Fatalf("items = %+v", items)
	}
	if _, err := l.ApplySingles(ctx, items); err != nil {
		t.Fatal(err)
	}
	type row struct {
		Album  string
		N, Tot int
	}
	got := map[string]row{}
	for p, e := range entries(t, l) {
		got[p] = row{e.Album, e.TrackNumber, e.TrackTotal}
	}
	want := map[string]row{
		"b.flac": {"Ado Singles", 1, 3}, // 2020
		"c.mp3":  {"Ado Singles", 2, 3}, // 2023-01
		"a.flac": {"Ado Singles", 3, 3}, // 2023-05
		"d.flac": {"Kyogen", 1, 0},
		"e.flac": {"Kyogen", 2, 0},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("grouped:\n got %v\nwant %v", got, want)
	}
	if items, _ := l.PlanSingles(ctx, config.SinglesGrouped); len(items) != 0 {
		t.Errorf("grouping twice should be a no-op: %+v", items)
	}

	// a new single with an earlier date renumbers the group
	addFile(t, l, "f.flac", tags.Track{Title: "Gira Gira", Album: "Gira Gira", Artist: "Ado", Date: "2021-01-01", TrackNumber: 1})
	scan(t, l)
	items, _ = l.PlanSingles(ctx, config.SinglesGrouped)
	l.ApplySingles(ctx, items)
	if e := entries(t, l)["a.flac"]; e.TrackNumber != 4 || e.TrackTotal != 4 {
		t.Errorf("renumbering failed: %d/%d", e.TrackNumber, e.TrackTotal)
	}

	// and back
	items, _ = l.PlanSingles(ctx, config.SinglesAsAlbum)
	if len(items) != 4 {
		t.Fatalf("revert items = %d", len(items))
	}
	l.ApplySingles(ctx, items)
	es := entries(t, l)
	if es["b.flac"].Album != "Usseewa - Single" || es["c.mp3"].Album != "Show" || es["c.mp3"].TrackNumber != 2 || es["c.mp3"].OriginalAlbum != "" {
		t.Errorf("revert: b=%+v c=%+v", es["b.flac"].Track, es["c.mp3"].Track)
	}
}

func TestRetagRewritesUnpaddedTrackNumbers(t *testing.T) {
	l := newLib(t)
	path := addFile(t, l, "a.flac", tags.Track{Title: "A"})
	// simulate a file tagged by another tool: TRACKNUMBER=3/12
	setRawTrack(t, path, "3/12")
	scan(t, l)
	ctx := context.Background()
	preset := config.Default().Presets["full"]

	p, err := l.PlanRetag(ctx, preset)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Items) != 1 {
		t.Fatalf("items = %+v", p.Items)
	}
	want := []tags.Change{{Field: config.FieldTrackNumber, Old: "3/12", New: "03"}}
	if !reflect.DeepEqual(p.Items[0].Changes, want) {
		t.Errorf("changes = %+v", p.Items[0].Changes)
	}
	if _, err := l.ApplyRetag(ctx, p, preset); err != nil {
		t.Fatal(err)
	}
	if s, _ := tags.StoredTrackNumber(path); s != "03" {
		t.Errorf("stored = %q", s)
	}
	if got, _ := tags.Read(path); got.TrackNumber != 3 || got.TrackTotal != 12 {
		t.Errorf("number/total lost: %d/%d", got.TrackNumber, got.TrackTotal)
	}
	if p, _ := l.PlanRetag(ctx, preset); len(p.Items) != 0 {
		t.Errorf("second retag should be a no-op: %+v", p.Items)
	}
}

func setRawTrack(t *testing.T, path, value string) {
	t.Helper()
	f, err := flac.ParseFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range f.Meta {
		if b.Type == flac.VorbisComment {
			c, _ := flacvorbis.ParseFromMetaDataBlock(*b)
			c.Comments = append(c.Comments, "TRACKNUMBER="+value)
			*b = c.Marshal()
		}
	}
	if err := f.Save(path + ".tmp"); err != nil {
		t.Fatal(err)
	}
	f.Close()
	if err := os.Rename(path+".tmp", path); err != nil {
		t.Fatal(err)
	}
}
