// Package audit writes audit_log rows. Reading and filtering follow with the admin tools.
package audit

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Execer is satisfied by pgxpool.Pool and pgx.Tx, so events can join a transaction.
type Execer interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

type Event struct {
	UserID     *uuid.UUID
	Client     string // claude, chatgpt, claude-code, api, system …
	Action     string
	SpaceID    *uuid.UUID
	DocumentID *uuid.UUID
	// Personal marks events inside a personal space: only action and time are kept,
	// details are dropped (concept 7.6 / 9).
	Personal bool
	Details  map[string]any
}

// sink receives every event as one JSON line, in addition to the table (SetSink). It lets an
// operator keep a copy of the audit log outside the database, for example in a log collector.
var (
	sinkMu sync.Mutex
	sink   io.Writer
)

// SetSink writes every event that is recorded from now on to w as one JSON line (nil = off). The
// line carries "log":"audit" so that a collector can tell it from the application log, which goes
// to another stream. Events of personal spaces carry no details here either (Event.Personal). A
// line is written when the event is recorded: a transaction that is rolled back afterwards leaves
// a line without a table row.
func SetSink(w io.Writer) {
	sinkMu.Lock()
	defer sinkMu.Unlock()
	sink = w
}

func writeSink(ev Event) {
	sinkMu.Lock()
	defer sinkMu.Unlock()
	if sink == nil {
		return
	}
	line := map[string]any{"log": "audit", "time": time.Now().UTC().Format(time.RFC3339Nano), "action": ev.Action}
	if ev.UserID != nil {
		line["user_id"] = ev.UserID
	}
	if ev.Client != "" {
		line["client"] = ev.Client
	}
	if ev.SpaceID != nil {
		line["space_id"] = ev.SpaceID
	}
	if ev.DocumentID != nil {
		line["document_id"] = ev.DocumentID
	}
	if !ev.Personal && len(ev.Details) > 0 {
		line["details"] = ev.Details
	}
	b, err := json.Marshal(line)
	if err != nil {
		return
	}
	_, _ = sink.Write(append(b, '\n'))
}

// Purge deletes the audit entries older than the given age and records that it did. Entries
// younger than MinRetention are never deleted: the database function refuses (see migration
// 00014), so even a server that is taken over cannot erase the recent trail.
func Purge(ctx context.Context, pool *pgxpool.Pool, olderThan time.Duration) (int64, error) {
	var n int64
	if err := pool.QueryRow(ctx, `SELECT audit_log_purge(now() - make_interval(secs => $1))`,
		olderThan.Seconds()).Scan(&n); err != nil {
		return 0, err
	}
	if n > 0 {
		err := Insert(ctx, pool, Event{Client: "system", Action: "audit_purge",
			Details: map[string]any{"deleted": n, "older_than_days": int(olderThan.Hours() / 24)}})
		return n, err
	}
	return n, nil
}

// MinRetention is the shortest time an entry stays in the table; shorter periods are refused.
const MinRetention = 30 * 24 * time.Hour

func Insert(ctx context.Context, ex Execer, ev Event) error {
	var details []byte
	if !ev.Personal && len(ev.Details) > 0 {
		var err error
		if details, err = json.Marshal(ev.Details); err != nil {
			return err
		}
	}
	_, err := ex.Exec(ctx, `INSERT INTO audit_log (user_id, client, action, space_id, document_id, details)
		VALUES ($1, NULLIF($2, ''), $3, $4, $5, $6)`,
		ev.UserID, ev.Client, ev.Action, ev.SpaceID, ev.DocumentID, details)
	if err == nil {
		writeSink(ev)
	}
	return err
}

// Filter narrows a Query. Zero values mean "any".
type Filter struct {
	UserEmail  string
	Action     string // exact, or a prefix when it ends in "*" (e.g. "admin_*")
	SpaceID    *uuid.UUID
	DocumentID *uuid.UUID
	// Skill keeps events about the skill with this name (details.skill); Client the events of
	// one client label (claude, chatgpt, claude-code, mcp-skills, api ...). Events in personal
	// spaces carry no details, so a skill name never matches there.
	Skill  string
	Client string
	Since  *time.Time
	Until  *time.Time
	Limit  int
}

type Entry struct {
	ID         int64
	Time       time.Time
	UserEmail  string
	Client     string
	Action     string
	Space      string // name of the team space; "personal space" for personal ones
	SpaceID    *uuid.UUID
	DocumentID *uuid.UUID
	Details    map[string]any
}

const (
	defaultQueryLimit = 50
	maxQueryLimit     = 200
	PersonalLabel     = "personal space"
)

// Query returns audit entries, newest first. Personal spaces are shown only as a label,
// never with their id, and their entries carry no details (see Event.Personal).
func Query(ctx context.Context, pool *pgxpool.Pool, f Filter) ([]Entry, error) {
	if f.Limit < 1 {
		f.Limit = defaultQueryLimit
	}
	f.Limit = min(f.Limit, maxQueryLimit)
	prefix, exact := "", f.Action
	if p, ok := strings.CutSuffix(f.Action, "*"); ok {
		prefix, exact = p, ""
	}
	rows, err := pool.Query(ctx, `
SELECT a.id, a.ts, COALESCE(u.email::text, ''), COALESCE(a.client, ''), a.action,
       CASE WHEN sp.type = 'personal' THEN $9 ELSE COALESCE(sp.name, '') END,
       CASE WHEN sp.type = 'personal' THEN NULL ELSE a.space_id END,
       a.document_id, a.details
FROM audit_log a
LEFT JOIN users u   ON u.id = a.user_id
LEFT JOIN spaces sp ON sp.id = a.space_id
WHERE ($1 = '' OR u.email = $1)
  AND ($2 = '' OR a.action = $2)
  AND ($3 = '' OR starts_with(a.action, $3))
  AND ($4::uuid IS NULL OR a.space_id = $4)
  AND ($5::uuid IS NULL OR a.document_id = $5)
  AND ($6::timestamptz IS NULL OR a.ts >= $6)
  AND ($7::timestamptz IS NULL OR a.ts < $7)
  AND ($10 = '' OR a.details->>'skill' = $10)
  AND ($11 = '' OR a.client = $11)
ORDER BY a.id DESC LIMIT $8`,
		f.UserEmail, exact, prefix, f.SpaceID, f.DocumentID, f.Since, f.Until, f.Limit, PersonalLabel, f.Skill, f.Client)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Entry
	for rows.Next() {
		var (
			e   Entry
			raw []byte
		)
		if err := rows.Scan(&e.ID, &e.Time, &e.UserEmail, &e.Client, &e.Action, &e.Space, &e.SpaceID, &e.DocumentID, &raw); err != nil {
			return nil, err
		}
		if len(raw) > 0 {
			_ = json.Unmarshal(raw, &e.Details)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
