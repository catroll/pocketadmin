# PocketAdmin Platform — Technical Design

Version: 0.1 (draft for review)
Companion documents: [`requirements.md`](requirements.md),
[`database-design.md`](database-design.md),
[`business-design.md`](business-design.md), [`../CAPABILITY-MAP.md`](../CAPABILITY-MAP.md)

## 1. Architecture

The existing single-binary shape is retained. PocketBase is embedded, Gin owns
the HTTP surface, and pages are rendered server-side with AdminLTE.

```
                    ┌───────────────────────────────────────────────┐
   browser ────────▶│  stdlib ServeMux                              │
                    │   /api/**  ──▶ PocketBase REST (locked down)  │
                    │   /_/**    ──▶ PocketBase dashboard           │
                    │   /**      ──▶ Gin                           │
                    └───────────────────┬───────────────────────────┘
                                        │
        Gin middleware chain            │
        logging → recovery → session → auth → rbac → audit
                                        │
                                        ▼
        handlers (HTTP, view models)    │
                    │                   │
                    ▼                   │
        services (business rules, transactions)
                    │
                    ▼
        repositories (PocketBase query helpers)
                    │
                    ▼
        core.App  (embedded PocketBase, bypasses collection rules)
```

### 1.1 Layers

| Layer | Responsibility | May depend on |
| --- | --- | --- |
| `handlers` | Parse and validate input, call a service, render or redirect. No business rules. | services, views |
| `services` | Business rules, multi-record transactions, cross-module orchestration, audit emission. | repositories |
| `repositories` | PocketBase queries and persistence, expressed in domain terms. | `core.App` |
| `views` | View models and template rendering. | — |
| `middleware` | Cross-cutting: session, permission, audit, request id. | services |

Rules that keep this honest:

- A handler never calls `app.FindRecordById` directly.
- A service never touches `gin.Context`; it takes a `context.Context` and plain
  Go arguments, which is what makes it unit-testable.
- A repository never returns a PocketBase `*core.Record` across the layer
  boundary for mutation; it returns domain structs. Reading a record to build a
  view model is allowed.

### 1.2 Package layout

```
main.go
internal/
  config/            YAML + env + flag resolution            (exists)
  logging/           slog JSON handler, Gin request logger   (exists)
  pbapp/             PocketBase instance + API handler       (exists)
  server/            Gin engine, routes, middleware wiring   (exists, grows)
  middleware/        session, auth, rbac, audit, requestid
  modules/
    identity/        handlers, service, repository, view
    organization/
    rbac/
    storage/
    workbench/
    announcement/
    jobs/
    settings/
    audit/
    integrations/
    monitoring/
    developer/
  settings/          typed key registry, validator, cached accessor
  permission/        code constants, checker, menu projection
  jobs/              handler registry, scheduler wiring
  drivers/           mailer, sms, storage, idp interfaces + impls
  audit/             redaction, writers
migrations/          schema + seed migrations, one per module
web/templates/       layouts, partials, per-module pages
docs/                this specification set
```

Each module package exposes the same shape so a reader can navigate an
unfamiliar module:

```
modules/<id>/
  handlers.go     gin handlers + route registration
  service.go      business rules
  repository.go   PocketBase queries
  model.go        domain structs and input types
  view.go         view models
  *_test.go
```

### 1.3 Route registration

Modules register their own routes, so adding a module does not require editing a
central route table:

```go
// modules/rbac/routes.go
func Register(r gin.IRouter, deps Deps) {
    g := r.Group("/system")

    g.GET("/menus", deps.Require(permission.SystemMenuList), deps.Handler.ListMenus)
    g.GET("/menus/:id", deps.Require(permission.SystemMenuView), deps.Handler.ViewMenu)
    g.POST("/menus", deps.Require(permission.SystemMenuCreate), deps.Handler.CreateMenu)
    g.POST("/menus/:id", deps.Require(permission.SystemMenuUpdate), deps.Handler.UpdateMenu)
    g.POST("/menus/:id/delete", deps.Require(permission.SystemMenuDelete), deps.Handler.DeleteMenu)
}
```

