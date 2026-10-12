# PocketAdmin Platform — Requirements

Version: 0.1 (draft for review)
Companion documents: [`../CAPABILITY-MAP.md`](../CAPABILITY-MAP.md),
[`database-design.md`](database-design.md), [`design.md`](design.md)

## 1. Objective

Grow the existing PocketAdmin panel (embedded PocketBase + Gin + server-rendered
AdminLTE) from a database browser into an operations platform: identity and
organisation management, role-based access control, content and messaging,
scheduled work, third-party integrations, audit trails and a developer surface.

**Users.** Back-office administrators, organised into departments and positions,
holding roles that grant permission points over a menu tree.

**Success looks like.** An operator can onboard a colleague (employee → admin
account → role → department), and that colleague sees exactly the menus and
records their role permits; every state change they make is attributable in the
operation log; and an administrator can answer "who did what, when, from where,
and did it succeed" without reading server logs.

**Non-goals.** No multi-tenant isolation, no end-user-facing web product, no
horizontal scale-out. See `CAPABILITY-MAP.md` § Out of scope.

## 2. Scope decisions

| Decision | Choice | Consequence |
| --- | --- | --- |
| Data layer | Embedded PocketBase, entities modelled as collections | Schema ships as Go migrations; no SQL DDL |
| Tenancy | Single tenant | No tenant column; no cross-tenant isolation logic |
| UI | Server-rendered Go templates + AdminLTE | Menus are filtered server-side; no client-side dynamic routes |
| Documentation language | English | Aligns with code identifiers |

Two consequences deserve emphasis:

1. **All authorisation lives in the Gin layer.** The embedded `core.App` bypasses
   PocketBase collection rules, so collection rules are *not* the security
   boundary for the panel. They exist only to lock down the public `/api`
   surface. Any handler that forgets its permission check is a real
   vulnerability — hence the mandatory-middleware rule in `design.md`.
2. **Menus are derived, not authored per role.** A role grants permission
   points; the navigation tree is the set of menu nodes whose permission code
   the actor holds.

## 3. Functional requirements

Requirement ids are `FR-<module>-<n>` and are referenced by the build plan.

### 3.1 `identity` — accounts, sessions, preferences

**Administrators** (backend login identities)

- FR-identity-1 Create, edit, disable and delete an administrator account
  (name, avatar, email, phone, department, position, status).
- FR-identity-2 Assign one or more roles to an administrator. An administrator
  with no role can sign in but sees only the workbench.
- FR-identity-3 Reset an administrator's password. A reset revokes all of that
  administrator's sessions.
- FR-identity-4 Lock an account automatically after N consecutive failed sign-ins
  (N configurable in `settings`) and unlock it manually or on expiry.
- FR-identity-5 List active sessions per administrator (device, IP, user agent,
  created, last seen, expiry) and revoke one or all of them. A revoked session
  is rejected on its next request.
- FR-identity-6 View an administrator's sign-in history: time, IP, location,
  device, result and failure reason.
- FR-identity-7 Store per-administrator preferences: theme, language, page size,
  sidebar state, timezone, notification channels.
- FR-identity-8 Enforce password policy from `settings` (minimum length,
  character classes, history, expiry).

**End users** (customer identities)

- FR-identity-9 Full CRUD over end-user accounts with the same field set plus
  membership level, tags, source and registration metadata.
- FR-identity-10 Ban, unban and soft-delete a user; banning revokes their
  sessions.
- FR-identity-11 Impersonate a user for support purposes, producing a
  `security_audit_logs` entry and a distinct session marked as impersonated.
- FR-identity-12 The same session, sign-in history and preference capabilities
  as administrators (FR-identity-5..7), scoped to users.

**Cross-cutting**

- FR-identity-13 Distinguish three person concepts and never conflate them:
  `employees` (HR record), `admins` (backend identity), `users` (customer
  identity). An admin or user may optionally link to an employee.
- FR-identity-14 Enforce uniqueness on email and phone within each identity
  collection, with a clear error message.
- FR-identity-15 Support optional multi-factor authentication, delegated to
  PocketBase's OTP/MFA support on the auth collection.

### 3.2 `organization` — departments, positions, employees

- FR-organization-1 Department tree: create, rename, move (re-parent), reorder
  and delete. Deleting a department with children or members is refused.
- FR-organization-2 Maintain a materialised path per department so that
  "this department and all descendants" is a single indexed query.
