-- +goose Up
-- Start of a session (a client's first login). Refresh rotation carries it over, so an
-- absolute maximum lifetime can be enforced even if the connection is in constant use.
ALTER TABLE oauth_tokens ADD COLUMN session_started_at timestamptz;
UPDATE oauth_tokens SET session_started_at = created_at;
ALTER TABLE oauth_tokens
    ALTER COLUMN session_started_at SET NOT NULL,
    ALTER COLUMN session_started_at SET DEFAULT now();

-- +goose Down
ALTER TABLE oauth_tokens DROP COLUMN session_started_at;
