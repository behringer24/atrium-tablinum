// Package license verifies and evaluates Atrium licence files. A licence is a small statement signed
// with an Ed25519 key whose public half is compiled into the binary; it is checked offline and never
// calls anybody. Design and the reasons for it: docs/design/licensing.md.
//
// A token is "atrium-license-v1.<payload>.<signature>", both base64url. The payload is JSON (Claims),
// the signature covers a fixed prefix and the exact payload bytes.
package license

import (
	"crypto/ed25519"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/behringer24/atrium-tablinum/internal/product"
)

const (
	tokenPrefix = "atrium-license-v1."
	signPrefix  = "atrium-license-v1\n"
	dateLayout  = "2006-01-02"

	// Community is the edition without a (covering) licence.
	Community = "community"
)

var (
	ErrFormat     = errors.New("not an Atrium licence")
	ErrUnknownKey = errors.New("the licence was signed with a key this build does not trust")
	ErrSignature  = errors.New("the signature of the licence does not match its content")
)

// Claims is what a licence says.
type Claims struct {
	V        int              `json:"v"`
	ID       string           `json:"id"`
	Licensee string           `json:"licensee"`
	Domain   string           `json:"domain"`             // the instance: host of instance.base_url
	Edition  string           `json:"edition"`            // a name such as pro or education; it switches nothing, Features and Limits do
	Features []string         `json:"features,omitempty"` // named switches
	Limits   map[string]int64 `json:"limits,omitempty"`   // named ceilings, 0 = unlimited; a missing key = the Community default
	Issued   string           `json:"issued"`             // YYYY-MM-DD
	// MaintenanceUntil: builds of release lines dated up to this day are covered, for ever.
	MaintenanceUntil string `json:"maintenance_until"`
	// Expires is optional, for trials and subscriptions: after it the licence has no effect.
	Expires   string `json:"expires,omitempty"`
	IssuerKey string `json:"issuer_key"`
}

// Dates parses the three dates; expires is the zero time if absent.
func (c Claims) Dates() (issued, maintenance, expires time.Time, err error) {
	if issued, err = time.Parse(dateLayout, c.Issued); err != nil {
		return issued, maintenance, expires, fmt.Errorf("issued: %w", err)
	}
	if maintenance, err = time.Parse(dateLayout, c.MaintenanceUntil); err != nil {
		return issued, maintenance, expires, fmt.Errorf("maintenance_until: %w", err)
	}
	if c.Expires != "" {
		if expires, err = time.Parse(dateLayout, c.Expires); err != nil {
			return issued, maintenance, expires, fmt.Errorf("expires: %w", err)
		}
	}
	return issued, maintenance, expires, nil
}

// Anchors are the trusted issuer public keys by id.
type Anchors map[string]ed25519.PublicKey

//go:embed anchors.txt
var anchorsFile string

// EmbeddedAnchors returns the issuer public keys compiled into this build (anchors.txt).
func EmbeddedAnchors() (Anchors, error) { return ParseAnchors(anchorsFile) }

// ParseAnchors reads lines of "<id> <base64 public key>"; blank lines and # comments are ignored.
func ParseAnchors(text string) (Anchors, error) {
	out := Anchors{}
	for _, raw := range strings.Split(text, "\n") {
		s := raw
		if i := strings.Index(s, "#"); i >= 0 {
			s = s[:i]
		}
		f := strings.Fields(s)
		if len(f) == 0 {
			continue
		}
		if len(f) != 2 {
			return nil, fmt.Errorf("anchors: cannot read %q", strings.TrimSpace(raw))
		}
		key, err := base64.StdEncoding.DecodeString(f[1])
		if err != nil || len(key) != ed25519.PublicKeySize {
			return nil, fmt.Errorf("anchors: %s: not an Ed25519 public key", f[0])
		}
		if _, dup := out[f[0]]; dup {
			return nil, fmt.Errorf("anchors: %s appears twice", f[0])
		}
		out[f[0]] = ed25519.PublicKey(key)
	}
	return out, nil
}

// Sign makes a token. The issuing tool uses it; the server never signs.
func Sign(key ed25519.PrivateKey, c Claims) (string, error) {
	if c.V == 0 {
		c.V = 1
	}
	if c.IssuerKey == "" || c.ID == "" || c.Licensee == "" || c.Domain == "" || c.Edition == "" {
		return "", errors.New("a licence needs id, licensee, domain, edition and issuer_key")
	}
	if _, _, _, err := c.Dates(); err != nil {
		return "", err
	}
	payload, err := json.Marshal(c)
	if err != nil {
		return "", err
	}
	sig := ed25519.Sign(key, append([]byte(signPrefix), payload...))
	return tokenPrefix + base64.RawURLEncoding.EncodeToString(payload) + "." + base64.RawURLEncoding.EncodeToString(sig), nil
}

