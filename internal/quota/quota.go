// Package quota holds the storage limits of spaces and how they are resolved. The defaults
// come from config.yaml; an instance admin overrides them at runtime for all personal spaces,
// all team spaces, one user or one space, and a change applies to the next write.
package quota

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/behringer24/atrium-tablinum/internal/spaces"
)

// Limits are resolved values. 0 means unlimited.
type Limits struct {
	Documents     int   // knowledge entries in the space (archived ones do not count)
	Bytes         int64 // bytes of the entries and handoffs in the space
	Skills        int   // skills in the space
	SkillBytes    int64 // bytes of skill files in the space, all versions
	SkillVersions int   // versions of one skill
}

// Defaults are the limits from config.yaml, one set for personal and one for team spaces.
type Defaults struct{ Personal, Team Limits }

// Scope says whom an override applies to.
type Scope string

const (
	PersonalDefault Scope = "personal_default" // all personal spaces without a user override
	TeamDefault     Scope = "team_default"     // all team spaces without a space override
	Space           Scope = "space"            // one team space
	User            Scope = "user"             // the personal space of one user
)

// Fields name the five limits in the order they are shown.
var Fields = []string{"documents", "bytes", "skills", "skill_bytes", "skill_versions"}

// Overrides are the values stored for one scope. Nil inherits from the next level.
type Overrides struct {
	Documents     *int64
	Bytes         *int64
	Skills        *int64
	SkillBytes    *int64
	SkillVersions *int64
}

func (o *Overrides) slot(name string) **int64 {
	switch name {
	case "documents":
		return &o.Documents
	case "bytes":
		return &o.Bytes
	case "skills":
		return &o.Skills
	case "skill_bytes":
		return &o.SkillBytes
	case "skill_versions":
		return &o.SkillVersions
	}
	return nil
}

// Get returns the value of a field by name (nil = inherited).
func (o Overrides) Get(name string) *int64 {
	if p := o.slot(name); p != nil {
		return *p
	}
	return nil
}

// Resolved is the effective set of limits for one space.
type Resolved struct {
	Limits
	// Source says per field where the value comes from: user, space, runtime default or config.
	Source map[string]string
}

// Usage is what a space holds.
type Usage struct {
	Documents  int   `json:"documents"`
	Bytes      int64 `json:"bytes"`
	Skills     int   `json:"skills"`
	SkillBytes int64 `json:"skill_bytes"`
}

type Store struct {
	pool *pgxpool.Pool
	def  Defaults
}

func New(pool *pgxpool.Pool, def Defaults) *Store { return &Store{pool: pool, def: def} }

// ConfigDefaults returns the limits from config.yaml.
func (s *Store) ConfigDefaults() Defaults { return s.def }

func scan(row pgx.Row) (Overrides, bool, error) {
	var d, sk, v *int32
	var b, sb *int64
	err := row.Scan(&d, &b, &sk, &sb, &v)
	if errors.Is(err, pgx.ErrNoRows) {
		return Overrides{}, false, nil
	}
	if err != nil {
		return Overrides{}, false, err
	}
	conv := func(p *int32) *int64 {
		if p == nil {
			return nil
		}
		x := int64(*p)
		return &x
	}
	return Overrides{Documents: conv(d), Bytes: b, Skills: conv(sk), SkillBytes: sb, SkillVersions: conv(v)}, true, nil
}

const rawSQL = `SELECT documents, bytes, skills, skill_bytes, skill_versions FROM quotas WHERE scope = $1 AND scope_id = $2`

// Raw returns the overrides stored for a scope; ok is false if there is none.
func (s *Store) Raw(ctx context.Context, scope Scope, id uuid.UUID) (Overrides, bool, error) {
	return scan(s.pool.QueryRow(ctx, rawSQL, string(scope), id))
}

// Resolve returns the effective limits of a space: the override of the user (personal space)
// or of the space (team space), else the runtime default of the kind, else config.yaml.
// Every field is resolved on its own.
func (s *Store) Resolve(ctx context.Context, sp spaces.Space) (Resolved, error) {
	type level struct {
		name string
		o    Overrides
		ok   bool
	}
	base, defScope := s.def.Team, TeamDefault
	own, ownName, ownID := Space, "space", sp.ID
	if sp.Type == "personal" {
		base, defScope = s.def.Personal, PersonalDefault
		own, ownName, ownID = User, "user", uuid.Nil
		if sp.OwnerUserID != nil {
			ownID = *sp.OwnerUserID
		}
	}
	var levels []level
	for _, l := range []struct {
		scope Scope
		id    uuid.UUID
		name  string
	}{{own, ownID, ownName}, {defScope, uuid.Nil, "runtime default"}} {
		o, ok, err := s.Raw(ctx, l.scope, l.id)
		if err != nil {
			return Resolved{}, err
		}
		levels = append(levels, level{l.name, o, ok})
	}
	r := Resolved{Source: map[string]string{}}
	resolve := func(field string, cfg int64) int64 {
		for _, l := range levels {
			if l.ok {
				if v := l.o.Get(field); v != nil {
					r.Source[field] = l.name
					return *v
				}
			}
		}
		r.Source[field] = "config"
		return cfg
	}
	r.Documents = int(resolve("documents", int64(base.Documents)))
	r.Bytes = resolve("bytes", base.Bytes)
	r.Skills = int(resolve("skills", int64(base.Skills)))
	r.SkillBytes = resolve("skill_bytes", base.SkillBytes)
	r.SkillVersions = int(resolve("skill_versions", int64(base.SkillVersions)))
	return r, nil
}

