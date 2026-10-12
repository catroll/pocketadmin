# Decision Log

Decisions taken while building the platform, recorded **at the point of
decision** so a reviewer does not have to reconstruct them from the diff.

Each entry states what was decided, why, and what was given up. Entries are
appended, never rewritten: a decision that is later reversed gets a new entry
that supersedes the old one, because the reasoning that was valid at the time is
still worth reading.

This file is for a human reviewer. It is not a specification — the specification
set is in `docs/README.md`.

---

## How this build is organised

### D-001 — One commit per module, verified against a live instance

**Decision.** Each module lands as its own commit, only after being driven
end-to-end against a running server: migrations applied to an empty data
directory, screens exercised over HTTP, and the server log checked for errors and
panics.

**Why.** The recurring failure in this codebase is not a compile error. It is a
feature that compiles, renders, returns 200, and is wrong — a truncated page, a
revocation that only marks a record, an audit write rejected by an enum. Those
are only visible when the thing is actually driven.

**Given up.** Speed. A module that "looks done" waits for a live run before it is
committed.

### D-002 — Modules are implemented one at a time, in the documented wave order

**Decision.** Follow `CAPABILITY-MAP.md` § Build order. Wave 2 remainder
(`storage`, `audit`), then 3, 4, 5, then the business domain 6 → 10.

**Why.** The wave order is a dependency order. `payments` needs `integrations`,
which needs `settings-product`; `orders` needs `catalog`, `promotions` and
`membership`. Building out of order means either stubbing a dependency or
rewriting the caller.

**Given up.** Any temptation to build the visible business screens first, which
would be more impressive earlier and more expensive later.

### D-003 — Work is delegated to fresh-context agents against a written brief

**Decision.** Each module is implemented by a separate agent that starts with no
memory of this session, working from `docs/module-brief.md` plus the module's
section in `docs/features.md`. The coordinating agent reviews the result, drives
it live, and commits.

**Why.** The established conventions (the resource layer, permission constants,
migration helpers, the guarded-route helpers) are worth more than any single
module, and an agent working from a written brief applies them consistently. It
also keeps the coordinating context from filling with implementation detail
across seventeen modules.

**Given up.** The coordinator's familiarity with each module's internals. This is
compensated by reviewing the diff and by the live verification in D-001.

---

## Foundation (already built)

### D-004 — PocketBase embedded as a library, one port, one binary

**Decision.** PocketBase is imported as a Go library, not run as a separate
process. `http.NewServeMux` routes `/api`, `/api/`, `/_`, `/_/` to PocketBase and
everything else to Gin. One process, one port, one binary.

**Why.** A separate process would need its own deployment, its own health check
and a network hop on every panel request, in exchange for nothing the embedded
form does not provide.

**Given up.** Upgrading PocketBase independently of the panel. The version is
pinned in `go.mod` and injected into the binary at build time.

### D-005 — Two enforcement points, one permission vocabulary

**Decision.** The panel authorises through `internal/rbac`; callers arriving
through the public REST API are authorised by PocketBase collection rules.
Both name the same `permissions.code` values. Every platform collection is
created with all five rules `null`.

**Why.** The panel reads through `core.App`, so PocketBase never evaluates
collection rules for panel traffic — the rules are structurally unable to govern
it. Keeping one vocabulary means this is one model checked at two doors rather
than two models that drift.

**Given up.** The option of a single mechanism. Making rules govern the panel
would require the panel to call its own HTTP API, which costs per-session
revocation (PocketBase tokens are stateless), multi-step transactions, and a
network hop per query. Documented in `design.md` § 12.2 and
`database-design.md` § 5; enforced by `internal/rbac/apirules_test.go`.

### D-006 — Permission points are compile-time constants

**Decision.** Every permission point is a Go constant in
`internal/permission/codes.go`, mirrored into the `permissions` collection by a
seed migration. A drift test asserts both directions.

**Why.** A typo in a permission string would otherwise produce a check that can
never be satisfied — a silent lockout rather than an error. As a constant it does
not compile.

**Given up.** Being able to add a permission point without a code change and a
deploy. That is the correct trade: a permission point is part of the
authorisation contract, not user data.

