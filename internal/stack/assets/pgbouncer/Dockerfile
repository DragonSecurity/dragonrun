FROM alpine:3.22@sha256:14358309a308569c32bdc37e2e0e9694be33a9d99e68afb0f5ff33cc1f695dce

# The apk package creates the `pgbouncer` user itself; adduser is guarded so
# the build does not break if a future package version stops doing that.
RUN apk add --no-cache pgbouncer su-exec \
 && (id pgbouncer >/dev/null 2>&1 || adduser -S -D -H pgbouncer) \
 && mkdir -p /etc/pgbouncer /var/log/pgbouncer /var/run/pgbouncer \
 && chown -R pgbouncer /etc/pgbouncer /var/log/pgbouncer /var/run/pgbouncer

COPY pgbouncer.ini /etc/pgbouncer/pgbouncer.ini
COPY entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh

EXPOSE 6432
ENTRYPOINT ["/entrypoint.sh"]