// ResolveDefault returns the effective defaults of personal or team spaces: the runtime
// default where set, else config.yaml.
func (s *Store) ResolveDefault(ctx context.Context, scope Scope) (Resolved, error) {
	switch scope {
	case PersonalDefault:
		return s.Resolve(ctx, spaces.Space{Type: "personal"})
	case TeamDefault:
		return s.Resolve(ctx, spaces.Space{Type: "team", ID: uuid.Nil})
	}
	return Resolved{}, fmt.Errorf("not a default scope: %s", scope)
}

// Patch holds new values: a value >= 0 sets the limit (0 = unlimited), -1 removes the
// override so that the field inherits again, nil leaves it as it is.
type Patch struct {
	Documents, Bytes, Skills, SkillBytes, SkillVersions *int64
}

// Get returns the new value of a field (nil = unchanged).
func (p Patch) Get(name string) *int64 {
	return map[string]*int64{"documents": p.Documents, "bytes": p.Bytes, "skills": p.Skills,
		"skill_bytes": p.SkillBytes, "skill_versions": p.SkillVersions}[name]
}

// Set applies a patch to the overrides of a scope and returns the overrides before and after.
// A row without any value left is removed.
func (s *Store) Set(ctx context.Context, scope Scope, id uuid.UUID, p Patch, by uuid.UUID) (before, after Overrides, err error) {
	for _, f := range Fields {
		if v := p.Get(f); v != nil && *v < -1 {
			return before, after, fmt.Errorf("%s: use 0 or more (0 = unlimited) or -1 to inherit again", f)
		}
	}
	if scope == PersonalDefault || scope == TeamDefault {
		id = uuid.Nil
	}
	// The audit of who changed it is a user; callers without one (tests, tools) pass uuid.Nil.
	var updatedBy any
	if by != uuid.Nil {
		updatedBy = by
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return before, after, err
	}
	defer tx.Rollback(ctx)
	before, _, err = scan(tx.QueryRow(ctx, rawSQL+` FOR UPDATE`, string(scope), id))
	if err != nil {
		return before, after, err
	}
	after = before
	for _, f := range Fields {
		v := p.Get(f)
		if v == nil {
			continue
		}
		slot := after.slot(f)
		if *v == -1 {
			*slot = nil
		} else {
			x := *v
			*slot = &x
		}
	}
	if after == (Overrides{}) {
		_, err = tx.Exec(ctx, `DELETE FROM quotas WHERE scope = $1 AND scope_id = $2`, string(scope), id)
	} else {
		_, err = tx.Exec(ctx, `INSERT INTO quotas (scope, scope_id, documents, bytes, skills, skill_bytes, skill_versions, updated_by, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, now())
			ON CONFLICT (scope, scope_id) DO UPDATE SET documents = EXCLUDED.documents, bytes = EXCLUDED.bytes, skills = EXCLUDED.skills,
			  skill_bytes = EXCLUDED.skill_bytes, skill_versions = EXCLUDED.skill_versions, updated_by = EXCLUDED.updated_by, updated_at = now()`,
			string(scope), id, after.Documents, after.Bytes, after.Skills, after.SkillBytes, after.SkillVersions, updatedBy)
	}
	if err != nil {
		return before, after, err
	}
	return before, after, tx.Commit(ctx)
}

// Usage counts what a space holds, with the same rules the limits are checked with.
func (s *Store) Usage(ctx context.Context, space uuid.UUID) (Usage, error) {
	var u Usage
	err := s.pool.QueryRow(ctx, `
SELECT count(*) FILTER (WHERE kind = 'knowledge'), COALESCE(sum(octet_length(content)), 0)
FROM documents WHERE space_id = $1 AND status <> 'archived' AND (expires_at IS NULL OR expires_at > now())`, space).Scan(&u.Documents, &u.Bytes)
	if err != nil {
		return u, err
	}
	err = s.pool.QueryRow(ctx, `
SELECT (SELECT count(*) FROM skills WHERE space_id = $1),
       COALESCE((SELECT sum(r.size) FROM skill_resources r JOIN skill_versions v ON v.id = r.version_id
                 JOIN skills k ON k.id = v.skill_id WHERE k.space_id = $1), 0)`, space).Scan(&u.Skills, &u.SkillBytes)
	return u, err
}
