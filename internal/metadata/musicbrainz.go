package metadata

import (
	"context"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

// MusicBrainz uses the MusicBrainz web service (no key; 1 request/second
// and a descriptive User-Agent are required) and the Cover Art Archive.
type MusicBrainz struct {
	http    *httpClient
	base    string
	caaBase string
}

const (
	MusicBrainzBaseURL    = "https://musicbrainz.org/ws/2"
	CoverArtArchiveURL    = "https://coverartarchive.org"
	musicBrainzSiteURL    = "https://musicbrainz.org"
	musicBrainzReleaseInc = "recordings+artist-credits+isrcs+genres+release-groups+labels"
)

// NewMusicBrainz returns a client. Empty base URLs mean production.
func NewMusicBrainz(opts HTTPOptions, base, caaBase string) *MusicBrainz {
	if base == "" {
		base = MusicBrainzBaseURL
	}
	if caaBase == "" {
		caaBase = CoverArtArchiveURL
	}
	if opts.MinInterval == 0 {
		opts.MinInterval = time.Second
	}
	return &MusicBrainz{http: newHTTP(opts), base: strings.TrimRight(base, "/"), caaBase: strings.TrimRight(caaBase, "/")}
}

func (m *MusicBrainz) Name() string { return "musicbrainz" }

type mbCredit struct {
	Name       string `json:"name"`
	JoinPhrase string `json:"joinphrase"`
	Artist     struct {
		Name string `json:"name"`
	} `json:"artist"`
}

func mbJoin(credits []mbCredit) (string, []string) {
	var b strings.Builder
	var names []string
	for _, c := range credits {
		b.WriteString(c.Name)
		b.WriteString(c.JoinPhrase)
		names = append(names, c.Artist.Name)
	}
	return strings.TrimSpace(b.String()), names
}

type mbReleaseGroup struct {
	PrimaryType      string    `json:"primary-type"`
	SecondaryTypes   []string  `json:"secondary-types"`
	FirstReleaseDate string    `json:"first-release-date"`
	Genres           []mbGenre `json:"genres"`
}

func (g mbReleaseGroup) releaseType() string {
	for _, s := range g.SecondaryTypes {
		if strings.EqualFold(s, "Compilation") {
			return "compilation"
		}
	}
	if g.PrimaryType == "" {
		return "album"
	}
	return strings.ToLower(g.PrimaryType)
}

type mbGenre struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

type mbReleaseSummary struct {
	ID           string         `json:"id"`
	Title        string         `json:"title"`
	Date         string         `json:"date"`
	TrackCount   int            `json:"track-count"`
	ArtistCredit []mbCredit     `json:"artist-credit"`
	ReleaseGroup mbReleaseGroup `json:"release-group"`
}

func (s mbReleaseSummary) candidate() Candidate {
	artist, _ := mbJoin(s.ArtistCredit)
	year := 0
	if len(s.Date) >= 4 {
		year, _ = strconv.Atoi(s.Date[:4])
	}
	rt := s.ReleaseGroup.releaseType()
	return Candidate{ID: s.ID, Title: s.Title, Artist: artist, Year: year, TrackCount: s.TrackCount,
		ReleaseType: rt, Compilation: rt == "compilation"}
}

// luceneQuote quotes a value for a MusicBrainz search query.
func luceneQuote(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}

func (m *MusicBrainz) Search(ctx context.Context, q Query) ([]Candidate, error) {
	if q.Album != "" {
		return m.searchReleases(ctx, q.Album, q.Artist)
	}
	if len(q.Tracks) == 0 {
		return nil, nil
	}
	// No album: a lone track is usually from its own single, which is
	// named like the track, so search releases by the title first. Then add
	// releases the recording appears on (often compilations).
	title := q.Tracks[0].Title
	out, err := m.searchReleases(ctx, title, q.Artist)
	if err != nil {
		return nil, err
	}
	parts := []string{"recording:" + luceneQuote(title)}
	if q.Artist != "" {
		parts = append(parts, "artist:"+luceneQuote(q.Artist))
	}
	var res struct {
		Recordings []struct {
			Releases []mbReleaseSummary `json:"releases"`
		} `json:"recordings"`
	}
	u := m.base + "/recording?" + url.Values{"query": {strings.Join(parts, " AND ")}, "limit": {"10"}, "fmt": {"json"}}.Encode()
	if err := m.http.getJSON(ctx, u, &res); err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	for _, c := range out {
		seen[c.ID] = true
	}
	for _, rec := range res.Recordings {
		for _, r := range rec.Releases {
			if !seen[r.ID] {
				seen[r.ID] = true
				out = append(out, r.candidate())
			}
		}
	}
	return out, nil
}

func (m *MusicBrainz) searchReleases(ctx context.Context, title, artist string) ([]Candidate, error) {
	parts := []string{"release:" + luceneQuote(title)}
	if artist != "" {
		parts = append(parts, "artist:"+luceneQuote(artist))
	}
	var res struct {
		Releases []mbReleaseSummary `json:"releases"`
	}
	u := m.base + "/release?" + url.Values{"query": {strings.Join(parts, " AND ")}, "limit": {"10"}, "fmt": {"json"}}.Encode()
	if err := m.http.getJSON(ctx, u, &res); err != nil {
		return nil, err
	}
	out := make([]Candidate, 0, len(res.Releases))
	for _, r := range res.Releases {
		out = append(out, r.candidate())
	}
	return out, nil
}

type mbRelease struct {
	ID           string         `json:"id"`
	Title        string         `json:"title"`
	Date         string         `json:"date"`
	Country      string         `json:"country"`
	Barcode      string         `json:"barcode"`
	ArtistCredit []mbCredit     `json:"artist-credit"`
	ReleaseGroup mbReleaseGroup `json:"release-group"`
	Genres       []mbGenre      `json:"genres"`
	LabelInfo    []struct {
		CatalogNumber string `json:"catalog-number"`
		Label         *struct {
			Name string `json:"name"`
		} `json:"label"`
	} `json:"label-info"`
	CoverArtArchive struct {
		Front bool `json:"front"`
		Back  bool `json:"back"`
	} `json:"cover-art-archive"`
	Media []struct {
		Position int    `json:"position"`
		Format   string `json:"format"`
		Tracks   []struct {
			Position     int        `json:"position"`
			Title        string     `json:"title"`
			Length       int        `json:"length"` // milliseconds
			ArtistCredit []mbCredit `json:"artist-credit"`
			Recording    struct {
				ID    string   `json:"id"`
				ISRCs []string `json:"isrcs"`
			} `json:"recording"`
		} `json:"tracks"`
	} `json:"media"`
}

func (m *MusicBrainz) Release(ctx context.Context, id string) (Release, error) {
	var r mbRelease
	u := m.base + "/release/" + url.PathEscape(id) + "?inc=" + musicBrainzReleaseInc + "&fmt=json"
	if err := m.http.getJSON(ctx, u, &r); err != nil {
		return Release{}, err
	}
	credit, names := mbJoin(r.ArtistCredit)
	rel := Release{
		Provider: "musicbrainz", ID: r.ID, URL: musicBrainzSiteURL + "/release/" + r.ID,
		Title: r.Title, Artist: credit, Artists: names,
		Date:        pickMBDate(r.ReleaseGroup.FirstReleaseDate, r.Date),
		ReleaseType: r.ReleaseGroup.releaseType(),
		Genre:       topGenre(r.Genres, r.ReleaseGroup.Genres),
		Barcode:     r.Barcode, Country: r.Country,
	}
	if len(r.LabelInfo) > 0 {
		rel.CatalogNumber = r.LabelInfo[0].CatalogNumber
		if r.LabelInfo[0].Label != nil {
			rel.Label = r.LabelInfo[0].Label.Name
		}
	}
	if len(r.Media) > 0 {
		rel.Format = r.Media[0].Format
	}
	if r.CoverArtArchive.Front {
		rel.FrontURL = m.caaBase + "/release/" + r.ID + "/front"
	}
	if r.CoverArtArchive.Back {
		rel.BackURL = m.caaBase + "/release/" + r.ID + "/back"
	}
	for _, md := range r.Media {
		for _, t := range md.Tracks {
			tr := Track{
				Disc: md.Position, Number: t.Position, Title: t.Title,
				Duration:      time.Duration(t.Length) * time.Millisecond,
				MBRecordingID: t.Recording.ID,
			}
			tr.Artist, tr.Artists = mbJoin(t.ArtistCredit)
			if tr.Artist == "" {
				tr.Artist, tr.Artists = credit, names
			}
			if len(t.Recording.ISRCs) > 0 {
				tr.ISRC = t.Recording.ISRCs[0]
			}
			rel.Tracks = append(rel.Tracks, tr)
		}
	}
	return rel, nil
}

// topGenre picks the most-voted genre of the release, else of its group,
// formatted for tags: "contemporary r&b" -> "Contemporary R&B".
func topGenre(lists ...[]mbGenre) string {
	for _, gs := range lists {
		if len(gs) == 0 {
			continue
		}
		sorted := append([]mbGenre(nil), gs...)
		sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Count > sorted[j].Count })
		words := strings.Fields(sorted[0].Name)
		for i, w := range words {
			if strings.ContainsAny(w, "&") || len(w) <= 2 {
				words[i] = strings.ToUpper(w) // r&b, uk, dj
			} else {
				words[i] = strings.ToUpper(w[:1]) + w[1:]
			}
		}
		return strings.Join(words, " ")
	}
	return ""
}

// pickMBDate prefers the original release date (release group). When that
// is only a year or year-month, this edition's date fills it in if it is
// from the same period; a later reissue's date is never used.
func pickMBDate(group, edition string) string {
	if group == "" {
		return edition
	}
	if moreSpecific(group, edition) {
		return edition
	}
	return group
}
