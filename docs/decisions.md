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

### D-013 — One tree implementation, not one per hierarchy

**Decision.** The materialised-path logic lives in `internal/tree`. `departments`
(from the organisation module) and `file_folders` (from storage) are both
instances of it.

**Why.** The folder tree needed exactly what the department tree already had:
the path embeds the record's own id, which PocketBase assigns during the save, so
it cannot be computed before one; a move rewrites the whole subtree in one
transaction or the prefix predicate that row-level scoping reads is left
half-applied; and a node must not move beneath its own descendant.

The second copy is where the cycle check gets forgotten — and forgetting it is
silent until someone builds a loop. The department tree's own history is the
evidence: its logic was originally in a handler, and a department created through
the REST API ended up with an empty path.

**Given up.** Touching working, verified code (the department tree) to make the
change. The regression was caught by the existing tests for department path
derivation and cache invalidation, which is precisely why they exist.

### D-014 — Storage keys are content-addressed, and deletion checks for sharers

**Decision.** A stored object's key is derived from the SHA-256 of its content.
Two uploads of identical bytes address the same object. Deleting a file row
removes the object only if no other row still addresses the same key.

**Why.** Deduplication then costs nothing to implement — the second upload
computes the same key and finds the object already present. The hazard is the
other side of it: two rows sharing one object means deleting one row can destroy
the other's bytes. Checking for remaining sharers after the row is gone is the
only moment the question has a clean answer.

**Given up.** Changing a file's content in place, which content addressing makes
impossible by construction. That is correct for a library: a file is immutable,
and a new version is a new file.

### D-015 — The storage driver refuses unclean keys rather than sanitising them

**Decision.** `LocalDriver` rejects any key that is not already a clean relative
path, instead of normalising it and continuing.

**Why.** Found by the test written for it. The first implementation cleaned keys,
so `../escaped.txt` became `escaped.txt` — contained, and therefore not a
traversal, but it silently addressed a different object than the caller named.
Keys here are generated from a content hash, so a valid key never needs
normalising, which makes rejection both stricter and safer than repair. Aliasing
of that kind is a correctness bug even when it is not an escape.

**Given up.** Tolerance of caller-supplied keys. There are none.

### D-016 — The sign-in history moves into the audit namespace

**Decision.** `identity:loginlog:list` is retired; the sign-in history is gated by
`audit:login:list`. The standalone list screen moves from `/identity/login-logs`
to `/audit/logins`. Identity keeps its per-account history view, which is a
different screen over the same rows.

**Why.** `database-design.md` § 3.9 places `login_logs` in the audit module, and
the module that owns a dataset should own the permission over it. Two permissions
over one dataset is the confusion this avoids: a role could see the list and not
the export, or vice versa, for no reason anyone could explain.

The migration backfills the new permission onto roles that held the old one, so
existing roles do not silently lose access to a screen they could see a moment
earlier. That is the kind of change that gets noticed in production rather than in
review.

**Given up.** A permission rename that touches an already-verified module.

### D-017 — Operation logging is middleware, not a call in each handler

**Decision.** Every state-changing panel request is recorded by one middleware.
Reads are not recorded.

**Why.** A handler that forgets to log is a gap nobody notices, and the value of
an operation log is that it is complete. Reads change nothing, and recording
every page view would bury the entries that matter.

Two consequences are handled rather than discovered later. The middleware runs
first in the chain, because the CSRF middleware parses the form and consumes the
body -- the first version ran after it and recorded no parameters at all. And
because it reads the body to redact it, it reads a bounded prefix and restores
the stream, so logging cannot become a way to exhaust memory or break a handler.

**Given up.** Per-handler control over what is recorded for a specific route. If
a route needs a richer entry, it writes a security audit event in addition.

### D-018 — The redactor parses a request body by its encoding

**Decision.** A urlencoded body is decoded into fields before redaction; a
multipart body's non-file fields are collected after the handler has parsed it and
its files are recorded by name only; JSON is walked directly.

**Why.** Found by driving it. The first implementation stored a urlencoded body as
one string, which put the field names out of the redactor's reach -- and a
urlencoded body is the encoding a browser form actually uses, so the secrets most
likely to appear were exactly the ones it missed. `apiKey=sk-live-xyz` reached the
log in the clear.