- FR-organization-3 Assign a leader and contact details per department.
- FR-organization-4 Positions belong to a department; a position has a level, a
  category and an optional headcount.
- FR-organization-5 Employee records: employee number, name, avatar, gender,
  contact details, department, position, line manager, hire date, employment
  type and status.
- FR-organization-6 Render the organisation chart (tree of departments with
  member counts) and an indented list view.
- FR-organization-7 Link an employee to at most one administrator account and at
  most one end-user account.
- FR-organization-8 Moving a department updates the path of every descendant in
  one transaction.
- FR-organization-9 Refuse to delete a position or department that still has
  active employees assigned.

### 3.3 `rbac` — roles, permissions, menus, dictionaries

- FR-rbac-1 Manage roles: name, code, description, status, sort order and
  whether the role is a protected system role.
- FR-rbac-2 Maintain the catalogue of permission points, keyed by
  `<module>:<resource>:<action>` (for example `system:menu:create`).
- FR-rbac-3 Grant a set of permission points to a role.
- FR-rbac-4 Grant a set of menu nodes to a role; the effective menu is the
  intersection of granted menus and menus whose permission code the role holds.
- FR-rbac-5 Data scope per role: all records, own department, own department and
  descendants, own records only, or a custom department set.
- FR-rbac-6 Manage the menu tree: directory / menu / button / external link
  nodes with title, route name, path, component, icon, sort order, visibility,
  keep-alive and permission code.
- FR-rbac-7 Menu nodes carry a platform discriminator (`admin` now, reserved for
  a future `web` surface) so one catalogue can serve both.
- FR-rbac-8 Dictionary types and items: label, value, ordering, status, colour
  and CSS class, with optional nesting for tree-shaped dictionaries.
- FR-rbac-9 Dictionary values are the single source of truth for the option sets
  rendered in forms and filters.
- FR-rbac-10 Publish a runtime, cache-backed accessor so permission checks and
  menu projection do not query on every request.
- FR-rbac-11 A permission change invalidates the affected actors' cached
  permissions within one request cycle.

### 3.4 `storage` — file library

- FR-storage-1 Upload one or many files with drag-and-drop and progress.
- FR-storage-2 Organise files into a folder tree with rename, move and delete.
- FR-storage-3 Record and display size, MIME type, extension, checksum, uploader
  and upload time per file.
- FR-storage-4 Enforce an upload policy from `settings`: allowed extensions,
  maximum size, image dimension limits.
- FR-storage-5 Constrain uploads by MIME type sniffing, not by the client
  supplied extension alone.
- FR-storage-6 Preview images and common document types; download everything
  else.
- FR-storage-7 Reference a file from rich-text content and from record fields,
  tracking a reference count so a file is not deleted while still referenced.
- FR-storage-8 Choose the active storage driver (local filesystem, or an object
  storage provider configured in `integrations`).
- FR-storage-9 Deduplicate by checksum within a folder.
- FR-storage-10 Clearly distinguish record-owned files (PocketBase file fields on
  the owning record) from library files; the two are not interchangeable.

### 3.5 `audit` — operation, login, security and exception logs

- FR-audit-1 Record every state-changing panel request: actor, module, action,
  HTTP method, path, sanitised parameters, result, status code, duration, IP and
  user agent.
- FR-audit-2 Redact secrets (passwords, tokens, API keys, provider credentials)
  before persisting parameters. Redaction is deny-by-key-name plus an explicit
  allow-list for known-safe fields.
- FR-audit-3 Record every sign-in attempt for both administrators and end users:
  identifier, IP, location, device, result and failure reason.
- FR-audit-4 Record security-relevant events as first-class audit entries:
  permission denied, password changed, role changed, account locked, session
  revoked, settings changed, API key created/revoked, sensitive data exported and
  login anomaly.
- FR-audit-5 Record unhandled application exceptions with message, stack trace,
  request id, route and actor.
- FR-audit-6 Filter and search each log type by actor, time range, module, result
  and free text, with pagination and CSV export.
- FR-audit-7 Audit collections are append-only through the application. No
  update or delete path exists outside the retention job.
- FR-audit-8 A retention job deletes records older than a configurable number of
  days per log type.
- FR-audit-9 Distinguish application logs from PocketBase's own `_logs`
  collection and link to the latter for raw request-level diagnostics.

### 3.6 `settings` — configuration

- FR-settings-1 Store settings as grouped key/value entries with a typed value,
  description and public/private flag.
- FR-settings-2 **Site**: site name, URL, logo, favicon, description, keywords,
  ICP filing, copyright, default timezone, default language.
