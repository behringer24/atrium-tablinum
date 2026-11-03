-- +goose Up
-- Additional login approvals, maintained at runtime by the instance admin (no restart).
-- They only extend what the configuration allows. provider = '' applies to all providers of
-- the matching type (domains and addresses: google/oidc, tenants: entra).
CREATE TABLE login_allowlist (
    kind       text        NOT NULL CHECK (kind IN ('domain', 'email', 'tenant')),
    value      text        NOT NULL,
    provider   text        NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    created_by uuid        REFERENCES users(id) ON DELETE SET NULL,
    PRIMARY KEY (kind, value, provider)
);

-- +goose Down
DROP TABLE login_allowlist;
