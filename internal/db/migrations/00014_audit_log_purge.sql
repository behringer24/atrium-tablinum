-- +goose Up
-- Retention for the audit log. The server account may only append to audit_log (see
-- scripts/db-roles.sql), so deleting old entries goes through a function that runs with the rights
-- of the schema owner and refuses to touch anything younger than 30 days: a server that is taken
-- over can still not erase the recent trail. Config: audit.retention_days.
-- +goose StatementBegin
CREATE FUNCTION audit_log_purge(cutoff timestamptz) RETURNS bigint
LANGUAGE plpgsql SECURITY DEFINER SET search_path = public, pg_temp AS $$
DECLARE
    n bigint;
BEGIN
    IF cutoff > now() - interval '30 days' THEN
        RAISE EXCEPTION 'audit entries younger than 30 days are not purged';
    END IF;
    DELETE FROM audit_log WHERE ts < cutoff;
    GET DIAGNOSTICS n = ROW_COUNT;
    RETURN n;
END $$;
-- +goose StatementEnd

REVOKE ALL ON FUNCTION audit_log_purge(timestamptz) FROM PUBLIC;
-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'atrium_app') THEN
        GRANT EXECUTE ON FUNCTION audit_log_purge(timestamptz) TO atrium_app;
    END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
DROP FUNCTION audit_log_purge(timestamptz);
