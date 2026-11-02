// Package idptest provides a minimal OpenID Connect provider for tests. It approves every
// authorization request at once and issues RS256-signed ID tokens carrying the claims set
// with SetClaims.
package idptest

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"
)

const (
	ClientID     = "idp-client"
	ClientSecret = "idp-secret"
)

type Server struct {
	*httptest.Server
	key *rsa.PrivateKey

	mu     sync.Mutex
	claims map[string]any
	codes  map[string]issued
	// Last holds the query of the most recent authorization request.
	Last url.Values
}

type issued struct {
	challenge, nonce, redirect string
}

func New(t *testing.T) *Server {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{key: key, codes: map[string]issued{}}
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", s.discovery)
	mux.HandleFunc("/authorize", s.authorize)
	mux.HandleFunc("/token", s.token)
	mux.HandleFunc("/jwks", s.jwks)
	s.Server = httptest.NewServer(mux)
	t.Cleanup(s.Close)
	s.SetClaims(nil)
	return s
}

// SetClaims replaces the claims of the next ID tokens; sub, email and email_verified
// get defaults unless overridden.
func (s *Server) SetClaims(c map[string]any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.claims = map[string]any{"sub": "user-1", "email": "anna@example.com", "email_verified": true, "name": "Anna"}
	for k, v := range c {
		if v == nil {
			delete(s.claims, k)
		} else {
			s.claims[k] = v
		}
	}
}

func (s *Server) discovery(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, map[string]any{
		"issuer": s.URL, "authorization_endpoint": s.URL + "/authorize", "token_endpoint": s.URL + "/token",
		"jwks_uri": s.URL + "/jwks", "response_types_supported": []string{"code"},
		"subject_types_supported": []string{"public"}, "id_token_signing_alg_values_supported": []string{"RS256"},
	})
}

func (s *Server) authorize(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if q.Get("client_id") != ClientID || q.Get("code_challenge_method") != "S256" || q.Get("code_challenge") == "" || q.Get("nonce") == "" {
		http.Error(w, "bad authorization request", http.StatusBadRequest)
		return
	}
	code := randomString()
	s.mu.Lock()
	s.codes[code] = issued{challenge: q.Get("code_challenge"), nonce: q.Get("nonce"), redirect: q.Get("redirect_uri")}
	s.Last = q
	s.mu.Unlock()
	u, _ := url.Parse(q.Get("redirect_uri"))
	v := u.Query()
	v.Set("code", code)
	v.Set("state", q.Get("state"))
	u.RawQuery = v.Encode()
	http.Redirect(w, r, u.String(), http.StatusFound)
}

func (s *Server) token(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	s.mu.Lock()
	info, ok := s.codes[r.Form.Get("code")]
	delete(s.codes, r.Form.Get("code"))
	claims := map[string]any{}
	for k, v := range s.claims {
		claims[k] = v
	}
	s.mu.Unlock()

	// client credentials arrive via HTTP Basic or in the form, depending on the client
	id, secret := r.Form.Get("client_id"), r.Form.Get("client_secret")
	if u, p, hasBasic := r.BasicAuth(); hasBasic {
		id, secret = u, p
	}
	sum := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
	if !ok || id != ClientID || secret != ClientSecret ||
		r.Form.Get("redirect_uri") != info.redirect || base64.RawURLEncoding.EncodeToString(sum[:]) != info.challenge {
		w.WriteHeader(http.StatusBadRequest)
		writeJSON(w, map[string]string{"error": "invalid_grant"})
		return
	}
	now := time.Now()
	claims["iss"], claims["aud"], claims["nonce"] = s.URL, ClientID, info.nonce
	claims["iat"], claims["exp"] = now.Unix(), now.Add(5*time.Minute).Unix()
	writeJSON(w, map[string]any{"access_token": "idp-access", "token_type": "Bearer", "expires_in": 300, "id_token": s.sign(claims)})
}

func (s *Server) jwks(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, map[string]any{"keys": []map[string]string{{
		"kty": "RSA", "alg": "RS256", "use": "sig", "kid": "k1",
		"n": b64(s.key.N.Bytes()), "e": b64(big.NewInt(int64(s.key.E)).Bytes()),
	}}})
}

func (s *Server) sign(claims map[string]any) string {
	header, _ := json.Marshal(map[string]string{"alg": "RS256", "typ": "JWT", "kid": "k1"})
	payload, _ := json.Marshal(claims)
	signing := b64(header) + "." + b64(payload)
	sum := sha256.Sum256([]byte(signing))
	sig, err := rsa.SignPKCS1v15(rand.Reader, s.key, crypto.SHA256, sum[:])
	if err != nil {
		panic(err)
	}
	return signing + "." + b64(sig)
}

func b64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

func randomString() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return b64(b)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