The general lesson is that a redactor is only as good as its understanding of the
shapes a body arrives in, and the shape it does not understand is the one that
leaks. Each encoding now has a test.

**Given up.** Nothing. A JSON body is still stored as text when it is not
form-encoded.

### D-019 — Two settings categories are added beyond the documented key list

**Decision.** The admin surface gains a `storage` category (upload size,
allow-list, image dimension bounds) and an `audit` category (the four retention
periods). Neither appears in the seeded key table in `database-design.md` § 3.8.

**Why.** Both are gaps in the specification rather than decisions against it.
`features.md` gives the file library an upload policy and the audit module
retention periods with per-type settings, and a policy has to be configured
somewhere. The alternative was to leave both in `config.Config`, which would mean
an operator editing a file and restarting the process to change a size limit --
exactly the arrangement the settings module exists to replace.

The extension is recorded here rather than edited into the specification, because
the specification is the thing being implemented against and quietly widening it
is how a specification stops describing the system.

**Given up.** The documented key list is no longer exhaustive. It should be
updated when the specification is next revised.

### D-020 — The two settings surfaces share a table and an engine, not a permission

**Decision.** One `settings` table, one validation engine, two registries and two
permissions. The back office and the storefront are separate pages, separate
audit categories and separately granted.

**Why.** They are separate concerns that happen to store the same shape of thing,
which is the same relationship orders and refunds have. Configuring the back
office is not the same authority as configuring the storefront, and an operator
may reasonably hold one and not the other; a single permission would force them
together.

The isolation -- "neither surface can write the other's keys" -- is a property of
the types rather than a check: a write walks a `Registry`, and a `Registry`
contains only its own surface's declarations, so a key belonging to the other
surface cannot be named. A crafted form that includes one is not rejected, it is
never looked at. Verified by posting a product key to the admin surface and
confirming the product value is untouched.

**Given up.** A single settings screen. Two pages is more to navigate and is the
correct amount for two audiences.

### D-021 — The cron expression parser is this repository's own

**Decision.** Five-field cron parsing and next-occurrence computation are
implemented in `internal/jobs/schedule.go` rather than taken from a dependency.

**Why.** The two candidates were both unsatisfactory, and the reasoning is worth
recording because the default is supposed to be "take the newest maintained
option". I checked the release channels rather than recalling them:

| Candidate | Latest | Released |
| --- | --- | --- |
| `github.com/robfig/cron/v3` | v3.0.1 | 2020-01-04 |
| `github.com/netresearch/go-cron` | v0.16.1 | 2026-09-22 |

The original has not been released in over six years. The maintained option is a
fork, and it is pre-1.0, so it makes no API-stability promise -- which for a
scheduler the platform will depend on for years is a real cost rather than a
theoretical one. Neither is clearly the right answer.

Against that: the problem is bounded and its semantics are fixed by decades of
convention. Fifty lines of parsing and a hundred of date arithmetic, with tests.
The alternates are "a six-year-old release" and "a pre-1.0 fork", and a
dependency that cannot be vouched for is worse than one that is understood.

**Given up.** Correctness rests on this repository's tests rather than on a
library's user base. The tests cover the cases that actually break hand-written
evaluators: the day-of-month and day-of-week union rule, the leap-day gap, a date
that does not exist in any year, and a daily schedule firing at the local hour.

If the fork reaches 1.0, re-evaluate.

### D-022 — A closed audit enum has to be widened at startup, not in a migration

**Decision.** `audit.EnsureEventEnum` runs at startup and appends any catalogue
event the stored `security_audit_logs.event` select is missing.

**Why.** Found by narrowing a stored enum and restarting, which is what an upgrade
looks like. The migration that creates the collection builds the enum from the
event catalogue, so a fresh database is always correct -- but migrations do not run
again, so an upgraded one keeps the enum it was created with and a new event is
rejected at write time. The action has already happened and its record does not
exist.

The very same defect had already been found and fixed for the permission action
enum, which makes the general rule worth stating: **a closed select built from a
compile-time catalogue needs a self-heal at startup, because the catalogue and the
stored schema advance independently.** Only additions are made; removing a value
would invalidate rows already stored under it.

