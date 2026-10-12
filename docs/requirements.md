# PocketAdmin Platform — Requirements

Version: 0.2 (revision 2 — draft for review)
Companion documents: [`../CAPABILITY-MAP.md`](../CAPABILITY-MAP.md),
[`database-design.md`](database-design.md), [`design.md`](design.md)

## 1. Objective

Grow the existing PocketAdmin panel (embedded PocketBase + Gin + server-rendered
AdminLTE) from a database browser into an operations platform: a single account
model with organisation and back-office profiles, role-based access control,
content and messaging, scheduled work, third-party integrations, audit trails, a
developer surface, and a business domain covering products, membership, orders,
payments, refunds, coupons and subscriptions.

**Users.** One `users` account per person. Some of those accounts additionally
hold an **employee** profile (organisation membership) and/or an **admin**
profile (back-office access with roles and data scope).

**Success looks like.** An operator can onboard a colleague (create a user,
attach the employee profile, grant the admin profile and a role), and that
colleague sees exactly the menus and records their role permits. A customer can
buy a product or subscribe to a plan, and the resulting order, payment and any
refund are traceable end to end. Every state change is attributable in the
operation log.

**Non-goals.** No multi-tenant isolation, no shipped customer-facing storefront,
no warehouse or fulfilment logistics, no horizontal scale-out. See
`CAPABILITY-MAP.md` § Out of scope.

## 2. Scope decisions

| Decision | Choice | Consequence |
| --- | --- | --- |
| Data layer | Embedded PocketBase, entities modelled as collections | Schema ships as Go migrations; no SQL DDL |
| Tenancy | Single tenant | No tenant column; no cross-tenant isolation logic |
| UI | Server-rendered Go templates + AdminLTE | Menus filtered server-side; no client-side dynamic routes |
| Documentation language | English | Aligns with code identifiers |
| Identity | Users-centric | `users` is the only auth collection; `employees` and `admins` are 1:1 profile extensions |
| Money | Integer minor units + explicit currency | No floating point in any business collection |

Two consequences deserve emphasis:

1. **All authorisation lives in the Gin layer.** The embedded `core.App` bypasses
   PocketBase collection rules, so those rules are *not* the security boundary
   for the panel. They exist only to lock down the public `/api` surface. A
   handler that forgets its permission check is a real vulnerability — hence the
   startup self-check in `design.md` § 12.4.
2. **Menus are derived, not authored per role.** A role grants permission
   points; the navigation tree is the set of menu nodes whose permission code the
   actor holds.

## 3. Functional requirements

Requirement ids are `FR-<module>-<n>`.

### 3.1 `identity` — the central account, profiles, sessions

The account model is users-centric. `users` is PocketBase's own auth collection,
extended with the fields this platform needs. `employees` and `admins` are
optional 1:1 profile extensions.

**The account**

- FR-identity-1 Create, edit, disable and delete a `users` account (email,
  phone, password, name, nickname, avatar, gender, birthday, status, language,
  timezone, tags, source, remarks).
- FR-identity-2 A single account serves both surfaces. The same credentials
  authenticate the customer-facing product and the back office; the back office
  additionally requires an active `admins` profile (FR-identity-10).
- FR-identity-3 Reset a password. A reset revokes every session for that account.
- FR-identity-4 Lock an account after N consecutive failed sign-ins (configurable
  in `settings-admin`) and unlock it manually or on expiry.
- FR-identity-5 Ban, unban and soft-delete an account; banning revokes sessions.
- FR-identity-6 Enforce uniqueness on email and phone, with a clear error.
- FR-identity-7 Optional multi-factor authentication, delegated to PocketBase's
  OTP/MFA support on the `users` collection.

**The employee profile (optional, 1:1 with a user)**

- FR-identity-8 Attach, edit and detach an `employees` profile: employee number,
  department, position, line manager, hire date, employment type, work contact
  details, employment status.
- FR-identity-9 Detaching the employee profile never deletes the account; it
  records a resignation and retains the HR history.

**The admin profile (optional, 1:1 with a user)**

- FR-identity-10 Attach, edit and detach an `admins` profile. Only an account
  with an active admin profile may sign in to the back office.
- FR-identity-11 Grant one or more roles to an admin profile. An admin profile
  with no role can sign in but sees only the workbench.
- FR-identity-12 Detaching the admin profile immediately bars back-office access
  and revokes that account's admin-context sessions.
