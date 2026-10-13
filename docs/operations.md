# PocketAdmin — Operations

Reference material for running and extending the panel: configuration, logging,
source layout and security posture. Kept out of the top-level `README.md`, which
stays focused on what the project is.

## Configuration

Runtime settings come from an optional **YAML** file. Precedence, lowest to
highest:

```
built-in defaults  <  pocketadmin.yaml  <  environment variables  <  CLI flags
```

Copy [`../pocketadmin.example.yaml`](../pocketadmin.example.yaml) to
`pocketadmin.yaml` (or point `-config` / `POCKETADMIN_CONFIG` at a path). A
missing `pocketadmin.yaml` is not an error — the panel starts on pure defaults.
Unknown keys are rejected at startup so a typo fails loudly.

```yaml
addr: 127.0.0.1:8090      # HTTP listen address
appName: PocketAdmin      # brand shown in the panel
dataDir: ./pb_data        # PocketBase data directory
perPage: 30               # default records per page
secureCookie: false       # mark the session cookie HTTPS-only
dev: false                # PocketBase dev mode
log:
  level: info             # debug | info | warn | error
  format: json            # json | text
```

| Flag | Environment | YAML key | Default |
| --- | --- | --- | --- |
| `-config` | `POCKETADMIN_CONFIG` | — | `pocketadmin.yaml` |
| `-addr` | `POCKETADMIN_ADDR` | `addr` | `127.0.0.1:8090` |
| `-app-name` | `POCKETADMIN_APP_NAME` | `appName` | `PocketAdmin` |
| `-dir` | `POCKETADMIN_DATA_DIR` | `dataDir` | `./pb_data` |
| `-per-page` | `POCKETADMIN_PER_PAGE` | `perPage` | `30` |
| `-secure-cookie` | `POCKETADMIN_SECURE_COOKIE` | `secureCookie` | `false` |
| `-dev` | `POCKETADMIN_DEV` | `dev` | `false` |
| `-log-level` | `POCKETADMIN_LOG_LEVEL` | `log.level` | `info` |
| `-log-format` | `POCKETADMIN_LOG_FORMAT` | `log.format` | `json` |

### Brand name

`appName` is resolved from this configuration and is **not** read from
PocketBase's own `Meta.AppName` setting. That setting lives in the git-ignored
data directory, so a fresh deployment would show PocketBase's factory default
(`"Acme"`) and the brand could not be version-controlled or differ per
environment. Renaming the application in the PocketBase dashboard affects the
PocketBase dashboard only.

The session signing key is **never** a hard-coded default: set
`POCKETADMIN_SESSION_KEY` (at least 32 characters), or let the app generate one
on first run and persist it as `pb_data/admin_session.key` (mode `0600`).

Any first positional argument is forwarded to PocketBase's own CLI, so
See [demo-data.md](demo-data.md) for the example dataset and the `demo` subcommand.

`pocketadmin superuser upsert …`, `pocketadmin superuser otp …` and friends work
as usual.

## Logging

Application logs go through `log/slog` as **one JSON object per line on stdout**:

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

## Endpoints

One port serves three things, dispatched by a stdlib mux in `main.go`:

| Prefix | Served by | Notes |
| --- | --- | --- |
| `/api`, `/api/` | PocketBase | REST API |
| `/_`, `/_/` | PocketBase | Bundled superuser dashboard |
| everything else | Gin | The AdminLTE panel |

Panel sign-in is at `/login`; the PocketBase dashboard is at `/_/`. Both
currently authenticate against `_superusers`. Section 1.1 of
[`database-design.md`](database-design.md) describes the planned move to a
`users` account with an `admins` profile.

## Front-end assets

`web/static/vendor/` contains four pinned packages, committed to the repository
so the binary embeds everything it needs:

| Package | Version | Files |
| --- | --- | --- |
| AdminLTE | 4.10.0 | `css/adminlte.min.css`, `js/adminlte.min.js` |
| Bootstrap | 5.3.8 | `css/bootstrap.min.css`, `js/bootstrap.bundle.min.js` |
| Font Awesome Free | 7.3.1 | `css/all.min.css`, `webfonts/*.woff2` |
| Chart.js | 4.5.1 | `chart.umd.min.js` |

`bash scripts/vendor-assets.sh` re-downloads them from the npm registry (no
node/npm required) and caches the tarballs under `.cache/thirdparty`. It also
vendors each package's own LICENSE file next to it — do not remove that step, as
MIT and CC BY 4.0 both require the notice to travel with the copies.

To update a version, edit the `PKGS` array and the extraction paths in that
script, then re-run it and `make build`. Check the current version with
`npm view <pkg> dist-tags.latest` rather than relying on memory.

**Not vendored, on purpose:** jQuery (AdminLTE 4 dropped it), Toastr (last
released 2017, jQuery-dependent; Bootstrap 5 toasts are used instead) and
OverlayScrollbars (AdminLTE 4 scrolls the sidebar natively).

## Source layout

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
and points `GOPROXY` at a reachable mirror. Every `go` invocation in the Makefile
sources it. Adjust or delete it if your environment can write to `$HOME` and
reach `proxy.golang.org` normally.

## Security notes

- Only records from `_superusers` can sign in; `IsSuperuser()` is re-checked on
  every request against the live database, so revoking access takes effect
  immediately.
- Panel writes require a valid CSRF token; PocketBase's API routes are excluded
  because they authenticate with bearer tokens rather than the panel cookie.
- Request bodies on panel routes are capped at 128 MiB before PocketBase
  validates each file field against its own limits.
- `-secure-cookie` should be enabled whenever the panel is served over TLS.

Because the embedded `core.App` bypasses PocketBase collection rules, those
rules are **not** the panel's security boundary — they exist to lock down the
public `/api` surface. See [`design.md`](design.md) § 12.

## Verification

The panel was exercised end-to-end against a live instance: superuser sign-in,
bad-password rejection, CSRF rejection on unauthenticated writes, all page
renders, and the full record lifecycle (create → search → update → delete)
including number and file fields, with uploaded files served back through
`/api/files/...`.
