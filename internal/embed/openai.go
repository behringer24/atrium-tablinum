package embed

import (
	"context"
	"fmt"
	"net/http"
	"strings"
)

func init() { register("openai", newOpenAI) }

const openAIBaseURL = "https://api.openai.com/v1"

// openai speaks the OpenAI embeddings API (POST {url}/embeddings). Many services offer
// the same interface, so embedding.url selects the provider: OpenAI itself (default),
// Mistral, IONOS, OVHcloud, Ollama, vLLM, LiteLLM or a TEI server.
// Options: batch_size (32), send_dimensions (false; true for OpenAI's text-embedding-3
// models, which can return shortened vectors), organization.
type openai struct {
	url     string
	apiKey  string
	model   string
	dim     int
	batch   int
	sendDim bool
	org     string
	client  *http.Client
	pinged  cachedPing
}

func newOpenAI(s Settings, o *options, c *http.Client) (backend, error) {
	b := &openai{
		url:     strings.TrimRight(s.URL, "/"),
		apiKey:  s.APIKey,
		model:   s.Model,
		dim:     s.Dimensions,
		batch:   o.integer("batch_size", 32, 1),
		sendDim: o.boolean("send_dimensions", false),
		org:     o.str("organization", ""),
		client:  c,
	}
	if b.url == "" {
		b.url = openAIBaseURL
		if b.apiKey == "" {
			o.fail("embedding.api_key is missing (the OpenAI API requires a key)")
		}
	} else if !validHTTPURL(b.url) {
		o.fail("embedding.url is not an http(s) address")
	}
	if b.model == "" {
		o.fail("embedding.model is missing")
	}
	if err := o.done(); err != nil {
		return nil, err
	}
	return b, nil
}

func (b *openai) headers() map[string]string {
	h := map[string]string{}
	if b.apiKey != "" {
		h["Authorization"] = "Bearer " + b.apiKey
	}
	if b.org != "" {
		h["OpenAI-Organization"] = b.org
	}
	return h
}

// ping embeds one word, at most once a minute, and checks the dimension: a wrong
// dimension is the typical configuration mistake and would break every write later.
func (b *openai) ping(ctx context.Context) error {
	return b.pinged.check(ctx, func(ctx context.Context) error {
		vecs, err := b.batchEmbed(ctx, []string{"ping"})
		if err != nil {
			return err
		}
		if len(vecs) != 1 || len(vecs[0]) != b.dim {
			return fmt.Errorf("embedder returns different dimensions than configured (%d)", b.dim)
		}
		return nil
	})
}

func (b *openai) embed(ctx context.Context, texts []string, _ purpose) ([][]float32, error) {
	out := make([][]float32, 0, len(texts))
	for start := 0; start < len(texts); start += b.batch {
		vecs, err := b.batchEmbed(ctx, texts[start:min(start+b.batch, len(texts))])
		if err != nil {
			return nil, err
		}
		out = append(out, vecs...)
	}
	return out, nil
}

func (b *openai) batchEmbed(ctx context.Context, batch []string) ([][]float32, error) {
	req := map[string]any{"model": b.model, "input": batch, "encoding_format": "float"}
	if b.sendDim {
		req["dimensions"] = b.dim
	}
	var resp struct {
		Data []struct {
			Index     int       `json:"index"`
			Embedding []float32 `json:"embedding"`
		} `json:"data"`
	}
	if err := postJSON(ctx, b.client, b.url+"/embeddings", b.headers(), req, &resp); err != nil {
		return nil, err
	}
	// The API labels each vector with the index of its input; do not rely on the order.
	out := make([][]float32, len(batch))
	for _, d := range resp.Data {
		if d.Index < 0 || d.Index >= len(batch) || out[d.Index] != nil {
			return nil, fmt.Errorf("embedder returned an unexpected index %d", d.Index)
		}
		out[d.Index] = d.Embedding
	}
	for i, v := range out {
		if v == nil {
			return nil, fmt.Errorf("embedder returned no vector for text %d", i)
		}
	}
	return out, nil
}