**Given up.** A startup step that writes to the schema. It is a no-op once the two
agree, and it logs what it added when they do not.

### D-023 — Rich text is sanitised by a dependency, not by hand

**Decision.** `github.com/microcosm-cc/bluemonday` v1.0.27 sanitises announcement
bodies, with an allow-list policy. It is the first dependency this project has
added, and it brings two transitive packages.

**Why.** PocketBase's editor field stores HTML **verbatim**. That was verified
rather than assumed: every common payload was written through the field and read
back, and `<script>`, `onerror=`, `javascript:` URLs, `<iframe>`, `<svg/onload>`
and `<style>` all survived unchanged. An announcement's audience can be every user
of the product, so unsanitised storage means whoever can write one can attack
everyone who reads it.

This is the case where "prefer fewer dependencies" does not apply. HTML
sanitisation is not a bounded problem: a deny-list has to anticipate every way a
browser can be made to execute something, and the browser knows more tricks than
the list does. A hand-written sanitiser is a well-known way to ship a
vulnerability while believing you have prevented one.

The dependency was checked rather than recalled: v1.0.27, released 2024-07-04. Two
years is old for a security library, and the alternatives are less established. It
is on the stable v1 line, its policy model is configured rather than inherited, its
job is bounded, and the reasoning is recorded here so the choice can be revisited
if a better-maintained option appears.

The sanitising runs in a **model hook**, so every write passes through it -- a
screen, a seed, an import and the API. A sanitiser installed in a handler protects
that handler and nothing else. It is tested against 26 payloads and against
ordinary markup that must survive.

**Given up.** Three dependencies where there were none, and an allow-list that will
occasionally reject markup someone wanted.

### D-024 — An announcement's visibility is decided by whether you can author, not by whether you are an administrator

**Decision.** An account that holds the announcement update permission sees every
published announcement. Everyone else sees the ones their audience matches.

**Why.** Two earlier attempts were wrong in the same way, and the second is the
instructive one. The first decided "administrator" by collection name, which is
true for every account because there is one account collection. The second decided
it by the presence of an `admins` record -- but signing into this panel **requires**
an `admins` record, so that is also true for every account that can reach the page.
Both versions showed everyone everything and made the audience field do nothing.

The question that actually separates the two groups is whether the reader is
responsible for the announcements. That is the update permission, which the RBAC
layer already answers.

**Given up.** An author cannot preview what a targeted reader would see, because
they see everything. A future "preview as" would restore that, and is not needed
yet.

### D-025 — A personal record is scoped by ownership, not by a permission

**Decision.** A todo and a message delivery belong to one account. The workbench
routes carry ordinary permission guards for whether the screen is reachable, and
the handlers separately resolve every row through the signed-in account. There is
no permission that grants access to another account's rows.

**Why.** The two questions look like one and are not. "May this person use the
todo screen" is a grant an administrator can hand out. "Whose todo is this" is a
fact about the row and cannot be delegated to anybody. Expressing both as
permissions would create a grant that lets one administrator read another's
private list, which is a thing that should not be expressible.

The distinction is enforced in the handlers rather than in the guard, and the
routes say so. A guard that only answered the first question would leave the route
looking protected while a fabricated id reached somebody else's row -- which is
exactly the class of mistake that looks correct in review.

Verified by driving it: a todo created by one account is absent from another's
workbench; a second account posting to the first account's todo id gets 403 and the
row is untouched; a forged owner field in a create is ignored and the todo lands on
the sender; and a second account cannot mark read or delete a delivery belonging to
the first, with the first account's copy unchanged after both attempts.

**Given up.** Nothing. Bulk actions and delete are per-recipient by construction,
which is more code than a broadcast query and is the only version that can record a
read receipt.

### D-026 — A broadcast is materialised into deliveries at send time

**Decision.** `messages` holds what was written; `message_recipients` holds one row
per person it reached, created when it is sent.

**Why.** A broadcast that stays a query cannot answer any of the questions the
inbox is for. It cannot record that somebody read it. It cannot be deleted by one
recipient without affecting the others. And it silently changes meaning when the
audience changes: a new member of a department would retroactively receive last
year's notices, and a departed one would lose the record that they were told.

