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
	"github.com/behringer24/atrium-tablinum/internal/audit"
	"github.com/behringer24/atrium-tablinum/internal/authz"
	"github.com/behringer24/atrium-tablinum/internal/docs"
	"github.com/behringer24/atrium-tablinum/internal/edition"
	"github.com/behringer24/atrium-tablinum/internal/embed"
	"github.com/behringer24/atrium-tablinum/internal/guard"
	"github.com/behringer24/atrium-tablinum/internal/ingest"
	"github.com/behringer24/atrium-tablinum/internal/jobs"
	"github.com/behringer24/atrium-tablinum/internal/quota"
	"github.com/behringer24/atrium-tablinum/internal/reembed"
	"github.com/behringer24/atrium-tablinum/internal/search"
	"github.com/behringer24/atrium-tablinum/internal/share"
	"github.com/behringer24/atrium-tablinum/internal/skills"
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
	// SharingEnabled lists the tools that send an entry or a skill to another user (sharing.enabled).
	SharingEnabled bool
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
	// BaseURL is the public address of the instance; export links are built from it.
	BaseURL string
	// IconURL is the public address of the icon, sent to clients in the server info; empty = none.
	IconURL  string
	IconMIME string
}

// HealthCheck is one probe shown by admin_health.
type HealthCheck struct {
	Name  string
	Check func(ctx context.Context) (detail string, err error)
}

type Deps struct {
	Users  *users.Store
	Spaces *spaces.Store
	Docs   *docs.Store
	// Skills serves the skill tools; nil = no skill registry.
	Skills *skills.Store
	// Edition is the edition of the running build, shown by server_info; nil = Community.
	Edition edition.Running
	// Quota serves admin_quotas and the usage shown to space admins; nil = not managed.
	Quota   *quota.Store
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
	// Reembed serves admin_reembed; nil = not managed.
	Reembed *reembed.Service
	// SupportConfig is the part of the configuration admin_support_bundle shows
	// (config.SupportSummary: settings only, no secrets).
	SupportConfig map[string]any
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

	// exports signs the download links of export_knowledge; exportSlots bounds the exports
	// running at once.
	exports     *exportLinks
	exportSlots chan struct{}

	// deniedAt remembers when a refusal was last audited (see denied).
	deniedMu sync.Mutex
	deniedAt map[string]time.Time

	// idMu guards the names an instance admin can change at runtime (see identity.go) and the
	// tool descriptions rendered from them.
	idMu       sync.RWMutex
	short, org string            // current short name and organisation name
	desc       map[string]string // rendered descriptions
}

func New(d Deps, set Settings) *Server {
	s := &Server{Deps: d, set: set, limiter: newLimiter(set.WritesPerMinute, time.Minute), reads: newLimiter(set.ReadsPerMinute, time.Minute),
		exports: newExportLinks(), exportSlots: make(chan struct{}, maxParallelExports)}
	short, org := s.set.ShortName, s.set.OrganizationName
	if short == "" {
		short = s.set.ProductName
	}
	s.short, s.org = short, org
	s.desc = s.renderDescriptions(short, org)
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
	})(s.skillsRPC(h))
}

// Cache hints (ttlMs and cacheScope, protocol 2026-07-28) of every answer that carries them. What
// the server tells a caller depends on who asks: the tool list and the instructions follow the roles,
// skill files and lists follow the space rights. So nothing may be shared between callers
// ("private"), and nothing is fresh for any time ("ttlMs" 0), because a change of rights has to show
// at the next request. The SDK default would be "public".
const (
	cacheScopePrivate = "private"
	cacheTTLMs        = 0
)

// privateCacheable is the SetCacheable hook of every per-caller MCP server.
func privateCacheable(_ context.Context, _ mcp.Request, c *mcp.Cacheable) {
	c.TTLMs, c.CacheScope = cacheTTLMs, cacheScopePrivate
}

