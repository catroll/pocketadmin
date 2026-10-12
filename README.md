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

Flags (each also has an environment-variable equivalent):

| Flag              | Env                        | Default            | Description                              |
| ----------------- | -------------------------- | ------------------ | ---------------------------------------- |
| `-addr`           | `POCKETADMIN_ADDR`         | `127.0.0.1:8090`   | HTTP listen address                      |
| `-dir`            | `POCKETADMIN_DATA_DIR`     | `./pb_data`        | PocketBase data directory                |
| `-per-page`       | —                          | `30`               | Default records per page                 |
| `-secure-cookie`  | —                          | `false`            | Mark the session cookie HTTPS-only       |
| `-dev`            | —                          | `false`            | PocketBase dev logging                   |

The session signing key is **never** a hard-coded default: set
`POCKETADMIN_SESSION_KEY`, or let the app generate one on first run and persist
it as `pb_data/admin_session.key` (mode `0600`).

Any first positional argument is forwarded to PocketBase's own CLI, so
`pocketadmin superuser upsert …`, `pocketadmin superuser otp …` and friends work
as usual.

## Layout

```
main.go                       flags → PocketBase bootstrap → Gin server
internal/config/              runtime configuration
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
  vendor-assets.sh            downloads/AdminLTE + plugins into web/static/vendor
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
