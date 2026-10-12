# PocketAdmin Platform — Feature List

Every feature the platform is specified to provide, grouped by module. Where a
feature needs acceptance criteria, the corresponding `FR-*` id in
[`requirements.md`](requirements.md) is the reference.

## Platform

### `identity` — accounts and profiles

- One account per person: the central `users` record with email, phone, password,
  name, nickname, avatar, gender, birthday, status, tags, source and remarks.
- One set of credentials serves both surfaces; the back office additionally
  requires an active admin profile.
- Optional **employee profile** (1:1 with a user): employee number, department,
  position, line manager, hire date, employment type, work contact details,
  employment status.
- Optional **admin profile** (1:1 with a user): granted roles, back-office status,
  protected bootstrap account, per-administrator MFA override.
- Attaching and detaching profiles, with detaching never deleting the account and
  never losing HR history.
- Sign-in, sign-out, password reset and password change.
- Account lockout after repeated failed sign-ins, with manual or timed unlock.
- Multi-factor authentication.
- Session list per account and surface (device, IP, user agent, created, last
  seen, expiry) with revoke-one and revoke-all.
- Sign-in history per account: time, IP, location, device, surface, result,
  failure reason.
- Per-account, per-surface preferences: theme, language, page size, sidebar
  state, timezone, notification channels.
- Impersonation for support, banner-marked, write-blocked, and audited.
- Ban, unban and soft delete, each revoking sessions.
- Uniqueness of email and phone with clear errors.

### `organization` — departments, positions, staff

- Department tree: create, rename, re-parent, reorder, delete.
- Materialised path per department, so "this department and all descendants" is
  one indexed query.
- Department leader and contact details.
- Positions belonging to a department, with level, category and headcount.
- Line-manager relationships, with cycle prevention.
- Organisation chart and indented list views with member counts.
- Refusal to delete a department or position that still has active staff.

### `rbac` — roles, permissions, menus, dictionaries

- Role management: name, code, description, status, sort order, protected system
  roles.
- Catalogue of permission points keyed `<module>:<resource>:<action>`, declared
  in code and mirrored into the database with a drift test.
- Granting permission points and menu nodes to roles.
- Navigation derived from permissions: the sidebar is the set of menu nodes the
  actor is permitted to see, never an independently authored list.
- Menu tree management: directory, menu, button and external-link nodes with
  title, route name, path, icon, sort order, visibility and permission code.
- Platform discriminator on menu nodes, reserving the catalogue for a future
  public surface.
- Dictionary types and items with labelling, ordering, status, colour and CSS
  class, including tree-shaped dictionaries.
- Dictionary values as the single source of truth for form and filter option
  sets.
- Row-level data scope per role: all, own department, own department and
  descendants, own records, or a custom department set.
- Cached permission resolution, invalidated on change.
- Startup self-check that fails the build on an unsafe route without a
  permission requirement.

### `storage` — file library

- Upload of one or many files, with drag-and-drop and progress.
- Folder tree with rename, move and delete.
- Recorded metadata: size, MIME type, extension, checksum, uploader, time.
- Upload policy enforcement: allowed extensions, maximum size, image dimension
  limits.
- MIME sniffing rather than trusting the client-supplied extension.
- Image and common-document preview; download for everything else.
- Reference counting, so a referenced file cannot be deleted.
- Pluggable storage drivers: local filesystem or a configured object store.
- Checksum-based deduplication within a folder.
- Clear separation of record-owned files from library files.

### `audit` — operation, login, security and exception logs

- Operation log of every state-changing request: actor, module, action, method,
  path, sanitised parameters, result, status code, duration, IP, user agent,
  request id.
- Secret redaction by deny-key-name plus a safe-field allow-list, with a test per
  denied key.
- Login log for both surfaces, including attempts that matched no account.
- Security audit log: permission denied, password changed, role changed, profile
  attached or detached, account locked, session revoked, settings changed, API
  key created or revoked, data exported, impersonation started, login anomaly,
  refund approved, manual payment recorded, coupon voided.
