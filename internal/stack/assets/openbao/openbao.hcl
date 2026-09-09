# OpenBao, configured for a single-node local dev stack.
#
# Storage is the shared postgres cluster, not a file volume: it is what
# Keycloak and dex already use, so there is one backend to back up, one
# `dragonrun down -v` that means the same thing everywhere, and nothing about
# the stack's data that lives somewhere `dragonrun db` cannot see. OpenBao
# encrypts every value with the barrier key before it is written, so postgres
# holds ciphertext and the superuser DSN is not a way into the secrets.
#
# The connection string is NOT here. This file is an embedded asset with no
# interpolation; the DSN carries a generated password and arrives as
# BAO_PG_CONNECTION_URL from stack/.env, which is the one place compose
# renders secrets.
#
# The kv and HA lock tables are created by OpenBao itself on first start.

ui = true

# The container is granted IPC_LOCK so mlock normally succeeds. This is the
# fallback for hosts that will not grant it (rootless docker, some CI): without
# it the process refuses to start rather than running unlocked.
disable_mlock = true

storage "postgresql" {}

listener "tcp" {
  address     = "0.0.0.0:8200"
  tls_disable = true
}

# How OpenBao refers to itself -- in redirects, and in the `issuer` of the OIDC
# discovery document its identity tokens are validated against. The service
# name resolves on the compose network; the host port is a convenience only.
api_addr = "http://openbao:8200"
