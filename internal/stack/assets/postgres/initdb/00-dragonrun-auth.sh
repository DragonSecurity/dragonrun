#!/bin/bash
set -euo pipefail

: "${PGBOUNCER_AUTH_PASSWORD:?required}"

# The lookup role itself. No CREATEDB, no superuser -- it exists only to run
# pgbouncer_get_auth(), which is why that function is SECURITY DEFINER.
psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname postgres \
     -v authpw="$PGBOUNCER_AUTH_PASSWORD" <<'EOSQL'
SELECT format('CREATE ROLE pgbouncer_auth LOGIN PASSWORD %L', :'authpw')
 WHERE NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'pgbouncer_auth')
\gexec
EOSQL

# template1 first: everything created later inherits from it.
for db in template1 postgres; do
  echo "dragonrun: installing pgbouncer auth hook into $db"
  psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname "$db" \
       -f /opt/dragonrun/auth.sql
done

# The isolation guard goes ONLY into template1, so it propagates to project and
# tenant databases without ever guarding `postgres` -- the shared admin database
# that ADMIN_DATABASE_URL points at for tenant create/drop.
echo "dragonrun: installing cross-project login guard into template1"
psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname template1 \
     -f /opt/dragonrun/guard.sql

# Belt and braces on template1 itself. This does NOT propagate to databases
# created from it -- CREATE DATABASE leaves datacl NULL, which means the
# built-in default of "PUBLIC may CONNECT". Cross-project isolation for tenant
# databases is enforced by the login event trigger in auth.sql, which IS
# inherited. This only stops project roles connecting to template1 directly.
echo "dragonrun: closing template1 to PUBLIC"
psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname postgres <<'EOSQL'
REVOKE CONNECT ON DATABASE template1 FROM PUBLIC;
GRANT  CONNECT ON DATABASE template1 TO pgbouncer_auth;
EOSQL
