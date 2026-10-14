package metadata

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestPickMBDate(t *testing.T) {
	cases := []struct{ group, edition, want string }{
		{"1979-08-10", "1988-10-26", "1979-08-10"}, // original wins when complete
		{"1987", "1987-07-27", "1987-07-27"},       // same year: the edition fills in
		{"1987-07", "1987-07-27", "1987-07-27"},
		{"1987", "2009-03-01", "1987"}, // a later reissue says nothing about the original
		{"", "2001-02-03", "2001-02-03"},
		{"1987", "", "1987"},
	}
	for _, c := range cases {
		if got := pickMBDate(c.group, c.edition); got != c.want {
			t.Errorf("pickMBDate(%q, %q) = %q, want %q", c.group, c.edition, got, c.want)
		}
	}
}

func TestDiscogsRefineDate(t *testing.T) {
	ctx := context.Background()

	t.Run("main release has no better date (real data)", func(t *testing.T) {
		srv := newFixtureServer(t, "discogs", map[string]string{
			"/releases/249504": "release_249504.json",
			"/masters/96559":   "master_96559.json",
			"/releases/221824": "release_221824.json",
		})
		d := NewDiscogs("t", fastOpts(), srv.URL)
		rel, err := d.Release(ctx, "249504")
		if err != nil {
			t.Fatal(err)
		}
		if rel.Date != "1987-07" || rel.MasterID != "96559" {
			t.Fatalf("release date %q master %q", rel.Date, rel.MasterID)
		}
		if got, err := d.RefineDate(ctx, rel); err != nil || got != "" {
			t.Errorf("RefineDate = %q, %v; want no better date", got, err)
		}
	})

	t.Run("main release has the full date", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/masters/7":
				w.Write([]byte(`{"id": 7, "main_release": 70, "year": 1987}`))
			case "/releases/70":
				w.Write([]byte(`{"id": 70, "title": "X", "released": "1987-07-27", "year": 1987, "tracklist": []}`))
			default:
				http.NotFound(w, r)
			}
		}))
		defer srv.Close()
		d := NewDiscogs("t", fastOpts(), srv.URL)
		got, err := d.RefineDate(ctx, Release{Provider: "discogs", ID: "71", MasterID: "7", Date: "1987-07"})
		if err != nil || got != "1987-07-27" {
			t.Errorf("RefineDate = %q, %v", got, err)
		}
		// an already complete date needs no requests
		if got, _ := d.RefineDate(ctx, Release{ID: "71", MasterID: "7", Date: "1987-07-01"}); got != "" {
			t.Errorf("complete date refined to %q", got)
		}
	})
}

type refiningProvider struct {
	fakeProvider
	refined []string
}

func (r *refiningProvider) RefineDate(_ context.Context, rel Release) (string, error) {
	r.refined = append(r.refined, rel.ID)
	return rel.Date + "-07-27", nil
}

func TestIdentifierRefinesOnlyTheChosenRelease(t *testing.T) {
	a := offTheWall()
	a.Provider, a.Date = "discogs", "1979"
	b := a
	b.ID = "other"
	p := &refiningProvider{fakeProvider: fakeProvider{name: "discogs", cands: []Candidate{{ID: "otw"}, {ID: "other"}},
		releases: map[string]Release{"otw": a, "other": b}}}
	q := Query{Artist: "Michael Jackson", Album: "Off the Wall", Tracks: []QueryTrack{{Title: "Rock With You"}}}
	res, err := (&Identifier{Providers: []Provider{p}, MinScore: 0.85}).Identify(context.Background(), q)
	if err != nil || res.Best == nil {
		t.Fatal(res, err)
	}
	if res.Best.Release.Date != "1979-07-27" || res.Candidates[0].Release.Date != "1979-07-27" {
		t.Errorf("best date = %q / %q", res.Best.Release.Date, res.Candidates[0].Release.Date)
	}
	if len(p.refined) != 1 {
		t.Errorf("refined %v; only the chosen release should cost requests", p.refined)
	}
}
