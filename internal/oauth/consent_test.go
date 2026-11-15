package oauth_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/behringer24/atrium-tablinum/internal/oauth"
)

func (e *env) rowCount(table string) int {
	e.t.Helper()
	var n int
	if err := e.pool.QueryRow(e.ctx, `SELECT count(*) FROM `+table).Scan(&n); err != nil {
		e.t.Fatal(err)
	}
	return n
}

// A client the user has not allowed yet gets no code: the user sees who is asking, where they are
// sent back to and as whom they are signed in, and nothing is issued before they answer.
func TestConsentPageAsksBeforeACodeIsIssued(t *testing.T) {
	e := setup(t, 1)
	clientID := e.register(claudeRedirect)
	resp, body := e.loginToCallback(e.authorizeURL(clientID, claudeRedirect, newPKCE(), nil))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("callback for a new client: %d %s", resp.StatusCode, body)
	}
	for _, want := range []string{"Allow this connection?", "Claude", "claude.ai", "anna@example.com", `value="allow"`, `value="deny"`} {
		if !strings.Contains(body, want) {
			t.Errorf("consent page lacks %q: %s", want, body)
		}
	}
	if e.rowCount("oauth_codes") != 0 || e.rowCount("oauth_tokens") != 0 {
		t.Error("a code or token exists before the user answered")
	}
	h := resp.Header
	if h.Get("Cache-Control") != "no-store" || h.Get("X-Frame-Options") != "DENY" {
		t.Errorf("consent page headers: %v", h)
	}
	// The browser applies form-action to the redirect that answers the form too.
	if csp := h.Get("Content-Security-Policy"); !strings.Contains(csp, "form-action 'self' https://claude.ai") || strings.Contains(csp, "script-src") {
		t.Errorf("CSP of the consent page: %q", csp)
	}
}

func TestConsentAllowIssuesTheCodeAndIsRemembered(t *testing.T) {
	e := setup(t, 1)
	clientID := e.register(claudeRedirect)
	p := newPKCE()
	_, body := e.loginToCallback(e.authorizeURL(clientID, claudeRedirect, p, nil))
	resp, body := e.answerConsent(e.consentRequest(body), "allow")
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("allow: %d %s", resp.StatusCode, body)
	}
	loc, _ := url.Parse(resp.Header.Get("Location"))
	if loc.Query().Get("state") != "client-state" || loc.Query().Get("iss") != e.http.URL {
		t.Errorf("redirect parameters: %v", loc.Query())
	}
	if r, m := e.exchange(clientID, claudeRedirect, loc.Query().Get("code"), p.verifier); r.StatusCode != http.StatusOK {
		t.Fatalf("token after consent: %d %v", r.StatusCode, m)
	}
	// the second login of the same user with the same client does not ask again
	resp, body = e.loginToCallback(e.authorizeURL(clientID, claudeRedirect, newPKCE(), nil))
	if resp.StatusCode != http.StatusFound || !strings.Contains(resp.Header.Get("Location"), "code=") {
		t.Fatalf("second login asked again: %d %s", resp.StatusCode, body)
	}
	// another client asks, even with the same name
	other := e.register(claudeRedirect)
	if resp, _ = e.loginToCallback(e.authorizeURL(other, claudeRedirect, newPKCE(), nil)); resp.StatusCode != http.StatusOK {
		t.Errorf("a new client with the same name was not asked: %d", resp.StatusCode)
	}
}

func TestConsentDenyGivesNoCodeAndIsAudited(t *testing.T) {
	e := setup(t, 1)
	clientID := e.register(claudeRedirect)
	_, body := e.loginToCallback(e.authorizeURL(clientID, claudeRedirect, newPKCE(), nil))
	resp, body := e.answerConsent(e.consentRequest(body), "deny")
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("deny: %d %s", resp.StatusCode, body)
	}
	loc, _ := url.Parse(resp.Header.Get("Location"))
	if loc.Query().Get("error") != "access_denied" || loc.Query().Get("code") != "" || loc.Query().Get("state") != "client-state" {
		t.Errorf("deny redirect: %v", loc.Query())
	}
	if e.rowCount("oauth_codes") != 0 || e.rowCount("oauth_consents") != 0 {
		t.Error("a denied client got a code or a remembered consent")
	}
	var n int
	if err := e.pool.QueryRow(e.ctx, `SELECT count(*) FROM audit_log WHERE action = 'client_consent_denied' AND details->>'client_id' = $1`, clientID).Scan(&n); err != nil || n != 1 {
		t.Errorf("audit entries for the denial: %d %v", n, err)
	}
	// and the next try asks again
	resp, body = e.loginToCallback(e.authorizeURL(clientID, claudeRedirect, newPKCE(), nil))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("a denied client was not asked again: %d", resp.StatusCode)
	}
	// anything but "allow" counts as no
	resp, _ = e.do(http.MethodPost, "/oauth/consent", url.Values{"r": {e.consentRequest(body)}}, nil)
	if loc, _ := url.Parse(resp.Header.Get("Location")); loc.Query().Get("error") != "access_denied" {
		t.Errorf("an answer without a decision: %d %v", resp.StatusCode, loc)
	}
}

