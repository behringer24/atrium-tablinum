// Package safetext makes text that somebody else chose safe to show to a person.
package safetext

import (
	"strings"
	"unicode"
)

// Line makes a name that a client or another user chose safe to show on one line: control and
// invisible formatting characters (line breaks, bidirectional overrides, zero-width characters)
// become a space, runs of white space shrink to one, and the text is cut to max characters
// (marked with an ellipsis). max <= 0 means no limit.
func Line(s string, max int) string {
	var b strings.Builder
	gap := false
	for _, r := range s {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) || unicode.IsSpace(r) {
			gap = true
			continue
		}
		if gap && b.Len() > 0 {
			b.WriteByte(' ')
		}
		gap = false
		b.WriteRune(r)
	}
	out := []rune(b.String())
	if max > 0 && len(out) > max {
		return string(out[:max]) + "…"
	}
	return string(out)
}
