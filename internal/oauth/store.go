// Package oauth implements the OAuth 2.1 authorization server of the MCP endpoint: client
// registration, authorization (login through an external IdP), token issuance and
// revocation, plus the resource-server token verifier. Secrets are stored only as SHA-256
// hashes.
package oauth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/behringer24/atrium-tablinum/internal/audit"
)

var (
	ErrInvalidGrant = errors.New("invalid_grant")
	ErrNotFound     = errors.New("nicht gefunden")
)

const (
	loginStateTTL = 10 * time.Minute
	codeTTL       = 2 * time.Minute
	maxClients    = 5000
	// ErrTooManyClients is returned when dynamic registration is saturated.
)

var ErrTooManyClients = errors.New("zu viele registrierte clients")

type Store struct {
	pool       *pgxpool.Pool
	accessTTL  time.Duration
	refreshTTL time.Duration
	// maxSession is the absolute lifetime of a session, however often it is refreshed; 0 = none.
	maxSession time.Duration
}

// WithSessionMaxAge sets the absolute session lifetime: after it the user has to log in again.
func (s *Store) WithSessionMaxAge(d time.Duration) *Store {
	s.maxSession = d
	return s
}

func NewStore(pool *pgxpool.Pool, accessTTL, refreshTTL time.Duration) *Store {
	return &Store{pool: pool, accessTTL: accessTTL, refreshTTL: refreshTTL}
}

func hash(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func randomToken(prefix string) string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err) // the OS entropy source failing is unrecoverable
	}
	return prefix + base64.RawURLEncoding.EncodeToString(b)
}

// ---- clients -------------------------------------------------------------------

type Client struct {
	ID           string
	Name         string
	RedirectURIs []string
}

func (s *Store) CreateClient(ctx context.Context, name string, redirectURIs []string) (Client, error) {
	var n int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM oauth_clients`).Scan(&n); err != nil {
		return Client{}, err
	}
	if n >= maxClients {
		return Client{}, ErrTooManyClients
	}
	c := Client{ID: randomToken("c_"), Name: name, RedirectURIs: redirectURIs}
	_, err := s.pool.Exec(ctx, `INSERT INTO oauth_clients (client_id, client_name, redirect_uris) VALUES ($1, $2, $3)`,
		c.ID, name, redirectURIs)
	return c, err
}

func (s *Store) GetClient(ctx context.Context, id string) (Client, error) {
	var c Client
	var name *string
	err := s.pool.QueryRow(ctx, `SELECT client_id, client_name, redirect_uris FROM oauth_clients WHERE client_id = $1`, id).
		Scan(&c.ID, &name, &c.RedirectURIs)
	if errors.Is(err, pgx.ErrNoRows) {
		return Client{}, ErrNotFound
	}
	if name != nil {
		c.Name = *name
	}
	return c, err
}

// ---- login state (authorization request while the user logs in at the IdP) -------

type LoginState struct {
	ClientID      string
	RedirectURI   string
	CodeChallenge string
	ClientState   string
	Resource      string
	IDP           string
	IDPNonce      string
	IDPVerifier   string
}

// CreateLoginState stores a pending authorization request and returns the random state
// value that travels through the IdP.
func (s *Store) CreateLoginState(ctx context.Context, st LoginState) (string, error) {
	raw := randomToken("")
	_, err := s.pool.Exec(ctx, `
INSERT INTO oauth_login_states (state_hash, client_id, redirect_uri, code_challenge, client_state, resource, expires_at)
VALUES ($1, $2, $3, $4, NULLIF($5, ''), $6, $7)`,
		hash(raw), st.ClientID, st.RedirectURI, st.CodeChallenge, st.ClientState, st.Resource, time.Now().Add(loginStateTTL))
	return raw, err
}

// BindLoginState attaches the chosen IdP with its nonce and PKCE verifier. It succeeds
// only once per state, so a leaked chooser link cannot be re-pointed at another IdP.
func (s *Store) BindLoginState(ctx context.Context, raw, idp, nonce, verifier string) (LoginState, error) {
	return s.scanLogin(s.pool.QueryRow(ctx, `
UPDATE oauth_login_states SET idp = $2, idp_nonce = $3, idp_verifier = $4
WHERE state_hash = $1 AND idp IS NULL AND expires_at > now()
RETURNING client_id, redirect_uri, code_challenge, COALESCE(client_state, ''), resource, COALESCE(idp, ''), COALESCE(idp_nonce, ''), COALESCE(idp_verifier, '')`,
		hash(raw), idp, nonce, verifier))
}

// TakeLoginState consumes the state (single use).
func (s *Store) TakeLoginState(ctx context.Context, raw string) (LoginState, error) {
	return s.scanLogin(s.pool.QueryRow(ctx, `
DELETE FROM oauth_login_states WHERE state_hash = $1 AND expires_at > now()
RETURNING client_id, redirect_uri, code_challenge, COALESCE(client_state, ''), resource, COALESCE(idp, ''), COALESCE(idp_nonce, ''), COALESCE(idp_verifier, '')`,
		hash(raw)))
}

func (s *Store) scanLogin(row pgx.Row) (LoginState, error) {
	var st LoginState
	err := row.Scan(&st.ClientID, &st.RedirectURI, &st.CodeChallenge, &st.ClientState, &st.Resource, &st.IDP, &st.IDPNonce, &st.IDPVerifier)
	if errors.Is(err, pgx.ErrNoRows) {
		return LoginState{}, ErrNotFound
	}
	return st, err
}

// ---- authorization codes -------------------------------------------------------------

type Code struct {
	ClientID      string
	UserID        uuid.UUID
	RedirectURI   string
	CodeChallenge string
	Resource      string
}

func (s *Store) CreateCode(ctx context.Context, c Code) (string, error) {
	raw := randomToken("")
	_, err := s.pool.Exec(ctx, `
INSERT INTO oauth_codes (code_hash, client_id, user_id, redirect_uri, code_challenge, resource, expires_at)
VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		hash(raw), c.ClientID, c.UserID, c.RedirectURI, c.CodeChallenge, c.Resource, time.Now().Add(codeTTL))
	return raw, err
}

