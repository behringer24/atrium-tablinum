package oauth

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"html/template"
	"log/slog"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"golang.org/x/oauth2"

	"github.com/behringer24/atrium-tablinum/internal/httpx"
	"github.com/behringer24/atrium-tablinum/internal/idp"
	"github.com/behringer24/atrium-tablinum/internal/users"
)

// ResourcePath is the path of the protected MCP endpoint.
const ResourcePath = "/mcp"

type Config struct {
	// BaseURL is the public https address; it is the issuer and the base of all endpoints.
	BaseURL         string
	ProductName     string
	AllowedRedirect []string
	AllowLoopback   bool
	TrustedProxies  []netip.Prefix
}

type Server struct {
	cfg       Config
	store     *Store
	users     *users.Store
	providers map[string]idp.Provider
	order     []string
	resource  string
	log       *slog.Logger
	regLimit  *httpx.Limiter
	authLimit *httpx.Limiter
	// tokenLimit bounds /oauth/token and /oauth/revoke per client address.
	tokenLimit *httpx.Limiter
}

func NewServer(cfg Config, store *Store, us *users.Store, providers []idp.Provider) *Server {
	cfg.BaseURL = strings.TrimRight(cfg.BaseURL, "/")
	s := &Server{
		cfg: cfg, store: store, users: us, providers: map[string]idp.Provider{},
		resource: cfg.BaseURL + ResourcePath, log: slog.Default(),
		regLimit: httpx.NewLimiter(20, time.Minute), authLimit: httpx.NewLimiter(60, time.Minute),
		tokenLimit: httpx.NewLimiter(300, time.Minute),
	}
	for _, p := range providers {
		s.providers[p.ID()] = p
		s.order = append(s.order, p.ID())
	}
	return s
}

// Resource is the canonical resource identifier tokens are bound to.
func (s *Server) Resource() string { return s.resource }

// ResourceMetadataURL is where clients find the protected resource metadata (RFC 9728).
func (s *Server) ResourceMetadataURL() string {
	return s.cfg.BaseURL + "/.well-known/oauth-protected-resource" + ResourcePath
}

// Register mounts all OAuth endpoints.
func (s *Server) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /.well-known/oauth-protected-resource", s.protectedResource)
	mux.HandleFunc("GET /.well-known/oauth-protected-resource"+ResourcePath, s.protectedResource)
	mux.HandleFunc("GET /.well-known/oauth-authorization-server", s.authServerMetadata)
	mux.HandleFunc("POST /oauth/register", s.register)
	mux.HandleFunc("GET /oauth/authorize", s.authorize)
	mux.HandleFunc("GET /oauth/login", s.login)
	mux.HandleFunc("GET /oauth/callback/{provider}", s.callback)
	mux.HandleFunc("POST /oauth/token", s.token)
	mux.HandleFunc("POST /oauth/revoke", s.revoke)
}

// acceptedResource tells whether a client-supplied resource parameter names this server.
// Both the bare origin and the /mcp URL are accepted, with or without trailing slash.
func (s *Server) acceptedResource(v string) bool {
	v = strings.TrimRight(v, "/")
	return v == s.cfg.BaseURL || v == s.resource
}

// ---- redirect URI policy ---------------------------------------------------------------

func isLoopbackHost(h string) bool {
	return h == "localhost" || h == "127.0.0.1" || h == "[::1]" || h == "::1"
}

// AllowsRedirect applies the registration allowlist.
func (s *Server) AllowsRedirect(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.Fragment != "" || u.User != nil {
		return false
	}
	if u.Scheme == "http" && s.cfg.AllowLoopback && isLoopbackHost(u.Hostname()) {
		return true
	}
	if u.Scheme != "https" {
		return false
	}
	for _, pat := range s.cfg.AllowedRedirect {
		if prefix, wild := strings.CutSuffix(pat, "*"); wild {
			// The part before the star must end in a path separator and name the same host, so
			// that "https://example.com/*" cannot match "https://example.com.evil.net/".
			if pu, err := url.Parse(prefix); err == nil && strings.HasSuffix(prefix, "/") && strings.EqualFold(pu.Host, u.Host) && strings.HasPrefix(raw, prefix) {
				return true
			}
		} else if raw == pat {
			return true
		}
	}
	return false
}