// instructions are the server-level hints a client shows its model. Users mostly say the
// short name, so the model has to connect it with this server.
func (s *Server) instructions() string {
	short, org := s.names()
	name := s.set.ProductName
	if short != "" && short != name {
		name += " (short: " + short + ")"
	}
	if org != "" {
		name += " of " + org
	}
	return "This is the central knowledge store " + name + ". When the user mentions \"" + short +
		"\", this store is meant. Only save something when the user explicitly asks for it. " +
		"After saving, always tell the user which space the entry is in. " +
		untrustedHint
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
		slog.Error("Roles for tool list could not be determined", "error", err)
		return nil
	}
	a := docs.Actor{User: u, Roles: roles}
	caps := &mcp.ServerCapabilities{Logging: &mcp.LoggingCapabilities{}} // the SDK default
	if s.Skills != nil {
		caps.AddExtension(skillsExtension, nil)
	}
	ms := mcp.NewServer(&mcp.Implementation{Name: s.set.ProductName, Icons: s.icons()}, &mcp.ServerOptions{Instructions: s.instructionsFor(r.Context(), a), Capabilities: caps, SetCacheable: privateCacheable})
	s.registerUserTools(ms)
	if s.Skills != nil {
		s.registerSkillResources(ms)
	}
	// The curator and space admin tools are listed for everyone: a client reads the list once and
	// keeps it, so a user who is made curator or space admin later would otherwise not see the tools
	// until the connector is reconnected. Whoever does not hold the role yet gets the compact form
	// (see compactFor); the role is checked on every call either way.
	cur, err := s.teamSpacesAtLeast(r.Context(), a, authz.Curator)
	s.registerCuratorTools(ms, err == nil && len(cur) > 0)
	admin, err := s.adminSpaces(r.Context(), a)
	s.registerSpaceAdminTools(ms, err == nil && len(admin) > 0)
	if u.InstanceAdmin {
		s.registerInstanceAdminTools(ms)
	}
	return ms
}

// userFromToken resolves the authenticated user live, so disabling takes effect at once.
func (s *Server) userFromToken(ctx context.Context, ti *auth.TokenInfo) (users.User, error) {
	if ti == nil {
		return users.User{}, errors.New("not logged in")
	}
	id, err := uuid.Parse(ti.UserID)
	if err != nil {
		return users.User{}, errors.New("not logged in")
	}
	u, err := s.Users.Get(ctx, id)
	if err != nil {
		return users.User{}, errors.New("not logged in")
	}
	if !u.Active {
		return users.User{}, errors.New("account disabled")
	}
	return u, nil
}

// call is what a tool function receives after the wrapper authenticated the caller.
type call struct {
	Actor docs.Actor
}

// addTool registers a tool behind the central wrapper: authenticate, compute roles,
// rate-limit writes, run, translate errors into messages the model can act on.
func addTool[In, Out any](s *Server, ms *mcp.Server, name, title string, min tier, write bool, fn func(context.Context, *call, In) (Out, error), opts ...toolOpt) {
	ann := &mcp.ToolAnnotations{Title: title, ReadOnlyHint: !write}
	tool := &mcp.Tool{Name: name, Description: s.description(name), Annotations: ann}
	if in := portableSchema[In](); in != nil {
		tool.InputSchema = in
	}
	if out := portableSchema[Out](); out != nil {
		tool.OutputSchema = out
	}
	for _, o := range opts {
		o(tool)
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
				slog.Error("Roles could not be determined", "tool", name, "error", err)
				return nil, zero, errors.New("internal error")
			}
			client := clientName(req)
			if !s.allowed(ctx, min, docs.Actor{User: u, Roles: roles}) {
				s.denied(ctx, u, client, name, "no permission for this tool")
				return nil, zero, errNoPermission
			}
			if write && !s.limiter.allow(u.ID) {
				s.denied(ctx, u, client, name, "write rate limit")
				return nil, zero, errors.New("too many write operations in a short time, please try again in a minute")
			}
			if !write && !s.reads.allow(u.ID) {
				s.denied(ctx, u, client, name, "read rate limit")
				return nil, zero, errors.New("too many requests in a short time, please try again in a minute")
			}
			out, err := fn(ctx, &call{Actor: docs.Actor{User: u, Roles: roles, Client: client}}, in)
			if err != nil {
				if reason := denialReason(err); reason != "" {
					s.denied(ctx, u, client, name, reason)
				}
				return nil, zero, translate(name, err)
			}
			return nil, out, nil
		})
}

var errNoPermission = errors.New("you do not have permission for this")

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

