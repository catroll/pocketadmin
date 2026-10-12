# PocketAdmin Platform — Business Domain Design

Version: 0.1 (revision 2 — draft for review)
Covers: `catalog`, `membership`, `promotions`, `subscriptions`, `orders`,
`payments`, `refunds`
Companion documents: [`requirements.md`](requirements.md),
[`database-design.md`](database-design.md), [`design.md`](design.md)

This document covers only what is specific to the business domain. The
platform-wide concerns it depends on — layering, authentication, authorisation,
audit, settings, jobs, integrations, security and testing — are defined in
`design.md` and are not repeated here.

## 1. Domain overview

```
                       ┌──────────┐
                       │ catalog  │  products, variants, stock
                       └────┬─────┘
              ┌─────────────┼─────────────┬──────────────┐
              ▼             ▼             ▼              ▼
       ┌────────────┐ ┌──────────┐ ┌─────────────┐ ┌────────┐
       │ membership │ │promotions│ │subscriptions│ │ orders │
       └────────────┘ └──────────┘ └─────────────┘ └───┬────┘
                                                        │
                                          ┌─────────────┴─────────────┐
                                          ▼                           ▼
                                    ┌──────────┐                ┌─────────┐
                                    │ payments │ ─────────────▶ │ refunds │
                                    └──────────┘                └─────────┘
```

Aggregate boundaries, and what may write what:

| Aggregate | Owner | Others may |
| --- | --- | --- |
| `products` / `product_variants` | `catalog` | Read. Only `catalog` writes. |
| `stock_ledger` | `catalog` | Append via the stock service. Never update or delete. |
| `orders` / `order_items` | `orders` | `payments` and `refunds` change **payment state and counters** through the orders service, never item contents or amounts. |
| `payment_intents` / `payment_transactions` | `payments` | Read. |
| `refunds` | `refunds` | Read. |
| `subscriptions` / `subscription_cycles` | `subscriptions` | Read. |
| `coupon_grants` | `promotions` | `orders` consumes a grant through the promotions service; it never writes the grant directly. |
| `user_memberships` | `membership` | Read. |

The rule that keeps this tractable: **cross-module writes go through the owning
module's service, never through its repository.**

## 2. Money and currency

- Every amount is an **integer in minor units** (cents, fen). No floating point
  anywhere in the domain, including in JSON payloads and template helpers.
- Every amount-carrying record also carries an explicit `currency` text field.
  A value without a currency is a bug.
- A single deployment uses **one currency**, taken from
  `settings-product` § `commerce.defaultCurrency` (open question OQ-10). The
  currency is copied onto each order, transaction and refund so that a later
  configuration change cannot reinterpret historical amounts.
- Arithmetic uses `int64`. Percentage discounts are computed with integer
  arithmetic and an explicit rounding rule: round half away from zero, at the
  **order** level, once, after summing. Intermediate per-line rounding is
  forbidden because it does not sum back to the total.
- A schema test asserts that every field named in the amount registry is an
  integer type. A second test asserts that a sample order's stored breakdown sums
  exactly to `grandTotal`.

## 3. Catalog and inventory

### 3.1 Product kinds

One `products` collection covers four kinds, discriminated by `kind`:

| `kind` | Behaviour | Extra record |
| --- | --- | --- |
| `physical` | Stock tracked per variant; shipping applies | — |
| `digital` | Unlimited stock; no shipping | — |
| `membership_plan` | Grants a tier for a duration on settlement | `member_plans` |
| `subscription_plan` | Recurring billing | `subscription_plans` |

Keeping plans in the catalogue is what lets a membership or a subscription be
discounted, ordered, paid and refunded through exactly the same pipeline as any
other product. There is no parallel "plan purchase" path to maintain.

### 3.2 Stock ledger

Stock is **never written directly**. Every change is an appended `stock_ledger`
row carrying a signed `delta`, the resulting `balanceAfter`, a `reason`, and a
`refType`/`refId` pointing at the cause. `product_variants.stock` is a
materialised cache of the ledger, updated in the same transaction.

| Reason | Trigger | Delta |
| --- | --- | --- |
| `purchase` | Manual stock-in, supplier delivery, import | + |
| `reserve` | Order created (unpaid) | — (into `lockedStock`) |
| `release` | Unpaid order closed or payment failed | + (out of `lockedStock`) |
| `sale` | Payment succeeded | Converts a reservation into a permanent deduction |
| `return` | Refund with `restock = true` | + |
| `adjustment` / `damage` / `stocktake` | Manual correction with a mandatory note | ± |

