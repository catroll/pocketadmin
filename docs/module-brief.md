# Module Brief

How to add a module to this platform without breaking its conventions.

Read this before writing code. It describes what already exists, what a module is
expected to provide, and how the result is verified. It exists because the
conventions here are worth more than any individual module: the resource layer,
the permission vocabulary and the guard helpers are the shared skeleton, and a
module that ignores them becomes a second way of doing things.

The authoritative feature list is `docs/features.md`. The build order and module
dependencies are in `CAPABILITY-MAP.md`. Decisions and their reasoning are in
`docs/decisions.md`.

---

## 1. Architecture in one paragraph

PocketBase is embedded as a Go library and owns the SQLite database, migrations,
settings and the REST API. `main.go` composes an `http.ServeMux` that sends
`/api`, `/api/`, `/_`, `/_/` to PocketBase and everything else to a Gin router
serving an AdminLTE 4 panel. **The panel reads and writes through `core.App`
directly, in-process.** It never calls the REST API. PocketBase therefore never
evaluates collection rules for panel traffic, and every platform collection is
created with all five rules `null` (superuser-only).

Consequence to internalise: **records read through `core.App` are not filtered by
anything.** Authorisation is the panel's job, and the panel's job is done with
permission points and route guards.

---

## 2. Adding a permission point

Every guarded action needs a permission point. These are compile-time constants.

1. Add the constant to `internal/permission/codes.go`, with its `Point` entry in
   the same file (name, module, resource, action, type).
2. That is all. A seed migration mirrors the constants into the `permissions`
   collection, and a drift test asserts both directions.

Never write a permission string inline. If it is not a constant, it cannot be
granted and no test will notice.

---

## 3. Adding a migration

Migrations live in `migrations/`, named `<timestamp>_<module>.go`, and are
registered in `init()`:

```go
func init() {
    migrations.Register(func(app core.App) error {
        // up
    }, func(app core.App) error {
        // down
    }, "1700000020_storage.go")
}
```

**Timestamp order is folder order.** The existing files are
`1700000001_rbac` through `1700000010_seed`; a new module continues from
`1700000020` upward, one per module, with its seed work in the same file or a
sibling.

Helpers in `migrations/migrations.go` — use them rather than hand-rolling:

| Helper | Purpose |
| --- | --- |
| `findCollection(app, name)` | Look up, returning nil rather than an error |
| `collectionID(app, name)` | Resolve a relation target |
| `newBase(name)` | A base collection with id/timestamps, **already locked down** |
| `newAuth(name)` | An auth collection, **already locked down** |
| `lockDown(col)` | Set all five API rules to nil |
| `hasField(col, name)` | Idempotency check |
| `addField(col, field)` | Add if absent; returns whether it was added |
| `ensureTimestamps(col)` | id, created, updated |
| `text(name, required)` | Text field, sensible max |
| `selectOf(name, required, values...)` | Single select |
| `multiSelectOf(name, maxSelect, values...)` | Multi select. **maxSelect must
  be ≤ len(values)** |
| `rel(app, name, target, required)` | Single relation |
| `multiRel(app, name, target, required)` | Multi relation |
| `jsonField(name, maxSize)` | JSON field; **maxSize is int64** |
| `disableCascade(col, fields...)` | Turn off cascade delete on relations |

Rules to respect, each learned from a failure:

- **Migrations must be idempotent.** They re-run against databases that already
  have some of the schema. `findCollection` + `addField` give you this.
- **A migration that has already been applied is never edited.** Change it with a
  new migration. (During pre-release, adding to the newest migration is
  acceptable if nothing has been deployed; when in doubt, add a new file.)
- **No cascade delete unless you mean it.** The default throughout this codebase
  is `cascadeDelete: false`, and refusing a delete with a helpful message is
  preferred over silently removing depended-on rows.
- **A self-relation or a relation pointing at a collection created later in the
  same migration needs a second `app.Save(col)`** — declare the collection, save,
  then add the field. See `1700000001_rbac.go` (`menus.parent`) and
  `1700000004_crosslinks.go`.
