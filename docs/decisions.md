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

### D-035 — Money is integer minor units with an explicit currency, and never a float

**Decision.** Every price, cost, discount and refund is an integer count of minor
units alongside an ISO-4217 code. `internal/money` parses a decimal *string* digit
by digit, and the number of decimal places comes from the currency.

**Why.** "19.99" through a float64 is 19.989999999999998. Multiplying by a hundred
and truncating gives 1998: a price one cent low, on every product, with nothing to
show it happened. A shop that is a cent out per line is a shop whose books do not
balance, and the error is invisible until somebody reconciles them.

The currency's exponent matters as much as the units. A bare "divide by a hundred"
shows a Japanese price a hundred times too small and loses a thousandth of every
Kuwaiti dinar, because a yen has no minor unit and a dinar has three. The exponent
is therefore a property of the currency, not a constant.

Parsing refuses more decimal places than the currency has rather than rounding
silently. A form that accepts 19.999 dollars has to decide what it meant, and
deciding without saying so is how a price ends up different from what was typed.

**Given up.** Every screen and every form has to convert at the edge. That is the
cost, and it is paid once per surface rather than once per bug.

### D-036 — Stock is a cache of an append-only ledger, not a number

**Decision.** `stock_ledger` is the source of truth. `product_variants.stock` is a
materialised balance, written only by `ApplyStock`, which writes the ledger entry
first and the balance second. `ReconcileStock` recomputes it from the entries.

**Why.** A stock number that can be edited directly has no history, and "why is this
3 and not 5" is the question that matters when a count is wrong. The write order is
deliberate: a crash between the two leaves the ledger correct and the cache stale,
which reconciliation repairs. The other order leaves a number with no entry
explaining it, which nothing can repair.

A movement that would take stock below zero is refused rather than recorded. Stock
below zero is not a number to be kept; it is a sign that something else is wrong --
an order placed against stock never received -- and accepting it moves the
discovery to a customer's inbox rather than this screen.

**Given up.** Two writes per movement instead of one, and a cache that can drift and
must be reconcilable.

### D-037 — Rich-text sanitising belongs to the field type, not to the module that first needed it

**Decision.** The sanitiser moved from `internal/announcement` to `internal/richtext`
and is registered for **every** collection with an editor field. A test enumerates
the live schema and fails when one is missing.

**Why.** It was written for announcements and applied only to announcements. When the
catalogue was built, five of the six editor fields in the platform were storing
whatever HTML was submitted: a product description, a category description, a todo, a
message and a release note. Every one was stored cross-site scripting, and the reason
was not carelessness at any single site -- it was that the protection had been
attached to a module instead of to the hazard.

The hazard belongs to the field type. So does the coverage, and so does the guard:
the drift test is what stops the next editor field from being added without it.

**Given up.** A sanitiser applied to fields that may not need it, which costs a
string scan on write and nothing on read.

### D-038 — A named byte slice is not a byte slice

**Decision.** `internal/server/jsonfield.go` holds one decoder for json columns, and
it names `types.JSONRaw` explicitly.

**Why.** PocketBase returns a json field as `types.JSONRaw`, which is a **named**
`[]byte`. A Go type switch matches exact types, so `case []byte` does not match it,
the value falls to the default branch and is silently lost. A matrix generator
received two attributes with values, did not recognise the list, and produced no
variants -- with no error to show for it. The same shape was waiting in the
integration provider configuration, where it happened to work because the default
branch used `fmt.Sprint` and reparsed the result.

One helper rather than a switch per call site, because the mistake is invisible
where it is made.

**Given up.** Nothing.

### D-039 — A tier's benefits are typed rows, and the discount is a fraction

**Decision.** `member_tier_benefits` stores a kind and a JSON payload whose shape the
kind decides. A discount is `{"rate": 0.9}`, a fraction pricing multiplies by. The
form takes a percentage and stores the fraction.

**Why.** "10% off" and "10 percent off selected items" are the same string to a
regular expression and different amounts of money to a customer. The specification
puts it plainly -- pricing and checkout have to read a discount rate rather than
parse prose -- and the reason is that a number which has been parsed from a sentence
is a number nobody can be confident in.

