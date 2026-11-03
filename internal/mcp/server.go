// Package mcpserver wires the MCP endpoint: per-session tool sets by role, a central
// wrapper that authenticates and authorizes every call, and the tool implementations.
package mcpserver

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"text/template"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/behringer24/atrium-tablinum/internal/allowlist"
	"github.com/behringer24/atrium-tablinum/internal/authz"
	"github.com/behringer24/atrium-tablinum/internal/docs"
	"github.com/behringer24/atrium-tablinum/internal/guard"
	"github.com/behringer24/atrium-tablinum/internal/ingest"
	"github.com/behringer24/atrium-tablinum/internal/jobs"
	"github.com/behringer24/atrium-tablinum/internal/search"
	"github.com/behringer24/atrium-tablinum/internal/spaces"
	"github.com/behringer24/atrium-tablinum/internal/users"
)

// Settings carries the configuration values the tools need.
type Settings struct {
	ProductName string
	// ShortName is how users usually call the product in a chat ("Atrium").
	ShortName        string
	OrganizationName string
	HandoffTTLDays   int
	// PersonalRetentionDays is how long the personal space of a disabled account is kept
	// before it is purged; 0 = never.
	PersonalRetentionDays int
	// DescriptionOverrides replaces default tool descriptions (Go templates).
	DescriptionOverrides map[string]string
	// DefaultSpaceByGroup maps a group name to the slug of its default target space.
	DefaultSpaceByGroup map[string]string
	WritesPerMinute     int
	// ReadsPerMinute limits search, read and list calls per user; 0 = unlimited.
	ReadsPerMinute int
	// DisableHostProtection turns off the SDK's DNS-rebinding guard, which rejects a
	// non-local Host header on loopback connections. Needed when a reverse proxy on the
	// same host forwards to 127.0.0.1.
	DisableHostProtection bool
}

// HealthCheck is one probe shown by admin_health.
type HealthCheck struct {
	Name  string
	Check func(ctx context.Context) (detail string, err error)
}

type Deps struct {
	Users   *users.Store
	Spaces  *spaces.Store
	Docs    *docs.Store
	Search  *search.Service
	Checker *authz.Checker
	// Pool serves audit reads/writes of the admin tools.
	Pool *pgxpool.Pool
	// RevokeTokens revokes all OAuth tokens of a user (offboarding); nil without OAuth.
	RevokeTokens func(ctx context.Context, user uuid.UUID) (int64, error)
	// Allowlist and LoginProviders serve admin_allowlist; Allowlist nil = not managed.
	Allowlist      *allowlist.Store
	LoginProviders []LoginProvider
	// ListSessions and RevokeClient serve my_sessions and revoke_my_sessions; nil without OAuth.
	ListSessions func(ctx context.Context, user uuid.UUID) ([]SessionInfo, error)
	RevokeClient func(ctx context.Context, user uuid.UUID, clientID string) (int64, error)
	// Jobs and Health feed admin_health; both may be nil.
	Jobs   *jobs.Runner
	Health []HealthCheck
	// Embedding describes the configured embedding model for admin_health.
	EmbeddingModel      string
	EmbeddingDimensions int
}

// tier is the least role needed to see and call a tool.
type tier int

const (
	tierUser       tier = iota
	tierCurator         // curator of at least one team space (or instance admin)
	tierSpaceAdmin      // admin of at least one team space (or instance admin)
	tierInstanceAdmin
)

type Server struct {
	Deps
	set     Settings
	limiter *limiter
	reads   *limiter
	desc    map[string]string // rendered descriptions
}

func New(d Deps, set Settings) *Server {
	s := &Server{Deps: d, set: set, limiter: newLimiter(set.WritesPerMinute, time.Minute), reads: newLimiter(set.ReadsPerMinute, time.Minute)}
	s.desc = s.renderDescriptions()
	return s
}

// Handler returns the HTTP handler for the /mcp endpoint, protected by bearer-token
// authentication via verifier.
func (s *Server) Handler(verifier auth.TokenVerifier, resourceMetadataURL string) http.Handler {
	h := mcp.NewStreamableHTTPHandler(s.serverFor, &mcp.StreamableHTTPOptions{
		// Atrium keeps no state between requests: every tool call re-checks the caller, and
		// nothing is pushed to clients. Stateless mode accepts clients of the older protocol
		// versions (initialize, then calls) as well as the sessionless one from 2026-07-28 on,
		// which a stateful server rejects. It also avoids trouble with clients that do not keep
		// the Mcp-Session-Id between calls.
		Stateless: true,
		// Without a logger the SDK drops its warnings, e.g. about rejected requests.
		Logger:                     slog.Default(),
		DisableLocalhostProtection: s.set.DisableHostProtection,
	})
	return auth.RequireBearerToken(verifier, &auth.RequireBearerTokenOptions{
		ResourceMetadataURL:    resourceMetadataURL,
		AllowMissingExpiration: true,
	})(h)
}

