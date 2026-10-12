# PocketAdmin Platform — Database Design

Version: 0.1 (draft for review)
Engine: embedded PocketBase (SQLite via `modernc.org/sqlite`)
Companion documents: [`requirements.md`](requirements.md), [`design.md`](design.md)

## 1. Conventions

These rules apply to every collection. Deviations must be justified in the
collection's own section.

| Concern | Rule |
| --- | --- |
| Collection naming | `snake_case`, plural. Never starts with `_` — that prefix is reserved for PocketBase's own collections. |
| Field naming | `camelCase`, matching PocketBase's own system field style (`collectionId`, `emailVisibility`). |
| Identifiers | PocketBase 15-character random ids. No business keys as primary keys. |
| Timestamps | Every base collection defines `created` and `updated` autodate fields, unless noted. Stored UTC. |
| Soft delete | Collections that need history carry a nullable `deletedAt` date field. Every query must filter `deletedAt = ""`. Log and join collections hard-delete. |
| Money | Integer minor units (for example cents) plus an explicit `currency` text field. Never floating point. |
| Option sets | Enumerated values use `select` fields whose options are mirrored from `dictionaries` so the UI and the database agree. |
| Polymorphic links | Two nullable relation fields rather than a type/id text pair, so referential integrity is real. Exactly one is populated. |
| Secrets | Never stored in plaintext. Provider secrets use an encrypted `json` field; API keys store only a hash plus a searchable prefix. |
| Multi-value joins | PocketBase `relation` fields with `maxSelect > 1` rather than explicit join collections, except where the join carries its own attributes (see `message_recipients`, `attachments`). |
| Indexes | Declared per collection in the section below. PocketBase stores them as SQL expressions on the collection. |
| API rules | Locked to superuser-only (`null`) on every collection in this document. See § 4. |
| Cascade | Relations use `cascadeDelete: false` unless stated. Business deletion rules are enforced in service code so they can produce a helpful error. |

### 1.1 Person model (important)

Three distinct concepts exist and must not be conflated:

```
employees  ── the HR record. Organisation membership. May exist with no login.
admins     ── a backend login identity. Optionally linked to one employee.
users      ── a customer login identity. Optionally linked to one employee.
```

An administrator is not an employee, and an employee cannot sign in unless an
`admins` record links to them. This keeps HR data (which is retained after
resignation) separate from credentials (which are revoked immediately).

### 1.2 File model (important)

Two file concepts coexist:

- **Record-owned files** use native PocketBase `file` fields on the owning record
  (for example `employees.avatar`). PocketBase manages their lifecycle.
- **Library files** use the `files` collection (metadata) plus `attachments`
  (polymorphic links). Used by the file manager, rich-text uploads and any file
  that must outlive a single record.

A library file is never referenced through a native `file` field, and a
record-owned file never appears in the file manager.

## 2. Collection catalogue

| # | Collection | Type | Module | Purpose |
| --- | --- | --- | --- | --- |
| 1 | `admins` | auth | identity | Backend login identities |
| 2 | `admin_preferences` | base | identity | Per-administrator UI preferences |
| 3 | `admin_sessions` | base | identity | Revocable administrator sessions |
| 4 | `users` | auth | identity | Customer login identities |
| 5 | `user_preferences` | base | identity | Per-user UI preferences |
| 6 | `user_sessions` | base | identity | Revocable user sessions |
| 7 | `member_levels` | base | identity | Membership tier catalogue |
| 8 | `user_memberships` | base | identity | Membership history per user |
| 9 | `departments` | base | organization | Department tree |
| 10 | `positions` | base | organization | Positions within a department |
| 11 | `employees` | base | organization | HR records |
| 12 | `roles` | base | rbac | Roles and data scope |
| 13 | `permissions` | base | rbac | Atomic permission points |
| 14 | `menus` | base | rbac | Navigation tree |
| 15 | `dictionaries` | base | rbac | Dictionary types |
| 16 | `dictionary_items` | base | rbac | Dictionary entries |
| 17 | `file_folders` | base | storage | Folder tree |
| 18 | `files` | base | storage | Library file metadata |
| 19 | `attachments` | base | storage | Polymorphic file links |
| 20 | `todos` | base | workbench | Personal todos |
| 21 | `messages` | base | workbench | Message content |
| 22 | `message_recipients` | base | workbench | Per-recipient delivery and read state |
| 23 | `recent_visits` | base | workbench | Recently visited pages |
| 24 | `quick_links` | base | workbench | Workbench shortcut tiles |
| 25 | `announcements` | base | announcement | Announcements |
| 26 | `announcement_reads` | base | announcement | Per-reader read state |
| 27 | `scheduled_jobs` | base | jobs | Task scheduling metadata |
| 28 | `job_runs` | base | jobs | Execution history |
| 29 | `settings` | base | settings | Grouped configuration |
| 30 | `operation_logs` | base | audit | State-changing request log |
| 31 | `login_logs` | base | audit | Sign-in attempts |
| 32 | `security_audit_logs` | base | audit | Security events |
| 33 | `exception_logs` | base | audit | Application exceptions |
| 34 | `integration_providers` | base | integrations | Provider configuration |
| 35 | `integration_logs` | base | integrations | Outbound call log |
| 36 | `metric_snapshots` | base | monitoring | Periodic metric samples |
| 37 | `system_updates` | base | monitoring | Update check and apply history |
| 38 | `api_keys` | base | developer | Developer API credentials |

