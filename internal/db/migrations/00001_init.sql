-- +goose Up
-- Schema from concept section 9. The `chunks` table depends on the configured
-- embedding dimension and is created in migration 00002 (Go).

CREATE EXTENSION IF NOT EXISTS vector;
CREATE EXTENSION IF NOT EXISTS pgcrypto;
CREATE EXTENSION IF NOT EXISTS citext;

CREATE TABLE meta (
    key   text PRIMARY KEY,
    value text NOT NULL
);

-- Users and groups -----------------------------------------------------------

CREATE TABLE users (
    id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    email            citext      NOT NULL UNIQUE,
    display_name     text        NOT NULL DEFAULT '',
    idp              text        NOT NULL,
    idp_subject      text        NOT NULL,
    status           text        NOT NULL DEFAULT 'active'
                     CHECK (status IN ('active', 'disabled')),
    instance_admin   boolean     NOT NULL DEFAULT false,
    default_space_id uuid,
    quota_documents  int,
    quota_bytes      bigint,
    disabled_at      timestamptz,
    created_at       timestamptz NOT NULL DEFAULT now(),
    last_login_at    timestamptz,
    UNIQUE (idp, idp_subject)
);

CREATE TABLE groups (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name        text NOT NULL,
    source      text NOT NULL CHECK (source IN ('local', 'idp')),
    idp         text,
    external_id text,
    created_at  timestamptz NOT NULL DEFAULT now(),
    UNIQUE (source, idp, external_id),
    UNIQUE (name)
);

CREATE TABLE group_members (
    group_id uuid NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    user_id  uuid NOT NULL REFERENCES users(id)  ON DELETE CASCADE,
    PRIMARY KEY (group_id, user_id)
);

-- Spaces -------------------------------------------------------------------

CREATE TABLE spaces (
    id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    slug             text        NOT NULL UNIQUE,
    name             text        NOT NULL,
    description      text        NOT NULL DEFAULT '',
    type             text        NOT NULL CHECK (type IN ('personal', 'team')),
    owner_user_id    uuid REFERENCES users(id) ON DELETE CASCADE,
    require_approval boolean     NOT NULL DEFAULT false,
    default_role     text CHECK (default_role IN ('reader', 'author')),
    status           text        NOT NULL DEFAULT 'active'
                     CHECK (status IN ('active', 'archived')),
    created_at       timestamptz NOT NULL DEFAULT now(),
    CHECK ((type = 'personal') = (owner_user_id IS NOT NULL)),
    CHECK (type = 'team' OR (require_approval = false AND default_role IS NULL))
);

CREATE UNIQUE INDEX one_personal_space_per_user ON spaces (owner_user_id) WHERE type = 'personal';

ALTER TABLE users ADD CONSTRAINT users_default_space_fk
    FOREIGN KEY (default_space_id) REFERENCES spaces(id) ON DELETE SET NULL;

CREATE TABLE space_members (
    space_id       uuid NOT NULL REFERENCES spaces(id) ON DELETE CASCADE,
    principal_type text NOT NULL CHECK (principal_type IN ('user', 'group')),
    principal_id   uuid NOT NULL,
    role           text NOT NULL CHECK (role IN ('reader', 'author', 'curator', 'admin')),
    created_at     timestamptz NOT NULL DEFAULT now(),
    created_by     uuid REFERENCES users(id),
    PRIMARY KEY (space_id, principal_type, principal_id)
);

-- principal_id refers to users or groups depending on principal_type (no FK possible).
-- When users/groups are deleted, memberships are removed by trigger.
-- +goose StatementBegin
CREATE FUNCTION remove_principal_memberships() RETURNS trigger AS $$
BEGIN
    DELETE FROM space_members
     WHERE principal_type = TG_ARGV[0] AND principal_id = OLD.id;
    RETURN OLD;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd

CREATE TRIGGER users_remove_memberships  AFTER DELETE ON users
    FOR EACH ROW EXECUTE FUNCTION remove_principal_memberships('user');
CREATE TRIGGER groups_remove_memberships AFTER DELETE ON groups
    FOR EACH ROW EXECUTE FUNCTION remove_principal_memberships('group');

-- Documents ------------------------------------------------------------------

