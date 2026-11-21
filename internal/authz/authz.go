package authz

import (
	"context"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/behringer24/atrium-tablinum/internal/users"
)

// DefaultCacheTTL is how long a user's computed roles are reused.
const DefaultCacheTTL = 30 * time.Second

// AdminsReadContent says whether instance admins hold the admin role in every team space (and see
// the archived ones). It is called whenever the roles of an instance admin are computed; an edition
// may replace it. The default is yes. If it says no, an instance admin has the roles of a member
// of each space like any other user, while the admin tools still work on all spaces (adminSpaces).
var AdminsReadContent = func(context.Context, *pgxpool.Pool) bool { return true }

// spaceRow is one space together with what the database knows about the caller's
// membership in it. Loading and deciding are separate so the rules are unit-testable.
type spaceRow struct {
	ID          uuid.UUID
	Personal    bool
	Owner       *uuid.UUID
	DefaultRole Role // None = no access for non-members
	Archived    bool
	MemberRole  Role // best of direct and group membership in this space itself, None if none
	// Parent is the space above this one, nil for a root. Without any parent set every space is a
	// root and the roles are those of a flat instance.
	Parent *uuid.UUID
	// NoInherit stops the lookup of memberships at this space: it does not take over what the
	// spaces above grant. The zero value inherits.
	NoInherit bool
}

// inheritedRole returns the membership role the user holds in s through the hierarchy: the role of
// the nearest space on the way up (s itself first) that has an entry for the user. The first level
// with an entry decides, even if a space further up grants more, so that an explicit entry in a
// child can lower what the parent would give. A space with NoInherit, a root, a parent that does
// not exist and a cycle all end the walk without a role. A parent that is archived still passes its
// memberships on: archiving it must not silently cut the access to its children.
func inheritedRole(s spaceRow, byID map[uuid.UUID]spaceRow) Role {
	cur := s
	for range len(byID) + 1 { // never longer than the number of spaces, so a cycle ends the walk
		if cur.MemberRole > None {
			return cur.MemberRole
		}
		if cur.NoInherit || cur.Parent == nil {
			return None
		}
		next, ok := byID[*cur.Parent]
		if !ok || next.Personal {
			return None
		}
		cur = next
	}
	return None
}

// compute applies the rules of concept section 7.3 for one user.
func compute(u users.User, rows []spaceRow) Roles {
	roles := Roles{}
	if !u.Active {
		return roles
	}
	var byID map[uuid.UUID]spaceRow
	for _, s := range rows {
		if s.Parent != nil {
			byID = make(map[uuid.UUID]spaceRow, len(rows))
			for _, r := range rows {
				byID[r.ID] = r
			}
			break
		}
	}
	for _, s := range rows {
		if s.Personal {
			// Only the owner, never members, defaults or instance admins.
			if s.Owner != nil && *s.Owner == u.ID && !s.Archived {
				roles[s.ID] = Admin
			}
			continue
		}
		if s.Archived {
			// Archived team spaces exist only for instance admins.
			if u.InstanceAdmin {
				roles[s.ID] = Admin
			}
			continue
		}
		member := s.MemberRole
		if byID != nil {
			member = inheritedRole(s, byID)
		}
		role := max(member, s.DefaultRole)
		if u.InstanceAdmin {
			role = Admin
		}
		if role > None {
			roles[s.ID] = role
		}
	}
	return roles
}

const rolesQuery = `
SELECT s.id, s.type = 'personal', s.owner_user_id, COALESCE(s.default_role, ''), s.status = 'archived',
  COALESCE((
    SELECT max(CASE m.role WHEN 'reader' THEN 1 WHEN 'author' THEN 2 WHEN 'curator' THEN 3 WHEN 'admin' THEN 4 ELSE 0 END)
      FROM space_members m
     WHERE m.space_id = s.id
       AND ((m.principal_type = 'user'  AND m.principal_id = $1)
         OR (m.principal_type = 'group' AND m.principal_id IN (SELECT group_id FROM group_members WHERE user_id = $1)))
  ), 0)
FROM spaces s`

type cacheEntry struct {
	roles   Roles
	expires time.Time
}

// Checker computes and caches effective roles.
type Checker struct {
	pool *pgxpool.Pool
	ttl  time.Duration
	now  func() time.Time

	mu    sync.Mutex
	cache map[uuid.UUID]cacheEntry
	epoch uint64 // bumped on invalidation so in-flight computations are not cached stale
}

func NewChecker(pool *pgxpool.Pool, ttl time.Duration) *Checker {
	if ttl <= 0 {
		ttl = DefaultCacheTTL
	}
	return &Checker{pool: pool, ttl: ttl, now: time.Now, cache: map[uuid.UUID]cacheEntry{}}
}

// InvalidateAll drops all cached roles; call after membership, group, status or
// space changes.
func (c *Checker) InvalidateAll() {
	c.mu.Lock()
	c.cache = map[uuid.UUID]cacheEntry{}
	c.epoch++
	c.mu.Unlock()
}

// EffectiveRoles returns the caller's role in every space they may see. Callers must
// treat the result as read-only.
func (c *Checker) EffectiveRoles(ctx context.Context, u users.User) (Roles, error) {
	c.mu.Lock()
	if e, ok := c.cache[u.ID]; ok && c.now().Before(e.expires) {
		c.mu.Unlock()
		return e.roles, nil
	}
	epoch := c.epoch
	c.mu.Unlock()

	rows, err := c.load(ctx, u.ID)
	if err != nil {
		return nil, err
	}
	if u.InstanceAdmin && !AdminsReadContent(ctx, c.pool) {
		u.InstanceAdmin = false // compute then gives the account the roles of a member, like anybody else
	}
	roles := compute(u, rows)

	c.mu.Lock()
	if c.epoch == epoch {
		c.cache[u.ID] = cacheEntry{roles, c.now().Add(c.ttl)}
	}
	c.mu.Unlock()
	return roles, nil
}

func (c *Checker) load(ctx context.Context, userID uuid.UUID) ([]spaceRow, error) {
	pgRows, err := c.pool.Query(ctx, rolesQuery, userID)
	if err != nil {
		return nil, err
	}
	defer pgRows.Close()
	var out []spaceRow
	for pgRows.Next() {
		var (
			r        spaceRow
			def      string
			memberAs int
		)
		if err := pgRows.Scan(&r.ID, &r.Personal, &r.Owner, &def, &r.Archived, &memberAs); err != nil {
			return nil, err
		}
		r.DefaultRole, _ = ParseRole(def)
		r.MemberRole = Role(memberAs)
		out = append(out, r)
	}
	return out, pgRows.Err()
}
