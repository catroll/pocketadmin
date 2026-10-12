# PocketAdmin Platform — Specification Set

Read in this order. Each document is reviewable on its own.

| # | Document | Answers |
| --- | --- | --- |
| 1 | [`../CAPABILITY-MAP.md`](../CAPABILITY-MAP.md) | What are the modules, how do they depend on each other, and in what order are they built? |
| 2 | [`requirements.md`](requirements.md) | What must the system do? Requirement ids, traceability to the feature request, open questions. |
| 3 | [`database-design.md`](database-design.md) | What collections exist, what fields and indexes do they carry, and what are the relationships? |
| 4 | [`design.md`](design.md) | How the platform is built: architecture, authentication, authorisation, audit, jobs, integrations, security, phasing, risks. |
| 5 | [`business-design.md`](business-design.md) | How the business domain works: pricing pipeline, order state machine, payment idempotency, refund workflow, subscription billing, inventory. |

## Status

| Document | Status |
| --- | --- |
| `CAPABILITY-MAP.md` | draft (revision 2) — awaiting review |
| `requirements.md` | draft (revision 2) — awaiting review |
| `database-design.md` | draft (revision 2) — awaiting review |
| `design.md` | draft (revision 2) — awaiting review |
| `business-design.md` | draft (revision 2) — awaiting review |

No implementation work starts until the capability map is approved
(`spec-driven-development` Phase 0 gate).

## Revision 2 summary

Three corrections from review:

1. **Settings are two surfaces, not one.** `settings-admin` configures the
   back-office console; `settings-product` configures the customer-facing
   product. Separate pages, registries, permissions and audit categories; they
   share only the storage table and the validation engine.
2. **Identity is users-centric.** `users` (PocketBase's own auth collection) is
   the single account. `employees` and `admins` are optional 1:1 profile
   extensions, so one person has one password, one reset flow and one set of
   sessions — while a user may hold neither, either or both profiles.
3. **A business domain is added**: catalog, membership, promotions,
   subscriptions, orders, payments, refunds.

## Module index

Module ids are stable and are used by every downstream spec, plan and task list.
20 modules: 13 platform, 7 business.

### Platform

| Module id | Requirements | Collections | Design |
| --- | --- | --- | --- |
| `identity` | § 3.1 | § 3.1 | `design.md` § 2 |
| `organization` | § 3.2 | § 3.2 | `design.md` § 3.5 |
| `rbac` | § 3.3 | § 3.3 | `design.md` § 3 |
| `storage` | § 3.4 | § 3.4 | `design.md` § 8 |
| `audit` | § 3.5 | § 3.9 | `design.md` § 4 |
| `settings-admin` | § 3.6 | § 3.8 | `design.md` § 5 |
| `settings-product` | § 3.7 | § 3.8 | `design.md` § 5 |
| `announcement` | § 3.8 | § 3.6 | — |
| `workbench` | § 3.9 | § 3.5 | — |
| `jobs` | § 3.10 | § 3.7 | `design.md` § 6 |
| `integrations` | § 3.11 | § 3.10 | `design.md` § 7 |
| `monitoring` | § 3.12 | § 3.11 | `design.md` § 10 |
| `developer` | § 3.13 | § 3.12 | `design.md` § 9 |

### Business

| Module id | Requirements | Collections | Design |
| --- | --- | --- | --- |
| `catalog` | § 3.14 | § 4.1 | `business-design.md` § 3 |
| `membership` | § 3.15 | § 4.2 | `business-design.md` § 10 |
| `promotions` | § 3.16 | § 4.3 | `business-design.md` § 5 |
| `subscriptions` | § 3.17 | § 4.4 | `business-design.md` § 9 |
| `orders` | § 3.18 | § 4.5 | `business-design.md` § 4, § 6 |
| `payments` | § 3.19 | § 4.6 | `business-design.md` § 7 |
| `refunds` | § 3.20 | § 4.7 | `business-design.md` § 8 |

## Coverage check

Every item in the originating feature request maps to a module:

- Platform items 1–7 (workbench, users, organization, files, backend, system,
  developer) — `requirements.md` § 4.1.
- Business items (membership, products, orders, payments, refunds, coupons,
  subscriptions) — `requirements.md` § 4.2.

## Next step

Review `CAPABILITY-MAP.md` first: module boundaries, dependency direction and
build order are the most expensive things to change later. Two specific
questions are worth settling at the same time:

- **OQ-2** — which settings surface owns which keys. `requirements.md` § 4.3
  states the assumption: the *System → Settings* sub-items (site,
  authentication, registration, announcement, features) are read as the product
  surface, because registration and authentication there describe the end-user
  journey.
- **OQ-8** — whether `orders`, `payments` and `refunds` stay three modules or
  collapse into one `commerce` module with three packages.

Once approved, the per-module specs (`SPEC-identity.md`, `SPEC-rbac.md`, …) are
written in build order, and `design.md` § 16 becomes the milestone plan.