- FR-identity-13 A user may hold both profiles; the panel shows both relationships
  and refuses to create a second profile of the same kind.

**Sessions, history and preferences (per account, per surface)**

- FR-identity-14 List active sessions per account, filtered by surface (back
  office or product): device, IP, user agent, created, last seen, expiry.
  Revoke one or all. A revoked session is rejected on its next request.
- FR-identity-15 View an account's sign-in history: time, IP, location, device,
  surface, result and failure reason.
- FR-identity-16 Store per-account, per-surface preferences: theme, language,
  page size, sidebar state, timezone, notification channels.
- FR-identity-17 Impersonate an account for support, producing a
  `security_audit_logs` entry and a session marked as impersonated. Write
  operations are blocked while impersonating unless explicitly allowed.
- FR-identity-18 Enforce the password policy from `settings-admin` for back-office
  accounts and from `settings-product` for product accounts.

### 3.2 `organization` — departments, positions

- FR-organization-1 Department tree: create, rename, move (re-parent), reorder
  and delete. Deleting a department with children or members is refused.
- FR-organization-2 Maintain a materialised path per department so
  "this department and all descendants" is one indexed query.
- FR-organization-3 Assign a leader and contact details per department.
- FR-organization-4 Positions belong to a department; a position has a level, a
  category and an optional headcount.
- FR-organization-5 Render the organisation chart (department tree with member
  counts) and an indented list view.
- FR-organization-6 Moving a department updates the path of every descendant in
  one transaction.
- FR-organization-7 Refuse to delete a position or department that still has
  active employees assigned.
- FR-organization-8 A line-manager relationship may not create a cycle.

### 3.3 `rbac` — roles, permissions, menus, dictionaries

- FR-rbac-1 Manage roles: name, code, description, status, sort order, protected
  system role flag.
- FR-rbac-2 Maintain the catalogue of permission points, keyed by
  `<module>:<resource>:<action>` (for example `rbac:menu:create`).
- FR-rbac-3 Grant a set of permission points to a role.
- FR-rbac-4 Grant a set of menu nodes to a role; the effective menu is the
  intersection of granted menus and menus whose permission code the role holds.
- FR-rbac-5 Data scope per role: all records, own department, own department and
  descendants, own records only, or a custom department set.
- FR-rbac-6 Manage the menu tree: directory / menu / button / external link nodes
  with title, route name, path, component, icon, sort order, visibility,
  keep-alive and permission code.
- FR-rbac-7 Menu nodes carry a platform discriminator (`admin`, `web`).
- FR-rbac-8 Dictionary types and items: label, value, ordering, status, colour,
  CSS class, optional nesting for tree-shaped dictionaries.
- FR-rbac-9 Dictionary values are the single source of truth for option sets
  rendered in forms and filters.
- FR-rbac-10 Publish a runtime, cache-backed accessor so permission checks and
  menu projection do not query per request.
- FR-rbac-11 A permission change invalidates the affected actors' cached
  permissions within one request cycle.

### 3.4 `storage` — file library

- FR-storage-1 Upload one or many files with drag-and-drop and progress.
- FR-storage-2 Organise files into a folder tree with rename, move and delete.
- FR-storage-3 Record size, MIME type, extension, checksum, uploader and time.
- FR-storage-4 Enforce an upload policy from settings: allowed extensions,
  maximum size, image dimension limits.
- FR-storage-5 Constrain uploads by MIME sniffing, not the client-supplied
  extension alone.
- FR-storage-6 Preview images and common document types; download the rest.
- FR-storage-7 Reference a file from rich-text content, record fields and product
  media, tracking a reference count so a referenced file is not deleted.
- FR-storage-8 Choose the active storage driver (local filesystem or an object
  storage provider configured in `integrations`).
- FR-storage-9 Deduplicate by checksum within a folder.
- FR-storage-10 Distinguish record-owned files (native PocketBase file fields)
  from library files; the two are not interchangeable.

### 3.5 `audit` — operation, login, security and exception logs

- FR-audit-1 Record every state-changing panel request: actor, module, action,
  method, path, sanitised parameters, result, status code, duration, IP, user
  agent, request id.
- FR-audit-2 Redact secrets (passwords, tokens, API keys, provider credentials)
  before persisting parameters, by deny-key-name plus a safe-field allow-list.
- FR-audit-3 Record every sign-in attempt for both surfaces: identifier, surface,
  IP, location, device, result, failure reason.
