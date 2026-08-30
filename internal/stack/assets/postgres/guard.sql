-- Cross-project isolation. Applied to template1 ONLY, so it is inherited by
-- every project control database and every tenant database created from it.
--
-- Why an event trigger and not a REVOKE: a database's ACL is NOT copied from
-- its template. CREATE DATABASE leaves datacl NULL, which means the built-in
-- default -- PUBLIC may CONNECT. Revoking CONNECT on template1 therefore does
-- nothing for the tenant databases an application creates at runtime, and
-- every one of them would be readable by every other project's role. A login
-- event trigger (PostgreSQL 17+) IS a per-database object and IS copied with
-- the template, so it closes the gap at the moment of creation rather than
-- whenever someone next remembers to run `dragonrun db harden`.
--
-- Deliberately NOT installed in the `postgres` database: that is the shared
-- admin entry point every project reaches through ADMIN_DATABASE_URL to create
-- and drop its tenants. Guarding it would break multi-tenant provisioning
-- outright. Nothing project-specific lives there.
--
-- session_user, not current_user: this function is SECURITY DEFINER, so
-- current_user would be the owner (a superuser) and the guard would pass for
-- everyone.
--
-- Superusers are exempted FIRST and deliberately: it keeps the cluster
-- recoverable if this function is ever broken. `dragonrun psql` connects as
-- the project role, so it is still subject to the guard.

CREATE OR REPLACE FUNCTION public.dragonrun_login_guard()
RETURNS event_trigger
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog
AS $fn$
BEGIN
  IF session_user = 'pgbouncer_auth' THEN RETURN; END IF;
  IF (SELECT rolsuper FROM pg_roles WHERE rolname = session_user) THEN RETURN; END IF;
  IF session_user = (SELECT pg_get_userbyid(datdba) FROM pg_database
                      WHERE datname = current_database()) THEN RETURN; END IF;
  RAISE EXCEPTION 'dragonrun: role % may not connect to database %',
                  session_user, current_database()
    USING HINT = 'each project may only reach its own control and tenant databases';
END
$fn$;

REVOKE ALL ON FUNCTION public.dragonrun_login_guard() FROM PUBLIC;

DROP EVENT TRIGGER IF EXISTS dragonrun_login_guard;
CREATE EVENT TRIGGER dragonrun_login_guard
  ON login EXECUTE FUNCTION public.dragonrun_login_guard();
