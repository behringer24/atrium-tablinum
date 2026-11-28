package notify

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Setting is what an instance admin chose for a connection. A connection without a setting sends nothing.
type Setting struct {
	Connection string
	Enabled    bool
	Mode       string // instant or digest
	DigestAt   string // HH:MM, UTC
	SpaceNames bool   // name the space in a message of a web hook
	Events     []Kind
}

// Patch changes a setting; nil leaves a value as it is.
type Patch struct {
	Enabled    *bool
	Mode       *string
	DigestAt   *string
	SpaceNames *bool
	Events     *[]string
}

var digestTime = regexp.MustCompile(`^([01][0-9]|2[0-3]):[0-5][0-9]$`)

func defaultSetting(name string) Setting {
	return Setting{Connection: name, Mode: "instant", DigestAt: "08:00", Events: []Kind{}}
}

// Settings returns the setting of every connection in use (the defaults where none was chosen).
func (s *Service) Settings(ctx context.Context) (map[string]Setting, error) {
	out := map[string]Setting{}
	for _, c := range s.active {
		out[c.Name] = defaultSetting(c.Name)
	}
	rows, err := s.pool.Query(ctx, `SELECT connection, enabled, mode, digest_at, space_names FROM notification_settings`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var st Setting
		if err := rows.Scan(&st.Connection, &st.Enabled, &st.Mode, &st.DigestAt, &st.SpaceNames); err != nil {
			return nil, err
		}
		if cur, ok := out[st.Connection]; ok {
			st.Events = cur.Events
			out[st.Connection] = st
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	er, err := s.pool.Query(ctx, `SELECT connection, event FROM notification_routes ORDER BY event`)
	if err != nil {
		return nil, err
	}
	defer er.Close()
	for er.Next() {
		var name, ev string
		if err := er.Scan(&name, &ev); err != nil {
			return nil, err
		}
		if st, ok := out[name]; ok && ValidKind(ev) {
			st.Events = append(st.Events, Kind(ev))
			out[name] = st
		}
	}
	return out, er.Err()
}

// ErrUnknownConnection: no connection of that name is in use.
var ErrUnknownConnection = errors.New("there is no such connection in use")

// SetSetting applies the patch to the connection and returns the setting before and after.
func (s *Service) SetSetting(ctx context.Context, name string, p Patch, by uuid.UUID) (before, after Setting, err error) {
	if _, ok := s.connection(name); !ok {
		return before, after, ErrUnknownConnection
	}
	all, err := s.Settings(ctx)
	if err != nil {
		return before, after, err
	}
	before = all[name]
	after = before
	if p.Enabled != nil {
		after.Enabled = *p.Enabled
	}
	if p.Mode != nil {
		m := strings.ToLower(strings.TrimSpace(*p.Mode))
		if m != "instant" && m != "digest" {
			return before, after, errors.New("mode must be instant or digest")
		}
		after.Mode = m
	}
	if p.DigestAt != nil {
		if !digestTime.MatchString(*p.DigestAt) {
			return before, after, errors.New("digest_at must be a time of day as HH:MM (UTC)")
		}
		after.DigestAt = *p.DigestAt
	}
	if p.SpaceNames != nil {
		after.SpaceNames = *p.SpaceNames
	}
	if p.Events != nil {
		seen := map[Kind]bool{}
		after.Events = []Kind{}
		for _, e := range *p.Events {
			e = strings.ToLower(strings.TrimSpace(e))
			if !ValidKind(e) {
				return before, after, fmt.Errorf("unknown event %q", e)
			}
			if !seen[Kind(e)] {
				seen[Kind(e)] = true
				after.Events = append(after.Events, Kind(e))
			}
		}
		sort.Slice(after.Events, func(i, j int) bool { return after.Events[i] < after.Events[j] })
	}
	if after.Enabled && len(after.Events) == 0 {
		return before, after, errors.New("choose at least one event before switching the connection on")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return before, after, err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `INSERT INTO notification_settings (connection, enabled, mode, digest_at, space_names, updated_by, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, now())
		ON CONFLICT (connection) DO UPDATE SET enabled = $2, mode = $3, digest_at = $4, space_names = $5, updated_by = $6, updated_at = now()`,
		name, after.Enabled, after.Mode, after.DigestAt, after.SpaceNames, by); err != nil {
		return before, after, err
	}
	if p.Events != nil {
		if _, err := tx.Exec(ctx, `DELETE FROM notification_routes WHERE connection = $1`, name); err != nil {
			return before, after, err
		}
		for _, e := range after.Events {
			if _, err := tx.Exec(ctx, `INSERT INTO notification_routes (connection, event) VALUES ($1, $2)`, name, string(e)); err != nil {
				return before, after, err
			}
		}
	}
	return before, after, tx.Commit(ctx)
}

// nextDigest is the next time of day (UTC) after now.
func nextDigest(now time.Time, at string) time.Time {
	now = now.UTC()
	var h, m int
	_, _ = fmt.Sscanf(at, "%d:%d", &h, &m)
	t := time.Date(now.Year(), now.Month(), now.Day(), h, m, 0, 0, time.UTC)
	if !t.After(now) {
		t = t.AddDate(0, 0, 1)
	}
	return t
}
