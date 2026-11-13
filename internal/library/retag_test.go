package library

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"os"
	"reflect"
	"testing"

	"github.com/go-flac/flacpicture/v2"
	"github.com/go-flac/flacvorbis/v2"
	flac "github.com/go-flac/go-flac/v2"

	"your-music-library/internal/tags"
	"your-music-library/internal/testutil"
)

func TestRetagFormatsAndSavesFullCoverFirst(t *testing.T) {
	l := newLib(t)
	big := testutil.JPEG(t, 1200, 1200, color.RGBA{200, 0, 0, 255})
	legacy := tags.Track{
		Title: "Bad", Album: "Bad’s", Artist: "MJ feat. X", Artists: []string{"MJ", "X"}, TrackNumber: 1,
		Cover: &tags.Picture{MIME: "image/jpeg", Data: big},
		IDs:   tags.Identity{ISRC: "X1"},
	}
	for _, rel := range []string{"MJ/Bad/01.flac", "MJ/Bad/02.flac"} {
		p := addFile(t, l, rel, legacy)
		addComments(t, p, "TRACKTOTAL=12", "REPLAYGAIN_TRACK_GAIN=-6.5 dB")
		addBackCover(t, p, testutil.JPEG(t, 50, 50, color.Black))
	}
	scan(t, l)

	ctx := context.Background()
	p, err := l.PlanRetag(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Items) != 2 {
		t.Fatalf("items = %+v", p.Items)
	}
	var fields []tags.Field
	for _, c := range p.Items[0].Changes {
		fields = append(fields, c.Field)
	}
	want := []tags.Field{tags.FieldTitle, tags.FieldAlbum, tags.FieldArtist, tags.FieldTrackNumber, tags.FieldPictureCover, tags.FieldPictureBack}
	if !reflect.DeepEqual(fields, want) {
		t.Errorf("changed fields = %v, want %v", fields, want)
	}
	if !reflect.DeepEqual(p.Items[0].SaveImages, []string{"cover.jpg"}) || p.Items[1].SaveImages != nil {
		t.Errorf("SaveImages = %v / %v", p.Items[0].SaveImages, p.Items[1].SaveImages)
	}
	if exists(l.abs("MJ/Bad/cover.jpg")) {
		t.Fatal("planning wrote files")
	}

	if r, err := l.ApplyRetag(ctx, p); err != nil || r.Changed != 2 || len(r.Failed) != 0 {
		t.Fatalf("apply: %+v %v", r, err)
	}
	saved, _ := os.ReadFile(l.abs("MJ/Bad/cover.jpg"))
	if !bytes.Equal(saved, big) {
		t.Error("cover.jpg must be the original full-size image")
	}
	if exists(l.abs("MJ/Bad/back.jpg")) {
		t.Error("back covers are not saved")
	}
	path := l.abs("MJ/Bad/01.flac")
	got, _ := tags.Read(path)
	cfg, _, _ := image.DecodeConfig(bytes.NewReader(got.Cover.Data))
	if cfg.Width != tags.CoverMaxPx || got.Back != nil || got.TrackTotal != 0 || got.IDs.ISRC != "X1" ||
		got.Title != "Bad (feat. X)" || got.Artist != "MJ" || got.Album != "Bad's" || !reflect.DeepEqual(got.Artists, []string{"MJ", "X"}) {
		t.Errorf("after retag: cover %dpx, %+v", cfg.Width, got)
	}
	if !hasComment(t, path, "REPLAYGAIN_TRACK_GAIN=-6.5 dB") {
		t.Error("unmanaged tags must be kept")
	}
	if p, _ := l.PlanRetag(ctx); len(p.Items) != 0 || p.Unchanged != 2 {
		t.Errorf("retag should be idempotent: %+v", p)
	}
}

