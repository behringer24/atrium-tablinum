package docs

import (
	"context"
	"errors"
	"fmt"
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
	// Type is the knowledge type (empty: documentation); rule needs a curator in a team space.
	Type string
	// OwnerID names the responsible person (team spaces only); the caller checked that they are
	// active and can read the space. ReviewDue is the review date.
	OwnerID   *uuid.UUID
	ReviewDue *time.Time
}

// checkMeta applies the rules for type, responsible person and review date of an entry in sp,
// written by an actor with role.
func checkMeta(sp spaces.Space, role authz.Role, typ string, owner *uuid.UUID) error {
	if !ValidType(typ) {
		return errors.Join(ingest.ErrInvalid, errors.New("type must be rule, documentation, recommendation or experience"))
	}
	if typ == TypeRule && sp.Type == "team" && role < authz.Curator {
		return ErrRule
	}
	if owner != nil && sp.Type != "team" {
		return errors.Join(ingest.ErrInvalid, errors.New("a responsible person can be named in team spaces only"))
	}
	return nil
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
		return StoreResult{}, errors.Join(ingest.ErrInvalid, errors.New("drafts exist only in team spaces"))
	}
	if p.Type == "" {
		p.Type = TypeDocumentation
	}
	if err := checkMeta(sp, role, p.Type, p.OwnerID); err != nil {
		return StoreResult{}, err
	}
	n, err := s.ing.Validate(p.Title, p.Content, p.Tags)
	if err != nil {
		return StoreResult{}, err
	}
	if err := s.checkQuota(ctx, sp, 1, int64(len(n.Content))); err != nil {
		return StoreResult{}, err
	}
	prep, err := s.ing.PrepareUpTo(ctx, n.Title, n.Content, s.opt.InlineIndexSections)
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
INSERT INTO documents (space_id, kind, title, content, tags, status, author_id, source, updated_by, knowledge_type, owner_id, review_due_at)
VALUES ($1, 'knowledge', $2, $3, $4, $5, $6, $7, $6, $8, $9, $10) RETURNING id`,
		sp.ID, n.Title, n.Content, n.Tags, status, a.User.ID, source(a), p.Type, p.OwnerID, p.ReviewDue).Scan(&id)
	if err != nil {
		return StoreResult{}, err
	}
	if err := writeVersion(ctx, tx, id, 1, sp.ID, n.Title, n.Content, n.Tags, status, a.User.ID); err != nil {
		return StoreResult{}, err
	}
	if err := s.ing.ReplaceChunks(ctx, tx, id, prep); err != nil {
		return StoreResult{}, err
	}
	if err := record(ctx, tx, a, "store", sp, id, map[string]any{"status": status, "type": p.Type}); err != nil {
		return StoreResult{}, err
	}
	doc, err := fetch(ctx, tx, a, id, false)
	if err != nil {
		return StoreResult{}, err
	}
	doc.IndexPending = prep.Pending
	return StoreResult{Document: &doc}, tx.Commit(ctx)
}

type UpdateParams struct {
	ID      uuid.UUID
	Title   *string
	Content *string
	Tags    *[]string
	Type    *string
	// ExpectedVersion is the version the caller started from. If it is set and the entry has moved
	// on, nothing is changed and the call fails with a *VersionConflict.
	ExpectedVersion *int
	// OwnerID names a new responsible person, ClearOwner removes the one named; the caller checked
	// that a new person is active and can read the space. ReviewDue and ClearReviewDue likewise.
	OwnerID        *uuid.UUID
	ClearOwner     bool
	ReviewDue      *time.Time
	ClearReviewDue bool
}

// VersionConflict is the error of an update that started from an outdated version. It matches
// ErrConflict, and its message tells the caller what to do.
type VersionConflict struct{ Expected, Current int }

func (e *VersionConflict) Error() string {
	return fmt.Sprintf("the entry is at version %d, not %d: somebody changed it in the meantime. "+
		"Nothing was saved. Load the entry again (get_knowledge), merge your change into the current text, "+
		"and call again with expected_version=%d.", e.Current, e.Expected, e.Current)
}

func (e *VersionConflict) Is(target error) bool { return target == ErrConflict }

// metaOnly reports whether p changes only the responsible person or the review date: neither
// says anything new about the content, so no version is written and the entry is not re-indexed.
func (p UpdateParams) metaOnly() bool {
	return p.Title == nil && p.Content == nil && p.Tags == nil && p.Type == nil
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

// Update replaces title, content, tags and/or type, re-indexes the entry if its text changed, and
// sets or clears the responsible person and the review date. A change of only the latter two
// writes no version.
func (s *Store) Update(ctx context.Context, a Actor, p UpdateParams) (Document, error) {
	cur, err := fetch(ctx, s.pool, a, p.ID, false)
	if err != nil {
		return Document{}, err
	}
	if cur.Kind != "knowledge" {
		return Document{}, errors.Join(ingest.ErrInvalid, errors.New("handoffs are not changed through update_knowledge"))
	}
	if err := canEdit(a, cur); err != nil {
		return Document{}, err
	}
	sp, role, err := s.writableSpace(ctx, a, cur.SpaceID)
	if err != nil {
		return Document{}, err
	}
	// A binding rule is the curators' business, whatever is changed about it.
	if cur.Type == TypeRule && sp.Type == "team" && role < authz.Curator {
		return Document{}, ErrRule
	}
	if p.ExpectedVersion != nil && *p.ExpectedVersion != cur.Version {
		return Document{}, &VersionConflict{Expected: *p.ExpectedVersion, Current: cur.Version}
	}

	title, content, tags, typ := cur.Title, cur.Content, cur.Tags, cur.Type
	if p.Title != nil {
		title = *p.Title
	}
	if p.Content != nil {
		content = *p.Content
	}
	if p.Tags != nil {
		tags = *p.Tags
	}
	if p.Type != nil {
		typ = *p.Type
	}
	owner, reviewDue := cur.OwnerID, cur.ReviewDue
	if p.OwnerID != nil || p.ClearOwner {
		owner = p.OwnerID
	}
	if p.ReviewDue != nil || p.ClearReviewDue {
		reviewDue = p.ReviewDue
	}
	if err := checkMeta(sp, role, typ, p.OwnerID); err != nil {
		return Document{}, err
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
	if p.metaOnly() {
		return s.updateMeta(ctx, a, sp, cur, owner, reviewDue)
	}
	if delta := int64(len(n.Content) - len(cur.Content)); delta > 0 {
		if err := s.checkQuota(ctx, sp, 0, delta); err != nil {
			return Document{}, err
		}
	}
	// The index depends on title and content only; tags and type need no new embeddings.
	reindex := n.Title != cur.Title || n.Content != cur.Content
	var prep ingest.Prepared
	if reindex {
		if prep, err = s.ing.PrepareUpTo(ctx, n.Title, n.Content, s.opt.InlineIndexSections); err != nil {
			return Document{}, err
		}
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
		version = $6, updated_at = now(), updated_by = $7, knowledge_type = $8, owner_id = $9, review_due_at = $10 WHERE id = $1`,
		p.ID, n.Title, n.Content, n.Tags, status, version, a.User.ID, typ, owner, reviewDue); err != nil {
		return Document{}, err
	}
	if err := writeVersion(ctx, tx, p.ID, version, sp.ID, n.Title, n.Content, n.Tags, status, a.User.ID); err != nil {
		return Document{}, err
	}
	if reindex {
		if err := s.ing.ReplaceChunks(ctx, tx, p.ID, prep); err != nil {
			return Document{}, err
		}
	}
	details := map[string]any{"version": version}
	if typ != cur.Type {
		details["type"] = typ
	}
	if err := record(ctx, tx, a, "update", sp, p.ID, details); err != nil {
		return Document{}, err
	}
	doc, err := fetch(ctx, tx, a, p.ID, false)
	if err != nil {
		return Document{}, err
	}
	doc.IndexPending = prep.Pending
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
	sp, role, err := s.writableSpace(ctx, a, cur.SpaceID)
	if err != nil {
		return err
	}
	// Like changing or moving, taking an approved entry away from the readers of a space with
	// approval is for curators: an author's own draft or returned entry is theirs to archive.
	if cur.Status == "approved" && sp.Type == "team" && sp.RequireApproval && role < authz.Curator {
		return ErrNeedsApproval
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
	if locked.Status == "approved" && sp.Type == "team" && sp.RequireApproval && role < authz.Curator {
		return ErrNeedsApproval // it was approved between the two reads
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

// Unarchive brings an archived entry back with the status it had before. Whoever may
// archive an entry may restore it. In an approval space an author's restored entry goes
// back to review, so the archive is no way around the release.
func (s *Store) Unarchive(ctx context.Context, a Actor, id uuid.UUID) (Document, error) {
	cur, err := fetchAny(ctx, s.pool, a, id, false)
	if err != nil {
		return Document{}, err
	}
	if cur.Kind != "knowledge" || cur.Status != "archived" {
		return Document{}, errors.Join(ingest.ErrInvalid, errors.New("the entry is not archived"))
	}
	if err := canEdit(a, cur); err != nil {
		return Document{}, err
	}
	sp, role, err := s.writableSpace(ctx, a, cur.SpaceID)
	if err != nil {
		return Document{}, err
	}
	if err := s.checkQuota(ctx, sp, 1, int64(len(cur.Content))); err != nil {
		return Document{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Document{}, err
	}
	defer tx.Rollback(ctx)
	locked, err := fetchAny(ctx, tx, a, id, true)
	if err != nil {
		return Document{}, err
	}
	if locked.Version != cur.Version || locked.Status != "archived" {
		return Document{}, ErrConflict
	}
	status := "approved"
	if err := tx.QueryRow(ctx, `SELECT status FROM document_versions
		WHERE document_id = $1 AND status <> 'archived' ORDER BY version DESC LIMIT 1`, id).Scan(&status); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return Document{}, err
	}
	if status == "approved" && sp.Type == "team" && sp.RequireApproval && role < authz.Curator {
		status = "draft"
	}
	version := locked.Version + 1
	if _, err := tx.Exec(ctx, `UPDATE documents SET status = $2, review_note = CASE WHEN $2 = 'draft' THEN NULL ELSE review_note END,
		version = $3, updated_at = now(), updated_by = $4 WHERE id = $1`, id, status, version, a.User.ID); err != nil {
		return Document{}, err
	}
	if err := writeVersion(ctx, tx, id, version, sp.ID, locked.Title, locked.Content, locked.Tags, status, a.User.ID); err != nil {
		return Document{}, err
	}
	if err := record(ctx, tx, a, "unarchive", sp, id, map[string]any{"version": version}); err != nil {
		return Document{}, err
	}
	doc, err := fetch(ctx, tx, a, id, false)
	if err != nil {
		return Document{}, err
	}
	return doc, tx.Commit(ctx)
}

// writeVersion records a state of an entry. The knowledge type is taken from the entry as it is in
// this transaction, so every caller writes the version after changing the entry.
func writeVersion(ctx context.Context, tx pgx.Tx, id uuid.UUID, version int, space uuid.UUID, title, content string, tags []string, status string, by uuid.UUID) error {
	_, err := tx.Exec(ctx, `INSERT INTO document_versions (document_id, version, space_id, title, content, tags, status, changed_by, knowledge_type)
		SELECT $1, $2, $3, $4, $5, $6, $7, $8, knowledge_type FROM documents WHERE id = $1`, id, version, space, title, content, tags, status, by)
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

// checkQuota enforces the limits of a space (see package quota): those of its owner in a
// personal space, its own in a team space, each falling back to the runtime and the configured
// defaults. addDocs/addBytes are the growth the pending write causes. The limits are read on
// every call, so a change by an instance admin applies to the next write.
func (s *Store) checkQuota(ctx context.Context, sp spaces.Space, addDocs int, addBytes int64) error {
	if s.opt.Quota == nil {
		return nil
	}
	lim, err := s.opt.Quota.Resolve(ctx, sp)
	if err != nil {
		return err
	}
	maxDocs, maxBytes := lim.Documents, lim.Bytes
	var docs int
	var bytes int64
	err = s.pool.QueryRow(ctx, `
SELECT count(*) FILTER (WHERE kind = 'knowledge'), COALESCE(sum(octet_length(content)), 0)
FROM documents WHERE space_id = $1 AND status <> 'archived' AND (expires_at IS NULL OR expires_at > now())`,
		sp.ID).Scan(&docs, &bytes)
	if err != nil {
		return err
	}
	if maxDocs > 0 && addDocs > 0 && docs+addDocs > maxDocs {
		return errors.Join(ErrQuota, fmt.Errorf("too many entries: the limit of this space is %d (an instance admin can raise it)", maxDocs))
	}
	if maxBytes > 0 && addBytes > 0 && bytes+addBytes > maxBytes {
		return errors.Join(ErrQuota, fmt.Errorf("storage limit reached: this space may hold %d KB (an instance admin can raise it)", maxBytes>>10))
	}
	return nil
}

// handoffExpiry converts a TTL in days to an absolute time.
func handoffExpiry(days int) time.Time { return time.Now().Add(time.Duration(days) * 24 * time.Hour) }
