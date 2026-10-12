# Capability Map: PocketAdmin Platform

Status: **draft — awaiting review (revision 2)**
Scope: single tenant, one deployment serves one organisation.

This initiative bundles many independently testable capabilities, so it is
decomposed into modules before any module spec is written. Module ids are stable
and are the index for every downstream spec, plan and task list.

## Revision 2 changes

1. **Identity is users-centric.** `users` (PocketBase's own auth collection) is
   the single account. `employees` and `admins` are *profile extensions* of a
   user, not separate identities. See § Identity model.
2. **Settings are two surfaces, not one.** `settings-admin` configures the
   back-office console; `settings-product` configures the customer-facing
   product. They are different concerns and are no longer merged.
3. **A business domain is added**: catalog, membership, promotions, orders,
   payments, refunds and subscriptions.

## Modules

### Platform domain

| Module id | Responsibility | Depends on |
| --- | --- | --- |
| `identity` | The central `users` account: credentials, status, sessions, login history, preferences, plus the employee and administrator profile extensions | — |
| `organization` | Departments, positions, the organisation tree | `identity` |
| `rbac` | Roles, permission points, menus, dictionaries, data scope | `identity` |
| `storage` | File library, folders, attachments, storage drivers | `identity` |
| `audit` | Operation, login, security-audit and exception logs | `identity` |
| `settings-admin` | Back-office console configuration: admin security policy, console branding, admin notifications | `identity`, `rbac` |
| `settings-product` | Customer-facing product configuration: site, user authentication, registration, announcement presentation, product feature flags | `identity`, `rbac` |
| `announcement` | Announcements and per-reader read state, for both audiences | `rbac` |
| `workbench` | Todos, messages, recent visits, quick links | `identity`, `rbac` |
| `jobs` | Scheduled task definitions, execution history, manual runs | `rbac` |
| `integrations` | Mail, SMS, object storage, identity providers, payment providers, outbound call logs | `settings-product` |
| `monitoring` | Runtime metrics, health, system information and updates | `settings-admin` |
| `developer` | OpenAPI reference, API keys and their usage | `identity`, `rbac` |

### Business domain

| Module id | Responsibility | Depends on |
| --- | --- | --- |
| `catalog` | Products, categories, SKUs, pricing, stock | `identity`, `rbac` |
| `membership` | Membership tiers, membership plans, per-user memberships | `identity`, `catalog` |
| `promotions` | Coupons, discount rules, campaign eligibility | `catalog` |
| `subscriptions` | Recurring plans, subscription lifecycle, billing cycles, renewals | `catalog`, `membership` |
| `orders` | Order aggregate: cart snapshot, order items, order lifecycle | `catalog`, `promotions`, `membership` |
| `payments` | Payment intents, transactions, provider callbacks, reconciliation | `orders`, `integrations` |
| `refunds` | Refund requests, approval workflow, execution | `payments`, `orders` |

### Identity model

`users` is the centre. Exactly one account per person, holding credentials and
shared profile data. Two optional 1:1 profile extensions give that account extra
meaning:

```
                    ┌──────────────────────┐
                    │ users  (auth)        │
                    │ credentials, status  │
                    └───────────┬──────────┘
                                │
              ┌─────────────────┴─────────────────┐
              ▼                                   ▼
   ┌──────────────────────┐            ┌──────────────────────┐
   │ employees (1:1 opt.) │            │ admins (1:1 opt.)    │
   │ HR / organisation    │            │ back-office access   │
   └──────────────────────┘            └──────────────────────┘
```

- A user with no profile is a plain customer.
- A user with an `employees` profile is staff (organisation membership).
- A user with an `admins` profile may sign in to the back office; that profile
  carries roles and data scope.
- A user may hold both profiles; the same credentials then serve both surfaces.

The back office authenticates against `users` and then requires an active
`admins` profile. `_superusers` remains PocketBase's own break-glass identity for
the bundled dashboard at `/_/`.

## Dependency direction

Arrows point one way; there are no cycles.

```
identity
├── organization
├── rbac ──┬── settings-admin ─── monitoring
│          │
│          ├── settings-product ─── integrations
│          ├── announcement
│          ├── jobs
│          └── developer
├── audit
├── storage
└── workbench

catalog ──┬── membership ──┬── subscriptions
          │                │
          ├── promotions ──┤
          │                │
          └────────────────┴── orders ──── payments ──── refunds
                                                            ▲
                                        integrations ───────┘
```

`audit` deliberately depends only on `identity`: every other module writes audit
records, so `audit` must sit below them and must never import from them.

`orders` owns the order aggregate. `payments` and `refunds` change its payment
state but never its contents.

## Build order

| Wave | Modules | Rationale |
| --- | --- | --- |
| 1 | `identity` | Every other module needs an authenticated actor. |
| 2 | `organization`, `rbac`, `storage`, `audit` | Independent of each other; the basis of "who may do what" and "what happened". |
| 3 | `settings-admin`, `settings-product` | Both need `rbac` to restrict who may change configuration. |
| 4 | `announcement`, `workbench`, `jobs`, `developer` | Feature surfaces over waves 1–3. |
| 5 | `integrations`, `monitoring` | Consume the settings surfaces. `integrations` must precede `payments`. |
| 6 | `catalog` | The business domain's root entity. |
| 7 | `membership`, `promotions`, `subscriptions` | Independent of each other; all require `catalog`. |
| 8 | `orders` | Requires catalog, promotions and membership. |
| 9 | `payments` | Requires orders and integrations. |
| 10 | `refunds` | Requires payments. |

Waves 1–5 are the platform and are independently useful. Waves 6–10 are the
business domain and can be deferred or delivered separately.

## Boundary notes

- **Two settings surfaces, one settings engine.** `settings-admin` and
  `settings-product` are distinct concerns with distinct pages, registries,
  permissions and audit categories. They share the storage table and the typed
  validation engine, in the same way that orders and refunds are distinct
  concerns that share a database.
- **`rbac` owns menus and dictionaries.** Navigation is a projection of
  permissions, not an independent source of truth.
- **`integrations` owns credentials and drivers** for email, SMS, object storage,
  identity providers *and payment providers*. Settings reference a provider by
  `code`; they never embed secrets.
- **`audit` is append-only.** No module may update or delete audit records except
  through a retention job in `jobs`.
- **`orders`, `payments` and `refunds` share one lifecycle.** They are separate
  modules because each has its own workflow, permissions and failure modes, but
  they operate on the same aggregate. If fewer boundaries are wanted, they
  collapse into one `commerce` module with three packages — say so and the map
  changes; nothing else does.
- **Money is integer minor units** everywhere, with an explicit currency. No
  floating point in any business collection.

## Interface ownership

| Boundary | Contract owner | Documented in |
| --- | --- | --- |
| `identity` → all | Actor, session and profile accessors; `RequireAdminProfile` | `docs/design.md` |
| `rbac` → all | `RequirePermission` middleware and menu projection | `docs/design.md` |
| `settings-*` → all | Typed setting registry and cached accessor | `docs/design.md` |
| `integrations` → `payments` | Payment provider driver resolution by channel | `docs/design.md` |
| `storage` → all | File upload/reference API used by rich text and product images | `docs/design.md` |
| `orders` → `payments`, `refunds` | Order aggregate and its payment-state transitions | `docs/design.md` |

## Out of scope (for this initiative)

- Multi-tenant isolation, per-tenant billing and quotas.
- A shipped customer-facing storefront application. The business modules manage
  products, orders and payments *in the back office*; the customer UI is a
  separate initiative that would consume the same data.
- Horizontal scale-out. `jobs` assumes a single in-process scheduler.
- Warehouse, shipping and fulfilment logistics.
