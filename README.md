# PocketAdmin

A self-contained admin panel for [PocketBase](https://pocketbase.io), written in
Go. PocketBase is embedded as a library, so the whole thing ships as a single
binary: one process, one port, one SQLite file, no external services.

## Goals

PocketBase already gives you a database, authentication, a REST API and a file
store. What it ships for a UI, though, is aimed at developers defining schemas —
not at operators doing day-to-day work.

PocketAdmin fills that gap: a familiar AdminLTE back office for browsing and
editing data, built so it can grow from a database browser into a full operations
platform.

The project runs in two layers:

- **The panel that works today** — sign in, browse collections, manage records
  and files.
- **A specified platform** — identity and organisation, role-based access
  control, audit trails, configuration, scheduled jobs, third-party
  integrations, and a business domain covering catalogue, orders, payments,
  refunds, memberships, coupons and subscriptions. See
  [Documentation](#documentation).

## Features

- **Single binary** — PocketBase embedded; no external database or service.
- **Dashboard** — collection, record and file counts, a records-per-collection
  chart and the storage footprint.
- **Collection browser** — type, field count and record count per collection.
- **Record management** for base and auth collections:
  - paginated, sortable, free-text-searchable tables;
  - forms generated from the collection schema, covering every PocketBase field
    type — text, email, url, number, bool, date, select, relation, file, editor,
    json, geopoint, password and autodate;
  - file uploads with per-file removal and thumbnails;
  - validation errors surfaced field by field.
- **Superuser sign-in** with timing-equalised credential checks and a
  redirect-back flow.
- **Security** — CSRF-protected writes, HttpOnly session cookie, upload size
  caps.
- **Nothing hidden** — the full PocketBase REST API and its bundled dashboard
  stay reachable on the same port at `/api/**` and `/_/`.

## Tech stack

| Layer | Choice |
| --- | --- |
| Language | Go |
| Data | [PocketBase](https://pocketbase.io) embedded as a library (SQLite) |
| HTTP | [Gin](https://gin-gonic.com) |
| UI | [AdminLTE 3](https://adminlte.io) on Bootstrap 4, server-rendered with `html/template` |
| Charts | [Chart.js](https://www.chartjs.org) |
| Configuration | YAML, overridable by environment variables and CLI flags |
| Logging | `log/slog`, one JSON object per line on stdout |

## Quick start

```bash
make assets      # vendor AdminLTE and its plugins (once)
make superuser EMAIL=admin@example.com PASSWORD='change-me-please'
make run         # http://127.0.0.1:8090
```

Panel: <http://127.0.0.1:8090/login> · PocketBase dashboard:
<http://127.0.0.1:8090/_/>

Requires Go 1.25 or newer. `make help` lists every target.

## Documentation

| Document | Contents |
| --- | --- |
| [docs/operations.md](docs/operations.md) | Configuration, logging, endpoints, source layout, security notes |
| [CAPABILITY-MAP.md](CAPABILITY-MAP.md) | Module decomposition, dependency direction, build order |
| [docs/requirements.md](docs/requirements.md) | Numbered functional requirements and open questions |
| [docs/database-design.md](docs/database-design.md) | Collection schema, indexes and relationships |
| [docs/design.md](docs/design.md) | Architecture, authentication, authorisation, audit, jobs, integrations, security |
| [docs/business-design.md](docs/business-design.md) | Pricing, orders, payments, refunds, subscriptions, inventory |
| [docs/README.md](docs/README.md) | Index and module map |

The specification set is a draft under review. The panel described under
[Features](#features) is what is implemented today; everything beyond it is
planned, not built.

## License

Licensed under the [Apache License, Version 2.0](LICENSE).

```
Copyright 2026 catroll
```

See [`NOTICE`](NOTICE) for attribution details.
