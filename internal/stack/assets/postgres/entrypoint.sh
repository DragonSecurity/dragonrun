#!/bin/sh
set -e

# Portable stat wrappers: support both GNU (stat -c) and BSD/Alpine (stat -f).
get_file_perms() {
    _path=$1
    if stat -c '%a' /dev/null >/dev/null 2>&1; then
        stat -c '%a' "$_path"
    else
        # BSD / busybox style
        stat -f '%Lp' "$_path"
    fi
}

get_file_owner() {
    _path=$1
    if stat -c '%U' /dev/null >/dev/null 2>&1; then
        stat -c '%U' "$_path"
    else
        # BSD / busybox style
        stat -f '%Su' "$_path"
    fi
}

KEY_FILE=/var/lib/postgresql/server.key
CERT_FILE=/var/lib/postgresql/server.crt

KEY_EXISTS=0
CERT_EXISTS=0
[ -f "$KEY_FILE" ]  && KEY_EXISTS=1
[ -f "$CERT_FILE" ] && CERT_EXISTS=1

if [ "$KEY_EXISTS" -eq 1 ] && [ "$CERT_EXISTS" -eq 1 ]; then
    # Both files are present — validate modes directly.
    # Do NOT rely on [ -r ] alone: root bypasses file permissions so a mode-000
    # file would pass a readability check and only fail once postgres drops
    # privileges inside docker-entrypoint.sh.
    KEY_PERMS=$(get_file_perms "$KEY_FILE")
    if [ "$KEY_PERMS" != "400" ] && [ "$KEY_PERMS" != "600" ]; then
        echo "ERROR: $KEY_FILE has permissions $KEY_PERMS — must be 400 or 600." >&2
        echo "       Private key must not be readable by group or world (e.g. chmod 600 $KEY_FILE)." >&2
        exit 1
    fi
    CERT_PERMS=$(get_file_perms "$CERT_FILE")
    # Certificates (public material) are typically world-readable (644); allowing
    # 400 and 600 as well for stricter deployments.  The key (private material)
    # is held to the tighter 400/600 standard above.
    if [ "$CERT_PERMS" != "400" ] && [ "$CERT_PERMS" != "600" ] && [ "$CERT_PERMS" != "644" ]; then
        echo "ERROR: $CERT_FILE has permissions $CERT_PERMS — must be 400, 600, or 644." >&2
        echo "       Certificate must be readable by the postgres user." >&2
        exit 1
    fi
    # When running as root, chown both files to postgres so they remain
    # accessible after docker-entrypoint.sh drops privileges.
    # Mode is already validated above (400/600 for key, 400/600/644 for cert),
    # so postgres will be able to read the files once it owns them.
    # On read-only mounts chown fails; fall back to verifying:
    #   - the key is owned by postgres (always required — private material)
    #   - the cert is owned by postgres only when its mode is 400 or 600
    #     (mode 644 is world-readable, so any user can read it regardless of owner)
    if [ "$(id -u)" = "0" ]; then
        if ! chown postgres:postgres "$KEY_FILE" "$CERT_FILE" 2>/dev/null; then
            KEY_OWNER=$(get_file_owner "$KEY_FILE")
            CERT_OWNER=$(get_file_owner "$CERT_FILE")
            FAIL=0
            if [ "$KEY_OWNER" != "postgres" ]; then
                echo "ERROR: $KEY_FILE is not owned by postgres and cannot be chowned (read-only mount?)." >&2
                echo "       $KEY_FILE owner: $KEY_OWNER — ensure it is owned by postgres:postgres." >&2
                FAIL=1
            fi
            # Only enforce postgres ownership on the cert when it isn't world-readable.
            if [ "$CERT_PERMS" != "644" ] && [ "$CERT_OWNER" != "postgres" ]; then
                echo "ERROR: $CERT_FILE has mode $CERT_PERMS and is not owned by postgres (cannot chown, read-only mount?)." >&2
                echo "       $CERT_FILE owner: $CERT_OWNER — ensure it is owned by postgres:postgres or use mode 644." >&2
                FAIL=1
            fi
            if [ "$FAIL" = "1" ]; then
                exit 1
            fi
        fi
    else
        # Non-root start: we cannot chown, so enforce that the current user owns
        # and can read the key/cert before handing off to docker-entrypoint.sh.
        CUR_USER=$(id -un)
        KEY_OWNER=$(get_file_owner "$KEY_FILE")
        CERT_OWNER=$(get_file_owner "$CERT_FILE")
        FAIL=0
        if [ "$KEY_OWNER" != "$CUR_USER" ]; then
            echo "ERROR: $KEY_FILE is not owned by $CUR_USER (current user)." >&2
            echo "       $KEY_FILE owner: $KEY_OWNER — ensure it is owned by $CUR_USER or start the container as root so it can be chowned to postgres." >&2
            FAIL=1
        fi
        # Only enforce ownership on the cert when it isn't world-readable.
        if [ "$CERT_PERMS" != "644" ] && [ "$CERT_OWNER" != "$CUR_USER" ]; then
            echo "ERROR: $CERT_FILE has mode $CERT_PERMS and is not owned by $CUR_USER (current user)." >&2
            echo "       $CERT_FILE owner: $CERT_OWNER — ensure it is owned by $CUR_USER or use mode 644." >&2
            FAIL=1
        fi
        # Regardless of ownership, verify the current user can actually read the files.
        if [ ! -r "$KEY_FILE" ]; then
            echo "ERROR: $KEY_FILE is not readable by $CUR_USER." >&2
            echo "       Adjust ownership/permissions so the current user can read the private key." >&2
            FAIL=1
        fi
        if [ ! -r "$CERT_FILE" ]; then
            echo "ERROR: $CERT_FILE is not readable by $CUR_USER." >&2
            echo "       Adjust ownership/permissions so the current user can read the certificate." >&2
            FAIL=1
        fi
        if [ "$FAIL" = "1" ]; then
            exit 1
        fi
    fi
