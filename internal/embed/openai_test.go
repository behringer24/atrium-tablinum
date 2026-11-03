package embed

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

type openAICall struct {
	Auth, Org string
	Path      string
	Body      map[string]any
}

// fakeOpenAI answers like the OpenAI embeddings API, but returns the vectors in reverse
// order (the client has to use the index field). Each vector starts with the text length.
func fakeOpenAI(t *testing.T, dim int) (*httptest.Server, *[]openAICall) {
	var mu sync.Mutex
	var calls []openAICall
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		calls = append(calls, openAICall{r.Header.Get("Authorization"), r.Header.Get("OpenAI-Organization"), r.URL.Path, body})
		mu.Unlock()
		inputs, _ := body["input"].([]any)
		type item struct {
			Index     int       `json:"index"`
			Embedding []float32 `json:"embedding"`
		}
		data := make([]item, len(inputs))
		for i, in := range inputs {
			v := make([]float32, dim)
			v[0] = float32(len(in.(string)))
			data[len(inputs)-1-i] = item{Index: i, Embedding: v}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

func TestOpenAIRequestAndOrder(t *testing.T) {
	srv, calls := fakeOpenAI(t, 4)
	e, err := New(Settings{Backend: "openai", URL: srv.URL + "/v1/", APIKey: "sk-test", Model: "text-embedding-3-small", Dimensions: 4,
		Options: map[string]string{"batch_size": "2", "send_dimensions": "true", "organization": "org-1"}})
	if err != nil {
		t.Fatal(err)
	}
	texts := []string{"a", "bb", "ccc", "dddd", "eeeee"}
	vecs, err := e.Embed(context.Background(), texts)
	if err != nil {
		t.Fatal(err)
	}
	for i, v := range vecs {
		if int(v[0]) != len(texts[i]) {
			t.Fatalf("order violated at %d: %v", i, v)
		}
	}
	if len(*calls) != 3 {
		t.Fatalf("%d requests", len(*calls))
	}
	c := (*calls)[0]
	if c.Path != "/v1/embeddings" || c.Auth != "Bearer sk-test" || c.Org != "org-1" ||
		c.Body["model"] != "text-embedding-3-small" || c.Body["dimensions"] != float64(4) || c.Body["encoding_format"] != "float" {
		t.Errorf("request: %+v", c)
	}
}

func TestOpenAIDimensionsOnlyWhenAsked(t *testing.T) {
	srv, calls := fakeOpenAI(t, 4)
	e, _ := New(Settings{Backend: "openai", URL: srv.URL, Model: "m", Dimensions: 4})
	if _, err := e.Embed(context.Background(), []string{"a"}); err != nil {
		t.Fatal(err)
	}
	c := (*calls)[0]
	if _, has := c.Body["dimensions"]; has || c.Auth != "" || c.Path != "/embeddings" {
		t.Errorf("request: %+v", c)
	}
}

func TestOpenAIValidation(t *testing.T) {
	for name, tc := range map[string]struct {
		s    Settings
		want string
	}{
		"official API needs a key": {Settings{Backend: "openai", Model: "m", Dimensions: 4}, "api_key"},
		"model missing":            {Settings{Backend: "openai", APIKey: "k", Dimensions: 4}, "embedding.model"},
		"URL invalid":              {Settings{Backend: "openai", URL: "nope", Model: "m", Dimensions: 4}, "http(s)"},
		"unknown option":           {Settings{Backend: "openai", APIKey: "k", Model: "m", Dimensions: 4, Options: map[string]string{"region": "x"}}, "unknown options"},
	} {
		if err := Validate(tc.s); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v", name, err)
		}
	}
	// a custom endpoint (Ollama, vLLM, ...) needs no key
	if err := Validate(Settings{Backend: "openai", URL: "http://localhost:11434/v1", Model: "m", Dimensions: 4}); err != nil {
		t.Errorf("custom endpoint: %v", err)
	}
}

func TestOpenAIPingChecksDimensionAndIsCached(t *testing.T) {
	srv, calls := fakeOpenAI(t, 4)
	e, _ := New(Settings{Backend: "openai", URL: srv.URL, Model: "m", Dimensions: 4})
	for i := 0; i < 3; i++ {
		if err := e.Ping(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if len(*calls) != 1 {
		t.Errorf("ping costs %d requests, want 1", len(*calls))
	}
	wrong, _ := New(Settings{Backend: "openai", URL: srv.URL, Model: "m", Dimensions: 8})
	if err := wrong.Ping(context.Background()); err == nil || !strings.Contains(err.Error(), "dimensions") {
		t.Errorf("wrong dimension: %v", err)
	}
}

func TestOpenAIRejectsBrokenAnswers(t *testing.T) {
	for name, answer := range map[string]string{
		"duplicate index":    `{"data":[{"index":0,"embedding":[1,2,3,4]},{"index":0,"embedding":[1,2,3,4]}]}`,
		"vector missing":     `{"data":[{"index":0,"embedding":[1,2,3,4]}]}`,
		"index out of range": `{"data":[{"index":5,"embedding":[1,2,3,4]},{"index":1,"embedding":[1,2,3,4]}]}`,
		"not JSON":           `<html>`,
		"wrong dimension":    `{"data":[{"index":0,"embedding":[1,2]},{"index":1,"embedding":[1,2]}]}`,
		"empty answer list":  `{"data":[]}`,
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(answer)) }))
		e, _ := New(Settings{Backend: "openai", URL: srv.URL, Model: "m", Dimensions: 4})
		if _, err := e.Embed(context.Background(), []string{"a", "b"}); err == nil {
			t.Errorf("%s: no error", name)
		}
		srv.Close()
	}
}
