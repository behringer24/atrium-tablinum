-- +goose Up
-- Storage limits that an instance admin changes at runtime, without a restart. A row overrides
-- the defaults from config.yaml for its scope; a NULL column inherits from the next level:
--   user  -> personal_default -> config   (the personal space of the user)
--   space -> team_default     -> config   (a team space)
-- 0 means unlimited. The per-user columns of the users table move here.

CREATE TABLE quotas (
    scope          text        NOT NULL CHECK (scope IN ('personal_default', 'team_default', 'space', 'user')),
    scope_id       uuid        NOT NULL DEFAULT '00000000-0000-0000-0000-000000000000',
    documents      int         CHECK (documents >= 0),
    bytes          bigint      CHECK (bytes >= 0),
    skills         int         CHECK (skills >= 0),
    skill_bytes    bigint      CHECK (skill_bytes >= 0),
    skill_versions int         CHECK (skill_versions >= 0),
    updated_by     uuid        REFERENCES users(id) ON DELETE SET NULL,
    updated_at     timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (scope, scope_id)
);

INSERT INTO quotas (scope, scope_id, documents, bytes)
SELECT 'user', id, quota_documents, quota_bytes FROM users
WHERE quota_documents IS NOT NULL OR quota_bytes IS NOT NULL;

ALTER TABLE users DROP COLUMN quota_documents, DROP COLUMN quota_bytes;

-- +goose Down
ALTER TABLE users ADD COLUMN quota_documents int, ADD COLUMN quota_bytes bigint;
UPDATE users u SET quota_documents = q.documents, quota_bytes = q.bytes FROM quotas q WHERE q.scope = 'user' AND q.scope_id = u.id;
DROP TABLE quotas;
