-- +goose Up
-- Tags of a skill, like the tags of a knowledge entry. They belong to the skill, not to a
-- version: a version is frozen once it leaves draft, and fixing a keyword must not need a
-- new version and a new review. They are no part of the digest and no instruction.

ALTER TABLE skills ADD COLUMN tags text[] NOT NULL DEFAULT '{}';
CREATE INDEX skills_tags_idx ON skills USING gin (tags);

-- +goose Down
DROP INDEX skills_tags_idx;
ALTER TABLE skills DROP COLUMN tags;
