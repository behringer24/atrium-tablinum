package embed

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestBreakerOpensAfterRepeatedFailuresAndProbesAfterCooldown(t *testing.T) {
	var hits atomic.Int32
	var failing atomic.Bool
	failing.Store(true)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/embed" {
			http.NotFound(w, r)
			return
		}
		hits.Add(1)
		if failing.Load() {
			http.Error(w, "busy", http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte("[[0,0,0,0]]"))
	}))
	t.Cleanup(srv.Close)
	e, err := New(Settings{Backend: "tei", URL: srv.URL, Dimensions: 4})
	if err != nil {
		t.Fatal(err)
	}
	w := e.(*wrapped)
	now := time.Now()
	w.br.now = func() time.Time { return now }
	ctx := context.Background()

	for i := 0; i < breakerFailures; i++ {
		if _, err := e.Embed(ctx, []string{"x"}); err == nil || errors.Is(err, ErrUnavailable) {
			t.Fatalf("request %d: %v", i, err)
		}
	}
	if !e.(StatusReporter).Circuit().Open {
		t.Fatal("breaker did not open")
	}
	before := hits.Load()
	if _, err := e.Embed(ctx, []string{"x"}); !errors.Is(err, ErrUnavailable) || hits.Load() != before {
		t.Fatalf("open breaker: err=%v, requests %d -> %d", err, before, hits.Load())
	}

	// After the cooldown one test request goes out; it fails, so the breaker stays open.
	now = now.Add(breakerCooldown + time.Second)
	if _, err := e.Embed(ctx, []string{"x"}); err == nil || errors.Is(err, ErrUnavailable) {
		t.Fatalf("probe: %v", err)
	}
	if _, err := e.Embed(ctx, []string{"x"}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("after failed probe: %v", err)
	}

	// The next test request succeeds and closes it.
	now = now.Add(breakerCooldown + time.Second)
	failing.Store(false)
	if _, err := e.Embed(ctx, []string{"x"}); err != nil {
		t.Fatalf("probe after recovery: %v", err)
	}
	if e.(StatusReporter).Circuit().Open {
		t.Error("breaker still open after a successful request")
	}
}

// Bad credentials, bad input and a caller that gave up say nothing about the service's health.
func TestBreakerIgnoresFailuresThatAreNotOutages(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/embed" {
			http.NotFound(w, r)
			return
		}
		http.Error(w, "no", http.StatusUnauthorized)
	}))
	t.Cleanup(srv.Close)
	e, err := New(Settings{Backend: "tei", URL: srv.URL, Dimensions: 4})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < breakerFailures+2; i++ {
		if _, err := e.Embed(context.Background(), []string{"x"}); err == nil || errors.Is(err, ErrUnavailable) {
			t.Fatalf("request %d: %v", i, err)
		}
	}
	if e.(StatusReporter).Circuit().Open {
		t.Error("401 opened the breaker")
	}
	if countsAsOutage(context.Canceled) || countsAsOutage(ErrModelMismatch) || !countsAsOutage(context.DeadlineExceeded) {
		t.Error("countsAsOutage classifies wrongly")
	}
}
