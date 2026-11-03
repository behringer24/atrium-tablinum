// Package guard rejects content that looks like credentials or sensitive financial
// identifiers before it is stored. It is a heuristic safety net, not a guarantee.
package guard

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// Finding names a kind of secret and the line it was found on. It never contains the
// matched text, so errors and logs cannot leak it.
type Finding struct {
	Kind string
	Line int
}

// Error is returned by Check when content must not be stored.
type Error struct{ Findings []Finding }

func (e *Error) Error() string {
	parts := make([]string, len(e.Findings))
	for i, f := range e.Findings {
		parts[i] = fmt.Sprintf("%s (line %d)", f.Kind, f.Line)
	}
	return "Content rejected, it probably contains: " + strings.Join(parts, "; ") +
		". Credentials, keys and payment data do not belong in the knowledge store - please remove them or replace them with a placeholder."
}

type pattern struct {
	kind string
	re   *regexp.Regexp
	// ok decides on the full match (and submatches); nil accepts every match.
	ok func(m []string) bool
}

var patterns = []pattern{
	{kind: "private key", re: regexp.MustCompile(`-----BEGIN [A-Z0-9 ]*PRIVATE KEY(?: BLOCK)?-----`)},
	{kind: "AWS access key", re: regexp.MustCompile(`\b(?:AKIA|ASIA)[0-9A-Z]{16}\b`)},
	{kind: "GitHub token", re: regexp.MustCompile(`\b(?:gh[pousr]_[A-Za-z0-9]{30,}|github_pat_[A-Za-z0-9_]{40,})\b`)},
	{kind: "Slack token", re: regexp.MustCompile(`\bxox[abprs]-[A-Za-z0-9-]{10,}\b`)},
	{kind: "Google API key", re: regexp.MustCompile(`\bAIza[0-9A-Za-z_-]{35}\b`)},
	{kind: "API key (sk-…)", re: regexp.MustCompile(`\b(?:sk|rk|pk)_(?:live|test)_[A-Za-z0-9]{16,}\b|\bsk-[A-Za-z0-9_-]{32,}\b`)},
	{kind: "JSON Web Token", re: regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{8,}\.eyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\b`)},
	{kind: "bearer token", re: regexp.MustCompile(`(?i)\bbearer\s+[A-Za-z0-9._~+/=-]{24,}`)},
	{
		kind: "credentials in URL",
		re:   regexp.MustCompile(`\b[a-z][a-z0-9+.-]*://[^\s:/@]+:([^\s@/]+)@`),
		ok:   func(m []string) bool { return !placeholder(m[1]) },
	},
	{
		kind: "password or secret",
		re:   regexp.MustCompile(`(?i)\b(?:password|passwort|passwd|pwd|kennwort|secret|client[_-]?secret|api[_-]?key|apikey|access[_-]?token|auth[_-]?token|private[_-]?key|token)\b["']?\s*[:=]\s*["']?([^\s"',;]{8,})`),
		ok:   func(m []string) bool { return looksSecret(m[1]) },
	},
}

var (
	ibanRe = regexp.MustCompile(`\b[A-Z]{2}\d{2}(?: ?[A-Z0-9]{4}){2,7}(?: ?[A-Z0-9]{1,3})?\b`)
	cardRe = regexp.MustCompile(`\b(?:\d[ -]?){12,18}\d\b`)
)

// Scan returns all findings in text, ordered by line.
func Scan(text string) []Finding {
	var out []Finding
	lineAt := lineIndex(text)
	add := func(kind string, pos int) {
		out = append(out, Finding{Kind: kind, Line: lineAt(pos)})
	}

	for _, p := range patterns {
		for _, loc := range p.re.FindAllStringSubmatchIndex(text, -1) {
			m := submatches(text, loc)
			if p.ok == nil || p.ok(m) {
				add(p.kind, loc[0])
			}
		}
	}
	for _, loc := range ibanRe.FindAllStringIndex(text, -1) {
		if validIBAN(text[loc[0]:loc[1]]) {
			add("IBAN", loc[0])
		}
	}
	for _, loc := range cardRe.FindAllStringIndex(text, -1) {
		if validCard(text[loc[0]:loc[1]]) {
			add("card number", loc[0])
		}
	}

	sort.SliceStable(out, func(i, j int) bool { return out[i].Line < out[j].Line })
	return dedupe(out)
}

