-- +goose Up
-- Tokens and codes are bound to the target resource (RFC 8707); the login state
-- additionally stores the PKCE verifier for the identity provider.
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
