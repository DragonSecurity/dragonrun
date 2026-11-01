FROM postgres:18@sha256:3a4264520e121176619f54ff867d12774bc81e95e7b84db58d63bccdb6144b81

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
