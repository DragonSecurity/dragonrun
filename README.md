# dragonrun

One shared local development stack — Postgres, PgBouncer, Mailpit, pgweb, Caddy
— and a CLI that gives every project a hostname instead of a port.

```sh
cd ~/projects/checkout-api
dragonrun new checkout-api -p 8080
# → https://checkout-api.test, a database, and a .env pointing at both
```

Your application keeps running the way it already does, on the host. dragonrun
owns the infrastructure and the edge, never your processes.

## The problem

Projects tend to ship a `docker-compose.yml` with a database, a mail catcher and
a database UI. Every project's copy is near identical, and they all bind the
same host ports — `5432`, `1025`, `8025`, `8081` — so only one project can run
at a time.

The usual fix is to bump one project to `55432`, another to `56432`, and write
those numbers into a `.env.example` where they live forever. Now you have both
collisions *and* drift, from a single cause: the port is written down in as many
places as you have projects.

dragonrun runs one stack for all of them and hands each project a generated
environment. The port stops being something anyone types.

## Requirements

- **macOS.** Wildcard DNS uses `/etc/resolver`, which is macOS-only. Everything
  else is portable; on Linux you can point a resolver at the bundled dnsmasq or
  add `/etc/hosts` entries yourself.
- **Docker** (Docker Desktop, OrbStack, Colima — anything with `docker compose`).
- **Go 1.27+** to build.

## Install

```sh
go install git.dragonsecurity.io/dragonrun@latest
dragonrun init
```

`init` prepares the machine: it builds and starts the stack, points `*.test` at
`127.0.0.1`, and trusts Caddy's local certificate authority so HTTPS works
without warnings. It asks for `sudo` twice — once to write `/etc/resolver/test`,
once for the keychain — and touches nothing else.

Skip either with `--no-dns` / `--no-trust`.

## Quickstart

**A new project.** Nothing to copy in, no compose file at any point:

```sh
mkdir checkout-api && cd checkout-api
dragonrun new checkout-api -p 8080
```

| | |
|---|---|
| role | `checkout_api` |
| database | `checkout_api` |
| site | `https://checkout-api.test` → host port 8080 |
| `.env` | written, with working DSNs |

**An existing project** that already has its own stack:

```sh
cd ~/projects/legacy-api
dragonrun adopt -w        # infer settings, register, merge into .env
docker compose down       # stop its old stack
dragonrun tidy --apply    # delete the files it no longer needs
```

`adopt` reads `.env`, `.env.example` and `docker-compose.yml` to work out the
project name, database, host port, and whether it provisions databases at
runtime. `--dry-run` shows its reasoning and changes nothing.

**Find anything again:**

```sh
dragonrun show checkout-api   # URLs, credentials, DSNs, databases
```

## How it works

```
             ┌── https://<project>.test ──→ your app, on the host
Caddy :443 ──┼── https://mail.test ───────→ Mailpit
             └── https://pgweb.test ──────→ pgweb

PgBouncer :6432 ──┐
                  ├──→ Postgres :5432   one cluster, one database per project
Postgres  :5432 ──┘
```

Everything binds `127.0.0.1`. The stack is embedded in the binary and extracted
to `$DRAGONRUN_HOME` (default `~/.dragonrun`), so there is no repository to
clone and no compose file to keep in sync.

## Multi-tenant projects

If your application creates databases at runtime — one per tenant — that works
without any per-tenant configuration.

A project named `saas` owns its control database plus everything under
`saas_`. `dragonrun env` emits two DSNs:

```sh
DATABASE_URL=postgres://saas:…@localhost:6432/saas          # pooled
ADMIN_DATABASE_URL=postgres://saas:…@localhost:5432/postgres # direct
```

Three things make runtime databases work with no configuration change:

- PgBouncer routes **any** database name through a wildcard, so a database that
  did not exist a second ago is reachable immediately.
- Credentials resolve through `auth_query` against Postgres, so there is no
  `userlist.txt` to maintain.
- That lookup function is installed into `template1`, so every database created
  afterwards inherits it — including ones dragonrun never sees.

**Why two DSNs.** Transaction pooling is what makes hundreds of application
connections cheap, but it breaks session-scoped state — advisory locks,
`LISTEN`/`NOTIFY`, session settings, temporary tables — and provisioning code
usually takes an advisory lock to serialise migrations. Teardown matters too:
PgBouncer holds idle server connections to a database, and those block
`DROP DATABASE` even after your application disconnects.

A bare `CREATE DATABASE` *does* survive the pooler. That is measured, not
assumed; it is not the reason for the split.

## Isolation

One cluster is one blast radius unless something prevents it.

Control databases get `REVOKE CONNECT … FROM PUBLIC` plus an explicit grant to
their owner. Databases your application creates at runtime are the harder case:
dragonrun is not in the loop, and they arrive with `datacl = NULL`, which means
`PUBLIC` may connect.

A database's ACL is **not** copied from its template, so revoking on `template1`
achieves nothing here. A **login event trigger** (PostgreSQL 17+) *is* a
per-database object and *is* inherited, so it refuses any role that does not own
the database, from the instant that database exists.

Superusers are exempt, which keeps the cluster recoverable. The `postgres`
database is deliberately unguarded — it is the shared admin entry point every
project reaches through `ADMIN_DATABASE_URL`.

```sh
dragonrun psql saas              # your own databases: fine
dragonrun psql other saas_acme   # someone else's: permission denied
```

## Hostnames and DNS

The shared services get names too, over the same trusted TLS:

| | |
|---|---|
| `https://<project>.test` | your app |
| `https://mail.test` | Mailpit |
| `https://pgweb.test` | pgweb |

