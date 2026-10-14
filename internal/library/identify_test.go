package library

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"os"
	"testing"

	"your-music-library/internal/config"
	"your-music-library/internal/metadata"
	"your-music-library/internal/tags"
	"your-music-library/internal/testutil"
)

type fakeProvider struct {
	releases map[string]metadata.Release
	searches int
}

func (f *fakeProvider) Name() string { return "musicbrainz" }
func (f *fakeProvider) Search(_ context.Context, q metadata.Query) ([]metadata.Candidate, error) {
	f.searches++
	var out []metadata.Candidate
	for id := range f.releases {
		out = append(out, metadata.Candidate{ID: id})
	}
	return out, nil
}
func (f *fakeProvider) Release(_ context.Context, id string) (metadata.Release, error) {
	return f.releases[id], nil
}

type fakeImages struct {
	data  []byte
	fails bool
}

func (f fakeImages) Fetch(context.Context, string) ([]byte, string, error) {
	if f.fails {
		return nil, "", errors.New("503")
	}
	return f.data, "image/jpeg", nil
}

func badRelease() metadata.Release {
	return metadata.Release{
		Provider: "musicbrainz", ID: "mb-bad", Title: "Bad", Artist: "Michael Jackson",
		Date: "1987-08-31", ReleaseType: "album", Genre: "Pop", FrontURL: "https://caa.test/front",
		Tracks: []metadata.Track{
			{Disc: 1, Number: 1, Title: "Bad", Artist: "Michael Jackson", ISRC: "USSM18700359", MBRecordingID: "rec-1"},
			{Disc: 1, Number: 2, Title: "The Way You Make Me Feel", Artist: "Michael Jackson"},
			{Disc: 1, Number: 3, Title: "Speed Demon", Artist: "Michael Jackson", MBRecordingID: "rec-3"},
		},
	}
}

func TestIdentifyAppliesConfidentMatch(t *testing.T) {
	l := newLib(t)
	addFile(t, l, "inbox/1.flac", tags.Track{Title: "bad", Artist: "michael jackson", Album: "Bad"})
	addFile(t, l, "inbox/2.mp3", tags.Track{Title: "Speed Demon", Artist: "Michael Jackson", Album: "Bad", IDs: tags.Identity{Source: "fake", SourceTrackID: "9"}})
	scan(t, l)
	ctx := context.Background()
	id := &metadata.Identifier{Providers: []metadata.Provider{&fakeProvider{releases: map[string]metadata.Release{"mb-bad": badRelease()}}}, MinScore: 0.85}

	plan, err := l.PlanIdentify(ctx, id, IdentifyOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Matches) != 1 || len(plan.Reviews) != 0 || len(plan.Matches[0].Items) != 2 {
		t.Fatalf("plan = %+v", plan)
	}
	if exists(l.abs("inbox/cover.jpg")) {
		t.Fatal("planning wrote files")
	}

	big := testutil.JPEG(t, 1000, 1000, color.RGBA{0, 0, 200, 255})
	preset := config.Default().Presets["mp3-player"]
	r, err := l.ApplyIdentify(ctx, plan, preset, fakeImages{data: big})
	if err != nil || r.Changed != 2 || len(r.Failed) != 0 {
		t.Fatalf("apply = %+v %v", r, err)
	}

	got, _ := tags.Read(l.abs("inbox/2.mp3"))
	if got.Title != "Speed Demon" || got.Album != "Bad" || got.AlbumArtist != "Michael Jackson" ||
		got.TrackNumber != 3 || got.TrackTotal != 3 || got.DiscNumber != 1 || got.DiscTotal != 1 ||
		got.Date != "1987-08-31" || got.Genre != "Pop" || got.ReleaseType != "album" {
		t.Errorf("tags = %+v", got)
	}
	if got.IDs.MBReleaseID != "mb-bad" || got.IDs.MBRecordingID != "rec-3" || got.IDs.Source != "fake" || got.IDs.SourceTrackID != "9" {
		t.Errorf("IDs = %+v (source IDs must survive)", got.IDs)
	}
	cfg, _, _ := image.DecodeConfig(bytes.NewReader(got.Cover.Data))
	if cfg.Width != 600 {
		t.Errorf("embedded cover %dpx, want 600 (mp3-player)", cfg.Width)
	}
	if saved, _ := os.ReadFile(l.abs("inbox/cover.jpg")); !bytes.Equal(saved, big) {
		t.Error("cover.jpg should be the full-size download")
	}
	first, _ := tags.Read(l.abs("inbox/1.flac"))
	if first.Title != "Bad" || first.IDs.ISRC != "USSM18700359" {
		t.Errorf("first file = %+v", first)
	}

	// identified files are skipped next time
	plan, _ = l.PlanIdentify(ctx, id, IdentifyOptions{})
	if plan.Skipped != 2 || len(plan.Matches) != 0 {
		t.Errorf("second plan = %+v", plan)
	}
	plan, _ = l.PlanIdentify(ctx, id, IdentifyOptions{Force: true})
	if len(plan.Matches) != 1 {
		t.Errorf("--force should re-identify: %+v", plan)
	}
}

