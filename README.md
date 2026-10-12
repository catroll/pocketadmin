# PocketAdmin

A small, self-contained admin panel for [PocketBase](https://pocketbase.io),
built with [Gin](https://gin-gonic.com) and [AdminLTE 3](https://adminlte.io).

PocketBase is embedded **as a library**: it owns the SQLite database, the data
directory, migrations, settings and the whole REST API. Gin serves an AdminLTE
management UI on top, and PocketBase's own REST API + bundled superuser
dashboard are mounted on the *same port* — so everything lives behind one URL
and ships as a single binary.

```
                     ┌──────────────────────────────────────────┐
   browser  ───────▶ │  Gin (single port)                       │
                     │                                          │
                     │  /login  /dashboard  /collections/...    │  AdminLTE UI
                     │        ───────────────────────────       │
                     │  /api/**            ──▶ PocketBase REST  │  mounted
                     │  /_/**              ──▶ PB dashboard     │  http.Handler
                     └──────────────────┬───────────────────────┘
                                        │ core.App (in-process)
                                        ▼
                              SQLite + storage/ in pb_data/
```

## Documentation

The platform roadmap lives in a specification set. Start with the capability
map: module boundaries, dependency direction and build order are the most
expensive things to change later.

| Document | Contents |
| --- | --- |
| [CAPABILITY-MAP.md](CAPABILITY-MAP.md) | 20 modules (13 platform, 7 business), dependency direction, build order, scope boundaries |
| [docs/requirements.md](docs/requirements.md) | Numbered functional requirements, traceability to the feature request, open questions |
| [docs/database-design.md](docs/database-design.md) | 55 PocketBase collections with fields, indexes and relationships |
| [docs/design.md](docs/design.md) | Architecture, authentication, authorisation, audit, jobs, integrations, security, phasing, risks |
| [docs/business-design.md](docs/business-design.md) | Pricing pipeline, order state machine, payment idempotency, refund workflow, subscription billing, inventory |
| [docs/README.md](docs/README.md) | Index, revision summary and module-to-document map |

These are drafts pending review; no implementation work starts until the
capability map is approved.

## Features

- **Superuser sign-in** against PocketBase's `_superusers` collection, with
  timing-equalised credential checks and a redirect-back (`?next=`) flow.
- **CSRF protection** on every panel write (double-submit token bound to the
  session), HttpOnly + SameSite=Lax session cookie, constant-time comparison.
- **Dashboard** — collection/record/file counters, a records-per-collection
  chart (Chart.js) and the storage footprint.
- **Collection browser** — every collection with type, field count and record
  count, user collections first.
- **Record CRUD** for `base` and `auth` collections:
  - paginated, sorted and free-text-searchable tables;
  - forms generated from the collection schema, covering text, email, url,
    number, bool, date, select, relation, file, editor, json, geopoint,
    password and autodate fields;
  - file uploads (append) and per-file removal, rendered with thumbnails;
  - PocketBase validation errors surfaced field-by-field.
- **Full PocketBase API and dashboard** still reachable at `/api/**` and `/_/`.

## Quick start

```bash
make assets      # vendor AdminLTE + plugins into web/static/vendor (once)
make build

# create the first superuser
make superuser EMAIL=admin@example.com PASSWORD='change-me-please'

make run         # http://127.0.0.1:8090
```

Then sign in at <http://127.0.0.1:8090/login>. PocketBase's own dashboard is at
<http://127.0.0.1:8090/_/>.

### Without make

```bash
go build -o bin/pocketadmin .
./bin/pocketadmin -dir ./pb_data superuser upsert admin@example.com 'change-me-please'
./bin/pocketadmin -dir ./pb_data -addr 127.0.0.1:8090
```

## Configuration

Runtime settings come from an optional **YAML** file. Precedence, lowest to
highest:

```
built-in defaults  <  pocketadmin.yaml  <  environment variables  <  CLI flags
```

Copy [`pocketadmin.example.yaml`](pocketadmin.example.yaml) to
`pocketadmin.yaml` (or point `-config` / `POCKETADMIN_CONFIG` at a path). A
missing `pocketadmin.yaml` is not an error — the panel starts on pure defaults.
Unknown keys are rejected at startup so a typo fails loudly.

```yaml
addr: 127.0.0.1:8090      # HTTP listen address
dataDir: ./pb_data        # PocketBase data directory (1-500 below)
perPage: 30               # default records per page
secureCookie: false       # mark the session cookie HTTPS-only
dev: false                # PocketBase dev mode
log:
  level: info             # debug | info | warn | error
  format: json            # json | text
```

| Flag             | Environment                | YAML key       | Default          |
| ---------------- | -------------------------- | -------------- | ---------------- |
| `-config`        | `POCKETADMIN_CONFIG`       | —              | `pocketadmin.yaml` |
| `-addr`          | `POCKETADMIN_ADDR`         | `addr`         | `127.0.0.1:8090` |
| `-dir`           | `POCKETADMIN_DATA_DIR`     | `dataDir`      | `./pb_data`      |
| `-per-page`      | `POCKETADMIN_PER_PAGE`     | `perPage`      | `30`             |
| `-secure-cookie` | `POCKETADMIN_SECURE_COOKIE`| `secureCookie` | `false`          |
| `-dev`           | `POCKETADMIN_DEV`          | `dev`          | `false`          |
| `-log-level`     | `POCKETADMIN_LOG_LEVEL`    | `log.level`    | `info`           |
| `-log-format`    | `POCKETADMIN_LOG_FORMAT`   | `log.format`   | `json`           |

The session signing key is **never** a hard-coded default: set
`POCKETADMIN_SESSION_KEY` (at least 32 characters), or let the app generate one
on first run and persist it as `pb_data/admin_session.key` (mode `0600`).

Any first positional argument is forwarded to PocketBase's own CLI, so
`pocketadmin superuser upsert …`, `pocketadmin superuser otp …` and friends work
as usual.

## Logging

Application logs go through `log/slog` as **one JSON object per line on
stdout**:

```json
{"time":"2026-10-12T19:50:43Z","level":"INFO","msg":"http request","method":"GET","path":"/dashboard","status":200,"latency":1810000,"clientIp":"127.0.0.1","bytes":8421}
```

Every request is logged once by Gin middleware, levelled by response status
(5xx → error, 4xx → warn, otherwise info). Startup and shutdown are logged as
`panel listening` / `shutdown signal received`.

PocketBase's *own* operational logs are independent of this: it always persists
them to the `_logs` collection (browsable in the PocketBase dashboard at `/_/`)
and only echoes them to stdout in dev mode. Set `log.format: text` for
human-readable output during local debugging.


## Layout

```
main.go                       flags/YAML → logging → PocketBase bootstrap → Gin
pocketadmin.example.yaml      annotated configuration template
internal/config/              YAML + env + flag configuration resolution
internal/logging/             log/slog JSON handler and the Gin request logger
internal/pbapp/               PocketBase instance + API handler mounted into Gin
internal/server/
  server.go                   Gin engine, routes, middleware wiring
  auth.go                     sessions, CSRF, login/logout, flash messages
  render.go                   per-page template sets + template helpers
  view.go                     view models
  fields.go                   schema → form inputs, search/sort, form apply
  handlers_dashboard.go       dashboard + collections index
  handlers_records.go         record list / create / edit / delete
web/
  assets.go                   embeds templates + static assets
  templates/                  AdminLTE layouts, partials and pages
  static/                     app.css, app.js and the vendored AdminLTE bundle
scripts/
  goenv.sh                    workspace-local Go caches + module proxy
  vendor-assets.sh            downloads AdminLTE + plugins into web/static/vendor
```

### Why the caches are local

`scripts/goenv.sh` pins `GOMODCACHE`, `GOCACHE` and `GOPATH` inside `.cache/`
and points `GOPROXY` at a reachable mirror. Every `go` invocation in the
Makefile sources it. Adjust or delete it if your environment can write to
`$HOME` and reach `proxy.golang.org` normally.

## Security notes

- Only records from `_superusers` can sign in; `IsSuperuser()` is re-checked on
  every request against the live database, so revoking access takes effect
  immediately.
- Panel writes require a valid CSRF token; PocketBase's API routes are excluded
  because they authenticate with bearer tokens rather than the panel cookie.
- Request bodies on panel routes are capped at 128 MiB before PocketBase
  validates each file field against its own limits.
- `-secure-cookie` should be enabled whenever the panel is served over TLS.

## Verification

The panel was exercised end-to-end against a live instance: superuser sign-in,
bad-password rejection, CSRF rejection on unauthenticated writes, all page
renders, and the full record lifecycle (create → search → update → delete)
including number and file fields, with uploaded files served back through
`/api/files/...`.
