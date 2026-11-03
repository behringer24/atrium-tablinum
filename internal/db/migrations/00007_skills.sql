-- +goose Up
-- Skill registry (docs/skills.md). A skill is a versioned package of text files, kept apart
-- from the knowledge entries in `documents`: knowledge is untrusted data, a skill is an
-- instruction an assistant follows when the user asks for it, so the two never share a table
-- or a read path.

CREATE TABLE skills (
    id         uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    space_id   uuid        NOT NULL REFERENCES spaces(id) ON DELETE CASCADE,
    name       text        NOT NULL CHECK (name ~ '^[a-z0-9]+(-[a-z0-9]+)*$' AND length(name) <= 64),
    created_by uuid        REFERENCES users(id) ON DELETE SET NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (space_id, name)
);

-- One row per release candidate. draft -> approved | rejected, approved -> published,
-- approved | published -> blocked. The content of a version is frozen once it leaves draft
-- (trigger below), so a published release can never change under its number.
CREATE TABLE skill_versions (
    id              uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    skill_id        uuid        NOT NULL REFERENCES skills(id) ON DELETE CASCADE,
    version         int         NOT NULL CHECK (version > 0),
    status          text        NOT NULL DEFAULT 'draft'
                    CHECK (status IN ('draft', 'approved', 'rejected', 'published', 'blocked')),
    description     text        NOT NULL,
    requested_tools text        NOT NULL DEFAULT '',
    risk_class      text        CHECK (risk_class IN ('A', 'B', 'C')),
    digest          text        NOT NULL,
    created_by      uuid        REFERENCES users(id) ON DELETE SET NULL,
    created_at      timestamptz NOT NULL DEFAULT now(),
    reviewed_by     uuid        REFERENCES users(id) ON DELETE SET NULL,
    reviewed_at     timestamptz,
    review_note     text,
    published_by    uuid        REFERENCES users(id) ON DELETE SET NULL,
    published_at    timestamptz,
    blocked_by      uuid        REFERENCES users(id) ON DELETE SET NULL,
    blocked_at      timestamptz,
    block_reason    text,
    UNIQUE (skill_id, version)
);

CREATE TABLE skill_resources (
    version_id uuid NOT NULL REFERENCES skill_versions(id) ON DELETE CASCADE,
    path       text NOT NULL,
    content    text NOT NULL,
    size       int  NOT NULL,
    sha256     text NOT NULL,
    PRIMARY KEY (version_id, path)
);

-- +goose StatementBegin
CREATE FUNCTION skill_resources_frozen() RETURNS trigger AS $$
DECLARE
    st text;
BEGIN
    SELECT status INTO st FROM skill_versions WHERE id = COALESCE(NEW.version_id, OLD.version_id);
    -- No parent row (cascade delete of the version) or a draft: allowed.
    IF st IS NOT NULL AND st <> 'draft' THEN
        RAISE EXCEPTION 'skill version is not a draft, its resources are frozen';
    END IF;
    RETURN COALESCE(NEW, OLD);
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE TRIGGER skill_resources_frozen
    BEFORE INSERT OR UPDATE OR DELETE ON skill_resources
    FOR EACH ROW EXECUTE FUNCTION skill_resources_frozen();

-- +goose StatementBegin
CREATE FUNCTION skill_versions_frozen() RETURNS trigger AS $$
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

CREATE TRIGGER skill_versions_frozen
    BEFORE UPDATE ON skill_versions
    FOR EACH ROW EXECUTE FUNCTION skill_versions_frozen();

CREATE INDEX skill_versions_status_idx ON skill_versions (skill_id, status);

-- +goose Down
DROP TABLE skill_resources;
DROP TABLE skill_versions;
DROP FUNCTION skill_versions_frozen();
DROP FUNCTION skill_resources_frozen();
DROP TABLE skills;
