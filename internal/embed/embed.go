// Package embed defines the Embedder interface and its backends (tei, openai, bedrock).
// A backend is one file that registers itself in init(); everything shared (prefixes,
// vector checks, retries, option parsing) lives here.
package embed

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"
)

// Embedder turns texts into vectors. Implementations must return exactly one vector
// of Dimensions() floats per input, in input order.
type Embedder interface {
	Embed(ctx context.Context, texts []string) ([][]float32, error)
	Model() string
	Dimensions() int
	// Ping checks that the backend is reachable and ready (used by /readyz).
	Ping(ctx context.Context) error
}

// QueryEmbedder is implemented by embedders that treat a search query differently from
// a stored document (query prefixes, input types). Embed always means "document".
type QueryEmbedder interface {
	EmbedQuery(ctx context.Context, text string) ([]float32, error)
}

// Query embeds a search query, using the query-specific path when the embedder has one.
func Query(ctx context.Context, e Embedder, text string) ([]float32, error) {
	if q, ok := e.(QueryEmbedder); ok {
		return q.EmbedQuery(ctx, text)
	}
	vecs, err := e.Embed(ctx, []string{text})
	if err != nil {
		return nil, err
	}
	if len(vecs) != 1 {
		return nil, fmt.Errorf("embedder returned %d vectors for 1 text", len(vecs))
	}
	return vecs[0], nil
}

// Settings mirrors the embedding section of the config.
type Settings struct {
	Backend    string
	URL        string
	Model      string
	Dimensions int
	APIKey     string
	// QueryPrefix and DocumentPrefix are put in front of every text, for models that need
	// them (E5: "query: " / "passage: "). They apply to every backend.
	QueryPrefix    string
	DocumentPrefix string
	// Timeout bounds one request to the backend; 0 = 60 seconds.
	Timeout time.Duration
	// Options carries the settings that only one backend knows (batch_size, region, ...).
	// Unknown keys are rejected so typos do not go unnoticed.
	Options map[string]string
}

const defaultTimeout = 60 * time.Second

// purpose says what a text is embedded for.
type purpose int

const (
	forDocument purpose = iota
	forQuery
)

// backend is what one embedding service has to implement. Prefixes, the check of the
// returned vectors and the empty-input case are handled by the wrapper.
type backend interface {
	embed(ctx context.Context, texts []string, p purpose) ([][]float32, error)
	ping(ctx context.Context) error
}

// factory builds a backend from the settings. It must not do any I/O (it also runs for
// config validation) and must call o.done() after it has read all of its options.
// client is nil during validation.
type factory func(s Settings, o *options, client *http.Client) (backend, error)

var registry = map[string]factory{}

func register(name string, f factory) { registry[name] = f }

// Backends lists the names usable as embedding.backend.
func Backends() []string {
	names := make([]string, 0, len(registry))
	for n := range registry {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// Validate checks the settings of the configured backend without contacting it.
func Validate(s Settings) error {
	_, err := build(s, nil)
	return err
}

func build(s Settings, client *http.Client) (backend, error) {
	f, ok := registry[s.Backend]
	if !ok {
		return nil, fmt.Errorf("embedding.backend %q unknown (%s)", s.Backend, strings.Join(Backends(), " | "))
	}
	if s.Dimensions <= 0 {
		return nil, fmt.Errorf("embedding.dimensions must be positive")
	}
	b, err := f(s, newOptions(s.Backend, s.Options), client)
	if err != nil {
		return nil, fmt.Errorf("embedding (%s): %w", s.Backend, err)
	}
	return b, nil
}

// New builds the backend selected in the config.
func New(s Settings) (Embedder, error) {
	if s.Timeout <= 0 {
		s.Timeout = defaultTimeout
	}
	b, err := build(s, &http.Client{Timeout: s.Timeout})
	if err != nil {
		return nil, err
	}
	return &wrapped{b: b, s: s}, nil
}

// wrapped adds what every backend needs: prefixes and a check that the backend returned
// the right number of vectors with the configured dimension.
type wrapped struct {
	b backend
	s Settings
}

func (w *wrapped) Model() string                  { return w.s.Model }
func (w *wrapped) Dimensions() int                { return w.s.Dimensions }
func (w *wrapped) Ping(ctx context.Context) error { return w.b.ping(ctx) }

func (w *wrapped) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	return w.run(ctx, texts, forDocument)
}

func (w *wrapped) EmbedQuery(ctx context.Context, text string) ([]float32, error) {
	vecs, err := w.run(ctx, []string{text}, forQuery)
	if err != nil {
		return nil, err
	}
	return vecs[0], nil
}

func (w *wrapped) run(ctx context.Context, texts []string, p purpose) ([][]float32, error) {
	if len(texts) == 0 {
		return [][]float32{}, nil
	}
	prefix := w.s.DocumentPrefix
	if p == forQuery {
		prefix = w.s.QueryPrefix
	}
	in := texts
	if prefix != "" {
		in = make([]string, len(texts))
		for i, t := range texts {
			in[i] = prefix + t
		}
	}
	vecs, err := w.b.embed(ctx, in, p)
	if err != nil {
		return nil, err
	}
	if len(vecs) != len(texts) {
		return nil, fmt.Errorf("embedder returned %d vectors for %d texts", len(vecs), len(texts))
	}
	for i, v := range vecs {
		if len(v) != w.s.Dimensions {
			return nil, fmt.Errorf("vector %d has %d dimensions, %d configured", i, len(v), w.s.Dimensions)
		}
	}
	return vecs, nil
}
