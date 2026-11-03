// Package skills is the skill registry: versioned packages of text files (an Agent Skills
// style SKILL.md plus references) that an organisation offers to the assistants of its
// users. It is deliberately separate from the knowledge entries in package docs. A
// knowledge entry is untrusted data an assistant must never obey; a skill is an instruction
// the assistant follows when the user asks for it. Mixing the two would either weaken the
// protection against prompt injection or break skills, so they have their own tables, their
// own tools and their own review.
//
// Atrium stores, reviews, versions and serves skills. It never executes anything, and what a
// skill asks for (allowed-tools) is only a request: the client decides what it permits.
package skills

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"gopkg.in/yaml.v3"

	"github.com/behringer24/atrium-tablinum/internal/audit"
	"github.com/behringer24/atrium-tablinum/internal/authz"
	"github.com/behringer24/atrium-tablinum/internal/docs"
	"github.com/behringer24/atrium-tablinum/internal/guard"
	"github.com/behringer24/atrium-tablinum/internal/spaces"
)

var (
	ErrNotFound  = errors.New("not found")
	ErrForbidden = errors.New("you do not have permission for this in this space")
	ErrInvalid   = errors.New("invalid skill")
	// ErrState is returned when a version is not in the state the action needs.
	ErrState = errors.New("the skill version is not in the right state for this")
	// ErrOwnVersion is returned when an author tries to approve their own version.
	ErrOwnVersion = errors.New("the author of a skill version cannot approve it, another curator has to do that")
	// ErrTampered is returned when the stored files no longer match the digest of the version.
	ErrTampered = errors.New("the stored files do not match the digest of this version")
	// ErrArchived is returned when a change needs an active skill but the skill is archived.
	ErrArchived = errors.New("the skill is archived: restore it first (unarchive_skill)")
)

// Statuses of a version.
const (
	StatusDraft     = "draft"
	StatusApproved  = "approved"
	StatusRejected  = "rejected"
	StatusPublished = "published"
	StatusBlocked   = "blocked"
)

// EntryPoint is the file every skill must contain.
const EntryPoint = "SKILL.md"

// Limits of a package. They keep a skill small enough to be read by a model in one go.
const (
	MaxFiles        = 40
	MaxFileBytes    = 64 * 1024
	MaxPackageBytes = 256 * 1024
	maxPathBytes    = 200
	maxNameLen      = 64
	maxDescription  = 1024
	maxNote         = 2000
)

// allowedExt lists the file types a package may contain. Scripts and binaries are not
// among them: this version of the registry only distributes text.
var allowedExt = map[string]bool{".md": true, ".txt": true, ".json": true, ".yaml": true, ".yml": true, ".csv": true}

var nameRe = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// Options configures the store.
type Options struct {
	// AllowSelfApproval lets the author of a version also approve it. Off by default: it is
	// meant for small installations that have only one curator.
	AllowSelfApproval bool
}

type Store struct {
	pool *pgxpool.Pool
	sp   *spaces.Store
	opt  Options
}

func New(pool *pgxpool.Pool, sp *spaces.Store, opt Options) *Store {
	return &Store{pool: pool, sp: sp, opt: opt}
}

// File is one file of a package as submitted.
type File struct {
	Path    string
	Content string
}

// Resource describes a stored file.
type Resource struct {
	Path   string
	Size   int
	SHA256 string
}

// Version is one release candidate of a skill.
type Version struct {
	SkillID        uuid.UUID
	VersionID      uuid.UUID
	SpaceID        uuid.UUID
	Name           string
	Version        int
	Status         string
	Description    string
	RequestedTools string
	RiskClass      string
	Digest         string
	CreatedBy      *uuid.UUID
	CreatedAt      time.Time
	ReviewNote     string
	BlockReason    string
	// Archived is a property of the whole skill, repeated on each of its versions.
	Archived  bool
	Resources []Resource
}

// ---- validation ---------------------------------------------------------------------

type frontmatter struct {
	Name         string `yaml:"name"`
	Description  string `yaml:"description"`
	AllowedTools any    `yaml:"allowed-tools"`
}