- **Select fields need values.** A multi-select with no `Values` is rejected by
  PocketBase, and `maxSelect` greater than the number of values is rejected too.

---

## 4. Adding a screen: the resource layer

Most module screens are list/create/edit/delete over one collection. Do **not**
hand-write handlers for these. Declare a `resource` and let the shared layer do
it.

`internal/server/resource.go` defines the descriptor:

```go
type resource struct {
    Collection string   // PocketBase collection name
    BasePath   string   // URL prefix, e.g. "/storage/folders"
    Title      string   // plural label
    Singular   string   // one label
    Active     string   // sidebar menu name to highlight

    ListPerm   string   // permission constants; "" means the operation is not offered
    CreatePerm string
    UpdatePerm string
    DeletePerm string

    Order       string   // default sort
    Filter      string   // base filter, e.g. "deletedAt = ''"
    ParentField string   // tree resources
    TreeView    bool     // indent rows by depth
    TreePathField string // name of the materialised path column, if any
    DerivedFields []string // server-owned; excluded from the editor
    CountOf     *countSpec // read-only related count column
    ChildOf     string   // scope to ?parent=<id>
    HasDetail   bool     // there is a detail screen at BasePath/:id
    LabelField  string   // label when shown as a relation option
    RowActionPath, RowActionLabel, RowActionIcon string // per-row link

    BeforeSave   func(s *Server, rec *core.Record, isNew bool) error
    AfterSave    func(s *Server, rec *core.Record, isNew bool)
    BeforeDelete func(s *Server, rec *core.Record) error
}
```

Register it in `internal/server/resources*.go` and mount it:

```go
for _, r := range storageResources() {
    s.resourceRoutes(panel, r)
}
```

`resourceRoutes` registers only the operations whose permission is non-empty, and
routes each through the correct guard. You must then add navigation: a menu node
in the seed migration.

### Derived fields

A column the server computes — a materialised path, a cached total — goes in
`DerivedFields`. It is excluded from the editor **and** skipped when applying a
form submission. Without this, a crafted POST can rewrite it. This was a real
bug: `path` and `level` were ordinary text inputs, so re-parenting a department
could not find the subtree prefix.

### Customising the list

`countSpec` adds a related-record count column:

```go
CountOf: &countSpec{Collection: "files", Field: "folder", Label: "Files", Filter: "deletedAt = ''"},
```

### When you need more than CRUD

Write dedicated handlers in the module's own file, mounted through
`register<Module>Routes(panel)`. Use the resource layer for the collection's
table and form, and dedicated routes for the operations that are not CRUD —
approve, execute, publish, retry.

---

## 5. Route guards

Every route is registered through one of these. **There is no way to register a
state-changing route without a guard, and the server refuses to start if you try.**

| Helper | Use for |
| --- | --- |
| `s.safe(panel, GET, path, h)` | A read that any signed-in account may perform |
| `s.readGuarded(panel, GET, path, perm, h)` | A read that requires a permission |
| `s.guarded(panel, POST, path, perm, h)` | A state change requiring a permission |
| `s.superuserGuarded(panel, POST, path, h)` | Break-glass only, not delegated |
| `s.selfGuarded(panel, POST, path, h)` | Acts only on the caller's own data |

**Prefer `readGuarded` over `safe`.** Registering reads with `safe` was a real
bug: a signed-in account with one permission could open every list and every form
by URL. `safe` is for screens that genuinely need no permission, such as the
dashboard and the preferences screen.

The startup self-check audits unsafe methods only. A read registered with `safe`
is not caught, so this is a judgement call you must make deliberately.

---

## 6. Honouring data scope

If your collection carries a department relation or an owning account, the shared
scope helper already narrows list queries — `resourceList` calls
`rbac.ScopeFor(actor, collection)` and ANDs the result. Nothing to do beyond
naming the relation `department` or `user`, which is the convention it looks for.

Data scope narrows the rows within a granted capability. It does not grant or
withhold the capability.

---

## 7. Templates

Templates live in `web/templates/`:

- `layout.html` — the authenticated shell
- `layout_auth.html` — the signed-out shell (login, second factor)
- `partials/` — navbar, sidebar, flash
- `pages/` — one file per page