Three specific choices follow from that. A rate outside nought to one is **ignored
and logged** rather than applied, because a value like 90 is far more likely to be a
percentage typed into the wrong field than an intended 9000% discount, and applying
it would charge a negative amount. When a tier declares two discount rates the
**better one wins**, because the order of rows is not something an editor controls.
And a malformed benefit is skipped rather than failing the whole resolution, because
one bad row must not strip every benefit from a paying member.

**Given up.** A benefit kind cannot carry arbitrary prose. A site that wants a
sentence has `custom` and a label, and gets no interpretation.

### D-040 — The highest tier wins, not the most recent

**Decision.** When an account holds several active memberships, the one with the
highest tier level applies; among equals, the one that lasts longest, and perpetual
beats any expiry.

**Why.** A person can legitimately hold a gifted tier and a purchased one. Any rule
based on recency or on the order rows were created means a gift can downgrade a
paying member, which is not what either party intended. Level is the field that
exists to express "better", so it decides.

A membership past its expiry grants nothing even before the scheduled task moves it.
The task keeps the list honest; the resolution keeps a request from granting a
benefit that ended yesterday.

**Given up.** The losing membership is still recorded, which is correct -- it is the
history -- and an operator reading one row may wonder why its benefits are not in
force. The list shows the status, and the resolution is documented.

### D-041 — A percentage discount is stored in basis points

**Decision.** `coupons.value` holds minor units for a fixed amount and **basis points**
for a percentage. The form takes a percentage; 12.5 is stored as 1250.

**Why.** The same reason as the tier discount rate, from the other direction. A
whole-number percent column cannot hold 12.5, and a float cannot be trusted with
"12.5% of 1999". Basis points keep the value an integer and the arithmetic exact, and
the conversion happens once, at the form.

The column therefore holds three different units depending on the type -- minor
units, basis points, and nothing at all for free shipping. That is a cost, and it is
paid in two places: the form converts on the way in, and the list converts on the way
out. It is recorded here because a reader looking at the raw value of 1250 has no way
to know it is twelve and a half percent.

**Given up.** One column, three meanings. A column per type would be a sparse table
and a migration per new coupon type.

### D-042 — A discount never exceeds what it is discounting, and an over-100% coupon is clamped

**Decision.** The computed discount is capped at the cart subtotal. A percentage above
100% is clamped to 100% rather than refused.

**Why.** A fixed-amount coupon on a small basket would otherwise produce a negative
total, which is a refund the shop never agreed to. The clamp is the other direction: a
misconfigured "free" coupon should give the order away, not fail at the till. Refusing
at checkout is the worse failure of the two, because it happens in front of a
customer.

The cap and the minimum spend are checked in the order a person would expect them
explained: is the coupon usable at all, does the cart qualify, and only then how much
comes off. Reporting "below the minimum spend" for a coupon that expired last week
sends somebody to fill a basket for nothing.

**Given up.** A wildly misconfigured coupon silently gives the order away instead of
raising an error. The audit entry records the configured value, which is where the
mistake is visible.

### D-043 — A unique index on an optional text column allows exactly one empty value

**Decision.** The coupon code index is partial: `UNIQUE (code) WHERE code != ''`.

**Why.** Found while writing tests. The specification says a coupon's `code` is
"unique; empty means auto-issued per grant" -- so empty is a legitimate value that
several rows may hold. A plain unique index allows exactly one such row, and the
second coupon an operator created without a public code would be refused with a
message about a field they left empty on purpose.

The general shape is worth remembering: a unique index expresses "no two rows share a
value", and an optional column with a meaningful empty value needs the index to say
which rows it governs.

**Given up.** Nothing. A partial index is supported and costs the same.

### D-044 — Adding a month clamps to the last day, because Go's AddDate does not

**Decision.** `subscription.AddInterval` computes the target month on its first day,
clamps the day to the length of that month, and rebuilds the date. Go's `AddDate` is
not used for months or years.

**Why.** `AddDate` normalises. The thirty-first of January plus one month is the third
of March, because the thirty-first of February does not exist and the overflow rolls
forward. A subscriber who signed up on the thirty-first would be billed three days
late every February, and the drift compounds: the next renewal starts from the moved
date. Every billing system clamps, and so does this one -- the same day of the month,
or the last one that exists. The leap day is handled by the same rule, so an
anniversary on the twenty-ninth of February lands on the twenty-eighth in a common
year.

`AddDate` is still used for days and weeks, where no normalisation can occur.

