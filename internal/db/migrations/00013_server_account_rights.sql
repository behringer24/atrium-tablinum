-- +goose Up
-- A database that was set up with scripts/db-roles.sql before its first start (the compose setup
-- does this) has the server account, atrium_app, from the beginning, and the default privileges
-- give it every table the migrations create. The audit log must stay append-only for that account,
-- and the migration bookkeeping is not its business. On a database without that account this does
-- nothing; the script applies the same rules to a database that already had tables.
-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'atrium_app') THEN
        REVOKE UPDATE, DELETE, TRUNCATE ON audit_log FROM atrium_app;
        REVOKE ALL ON goose_db_version FROM atrium_app;
    END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
SELECT 1;
