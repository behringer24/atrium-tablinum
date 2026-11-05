package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/behringer24/atrium-tablinum/internal/license"
)

// capture runs f and returns what it wrote to stdout.
func capture(t *testing.T, f func() error) (string, error) {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	done := make(chan string)
	go func() { b, _ := io.ReadAll(r); done <- string(b) }()
	ferr := f()
	w.Close()
	os.Stdout = old
	return <-done, ferr
}

func TestGenesisIssueAndVerify(t *testing.T) {
	dir := t.TempDir()
	keys := filepath.Join(dir, "keys")
	if _, err := capture(t, func() error { return genesis([]string{"-out", keys, "-count", "5"}) }); err != nil {
		t.Fatal(err)
	}
	files, _ := filepath.Glob(filepath.Join(keys, "k*.key"))
	if len(files) != 5 {
		t.Fatalf("key files: %v", files)
	}
	anchorsText, err := os.ReadFile(filepath.Join(keys, "anchors.txt"))
	if err != nil {
		t.Fatal(err)
	}
	anchors, err := license.ParseAnchors(string(anchorsText))
	if err != nil || len(anchors) != 5 {
		t.Fatalf("anchors: %d %v", len(anchors), err)
	}
	// a second run into the same directory is refused: it would overwrite anchors.txt
	if _, err := capture(t, func() error { return genesis([]string{"-out", keys, "-count", "1"}) }); err == nil {
		t.Fatal("genesis wrote a second set into a directory that holds keys")
	}

	register := filepath.Join(dir, "issued.jsonl")
	token, err := capture(t, func() error {
		return issue([]string{"-key", files[0], "-licensee", "Example GmbH", "-domain", "Atrium.Example.com", "-months", "12",
			"-features", "scim, group_sync", "-limit", "users=50", "-register", register})
	})
	if err != nil {
		t.Fatal(err)
	}
	token = strings.TrimSpace(token)
	c, err := license.Verify(token, anchors)
	if err != nil || c.Domain != "atrium.example.com" || c.Limits["users"] != 50 || len(c.Features) != 2 || c.Edition != "pro" {
		t.Fatalf("issued: %+v %v", c, err)
	}
	if b, err := os.ReadFile(register); err != nil || !strings.Contains(string(b), c.ID) {
		t.Errorf("register: %q %v", b, err)
	}
	// lifetime and an explicit date
	for _, args := range [][]string{{"-lifetime"}, {"-until", "2030-06-30"}} {
		tok, err := capture(t, func() error {
			return issue(append([]string{"-key", files[1], "-licensee", "X", "-domain", "x.example.com"}, args...))
		})
		if err != nil {
			t.Fatal(err)
		}
		if c, err := license.Verify(strings.TrimSpace(tok), anchors); err != nil || (args[0] == "-lifetime" && c.MaintenanceUntil != "2099-12-31") {
			t.Errorf("%v: %+v %v", args, c, err)
		}
	}

	// verify says OK for the real token and fails for a changed one
	out, err := capture(t, func() error {
		return verify([]string{"-anchors", filepath.Join(keys, "anchors.txt"), "-base-url", "https://atrium.example.com", token})
	})
	if err != nil || !strings.Contains(out, "signature OK") || !strings.Contains(out, "Example GmbH") {
		t.Errorf("verify: %q %v", out, err)
	}
	if _, err := capture(t, func() error { return verify([]string{"-anchors", filepath.Join(keys, "anchors.txt"), token + "x"}) }); err == nil {
		t.Error("a changed licence verified")
	}
	// the embedded keys are not these test keys
	if _, err := capture(t, func() error { return verify([]string{token}) }); err == nil {
		t.Error("a licence signed with a foreign key verified against the embedded anchors")
	}

	// input errors
	for name, args := range map[string][]string{
		"no maintenance": {"-key", files[0], "-licensee", "X", "-domain", "x"},
		"bad limit":      {"-key", files[0], "-licensee", "X", "-domain", "x", "-months", "1", "-limit", "users"},
		"no key file":    {"-key", filepath.Join(dir, "missing.key"), "-licensee", "X", "-domain", "x", "-months", "1"},
	} {
		if _, err := capture(t, func() error { return issue(args) }); err == nil {
			t.Errorf("issue accepted: %s", name)
		}
	}
}

// Private keys must never be written into a repository.
func TestGenesisRefusesAGitWorkingTree(t *testing.T) {
	target := filepath.Join("keys-must-not-exist")
	if _, err := capture(t, func() error { return genesis([]string{"-out", target}) }); err == nil {
		os.RemoveAll(target)
		t.Fatal("genesis wrote keys inside the repository")
	}
	if _, err := os.Stat(target); err == nil {
		os.RemoveAll(target)
		t.Fatal("the directory was created")
	}
}
