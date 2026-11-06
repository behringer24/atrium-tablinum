package embed

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

func init() { register("tei", newTEI) }

// tei talks to a Hugging Face text-embeddings-inference server (POST /embed).
// Options: batch_size (16, below TEI's default --max-client-batch-size of 32),
// normalize (true), truncate (true), check_model (true).
type tei struct {
	url        string
	apiKey     string
	model      string
	batch      int
	normalize  bool
	truncate   bool
	checkModel bool
	client     *http.Client
	served     servedModel
}

func newTEI(s Settings, o *options, c *http.Client) (backend, error) {
	t := &tei{
		url:        strings.TrimRight(s.URL, "/"),
		apiKey:     s.APIKey,
		model:      strings.TrimSpace(s.Model),
		batch:      o.integer("batch_size", 16, 1),
		normalize:  o.boolean("normalize", true),
		truncate:   o.boolean("truncate", true),
		checkModel: o.boolean("check_model", true),
		client:     c,
	}
	if !validHTTPURL(t.url) {
		o.fail("embedding.url is missing or not an http(s) address")
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
		return fmt.Errorf("embedder unreachable: %w", stripURL(err))
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("embedder reports status %d", resp.StatusCode)
	}
	return t.checkServedModel(ctx)
}

func (t *tei) embed(ctx context.Context, texts []string, _ purpose) ([][]float32, error) {
	if err := t.checkServedModel(ctx); err != nil {
		return nil, err
	}
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

// modelCheckEvery is how long the answer of TEI's /info is kept.
const modelCheckEvery = time.Minute

// servedModel remembers the comparison of the model TEI has loaded with embedding.model.
// TEI takes its model from --model-id, not from the request, so changing one without the
// other would store vectors of one model under the name of another: they would look
// current, search would compare them with vectors of the other model and admin_reembed
// would find nothing to rebuild.
type servedModel struct {
	mu      sync.Mutex
	until   time.Time
	err     error
	checked bool
	last    string // the model reported at the last check, to log only changes
}

// checkServedModel returns ErrModelMismatch while TEI serves another model than configured.
// A server that does not name its model (no /info, or a model loaded from a directory) is
// accepted, as is every server with the option check_model=false. When /info cannot be
// asked, the last answer stands and the next call asks again; a refused key is an error.
func (t *tei) checkServedModel(ctx context.Context) error {
	if !t.checkModel {
		return nil
	}
	s := &t.served
	s.mu.Lock()
	defer s.mu.Unlock()
	if time.Now().Before(s.until) {
		return s.err
	}
	served, answered, err := t.info(ctx)
	if err != nil {
		return err
	}
	if !answered {
		return s.err
	}
	unnamed := served == "" || strings.HasPrefix(served, "/") || strings.HasPrefix(served, ".")
	switch {
	case unnamed:
		s.err = nil
	case !strings.EqualFold(served, t.model):
		s.err = fmt.Errorf("%w: TEI serves %q, embedding.model says %q; change --model-id and embedding.model together (docs/embedding.md)",
			ErrModelMismatch, served, t.model)
	default:
		s.err = nil
	}
	if !s.checked || served != s.last {
		switch {
		case unnamed:
			slog.Info("The embedding service does not name its model, so it cannot be compared with embedding.model", "served", served, "configured", t.model)
		case s.err != nil:
			slog.Error("The embedding service serves another model than configured: saving entries is blocked until both match", "served", served, "configured", t.model)
		}
	}
	s.checked, s.last, s.until = true, served, time.Now().Add(modelCheckEvery)
	return s.err
}

// info asks TEI which model it serves (GET /info, field model_id). served is "" when the
// server has no such endpoint or answers with something else than TEI's description;
// answered is false when it could not be asked (unreachable, busy, failing), and an error
// comes only for refused credentials, which /embed would refuse as well.
func (t *tei) info(ctx context.Context) (served string, answered bool, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, t.url+"/info", nil)
	if err != nil {
		return "", false, err
	}
	for k, v := range t.headers() {
		req.Header.Set(k, v)
	}
	resp, err := t.client.Do(req)
	if err != nil {
		return "", false, nil
	}
	defer resp.Body.Close()
	switch code := resp.StatusCode; {
	case code == http.StatusUnauthorized || code == http.StatusForbidden:
		return "", false, statusError(code)
	case code == http.StatusTooManyRequests || code >= 500:
		return "", false, nil
	case code != http.StatusOK:
		return "", true, nil
	}
	var info struct {
		ModelID string `json:"model_id"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&info); err != nil {
		return "", true, nil
	}
	return strings.TrimSpace(info.ModelID), true, nil
}
