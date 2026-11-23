-- +goose Up
-- A team space may sit below another team space. The roles of a user are then looked up from the space
-- upwards (see internal/authz): the nearest space with an entry for the user decides, and
-- inherit_members = false stops the lookup at that space. Without a parent every space is a root and
-- nothing changes. Depth and cycles are checked where a parent is set; the constraints here only
-- keep the data coherent. A space with sub-spaces cannot be deleted (RESTRICT), so a parent is never
-- removed from under its children.
ALTER TABLE spaces
    ADD COLUMN parent_id uuid REFERENCES spaces(id) ON DELETE RESTRICT,
    ADD COLUMN inherit_members boolean NOT NULL DEFAULT true,
    ADD CONSTRAINT spaces_parent_not_self CHECK (parent_id IS NULL OR parent_id <> id),
    ADD CONSTRAINT spaces_parent_only_team CHECK (parent_id IS NULL OR type = 'team');

CREATE INDEX spaces_parent_idx ON spaces (parent_id) WHERE parent_id IS NOT NULL;

-- +goose Down
DROP INDEX spaces_parent_idx;
ALTER TABLE spaces
    DROP CONSTRAINT spaces_parent_only_team,
    DROP CONSTRAINT spaces_parent_not_self,
    DROP COLUMN inherit_members,
    DROP COLUMN parent_id;
