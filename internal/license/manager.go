package license

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/behringer24/atrium-tablinum/internal/product"
)

// metaKey is the row in `meta` that holds a licence installed at run time.
const metaKey = "license"

// Config configures a Manager.
type Config struct {
	Anchors Anchors
	Line    product.Line
	// BaseURL is instance.base_url: the host a licence is bound to.
	BaseURL string
	// File is license.file from config.yaml, used when no licence is installed at run time.
	File string
	// Pool stores a licence installed at run time; nil keeps it in memory only.
	Pool *pgxpool.Pool
	// CommunityMaxUsers is the limit of active users without a licence that sets one; 0 = unlimited.
	CommunityMaxUsers int
	// Now is the clock; nil = time.Now. Only the optional expires date uses it.
	Now func() time.Time
}

// Manager holds the licence of the running instance. An instance admin installs or removes one at run
// time and the change applies at once.
type Manager struct {
	cfg Config

	mu     sync.RWMutex
	token  string
	source string // installed | file | ""
	claims *Claims
	bad    string // why a stored licence could not be used
}

func NewManager(cfg Config) *Manager {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	return &Manager{cfg: cfg}
}

// Load reads the licence: the one installed at run time if there is one, else license.file. A licence
// that cannot be used is reported by State, not by an error: the instance runs as Community.
func (m *Manager) Load(ctx context.Context) error {
	token, source := "", ""
	if m.cfg.Pool != nil {
		err := m.cfg.Pool.QueryRow(ctx, `SELECT value FROM meta WHERE key = $1`, metaKey).Scan(&token)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if err == nil {
			source = "installed"
		}
	}
	if token == "" && m.cfg.File != "" {
		b, err := os.ReadFile(m.cfg.File)
		if err != nil {
			m.set("", "file", nil, "the licence file cannot be read: "+err.Error())
			slog.Warn("Licence file not readable", "file", m.cfg.File, "error", err)
			return nil
		}
		token, source = strings.TrimSpace(string(b)), "file"
	}
	if token == "" {
		m.set("", "", nil, "")
		return nil
	}
	c, err := Verify(token, m.cfg.Anchors)
	if err != nil {
		m.set(token, source, nil, err.Error())
		slog.Warn("Licence not usable", "source", source, "error", err)
		return nil
	}
	m.set(token, source, &c, "")
	return nil
}

func (m *Manager) set(token, source string, c *Claims, bad string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.token, m.source, m.claims, m.bad = token, source, c, bad
}

// State evaluates the licence now.
func (m *Manager) State() State {
	m.mu.RLock()
	c, bad, token := m.claims, m.bad, m.token
	m.mu.RUnlock()
	if c == nil && (bad != "" || token != "") {
		return State{Status: Invalid, Edition: Community, Coverage: m.cfg.Line.Coverage(),
			Detail: "A licence is present but cannot be used: " + bad + "."}
	}
	return Evaluate(c, m.cfg.Line, m.cfg.BaseURL, m.cfg.Now())
}

// Source says where the licence in use comes from: "installed" (by an instance admin), "file"
// (license.file) or "".
func (m *Manager) Source() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.source
}

// Has reports whether a feature is switched on.
func (m *Manager) Has(feature string) bool { return m.State().Has(feature) }

// Limit returns a licensed limit (0 = unlimited) and whether the licence sets it.
func (m *Manager) Limit(name string) (int64, bool) { return m.State().Limit(name) }

// MaxUsers is the number of active users the instance may have: the licensed limit if a valid licence
// sets one, else the Community default. 0 = unlimited.
func (m *Manager) MaxUsers() int {
	if v, ok := m.Limit("users"); ok {
		return int(v)
	}
	return m.cfg.CommunityMaxUsers
}

// CommunityMaxUsers returns the configured Community limit.
func (m *Manager) CommunityMaxUsers() int { return m.cfg.CommunityMaxUsers }

// Install checks a licence and stores it. A licence that is unreadable, signed with an unknown key or
// bound to another domain is refused; one that is merely not covering this build is accepted and
// reported (so that an admin can see why it has no effect).
func (m *Manager) Install(ctx context.Context, token string) (State, error) {
	token = strings.TrimSpace(token)
	c, err := Verify(token, m.cfg.Anchors)
	if err != nil {
		return State{}, err
	}
	st := Evaluate(&c, m.cfg.Line, m.cfg.BaseURL, m.cfg.Now())
	if st.Status == WrongHost {
		return State{}, fmt.Errorf("%s", st.Detail)
	}
	if m.cfg.Pool != nil {
		if _, err := m.cfg.Pool.Exec(ctx, `INSERT INTO meta (key, value) VALUES ($1, $2)
			ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`, metaKey, token); err != nil {
			return State{}, err
		}
	}
	m.set(token, "installed", &c, "")
	return st, nil
}

// Remove deletes the licence installed at run time. A licence from license.file stays.
func (m *Manager) Remove(ctx context.Context) error {
	if m.cfg.Pool != nil {
		if _, err := m.cfg.Pool.Exec(ctx, `DELETE FROM meta WHERE key = $1`, metaKey); err != nil {
			return err
		}
	}
	return m.Load(ctx)
}