// ---- metadata ------------------------------------------------------------------------------

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func cors(w http.ResponseWriter) { w.Header().Set("Access-Control-Allow-Origin", "*") }

func (s *Server) protectedResource(w http.ResponseWriter, _ *http.Request) {
	cors(w)
	writeJSON(w, http.StatusOK, map[string]any{
		"resource":                 s.resource,
		"authorization_servers":    []string{s.cfg.BaseURL},
		"bearer_methods_supported": []string{"header"},
		"resource_name":            s.cfg.ProductName,
	})
}

func (s *Server) authServerMetadata(w http.ResponseWriter, _ *http.Request) {
	cors(w)
	b := s.cfg.BaseURL
	writeJSON(w, http.StatusOK, map[string]any{
		"issuer":                                         b,
		"authorization_endpoint":                         b + "/oauth/authorize",
		"token_endpoint":                                 b + "/oauth/token",
		"registration_endpoint":                          b + "/oauth/register",
		"revocation_endpoint":                            b + "/oauth/revoke",
		"response_types_supported":                       []string{"code"},
		"grant_types_supported":                          []string{"authorization_code", "refresh_token"},
		"code_challenge_methods_supported":               []string{"S256"},
		"token_endpoint_auth_methods_supported":          []string{"none"},
		"revocation_endpoint_auth_methods_supported":     []string{"none"},
		"authorization_response_iss_parameter_supported": true,
	})
}

// ---- dynamic client registration (RFC 7591) -----------------------------------------------

type oauthError struct {
	Error       string `json:"error"`
	Description string `json:"error_description,omitempty"`
}

func tokenError(w http.ResponseWriter, status int, code, desc string) {
	writeJSON(w, status, oauthError{code, desc})
}

func (s *Server) clientIP(r *http.Request) string { return httpx.ClientIP(r, s.cfg.TrustedProxies) }

func (s *Server) register(w http.ResponseWriter, r *http.Request) {
	if !s.regLimit.Allow(s.clientIP(r)) {
		tokenError(w, http.StatusTooManyRequests, "temporarily_unavailable", "too many registrations")
		return
	}
	var req struct {
		RedirectURIs  []string `json:"redirect_uris"`
		ClientName    string   `json:"client_name"`
		GrantTypes    []string `json:"grant_types"`
		ResponseTypes []string `json:"response_types"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&req); err != nil {
		tokenError(w, http.StatusBadRequest, "invalid_client_metadata", "invalid JSON")
		return
	}
	if len(req.RedirectURIs) == 0 || len(req.RedirectURIs) > 10 {
		tokenError(w, http.StatusBadRequest, "invalid_redirect_uri", "redirect_uris: 1 to 10 entries required")
		return
	}
	for _, u := range req.RedirectURIs {
		if !s.AllowsRedirect(u) {
			tokenError(w, http.StatusBadRequest, "invalid_redirect_uri", "redirect_uri is not allowed for this instance")
			return
		}
	}
	for _, g := range req.GrantTypes {
		if g != "authorization_code" && g != "refresh_token" {
			tokenError(w, http.StatusBadRequest, "invalid_client_metadata", "grant_type not supported: "+g)
			return
		}
	}
	for _, t := range req.ResponseTypes {
		if t != "code" {
			tokenError(w, http.StatusBadRequest, "invalid_client_metadata", "response_type not supported: "+t)
			return
		}
	}
	name := strings.TrimSpace(req.ClientName)
	if len([]rune(name)) > 100 {
		name = string([]rune(name)[:100])
	}
	c, err := s.store.CreateClient(r.Context(), name, req.RedirectURIs)
	if errors.Is(err, ErrTooManyClients) {
		tokenError(w, http.StatusServiceUnavailable, "temporarily_unavailable", "registration currently not possible")
		return
	}
	if err != nil {
		s.log.Error("client registration", "error", err)
		tokenError(w, http.StatusInternalServerError, "server_error", "")
		return
	}
	// All clients are public (PKCE, no secret), whatever authentication method was asked for.
	writeJSON(w, http.StatusCreated, map[string]any{
		"client_id": c.ID, "client_id_issued_at": time.Now().Unix(), "client_name": name,
		"redirect_uris": c.RedirectURIs, "grant_types": []string{"authorization_code", "refresh_token"},
		"response_types": []string{"code"}, "token_endpoint_auth_method": "none",
	})
}

// ---- authorization endpoint ---------------------------------------------------------------

var chooserTmpl = template.Must(template.New("chooser").Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>{{.Product}} – Log in</title>
<style>body{font:16px system-ui,sans-serif;max-width:24rem;margin:15vh auto;padding:0 1rem}
a.b{display:block;margin:.6rem 0;padding:.75rem 1rem;border:1px solid #888;border-radius:.5rem;text-decoration:none;color:inherit;text-align:center}
a.b:hover{background:#8881}</style></head>
<body><h1>{{.Product}}</h1><p>How would you like to log in?</p>
{{range .Providers}}<a class="b" href="{{.URL}}">{{.Name}}</a>{{end}}</body></html>`))