- Exception log with message, stack, request id, route and actor.
- Filtering and search by actor, time range, module, result and free text, with
  pagination and CSV export.
- Append-only through the application; retention is the only deletion path.
- Retention jobs with per-type periods.
- Correlation with PocketBase's own `_logs` collection via request id.
- Business state transitions affecting money recorded at `info` or higher.

### `settings-admin` — back-office console configuration

- Typed, validated, audited key/value settings, separate from the product
  surface.
- Console settings: name, logo, favicon, accent colour, default theme, default
  language, default page size, footer text.
- Security settings: password minimum length and character classes, password
  expiry, session timeout, idle timeout, maximum failed sign-ins, lockout
  duration, captcha on sign-in, MFA requirement, back-office IP allow-list.
- Notification settings: which events notify administrators, default channels,
  digest frequency.
- Back-office announcement banner with severity and dismissibility.
- Every change recorded with its old and new value.

### `settings-product` — customer-facing product configuration

- Typed, validated, audited key/value settings, separate from the console
  surface.
- Site settings: name, URL, logo, favicon, description, SEO keywords, ICP filing,
  copyright, timezone, default language, currency, currency display format.
- Authentication settings: enabled sign-in methods, OAuth providers, MFA
  requirement, session timeout, trusted-device duration, lockout policy.
- Registration settings: enabled, identifier method, verification requirement,
  default membership tier, terms acceptance, invitation-only mode.
- Announcement presentation: enabled, display position, display mode,
  dismissibility.
- Product feature toggles: catalogue, cart, checkout, coupons, membership,
  subscriptions, reviews, wishlist, plus maintenance mode with a message.
- Commerce settings: default currency, tax display mode, tax rate, unpaid-order
  auto-close, refund window, invoice numbering prefix.
- Neither surface can write the other's keys.

### `announcement`

- Rich-text announcements with attachments.
- Type (notice, announcement, maintenance, update) and severity (normal,
  important, urgent) driving presentation.
- Scheduling by publish-at and expire-at.
- Audience targeting: everyone, administrators only, product users only, or a set
  of departments.
- Pinning to the top of the list and the workbench.
- Per-reader read state, read counts, and who has not read a targeted
  announcement.
- Unread announcements surfaced as workbench messages.

### `workbench`

- Personal todos: create, edit, complete, reopen, cancel, prioritise, schedule,
  due dates and reminders.
- Todos linkable to other records for deep-linking.
- Per-administrator message inbox fed by system events, announcements and
  explicit sends.
- Mark read, mark all read, delete.
- Broadcast to a department, a role, or all administrators, with per-recipient
  delivery records.
- Recently visited pages with title, path, icon and visit count.
- Per-administrator quick-link tiles, seeded with defaults and editable.
- Widgets rendered only when their module is enabled and the actor holds the
  permission.
- Business widgets: today's orders, pending refunds, low stock, expiring
  memberships.

### `jobs` — scheduled tasks

- Task definitions registered in code; the handler catalogue is fixed at build
  time and not user-editable.
- Scheduling metadata: cron expression, enabled state, concurrency policy,
  timeout, retry policy, failure notifications.
- Enable, disable, pause and resume at runtime.
- Manual runs, recording the trigger source.
- Execution history: start, finish, duration, status, output, error.
- Concurrency policy enforcement and timeouts.
- Bounded retries with backoff.
- Failure notification to configured administrators.
- Built-in platform tasks: audit retention, login-log retention, expired-session
  cleanup, announcement publish and expire, temporary-file cleanup, metric
  snapshot.
- Built-in business tasks: membership expiry, subscription renewal, subscription
  expiry, dunning retry, unpaid-order auto-close, low-stock alert.

### `integrations`

- Provider configurations of type email, SMS, object storage, identity provider,
  payment provider and outbound webhook.
- Per provider: driver, non-secret configuration, encrypted secrets, enabled
  state, default flag per channel, priority.
