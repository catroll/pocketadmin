# PocketAdmin Platform — Database Design

Version: 0.2 (revision 2 — draft for review)
Engine: embedded PocketBase (SQLite via `modernc.org/sqlite`)
Companion documents: [`requirements.md`](requirements.md), [`design.md`](design.md)

## 1. Conventions

| Concern | Rule |
| --- | --- |
| Collection naming | `snake_case`, plural. Never starts with `_` — that prefix is reserved for PocketBase's own collections. |
| Field naming | `camelCase`, matching PocketBase's own system field style (`collectionId`, `emailVisibility`). |
| Identifiers | PocketBase 15-character random ids. No business keys as primary keys. |
| Timestamps | Every base collection defines `created` and `updated` autodate fields unless noted. Stored UTC. |
| Soft delete | Collections that need history carry a nullable `deletedAt` date field; every query filters `deletedAt = ""`. Log, ledger and history collections hard-delete. |
| Money | Integer minor units (cents) plus an explicit `currency` text field. Never floating point. |
| Option sets | Enumerated values use `select` fields whose options mirror `dictionaries` entries. |
| Polymorphic links | Two nullable relation fields rather than a type/id text pair, so referential integrity is real. Exactly one is populated. |
| Secrets | Never plaintext. Provider secrets use an encrypted `json` field; API keys store only a hash plus a searchable prefix. |
| Multi-value joins | PocketBase `relation` fields with `maxSelect > 1`, except where the join carries its own attributes (see `message_recipients`, `attachments`, `coupon_grants`). |
| Indexes | Declared per collection below. PocketBase stores them as SQL expressions on the collection. |
| API rules | Superuser-only (`null`) on every collection in this document. See § 4. |
| Cascade | `cascadeDelete: false` unless stated. Business deletion rules live in service code so they can produce a helpful error. |
| Money-bearing state | Order, payment, refund and subscription transitions are never hard-deleted; they are reversed by a compensating record. |

### 1.1 Person model — users-centric (important)

There is exactly one account per person: **`users`**, which is PocketBase's own
built-in auth collection, extended with the fields this platform needs. Two
optional 1:1 profile extensions give that account additional meaning:

```
                    users (auth)  ── credentials, status, shared profile
                         │
        ┌────────────────┴────────────────┐
        ▼                                 ▼
  employees (1:1, optional)        admins (1:1, optional)
  HR / organisation membership     back-office access + roles
```

- A user with no profile is a plain customer.
- A user with an `employees` profile is staff.
- A user with an `admins` profile may sign in to the back office.
- A user may hold both profiles; one set of credentials then serves both surfaces.

Consequences that shape the schema:

- **Only `users` is an auth collection.** `employees` and `admins` are base
  collections with a unique relation back to `users`. They hold no password.
- Detaching an `employees` profile retains the account and the HR history.
  Detaching an `admins` profile immediately bars back-office access without
  deleting the person.
- Sessions, sign-in history and preferences are keyed by account and
  discriminated by a `context` field (`admin` | `product`). "Administrator
  sessions" and "user sessions" are therefore two filtered views of one table,
  not two tables that must be kept in sync.
- PocketBase seeds `users` with owner-scoped API rules (public create, owner
  read/update/delete). Those rules are **replaced** with superuser-only, because
  this platform does not expose a public sign-up endpoint. Registration happens
  through a panel-controlled service or a purpose-built API.

### 1.2 File model (important)

- **Record-owned files** use native PocketBase `file` fields on the owning record
  (for example `users.avatar`, `products.cover`). PocketBase manages their
  lifecycle.
- **Library files** use the `files` collection (metadata) plus `attachments`
  (polymorphic links). Used by the file manager, rich-text uploads and any file
  that must outlive one record.

A library file is never referenced through a native `file` field, and a
record-owned file never appears in the file manager.

## 2. Collection catalogue

55 collections: 34 platform, 21 business.

### Platform

| # | Collection | Type | Module | Purpose |
| --- | --- | --- | --- | --- |
| 1 | `users` | auth | identity | The single account (PocketBase built-in, extended) |
| 2 | `employees` | base | identity | HR profile extension, 1:1 with a user |
| 3 | `admins` | base | identity | Back-office profile extension, 1:1 with a user |
| 4 | `user_preferences` | base | identity | Per-account, per-surface preferences |
| 5 | `sessions` | base | identity | Revocable sessions, per account and surface |
| 6 | `departments` | base | organization | Department tree |
| 7 | `positions` | base | organization | Positions within a department |
| 8 | `roles` | base | rbac | Roles and data scope |
| 9 | `permissions` | base | rbac | Atomic permission points |
| 10 | `menus` | base | rbac | Navigation tree |
| 11 | `dictionaries` | base | rbac | Dictionary types |
| 12 | `dictionary_items` | base | rbac | Dictionary entries |
| 13 | `file_folders` | base | storage | Folder tree |
| 14 | `files` | base | storage | Library file metadata |
| 15 | `attachments` | base | storage | Polymorphic file links |
| 16 | `todos` | base | workbench | Personal todos |
| 17 | `messages` | base | workbench | Message content |
| 18 | `message_recipients` | base | workbench | Per-recipient delivery and read state |
| 19 | `recent_visits` | base | workbench | Recently visited pages |
| 20 | `quick_links` | base | workbench | Workbench shortcut tiles |
| 21 | `announcements` | base | announcement | Announcements |
| 22 | `announcement_reads` | base | announcement | Per-reader read state |
| 23 | `scheduled_jobs` | base | jobs | Task scheduling metadata |
| 24 | `job_runs` | base | jobs | Execution history |
| 25 | `settings` | base | settings-admin, settings-product | Grouped configuration, discriminated by `surface` |
| 26 | `operation_logs` | base | audit | State-changing request log |
| 27 | `login_logs` | base | audit | Sign-in attempts, both surfaces |
| 28 | `security_audit_logs` | base | audit | Security events |
| 29 | `exception_logs` | base | audit | Application exceptions |
| 30 | `integration_providers` | base | integrations | Provider configuration |
| 31 | `integration_logs` | base | integrations | Outbound call log |
| 32 | `metric_snapshots` | base | monitoring | Periodic metric samples |
| 33 | `system_updates` | base | monitoring | Update check and apply history |
| 34 | `api_keys` | base | developer | Developer API credentials |

### Business

| # | Collection | Type | Module | Purpose |
| --- | --- | --- | --- | --- |
| 35 | `product_categories` | base | catalog | Category tree |
| 36 | `products` | base | catalog | Products, including membership and subscription plans |
| 37 | `product_attributes` | base | catalog | Variant attributes and their values |
| 38 | `product_variants` | base | catalog | SKUs |
| 39 | `stock_ledger` | base | catalog | Append-only stock movements |
| 40 | `member_tiers` | base | membership | Membership tier catalogue |
| 41 | `member_tier_benefits` | base | membership | Typed, queryable tier benefits |
| 42 | `member_plans` | base | membership | Purchasable plans that grant a tier |
| 43 | `user_memberships` | base | membership | Membership history per account |
| 44 | `coupons` | base | promotions | Coupon templates |
| 45 | `coupon_grants` | base | promotions | Issued coupon instances |
| 46 | `campaigns` | base | promotions | Scheduled groups of coupons |
| 47 | `subscription_plans` | base | subscriptions | Recurring billing plans |
| 48 | `subscriptions` | base | subscriptions | Per-account subscriptions |
| 49 | `subscription_cycles` | base | subscriptions | Per-period billing history |
| 50 | `orders` | base | orders | Order aggregate |
| 51 | `order_items` | base | orders | Order line snapshots |
| 52 | `order_events` | base | orders | Order timeline |
| 53 | `payment_intents` | base | payments | Amount to collect per order |
| 54 | `payment_transactions` | base | payments | Individual payment attempts |
| 55 | `refunds` | base | refunds | Refund requests and execution |