`mail`, `pgweb` and `db` are reserved project names — a project called `mail`
would emit a duplicate site address, and Caddy refuses those by failing to load,
taking down the whole edge rather than one site.

`.test` is reserved for this purpose by [RFC 6761][rfc6761]. Not `.dev`, which
is a real TLD and HSTS-preloaded, so plain HTTP breaks; and not `.local`, which
is mDNS and will fight macOS.

[rfc6761]: https://www.rfc-editor.org/rfc/rfc6761#section-6.2

### Two DNS modes

```sh
dragonrun dns              # show current mode, and whether it resolves
dragonrun dns external     # something on your network already answers *.test
dragonrun dns dnsmasq      # let dragonrun answer (default)
```

Use `external` if you run AdGuard Home, Pi-hole, or a router that rewrites
`*.test`. It removes `/etc/resolver/<domain>` and starts no resolver — and that
removal is the whole point, because **`/etc/resolver` takes precedence over the
system resolver**. Leaving it in place alongside a network-wide rewrite gives
you a resolver that looks configured and names that never resolve.

For a network-wide rewrite you need two entries; `*.test` does not cover the
apex:

| Domain | Answer |
|---|---|
| `test` | `127.0.0.1` |
| `*.test` | `127.0.0.1` |

`127.0.0.1` means the loopback of whichever device asked — correct for the
machine running dragonrun, useless for a phone. Caddy binds `127.0.0.1` only, so
other devices cannot reach it regardless.

### Database and mail use `localhost`, not a hostname

Only `BASE_URL` gets a `.test` name, because Caddy routes on it. The database
and SMTP DSNs point at `localhost`.

Your application runs on the same host as the stack, so a hostname buys nothing
there while costing availability: in `external` mode every `.test` lookup
depends on the network resolver, so a `db.test` DSN stops resolving the moment
the machine leaves that network — and every project fails to boot. A browser URL
failing off-network is an annoyance; an application failing to start is not.

## Commands

Grouped by what they touch. Machine state outlives the stack, and the stack
outlives any project. Installing the **binary** is not among them — that is
`go install`.

**Machine**

| | |
|---|---|
| `init` | prepare this machine: stack, DNS, local CA (once) |
| `destroy` | undo `init` entirely — containers, data, DNS, CA |
| `dns [mode]` | show, or switch between `dnsmasq` and `external` |
| `trust` | trust Caddy's CA (`--prune` removes superseded ones) |

**Stack**

| | |
|---|---|
| `up` / `down` | start, stop (`down -v` also deletes data) |
| `status` | health, DNS wiring, registered projects |
| `logs [service]` | tail |

**Projects**

| | |
|---|---|
| `new <name>` | create from scratch and write `.env` |
| `adopt [path]` | infer and register an existing repo |
| `show [name]` | URLs, credentials, DSNs, databases |
| `env [name]` | print, or `--write` to merge into `.env` |
| `psql [name] [db]` | shell as the project's own role |
| `db list \| create \| drop \| reset \| harden` | manage databases |
| `tidy [path]` | delete per-project stack files, once safe |
| `delete <name>` | unregister (`--data` also drops databases) |
| `register` / `sync` | explicit registration; rebuild from the registry |

## Adopting and tidying up

`tidy` reports whether a repository's `docker-compose.yml` and its
`postgres/` and `pgbouncer/` build contexts are still needed, and deletes them
with `--apply`.

It refuses when the compose file runs anything dragonrun does not provide —
Redis, an identity provider, object storage, or the application's own services.
Those projects keep their compose file, and `tidy` names the reason:

```
    postgres       replaced by dragonrun's postgres
    pgweb          replaced by dragonrun's pgweb
    redis          NOT replaced — dragonrun does not run this

  → keeping docker-compose.yml: it still runs redis
```

It also refuses on a repository that has not been adopted, since removing its
stack first would leave it with no database at all. Build contexts are deleted
only if they look like stack scaffolding — a `Dockerfile` plus shell and config
files, nothing else — so a context containing source code is left alone.

## Removing things

| scope | command |
|---|---|
| one tenant database | `dragonrun db drop <project> <tenant>` |
| a project's data, kept registered | `dragonrun db reset <project>` |
| a project entirely | `dragonrun delete <project> --data` |
| all data, stack stays installed | `dragonrun down -v` |
| everything, including DNS and CA | `dragonrun destroy` |

`destroy` is the counterpart to `init`. It requires typing `destroy`, and never
touches your repositories — their `.env` files survive, pointing at nothing.

## State

`$DRAGONRUN_HOME` (default `~/.dragonrun`) holds `registry.json`, the extracted
stack, and generated Caddy sites and pgweb bookmarks. The registry is the source
of truth: after `down -v`, `dragonrun sync` rebuilds every role, database and
route from it.

`registry.json` is mode `0600` and contains every project's database password in
plain text. It lives outside any working tree for that reason.

Each instance uses its own Docker Compose project name, derived from
`$DRAGONRUN_HOME`. That is load-bearing: the compose file declares a fixed
project name, so without it a second instance — a scratch one for testing, say —
shares containers and volumes with the real stack no matter what ports or paths
it was given, and its `down -v` destroys real data.

## Notes

- dnsmasq publishes on a high port (`15353`); `/etc/resolver/test` carries a
  matching `port` line, so nothing needs a privileged bind or a fight with
  whatever else answers on 53.
- Do not give dnsmasq `listen-address=0.0.0.0`. It matches each query's
  destination against that list, `0.0.0.0` never matches a real destination, and
  every query is dropped in silence while `netstat` shows a healthy listener.
- Ports are overridable at `init` (`--postgres-port`, `--https-port`, …). A tool
  whose purpose is ending port collisions should not itself be the immovable one.

## License

Apache License 2.0. See [LICENSE](LICENSE).
