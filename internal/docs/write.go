package docs

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/behringer24/atrium-tablinum/internal/audit"
	"github.com/behringer24/atrium-tablinum/internal/authz"
	"github.com/behringer24/atrium-tablinum/internal/ingest"
	"github.com/behringer24/atrium-tablinum/internal/spaces"
)

const maxSourceLen = 40

type StoreParams struct {
	SpaceID uuid.UUID
	Title   string
	Content string
	Tags    []string
	// Force stores even if very similar entries exist in the space.
	Force bool
	// Draft asks for status draft: a curator of the space has to release the entry before
	// others see it (for example an admin writing for a field outside their own). Only team
	// spaces have drafts. Authors of approval spaces get drafts anyway.
	Draft bool
}

// StoreResult holds either the stored document or, when similar entries exist and Force
// was not set, nothing stored and the candidates to ask the user about.
type StoreResult struct {
	Document   *Document
	Duplicates []ingest.Duplicate
}

// writableSpace checks that the actor may write to the space and returns it.
func (s *Store) writableSpace(ctx context.Context, a Actor, id uuid.UUID) (spaces.Space, authz.Role, error) {
	role := a.Roles.Role(id)
	if role == authz.None {
		return spaces.Space{}, role, ErrNotFound
	}
	sp, err := s.spaces.GetByID(ctx, id)
	if err != nil {
		return spaces.Space{}, role, ErrNotFound
	}
	if sp.Archived {
		return spaces.Space{}, role, ErrNotFound
	}
	if role < authz.Author {
		return spaces.Space{}, role, ErrForbidden
	}
	return sp, role, nil
}

// statusFor applies the approval rules of concept 7.5 to a new entry; draft is the explicit
// request of the author.
func statusFor(sp spaces.Space, role authz.Role, draft bool) string {
	if sp.Type == "team" && (draft || sp.RequireApproval && role < authz.Curator) {
		return "draft"
	}
	return "approved"
}

func source(a Actor) string {
	c := strings.ToLower(strings.TrimSpace(a.Client))
	if c == "" {
		return "api"
	}
	if len(c) > maxSourceLen {
		c = c[:maxSourceLen]
	}
	return c
}

// Store saves a new knowledge entry (concept section 10).
func (s *Store) Store(ctx context.Context, a Actor, p StoreParams) (StoreResult, error) {
	sp, role, err := s.writableSpace(ctx, a, p.SpaceID)
	if err != nil {
		return StoreResult{}, err
	}
	if p.Draft && sp.Type == "personal" {
		return StoreResult{}, errors.Join(ingest.ErrInvalid, errors.New("entwürfe gibt es nur in team-bereichen"))
	}
	n, err := s.ing.Validate(p.Title, p.Content, p.Tags)
	if err != nil {
		return StoreResult{}, err
	}
	if sp.Type == "personal" {
		if err := s.checkQuota(ctx, a, sp.ID, 1, int64(len(n.Content))); err != nil {
			return StoreResult{}, err
		}
	}
	prep, err := s.ing.Prepare(ctx, n.Title, n.Content)
	if err != nil {
		return StoreResult{}, err
	}
	if !p.Force {
		dups, err := s.ing.FindDuplicates(ctx, s.pool, a.Scope(), sp.ID, prep, s.opt.DuplicateThreshold, nil)
		if err != nil {
			return StoreResult{}, err
		}
		if len(dups) > 0 {
			return StoreResult{Duplicates: dups}, nil
		}
	}

	status := statusFor(sp, role, p.Draft)
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return StoreResult{}, err
	}
	defer tx.Rollback(ctx)

	var id uuid.UUID
	err = tx.QueryRow(ctx, `
INSERT INTO documents (space_id, kind, title, content, tags, status, author_id, source, updated_by)
VALUES ($1, 'knowledge', $2, $3, $4, $5, $6, $7, $6) RETURNING id`,
		sp.ID, n.Title, n.Content, n.Tags, status, a.User.ID, source(a)).Scan(&id)
	if err != nil {
		return StoreResult{}, err
	}
	if err := writeVersion(ctx, tx, id, 1, sp.ID, n.Title, n.Content, n.Tags, status, a.User.ID); err != nil {
		return StoreResult{}, err
	}
	if err := s.ing.ReplaceChunks(ctx, tx, id, prep); err != nil {
		return StoreResult{}, err
	}
	if err := record(ctx, tx, a, "store", sp, id, map[string]any{"status": status}); err != nil {
		return StoreResult{}, err
	}
	doc, err := fetch(ctx, tx, a, id, false)
	if err != nil {
		return StoreResult{}, err
	}
	return StoreResult{Document: &doc}, tx.Commit(ctx)
}