`available = stock - lockedStock`. Availability checks read that expression, not
`stock`, so two concurrent checkouts cannot both reserve the last unit.

The reservation is enforced with a conditional update inside a transaction:

```go
// Reserve atomically moves qty from available to locked, or fails.
// It returns ErrInsufficientStock when available < qty.
func (s *StockService) Reserve(ctx context.Context, variantID string, qty int64, ref Ref) error
```

## 4. Pricing pipeline

Everything that produces a total — the checkout preview, the order creation and
the order edit — runs the same pure function:

```go
// Quote computes the amount breakdown for a candidate order.
//
// It is pure: no database writes, no clock reads beyond the supplied now, no
// side effects. Given the same input it always returns the same output, which is
// what makes a stored order reproducible.
func Quote(in QuoteInput) (QuoteResult, error)

type QuoteInput struct {
    Items       []QuoteItem   // product, variant, unit price, quantity
    Tier        *MemberTier   // the buyer's tier, if any
    Benefits    []TierBenefit // the tier's typed benefits
    Grants      []CouponGrant // coupon instances the buyer intends to use
    Coupons     []Coupon      // their templates
    Settings    CommerceSettings
    Now         time.Time
}

type QuoteResult struct {
    Subtotal        int64
    MemberDiscount  int64
    CouponDiscount  int64
    DiscountTotal   int64
    ShippingFee     int64
    TaxAmount       int64
    GrandTotal      int64
    Lines           []QuoteLine // per-line explanation
    AppliedGrants   []string    // grant ids that were consumed
    RejectedGrants  []GrantRejection
}
```

Order of application, which is a business decision and therefore fixed here:

1. Line subtotals: `unitPrice × quantity`.
2. Membership discount, applied per eligible line.
3. Coupon discounts, applied to the member-discounted subtotal, in the order
   the buyer selected them, respecting `maxStacked` and each coupon's
   `stacksWithMemberDiscount`.
4. Shipping, unless a `free_shipping` benefit or coupon applies.
5. Tax, per `taxDisplayMode` (inclusive or exclusive).
6. Round once, at the end.