- FR-audit-4 Record security events as first-class entries: permission denied,
  password changed, role changed, account locked, session revoked, settings
  changed, API key created/revoked, sensitive data exported, impersonation
  started, login anomaly.
- FR-audit-5 Record unhandled exceptions with message, stack, request id, route
  and actor.
- FR-audit-6 Filter and search each log type by actor, time range, module, result
  and free text, with pagination and CSV export.
- FR-audit-7 Audit collections are append-only through the application.
- FR-audit-8 A retention job prunes each log type after a configurable period.
- FR-audit-9 Distinguish application logs from PocketBase's own `_logs`
  collection and link to the latter for raw request diagnostics.
- FR-audit-10 Record every business state transition that affects money (order,
  payment, refund, subscription) at `info` or higher severity.

### 3.6 `settings-admin` — back-office console configuration

These settings govern the back office itself. They are separate from
`settings-product` and are edited on their own page by their own permission.

- FR-settings-admin-1 Typed, validated, audited key/value settings.
- FR-settings-admin-2 **Console**: console name, logo, favicon, accent colour,
  default theme, default language, default page size, footer text.
- FR-settings-admin-3 **Security**: administrator password minimum length and
  required character classes, password expiry days, session timeout, idle
  timeout, maximum failed sign-ins, lockout duration, captcha on sign-in, MFA
  requirement, back-office IP allow-list.
- FR-settings-admin-4 **Notifications**: which events notify administrators
  (job failure, security event, new order, refund request), default notification
  channels, notification digest frequency.
- FR-settings-admin-5 **Announcement banner**: an optional message shown at the
  top of every back-office page, with severity and dismissibility.
- FR-settings-admin-6 Every change writes a `security_audit_logs` entry with the
  old and new value.

### 3.7 `settings-product` — customer-facing product configuration

These settings govern the product the customers use. They are separate from
`settings-admin` and are edited on their own page by their own permission.

- FR-settings-product-1 Typed, validated, audited key/value settings.
- FR-settings-product-2 **Site**: site name, URL, logo, favicon, description, SEO
  keywords, ICP filing, copyright, timezone, default language, currency,
  currency display format.
- FR-settings-product-3 **Authentication**: enabled sign-in methods (password,
  email code, SMS code, OAuth providers), MFA requirement, session timeout,
  trusted-device duration, account lockout policy for product accounts.
- FR-settings-product-4 **Registration**: enabled, identifier method (email /
  phone / username), verification requirement, default membership tier, terms
  acceptance, invitation-only mode.
- FR-settings-product-5 **Announcement presentation**: enabled, display position
  (sign-in page, home, modal), display mode, dismissibility.
- FR-settings-product-6 **Features**: individual on/off switches per product
  capability (catalog, cart, checkout, coupons, membership, subscriptions,
  reviews, wishlist) plus a maintenance mode with a message.
- FR-settings-product-7 **Commerce**: default currency, tax display mode
  (inclusive / exclusive), tax rate, order auto-close minutes for unpaid orders,
  refund window days, invoice numbering prefix.
- FR-settings-product-8 Every change writes a `security_audit_logs` entry with
  the old and new value.

### 3.8 `announcement`

- FR-announcement-1 Create, edit, publish, archive and expire announcements with
  a rich-text body and attachments.
- FR-announcement-2 Type (notice / announcement / maintenance / update) and
  severity (normal / important / urgent) drive presentation.
- FR-announcement-3 Schedule by publish-at and expire-at.
- FR-announcement-4 Target an audience: everyone, administrators only, product
  users only, or a set of departments.
- FR-announcement-5 Pin to the top of the list and workbench.
- FR-announcement-6 Track read state per reader; show read counts and who has not
  read a targeted announcement.
- FR-announcement-7 Surface unread announcements as `workbench` messages.

### 3.9 `workbench`

- FR-workbench-1 **Todos**: create, edit, complete, reopen, cancel, prioritise
  and schedule a personal todo with optional due date and reminder.
- FR-workbench-2 Link a todo to another record so the workbench can deep-link.
- FR-workbench-3 **Messages**: a per-administrator inbox fed by system events,
  announcements and explicit sends; mark read, mark all read, delete.
- FR-workbench-4 Broadcast a message to a department, a role or all
  administrators; delivery fans out to per-recipient records.
- FR-workbench-5 **Recent visits**: automatically record the last N distinct
  pages an administrator visited, with title, path, icon and visit count.