// Verify checks the signature against the anchors and returns the claims. It says nothing about
// whether the licence covers this build or this instance: see Evaluate.
func Verify(token string, anchors Anchors) (Claims, error) {
	token = strings.TrimSpace(token)
	rest, ok := strings.CutPrefix(token, tokenPrefix)
	if !ok {
		return Claims{}, ErrFormat
	}
	p, s, ok := strings.Cut(rest, ".")
	if !ok {
		return Claims{}, ErrFormat
	}
	payload, err1 := base64.RawURLEncoding.DecodeString(p)
	sig, err2 := base64.RawURLEncoding.DecodeString(s)
	if err1 != nil || err2 != nil {
		return Claims{}, ErrFormat
	}
	var c Claims
	if err := json.Unmarshal(payload, &c); err != nil || c.V != 1 {
		return Claims{}, ErrFormat
	}
	key, ok := anchors[c.IssuerKey]
	if !ok {
		return Claims{}, ErrUnknownKey
	}
	if !ed25519.Verify(key, append([]byte(signPrefix), payload...), sig) {
		return Claims{}, ErrSignature
	}
	if _, _, _, err := c.Dates(); err != nil {
		return Claims{}, fmt.Errorf("%w: %v", ErrFormat, err)
	}
	return c, nil
}

// Status is how a licence relates to this build and this instance.
type Status string

const (
	None       Status = "none"        // no licence installed: Community
	Valid      Status = "valid"       // covers this build and this instance
	NotCovered Status = "not covered" // this build is newer than the maintenance
	Expired    Status = "expired"     // the optional expires date has passed
	WrongHost  Status = "wrong domain"
	Invalid    Status = "invalid" // unreadable, unknown key or bad signature
)

// State is the evaluated licence.
type State struct {
	Status Status
	Detail string // a sentence for the admin
	Claims *Claims
	// Edition in effect: the licence's edition when Valid, else Community.
	Edition string
	// Coverage is the date of the build's release line that was compared with MaintenanceUntil.
	Coverage time.Time
}

// Has reports whether a feature is on: only a valid licence switches features on.
func (s State) Has(feature string) bool {
	if s.Status != Valid || s.Claims == nil {
		return false
	}
	for _, f := range s.Claims.Features {
		if f == feature {
			return true
		}
	}
	return false
}

// Limit returns the licensed ceiling of a limit (0 = unlimited) and whether the licence sets one.
// Without a valid licence it sets none, so the Community default applies.
func (s State) Limit(name string) (int64, bool) {
	if s.Status != Valid || s.Claims == nil {
		return 0, false
	}
	v, ok := s.Claims.Limits[name]
	return v, ok
}

// Evaluate compares a licence with the release line of this build, the host of the instance and the
// clock. The coverage rule compares two fixed dates and needs no clock: the build's line date must
// not be after maintenance_until. Only the optional expires date uses now.
func Evaluate(c *Claims, line product.Line, baseURL string, now time.Time) State {
	st := State{Status: None, Edition: Community, Coverage: line.Coverage()}
	if c == nil {
		st.Detail = "No licence is installed: Community edition."
		return st
	}
	st.Claims = c
	_, maintenance, expires, err := c.Dates()
	if err != nil {
		st.Status, st.Detail = Invalid, "The dates of the licence cannot be read."
		return st
	}
	if want := hostOf(baseURL); want == "" || !strings.EqualFold(want, c.Domain) {
		st.Status = WrongHost
		st.Detail = fmt.Sprintf("The licence is for %s, this instance is %s.", c.Domain, orNone(want))
		return st
	}
	if !expires.IsZero() && !now.Before(expires.AddDate(0, 0, 1)) {
		st.Status = Expired
		st.Detail = fmt.Sprintf("The licence expired on %s.", c.Expires)
		return st
	}
	if line.Coverage().After(maintenance) {
		st.Status = NotCovered
		st.Detail = fmt.Sprintf("This build (line %s, %s) is newer than the maintenance of the licence, which covers lines up to %s. "+
			"Pro features are off; use a version of an earlier line, or renew the maintenance.",
			line.Name, line.Coverage().Format(dateLayout), c.MaintenanceUntil)
		return st
	}
	st.Status, st.Edition = Valid, c.Edition
	st.Detail = fmt.Sprintf("Licensed to %s, edition %s, maintenance until %s.", c.Licensee, c.Edition, c.MaintenanceUntil)
	return st
}

func hostOf(baseURL string) string {
	u, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil {
		return ""
	}
	return strings.ToLower(u.Hostname())
}

func orNone(s string) string {
	if s == "" {
		return "(no host)"
	}
	return s
}