### D-007 — Derived data is maintained by model hooks, not handlers

**Decision.** The department materialised path and the line-manager cycle guard
are bound as PocketBase model hooks in `main`, not as resource hooks in the web
layer.

**Why.** They were originally resource hooks, and departments created through the
REST API ended up with an empty path, which silently disabled the "and below"
data scope. Derived data has to be derived on every write path.

**Given up.** Nothing. The web layer keeps only what is genuinely a UI concern.

### D-008 — Revocation is server-side and immediate

**Decision.** Panel sessions are rows in a `sessions` collection holding only the
SHA-256 of the token. `requireAuth` validates that row on every request.

**Why.** The first implementation marked rows revoked and checked nothing, so
revoke-one, revoke-all, a ban and a password reset were all cosmetic until the
holder happened to sign out. PocketBase's own tokens are stateless JWTs with no
per-session kill switch, which is exactly why this exists.

**Given up.** The simplicity of trusting a signed token.

### D-009 — Fail loudly on the paths that fail silently

**Decision.** Specific hardening, each added after a real defect:

- Page rendering buffers before writing, so a template error becomes a 500 rather
  than a truncated 200.
- A failed audit or sign-in-history write logs at `error` and names the action.
- The startup self-check aborts the build on a state-changing route without a
  permission guard.

**Why.** Each of these replaced a failure mode that produced no error. A
half-drawn page, a silently dropped audit entry and an unguarded route all look
like success from the outside.

**Given up.** Some noisy logs on genuinely invalid input.

### D-010 — Test-only packages and helpers live beside their subject

**Decision.** Tests boot an isolated application with the platform's migrations
applied, and register the same model hooks production does.

**Why.** A test that asserts behaviour production does not have is worse than no
test. The `organization` hooks are registered in the test bootstrap for exactly
this reason.

**Given up.** Faster test startup.

---

## Module decisions

### D-011 — The audit collection is renamed and reshaped to match the schema design

**Decision.** `audit_logs` becomes `security_audit_logs` with the shape in
`database-design.md` § 3.9: a closed `event` enum, `severity`
(`info`/`warning`/`critical`), `actorType` (`user`/`system`/`api_key`), a
`requestId`, and `targetType`/`targetId` in place of `resource`/`resourceId`.

**Why.** `audit_logs` was created opportunistically in the identity module because
impersonation needed somewhere to write, and its shape was invented rather than
designed: a free-text `action`, a three-level `riskLevel`, and no `requestId`, so
the spec's requirement to correlate with PocketBase's own `_logs` by request id
could not be met.

The closed `event` enum is the spec's choice and it is the right one for an audit
log: the set of auditable events should be knowable and reviewable, not whatever
a caller happens to write. The known hazard is that an enum rejects an unlisted
value — which is exactly how the account-lifecycle audit trail was silently lost
in an earlier round — so the event names become **Go constants with a drift test**,
the same treatment as permission points, and the write path must log failures at
`error` level rather than warn.

**Given up.** The flexibility of free-text actions. Adding an event is now a code
change, which is correct for an audit catalogue but does mean the enum has to be
extended deliberately.

### D-012 — Forward dependencies are satisfied by an interface now and a caller later

**Decision.** Where a module in an earlier wave needs configuration or scheduling
that a later wave owns, the earlier module defines the type or function and uses
a built-in default; the later module supplies the real source.

Two instances so far:

- `storage` needs upload policy (allowed extensions, maximum size, image
  dimension limits), owned by `settings` (wave 3). Storage reads a `Policy` from
  `config.Config` with sensible defaults.
- `audit` needs retention pruning, which is a scheduled task owned by `jobs`
  (wave 4). Audit exposes the pruning function; `jobs` schedules it.

**Why.** The alternative is either to stub the later module, which produces two
implementations to reconcile, or to reorder the build, which breaks the dependency
order that the waves encode. Defining the seam now and filling it later keeps each
module complete and testable on its own.

**Given up.** A module is briefly "complete except that one value comes from
elsewhere". This is recorded here so it is not mistaken for an oversight.

---

## Pending

Decisions for the remaining modules are appended below as they are taken.
