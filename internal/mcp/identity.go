package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
)

// The names assistants and users know this instance by. They come from config.yaml on the first
// start only; afterwards the values in `meta` are the truth and an instance admin changes them
// with admin_instance, without a restart. The product name stays in the configuration.
const (
	metaShortNameKey = "instance_short_name"
	metaOrgNameKey   = "instance_organization_name"

	maxShortNameRunes = 40
	maxOrgNameRunes   = 100
)

// names returns the current short name and organisation name.
func (s *Server) names() (short, org string) {
	s.idMu.RLock()
	defer s.idMu.RUnlock()
	return s.short, s.org
}

// description returns the rendered description of a tool.
func (s *Server) description(tool string) string {
	s.idMu.RLock()
	defer s.idMu.RUnlock()
	return s.desc[tool]
}

// applyNames switches to new names and renders the tool descriptions again. New connections see
// them at once, because the tool list is built per connection.
func (s *Server) applyNames(short, org string) {
	desc := s.renderDescriptions(short, org)
	s.idMu.Lock()
	s.short, s.org, s.desc = short, org, desc
	s.idMu.Unlock()
}

// InitIdentity seeds the names from the configuration where the database has none yet and then
// adopts what the database holds. Call it once at start, before the server takes requests.
func (s *Server) InitIdentity(ctx context.Context) error {
	cfgShort, cfgOrg := s.set.ShortName, s.set.OrganizationName
	if cfgShort == "" {
		cfgShort = s.set.ProductName
	}
	for key, v := range map[string]string{metaShortNameKey: cfgShort, metaOrgNameKey: cfgOrg} {
		if _, err := s.Pool.Exec(ctx, `INSERT INTO meta (key, value) VALUES ($1, $2) ON CONFLICT (key) DO NOTHING`, key, v); err != nil {
			return fmt.Errorf("seeding instance names: %w", err)
		}
	}
	short, org, err := s.loadNames(ctx)
	if err != nil {
		return err
	}
	s.applyNames(short, org)
	return nil
}

func (s *Server) loadNames(ctx context.Context) (short, org string, err error) {
	read := func(key string) (string, error) {
		var v string
		err := s.Pool.QueryRow(ctx, `SELECT value FROM meta WHERE key = $1`, key).Scan(&v)
		if errors.Is(err, pgx.ErrNoRows) {
			return "", nil
		}
		return v, err
	}
	if short, err = read(metaShortNameKey); err != nil {
		return "", "", fmt.Errorf("reading instance names: %w", err)
	}
	if org, err = read(metaOrgNameKey); err != nil {
		return "", "", fmt.Errorf("reading instance names: %w", err)
	}
	if short == "" {
		short = s.set.ProductName
	}
	return short, org, nil
}

// ---- admin_instance -------------------------------------------------------------------

type adminInstanceIn struct {
	Action           string `json:"action" jsonschema:"get or set"`
	ShortName        string `json:"short_name,omitempty" jsonschema:"set only: how users call this store in a chat (at most 40 characters); omit to keep it"`
	OrganizationName string `json:"organization_name,omitempty" jsonschema:"set only: the organisation this store belongs to (at most 100 characters); omit to keep it"`
	Confirm          bool   `json:"confirm,omitempty" jsonschema:"For set: true only after confirmation by the user, who was shown the old and new names"`
}

type adminInstanceOut struct {
	Message          string `json:"message"`
	ProductName      string `json:"product_name"`
	ShortName        string `json:"short_name"`
	OrganizationName string `json:"organization_name"`
}

func (s *Server) adminInstance(ctx context.Context, c *call, in adminInstanceIn) (adminInstanceOut, error) {
	short, org := s.names()
	out := adminInstanceOut{ProductName: s.set.ProductName, ShortName: short, OrganizationName: org}
	switch strings.ToLower(strings.TrimSpace(in.Action)) {
	case "get":
		out.Message = "Current names. The product name is part of the configuration and not changed here."
		return out, nil
	case "set":
		newShort, newOrg := short, org
		if v := strings.TrimSpace(in.ShortName); v != "" {
			newShort = v
		}
		if v := strings.TrimSpace(in.OrganizationName); v != "" {
			newOrg = v
		}
		if newShort == short && newOrg == org {
			return out, userErr("Nothing to change: pass short_name and/or organization_name with a new value.")
		}
		if err := checkName("short_name", newShort, maxShortNameRunes); err != nil {
			return out, err
		}
		if err := checkName("organization_name", newOrg, maxOrgNameRunes); err != nil {
			return out, err
		}
		if !in.Confirm {
			return out, userErr(fmt.Sprintf("The names go to the assistants of all users. Show the user the old (short name %q, organisation %q) and new names (%q, %q), ask for their confirmation and then call again with confirm=true.",
				short, org, newShort, newOrg))
		}
		for key, v := range map[string]string{metaShortNameKey: newShort, metaOrgNameKey: newOrg} {
			if _, err := s.Pool.Exec(ctx, `INSERT INTO meta (key, value) VALUES ($1, $2)
				ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`, key, v); err != nil {
				return out, err
			}
		}
		s.applyNames(newShort, newOrg)
		s.record(ctx, c.Actor, "admin_instance_set", nil, map[string]any{
			"previous_short_name": short, "previous_organization_name": org,
			"short_name": newShort, "organization_name": newOrg,
		})
		out.ShortName, out.OrganizationName = newShort, newOrg
		out.Message = "Names saved. They apply to new connections of the assistants; open chats pick them up when they reconnect."
		return out, nil
	}
	return out, userErr("action must be get or set.")
}

// checkName accepts a single line of printable text.
func checkName(field, v string, max int) error {
	if n := utf8.RuneCountInString(v); n > max {
		return userErr(fmt.Sprintf("%s is too long at %d characters (at most %d).", field, n, max))
	}
	for _, r := range v {
		if unicode.IsControl(r) {
			return userErr(field + " must be a single line of text.")
		}
	}
	return nil
}