func TestRetagRestoresGroupedSingles(t *testing.T) {
	l := newLib(t)
	path := addFile(t, l, "Ado/Ado Singles/04.flac", tags.Track{Title: "Usseewa", Album: "Ado Singles", AlbumArtist: "Ado", TrackNumber: 4})
	addComments(t, path, "YMLIB_ORIGINAL_ALBUM=Usseewa", "YMLIB_ORIGINAL_TRACK=1")
	scan(t, l)
	ctx := context.Background()

	p, err := l.PlanRetag(ctx)
	if err != nil || len(p.Items) != 1 {
		t.Fatalf("plan = %+v, %v", p, err)
	}
	if _, err := l.ApplyRetag(ctx, p); err != nil {
		t.Fatal(err)
	}
	got, _ := tags.Read(path)
	if got.Album != "Usseewa" || got.TrackNumber != 1 || got.OriginalAlbum != "" || got.OriginalTrack != 0 {
		t.Errorf("not restored: %+v", got)
	}
	if e := entries(t, l)["Ado/Ado Singles/04.flac"]; e.Album != "Usseewa" {
		t.Errorf("index not updated: %+v", e)
	}
}

func TestRetagRewritesUnpaddedNumbers(t *testing.T) {
	l := newLib(t)
	path := addFile(t, l, "a.flac", tags.Track{Title: "A"})
	// simulate a file tagged by another tool
	addComments(t, path, "TRACKNUMBER=3/12", "DISCNUMBER=1")
	scan(t, l)
	ctx := context.Background()

	p, err := l.PlanRetag(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Items) != 1 {
		t.Fatalf("items = %+v", p.Items)
	}
	want := []tags.Change{
		{Field: tags.FieldTrackNumber, Old: "03/12", New: "03"},
		{Field: tags.FieldDiscNumber, Old: "1", New: "01"},
	}
	if !reflect.DeepEqual(p.Items[0].Changes, want) {
		t.Errorf("changes = %+v", p.Items[0].Changes)
	}
	if _, err := l.ApplyRetag(ctx, p); err != nil {
		t.Fatal(err)
	}
	if track, disc, _ := tags.StoredNumbers(path); track != "03" || disc != "01" {
		t.Errorf("stored = %q, %q", track, disc)
	}
	if p, _ := l.PlanRetag(ctx); len(p.Items) != 0 {
		t.Errorf("second retag should be a no-op: %+v", p.Items)
	}
}

// addComments appends raw Vorbis comments, as another tool would write them.
func addComments(t *testing.T, path string, kvs ...string) {
	t.Helper()
	rewriteFLAC(t, path, func(f *flac.File) {
		for _, b := range f.Meta {
			if b.Type == flac.VorbisComment {
				c, _ := flacvorbis.ParseFromMetaDataBlock(*b)
				c.Comments = append(c.Comments, kvs...)
				*b = c.Marshal()
				return
			}
		}
		c := flacvorbis.New()
		c.Comments = kvs
		b := c.Marshal()
		f.Meta = append(f.Meta, &b)
	})
}

// addBackCover embeds a back cover, which ymlib no longer writes itself.
func addBackCover(t *testing.T, path string, data []byte) {
	t.Helper()
	rewriteFLAC(t, path, func(f *flac.File) {
		p, err := flacpicture.NewFromImageData(flacpicture.PictureTypeBackCover, "", data, "image/jpeg")
		if err != nil {
			t.Fatal(err)
		}
		b := p.Marshal()
		f.Meta = append(f.Meta, &b)
	})
}

func hasComment(t *testing.T, path, kv string) bool {
	t.Helper()
	f, err := flac.ParseFile(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	for _, b := range f.Meta {
		if b.Type == flac.VorbisComment {
			c, _ := flacvorbis.ParseFromMetaDataBlock(*b)
			for _, x := range c.Comments {
				if x == kv {
					return true
				}
			}
		}
	}
	return false
}

func rewriteFLAC(t *testing.T, path string, change func(*flac.File)) {
	t.Helper()
	f, err := flac.ParseFile(path)
	if err != nil {
		t.Fatal(err)
	}
	change(f)
	if err := f.Save(path + ".tmp"); err != nil {
		t.Fatal(err)
	}
	f.Close()
	if err := os.Rename(path+".tmp", path); err != nil {
		t.Fatal(err)
	}
}
