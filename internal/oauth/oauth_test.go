package oauth_test

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"github.com/behringer24/atrium-tablinum/internal/allowlist"
	"github.com/google/uuid"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/modelcontextprotocol/go-sdk/auth"
	"golang.org/x/oauth2"

	"github.com/behringer24/atrium-tablinum/internal/config"
	"github.com/behringer24/atrium-tablinum/internal/dbtest"
	"github.com/behringer24/atrium-tablinum/internal/idp"
	"github.com/behringer24/atrium-tablinum/internal/idp/idptest"
	"github.com/behringer24/atrium-tablinum/internal/oauth"
	"github.com/behringer24/atrium-tablinum/internal/users"
)

const claudeRedirect = "https://claude.ai/api/mcp/auth_callback"

type env struct {
	t     *testing.T
	ctx   context.Context
	pool  *pgxpool.Pool
	idp   *idptest.Server
	idp2  *idptest.Server
	users *users.Store
	allow *allowlist.Store
	srv   *oauth.Server
	http  *httptest.Server
	cl    *http.Client // does not follow redirects
}

func setup(t *testing.T, providers int) *env {
	t.Helper()
	e := &env{t: t, ctx: context.Background(), pool: dbtest.NewPool(t), idp: idptest.New(t)}
	e.users = users.New(e.pool, users.Options{PersonalEnabled: true})

	mk := func(id string, fake *idptest.Server) idp.Provider {
		p, err := idp.New(config.Provider{ID: id, Type: "oidc", Issuer: fake.URL, ClientID: idptest.ClientID,
			ClientSecret: idptest.ClientSecret, AllowedDomains: []string{"example.com"}})
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	provs := []idp.Provider{mk("kc", e.idp)}
	e.allow = allowlist.New(e.pool)
	if providers > 1 {
		e.idp2 = idptest.New(t)
		provs = append(provs, mk("kc2", e.idp2))
	}

	for _, p := range provs {
		p.(idp.AdditionsSetter).SetAdditions(e.allow)
	}

	mux := http.NewServeMux()
	e.http = httptest.NewServer(mux)
	t.Cleanup(e.http.Close)
	e.srv = oauth.NewServer(oauth.Config{
		BaseURL: e.http.URL, ProductName: "Atrium", AllowLoopback: true,
		AllowedRedirect: []string{claudeRedirect, "https://chatgpt.com/connector/oauth/*"},
	}, oauth.NewStore(e.pool, time.Hour, 720*time.Hour), e.users, provs)
	e.srv.Register(mux)
	mux.Handle("/mcp", auth.RequireBearerToken(e.srv.Verifier(), &auth.RequireBearerTokenOptions{
		ResourceMetadataURL: e.srv.ResourceMetadataURL(), AllowMissingExpiration: true,
	})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, auth.TokenInfoFromContext(r.Context()).UserID)
	})))

	e.cl = &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return e
}

func (e *env) do(method, target string, form url.Values, hdr map[string]string) (*http.Response, string) {
	e.t.Helper()
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	if !strings.HasPrefix(target, "http") {
		target = e.http.URL + target
	}
	req, _ := http.NewRequest(method, target, body)
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := e.cl.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, string(b)
}

func (e *env) jsonPost(path string, body string) (*http.Response, map[string]any) {
	e.t.Helper()
	req, _ := http.NewRequest(http.MethodPost, e.http.URL+path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := e.cl.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	var m map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&m)
	return resp, m
}

func (e *env) register(redirect string) string {
	e.t.Helper()
	b, _ := json.Marshal(map[string]any{"redirect_uris": []string{redirect}, "client_name": "Claude"})
	resp, m := e.jsonPost("/oauth/register", string(b))
	if resp.StatusCode != http.StatusCreated {
		e.t.Fatalf("register %s: %d %v", redirect, resp.StatusCode, m)
	}
	return m["client_id"].(string)
}

type pkce struct{ verifier, challenge string }

func newPKCE() pkce {
	v := oauth2.GenerateVerifier()
	sum := sha256.Sum256([]byte(v))
	return pkce{v, base64.RawURLEncoding.EncodeToString(sum[:])}
}

func (e *env) authorizeURL(clientID, redirect string, p pkce, extra url.Values) string {
	q := url.Values{"response_type": {"code"}, "client_id": {clientID}, "redirect_uri": {redirect}, "state": {"client-state"},
		"code_challenge": {p.challenge}, "code_challenge_method": {"S256"}, "resource": {e.http.URL + "/mcp"}}
	for k, v := range extra {
		q[k] = v
	}
	return "/oauth/authorize?" + q.Encode()
}

