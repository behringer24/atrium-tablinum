package edition

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
)

func TestCommunityDefault(t *testing.T) {
	r, err := Start(context.Background(), Env{})
	if err != nil {
		t.Fatal(err)
	}
	if r.Name() != Community || r.MaxUsers() != CommunityMaxUsers || CommunityMaxUsers != 8 {
		t.Errorf("community: %q, %d", r.Name(), r.MaxUsers())
	}
}

// A "license" section does not stop the start: it is ignored, and the log says so.
func TestCommunityIgnoresLicenseSection(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, nil))
	r, err := Start(context.Background(), Env{HasSection: true, Log: log})
	if err != nil || r.Name() != Community || r.MaxUsers() != CommunityMaxUsers {
		t.Fatalf("a license section must be ignored: %v %v", r, err)
	}
	if out := buf.String(); !strings.Contains(out, "level=WARN") || !strings.Contains(out, "license") || !strings.Contains(out, "ignored") {
		t.Errorf("the log must say that the section is ignored, got %q", out)
	}
	buf.Reset()
	if _, err := Start(context.Background(), Env{Log: log}); err != nil || buf.Len() != 0 {
		t.Errorf("without a section nothing is logged: %v %q", err, buf.String())
	}
}

func TestRegisterReplacesDefault(t *testing.T) {
	old := start
	defer func() { start = old }()
	Register(func(context.Context, Env) (Running, error) { return fake{}, nil })
	r, err := Start(context.Background(), Env{HasSection: true})
	if err != nil || r.Name() != "pro" || r.MaxUsers() != 50 {
		t.Errorf("registered edition not used: %v %v", r, err)
	}
}

type fake struct{}

func (fake) Name() string  { return "pro" }
func (fake) MaxUsers() int { return 50 }