**Given up.** Three functions instead of one, and a day-of-month that moves to the
twenty-eighth for a February renewal and then stays there rather than returning to
the thirty-first in March. That is the behaviour of every calendar a customer has
ever used, and returning to the thirty-first would be the surprising one.

### D-045 — Proration credits and charges whole amounts, and a downgrade is owed time

**Decision.** A mid-period plan change computes a credit for the unused part of the
current period and a charge for the same part of the new plan, both as whole amounts
derived from the daily rate. The difference is what is due. A negative difference
means the account is owed time, and the caller schedules the change for the end of the
period rather than refunding.

**Why.** A percentage of a period and a percentage of a price do not necessarily agree
to the unit, and the difference has to go somewhere. Computing both figures from the
same remaining-day count means the two numbers shown to an operator add up to the
amount actually charged, which is the property that makes the screen trustworthy.

Refunding a fraction of a period is a decision about money, and the specification puts
a downgrade at the next cycle. Scheduling it is also the kinder behaviour: the
customer has paid for the period and keeps it.

A part-day rounds up, so a change made in the last hour of a period is not free. An
account could otherwise extend its subscription indefinitely by changing plan
repeatedly near the period end.

**Given up.** An upgrade is charged immediately and a downgrade waits, which is
asymmetric -- deliberately, and it is the asymmetry the specification describes.

### D-046 — A pause credits whole days only, and extends the period on resume

**Decision.** `pausedDaysAccrued` counts whole days between the pause and the resume.
Resuming adds them to the current period end and to the next billing time.

**Why.** Without the extension, a subscriber who paused for a fortnight would pay for
a period they could not use, which is the thing pausing exists to avoid. And without
the whole-days rule, crediting an hour would let a subscriber extend their
subscription indefinitely by pausing and resuming repeatedly.

**Given up.** A pause of twenty-three hours credits nothing. That is the correct
direction to be wrong in, and it is stated on the screen.

### D-047 — A failed charge lapses the subscription rather than cancelling it

**Decision.** Dunning retries a failed cycle up to a bounded number of times with an
exponential backoff capped at a week. When the budget is spent the subscription is
marked **expired**, not cancelled.

**Why.** A cancellation is an intent the customer expresses; an expired card is not.
Marking it cancelled would tell the customer they had asked for something they did
not, and would make "why was I cancelled" a question with no honest answer. An expired
subscription can be revived by fixing the card; a cancelled one has to be started
again.

**Given up.** An expired subscription still occupies the account's history and a
support agent has to know the difference. The status list shows it.

### D-048 — An order records what it cost, so the breakdown is stored and never recomputed

**Decision.** `orders` holds the whole amount breakdown, and `order_items` holds a
snapshot of the product name, variant label, SKU, image and unit price. Nothing is
looked up from the catalogue when an order is read.

**Why.** A price edited next month must not change what an order placed today says it
cost. That is the difference between a record and a view, and it is the difference
that decides whether a two-year-old invoice can be defended. The product and variant
relations are kept alongside the snapshots for reporting only.

**Given up.** A catalogue rename does not propagate to past orders, which is correct
and occasionally surprising to an operator reading a list. The snapshot is what was
sold; the relation is what it is called now.

### D-049 — The state machine refuses a cancellation of a paid order and says to refund instead

**Decision.** `pending_payment` may become paid, fulfilling, cancelled or closed. A
paid, fulfilling or completed order may not become cancelled or closed; the refusal
says to refund it.

**Why.** The two look like the same intent to somebody who has not done it before, and
only one of them returns the customer's money. "The system would not let me" is not an
answer an operator can give a customer, so every refusal names both ends of the move
and lists what is legal from where the order is. The legal moves on the screen are read
from the same table, because a second list is a second place for the two to disagree.

Terminal states are terminal: a refunded order stays refunded. A refund is not undone,
it is followed by a new order.

**Given up.** An operator who genuinely wants to cancel a paid order has to refund it
first. That is the intended friction.

### D-050 — The payment status is derived from the amounts, not set by the caller

**Decision.** `PaymentStatusFor` computes the payment status from the grand total, the
amount paid and the amount refunded.

**Why.** An order that says it is paid and shows nothing received is a reconciliation
failure waiting to happen, and the failure surfaces at month end rather than at the
moment it is introduced. Deriving the status from the amounts makes the two impossible
to disagree.

A partial payment is recorded against the amounts **without a status transition**, so no
timeline entry claims the order changed state when it only became partly paid.