// login runs authorize -> IdP -> callback and returns the final redirect to the client.
func (e *env) login(authorizeURL string) *url.URL {
	e.t.Helper()
	resp, body := e.do(http.MethodGet, authorizeURL, nil, nil)
	if resp.StatusCode != http.StatusFound {
		e.t.Fatalf("authorize: %d %s", resp.StatusCode, body)
	}
	idpURL := resp.Header.Get("Location")
	resp, _ = e.do(http.MethodGet, idpURL, nil, nil) // fake IdP approves at once
	if resp.StatusCode != http.StatusFound {
		e.t.Fatalf("idp: %d", resp.StatusCode)
	}
	resp, body = e.do(http.MethodGet, resp.Header.Get("Location"), nil, nil)
	if resp.StatusCode != http.StatusFound {
		e.t.Fatalf("callback: %d %s", resp.StatusCode, body)
	}
	loc, _ := url.Parse(resp.Header.Get("Location"))
	return loc
}

func (e *env) exchange(clientID, redirect, code, verifier string) (*http.Response, map[string]any) {
	e.t.Helper()
	resp, body := e.do(http.MethodPost, "/oauth/token", url.Values{"grant_type": {"authorization_code"}, "code": {code},
		"redirect_uri": {redirect}, "client_id": {clientID}, "code_verifier": {verifier}, "resource": {e.http.URL + "/mcp"}}, nil)
	var m map[string]any
	_ = json.Unmarshal([]byte(body), &m)
	return resp, m
}

// tokens performs the complete flow and returns the token response.
func (e *env) tokens() (clientID string, m map[string]any) {
	e.t.Helper()
	clientID = e.register(claudeRedirect)
	p := newPKCE()
	loc := e.login(e.authorizeURL(clientID, claudeRedirect, p, nil))
	resp, m := e.exchange(clientID, claudeRedirect, loc.Query().Get("code"), p.verifier)
	if resp.StatusCode != http.StatusOK {
		e.t.Fatalf("token: %d %v", resp.StatusCode, m)
	}
	return clientID, m
}

func (e *env) mcpStatus(token string) (int, string) {
	e.t.Helper()
	resp, body := e.do(http.MethodPost, "/mcp", nil, map[string]string{"Authorization": "Bearer " + token})
	return resp.StatusCode, body
}

// ---------------------------------------------------------------------------------------------

func TestMetadata(t *testing.T) {
	e := setup(t, 1)
	for _, path := range []string{"/.well-known/oauth-protected-resource", "/.well-known/oauth-protected-resource/mcp"} {
		_, body := e.do(http.MethodGet, path, nil, nil)
		var m map[string]any
		_ = json.Unmarshal([]byte(body), &m)
		if m["resource"] != e.http.URL+"/mcp" || m["authorization_servers"].([]any)[0] != e.http.URL {
			t.Errorf("%s: %v", path, m)
		}
	}
	_, body := e.do(http.MethodGet, "/.well-known/oauth-authorization-server", nil, nil)
	var m map[string]any
	_ = json.Unmarshal([]byte(body), &m)
	if m["issuer"] != e.http.URL || m["authorization_endpoint"] != e.http.URL+"/oauth/authorize" ||
		m["registration_endpoint"] != e.http.URL+"/oauth/register" || m["authorization_response_iss_parameter_supported"] != true {
		t.Errorf("AS-Metadaten: %v", m)
	}
	if methods := m["code_challenge_methods_supported"].([]any); len(methods) != 1 || methods[0] != "S256" {
		t.Errorf("PKCE-Methoden: %v", methods)
	}
	// 401 points to the resource metadata
	resp, _ := e.do(http.MethodPost, "/mcp", nil, nil)
	if resp.StatusCode != 401 || !strings.Contains(resp.Header.Get("WWW-Authenticate"), "resource_metadata="+`"`+e.srv.ResourceMetadataURL()+`"`) {
		t.Errorf("401: %d %q", resp.StatusCode, resp.Header.Get("WWW-Authenticate"))
	}
}