// parseSkillMD reads the front matter of a SKILL.md and returns name, description and the
// requested tools as one space separated string.
func parseSkillMD(text string) (name, description, tools string, err error) {
	text = strings.TrimPrefix(text, "\ufeff")
	text = strings.ReplaceAll(text, "\r\n", "\n")
	if !strings.HasPrefix(text, "---\n") {
		return "", "", "", invalid("SKILL.md must start with a front matter block (--- name and description ---)")
	}
	rest := text[4:]
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return "", "", "", invalid("the front matter of SKILL.md is not closed with ---")
	}
	var fm frontmatter
	if err := yaml.Unmarshal([]byte(rest[:end]), &fm); err != nil {
		return "", "", "", invalid("the front matter of SKILL.md is not valid YAML")
	}
	switch v := fm.AllowedTools.(type) {
	case nil:
	case string:
		tools = strings.Join(strings.Fields(v), " ")
	case []any:
		var parts []string
		for _, x := range v {
			if s, ok := x.(string); ok && strings.TrimSpace(s) != "" {
				parts = append(parts, strings.TrimSpace(s))
			}
		}
		tools = strings.Join(parts, " ")
	default:
		return "", "", "", invalid("allowed-tools must be a string or a list of strings")
	}
	return strings.TrimSpace(fm.Name), strings.TrimSpace(fm.Description), tools, nil
}

func invalid(msg string) error { return fmt.Errorf("%w: %s", ErrInvalid, msg) }

// ValidName reports whether s is a legal skill name (lower case letters, digits, hyphens).
func ValidName(s string) bool { return len(s) <= maxNameLen && nameRe.MatchString(s) }

// validatePath rejects everything that is not a plain relative path of a text file.
func validatePath(p string) error {
	switch {
	case p == "" || len(p) > maxPathBytes:
		return invalid("a file path is empty or too long")
	case strings.ContainsAny(p, "\\\x00") || strings.HasPrefix(p, "/") || strings.HasSuffix(p, "/"):
		return invalid(fmt.Sprintf("the path %q must be relative and use forward slashes", p))
	}
	for _, r := range p {
		if unicode.IsControl(r) {
			return invalid(fmt.Sprintf("the path %q contains control characters", p))
		}
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == "" || seg == "." || seg == ".." || strings.HasPrefix(seg, ".") {
			return invalid(fmt.Sprintf("the path %q must not contain empty, hidden or relative segments", p))
		}
	}
	if strings.EqualFold(strings.SplitN(p, "/", 2)[0], "scripts") {
		return invalid("scripts are not supported: this registry only distributes text files")
	}
	if !allowedExt[strings.ToLower(path.Ext(p))] {
		return invalid(fmt.Sprintf("the file %q is not a text file of an allowed type (.md, .txt, .json, .yaml, .yml, .csv)", p))
	}
	return nil
}

type prepared struct {
	description string
	tools       string
	digest      string
	files       []File
	resources   []Resource
}

// prepare validates a submitted package and computes its digest.
func prepare(name string, files []File) (prepared, error) {
	var p prepared
	if !ValidName(name) {
		return p, invalid("the name must consist of lower case letters, digits and single hyphens (at most 64 characters)")
	}
	if len(files) == 0 || len(files) > MaxFiles {
		return p, invalid(fmt.Sprintf("a skill needs between 1 and %d files", MaxFiles))
	}
	seen := map[string]bool{}
	total := 0
	var entry string
	var hasEntry bool
	for _, f := range files {
		if err := validatePath(f.Path); err != nil {
			return p, err
		}
		key := strings.ToLower(f.Path)
		if seen[key] {
			return p, invalid(fmt.Sprintf("the path %q appears twice", f.Path))
		}
		seen[key] = true
		if !utf8.ValidString(f.Content) || strings.ContainsRune(f.Content, 0) {
			return p, invalid(fmt.Sprintf("%s is not valid UTF-8 text", f.Path))
		}
		if len(f.Content) > MaxFileBytes {
			return p, invalid(fmt.Sprintf("%s is larger than %d bytes", f.Path, MaxFileBytes))
		}
		total += len(f.Content)
		if f.Path == EntryPoint {
			entry, hasEntry = f.Content, true
		}
		if err := guard.Check(f.Content); err != nil {
			return p, fmt.Errorf("%s: %w", f.Path, err)
		}
	}
	if total > MaxPackageBytes {
		return p, invalid(fmt.Sprintf("the package is larger than %d bytes", MaxPackageBytes))
	}
	if !hasEntry {
		return p, invalid("the package needs a file named SKILL.md (exact spelling)")
	}
	fmName, desc, tools, err := parseSkillMD(entry)
	if err != nil {
		return p, err
	}
	if fmName != name {
		return p, invalid(fmt.Sprintf("the name in the front matter (%q) must equal the skill name (%q)", fmName, name))
	}
	if desc == "" || utf8.RuneCountInString(desc) > maxDescription {
		return p, invalid(fmt.Sprintf("the description is required and may have at most %d characters", maxDescription))
	}
	p.description, p.tools = desc, tools
	p.files = append([]File(nil), files...)
	sort.Slice(p.files, func(i, j int) bool { return p.files[i].Path < p.files[j].Path })
	for _, f := range p.files {
		p.resources = append(p.resources, Resource{Path: f.Path, Size: len(f.Content), SHA256: sum(f.Content)})
	}
	p.digest = digest(p.resources)
	return p, nil
}