// Check returns *Error when Scan finds anything.
func Check(text string) error {
	if f := Scan(text); len(f) > 0 {
		return &Error{Findings: f}
	}
	return nil
}

func submatches(text string, loc []int) []string {
	m := make([]string, len(loc)/2)
	for i := range m {
		if loc[2*i] >= 0 {
			m[i] = text[loc[2*i]:loc[2*i+1]]
		}
	}
	return m
}

func lineIndex(text string) func(pos int) int {
	var starts []int
	for i, r := range text {
		if r == '\n' {
			starts = append(starts, i)
		}
	}
	return func(pos int) int { return sort.SearchInts(starts, pos) + 1 }
}

func dedupe(in []Finding) []Finding {
	seen := map[Finding]bool{}
	var out []Finding
	for _, f := range in {
		if !seen[f] {
			seen[f] = true
			out = append(out, f)
		}
	}
	return out
}

// placeholder recognises template values like <password>, ${VAR}, xxxx or ****.
func placeholder(v string) bool {
	if v == "" {
		return true
	}
	switch v[0] {
	case '<', '{', '$', '[', '%':
		return true
	}
	if strings.Trim(v, "xX*•.-_") == "" {
		return true
	}
	l := strings.ToLower(v)
	for _, w := range []string{"example", "placeholder", "platzhalter", "your", "dein", "changeme"} {
		if strings.Contains(l, w) {
			return true
		}
	}
	return false
}

// looksSecret requires a mix of character classes, so prose after "Passwort:" passes
// while "Tr0ub4dor&3" does not.
func looksSecret(v string) bool {
	if placeholder(v) {
		return false
	}
	var lower, upper, digit, symbol bool
	for _, r := range v {
		switch {
		case r >= 'a' && r <= 'z', r > 127:
			lower = true
		case r >= 'A' && r <= 'Z':
			upper = true
		case r >= '0' && r <= '9':
			digit = true
		default:
			symbol = true
		}
	}
	classes := 0
	for _, b := range []bool{lower, upper, digit, symbol} {
		if b {
			classes++
		}
	}
	return classes >= 2 && (digit || symbol)
}

func validIBAN(s string) bool {
	s = strings.ReplaceAll(s, " ", "")
	if len(s) < 15 || len(s) > 34 {
		return false
	}
	s = s[4:] + s[:4]
	rem := 0
	for _, r := range s {
		var n int
		switch {
		case r >= '0' && r <= '9':
			n = int(r - '0')
			rem = (rem*10 + n) % 97
		case r >= 'A' && r <= 'Z':
			n = int(r-'A') + 10
			rem = (rem*100 + n) % 97
		default:
			return false
		}
	}
	return rem == 1
}

func validCard(s string) bool {
	var digits []int
	for _, r := range s {
		if r >= '0' && r <= '9' {
			digits = append(digits, int(r-'0'))
		}
	}
	if len(digits) < 13 || len(digits) > 19 || !plausibleIssuer(digits) {
		return false
	}
	sum := 0
	for i := range digits {
		d := digits[len(digits)-1-i]
		if i%2 == 1 {
			if d *= 2; d > 9 {
				d -= 9
			}
		}
		sum += d
	}
	return sum%10 == 0
}

// plausibleIssuer limits Luhn matches to real card ranges (Visa, Mastercard, Amex,
// Discover, Diners, JCB, UnionPay), which keeps long ids from tripping the guard.
func plausibleIssuer(d []int) bool {
	p2 := d[0]*10 + d[1]
	p4 := p2*100 + d[2]*10 + d[3]
	switch {
	case d[0] == 4:
		return true
	case p2 >= 51 && p2 <= 55, p4 >= 2221 && p4 <= 2720:
		return true
	case p2 == 34 || p2 == 37:
		return true
	case p4 == 6011 || p2 == 65 || (p2 >= 64 && p2 <= 65):
		return true
	case p2 == 36 || p2 == 38 || p2 == 30 || p2 == 62 || p2 == 35:
		return true
	}
	return false
}