- FR-workbench-6 **Quick links**: per-administrator shortcut tiles, seeded with
  defaults and freely editable.
- FR-workbench-7 The workbench renders only widgets whose owning module is
  enabled in settings and whose permission the actor holds.
- FR-workbench-8 Business widgets (today's orders, pending refunds, low stock,
  expiring memberships) appear only when the business domain is enabled and the
  actor holds the corresponding permission.

### 3.10 `jobs` — scheduled tasks

- FR-jobs-1 Task definitions are registered in code; the catalogue of executable
  handlers is fixed at build time and not user-editable.
- FR-jobs-2 Persist per-task scheduling metadata: cron expression, enabled state,
  concurrency policy, timeout, retry policy, failure notifications.
- FR-jobs-3 Enable, disable, pause and resume a task at runtime.
- FR-jobs-4 Run a task manually, recording the trigger source.
- FR-jobs-5 Record every execution: start, finish, duration, status, output, error.
- FR-jobs-6 Enforce the concurrency policy and timeout.
- FR-jobs-7 Retry a failed run per its retry policy, with bounded attempts and
  backoff.
- FR-jobs-8 Notify configured administrators on failure.
- FR-jobs-9 Prevent two instances from running the same task concurrently; a
  single in-process scheduler is assumed.
- FR-jobs-10 Built-in platform tasks: audit retention, login-log retention,
  expired-session cleanup, announcement publish/expire, temporary-file cleanup,
  metric snapshot.
- FR-jobs-11 Built-in business tasks: membership expiry, subscription renewal,
  subscription expiry, dunning retry, unpaid-order auto-close, low-stock alert.

### 3.11 `integrations`

- FR-integrations-1 Manage provider configurations of type email, SMS, object
  storage, identity provider, payment provider and outbound webhook.
- FR-integrations-2 Each provider has a driver, a non-secret configuration
  object, an encrypted secret object, an enabled state and a default flag per
  channel.
- FR-integrations-3 **Email**: SMTP plus at least one transactional API driver,
  sender name and address, test-send action.
- FR-integrations-4 **SMS**: at least one regional gateway driver, template
  support, test-send action.
- FR-integrations-5 **Object storage**: S3-compatible plus at least one regional
  provider; endpoint, bucket, region, path prefix; connectivity test.
- FR-integrations-6 **Identity providers**: OAuth2 / OIDC and LDAP, with a
  configurable attribute-to-field mapping.
- FR-integrations-7 **Payment providers**: at least one aggregator driver with
  hosted checkout or intent-based flow, webhook signature verification, and a
  sandbox/test mode flag.
- FR-integrations-8 Secrets are encrypted at rest and never returned to the
  browser. The edit form shows a mask; submitting the mask leaves the stored
  secret unchanged.
- FR-integrations-9 Every outbound attempt is logged with channel, provider,
  action, duration, status and error.
- FR-integrations-10 Exactly one provider per channel may be the default; setting
  a new default clears the previous one atomically.
- FR-integrations-11 Deleting a provider referenced by settings, by stored files
  or by a payment transaction is refused with an explanation.

### 3.12 `monitoring`

- FR-monitoring-1 Live runtime panel: uptime, goroutines, heap, allocations, GC,
  CPU count, Go version.
- FR-monitoring-2 Database panel: main and auxiliary database size, table count,
  WAL size, slow-query summary from PocketBase's log collection.
- FR-monitoring-3 Storage panel: total uploaded bytes, file count, 24-hour growth.
- FR-monitoring-4 Traffic panel: requests per minute, error rate, p50/p95/p99
  latency, derived from the audit logs.
- FR-monitoring-5 Health endpoint reporting application, database and scheduler
  liveness separately.
- FR-monitoring-6 Optional periodic metric snapshots for 24-hour and 7-day trends.
- FR-monitoring-7 **System information**: application version, PocketBase version,
  Go version, build time, commit, OS, architecture, data directory, configuration
  file in use.
- FR-monitoring-8 **System updates**: check for a newer release, show release
  notes and channel (stable / beta), record the outcome of an applied update.
- FR-monitoring-9 Applying an update is an assisted, explicit action with a
  recorded before/after version; the platform never self-updates silently.

### 3.13 `developer`

- FR-developer-1 Serve an OpenAPI 3 specification for the panel's JSON API.
- FR-developer-2 Serve an interactive Swagger UI at a permission-gated route.
- FR-developer-3 Keep the specification in sync with handlers, verified by a test
  that fails when a route exists without a documented operation.
