package embed

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"
)

const maxAttempts = 3

// retryBase is the first wait between two attempts (doubled each time); tests shorten it.
var retryBase = 500 * time.Millisecond

// postJSON sends body as JSON and decodes a 200 answer into out. Rate limits (429) and
// server errors (5xx) are retried with a growing pause, honouring Retry-After. Error
// messages never contain the answer body: it can echo the input text.
func postJSON(ctx context.Context, c *http.Client, endpoint string, headers map[string]string, body, out any) error {
	payload, err := json.Marshal(body)
	if err != nil {
		return err
	}
	var last error
	var wait time.Duration
	for attempt := 0; attempt < maxAttempts; attempt++ {
		if attempt > 0 {
			if wait == 0 {
				wait = retryBase << (attempt - 1)
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(wait):
			}
			wait = 0
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/json")
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		resp, err := c.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return fmt.Errorf("embedding request: %w", ctx.Err())
			}
			last = fmt.Errorf("embedding request: %w", stripURL(err))
			continue
		}
		status := resp.StatusCode
		retryAfter := resp.Header.Get("Retry-After")
		if status == http.StatusOK {
			err = json.NewDecoder(resp.Body).Decode(out)
			resp.Body.Close()
			if err != nil {
				return fmt.Errorf("reading embedding response: %w", err)
			}
			return nil
		}
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
		resp.Body.Close()
		last = statusError(status)
		if status != http.StatusTooManyRequests && status < 500 {
			return last
		}
		if s, err := strconv.Atoi(retryAfter); err == nil && s > 0 {
			wait = min(time.Duration(s)*time.Second, 10*time.Second)
		}
	}
	return last
}

func statusError(status int) error {
	switch status {
	case http.StatusUnauthorized, http.StatusForbidden:
		return fmt.Errorf("embedder rejects authentication (status %d), check credentials", status)
	case http.StatusTooManyRequests:
		return fmt.Errorf("embedder throttles requests (status 429)")
	}
	return fmt.Errorf("embedder responded with status %d", status)
}

// stripURL drops the request URL from a transport error, keeping only the cause.
func stripURL(err error) error {
	if ue, ok := err.(*url.Error); ok {
		return ue.Err
	}
	return err
}

// cachedPing runs a check at most once a minute while it succeeds. Backends that bill by
// token use it so that readiness probes do not generate cost.
type cachedPing struct {
	mu      sync.Mutex
	okUntil time.Time
}

func (p *cachedPing) check(ctx context.Context, fn func(context.Context) error) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if time.Now().Before(p.okUntil) {
		return nil
	}
	if err := fn(ctx); err != nil {
		return err
	}
	p.okUntil = time.Now().Add(time.Minute)
	return nil
}
