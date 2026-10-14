package tags

import (
	"bytes"
	"image"
	"image/color"
	_ "image/jpeg"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"testing"

	"github.com/bogem/id3v2/v2"
	"github.com/go-flac/flacpicture/v2"
	"github.com/go-flac/flacvorbis/v2"
	flac "github.com/go-flac/go-flac/v2"

	"your-music-library/internal/config"
	"your-music-library/internal/testutil"
)

func sampleTrack(t *testing.T) Track {
	return Track{
		Title:       "Bad",
		Album:       "Bad",
		Artist:      "Michael Jackson",
		Artists:     []string{"Michael Jackson", "Siedah Garrett"},
		AlbumArtist: "Michael Jackson",
		Date:        "1987-08-31",
		TrackNumber: 1, TrackTotal: 11,
		DiscNumber: 1, DiscTotal: 2,
		Genre:       "Pop",
		ReleaseType: "album",
		Cover:       &Picture{MIME: "image/jpeg", Data: testutil.JPEG(t, 20, 20, color.RGBA{255, 0, 0, 255})},
		Back:        &Picture{MIME: "image/jpeg", Data: testutil.JPEG(t, 10, 10, color.RGBA{0, 0, 255, 255})},
		IDs: Identity{
			Source: "fake", SourceTrackID: "42", ISRC: "USSM18700359",
			MBRecordingID: "rec-1", MBReleaseID: "rel-1", DiscogsReleaseID: "123",
		},
		OriginalAlbum: "Bad (Single)", OriginalTrack: 1,
	}
}

func TestRoundTrip(t *testing.T) {
	for _, ext := range []string{"flac", "mp3"} {
		t.Run(ext, func(t *testing.T) {
			path := testutil.CopyAudio(t, ext, filepath.Join(t.TempDir(), "a."+ext))
			want := sampleTrack(t)
			if err := Write(path, want); err != nil {
				t.Fatal(err)
			}
			got, err := Read(path)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("round trip mismatch\n got: %+v\nwant: %+v", got, want)
			}
		})
	}
}

func TestReadUntaggedFile(t *testing.T) {
	for _, ext := range []string{"flac", "mp3"} {
		path := testutil.CopyAudio(t, ext, filepath.Join(t.TempDir(), "a."+ext))
		got, err := Read(path)
		if err != nil {
			t.Fatalf("%s: %v", ext, err)
		}
		if !reflect.DeepEqual(got, Track{}) {
			t.Errorf("%s: expected empty track, got %+v", ext, got)
		}
	}
}

func TestWriteClearsEmptyFields(t *testing.T) {
	for _, ext := range []string{"flac", "mp3"} {
		t.Run(ext, func(t *testing.T) {
			path := testutil.CopyAudio(t, ext, filepath.Join(t.TempDir(), "a."+ext))
			if err := Write(path, sampleTrack(t)); err != nil {
				t.Fatal(err)
			}
			if err := Write(path, Track{Title: "Only title"}); err != nil {
				t.Fatal(err)
			}
			got, _ := Read(path)
			if !reflect.DeepEqual(got, Track{Title: "Only title"}) {
				t.Errorf("fields not cleared: %+v", got)
			}
		})
	}
}

func TestWritePreservesUnmanagedTags(t *testing.T) {
	t.Run("flac", func(t *testing.T) {
		path := testutil.CopyAudio(t, "flac", filepath.Join(t.TempDir(), "a.flac"))
		f, _ := flac.ParseFile(path)
		c := flacvorbis.New()
		c.Add("REPLAYGAIN_TRACK_GAIN", "-6.5 dB")
		c.Add("TITLE", "old")
		b := c.Marshal()
		f.Meta = append(f.Meta, &b)
		tmp := path + ".tmp"
		f.Save(tmp)
		f.Close()
		replace(t, tmp, path)

		if err := Write(path, Track{Title: "new"}); err != nil {
			t.Fatal(err)
		}
		comments := flacComments(t, path)
		if !contains(comments, "REPLAYGAIN_TRACK_GAIN=-6.5 dB") || !contains(comments, "TITLE=new") || contains(comments, "TITLE=old") {
			t.Errorf("comments = %v", comments)
		}
	})
	t.Run("mp3", func(t *testing.T) {
		path := testutil.CopyAudio(t, "mp3", filepath.Join(t.TempDir(), "a.mp3"))
		tag, _ := id3v2.Open(path, id3v2.Options{Parse: true})
		tag.AddUserDefinedTextFrame(id3v2.UserDefinedTextFrame{Encoding: id3v2.EncodingUTF8, Description: "REPLAYGAIN_TRACK_GAIN", Value: "-6.5 dB"})
		tag.AddCommentFrame(id3v2.CommentFrame{Encoding: id3v2.EncodingUTF8, Language: "eng", Text: "keep me"})
		tag.Save()
		tag.Close()

		if err := Write(path, Track{Title: "new"}); err != nil {
			t.Fatal(err)
		}
		tag, _ = id3v2.Open(path, id3v2.Options{Parse: true})
		defer tag.Close()
		if len(tag.GetFrames("COMM")) != 1 {
			t.Error("comment frame lost")
		}
		found := false
		for _, fr := range tag.GetFrames("TXXX") {
			if u := fr.(id3v2.UserDefinedTextFrame); u.Description == "REPLAYGAIN_TRACK_GAIN" {
				found = true
			}
		}
		if !found {
			t.Error("REPLAYGAIN TXXX lost")
		}
	})
}

