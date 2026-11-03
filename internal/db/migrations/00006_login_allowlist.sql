-- +goose Up
-- Zusätzliche Freigaben für den Login, vom Instanz-Admin zur Laufzeit gepflegt (ohne Neustart).
-- Sie erweitern nur, was die Konfiguration erlaubt. provider = '' gilt für alle Provider des
-- passenden Typs (Domains und Adressen: google/oidc, Tenants: entra).
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
