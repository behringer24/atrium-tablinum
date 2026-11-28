package config

import (
	"net"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

// Notifications configures the connections that notices are sent through (docs/design/notifications.md).
// A connection holds an address and its secret, so it lives here and nowhere else: no tool creates or
// changes one. What goes through a connection is chosen at runtime by an instance admin (admin_notifications).
// Off by default.
type Notifications struct {
	Enabled bool `yaml:"enabled"`
	// AllowPrivateNetworks lets a web hook reach loopback, private and link-local addresses. Off, such an
	// address is refused when the connection is made. Meant for a chat system inside the own network.
	AllowPrivateNetworks bool `yaml:"allow_private_networks"`
	// Connections are the places a notice can go. A build may use only some of them (the first of each kind).
	Connections []NotificationConnection `yaml:"connections"`
}

// NotificationConnection is a web hook or an SMTP server.
type NotificationConnection struct {
	// Name identifies the connection in admin_notifications and the health check.
	Name string `yaml:"name"`
	// Type is slack (the format of a Slack incoming web hook), webhook (a signed JSON body) or smtp.
	Type string `yaml:"type"`

	// URL is the HTTPS address of a web hook. It is a secret for slack: whoever knows it can post.
	URL string `yaml:"url"`
	// Secret signs the body of a webhook connection (HMAC-SHA256).
	Secret string `yaml:"secret"`

	// SMTP: server, TLS mode (starttls, tls or none, the last for a local server only), login and sender.
	Host     string `yaml:"host"`
	Port     int    `yaml:"port"`
	TLS      string `yaml:"tls"`
	Username string `yaml:"username"`
	Password string `yaml:"password"`
	From     string `yaml:"from"`
}

const (
	ConnSlack   = "slack"
	ConnWebhook = "webhook"
	ConnSMTP    = "smtp"
)

var connectionName = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,39}$`)

func (n *Notifications) defaults() {
	for i := range n.Connections {
		c := &n.Connections[i]
		c.Name = strings.TrimSpace(c.Name)
		c.Type = strings.ToLower(strings.TrimSpace(c.Type))
		if c.Type == ConnSMTP {
			if c.TLS == "" {
				c.TLS = "starttls"
			}
			if c.Port == 0 {
				switch c.TLS {
				case "tls":
					c.Port = 465
				case "none":
					c.Port = 25
				default:
					c.Port = 587
				}
			}
		}
	}
}

func isLoopbackHost(h string) bool {
	if strings.EqualFold(h, "localhost") {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}

func (n *Notifications) validate(add func(string, ...any)) {
	seen := map[string]bool{}
	for i, c := range n.Connections {
		at := "notifications.connections[" + strconv.Itoa(i) + "]"
		switch {
		case !connectionName.MatchString(c.Name):
			add("%s.name must be 1 to 40 characters: lower-case letters, digits, - and _", at)
		case seen[c.Name]:
			add("%s.name %q is used twice", at, c.Name)
		}
		seen[c.Name] = true
		switch c.Type {
		case ConnSlack, ConnWebhook:
			u, err := url.Parse(c.URL)
			switch {
			case err != nil || u.Host == "":
				add("%s.url must be an absolute URL", at)
			case u.Scheme != "https":
				add("%s.url must use https", at)
			case u.User != nil:
				add("%s.url must not contain a user name or password", at)
			case c.Type == ConnSlack && !strings.EqualFold(u.Hostname(), "hooks.slack.com"):
				add("%s.url of a slack connection must be a hooks.slack.com address", at)
			}
			if c.Type == ConnWebhook && len(c.Secret) < 16 {
				add("%s.secret must be at least 16 characters: it signs what is sent", at)
			}
			if c.Type == ConnSlack && c.Secret != "" {
				add("%s.secret is for webhook connections only: Slack does not check a signature", at)
			}
		case ConnSMTP:
			switch {
			case strings.TrimSpace(c.Host) == "":
				add("%s.host must not be empty", at)
			case c.Port < 1 || c.Port > 65535:
				add("%s.port must be between 1 and 65535", at)
			}
			switch c.TLS {
			case "starttls", "tls":
			case "none":
				if !isLoopbackHost(c.Host) {
					add("%s.tls: none is allowed for a server on this host only", at)
				}
			default:
				add("%s.tls must be starttls, tls or none", at)
			}
			if !strings.Contains(c.From, "@") || strings.ContainsAny(c.From, "\r\n") {
				add("%s.from must be an e-mail address", at)
			}
			if (c.Username == "") != (c.Password == "") {
				add("%s.username and password go together", at)
			}
		default:
			add("%s.type must be slack, webhook or smtp", at)
		}
	}
	if n.Enabled && len(n.Connections) == 0 {
		add("notifications.enabled needs at least one entry in notifications.connections")
	}
}

// notificationNames lists the connections as "name (type)" for the support bundle.
func notificationNames(n Notifications) []string {
	out := make([]string, 0, len(n.Connections))
	for _, c := range n.Connections {
		out = append(out, c.Name+" ("+c.Type+")")
	}
	return out
}