- FR-developer-4 Manage API keys: name, owning account, scopes, rate limit, IP
  allow-list, expiry.
- FR-developer-5 Display a new key's secret exactly once. Only a hash and a
  searchable prefix are stored.
- FR-developer-6 Revoke a key immediately; a revoked key is rejected on its next
  request.
- FR-developer-7 Record per-key usage: last used time, request count, most recent
  source IP.
- FR-developer-8 Scope a key to a subset of permission points; effective authority
  is the intersection of the owner's permissions and the key's scopes.
- FR-developer-9 Rate-limit per key and reject requests from IPs outside the
  allow-list.

### 3.14 `catalog` — products

- FR-catalog-1 Product CRUD: name, slug, subtitle, rich-text description, images,
  category, brand, tags, status (draft / on sale / off shelf / archived),
  SEO fields.
- FR-catalog-2 Category tree with sort order, icon and status.
- FR-catalog-3 Product variants (SKUs): attribute combination (for example colour
  and size), SKU code, barcode, price, cost, weight, stock, status.
- FR-catalog-4 Product attributes: define attribute names and their values, then
  generate the variant matrix from selected values.
- FR-catalog-5 Pricing in integer minor units with an explicit currency: list
  price, sale price, cost price. Reject a sale price above the list price.
- FR-catalog-6 Stock: quantity per SKU, low-stock threshold, and a stock
  adjustment ledger recording delta, reason, operator and time. Stock is never
  edited without a ledger entry.
- FR-catalog-7 Reserve stock on order creation and release it when an unpaid order
  closes or a payment fails.
- FR-catalog-8 Bulk operations: on/off shelf, price adjustment by percentage or
  fixed delta, category move, tag add/remove.
- FR-catalog-9 Search and filter by category, status, brand, price range, stock
  level and free text.
- FR-catalog-10 Refuse to delete a product referenced by an order; offer
  off-shelf instead.
- FR-catalog-11 Product media is stored through `storage` and reference-counted.

### 3.15 `membership` — membership tiers and plans

- FR-membership-1 Membership tier catalogue: name, code, level, icon,
  description, benefits, status, sort order.
- FR-membership-2 Benefits are declared as typed entries (discount rate, free
  shipping, points multiplier, exclusive content access, priority support) so
  other modules can query them rather than parse prose.
- FR-membership-3 Membership plans: a purchasable plan that grants a tier for a
  duration, with price, currency, duration, tier and status. A plan is an
  ordinary `catalog` product so it can be bought, discounted and refunded
  through the same pipeline.
- FR-membership-4 Grant, extend, downgrade, cancel or revoke a user's membership
  manually, recording the operator and reason.
- FR-membership-5 Per-user membership history: tier, start, expiry, status,
  source (purchase / gift / trial / admin / promotion), related order.
- FR-membership-6 Automatic expiry transition handled by a `jobs` task; an
  expiring-soon reminder is emitted at a configurable lead time.
- FR-membership-7 Query accounts by tier, by expiry window and by membership
  source.
- FR-membership-8 Refuse to delete a tier that has active memberships or that a
  plan references.

### 3.16 `promotions` — coupons and discounts

- FR-promotions-1 Coupon templates: name, code (or auto-issued), type (fixed
  amount / percentage / free shipping), value, currency, minimum spend, maximum
  discount cap.
- FR-promotions-2 Validity: an absolute window, or a relative period counted from
  the moment of claim.
- FR-promotions-3 Issuance limits: total quantity, per-user limit, claimed and
  used counters, and a per-campaign budget cap.
- FR-promotions-4 Applicability: all products, selected categories, selected
  products, or selected membership tiers; with explicit exclusions.
- FR-promotions-5 Stacking rules: whether the coupon stacks with other coupons,
  with membership discounts, and with campaign pricing. The evaluator applies at
  most the configured number of stackable promotions.
- FR-promotions-6 Issue coupons to a user segment, to named accounts, by public
  code claim, or automatically on registration or membership purchase.
- FR-promotions-7 Redeem and void with an auditable ledger per coupon instance.
- FR-promotions-8 Campaigns group coupons with a schedule and an on/off switch.
- FR-promotions-9 A single deterministic evaluator computes the discount for a
  candidate order and explains its result line by line. Order creation and the
  checkout preview call the same evaluator.
- FR-promotions-10 Refuse to delete a coupon instance that has been redeemed.