elif [ "$KEY_EXISTS" -eq 0 ] && [ "$CERT_EXISTS" -eq 0 ]; then
    # Neither file is present — generate a self-signed certificate.
    # To supply your own certificates, mount them before container start:
    #   /var/lib/postgresql/server.key  (mode 400 or 600, owner postgres)
    #   /var/lib/postgresql/server.crt  (mode 400, 600, or 644, owner postgres)
    echo "No TLS certificates found — generating a self-signed certificate for postgres."
    # Use a restrictive umask so the key file is never transiently world-readable.
    (
        umask 077
        # Prefer -addext when available (OpenSSL >= 1.1.1); fall back to a config
        # file for older OpenSSL versions that do not support -addext.
        if openssl req -help 2>&1 | grep -q -- '-addext'; then
            openssl req -x509 -nodes -newkey rsa:4096 \
                -subj "/CN=localhost" \
                -addext "subjectAltName=DNS:localhost,IP:127.0.0.1" \
                -keyout "$KEY_FILE" \
                -out "$CERT_FILE" \
                -days 365
        else
            SAN_CONFIG="$(mktemp)"
            cat >"$SAN_CONFIG" <<'EOF'
[ req ]
default_bits       = 4096
prompt             = no
default_md         = sha256
req_extensions     = v3_req
distinguished_name = req_distinguished_name

[ req_distinguished_name ]
CN = localhost

[ v3_req ]
subjectAltName = @alt_names

[ alt_names ]
DNS.1 = localhost
IP.1  = 127.0.0.1
EOF
            openssl req -x509 -nodes -newkey rsa:4096 \
                -config "$SAN_CONFIG" \
                -keyout "$KEY_FILE" \
                -out "$CERT_FILE" \
                -days 365
            rm -f "$SAN_CONFIG"
        fi
    )
    chmod 600 "$KEY_FILE" "$CERT_FILE"
    # Only chown when running as root; otherwise the files are already owned by
    # the current user and chown would fail with "Operation not permitted".
    if [ "$(id -u)" = "0" ]; then
        chown postgres:postgres "$KEY_FILE" "$CERT_FILE"
    fi
else
    # Exactly one file is present — this is a misconfiguration; refuse to
    # overwrite or silently proceed, as this could corrupt a partial mount.
    if [ "$KEY_EXISTS" -eq 0 ]; then
        MISSING="$KEY_FILE"
        PRESENT="$CERT_FILE"
    else
        MISSING="$CERT_FILE"
        PRESENT="$KEY_FILE"
    fi
    echo "ERROR: TLS certificate mismatch — $PRESENT is present but $MISSING is missing." >&2
    echo "       Mount both files or neither. Refusing to overwrite existing material." >&2
    exit 1
fi

exec docker-entrypoint.sh "$@"
