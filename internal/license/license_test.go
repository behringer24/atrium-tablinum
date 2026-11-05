package license

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/behringer24/atrium-tablinum/internal/product"
)

func day(s string) time.Time {
	d, err := time.Parse(dateLayout, s)
	if err != nil {
		panic(err)
	}
	return d
}

func keypair(t *testing.T, id string) (Anchors, ed25519.PrivateKey) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return Anchors{id: pub}, priv
}

func claims(maintenance string) Claims {
	return Claims{V: 1, ID: "lic-1", Licensee: "Example GmbH", Domain: "atrium.example.com", Edition: "pro",
		Features: []string{"scim"}, Limits: map[string]int64{"users": 50}, Issued: "2026-01-01",
		MaintenanceUntil: maintenance, IssuerKey: "k1"}
}

func TestSignAndVerify(t *testing.T) {
	anchors, priv := keypair(t, "k1")
	tok, err := Sign(priv, claims("2027-01-01"))
	if err != nil || !strings.HasPrefix(tok, "atrium-license-v1.") {
		t.Fatalf("sign: %v %q", err, tok)
	}
	c, err := Verify(tok, anchors)
	if err != nil || c.Licensee != "Example GmbH" || c.Limits["users"] != 50 || c.IssuerKey != "k1" {
		t.Fatalf("verify: %+v %v", c, err)
	}
	// whitespace around the token (a pasted file) is fine
	if _, err := Verify("  "+tok+"\n", anchors); err != nil {
		t.Errorf("padded token: %v", err)
	}

	// a changed payload does not verify
	rest := strings.TrimPrefix(tok, tokenPrefix)
	p, s, _ := strings.Cut(rest, ".")
	raw, _ := base64.RawURLEncoding.DecodeString(p)
	forged := strings.Replace(string(raw), `"users":50`, `"users":5000`, 1)
	if forged == string(raw) {
		t.Fatal("test setup: nothing replaced")
	}
	bad := tokenPrefix + base64.RawURLEncoding.EncodeToString([]byte(forged)) + "." + s
	if _, err := Verify(bad, anchors); !errors.Is(err, ErrSignature) {
		t.Errorf("forged payload: %v", err)
	}
	// another issuer key, an unknown key id, garbage
	other, _ := keypair(t, "k1")
	if _, err := Verify(tok, other); !errors.Is(err, ErrSignature) {
		t.Errorf("wrong key under the same id: %v", err)
	}
	if _, err := Verify(tok, Anchors{"k9": anchors["k1"]}); !errors.Is(err, ErrUnknownKey) {
		t.Errorf("unknown key id: %v", err)
	}
	for _, g := range []string{"", "nonsense", "atrium-license-v1.", "atrium-license-v1.abc", "atrium-license-v1.!!.!!", tokenPrefix + p} {
		if _, err := Verify(g, anchors); !errors.Is(err, ErrFormat) {
			t.Errorf("%q: %v", g, err)
		}
	}
	// a licence needs its fields
	if _, err := Sign(priv, Claims{}); err == nil {
		t.Error("an empty licence was signed")
	}
	if _, err := Sign(priv, claims("not a date")); err == nil {
		t.Error("a licence with a bad date was signed")
	}
}

func TestParseAnchors(t *testing.T) {
	a, _ := keypair(t, "k1")
	b64 := base64.StdEncoding.EncodeToString(a["k1"])
	got, err := ParseAnchors("# comment\n\nk1 " + b64 + "  # trailing\nk2 " + b64 + "\n")
	if err != nil || len(got) != 2 {
		t.Fatalf("%v %v", got, err)
	}
	for _, bad := range []string{"k1", "k1 notbase64!!", "k1 " + base64.StdEncoding.EncodeToString([]byte("short")), "k1 " + b64 + "\nk1 " + b64} {
		if _, err := ParseAnchors(bad); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
	// the anchors compiled into this build parse (real keys are pasted into anchors.txt)
	if _, err := EmbeddedAnchors(); err != nil {
		t.Errorf("embedded anchors: %v", err)
	}
}

func TestEvaluate(t *testing.T) {
	line := product.Line{Name: "3.4", Date: day("2022-05-10")}
	now := day("2026-11-05")
	base := "https://Atrium.Example.com:8443/"
	c := claims("2022-05-10")

	cases := []struct {
		name string
		c    func() *Claims
		line product.Line
		url  string
		want Status
	}{
		{"no licence", func() *Claims { return nil }, line, base, None},
		{"maintenance ends the day the line was released", func() *Claims { return &c }, line, base, Valid},
		{"covered by far", func() *Claims { x := claims("2099-12-31"); return &x }, line, base, Valid},
		{"a patch of a covered line carries the line date", func() *Claims { return &c }, product.Line{Name: "3.4", Date: day("2022-05-10")}, base, Valid},
		{"the next line is newer than the maintenance", func() *Claims { return &c }, product.Line{Name: "3.5", Date: day("2022-05-11")}, base, NotCovered},
		{"an amnesty epoch covers a newer line", func() *Claims { return &c }, product.Line{Name: "6.2", Date: day("2026-03-01"), Epoch: ptr(day("2022-01-01"))}, base, Valid},
		{"wrong domain", func() *Claims { return &c }, line, "https://other.example.com", WrongHost},
		{"no host", func() *Claims { return &c }, line, "not a url", WrongHost},
		{"expired", func() *Claims { x := claims("2099-12-31"); x.Expires = "2026-11-04"; return &x }, line, base, Expired},
		{"expires today: still valid all day", func() *Claims { x := claims("2099-12-31"); x.Expires = "2026-11-05"; return &x }, line, base, Valid},
		{"expires later", func() *Claims { x := claims("2099-12-31"); x.Expires = "2027-01-01"; return &x }, line, base, Valid},
	}
	for _, tc := range cases {
		st := Evaluate(tc.c(), tc.line, tc.url, now)
		if st.Status != tc.want {
			t.Errorf("%s: %s (%s), want %s", tc.name, st.Status, st.Detail, tc.want)
		}
		if st.Status != Valid && (st.Edition != Community || st.Has("scim")) {
			t.Errorf("%s: a licence that is not valid still grants things: %+v", tc.name, st)
		}
		if _, ok := st.Limit("users"); ok != (st.Status == Valid) {
			t.Errorf("%s: limit visible = %v in state %s", tc.name, ok, st.Status)
		}
	}

	// a valid licence grants its edition, features and limits; the build date never matters
	x := claims("2099-12-31")
	st := Evaluate(&x, line, base, now)
	if st.Edition != "pro" || !st.Has("scim") || st.Has("audit_export") {
		t.Errorf("valid: %+v", st)
	}
	if n, ok := st.Limit("users"); !ok || n != 50 {
		t.Errorf("limit: %d %v", n, ok)
	}
	if _, ok := st.Limit("spaces"); ok {
		t.Error("a limit the licence does not set must fall back to the Community default")
	}
	// the coverage of a build that lies before the clock: no clock is involved in the coverage rule
	if st := Evaluate(&c, line, base, day("2099-01-01")); st.Status != Valid {
		t.Errorf("a covered build stays covered for ever: %s", st.Status)
	}
}

func ptr[T any](v T) *T { return &v }