CREATE TABLE documents (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    space_id      uuid        NOT NULL REFERENCES spaces(id) ON DELETE CASCADE,
    kind          text        NOT NULL DEFAULT 'knowledge'
                  CHECK (kind IN ('knowledge', 'handoff')),
    title         text        NOT NULL,
    content       text        NOT NULL,
    tags          text[]      NOT NULL DEFAULT '{}',
    status        text        NOT NULL DEFAULT 'approved'
                  CHECK (status IN ('draft', 'approved', 'rejected', 'archived')),
    review_note   text,
    author_id     uuid        NOT NULL REFERENCES users(id),
    source        text        NOT NULL,
    version       int         NOT NULL DEFAULT 1,
    review_due_at timestamptz,
    expires_at    timestamptz,
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now(),
    updated_by    uuid        NOT NULL REFERENCES users(id),
    CHECK (kind = 'knowledge' OR expires_at IS NOT NULL)
);

CREATE INDEX documents_space_status_idx ON documents (space_id, status);
CREATE INDEX documents_author_idx       ON documents (author_id);
CREATE INDEX documents_tags_idx         ON documents USING GIN (tags);
CREATE INDEX documents_expires_idx      ON documents (expires_at) WHERE expires_at IS NOT NULL;

CREATE TABLE document_versions (
    document_id uuid        NOT NULL REFERENCES documents(id) ON DELETE CASCADE,
    version     int         NOT NULL,
    space_id    uuid        NOT NULL,
    title       text        NOT NULL,
    content     text        NOT NULL,
    tags        text[]      NOT NULL,
    status      text        NOT NULL,
    changed_by  uuid        NOT NULL REFERENCES users(id),
    changed_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (document_id, version)
);

-- OAuth ---------------------------------------------------------------------

CREATE TABLE oauth_clients (
    client_id     text PRIMARY KEY,
    client_name   text,
    redirect_uris text[]      NOT NULL,
    created_at    timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE oauth_login_states (
    state_hash     text PRIMARY KEY,
    client_id      text        NOT NULL REFERENCES oauth_clients(client_id) ON DELETE CASCADE,
    redirect_uri   text        NOT NULL,
    code_challenge text        NOT NULL,
    client_state   text,
    idp            text,
    idp_nonce      text,
    expires_at     timestamptz NOT NULL
);

CREATE TABLE oauth_codes (
    code_hash      text PRIMARY KEY,
    client_id      text        NOT NULL REFERENCES oauth_clients(client_id) ON DELETE CASCADE,
    user_id        uuid        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    redirect_uri   text        NOT NULL,
    code_challenge text        NOT NULL,
    expires_at     timestamptz NOT NULL
);

CREATE TABLE oauth_tokens (
    token_hash   text PRIMARY KEY,
    kind         text        NOT NULL CHECK (kind IN ('access', 'refresh')),
    client_id    text        NOT NULL REFERENCES oauth_clients(client_id) ON DELETE CASCADE,
    user_id      uuid        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    expires_at   timestamptz NOT NULL,
    revoked_at   timestamptz,
    created_at   timestamptz NOT NULL DEFAULT now(),
    last_used_at timestamptz
);
CREATE INDEX oauth_tokens_user_idx ON oauth_tokens (user_id);

-- Operations --------------------------------------------------------------------

CREATE TABLE audit_log (
    id          bigserial PRIMARY KEY,
    ts          timestamptz NOT NULL DEFAULT now(),
    user_id     uuid,
    client      text,
    action      text        NOT NULL,
    space_id    uuid,
    document_id uuid,
    details     jsonb
);
CREATE INDEX audit_log_ts_idx      ON audit_log (ts);
CREATE INDEX audit_log_user_ts_idx ON audit_log (user_id, ts);

CREATE TABLE jobs (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    kind        text        NOT NULL,
    status      text        NOT NULL DEFAULT 'queued'
                CHECK (status IN ('queued', 'running', 'done', 'failed')),
    params      jsonb,
    progress    int         NOT NULL DEFAULT 0,
    total       int,
    error       text,
    created_by  uuid,
    created_at  timestamptz NOT NULL DEFAULT now(),
    started_at  timestamptz,
    finished_at timestamptz
);

-- +goose Down
DROP TABLE jobs, audit_log, oauth_tokens, oauth_codes, oauth_login_states, oauth_clients,
    document_versions, documents, space_members, spaces, group_members, groups, users, meta CASCADE;
DROP FUNCTION remove_principal_memberships();
