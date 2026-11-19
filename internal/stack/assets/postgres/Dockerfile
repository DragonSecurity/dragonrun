FROM postgres:18@sha256:e754b839f447639b7004895f4d8a4bfcb4e9dbe3a1bc035f27f646562a16fd61

# Same SSL-bootstrapping entrypoint the per-project stacks already use, so the
# shared cluster behaves identically to what these repos were talking to.
COPY entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh

# Kept OUT of /docker-entrypoint-initdb.d so it is never auto-run against a
# single database — the init script below applies it to template1 by hand.
COPY auth.sql guard.sql /opt/dragonrun/
COPY initdb/ /docker-entrypoint-initdb.d/

ENTRYPOINT ["/entrypoint.sh"]
CMD ["postgres", \
     "-c", "ssl=on", \
     "-c", "ssl_cert_file=/var/lib/postgresql/server.crt", \
     "-c", "ssl_key_file=/var/lib/postgresql/server.key", \
     "-c", "max_connections=200"]
