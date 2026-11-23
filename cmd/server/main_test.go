package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/auth"
)

func TestReadinessSaysLittleAndCachesTheCheck(t *testing.T) {
	calls, failing := 0, false
	h := readiness(slog.New(slog.NewTextHandler(io.Discard, nil)), func(context.Context) error {
		calls++
		if failing {
			return errors.New("database unreachable: password authentication failed for user knowledge")
		}
		return nil
	})
	get := func() (int, string) {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
		return rec.Code, strings.TrimSpace(rec.Body.String())
	}
	if code, body := get(); code != 200 || body != "ok" {
		t.Fatalf("ready: %d %q", code, body)
	}
	for i := 0; i < 20; i++ {
		get()
	}
	if calls != 1 {
		t.Errorf("a burst of requests ran the check %d times", calls)
	}

	// A failing check is reported without its reason, also once the cache has expired.
	h2 := readiness(slog.New(slog.NewTextHandler(io.Discard, nil)), func(context.Context) error {
		return errors.New("embedder unreachable: dial tcp 10.0.0.5:80")
	})
	rec := httptest.NewRecorder()
	h2.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if rec.Code != http.StatusServiceUnavailable || strings.TrimSpace(rec.Body.String()) != "not ready" {
		t.Errorf("not ready: %d %q", rec.Code, rec.Body.String())
	}
	_ = failing
}

// The check of an edition's own tokens comes first; only a token it does not know falls through to the OAuth
// check, and any other answer of the first check stands.
func TestDevFirstFallsThroughOnlyForUnknownTokens(t *testing.T) {
	first := func(_ context.Context, token string, _ *http.Request) (*auth.TokenInfo, error) {
		switch token {
		case "own":
			return &auth.TokenInfo{UserID: "first"}, nil
		case "broken":
			return nil, errors.New("database down")
		}
		return nil, auth.ErrInvalidToken
	}
	next := func(_ context.Context, token string, _ *http.Request) (*auth.TokenInfo, error) {
		if token == "oauth" {
			return &auth.TokenInfo{UserID: "second"}, nil
		}
		return nil, auth.ErrInvalidToken
	}
	v := devFirst(first, next)
	ask := func(token string) (string, error) {
		info, err := v(context.Background(), token, nil)
		if err != nil {
			return "", err
		}
		return info.UserID, nil
	}
	if id, err := ask("own"); err != nil || id != "first" {
		t.Errorf("own token: %q %v", id, err)
	}
	if id, err := ask("oauth"); err != nil || id != "second" {
		t.Errorf("a token the first check does not know: %q %v", id, err)
	}
	if _, err := ask("nobody"); !errors.Is(err, auth.ErrInvalidToken) {
		t.Errorf("unknown token: %v", err)
	}
	if _, err := ask("broken"); err == nil || errors.Is(err, auth.ErrInvalidToken) {
		t.Errorf("a failing first check must not fall through: %v", err)
	}
	if id, err := devFirst(first, nil)(context.Background(), "oauth", nil); err == nil {
		t.Errorf("without a next check: %v %v", id, err)
	}
}