type UpdateParams struct {
	ID      uuid.UUID
	Title   *string
	Content *string
	Tags    *[]string
}

// canEdit: own entries need author, foreign entries need curator.
func canEdit(a Actor, d Document) error {
	role := a.Roles.Role(d.SpaceID)
	switch {
	case role == authz.None:
		return ErrNotFound
	case role >= authz.Curator, role >= authz.Author && d.AuthorID == a.User.ID:
		return nil
	}
	return ErrForbidden
}

// Update replaces title, content and/or tags and re-indexes the entry.
func (s *Store) Update(ctx context.Context, a Actor, p UpdateParams) (Document, error) {
	cur, err := fetch(ctx, s.pool, a, p.ID, false)
	if err != nil {
		return Document{}, err
	}
	if cur.Kind != "knowledge" {
		return Document{}, errors.Join(ingest.ErrInvalid, errors.New("übergaben werden nicht über update_knowledge geändert"))
	}
	if err := canEdit(a, cur); err != nil {
		return Document{}, err
	}
	sp, role, err := s.writableSpace(ctx, a, cur.SpaceID)
	if err != nil {
		return Document{}, err
	}

	title, content, tags := cur.Title, cur.Content, cur.Tags
	if p.Title != nil {
		title = *p.Title
	}
	if p.Content != nil {
		content = *p.Content
	}
	if p.Tags != nil {
		tags = *p.Tags
	}
	n, err := s.ing.Validate(title, content, tags)
	if err != nil {
		return Document{}, err
	}

	status := cur.Status
	if cur.Status == "rejected" {
		status = "draft" // a revised rejected entry goes back to review
	}
	if sp.Type == "team" && sp.RequireApproval && role < authz.Curator {
		if cur.Status == "approved" {
			return Document{}, ErrNeedsApproval
		}
		status = "draft" // an author revising their own draft or rejected entry
	}
	if sp.Type == "personal" {
		delta := int64(len(n.Content) - len(cur.Content))
		if err := s.checkQuota(ctx, a, sp.ID, 0, delta); err != nil {
			return Document{}, err
		}
	}
	prep, err := s.ing.Prepare(ctx, n.Title, n.Content)
	if err != nil {
		return Document{}, err
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Document{}, err
	}
	defer tx.Rollback(ctx)
	locked, err := fetch(ctx, tx, a, p.ID, true)
	if err != nil {
		return Document{}, err
	}
	if locked.Version != cur.Version {
		return Document{}, ErrConflict
	}
	version := locked.Version + 1
	if _, err := tx.Exec(ctx, `UPDATE documents SET title = $2, content = $3, tags = $4, status = $5,
		review_note = CASE WHEN $5 = 'draft' THEN NULL ELSE review_note END,
		version = $6, updated_at = now(), updated_by = $7 WHERE id = $1`,
		p.ID, n.Title, n.Content, n.Tags, status, version, a.User.ID); err != nil {
		return Document{}, err
	}
	if err := writeVersion(ctx, tx, p.ID, version, sp.ID, n.Title, n.Content, n.Tags, status, a.User.ID); err != nil {
		return Document{}, err
	}
	if err := s.ing.ReplaceChunks(ctx, tx, p.ID, prep); err != nil {
		return Document{}, err
	}
	if err := record(ctx, tx, a, "update", sp, p.ID, map[string]any{"version": version}); err != nil {
		return Document{}, err
	}
	doc, err := fetch(ctx, tx, a, p.ID, false)
	if err != nil {
		return Document{}, err
	}
	return doc, tx.Commit(ctx)
}

