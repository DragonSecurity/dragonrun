-- Applied to template1 (and postgres) at cluster init. Everything here is
-- therefore inherited by every database created afterwards -- crucially
-- including the tenant databases an application creates at RUNTIME, which
-- dragonrun never sees.

-- 1. Credential lookup for pgbouncer -------------------------------------
-- Lets pgbouncer resolve a role's SCRAM verifier at connect time instead of us
-- maintaining a userlist.txt entry per project. Without it, every runtime
-- CREATE DATABASE would need a matching pgbouncer config edit.

CREATE OR REPLACE FUNCTION public.pgbouncer_get_auth(uname TEXT)
RETURNS TABLE(username TEXT, password TEXT)
LANGUAGE sql
SECURITY DEFINER
SET search_path = pg_catalog
AS 'SELECT rolname::TEXT, rolpassword::TEXT
      FROM pg_authid
     WHERE rolname = uname
       AND rolcanlogin';

REVOKE ALL ON FUNCTION public.pgbouncer_get_auth(TEXT) FROM PUBLIC;
GRANT EXECUTE ON FUNCTION public.pgbouncer_get_auth(TEXT) TO pgbouncer_auth;