- Email: SMTP plus a transactional API driver, sender identity, test send.
- SMS: regional gateway drivers, template support, test send.
- Object storage: S3-compatible plus a regional provider; endpoint, bucket,
  region, path prefix; connectivity test.
- Identity providers: OAuth2 / OIDC and LDAP with attribute-to-field mapping.
- Payment providers: aggregator drivers with hosted checkout or intent flows,
  webhook signature verification, sandbox mode.
- Secrets encrypted at rest and never returned to the browser; the edit form
  shows a mask that leaves the stored value unchanged when resubmitted.
- Per-call logging with channel, provider, action, duration, status and error.
- Exactly one default provider per channel, enforced atomically.
- Refusal to delete a provider that settings, stored files or payment
  transactions still reference.

### `monitoring`

- Live runtime panel: uptime, goroutines, heap, allocations, GC, CPU count, Go
  version.
- Database panel: main and auxiliary size, table count, WAL size, slow-query
  summary.
- Storage panel: uploaded bytes, file count, 24-hour growth.
- Traffic panel: requests per minute, error rate, p50/p95/p99 latency.
- Health endpoint reporting application, database and scheduler liveness
  separately.
- Optional periodic metric snapshots for trend charts.
- System information: application version, PocketBase version, Go version, build
  time, commit, OS, architecture, data directory, configuration file in use.
- System updates: check for a newer release, show notes and channel, record the
  outcome of an applied update.
- Assisted, explicit update with a recorded before and after version; never a
  silent self-update.

### `developer`

- OpenAPI 3 specification for the panel's JSON API.
- Interactive Swagger UI behind a permission.
- Drift test that fails when a registered route has no documented operation.
- API keys: name, owning account, scopes, rate limit, IP allow-list, expiry.
- New keys displayed exactly once; only a hash and a searchable prefix stored.
- Immediate revocation.
- Per-key usage: last used time, request count, most recent source IP.
- Scoped authority: the intersection of the owner's permissions and the key's
  scopes.
- Per-key rate limiting and IP allow-list enforcement.

## Business

### `catalog` — products

- Product records: name, slug, subtitle, rich-text description, images,
  category, brand, tags, status, SEO fields.
- Four product kinds: physical, digital, membership plan, subscription plan.
- Category tree with ordering, icon and status.
- Variant (SKU) management: attribute combination, SKU code, barcode, price,
  cost, weight, stock, status.
- Variant attributes and their values, with variant-matrix generation.
- Pricing in integer minor units: list price, sale price, cost price, with a sale
  price that cannot exceed list.
- Append-only stock ledger recording every movement with delta, resulting
  balance, reason, reference and operator.
- Stock reservation on order creation and release on unpaid-order close or
  payment failure.
- Bulk operations: on and off shelf, price adjustment, category move, tag change.
- Search and filter by category, status, brand, price range, stock level and free
  text.
- Refusal to delete a product an order references.
- Product media through the file library, reference-counted.

### `membership`

- Membership tier catalogue: name, code, level, icon, description, status,
  ordering.
- Typed, queryable tier benefits: discount rate, free shipping, points
  multiplier, content access, priority support.
- Purchasable membership plans granting a tier for a duration, priced as ordinary
  catalogue products.
- Manual grant, extend, downgrade, cancel and revoke, recording operator and
  reason.
- Per-account membership history: tier, start, expiry, status, source, related
  order.
- Automatic expiry transition by a scheduled task, with expiring-soon reminders.
- Queries by tier, expiry window and membership source.
- Refusal to delete a tier with active memberships or a referenced plan.

### `promotions` — coupons and discounts

- Coupon templates: name, code or auto-issue, type (fixed amount, percentage,
  free shipping), value, currency, minimum spend, maximum discount cap.
- Validity as an absolute window or a relative period from the moment of claim.
- Issuance limits: total quantity, per-user limit, claimed and used counters,
  campaign budget cap.
- Applicability to all products, categories, products or membership tiers, with
  explicit exclusions.
