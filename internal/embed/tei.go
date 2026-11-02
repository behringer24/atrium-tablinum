package embed

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

func init() { register("tei", newTEI) }

// tei talks to a Hugging Face text-embeddings-inference server (POST /embed).
// Options: batch_size (16, below TEI's default --max-client-batch-size of 32),
// normalize (true), truncate (true).
type tei struct {
	url       string
	apiKey    string
	batch     int
	normalize bool
	truncate  bool
	client    *http.Client
}

func newTEI(s Settings, o *options, c *http.Client) (backend, error) {
	t := &tei{
		url:       strings.TrimRight(s.URL, "/"),
		apiKey:    s.APIKey,
		batch:     o.integer("batch_size", 16, 1),
		normalize: o.boolean("normalize", true),
		truncate:  o.boolean("truncate", true),
		client:    c,
	}
	if !validHTTPURL(t.url) {
		o.fail("embedding.url fehlt oder ist keine http(s)-Adresse")
	}
	if err := o.done(); err != nil {
		return nil, err
	}
	return t, nil
}

// validHTTPURL reports whether raw is an absolute http or https address.
func validHTTPURL(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}

// headers returns the authorization header for a TEI server started with --api-key.
func (t *tei) headers() map[string]string {
	if t.apiKey == "" {
		return nil
	}
	return map[string]string{"Authorization": "Bearer " + t.apiKey}
}

func (t *tei) ping(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, t.url+"/health", nil)
	if err != nil {
		return err
	}
	resp, err := t.client.Do(req)
	if err != nil {
		return fmt.Errorf("embedder nicht erreichbar: %w", stripURL(err))
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("embedder meldet Status %d", resp.StatusCode)
	}
	return nil
}

func (t *tei) embed(ctx context.Context, texts []string, _ purpose) ([][]float32, error) {
	out := make([][]float32, 0, len(texts))
	for start := 0; start < len(texts); start += t.batch {
		end := min(start+t.batch, len(texts))
		var vecs [][]float32
		req := map[string]any{"inputs": texts[start:end], "truncate": t.truncate, "normalize": t.normalize}
		if err := postJSON(ctx, t.client, t.url+"/embed", t.headers(), req, &vecs); err != nil {
			return nil, err
		}
		out = append(out, vecs...)
	}
	return out, nil
}