`QuoteResult.Lines` is mandatory and is what the UI renders ("why is this
discount 12.00?"). A discount path that cannot explain itself line by line is
not acceptable.

**Reproducibility.** The result is snapshotted onto the order: every amount
column, the applied grant ids, and the tier in force. A test replays `Quote` from
the stored snapshot and asserts it reproduces `grandTotal` exactly. This is the
mechanism behind the success criterion in `requirements.md` § 6.

## 5. Discount evaluation

The coupon evaluator is not a separate component; it is the coupon stage of
`Quote`. Its rules:

- **Eligibility**: status `active`, within its validity window at `Now`, the
  grant is `issued` and unexpired, `minSpend` satisfied, and the cart intersects
  the coupon's applicability set and avoids its exclusions.
- **Value**: `fixed_amount` deducts the value capped at the eligible subtotal;
  `percentage` deducts basis points capped at `maxDiscount`; `free_shipping`
  zeroes the shipping fee.
- **Stacking**: at most `maxStacked` coupons apply. A non-stackable coupon may
  only be applied alone. A coupon with `stacksWithMemberDiscount = false` is
  evaluated before the membership discount is considered, and the two are
  compared — the engine keeps the larger and reports the rejected one, rather
  than silently applying both.
- **Determinism**: coupon order is the buyer's selection order, then coupon id as
  a tiebreak. Never map iteration order.

Consumption happens only on successful payment: `coupon_grants.status` moves
`issued → used` with `order` and `discountApplied` recorded. A grant is never
consumed by an unpaid order, and the auto-close job returns nothing because
nothing was taken.

## 6. Order state machine

```
                    ┌──────────────────┐
     create ───────▶│ pending_payment  │
                    └───┬──────────┬───┘
              pay       │          │  timeout / cancel
                        ▼          ▼
                    ┌────────┐  ┌──────────┐
                    │  paid  │  │  closed  │
                    └───┬────┘  └──────────┘
              fulfil    │
                        ▼
                 ┌────────────┐
                 │ fulfilling │
                 └─────┬──────┘
             complete  │
                       ▼
                ┌───────────┐
                │ completed │
                └───────────┘
```

`cancelled` is reachable from `pending_payment` only. `refunded` is a terminal
state reached from `paid`, `fulfilling` or `completed` when the refunded amount
equals the amount paid.

Transitions are declared, not implied:

```go
// internal/modules/orders/statemachine.go
var transitions = map[Status][]transition{
    StatusPendingPayment: {
        {To: StatusPaid, Event: EventPaid, Permission: permission.OrderMarkPaid},
        {To: StatusClosed, Event: EventTimeout, System: true},
        {To: StatusCancelled, Event: EventCancel, Permission: permission.OrderCancel},
    },
    StatusPaid: {
        {To: StatusFulfilling, Event: EventFulfil, Permission: permission.OrderUpdate},
        {To: StatusRefunded, Event: EventRefunded, System: true},
    },
    // ...
}
```

Every transition, in order and inside one transaction:

1. Validate the transition is declared and the actor holds its permission (or the
   event is system-triggered).
2. Compare-and-set on `status` (reject if another writer moved it first).
3. Apply the side effects — stock, coupon grants, membership or subscription
   grant, payment counters.
4. Append an `order_events` row with from-state, to-state, actor, reason.
5. Write the audit entry.

Because the side effects are inside the same transaction as the state change, a
failure to reserve stock or consume a coupon rolls the transition back rather than
leaving a paid order with no stock movement.

## 7. Payments

### 7.1 Flow

```
order(pending_payment)
   │
   ├─ create payment_intent(amount = grandTotal, provider, method, expiresAt)
   │
   ├─ hand off to provider  ── hosted checkout redirect, or client SDK
   │
   ├─ provider callback ──▶ verify signature  ──▶ reject if invalid
   │                          │
   │                          ├─ look up by providerTxnId
   │                          │     ├─ not seen  → create transaction(succeeded)
   │                          │     └─ seen      → increment callbackCount, stop
   │                          │
   │                          └─ transition order pending_payment → paid
   │                                 ├─ stock: reserve → sale
   │                                 ├─ coupon grants: issued → used
   │                                 └─ if the product is a plan: grant membership
   │                                    or start the subscription
   │
   └─ no callback ever ──▶ expiry job cancels the intent and releases stock
```

### 7.2 Idempotency

Providers retry callbacks, sometimes concurrently. The design makes a double
apply impossible rather than unlikely:

- `payment_transactions.providerTxnId` is **unique**. The first callback inserts;
  any later one violates the constraint and is treated as a replay.
- The order transition is a compare-and-set on `status`, so even two different
  transactions for one order cannot both drive it to `paid`.
- Replays increment `callbackCount` and are logged, but apply nothing.
- The raw payload is stored **once**, on the transaction created by the first
  callback. Later payloads are not appended to it.

A test issues the same callback three times concurrently and asserts one
transaction, one stock movement, one coupon consumption and one `paid` event.

### 7.3 Signature verification

Each payment driver exposes `VerifyCallback(headers, body) (Event, error)`. The
handler never parses the body before verifying, and verification uses a
constant-time comparison. A driver that cannot verify signatures (for example a
manual bank transfer) is not webhook-capable and is excluded from the callback
route.

### 7.4 Offline payment

FR-payments-6 allows an administrator to mark an order paid for a bank transfer
or cash. That path creates a `payment_transactions` row with `manual = true`, a
mandatory `manualReason`, and `recordedBy`, then runs the **same** transition
handler. It is not a separate code path, so the stock, coupon and audit effects
cannot diverge.

### 7.5 Reconciliation

A daily `jobs` task fetches the provider's settlement records for the previous
day and matches them on `providerTxnId`:

| Case | Meaning | Action |
| --- | --- | --- |
| Local succeeded, settled | Normal | Mark `reconciled` |
| Local succeeded, not settled | Provider delay or lost webhook | Alert; escalate after 48 h |
| Settled, no local record | Missed callback | Create the transaction and run the transition; alert |
| Amount mismatch | Provider error or tampering | Never auto-resolve; raise a high-severity audit entry |

Reconciliation never edits a `succeeded` transaction in place; a correction is a
refund or a new transaction.

## 8. Refunds

```
                  ┌─────────┐
   request ──────▶│ pending │
                  └────┬────┘
        ┌──────────────┼──────────────┐
        │              │              │
   auto-approve    approve        reject
   (under cap)        │              │
        └──────┬───────┘              ▼
               ▼                 ┌──────────┐
          ┌──────────┐           │ rejected │
          │ approved │           └──────────┘
          └────┬─────┘
       execute │
               ▼
        ┌──────────────┐   failure   ┌────────┐
        │  processing  │────────────▶│ failed │──┐
        └──────┬───────┘             └────────┘  │ retry
               │ success                          │
               ▼                                  │
        ┌───────────┐                             │
        │ succeeded │◀────────────────────────────┘
        └───────────┘
```

Rules that matter:

- **The cap is checked at approval time, inside a transaction**, not at request
  time. Two pending refunds can each look affordable when requested; approval
  re-reads `amountPaid - amountRefunded` under the transaction and refuses the
  second if the total would exceed it (FR-refunds-6).
- **Auto-approval** applies only when the amount is under the configured
  threshold **and** the requester is not the approver. An administrator may never
  approve their own refund request.
- Approval and execution are separate steps so that a provider outage does not
  lose an approved decision. A `failed` execution is retryable without a new
  approval.
- On success, in one transaction: update the transaction's refunded amount, update
  `orders.amountRefunded` and `paymentStatus`, update the affected
  `order_items.refundedQuantity` / `refundedAmount`, restock if `restock`, append
  an `order_events` row, and audit.
- If the refunded amount reaches the amount paid, the order becomes `refunded`
  and any membership granted by it is cancelled with source `refunded`. A partial
  refund does not revoke a delivered entitlement.

## 9. Subscriptions

### 9.1 Lifecycle

```
   subscribe ──▶ trialing ──trial ends──▶ active
                    │                      │
                    └──────────────────────┤
                                           │
        ┌──────────────────────────────────┼───────────────────┐
        │                                  │                   │
   cycle renews                      payment fails        cancel
        │                                  │                   │
        ▼                                  ▼                   ▼
     active                          past_due            cancelled
                                    (dunning)          (at period end)
                                           │
                                    budget exhausted
                                           ▼
                                       cancelled
```

### 9.2 Billing cycles

A `jobs` task runs frequently (every 5 minutes) and selects subscriptions whose
`nextBillingAt <= now` and whose status is `trialing` or `active`. For each, in
one transaction:

1. Create a `subscription_cycles` row with `cycleNumber`, period and `pending`.
2. Create an order for the plan product, with `source = subscription` and
   `subscription` set.
3. Advance `currentPeriodStart` / `currentPeriodEnd` and `nextBillingAt`.
4. Hand the order to `payments`.

The order is created **before** payment is attempted, so a provider outage leaves
a visible unpaid order rather than a silent gap in the billing history.

### 9.3 Dunning

On payment failure:

1. Increment `subscriptions.retryCount` and set `past_due`.
2. The cycle row records `failed` and its `attempts`.
3. A retry is scheduled at intervals from the plan's dunning policy (default
   1 day, 3 days, 7 days).
