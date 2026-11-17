package docs

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode"

	"github.com/behringer24/atrium-tablinum/internal/ingest"
	"github.com/behringer24/atrium-tablinum/internal/safetext"
)

// Limits of the references to the originals of an entry.
const (
	MaxOrigins        = 10
	maxOriginRefLen   = 500
	maxOriginTitleLen = 200
)

// Origin is a reference to the original a piece of knowledge was taken from. Atrium keeps the
// reference, never the file: the entry itself is the lasting copy of the knowledge, the original
// is the proof of where it came from.
type Origin struct {
	// Ref is the address of the original, in the form scheme://... (https://, sharepoint://, ...).
	Ref string `json:"ref"`
	// Title names the original for a reader (file name, page title).
	Title string `json:"title,omitempty"`
	// ReadAt is the day (YYYY-MM-DD) the original was read.
	ReadAt string `json:"read_at,omitempty"`
	// SHA256 is the hash (hex) of the text that was taken over, to see later whether the original
	// has changed.
	SHA256 string `json:"sha256,omitempty"`
}

var (
	originScheme = regexp.MustCompile(`(?i)^[a-z][a-z0-9+.-]*://\S+$`)
	originHash   = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

func invalid(msg string) error { return errors.Join(ingest.ErrInvalid, errors.New(msg)) }

// NormalizeOrigins checks and cleans the references: each needs an address of the form
// scheme://..., names and dates are made safe to show, a hash is written in lower case. The
// references come from an assistant or from the original's own text and are shown to other people
// and assistants, so nothing is followed or fetched.
func NormalizeOrigins(in []Origin) ([]Origin, error) {
	if len(in) > MaxOrigins {
		return nil, invalid(fmt.Sprintf("at most %d origins per entry", MaxOrigins))
	}
	out := make([]Origin, 0, len(in))
	seen := map[string]bool{}
	for _, o := range in {
		ref := strings.TrimSpace(o.Ref)
		if ref == "" || len([]rune(ref)) > maxOriginRefLen {
			return nil, invalid(fmt.Sprintf("an origin needs a ref of 1 to %d characters", maxOriginRefLen))
		}
		for _, r := range ref {
			if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) || unicode.IsSpace(r) {
				return nil, invalid("an origin ref must not contain white space or control characters")
			}
		}
		if !originScheme.MatchString(ref) {
			return nil, invalid("an origin ref must have the form scheme://address, for example https://...")
		}
		if seen[ref] {
			continue
		}
		seen[ref] = true
		n := Origin{Ref: ref, Title: safetext.Line(o.Title, maxOriginTitleLen)}
		if d := strings.TrimSpace(o.ReadAt); d != "" {
			if _, err := time.Parse("2006-01-02", d); err != nil {
				return nil, invalid("origin read_at must be a date (YYYY-MM-DD)")
			}
			n.ReadAt = d
		}
		if h := strings.ToLower(strings.TrimSpace(o.SHA256)); h != "" {
			if !originHash.MatchString(h) {
				return nil, invalid("origin sha256 must be 64 hexadecimal characters")
			}
			n.SHA256 = h
		}
		out = append(out, n)
	}
	return out, nil
}
