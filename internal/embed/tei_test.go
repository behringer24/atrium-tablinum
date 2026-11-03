package embed

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
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
