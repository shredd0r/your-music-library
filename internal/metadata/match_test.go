package metadata

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestNormalizeAndSimilarity(t *testing.T) {
	eq := [][2]string{
		{"Don’t Stop ’Til You Get Enough", "Don't Stop 'til You Get Enough"},
		{"Beyoncé", "Beyonce"},
		{"Simon & Garfunkel", "Simon and Garfunkel"},
		{"Workin’ Day and Night", "Workin' Day And Night"},
		{"Let It Be (Remastered 2009)", "Let It Be"},
		{"Bad - 2012 Remaster", "Bad"},
		{"Thriller [Deluxe Edition]", "Thriller"},
	}
	for _, p := range eq {
		if s := similarity(p[0], p[1]); s != 1 {
			t.Errorf("similarity(%q, %q) = %.3f, want 1", p[0], p[1], s)
		}
	}
	if s := similarity("Bad", "Thriller"); s > 0.3 {
		t.Errorf("unrelated titles too similar: %.3f", s)
	}
	if s := similarity("Working Day And Night", "Workin’ Day and Night"); s < 0.9 {
		t.Errorf("near-identical titles: %.3f", s)
	}
}

func offTheWall() Release {
	titles := []string{"Don’t Stop ’til You Get Enough", "Rock With You", "Workin’ Day and Night", "Get on the Floor",
		"Off the Wall", "Girlfriend", "She’s Out of My Life", "I Can’t Help It", "It’s the Falling in Love", "Burn This Disco Out"}
	r := Release{Provider: "fake", ID: "otw", Title: "Off the Wall", Artist: "Michael Jackson", Date: "1979-08-10", ReleaseType: "album"}
	for i, title := range titles {
		r.Tracks = append(r.Tracks, Track{Disc: 1, Number: i + 1, Title: title, Duration: 4 * time.Minute})
	}
	return r
}

func TestScoreAlbum(t *testing.T) {
	q := Query{Artist: "Michael Jackson", Album: "Off The Wall", Tracks: []QueryTrack{
		{Title: "Rock With You", Number: 2},
		{Title: "Don't Stop 'Til You Get Enough", Number: 1},
	}}
	m := Score(q, offTheWall())
	if m.Score < 0.95 {
		t.Errorf("score = %.3f", m.Score)
	}
	if m.Assign[0] != 1 || m.Assign[1] != 0 {
		t.Errorf("assign = %v", m.Assign)
	}

	wrong := Score(Query{Artist: "Michael Jackson", Album: "Bad", Tracks: []QueryTrack{{Title: "Smooth Criminal"}}}, offTheWall())
	if wrong.Score > 0.5 || wrong.Assign[0] != -1 {
		t.Errorf("wrong album scored %.3f assign %v", wrong.Score, wrong.Assign)
	}
}

func TestScoreLoneTrackPrefersSingleOverCompilation(t *testing.T) {
	q := Query{Artist: "Rick Astley", Tracks: []QueryTrack{{Title: "Never Gonna Give You Up"}}}
	single := Release{Title: "Never Gonna Give You Up", Artist: "Rick Astley", ReleaseType: "single",
		Tracks: []Track{{Disc: 1, Number: 1, Title: "Never Gonna Give You Up"}, {Disc: 1, Number: 2, Title: "Never Gonna Give You Up (Instrumental)"}}}
	comp := Release{Title: "Dancemania Super Classics 3", Artist: "Various Artists", ReleaseType: "compilation",
		Tracks: []Track{{Disc: 1, Number: 7, Title: "Never Gonna Give You Up"}}}
	s, c := Score(q, single), Score(q, comp)
	if s.Score < 0.95 || c.Score > 0.6 {
		t.Errorf("single %.3f, compilation %.3f", s.Score, c.Score)
	}
	if s.Assign[0] != 0 {
		t.Error("should match the vocal track, not the instrumental")
	}
}

func TestScoreISRCAndDuration(t *testing.T) {
	rel := offTheWall()
	rel.Tracks[1].ISRC = "USSM17900001"
	q := Query{Artist: "MJ", Album: "Off the Wall", Tracks: []QueryTrack{{Title: "Rock With You (Single Version)", ISRC: "ussm17900001"}}}
	if m := Score(q, rel); m.Assign[0] != 1 {
		t.Errorf("ISRC should pair the track: %v", m.Assign)
	}
	q = Query{Artist: "Michael Jackson", Album: "Off the Wall", Tracks: []QueryTrack{{Title: "Rock With You", Duration: 30 * time.Second}}}
	if m := Score(q, rel); m.Score > 0.86 {
		t.Errorf("large duration mismatch should cost: %.3f", m.Score)
	}
}

type fakeProvider struct {
	name     string
	cands    []Candidate
	releases map[string]Release
	err      error
	fetched  []string
}

func (f *fakeProvider) Name() string { return f.name }
func (f *fakeProvider) Search(context.Context, Query) ([]Candidate, error) {
	return f.cands, f.err
}
func (f *fakeProvider) Release(_ context.Context, id string) (Release, error) {
	f.fetched = append(f.fetched, id)
	r, ok := f.releases[id]
	if !ok {
		return Release{}, errors.New("not found")
	}
	return r, nil
}