The same reasoning as the announcements module, reached independently, which is
mild evidence that it is the right shape for "who was told what".

An empty recipient list for a direct message delivers to **nobody**, not to
everybody. A fallback to the widest audience is the failure mode worth designing
against, and it is tested.

**Given up.** A delivery row per recipient, which is more rows than a query would
need. At this scale that is not a cost worth optimising.

### D-027 — An API key's authority is the intersection of its scopes and its owner's permissions

**Decision.** A key carries `ownerPermissions ∩ scopes`. It is never a superuser,
whatever its owner is. Scoping a key beyond what its owner holds is **refused at
creation** rather than silently narrowed.

**Why.** Without the intersection a key would be a way to become somebody else
rather than a way for a person to act. The intersection also gives the property
that makes key management tractable: withdrawing a permission from the owner
narrows every key they ever issued, immediately, without anybody remembering to
edit the keys. Verified by emptying the owner's role and watching the key start
returning 403 with no change to the key.

Refusing an excess scope rather than storing it is the honest version. A stored
scope the owner does not hold would produce a key that looks more powerful than it
is, and the difference would surface as a puzzling 403 much later.

A key is never a superuser: a break-glass account issuing one would otherwise hand
out its own unlimited authority with no scope able to narrow it.

Only a SHA-256 hash and a short clear prefix are stored. SHA-256 without a salt is
correct here and would be wrong for a password: a password is short, guessable and
reused, so it needs a slow salted hash; a key is 256 bits of randomness, so there
is nothing to guess and a slow hash would only make every request slower. The
prefix is not a secret and is not treated as one -- it exists so a key can be
identified in a list. Lookup is by prefix and the match is then confirmed by
constant-time hash comparison, because searching on the visible part alone would
mean the visible part was sufficient.

The plaintext is shown once, held in the encrypted session for exactly one render,
and never written to the database, a log or a URL.

**Given up.** Nothing. This is the arrangement the documentation describes.

### D-028 — The machine API is mounted outside the session middleware

**Decision.** `/developer/api/v1/` is registered on the root router, not on the
panel group. It authenticates with a key and returns JSON for every outcome,
including failures.

**Why.** Found by driving it. The panel group carries the session middleware, so
the first version intercepted every key-authenticated request and redirected it to
the sign-in page -- the key middleware never ran at all, and every machine caller
received an HTML redirect rather than JSON. The unit tests passed throughout,
because the defect was in where the route was attached rather than in what it did.

A machine caller that follows a redirect to a sign-in page reports a parse failure
instead of an authentication failure, which is the worst possible diagnostic. Every
refusal is therefore JSON with a stable code, and the codes are documented on the
reference page.

**Given up.** The API does not share the panel's middleware, so anything the panel
gains there -- request logging, size limits, CSRF -- has to be considered
separately for the API. CSRF is not applicable to a bearer token; the others are.

### D-029 — The API reference is hand-written, and the generated specification is not done

**Decision.** `/developer/api` is a hand-written table of operations and error
codes. The OpenAPI 3 specification and the drift test that `features.md` asks for
are **not implemented**.

**Why, and what is outstanding.** The drift test is the valuable half: it is what
keeps a document true. A generated specification for a surface this size would be
complete and unreadable, and the honest position is that the readable version is
done and the machine-readable one is not, rather than shipping a generator with no
test behind it.

This is recorded as outstanding work rather than left to be discovered. It should
be closed when the API surface is large enough that a hand-written table stops
being maintainable, which is also when the drift test starts paying for itself.

**Given up.** No machine-readable specification, so no generated client and no
Swagger UI. An integrator reads a page instead.

### D-030 — Credentials are refused rather than stored in clear

**Decision.** Integration credentials are encrypted with `POCKETBASE_ENCRYPTION_KEY`
before they are stored. If no key is configured the platform **refuses to store
them** and says so.

**Why.** The alternative -- store them in clear when the key is missing -- is a
silent downgrade. The provider would work, nothing would look wrong, and a payment
provider's secret key would be readable by anyone who obtained the database. A
refusal is visible, actionable and reversible; a silent downgrade is none of those.