func TestRegistrationValidation(t *testing.T) {
	e := setup(t, 1)
	cases := []struct {
		name string
		body string
		want int
		err  string
	}{
		{"claude", `{"redirect_uris":["` + claudeRedirect + `"]}`, 201, ""},
		{"wildcard match", `{"redirect_uris":["https://chatgpt.com/connector/oauth/abc123"]}`, 201, ""},
		{"loopback with port", `{"redirect_uris":["http://localhost:53682/callback"]}`, 201, ""},
		{"loopback ip", `{"redirect_uris":["http://127.0.0.1:1/cb"]}`, 201, ""},
		{"unknown host", `{"redirect_uris":["https://evil.example/cb"]}`, 400, "invalid_redirect_uri"},
		{"host trick via wildcard prefix", `{"redirect_uris":["https://chatgpt.com.evil.example/connector/oauth/x"]}`, 400, "invalid_redirect_uri"},
		{"http non-loopback", `{"redirect_uris":["http://claude.ai/api/mcp/auth_callback"]}`, 400, "invalid_redirect_uri"},
		{"userinfo", `{"redirect_uris":["https://user@claude.ai/api/mcp/auth_callback"]}`, 400, "invalid_redirect_uri"},
		{"fragment", `{"redirect_uris":["` + claudeRedirect + `#x"]}`, 400, "invalid_redirect_uri"},
		{"none", `{"redirect_uris":[]}`, 400, "invalid_redirect_uri"},
		{"bad grant", `{"redirect_uris":["` + claudeRedirect + `"],"grant_types":["password"]}`, 400, "invalid_client_metadata"},
		{"bad response type", `{"redirect_uris":["` + claudeRedirect + `"],"response_types":["token"]}`, 400, "invalid_client_metadata"},
		{"not json", `nope`, 400, "invalid_client_metadata"},
	}
	for _, tc := range cases {
		resp, m := e.jsonPost("/oauth/register", tc.body)
		if resp.StatusCode != tc.want || (tc.err != "" && m["error"] != tc.err) {
			t.Errorf("%s: %d %v", tc.name, resp.StatusCode, m)
		}
		if tc.want == 201 && (m["token_endpoint_auth_method"] != "none" || m["client_id"] == "") {
			t.Errorf("%s: %v", tc.name, m)
		}
	}
	// a client asking for a secret still becomes public
	_, m := e.jsonPost("/oauth/register", `{"redirect_uris":["`+claudeRedirect+`"],"token_endpoint_auth_method":"client_secret_post"}`)
	if m["token_endpoint_auth_method"] != "none" || m["client_secret"] != nil {
		t.Errorf("%v", m)
	}
}

func TestRegistrationRateLimit(t *testing.T) {
	e := setup(t, 1)
	var limited bool
	for i := 0; i < 25; i++ {
		if resp, _ := e.jsonPost("/oauth/register", `{"redirect_uris":["`+claudeRedirect+`"]}`); resp.StatusCode == http.StatusTooManyRequests {
			limited = true
		}
	}
	if !limited {
		t.Error("kein Rate Limit bei 25 Registrierungen")
	}
}

func TestFullFlowAndTokenUse(t *testing.T) {
	e := setup(t, 1)
	clientID, tok := e.tokens()
	if tok["token_type"] != "Bearer" || tok["expires_in"].(float64) != 3600 || tok["refresh_token"] == "" {
		t.Fatalf("%v", tok)
	}

	// Redirect contains state and iss
	p := newPKCE()
	loc := e.login(e.authorizeURL(clientID, claudeRedirect, p, nil))
	if loc.Query().Get("state") != "client-state" || loc.Query().Get("iss") != e.http.URL || loc.Query().Get("code") == "" {
		t.Errorf("Redirect: %s", loc)
	}
	if loc.Scheme+"://"+loc.Host+loc.Path != claudeRedirect {
		t.Errorf("falsches Ziel: %s", loc)
	}

	status, uid := e.mcpStatus(tok["access_token"].(string))
	if status != 200 || uid == "" {
		t.Fatalf("MCP mit Token: %d %s", status, uid)
	}
	u, err := e.users.GetByEmail(e.ctx, "anna@example.com")
	if err != nil || uid != u.ID.String() {
		t.Errorf("Token gehört nicht zu Anna: %s vs %v", uid, u.ID)
	}
	if status, _ := e.mcpStatus("at_unknown"); status != 401 {
		t.Errorf("unbekanntes Token: %d", status)
	}
	// A refresh token is not an access token
	if status, _ := e.mcpStatus(tok["refresh_token"].(string)); status != 401 {
		t.Errorf("Refresh-Token als Access-Token: %d", status)
	}
	// Token is bound to the resource (audience)
	if _, err := e.pool.Exec(e.ctx, `UPDATE oauth_tokens SET resource = 'https://other.example/mcp'`); err != nil {
		t.Fatal(err)
	}
	if status, _ := e.mcpStatus(tok["access_token"].(string)); status != 401 {
		t.Errorf("Token für fremde Ressource akzeptiert: %d", status)
	}
}

