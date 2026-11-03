package idp

import (
	"context"
	"log/slog"

	"github.com/behringer24/atrium-tablinum/internal/config"
)

// Entries are allowlist entries an instance admin added at runtime (see internal/allowlist).
type Entries struct {
	Domains []string
	Emails  []string
	Tenants []string
}

// Additions supplies the runtime entries for one provider. They only ever widen what the
// configuration allows: the config file stays the floor an admin cannot go below.
type Additions interface {
	Additions(ctx context.Context, providerID string) (Entries, error)
}

// AdditionsSetter is implemented by the providers that honour runtime entries.
type AdditionsSetter interface {
	SetAdditions(Additions)
}

// effective merges the configured allowlists with the runtime entries. Which lists count
// depends on the provider type: Entra is bound to tenants (a domain list there restricts,
// so it must not grow), the other types allow by domain and by single address. A read
// failure falls back to the configuration alone, which can only deny more, never less.
func effective(ctx context.Context, cfg config.Provider, extra Additions) config.Provider {
	if extra == nil {
		return cfg
	}
	e, err := extra.Additions(ctx, cfg.ID)
	if err != nil {
		slog.Error("Freigabeliste nicht lesbar, es gilt nur die Konfiguration", "provider", cfg.ID, "error", err)
		return cfg
	}
	out := cfg
	if cfg.Type == "entra" {
		out.AllowedTenants = append(append([]string{}, cfg.AllowedTenants...), e.Tenants...)
		return out
	}
	out.AllowedDomains = append(append([]string{}, cfg.AllowedDomains...), e.Domains...)
	out.AllowedEmails = append(append([]string{}, cfg.AllowedEmails...), e.Emails...)
	return out
}