Retained PocketBase system collections: `_superusers` (break-glass),
`_logs` (PocketBase's own request/SQL log, auxiliary database),
`_externalAuths`, `_authOrigins`, `_mfas`, `_otps`.

`_externalAuths` is used for OAuth identities; `_mfas` and `_otps` back MFA. The
custom `sessions` collection replaces `_authOrigins` because the panel needs
explicit list-and-revoke over its own cookie sessions with a surface
discriminator.

## 3. Platform collections

Field tables list business fields only; `id`, `created`, `updated` are implied.

### 3.1 `identity`

#### `users` — auth collection (PocketBase built-in, extended)

Inherited from PocketBase: `email`, `password`, `tokenKey`, `verified`,
`emailVisibility`, `created`, `updated`.

Added by this platform:

| Field | Type | Notes |
| --- | --- | --- |
| `name` | text | PocketBase already defines this. Display name. |
| `nickname` | text | |
| `avatar` | file | PocketBase already defines this. Record-owned. |
| `phone` | text | Unique when set. An alternative identifier. |
| `gender` | select | `unknown` \| `male` \| `female` \| `other`. |
| `birthday` | date | |
| `status` | select | `active` \| `inactive` \| `banned`. |
| `tags` | select (multi) | Mirrors the `user_tag` dictionary. |
| `source` | select | `web` \| `ios` \| `android` \| `admin` \| `import`. |
| `referrer` | text | |
| `registeredAt` | date | Distinct from `created` when importing legacy data. |
| `invitationCode` | text | Set when registration was invitation-only. |
| `failedAttempts` | number | Consecutive failed sign-ins. Reset on success. |
| `lockedUntil` | date | Lockout expiry. |
| `lastLoginAt` | date | Any surface. |
| `lastLoginIp` | text | |
| `passwordChangedAt` | date | Drives the password-expiry policy. |
| `remarks` | text | |
| `deletedAt` | date | Soft delete. |

Indexes: `UNIQUE (email)`, `UNIQUE (phone) WHERE phone != ''`, `(status)`,
`(registeredAt)`, `(deletedAt)`.

API rules: all five set to `null` (superuser-only), replacing PocketBase's
owner-scoped defaults.

#### `employees` — 1:1 profile extension

| Field | Type | Notes |
| --- | --- | --- |
| `user` | relation → `users` (1) | **Unique.** The account this profile extends. |
| `employeeNo` | text | Unique. |
| `department` | relation → `departments` (1) | |
| `position` | relation → `positions` (1) | |
| `manager` | relation → `employees` (1) | Line manager. Must not create a cycle. |
| `hireDate` | date | |
| `leaveDate` | date | |
| `employmentType` | select | `fulltime` \| `parttime` \| `intern` \| `contractor`. |
| `status` | select | `probation` \| `active` \| `on_leave` \| `resigned`. |
| `workEmail` | email | Distinct from the account's sign-in email. |
| `workPhone` | text | |
| `remarks` | text | |
| `deletedAt` | date | Soft delete; retains HR history. |

Indexes: `UNIQUE (user)`, `UNIQUE (employeeNo)`,
`(department, status)`, `(position)`, `(manager)`.

#### `admins` — 1:1 profile extension

| Field | Type | Notes |
| --- | --- | --- |
| `user` | relation → `users` (1) | **Unique.** The account this profile extends. |
| `roles` | relation → `roles` (n) | Granted roles. |
| `department` | relation → `departments` (1) | Denormalised from the employee profile for fast data-scope filtering; kept in sync by the service layer. Empty when the account has no employee profile. |
| `status` | select | `active` \| `disabled` \| `locked`. |
| `isProtected` | bool | The bootstrap administrator cannot be detached or disabled. |
| `mfaRequired` | bool | Overrides the global setting for this administrator. |
| `lastAdminLoginAt` | date | Back-office sign-in only; distinct from the account's `lastLoginAt`. |
| `lastAdminLoginIp` | text | |
| `remarks` | text | |
| `deletedAt` | date | Soft delete. |

Indexes: `UNIQUE (user)`, `(status)`, `(department)`.

> **Why no password here.** Credentials belong to the account, so a person has
> one password, one reset flow and one set of sessions. The admin profile grants
> *authority*, not identity.

#### `user_preferences`

| Field | Type | Notes |
| --- | --- | --- |
| `user` | relation → `users` (1) | |
| `context` | select | `admin` \| `product`. An administrator may want different preferences in each surface. |
| `theme` | select | `light` \| `dark` \| `auto`. |
| `language` | text | BCP-47 tag, for example `en` or `zh-CN`. |
| `pageSize` | number | Default list page size. |
| `sidebarCollapsed` | bool | |
| `timezone` | text | IANA name. |
| `notifyEmail` | bool | |
| `notifySms` | bool | |
| `extra` | json | Forward-compatible bag. |

Indexes: `UNIQUE (user, context)`.

#### `sessions`

| Field | Type | Notes |
| --- | --- | --- |
| `user` | relation → `users` (1) | |
| `context` | select | `admin` \| `product` \| `api`. |
| `tokenHash` | text | SHA-256 of the session identifier. The raw value never reaches the database. |
| `ip` | text | |
| `userAgent` | text | |
| `device` | text | Parsed device label. |
| `location` | text | Optional geo lookup result. |
| `impersonated` | bool | True when created by impersonation. |
| `impersonatedBy` | relation → `users` (1) | The administrator who started it. |
| `lastSeenAt` | date | Slides forward on activity, at most once per minute. |
| `expiresAt` | date | Hard expiry. |
| `revokedAt` | date | |
| `revokedReason` | text | `logout` \| `password_reset` \| `admin_revoked` \| `banned` \| `profile_detached`. |

Indexes: `UNIQUE (tokenHash)`, `(user, context, revokedAt)`, `(expiresAt)`,
`(context)`.

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
| `phone`, `email` | text, email | |
| `sortOrder` | number | |
| `status` | select | `active` \| `disabled`. |
| `description` | text | |
| `deletedAt` | date | Soft delete. |

Indexes: `UNIQUE (code)`, `(parent)`, `(path)`, `(status)`.

#### `positions`

| Field | Type | Notes |
| --- | --- | --- |
| `name`, `code` | text | `code` unique. |
| `department` | relation → `departments` (1) | |
| `level` | number | Seniority ordering. |
| `category` | select | Mirrors the `position_category` dictionary. |
| `headcount` | number | 0 means unlimited. |
| `sortOrder` | number | |
| `status` | select | `active` \| `disabled`. |
| `deletedAt` | date | Soft delete. |

Indexes: `UNIQUE (code)`, `(department)`, `(status)`.

### 3.3 `rbac`

#### `roles`

| Field | Type | Notes |
| --- | --- | --- |
| `name` | text | |
| `code` | text | Unique. Stable identifier used in code and tests. |
| `description` | text | |
| `dataScope` | select | `all` \| `department` \| `department_and_below` \| `self` \| `custom`. |
| `customDepartments` | relation → `departments` (n) | Used when `dataScope = custom`. |
| `permissions` | relation → `permissions` (n) | Granted permission points. |
| `menus` | relation → `menus` (n) | Granted menu nodes. |
| `status` | select | `active` \| `disabled`. |
| `sortOrder` | number | |
| `isSystem` | bool | System roles cannot be deleted or re-coded. |
| `deletedAt` | date | Soft delete. |

Indexes: `UNIQUE (code)`, `(status)`.

#### `permissions`

| Field | Type | Notes |
| --- | --- | --- |
| `name` | text | Human label. |
| `code` | text | Unique. `<module>:<resource>:<action>`, for example `rbac:menu:create`. |
| `module` | text | Owning module id from `CAPABILITY-MAP.md`. |
| `resource` | text | Resource within the module. |
| `action` | select | `list` \| `view` \| `create` \| `update` \| `delete` \| `export` \| `import` \| `execute` \| `approve` \| `manage`. |
| `type` | select | `menu` \| `button` \| `api` \| `data`. |
| `description` | text | |
| `isSystem` | bool | Seeded permissions are protected. |
| `status` | select | `active` \| `disabled`. |

Indexes: `UNIQUE (code)`, `(module, resource)`, `(type)`.

#### `menus`

| Field | Type | Notes |
| --- | --- | --- |
| `title` | text | Display label. |
| `name` | text | Unique route name; the stable key in code. |
| `parent` | relation → `menus` (1) | Empty for a root node. |
| `type` | select | `directory` \| `menu` \| `button` \| `link`. |
| `platform` | select | `admin` \| `web`. |
| `path` | text | URL path for `menu` and `link`. |
| `component` | text | Reserved for a future SPA. |
| `icon` | text | Font Awesome class. |
| `permissionCode` | text | Permission required to see and use this node. |
| `sortOrder` | number | |
| `visible` | bool | |
| `keepAlive` | bool | Reserved. |
| `external`, `link` | bool, url | |
| `status` | select | `active` \| `disabled`. |
| `isSystem` | bool | |

Indexes: `UNIQUE (name)`, `(parent)`, `(platform, status)`, `(permissionCode)`.

#### `dictionaries` / `dictionary_items`

`dictionaries`: `name`, `code` (unique), `description`, `status`, `isSystem`.

`dictionary_items`:

| Field | Type | Notes |
| --- | --- | --- |
| `dictionary` | relation → `dictionaries` (1) | |
| `parent` | relation → `dictionary_items` (1) | Tree-shaped dictionaries. |
| `label`, `value` | text | |
| `sortOrder` | number | |
| `status` | select | `active` \| `disabled`. |
| `color`, `cssClass` | text | |
| `isDefault` | bool | |
| `extra` | json | |

Indexes: `UNIQUE (dictionary, value)`, `(dictionary, sortOrder)`.

Seeded codes: `user_tag`, `position_category`, `announcement_type`,
`product_status`, `order_status`, `payment_method`, `refund_reason`,
`subscription_status`, `coupon_type`, `provider_type`, `log_level`, `gender`.

### 3.4 `storage`

#### `file_folders`

| Field | Type | Notes |
| --- | --- | --- |
| `name` | text | |
| `parent` | relation → `file_folders` (1) | |
| `path` | text | Materialised path. |
| `owner` | relation → `users` (1) | |
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
| `provider` | relation → `integration_providers` (1) | Empty for local. |
| `url`, `thumbnail` | text | Resolved public or signed URLs. |
| `owner` | relation → `users` (1) | |
| `refCount` | number | Number of `attachments` rows. Deletion refused while positive. |
| `scope` | select | `private` \| `public` \| `shared`. |
| `status` | select | `active` \| `quarantined` \| `deleted`. |
| `tags` | select (multi) | |
| `deletedAt` | date | Soft delete. |

Indexes: `(folder, name)`, `(checksum)`, `(owner)`, `(status)`, `(mimeType)`.

#### `attachments`

| Field | Type | Notes |
| --- | --- | --- |
| `file` | relation → `files` (1) | |
| `ownerType` | text | Collection name, for example `announcements`. |
| `ownerId` | text | Record id within that collection. |
| `field` | text | Owning field, for example `attachments` or `body`. |
| `sortOrder` | number | |
| `createdBy` | relation → `users` (1) | |

Indexes: `(ownerType, ownerId)`, `(file)`,
`UNIQUE (file, ownerType, ownerId, field)`.

### 3.5 `workbench`

#### `todos`

| Field | Type | Notes |
| --- | --- | --- |
| `title` | text | |
| `description` | editor | |
| `owner` | relation → `users` (1) | |
| `status` | select | `pending` \| `in_progress` \| `done` \| `cancelled`. |
| `priority` | select | `low` \| `normal` \| `high` \| `urgent`. |
| `dueAt`, `remindAt`, `completedAt` | date | |
| `relatedType`, `relatedId` | text | Optional deep link target. |
| `tags` | select (multi) | |
| `sortOrder` | number | |

Indexes: `(owner, status)`, `(dueAt)`, `(remindAt)`.

#### `messages` / `message_recipients`

`messages`: `title`, `content` (editor), `type` (select: `system` \| `notice` \|
`todo` \| `announcement`), `level` (select: `info` \| `success` \| `warning` \|
`error`), `sender` (relation → `users`, empty means system), `link` (text),
`audience` (select: `direct` \| `role` \| `department` \| `all`).

`message_recipients`: `message` (relation → `messages`), `user`
(relation → `users`), `readAt` (date), `deletedAt` (date).

Indexes: `UNIQUE (message, user)`, `(user, readAt)`.

#### `recent_visits`

| Field | Type | Notes |
| --- | --- | --- |
| `user` | relation → `users` (1) | |
| `title`, `path`, `icon` | text | |
| `visitCount` | number | |
| `visitedAt` | date | Last visit; drives ordering and pruning. |

Indexes: `UNIQUE (user, path)`, `(user, visitedAt)`.

#### `quick_links`

| Field | Type | Notes |
| --- | --- | --- |
| `user` | relation → `users` (1) | Empty means a global default for new administrators. |
| `title`, `path`, `icon`, `color` | text | |
| `permissionCode` | text | Hidden when the actor lacks it. |
| `sortOrder` | number | |

Indexes: `(user, sortOrder)`, `(path)`.

### 3.6 `announcement`

#### `announcements`

| Field | Type | Notes |
| --- | --- | --- |
| `title` | text | |
| `content` | editor | |
| `type` | select | `notice` \| `announcement` \| `maintenance` \| `update`. |
| `level` | select | `normal` \| `important` \| `urgent`. |
| `status` | select | `draft` \| `scheduled` \| `published` \| `archived` \| `expired`. |
| `publishAt`, `expireAt` | date | |
| `author` | relation → `users` (1) | |
| `audience` | select | `all` \| `admins` \| `users` \| `departments`. |
| `departments` | relation → `departments` (n) | |
| `pinned` | bool | |
| `readCount` | number | Denormalised counter. |
| `deletedAt` | date | Soft delete. |

Indexes: `(status, publishAt)`, `(expireAt)`, `(pinned, publishAt)`, `(audience)`.

#### `announcement_reads`

| Field | Type | Notes |
| --- | --- | --- |
| `announcement` | relation → `announcements` (1) | |
| `user` | relation → `users` (1) | Readers are accounts; which surface is implied by the announcement audience. |
| `readAt` | date | |

Indexes: `UNIQUE (announcement, user)`, `(user)`.

### 3.7 `jobs`

#### `scheduled_jobs`

| Field | Type | Notes |
| --- | --- | --- |
| `name`, `code`, `description` | text | `code` unique; must match a handler registered in code. |
| `cron` | text | Validated on write. |
| `params` | json | Passed to the handler. |
| `status` | select | `enabled` \| `disabled` \| `paused`. |
| `concurrency` | select | `allow` \| `forbid` \| `replace`. |
| `timeoutSeconds`, `maxRetries`, `retryBackoffSeconds` | number | |
| `notifyOnFailure` | bool | |
| `notifyTargets` | relation → `users` (n) | |
| `lastRunAt`, `nextRunAt` | date | |
| `lastStatus` | select | Mirror of the most recent `job_runs.status`. |
| `createdBy` | relation → `users` (1) | |

Indexes: `UNIQUE (code)`, `(status)`, `(nextRunAt)`.

#### `job_runs`

| Field | Type | Notes |
| --- | --- | --- |
| `job` | relation → `scheduled_jobs` (1) | |
| `startedAt`, `finishedAt` | date | |
| `durationMs` | number | |
| `status` | select | `running` \| `success` \| `failed` \| `timeout` \| `cancelled` \| `skipped`. |
| `triggeredBy` | select | `cron` \| `manual` \| `retry` \| `api`. |
| `triggeredByUser` | relation → `users` (1) | Set for manual runs. |
| `attempt` | number | 1-based. |
| `output`, `error` | text | |

Indexes: `(job, startedAt)`, `(status)`, `(startedAt)`.

### 3.8 `settings` — two surfaces, one engine

`settings-admin` and `settings-product` are separate concerns with separate
pages, registries, permissions and audit categories. They share this one table
and the typed validation engine, discriminated by `surface` — exactly as orders
and refunds are separate concerns that share a database.

| Field | Type | Notes |
| --- | --- | --- |
| `surface` | select | `admin` \| `product`. |
| `category` | select | Admin: `console` \| `security` \| `notification` \| `banner`. Product: `site` \| `auth` \| `registration` \| `announcement` \| `feature` \| `commerce`. Named `category` rather than `group` to avoid a SQL keyword. |
| `key` | text | Unique within a surface and category. |
| `value` | json | Typed payload, validated against the registry entry. |
| `valueType` | select | `string` \| `number` \| `bool` \| `json` \| `file` \| `select`. |
| `description` | text | Help text in the settings form. |
| `isPublic` | bool | Readable without authentication. Only meaningful for `surface = product`. |
| `isSystem` | bool | Cannot be deleted from the UI. |
| `updatedBy` | relation → `users` (1) | |

Indexes: `UNIQUE (surface, category, key)`, `(surface, isPublic)`.

The key registry lives in code — `internal/settings/registry_admin.go` and
`internal/settings/registry_product.go` — so every key has a declared type,
default and validator. A row without a registry entry is ignored, which lets a
migration add a key without breaking an older binary. **A page may only write
keys belonging to its own surface**; the two registries cannot address each
other's keys.

Seeded keys:

| Surface | Category | Keys |
| --- | --- | --- |
| `admin` | `console` | `consoleName`, `logo`, `favicon`, `accentColor`, `defaultTheme`, `defaultLanguage`, `defaultPageSize`, `footerText` |
| `admin` | `security` | `passwordMinLength`, `passwordRequireClasses`, `passwordExpiryDays`, `sessionTimeoutMinutes`, `idleTimeoutMinutes`, `maxLoginAttempts`, `lockoutMinutes`, `captchaEnabled`, `mfaRequired`, `ipAllowList` |
| `admin` | `notification` | `notifyOnJobFailure`, `notifyOnSecurityEvent`, `notifyOnNewOrder`, `notifyOnRefundRequest`, `defaultChannels`, `digestFrequency` |
| `admin` | `banner` | `enabled`, `message`, `level`, `dismissible` |
| `product` | `site` | `siteName`, `siteUrl`, `logo`, `favicon`, `description`, `keywords`, `icp`, `copyright`, `timezone`, `language`, `currency`, `currencyFormat` |
| `product` | `auth` | `methods`, `oauthProviders`, `mfaRequired`, `sessionTimeoutMinutes`, `trustedDeviceDays`, `maxLoginAttempts`, `lockoutMinutes` |
| `product` | `registration` | `enabled`, `method`, `requireVerification`, `defaultTier`, `agreementRequired`, `invitationOnly` |
| `product` | `announcement` | `enabled`, `position`, `displayMode`, `dismissible` |
| `product` | `feature` | `catalog.enabled`, `cart.enabled`, `checkout.enabled`, `coupons.enabled`, `membership.enabled`, `subscriptions.enabled`, `reviews.enabled`, `wishlist.enabled`, `maintenanceMode`, `maintenanceMessage` |
| `product` | `commerce` | `defaultCurrency`, `taxDisplayMode`, `taxRate`, `unpaidOrderTimeoutMinutes`, `refundWindowDays`, `invoicePrefix` |

### 3.9 `audit`

Append-only through the application; pruned by retention jobs.

#### `operation_logs`

| Field | Type | Notes |
| --- | --- | --- |
| `user` | relation → `users` (1) | Empty for unauthenticated or API-key requests. |
| `apiKey` | relation → `api_keys` (1) | Set when the actor used an API key. |
| `module`, `action`, `method`, `path` | text | |
| `params` | json | Redacted. |
| `result` | select | `success` \| `failure`. |
| `statusCode` | number | |
| `errorMessage` | text | |
| `targetType`, `targetId` | text | |
| `durationMs` | number | |
| `ip`, `userAgent`, `requestId` | text | |

Indexes: `(user, created)`, `(module, action)`, `(created)`, `(requestId)`,
`(result)`.

#### `login_logs`

| Field | Type | Notes |
| --- | --- | --- |
| `user` | relation → `users` (1) | Empty when no account matched the identifier. |
| `context` | select | `admin` \| `product`. |
| `identifier` | text | The submitted identifier, retained even when unmatched. |
| `ip`, `userAgent`, `device`, `location` | text | |
| `result` | select | `success` \| `failure` \| `locked` \| `captcha_error` \| `mfa_required` \| `mfa_failed`. |
| `reason` | text | |
| `riskLevel` | select | `low` \| `medium` \| `high`. |

Indexes: `(context, created)`, `(user, created)`, `(identifier)`, `(ip)`,
`(result)`.

#### `security_audit_logs`

| Field | Type | Notes |
| --- | --- | --- |
| `event` | select | `permission_denied` \| `password_changed` \| `password_reset` \| `role_changed` \| `profile_attached` \| `profile_detached` \| `account_locked` \| `session_revoked` \| `settings_changed` \| `api_key_created` \| `api_key_revoked` \| `data_exported` \| `impersonation_started` \| `login_anomaly` \| `refund_approved` \| `manual_payment_marked` \| `coupon_voided`. |
| `severity` | select | `info` \| `warning` \| `critical`. |
| `actorType` | select | `user` \| `system` \| `api_key`. |
| `actor` | relation → `users` (1) | |
| `targetType`, `targetId` | text | |
| `detail` | json | Old and new values for configuration changes. |
| `ip`, `userAgent`, `requestId` | text | |

Indexes: `(event, created)`, `(severity, created)`, `(actor, created)`,
`(actorType)`.

#### `exception_logs`

| Field | Type | Notes |
| --- | --- | --- |
| `level` | select | `error` \| `fatal` \| `panic`. |
| `message`, `stack` | text | |
| `module`, `path`, `method`, `requestId` | text | |
| `params` | json | Redacted. |
| `user` | relation → `users` (1) | |
| `ip`, `userAgent` | text | |
| `resolved` | bool | |
| `resolvedBy` | relation → `users` (1) | |
| `resolvedAt` | date | |
| `note` | text | |

Indexes: `(level, created)`, `(resolved, created)`, `(requestId)`, `(module)`.

### 3.10 `integrations`

#### `integration_providers`

| Field | Type | Notes |
| --- | --- | --- |
| `name`, `code` | text | `code` unique; referenced by settings, `files.provider` and `payment_transactions`. |
| `type` | select | `email` \| `sms` \| `storage` \| `idp` \| `payment` \| `webhook`. |
| `driver` | select | `smtp` \| `sendgrid` \| `ses` \| `aliyun_sms` \| `tencent_sms` \| `twilio` \| `local` \| `s3` \| `oss` \| `cos` \| `minio` \| `oauth2` \| `oidc` \| `ldap` \| `stripe` \| `paypal` \| `alipay` \| `wechatpay` \| `http`. |
| `config` | json | Non-secret settings. |
| `secrets` | json | Encrypted at rest. Never serialised to the browser. |
| `sandbox` | bool | Use the provider's test mode. |
| `status` | select | `enabled` \| `disabled` \| `testing` \| `error`. |
| `isDefault` | bool | At most one default per `type`; enforced in a transaction. |
| `priority` | number | Fallback ordering. |
| `lastTestAt` | date | |
| `lastTestResult` | select | `success` \| `failure` \| `untested`. |
| `lastTestMessage` | text | |
| `description` | text | |
| `createdBy` | relation → `users` (1) | |
| `deletedAt` | date | Soft delete. |

Indexes: `UNIQUE (code)`, `(type, isDefault)`, `(status)`.

#### `integration_logs`

| Field | Type | Notes |
| --- | --- | --- |
| `provider` | relation → `integration_providers` (1) | |
| `channel` | select | `email` \| `sms` \| `storage` \| `auth` \| `payment` \| `webhook`. |
| `action`, `target` | text | For example `send`, `upload`, `charge`, `token_exchange`. |
| `request`, `response` | json | Redacted and truncated. |
| `status` | select | `success` \| `failure`. |
| `error` | text | |
| `durationMs` | number | |

Indexes: `(provider, created)`, `(channel, status)`, `(created)`.

### 3.11 `monitoring`

#### `metric_snapshots` (optional)

`capturedAt`, `cpuPercent`, `memoryBytes`, `goroutines`, `heapBytes`, `dbBytes`,
`auxDbBytes`, `fileBytes`, `fileCount`, `requestsPerMinute`, `errorRate`,
`latencyP95Ms`.

Indexes: `(capturedAt)`.

#### `system_updates`

| Field | Type | Notes |
| --- | --- | --- |
| `version`, `fromVersion` | text | |
| `channel` | select | `stable` \| `beta`. |
| `status` | select | `available` \| `downloading` \| `ready` \| `applied` \| `failed` \| `rolled_back`. |
| `releaseNotes` | editor | |
| `publishedAt`, `appliedAt` | date | |
| `appliedBy` | relation → `users` (1) | |
| `checksum`, `error` | text | |

Indexes: `(status)`, `(version)`.

### 3.12 `developer`

#### `api_keys`

| Field | Type | Notes |
| --- | --- | --- |
| `name` | text | |
| `keyPrefix` | text | First 8 characters in clear, for lookup and display. |
| `keyHash` | text | SHA-256 of the full key. The key itself is never stored. |
| `owner` | relation → `users` (1) | Must hold an active `admins` profile. |
| `scopes` | relation → `permissions` (n) | Intersected with the owner's live permissions. |
| `rateLimitPerMinute` | number | 0 means the global default. |
| `ipAllowList` | text | Comma-separated CIDR list. Empty means any. |
| `status` | select | `active` \| `revoked` \| `expired`. |
| `expiresAt`, `lastUsedAt`, `revokedAt` | date | |
| `lastUsedIp` | text | |
| `usageCount` | number | |
| `revokedBy` | relation → `users` (1) | |

Indexes: `UNIQUE (keyPrefix)`, `(keyHash)`, `(owner)`, `(status)`.

## 4. Business collections

### 4.1 `catalog`

#### `product_categories`

| Field | Type | Notes |
| --- | --- | --- |
| `name`, `slug` | text | `slug` unique. |
| `parent` | relation → `product_categories` (1) | Tree. |
| `path` | text | Materialised path. |
| `icon`, `image` | text, file | |
| `sortOrder` | number | |
| `status` | select | `active` \| `hidden`. |
| `description` | editor | |
| `deletedAt` | date | Soft delete. |

Indexes: `UNIQUE (slug)`, `(parent)`, `(path)`, `(status)`.

#### `products`

Products cover physical goods, digital goods, membership plans and subscription
plans. `kind` selects the behaviour; the plan-specific fields live on
`member_plans` and `subscription_plans`, which hold a unique relation back here.
This is what lets a plan be bought, discounted and refunded through the same
order pipeline as anything else.

| Field | Type | Notes |
| --- | --- | --- |
| `kind` | select | `physical` \| `digital` \| `membership_plan` \| `subscription_plan`. |
| `name`, `slug`, `subtitle` | text | `slug` unique. |
| `description` | editor | |
| `category` | relation → `product_categories` (1) | |
| `brand` | text | |
| `tags` | select (multi) | |
| `cover` | file | Record-owned primary image. |
| `gallery` | file (multi) | Record-owned gallery. |
| `status` | select | `draft` \| `on_sale` \| `off_shelf` \| `archived`. |
| `listPrice` | number | Minor units. |
| `salePrice` | number | Minor units. Must be ≤ `listPrice`. |
| `costPrice` | number | Minor units. Never shown in the product API. |
| `currency` | text | ISO-4217. |
| `taxable` | bool | |
| `stockMode` | select | `variant` \| `single` \| `unlimited`. |
| `stock` | number | Used when `stockMode = single`. |
| `lowStockThreshold` | number | |
| `weightGrams` | number | |
| `seoTitle`, `seoKeywords`, `seoDescription` | text | |
| `sortOrder` | number | |
| `publishedAt` | date | |
| `createdBy` | relation → `users` (1) | |
| `deletedAt` | date | Soft delete. |

Indexes: `UNIQUE (slug)`, `(status, sortOrder)`, `(category)`, `(kind)`,
`(publishedAt)`.

#### `product_attributes`

| Field | Type | Notes |
| --- | --- | --- |
| `product` | relation → `products` (1) | |
| `name` | text | For example `Colour` or `Size`. |
| `values` | json | Array of `{ value, label, color }`. Stored as JSON rather than a child collection because attributes are always edited and read as a set. |
| `sortOrder` | number | |

Indexes: `UNIQUE (product, name)`, `(product, sortOrder)`.

#### `product_variants`

| Field | Type | Notes |
| --- | --- | --- |
| `product` | relation → `products` (1) | |
| `sku` | text | Unique. |
| `barcode` | text | |
| `options` | json | Attribute combination, for example `{"Colour":"Red","Size":"M"}`. |
| `label` | text | Human-readable combination. |
| `listPrice`, `salePrice`, `costPrice` | number | Minor units; fall back to the product when unset. |
| `currency` | text | |
| `stock` | number | Materialised from `stock_ledger`. |
| `lockedStock` | number | Reserved by unpaid orders. |
| `lowStockThreshold` | number | |
| `weightGrams` | number | |
| `image` | file | |
| `status` | select | `active` \| `disabled`. |
| `deletedAt` | date | Soft delete. |

Indexes: `UNIQUE (sku)`, `(product, status)`, `(barcode)`, `(stock)`.

#### `stock_ledger`

Append-only. Stock is never edited without a ledger entry (FR-catalog-6).

| Field | Type | Notes |
| --- | --- | --- |
| `variant` | relation → `product_variants` (1) | |
| `delta` | number | Signed. |
| `balanceAfter` | number | Materialised running balance for auditability. |
| `reason` | select | `purchase` \| `sale` \| `reserve` \| `release` \| `return` \| `adjustment` \| `damage` \| `stocktake` \| `import`. |
| `refType`, `refId` | text | For example the order that caused the movement. |
| `operator` | relation → `users` (1) | Empty for system-generated movements. |
| `note` | text | |

Indexes: `(variant, created)`, `(reason)`, `(refType, refId)`.

### 4.2 `membership`

#### `member_tiers`

| Field | Type | Notes |
| --- | --- | --- |
| `name`, `code` | text | `code` unique. |
| `level` | number | Ordering; higher is better. |
| `icon` | file | |
| `description` | editor | |
| `status` | select | `active` \| `disabled`. |
| `sortOrder` | number | |
| `isDefault` | bool | Granted on registration. Exactly one default. |

Indexes: `UNIQUE (code)`, `(level)`, `(status)`.

#### `member_tier_benefits`

Typed and queryable, so pricing and checkout can read a discount rate instead of
parsing prose (FR-membership-2).

| Field | Type | Notes |
| --- | --- | --- |
| `tier` | relation → `member_tiers` (1) | |
| `kind` | select | `discount_rate` \| `free_shipping` \| `points_multiplier` \| `content_access` \| `priority_support` \| `custom`. |
| `value` | json | For example `{"rate": 0.9}` or `{"multiplier": 2}`. |
| `label` | text | Display string. |
| `sortOrder` | number | |

Indexes: `(tier, kind)`.

#### `member_plans`

| Field | Type | Notes |
| --- | --- | --- |
| `product` | relation → `products` (1) | **Unique.** The purchasable product; price and currency live there. |
| `name`, `code` | text | `code` unique. |
| `tier` | relation → `member_tiers` (1) | The tier this plan grants. |
| `durationDays` | number | 0 means perpetual. |
| `status` | select | `active` \| `disabled`. |
| `sortOrder` | number | |

Indexes: `UNIQUE (product)`, `UNIQUE (code)`, `(tier)`, `(status)`.

#### `user_memberships`

| Field | Type | Notes |
| --- | --- | --- |
| `user` | relation → `users` (1) | |
| `tier` | relation → `member_tiers` (1) | |
| `plan` | relation → `member_plans` (1) | Empty when granted manually. |
| `order` | relation → `orders` (1) | The purchase that created it. |
| `startAt`, `expireAt` | date | Empty `expireAt` means perpetual. |
| `status` | select | `pending` \| `active` \| `expired` \| `cancelled` \| `refunded`. |
| `source` | select | `purchase` \| `gift` \| `trial` \| `admin` \| `promotion`. |
| `grantedBy` | relation → `users` (1) | Set when granted manually. |
| `reason` | text | |
| `remarks` | text | |

Indexes: `(user, status)`, `(expireAt)`, `(tier)`, `(order)`.

### 4.3 `promotions`

#### `coupons` — templates

| Field | Type | Notes |
| --- | --- | --- |
| `name`, `code` | text | `code` unique; empty means auto-issued per grant. |
| `type` | select | `fixed_amount` \| `percentage` \| `free_shipping`. |
| `value` | number | Minor units for `fixed_amount`; basis points for `percentage`. |
| `maxDiscount` | number | Cap for percentage coupons. 0 means uncapped. |
| `minSpend` | number | Minimum order subtotal. |
| `currency` | text | |
| `validityMode` | select | `absolute` \| `relative`. |
| `validFrom`, `validTo` | date | Used when `validityMode = absolute`. |
| `validDays` | number | Used when `validityMode = relative`; counted from grant. |
| `totalQuantity` | number | 0 means unlimited. |
| `perUserLimit` | number | |
| `claimedCount`, `usedCount` | number | Denormalised counters. |
| `applicability` | select | `all` \| `categories` \| `products` \| `tiers`. |
| `categories` | relation → `product_categories` (n) | |
| `products` | relation → `products` (n) | |
| `tiers` | relation → `member_tiers` (n) | |
| `excludeProducts` | relation → `products` (n) | |
| `stackable` | bool | |
| `stacksWithMemberDiscount` | bool | |
| `maxStacked` | number | |
| `campaign` | relation → `campaigns` (1) | |
| `status` | select | `draft` \| `active` \| `paused` \| `expired`. |
| `createdBy` | relation → `users` (1) | |
| `deletedAt` | date | Soft delete. |

Indexes: `UNIQUE (code)`, `(status)`, `(validFrom, validTo)`, `(campaign)`.

#### `coupon_grants` — issued instances

| Field | Type | Notes |
| --- | --- | --- |
| `coupon` | relation → `coupons` (1) | |
| `user` | relation → `users` (1) | Empty for an unclaimed public code. |
| `code` | text | Unique per grant when auto-issued. |
| `status` | select | `issued` \| `used` \| `expired` \| `voided`. |
| `issuedAt`, `expireAt`, `usedAt` | date | |
| `order` | relation → `orders` (1) | The order that consumed it. |
| `discountApplied` | number | Minor units actually deducted. |
| `issuedBy` | relation → `users` (1) | Empty for automatic issuance. |
| `voidReason` | text | |

Indexes: `UNIQUE (code)`, `(user, status)`, `(coupon, status)`,
`(expireAt)`, `(order)`.

#### `campaigns`

| Field | Type | Notes |
| --- | --- | --- |
| `name`, `code` | text | `code` unique. |
| `description` | editor | |
| `startAt`, `endAt` | date | |
| `budgetCap` | number | Minor units. 0 means uncapped. |
| `spentAmount` | number | Denormalised. |
| `status` | select | `draft` \| `scheduled` \| `active` \| `ended` \| `paused`. |
| `createdBy` | relation → `users` (1) | |

Indexes: `UNIQUE (code)`, `(status, startAt)`, `(endAt)`.

### 4.4 `subscriptions`

#### `subscription_plans`

| Field | Type | Notes |
| --- | --- | --- |
| `product` | relation → `products` (1) | **Unique.** Price and currency live on the product. |
| `name`, `code` | text | `code` unique. |
| `interval` | select | `day` \| `week` \| `month` \| `year`. |
| `intervalCount` | number | For example interval `month`, count 3 = quarterly. |
| `trialDays` | number | |
| `tier` | relation → `member_tiers` (1) | Tier granted while subscribed. |
| `maxCycles` | number | 0 means unbounded. |
| `status` | select | `active` \| `disabled`. |
| `sortOrder` | number | |

Indexes: `UNIQUE (product)`, `UNIQUE (code)`, `(status)`.

#### `subscriptions`

| Field | Type | Notes |
| --- | --- | --- |
| `user` | relation → `users` (1) | |
| `plan` | relation → `subscription_plans` (1) | |
| `status` | select | `trialing` \| `active` \| `past_due` \| `paused` \| `cancelled` \| `expired`. |
| `startedAt`, `currentPeriodStart`, `currentPeriodEnd` | date | |
| `trialEndAt` | date | |
| `cancelAtPeriodEnd` | bool | |
| `cancelledAt`, `cancelReason` | date, text | |
| `cancelledBy` | relation → `users` (1) | |
| `pausedAt`, `resumedAt` | date | |
| `pausedDaysAccrued` | number | Extends the period on resume. |
| `pendingPlan` | relation → `subscription_plans` (1) | Set for a scheduled downgrade. |
| `retryCount` | number | Dunning attempts in the current cycle. |
| `nextBillingAt` | date | |

Indexes: `(user, status)`, `(status, nextBillingAt)`, `(plan)`, `(currentPeriodEnd)`.

#### `subscription_cycles`

| Field | Type | Notes |
| --- | --- | --- |
| `subscription` | relation → `subscriptions` (1) | |
| `cycleNumber` | number | 1-based. |
| `periodStart`, `periodEnd` | date | |
| `order` | relation → `orders` (1) | The order generated for this cycle. |
| `amount` | number | Minor units. |
| `currency` | text | |
| `status` | select | `pending` \| `paid` \| `failed` \| `skipped` \| `refunded`. |
| `attempts` | number | |
| `lastAttemptAt` | date | |
| `prorationCredit` | number | Minor units credited from a mid-cycle plan change. |
| `note` | text | |

Indexes: `(subscription, cycleNumber)`, `(status)`, `(periodEnd)`, `(order)`.

### 4.5 `orders`

#### `orders`

| Field | Type | Notes |
| --- | --- | --- |
| `orderNo` | text | Unique, human-readable, generated from the invoice prefix. |
| `user` | relation → `users` (1) | |
| `status` | select | `pending_payment` \| `paid` \| `fulfilling` \| `completed` \| `cancelled` \| `closed` \| `refunded`. |
| `paymentStatus` | select | `unpaid` \| `partial` \| `paid` \| `refunding` \| `refunded`. |
| `source` | select | `web` \| `ios` \| `android` \| `admin` \| `subscription` \| `import`. |
| `currency` | text | |
| `subtotal` | number | Sum of item subtotals. |
| `discountTotal` | number | Coupon plus promotion discounts. |
| `memberDiscount` | number | Membership discount. |
| `couponDiscount` | number | Sum of applied coupon discounts. |
| `shippingFee` | number | |
| `taxAmount` | number | |
| `grandTotal` | number | |
| `amountPaid` | number | |
| `amountRefunded` | number | |
| `couponGrants` | relation → `coupon_grants` (n) | Coupons applied at creation. |
| `memberTier` | relation → `member_tiers` (1) | Tier in force at creation, for reproducibility. |
| `subscription` | relation → `subscriptions` (1) | Set for a subscription cycle order. |
| `shippingAddress` | json | Name, phone, country, province, city, district, street, postal code. |
| `billingAddress` | json | |
| `customerNote`, `adminNote` | text | Kept separate (FR-orders-8). |
| `expiresAt` | date | Auto-close deadline for unpaid orders. |
| `paidAt`, `completedAt`, `cancelledAt` | date | |
| `cancelReason` | text | |
| `placedBy` | relation → `users` (1) | The administrator who created it manually. Empty for self-service. |
| `deletedAt` | date | Soft delete. |

Indexes: `UNIQUE (orderNo)`, `(user, created)`, `(status)`, `(paymentStatus)`,
`(created)`, `(expiresAt)`, `(subscription)`.

#### `order_items`

Snapshots; later catalogue edits never change a placed order (FR-orders-2).

| Field | Type | Notes |
| --- | --- | --- |
| `order` | relation → `orders` (1) | |
| `product` | relation → `products` (1) | Kept for reporting. |
| `variant` | relation → `product_variants` (1) | Empty for non-variant products. |
| `productName` | text | Snapshot. |
| `variantLabel` | text | Snapshot. |
| `sku` | text | Snapshot. |
| `image` | text | Snapshot of the image URL. |
| `unitPrice` | number | Snapshot, minor units. |
| `quantity` | number | |
| `subtotal` | number | |
| `discountAmount` | number | |
| `taxAmount` | number | |
| `total` | number | |
| `currency` | text | |
| `refundedQuantity` | number | Updated as refunds are approved. |
| `refundedAmount` | number | |

Indexes: `(order)`, `(product)`, `(variant)`.

#### `order_events`

The order timeline (FR-orders-11).

| Field | Type | Notes |
| --- | --- | --- |
| `order` | relation → `orders` (1) | |
| `event` | select | `created` \| `payment_started` \| `paid` \| `payment_failed` \| `fulfilling` \| `completed` \| `cancelled` \| `closed` \| `refund_requested` \| `refunded` \| `note_added` \| `stock_released`. |
| `fromStatus`, `toStatus` | text | |
| `actor` | relation → `users` (1) | Empty for system events. |
| `reason` | text | |
| `detail` | json | |

Indexes: `(order, created)`, `(event)`, `(created)`.

### 4.6 `payments`

#### `payment_intents`

| Field | Type | Notes |
| --- | --- | --- |
| `order` | relation → `orders` (1) | |
| `provider` | relation → `integration_providers` (1) | |
| `amount` | number | Minor units. |
| `currency` | text | |
| `method` | select | `card` \| `bank_transfer` \| `wallet` \| `cash` \| `other`. |
| `status` | select | `pending` \| `processing` \| `succeeded` \| `failed` \| `cancelled` \| `expired`. |
| `providerIntentId` | text | The provider's own identifier. |
| `clientSecret` | text | If the provider requires one; never logged. |
| `expiresAt` | date | |
| `createdBy` | relation → `users` (1) | Empty for customer-initiated. |

Indexes: `(order)`, `(status)`, `(providerIntentId)`, `(expiresAt)`.

#### `payment_transactions`

| Field | Type | Notes |
| --- | --- | --- |
| `intent` | relation → `payment_intents` (1) | |
| `order` | relation → `orders` (1) | Denormalised for reporting. |
| `provider` | relation → `integration_providers` (1) | |
| `providerTxnId` | text | **Unique when set.** The idempotency key for callbacks. |
| `amount` | number | Minor units. |
| `currency` | text | |
| `feeAmount` | number | Provider fee, when reported. |
| `status` | select | `pending` \| `processing` \| `succeeded` \| `failed` \| `cancelled` \| `expired`. |
| `failureCode`, `failureMessage` | text | |
| `callbackPayload` | json | Raw provider callback, redacted and stored once. |
| `callbackCount` | number | Incremented on every replay; the payment is applied only once. |
| `settledAt` | date | From reconciliation. |
| `reconciled` | bool | |
| `reconciledAt` | date | |
| `manual` | bool | True for an administrator-recorded offline payment. |
| `manualReason` | text | Required when `manual` is true (FR-payments-6). |
| `recordedBy` | relation → `users` (1) | |

Indexes: `UNIQUE (providerTxnId)`, `(intent)`, `(order)`, `(status)`,
`(settledAt, reconciled)`, `(created)`.

### 4.7 `refunds`

| Field | Type | Notes |
| --- | --- | --- |
| `refundNo` | text | Unique, human-readable. |
| `order` | relation → `orders` (1) | |
| `transaction` | relation → `payment_transactions` (1) | The payment being refunded. |
| `mode` | select | `full` \| `partial_amount` \| `partial_items`. |
| `items` | json | For `partial_items`: `[{ orderItemId, quantity }]`. |
| `amount` | number | Minor units, computed at approval. |
| `currency` | text | |
| `status` | select | `pending` \| `approved` \| `rejected` \| `processing` \| `succeeded` \| `failed` \| `cancelled`. |
| `reason` | select | Mirrors the `refund_reason` dictionary. |
| `reasonDetail` | text | |
| `attachments` | relation → `files` (n) | Evidence. |
| `requestedBy` | relation → `users` (1) | |
| `autoApproved` | bool | True when under the auto-approval threshold. |
| `reviewedBy` | relation → `users` (1) | |
| `reviewedAt` | date | |
| `reviewComment` | text | |
| `providerRefundId` | text | |
| `providerResponse` | json | Redacted. |
| `attempts` | number | Execution retries. |
| `lastError` | text | |
| `completedAt` | date | |
| `restock` | bool | Whether approving restores stock. |

Indexes: `UNIQUE (refundNo)`, `(order)`, `(status)`, `(requestedBy, created)`,
`(reviewedBy)`, `(created)`.

## 5. API rules and the security boundary

Every collection in § 3 and § 4 is created with all five rules set to `null`
(superuser only):

```
listRule = viewRule = createRule = updateRule = deleteRule = null
```

This is deliberate. Two access paths exist:

1. **The panel** renders through Gin handlers that call `core.App` directly.
   PocketBase does not evaluate collection rules on that path, so the rules are
   irrelevant to it — **all authorisation is the Gin middleware's job**
   (`design.md` § 3 and § 12).
2. **The public `/api` surface** is served by PocketBase and *does* evaluate the
   rules. With `null` rules it returns 404/403 to everyone except superusers,
   which is what is wanted: no collection is exposed to untrusted clients by
   accident.

This matters most for `users`, whose PocketBase default rules permit public
sign-up and owner-scoped reads. Those defaults are replaced by the migration, and
registration is exposed only through a purpose-built, rate-limited Gin endpoint
that honours `settings-product` § `registration`.

Collections that must be readable without authentication (site name, logo) are
exposed through a purpose-built Gin endpoint reading specific public product
settings, never by loosening a collection rule.

The two invariants above are enforced by tests in `internal/rbac/apirules_test.go`
rather than left to review:

- Every platform collection is either **closed** (all five rules `null`) or
  **deliberately open** (all five written). A half-open collection is the state
  that slips through, because it produces no error and no failing screen.
- Every rule on an open collection is validated: a blank rule is rejected
  (it admits everyone, and `null` is how closed is spelled), every code-shaped
  literal must be a declared permission point, and every `@request.auth.<name>`
  must resolve to a real field or a real back-relation.

The second check exists because PocketBase does not fail on an unknown field in a
filter: it evaluates it, and a rule such as `@request.auth.adminProfile ?= ""`
matches every record. A rule that admits everyone while reading as a restriction
is worse than one that errors, so field names are checked against the schema.

## 6. Relationship overview

### Platform

```
users ─┬── employees            (1:1, optional)
       ├── admins               (1:1, optional) ──< roles ──< permissions
       │                                                  └──< menus
       ├── user_preferences     (1:1 per context)
       ├── sessions
       ├── todos, quick_links, recent_visits
       ├── message_recipients ──> messages
       ├── operation_logs
       └── api_keys ──< permissions (scopes)

departments ─┬── departments (self, tree)
             └──< positions
roles ──< departments   (custom data scope)

file_folders ──< file_folders (self, tree)
file_folders ──< files ──< attachments >── (announcements, products, refunds)
files ──> integration_providers (storage)

integration_providers ──< integration_logs
scheduled_jobs ──< job_runs
announcements ──< announcement_reads
```

### Business

```
product_categories ──< product_categories (self, tree)
product_categories ──< products ──< product_attributes
                            │
                            ├──< product_variants ──< stock_ledger
                            │
                            ├──> member_plans ──> member_tiers ──< member_tier_benefits
                            │                        │
                            └──> subscription_plans   │
                                                      │
coupons ──< coupon_grants ─────────────────────────┐  │
campaigns ──< coupons                              │  │
                                                   ▼  ▼
orders ─┬──< order_items                      user_memberships
        ├──< order_events
        ├──> subscriptions ──< subscription_cycles
        ├──< payment_intents ──< payment_transactions
        └──< refunds ──> payment_transactions
```

## 7. Migration strategy

- Schema lives in Go migrations under `migrations/`, registered with
  `migrations.Register` and applied by `app.RunAllMigrations()` on boot. No
  manual Dashboard edits above local development.
- One migration file per module, numbered by wave:
  `1700000001_identity.go`, `1700000002_organization.go`,
  `1700000003_rbac.go`, … `1700020004_refunds.go`.
- A migration is **idempotent**: it checks for the collection before creating it,
  so a partially applied deployment can be re-run safely.
- The identity migration **extends** the existing `users` collection rather than
  creating it, and replaces its API rules.
- Seed data ships in separate `*_seed.go` migrations so schema and content are
  reasoned about independently.
- Destructive changes (dropping a field or collection) require a two-step
  release: stop writing in release N, remove in release N+1.
- Money-bearing collections (`orders`, `payment_transactions`, `refunds`,
  `stock_ledger`, `coupon_grants`, `order_events`) are never truncated by a
  migration; they are reversed by compensating records.

## 8. Seed data summary

| Collection | Seeded rows |
| --- | --- |
| `permissions` | One row per `<module>:<resource>:<action>` across all 20 modules |
| `menus` | The full navigation tree, including the seven business entries, with button nodes for in-page actions |
| `roles` | `super_admin` (all permissions, `dataScope = all`, `isSystem`), `admin` (all except developer and settings-admin), `operator` (day-to-day operations), `finance` (orders, payments, refunds, read-only catalog), `support` (users, orders read-only, refund requests) |
| `dictionaries` | `user_tag`, `position_category`, `announcement_type`, `product_status`, `order_status`, `payment_method`, `refund_reason`, `subscription_status`, `coupon_type`, `provider_type`, `log_level`, `gender` |
| `dictionary_items` | Entries for each seeded dictionary |
| `settings` | Every key from § 3.8, for both surfaces, with its default value |
| `member_tiers` | `free` (default, level 0) and `premium` (level 1) |
| `member_tier_benefits` | A `discount_rate` benefit on `premium` |
| `product_categories` | A single root category |
| `scheduled_jobs` | The built-in catalogue from FR-jobs-10 and FR-jobs-11, all `disabled` except retention jobs |
| `quick_links` | Global defaults for a new administrator |
| `departments` | A single root department |
| `users` | One bootstrap account with an `admins` profile, `isProtected = true`, holding `super_admin` |