// instructions are the server-level hints a client shows its model. Users mostly say the
// short name, so the model has to connect it with this server.
func (s *Server) instructions() string {
	name := s.set.ProductName
	if s.set.ShortName != "" && s.set.ShortName != name {
		name += " (kurz: " + s.set.ShortName + ")"
	}
	return "Dies ist der zentrale Wissensspeicher " + name + ". Spricht der Nutzer von „" + s.shortName() +
		"“, ist dieser Speicher gemeint. Speichere nur dann etwas, wenn der Nutzer es ausdrücklich verlangt. " +
		"Nenne dem Nutzer nach dem Speichern immer, in welchem Bereich der Eintrag liegt. " +
		untrustedHint
}

// shortName falls back to the product name when no short form is configured.
func (s *Server) shortName() string {
	if s.set.ShortName != "" {
		return s.set.ShortName
	}
	return s.set.ProductName
}

// serverFor builds the tool set for one new session from the authenticated caller. The
// list is a convenience for the client; every handler re-checks permissions.
func (s *Server) serverFor(r *http.Request) *mcp.Server {
	u, err := s.userFromToken(r.Context(), auth.TokenInfoFromContext(r.Context()))
	if err != nil {
		return nil
	}
	roles, err := s.Checker.EffectiveRoles(r.Context(), u)
	if err != nil {
		slog.Error("Rollen für Tool-Liste nicht ermittelbar", "error", err)
		return nil
	}
	a := docs.Actor{User: u, Roles: roles}
	ms := mcp.NewServer(&mcp.Implementation{Name: s.set.ProductName}, &mcp.ServerOptions{Instructions: s.instructionsFor(r.Context(), a)})
	s.registerUserTools(ms)
	if cur, err := s.teamSpacesAtLeast(r.Context(), a, authz.Curator); err == nil && len(cur) > 0 {
		s.registerCuratorTools(ms)
	}
	if admin, err := s.adminSpaces(r.Context(), a); err == nil && len(admin) > 0 {
		s.registerSpaceAdminTools(ms)
	}
	if u.InstanceAdmin {
		s.registerInstanceAdminTools(ms)
	}
	return ms
}

// userFromToken resolves the authenticated user live, so disabling takes effect at once.
func (s *Server) userFromToken(ctx context.Context, ti *auth.TokenInfo) (users.User, error) {
	if ti == nil {
		return users.User{}, errors.New("nicht angemeldet")
	}
	id, err := uuid.Parse(ti.UserID)
	if err != nil {
		return users.User{}, errors.New("nicht angemeldet")
	}
	u, err := s.Users.Get(ctx, id)
	if err != nil {
		return users.User{}, errors.New("nicht angemeldet")
	}
	if !u.Active {
		return users.User{}, errors.New("konto deaktiviert")
	}
	return u, nil
}

// call is what a tool function receives after the wrapper authenticated the caller.
type call struct {
	Actor docs.Actor
}

// addTool registers a tool behind the central wrapper: authenticate, compute roles,
// rate-limit writes, run, translate errors into messages the model can act on.
func addTool[In, Out any](s *Server, ms *mcp.Server, name, title string, min tier, write bool, fn func(context.Context, *call, In) (Out, error)) {
	ann := &mcp.ToolAnnotations{Title: title, ReadOnlyHint: !write}
	tool := &mcp.Tool{Name: name, Description: s.desc[name], Annotations: ann}
	if in := portableSchema[In](); in != nil {
		tool.InputSchema = in
	}
	if out := portableSchema[Out](); out != nil {
		tool.OutputSchema = out
	}
	mcp.AddTool(ms, tool,
		func(ctx context.Context, req *mcp.CallToolRequest, in In) (*mcp.CallToolResult, Out, error) {
			var zero Out
			var ti *auth.TokenInfo
			if req.Extra != nil {
				ti = req.Extra.TokenInfo
			}
			u, err := s.userFromToken(ctx, ti)
			if err != nil {
				return nil, zero, err
			}
			roles, err := s.Checker.EffectiveRoles(ctx, u)
			if err != nil {
				slog.Error("Rollen nicht ermittelbar", "tool", name, "error", err)
				return nil, zero, errors.New("interner Fehler")
			}
			if !s.allowed(ctx, min, docs.Actor{User: u, Roles: roles}) {
				return nil, zero, errNoPermission
			}
			if write && !s.limiter.allow(u.ID) {
				return nil, zero, errors.New("zu viele Schreibvorgänge in kurzer Zeit, bitte in einer Minute erneut versuchen")
			}
			if !write && !s.reads.allow(u.ID) {
				return nil, zero, errors.New("zu viele Abfragen in kurzer Zeit, bitte in einer Minute erneut versuchen")
			}
			out, err := fn(ctx, &call{Actor: docs.Actor{User: u, Roles: roles, Client: clientName(req)}}, in)
			if err != nil {
				return nil, zero, translate(name, err)
			}
			return nil, out, nil
		})
}

var errNoPermission = errors.New("dazu fehlt die Berechtigung")