- FR-settings-3 **Authentication**: captcha on/off, maximum failed attempts,
  lockout duration, password minimum length and complexity, session timeout,
  MFA requirement, sign-in IP allow-list.
- FR-settings-4 **Registration**: enabled, identifier method (email / phone /
  username), verification requirement, default role, default membership level,
  terms acceptance.
- FR-settings-5 **Announcement**: enabled, display position (sign-in page,
  workbench, modal), display mode and dismissal behaviour.
- FR-settings-6 **Features**: individual on/off switches per optional module
  (files, workbench, jobs, integrations, developer surface, API keys) plus a
  global maintenance mode with a maintenance message.
- FR-settings-7 Every settings change writes a `security_audit_logs` entry
  recording old and new value.
- FR-settings-8 Settings are typed and validated on write; an invalid value is
  rejected with a field-level message.
- FR-settings-9 Public settings are readable without authentication (site name,
  logo) and are never used to expose secrets.

### 3.7 `announcement`

- FR-announcement-1 Create, edit, publish, archive and expire announcements with
  a rich-text body and attachments.
- FR-announcement-2 Type (notice / announcement / maintenance / update) and
  severity (normal / important / urgent) drive presentation.
- FR-announcement-3 Schedule by publish-at and expire-at; a scheduled
  announcement becomes visible without manual action.
- FR-announcement-4 Target an audience: everyone, administrators only, end users
  only, or a set of departments.
- FR-announcement-5 Pin an announcement to the top of the list and workbench.
- FR-announcement-6 Track read state per reader; show read counts and let an
  administrator see who has not read a targeted announcement.
- FR-announcement-7 Surface unread announcements as `workbench` messages.

### 3.8 `workbench`

- FR-workbench-1 **Todos**: create, edit, complete, reopen, cancel, prioritise
  and schedule a personal todo with an optional due date and reminder.
- FR-workbench-2 Link a todo to another record (for example an announcement or a
  user) so the workbench can deep-link into it.
- FR-workbench-3 **Messages**: a per-administrator inbox fed by system events,
  announcements and explicit sends; mark read, mark all read and delete.
- FR-workbench-4 Broadcast a message to a department, a role or all
  administrators; delivery fans out to per-recipient records.
- FR-workbench-5 **Recent visits**: automatically record the last N distinct
  pages an administrator visited, with title, path, icon and visit count.
- FR-workbench-6 **Quick links**: a per-administrator set of shortcut tiles,
  seeded with sensible defaults and freely editable.
- FR-workbench-7 The workbench renders only widgets whose owning module is
  enabled in `settings` and whose permission the actor holds.

### 3.9 `jobs` — scheduled tasks

- FR-jobs-1 Register task definitions in code; the catalogue of executable
  handlers is fixed at build time and not user-editable.
- FR-jobs-2 Persist per-task scheduling metadata: cron expression, enabled
  state, concurrency policy, timeout, retry policy and failure notifications.
- FR-jobs-3 Enable, disable, pause and resume a task at runtime without a
  restart.
- FR-jobs-4 Run a task manually, recording the trigger source.
- FR-jobs-5 Record every execution: start, finish, duration, status, captured
  output and error.
- FR-jobs-6 Enforce the concurrency policy (allow / forbid / replace) and the
  timeout, marking a timed-out run accordingly.
- FR-jobs-7 Retry a failed run according to its retry policy, with a bounded
  attempt count and backoff.
- FR-jobs-8 Notify configured administrators on failure.
- FR-jobs-9 Prevent two instances from running the same task concurrently; a
  single in-process scheduler is assumed.
- FR-jobs-10 Built-in tasks include: audit retention, login-log retention,
  expired-session cleanup, membership expiry, announcement publish/expire and
  temporary-file cleanup.

### 3.10 `integrations`

- FR-integrations-1 Manage provider configurations of type email, SMS, object
  storage, identity provider and outbound webhook.
- FR-integrations-2 Each provider has a driver, a non-secret configuration
  object, an encrypted secret object, an enabled state and a default flag per
  channel.
- FR-integrations-3 **Email**: SMTP, plus at least one transactional API driver;
  sender name and address; a test-send action.
- FR-integrations-4 **SMS**: at least one regional gateway driver, with template
  support and a test-send action.
- FR-integrations-5 **Object storage**: S3-compatible, plus at least one regional
  provider; endpoint, bucket, region and path-prefix configuration; a
  connectivity test.
