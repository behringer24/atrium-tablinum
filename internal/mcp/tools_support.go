package mcpserver

import (
	"context"
	"runtime"
	"time"

	"github.com/behringer24/atrium-tablinum/internal/product"
)

// started is when the process came up, for the uptime in the support bundle.
var started = time.Now()

type supportIn struct{}

// supportOut is what a support request needs: versions, health, counts and settings. It
// holds no content, no names of people or spaces and no secrets, so that an admin can pass it on.
type supportOut struct {
	Notice      string         `json:"notice"`
	GeneratedAt string         `json:"generated_at"`
	Product     map[string]any `json:"product"`
	Runtime     map[string]any `json:"runtime"`
	Database    map[string]any `json:"database"`
	Counts      map[string]any `json:"counts"`
	// Edition holds what the edition adds.
	Edition       map[string]any `json:"edition,omitempty"`
	Health        healthOut      `json:"health"`
	Configuration map[string]any `json:"configuration,omitempty"`
}

func (s *Server) adminSupportBundle(ctx context.Context, c *call, _ supportIn) (supportOut, error) {
	now := time.Now().UTC()
	line := product.CurrentLine()
	out := supportOut{
		Notice: "For a support request: versions, health, counts and settings. It contains no content, " +
			"no names of people or spaces and no secrets. Show it to the user before they pass it on.",
		GeneratedAt: now.Format(time.RFC3339),
		Product: map[string]any{"name": s.set.ProductName, "version": product.Version, "commit": product.Commit(),
			"build_date": product.BuildDate, "line": line.Name, "line_date": line.Coverage().Format("2006-01-02")},
		Configuration: s.SupportConfig,
	}
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)
	out.Runtime = map[string]any{"go": runtime.Version(), "os": runtime.GOOS, "arch": runtime.GOARCH, "cpus": runtime.NumCPU(),
		"goroutines": runtime.NumGoroutine(), "memory_mb": mem.Sys >> 20, "uptime": time.Since(started).Round(time.Second).String()}

	var pgVersion, vectorVersion string
	var sizeMB int64
	if err := s.Pool.QueryRow(ctx, `SELECT current_setting('server_version'),
       COALESCE((SELECT extversion FROM pg_extension WHERE extname = 'vector'), ''),
       pg_database_size(current_database()) / 1048576`).Scan(&pgVersion, &vectorVersion, &sizeMB); err != nil {
		return supportOut{}, err
	}
	out.Database = map[string]any{"postgres": pgVersion, "pgvector": vectorVersion, "size_mb": sizeMB}
	// With separate database accounts (scripts/db-roles.sql) the application may not read the
	// migration table; the rest of the bundle is still worth having.
	var migration int64
	if err := s.Pool.QueryRow(ctx, `SELECT COALESCE(max(version_id), 0) FROM goose_db_version WHERE is_applied`).Scan(&migration); err != nil {
		out.Database["migration"] = "not readable by the application account"
	} else {
		out.Database["migration"] = migration
	}

	var usersActive, usersLocked, teamSpaces, teamArchived, personalSpaces, groups int64
	var entries, drafts, rejected, archived, handoffs, chunks, skills, skillsReleased, auditRows int64
	if err := s.Pool.QueryRow(ctx, `SELECT
  (SELECT count(*) FROM users WHERE status = 'active'),
  (SELECT count(*) FROM users WHERE status <> 'active'),
  (SELECT count(*) FROM spaces WHERE type = 'team' AND status = 'active'),
  (SELECT count(*) FROM spaces WHERE type = 'team' AND status <> 'active'),
  (SELECT count(*) FROM spaces WHERE type = 'personal'),
  (SELECT count(*) FROM groups),
  (SELECT count(*) FROM documents WHERE kind = 'knowledge' AND status = 'approved'),
  (SELECT count(*) FROM documents WHERE kind = 'knowledge' AND status = 'draft'),
  (SELECT count(*) FROM documents WHERE kind = 'knowledge' AND status = 'rejected'),
  (SELECT count(*) FROM documents WHERE kind = 'knowledge' AND status = 'archived'),
  (SELECT count(*) FROM documents WHERE kind = 'handoff'),
  (SELECT count(*) FROM chunks),
  (SELECT count(*) FROM skills),
  (SELECT count(DISTINCT skill_id) FROM skill_versions WHERE status = 'published'),
  (SELECT count(*) FROM audit_log)`).Scan(&usersActive, &usersLocked, &teamSpaces, &teamArchived, &personalSpaces, &groups,
		&entries, &drafts, &rejected, &archived, &handoffs, &chunks, &skills, &skillsReleased, &auditRows); err != nil {
		return supportOut{}, err
	}
	out.Counts = map[string]any{
		"users_active": usersActive, "users_locked": usersLocked, "team_spaces": teamSpaces, "team_spaces_archived": teamArchived,
		"personal_spaces": personalSpaces, "groups": groups, "entries_approved": entries, "entries_draft": drafts,
		"entries_rejected": rejected, "entries_archived": archived, "handoffs": handoffs, "chunks": chunks,
		"skills": skills, "skills_released": skillsReleased, "audit_entries": auditRows,
	}

	for _, add := range editionSupport {
		name, part, err := add(ctx, s)
		if err != nil {
			return supportOut{}, err
		}
		if name == "" {
			continue // the hook does not apply to this edition
		}
		if out.Edition == nil {
			out.Edition = map[string]any{}
		}
		out.Edition[name] = part
	}
	health, err := s.adminHealth(ctx, c, healthIn{})
	if err != nil {
		return supportOut{}, err
	}
	out.Health = health
	s.record(ctx, c.Actor, "support_bundle", nil, map[string]any{})
	return out, nil
}