func TestConsentAllowIsAudited(t *testing.T) {
	e := setup(t, 1)
	clientID := e.register(claudeRedirect)
	e.login(e.authorizeURL(clientID, claudeRedirect, newPKCE(), nil))
	var name, host string
	if err := e.pool.QueryRow(e.ctx, `SELECT details->>'client_name', details->>'redirect_host' FROM audit_log
		WHERE action = 'client_consent' AND details->>'client_id' = $1`, clientID).Scan(&name, &host); err != nil {
		t.Fatalf("audit entry for the consent: %v", err)
	}
	if name != "Claude" || host != "claude.ai" {
		t.Errorf("audit details: %q %q", name, host)
	}
}

// The request value is the only thing that ties an answer to the login that asked: it is random,
// works once, expires, and the page can only be answered by POST.
func TestConsentRequestIsSingleUseAndExpires(t *testing.T) {
	e := setup(t, 1)
	clientID := e.register(claudeRedirect)
	_, body := e.loginToCallback(e.authorizeURL(clientID, claudeRedirect, newPKCE(), nil))
	req := e.consentRequest(body)

	if resp, _ := e.answerConsent("not-a-request", "allow"); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("unknown request value: %d", resp.StatusCode)
	}
	if resp, _ := e.answerConsent("", "allow"); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("empty request value: %d", resp.StatusCode)
	}
	if resp, _ := e.do(http.MethodGet, "/oauth/consent?r="+url.QueryEscape(req)+"&decision=allow", nil, nil); resp.StatusCode == http.StatusFound {
		t.Error("a GET answered the consent page")
	}
	if e.rowCount("oauth_codes") != 0 {
		t.Fatal("a code exists after answers that were not valid")
	}
	if resp, _ := e.answerConsent(req, "allow"); resp.StatusCode != http.StatusFound {
		t.Fatalf("the real answer: %d", resp.StatusCode)
	}
	if resp, _ := e.answerConsent(req, "allow"); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("replay of an answered request: %d", resp.StatusCode)
	}
	if e.rowCount("oauth_codes") != 1 {
		t.Errorf("the replay issued another code: %d codes", e.rowCount("oauth_codes"))
	}

	// an expired question cannot be answered, and the cleanup removes it
	_, body = e.loginToCallback(e.authorizeURL(e.register(claudeRedirect), claudeRedirect, newPKCE(), nil))
	req = e.consentRequest(body)
	if _, err := e.pool.Exec(e.ctx, `UPDATE oauth_consent_requests SET expires_at = now() - interval '1 minute'`); err != nil {
		t.Fatal(err)
	}
	if resp, _ := e.answerConsent(req, "allow"); resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expired request: %d", resp.StatusCode)
	}
	if e.rowCount("oauth_consent_requests") != 1 {
		t.Fatal("the expired request should still be there before the cleanup")
	}
	if err := e.srv.Cleanup(e.ctx); err != nil || e.rowCount("oauth_consent_requests") != 0 {
		t.Errorf("cleanup left requests: %v", err)
	}
}

// A user who was locked between the page and the answer gets no code.
func TestConsentOfAnAccountLockedMeanwhile(t *testing.T) {
	e := setup(t, 1)
	clientID := e.register(claudeRedirect)
	_, body := e.loginToCallback(e.authorizeURL(clientID, claudeRedirect, newPKCE(), nil))
	u, _ := e.users.GetByEmail(e.ctx, "anna@example.com")
	if err := e.users.SetActive(e.ctx, u.ID, false); err != nil {
		t.Fatal(err)
	}
	resp, _ := e.answerConsent(e.consentRequest(body), "allow")
	if loc, _ := url.Parse(resp.Header.Get("Location")); loc.Query().Get("error") != "access_denied" || e.rowCount("oauth_codes") != 0 || e.rowCount("oauth_consents") != 0 {
		t.Errorf("answer of a locked account: %d %v", resp.StatusCode, loc)
	}
}

