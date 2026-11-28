package notify

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestLinesPointAndNameNoContent(t *testing.T) {
	cases := []struct {
		d     delivery
		space string
		want  string
	}{
		{delivery{kind: DraftWaiting, count: 1}, "", "1 draft waits for review."},
		{delivery{kind: DraftWaiting, count: 3}, "Operations", "3 drafts wait for review in the space Operations."},
		{delivery{kind: DraftApproved, count: 1}, "", "1 of your drafts was approved."},
		{delivery{kind: DraftReturned, count: 2}, "", "2 of your drafts were returned to you with a note."},
		{delivery{kind: ItemReceived, count: 1}, "", "1 item was sent to your personal space and waits for you to review."},
		{delivery{kind: HealthChanged, count: 1, detail: "embedder ok"}, "", "The health check embedder is ok again."},
	}
	for _, c := range cases {
		if got := line(c.d, c.space); got != c.want {
			t.Errorf("%s: %q, want %q", c.d.kind, got, c.want)
		}
	}
	if got := line(delivery{kind: HealthChanged, count: 1, detail: "embedder failing"}, ""); !strings.Contains(got, "embedder is failing") {
		t.Errorf("failing: %q", got)
	}
}

func TestBlockedAddresses(t *testing.T) {
	for _, ip := range []string{"127.0.0.1", "::1", "10.0.0.5", "192.168.1.1", "172.16.0.1", "169.254.169.254", "100.64.0.1", "0.0.0.0", "fe80::1", "fc00::1", "224.0.0.1"} {
		if !blockedIP(net.ParseIP(ip)) {
			t.Errorf("%s is not blocked", ip)
		}
	}
	for _, ip := range []string{"8.8.8.8", "1.1.1.1", "2606:4700:4700::1111"} {
		if blockedIP(net.ParseIP(ip)) {
			t.Errorf("%s is blocked", ip)
		}
	}
}

func TestNextDigest(t *testing.T) {
	at := func(s string) time.Time { v, _ := time.Parse(time.RFC3339, s); return v }
	if got := nextDigest(at("2026-11-28T07:00:00Z"), "08:00"); !got.Equal(at("2026-11-28T08:00:00Z")) {
		t.Errorf("before the time: %v", got)
	}
	if got := nextDigest(at("2026-11-28T08:00:00Z"), "08:00"); !got.Equal(at("2026-11-29T08:00:00Z")) {
		t.Errorf("at the time: %v", got)
	}
	if got := nextDigest(at("2026-11-28T23:30:00Z"), "00:15"); !got.Equal(at("2026-11-29T00:15:00Z")) {
		t.Errorf("after midnight: %v", got)
	}
}

func TestMailHeadersCannotBeInjected(t *testing.T) {
	msg := string(buildMail(time.Unix(0, 0), "Atrium", "atrium@example.org", "a@example.org\r\nBcc: x@evil.test", "Hello\r\nBcc: y@evil.test", "- line\r\n", "Atrium"))
	head, _, _ := strings.Cut(msg, "\r\n\r\n")
	for _, l := range strings.Split(head, "\r\n") {
		if strings.HasPrefix(strings.ToLower(l), "bcc:") {
			t.Errorf("a header was injected: %q", l)
		}
	}
	if !strings.Contains(msg, "To: a@example.org Bcc: x@evil.test") {
		t.Errorf("To header: %q", head)
	}
}

func testService(t *testing.T, allowPrivate bool) *Service {
	t.Helper()
	return &Service{opt: Options{Enabled: true, Instance: "Atrium", AllowPrivateNetworks: allowPrivate}, now: time.Now, http: newHTTPClient(allowPrivate)}
}

func TestWebhookSlackAndSigned(t *testing.T) {
	var got struct {
		path, ctype, sig, ts, id string
		body                     []byte
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.path, got.ctype = r.URL.Path, r.Header.Get("Content-Type")
		got.sig, got.ts, got.id = r.Header.Get("X-Atrium-Signature"), r.Header.Get("X-Atrium-Timestamp"), r.Header.Get("X-Atrium-Delivery")
		got.body, _ = io.ReadAll(r.Body)
	}))
	defer srv.Close()
	s := testService(t, true)
	id := uuid.New()

	if err := s.sendWebhook(context.Background(), Connection{Name: "chat", Type: "slack", URL: srv.URL + "/services/T/B/x"}, delivery{id: id, kind: DraftWaiting, count: 2}, false); err != nil {
		t.Fatal(err)
	}
	var slack map[string]string
	if err := json.Unmarshal(got.body, &slack); err != nil || slack["text"] != "Atrium: 2 drafts wait for review." || got.sig != "" {
		t.Errorf("slack: %v %v sig=%q", err, slack, got.sig)
	}

	c := Connection{Name: "auto", Type: "webhook", URL: srv.URL + "/hook", Secret: "0123456789abcdef"}
	if err := s.sendWebhook(context.Background(), c, delivery{id: id, kind: DraftWaiting, count: 1, detail: "d"}, false); err != nil {
		t.Fatal(err)
	}
	if got.sig != "sha256="+sign(c.Secret, got.ts, got.body) || got.id != id.String() || got.ctype != "application/json" {
		t.Errorf("signature or headers: %+v", got)
	}
	var wb webhookBody
	if err := json.Unmarshal(got.body, &wb); err != nil || wb.Type != "draft_waiting" || wb.Count != 1 || wb.Space != "" || wb.ID != id.String() {
		t.Errorf("body: %v %+v", err, wb)
	}
	if strings.Contains(string(got.body), "title") {
		t.Errorf("the body must carry no title: %s", got.body)
	}
}

