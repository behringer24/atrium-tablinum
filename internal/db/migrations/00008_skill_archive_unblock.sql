-- +goose Up
-- Skills get the same life cycle tools as knowledge entries: archive (a skill as a whole is
-- hidden from everyone who cannot restore it), delete (a plain DELETE of the skill, the
-- versions and files go with it) and unblock. An unblocked version does not go back to
-- published but to approved, so it needs an explicit publish again.

ALTER TABLE skills
    ADD COLUMN archived_at timestamptz,
    ADD COLUMN archived_by uuid REFERENCES users(id) ON DELETE SET NULL;

-- +goose StatementBegin
CREATE OR REPLACE FUNCTION skill_versions_frozen() RETURNS trigger AS $$
BEGIN
    IF OLD.status <> 'draft' AND (
        NEW.skill_id IS DISTINCT FROM OLD.skill_id OR NEW.version IS DISTINCT FROM OLD.version OR
        NEW.description IS DISTINCT FROM OLD.description OR NEW.requested_tools IS DISTINCT FROM OLD.requested_tools OR
        NEW.digest IS DISTINCT FROM OLD.digest OR NEW.created_by IS DISTINCT FROM OLD.created_by OR
        NEW.created_at IS DISTINCT FROM OLD.created_at) THEN
        RAISE EXCEPTION 'skill version is not a draft, its content is frozen';
    END IF;
    IF OLD.status = 'blocked' AND NEW.status NOT IN ('blocked', 'approved') THEN
        RAISE EXCEPTION 'a blocked skill version can only be unblocked to approved';
    END IF;
    IF OLD.status <> 'draft' AND NEW.status = 'draft' THEN
        RAISE EXCEPTION 'a skill version cannot go back to draft';
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
CREATE OR REPLACE FUNCTION skill_versions_frozen() RETURNS trigger AS $$
BEGIN
    IF OLD.status <> 'draft' AND (
        NEW.skill_id IS DISTINCT FROM OLD.skill_id OR NEW.version IS DISTINCT FROM OLD.version OR
        NEW.description IS DISTINCT FROM OLD.description OR NEW.requested_tools IS DISTINCT FROM OLD.requested_tools OR
        NEW.digest IS DISTINCT FROM OLD.digest OR NEW.created_by IS DISTINCT FROM OLD.created_by OR
        NEW.created_at IS DISTINCT FROM OLD.created_at) THEN
        RAISE EXCEPTION 'skill version is not a draft, its content is frozen';
    END IF;
    IF OLD.status = 'blocked' AND NEW.status <> 'blocked' THEN
        RAISE EXCEPTION 'a blocked skill version stays blocked';
    END IF;
    IF OLD.status <> 'draft' AND NEW.status = 'draft' THEN
        RAISE EXCEPTION 'a skill version cannot go back to draft';
    END IF;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

ALTER TABLE skills DROP COLUMN archived_by, DROP COLUMN archived_at;
