package metadata

import (
	"context"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Discogs uses the Discogs API. Search needs a personal access token.
type Discogs struct {
	http *httpClient
	base string
}

// DiscogsBaseURL is the production API.
const DiscogsBaseURL = "https://api.discogs.com"

// NewDiscogs returns a client. Authenticated clients may make 60 requests
// per minute, so MinInterval defaults to one second.
func NewDiscogs(token string, opts HTTPOptions, base string) *Discogs {
	if base == "" {
		base = DiscogsBaseURL
	}
	if opts.MinInterval == 0 {
		opts.MinInterval = time.Second
	}
	if token != "" {
		opts.Header = http.Header{"Authorization": {"Discogs token=" + token}}
	}
	return &Discogs{http: newHTTP(opts), base: strings.TrimRight(base, "/")}
}

func (d *Discogs) Name() string { return "discogs" }

type discogsSearch struct {
	Results []struct {
		ID     int      `json:"id"`
		Title  string   `json:"title"` // "Artist - Title"
		Year   string   `json:"year"`
		Format []string `json:"format"`
	} `json:"results"`
}

func (d *Discogs) Search(ctx context.Context, q Query) ([]Candidate, error) {
	v := url.Values{"type": {"release"}, "per_page": {"10"}}
	if q.Artist != "" {
		v.Set("artist", q.Artist)
	}
	if q.Album != "" {
		v.Set("release_title", q.Album)
	} else if len(q.Tracks) > 0 {
		v.Set("track", q.Tracks[0].Title)
	}
	var res discogsSearch
	if err := d.http.getJSON(ctx, d.base+"/database/search?"+v.Encode(), &res); err != nil {
		return nil, err
	}
	var out []Candidate
	for _, r := range res.Results {
		artist, title, ok := strings.Cut(r.Title, " - ")
		if !ok {
			title, artist = r.Title, ""
		}
		year, _ := strconv.Atoi(r.Year)
		rt := discogsReleaseType(r.Format)
		out = append(out, Candidate{
			ID: strconv.Itoa(r.ID), Title: title, Artist: cleanDiscogsName(artist),
			Year: year, ReleaseType: rt, Compilation: rt == "compilation",
		})
	}
	return out, nil
}

type discogsArtist struct {
	Name string `json:"name"`
	ANV  string `json:"anv"` // name variation as credited
	Join string `json:"join"`
}

type discogsTrack struct {
	Position  string          `json:"position"`
	Type      string          `json:"type_"`
	Title     string          `json:"title"`
	Duration  string          `json:"duration"`
	Artists   []discogsArtist `json:"artists"`
	SubTracks []discogsTrack  `json:"sub_tracks"`
}

type discogsRelease struct {
	ID       int             `json:"id"`
	MasterID int             `json:"master_id"`
	URI      string          `json:"uri"`
	Title    string          `json:"title"`
	Artists  []discogsArtist `json:"artists"`
	Released string          `json:"released"`
	Year     int             `json:"year"`
	Country  string          `json:"country"`
	Genres   []string        `json:"genres"`
	Labels   []struct {
		Name  string `json:"name"`
		Catno string `json:"catno"`
	} `json:"labels"`
	Formats []struct {
		Name         string   `json:"name"`
		Descriptions []string `json:"descriptions"`
	} `json:"formats"`
	Identifiers []struct {
		Type  string `json:"type"`
		Value string `json:"value"`
	} `json:"identifiers"`
	Tracklist []discogsTrack `json:"tracklist"`
	Images    []struct {
		Type string `json:"type"`
		URI  string `json:"uri"`
	} `json:"images"`
}

func (d *Discogs) Release(ctx context.Context, id string) (Release, error) {
	var r discogsRelease
	if err := d.http.getJSON(ctx, d.base+"/releases/"+url.PathEscape(id), &r); err != nil {
		return Release{}, err
	}
	credit, names := discogsCredit(r.Artists)
	rel := Release{
		Provider: "discogs", ID: strconv.Itoa(r.ID), URL: r.URI,
		Title: r.Title, Artist: credit, Artists: names,
		Date: discogsDate(r.Released, r.Year), Country: r.Country,
	}
	if r.MasterID > 0 {
		rel.MasterID = strconv.Itoa(r.MasterID)
	}
	if len(r.Genres) > 0 {
		rel.Genre = r.Genres[0]
	}
	if len(r.Labels) > 0 {
		rel.Label, rel.CatalogNumber = cleanDiscogsName(r.Labels[0].Name), r.Labels[0].Catno
	}
	var descriptions []string
	for _, f := range r.Formats {
		descriptions = append(descriptions, f.Descriptions...)
	}
	if len(r.Formats) > 0 {
		rel.Format = r.Formats[0].Name
	}
	rel.ReleaseType = discogsReleaseType(descriptions)
	for _, idf := range r.Identifiers {
		if idf.Type == "Barcode" {
			rel.Barcode = strings.ReplaceAll(idf.Value, " ", "")
			break
		}
	}
	for _, img := range r.Images {
		if img.Type == "primary" {
			rel.FrontURL = img.URI
			break
		}
	}
	if rel.FrontURL == "" && len(r.Images) > 0 {
		rel.FrontURL = r.Images[0].URI
	}

	var flat []discogsTrack
	for _, t := range r.Tracklist {
		switch t.Type {
		case "track", "":
			flat = append(flat, t)
		case "index": // a medley/suite: its parts are the tracks
			flat = append(flat, t.SubTracks...)
		}
	}
	perDisc := map[int]int{}
	for _, t := range flat {
		disc, num, ok := parseDiscogsPosition(t.Position)
		if !ok {
			disc = 1
			perDisc[disc]++
			num = perDisc[disc]
		}
		tr := Track{Disc: disc, Number: num, Title: t.Title, Duration: parseClock(t.Duration)}
		if len(t.Artists) > 0 {
			tr.Artist, tr.Artists = discogsCredit(t.Artists)
		} else {
			tr.Artist, tr.Artists = credit, names
		}
		rel.Tracks = append(rel.Tracks, tr)
	}
	return rel, nil
}

// discogsNumberSuffix matches Discogs' disambiguation "Prince (2)".
var discogsNumberSuffix = regexp.MustCompile(`\s+\(\d+\)$`)

func cleanDiscogsName(s string) string {
	return strings.TrimSpace(discogsNumberSuffix.ReplaceAllString(s, ""))
}

// discogsCredit joins artists as printed ("A & B", "A Feat. B").
func discogsCredit(as []discogsArtist) (string, []string) {
	var b strings.Builder
	var names []string
	for i, a := range as {
		name := cleanDiscogsName(a.Name)
		names = append(names, name)
		if a.ANV != "" {
			b.WriteString(a.ANV)
		} else {
			b.WriteString(name)
		}
		if i < len(as)-1 {
			switch j := strings.TrimSpace(a.Join); j {
			case "", ",":
				b.WriteString(", ")
			default:
				b.WriteString(" " + j + " ")
			}
		}
	}
	return b.String(), names
}

// discogsDate turns "1987-07-00" into "1987-07"; falls back to the year.
func discogsDate(released string, year int) string {
	parts := strings.Split(released, "-")
	for len(parts) > 1 && (parts[len(parts)-1] == "00" || parts[len(parts)-1] == "") {
		parts = parts[:len(parts)-1]
	}
	if d := strings.Join(parts, "-"); d != "" && d != "0" {
		return d
	}
	if year > 0 {
		return strconv.Itoa(year)
	}
	return ""
}

func discogsReleaseType(descriptions []string) string {
	has := func(s string) bool {
		for _, d := range descriptions {
			if strings.EqualFold(d, s) {
				return true
			}
		}
		return false
	}
	switch {
	case has("Compilation"):
		return "compilation"
	case has("Single") || has("Maxi-Single"):
		return "single"
	case has("EP"):
		return "ep"
	}
	return "album"
}

// discogsPosition matches multi-disc positions: "2-03", "CD2-3", "2.03".
var discogsPosition = regexp.MustCompile(`^(?i:cd|disc|dvd)?(\d+)[-.](\d+)$`)

func parseDiscogsPosition(p string) (disc, num int, ok bool) {
	m := discogsPosition.FindStringSubmatch(strings.TrimSpace(p))
	if m == nil {
		return 0, 0, false // "A1", "B", "3": numbered in order on disc 1
	}
	disc, _ = strconv.Atoi(m[1])
	num, _ = strconv.Atoi(m[2])
	return disc, num, disc > 0 && num > 0
}

// parseClock parses "3:32" or "1:02:03".
func parseClock(s string) time.Duration {
	var total int
	for _, part := range strings.Split(strings.TrimSpace(s), ":") {
		n, err := strconv.Atoi(part)
		if err != nil {
			return 0
		}
		total = total*60 + n
	}
	return time.Duration(total) * time.Second
}

// RefineDate looks up the master release's main edition, which often has
// the full date when a reissue or regional edition only has the year.
func (d *Discogs) RefineDate(ctx context.Context, rel Release) (string, error) {
	if fullDate(rel.Date) || rel.MasterID == "" {
		return "", nil
	}
	var master struct {
		MainRelease int `json:"main_release"`
	}
	if err := d.http.getJSON(ctx, d.base+"/masters/"+url.PathEscape(rel.MasterID), &master); err != nil {
		return "", err
	}
	main := strconv.Itoa(master.MainRelease)
	if master.MainRelease == 0 || main == rel.ID {
		return "", nil
	}
	mr, err := d.Release(ctx, main)
	if err != nil {
		return "", err
	}
	if moreSpecific(rel.Date, mr.Date) {
		return mr.Date, nil
	}
	return "", nil
}