Retained PocketBase system collections: `_superusers` (break-glass), `_logs`
(PocketBase's own request/SQL log, auxiliary database), `_externalAuths`,
`_authOrigins`, `_mfas`, `_otps`.

`_authOrigins` and `_mfas` are available for MFA and trusted-device support; the
custom `*_sessions` collections are used instead of `_authOrigins` because the
panel needs explicit list-and-revoke over its own cookie sessions.

## 3. Collection definitions

Field tables list business fields only. `id`, `created`, `updated` are implied
by the conventions unless a `deletedAt` column is listed.

### 3.1 `identity`

#### `admins` — auth collection

| Field | Type | Notes |
| --- | --- | --- |
| `name` | text | Display name. Required. |
| `avatar` | file | Single image. |
| `email` | email | Auth identity. Unique. |
| `password` | password | Auth secret. |
| `tokenKey` | text | PocketBase auth token key. |
| `verified` | bool | PocketBase auth flag. |
| `emailVisibility` | bool | PocketBase auth flag. |
| `phone` | text | Unique when set. |
| `employee` | relation → `employees` (1) | Optional HR link. |
| `department` | relation → `departments` (1) | Denormalised from `employee` for fast data-scope filtering. Kept in sync by the service layer. |
| `position` | relation → `positions` (1) | Denormalised from `employee`. |
| `roles` | relation → `roles` (n) | Granted roles. |
| `status` | select | `active` \| `disabled` \| `locked`. |
| `isProtected` | bool | Protected accounts (`id = 1`-equivalent bootstrap admin) cannot be disabled or deleted. |
| `failedAttempts` | number | Consecutive failed sign-ins. Reset on success. |
| `lockedUntil` | date | Lockout expiry. |
| `lastLoginAt` | date | Updated on successful sign-in. |
| `lastLoginIp` | text | Updated on successful sign-in. |
| `passwordChangedAt` | date | Drives password-expiry policy. |
| `remarks` | text | Free-form note. |
| `deletedAt` | date | Soft delete. |

Indexes: `UNIQUE (email)`, `UNIQUE (phone) WHERE phone != ''`,
`(status)`, `(department)`.

#### `admin_preferences` — 1:1 with `admins`

| Field | Type | Notes |
| --- | --- | --- |
| `admin` | relation → `admins` (1) | Unique. |
| `theme` | select | `light` \| `dark` \| `auto`. |
| `language` | text | BCP-47 tag, for example `en` or `zh-CN`. |
| `pageSize` | number | Default list page size. |
| `sidebarCollapsed` | bool | |
| `timezone` | text | IANA name. |
| `notifyEmail` | bool | |
| `notifySms` | bool | |
| `extra` | json | Forward-compatible bag for future preferences. |

Indexes: `UNIQUE (admin)`.

#### `admin_sessions`

| Field | Type | Notes |
| --- | --- | --- |
| `admin` | relation → `admins` (1) | |
| `tokenHash` | text | SHA-256 of the session identifier. The raw value never reaches the database. |
| `ip` | text | |
| `userAgent` | text | |
| `device` | text | Parsed device label. |
| `location` | text | Optional geo lookup result. |
| `impersonated` | bool | True when the session was created by impersonation. |
| `lastSeenAt` | date | Slides forward on activity. |
| `expiresAt` | date | Hard expiry. |
| `revokedAt` | date | Set on explicit revocation or password reset. |
| `revokedReason` | text | `logout` \| `password_reset` \| `admin_revoked` \| `banned`. |

Indexes: `UNIQUE (tokenHash)`, `(admin, revokedAt)`, `(expiresAt)`.

#### `users` — auth collection

Same auth fields as `admins`, plus:

| Field | Type | Notes |
| --- | --- | --- |
| `nickname` | text | |
| `phone` | text | Unique when set. An alternative sign-in identifier. |
| `gender` | select | `unknown` \| `male` \| `female` \| `other`. |
| `birthday` | date | |
| `status` | select | `active` \| `inactive` \| `banned`. |
| `level` | relation → `member_levels` (1) | Current membership tier. |
| `levelExpireAt` | date | Denormalised from the active `user_memberships` row. |
| `tags` | select (multi) | Mirrored from the `user_tag` dictionary. |
| `source` | select | `web` \| `ios` \| `android` \| `admin` \| `import`. |
| `referrer` | text | |
| `registeredAt` | date | Distinct from `created` when importing legacy data. |
| `lastLoginAt` | date | |
| `lastLoginIp` | text | |
| `remarks` | text | |
| `deletedAt` | date | Soft delete. |

Indexes: `UNIQUE (email)`, `UNIQUE (phone) WHERE phone != ''`,
`(status)`, `(level)`, `(registeredAt)`.

#### `user_preferences`, `user_sessions`

Structurally identical to `admin_preferences` and `admin_sessions`, with the
relation pointing at `users` instead of `admins`.

#### `member_levels`

| Field | Type | Notes |
| --- | --- | --- |
| `name` | text | Display name. |
| `code` | text | Unique machine key. |
| `level` | number | Ordering; higher is better. |
| `icon` | file | |
| `price` | number | Minor units. |
| `currency` | text | ISO-4217. |
| `durationDays` | number | 0 means perpetual. |
| `benefits` | json | Array of `{ code, label, value }`. |
| `description` | editor | |
| `status` | select | `active` \| `disabled`. |
| `sortOrder` | number | |
| `isDefault` | bool | Granted on registration. Exactly one default. |

Indexes: `UNIQUE (code)`, `(level)`, `(status)`.

#### `user_memberships`

| Field | Type | Notes |
| --- | --- | --- |
| `user` | relation → `users` (1) | |
| `level` | relation → `member_levels` (1) | |
| `startAt` | date | |
| `expireAt` | date | Empty means perpetual. |
| `status` | select | `pending` \| `active` \| `expired` \| `cancelled` \| `refunded`. |
| `source` | select | `purchase` \| `gift` \| `trial` \| `admin` \| `promotion`. |
| `orderNo` | text | External order reference. |
| `autoRenew` | bool | |
| `grantedBy` | relation → `admins` (1) | Set when granted manually. |
| `remarks` | text | |

Indexes: `(user, status)`, `(expireAt)`, `(orderNo)`.

> If membership turns out to require orders and payments (open question OQ-1),
> an `orders` module is added and `user_memberships.orderNo` becomes a real
> relation. Nothing in the current design blocks that.

### 3.2 `organization`

#### `departments`

| Field | Type | Notes |
| --- | --- | --- |
| `name` | text | |
| `code` | text | Unique machine key. |
| `parent` | relation → `departments` (1) | Empty for a root department. |
| `path` | text | Materialised path, for example `/dep1/dep3/dep7/`. Maintained by the service layer. |
| `level` | number | Depth, root = 1. |
| `leader` | relation → `employees` (1) | |
| `phone` | text | |
| `email` | email | |
| `sortOrder` | number | Sibling ordering. |
| `status` | select | `active` \| `disabled`. |
| `description` | text | |
| `deletedAt` | date | Soft delete. |

Indexes: `UNIQUE (code)`, `(parent)`, `(path)`, `(status)`.

The materialised `path` makes "this department and all descendants" a single
`path ~ '/dep3/'` filter instead of a recursive walk. Re-parenting rewrites the
paths of all descendants inside one transaction (FR-organization-8).

#### `positions`

| Field | Type | Notes |
| --- | --- | --- |
| `name` | text | |
| `code` | text | Unique. |
| `department` | relation → `departments` (1) | |
| `level` | number | Seniority ordering. |
| `category` | select | Mirrored from the `position_category` dictionary. |
| `headcount` | number | 0 means unlimited. |
| `sortOrder` | number | |
| `status` | select | `active` \| `disabled`. |
| `description` | text | |
| `deletedAt` | date | Soft delete. |

Indexes: `UNIQUE (code)`, `(department)`, `(status)`.

#### `employees`

| Field | Type | Notes |
| --- | --- | --- |
| `name` | text | |
| `employeeNo` | text | Unique. |
| `avatar` | file | Record-owned. |
| `gender` | select | `unknown` \| `male` \| `female` \| `other`. |
| `phone` | text | |
| `email` | email | Work address; not a login identity. |
| `department` | relation → `departments` (1) | |
| `position` | relation → `positions` (1) | |
| `manager` | relation → `employees` (1) | Line manager. Must not create a cycle. |
| `hireDate` | date | |
| `leaveDate` | date | |
| `employmentType` | select | `fulltime` \| `parttime` \| `intern` \| `contractor`. |
| `status` | select | `active` \| `probation` \| `on_leave` \| `resigned`. |
| `adminAccount` | relation → `admins` (1) | Optional backend login. |
| `userAccount` | relation → `users` (1) | Optional customer login. |
| `sortOrder` | number | |
| `remarks` | text | |
| `deletedAt` | date | Soft delete. |

Indexes: `UNIQUE (employeeNo)`, `(department, status)`, `(position)`,
`(manager)`, `(adminAccount)`, `(userAccount)`.

### 3.3 `rbac`

#### `roles`

| Field | Type | Notes |
| --- | --- | --- |
| `name` | text | |
| `code` | text | Unique. Used as the stable identifier in code and tests. |
| `description` | text | |
| `dataScope` | select | `all` \| `department` \| `department_and_below` \| `self` \| `custom`. |
| `customDepartments` | relation → `departments` (n) | Used when `dataScope = custom`. |
| `permissions` | relation → `permissions` (n) | Granted permission points. |
| `menus` | relation → `menus` (n) | Granted menu nodes. |
| `status` | select | `active` \| `disabled`. |
| `sortOrder` | number | |
| `isSystem` | bool | System roles cannot be deleted or have their code changed. |
| `deletedAt` | date | Soft delete. |

Indexes: `UNIQUE (code)`, `(status)`.

#### `permissions`

| Field | Type | Notes |
| --- | --- | --- |
| `name` | text | Human label. |
| `code` | text | Unique. `<module>:<resource>:<action>`, for example `system:menu:create`. |
| `module` | text | Owning module id from `CAPABILITY-MAP.md`. |
| `resource` | text | Resource within the module. |
| `action` | select | `list` \| `view` \| `create` \| `update` \| `delete` \| `export` \| `import` \| `execute` \| `manage`. |
| `type` | select | `menu` \| `button` \| `api` \| `data`. |
| `description` | text | |
| `isSystem` | bool | Seeded permissions are protected. |
| `status` | select | `active` \| `disabled`. |

Indexes: `UNIQUE (code)`, `(module, resource)`, `(type)`.

#### `menus`

| Field | Type | Notes |
| --- | --- | --- |
| `title` | text | Display label. |
| `name` | text | Unique route name, used as the stable key in code. |
| `parent` | relation → `menus` (1) | Empty for a root node. |
| `type` | select | `directory` \| `menu` \| `button` \| `link`. |
| `platform` | select | `admin` \| `web`. Reserves the catalogue for a future public surface. |
| `path` | text | URL path for `menu` and `link`. |
| `component` | text | Reserved for a future SPA; unused by server-rendered pages. |
| `icon` | text | Font Awesome class. |
| `permissionCode` | text | Permission required to see and use this node. |
| `sortOrder` | number | |
| `visible` | bool | Hidden nodes exist but are not rendered in navigation. |
| `keepAlive` | bool | Reserved for a future SPA. |
| `external` | bool | |
| `link` | url | Target for `type = link`. |
| `status` | select | `active` \| `disabled`. |
| `isSystem` | bool | |

Indexes: `UNIQUE (name)`, `(parent)`, `(platform, status)`, `(permissionCode)`.

Buttons are menu nodes of `type = button` so that a single tree expresses both
navigation and in-page actions, and a role can be granted either.

#### `dictionaries` and `dictionary_items`

`dictionaries`: `name`, `code` (unique), `description`, `status`, `isSystem`.

`dictionary_items`:

| Field | Type | Notes |
| --- | --- | --- |
| `dictionary` | relation → `dictionaries` (1) | |
| `parent` | relation → `dictionary_items` (1) | Enables tree-shaped dictionaries. |
| `label` | text | Displayed. |
| `value` | text | Stored. |
| `sortOrder` | number | |
| `status` | select | `active` \| `disabled`. |
| `color` | text | Badge colour. |
| `cssClass` | text | |
| `isDefault` | bool | |
| `extra` | json | |

Indexes: `UNIQUE (dictionary, value)`, `(dictionary, sortOrder)`.

Seeded dictionary codes: `user_tag`, `position_category`, `announcement_type`,
`job_status`, `provider_type`, `log_level`, `gender`.

### 3.4 `storage`

#### `file_folders`

| Field | Type | Notes |
| --- | --- | --- |
| `name` | text | |
| `parent` | relation → `file_folders` (1) | |
| `path` | text | Materialised path. |
| `owner` | relation → `admins` (1) | |
| `scope` | select | `private` \| `public` \| `shared`. |
| `sortOrder` | number | |
| `deletedAt` | date | Soft delete. |

Indexes: `(parent)`, `(path)`, `(owner)`.

#### `files`

| Field | Type | Notes |
| --- | --- | --- |
| `name` | text | Original file name. |
| `storedName` | text | Name on the storage backend. |
| `folder` | relation → `file_folders` (1) | |
| `size` | number | Bytes. |
| `mimeType` | text | Sniffed, not client-supplied. |
| `extension` | text | |
| `checksum` | text | SHA-256, used for deduplication. |
| `storage` | select | `local` \| `s3` \| `oss` \| `cos` \| `minio`. |
| `provider` | relation → `integration_providers` (1) | Empty for local storage. |
| `url` | text | Resolved public or signed URL. |
| `thumbnail` | text | Resolved thumbnail URL for images. |
| `owner` | relation → `admins` (1) | |
| `refCount` | number | Number of `attachments` rows. Deletion is refused while positive. |
| `scope` | select | `private` \| `public` \| `shared`. |
| `status` | select | `active` \| `quarantined` \| `deleted`. |
| `tags` | select (multi) | |
| `deletedAt` | date | Soft delete. |

Indexes: `(folder, name)`, `(checksum)`, `(owner)`, `(status)`,
`(mimeType)`.

#### `attachments`

The join collection that carries its own attributes, so it is explicit rather
than a multi-relation.

| Field | Type | Notes |
| --- | --- | --- |
| `file` | relation → `files` (1) | |
| `ownerType` | text | Collection name, for example `announcements`. |
| `ownerId` | text | Record id within that collection. |
| `field` | text | Owning field, for example `attachments` or `body`. |
| `sortOrder` | number | |
| `createdBy` | relation → `admins` (1) | |

Indexes: `(ownerType, ownerId)`, `(file)`, `UNIQUE (file, ownerType, ownerId, field)`.

### 3.5 `workbench`

#### `todos`

| Field | Type | Notes |
| --- | --- | --- |
| `title` | text | |
| `description` | editor | |
| `owner` | relation → `admins` (1) | |
| `status` | select | `pending` \| `in_progress` \| `done` \| `cancelled`. |
| `priority` | select | `low` \| `normal` \| `high` \| `urgent`. |
| `dueAt` | date | |
| `remindAt` | date | Consumed by a `jobs` task. |
| `completedAt` | date | |
| `relatedType` | text | Optional deep link target. |
| `relatedId` | text | |
| `tags` | select (multi) | |
| `sortOrder` | number | |

Indexes: `(owner, status)`, `(dueAt)`, `(remindAt)`.

#### `messages` and `message_recipients`

`messages`: `title`, `content` (editor), `type` (select: `system` \| `notice` \|
`todo` \| `announcement`), `level` (select: `info` \| `success` \| `warning` \|
`error`), `sender` (relation → `admins`, empty means system), `link` (text),
`audience` (select: `direct` \| `role` \| `department` \| `all`).

`message_recipients`:

| Field | Type | Notes |
| --- | --- | --- |
| `message` | relation → `messages` (1) | |
| `admin` | relation → `admins` (1) | |
| `readAt` | date | Empty means unread. |
| `deletedAt` | date | Per-recipient delete. |

Indexes: `UNIQUE (message, admin)`, `(admin, readAt)`.

Splitting content from delivery keeps a broadcast to 500 administrators at one
message row plus 500 small recipient rows, and gives each recipient independent
read and delete state.

#### `recent_visits`

| Field | Type | Notes |
| --- | --- | --- |
| `admin` | relation → `admins` (1) | |
| `title` | text | |
| `path` | text | |
| `icon` | text | |
| `visitCount` | number | |
| `visitedAt` | date | Last visit; drives ordering and pruning. |

Indexes: `UNIQUE (admin, path)`, `(admin, visitedAt)`.

#### `quick_links`

| Field | Type | Notes |
| --- | --- | --- |
| `admin` | relation → `admins` (1) | Empty means a global default offered to new administrators. |
| `title` | text | |
| `path` | text | |
| `icon` | text | |
| `color` | text | |
| `permissionCode` | text | Hidden when the actor lacks it. |
| `sortOrder` | number | |

Indexes: `(admin, sortOrder)`, `(path)`.

### 3.6 `announcement`

#### `announcements`

| Field | Type | Notes |
| --- | --- | --- |
| `title` | text | |
| `content` | editor | |
| `type` | select | `notice` \| `announcement` \| `maintenance` \| `update`. |
| `level` | select | `normal` \| `important` \| `urgent`. |
| `status` | select | `draft` \| `scheduled` \| `published` \| `archived` \| `expired`. |
| `publishAt` | date | |
| `expireAt` | date | |
| `author` | relation → `admins` (1) | |
| `audience` | select | `all` \| `admins` \| `users` \| `departments`. |
| `departments` | relation → `departments` (n) | Used when `audience = departments`. |
| `pinned` | bool | |
| `readCount` | number | Denormalised counter. |
| `deletedAt` | date | Soft delete. |

Indexes: `(status, publishAt)`, `(expireAt)`, `(pinned, publishAt)`,
`(audience)`.

#### `announcement_reads`

| Field | Type | Notes |
| --- | --- | --- |
| `announcement` | relation → `announcements` (1) | |
| `readerType` | select | `admin` \| `user`. |
| `readerId` | text | Paired with the type; see the polymorphic note in § 1. Here a text id is used because the two reader collections are read-only from this table's perspective and the volume is high. |
| `readAt` | date | |

Indexes: `UNIQUE (announcement, readerType, readerId)`, `(readerType, readerId)`.

### 3.7 `jobs`

#### `scheduled_jobs`

| Field | Type | Notes |
| --- | --- | --- |
| `name` | text | |
| `code` | text | Unique. Must match a handler registered in code. |
| `description` | text | |
| `cron` | text | Five- or six-field expression. Validated on write. |
| `params` | json | Passed to the handler. |
| `status` | select | `enabled` \| `disabled` \| `paused`. |
| `concurrency` | select | `allow` \| `forbid` \| `replace`. |
| `timeoutSeconds` | number | 0 means no timeout. |
| `maxRetries` | number | |
| `retryBackoffSeconds` | number | |
| `notifyOnFailure` | bool | |
| `notifyTargets` | relation → `admins` (n) | |
| `lastRunAt` | date | |
| `lastStatus` | select | Mirror of the most recent `job_runs.status`. |
| `nextRunAt` | date | Computed after each run. |
| `createdBy` | relation → `admins` (1) | |

Indexes: `UNIQUE (code)`, `(status)`, `(nextRunAt)`.

#### `job_runs`

| Field | Type | Notes |
| --- | --- | --- |
| `job` | relation → `scheduled_jobs` (1) | |
| `startedAt` | date | |
| `finishedAt` | date | |
| `durationMs` | number | |
| `status` | select | `running` \| `success` \| `failed` \| `timeout` \| `cancelled` \| `skipped`. |
| `triggeredBy` | select | `cron` \| `manual` \| `retry` \| `api`. |
| `triggeredByAdmin` | relation → `admins` (1) | Set for manual runs. |
| `attempt` | number | 1-based retry attempt. |
| `output` | text | Captured handler output, truncated. |
| `error` | text | |

Indexes: `(job, startedAt)`, `(status)`, `(startedAt)`.

### 3.8 `settings`

#### `settings`

| Field | Type | Notes |
| --- | --- | --- |
| `category` | select | `site` \| `auth` \| `registration` \| `announcement` \| `feature`. Named `category` rather than `group` to avoid a SQL keyword. |
| `key` | text | Unique within a category. |
| `value` | json | Typed payload; the shape is validated against the registry entry. |
| `valueType` | select | `string` \| `number` \| `bool` \| `json` \| `file` \| `select`. |
| `description` | text | Shown as help text in the settings form. |
| `isPublic` | bool | Readable without authentication. |
| `isSystem` | bool | Cannot be deleted from the UI. |
| `updatedBy` | relation → `admins` (1) | |

Indexes: `UNIQUE (category, key)`, `(isPublic)`.

The key registry is defined in code (`internal/settings/registry.go`) so that
every key has a declared type, default and validator. A row without a registry
entry is ignored, which lets a migration add a key without breaking an older
binary.

Seeded keys:

| Category | Keys |
| --- | --- |
| `site` | `siteName`, `siteUrl`, `logo`, `favicon`, `description`, `keywords`, `icp`, `copyright`, `timezone`, `language` |
| `auth` | `captchaEnabled`, `maxLoginAttempts`, `lockoutMinutes`, `passwordMinLength`, `passwordRequireClasses`, `passwordExpiryDays`, `sessionTimeoutMinutes`, `mfaRequired`, `ipAllowList` |
| `registration` | `enabled`, `method`, `requireVerification`, `defaultRole`, `defaultLevel`, `agreementRequired` |
| `announcement` | `enabled`, `position`, `displayMode`, `dismissible` |
| `feature` | `files.enabled`, `workbench.enabled`, `jobs.enabled`, `integrations.enabled`, `developer.enabled`, `apiKeys.enabled`, `maintenanceMode`, `maintenanceMessage` |

### 3.9 `audit`

All four collections are append-only through the application and are pruned by
retention jobs.

#### `operation_logs`

| Field | Type | Notes |
| --- | --- | --- |
| `admin` | relation → `admins` (1) | Empty for unauthenticated or API-key requests. |
| `apiKey` | relation → `api_keys` (1) | Set when the actor used an API key. |
| `module` | text | |
| `action` | text | |
| `method` | text | |
| `path` | text | |
| `params` | json | Redacted. |
| `result` | select | `success` \| `failure`. |
| `statusCode` | number | |
| `errorMessage` | text | |
| `targetType` | text | |
| `targetId` | text | |
| `durationMs` | number | |
| `ip` | text | |
| `userAgent` | text | |
| `requestId` | text | Correlates with `exception_logs` and PocketBase's `_logs`. |

Indexes: `(admin, created)`, `(module, action)`, `(created)`, `(requestId)`,
`(result)`.

#### `login_logs`

| Field | Type | Notes |
| --- | --- | --- |
| `actorType` | select | `admin` \| `user`. |
| `admin` | relation → `admins` (1) | Exactly one of `admin`/`user` is set. |
| `user` | relation → `users` (1) | |
| `identifier` | text | The submitted identifier, retained even when no account matched. |
| `ip` | text | |
| `userAgent` | text | |
| `device` | text | |
| `location` | text | |
| `result` | select | `success` \| `failure` \| `locked` \| `captcha_error` \| `mfa_required` \| `mfa_failed`. |
| `reason` | text | |
| `riskLevel` | select | `low` \| `medium` \| `high`. |

Indexes: `(actorType, created)`, `(admin, created)`, `(user, created)`,
`(identifier)`, `(ip)`, `(result)`.

#### `security_audit_logs`

| Field | Type | Notes |
| --- | --- | --- |
| `event` | select | `permission_denied` \| `password_changed` \| `password_reset` \| `role_changed` \| `account_locked` \| `session_revoked` \| `settings_changed` \| `api_key_created` \| `api_key_revoked` \| `data_exported` \| `impersonation_started` \| `login_anomaly`. |
| `severity` | select | `info` \| `warning` \| `critical`. |
| `actorType` | select | `admin` \| `user` \| `system` \| `api_key`. |
| `admin` | relation → `admins` (1) | |
| `user` | relation → `users` (1) | |
| `targetType` | text | |
| `targetId` | text | |
| `detail` | json | Old and new values for configuration changes. |
| `ip` | text | |
| `userAgent` | text | |
| `requestId` | text | |

Indexes: `(event, created)`, `(severity, created)`, `(admin, created)`,
`(actorType)`.

#### `exception_logs`

| Field | Type | Notes |
| --- | --- | --- |
| `level` | select | `error` \| `fatal` \| `panic`. |
| `message` | text | |
| `stack` | text | Truncated stack trace. |
| `module` | text | |
| `path` | text | |
| `method` | text | |
| `requestId` | text | |
| `params` | json | Redacted. |
| `admin` | relation → `admins` (1) | |
| `ip` | text | |
| `userAgent` | text | |
| `resolved` | bool | |
| `resolvedBy` | relation → `admins` (1) | |
| `resolvedAt` | date | |
| `note` | text | |

Indexes: `(level, created)`, `(resolved, created)`, `(requestId)`, `(module)`.

### 3.10 `integrations`

#### `integration_providers`

| Field | Type | Notes |
| --- | --- | --- |
| `name` | text | |
| `code` | text | Unique. Referenced by `settings` and `files.provider`. |
| `type` | select | `email` \| `sms` \| `storage` \| `idp` \| `webhook`. |
| `driver` | select | `smtp` \| `sendgrid` \| `ses` \| `aliyun_sms` \| `tencent_sms` \| `twilio` \| `local` \| `s3` \| `oss` \| `cos` \| `minio` \| `oauth2` \| `oidc` \| `ldap` \| `http`. |
| `config` | json | Non-secret settings (host, port, region, bucket, client id, …). |
| `secrets` | json | Encrypted at rest. Never serialised to the browser. |
| `status` | select | `enabled` \| `disabled` \| `testing` \| `error`. |
| `isDefault` | bool | At most one default per `type`. Enforced in a transaction. |
| `priority` | number | Fallback ordering. |
| `lastTestAt` | date | |
| `lastTestResult` | select | `success` \| `failure` \| `untested`. |
| `lastTestMessage` | text | |
| `description` | text | |
| `createdBy` | relation → `admins` (1) | |
| `deletedAt` | date | Soft delete. |

Indexes: `UNIQUE (code)`, `(type, isDefault)`, `(status)`.

#### `integration_logs`

| Field | Type | Notes |
| --- | --- | --- |
| `provider` | relation → `integration_providers` (1) | |
| `channel` | select | `email` \| `sms` \| `storage` \| `auth` \| `webhook`. |
| `action` | text | For example `send`, `upload`, `token_exchange`. |
| `target` | text | Recipient address or object key. |
| `request` | json | Redacted. |
| `response` | json | Redacted and truncated. |
| `status` | select | `success` \| `failure`. |
| `error` | text | |
| `durationMs` | number | |

Indexes: `(provider, created)`, `(channel, status)`, `(created)`.

### 3.11 `monitoring`

#### `metric_snapshots`

Optional; enabled when trend charts are wanted.

| Field | Type | Notes |
| --- | --- | --- |
| `capturedAt` | date | |
| `cpuPercent` | number | |
| `memoryBytes` | number | |
| `goroutines` | number | |
| `heapBytes` | number | |
| `dbBytes` | number | |
| `auxDbBytes` | number | |
| `fileBytes` | number | |
| `fileCount` | number | |
| `requestsPerMinute` | number | |
| `errorRate` | number | Percent. |
| `latencyP95Ms` | number | |

Indexes: `(capturedAt)`.

#### `system_updates`

| Field | Type | Notes |
| --- | --- | --- |
| `version` | text | Target version. |
| `fromVersion` | text | |
| `channel` | select | `stable` \| `beta`. |
| `status` | select | `available` \| `downloading` \| `ready` \| `applied` \| `failed` \| `rolled_back`. |
| `releaseNotes` | editor | |
| `publishedAt` | date | |
| `appliedAt` | date | |
| `appliedBy` | relation → `admins` (1) | |
| `checksum` | text | |
| `error` | text | |

Indexes: `(status)`, `(version)`.

### 3.12 `developer`

#### `api_keys`

| Field | Type | Notes |
| --- | --- | --- |
| `name` | text | |
| `keyPrefix` | text | First 8 characters, stored in clear for lookup and display (`pk_live_a1b2c3d4…`). |
| `keyHash` | text | SHA-256 of the full key. The key itself is never stored. |
| `owner` | relation → `admins` (1) | |
| `scopes` | relation → `permissions` (n) | Intersected with the owner's live permissions. |
| `rateLimitPerMinute` | number | 0 means the global default. |
| `ipAllowList` | text | Comma-separated CIDR list. Empty means any. |
| `status` | select | `active` \| `revoked` \| `expired`. |
| `expiresAt` | date | |
| `lastUsedAt` | date | |
| `lastUsedIp` | text | |
| `usageCount` | number | |
| `revokedAt` | date | |
| `revokedBy` | relation → `admins` (1) | |

Indexes: `UNIQUE (keyPrefix)`, `(keyHash)`, `(owner)`, `(status)`.

## 4. API rules and the security boundary

Every collection in § 3 is created with all five rules set to `null`
(superuser only):

```
listRule = viewRule = createRule = updateRule = deleteRule = null
```

This is deliberate. Two access paths exist:

1. **The panel** renders through Gin handlers that call `core.App` directly.
   PocketBase does not evaluate collection rules on that path, so the rules are
   irrelevant to it — **all authorisation is the Gin middleware's job**
   (see `design.md` § 4).
2. **The public `/api` surface** is served by PocketBase and *does* evaluate the
   rules. With `null` rules it returns 404/403 to everyone except superusers,
   which is exactly what is wanted: no collection is exposed to untrusted
   clients by accident.

Collections that must be readable without authentication (`siteName`, `logo`)
are exposed through a purpose-built Gin endpoint that reads specific public
settings, never by loosening a collection rule.

## 5. Relationship overview

```
departments ─┬─< departments (self, tree)
             ├─< positions
             └─< employees ──┬─< employees (self, manager)
                             ├──> admins   (optional login)
                             └──> users    (optional login)

roles ──< permissions          (m2m, relation field)
roles ──< menus                (m2m, relation field)
menus ──< menus                (self, tree)
roles ──< departments          (m2m, custom data scope)

admins ─┬─< admin_sessions
        ├─< admin_preferences (1:1)
        ├─< todos
        ├─< quick_links
        ├─< recent_visits
        ├─< message_recipients ──> messages
        ├─< operation_logs
        └─< api_keys ──< permissions (m2m scopes)

users ─┬─< user_sessions
       ├─< user_preferences (1:1)
       ├─< user_memberships ──> member_levels
       └─< login_logs

file_folders ──< file_folders (self, tree)
file_folders ──< files ──< attachments >── (announcements, any owner)
files ──> integration_providers (storage)

integration_providers ──< integration_logs
scheduled_jobs ──< job_runs
announcements ──< announcement_reads
```

## 6. Migration strategy

- Schema lives in Go migrations under `migrations/`, registered with
  `migrations.Register` and applied by `app.RunAllMigrations()` on boot. No
  manual Dashboard edits in any environment above local development.
- One migration file per module, numbered by wave:
  `1700000001_identity.go`, `1700000002_organization.go`, `1700000003_rbac.go`, …
- A migration is **idempotent**: it checks for the collection before creating it,
  so a partially applied deployment can be re-run safely.
- Seed data (permission points, menu tree, dictionary entries, setting defaults,
  the bootstrap role) ships in separate `*_seed.go` migrations so that schema
  and content can be reasoned about independently.
- Destructive changes (dropping a field or collection) require a two-step
  release: stop writing in release N, remove in release N+1.

## 7. Seed data summary

| Collection | Seeded rows |
| --- | --- |
| `permissions` | One row per `<module>:<resource>:<action>` across all 12 modules |
| `menus` | The full navigation tree from the feature request, with button nodes for in-page actions |
| `roles` | `super_admin` (all permissions, `dataScope = all`, `isSystem`), `admin` (all except developer and system settings), `operator` (day-to-day operational permissions) |
| `dictionaries` | `user_tag`, `position_category`, `announcement_type`, `job_status`, `provider_type`, `log_level`, `gender` |
| `dictionary_items` | Entries for each seeded dictionary |
| `settings` | Every key from § 3.8 with its default value |
| `member_levels` | `free` (default, level 0) |
| `scheduled_jobs` | The built-in task catalogue from FR-jobs-10, all `disabled` except retention jobs |
| `quick_links` | Global defaults for a new administrator |
| `departments` | A single root department |
| `admins` | One bootstrap administrator, `isProtected = true` |
