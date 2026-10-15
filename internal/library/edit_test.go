package library

import (
	"bytes"
	"context"
	"image/color"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"your-music-library/internal/config"
	"your-music-library/internal/tags"
	"your-music-library/internal/testutil"
)

func TestEditApply(t *testing.T) {
	base := tags.Track{Title: "Old", Artists: []string{"A"}, TrackNumber: 1, TrackTotal: 9, Genre: "Pop"}
	got, err := TagEdit{
		config.FieldTitle:       " New ",
		config.FieldArtists:     "A; B ;;C",
		config.FieldTrackNumber: "3/12",
		config.FieldDiscNumber:  "2",
		config.FieldDate:        "1979-08-10",
		config.FieldGenre:       "",
	}.Apply(base)
	if err != nil {
		t.Fatal(err)
	}
	want := tags.Track{Title: "New", Artists: []string{"A", "B", "C"}, TrackNumber: 3, TrackTotal: 12,
		DiscNumber: 2, Date: "1979-08-10"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v\nwant %+v", got, want)
	}
	for f, v := range FieldValues(want) {
		if back, _ := (TagEdit{f: v}).Apply(want); !reflect.DeepEqual(back, want) {
			t.Errorf("%s: FieldValue %q does not round-trip", f, v)
		}
	}
}

// FieldValues is a test helper listing every editable field's value.
func FieldValues(t tags.Track) map[config.Field]string {
	m := map[config.Field]string{}
	for _, f := range EditableFields {
		m[f] = FieldValue(t, f)
	}
	return m
}

func TestEditRejectsBadValues(t *testing.T) {
	for _, e := range []TagEdit{
		{config.FieldDate: "10.08.1979"},
		{config.FieldDate: "1979-8"},
		{config.FieldTrackNumber: "three"},
		{config.FieldTrackNumber: "0"},
		{config.FieldTrackNumber: "13/12"},
		{config.FieldDiscNumber: "1/x"},
		{config.FieldPictureCover: "x"},
	} {
		if _, err := e.Apply(tags.Track{}); err == nil {
			t.Errorf("%v accepted", e)
		}
	}
}

func TestPlanAndApplyEditKeepsOtherTags(t *testing.T) {
	l := newLib(t)
	cover := &tags.Picture{MIME: "image/jpeg", Data: testutil.JPEG(t, 20, 20, color.White)}
	addFile(t, l, "A/X/01.flac", tags.Track{Title: "One", Album: "X", Artist: "A", TrackNumber: 1, Cover: cover,
		IDs: tags.Identity{MBReleaseID: "rel-1"}})
	addFile(t, l, "A/X/02.mp3", tags.Track{Title: "Two", Album: "x", Artist: "A", TrackNumber: 2})
	addFile(t, l, "A/X/03.mp3", tags.Track{Title: "Three", Album: "X (Deluxe)", Artist: "A", TrackNumber: 3})
	scan(t, l)
	ctx := context.Background()
	paths := []string{"A/X/01.flac", "A/X/02.mp3", "A/X/03.mp3"}

	plan, err := l.PlanEdit(ctx, paths, TagEdit{config.FieldAlbum: "X", config.FieldDate: "1979"})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Items) != 3 || plan.Unchanged != 0 {
		t.Fatalf("plan = %+v", plan)
	}
	if c := plan.Items[1].Changes; len(c) != 2 || c[0].Field != config.FieldAlbum || c[0].Old != "x" {
		t.Errorf("changes of 02 = %+v", c)
	}
	// planning wrote nothing
	if e := entries(t, l)["A/X/02.mp3"]; e.Album != "x" {
		t.Fatalf("plan changed the file: %q", e.Album)
	}

	r, err := l.ApplyEdit(ctx, plan)
	if err != nil || r.Changed != 3 || len(r.Failed) != 0 {
		t.Fatalf("apply = %+v, %v", r, err)
	}
	es := entries(t, l)
	for _, p := range paths {
		if es[p].Album != "X" || es[p].Date != "1979" {
			t.Errorf("%s indexed as %q %q", p, es[p].Album, es[p].Date)
		}
	}
	got, err := tags.Read(l.abs("A/X/01.flac"))
	if err != nil {
		t.Fatal(err)
	}
	if got.Cover == nil || !bytes.Equal(got.Cover.Data, cover.Data) || got.IDs.MBReleaseID != "rel-1" {
		t.Errorf("edit lost the cover or IDs: %+v", got.IDs)
	}

	again, err := l.PlanEdit(ctx, paths, TagEdit{config.FieldAlbum: "X"})
	if err != nil || len(again.Items) != 0 || again.Unchanged != 3 {
		t.Errorf("second plan = %+v, %v", again, err)
	}
	if _, err := l.PlanEdit(ctx, paths, TagEdit{config.FieldDate: "soon"}); err == nil {
		t.Error("bad date planned")
	}
}

func TestCoverEmbeddedThenFolder(t *testing.T) {
	l := newLib(t)
	embedded := testutil.JPEG(t, 8, 8, color.Black)
	addFile(t, l, "A/X/01.flac", tags.Track{Title: "One", Cover: &tags.Picture{MIME: "image/jpeg", Data: embedded}})
	addFile(t, l, "A/Y/01.mp3", tags.Track{Title: "Two"})
	addFile(t, l, "A/Z/01.mp3", tags.Track{Title: "Three"})
	folder := testutil.JPEG(t, 9, 9, color.White)
	if err := os.WriteFile(filepath.Join(l.Root(), "A", "Y", "folder.jpg"), folder, 0o644); err != nil {
		t.Fatal(err)
	}
	// a back cover alone is not a front cover
	if err := os.WriteFile(filepath.Join(l.Root(), "A", "Z", "back.jpg"), folder, 0o644); err != nil {
		t.Fatal(err)
	}
	for rel, want := range map[string][]byte{"A/X/01.flac": embedded, "A/Y/01.mp3": folder, "A/Z/01.mp3": nil} {
		got, err := l.Cover(rel)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("%s: got %d bytes, want %d", rel, len(got), len(want))
		}
	}
}