**Given up.** Nothing. The derivation is one function and one test.

### D-051 — A rate and a percentage are different units and need different conversions

**Decision.** `rateToBasisPoints` (a fraction, ×10000) and `percentageToBasisPoints` (a
percentage, ×100) are separate functions, and both round rather than truncate.

**Why.** Two real defects, found by writing the test that should have passed.

The membership rate is a fraction meaning "charge ninety percent". Deriving its
complement first looks equivalent and is not: `1 - 0.9` is `0.09999999999999998` in
binary floating point, which truncated to 999 basis points and under-discounted every
membership by one minor unit.

The tax path had the same defect from the other side. `8.2 * 100` is
`819.9999999999999`, so a straight conversion to basis points taxed at 8.19%, and the
error is invisible on any single receipt.

Rounding to the nearest basis point is the closest a float can honestly get to the
number somebody typed. Both cases are locked down by a regression test.

**Given up.** Nothing, but the general lesson is recorded: any float that becomes an
integer in this codebase is a place to look for a truncation.

### D-052 — A failed stock reservation discards the order, releasing only what was taken

**Decision.** `ReserveStock` reports the stock it actually took, and `discard` gives
back exactly that before deleting the order, its items and its timeline.

**Why.** Two defects, both found by driving the running binary rather than by reading
the code.

The first: the reservation runs after the order is written, because a ledger entry
names the order that took the stock. When it failed, the order stayed behind -- an
order that exists, holds nothing, and can be paid for by somebody nobody can fulfil. It
surfaced as an order count one higher than the orders that had actually been placed.

The second, found while fixing the first: releasing everything the order asked for
credits stock that was never taken. An order for 999 of 8 left the catalogue holding
1001. The reservation now returns what it took and the cleanup returns exactly that.

A third, found by the regression test: the cleanup could not delete the order at all,
because `order_events.order` is a required relation and the timeline row was written
before the reservation ran. The deletion failed, logged, and left the order in place --
a cleanup that looks like it worked.

**Given up.** The reservation is not atomic with the order write. A crash between them
leaves an order with no stock held, which the reconciliation can find and a retry can
repair. A transaction would be better and is not expressible through the record API
here.

### D-053 — A provider transaction id is the idempotency key, and a replay is not an error

**Decision.** A callback is matched to a transaction by the provider's own identifier,
which carries a partial unique index. A callback naming a transaction already recorded
as succeeded increments `callbackCount` and changes nothing. It is answered with
**200 and a "duplicate" status**, not with a failure.

**Why.** Providers retry. A callback that timed out on the network arrives again,
sometimes several times, sometimes hours later. Without an idempotency key the second
arrival charges the order again, and the customer finds out from their statement. The
key is the provider's identifier rather than anything this system generates, because
the duplicate arrives from outside and has to be recognisable without a lookup table of
intents.

Answering a replay with a failure would be worse than answering it with nothing. A
provider that receives an error retries, so refusing a duplicate makes it retry the
duplicate forever -- a callback storm manufactured by the receiver.

The count is kept rather than discarded. A duplicate storm should look like one event
seen six times, not like nothing at all, and the count is what makes it visible.

**Given up.** A provider that reuses a transaction identifier for a genuinely different
attempt would have that attempt ignored. No provider does this, and the alternative --
trusting an identifier only sometimes -- is not a rule.

### D-054 — A succeeded transaction is never altered; corrections go through a refund

**Decision.** A callback that contradicts a succeeded transaction is refused. The
transaction stays succeeded and the order stays credited.

**Why.** The specification requires it, and the reason is that the alternative
destroys the record. A transaction that can be un-succeeded by a later message has no
defensible state: a reconciliation six months later cannot tell whether the payment
happened. A refund is a record of its own, with its own approval, and the pair is
what an auditor reads.

A failure that later succeeds **is** applied, because that is a genuine change of state
rather than a contradiction.

**Given up.** A provider that actually reverses a payment in place needs a refund to be
created, which is more work for the operator and the correct amount of work.

### D-055 — The amount on a callback is compared, never trusted

**Decision.** A transaction records the amount from the intent, not from the callback.
A callback reporting a different amount is flagged and the mismatch is audited at
critical; the order is credited what was requested.

**Why.** A provider that took a different amount from the one requested is a
discrepancy somebody has to look at. Accepting the reported figure would make the
order's own totals wrong and would let a forged callback -- correctly signed, because
the attacker has the secret -- inflate a credit.

