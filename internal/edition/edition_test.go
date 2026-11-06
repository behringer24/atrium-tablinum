package edition

import (
	"context"
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

func TestCommunityRefusesLicenseSection(t *testing.T) {
	_, err := Start(context.Background(), Env{HasSection: true})
	if err == nil || !strings.Contains(err.Error(), "license") || !strings.Contains(err.Error(), "Community") {
		t.Errorf("a license section must be refused with an explanation, got %v", err)
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