// TakeCode consumes an authorization code (single use, also when later checks fail).
func (s *Store) TakeCode(ctx context.Context, raw string) (Code, error) {
	var c Code
	err := s.pool.QueryRow(ctx, `
DELETE FROM oauth_codes WHERE code_hash = $1 AND expires_at > now()
RETURNING client_id, user_id, redirect_uri, code_challenge, resource`, hash(raw)).
		Scan(&c.ClientID, &c.UserID, &c.RedirectURI, &c.CodeChallenge, &c.Resource)
	if errors.Is(err, pgx.ErrNoRows) {
		return Code{}, ErrInvalidGrant
	}
	return c, err
}

// ---- tokens ------------------------------------------------------------------------------

type TokenPair struct {
	Access     string
	Refresh    string
	AccessTTL  time.Duration
	RefreshTTL time.Duration
}

type tx interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

func (s *Store) insertPair(ctx context.Context, q tx, clientID string, userID uuid.UUID, resource string, started time.Time) (TokenPair, error) {
	p := TokenPair{Access: randomToken("at_"), Refresh: randomToken("rt_"), AccessTTL: s.accessTTL, RefreshTTL: s.refreshTTL}
	now := time.Now()
	for _, t := range []struct {
		raw, kind string
		ttl       time.Duration
	}{{p.Access, "access", s.accessTTL}, {p.Refresh, "refresh", s.refreshTTL}} {
		if _, err := q.Exec(ctx, `INSERT INTO oauth_tokens (token_hash, kind, client_id, user_id, resource, expires_at, session_started_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7)`, hash(t.raw), t.kind, clientID, userID, resource, now.Add(t.ttl), started); err != nil {
			return TokenPair{}, err
		}
	}
	return p, nil
}