var errorTmpl = template.Must(template.New("error").Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>{{.Product}}</title><style>body{font:16px system-ui,sans-serif;max-width:30rem;margin:15vh auto;padding:0 1rem}</style></head>
<body><h1>Login not possible</h1><p>{{.Message}}</p></body></html>`))

// errorPage is used when the redirect URI cannot be trusted, so nothing is redirected.
func (s *Server) errorPage(w http.ResponseWriter, status int, msg string) {
	htmlHeaders(w)
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = errorTmpl.Execute(w, map[string]string{"Product": s.cfg.ProductName, "Message": msg})
}

// redirectClient sends the user back to the client with the given parameters and the
// issuer (RFC 9207).
func (s *Server) redirectClient(w http.ResponseWriter, r *http.Request, redirectURI, state string, params map[string]string) {
	u, err := url.Parse(redirectURI)
	if err != nil {
		s.errorPage(w, http.StatusBadRequest, "Invalid redirect.")
		return
	}
	q := u.Query()
	for k, v := range params {
		q.Set(k, v)
	}
	if state != "" {
		q.Set("state", state)
	}
	q.Set("iss", s.cfg.BaseURL)
	u.RawQuery = q.Encode()
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, u.String(), http.StatusFound)
}

func (s *Server) authorize(w http.ResponseWriter, r *http.Request) {
	if !s.authLimit.Allow(s.clientIP(r)) {
		s.errorPage(w, http.StatusTooManyRequests, "Too many requests, please try again later.")
		return
	}
	q := r.URL.Query()
	client, err := s.store.GetClient(r.Context(), q.Get("client_id"))
	if err != nil {
		s.errorPage(w, http.StatusBadRequest, "Unknown client. Please set up the connection in your AI assistant again.")
		return
	}
	redirectURI := q.Get("redirect_uri")
	if redirectURI == "" && len(client.RedirectURIs) == 1 {
		redirectURI = client.RedirectURIs[0]
	}
	if !contains(client.RedirectURIs, redirectURI) {
		s.errorPage(w, http.StatusBadRequest, "The redirect address is not registered for this client.")
		return
	}
	state := q.Get("state")
	fail := func(code, desc string) {
		s.redirectClient(w, r, redirectURI, state, map[string]string{"error": code, "error_description": desc})
	}

	if q.Get("response_type") != "code" {
		fail("unsupported_response_type", "only response_type=code is supported")
		return
	}
	if q.Get("code_challenge") == "" || q.Get("code_challenge_method") != "S256" {
		fail("invalid_request", "PKCE with code_challenge_method=S256 is required")
		return
	}
	resource := q.Get("resource")
	if resource != "" && !s.acceptedResource(resource) {
		fail("invalid_target", "resource does not match this server")
		return
	}
	if len(s.order) == 0 {
		fail("server_error", "no login method configured")
		return
	}

	raw, err := s.store.CreateLoginState(r.Context(), LoginState{
		ClientID: client.ID, RedirectURI: redirectURI, CodeChallenge: q.Get("code_challenge"),
		ClientState: state, Resource: s.resource,
	})
	if err != nil {
		s.log.Error("create login state", "error", err)
		fail("server_error", "")
		return
	}
	if len(s.order) == 1 {
		s.startIDP(w, r, raw, s.order[0])
		return
	}
	type choice struct{ Name, URL string }
	var choices []choice
	for _, id := range s.order {
		choices = append(choices, choice{s.providers[id].DisplayName(), "/oauth/login?" + url.Values{"s": {raw}, "p": {id}}.Encode()})
	}
	htmlHeaders(w)
	w.Header().Set("Cache-Control", "no-store")
	_ = chooserTmpl.Execute(w, map[string]any{"Product": s.cfg.ProductName, "Providers": choices})
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	if !s.authLimit.Allow(s.clientIP(r)) {
		s.errorPage(w, http.StatusTooManyRequests, "Too many requests, please try again later.")
		return
	}
	s.startIDP(w, r, r.URL.Query().Get("s"), r.URL.Query().Get("p"))
}

// startIDP binds the pending request to a provider and redirects the browser to it.
func (s *Server) startIDP(w http.ResponseWriter, r *http.Request, rawState, providerID string) {
	p, ok := s.providers[providerID]
	if !ok {
		s.errorPage(w, http.StatusBadRequest, "Unknown login method.")
		return
	}
	nonce, verifier := randomToken(""), oauth2.GenerateVerifier()
	if _, err := s.store.BindLoginState(r.Context(), rawState, providerID, nonce, verifier); err != nil {
		s.errorPage(w, http.StatusBadRequest, "The login has expired or was already started. Please reconnect in your AI assistant.")
		return
	}
	authURL, err := p.AuthURL(r.Context(), idp.AuthRequest{
		State: rawState, Nonce: nonce, Verifier: verifier, RedirectURL: s.callbackURL(providerID),
	})
	if err != nil {
		s.log.Error("IdP unreachable", "provider", providerID, "error", err)
		s.errorPage(w, http.StatusBadGateway, "The identity provider is currently unreachable.")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, authURL, http.StatusFound)
}

func (s *Server) callbackURL(providerID string) string {
	return s.cfg.BaseURL + "/oauth/callback/" + url.PathEscape(providerID)
}

func (s *Server) callback(w http.ResponseWriter, r *http.Request) {
	if !s.authLimit.Allow(s.clientIP(r)) {
		s.errorPage(w, http.StatusTooManyRequests, "Too many requests, please try again later.")
		return
	}
	providerID := r.PathValue("provider")
	q := r.URL.Query()
	st, err := s.store.TakeLoginState(r.Context(), q.Get("state"))
	p, known := s.providers[providerID]
	if err != nil || !known || st.IDP != providerID {
		s.errorPage(w, http.StatusBadRequest, "The login has expired or is invalid. Please reconnect in your AI assistant.")
		return
	}
	deny := func(desc string) {
		s.redirectClient(w, r, st.RedirectURI, st.ClientState, map[string]string{"error": "access_denied", "error_description": desc})
	}
	if q.Get("error") != "" {
		s.log.Info("Login cancelled at the IdP", "provider", providerID, "error", q.Get("error"))
		deny("Login cancelled")
		return
	}
	if q.Get("code") == "" {
		deny("Login failed")
		return
	}

	id, err := p.Exchange(r.Context(), q.Get("code"), idp.AuthRequest{
		Nonce: st.IDPNonce, Verifier: st.IDPVerifier, RedirectURL: s.callbackURL(providerID),
	})
	if errors.Is(err, idp.ErrNotAllowed) {
		s.log.Warn("Login denied", "provider", providerID, "reason", err.Error())
		s.store.RecordDenied(r.Context(), providerID, strings.TrimPrefix(err.Error(), idp.ErrNotAllowed.Error()+": "))
		deny("This account is not allowed for this instance")
		return
	}
	if err != nil {
		s.log.Error("Login failed", "provider", providerID, "error", err)
		deny("Login failed")
		return
	}
	u, err := s.users.Provision(r.Context(), users.Identity{
		IDP: providerID, Subject: id.Subject, Email: id.Email, DisplayName: id.DisplayName, EmailVerified: id.EmailVerified,
	})
	if errors.Is(err, users.ErrDisabled) {
		s.log.Warn("Login of a disabled account", "provider", providerID)
		deny("This account is disabled")
		return
	}
	if errors.Is(err, users.ErrUserLimit) {
		s.log.Warn("Login refused: the number of users the instance may have is reached", "provider", providerID)
		s.store.RecordDenied(r.Context(), providerID, "user limit reached")
		deny("This instance has reached the number of users it may have. Please contact the administrator.")
		return
	}
	if errors.Is(err, users.ErrEmailUnverified) {
		s.log.Warn("Login refused: e-mail not verified for linking or admin rights", "provider", providerID)
		s.store.RecordDenied(r.Context(), providerID, "e-mail not verified: needed to link an account or for an administrator account")
		deny("Your identity provider does not confirm this e-mail address, so it cannot be linked to an existing account or used for an administrator account. Please contact the administrator.")
		return
	}
	if errors.Is(err, users.ErrEmailTaken) {
		s.log.Warn("Login with an e-mail that belongs to a different login", "provider", providerID)
		deny("This e-mail address already belongs to a login via a different provider. Please log in there or contact the administrator.")
		return
	}
	if err != nil {
		s.log.Error("create user", "error", err)
		deny("Login failed")
		return
	}
	s.store.RecordLogin(r.Context(), u.ID, providerID)
	code, err := s.store.CreateCode(r.Context(), Code{
		ClientID: st.ClientID, UserID: u.ID, RedirectURI: st.RedirectURI, CodeChallenge: st.CodeChallenge, Resource: st.Resource,
	})
	if err != nil {
		s.log.Error("create code", "error", err)
		deny("Login failed")
		return
	}
	s.redirectClient(w, r, st.RedirectURI, st.ClientState, map[string]string{"code": code})
}

// ---- token endpoint -------------------------------------------------------------------------

type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int64  `json:"expires_in"`
	RefreshToken string `json:"refresh_token"`
}

func noStore(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
}

// clientID reads client_id from the form or from HTTP Basic (public clients send no secret).
func clientID(r *http.Request) string {
	if id := r.PostForm.Get("client_id"); id != "" {
		return id
	}
	if u, _, ok := r.BasicAuth(); ok {
		return u
	}
	return ""
}

func (s *Server) token(w http.ResponseWriter, r *http.Request) {
	noStore(w)
	if !s.tokenLimit.Allow(s.clientIP(r)) {
		tooManyRequests(w)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	if err := r.ParseForm(); err != nil {
		tokenError(w, http.StatusBadRequest, "invalid_request", "form not readable")
		return
	}
	cid := clientID(r)
	if cid == "" {
		tokenError(w, http.StatusBadRequest, "invalid_client", "client_id missing")
		return
	}
	if _, err := s.store.GetClient(r.Context(), cid); err != nil {
		tokenError(w, http.StatusBadRequest, "invalid_client", "unknown client")
		return
	}
	if res := r.PostForm.Get("resource"); res != "" && !s.acceptedResource(res) {
		tokenError(w, http.StatusBadRequest, "invalid_target", "resource does not match this server")
		return
	}

	switch r.PostForm.Get("grant_type") {
	case "authorization_code":
		s.grantCode(w, r, cid)
	case "refresh_token":
		s.grantRefresh(w, r, cid)
	default:
		tokenError(w, http.StatusBadRequest, "unsupported_grant_type", "")
	}
}

func (s *Server) grantCode(w http.ResponseWriter, r *http.Request, cid string) {
	c, err := s.store.TakeCode(r.Context(), r.PostForm.Get("code"))
	if err != nil {
		tokenError(w, http.StatusBadRequest, "invalid_grant", "code invalid or expired")
		return
	}
	verifier := r.PostForm.Get("code_verifier")
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])
	if c.ClientID != cid || c.RedirectURI != r.PostForm.Get("redirect_uri") || verifier == "" ||
		subtle.ConstantTimeCompare([]byte(challenge), []byte(c.CodeChallenge)) != 1 {
		tokenError(w, http.StatusBadRequest, "invalid_grant", "code, redirect_uri or code_verifier do not match")
		return
	}
	u, err := s.users.Get(r.Context(), c.UserID)
	if err != nil || !u.Active {
		tokenError(w, http.StatusBadRequest, "invalid_grant", "account not available")
		return
	}
	pair, err := s.store.IssueTokens(r.Context(), cid, c.UserID, c.Resource)
	if err != nil {
		s.log.Error("issue token", "error", err)
		tokenError(w, http.StatusInternalServerError, "server_error", "")
		return
	}
	writeTokens(w, pair)
}

func (s *Server) grantRefresh(w http.ResponseWriter, r *http.Request, cid string) {
	res, err := s.store.Rotate(r.Context(), r.PostForm.Get("refresh_token"), cid)
	if errors.Is(err, ErrInvalidGrant) {
		tokenError(w, http.StatusBadRequest, "invalid_grant", "refresh token invalid or expired")
		return
	}
	if err != nil {
		s.log.Error("Refresh", "error", err)
		tokenError(w, http.StatusInternalServerError, "server_error", "")
		return
	}
	if u, err := s.users.Get(r.Context(), res.UserID); err != nil || !u.Active {
		_, _ = s.store.RevokeUser(r.Context(), res.UserID)
		tokenError(w, http.StatusBadRequest, "invalid_grant", "account not available")
		return
	}
	writeTokens(w, res.Pair)
}

func writeTokens(w http.ResponseWriter, p TokenPair) {
	writeJSON(w, http.StatusOK, tokenResponse{
		AccessToken: p.Access, TokenType: "Bearer", ExpiresIn: int64(p.AccessTTL.Seconds()), RefreshToken: p.Refresh,
	})
}

func (s *Server) revoke(w http.ResponseWriter, r *http.Request) {
	noStore(w)
	if !s.tokenLimit.Allow(s.clientIP(r)) {
		tooManyRequests(w)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	if err := r.ParseForm(); err != nil {
		tokenError(w, http.StatusBadRequest, "invalid_request", "")
		return
	}
	if cid, tok := clientID(r), r.PostForm.Get("token"); cid != "" && tok != "" {
		if err := s.store.Revoke(r.Context(), tok, cid); err != nil {
			s.log.Error("Revoke", "error", err)
		}
	}
	w.WriteHeader(http.StatusOK) // RFC 7009: unknown tokens are not an error
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// RevokeUser is exposed for offboarding (admin tools).
func (s *Server) RevokeUser(ctx context.Context, id uuid.UUID) (int64, error) {
	return s.store.RevokeUser(ctx, id)
}

// Sessions lists the clients a user is logged in with.
func (s *Server) Sessions(ctx context.Context, id uuid.UUID) ([]Session, error) {
	return s.store.Sessions(ctx, id)
}

// RevokeClient ends the sessions of one client of the user.
func (s *Server) RevokeClient(ctx context.Context, id uuid.UUID, clientID string) (int64, error) {
	return s.store.RevokeClient(ctx, id, clientID)
}

// Cleanup removes expired rows; run it periodically.
func (s *Server) Cleanup(ctx context.Context) error { return s.store.Cleanup(ctx) }

func tooManyRequests(w http.ResponseWriter) {
	w.Header().Set("Retry-After", "60")
	tokenError(w, http.StatusTooManyRequests, "temporarily_unavailable", "too many requests, try again in a minute")
}

// htmlHeaders sets the headers of the pages the server renders itself: no framing, no
// scripts, no referrer, and nothing cached.
func htmlHeaders(w http.ResponseWriter) {
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("X-Frame-Options", "DENY")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
}