4. On success, `retryCount` resets to 0 and the status returns to `active`.
5. When the retry budget is exhausted, the subscription is cancelled with reason
   `payment_failed` and the entitlement is revoked at the end of the current
   period, not immediately.

### 9.4 Pause, cancel and plan change

- **Pause** records `pausedAt`; resume records `resumedAt` and adds the paused
  duration to `currentPeriodEnd` via `pausedDaysAccrued`. Pausing does not refund
  and does not extend beyond the paid period on cancel.
- **Cancel at period end** sets `cancelAtPeriodEnd`; the entitlement survives
  until `currentPeriodEnd`, and a job flips it to `cancelled` and `expired`.
- **Cancel immediately** ends the entitlement now and does not refund. A refund
  is a separate, approved decision (FR-refunds-1).
- **Plan change**: an upgrade applies immediately and creates a prorated order
  for `remaining_days / period_days × new_price - unused_credit`; a downgrade
  sets `pendingPlan` and takes effect at the next cycle. Proration is computed by
  the same `Quote` function, so it is explainable line by line.

## 10. Membership lifecycle

- **Grant** happens on: a paid order for a `membership_plan`, a manual
  administrator grant, a promotion, or registration (the default tier).
- **Extend** adds the plan duration to the later of `now` and the current
  `expireAt`, so renewing early never loses paid time.
- **Upgrade** creates a new `user_memberships` row for the higher tier and marks
  the previous one `cancelled` at the change time, preserving history rather than
  mutating it.
- **Expire** is driven by a job: `active` rows past `expireAt` become `expired`
  and the account's tier falls back to the default. A reminder fires at the
  configured lead time.
- **Refund** of the originating order cancels the membership with source
  `refunded`; a partial refund leaves it in place.