// What the application calls itself is its own choice: the page shows it as text, without markup
// and without characters that reorder or hide what is shown.
func TestConsentPageShowsTheClientNameAsPlainText(t *testing.T) {
	e := setup(t, 1)
	b, _ := json.Marshal(map[string]any{"redirect_uris": []string{claudeRedirect},
		"client_name": "<script>alert(1)</script>‮ Trusted\nCorp"})
	_, m := e.jsonPost("/oauth/register", string(b))
	_, body := e.loginToCallback(e.authorizeURL(m["client_id"].(string), claudeRedirect, newPKCE(), nil))
	if strings.Contains(body, "<script>") || strings.ContainsRune(body, '‮') || strings.Contains(body, "Trusted\nCorp") {
		t.Errorf("client name not made safe: %s", body)
	}
	if !strings.Contains(body, "&lt;script&gt;") || !strings.Contains(body, "Trusted Corp") {
		t.Errorf("client name not shown as text: %s", body)
	}
	// a client without a name is shown as such
	id := e.register(claudeRedirect)
	if _, err := e.pool.Exec(e.ctx, `UPDATE oauth_clients SET client_name = NULL WHERE client_id = $1`, id); err != nil {
		t.Fatal(err)
	}
	if _, body = e.loginToCallback(e.authorizeURL(id, claudeRedirect, newPKCE(), nil)); !strings.Contains(body, "(no name given)") {
		t.Errorf("unnamed client: %s", body)
	}
}

// Whoever cuts a client off makes it ask again: ending its sessions, ending all sessions, the
// incident switch and the detection of a stolen refresh token all forget the consent.
func TestConsentIsForgottenWhenAClientIsCutOff(t *testing.T) {
	e := setup(t, 1)
	store := oauth.NewStore(e.pool, time.Hour, 720*time.Hour)
	clientID, _ := e.tokens()
	u, _ := e.users.GetByEmail(e.ctx, "anna@example.com")
	asks := func(when string) {
		t.Helper()
		if resp, _ := e.loginToCallback(e.authorizeURL(clientID, claudeRedirect, newPKCE(), nil)); resp.StatusCode != http.StatusOK {
			t.Errorf("%s: the client was not asked again (%d)", when, resp.StatusCode)
		}
	}
	grant := func() {
		t.Helper()
		if err := store.GrantConsent(e.ctx, u.ID, clientID); err != nil {
			t.Fatal(err)
		}
	}
	if ok, _ := store.HasConsent(e.ctx, u.ID, clientID); !ok {
		t.Fatal("the full flow left no consent")
	}

	if _, err := store.RevokeClient(e.ctx, u.ID, clientID); err != nil {
		t.Fatal(err)
	}
	asks("after the user ended the client")

	grant()
	if _, err := store.RevokeUser(e.ctx, u.ID); err != nil {
		t.Fatal(err)
	}
	asks("after the sessions of the user were ended")

	grant()
	if _, err := store.RevokeAll(e.ctx); err != nil {
		t.Fatal(err)
	}
	asks("after all sessions were ended")

	// reuse of a refresh token: the client is cut off and has to ask again
	clientID, tok := e.tokens()
	grant()
	refresh := tok["refresh_token"].(string)
	if _, err := store.Rotate(e.ctx, refresh, clientID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Rotate(e.ctx, refresh, clientID); !errors.Is(err, oauth.ErrInvalidGrant) {
		t.Fatalf("reuse of a refresh token: %v", err)
	}
	asks("after a refresh token was used twice")
}

// A client that has a pending question or a consent is in use: the hourly cleanup of unused
// registrations must not remove it.
func TestUnusedClientCleanupKeepsClientsWithConsent(t *testing.T) {
	e := setup(t, 1)
	pending := e.register(claudeRedirect)
	e.loginToCallback(e.authorizeURL(pending, claudeRedirect, newPKCE(), nil))
	allowed := e.register(claudeRedirect)
	u, _ := e.users.GetByEmail(e.ctx, "anna@example.com")
	if err := oauth.NewStore(e.pool, time.Hour, 720*time.Hour).GrantConsent(e.ctx, u.ID, allowed); err != nil {
		t.Fatal(err)
	}
	unused := e.register(claudeRedirect)
	if _, err := e.pool.Exec(e.ctx, `UPDATE oauth_clients SET created_at = now() - interval '2 hours'`); err != nil {
		t.Fatal(err)
	}
	if _, err := e.pool.Exec(e.ctx, `DELETE FROM oauth_login_states`); err != nil {
		t.Fatal(err)
	}
	if err := e.srv.Cleanup(e.ctx); err != nil {
		t.Fatal(err)
	}
	for id, want := range map[string]bool{pending: true, allowed: true, unused: false} {
		var n int
		_ = e.pool.QueryRow(e.ctx, `SELECT count(*) FROM oauth_clients WHERE client_id = $1`, id).Scan(&n)
		if (n == 1) != want {
			t.Errorf("client %s kept=%v, want %v", id, n == 1, want)
		}
	}
}
