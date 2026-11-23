-- +goose Up
-- Accounts that are not a person: a tool that reads from or writes to Atrium on its own. They are rows
-- of `users` with kind = 'service', so roles, groups, quotas, the audit log and locking work as for a
-- person. The rules that set them apart live in the code (internal/users, internal/authz): they never
-- are an instance admin, the default role of a space does not apply to them, and their role is capped.
-- Nothing in this edition creates one, so the column stays at 'person' and the tables stay empty.
--
-- A service account names the person who answers for it (responsible_id): who is asked when it misbehaves
-- and who it is handed to when that person leaves. Accounts of people have none.
ALTER TABLE users
    ADD COLUMN kind text NOT NULL DEFAULT 'person' CHECK (kind IN ('person', 'service')),
    ADD COLUMN responsible_id uuid REFERENCES users(id),
    ADD CONSTRAINT users_service_not_admin CHECK (kind = 'person' OR instance_admin = false),
    ADD CONSTRAINT users_responsible_only_service CHECK (kind = 'service' OR responsible_id IS NULL),
    ADD CONSTRAINT users_not_own_responsible CHECK (responsible_id IS NULL OR responsible_id <> id);

CREATE INDEX users_responsible_idx ON users (responsible_id) WHERE responsible_id IS NOT NULL;

-- Tokens of a service account. Only the SHA-256 of the token is stored. A token always expires and can
-- be revoked; two live tokens of one account may overlap for a rotation.
CREATE TABLE service_tokens (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id      uuid        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name         text        NOT NULL,
    token_hash   bytea       NOT NULL UNIQUE CHECK (octet_length(token_hash) = 32),
    created_at   timestamptz NOT NULL DEFAULT now(),
    expires_at   timestamptz NOT NULL,
    last_used_at timestamptz,
    revoked_at   timestamptz,
    UNIQUE (user_id, name),
    CHECK (expires_at > created_at)
);

-- A stable key for an entry that a tool keeps up to date from an outside source (for example a file in a
-- Git repository), and the account that owns such an entry. An entry with a key is updated in place
-- instead of being stored again.
ALTER TABLE documents
    ADD COLUMN sync_key   text,
    ADD COLUMN managed_by uuid REFERENCES users(id),
    ADD CONSTRAINT documents_sync_key_knowledge CHECK (sync_key IS NULL OR kind = 'knowledge'),
    ADD CONSTRAINT documents_managed_has_key CHECK (managed_by IS NULL OR sync_key IS NOT NULL);

CREATE UNIQUE INDEX documents_sync_key_idx ON documents (space_id, sync_key) WHERE sync_key IS NOT NULL;

-- +goose Down
DROP INDEX documents_sync_key_idx;
ALTER TABLE documents
    DROP CONSTRAINT documents_managed_has_key,
    DROP CONSTRAINT documents_sync_key_knowledge,
    DROP COLUMN managed_by,
    DROP COLUMN sync_key;
DROP TABLE service_tokens;
DROP INDEX users_responsible_idx;
ALTER TABLE users
    DROP CONSTRAINT users_not_own_responsible,
    DROP CONSTRAINT users_responsible_only_service,
    DROP CONSTRAINT users_service_not_admin,
    DROP COLUMN responsible_id,
    DROP COLUMN kind;
