FROM alpine:3.24@sha256:5b02b42e375f7426f8d65c3af331ca05d9878f9989230354504e0b9dfd431f60

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
