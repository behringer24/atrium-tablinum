package embed

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func init() { retryBase = time.Millisecond }

func TestRegistryListsBackendsAndRejectsUnknown(t *testing.T) {
	got := strings.Join(Backends(), ",")
	if got != "bedrock,openai,tei" {
		t.Fatalf("Backends = %s", got)
	}
	err := Validate(Settings{Backend: "ollama", Dimensions: 4})
	if err == nil || !strings.Contains(err.Error(), "bedrock | openai | tei") {
		t.Fatalf("err = %v", err)
	}
	if err := Validate(Settings{Backend: "tei", URL: "http://x", Dimensions: 0}); err == nil {
		t.Fatal("dimension 0 must be rejected")
	}
}

func TestOptionsAreValidated(t *testing.T) {
	base := Settings{Backend: "tei", URL: "http://x", Dimensions: 4}
	for name, tc := range map[string]struct {
		opts map[string]string
		want string
	}{
		"unknown key":     {map[string]string{"batchsize": "4"}, "batch_size"}, // names the valid keys
		"not a number":    {map[string]string{"batch_size": "many"}, "integer"},
		"too small":       {map[string]string{"batch_size": "0"}, ">= 1"},
		"not a boolean":   {map[string]string{"normalize": "maybe"}, "neither true nor false"},
		"multiple errors": {map[string]string{"batch_size": "x", "foo": "1"}, "unknown options"},
	} {
		s := base
		s.Options = tc.opts
		if err := Validate(s); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v", name, err)
		}
	}
	s := base
	s.Options = map[string]string{"batch_size": "4", "normalize": "false", "truncate": "true"}
	if err := Validate(s); err != nil {
		t.Errorf("valid options: %v", err)
	}
	if err := Validate(Settings{Backend: "tei", Dimensions: 4}); err == nil || !strings.Contains(err.Error(), "embedding.url") {
		t.Errorf("tei without URL: %v", err)
	}
}

// capturingTEI records the inputs and the Authorization header of every /embed call.
func capturingTEI(t *testing.T, dim int) (*httptest.Server, *[][]string, *[]string) {
	var mu sync.Mutex
	var batches [][]string
	var auth []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Inputs    []string `json:"inputs"`
			Normalize bool     `json:"normalize"`
			Truncate  bool     `json:"truncate"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		mu.Lock()
		batches = append(batches, req.Inputs)
		auth = append(auth, r.Header.Get("Authorization"))
		mu.Unlock()
		out := make([][]float32, len(req.Inputs))
		for i := range out {
			out[i] = make([]float32, dim)
		}
		_ = json.NewEncoder(w).Encode(out)
	}))
	t.Cleanup(srv.Close)
	return srv, &batches, &auth
}

func TestPrefixesDistinguishQueryFromDocument(t *testing.T) {
	srv, batches, _ := capturingTEI(t, 4)
	e, err := New(Settings{Backend: "tei", URL: srv.URL, Dimensions: 4, QueryPrefix: "query: ", DocumentPrefix: "passage: "})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.Embed(context.Background(), []string{"alpha", "beta"}); err != nil {
		t.Fatal(err)
	}
	if _, err := Query(context.Background(), e, "frage"); err != nil {
		t.Fatal(err)
	}
	got := *batches
	if len(got) != 2 || got[0][0] != "passage: alpha" || got[0][1] != "passage: beta" || got[1][0] != "query: frage" {
		t.Fatalf("inputs: %v", got)
	}
}

type plainEmbedder struct{ calls int }

func (p *plainEmbedder) Embed(_ context.Context, texts []string) ([][]float32, error) {
	p.calls++
	return make([][]float32, len(texts)), nil
}
func (p *plainEmbedder) Model() string              { return "m" }
func (p *plainEmbedder) Dimensions() int            { return 1 }
func (p *plainEmbedder) Ping(context.Context) error { return nil }

func TestQueryFallsBackToEmbed(t *testing.T) {
	p := &plainEmbedder{}
	if _, err := Query(context.Background(), p, "frage"); err != nil || p.calls != 1 {
		t.Fatalf("calls=%d err=%v", p.calls, err)
	}
}

func TestTEIOptionsAndAuth(t *testing.T) {
	srv, batches, auth := capturingTEI(t, 4)
	e, err := New(Settings{Backend: "tei", URL: srv.URL, Dimensions: 4, APIKey: "secret", Options: map[string]string{"batch_size": "2"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.Embed(context.Background(), []string{"a", "b", "c", "d", "e"}); err != nil {
		t.Fatal(err)
	}
	if len(*batches) != 3 || len((*batches)[2]) != 1 {
		t.Errorf("Batches = %v", *batches)
	}
	for _, a := range *auth {
		if a != "Bearer secret" {
			t.Errorf("Authorization = %q", a)
		}
	}
}

func TestEmptyInputNeedsNoRequest(t *testing.T) {
	srv, batches, _ := capturingTEI(t, 4)
	e, _ := New(Settings{Backend: "tei", URL: srv.URL, Dimensions: 4})
	vecs, err := e.Embed(context.Background(), nil)
	if err != nil || len(vecs) != 0 || len(*batches) != 0 {
		t.Fatalf("vecs=%v err=%v requests=%d", vecs, err, len(*batches))
	}
}

func TestRetriesRateLimitsThenGivesUp(t *testing.T) {
	var mu sync.Mutex
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hits++
		n := hits
		mu.Unlock()
		if n < 3 {
			w.Header().Set("Retry-After", "0")
			http.Error(w, "too many requests with secret text", http.StatusTooManyRequests)
			return
		}
		_ = json.NewEncoder(w).Encode([][]float32{{1, 2, 3, 4}})
	}))
	defer srv.Close()
	e, _ := New(Settings{Backend: "tei", URL: srv.URL, Dimensions: 4})
	if _, err := e.Embed(context.Background(), []string{"x"}); err != nil || hits != 3 {
		t.Fatalf("after throttling: hits=%d err=%v", hits, err)
	}

	always := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "secret content", http.StatusServiceUnavailable)
	}))
	defer always.Close()
	e2, _ := New(Settings{Backend: "tei", URL: always.URL, Dimensions: 4})
	_, err := e2.Embed(context.Background(), []string{"x"})
	if err == nil || !strings.Contains(err.Error(), "503") || strings.Contains(err.Error(), "secret") {
		t.Fatalf("err = %v", err)
	}
}

func TestAuthErrorsAreNotRetriedAndAreExplained(t *testing.T) {
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		http.Error(w, "kaputt", http.StatusUnauthorized)
	}))
	defer srv.Close()
	e, _ := New(Settings{Backend: "tei", URL: srv.URL, Dimensions: 4})
	_, err := e.Embed(context.Background(), []string{"x"})
	if err == nil || !strings.Contains(err.Error(), "credentials") || hits != 1 {
		t.Fatalf("hits=%d err=%v", hits, err)
	}
}