Every non-GET route is wrapped in a permission requirement at registration
time. A route registered without `deps.Require` on an unsafe method fails a
startup self-check (see § 12.4).

## 2. Authentication and sessions

### 2.1 One account, two surfaces

There is exactly one auth collection — `users`, PocketBase's own. A person has
one password, one reset flow and one identity. Authority is added by a 1:1
profile extension:

| Concept | Collection | Grants |
| --- | --- | --- |
| The account | `users` (auth) | Credentials and shared profile |
| Employee profile | `employees` (base, 1:1) | Organisation membership; **no** sign-in authority of its own |
| Admin profile | `admins` (base, 1:1) | Back-office sign-in authority, roles and data scope |
| Break-glass | `_superusers` | PocketBase dashboard at `/_/` only |

The panel currently signs administrators in through `_superusers`. That is
replaced by "a `users` account holding an active `admins` profile" (open question
OQ-4), with `_superusers` retained purely so an operator can still reach `/_/` if
the RBAC data is damaged.

A middleware guard expresses the rule once:

```go
// RequireAdminProfile rejects any request whose account has no active admin profile.
func RequireAdminProfile(app core.App) gin.HandlerFunc {
    return func(c *gin.Context) {
        actor := CurrentActor(c)
        if actor == nil || actor.AdminProfileID == "" || actor.AdminStatus != "active" {
            c.AbortWithStatus(http.StatusForbidden)
            return
        }
        c.Next()
    }
}
```

Detaching an admin profile (FR-identity-12) sets every `admin`-context session for
that account to revoked, so access ends on the next request rather than at the
next token expiry.

### 2.2 Session flow

1. `POST /login` verifies credentials against `users`, honouring the lockout and
   captcha settings from the relevant settings surface, then requires an active
   `admins` profile for the back-office route.
2. On success a random session id is generated. Its SHA-256 is stored in
   `sessions` with `context = admin`; the raw id lives only in the HttpOnly
   cookie.
3. The cookie value is the session id, signed by the existing
   `gin-contrib/sessions` cookie store. The signature prevents tampering; the
   database row provides revocation. Both must be valid.
4. Every request: verify the cookie signature, load the session row, reject if
   revoked, expired or of the wrong `context`, then slide `lastSeenAt` forward at
   most once a minute to avoid a write per request.
5. Sign-out, password reset, ban, profile detach and explicit revocation set
   `revokedAt`; the next request with that cookie is rejected.

A database-backed session table exists because PocketBase auth tokens are
stateless JWTs with no server-side revocation. Listing and revoking sessions is a
stated requirement (FR-identity-14), so the panel owns its session records.

Because sessions carry a `context`, "administrator sessions" and "user sessions"
are two filtered views of one table. Signing in to the back office does not grant
a product session and vice versa, so a compromise in one surface does not carry
into the other.

MFA, when enabled, uses PocketBase's OTP/MFA support on `users` before the
session row is created.

### 2.3 Impersonation

Impersonation creates a session with `impersonated = true` and the acting
administrator recorded in `security_audit_logs`. While impersonating, the UI is
banner-marked, write operations are blocked unless explicitly allowed, and the
session cannot be used to change credentials.

## 3. Authorisation

### 3.1 Permission points

A permission point is a stable string `<module>:<resource>:<action>`, declared
once as a Go constant so that a typo is a compile error rather than a silent
grant:

```go
// internal/permission/codes.go
package permission

const (
    SystemMenuList   = "rbac:menu:list"
    SystemMenuCreate = "rbac:menu:create"
    SystemMenuUpdate = "rbac:menu:update"
    SystemMenuDelete = "rbac:menu:delete"

    IdentityAdminList   = "identity:admin:list"
    IdentityAdminUpdate = "identity:admin:update"
    // ...
)
```

A seed migration mirrors these constants into the `permissions` collection. A
test asserts the two are in sync in both directions: a constant without a row,
or a row without a constant, fails the build.

### 3.2 Effective permission set

On a successful sign-in the middleware resolves and caches, keyed by
administrator id:

```
effective = union(permissions of every active role held by the admin)
```