func (s *Store) IssueTokens(ctx context.Context, clientID string, userID uuid.UUID, resource string) (TokenPair, error) {
	return s.insertPair(ctx, s.pool, clientID, userID, resource, time.Now())
}

// RefreshResult carries the new pair and whom it belongs to.
type RefreshResult struct {
	Pair   TokenPair
	UserID uuid.UUID
}

// Rotate exchanges a refresh token for a new pair and revokes the old one. Presenting an
// already used refresh token signals theft: every token of that user and client is
// revoked and the request fails.
func (s *Store) Rotate(ctx context.Context, rawRefresh, clientID string) (RefreshResult, error) {
	t, err := s.pool.Begin(ctx)
	if err != nil {
		return RefreshResult{}, err
	}
	defer t.Rollback(ctx)

	var (
		owner, resource  string
		userID           uuid.UUID
		expires, started time.Time
		revoked          *time.Time
	)
	err = t.QueryRow(ctx, `SELECT client_id, user_id, resource, expires_at, revoked_at, session_started_at FROM oauth_tokens
		WHERE token_hash = $1 AND kind = 'refresh' FOR UPDATE`, hash(rawRefresh)).Scan(&owner, &userID, &resource, &expires, &revoked, &started)
	if errors.Is(err, pgx.ErrNoRows) {
		return RefreshResult{}, ErrInvalidGrant
	}
	if err != nil {
		return RefreshResult{}, err
	}
	if owner != clientID {
		return RefreshResult{}, ErrInvalidGrant
	}
	if revoked != nil {
		if _, err := t.Exec(ctx, `UPDATE oauth_tokens SET revoked_at = now()
			WHERE user_id = $1 AND client_id = $2 AND revoked_at IS NULL`, userID, clientID); err != nil {
			return RefreshResult{}, err
		}
		if err := t.Commit(ctx); err != nil {
			return RefreshResult{}, err
		}
		return RefreshResult{}, ErrInvalidGrant
	}
	if !expires.After(time.Now()) || (s.maxSession > 0 && time.Since(started) > s.maxSession) {
		return RefreshResult{}, ErrInvalidGrant
	}
	if _, err := t.Exec(ctx, `UPDATE oauth_tokens SET revoked_at = now() WHERE token_hash = $1`, hash(rawRefresh)); err != nil {
		return RefreshResult{}, err
	}
	pair, err := s.insertPair(ctx, t, clientID, userID, resource, started)
	if err != nil {
		return RefreshResult{}, err
	}
	return RefreshResult{Pair: pair, UserID: userID}, t.Commit(ctx)
}

// AccessInfo describes a valid access token.
type AccessInfo struct {
	UserID   uuid.UUID
	Resource string
	Expires  time.Time
}

// LookupAccess returns the token's owner if it is a live access token.
func (s *Store) LookupAccess(ctx context.Context, raw string) (AccessInfo, error) {
	var a AccessInfo
	err := s.pool.QueryRow(ctx, `SELECT user_id, resource, expires_at FROM oauth_tokens
		WHERE token_hash = $1 AND kind = 'access' AND revoked_at IS NULL AND expires_at > now()`, hash(raw)).
		Scan(&a.UserID, &a.Resource, &a.Expires)
	if errors.Is(err, pgx.ErrNoRows) {
		return AccessInfo{}, ErrNotFound
	}
	if err == nil {
		// best effort and throttled, so busy sessions do not write on every request
		_, _ = s.pool.Exec(ctx, `UPDATE oauth_tokens SET last_used_at = now()
			WHERE token_hash = $1 AND (last_used_at IS NULL OR last_used_at < now() - interval '1 minute')`, hash(raw))
	}
	return a, err
}

