# PocketAdmin Platform — Specification Set

Read in this order. Each document is reviewable on its own.

| # | Document | Answers |
| --- | --- | --- |
| 1 | [`../CAPABILITY-MAP.md`](../CAPABILITY-MAP.md) | What are the modules, how do they depend on each other, and in what order are they built? |
| 2 | [`requirements.md`](requirements.md) | What must the system do? Requirement ids, traceability to the original feature request, open questions. |
| 3 | [`database-design.md`](database-design.md) | What collections exist, what fields and indexes do they carry, and what are the relationships? |
| 4 | [`design.md`](design.md) | How is it built: architecture, authentication, authorisation, audit, jobs, integrations, security, phasing, risks. |

## Status

| Document | Status |
| --- | --- |
| `CAPABILITY-MAP.md` | draft — awaiting review |
| `requirements.md` | draft — awaiting review |
| `database-design.md` | draft — awaiting review |
| `design.md` | draft — awaiting review |

No implementation work starts until the capability map is approved
(`spec-driven-development` Phase 0 gate).

## Module index

Module ids are stable and are used by every downstream spec, plan and task list.

| Module id | Requirements | Collections | Design section |
| --- | --- | --- | --- |
| `identity` | § 3.1 | § 3.1 | § 2, § 3 |
| `organization` | § 3.2 | § 3.2 | § 3.5 |
| `rbac` | § 3.3 | § 3.3 | § 3 |
| `storage` | § 3.4 | § 3.4 | § 8 |
| `audit` | § 3.5 | § 3.9 | § 4 |
| `settings` | § 3.6 | § 3.8 | § 5 |
| `announcement` | § 3.7 | § 3.6 | — |
| `workbench` | § 3.8 | § 3.5 | — |
| `jobs` | § 3.9 | § 3.7 | § 6 |
| `integrations` | § 3.10 | § 3.10 | § 7 |
| `monitoring` | § 3.11 | § 3.11 | § 10 |
| `developer` | § 3.12 | § 3.12 | § 9 |

## Coverage check

Every item in the originating feature request maps to a module; the mapping
table is `requirements.md` § 4. Three items needed a definition and are listed
in § 4.1:

- **Membership** — assumed to be a tiered level plus per-user history, without
  orders or payments.
- **"Settings" listed twice** (Backend and System) — consolidated into one
  `settings` module with five categories. Nothing is lost.
- **Data scope** — assumed to be part of "Permissions" in addition to menu and
  button grants.

## Next step

Review `CAPABILITY-MAP.md` first: module boundaries, dependency direction and
build order are the most expensive things to change later. Once approved, the
per-module specs (`SPEC-identity.md`, `SPEC-rbac.md`, …) are written in build
order, and `design.md` § 16 becomes the milestone plan.