- Stacking rules: with other coupons, with membership discounts, and a maximum
  number of stacked promotions.
- Issuance to a segment, to named accounts, by public code claim, or automatically
  on registration or membership purchase.
- Redemption and voiding with an auditable per-instance ledger.
- Campaigns grouping coupons with a schedule and on/off switch.
- One deterministic evaluator computing the discount for a candidate order and
  explaining the result line by line, shared by checkout preview and order
  creation.
- Refusal to delete a redeemed coupon instance.

### `subscriptions` — recurring billing

- Subscription plans: billing interval and count, price, currency, trial days,
  tier granted, status, priced as ordinary catalogue products.
- Subscribing an account, with optional trial.
- Cancellation immediately or at period end, recording who and why.
- Pause and resume, preserving remaining paid time.
- One order generated per billing cycle and driven through payments.
- Automatic renewal by a scheduled task, with configurable retry and dunning and
  a bounded budget.
- Plan change with proration: immediate upgrade charged the prorated difference,
  downgrade at the next cycle.
- Status lifecycle: trialing, active, past_due, paused, cancelled, expired.
- Per-subscription history of cycles, charges, plan changes and status
  transitions.
- Refusal to delete a plan with active subscriptions.

### `orders`

- Order lifecycle validated against a state machine, rejecting illegal
  transitions with an explanation.
- Order items snapshotting product name, SKU description, unit price, quantity
  and image at purchase time.
- Stored amount breakdown in integer minor units: subtotal, discount, membership
  discount, coupon discount, shipping, tax, grand total, amount paid, amount
  refunded.
- Recording which coupon instances and which membership tier applied.
- Shipping and billing addresses, contact name and phone.
- Administrative order creation on behalf of an account, and editing while still
  pending payment.
- Cancel and close with a reason; cancelling a paid order refused in favour of a
  refund.
- Separate customer and administrator notes.
- Search and filter by order number, account, status, payment status, date range,
  amount range and applied coupon.
- CSV export of a filtered order list.
- Order timeline recording actor, time, from-state, to-state and reason for every
  transition.
- Stock reservation following the catalogue rules.

### `payments`

- Payment intent per order: amount, currency, provider, method, expiry.
- A transaction record per attempt, with provider transaction id, amount, status
  and raw callback payload.
- Transaction status: pending, processing, succeeded, failed, cancelled, expired.
- Provider callbacks and webhooks with signature verification and idempotency, so
  a replayed callback never double-applies a payment.
- Multiple attempts against one order, and partial payments where supported.
- Manual mark-as-paid for offline settlement, with a mandatory reason, the
  operator and an audit entry.
- Reconciliation of provider settlement records against local transactions,
  surfacing mismatches.
- Refusal to alter a succeeded transaction; corrections go through refunds.
- Provider resolution from integrations by channel and default flag.

### `refunds`

- Refund request against an order: full, partial by amount, or partial by item and
  quantity.
- Reason, requester and supporting attachments.
- Approval workflow: pending, approved, rejected, with approver, decision time and
  comment.
- Configurable auto-approval threshold, never allowing self-approval.
- Execution against the payment provider, recording the provider refund id and
  response.
- Refund status: pending, approved, rejected, processing, succeeded, failed, with
  retry on failure.
- Multiple refunds per order, capped at the amount paid with the cap enforced at
  approval time under a transaction.
- Per-order refund ledger, updating the order's refunded amount and status.
- Full refund restoring stock where configured.
- Every state change audited at `info` or higher.

## Cross-cutting

- All authorisation enforced in the application layer, with collection rules
  locked to superuser-only so the public API surface exposes nothing by accident.
- Every state-changing request attributable, with secrets redacted before
  storage.
- Navigation derived from permissions rather than authored per role.
- Row-level data scope applied through a shared helper.
- Money as integer minor units with an explicit currency everywhere.
- Schema as idempotent Go migrations, one per module, seeded separately.
- Append-only audit; money-bearing records reversed by compensating records,
  never deleted.
