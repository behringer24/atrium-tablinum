-- +goose Up
-- A client that is new to a user has to be allowed by that user before it gets a login code
-- (the consent page). oauth_consent_requests holds the question while the user decides (single
-- use, short lived, the form carries only the random request value), oauth_consents what the user
-- allowed. Disconnecting a client, ending the sessions of a user and the other revocations delete
-- the consents with the tokens, so a client that was cut off has to ask again.
CREATE TABLE oauth_consent_requests (
    request_hash   text PRIMARY KEY,
    client_id      text        NOT NULL REFERENCES oauth_clients(client_id) ON DELETE CASCADE,
    user_id        uuid        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    redirect_uri   text        NOT NULL,
    code_challenge text        NOT NULL,
    client_state   text,
    resource       text        NOT NULL,
    expires_at     timestamptz NOT NULL
);

CREATE TABLE oauth_consents (
    user_id    uuid        NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    client_id  text        NOT NULL REFERENCES oauth_clients(client_id) ON DELETE CASCADE,
    granted_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, client_id)
);

-- +goose Down
DROP TABLE oauth_consents, oauth_consent_requests;