- FR-integrations-6 **Identity providers**: OAuth2 / OIDC and LDAP for
  administrator sign-in, with a configurable attribute-to-field mapping.
- FR-integrations-7 Secrets are encrypted at rest and are never returned to the
  browser. The edit form shows a masked placeholder; submitting the placeholder
  leaves the stored secret unchanged.
- FR-integrations-8 Every outbound attempt is logged with channel, provider,
  action, duration, status and error.
- FR-integrations-9 Exactly one provider per channel may be the default; setting
  a new default clears the previous one atomically.
- FR-integrations-10 Deleting a provider that is referenced by `settings` or by
  stored files is refused with an explanation.

### 3.11 `monitoring`

- FR-monitoring-1 Live runtime panel: process uptime, goroutine count, heap and
  allocation, GC activity, CPU count and Go version.
- FR-monitoring-2 Database panel: main database size, auxiliary database size,
  table count, WAL size and slow-query summary from PocketBase's log collection.
- FR-monitoring-3 Storage panel: total uploaded bytes, file count, growth over
  the last 24 hours.
- FR-monitoring-4 Traffic panel: requests per minute, error rate, p50/p95/p99
  latency, derived from the audit logs.
- FR-monitoring-5 Health endpoint reporting application, database and scheduler
  liveness for external probes.
- FR-monitoring-6 Optional periodic metric snapshots to render 24-hour and
  7-day trends.
- FR-monitoring-7 **System information**: application version, PocketBase
  version, Go version, build time and commit, OS and architecture, data
  directory, configuration file in use.
- FR-monitoring-8 **System updates**: check for a newer release, display release
  notes and channel (stable / beta), and record the outcome of an applied
  update.
- FR-monitoring-9 Applying an update is an assisted, explicit action with a
  recorded before/after version; the platform never self-updates silently.

### 3.12 `developer`

- FR-developer-1 Serve an OpenAPI 3 specification for the panel's JSON API.
- FR-developer-2 Serve an interactive Swagger UI at a permission-gated route.
- FR-developer-3 Keep the specification in sync with handlers; a stale
  specification is a defect, verified by a test that fails when a route exists
  without a documented operation.
- FR-developer-4 Manage API keys: name, owning administrator, scopes, rate
  limit, IP allow-list and expiry.
- FR-developer-5 Display a new key's secret exactly once, at creation. Only a
  hash and a searchable prefix are stored thereafter.
- FR-developer-6 Revoke a key immediately; a revoked key is rejected on its next
  request.
- FR-developer-7 Record per-key usage: last used time, request count and the
  most recent source IP.
- FR-developer-8 Scope a key to a subset of permission points; the effective
  authority is the intersection of the owner's permissions and the key's scopes.
- FR-developer-9 Rate-limit requests per key and reject requests from IPs outside
  the allow-list.

## 4. Requirements traceability

Every item in the originating feature request maps to a module.

