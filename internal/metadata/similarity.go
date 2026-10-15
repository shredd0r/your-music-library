package metadata

import (
	"regexp"
	"strings"
	"unicode"

	"golang.org/x/text/runes"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"
)

var stripMarks = transform.Chain(norm.NFKD, runes.Remove(runes.In(unicode.Mn)), norm.NFC)

// normalize folds case, accents, typographic quotes and punctuation so
// "Don’t Stop ’Til" and "Don't Stop 'til" compare equal.
func normalize(s string) string {
	s, _, _ = transform.String(stripMarks, s)
	s = strings.ToLower(s)
	s = strings.NewReplacer("&", " and ", "’", "'", "‘", "'", "`", "'").Replace(s)
	var b strings.Builder
	space := true
	for _, r := range s {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(r)
			space = false
		case r == '\'':
			// drop apostrophes: "don't" == "dont"
		default:
			if !space {
				b.WriteByte(' ')
				space = true
			}
		}
	}
	return strings.TrimSpace(b.String())
}

// editionNoise matches version suffixes that should not count against a
// match: "(Remastered 2009)", "- 2011 Remaster", "[Deluxe Edition]".
var editionNoise = regexp.MustCompile(`(?i)\s*([(\[][^)\]]*(remaster|deluxe|edition|version|mono|stereo|bonus|expanded|anniversary)[^)\]]*[)\]]|\s-\s.*(remaster|version|edit).*)$`)

// similarity returns 0..1; 1 means equal after normalization.
func similarity(a, b string) float64 {
	best := ratio(normalize(a), normalize(b))
	if sa, sb := editionNoise.ReplaceAllString(a, ""), editionNoise.ReplaceAllString(b, ""); sa != a || sb != b {
		best = max(best, ratio(normalize(sa), normalize(sb)))
	}
	return best
}

func ratio(a, b string) float64 {
	if a == b {
		return 1
	}
	ra, rb := []rune(a), []rune(b)
	n := max(len(ra), len(rb))
	if n == 0 {
		return 1
	}
	return 1 - float64(levenshtein(ra, rb))/float64(n)
}

func levenshtein(a, b []rune) int {
	prev := make([]int, len(b)+1)
	cur := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(b)]
}

// Normalize is the comparison form used for exact matching of names
// (case, accents, quotes and punctuation folded).
func Normalize(s string) string { return normalize(s) }

// NormalizeTitle is Normalize with edition noise removed, so
// "Bad (2012 Remaster)" and "Bad" compare equal.
func NormalizeTitle(s string) string { return normalize(editionNoise.ReplaceAllString(s, "")) }