// translate turns internal errors into short English messages; unknown errors are
// logged and replaced so nothing internal reaches the model.
func translate(tool string, err error) error {
	var ge *guard.Error
	switch {
	case errors.As(err, &ge):
		return err
	case errors.Is(err, docs.ErrNotFound), errors.Is(err, spaces.ErrNotFound), errors.Is(err, users.ErrNotFound), errors.Is(err, skills.ErrNotFound):
		return errors.New("not found")
	case errors.Is(err, docs.ErrForbidden), errors.Is(err, docs.ErrQuota), errors.Is(err, docs.ErrConflict),
		errors.Is(err, docs.ErrNeedsApproval), errors.Is(err, docs.ErrOwnDraft), errors.Is(err, docs.ErrRule), errors.Is(err, ingest.ErrInvalid), errors.Is(err, ingest.ErrTooLarge),
		errors.Is(err, search.ErrInvalid), errors.Is(err, errUser), errors.Is(err, errNoPermission),
		errors.Is(err, spaces.ErrInvalid), errors.Is(err, spaces.ErrSlugTaken), errors.Is(err, spaces.ErrNotTeam),
		errors.Is(err, users.ErrGroupExists), errors.Is(err, users.ErrGroupReadOnly), errors.Is(err, users.ErrBootstrap),
		errors.Is(err, skills.ErrForbidden), errors.Is(err, skills.ErrInvalid), errors.Is(err, skills.ErrState), errors.Is(err, skills.ErrOwnVersion), errors.Is(err, skills.ErrTampered), errors.Is(err, skills.ErrArchived), errors.Is(err, skills.ErrQuota), errors.Is(err, skills.ErrChanged), errors.Is(err, skills.ErrNameTaken), errors.Is(err, users.ErrUserLimit),
		errors.Is(err, share.ErrDisabled), errors.Is(err, share.ErrSelf), errors.Is(err, share.ErrUndeliverable):
		return err
	}
	slog.Error("Tool error", "tool", tool, "error", err)
	if errors.Is(err, embed.ErrModelMismatch) {
		return errors.New("the embedding service serves another model than configured, so entries cannot be saved right now; " +
			"tell an instance admin, admin_health shows the cause")
	}
	return errors.New("internal error")
}

// errUser marks messages written for the user/model (not internal failures).
var errUser = errors.New("input")

func userErr(msg string) error { return errors.Join(errUser, errors.New(msg)) }

// clientName maps the MCP client's self-reported name to the audit/source label.
func clientName(req *mcp.CallToolRequest) string {
	if req == nil {
		return "api"
	}
	return clientLabel(req.Session)
}

func clientLabel(ss *mcp.ServerSession) string {
	if ss == nil {
		return "api"
	}
	p := ss.InitializeParams()
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

// renderDescriptions renders every tool description for the given names.
func (s *Server) renderDescriptions(short, org string) map[string]string {
	data := struct {
		ProductName, ShortName, OrganizationName string
		HandoffTTLDays                           int
	}{s.set.ProductName, short, org, s.set.HandoffTTLDays}
	out := map[string]string{}
	defaults := map[string]string{}
	for name, def := range defaultDescriptions {
		defaults[name] = def
	}
	for name, def := range editionDescriptions {
		defaults[name] = def
	}
	for name, def := range defaults {
		text := def
		if o, ok := s.set.DescriptionOverrides[name]; ok {
			text = o
		}
		rendered, err := renderTemplate(name, text, data)
		if err != nil {
			slog.Warn("Tool description invalid, using default", "tool", name, "error", err)
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

// denialReason names the refusals that belong in the audit log: a caller who tried something their
// role does not allow. A missing item is not one (it may simply not exist), nor is a wrong input.
func denialReason(err error) string {
	switch {
	case errors.Is(err, docs.ErrForbidden), errors.Is(err, skills.ErrForbidden):
		return "role does not allow this"
	case errors.Is(err, docs.ErrNeedsApproval):
		return "approval required"
	case errors.Is(err, docs.ErrRule):
		return "binding rules are for curators"
	case errors.Is(err, docs.ErrOwnDraft), errors.Is(err, skills.ErrOwnVersion):
		return "own version cannot be approved"
	case errors.Is(err, errNoPermission):
		return "no permission for this tool"
	}
	return ""
}

// denied records a refused attempt in the audit log (action denied, with the tool and the reason, no
// arguments), at most once a minute per user, tool and reason so that hammering a tool cannot flood
// the log. This is what makes probing visible.
func (s *Server) denied(ctx context.Context, u users.User, client, tool, reason string) {
	if s.Pool == nil {
		return
	}
	key := u.ID.String() + "|" + tool + "|" + reason
	s.deniedMu.Lock()
	if last, ok := s.deniedAt[key]; ok && time.Since(last) < time.Minute {
		s.deniedMu.Unlock()
		return
	}
	if s.deniedAt == nil || len(s.deniedAt) > 10000 {
		s.deniedAt = map[string]time.Time{}
	}
	s.deniedAt[key] = time.Now()
	s.deniedMu.Unlock()
	uid := u.ID
	if err := audit.Insert(ctx, s.Pool, audit.Event{UserID: &uid, Client: client, Action: "denied",
		Details: map[string]any{"tool": tool, "reason": reason}}); err != nil {
		slog.Error("Audit entry for a denied attempt failed", "tool", tool, "error", err)
	}
}