| Original item | Module | Requirement ids |
| --- | --- | --- |
| 1. Workbench — todos | `workbench` | FR-workbench-1, -2 |
| 1. Workbench — messages | `workbench` | FR-workbench-3, -4 |
| 1. Workbench — recent visits | `workbench` | FR-workbench-5 |
| 1. Workbench — quick entries | `workbench` | FR-workbench-6, -7 |
| 2. Users — account | `identity` | FR-identity-9, -10, -13, -14 |
| 2. Users — login records | `audit` | FR-audit-3, FR-identity-6, -12 |
| 2. Users — preferences | `identity` | FR-identity-7, -12 |
| 2. Users — membership | `identity` | FR-identity-9 (and § 4.1 below) |
| 2. Users — sessions | `identity` | FR-identity-5, -12 |
| 3. Organization — departments | `organization` | FR-organization-1..3, -8, -9 |
| 3. Organization — positions | `organization` | FR-organization-4, -9 |
| 3. Organization — employees | `organization` | FR-organization-5, -7 |
| 3. Organization — org chart | `organization` | FR-organization-6 |
| 4. Files | `storage` | FR-storage-1..10 |
| 5. Backend — administrators (account) | `identity` | FR-identity-1..4, -8 |
| 5. Backend — administrators (login records) | `audit` | FR-audit-3, FR-identity-6 |
| 5. Backend — administrators (preferences) | `identity` | FR-identity-7 |
| 5. Backend — administrators (sessions) | `identity` | FR-identity-5 |
| 5. Backend — announcements | `announcement` | FR-announcement-1..7 |
| 5. Backend — settings | `settings` | FR-settings-1..9 |
| 5. Backend — scheduled tasks | `jobs` | FR-jobs-1..10 |
| 5. Backend — operation logs | `audit` | FR-audit-1, -2, -6..9 |
| 5. Backend — login logs | `audit` | FR-audit-3, -6 |
| 5. Backend — security audit logs | `audit` | FR-audit-4, -6 |
| 5. Backend — exception logs | `audit` | FR-audit-5, -6 |
| 6. System — menus | `rbac` | FR-rbac-6, -7 |
| 6. System — dictionaries | `rbac` | FR-rbac-8, -9 |
| 6. System — permissions | `rbac` | FR-rbac-1..5, -10, -11 |
| 6. System — settings / site | `settings` | FR-settings-2 |
| 6. System — settings / authentication | `settings` | FR-settings-3 |
| 6. System — settings / registration | `settings` | FR-settings-4 |
| 6. System — settings / announcement | `settings` | FR-settings-5 |
| 6. System — settings / features (toggles) | `settings` | FR-settings-6 |
| 6. System — monitoring | `monitoring` | FR-monitoring-1..7 |
| 6. System — integrations (email) | `integrations` | FR-integrations-1..3, -8, -9 |
| 6. System — integrations (SMS) | `integrations` | FR-integrations-1, -2, -4, -8, -9 |
| 6. System — integrations (object storage) | `integrations` | FR-integrations-1, -2, -5, -8, -9 |
| 6. System — integrations (identity providers) | `integrations` | FR-integrations-1, -2, -6, -8, -9 |
| 6. System — system information / updates | `monitoring` | FR-monitoring-7..9 |
| 7. Developer — API docs (Swagger) | `developer` | FR-developer-1..3 |
| 7. Developer — API Key | `developer` | FR-developer-4..9 |

### 4.1 Items named in the request but needing a definition

Three request items are named without enough detail to be unambiguous. The
design assumes the following; confirm or redirect before implementation.

- **Membership.** Assumed to mean a tiered customer membership: a
  `member_levels` catalogue (name, level, price, duration, benefits) plus
  `user_memberships` history per user (level, start, expiry, status, source).
  If it instead means a paid-subscription product with orders and payments, an
  `orders` module is required and is not currently scoped.
- **Backend settings vs system settings.** The request lists "settings" twice —
  once under Backend, once under System with sub-sections. The design
  consolidates both into the single `settings` module (§ 3.6) grouped as site,
  authentication, registration, announcement and features. Nothing is lost, and
  there is exactly one place to change a setting.
- **Data scope.** "Permissions" is taken to include row-level data scope
  (all / department / department and below / self / custom) in addition to
  menu and button permissions. If only functional permissions are wanted,
  FR-rbac-5 and the `dataScope` fields can be dropped.

## 5. Open questions

| Id | Question | Blocks | Default if unanswered |
| --- | --- | --- | --- |
| OQ-1 | Does membership imply orders/payments? | `identity` scope | No orders module |
| OQ-2 | Should employees be able to sign in to the panel, or must they also hold an `admins` record? | `organization`, `identity` | Employees are HR-only; an admin record is required to sign in |
| OQ-3 | Is row-level data scope required, or are functional permissions sufficient? | `rbac` | Data scope included |
| OQ-4 | Is the current `_superusers` panel sign-in replaced by `admins`, or kept as break-glass? | `identity` | `admins` is the panel identity; `_superusers` retained as break-glass |
| OQ-5 | Are announcements visible to end users, or administrators only? | `announcement`, `settings` | Both, audience-selectable |
| OQ-6 | Retention period per audit log type? | `audit`, `jobs` | 180 days operation/login, 365 days security, 90 days exception |
| OQ-7 | Is the OpenAPI specification hand-written or generated from handler annotations? | `developer` | Generated (swaggo) with a drift test |

## 6. Success criteria

- Every requirement above is either implemented or explicitly deferred with a
  recorded reason.
- A new administrator can be onboarded end-to-end (employee, account, role,
  department) without editing the database directly.
- Every menu item visible to an actor corresponds to a permission the actor
  holds; a request to a route the actor lacks permission for returns 403 and
  writes a `security_audit_logs` entry.
- Every state-changing request appears in `operation_logs` with no secret value
  present in the stored parameters.
- A failing scheduled task is visible in `job_runs` with its error and produces
  a notification.
- The OpenAPI document describes every registered route, verified by an
  automated drift test.