func TestIdentifyCoverFailureStillTags(t *testing.T) {
	l := newLib(t)
	addFile(t, l, "a.flac", tags.Track{Title: "Bad", Artist: "Michael Jackson", Album: "Bad"})
	scan(t, l)
	ctx := context.Background()
	id := &metadata.Identifier{Providers: []metadata.Provider{&fakeProvider{releases: map[string]metadata.Release{"mb-bad": badRelease()}}}, MinScore: 0.85}
	plan, _ := l.PlanIdentify(ctx, id, IdentifyOptions{})
	r, err := l.ApplyIdentify(ctx, plan, config.Default().Presets["full"], fakeImages{fails: true})
	if err != nil || r.Changed != 1 || len(r.Warnings) != 1 {
		t.Errorf("report = %+v %v", r, err)
	}
}

func TestIdentifyLowConfidenceGoesToReview(t *testing.T) {
	l := newLib(t)
	addFile(t, l, "x/1.flac", tags.Track{Title: "Bad", Artist: "Michael Jackson", Album: "Bad (Live in Tokyo)"})
	addFile(t, l, "x/2.flac", tags.Track{Title: "Unknown Jam", Artist: "Michael Jackson", Album: "Bad (Live in Tokyo)"})
	scan(t, l)
	ctx := context.Background()
	id := &metadata.Identifier{Providers: []metadata.Provider{&fakeProvider{releases: map[string]metadata.Release{"mb-bad": badRelease()}}}, MinScore: 0.85}

	plan, err := l.PlanIdentify(ctx, id, IdentifyOptions{})
	if err != nil || len(plan.Reviews) != 1 || len(plan.Matches) != 0 {
		t.Fatalf("plan = %+v %v", plan, err)
	}
	if _, err := l.ApplyIdentify(ctx, plan, config.Default().Presets["full"], nil); err != nil {
		t.Fatal(err)
	}
	// identifying again replaces the open review instead of duplicating it
	plan, _ = l.PlanIdentify(ctx, id, IdentifyOptions{})
	l.ApplyIdentify(ctx, plan, config.Default().Presets["full"], nil)
	reviews, err := l.Reviews(ctx)
	if err != nil || len(reviews) != 1 {
		t.Fatalf("reviews = %+v %v", reviews, err)
	}
	rv := reviews[0]
	if rv.Folder != "x" || len(rv.Paths) != 2 || len(rv.Candidates) != 1 || rv.Query.Album != "Bad (Live in Tokyo)" {
		t.Errorf("review = %+v", rv)
	}

	am, err := l.PlanResolve(ctx, rv.ID, 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(am.Items) != 1 || len(am.Unmatched) != 1 || am.Unmatched[0] != "x/2.flac" {
		t.Errorf("resolve plan = %+v", am)
	}
	if _, err := l.PlanResolve(ctx, rv.ID, 2, nil); err == nil {
		t.Error("candidate out of range accepted")
	}
	if _, err := l.ApplyResolve(ctx, rv.ID, am, config.Default().Presets["full"], nil); err != nil {
		t.Fatal(err)
	}
	if got, _ := tags.Read(l.abs("x/1.flac")); got.Album != "Bad" || got.IDs.MBReleaseID != "mb-bad" {
		t.Errorf("resolved tags = %+v", got)
	}
	if reviews, _ := l.Reviews(ctx); len(reviews) != 0 {
		t.Errorf("review still open: %+v", reviews)
	}
	if err := l.SkipReview(ctx, rv.ID); !errors.Is(err, ErrNoReview) {
		t.Errorf("skip closed review = %v", err)
	}
}

func TestIdentifyPrefixesAndUntitled(t *testing.T) {
	l := newLib(t)
	addFile(t, l, "a/1.flac", tags.Track{Title: "Bad", Album: "Bad", Artist: "Michael Jackson"})
	addFile(t, l, "b/1.flac", tags.Track{Title: "Bad", Album: "Bad", Artist: "Michael Jackson"})
	addFile(t, l, "a/untitled.flac", tags.Track{Album: "Bad"})
	scan(t, l)
	fp := &fakeProvider{releases: map[string]metadata.Release{"mb-bad": badRelease()}}
	id := &metadata.Identifier{Providers: []metadata.Provider{fp}, MinScore: 0.85}
	plan, _ := l.PlanIdentify(context.Background(), id, IdentifyOptions{Prefixes: []string{"a/"}})
	if len(plan.Matches) != 1 || plan.Matches[0].Items[0].Path != "a/1.flac" || len(plan.Untitled) != 1 {
		t.Errorf("plan = %+v", plan)
	}
}

func TestApplyReleaseKeepsGroupedSingle(t *testing.T) {
	cur := tags.Track{Title: "usseewa", Album: "Ado Singles", AlbumArtist: "Ado", TrackNumber: 2, TrackTotal: 5,
		OriginalAlbum: "Usseewa", OriginalTrack: 1}
	rel := metadata.Release{Provider: "discogs", ID: "77", Title: "うっせぇわ", Artist: "Ado", ReleaseType: "single",
		Tracks: []metadata.Track{{Disc: 1, Number: 1, Title: "Usseewa", Artist: "Ado"}}}
	got := ApplyRelease(cur, rel, 0)
	if got.Album != "Ado Singles" || got.TrackNumber != 2 || got.TrackTotal != 5 || got.OriginalAlbum != "うっせぇわ" ||
		got.OriginalTrack != 1 || got.Title != "Usseewa" || got.IDs.DiscogsReleaseID != "77" {
		t.Errorf("got %+v", got)
	}
}

func TestReviewsFollowIdentifyAndOrganize(t *testing.T) {
	l := newLib(t)
	addFile(t, l, "x/1.flac", tags.Track{Title: "Bad", Artist: "Michael Jackson", Album: "Bad (Live)", TrackNumber: 1})
	addFile(t, l, "y/1.flac", tags.Track{Title: "Bad", Artist: "Michael Jackson", Album: "Bad (Tokyo)", TrackNumber: 1})
	scan(t, l)
	ctx := context.Background()
	full := config.Default().Presets["full"]
	weak := &metadata.Identifier{Providers: []metadata.Provider{&fakeProvider{releases: map[string]metadata.Release{"mb-bad": badRelease()}}}, MinScore: 0.99}
	plan, _ := l.PlanIdentify(ctx, weak, IdentifyOptions{})
	l.ApplyIdentify(ctx, plan, full, nil)
	if rs, _ := l.Reviews(ctx); len(rs) != 2 {
		t.Fatalf("reviews = %d", len(rs))
	}

	// organize moves x/1.flac; its review must follow the file
	op := plan2(t, l)
	apply(t, l, op)
	rs, _ := l.Reviews(ctx)
	for _, r := range rs {
		for _, p := range r.Paths {
			if !exists(l.abs(p)) {
				t.Errorf("review #%d points to missing %s", r.ID, p)
			}
		}
		if _, err := l.PlanResolve(ctx, r.ID, 1, nil); err != nil {
			t.Errorf("resolve after organize: %v", err)
		}
	}

	// a later successful identify closes the review for those files
	sure := &metadata.Identifier{Providers: weak.Providers, MinScore: 0.5}
	plan, _ = l.PlanIdentify(ctx, sure, IdentifyOptions{Prefixes: []string{rs[0].Folder}})
	if len(plan.Matches) != 1 {
		t.Fatalf("plan = %+v", plan)
	}
	l.ApplyIdentify(ctx, plan, full, nil)
	if left, _ := l.Reviews(ctx); len(left) != 1 || left[0].ID == rs[0].ID {
		t.Errorf("review of identified files still open: %+v", left)
	}
}

func plan2(t *testing.T, l *Library) OrganizePlan {
	t.Helper()
	p, err := l.PlanOrganize(context.Background())
	if err != nil || len(p.Moves) == 0 {
		t.Fatalf("organize plan = %+v %v", p, err)
	}
	return p
}