The safe direction to be wrong in is the one that does not credit money, so an
unrecognised provider status becomes pending rather than succeeded.

**Given up.** A legitimate overpayment or underpayment has to be corrected by hand
rather than absorbed. That is what a discrepancy is for.

### D-056 — A callback endpoint is declared signature-guarded rather than exempted

**Decision.** `signatureGuarded` is a named registration helper. The startup self-check
still aborts on any state-changing route without a guard, and the webhook passes it by
declaring that its authority is an HMAC.

**Why.** The self-check fired the moment the endpoint was added, which is the behaviour
that was designed for. The obvious fix -- adding the path to the exemption list beside
`/login` -- would have been the wrong one: an exemption list grows quietly, and the
next unguarded route would be added the same way.

A named helper is greppable. The next person to add a route of this kind has to write
the word "signature", which is a claim they can be asked to defend.

The endpoint is also closed by default rather than open by default: it refuses every
request when the provider has no signing secret, so a misconfiguration fails safe.

**Given up.** One more concept in the server package. The alternative is a list nobody
reads.

### D-057 — A status vocabulary is a contract between modules, and guessing one fails silently

**Decision.** The webhook checks a provider's status against `integration.StatusEnabled`
rather than a string literal.

**Why.** A real defect, and the worst-shaped one in this module. The integrations
vocabulary is `enabled`, `disabled`, `testing`, `error`; the handler checked for
`"active"`. Every correctly configured provider therefore returned an empty signing
secret, every callback was refused, and the endpoint looked like it was working
correctly -- it refused forged callbacks for the right reason and legitimate ones for a
reason nobody could see. It surfaced only by reading the log line rather than the HTTP
response.

The general lesson is recorded: a status compared against a literal in another module's
vocabulary is a comparison that cannot be checked by the compiler and will not fail
loudly.

**Given up.** Nothing. The constant existed.

### D-058 — Reconciliation reports differences and repairs nothing

**Decision.** `Reconcile` compares a settlement report against the local transactions,
marks agreed transactions as reconciled, and returns a list of differences. It changes
no amount and no credit.

**Why.** A mismatch is a question about money. A system that silently adjusts its own
figures to agree with a file it was handed has destroyed the evidence of whatever went
wrong, and the file may itself be wrong.

It looks both ways. A settlement for a transaction this system has never seen is money
the shop has and does not know about; a transaction recorded here as succeeded that the
report does not mention is the shape of a forged callback. Only the second direction is
restricted to transactions old enough to have settled, because a payment taken an hour
ago has not reached a statement yet and reporting it would make every reconciliation
noisy enough to ignore.

A report listing the same transaction twice is reported rather than netted out: that is
money that arrived twice for one sale.

**Given up.** Every discrepancy needs a person. That is the point.

### D-059 — A pending refund does not reserve capacity; an approval does

**Decision.** `Commits` counts an approved, processing, succeeded or failed refund.
A pending one does not. The cap is enforced inside a transaction when a refund is
approved, against the order's `amountPaid`.

**Why.** Reached by writing the test that should have passed. Counting pending refunds
as committed means two requests for the whole order each see the other as committed and
**neither can be approved**. A deadlock is not a conservative choice, it is a broken
one, and it took the test to see it -- the rule had felt safer.

So the specification is taken at its word: the cap is enforced *at approval time*,
under a transaction. A request is a question rather than a reservation, and many may be
open at once. Only the approvals are serialised, and that is the property that protects
the money: the sum of everything approved never exceeds what was paid.

A **failed** refund does keep its capacity, which is the counter-intuitive half. It may
be retried by the retry path, and a retry does not go through an approval -- so freeing
its capacity would let another refund be approved against the same money, and the retry
would then overdraw with nothing to catch it. Releasing it is an explicit act:
withdrawing the refund.

**Given up.** A stale pending request holds nothing and blocks nothing, so a queue of
requests can build up unnoticed. The list shows the pending ones, and the cap is still
enforced where it counts.

### D-060 — Nobody decides their own refund, not even automatically

**Decision.** `Approve` and `Reject` refuse a reviewer who is the requester. This is
checked before the auto-approval threshold is applied, so a refund is not auto-approved
for the person who requested it.

