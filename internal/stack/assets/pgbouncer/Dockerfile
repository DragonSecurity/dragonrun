FROM alpine:3.24@sha256:f59abe656b692d901e184be3445e084786602108599d2a51d229c0f5a48c71bf

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
