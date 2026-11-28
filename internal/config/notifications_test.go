package config

import (
	"strings"
	"testing"
)

func TestNotificationsAreOffByDefault(t *testing.T) {
	cfg, err := Parse([]byte(minimal), env(map[string]string{"GOOGLE_SECRET": "s", "DATABASE_URL": "postgres://x"}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Notifications.Enabled || len(cfg.Notifications.Connections) != 0 || cfg.Notifications.AllowPrivateNetworks {
		t.Errorf("defaults: %+v", cfg.Notifications)
	}
}

func TestNotificationConnections(t *testing.T) {
	vars := env(map[string]string{"GOOGLE_SECRET": "s", "DATABASE_URL": "postgres://x", "HOOK": "https://hooks.slack.com/services/T/B/x"})
	ok := minimal + `
notifications:
  enabled: true
  connections:
    - name: chat
      type: slack
      url: ${HOOK}
    - name: automation
      type: webhook
      url: https://automation.example.org/hook
      secret: 0123456789abcdef
    - name: mail
      type: smtp
      host: mail.example.org
      username: u
      password: p
      from: atrium@example.org
`
	cfg, err := Parse([]byte(ok), vars)
	if err != nil {
		t.Fatal(err)
	}
	m := cfg.Notifications.Connections[2]
	if m.TLS != "starttls" || m.Port != 587 {
		t.Errorf("the smtp defaults: %+v", m)
	}

	bad := map[string]string{
		"a plain http address":        strings.Replace(ok, "https://automation.example.org/hook", "http://automation.example.org/hook", 1),
		"a slack address elsewhere":   strings.Replace(ok, "${HOOK}", "https://example.org/services/x", 1),
		"a short secret":              strings.Replace(ok, "0123456789abcdef", "short", 1),
		"a name twice":                strings.Replace(ok, "name: automation", "name: chat", 1),
		"a name with capitals":        strings.Replace(ok, "name: mail", "name: Mail", 1),
		"an unknown type":             strings.Replace(ok, "type: smtp", "type: pager", 1),
		"no tls on a remote server":   strings.Replace(ok, "      from:", "      tls: none\n      from:", 1),
		"a sender without an address": strings.Replace(ok, "atrium@example.org", "atrium", 1),
		"a user without a password":   strings.Replace(ok, "      password: p\n", "", 1),
		"a login in the address":      strings.Replace(ok, "https://automation.example.org/hook", "https://u:p@automation.example.org/hook", 1),
		"on without connections":      minimal + "notifications:\n  enabled: true\n",
	}
	for what, raw := range bad {
		if _, err := Parse([]byte(raw), vars); err == nil || !strings.Contains(err.Error(), "notifications") {
			t.Errorf("%s must be refused naming notifications: %v", what, err)
		}
	}

	local := strings.Replace(strings.Replace(ok, "host: mail.example.org", "host: localhost", 1), "      from:", "      tls: none\n      from:", 1)
	if cfg, err := Parse([]byte(local), vars); err != nil || cfg.Notifications.Connections[2].Port != 25 {
		t.Errorf("a local server without tls: %v", err)
	}
}

func TestNotificationAddressesStayOutOfTheSupportBundle(t *testing.T) {
	vars := env(map[string]string{"GOOGLE_SECRET": "s", "DATABASE_URL": "postgres://x"})
	cfg, err := Parse([]byte(minimal+"notifications:\n  enabled: true\n  connections:\n    - name: chat\n      type: slack\n      url: https://hooks.slack.com/services/T/B/SECRETVALUE\n"), vars)
	if err != nil {
		t.Fatal(err)
	}
	sum := cfg.SupportSummary()["notifications"].(map[string]any)
	if got := sum["connections"].([]string); len(got) != 1 || got[0] != "chat (slack)" {
		t.Errorf("connections: %v", got)
	}
	if strings.Contains(strings.ToLower(strings.TrimSpace(strings.Join(sum["connections"].([]string), " "))), "secretvalue") {
		t.Errorf("the address is in the summary")
	}
}
