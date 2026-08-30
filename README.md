# dragonrun

One shared local dev stack — postgres, pgbouncer, mailpit, pgweb, caddy,
dnsmasq — plus a CLI that hands each project a generated environment pointing
at it.

Applications keep running on the host under `mprocs`. dragonrun owns the infra
and the edge; it never supervises your app.

## The problem it solves

Across ~60 repos here, the per-project `docker-compose.yml` files are near
identical, and they nearly all bind the *same* host ports:

| binding | repos |
|---|---|
| `1025:1025` (mailpit smtp) | 40 |
| `8025:8025` (mailpit ui) | 39 |
| `8081:8081` (pgweb) | 31 |
| `5432:5432` (postgres) | 15 |

So only one project can run at a time. The exceptions — `55432`, `56432`,
`58085`, `1026`, `8026` — are hand-dodges that then froze into `.env.example`
files and became permanent. It is collision *and* drift from one cause: the
port is written down in sixty places.

dragonrun removes the port from the conversation. You get a hostname.

## Install

```sh
go install git.dragonsecurity.io/dragonrun@latest   # the binary
dragonrun init                                      # this machine
```

Needs sudo twice: to write `/etc/resolver/test`, and to trust caddy's local CA.
Nothing else touches the system. Add `--no-dns --no-trust` to skip both.

## Use

A **new** project — nothing to copy, no compose file at any point:

```sh
mkdir ~/projects/newthing && cd ~/projects/newthing
dragonrun new newthing --tenants -p 8181
```

gives you, with nothing to copy in:

| | |
|---|---|
| role | `newthing` |
| control database | `newthing` |
| tenant databases | `newthing_*` |
| site | `https://newthing.test` -> host port 8181 |
| `.env` | written, with working DSNs |

`adopt` differs on one point: it inherits `DB_NAME` from an existing `.env`, so
an adopted project keeps whatever control database it already had rather than
being renamed to match the project.

An **existing** project that still has its own stack:

```sh
cd ~/projects/some-api
dragonrun adopt -w          # infer, register, write .env
docker compose down         # stop its old stack
dragonrun tidy --apply      # delete the files it no longer needs
mprocs                      # app on the host, unchanged
open https://some-api.test
```

And to remember where anything is:

```sh
dragonrun show some-api     # URLs, credentials, DSNs, databases
```

`adopt` reads `.env`, `.env.example` and `docker-compose.yml` to work out the
project name, control database, host port, and whether it provisions tenant
databases at runtime. `--dry-run` shows its reasoning without changing anything.

The old `docker-compose.yml` is left in place. Stop it (`docker compose down`)
and dragonrun serves the same canonical ports, so unmigrated DSNs still resolve.

## Multi-tenant

Projects here create tenant databases at runtime. dragonrun expects that.

A project named `saas` owns control database `dragon` and every database under
`dragon_`. `dragonrun env` emits two DSNs:

```
DATABASE_URL=postgres://saas:…@localhost:6432/dragon        # pooled, transaction mode
ADMIN_DATABASE_URL=postgres://saas:…@localhost:5432/postgres  # direct
```

They differ for measured reasons, not style:

- A bare `CREATE DATABASE` *does* pass through pgbouncer. Inside an explicit
  transaction it fails — but that is a postgres rule that applies identically to
  a direct connection, so it is not the reason.
- Teardown is: pgbouncer holds idle server connections to a tenant database and
  those block `DROP DATABASE` even after the app disconnects. dragonrun drops
  `WITH (FORCE)`.
- Transaction pooling silently breaks session state — advisory locks,
  `LISTEN`/`NOTIFY`, session GUCs, temp tables. Tenant provisioning and
  migration code leans on advisory locks.

Tenant databases created at runtime need **no** pgbouncer configuration:
`[databases] * = …` routes any name, and `auth_query` resolves credentials from
postgres, so there is no `userlist.txt` to maintain.

## Isolation

One cluster is one blast radius unless something stops it. Two things do:

- Control databases get `REVOKE CONNECT … FROM PUBLIC` plus an explicit grant.
- Tenant databases are created by *your app*, with dragonrun nowhere in the
  loop, and arrive with `datacl = NULL` — meaning PUBLIC may connect. A
  database's ACL is **not** copied from its template, so revoking on `template1`
  does nothing here. A **login event trigger** (PostgreSQL 17+) *is* a
  per-database object and *is* copied, so it refuses any role that is not the
  database owner from the instant the database exists.

Superusers are exempt, which keeps the cluster recoverable. The `postgres`
database is deliberately unguarded — it is the shared admin entry point every
project reaches through `ADMIN_DATABASE_URL`.

Verify it:

```sh
dragonrun psql saas            # your own databases: fine
dragonrun psql autobot dragon  # someone else's: permission denied
```

## Built-in hostnames

The shared services get names too, served by caddy over the same trusted TLS:

| | |
|---|---|
| `https://mail.test` | mailpit |
| `https://pgweb.test` | pgweb |
| `https://<project>.test` | your app |

These proxy to the containers by service name, not back out through the host's
published ports — though `localhost:8025` and `localhost:8081` keep working.

`mail`, `pgweb` and `db` are reserved project names: a project called `mail`
would emit a second `mail.test` block and caddy refuses duplicate addresses,
taking the whole edge down rather than just that project.

A hostname is fine here because these are **browser** URLs. The database and
SMTP DSNs stay on `localhost` for the reason in the next section — a browser URL
failing off-network is an annoyance, an app failing to boot is not.

## DNS

Two modes. Pick one — running both is the failure case.