// Archive hides an entry from reading and search. The text stays for versions/restore.
func (s *Store) Archive(ctx context.Context, a Actor, id uuid.UUID) error {
	cur, err := fetch(ctx, s.pool, a, id, false)
	if err != nil {
		return err
	}
	if err := canEdit(a, cur); err != nil {
		return err
	}
	sp, _, err := s.writableSpace(ctx, a, cur.SpaceID)
	if err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	locked, err := fetch(ctx, tx, a, id, true)
	if err != nil {
		return err
	}
	version := locked.Version + 1
	if _, err := tx.Exec(ctx, `UPDATE documents SET status = 'archived', version = $2, updated_at = now(), updated_by = $3
		WHERE id = $1`, id, version, a.User.ID); err != nil {
		return err
	}
	if err := writeVersion(ctx, tx, id, version, sp.ID, locked.Title, locked.Content, locked.Tags, "archived", a.User.ID); err != nil {
		return err
	}
	if err := record(ctx, tx, a, "archive", sp, id, nil); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func writeVersion(ctx context.Context, tx pgx.Tx, id uuid.UUID, version int, space uuid.UUID, title, content string, tags []string, status string, by uuid.UUID) error {
	_, err := tx.Exec(ctx, `INSERT INTO document_versions (document_id, version, space_id, title, content, tags, status, changed_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`, id, version, space, title, content, tags, status, by)
	return err
}

// record writes an audit event; inside personal spaces it keeps no details.
func record(ctx context.Context, tx pgx.Tx, a Actor, action string, sp spaces.Space, doc uuid.UUID, details map[string]any) error {
	uid, sid, did := a.User.ID, sp.ID, doc
	return audit.Insert(ctx, tx, audit.Event{
		UserID: &uid, Client: source(a), Action: action, SpaceID: &sid, DocumentID: &did,
		Personal: sp.Type == "personal", Details: details,
	})
}

// checkQuota enforces the personal-space limits. addDocs/addBytes are the growth the
// pending write causes;
func (s *Store) checkQuota(ctx context.Context, a Actor, space uuid.UUID, addDocs int, addBytes int64) error {
	maxDocs, maxBytes := s.opt.PersonalMaxDocuments, s.opt.PersonalMaxBytes
	if a.User.QuotaDocuments != nil {
		maxDocs = *a.User.QuotaDocuments
	}
	if a.User.QuotaBytes != nil {
		maxBytes = *a.User.QuotaBytes
	}
	var docs int
	var bytes int64
	err := s.pool.QueryRow(ctx, `
SELECT count(*) FILTER (WHERE kind = 'knowledge'), COALESCE(sum(octet_length(content)), 0)
FROM documents WHERE space_id = $1 AND status <> 'archived' AND (expires_at IS NULL OR expires_at > now())`,
		space).Scan(&docs, &bytes)
	if err != nil {
		return err
	}
	if maxDocs > 0 && addDocs > 0 && docs+addDocs > maxDocs {
		return errors.Join(ErrQuota, errors.New("zu viele einträge"))
	}
	if maxBytes > 0 && addBytes > 0 && bytes+addBytes > maxBytes {
		return errors.Join(ErrQuota, errors.New("speichergrenze erreicht"))
	}
	return nil
}

// handoffExpiry converts a TTL in days to an absolute time.
func handoffExpiry(days int) time.Time { return time.Now().Add(time.Duration(days) * 24 * time.Hour) }
