-- Hardened database accounts for Atrium (docs/deployment.md, "Separate database accounts").
--
-- Why: out of the box the server connects as the database's superuser, so a flaw in the server (or
-- anyone holding its connection string) could switch off the triggers that freeze released skills,
-- rewrite the audit log or change the schema. With this script the server runs with an account that
-- can read and write the data but cannot change the schema, disable triggers, or alter or delete an
-- audit entry, and a second account runs the migrations.
--
-- Run it once, as a superuser, on the existing database, then start the server with
--   database.url:           postgres://atrium_app:<app password>@host/dbname
--   database.migration_url: postgres://atrium_migrate:<migrate password>@host/dbname
--
--   psql -v ON_ERROR_STOP=1 -v app_password='...' -v migrate_password='...' -d <dbname> -f scripts/db-roles.sql
--
-- It is safe to run again (for example after a restore). Run it as the user that currently owns the
-- tables (the superuser the server used so far).

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'atrium_migrate') THEN
        CREATE ROLE atrium_migrate LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'atrium_app') THEN
        CREATE ROLE atrium_app LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE;
    END IF;
END $$;
ALTER ROLE atrium_migrate PASSWORD :'migrate_password';
ALTER ROLE atrium_app PASSWORD :'app_password';

-- pgvector is installed by a superuser, so the migrations do not need that right.
CREATE EXTENSION IF NOT EXISTS vector;

-- The migration account owns the schema and everything in it; the server owns nothing.
DO $$
DECLARE
    r record;
BEGIN
    EXECUTE format('GRANT CONNECT, CREATE ON DATABASE %I TO atrium_migrate', current_database());
    EXECUTE format('GRANT CONNECT ON DATABASE %I TO atrium_app', current_database());
    ALTER SCHEMA public OWNER TO atrium_migrate;
    FOR r IN SELECT format('ALTER TABLE %I.%I OWNER TO atrium_migrate', schemaname, tablename) AS cmd
             FROM pg_tables WHERE schemaname = 'public' LOOP
        EXECUTE r.cmd;
    END LOOP;
    FOR r IN SELECT format('ALTER SEQUENCE %I.%I OWNER TO atrium_migrate', sequence_schema, sequence_name) AS cmd
             FROM information_schema.sequences WHERE sequence_schema = 'public' LOOP
        BEGIN
            EXECUTE r.cmd;
        EXCEPTION WHEN OTHERS THEN
            NULL; -- a sequence owned by a column follows its table
        END;
    END LOOP;
    FOR r IN SELECT format('ALTER FUNCTION %s OWNER TO atrium_migrate', p.oid::regprocedure) AS cmd
             FROM pg_proc p JOIN pg_namespace n ON n.oid = p.pronamespace
             WHERE n.nspname = 'public'
               AND NOT EXISTS (SELECT 1 FROM pg_depend d WHERE d.objid = p.oid AND d.deptype = 'e') LOOP
        EXECUTE r.cmd;
    END LOOP;
END $$;

-- The server may use the data ...
GRANT USAGE ON SCHEMA public TO atrium_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public TO atrium_app;
GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA public TO atrium_app;
GRANT EXECUTE ON ALL FUNCTIONS IN SCHEMA public TO atrium_app;
-- ... but the audit log is append-only for it, and the migration bookkeeping is not its business.
REVOKE UPDATE, DELETE, TRUNCATE ON audit_log FROM atrium_app;
REVOKE ALL ON goose_db_version FROM atrium_app;

-- Tables, sequences and functions that later migrations create get the same rights; the audit log
-- rule above is repeated by the migration that creates a table for it (none yet).
ALTER DEFAULT PRIVILEGES FOR ROLE atrium_migrate IN SCHEMA public GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO atrium_app;
ALTER DEFAULT PRIVILEGES FOR ROLE atrium_migrate IN SCHEMA public GRANT USAGE, SELECT ON SEQUENCES TO atrium_app;
ALTER DEFAULT PRIVILEGES FOR ROLE atrium_migrate IN SCHEMA public GRANT EXECUTE ON FUNCTIONS TO atrium_app;
