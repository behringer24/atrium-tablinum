-- +goose Up
-- Gruppen werden ohne Beachtung der Groß-/Kleinschreibung aufgelöst, also muss der Name so eindeutig sein.
CREATE UNIQUE INDEX groups_name_ci_idx ON groups (lower(name));

-- +goose Down
DROP INDEX groups_name_ci_idx;