Cached in the process-local store with the PocketBase record `updated`
timestamp as part of the key. A change to a role, or to the administrator's role
assignment, invalidates the affected entries (FR-rbac-11).

API-key requests compute the same set for the key owner, then intersect it with
the key's `scopes` (FR-developer-8).

### 3.3 Route enforcement

```go
// internal/middleware/rbac.go
func RequirePermission(set *permission.Checker, code string) gin.HandlerFunc {
    return func(c *gin.Context) {
        actor := CurrentActor(c)
        if actor == nil {
            c.AbortWithStatus(http.StatusUnauthorized)
            return
        }
        if !set.Has(actor, code) {
            audit.SecurityEvent(c, audit.EventPermissionDenied, slog.LevelWarn,
                "required", code, "actor", actor.ID)
            c.AbortWithStatus(http.StatusForbidden)
            return
        }
        c.Next()
    }
}
```

The default is deny. A route with no permission requirement is reachable by any
authenticated administrator, which is only acceptable for the workbench and the
signed-in user's own profile; both are annotated as such in code review.

### 3.4 Menu projection

Navigation is derived, never authored per role:

```
visibleMenus(actor, platform) =
    { m ∈ menus : m.status = active
                ∧ m.platform = platform
                ∧ m.type ≠ button
                ∧ set.Has(actor, m.permissionCode) }
    ordered by (parent, sortOrder)
```

A node whose parent is not visible is not rendered, even if the node itself is
granted, so a granted child never appears orphaned.

`roles.menus` narrows the result further when a role explicitly lists menus. The
effective set is `visibleMenus ∩ grantedMenus`. Both inputs are needed: the
permission code expresses capability, the menu grant expresses intent for
navigation.

### 3.5 Data scope

Functional permission answers "may this actor call this operation". Data scope
answers "over which rows".

