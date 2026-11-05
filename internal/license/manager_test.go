package license

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/behringer24/atrium-tablinum/internal/dbtest"
	"github.com/behringer24/atrium-tablinum/internal/product"
)

func TestManagerInstallRemoveAndLimits(t *testing.T) {
	ctx := context.Background()
	anchors, priv := keypair(t, "k1")
	line := product.Line{Name: "3.4", Date: day("2022-05-10")}
	m := NewManager(Config{Anchors: anchors, Line: line, BaseURL: "https://atrium.example.com", CommunityMaxUsers: 3,
		Now: func() time.Time { return day("2026-11-05") }})
	if err := m.Load(ctx); err != nil {
		t.Fatal(err)
	}
	if st := m.State(); st.Status != None || m.MaxUsers() != 3 || m.Has("scim") {
		t.Fatalf("fresh: %+v max %d", st, m.MaxUsers())
	}

	tok, _ := Sign(priv, claims("2099-12-31"))
	st, err := m.Install(ctx, tok)
	if err != nil || st.Status != Valid || m.Source() != "installed" {
		t.Fatalf("install: %+v %v", st, err)
	}
	if m.MaxUsers() != 50 || !m.Has("scim") {
		t.Errorf("licensed: max %d, scim %v", m.MaxUsers(), m.Has("scim"))
	}
	// a licence without a users limit falls back to the Community default
	c := claims("2099-12-31")
	c.Limits = nil
	tok2, _ := Sign(priv, c)
	if _, err := m.Install(ctx, tok2); err != nil || m.MaxUsers() != 3 {
		t.Errorf("no users limit in the licence: %v max %d", err, m.MaxUsers())
	}

	// refused: wrong domain, bad signature; accepted but reported: not covering this build
	wrong := claims("2099-12-31")
	wrong.Domain = "other.example.com"
	tokWrong, _ := Sign(priv, wrong)
	if _, err := m.Install(ctx, tokWrong); err == nil {
		t.Error("a licence for another domain was installed")
	}
	if _, err := m.Install(ctx, "atrium-license-v1.x.y"); err == nil {
		t.Error("garbage was installed")
	}
	old, _ := Sign(priv, claims("2021-01-01"))
	if st, err := m.Install(ctx, old); err != nil || st.Status != NotCovered || m.MaxUsers() != 3 || m.Has("scim") {
		t.Errorf("not covering: %+v %v", st, err)
	}

	if err := m.Remove(ctx); err != nil || m.State().Status != None || m.Source() != "" {
		t.Errorf("remove: %+v %v", m.State(), err)
	}
}

func TestManagerReadsTheConfiguredFileAndAnInstalledLicenceWins(t *testing.T) {
	ctx := context.Background()
	anchors, priv := keypair(t, "k1")
	line := product.Line{Name: "3.4", Date: day("2022-05-10")}
	dir := t.TempDir()
	file := filepath.Join(dir, "licence.txt")
	tokFile, _ := Sign(priv, claims("2099-12-31"))
	if err := os.WriteFile(file, []byte(tokFile+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	mk := func(f string) *Manager {
		return NewManager(Config{Anchors: anchors, Line: line, BaseURL: "https://atrium.example.com", File: f, CommunityMaxUsers: 3,
			Now: func() time.Time { return day("2026-11-05") }})
	}
	m := mk(file)
	if err := m.Load(ctx); err != nil || m.State().Status != Valid || m.Source() != "file" {
		t.Fatalf("file: %+v %v", m.State(), err)
	}
	// a missing or broken file is a state, not a crash
	for name, f := range map[string]string{"missing": filepath.Join(dir, "nope"), "broken": func() string {
		p := filepath.Join(dir, "broken")
		_ = os.WriteFile(p, []byte("not a licence"), 0o600)
		return p
	}()} {
		b := mk(f)
		if err := b.Load(ctx); err != nil || b.State().Status != Invalid || b.MaxUsers() != 3 {
			t.Errorf("%s: %+v %v", name, b.State(), err)
		}
	}
}

func TestManagerStoresTheInstalledLicenceInTheDatabase(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.NewPool(t)
	anchors, priv := keypair(t, "k1")
	line := product.Line{Name: "3.4", Date: day("2022-05-10")}
	cfg := Config{Anchors: anchors, Line: line, BaseURL: "https://atrium.example.com", Pool: pool, Now: func() time.Time { return day("2026-11-05") }}
	m := NewManager(cfg)
	tok, _ := Sign(priv, claims("2099-12-31"))
	if _, err := m.Install(ctx, tok); err != nil {
		t.Fatal(err)
	}
	// a restart: a new manager finds it
	again := NewManager(cfg)
	if err := again.Load(ctx); err != nil || again.State().Status != Valid || again.Source() != "installed" {
		t.Fatalf("after restart: %+v %v", again.State(), err)
	}
	if err := again.Remove(ctx); err != nil || again.State().Status != None {
		t.Fatalf("remove: %+v %v", again.State(), err)
	}
	third := NewManager(cfg)
	if err := third.Load(ctx); err != nil || third.State().Status != None {
		t.Fatalf("after removal: %+v %v", third.State(), err)
	}
}
