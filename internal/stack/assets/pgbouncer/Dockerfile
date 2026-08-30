FROM alpine:3.21@sha256:48b0309ca019d89d40f670aa1bc06e426dc0931948452e8491e3d65087abc07d

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