The edit form never receives a stored value. It is told which credentials exist and
shows a fixed mask, and a submission carrying the mask or an empty field leaves the
stored value alone. That rule is what makes the form usable at all: without it, the
form would either have to send the real secret back to the browser for resubmission
or overwrite every credential with the literal mask the first time somebody changed
an unrelated field.

The mask is fixed rather than partially revealed. Showing the first and last
characters is a common habit and a poor one: it leaks length and shape, it is enough
to confirm a guess, and it gives the operator a false impression that they can
recognise the value.

**Given up.** An installation without an encryption key cannot configure any
credential-bearing provider. That is the intended outcome and is stated on the
form.

### D-031 — The redactor reports whether it decoded, because handing the input back recursed

**Decision.** `parseJSON` returns `(value, ok)`. On failure the callers stop rather
than passing the input on.

**Why.** This is the most serious defect found across all waves, and it was found by
driving the platform rather than by a test. `looksLikeJSON` accepts anything that
starts with a brace and ends with one. `parseJSON` used to return an unparsable
input unchanged, and the caller had only reached it *because* the value looked like
JSON, so it recognised the same string again and recursed. The result is
`fatal error: stack overflow` -- the process dies, it is not a caught panic and
there is no degradation to recover from.

It was reachable from any request body, because the operation log redacts the body
it is sent. A signed-in user could stop the server by submitting a form field
containing `{not valid json}`.

The module had thirteen passing tests, written when it shipped, and none of them
fed it a malformed JSON-ish string. The lesson is not that the tests were careless
but that a redactor's input domain is *whatever a caller sends*, and a test suite
that only covers the shapes its author thought of does not cover that domain.

Three regression tests now: that malformed JSON-ish strings terminate, that
well-formed JSON is still walked, and that unparsable bytes are not handed back as
though they had been examined.

**Given up.** Nothing.

### D-032 — The public health endpoint says one word

**Decision.** `/healthz` and `/readyz` are unauthenticated and answer
`{"status":"..."}` and nothing else. The detailed view, naming each component and
the reason it is unwell, is behind a permission on the panel.

**Why.** A health endpoint has to be reachable by an orchestrator, which holds no
session. A health endpoint that names the failing component to an anonymous caller
is a free map of the system for anyone who asks: it discloses the database engine,
the storage layout and where the weak point is. The two audiences want different
things and get different answers.

Liveness and readiness are separate endpoints because they call for opposite
actions. A process whose database is unreachable is **alive** and should be taken
out of a load balancer's rotation, not killed; reporting one status for both makes
an orchestrator restart a process that was working.

**Given up.** An operator who wants the detail needs a session. That is the point.

### D-033 — Only 5xx counts as an error

**Decision.** The error rate counts responses with status 5xx. A 4xx does not count.

**Why.** A 4xx is the caller being wrong; a 5xx is this process being wrong. An
error rate that includes 404s is a rate that goes up when somebody follows a stale
link, which is not a thing an operator can act on, and mixing the two means the
number cannot be alerted on at all. The request count includes both, so the two
figures together still say everything.

**Given up.** A surge of 401s -- a credential-stuffing attempt, say -- is invisible
in the error rate. It is visible in the request count and in the security log,
which is where it belongs.

### D-034 — The update feature records, it does not self-update

**Decision.** `system_updates` records version checks and applied updates with a
before and an after version. This build performs no automatic update and checks no
feed unless one is configured at link time.

**Why.** `features.md` asks for an "assisted, explicit update with a recorded before
and after version; never a silent self-update". The record is the part that makes an
update auditable, and it is implemented. The mechanism is not: replacing a running
binary is a deployment concern, and a web panel that can download and execute a new
version of itself is a remote code execution feature with a permission attached,
which is not a trade this platform should make on an operator's behalf.

The version check is off by default for a related reason: it is an outbound request
that names the installation and its version to a third party, and enabling that
without being asked is not a decision the software should make silently.

This is recorded as a deliberate omission rather than left to be discovered, in the
same way as D-029.

**Given up.** No "check for updates" button by default. An operator updates the way
they deployed.

---

## Pending

Decisions for the remaining modules are appended below as they are taken.
