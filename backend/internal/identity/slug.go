package identity

import (
	"regexp"
	"strings"
	"unicode"

	"golang.org/x/text/runes"
	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"
)

var slugCollapse = regexp.MustCompile(`-+`)

// Slugify returns a lowercase, hyphen-separated slug derived from s.
// Non-ASCII letters are folded to their ASCII base; punctuation is stripped;
// max length 63 (matches the schema check `^[a-z0-9][a-z0-9-]{1,62}$`).
// Returns "" if no usable characters remain.
func Slugify(s string) string {
	t := transform.Chain(norm.NFD, runes.Remove(runes.In(unicode.Mn)), norm.NFC)
	s, _, _ = transform.String(t, s)
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case unicode.IsSpace(r), r == '-', r == '_':
			b.WriteByte('-')
		default:
			// strip
		}
	}
	out := strings.Trim(slugCollapse.ReplaceAllString(b.String(), "-"), "-")
	if len(out) > 63 {
		out = strings.TrimRight(out[:63], "-")
	}
	return out
}