// allowed re-checks the tool tier on every call; the tool list is only a convenience.
func (s *Server) allowed(ctx context.Context, min tier, a docs.Actor) bool {
	switch min {
	case tierInstanceAdmin:
		return a.User.InstanceAdmin
	case tierCurator:
		cur, err := s.teamSpacesAtLeast(ctx, a, authz.Curator)
		return err == nil && len(cur) > 0
	case tierSpaceAdmin:
		admin, err := s.adminSpaces(ctx, a)
		return err == nil && len(admin) > 0
	}
	return true
}

// adminSpaces lists the team spaces the actor administers (all of them for instance
// admins). Personal spaces never count: their owner is admin there by construction.
func (s *Server) adminSpaces(ctx context.Context, a docs.Actor) ([]spaces.Space, error) {
	return s.teamSpacesAtLeast(ctx, a, authz.Admin)
}

// teamSpacesAtLeast lists the team spaces where the actor holds at least the given role.
func (s *Server) teamSpacesAtLeast(ctx context.Context, a docs.Actor, min authz.Role) ([]spaces.Space, error) {
	all, err := s.spacesAtLeast(ctx, a, min)
	if err != nil {
		return nil, err
	}
	out := all[:0]
	for _, sp := range all {
		if sp.Type == "team" {
			out = append(out, sp)
		}
	}
	return out, nil
}

// translate turns internal errors into short German messages; unknown errors are
// logged and replaced so nothing internal reaches the model.
func translate(tool string, err error) error {
	var ge *guard.Error
	switch {
	case errors.As(err, &ge):
		return err
	case errors.Is(err, docs.ErrNotFound), errors.Is(err, spaces.ErrNotFound), errors.Is(err, users.ErrNotFound):
		return errors.New("nicht gefunden")
	case errors.Is(err, docs.ErrForbidden), errors.Is(err, docs.ErrQuota), errors.Is(err, docs.ErrConflict),
		errors.Is(err, docs.ErrNeedsApproval), errors.Is(err, docs.ErrOwnDraft), errors.Is(err, ingest.ErrInvalid), errors.Is(err, ingest.ErrTooLarge),
		errors.Is(err, search.ErrInvalid), errors.Is(err, errUser), errors.Is(err, errNoPermission),
		errors.Is(err, spaces.ErrInvalid), errors.Is(err, spaces.ErrSlugTaken), errors.Is(err, spaces.ErrNotTeam),
		errors.Is(err, users.ErrGroupExists), errors.Is(err, users.ErrGroupReadOnly), errors.Is(err, users.ErrBootstrap):
		return err
	}
	slog.Error("Tool-Fehler", "tool", tool, "error", err)
	return errors.New("interner Fehler")
}

// errUser marks messages written for the user/model (not internal failures).
var errUser = errors.New("eingabe")

func userErr(msg string) error { return errors.Join(errUser, errors.New(msg)) }

// clientName maps the MCP client's self-reported name to the audit/source label.
func clientName(req *mcp.CallToolRequest) string {
	if req == nil || req.Session == nil {
		return "api"
	}
	p := req.Session.InitializeParams()
	if p == nil || p.ClientInfo == nil {
		return "api"
	}
	n := strings.ToLower(p.ClientInfo.Name)
	switch {
	case strings.Contains(n, "claude-code"), strings.Contains(n, "claude code"):
		return "claude-code"
	case strings.Contains(n, "claude"):
		return "claude"
	case strings.Contains(n, "openai"), strings.Contains(n, "chatgpt"):
		return "chatgpt"
	}
	return "api"
}

// limiter is a per-user sliding-window rate limiter for write tools.
type limiter struct {
	mu     sync.Mutex
	limit  int
	window time.Duration
	hits   map[uuid.UUID][]time.Time
}

func newLimiter(limit int, window time.Duration) *limiter {
	return &limiter{limit: limit, window: window, hits: map[uuid.UUID][]time.Time{}}
}

func (l *limiter) allow(id uuid.UUID) bool {
	if l.limit <= 0 {
		return true
	}
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()
	recent := l.hits[id][:0]
	for _, t := range l.hits[id] {
		if now.Sub(t) < l.window {
			recent = append(recent, t)
		}
	}
	if len(recent) >= l.limit {
		l.hits[id] = recent
		return false
	}
	l.hits[id] = append(recent, now)
	return true
}

func (s *Server) renderDescriptions() map[string]string {
	data := struct {
		ProductName, ShortName, OrganizationName string
		HandoffTTLDays                           int
	}{s.set.ProductName, s.shortName(), s.set.OrganizationName, s.set.HandoffTTLDays}
	out := map[string]string{}
	for name, def := range defaultDescriptions {
		text := def
		if o, ok := s.set.DescriptionOverrides[name]; ok {
			text = o
		}
		rendered, err := renderTemplate(name, text, data)
		if err != nil {
			slog.Warn("Tool-Beschreibung ungültig, Standard wird verwendet", "tool", name, "error", err)
			rendered, _ = renderTemplate(name, def, data)
		}
		out[name] = rendered
	}
	return out
}

func renderTemplate(name, text string, data any) (string, error) {
	t, err := template.New(name).Option("missingkey=error").Parse(text)
	if err != nil {
		return "", err
	}
	var b bytes.Buffer
	if err := t.Execute(&b, data); err != nil {
		return "", err
	}
	return b.String(), nil
}