### 3.17 `subscriptions` — recurring billing

- FR-subscriptions-1 Subscription plans: name, billing interval (day / week /
  month / year) and interval count, price, currency, trial days, tier granted,
  status. A plan is an ordinary `catalog` product.
- FR-subscriptions-2 Subscribe an account to a plan, with optional trial.
- FR-subscriptions-3 Cancel immediately or at the end of the current period;
  record who cancelled and why.
- FR-subscriptions-4 Pause and resume a subscription, preserving the remaining
  period.
- FR-subscriptions-5 Generate an order per billing cycle and drive it through
  `payments`.
- FR-subscriptions-6 Automatic renewal handled by a `jobs` task, with a
  configurable retry and dunning schedule on failure. Enter `past_due` after the
  first failure and cancel after the retry budget is exhausted.
- FR-subscriptions-7 Plan change with proration: upgrade takes effect immediately
  and charges the prorated difference; downgrade takes effect at the next cycle.
- FR-subscriptions-8 Subscription status lifecycle: trialing, active, past_due,
  paused, cancelled, expired.
- FR-subscriptions-9 Per-subscription history of cycles, charges, plan changes and
  status transitions.
- FR-subscriptions-10 Refuse to delete a plan that has active subscriptions.

### 3.18 `orders`

- FR-orders-1 Order lifecycle: pending payment, paid, fulfilling, completed, plus
  cancelled and closed. Transitions are validated against a state machine; an
  illegal transition is rejected with an explanation.
- FR-orders-2 Order items snapshot the product name, SKU description, unit price,
  quantity and image at purchase time. Later catalogue edits never change a
  placed order.
- FR-orders-3 Amount breakdown in integer minor units: subtotal, discount,
  membership discount, coupon discount, shipping, tax, grand total, amount paid,
  amount refunded. The breakdown is stored, not recomputed on read.
- FR-orders-4 Record which coupon instances and which membership tier were applied
  at creation.
- FR-orders-5 Shipping and billing address, contact name and phone.
- FR-orders-6 Administrative creation of an order on behalf of an account, and
  editing of an order while it is still pending payment.
- FR-orders-7 Cancel and close with a reason. Cancelling a paid order is refused;
  that is a refund.
- FR-orders-8 Admin and customer notes, kept separately.
- FR-orders-9 Search and filter by order number, account, status, payment status,
  date range, amount range and applied coupon.
- FR-orders-10 Export a filtered order list to CSV.
- FR-orders-11 Every status transition records actor, time, from-state, to-state
  and reason in the order timeline.
- FR-orders-12 Stock reservation follows FR-catalog-7.

### 3.19 `payments`

- FR-payments-1 Create a payment intent for an order: amount, currency, provider,
  method, expiry.
- FR-payments-2 Record each payment attempt as a transaction with the provider
  transaction id, amount, status, and the raw callback payload.
- FR-payments-3 Transaction status: pending, processing, succeeded, failed,
  cancelled, expired.
- FR-payments-4 Handle provider callbacks and webhooks with signature
  verification and idempotency. Replaying the same callback must not double-apply
  a payment.
- FR-payments-5 Support multiple attempts against one order, and partial payments
  where the provider allows them.
- FR-payments-6 Mark an order paid manually for offline payment (bank transfer,
  cash), with a mandatory reason, the operator, and an audit entry.
- FR-payments-7 Reconcile provider settlement records against local transactions,
  surfacing mismatches (paid locally but not settled, and the reverse).
- FR-payments-8 Refuse to alter a succeeded transaction; corrections go through
  `refunds`.
- FR-payments-9 Payment provider configuration resolves from `integrations` by
  channel and default flag.

### 3.20 `refunds`

- FR-refunds-1 Create a refund request against an order: full or partial, by
  amount or by order item and quantity.
- FR-refunds-2 Record the reason, the requester, and supporting attachments.
- FR-refunds-3 Approval workflow: pending, approved, rejected, with approver,
  decision time and comment. A configurable auto-approval threshold may bypass
  manual approval for small amounts.
- FR-refunds-4 Execution against the payment provider, recording the provider
  refund id and the raw response.
- FR-refunds-5 Refund status: pending, approved, rejected, processing, succeeded,
  failed. A failed execution may be retried.
- FR-refunds-6 Support multiple refunds against one order, capped at the amount
  paid; the cap is enforced at approval time under a transaction.
- FR-refunds-7 Maintain a per-order refund ledger and update the order's amount
  refunded and status accordingly.