func TestSecretsAreStoredHashed(t *testing.T) {
	e := setup(t, 1)
	_, tok := e.tokens()
	var n int
	if err := e.pool.QueryRow(e.ctx, `SELECT count(*) FROM oauth_tokens WHERE token_hash IN ($1, $2)`,
		tok["access_token"], tok["refresh_token"]).Scan(&n); err != nil || n != 0 {
		t.Errorf("Klartext-Token in der Datenbank (%d, %v)", n, err)
	}
}

func TestCodeExchangeChecks(t *testing.T) {
	e := setup(t, 1)
	clientID := e.register(claudeRedirect)
	other := e.register("https://chatgpt.com/connector/oauth/x")

	get := func(p pkce) string {
		return e.login(e.authorizeURL(clientID, claudeRedirect, p, nil)).Query().Get("code")
	}
	p := newPKCE()
	code := get(p)
	if resp, m := e.exchange(clientID, claudeRedirect, code, newPKCE().verifier); resp.StatusCode != 400 || m["error"] != "invalid_grant" {
		t.Errorf("falscher Verifier: %d %v", resp.StatusCode, m)
	}
	// Code is used up after the failed attempt
	if resp, m := e.exchange(clientID, claudeRedirect, code, p.verifier); resp.StatusCode != 400 || m["error"] != "invalid_grant" {
		t.Errorf("Code wiederverwendbar: %d %v", resp.StatusCode, m)
	}
	code = get(p)
	if resp, _ := e.exchange(clientID, "https://claude.ai/other", code, p.verifier); resp.StatusCode != 400 {
		t.Errorf("falsche redirect_uri: %d", resp.StatusCode)
	}
	code = get(p)
	if resp, _ := e.exchange(other, claudeRedirect, code, p.verifier); resp.StatusCode != 400 {
		t.Errorf("fremder Client: %d", resp.StatusCode)
	}
	code = get(p)
	if resp, _ := e.exchange(clientID, claudeRedirect, code, ""); resp.StatusCode != 400 {
		t.Errorf("ohne Verifier: %d", resp.StatusCode)
	}
	// expired code
	code = get(p)
	_, _ = e.pool.Exec(e.ctx, `UPDATE oauth_codes SET expires_at = now() - interval '1 second'`)
	if resp, _ := e.exchange(clientID, claudeRedirect, code, p.verifier); resp.StatusCode != 400 {
		t.Errorf("abgelaufener Code: %d", resp.StatusCode)
	}
	// Success remains possible
	code = get(p)
	if resp, _ := e.exchange(clientID, claudeRedirect, code, p.verifier); resp.StatusCode != 200 {
		t.Errorf("gültiger Austausch: %d", resp.StatusCode)
	}
	// Resource parameter for a foreign server
	code = get(p)
	resp, body := e.do(http.MethodPost, "/oauth/token", url.Values{"grant_type": {"authorization_code"}, "code": {code},
		"redirect_uri": {claudeRedirect}, "client_id": {clientID}, "code_verifier": {p.verifier}, "resource": {"https://other.example/mcp"}}, nil)
	if resp.StatusCode != 400 || !strings.Contains(body, "invalid_target") {
		t.Errorf("fremde resource: %d %s", resp.StatusCode, body)
	}
	// unknown client / grant
	if resp, _ := e.do(http.MethodPost, "/oauth/token", url.Values{"grant_type": {"authorization_code"}, "client_id": {"nope"}}, nil); resp.StatusCode != 400 {
		t.Errorf("unbekannter Client: %d", resp.StatusCode)
	}
	if resp, body := e.do(http.MethodPost, "/oauth/token", url.Values{"grant_type": {"password"}, "client_id": {clientID}}, nil); resp.StatusCode != 400 || !strings.Contains(body, "unsupported_grant_type") {
		t.Errorf("password-Grant: %d %s", resp.StatusCode, body)
	}
	if resp, _ := e.do(http.MethodGet, "/oauth/token", nil, nil); resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("GET auf Token-Endpunkt: %d", resp.StatusCode)
	}
}

