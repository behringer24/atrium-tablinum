// Package edition is the extension point of the build: it names the edition in effect and the number
// of active user accounts it is for.
//
// This tree is complete and tested on its own: every hook has a default that needs nothing else.
// Keep the hooks few.
package edition

import (
	"context"
	"errors"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	// Community is the name of the edition of this build.
	Community = "community"

	// CommunityMaxUsers is the number of active user accounts the Community edition is for.
	CommunityMaxUsers = 8
)

// Env is what the server hands to the edition at start-up.
type Env struct {
	Pool    *pgxpool.Pool
	BaseURL string // instance.base_url
	Log     *slog.Logger
	// HasSection says whether config.yaml has a "license" section; Section decodes it into a struct.
	// The section is opaque here.
	HasSection bool
	Section    func(into any) error
}

// Running is the edition of the running instance.
type Running interface {
	// Name is the edition in effect, shown by server_info.
	Name() string
	// MaxUsers is the number of active user accounts the instance may have; 0 = unlimited. It is
	// asked whenever a new account would be created, so a change applies at once.
	MaxUsers() int
}

// Prober is optionally implemented by an edition to say more in admin_health than its name.
type Prober interface {
	Probe(ctx context.Context) (detail string, err error)
}

// Probe is the detail of the "edition" check of admin_health.
func Probe(ctx context.Context, r Running) (string, error) {
	if p, ok := r.(Prober); ok {
		return p.Probe(ctx)
	}
	return r.Name() + " edition", nil
}

// StartFunc starts an edition once the database is ready.
type StartFunc func(ctx context.Context, env Env) (Running, error)

var start StartFunc = startCommunity

// Register replaces the default edition. It is meant to be called from init().
func Register(f StartFunc) { start = f }

// Start starts the edition of this build.
func Start(ctx context.Context, env Env) (Running, error) { return start(ctx, env) }

type community struct{}

func (community) Name() string  { return Community }
func (community) MaxUsers() int { return CommunityMaxUsers }

func startCommunity(_ context.Context, env Env) (Running, error) {
	if env.HasSection {
		return nil, errors.New("config: the \"license\" section is not supported by this build (remove the section)")
	}
	return community{}, nil
}
