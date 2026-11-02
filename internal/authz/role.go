// Package authz computes effective roles per space and answers permission questions.
package authz

import (
	"sort"

	"github.com/google/uuid"
)

// Role is ordered: None < Reader < Author < Curator < Admin.
type Role int

const (
	None Role = iota
	Reader
	Author
	Curator
	Admin
)

var roleNames = [...]string{"", "reader", "author", "curator", "admin"}

func (r Role) String() string {
	if r < 0 || int(r) >= len(roleNames) {
		return ""
	}
	return roleNames[r]
}

// ParseRole returns None, false for anything that is not a real role name.
func ParseRole(s string) (Role, bool) {
	for i := Reader; i <= Admin; i++ {
		if roleNames[i] == s {
			return i, true
		}
	}
	return None, false
}

// Roles maps a space to the caller's effective role. Spaces without an entry are
// invisible to the caller.
type Roles map[uuid.UUID]Role

func (r Roles) Role(space uuid.UUID) Role { return r[space] }

// Can reports whether the caller holds at least min in space. Unknown spaces yield
// false, exactly like spaces the caller may not see.
func (r Roles) Can(space uuid.UUID, min Role) bool { return min > None && r[space] >= min }

// Spaces lists all spaces where the caller holds at least min, in stable order.
func (r Roles) Spaces(min Role) []uuid.UUID {
	var out []uuid.UUID
	for id, role := range r {
		if role >= min {
			out = append(out, id)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].String() < out[j].String() })
	return out
}

// Any reports whether the caller holds at least min in some space.
func (r Roles) Any(min Role) bool {
	for _, role := range r {
		if role >= min {
			return true
		}
	}
	return false
}