// Revoke invalidates a token of the given client (RFC 7009). Unknown tokens are ignored.
// Revoking a refresh token also revokes the user's access tokens of that client.
func (s *Store) Revoke(ctx context.Context, raw, clientID string) error {
	var userID uuid.UUID
	var kind string
	err := s.pool.QueryRow(ctx, `UPDATE oauth_tokens SET revoked_at = COALESCE(revoked_at, now())
		WHERE token_hash = $1 AND client_id = $2 RETURNING user_id, kind`, hash(raw), clientID).Scan(&userID, &kind)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if kind == "refresh" {
		_, err = s.pool.Exec(ctx, `UPDATE oauth_tokens SET revoked_at = now()
			WHERE user_id = $1 AND client_id = $2 AND kind = 'access' AND revoked_at IS NULL`, userID, clientID)
	}
	return err
}

// Session is one client a user is logged in with.
type Session struct {
	ClientID   string
	ClientName string
	Started    time.Time
	LastUsed   *time.Time
}

// Sessions lists the clients that hold a live refresh token of the user, newest first.
func (s *Store) Sessions(ctx context.Context, userID uuid.UUID) ([]Session, error) {
	rows, err := s.pool.Query(ctx, `
SELECT t.client_id, COALESCE(c.client_name, ''), min(t.session_started_at),
       (SELECT max(u.last_used_at) FROM oauth_tokens u WHERE u.user_id = $1 AND u.client_id = t.client_id)
FROM oauth_tokens t JOIN oauth_clients c ON c.client_id = t.client_id
WHERE t.user_id = $1 AND t.kind = 'refresh' AND t.revoked_at IS NULL AND t.expires_at > now()
GROUP BY t.client_id, c.client_name
ORDER BY min(t.session_started_at) DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Session
	for rows.Next() {
		var x Session
		if err := rows.Scan(&x.ClientID, &x.ClientName, &x.Started, &x.LastUsed); err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

// RevokeClient ends the sessions of one client of the user (all its tokens).
func (s *Store) RevokeClient(ctx context.Context, userID uuid.UUID, clientID string) (int64, error) {
	tag, err := s.pool.Exec(ctx, `UPDATE oauth_tokens SET revoked_at = now()
		WHERE user_id = $1 AND client_id = $2 AND revoked_at IS NULL`, userID, clientID)
	return tag.RowsAffected(), err
}

// RevokeUser revokes all tokens of a user (offboarding, "revoke sessions").
func (s *Store) RevokeUser(ctx context.Context, userID uuid.UUID) (int64, error) {
	tag, err := s.pool.Exec(ctx, `UPDATE oauth_tokens SET revoked_at = now() WHERE user_id = $1 AND revoked_at IS NULL`, userID)
	return tag.RowsAffected(), err
}

// Cleanup removes expired codes and login states, tokens a week past their end, and
// clients that never completed a login.
func (s *Store) Cleanup(ctx context.Context) error {
	for _, q := range []string{
		`DELETE FROM oauth_codes WHERE expires_at < now()`,
		`DELETE FROM oauth_login_states WHERE expires_at < now()`,
		`DELETE FROM oauth_tokens WHERE expires_at < now() - interval '7 days'`,
		`DELETE FROM oauth_clients c WHERE c.created_at < now() - interval '1 day'
			AND NOT EXISTS (SELECT 1 FROM oauth_tokens t WHERE t.client_id = c.client_id)
			AND NOT EXISTS (SELECT 1 FROM oauth_codes k WHERE k.client_id = c.client_id)
			AND NOT EXISTS (SELECT 1 FROM oauth_login_states l WHERE l.client_id = c.client_id)`,
	} {
		if _, err := s.pool.Exec(ctx, q); err != nil {
			return err
		}
	}
	return nil
}

// RecordLogin writes the audit entry for a completed login.
func (s *Store) RecordLogin(ctx context.Context, userID uuid.UUID, provider string) {
	if err := audit.Insert(ctx, s.pool, audit.Event{UserID: &userID, Client: "system", Action: "login",
		Details: map[string]any{"provider": provider}}); err != nil {
		slog.Error("Audit-Eintrag für Login fehlgeschlagen", "error", err)
	}
}