func sum(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

// digest identifies the content of a package: the hash over all paths and file hashes in
// path order. It does not say the package is harmless, only that it is the one reviewed.
func digest(res []Resource) string {
	rs := append([]Resource(nil), res...)
	sort.Slice(rs, func(i, j int) bool { return rs[i].Path < rs[j].Path })
	h := sha256.New()
	for _, r := range rs {
		fmt.Fprintf(h, "%s\x00%s\n", r.Path, r.SHA256)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// ---- writing ------------------------------------------------------------------------

func record(ctx context.Context, tx pgx.Tx, a docs.Actor, action string, sp spaces.Space, details map[string]any) error {
	uid, sid := a.User.ID, sp.ID
	return audit.Insert(ctx, tx, audit.Event{
		UserID: &uid, Client: a.Client, Action: action, SpaceID: &sid,
		Personal: sp.Type == "personal", Details: details,
	})
}

func (s *Store) space(ctx context.Context, a docs.Actor, id uuid.UUID, min authz.Role) (spaces.Space, authz.Role, error) {
	role := a.Roles.Role(id)
	if role == authz.None {
		return spaces.Space{}, role, ErrNotFound
	}
	sp, err := s.sp.GetByID(ctx, id)
	if err != nil || sp.Archived {
		return spaces.Space{}, role, ErrNotFound
	}
	if role < min {
		return spaces.Space{}, role, ErrForbidden
	}
	return sp, role, nil
}

// Save stores a package as a new version of the skill, or replaces the draft of the same
// author. In a team space the result is a draft that a curator has to review. In a personal
// space only the owner can ever see it, so it is published at once.
func (s *Store) Save(ctx context.Context, a docs.Actor, spaceID uuid.UUID, name string, files []File) (Version, error) {
	p, err := prepare(name, files)
	if err != nil {
		return Version{}, err
	}
	sp, _, err := s.space(ctx, a, spaceID, authz.Author)
	if err != nil {
		return Version{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Version{}, err
	}
	defer tx.Rollback(ctx)

	var skillID uuid.UUID
	err = tx.QueryRow(ctx, `INSERT INTO skills (space_id, name, created_by) VALUES ($1, $2, $3)
		ON CONFLICT (space_id, name) DO UPDATE SET name = EXCLUDED.name RETURNING id`,
		sp.ID, name, a.User.ID).Scan(&skillID)
	if err != nil {
		return Version{}, err
	}
	// Serialise concurrent saves of the same skill.
	var archived bool
	if err := tx.QueryRow(ctx, `SELECT archived_at IS NOT NULL FROM skills WHERE id = $1 FOR UPDATE`, skillID).Scan(&archived); err != nil {
		return Version{}, err
	}
	if archived {
		return Version{}, ErrArchived
	}

	var latestID uuid.UUID
	var latest int
	var latestStatus string
	var latestBy *uuid.UUID
	err = tx.QueryRow(ctx, `SELECT id, version, status, created_by FROM skill_versions
		WHERE skill_id = $1 ORDER BY version DESC LIMIT 1`, skillID).Scan(&latestID, &latest, &latestStatus, &latestBy)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return Version{}, err
	}

	var versionID uuid.UUID
	version := latest + 1
	if err == nil && latestStatus == StatusDraft && latestBy != nil && *latestBy == a.User.ID {
		// The author revises their own draft: same version, new content.
		versionID, version = latestID, latest
		if _, err := tx.Exec(ctx, `DELETE FROM skill_resources WHERE version_id = $1`, versionID); err != nil {
			return Version{}, err
		}
		if _, err := tx.Exec(ctx, `UPDATE skill_versions SET description = $2, requested_tools = $3, digest = $4, created_at = now()
			WHERE id = $1`, versionID, p.description, p.tools, p.digest); err != nil {
			return Version{}, err
		}
	} else if err := tx.QueryRow(ctx, `INSERT INTO skill_versions (skill_id, version, description, requested_tools, digest, created_by)
		VALUES ($1, $2, $3, $4, $5, $6) RETURNING id`,
		skillID, version, p.description, p.tools, p.digest, a.User.ID).Scan(&versionID); err != nil {
		return Version{}, err
	}
	for i, f := range p.files {
		if _, err := tx.Exec(ctx, `INSERT INTO skill_resources (version_id, path, content, size, sha256) VALUES ($1, $2, $3, $4, $5)`,
			versionID, f.Path, f.Content, p.resources[i].Size, p.resources[i].SHA256); err != nil {
			return Version{}, err
		}
	}
	action := "skill_save"
	if sp.Type == "personal" {
		if _, err := tx.Exec(ctx, `UPDATE skill_versions SET status = 'published', risk_class = 'A', published_by = $2, published_at = now()
			WHERE id = $1`, versionID, a.User.ID); err != nil {
			return Version{}, err
		}
		action = "skill_publish"
	}
	if err := record(ctx, tx, a, action, sp, map[string]any{"skill": name, "version": version, "digest": p.digest}); err != nil {
		return Version{}, err
	}
	v, err := loadVersion(ctx, tx, versionID)
	if err != nil {
		return Version{}, err
	}
	return v, tx.Commit(ctx)
}

// Ref names one version: the skill by id and the version number.
type Ref struct {
	SkillID uuid.UUID
	Version int
}

// ReviewParams is the decision of a curator on a draft.
type ReviewParams struct {
	Ref       Ref
	Approve   bool
	Note      string
	RiskClass string // required to approve: A, B or C
}

// Review approves or rejects a draft. Only curators of the space may, and not for their own
// version unless the instance allows self-approval.
func (s *Store) Review(ctx context.Context, a docs.Actor, p ReviewParams) (Version, error) {
	note := strings.TrimSpace(p.Note)
	if utf8.RuneCountInString(note) > maxNote {
		return Version{}, invalid("the note is too long")
	}
	if !p.Approve && note == "" {
		return Version{}, invalid("a note for the author is required when rejecting")
	}
	risk := strings.ToUpper(strings.TrimSpace(p.RiskClass))
	if p.Approve && risk != "A" && risk != "B" && risk != "C" {
		return Version{}, invalid("approving needs a risk class: A (instructions only), B (uses tools), C (critical actions)")
	}
	return s.transition(ctx, a, p.Ref, authz.Curator, func(v Version, sp spaces.Space) (string, map[string]any, error) {
		if v.Archived {
			return "", nil, ErrArchived
		}
		if v.Status != StatusDraft {
			return "", nil, ErrState
		}
		if v.CreatedBy != nil && *v.CreatedBy == a.User.ID && !s.opt.AllowSelfApproval {
			return "", nil, ErrOwnVersion
		}
		if !p.Approve {
			return "skill_reject", map[string]any{"skill": v.Name, "version": v.Version}, nil
		}
		if v.RequestedTools != "" && risk == "A" {
			return "", nil, invalid("the skill asks for tools (" + v.RequestedTools + "), so its risk class cannot be A")
		}
		return "skill_approve", map[string]any{"skill": v.Name, "version": v.Version, "risk_class": risk}, nil
	}, func(ctx context.Context, tx pgx.Tx, v Version, action string) error {
		if action == "skill_reject" {
			_, err := tx.Exec(ctx, `UPDATE skill_versions SET status = 'rejected', reviewed_by = $2, reviewed_at = now(), review_note = $3 WHERE id = $1`,
				v.VersionID, a.User.ID, note)
			return err
		}
		_, err := tx.Exec(ctx, `UPDATE skill_versions SET status = 'approved', risk_class = $4, reviewed_by = $2, reviewed_at = now(), review_note = NULLIF($3, '') WHERE id = $1`,
			v.VersionID, a.User.ID, note, risk)
		return err
	})
}

// Publish makes an approved version available to the readers of the space. The stored
// files are checked against the digest once more. Class C skills need an instance admin.
func (s *Store) Publish(ctx context.Context, a docs.Actor, ref Ref) (Version, error) {
	return s.transition(ctx, a, ref, authz.Curator, func(v Version, sp spaces.Space) (string, map[string]any, error) {
		if v.Archived {
			return "", nil, ErrArchived
		}
		if v.Status != StatusApproved {
			return "", nil, ErrState
		}
		if v.RiskClass == "C" && !a.User.InstanceAdmin {
			return "", nil, fmt.Errorf("%w: a skill of risk class C can only be published by an instance admin", ErrForbidden)
		}
		return "skill_publish", map[string]any{"skill": v.Name, "version": v.Version, "digest": v.Digest}, nil
	}, func(ctx context.Context, tx pgx.Tx, v Version, _ string) error {
		if err := verifyStored(ctx, tx, v); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `UPDATE skill_versions SET status = 'published', published_by = $2, published_at = now() WHERE id = $1`, v.VersionID, a.User.ID)
		return err
	})
}

// verifyStored recomputes the hashes from the stored text and compares the digest with the
// one recorded when the version was saved.
func verifyStored(ctx context.Context, q querier, v Version) error {
	rows, err := q.Query(ctx, `SELECT path, content FROM skill_resources WHERE version_id = $1`, v.VersionID)
	if err != nil {
		return err
	}
	defer rows.Close()
	var res []Resource
	for rows.Next() {
		var p, c string
		if err := rows.Scan(&p, &c); err != nil {
			return err
		}
		res = append(res, Resource{Path: p, SHA256: sum(c)})
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if digest(res) != v.Digest {
		return ErrTampered
	}
	return nil
}

// Block withdraws an approved or published version: new reads are refused. Copies a client
// already fetched are not recalled; that needs the cooperation of the client.
func (s *Store) Block(ctx context.Context, a docs.Actor, ref Ref, reason string) (Version, error) {
	reason = strings.TrimSpace(reason)
	if reason == "" || utf8.RuneCountInString(reason) > maxNote {
		return Version{}, invalid("a reason is required (at most 2000 characters)")
	}
	// Blocking stays possible on an archived skill: withdrawing is always the safe direction.
	return s.transition(ctx, a, ref, authz.Curator, func(v Version, sp spaces.Space) (string, map[string]any, error) {
		if v.Status != StatusApproved && v.Status != StatusPublished {
			return "", nil, ErrState
		}
		return "skill_block", map[string]any{"skill": v.Name, "version": v.Version, "reason": reason}, nil
	}, func(ctx context.Context, tx pgx.Tx, v Version, _ string) error {
		_, err := tx.Exec(ctx, `UPDATE skill_versions SET status = 'blocked', blocked_by = $2, blocked_at = now(), block_reason = $3 WHERE id = $1`,
			v.VersionID, a.User.ID, reason)
		return err
	})
}

// Unblock lifts a block. Only admins of the space (instance admins in every team space) may,
// with a reason for the audit log. The version does not become published again but
// approved: a curator has to release it explicitly, and publishing checks the stored files
// against the digest once more. The block itself stays in the audit log.
func (s *Store) Unblock(ctx context.Context, a docs.Actor, ref Ref, reason string) (Version, error) {
	reason = strings.TrimSpace(reason)
	if reason == "" || utf8.RuneCountInString(reason) > maxNote {
		return Version{}, invalid("a reason is required (at most 2000 characters)")
	}
	return s.transition(ctx, a, ref, authz.Admin, func(v Version, sp spaces.Space) (string, map[string]any, error) {
		if v.Status != StatusBlocked {
			return "", nil, ErrState
		}
		return "skill_unblock", map[string]any{"skill": v.Name, "version": v.Version, "reason": reason, "block_reason": v.BlockReason}, nil
	}, func(ctx context.Context, tx pgx.Tx, v Version, _ string) error {
		_, err := tx.Exec(ctx, `UPDATE skill_versions SET status = 'approved', blocked_by = NULL, blocked_at = NULL, block_reason = NULL
			WHERE id = $1`, v.VersionID)
		return err
	})
}

// Archive hides a whole skill, all its versions, from everyone who cannot restore it. Nothing
// is deleted, and a skill in use stops being offered at once.
func (s *Store) Archive(ctx context.Context, a docs.Actor, skillID uuid.UUID) error {
	return s.skillAction(ctx, a, skillID, "skill_archive", func(archived bool, sp spaces.Space) error {
		if archived {
			return ErrState
		}
		if a.Roles.Role(sp.ID) < authz.Curator {
			return ErrForbidden
		}
		return nil
	}, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE skills SET archived_at = now(), archived_by = $2 WHERE id = $1`, skillID, a.User.ID)
		return err
	})
}

// Unarchive brings an archived skill back with its versions in the state they had.
func (s *Store) Unarchive(ctx context.Context, a docs.Actor, skillID uuid.UUID) error {
	return s.skillAction(ctx, a, skillID, "skill_unarchive", func(archived bool, sp spaces.Space) error {
		if !archived {
			return ErrState
		}
		if a.Roles.Role(sp.ID) < authz.Curator {
			return ErrForbidden
		}
		return nil
	}, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `UPDATE skills SET archived_at = NULL, archived_by = NULL WHERE id = $1`, skillID)
		return err
	})
}

// Delete removes a skill with all its versions and files for good. In a team space only an
// instance admin may: skills are instructions that assistants followed, so removing the
// evidence is not a space matter. In a personal space the owner may. Everyone else archives
// or blocks. The audit log keeps name, versions and digests of what was deleted.
func (s *Store) Delete(ctx context.Context, a docs.Actor, skillID uuid.UUID) error {
	return s.skillAction(ctx, a, skillID, "skill_delete", func(_ bool, sp spaces.Space) error {
		if sp.Type == "personal" {
			if a.Roles.Role(sp.ID) < authz.Admin {
				return ErrForbidden
			}
			return nil
		}
		if !a.User.InstanceAdmin {
			return fmt.Errorf("%w: only an instance admin can delete a skill of a team space; archive or block it instead", ErrForbidden)
		}
		return nil
	}, func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `DELETE FROM skills WHERE id = $1`, skillID)
		return err
	})
}

// skillAction applies a change to a whole skill under lock. check decides whether the actor
// may, from the archive state and the space. The audit event carries the name and the
// versions of the skill; for a delete that is all that is left afterwards.
func (s *Store) skillAction(ctx context.Context, a docs.Actor, skillID uuid.UUID, action string,
	check func(archived bool, sp spaces.Space) error, apply func(context.Context, pgx.Tx) error) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var name string
	var spaceID uuid.UUID
	var archived bool
	err = tx.QueryRow(ctx, `SELECT name, space_id, archived_at IS NOT NULL FROM skills WHERE id = $1 FOR UPDATE`, skillID).Scan(&name, &spaceID, &archived)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	// Whoever cannot even see the skill (it is archived and they cannot restore it, or the space
	// is not theirs) gets "not found", not "forbidden".
	sp, _, err := s.space(ctx, a, spaceID, authz.Curator)
	if err != nil {
		return err
	}
	if err := check(archived, sp); err != nil {
		return err
	}
	versions, err := listVersions(ctx, tx, skillID)
	if err != nil {
		return err
	}
	if err := apply(ctx, tx); err != nil {
		return err
	}
	if err := record(ctx, tx, a, action, sp, map[string]any{"skill": name, "versions": versions}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// listVersions summarises the versions of a skill for the audit log.
func listVersions(ctx context.Context, q querier, skillID uuid.UUID) ([]map[string]any, error) {
	rows, err := q.Query(ctx, `SELECT version, status, digest FROM skill_versions WHERE skill_id = $1 ORDER BY version`, skillID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []map[string]any
	for rows.Next() {
		var n int
		var st, dg string
		if err := rows.Scan(&n, &st, &dg); err != nil {
			return nil, err
		}
		out = append(out, map[string]any{"version": n, "status": st, "digest": dg})
	}
	return out, rows.Err()
}

// transition loads a version under lock, checks that the actor has at least the role min in
// its space, lets check decide the action and its audit details, applies it and records the
// event.
func (s *Store) transition(ctx context.Context, a docs.Actor, ref Ref, min authz.Role,
	check func(Version, spaces.Space) (string, map[string]any, error),
	apply func(context.Context, pgx.Tx, Version, string) error) (Version, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Version{}, err
	}
	defer tx.Rollback(ctx)
	var versionID, spaceID uuid.UUID
	err = tx.QueryRow(ctx, `SELECT v.id, s.space_id FROM skill_versions v JOIN skills s ON s.id = v.skill_id
		WHERE v.skill_id = $1 AND v.version = $2 FOR UPDATE OF v`, ref.SkillID, ref.Version).Scan(&versionID, &spaceID)
	if errors.Is(err, pgx.ErrNoRows) {
		return Version{}, ErrNotFound
	} else if err != nil {
		return Version{}, err
	}
	sp, _, err := s.space(ctx, a, spaceID, min)
	if err != nil {
		return Version{}, err
	}
	v, err := loadVersion(ctx, tx, versionID)
	if err != nil {
		return Version{}, err
	}
	action, details, err := check(v, sp)
	if err != nil {
		return Version{}, err
	}
	if err := apply(ctx, tx, v, action); err != nil {
		return Version{}, err
	}
	if err := record(ctx, tx, a, action, sp, details); err != nil {
		return Version{}, err
	}
	out, err := loadVersion(ctx, tx, versionID)
	if err != nil {
		return Version{}, err
	}
	return out, tx.Commit(ctx)
}

// ---- reading ------------------------------------------------------------------------

type querier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

const versionSelect = `SELECT s.id, v.id, s.space_id, s.name, v.version, v.status, v.description, v.requested_tools,
  COALESCE(v.risk_class, ''), v.digest, v.created_by, v.created_at, COALESCE(v.review_note, ''), COALESCE(v.block_reason, ''),
  s.archived_at IS NOT NULL
FROM skill_versions v JOIN skills s ON s.id = v.skill_id `

func scanVersion(row pgx.Row) (Version, error) {
	var v Version
	err := row.Scan(&v.SkillID, &v.VersionID, &v.SpaceID, &v.Name, &v.Version, &v.Status, &v.Description, &v.RequestedTools,
		&v.RiskClass, &v.Digest, &v.CreatedBy, &v.CreatedAt, &v.ReviewNote, &v.BlockReason, &v.Archived)
	return v, err
}

func loadVersion(ctx context.Context, q querier, id uuid.UUID) (Version, error) {
	v, err := scanVersion(q.QueryRow(ctx, versionSelect+`WHERE v.id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return Version{}, ErrNotFound
	} else if err != nil {
		return Version{}, err
	}
	rows, err := q.Query(ctx, `SELECT path, size, sha256 FROM skill_resources WHERE version_id = $1 ORDER BY path`, id)
	if err != nil {
		return Version{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var r Resource
		if err := rows.Scan(&r.Path, &r.Size, &r.SHA256); err != nil {
			return Version{}, err
		}
		v.Resources = append(v.Resources, r)
	}
	return v, rows.Err()
}

// visibleSQL is the one predicate that decides which versions a caller may see: published
// versions in spaces they can read, and any other state only for the author and the
// curators of the space (archived skills: only those who may restore them).
// $1 readable spaces, $2 the caller, $3 spaces where they curate.
const visibleSQL = `(s.space_id = ANY($1::uuid[])
  AND (v.status = 'published' OR v.created_by = $2::uuid OR s.space_id = ANY($3::uuid[]))
  AND (s.archived_at IS NULL OR s.space_id = ANY($3::uuid[])))`

func scope(a docs.Actor) (visible []uuid.UUID, curator []uuid.UUID) {
	return a.Roles.Spaces(authz.Reader), a.Roles.Spaces(authz.Curator)
}

// ListFilter narrows List. Zero values mean "any".
type ListFilter struct {
	Name          string
	SpaceID       uuid.UUID
	PublishedOnly bool
	// Archive selects by archive state: "" only active skills, ArchiveOnly only archived
	// ones, ArchiveAny both. Archived skills are visible to curators of the space only.
	Archive string
	Limit   int
}

// Values of ListFilter.Archive.
const (
	ArchiveOnly = "only"
	ArchiveAny  = "any"
)

// List returns the versions the caller may see, newest version of each skill first.
func (s *Store) List(ctx context.Context, a docs.Actor, f ListFilter) ([]Version, error) {
	vis, cur := scope(a)
	limit := f.Limit
	if limit <= 0 || limit > 500 {
		limit = 500
	}
	rows, err := s.pool.Query(ctx, versionSelect+`WHERE `+visibleSQL+`
		AND ($4 = '' OR s.name = $4) AND ($5::uuid = '00000000-0000-0000-0000-000000000000'::uuid OR s.space_id = $5)
		AND (NOT $6 OR v.status = 'published')
		AND ($8 = 'any' OR ($8 = 'only') = (s.archived_at IS NOT NULL))
		ORDER BY s.name, s.space_id, v.version DESC LIMIT $7`,
		vis, a.User.ID, cur, f.Name, f.SpaceID, f.PublishedOnly, limit, f.Archive)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Version
	for rows.Next() {
		v, err := scanVersion(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// Get loads one version with its file list. Version 0 means the newest published version.
// Whatever the caller may not see is reported as not found.
func (s *Store) Get(ctx context.Context, a docs.Actor, skillID uuid.UUID, version int) (Version, error) {
	vis, cur := scope(a)
	q := versionSelect + `WHERE s.id = $4 AND ` + visibleSQL
	args := []any{vis, a.User.ID, cur, skillID}
	if version > 0 {
		q += ` AND v.version = $5`
		args = append(args, version)
	} else {
		q += ` AND v.status = 'published' ORDER BY v.version DESC LIMIT 1`
	}
	v, err := scanVersion(s.pool.QueryRow(ctx, q, args...))
	if errors.Is(err, pgx.ErrNoRows) {
		return Version{}, ErrNotFound
	} else if err != nil {
		return Version{}, err
	}
	return loadVersion(ctx, s.pool, v.VersionID)
}

// Resource returns the content of one file of a version the caller may see, after checking
// it against the hash recorded in the manifest.
func (s *Store) Resource(ctx context.Context, a docs.Actor, v Version, path string) (string, Resource, error) {
	// Get has already applied the visibility rule to v; check again through the same
	// predicate so a direct call cannot skip it.
	if _, err := s.Get(ctx, a, v.SkillID, v.Version); err != nil {
		return "", Resource{}, err
	}
	var r Resource
	var content string
	err := s.pool.QueryRow(ctx, `SELECT path, size, sha256, content FROM skill_resources WHERE version_id = $1 AND path = $2`,
		v.VersionID, path).Scan(&r.Path, &r.Size, &r.SHA256, &content)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", Resource{}, ErrNotFound
	} else if err != nil {
		return "", Resource{}, err
	}
	if sum(content) != r.SHA256 {
		return "", Resource{}, ErrTampered
	}
	return content, r, nil
}

// Audit records that a caller read a skill. Reads of a personal space keep no details.
func (s *Store) Audit(ctx context.Context, a docs.Actor, v Version, action string) {
	sp, err := s.sp.GetByID(ctx, v.SpaceID)
	if err != nil {
		return
	}
	uid, sid := a.User.ID, sp.ID
	_ = audit.Insert(ctx, s.pool, audit.Event{
		UserID: &uid, Client: a.Client, Action: action, SpaceID: &sid, Personal: sp.Type == "personal",
		Details: map[string]any{"skill": v.Name, "version": v.Version},
	})
}
