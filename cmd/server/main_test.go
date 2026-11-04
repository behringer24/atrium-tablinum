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
