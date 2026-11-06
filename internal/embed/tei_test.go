package embed

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func fakeTEI(t *testing.T, dim int, calls *[]int) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			w.WriteHeader(http.StatusOK)
		case "/embed":
			var req struct {
				Inputs []string `json:"inputs"`
			}
			_ = json.NewDecoder(r.Body).Decode(&req)
			*calls = append(*calls, len(req.Inputs))
			out := make([][]float32, len(req.Inputs))
			for i := range out {
				out[i] = make([]float32, dim)
				out[i][0] = float32(len(req.Inputs[i])) // marks the input so order can be checked
			}
			_ = json.NewEncoder(w).Encode(out)
		default:
			http.NotFound(w, r)
		}
	}))
}

func TestTEIBatchesAndKeepsOrder(t *testing.T) {
	var calls []int
	srv := fakeTEI(t, 4, &calls)
	defer srv.Close()
	e, err := New(Settings{Backend: "tei", URL: srv.URL + "/", Model: "m", Dimensions: 4})
	if err != nil {
		t.Fatal(err)
	}
	var texts []string
	for i := 0; i < 40; i++ {
		texts = append(texts, strings.Repeat("x", i+1))
	}
	vecs, err := e.Embed(context.Background(), texts)
	if err != nil {
		t.Fatal(err)
	}
	if len(vecs) != 40 {
		t.Fatalf("%d vectors", len(vecs))
	}
	for i, v := range vecs {
		if int(v[0]) != i+1 {
			t.Fatalf("order violated at %d", i)
		}
	}
	if len(calls) != 3 || calls[0] != 16 || calls[2] != 8 {
		t.Errorf("Batches = %v", calls)
	}
}

func TestTEIDimensionMismatch(t *testing.T) {
	var calls []int
	srv := fakeTEI(t, 8, &calls)
	defer srv.Close()
	e, _ := New(Settings{Backend: "tei", URL: srv.URL, Dimensions: 4})
	if _, err := e.Embed(context.Background(), []string{"a"}); err == nil || !strings.Contains(err.Error(), "dimensions") {
		t.Fatalf("err = %v", err)
	}
}

func TestTEIErrorDoesNotLeakInput(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "secret content", http.StatusBadRequest)
	}))
	defer srv.Close()
	e, _ := New(Settings{Backend: "tei", URL: srv.URL, Dimensions: 4})
	_, err := e.Embed(context.Background(), []string{"secret content"})
	if err == nil || strings.Contains(err.Error(), "secret") {
		t.Fatalf("err = %v", err)
	}
}

func TestTEIPing(t *testing.T) {
	var calls []int
	srv := fakeTEI(t, 4, &calls)
	e, _ := New(Settings{Backend: "tei", URL: srv.URL, Dimensions: 4})
	if err := e.Ping(context.Background()); err != nil {
		t.Fatal(err)
	}
	srv.Close()
	if err := e.Ping(context.Background()); err == nil {
		t.Fatal("ping after shutdown must fail")
	}
}

func TestUnknownBackend(t *testing.T) {
	if _, err := New(Settings{Backend: "ollama"}); err == nil {
		t.Fatal("unimplemented backend must return an error")
	}
}

// modelTEI serves /info with the given answer (status 200 and model_id, or another status)
// and counts the calls of /info and /embed.
func modelTEI(t *testing.T, status int, served string) (*httptest.Server, *int, *int) {
	var mu sync.Mutex
	infos, embeds := 0, 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch r.URL.Path {
		case "/health":
			w.WriteHeader(http.StatusOK)
		case "/info":
			infos++
			if status != http.StatusOK {
				w.WriteHeader(status)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"model_id": served, "max_input_length": 8192})
		case "/embed":
			embeds++
			var req struct {
				Inputs []string `json:"inputs"`
			}
			_ = json.NewDecoder(r.Body).Decode(&req)
			out := make([][]float32, len(req.Inputs))
			for i := range out {
				out[i] = make([]float32, 4)
			}
			_ = json.NewEncoder(w).Encode(out)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &infos, &embeds
}

// TEI takes its model from --model-id, not from the request: a server that serves another
// model than embedding.model names must not embed, or its vectors would be stored under the
// wrong name and look current.
func TestTEIRefusesAnotherServedModel(t *testing.T) {
	ctx := context.Background()
	srv, infos, embeds := modelTEI(t, http.StatusOK, "Qwen/Qwen3-Embedding-0.6B")
	e, err := New(Settings{Backend: "tei", URL: srv.URL, Model: "BAAI/bge-m3", Dimensions: 4})
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.Embed(ctx, []string{"a"})
	if !errors.Is(err, ErrModelMismatch) || !strings.Contains(err.Error(), "Qwen/Qwen3-Embedding-0.6B") || !strings.Contains(err.Error(), "BAAI/bge-m3") {
		t.Fatalf("embed: %v", err)
	}
	if _, err := Query(ctx, e, "frage"); !errors.Is(err, ErrModelMismatch) {
		t.Errorf("query: %v", err)
	}
	if err := e.Ping(ctx); !errors.Is(err, ErrModelMismatch) {
		t.Errorf("ping: %v", err)
	}
	if *embeds != 0 {
		t.Errorf("%d texts embedded by the wrong model", *embeds)
	}
	// the answer is kept: one /info for three calls
	if *infos != 1 {
		t.Errorf("/info asked %d times", *infos)
	}

	off, err := New(Settings{Backend: "tei", URL: srv.URL, Model: "BAAI/bge-m3", Dimensions: 4, Options: map[string]string{"check_model": "false"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := off.Embed(ctx, []string{"a"}); err != nil {
		t.Errorf("check_model=false: %v", err)
	}
}

func TestTEIAcceptsTheConfiguredOrAnUnnamedModel(t *testing.T) {
	ctx := context.Background()
	for _, c := range []struct {
		name   string
		status int
		served string
	}{
		{"same model, other case", http.StatusOK, "baai/BGE-M3"},
		{"loaded from a directory", http.StatusOK, "/repository"},
		{"no model_id", http.StatusOK, ""},
		{"no /info", http.StatusNotFound, ""},
		{"/info failing", http.StatusServiceUnavailable, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			srv, _, embeds := modelTEI(t, c.status, c.served)
			e, err := New(Settings{Backend: "tei", URL: srv.URL, Model: "BAAI/bge-m3", Dimensions: 4})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := e.Embed(ctx, []string{"a"}); err != nil || *embeds != 1 {
				t.Fatalf("embed: %v (%d calls)", err, *embeds)
			}
			if err := e.Ping(ctx); err != nil {
				t.Errorf("ping: %v", err)
			}
		})
	}
}