**Why.** An approval workflow whose approver can be the requester is a formality, and
the reason `refunds:refund:request` and `refunds:refund:approve` are two permissions is
that they are meant to be held by different people. The rule is enforced in the package
rather than in the screen, because a screen can be bypassed and this cannot.

The threshold does not override it. An automatic decision taken because the requester
happens to be the only person available is still the requester deciding.

**Given up.** A shop with one administrator cannot refund anything, and has to grant a
second account the approving permission. That is the correct amount of friction for a
control that exists to stop one person moving money to themselves.

### D-061 — A refund by item subtracts the line's share of the order discount

**Decision.** A refund computed from order items totals the chosen quantities at the
prices the order recorded, then subtracts the line's proportional share of the
order-level discount.

**Why.** Without the share, refunding every item of a discounted order gives back more
than was paid for it. A 6000 order discounted by 1000 was paid 5000, and its items
still sum to 6000; refunding them all at their own prices returns 6000.

The prices come from the order's snapshots rather than the catalogue, for the same
reason the order stores them at all: the catalogue may have changed since, and a refund
is against what was bought.

**Given up.** The share is proportional rather than per-item, so refunding one item of a
discounted order gives back slightly less than a person might compute by hand. The
alternative is a per-item discount allocation that has to be stored on the order, and
the proportional figure is the one that makes the total come out right.

### D-062 — An unclassified provider refusal is not retried

**Decision.** `isRetryable` returns true only for named transient codes: a timeout, a
network error, a rate limit, an unavailable provider. An empty code with a failure
message is not retryable.

**Why.** Found by a test. The first version treated an empty code as transient, on the
reasoning that a failure with no classification is probably a network problem. It is
not: it means the provider refused without saying why, and retrying an unclassified
refusal risks sending the same refund twice to a provider that is about to accept it.

**Given up.** A genuinely transient failure that the provider forgot to classify needs
a person to press the button again. That is the correct direction to be wrong in.

### D-063 — Cron accepts a leading seconds field, superseding the earlier refusal

**Decision.** A cron expression may have five fields or six. Six adds a **leading**
seconds field, the shape Quartz and Spring use. Anything else is refused by count.

**Why, and what it supersedes.** This reverses D-021's neighbour rule that six-field
expressions are rejected. The rejection existed to prevent a silent misreading -- a
six-field expression read as five would take seconds for minutes -- and the field count
already prevents that, so the rejection was buying nothing once seconds were wanted.

The count is the only safe discriminator: five or six, nothing else, because there is no
ambiguity about which shape an expression is when it has to be one of two.

The scheduler's tick went from twenty seconds to one, because a twenty-second tick cannot
run a ten-second task more often than once per twenty seconds -- the field would have
parsed and then meant nothing. The **query** did not follow the tick: the loop remembers
the earliest `nextRunAt` it has seen and skips the database entirely while the clock is
short of it, re-looking at least every thirty seconds so an operator's change is still
picked up. An empty tick is a mutex read.

**Given up.** Five-field expressions keep their old meaning, so nothing an operator has
already written changes. The cost is that the two shapes are distinguished by a count,
which is not self-describing -- the jobs screen now says both shapes out loud.

### D-064 — Host measurements store counters, not rates

**Decision.** The lightweight host monitor stores processor time as used and total
seconds, and network as cumulative byte totals. Percentages and rates are derived when
the rows are read.

**Why.** A percentage computed at write time is frozen against the interval it was
computed over; if that interval is later read differently the figure cannot be
recomputed. A rate needs two rows and the time between them, so storing one makes each
row's meaning depend on its neighbour's. Both are cheap to derive and impossible to get
wrong twice.

The sampler is a **goroutine**, not a scheduled task. A task would write a run row every
time it fired -- a thousand a day at the default interval, every one saying "took a
measurement" -- and the interval would be limited to what a cron expression can say. A
one-second ticker comparing two times is cheaper than either, and fifty-nine of every
sixty wake-ups do nothing at all.

A platform that cannot read something lists it as unavailable rather than reporting zero.
Zero and unknown are different claims, and a monitoring screen is exactly where confusing
them costs somebody an afternoon.

**Given up.** The screen does the arithmetic, so two rows are needed before a rate can be
shown and the first row after a restart has none. That is stated in the card's footer
rather than hidden.

### D-065 — A monitored machine is a row, and an agent's key is bound to one

**Decision.** `monitor_nodes` records each machine. A remote agent authenticates with an
API key carrying the submission permission **and a `monitorNode` binding**; a key with the
permission and no binding is refused.

