package httpx

import (
	"net/http"
	"testing"
	"time"
)

func req(remote string, xff ...string) *http.Request {
	r := &http.Request{RemoteAddr: remote, Header: http.Header{}}
	for _, v := range xff {
		r.Header.Add("X-Forwarded-For", v)
	}
	return r
}

func TestClientIP(t *testing.T) {
	proxies, err := ParseProxies([]string{"10.0.0.0/8", "::1"})
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		r    *http.Request
		want string
	}{
		{"direct, header ignored", req("203.0.113.5:1234", "1.2.3.4"), "203.0.113.5"},
		{"via proxy", req("10.0.0.2:80", "198.51.100.7"), "198.51.100.7"},
		{"forged prefix is skipped", req("10.0.0.2:80", "6.6.6.6, 198.51.100.7"), "198.51.100.7"},
		{"proxy chain", req("10.0.0.2:80", "198.51.100.7, 10.0.0.9"), "198.51.100.7"},
		{"proxy without header", req("10.0.0.2:80"), "10.0.0.2"},
		{"garbage header", req("10.0.0.2:80", "nonsense"), "10.0.0.2"},
		{"ipv6 loopback proxy", req("[::1]:80", "198.51.100.7"), "198.51.100.7"},
	}
	for _, tc := range cases {
		if got := ClientIP(tc.r, proxies); got != tc.want {
			t.Errorf("%s: %s, want %s", tc.name, got, tc.want)
		}
	}
	if got := ClientIP(req("10.0.0.2:80", "1.1.1.1"), nil); got != "10.0.0.2" {
		t.Errorf("ohne Proxys: %s", got)
	}
	if _, err := ParseProxies([]string{"x"}); err == nil {
		t.Error("ungültiger Eintrag")
	}
}

func TestLimiter(t *testing.T) {
	l := NewLimiter(2, time.Minute)
	now := time.Now()
	l.now = func() time.Time { return now }
	if !l.Allow("a") || !l.Allow("a") || l.Allow("a") {
		t.Fatal("Limit 2")
	}
	if !l.Allow("b") {
		t.Error("anderer Schlüssel")
	}
	now = now.Add(2 * time.Minute)
	if !l.Allow("a") {
		t.Error("Fenster")
	}
}
