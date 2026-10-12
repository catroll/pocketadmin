# Capability Map: PocketAdmin Platform

Status: **draft — awaiting review**
Scope: single tenant, one deployment serves one organisation.

This initiative bundles many independently testable capabilities, so it is
decomposed into modules before any module spec is written. Module ids are stable
and are the index for every downstream spec, plan and task list.

## Modules

| Module id | Responsibility | Depends on |
| --- | --- | --- |
| `identity` | Administrator and end-user accounts, credentials, sessions, per-actor preferences | — |
| `organization` | Departments, positions, employees, organisation tree | `identity` |
| `rbac` | Roles, permission points, menus, dictionaries, data scope | `identity` |
| `storage` | File library, folders, attachments, storage drivers | `identity` |
| `audit` | Operation, login, security-audit and exception logs | `identity` |
| `settings` | Site, authentication, registration, announcement and feature-flag settings | `identity`, `rbac` |
| `announcement` | Announcements and per-reader read state | `rbac` |
| `workbench` | Todos, messages, recent visits, quick links | `identity`, `rbac` |
| `jobs` | Scheduled task definitions, execution history, manual runs | `rbac` |
| `integrations` | Mail, SMS, object storage, identity providers, outbound call logs | `settings` |
| `monitoring` | Runtime metrics, health, system information and updates | `settings` |
| `developer` | OpenAPI reference, API keys and their usage | `identity`, `rbac` |

## Dependency direction

Arrows point one way; there are no cycles.

```
identity
├── organization
├── rbac ──┬── settings ──┬── integrations
│          │              └── monitoring
│          ├── announcement
│          ├── jobs
│          └── developer
└── audit
     workbench  (identity + rbac)
     storage    (identity)
```

`audit` deliberately depends only on `identity`: every other module writes audit
records, so `audit` must sit below them and must never import from them.

## Build order

| Wave | Modules | Rationale |
| --- | --- | --- |
| 1 | `identity` | Every other module needs an authenticated actor. |
| 2 | `organization`, `rbac`, `storage`, `audit` | Independent of each other; all that is needed to express "who may do what" and "what happened". |
| 3 | `settings` | Needs `rbac` to restrict who may change configuration. |
| 4 | `announcement`, `workbench`, `jobs`, `developer` | Feature surfaces over waves 1–3. |
| 5 | `integrations`, `monitoring` | Consume `settings` and are operationally optional for a first useful release. |

## Boundary notes

- **`identity` owns three distinct person concepts.** `employees` is the HR
  record (organisation membership), `admins` and `users` are login identities
  that may each optionally link to an employee. Conflating them is the most
  likely modelling mistake; see `docs/database-design.md`.
- **`rbac` owns menus and dictionaries.** Navigation is a projection of
  permissions, not an independent source of truth.
- **`settings` owns configuration; `integrations` owns credentials and drivers.**
  Settings reference a provider; they never embed secrets.
- **`audit` is append-only.** No module may update or delete audit records
  except through a retention job in `jobs`.

## Interface ownership

| Boundary | Contract owner | Documented in |
| --- | --- | --- |
| `identity` → all | Actor, session and preference accessors | `docs/design.md` |
| `rbac` → all | `RequirePermission` middleware and menu projection | `docs/design.md` |
| `settings` → `integrations` | Provider resolution by `code` and channel | `docs/database-design.md` |
| `storage` → all | File upload/reference API used by rich text and record fields | `docs/design.md` |

## Out of scope (for this initiative)

- Multi-tenant isolation, per-tenant billing and quotas.
- A customer-facing (non-admin) web application; `users` exists for identity and
  membership data, not for a shipped end-user UI.
- Horizontal scale-out. `jobs` assumes a single in-process scheduler.