**Why.** The first version recorded one machine, the one the process runs on. An
installation with a single server needs that; an installation with two needs the opposite,
because a disk filling up on the machine nobody logs into is exactly what this is for.

The binding is a relation on the key rather than a token of its own so a node inherits the
key's expiry, revocation, rate limit and audit trail instead of duplicating all four.
Letting a key report for whichever machine it names would let one compromised agent rewrite
another machine's history, and the binding is one field.

The machine running the process is marked with a column rather than by comparing host
names, because "which one is this" is a question the screen asks and the answer must not
depend on two names agreeing. Its code is fixed at `local`: a machine that renames itself
would orphan its own history.

Readings are validated rather than stored. A chart is read to answer "is something wrong",
and a figure that cannot be true makes it lie about the one thing it is for. Every refusal
names what is wrong, because an agent operator has to fix it from the message.

**Given up.** Registering a machine and issuing its key are two steps. That is one more
step than a shared token would need, and it is the step that says which machine a reading
belongs to.

### D-066 — A missing collection in a migration fails loudly

**Decision.** A migration that depends on a collection calls `requireCollection`, which
errors when it is absent, rather than `findCollection`, which returns nil.

**Why.** A real and silent defect. The migration added the monitoring binding to
`developer_api_keys`; the collection is called `api_keys` -- the module that owns it is
called developer and the collection is not. The `if col != nil` guard turned the
misspelling into a no-op: the field was never created, every binding was discarded on save,
and nothing failed until a live check asked where the value had gone.

`findCollection` is right for an idempotent migration that may run before its target. It is
wrong when the collection is the point of the migration, and the difference has to be
written down at the call site rather than inferred from a nil check.

**Given up.** One more function. The alternative is a class of failure that presents as
"the field is not there" three steps away from the cause.

### D-067 — Chart bucket boundaries align to the clock, and a percentage is not divided twice

**Decision.** A window's buckets are aligned down to a multiple of their width. A
percentage series passes the bucket's own ratio; a load average is divided by its sample
count by the caller.

**Why.** Two defects, both found by writing the test that should have passed.

The boundaries were aligned to the window's start, which is "now minus an hour" at
whatever second the page was opened. Two reloads a second apart put the same reading in
different buckets and drew a slightly different chart -- a chart that shifts under a reader
is a chart nobody can compare with anything.

And a percentage was divided twice. The bucket's processor figure is already
`sum(used)/sum(total)`, and the averaging helper divided that by the sample count as well,
so a machine at twenty percent was drawn at six and a half. That is a chart that sends
somebody looking for a problem that is not there, which is the worst thing a monitoring
screen can do.

**Given up.** A helper that takes a mean rather than a total is slightly less convenient to
call, and it makes the arithmetic each caller's responsibility in the open rather than
hidden in a function that cannot know which kind of figure it was given.

### D-068 — Traditional Chinese is a separate catalogue, not a conversion

**Decision.** `zh-TW` is translated independently of `zh-CN`. No conversion runs between
them.

**Why.** The two differ in vocabulary as much as in glyphs -- 数据表 and 資料表, 优惠券 and
優惠券, 视频 and 影片 -- so a mechanical conversion produces characters that are right and
words that are wrong, in exactly the places a reader would notice most.

The first version of the test guarding this counted messages identical in both catalogues,
on the theory that a conversion would leave many the same. It found eighteen, and every one
is a word that genuinely uses the same characters in both scripts: 取消, 全部, 是, 否, 商品,
角色. **Counting overlap measures the language, not the translation.** The test now looks
for characters that exist in one script and not the other, which is a signal that means what
it says.

**Given up.** Twice the translation work, and a third catalogue to keep in step with the
other two. The coverage test fails on any gap, so the cost is paid at build time rather than
by a reader.

### D-069 — The catalogue is Go, and the fallback is visible

**Decision.** Messages are Go maps keyed by a dotted name, not files loaded at run time.
The lookup order is the locale, then English, then the key itself.

**Why.** A catalogue in the binary cannot go missing from a deployment, cannot fail to parse
at startup, and is checked by the compiler when referenced through a constant -- the same
reasoning that put the permission codes in Go.

Falling back to the key rather than to a blank is deliberate: a screen showing
`monitoring.nodes.title` is obviously unfinished and somebody can report it, and a screen
showing a blank where a label should be is not. English is the middle step so a locale that
is complete but for one message shows one English word rather than one key.