func TestAuthorizeRequestValidation(t *testing.T) {
	e := setup(t, 1)
	clientID := e.register(claudeRedirect)
	p := newPKCE()

	// Error without a trusted redirect: page instead of redirect
	for name, u := range map[string]string{
		"unbekannter Client":     e.authorizeURL("nope", claudeRedirect, p, nil),
		"nicht registrierte URI": e.authorizeURL(clientID, "https://claude.ai/other", p, nil),
		"URI ohne Registrierung": e.authorizeURL(clientID, "http://localhost:9/cb", p, nil),
	} {
		resp, body := e.do(http.MethodGet, u, nil, nil)
		if resp.StatusCode != 400 || resp.Header.Get("Location") != "" || !strings.Contains(body, "Anmeldung nicht möglich") {
			t.Errorf("%s: %d %q", name, resp.StatusCode, resp.Header.Get("Location"))
		}
	}
	// Error with redirect to the registered client (incl. state and iss)
	for name, tc := range map[string]struct {
		extra url.Values
		want  string
	}{
		"ohne PKCE":        {url.Values{"code_challenge": nil}, "invalid_request"},
		"plain statt S256": {url.Values{"code_challenge_method": {"plain"}}, "invalid_request"},
		"falscher Typ":     {url.Values{"response_type": {"token"}}, "unsupported_response_type"},
		"fremde Resource":  {url.Values{"resource": {"https://other.example/mcp"}}, "invalid_target"},
	} {
		u := e.authorizeURL(clientID, claudeRedirect, p, nil)
		parsed, _ := url.Parse(u)
		q := parsed.Query()
		for k, v := range tc.extra {
			if v == nil {
				q.Del(k)
			} else {
				q[k] = v
			}
		}
		resp, _ := e.do(http.MethodGet, "/oauth/authorize?"+q.Encode(), nil, nil)
		loc, _ := url.Parse(resp.Header.Get("Location"))
		if resp.StatusCode != 302 || loc.Query().Get("error") != tc.want || loc.Query().Get("state") != "client-state" || loc.Query().Get("iss") != e.http.URL {
			t.Errorf("%s: %d %s", name, resp.StatusCode, loc)
		}
	}
	// Resource without a path (origin only) is allowed
	resp, _ := e.do(http.MethodGet, e.authorizeURL(clientID, claudeRedirect, p, url.Values{"resource": {e.http.URL}}), nil, nil)
	if resp.StatusCode != 302 || !strings.HasPrefix(resp.Header.Get("Location"), e.idp.URL) {
		t.Errorf("Origin als resource: %d %s", resp.StatusCode, resp.Header.Get("Location"))
	}
	// redirect_uri may be omitted if exactly one is registered
	q := url.Values{"response_type": {"code"}, "client_id": {clientID}, "code_challenge": {p.challenge}, "code_challenge_method": {"S256"}}
	if resp, _ := e.do(http.MethodGet, "/oauth/authorize?"+q.Encode(), nil, nil); resp.StatusCode != 302 {
		t.Errorf("ohne redirect_uri: %d", resp.StatusCode)
	}
}

func TestIdPRequestUsesPKCEAndNonce(t *testing.T) {
	e := setup(t, 1)
	clientID := e.register(claudeRedirect)
	e.login(e.authorizeURL(clientID, claudeRedirect, newPKCE(), nil))
	last := e.idp.Last
	if last.Get("code_challenge_method") != "S256" || last.Get("code_challenge") == "" || last.Get("nonce") == "" ||
		last.Get("redirect_uri") != e.http.URL+"/oauth/callback/kc" {
		t.Errorf("Anfrage an den IdP: %v", last)
	}
}

func TestRefreshRotationAndReuseDetection(t *testing.T) {
	e := setup(t, 1)
	clientID, tok := e.tokens()
	refresh := func(rt string) (*http.Response, map[string]any) {
		resp, body := e.do(http.MethodPost, "/oauth/token", url.Values{"grant_type": {"refresh_token"}, "refresh_token": {rt}, "client_id": {clientID}}, nil)
		var m map[string]any
		_ = json.Unmarshal([]byte(body), &m)
		return resp, m
	}
	resp, second := refresh(tok["refresh_token"].(string))
	if resp.StatusCode != 200 || second["refresh_token"] == tok["refresh_token"] || second["access_token"] == tok["access_token"] {
		t.Fatalf("Rotation: %d %v", resp.StatusCode, second)
	}
	if status, _ := e.mcpStatus(second["access_token"].(string)); status != 200 {
		t.Errorf("neues Access-Token: %d", status)
	}
	// old refresh token again: suspected theft -> revoke everything
	if resp, m := refresh(tok["refresh_token"].(string)); resp.StatusCode != 400 || m["error"] != "invalid_grant" {
		t.Errorf("Wiederverwendung: %d %v", resp.StatusCode, m)
	}
	if status, _ := e.mcpStatus(second["access_token"].(string)); status != 401 {
		t.Errorf("Access-Token nach Reuse-Erkennung noch gültig: %d", status)
	}
	if resp, _ := refresh(second["refresh_token"].(string)); resp.StatusCode != 400 {
		t.Errorf("neues Refresh-Token nach Reuse-Erkennung noch gültig: %d", resp.StatusCode)
	}
}