- FR-refunds-8 A refund that brings the order total to zero marks the order
  refunded and restores stock where configured.
- FR-refunds-9 Every state change is audited at `info` or higher severity.

## 4. Requirements traceability

### 4.1 Platform items

| Original item | Module | Requirement ids |
| --- | --- | --- |
| 1. Workbench — todos | `workbench` | FR-workbench-1, -2 |
| 1. Workbench — messages | `workbench` | FR-workbench-3, -4 |
| 1. Workbench — recent visits | `workbench` | FR-workbench-5 |
| 1. Workbench — quick entries | `workbench` | FR-workbench-6, -7, -8 |
| 2. Users — account | `identity` | FR-identity-1..7 |
| 2. Users — login records | `audit`, `identity` | FR-audit-3, FR-identity-15 |
| 2. Users — preferences | `identity` | FR-identity-16 |
| 2. Users — membership | `membership` | FR-membership-1..8 |
| 2. Users — sessions | `identity` | FR-identity-14 |
| 3. Organization — departments | `organization` | FR-organization-1..3, -6, -7 |
| 3. Organization — positions | `organization` | FR-organization-4, -7 |
| 3. Organization — employees | `organization`, `identity` | FR-identity-8, -9, FR-organization-7, -8 |
| 3. Organization — org chart | `organization` | FR-organization-5 |
| 4. Files | `storage` | FR-storage-1..10 |
| 5. Backend — administrators (account) | `identity` | FR-identity-10..13 |
| 5. Backend — administrators (login records) | `audit`, `identity` | FR-audit-3, FR-identity-15 |
| 5. Backend — administrators (preferences) | `identity` | FR-identity-16 |
| 5. Backend — administrators (sessions) | `identity` | FR-identity-14 |
| 5. Backend — announcements | `announcement` | FR-announcement-1..7 |
| 5. Backend — **settings (back-office)** | `settings-admin` | FR-settings-admin-1..6 |
| 5. Backend — scheduled tasks | `jobs` | FR-jobs-1..11 |
| 5. Backend — operation logs | `audit` | FR-audit-1, -2, -6..10 |
| 5. Backend — login logs | `audit` | FR-audit-3, -6 |
| 5. Backend — security audit logs | `audit` | FR-audit-4, -6 |
| 5. Backend — exception logs | `audit` | FR-audit-5, -6 |
| 6. System — menus | `rbac` | FR-rbac-6, -7 |
| 6. System — dictionaries | `rbac` | FR-rbac-8, -9 |
| 6. System — permissions | `rbac` | FR-rbac-1..5, -10, -11 |
| 6. System — **settings / site** | `settings-product` | FR-settings-product-2 |
| 6. System — **settings / authentication** | `settings-product` | FR-settings-product-3 |
| 6. System — **settings / registration** | `settings-product` | FR-settings-product-4 |
| 6. System — **settings / announcement** | `settings-product` | FR-settings-product-5 |
| 6. System — **settings / features (toggles)** | `settings-product` | FR-settings-product-6 |
| 6. System — monitoring | `monitoring` | FR-monitoring-1..7 |
| 6. System — integrations (email) | `integrations` | FR-integrations-1..3, -9..11 |
| 6. System — integrations (SMS) | `integrations` | FR-integrations-1, -2, -4, -9..11 |
| 6. System — integrations (object storage) | `integrations` | FR-integrations-1, -2, -5, -9..11 |
| 6. System — integrations (identity providers) | `integrations` | FR-integrations-1, -2, -6, -9..11 |
| 6. System — system information / updates | `monitoring` | FR-monitoring-7..9 |
| 7. Developer — API docs (Swagger) | `developer` | FR-developer-1..3 |
| 7. Developer — API Key | `developer` | FR-developer-4..9 |

### 4.2 Business items

| Original item | Module | Requirement ids |
| --- | --- | --- |
| 8. Business — membership management | `membership` | FR-membership-1..8 |
| 8. Business — product management | `catalog` | FR-catalog-1..11 |
| 8. Business — order management | `orders` | FR-orders-1..12 |
| 8. Business — payment management | `payments` | FR-payments-1..9 |
| 8. Business — refund management | `refunds` | FR-refunds-1..9 |
| 8. Business — coupon management | `promotions` | FR-promotions-1..10 |
| 8. Business — subscription management | `subscriptions` | FR-subscriptions-1..10 |

### 4.3 Interpretation notes