func TestReadsCommonAliases(t *testing.T) {
	path := testutil.CopyAudio(t, "flac", filepath.Join(t.TempDir(), "a.flac"))
	f, _ := flac.ParseFile(path)
	c := flacvorbis.New()
	c.Comments = append(c.Comments, []string{"album artist=AA", "YEAR=1999", "TRACKNUMBER=3", "TOTALTRACKS=12", "DISCNUMBER=2/2"}...)
	b := c.Marshal()
	f.Meta = append(f.Meta, &b)
	f.Save(path + ".tmp")
	f.Close()
	replace(t, path+".tmp", path)

	got, err := Read(path)
	if err != nil {
		t.Fatal(err)
	}
	want := Track{AlbumArtist: "AA", Date: "1999", TrackNumber: 3, TrackTotal: 12, DiscNumber: 2, DiscTotal: 2}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestMP3ReadsV23Year(t *testing.T) {
	path := testutil.CopyAudio(t, "mp3", filepath.Join(t.TempDir(), "a.mp3"))
	tag, _ := id3v2.Open(path, id3v2.Options{Parse: true})
	tag.SetVersion(3)
	tag.AddTextFrame("TYER", id3v2.EncodingISO, "1987")
	tag.Save()
	tag.Close()
	got, err := Read(path)
	if err != nil || got.Date != "1987" {
		t.Errorf("Date = %q, %v", got.Date, err)
	}
}

func TestUnsupportedFormat(t *testing.T) {
	if Supported("x.m4a") || !Supported("X.FLAC") || !Supported("a.mp3") {
		t.Error("Supported() wrong")
	}
	if _, err := Read("x.m4a"); err == nil {
		t.Error("expected error for m4a")
	}
}

func TestApplyPreset(t *testing.T) {
	tr := sampleTrack(t)
	tr.Cover = &Picture{MIME: "image/jpeg", Data: testutil.JPEG(t, 1200, 800, color.RGBA{0, 255, 0, 255})}
	mp3 := config.Default().Presets["mp3-player"]

	got, err := ApplyPreset(tr, mp3)
	if err != nil {
		t.Fatal(err)
	}
	if got.Artists != nil || got.Back != nil {
		t.Errorf("Artists/Back should be removed: %+v", got)
	}
	if got.Title != tr.Title || got.Genre != tr.Genre || got.DiscNumber != 1 {
		t.Errorf("kept fields changed: %+v", got)
	}
	if got.IDs != tr.IDs || got.ReleaseType != tr.ReleaseType || got.OriginalAlbum != tr.OriginalAlbum {
		t.Error("identity and internal fields must survive any preset")
	}
	img, _, err := image.Decode(bytes.NewReader(got.Cover.Data))
	if err != nil {
		t.Fatal(err)
	}
	if b := img.Bounds(); b.Dx() != 600 || b.Dy() != 400 {
		t.Errorf("cover resized to %dx%d, want 600x400", b.Dx(), b.Dy())
	}
	// input must not be mutated
	if tr.Artists == nil || tr.Back == nil {
		t.Error("ApplyPreset mutated its input")
	}

	// small covers are left untouched
	small := sampleTrack(t)
	got, _ = ApplyPreset(small, mp3)
	if !bytes.Equal(got.Cover.Data, small.Cover.Data) {
		t.Error("small cover was re-encoded")
	}
}

func TestDiff(t *testing.T) {
	a := sampleTrack(t)
	b := a
	b.Artists = nil
	b.Back = nil
	b.Genre = "Rock"
	changes := Diff(a, b)
	want := []Change{
		{Field: config.FieldArtists, Old: "Michael Jackson; Siedah Garrett", New: ""},
		{Field: config.FieldGenre, Old: "Pop", New: "Rock"},
		{Field: config.FieldPictureBack, Old: "image/jpeg 10x10", New: ""},
	}
	if !reflect.DeepEqual(changes, want) {
		t.Errorf("Diff =\n%+v\nwant\n%+v", changes, want)
	}
	if len(Diff(a, a)) != 0 {
		t.Error("Diff of equal tracks should be empty")
	}
}

func flacComments(t *testing.T, path string) []string {
	f, err := flac.ParseFile(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	for _, b := range f.Meta {
		if b.Type == flac.VorbisComment {
			c, _ := flacvorbis.ParseFromMetaDataBlock(*b)
			return c.Comments
		}
	}
	return nil
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

func replace(t *testing.T, from, to string) {
	t.Helper()
	if err := rename(from, to); err != nil {
		t.Fatal(err)
	}
}

// Some tools (ffmpeg, some rippers) mark the cover as picture type "Other".
func TestOtherPictureTypeIsUsedAsCover(t *testing.T) {
	img := testutil.JPEG(t, 30, 30, color.White)
	extra := testutil.JPEG(t, 5, 5, color.Black)
	for _, ext := range []string{"flac", "mp3"} {
		t.Run(ext, func(t *testing.T) {
			// only an "Other" picture: it is the cover and gets replaced on write
			path := testutil.CopyAudio(t, ext, filepath.Join(t.TempDir(), "a."+ext))
			addRawPicture(t, path, 0, img)
			got, err := Read(path)
			if err != nil || got.Cover == nil || !bytes.Equal(got.Cover.Data, img) {
				t.Fatalf("cover not read from type Other: %+v %v", got.Cover, err)
			}
			if err := Write(path, got); err != nil {
				t.Fatal(err)
			}
			if types := pictureTypes(t, path); !reflect.DeepEqual(types, []int{3}) {
				t.Errorf("after write picture types = %v, want [3]", types)
			}

			// a real front cover exists: the "Other" picture is left alone
			path = testutil.CopyAudio(t, ext, filepath.Join(t.TempDir(), "b."+ext))
			addRawPicture(t, path, 3, img)
			addRawPicture(t, path, 0, extra)
			got, _ = Read(path)
			if !bytes.Equal(got.Cover.Data, img) {
				t.Error("front cover should win over Other")
			}
			Write(path, got)
			if types := pictureTypes(t, path); !reflect.DeepEqual(types, []int{0, 3}) {
				t.Errorf("picture types = %v, want [0 3]", types)
			}
		})
	}
}

func addRawPicture(t *testing.T, path string, typ int, data []byte) {
	t.Helper()
	if filepath.Ext(path) == ".mp3" {
		tag, _ := id3v2.Open(path, id3v2.Options{Parse: true})
		tag.AddAttachedPicture(id3v2.PictureFrame{Encoding: id3v2.EncodingUTF8, MimeType: "image/jpeg", PictureType: byte(typ), Description: strconv.Itoa(typ), Picture: data})
		tag.Save()
		tag.Close()
		return
	}
	f, _ := flac.ParseFile(path)
	p, err := flacpicture.NewFromImageData(flacpicture.PictureType(typ), "", data, "image/jpeg")
	if err != nil {
		t.Fatal(err)
	}
	b := p.Marshal()
	f.Meta = append(f.Meta, &b)
	f.Save(path + ".tmp")
	f.Close()
	replace(t, path+".tmp", path)
}

func pictureTypes(t *testing.T, path string) []int {
	t.Helper()
	var out []int
	if filepath.Ext(path) == ".mp3" {
		tag, _ := id3v2.Open(path, id3v2.Options{Parse: true})
		defer tag.Close()
		for _, fr := range tag.GetFrames("APIC") {
			out = append(out, int(fr.(id3v2.PictureFrame).PictureType))
		}
	} else {
		f, _ := flac.ParseFile(path)
		defer f.Close()
		for _, b := range f.Meta {
			if b.Type == flac.Picture {
				p, _ := flacpicture.ParseFromMetaDataBlock(*b)
				out = append(out, int(p.PictureType))
			}
		}
	}
	sort.Ints(out)
	return out
}

func TestTrackNumberIsZeroPadded(t *testing.T) {
	cases := []struct {
		ext      string
		n, total int
		want     string
	}{
		{"flac", 1, 11, "01"},
		{"flac", 10, 0, "10"},
		{"flac", 104, 120, "104"},
		{"mp3", 1, 11, "01/11"},
		{"mp3", 7, 0, "07"},
	}
	for _, c := range cases {
		path := testutil.CopyAudio(t, c.ext, filepath.Join(t.TempDir(), "a."+c.ext))
		if err := Write(path, Track{TrackNumber: c.n, TrackTotal: c.total}); err != nil {
			t.Fatal(err)
		}
		stored, err := StoredTrackNumber(path)
		if err != nil || stored != c.want {
			t.Errorf("%s %d/%d: stored %q, %v; want %q", c.ext, c.n, c.total, stored, err, c.want)
		}
		got, _ := Read(path)
		if got.TrackNumber != c.n || got.TrackTotal != c.total {
			t.Errorf("%s: read back %d/%d", c.ext, got.TrackNumber, got.TrackTotal)
		}
		if want, _ := CanonicalTrackNumber(path, got); want != c.want {
			t.Errorf("%s: CanonicalTrackNumber = %q, want %q", c.ext, want, c.want)
		}
	}
}
