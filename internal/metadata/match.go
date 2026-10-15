package metadata

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

// Match is a release scored against a Query.
type Match struct {
	Release Release
	Score   float64 // 0..1
	// Assign[i] is the index in Release.Tracks matched to Query.Tracks[i],
	// or -1 when no track matched.
	Assign []int
}

// Track returns the release track for query track i.
func (m Match) Track(i int) (Track, bool) {
	if i >= len(m.Assign) || m.Assign[i] < 0 {
		return Track{}, false
	}
	return m.Release.Tracks[m.Assign[i]], true
}

// minTitleSim is the least title similarity that pairs a file with a track.
const minTitleSim = 0.75

// Score rates how well rel matches q.
func Score(q Query, rel Release) Match {
	m := Match{Release: rel, Assign: make([]int, len(q.Tracks))}

	artist := similarity(q.Artist, rel.Artist)
	for _, a := range rel.Artists {
		artist = max(artist, similarity(q.Artist, a))
	}
	var album float64
	if q.Album != "" {
		album = similarity(q.Album, rel.Title)
	} else if len(q.Tracks) > 0 {
		// a lone track: its own single release is named like the track
		album = similarity(q.Tracks[0].Title, rel.Title)
	}

	used := make([]bool, len(rel.Tracks))
	var trackSum float64
	var durationOff, durationN int
	for i, qt := range q.Tracks {
		m.Assign[i] = -1
		best, bestSim := -1, 0.0
		for j, rt := range rel.Tracks {
			if used[j] {
				continue
			}
			sim := similarity(qt.Title, rt.Title)
			if qt.ISRC != "" && strings.EqualFold(qt.ISRC, rt.ISRC) {
				sim = 1
			}
			if sim < minTitleSim {
				continue
			}
			// prefer the same position when titles tie (repeated titles)
			if qt.Number == rt.Number && (qt.Disc == 0 || qt.Disc == rt.Disc) {
				sim += 0.01
			}
			if sim > bestSim {
				best, bestSim = j, sim
			}
		}
		if best < 0 {
			continue
		}
		used[best] = true
		m.Assign[i] = best
		trackSum += min(bestSim, 1)
		if qt.Duration > 0 && rel.Tracks[best].Duration > 0 {
			durationN++
			if absDur(qt.Duration-rel.Tracks[best].Duration) > 15*time.Second {
				durationOff++
			}
		}
	}
	tracks := 0.0
	if len(q.Tracks) > 0 {
		tracks = trackSum / float64(len(q.Tracks))
	}

	score := 0.25*artist + 0.25*album + 0.5*tracks
	if isCompilation(rel) && album < 0.9 {
		// a lone single found on "Hits of 1987" is almost never the wanted release
		score *= 0.7
	}
	if durationN > 0 && durationOff*2 > durationN {
		score *= 0.85
	}
	m.Score = math.Round(score*1000) / 1000
	return m
}

func isCompilation(r Release) bool { return r.ReleaseType == "compilation" }

func absDur(d time.Duration) time.Duration {
	if d < 0 {
		return -d
	}
	return d
}

// better orders matches: higher score, then titles that match the files
// literally (an edition spelling "Rock With You." loses to "Rock With
// You"), then a track count closer to the query (standard over deluxe),
// then the earlier release.
func better(q Query, a, b Match) bool {
	if a.Score != b.Score {
		return a.Score > b.Score
	}
	if la, lb := literal(q, a), literal(q, b); la != lb {
		return la > lb
	}
	da, db := abs(len(a.Release.Tracks)-len(q.Tracks)), abs(len(b.Release.Tracks)-len(q.Tracks))
	if da != db {
		return da < db
	}
	return a.Release.Date < b.Release.Date
}

// literal counts album and track titles equal to the query's, ignoring
// only letter case.
func literal(q Query, m Match) int {
	n := 0
	if q.Album != "" && strings.EqualFold(strings.TrimSpace(q.Album), strings.TrimSpace(m.Release.Title)) {
		n++
	}
	for i, qt := range q.Tracks {
		if t, ok := m.Track(i); ok && strings.EqualFold(strings.TrimSpace(qt.Title), strings.TrimSpace(t.Title)) {
			n++
		}
	}
	return n
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// Result is the outcome of Identify.
type Result struct {
	// Best is set when the top match reaches the confidence threshold.
	Best *Match
	// Candidates are all scored releases, best first (for review).
	Candidates []Match
	// ProviderErrors are failures that were skipped over.
	ProviderErrors []error
}

// Identifier queries providers in order. Whole-record: the result comes
// from a single release. The next provider is tried when the previous one
// fails, finds nothing, or finds nothing confident.
type Identifier struct {
	Providers []Provider
	MinScore  float64
	// Details is how many search hits per provider get full details
	// fetched (each costs a request). Default 3.
	Details int
}

// Identify finds the release matching q.
func (id *Identifier) Identify(ctx context.Context, q Query) (Result, error) {
	var res Result
	details := id.Details
	if details <= 0 {
		details = 3
	}
	for _, p := range id.Providers {
		cands, err := p.Search(ctx, q)
		if err != nil {
			if ctx.Err() != nil {
				return res, ctx.Err()
			}
			res.ProviderErrors = append(res.ProviderErrors, fmt.Errorf("%s: %w", p.Name(), err))
			continue
		}
		sort.SliceStable(cands, func(i, j int) bool { return summaryScore(q, cands[i]) > summaryScore(q, cands[j]) })
		for _, c := range cands[:min(details, len(cands))] {
			rel, err := p.Release(ctx, c.ID)
			if err != nil {
				if ctx.Err() != nil {
					return res, ctx.Err()
				}
				res.ProviderErrors = append(res.ProviderErrors, fmt.Errorf("%s release %s: %w", p.Name(), c.ID, err))
				continue
			}
			res.Candidates = append(res.Candidates, Score(q, rel))
		}
		sort.SliceStable(res.Candidates, func(i, j int) bool { return better(q, res.Candidates[i], res.Candidates[j]) })
		if len(res.Candidates) > 0 && res.Candidates[0].Score >= id.MinScore {
			id.Refine(ctx, &res.Candidates[0].Release)
			best := res.Candidates[0]
			res.Best = &best
			return res, nil
		}
	}
	if len(res.Candidates) == 0 && len(res.ProviderErrors) == len(id.Providers) && len(id.Providers) > 0 {
		return res, errors.Join(res.ProviderErrors...)
	}
	return res, nil
}

// summaryScore pre-ranks search hits so details are fetched for the most
// promising ones.
func summaryScore(q Query, c Candidate) float64 {
	album := q.Album
	if album == "" && len(q.Tracks) > 0 {
		album = q.Tracks[0].Title
	}
	s := similarity(q.Artist, c.Artist) + similarity(album, c.Title)
	if c.TrackCount > 0 && c.TrackCount >= len(q.Tracks) {
		s += 0.5 / float64(1+c.TrackCount-len(q.Tracks))
	}
	if c.Compilation {
		s -= 0.5
	}
	return s
}

// Refine asks rel's provider for a full date when rel has only a year or
// year-month. It is called for chosen releases only, since it may cost
// requests. Failures leave the date as it is.
func (id *Identifier) Refine(ctx context.Context, rel *Release) {
	if fullDate(rel.Date) {
		return
	}
	for _, p := range id.Providers {
		r, ok := p.(DateRefiner)
		if !ok || p.Name() != rel.Provider {
			continue
		}
		if d, err := r.RefineDate(ctx, *rel); err == nil && moreSpecific(rel.Date, d) {
			rel.Date = d
		}
		return
	}
}
