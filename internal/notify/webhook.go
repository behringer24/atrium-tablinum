package notify

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// blockedIP reports whether an address is one a web hook must not reach: loopback, private, link-local
// (which includes the metadata address of cloud hosts), shared address space and anything that is no
// public unicast address.
func blockedIP(ip net.IP) bool {
	if ip4 := ip.To4(); ip4 != nil {
		if ip4[0] == 100 && ip4[1]&0xc0 == 64 { // 100.64.0.0/10, shared address space
			return true
		}
		ip = ip4
	}
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsUnspecified() || ip.IsMulticast() || !ip.IsGlobalUnicast()
}

// newHTTPClient returns the client for web hooks. The address is checked when the connection is made, on
// the address the name resolved to, so that a name that points inside is refused too. Redirects are not
// followed and no proxy is used.
func newHTTPClient(allowPrivate bool) *http.Client {
	d := &net.Dialer{Timeout: 10 * time.Second}
	if !allowPrivate {
		d.Control = func(_, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return err
			}
			if ip := net.ParseIP(host); ip == nil || blockedIP(ip) {
				return errors.New("the address is not a public one (set notifications.allow_private_networks to allow it)")
			}
			return nil
		}
	}
	return &http.Client{
		Timeout:       15 * time.Second,
		Transport:     &http.Transport{DialContext: d.DialContext, Proxy: nil, TLSHandshakeTimeout: 10 * time.Second, ResponseHeaderTimeout: 10 * time.Second, DisableKeepAlives: true},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// webhookBody is what a webhook connection posts. It carries no title and no text of an entry.
type webhookBody struct {
	ID        string `json:"id"` // the delivery id; a receiver drops one it has seen
	Type      string `json:"type"`
	CreatedAt string `json:"created_at"`
	Instance  string `json:"instance"`
	Space     string `json:"space,omitempty"` // only when the connection is set to name spaces
	Count     int    `json:"count"`
	Detail    string `json:"detail,omitempty"`
	Message   string `json:"message"`
}

// sendWebhook posts one delivery. spaceNames says whether the message may name the space.
func (s *Service) sendWebhook(ctx context.Context, c Connection, d delivery, spaceNames bool) error {
	space := ""
	if spaceNames {
		space = s.spaceName(ctx, d.space)
	}
	text := s.opt.Instance + ": " + line(d, space)
	var body []byte
	var err error
	if c.Type == "slack" {
		body, err = json.Marshal(map[string]string{"text": text})
	} else {
		body, err = json.Marshal(webhookBody{ID: d.id.String(), Type: string(d.kind), CreatedAt: s.now().UTC().Format(time.RFC3339),
			Instance: s.opt.Instance, Space: space, Count: d.count, Detail: d.detail, Message: text})
	}
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.URL, bytes.NewReader(body))
	if err != nil {
		return &permanentError{"the address is not valid"}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Atrium-Notifications")
	if c.Type == "webhook" {
		ts := strconv.FormatInt(s.now().Unix(), 10)
		req.Header.Set("X-Atrium-Delivery", d.id.String())
		req.Header.Set("X-Atrium-Timestamp", ts)
		req.Header.Set("X-Atrium-Signature", "sha256="+sign(c.Secret, ts, body))
	}
	resp, err := s.http.Do(req)
	if err != nil {
		return scrub(err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		return nil
	case resp.StatusCode >= 300 && resp.StatusCode < 400:
		return &permanentError{fmt.Sprintf("the receiver redirects (%d); a redirect is not followed", resp.StatusCode)}
	case resp.StatusCode == http.StatusTooManyRequests:
		after := time.Minute
		if v, err := strconv.Atoi(strings.TrimSpace(resp.Header.Get("Retry-After"))); err == nil && v > 0 && v < 24*3600 {
			after = time.Duration(v) * time.Second
		}
		return &retryError{msg: "the receiver asked to slow down (429)", after: after}
	case resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone:
		return &permanentError{fmt.Sprintf("the receiver refused the address (%d): it may have been revoked", resp.StatusCode)}
	case resp.StatusCode >= 400 && resp.StatusCode < 500:
		return &permanentError{fmt.Sprintf("the receiver answered %d", resp.StatusCode)}
	}
	return fmt.Errorf("the receiver answered %d", resp.StatusCode)
}

// sign is the HMAC-SHA256 of timestamp + "." + body.
func sign(secret, ts string, body []byte) string {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write([]byte(ts))
	m.Write([]byte("."))
	m.Write(body)
	return hex.EncodeToString(m.Sum(nil))
}