func TestWebhookAnswers(t *testing.T) {
	status, retry := 200, ""
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if retry != "" {
			w.Header().Set("Retry-After", retry)
		}
		w.WriteHeader(status)
	}))
	defer srv.Close()
	s := testService(t, true)
	c := Connection{Name: "chat", Type: "slack", URL: srv.URL + "/x"}
	send := func() error {
		return s.sendWebhook(context.Background(), c, delivery{id: uuid.New(), kind: DraftWaiting, count: 1}, false)
	}

	status = 404
	if _, ok := send().(*permanentError); !ok {
		t.Errorf("404 must not be retried")
	}
	status = 400
	if _, ok := send().(*permanentError); !ok {
		t.Errorf("400 must not be retried")
	}
	status, retry = 429, "120"
	re, ok := send().(*retryError)
	if !ok || re.after != 120*time.Second {
		t.Errorf("429: %v", send())
	}
	status, retry = 503, ""
	if err := send(); err == nil {
		t.Errorf("503 must be an error")
	} else if _, perm := err.(*permanentError); perm {
		t.Errorf("503 must be retried")
	}
}

func TestWebhookRefusesPrivateAddressesAndRedirects(t *testing.T) {
	hit := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hit = true }))
	defer srv.Close()
	s := testService(t, false)
	err := s.sendWebhook(context.Background(), Connection{Name: "chat", Type: "slack", URL: srv.URL + "/services/SECRET"}, delivery{id: uuid.New(), kind: DraftWaiting, count: 1}, false)
	if err == nil || hit {
		t.Fatalf("a loopback address must be refused: %v hit=%v", err, hit)
	}
	if strings.Contains(err.Error(), "SECRET") || strings.Contains(err.Error(), srv.URL) {
		t.Errorf("the error shows the address: %v", err)
	}

	// A redirect is not followed, so an allowed address cannot send the call somewhere else.
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hit = true }))
	defer target.Close()
	red := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, http.StatusFound) }))
	defer red.Close()
	s = testService(t, true)
	err = s.sendWebhook(context.Background(), Connection{Name: "chat", Type: "slack", URL: red.URL}, delivery{id: uuid.New(), kind: DraftWaiting, count: 1}, false)
	if hit {
		t.Errorf("the redirect was followed")
	}
	if _, ok := err.(*permanentError); !ok {
		t.Errorf("a redirect answer (3xx) is no success: %v", err)
	}
}

func TestBackoffGrowsAndStops(t *testing.T) {
	prev := time.Duration(0)
	for i := 1; i <= maxAttempts; i++ {
		d := retryAfter(i)
		if d < prev {
			t.Errorf("attempt %d waits %v, less than before", i, d)
		}
		prev = d
	}
	if retryAfter(0) != backoff[0] || retryAfter(99) != backoff[len(backoff)-1] {
		t.Errorf("limits: %v %v", retryAfter(0), retryAfter(99))
	}
}

// The limits of the edition are asked each time, so a licence installed or ended applies at once.
func TestLimitsAreAskedEachTime(t *testing.T) {
	webhooks, mail := 1, 1
	s := New(nil, nil, nil, nil, Options{Enabled: true, Limits: func() (int, int) { return webhooks, mail },
		Connections: []Connection{{Name: "a", Type: "slack"}, {Name: "b", Type: "webhook"}, {Name: "m1", Type: "smtp"}, {Name: "m2", Type: "smtp"}}})
	check := func(wantActive, wantLeft int) {
		t.Helper()
		active, left := s.Connections()
		if len(active) != wantActive || len(left) != wantLeft {
			t.Errorf("limits %d/%d: active %v, left out %v", webhooks, mail, active, left)
		}
	}
	check(2, 2)
	webhooks, mail = 0, 0 // unlimited
	check(4, 0)
	webhooks, mail = 2, 1
	check(3, 1)
}