Register every page in `pageDefs` in `internal/server/render.go`:

```go
{name: "storage_folders.html", pageFile: "pages/storage_folders.html",
 layoutFile: "layout.html", layoutName: "layout"},
```

Conventions:

- Each page is `{{define "content"}}…{{end}}`. A reusable partial is a second
  top-level `{{define}}`. **A `{{define}}` may not be nested inside another** —
  it is a parse error, and the server fails to start with a message naming the
  file and line. (This is deliberate: a broken template is a boot failure, not a
  half-drawn page.)
- AdminLTE 4 classes: `.app-wrapper`, `.app-header`, `.app-sidebar`, `.app-main`,
  `.app-content`, `.app-content-header`, `.card`, `.card-body`, `.card-tools`,
  `.small-box`, `.small-box-icon`, `data-lte-toggle`.
  Removed since AdminLTE 3: `.small-box .icon`, `.elevation-*`, `.img-circle`,
  `.error-page`, `.chart`, `.btn-default`.
- `.Can "permission:code"` is available in templates and reads the signed-in
  actor's permissions. **Hide controls the actor may not use** rather than
  rendering a button that fails on submit. `.Can` is a method, not a field.
- Watch template argument grouping: `{{if .Can "x"}}` is correct,
  `{{not .X "arg"}}` is a parse-time error because `not` is being given two
  arguments — write `{{not (.X "arg")}}`.

---

## 8. Tests

Tests boot an isolated application with the platform's migrations applied:

```go
func TestSomething(t *testing.T) {
    app := newTestApp(t)   // defined in internal/rbac/rbac_test.go
    ...
}
```

If your module binds model hooks (see D-007 in `docs/decisions.md`), register them
in the test bootstrap too, or the test asserts behaviour production does not have.

What to test, in rough priority:

1. **Invariants that fail silently.** A permission check that can never be
   satisfied, a derived column a crafted POST can rewrite, a rule that admits
   everyone. These do not produce errors at runtime.
2. **Pure logic**, extracted so it does not need a database. The tree ordering
   algorithm and the TOTP implementation are both tested this way.
3. **Standard conformance**, where a standard exists. TOTP is tested against the
   RFC 6238 vectors rather than for self-consistency, because an implementation
   that computed the wrong function would still verify its own codes.

**Prove the test can fail.** A guard test that has only ever passed is not
evidence. Temporarily break the invariant, watch the test fail with a useful
message, then revert. This has been done for the API-surface guards and is
expected for new ones.

---

## 9. Verification before committing

A module is not done when it compiles.

```
source scripts/goenv.sh
gofmt -l $(find . -name '*.go' -not -path './.cache/*')
go vet ./...
go test ./...
go build -o bin/pocketadmin .
```

Then drive it against a live instance:

1. Fresh data directory; confirm the migrations apply with no error.
2. Create a superuser, sign in, and open **every** screen the module adds,
   including the create and edit forms. `200` for all of them.
3. Exercise the write paths with `curl`, including at least one refusal (a
   permission the actor lacks, a delete that should be blocked).
4. Check the log for `"level":"ERROR"` and for recovered panics, **and** for
   `errors` fields on 200 responses — a template error is reported there while
   still returning 200.
5. Confirm the mutating requests produced the records and audit entries you
   expect, by querying through the superuser API.

Things that have actually gone wrong here, so check them explicitly:

- A page returning 200 with a truncated body.
- A record saved with a derived field cleared.
- An audit entry silently rejected by an enum that lacks the value.
- A permission check that passes for the wrong reason.
- Two routes registered on the same path (the server panics at boot).
- A route registered without a guard (the server refuses to start).

Commit only after all of the above. One module per commit. **Never run
`git push`**; commits stay local for the user to push.

---

## 10. Documentation

- Add the module's screens and permission points to nothing — the permission
  constants are the catalogue.
- If you make a decision a reviewer would question, add an entry to
  `docs/decisions.md`: what, why, what was given up.
- Update `docs/features.md` only if the feature list itself was wrong.