func TestRefreshChecks(t *testing.T) {
	e := setup(t, 1)
	clientID, tok := e.tokens()
	otherClient := e.register(claudeRedirect)
	rt := tok["refresh_token"].(string)
	post := func(cid, token string) int {
		resp, _ := e.do(http.MethodPost, "/oauth/token", url.Values{"grant_type": {"refresh_token"}, "refresh_token": {token}, "client_id": {cid}}, nil)
		return resp.StatusCode
	}
	if post(otherClient, rt) != 400 {
		t.Error("Refresh-Token mit fremdem Client")
	}
	if post(clientID, tok["access_token"].(string)) != 400 {
		t.Error("Access-Token als Refresh-Token")
	}
	if post(clientID, "rt_unknown") != 400 {
		t.Error("unbekanntes Refresh-Token")
	}
	_, _ = e.pool.Exec(e.ctx, `UPDATE oauth_tokens SET expires_at = now() - interval '1 second' WHERE kind = 'refresh'`)
	if post(clientID, rt) != 400 {
		t.Error("abgelaufenes Refresh-Token")
	}
}

func TestRevoke(t *testing.T) {
	e := setup(t, 1)
	clientID, tok := e.tokens()
	access, refresh := tok["access_token"].(string), tok["refresh_token"].(string)

	// a foreign client may not revoke anything, but still gets 200
	other := e.register(claudeRedirect)
	if resp, _ := e.do(http.MethodPost, "/oauth/revoke", url.Values{"token": {access}, "client_id": {other}}, nil); resp.StatusCode != 200 {
		t.Errorf("Status %d", resp.StatusCode)
	}
	if status, _ := e.mcpStatus(access); status != 200 {
		t.Error("Token durch fremden Client widerrufen")
	}
	if resp, _ := e.do(http.MethodPost, "/oauth/revoke", url.Values{"token": {"rt_unknown"}, "client_id": {clientID}}, nil); resp.StatusCode != 200 {
		t.Errorf("unbekanntes Token: %d", resp.StatusCode)
	}
	// Revoking the refresh token also revokes the access token
	e.do(http.MethodPost, "/oauth/revoke", url.Values{"token": {refresh}, "client_id": {clientID}}, nil)
	if status, _ := e.mcpStatus(access); status != 401 {
		t.Errorf("Access-Token nach Revoke des Refresh-Tokens: %d", status)
	}
	resp, _ := e.do(http.MethodPost, "/oauth/token", url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refresh}, "client_id": {clientID}}, nil)
	if resp.StatusCode != 400 {
		t.Errorf("Refresh nach Revoke: %d", resp.StatusCode)
	}
}

func TestLoginPolicyAndDisabledUsers(t *testing.T) {
	e := setup(t, 1)
	clientID := e.register(claudeRedirect)
	denied := func(loc *url.URL, why string) {
		t.Helper()
		if loc.Query().Get("error") != "access_denied" || loc.Query().Get("code") != "" || loc.Query().Get("state") != "client-state" {
			t.Errorf("%s: %s", why, loc)
		}
	}

	e.idp.SetClaims(map[string]any{"email": "eve@evil.com"})
	denied(e.login(e.authorizeURL(clientID, claudeRedirect, newPKCE(), nil)), "fremde Domain")
	e.idp.SetClaims(map[string]any{"email_verified": false})
	denied(e.login(e.authorizeURL(clientID, claudeRedirect, newPKCE(), nil)), "unverifizierte E-Mail")
	if n := countUsers(t, e); n != 0 {
		t.Errorf("abgelehnte Logins haben %d Konten angelegt", n)
	}

	e.idp.SetClaims(nil)
	_, tok := e.tokens()
	u, _ := e.users.GetByEmail(e.ctx, "anna@example.com")
	if err := e.users.SetActive(e.ctx, u.ID, false); err != nil {
		t.Fatal(err)
	}
	// existing tokens are worthless immediately, new logins are rejected
	if status, _ := e.mcpStatus(tok["access_token"].(string)); status != 401 {
		t.Errorf("Token eines deaktivierten Nutzers: %d", status)
	}
	denied(e.login(e.authorizeURL(clientID, claudeRedirect, newPKCE(), nil)), "deaktiviertes Konto")
	resp, _ := e.do(http.MethodPost, "/oauth/token", url.Values{"grant_type": {"refresh_token"}, "refresh_token": {tok["refresh_token"].(string)}, "client_id": {clientID}}, nil)
	if resp.StatusCode != 400 {
		t.Errorf("Refresh eines deaktivierten Nutzers: %d", resp.StatusCode)
	}
}

