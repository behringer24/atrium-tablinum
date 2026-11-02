-- +goose Up
-- Tokens und Codes sind an die Ziel-Ressource gebunden (RFC 8707); der Login-State
-- speichert zusätzlich den PKCE-Verifier für den Identity Provider.
ALTER TABLE oauth_login_states
    ADD COLUMN resource     text NOT NULL DEFAULT '',
    ADD COLUMN idp_verifier text;
ALTER TABLE oauth_codes  ADD COLUMN resource text NOT NULL DEFAULT '';
ALTER TABLE oauth_tokens ADD COLUMN resource text NOT NULL DEFAULT '';
CREATE INDEX oauth_tokens_client_user_idx ON oauth_tokens (user_id, client_id);

-- +goose Down
DROP INDEX oauth_tokens_client_user_idx;
ALTER TABLE oauth_tokens DROP COLUMN resource;
ALTER TABLE oauth_codes DROP COLUMN resource;
ALTER TABLE oauth_login_states DROP COLUMN idp_verifier, DROP COLUMN resource;
