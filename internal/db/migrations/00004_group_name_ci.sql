-- +goose Up
-- Groups are resolved case-insensitively, so the name must be unique in the same way.
CREATE UNIQUE INDEX groups_name_ci_idx ON groups (lower(name));

-- +goose Down
DROP INDEX groups_name_ci_idx;