Every state is a row, never an overwrite, which is what makes the history in
FR-membership-5 answerable.

## 11. Concurrency and transactions

| Hazard | Control |
| --- | --- |
| Two checkouts buy the last unit | Availability is `stock - lockedStock`, checked in a conditional update inside a transaction. |
| Two callbacks for one payment | Unique `providerTxnId` plus a compare-and-set order transition. |
| Two refunds exceed the paid amount | Cap re-checked at approval under a transaction. |
| Two schedulers bill one subscription in one period | `subscription_cycles` is unique on `(subscription, cycleNumber)`; the insert is the lock. |
| Two administrators edit one order | Optimistic concurrency on the PocketBase `updated` timestamp; a stale write is rejected with a reload prompt. |
| A coupon is used twice | Consumption is a compare-and-set from `issued` to `used` inside the payment transition. |

PocketBase runs SQLite, so the practical isolation is serialised writes. The
design nonetheless relies on conditional updates and unique constraints rather
than on that fact, so the guarantees survive a move to a client-server database.

## 12. Failure modes and their handling

| Failure | Behaviour |
| --- | --- |
| Provider callback never arrives | Intent expires; a job cancels it and releases stock. Reconciliation catches a late settlement. |
| Provider is down during checkout | The order stays `pending_payment`; stock stays reserved until the auto-close timeout. |
| Provider is down during refund execution | The refund stays `approved` with `lastError`; a retry job re-attempts with backoff. |
| Job process crashes mid-cycle | `job_runs` leaves a `running` row; the subscription cycle insert is idempotent by cycle number. |
| Partial write during order transition | All side effects share one transaction; the state change rolls back with them. |
| Currency mismatch between cart and settings | `Quote` rejects the input; order creation fails before writing. |
| Product deleted while in a cart | Order creation re-reads the product and fails with a specific message rather than snapshotting a ghost. |

## 13. Testing strategy for the business domain

| Level | What is proven |
| --- | --- |
| Unit — `Quote` | Table-driven cases per discount combination; determinism (same input twice, identical output); the rounding rule; that `Lines` sum to the total. |
| Unit — state machines | Every declared transition accepted, every undeclared one rejected, every permission guard enforced. |
| Unit — stock | Available never goes negative under a randomised concurrent reserve/release sequence. |
| Integration | Callback replay (3× concurrent) yields one transaction and one stock movement. |
| Integration | Two refund approvals racing cannot exceed the paid amount. |
| Integration | A full purchase of a membership plan grants the tier exactly once and consumes the coupon once. |
| Integration | A subscription bills three cycles, fails once, recovers through dunning, then cancels on budget exhaustion. |
| Reproducibility | Replaying `Quote` from a stored order snapshot reproduces `grandTotal`. |
| Property | For any generated order, `memberDiscount + couponDiscount + subtotal + shippingFee + taxAmount == grandTotal`. |

## 14. Package layout

```
internal/modules/
  catalog/        products, categories, attributes, variants, stock service
  membership/     tiers, benefits, plans, user memberships, expiry
  promotions/     coupons, grants, campaigns, the coupon stage of Quote
  subscriptions/  plans, subscriptions, cycles, dunning
  orders/         order aggregate, Quote, state machine, timeline
  payments/       intents, transactions, provider callbacks, reconciliation
  refunds/        requests, approval workflow, execution
internal/money/   minor-unit arithmetic, rounding, formatting
internal/business/
  quote.go        the shared pricing pipeline (owned by orders, used by all)
```

`internal/money` exists so that no business package implements its own rounding.
`Quote` lives with `orders` because the order aggregate owns the total, but it is
importable by `promotions`, `membership` and `subscriptions` for previews.

## 15. Open questions specific to this domain

| Id | Question | Default if unanswered |
| --- | --- | --- |
| OQ-9 | Physical fulfilment — carrier integration, tracking numbers, address book? | Address captured on the order; no carrier integration |
| OQ-10 | One currency per deployment, or per-currency pricing? | One currency per deployment |
| OQ-11 | Platform-computed tax, or an external tax engine? | One configurable rate in `settings-product` |
| OQ-13 | Points and loyalty: is `points_multiplier` a real ledger or just a benefit label? | Label only in this initiative; a points ledger is a separate module |
| OQ-14 | Invoicing: is a PDF invoice required, or is the order page sufficient? | Order page only; `invoicePrefix` reserves the number format |
| OQ-15 | Does a partial refund revoke a delivered membership? | No; only a full refund does |