The language list is derived from the catalogue, so a locale cannot be offered without
messages behind it. The screen no longer claims that only English ships.

**Given up.** Adding a message means editing three Go files and rebuilding, where a
translation platform would let somebody else do it without a release. That is a real cost
and it is the right one at this size; at a hundred locales it would not be.

**What is not translated** is stated rather than left to be discovered: the module screens
behind the navigation -- tables, forms, field labels and help text -- are still English.
The catalogue and the two helpers are the whole mechanism, so adding a module is adding
keys.

### D-070 — The example dataset is a subcommand, and installation never runs it

**Decision.** `pocketadmin demo seed` writes about 250 example rows. A fresh install is
empty and stays empty until somebody types the command.

**Why.** A panel that quietly filled itself with example customers would be worse than one
that showed nothing, because nobody would know which records were theirs. The dataset is
for a demonstration or a test, and both of those are somebody deciding to have one.

`demo seed` refuses a second run rather than half-succeeding. The dataset has unique slugs,
SKUs, coupon codes and order numbers, so a second seed inserts until it hits one and stops,
leaving a half-duplicated state worse than either the old data or the new. The refusal names
the row count and the flag to use.

Without `--yes` the command prints every collection and row count it is about to destroy
and asks. It also refuses when standard input is not a terminal, so a script cannot destroy
a demonstration by accident. A `--force` flag was rejected: the size of what is being
removed is the thing an operator needs to see, and a prompt is the only place they read it.

**Given up.** Demonstrating the panel needs one extra command. That is the cost of not
guessing which records are real.

### D-071 — The examples are written through the modules, not around them

**Decision.** Orders are created by `order.Create`, stock moves through the catalogue
ledger, refunds go through request, approval and execution, memberships through
`membership.GrantMembership`.

**Why.** Inserting rows directly would produce a demonstration whose totals do not add up,
which is the one thing a demonstration of a money system must not do. Writing through the
modules means the example data is priced and scheduled by the same code the panel uses, so
a defect in the arithmetic shows up as a broken demonstration rather than as a
demonstration that is quietly wrong.

It also means the dataset cannot show a state the panel could not have produced. A refund
is requested by one person and approved by another because that is the rule the screen
enforces; the examples show the rule rather than an exception to it.

**Given up.** The seed is coupled to the modules' signatures and breaks when they change.
That is the point: a change that breaks the examples is a change that would break a real
caller.

### D-072 — A reset detaches relations rather than ordering around them

**Decision.** Before deleting anything, a reset empties every **optional** relation that
points inside the set being cleared. Required relations are resolved by sweeping: delete
whatever can be deleted, go round again.

**Why.** The first version walked a hand-written list backwards so a child went before its
parent. Deriving the requirement from the schemas showed that cannot work:
`departments.leader` points at `employees` and `employees.department` points back, so those
two have no correct order at all. Five more single-direction pairs were the wrong way
round. The reset emptied fifteen of thirty-eight collections, hit a delete that a child
record blocked, and returned no error -- so it printed a list of what it had cleared and
the operator had no reason to look further.

Detaching removes the problem instead of ordering around it. A required relation cannot be
emptied -- a variant with no product is not a state the panel can produce, and the schema
refuses it -- so those are left to the sweep, which derives the order from the data.

A per-collection count afterwards turns a partial clear into a failure. **A clear that
stops halfway is worse than one that fails**, because it looks like success.

**Given up.** The reset does more work than a correct hand-written order would. It is
bounded by the number of relation fields rather than the number of rows, and it cannot be
wrong.

### D-073 — Deriving an invariant beats writing it down twice

**Decision.** The test that guards the reset derives its requirement from the relation
fields in the schemas. It does not check a second hand-written list.

**Why.** The order requirement was originally stated in prose and checked by reading. A
test written that way would have agreed with the prose and missed all six mistakes. The
schema-derived version found five of them on its first run, including the cycle that made
the whole approach unfinishable.

A hand-written list agrees with itself. It is the same reason the permission points are Go
constants mirrored by a drift test rather than a document.

**Given up.** The test reads schemas and is slower and more indirect than asserting a list.
It is also the only version that can be wrong in a way somebody notices.

---

## Pending

Decisions for the remaining modules are appended below as they are taken.