func countUsers(t *testing.T, e *env) int {
	var n int
	if err := e.pool.QueryRow(e.ctx, `SELECT count(*) FROM users`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestStateIsSingleUseAndBoundToProvider(t *testing.T) {
	e := setup(t, 1)
	clientID := e.register(claudeRedirect)
	resp, _ := e.do(http.MethodGet, e.authorizeURL(clientID, claudeRedirect, newPKCE(), nil), nil, nil)
	resp, _ = e.do(http.MethodGet, resp.Header.Get("Location"), nil, nil)
	callback := resp.Header.Get("Location")

	if resp, _ = e.do(http.MethodGet, callback, nil, nil); resp.StatusCode != 302 {
		t.Fatalf("erster Aufruf: %d", resp.StatusCode)
	}
	if resp, body := e.do(http.MethodGet, callback, nil, nil); resp.StatusCode != 400 || strings.Contains(resp.Header.Get("Location"), "code=") {
		t.Errorf("State wiederverwendbar: %d %s", resp.StatusCode, body)
	}
	for _, u := range []string{"/oauth/callback/kc", "/oauth/callback/kc?state=garbage&code=x", "/oauth/callback/unknown?state=x"} {
		if resp, _ := e.do(http.MethodGet, u, nil, nil); resp.StatusCode != 400 {
			t.Errorf("%s: %d", u, resp.StatusCode)
		}
	}
	// IdP reports cancellation -> access_denied at the client, state consumed
	resp, _ = e.do(http.MethodGet, e.authorizeURL(clientID, claudeRedirect, newPKCE(), nil), nil, nil)
	idpAuth, _ := url.Parse(resp.Header.Get("Location"))
	cb := e.http.URL + "/oauth/callback/kc?" + url.Values{"state": {idpAuth.Query().Get("state")}, "error": {"access_denied"}}.Encode()
	resp, _ = e.do(http.MethodGet, cb, nil, nil)
	if loc, _ := url.Parse(resp.Header.Get("Location")); loc.Query().Get("error") != "access_denied" {
		t.Errorf("IdP-Abbruch: %s", loc)
	}
}

func TestProviderChooser(t *testing.T) {
	e := setup(t, 2)
	clientID := e.register(claudeRedirect)
	resp, body := e.do(http.MethodGet, e.authorizeURL(clientID, claudeRedirect, newPKCE(), nil), nil, nil)
	if resp.StatusCode != 200 || !strings.Contains(body, "kc2") || !strings.Contains(body, "/oauth/login?") {
		t.Fatalf("Auswahlseite: %d %s", resp.StatusCode, body)
	}
	href := body[strings.Index(body, "/oauth/login?"):]
	href = strings.ReplaceAll(href[:strings.Index(href, `"`)], "&amp;", "&")
	resp, _ = e.do(http.MethodGet, href, nil, nil)
	if resp.StatusCode != 302 || !strings.HasPrefix(resp.Header.Get("Location"), e.idp.URL) {
		t.Fatalf("Weiterleitung zum IdP: %d %s", resp.StatusCode, resp.Header.Get("Location"))
	}
	// the same link a second time: the state is already bound
	if resp, _ = e.do(http.MethodGet, href, nil, nil); resp.StatusCode != 400 {
		t.Errorf("Link wiederverwendbar: %d", resp.StatusCode)
	}
	if resp, _ = e.do(http.MethodGet, "/oauth/login?s=x&p=kc", nil, nil); resp.StatusCode != 400 {
		t.Errorf("unbekannter State: %d", resp.StatusCode)
	}
	if resp, _ = e.do(http.MethodGet, "/oauth/login?s=x&p=nope", nil, nil); resp.StatusCode != 400 {
		t.Errorf("unbekannter Provider: %d", resp.StatusCode)
	}
}

func TestIdPOutageShowsErrorPage(t *testing.T) {
	e := setup(t, 1)
	clientID := e.register(claudeRedirect)
	e.idp.Close()
	resp, body := e.do(http.MethodGet, e.authorizeURL(clientID, claudeRedirect, newPKCE(), nil), nil, nil)
	if resp.StatusCode != http.StatusBadGateway || !strings.Contains(body, "nicht erreichbar") {
		t.Errorf("%d %s", resp.StatusCode, body)
	}
}

func TestCleanup(t *testing.T) {
	e := setup(t, 1)
	e.tokens() // client with tokens remains
	e.register(claudeRedirect)
	_, _ = e.pool.Exec(e.ctx, `UPDATE oauth_clients SET created_at = now() - interval '2 days'`)
	_, _ = e.pool.Exec(e.ctx, `UPDATE oauth_tokens SET expires_at = now() - interval '8 days' WHERE kind = 'access'`)
	if err := e.srv.Cleanup(e.ctx); err != nil {
		t.Fatal(err)
	}
	count := func(q string) int {
		var n int
		_ = e.pool.QueryRow(e.ctx, q).Scan(&n)
		return n
	}
	if n := count(`SELECT count(*) FROM oauth_clients`); n != 1 {
		t.Errorf("Clients nach Cleanup: %d (erwartet 1: nur der mit Tokens)", n)
	}
	if n := count(`SELECT count(*) FROM oauth_tokens WHERE kind = 'access'`); n != 0 {
		t.Errorf("alte Access-Tokens: %d", n)
	}
	if n := count(`SELECT count(*) FROM oauth_tokens WHERE kind = 'refresh'`); n != 1 {
		t.Errorf("gültiges Refresh-Token verloren: %d", n)
	}
}

func TestSessionsAndAbsoluteLifetime(t *testing.T) {
	e := setup(t, 1)
	clientID, tok := e.tokens()
	u, _ := e.users.GetByEmail(e.ctx, "anna@example.com")
	store := oauth.NewStore(e.pool, time.Hour, 720*time.Hour).WithSessionMaxAge(24 * time.Hour)

	list, err := store.Sessions(e.ctx, u.ID)
	if err != nil || len(list) != 1 || list[0].ClientID != clientID || list[0].ClientName != "Claude" {
		t.Fatalf("Sessions: %+v %v", list, err)
	}
	// Rotation carries over the session start, so it remains the same session
	res, err := store.Rotate(e.ctx, tok["refresh_token"].(string), clientID)
	if err != nil {
		t.Fatal(err)
	}
	if list, _ = store.Sessions(e.ctx, u.ID); len(list) != 1 {
		t.Errorf("Sessions nach Rotation: %+v", list)
	}
	// after the maximum age even a valid refresh token no longer helps
	if _, err := e.pool.Exec(e.ctx, `UPDATE oauth_tokens SET session_started_at = now() - interval '48 hours'`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Rotate(e.ctx, res.Pair.Refresh, clientID); !errors.Is(err, oauth.ErrInvalidGrant) {
		t.Errorf("Rotation nach der Höchstdauer: %v", err)
	}
	// without a maximum age (0) the previous behavior remains
	if _, err := oauth.NewStore(e.pool, time.Hour, 720*time.Hour).Rotate(e.ctx, res.Pair.Refresh, clientID); err != nil {
		t.Errorf("Rotation ohne Höchstdauer: %v", err)
	}

	n, err := store.RevokeClient(e.ctx, u.ID, clientID)
	if err != nil || n == 0 {
		t.Fatalf("RevokeClient: %d %v", n, err)
	}
	if list, _ = store.Sessions(e.ctx, u.ID); len(list) != 0 {
		t.Errorf("Sessions nach dem Beenden: %+v", list)
	}
}

// An admin lets a new domain in while the server runs; a rejected login leaves a trace, but no account.
func TestAllowlistAtRuntime(t *testing.T) {
	e := setup(t, 1)
	clientID := e.register(claudeRedirect)
	attempt := func() *url.URL { return e.login(e.authorizeURL(clientID, claudeRedirect, newPKCE(), nil)) }

	e.idp.SetClaims(map[string]any{"email": "bob@partner.de"})
	if loc := attempt(); loc.Query().Get("error") != "access_denied" {
		t.Fatalf("fremde Domain wurde nicht abgelehnt: %s", loc)
	}
	if n := countUsers(t, e); n != 0 {
		t.Errorf("abgelehnter Login hat %d Konten angelegt", n)
	}
	var reason string
	if err := e.pool.QueryRow(e.ctx, `SELECT details->>'reason' FROM audit_log WHERE action = 'login_denied'`).Scan(&reason); err != nil {
		t.Fatalf("kein Audit-Eintrag login_denied: %v", err)
	}
	if !strings.Contains(reason, "partner.de") || strings.Contains(reason, "bob@") {
		t.Errorf("Grund im Audit-Log: %q (Domain ja, Adresse nein)", reason)
	}

	if _, err := e.allow.Add(e.ctx, "domain", "partner.de", "", uuid.Nil); err != nil {
		t.Fatal(err)
	}
	if loc := attempt(); loc.Query().Get("code") == "" {
		t.Fatalf("nach der Freigabe kein Login: %s", loc)
	}
	if n := countUsers(t, e); n != 1 {
		t.Errorf("Konten nach der Freigabe: %d", n)
	}

	if err := e.allow.Remove(e.ctx, "domain", "partner.de", ""); err != nil {
		t.Fatal(err)
	}
	if loc := attempt(); loc.Query().Get("error") != "access_denied" {
		t.Errorf("nach dem Entfernen wieder abgelehnt erwartet: %s", loc)
	}
}