```sh
dragonrun dns              # show current mode and whether it actually resolves
dragonrun dns external     # you already have AdGuard Home / Pi-hole / a router rewrite
dragonrun dns dnsmasq      # let dragonrun answer *.test itself (default)
```

`external` removes `/etc/resolver/<domain>` and never starts the bundled
resolver. That removal is the entire point: **`/etc/resolver` takes precedence
over the system resolver**, so a leftover file shadows a perfectly good
network-wide rewrite. The symptom is a resolver that looks configured, a
container that looks healthy, and names that never resolve.

For AdGuard Home, two DNS rewrites are needed — `*.test` does not cover the
apex:

| Domain | Answer |
|---|---|
| `test` | `127.0.0.1` |
| `*.test` | `127.0.0.1` |

### The data plane uses `localhost`, not a hostname

Only `BASE_URL` gets a `.test` name — caddy routes on it, so it genuinely needs
DNS. Database and SMTP go to `localhost`, because your apps run on the same host
as the stack and a hostname there buys nothing while costing availability: in
`external` mode every `.test` lookup depends on the network resolver, so a
`db.test` DSN stops resolving the moment the machine leaves that network or the
resolver reboots — and every project fails to start. `localhost` is immune.

There is a test pinning this (`TestDataPlaneDSNsDoNotDependOnDNS`); do not
"tidy" the DSNs back onto hostnames.

**`127.0.0.1` means the loopback of whichever device asked.** That is correct
for the machine running dragonrun and wrong for every other device on the
network — a phone resolving `api.test` gets *itself*. caddy binds `127.0.0.1`
only, so other devices could not reach it regardless.

To serve the LAN you would need caddy republished on `0.0.0.0`, the rewrite
pointing at this machine's address, and Caddy's root CA installed on each
device. Note that under OrbStack ports 80/443 are already accepted on the LAN
address by OrbStack's own wildcard listener — so pointing a rewrite there
without republishing caddy gives a hang, not a clean "connection refused".

## Commands

Grouped by what they touch — machine state outlives the stack, and the stack
outlives any project. Installing the **binary** is not dragonrun's job; that is
`go install`.

| | |
|---|---|
| `init` | prepare this machine: stack, DNS, local CA (once) |
| `up` / `down` / `logs` / `status` | stack lifecycle |
| `new <name>` | create a project from scratch and write its `.env` |
| `adopt [path]` | infer and register an existing repo (`--dry-run`, `-w`) |
| `register <name>` | register explicitly (`--upstream`, `--db`, `--tenants`) |
| `show [name]` | URLs, credentials, DSNs and databases for one project |
| `tidy [path]` | delete the per-project stack files, once safe (`--apply`) |
| `env [name]` | print, or `--write` to merge into `.env` |
| `db list \| create \| drop \| reset \| harden` | inspect and manage databases |
| `psql [name] [db]` | shell as the project's own role |
| `sync` | rebuild everything from `registry.json` |
| `delete <name>` | unregister (`--data` also drops databases; aliases `remove`, `rm`) |
| `dns [mode]` | show, or switch between `dnsmasq` and `external` |
| `trust` | trust caddy's CA |
| `destroy` | undo `init` entirely — containers, data, DNS, CA |

## Dropping the per-project stack

`dragonrun tidy` reports whether a repo's `docker-compose.yml`, `postgres/` and
`pgbouncer/` are still needed, and deletes them with `--apply`.

It refuses when the compose file runs anything dragonrun does not provide.
Across this fleet that means `redis`, `keycloak`, `minio`, `openbao` and whole
app stacks — those repos keep their compose file, and tidy says which services
are the reason:

```
    postgres       replaced by dragonrun's postgres
    pgweb          replaced by dragonrun's pgweb
    redis          NOT replaced — dragonrun does not run this

  → keeping docker-compose.yml: it still runs redis
```

It also refuses on a repo that has not been adopted, since removing its stack
first would leave it with no database at all.

## Removing things

| scope | command |
|---|---|
| one tenant database | `dragonrun db drop <project> <tenant>` |
| a project's data, kept registered | `dragonrun db reset <project>` |
| a project entirely | `dragonrun delete <project> --data` |
| all data, stack stays installed | `dragonrun down -v` |
| everything, including DNS and CA | `dragonrun destroy` |

`destroy` is the counterpart to `init`: containers, volumes, the resolver
file, the trusted CA and `DRAGONRUN_HOME` all go. It requires typing `destroy`.
Repositories are never touched, so their `.env` files survive pointing at
nothing.

Each instance uses its own docker compose project name, derived from
`DRAGONRUN_HOME`. That is load-bearing: the compose file declares
`name: dragonrun`, so without it a second instance — a scratch one for testing,
say — shares containers and volumes with the real stack no matter what ports or
paths it was given, and its `down -v` destroys real data.

## State

`~/.dragonrun/` — `registry.json` (0600, holds passwords), the extracted stack,
generated caddy sites and pgweb bookmarks. The registry is the source of truth;
`dragonrun sync` rebuilds the cluster from it after a `down -v`.

## Notes

- `.test` is reserved by RFC 6761. Not `.dev` (real TLD, HSTS-preloaded, breaks
  plain http) and not `.local` (mDNS, fights macOS).
- dnsmasq publishes high (`15353`); `/etc/resolver/test` carries a matching
  `port` line, so no privileged bind and no fight with anything on 53.
- dnsmasq must NOT be given `listen-address=0.0.0.0`: it matches each query's
  destination address against that list, `0.0.0.0` never matches a real
  destination, and every query is dropped in silence while `netstat` shows a
  healthy listener.
- Ports are overridable at install (`--postgres-port`, `--https-port`, …) — a
  tool for ending port collisions should not itself be the immovable one.