| `dataScope` | Predicate applied to list and detail queries |
| --- | --- |
| `all` | none |
| `department` | `department = actor.department` |
| `department_and_below` | `department.path ~ actor.department.path` |
| `self` | `createdBy = actor.id` (or the collection's owner field) |
| `custom` | `department ∈ role.customDepartments` (plus descendants) |

Scope is applied by the repository layer through a shared helper so it cannot be
forgotten per query:

```go
q := app.RecordQuery("employees")
scoper.Apply(q, actor, "department")   // adds the predicate, or nothing for "all"
```

A module that exposes a scoped resource must call `Apply`; a repository test
asserts that every scoped query builder includes it.

## 4. Audit pipeline

Audit is emitted by middleware, not by handlers, so a new handler cannot forget
it.

```
request
  → requestid middleware        (generates requestId, sets header)
  → session/auth middleware     (resolves actor)
  → [handler executes]
  → audit middleware (deferred)
        ├─ skip if method is safe AND route is marked non-auditable
        ├─ redact(params)
        ├─ write operation_logs
        └─ if status >= 500, write exception_logs (stack captured by recovery)
```

### 4.1 Redaction

Redaction is a deny-by-key-name list plus an allow-list for known-safe fields,
applied recursively to the captured parameter map:

- Denied keys: `password`, `newPassword`, `confirmPassword`, `token`,
  `accessToken`, `refreshToken`, `secret`, `clientSecret`, `apiKey`, `key`,
  `credential`, `privateKey`, `otp`, `code`.
- Denied values: anything matching an API-key prefix pattern, and any string
  longer than 512 bytes inside a JSON field (truncated with a marker).
- Everything else is stored. A unit test feeds a request containing each denied
  key name and asserts the stored payload contains the redaction marker and not
  the value.

### 4.2 Separation from PocketBase logs

`operation_logs` answers "what did a person do in the product". PocketBase's own
`_logs` collection answers "what SQL and HTTP happened". Both are kept.
`requestId` ties them together, and the audit detail page links to the matching
PocketBase log rows.

## 5. Settings

A typed registry in code is the source of truth; the `settings` collection is
storage.

```go
// internal/settings/registry.go
var Registry = []Spec{
    {Category: CategorySite, Key: "siteName", Type: TypeString, Default: "PocketAdmin",
     Validate: validators.NonEmpty, Public: true,
     Description: "Name shown in the sidebar, page titles and emails."},
    {Category: CategoryAuth, Key: "maxLoginAttempts", Type: TypeNumber, Default: 5,
     Validate: validators.IntRange(1, 100)},
    // ...
}
```

- Reading a setting goes through a cached accessor; values are loaded once and
  invalidated on write.
- Writing validates against the registry, persists, emits a
  `security_audit_logs` entry containing the old and new value, and invalidates
  the cache.
- A row with no registry entry is ignored rather than trusted, so an older
  binary tolerates a newer schema.
- `Public: true` entries are the only ones exposed by the unauthenticated
  `/api/public/settings` endpoint.

Feature flags are settings, not build-time switches, which is what makes
FR-settings-6 testable without a rebuild.

## 6. Jobs

PocketBase already provides an in-process cron (`app.Cron()`). The design reuses
it rather than adding a scheduler dependency.

```go
// internal/jobs/registry.go
type Handler func(ctx context.Context, params map[string]any) (string, error)

var registry = map[string]Handler{
    "audit.retention":       audit.RetentionJob,
    "identity.session.clean": identity.SessionCleanupJob,
    "identity.membership.expire": identity.MembershipExpiryJob,
    "announcement.publish":  announcement.PublishJob,
    "announcement.expire":   announcement.ExpireJob,
    "storage.temp.clean":    storage.TempCleanupJob,
    "monitoring.snapshot":   monitoring.SnapshotJob,
    "workbench.todo.remind": workbench.TodoReminderJob,
}
```

- `scheduled_jobs.code` must exist in the registry. A row naming an unknown code
  is disabled at startup and flagged in the UI rather than silently skipped.
- On boot, every `enabled` row is registered with `app.Cron()`; enabling,
  disabling or editing a job at runtime removes and re-adds the entry.
- Concurrency policy is enforced with a per-job in-process mutex plus a check
  for a `running` row, which also guards against a slow previous run
  (FR-jobs-9).
- Every run writes a `job_runs` row before the handler starts (status
  `running`) and updates it on completion, so a crashed process leaves visible
  evidence.
- A `forbid` job that is still `running` past its timeout is marked `timeout`
  and its context is cancelled.

**Assumption.** Single process, therefore no distributed lock is needed. If the
deployment ever runs two instances, this module requires a database-backed
lease before it is safe.

## 7. Integrations

Driver interfaces live in `internal/drivers`, implementations beside them. The
`integrations` service resolves a provider by channel to a constructed driver.

```go
type Mailer interface {
    Send(ctx context.Context, msg Message) error
    Test(ctx context.Context) error
}

type SMSProvider interface {
    Send(ctx context.Context, to, templateID string, params map[string]string) error
    Test(ctx context.Context) error
}

type StorageProvider interface {
    Put(ctx context.Context, key string, r io.Reader, size int64, contentType string) error
    Delete(ctx context.Context, key string) error
    URL(ctx context.Context, key string, ttl time.Duration) (string, error)
    Test(ctx context.Context) error
}

type IdentityProvider interface {
    AuthorizeURL(state, redirectURI string) string
    Exchange(ctx context.Context, code, redirectURI string) (ExternalIdentity, error)
    Test(ctx context.Context) error
}
```

`ExternalIdentity` carries the provider subject plus the attribute mapping
configured on the provider, so adding a provider does not require a new column.

Secrets are encrypted with a key derived from the PocketBase `--encryptionEnv`
value, falling back to the generated session key file. The edit form renders a
fixed mask; submitting the mask unchanged leaves the stored secret intact
(FR-integrations-7).

Every driver call is wrapped by a helper that writes `integration_logs` and
enforces the timeout, so no driver implementation contains logging code.

## 8. Files

- Uploads are validated by sniffing the first 512 bytes for a MIME type and
  comparing against the allow-list, never by trusting the extension
  (FR-storage-5).
- Size limits are enforced by `http.MaxBytesReader` before the body is parsed, so
  an oversized upload cannot exhaust memory.
- Checksum-based deduplication happens inside the folder: an identical checksum
  reuses the existing stored object and increments a reference count.
- Deleting a file is refused while `refCount > 0` (FR-storage-7); the UI offers
  "show references" instead.
- The active storage driver comes from settings; changing it does not migrate
  existing objects, and the UI states which driver each file lives on.

## 9. Developer surface

- The OpenAPI document is generated from handler annotations with `swaggo/swag`
  and committed. CI regenerates it and fails on a diff, which is the drift test
  required by FR-developer-3.
- Swagger UI is served from embedded assets at `/developer/api-docs`, gated by
  `developer:apidoc:view`.
- API keys authenticate with `Authorization: Bearer pk_live_…`. The middleware
  hashes the presented key, looks it up by prefix then hash, checks status,
  expiry, IP allow-list and rate limit, then attaches the scoped actor.
- Key creation returns the plaintext exactly once. The response is rendered with
  caching disabled and is not written to `operation_logs` parameters (the key
  value is redacted by the `key` deny-rule).

## 10. Observability and error handling

- Every request carries a `requestId`, returned in the response header and
  attached to every log line for that request.
- Panics are recovered by `gin.Recovery()`, converted to a 500, and written to
  `exception_logs` with the stack and `requestId`.
- The `monitoring` health endpoint reports application, database and scheduler
  liveness separately so an orchestrator can distinguish "process up, database
  gone" from "process gone".
- Log levels follow the existing convention: 5xx `error`, 4xx `warn`, otherwise
  `info` (see the global logging preference).

## 11. Code style

The existing conventions are extended, not replaced.

```go
// ListDepartments returns a page of departments, applying the actor's data scope.
//
// Returns an empty page rather than an error when nothing matches.
func (s *Service) ListDepartments(ctx context.Context, actor *identity.Actor, in ListInput) (*Page[Department], error) {
	q := s.repo.Query()
	s.scoper.Apply(q, actor, "department")

	if in.Keyword != "" {
		q.AndWhere(dbx.Like("name", in.Keyword))
	}

	items, total, err := s.repo.Page(ctx, q, in.Page, in.PerPage)
	if err != nil {
		return nil, fmt.Errorf("list departments: %w", err)
	}

	return &Page[Department]{Items: items, Total: total}, nil
}
```

Conventions:

- Errors are wrapped with `%w` and a lower-case verb phrase; no `panic` outside
  `main`.
- Exported identifiers carry doc comments starting with the identifier name.
- No naked returns, no `init()` for wiring.
- Table-driven tests with `t.Run` subtests.
- All comments and identifiers in English.

## 12. Security design

### 12.1 Threats and mitigations

| Threat | Mitigation |
| --- | --- |
| Privilege escalation via a forgotten check | Permission requirement is part of route registration; a startup self-check fails on an unsafe route without one (§ 12.4). |
| Session theft | HttpOnly, SameSite=Lax, `Secure` when configured; signed cookie plus revocable database row; `lastSeenAt` sliding expiry. |
| CSRF | Existing double-submit token on all panel writes (already implemented). |
| Password brute force | Attempt counter with lockout (FR-identity-4), optional captcha, all attempts recorded in `login_logs`. |
| Secret leakage into logs | Deny-list redaction with a test per denied key (§ 4.1); provider secrets never serialised. |
| API key leakage | Only a hash is stored; plaintext shown once; prefix-only logging. |
| Malicious upload | MIME sniffing, extension allow-list, size cap, optional quarantine status. |
| SQL/filter injection | PocketBase filter expressions use bound parameters (`{:param}`); no string concatenation of user input into filters. |
| Audit tampering | Append-only through the application; no update or delete route exists; retention is the only deletion path. |
| Enumeration | Sign-in responds identically for unknown accounts and wrong passwords, with equalised timing (already implemented). |

### 12.2 Authorisation is not the collection rules

Restated because it is the most likely place to introduce a hole: because
services call `core.App` directly, PocketBase never evaluates collection rules
for panel traffic. The collection rules in `database-design.md` § 4 exist to
protect the *public* `/api` surface. Any code that serves panel data must pass
through the permission middleware.

There are therefore two enforcement points and one vocabulary: both the panel's
permission checks and the collection rules name the same `permissions.code`
values, so this is one model checked at two doors rather than two models that can
drift apart. Rules govern callers arriving from outside; the middleware governs
the panel.

The boundary is enforced by tests rather than by review. `internal/rbac` holds a
drift test between the permission constants and the `permissions` collection, and
`internal/rbac/apirules_test.go` asserts that no collection is half-open and
validates the rules of any that is opened. What neither covers is a handler that
reaches for `core.App` without a permission guard: the startup self-check in
§ 12.4 is what catches that, and only for state-changing routes.

### 12.3 Bootstrap and recovery

- The seeded bootstrap administrator is `isProtected`; disabling or deleting it
  is refused.
- If RBAC data is corrupted, `/_/` remains reachable with `_superusers`, which
  bypasses the panel entirely. The recovery procedure is documented in the
  operator README.
- Deleting the last role holding `rbac:role:update` is refused.

### 12.4 Startup self-check

On boot the router is walked; any route whose method is unsafe (POST, PUT,
PATCH, DELETE) and whose handler chain lacks a permission requirement aborts
startup with the offending route listed. This turns "someone forgot the check"
from a production vulnerability into a failed deploy.

## 13. Performance

| Concern | Approach |
| --- | --- |
| Permission lookup per request | Process-local cache keyed by administrator id and record `updated` timestamp, invalidated on write. |
| Menu projection per request | Cached alongside permissions; the menu tree is small and changes rarely. |
| Large log tables | Indexed on `(actor, created)` and `(created)`; retention jobs prune; list views default to a bounded time range. |
| File listing | Server-side pagination; checksum and folder indexed. |
| Dashboard aggregates | Counts cached for 30 seconds; monitoring charts read snapshots rather than scanning logs when `metric_snapshots` is enabled. |
| Write amplification on session touch | `lastSeenAt` updated at most once per minute per session. |

Targets: p95 < 300 ms for list pages at 10,000 rows per collection; p95 < 800 ms
for the monitoring panel.

## 14. Testing strategy

| Level | Scope | Tooling |
| --- | --- | --- |
| Unit | Services with a fake repository; redaction; permission set algebra; data-scope predicates; settings validation; cron expression parsing | `go test`, table-driven |
| Integration | Repositories and services against `tests.TestApp(t)` (PocketBase's own test harness), real SQLite and real migrations | `go test` |
| Route/authz | A test walks every registered route and asserts that an unauthenticated request is rejected and that a request without the required permission returns 403 | `go test` |
| Drift | Permission constants ↔ `permissions` rows; registered routes ↔ OpenAPI operations | `go test` |
| End-to-end | Build the binary, run it, drive it over HTTP: sign in, onboard an administrator, verify menu filtering and that the action appears in `operation_logs` | shell + `curl`, as used for the existing panel |

The end-to-end suite is the acceptance mechanism for § 6 of `requirements.md`.

## 15. Non-functional requirements

| Area | Requirement |
| --- | --- |
| Language | All code, comments, logs, errors and documentation in English. |
| Logging | `log/slog` JSON to stdout, one object per line; levels by status code. |
| Configuration | YAML with environment and flag overrides; unknown keys rejected. |
| Accessibility | Keyboard reachable navigation and forms; visible focus; labels bound to inputs; colour contrast ≥ 4.5:1 for text. |
| Internationalisation | UI strings externalised; `language` preference honoured. No hard-coded user-facing text in handlers. |
| Backups | PocketBase's built-in backup covers the database; uploaded files are backed up by the operator and the requirement is documented. |
| Upgrade path | Additive migrations; destructive changes require a two-release window (§ 6 of `database-design.md`). |
| Browser support | Current Chrome, Firefox, Safari and Edge. No IE. |

## 16. Delivery phasing

Milestones map to the waves in `CAPABILITY-MAP.md`. Each milestone is
independently demonstrable and testable.

| Milestone | Modules | Demonstrable outcome |
| --- | --- | --- |
| M1 Foundation | `identity`, `rbac` | An account with an admin profile signs in; roles and permission points exist; the sidebar renders from the menu tree filtered by permissions; the employee and admin profiles attach to one account. |
| M2 Organisation and audit | `organization`, `audit`, `storage` | Departments, positions and employee profiles are managed; every write is attributable; files can be uploaded and referenced. |
| M3 Configuration | `settings-admin`, `settings-product` | Back-office security policy and product configuration are editable on separate pages, each enforced and each audited separately. |
| M4 Surfaces | `announcement`, `workbench`, `jobs`, `developer` | Announcements publish on schedule; the workbench shows todos, messages, recent visits and quick links; jobs run and report; the OpenAPI document and API keys work. |
| M5 Operations | `integrations`, `monitoring` | Mail, SMS, storage, identity and payment providers are configurable and testable; monitoring and system information render. |
| M6 Catalogue | `catalog` | Products with variants, pricing and a stock ledger; categories; media through `storage`. |
| M7 Entitlements | `membership`, `promotions`, `subscriptions` | Membership tiers and plans; coupons with a deterministic discount evaluator; subscription plans and cycles. |
| M8 Orders | `orders` | An order can be placed, priced, discounted and driven through its state machine with a full timeline. |
| M9 Money in | `payments` | A payment intent is collected through a configured provider; callbacks are verified, idempotent and reconcilable. |
| M10 Money out | `refunds` | A partial refund is requested, approved, executed and reflected in the order's amounts. |

Two useful cut lines exist. After **M5** the platform is a complete
access-controlled admin system. After **M8** the business domain can take orders
even if settlement is handled offline via FR-payments-6.


## 17. Risks

| Risk | Impact | Mitigation |
| --- | --- | --- |
| Collection count (38) makes migrations unwieldy | Slow delivery, merge conflicts | One migration per module; module packages own their schema. |
| Permission drift between code and data | Silent over- or under-grant | Bidirectional drift test (§ 3.1). |
| Menus and permissions modelled as two sources of truth | Confusing behaviour | Menus are a projection; the permission code is authoritative (§ 3.4). |
| Audit volume grows without bound | Slow queries, disk growth | Retention jobs from day one, not retrofitted. |
| In-process scheduler | Jobs lost on crash, unsafe with two instances | Every run persisted; documented single-instance assumption. |
| Auto-update of a running binary | Corrupted deployment | Assisted update only, with recorded versions (FR-monitoring-8, -9). |
| Scope creep from "membership" | Unbounded orders/payments work | OQ-1 gates it; the current design does not presuppose it. |
| Data scope semantics misunderstood | Users see rows they should not | Scope applied by a shared repository helper with a test per scoped resource. |
| Non-idempotent payment callback double-credits an order | Financial loss, reconciliation break | Unique `providerTxnId`; the callback is applied only on the transition into `succeeded`; `callbackCount` records replays. See `business-design.md` § 4. |
| Refund exceeds the amount paid | Financial loss | The cap is checked at approval time inside a transaction, not at request time. |
| Coupon evaluation diverges between preview and order | Customer disputes, wrong totals | One evaluator, called by both paths; the applied result is snapshotted onto the order so it can be replayed. |
| Subscription dunning loops forever | Silent revenue loss, provider rate limits | Bounded retry budget; `past_due` then `cancelled`; every attempt is a `subscription_cycles` row. |
| Stock drifts from reality | Overselling | Every quantity change is a `stock_ledger` row; stock is never written directly. |
| Money stored as floating point | Rounding errors compounding | Integer minor units everywhere, asserted by a schema test. |
| Business tables hard-deleted | Audit and accounting gaps | Money-bearing collections are reversed by compensating records, never deleted. |

## 18. Open questions

Tracked in `requirements.md` § 5. The four that most affect this design are
OQ-2 (which settings surface is which), OQ-3 (whether row-level data scope is
wanted at all), OQ-7 (generated vs hand-written OpenAPI) and OQ-9 (whether
physical-goods fulfilment is in scope).
