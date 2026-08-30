#!/bin/sh
set -eu

: "${PGBOUNCER_AUTH_PASSWORD:?required}"

# Only ONE entry ever lands here: the lookup role pgbouncer uses to run
# auth_query. Every other role -- including ones created long after this
# container started -- resolves through that query instead. No per-project
# userlist maintenance, which is what makes runtime tenant DBs work.
umask 077
printf '"pgbouncer_auth" "%s"\n' "$PGBOUNCER_AUTH_PASSWORD" > /etc/pgbouncer/userlist.txt
chown pgbouncer /etc/pgbouncer/userlist.txt

exec su-exec pgbouncer pgbouncer /etc/pgbouncer/pgbouncer.ini
