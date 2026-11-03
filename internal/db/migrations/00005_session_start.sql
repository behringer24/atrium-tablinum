-- +goose Up
-- Beginn einer Sitzung (erste Anmeldung eines Clients). Refresh-Rotation übernimmt ihn, so
-- lässt sich eine absolute Höchstdauer durchsetzen, auch wenn die Verbindung ständig genutzt wird.
ALTER TABLE oauth_tokens ADD COLUMN session_started_at timestamptz;
UPDATE oauth_tokens SET session_started_at = created_at;
ALTER TABLE oauth_tokens
    ALTER COLUMN session_started_at SET NOT NULL,
    ALTER COLUMN session_started_at SET DEFAULT now();

-- +goose Down
ALTER TABLE oauth_tokens DROP COLUMN session_started_at;