func TestIdentifyChain(t *testing.T) {
	q := Query{Artist: "Michael Jackson", Album: "Off the Wall", Tracks: []QueryTrack{{Title: "Rock With You", Number: 2}}}
	good := offTheWall()
	deluxe := offTheWall()
	deluxe.ID = "deluxe"
	deluxe.Title = "Off the Wall (Special Edition)"
	deluxe.Tracks = append(deluxe.Tracks, Track{Disc: 1, Number: 11, Title: "Interview"})

	t.Run("first provider confident", func(t *testing.T) {
		a := &fakeProvider{name: "discogs", cands: []Candidate{{ID: "deluxe", Title: "Off the Wall (Special Edition)", Artist: "Michael Jackson"}, {ID: "otw", Title: "Off the Wall", Artist: "Michael Jackson"}},
			releases: map[string]Release{"otw": good, "deluxe": deluxe}}
		b := &fakeProvider{name: "musicbrainz"}
		res, err := (&Identifier{Providers: []Provider{a, b}, MinScore: 0.85}).Identify(context.Background(), q)
		if err != nil || res.Best == nil {
			t.Fatalf("res = %+v, %v", res, err)
		}
		if res.Best.Release.ID != "otw" {
			t.Errorf("standard edition should win the tie, got %s", res.Best.Release.ID)
		}
		if len(b.fetched) != 0 {
			t.Error("second provider must not be called")
		}
	})

	t.Run("error falls back", func(t *testing.T) {
		a := &fakeProvider{name: "discogs", err: errors.New("401 unauthorized")}
		b := &fakeProvider{name: "musicbrainz", cands: []Candidate{{ID: "otw"}}, releases: map[string]Release{"otw": good}}
		res, err := (&Identifier{Providers: []Provider{a, b}, MinScore: 0.85}).Identify(context.Background(), q)
		if err != nil || res.Best == nil || len(res.ProviderErrors) != 1 {
			t.Fatalf("res = %+v, %v", res, err)
		}
	})

	t.Run("weak match falls back, both weak means review", func(t *testing.T) {
		weak := good
		weak.ID = "weak"
		weak.Artist = "Michael Jackson Tribute Band"
		weak.Title = "Off the Wall Covers"
		a := &fakeProvider{name: "discogs", cands: []Candidate{{ID: "weak"}}, releases: map[string]Release{"weak": weak}}
		b := &fakeProvider{name: "musicbrainz", cands: []Candidate{{ID: "weak"}}, releases: map[string]Release{"weak": weak}}
		res, err := (&Identifier{Providers: []Provider{a, b}, MinScore: 0.85}).Identify(context.Background(), q)
		if err != nil || res.Best != nil || len(res.Candidates) != 2 {
			t.Fatalf("res = %+v, %v", res, err)
		}
	})

	t.Run("all providers fail", func(t *testing.T) {
		a := &fakeProvider{name: "discogs", err: errors.New("down")}
		b := &fakeProvider{name: "musicbrainz", err: errors.New("down too")}
		if _, err := (&Identifier{Providers: []Provider{a, b}, MinScore: 0.85}).Identify(context.Background(), q); err == nil {
			t.Error("expected error")
		}
	})

	t.Run("details limited", func(t *testing.T) {
		var cands []Candidate
		for i := 0; i < 10; i++ {
			cands = append(cands, Candidate{ID: "x"})
		}
		a := &fakeProvider{name: "discogs", cands: cands, releases: map[string]Release{}}
		(&Identifier{Providers: []Provider{a}, MinScore: 0.85}).Identify(context.Background(), q)
		if len(a.fetched) != 3 {
			t.Errorf("fetched %d releases, want 3", len(a.fetched))
		}
	})
}

func TestTieBreakPrefersLiteralTitles(t *testing.T) {
	q := Query{Artist: "Michael Jackson", Album: "Off the Wall", Tracks: []QueryTrack{{Title: "Rock With You"}, {Title: "Off the Wall"}}}
	clean := offTheWall()
	clean.ID = "clean"
	dotted := offTheWall()
	dotted.ID, dotted.Title = "dotted", "Off The Wall."
	dotted.Tracks = append([]Track(nil), dotted.Tracks...)
	for i := range dotted.Tracks {
		dotted.Tracks[i].Title += "."
	}
	for _, order := range [][]Release{{dotted, clean}, {clean, dotted}} {
		p := &fakeProvider{name: "discogs", releases: map[string]Release{}}
		for _, r := range order {
			p.cands = append(p.cands, Candidate{ID: r.ID})
			p.releases[r.ID] = r
		}
		res, err := (&Identifier{Providers: []Provider{p}, MinScore: 0.85}).Identify(context.Background(), q)
		if err != nil || res.Best == nil || res.Best.Release.ID != "clean" {
			t.Errorf("order %s,%s: best = %+v", order[0].ID, order[1].ID, res.Best)
		}
	}
}
