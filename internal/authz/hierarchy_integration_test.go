package authz_test

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/behringer24/atrium-tablinum/internal/authz"
	"github.com/behringer24/atrium-tablinum/internal/spaces"
)

// setParent sets the parent directly: nothing in the public tree does that yet.
func (e env) setParent(t *testing.T, child, parent uuid.UUID) {
	t.Helper()
	if _, err := e.pool.Exec(e.ctx, `UPDATE spaces SET parent_id = $2 WHERE id = $1`, child, parent); err != nil {
		t.Fatal(err)
	}
	e.checker.InvalidateAll()
}

func TestHierarchyRolesFromDatabase(t *testing.T) {
	e := setup(t, time.Minute)
	anna := e.user(t, "anna@x.de")
	root := e.team(t, "marketing", authz.None)
	mid := e.team(t, "kampagnen", authz.None)
	leaf := e.team(t, "messe", authz.None)
	e.setParent(t, mid.ID, root.ID)
	e.setParent(t, leaf.ID, mid.ID)

	g, err := e.users.CreateGroup(e.ctx, "Marketing")
	if err != nil {
		t.Fatal(err)
	}
	if err := e.users.AddGroupMember(e.ctx, g.ID, anna.ID); err != nil {
		t.Fatal(err)
	}
	if err := e.spaces.SetMember(e.ctx, root.ID, spaces.PrincipalGroup, g.ID, authz.Author, anna.ID); err != nil {
		t.Fatal(err)
	}

	r := e.roles(t, anna)
	for _, sp := range []spaces.Space{root, mid, leaf} {
		if r.Role(sp.ID) != authz.Author {
			t.Errorf("%s: role %v, expected author through the group of the root", sp.Slug, r.Role(sp.ID))
		}
	}

	// An explicit entry in the middle overrides downwards, and the leaf follows it.
	if err := e.spaces.SetMember(e.ctx, mid.ID, spaces.PrincipalUser, anna.ID, authz.Reader, anna.ID); err != nil {
		t.Fatal(err)
	}
	r = e.roles(t, anna)
	if r.Role(root.ID) != authz.Author || r.Role(mid.ID) != authz.Reader || r.Role(leaf.ID) != authz.Reader {
		t.Errorf("roles after the override: %v %v %v", r.Role(root.ID), r.Role(mid.ID), r.Role(leaf.ID))
	}

	// Stopping the inheritance in the leaf closes it for her.
	if _, err := e.pool.Exec(e.ctx, `UPDATE spaces SET inherit_members = false WHERE id = $1`, leaf.ID); err != nil {
		t.Fatal(err)
	}
	e.checker.InvalidateAll()
	if e.roles(t, anna).Can(leaf.ID, authz.Reader) {
		t.Error("the leaf is still open after inherit_members = false")
	}
}

// A user with an entry only in the leaf sees neither of the spaces above it.
func TestHierarchyEntryOnlyInLeaf(t *testing.T) {
	e := setup(t, time.Minute)
	anna := e.user(t, "anna@x.de")
	root := e.team(t, "marketing", authz.None)
	leaf := e.team(t, "messe", authz.None)
	e.setParent(t, leaf.ID, root.ID)
	if err := e.spaces.SetMember(e.ctx, leaf.ID, spaces.PrincipalUser, anna.ID, authz.Author, anna.ID); err != nil {
		t.Fatal(err)
	}
	r := e.roles(t, anna)
	if r.Can(root.ID, authz.Reader) {
		t.Error("the parent is visible")
	}
	if r.Role(leaf.ID) != authz.Author {
		t.Errorf("leaf role = %v", r.Role(leaf.ID))
	}
}

func TestHierarchyConstraintsAndDelete(t *testing.T) {
	e := setup(t, time.Minute)
	anna := e.user(t, "anna@x.de")
	root := e.team(t, "marketing", authz.None)
	leaf := e.team(t, "messe", authz.None)

	if _, err := e.pool.Exec(e.ctx, `UPDATE spaces SET parent_id = id WHERE id = $1`, root.ID); err == nil {
		t.Error("a space as its own parent was accepted")
	}
	var personal uuid.UUID
	if err := e.pool.QueryRow(e.ctx, `SELECT id FROM spaces WHERE owner_user_id = $1`, anna.ID).Scan(&personal); err != nil {
		t.Fatal(err)
	}
	if _, err := e.pool.Exec(e.ctx, `UPDATE spaces SET parent_id = $2 WHERE id = $1`, personal, root.ID); err == nil {
		t.Error("a personal space with a parent was accepted")
	}

	e.setParent(t, leaf.ID, root.ID)
	if err := e.spaces.Delete(e.ctx, root.ID); !errors.Is(err, spaces.ErrNotEmpty) {
		t.Errorf("deleting a space with a sub-space: %v, expected ErrNotEmpty", err)
	}
	if err := e.spaces.Delete(e.ctx, leaf.ID); err != nil {
		t.Fatal(err)
	}
	if err := e.spaces.Delete(e.ctx, root.ID); err != nil {
		t.Errorf("deleting the now empty parent: %v", err)
	}
}