- **Two settings surfaces, deliberately not merged** (revision 2).
  `settings-admin` configures the back-office console — administrator password
  policy, session and idle timeouts, captcha, MFA, console branding, administrator
  notifications. `settings-product` configures the customer-facing product — site
  identity, end-user authentication methods, registration rules, announcement
  presentation and product feature toggles. They are separate pages, separate
  registries, separate permissions and separate audit categories. Only the
  storage table and the typed validation engine are shared.
  **Assumption to confirm:** the sub-items you listed under *System → Settings*
  (site, authentication, registration, announcement, features) are read as the
  product surface, because registration and authentication there describe the
  end-user journey. If they are meant as back-office settings, the two registries
  swap contents and nothing else changes.
- **Membership is now a paid capability.** Revision 1 assumed membership without
  orders or payments and flagged it as open question OQ-1. The business domain
  resolves that: a membership plan is an ordinary catalog product, so buying
  membership, paying for it and refunding it reuse the order and payment
  pipeline. The `member_levels` / `user_memberships` shape from revision 1 is
  retained and extended with plans and orders.
- **Products, orders, payments and refunds are back-office management surfaces.**
  This initiative does not ship a customer-facing storefront or checkout UI; it
  manages the data and provides the JSON API a storefront would call.

## 5. Open questions

| Id | Question | Blocks | Default if unanswered |
| --- | --- | --- | --- |
| OQ-1 | *(resolved)* Membership without orders was assumed in revision 1; the business domain supersedes it. | — | Resolved |
| OQ-2 | Settings surface mapping — see § 4.3. | `settings-admin`, `settings-product` | As stated in § 4.3 |
| OQ-3 | Is row-level data scope required, or are functional permissions sufficient? | `rbac` | Data scope included |
| OQ-4 | Is the current `_superusers` panel sign-in replaced by the admin profile, or kept as break-glass? | `identity` | `users` + admin profile is the panel identity; `_superusers` retained as break-glass |
| OQ-5 | Are announcements visible to product users, or administrators only? | `announcement` | Both, audience-selectable |
| OQ-6 | Retention period per audit log type? | `audit`, `jobs` | 180 days operation/login, 365 days security, 90 days exception |
| OQ-7 | Is the OpenAPI specification hand-written or generated from handler annotations? | `developer` | Generated (swaggo) with a drift test |
| OQ-8 | Are orders, payments and refunds three modules or one `commerce` module with three packages? | boundary | Three modules, as mapped |
| OQ-9 | Is a physical-goods flow (shipping, tracking, address book) in scope, or digital goods only? | `orders`, `catalog` | Address captured on the order; no carrier integration or tracking |
| OQ-10 | Multi-currency: one currency per deployment, or price per currency? | `catalog`, `orders` | One currency per deployment |
| OQ-11 | Are taxes computed by the platform, or supplied by an external engine? | `orders` | A single configurable rate in `settings-product` |
| OQ-12 | Should the customer-facing storefront be a follow-on initiative on the same data? | scope | Out of scope; data model supports it |

## 6. Success criteria

**Platform**

- Every requirement above is implemented or explicitly deferred with a reason.
- A new administrator is onboarded end-to-end (user → employee profile → admin
  profile → role → department) without editing the database directly.
- Every menu item visible to an actor corresponds to a permission the actor
  holds; an unauthorised request returns 403 and writes a `security_audit_logs`
  entry.
- Every state-changing request appears in `operation_logs` with no secret value
  in the stored parameters.
- A failing scheduled task is visible in `job_runs` with its error and produces a
  notification.
- The OpenAPI document describes every registered route, verified by an
  automated drift test.
- Changing a back-office setting affects only the back office; changing a product
  setting affects only the product. Neither page can write to the other's keys.

**Business**

- A product with variants can be created, priced, stocked and put on sale, and
  the stock ledger explains every quantity change.
- An order can be placed for that product, paid through the configured provider,
  and the payment transaction is matched to the order by a verified, idempotent
  callback.
- The same order can be partially refunded twice, never exceeding the amount
  paid, with each refund approved and audited.
- A customer's membership can be purchased, extended, expired by the scheduler
  and reinstated, with a complete history.
- A coupon can be issued, redeemed exactly once, and the discount it produced is
  reproducible by re-running the evaluator on the stored order snapshot.
- A subscription bills on schedule, recovers from one failed payment via dunning,
  and cancels after the retry budget is exhausted.
