# The example dataset

PocketAdmin ships a command that loads a coherent set of example records so the panel can
be demonstrated or exercised without anybody typing in a catalogue by hand.

**It is never run by installation.** A fresh install is empty and stays empty until
somebody asks for the examples. A panel that quietly filled itself with example customers
would be worse than one that showed nothing, because nobody would know which records were
theirs.

## Using it

```sh
# Load the examples into a fresh installation.
make demo

# Return to a known state: clear the examples and write them again.
make demo -- ARGS="--reset --yes"
# or, without make:
./bin/pocketadmin demo seed --reset --yes

# See what is there.
make demo-status

# Remove the examples.
make demo-clear
```

The command bootstraps the database and runs the migrations itself, so it works in a data
directory that has never been served.

### The subcommands

| Command | What it does |
| --- | --- |
| `demo seed` | Writes the examples. Refuses if they are already present. |
| `demo seed --reset` | Clears the examples first, then writes them. Repeatable. |
| `demo seed --reset --dry-run` | Prints what would be cleared and stops. |
| `demo clear` | Removes the examples. |
| `demo status` | Lists what the examples currently hold. |

`--yes` skips the confirmation. Without it the command prints exactly what it is about to
remove and asks; it also refuses outright when standard input is not a terminal, so a
script cannot accidentally destroy a demonstration.

### Why `seed` refuses a second time

The dataset has unique slugs, SKUs, coupon codes and order numbers. A second seed without
a reset inserts rows until it hits one of them and then stops, leaving a half-duplicated
state that is worse than either the old data or the new. The command says which flag to
use instead of trying.

## What it touches, and what it never touches

Every collection the tool touches is declared in `internal/demo/scope.go`, in two lists,
and the command prints both before it does anything.

**Cleared and rewritten** — the example data:

- the catalogue: categories, products, variants, attributes, stock ledger
- membership: tiers, benefits, plans, memberships
- promotions: campaigns, coupons, issued coupons
- subscriptions: plans, subscriptions, billing cycles
- orders and money: orders, items, events, payment intents, transactions, refunds
- the organisation: departments, positions, staff records
- content and the workbench: announcements, messages, todos, quick links, recent visits
- files: folders, file records, attachments
- monitoring readings and metric snapshots
- the logs: security, operation, sign-in, exception, integration, job runs, system updates

**Never cleared** — configuration and identity:

`users`, `admins`, `roles`, `permissions`, `menus`, `dictionaries`, `dictionary_items`,
`settings`, `scheduled_jobs`, `integration_providers`, `refund_reason`, `monitor_nodes`,
`api_keys`, `sessions`, `user_preferences`.

An operator who has configured their mail provider, their roles and their menu should be
able to reload the examples without doing any of it again. Nothing on the second list can
be removed by any sequence of these commands, and a test asserts that the two lists do not
overlap.

The one exception is the example accounts themselves. They are created with addresses
beginning `demo.` — `demo.alice@example.com` and so on — and a reset removes exactly
those. An account you made is untouched, because `users` is on the untouchable list and
only rows carrying the marker are ever deleted.

## What the examples contain

About 250 rows telling one small story, so the screens are not a set of unrelated sample
records:

- nine accounts: four staff and five customers
- four departments and three positions, with the staff assigned
- three product categories and six products with nine variants, with opening stock
- three membership tiers with benefits, two purchasable plans, four memberships granted
- a campaign with four coupons, one of them relative to its issue date, one issued and
  unspent
- a subscription plan with two subscribers, one in its trial and one three cycles in
- five orders in five different states, four paid and one refunded in full
- a day of host readings, at fifteen-minute intervals, with a daily curve

### It is written through the modules, not around them

The orders are created by `order.Create`, priced by the same membership and coupon
arithmetic the panel uses. Stock moves through the catalogue's ledger rather than by
setting a cached balance, so the reconciliation screen does not show a discrepancy nobody
caused. Refunds go through request, approval and execution, with the approval by a
different person than the requester — the rule the screen enforces, and therefore the only
state a demonstration should show.

The consequence is that the examples exercise the real code. A defect in the pricing or
the ledger shows up as a broken demonstration rather than as a demonstration that is
quietly wrong.

## Things worth knowing

**A reset detaches relations before it deletes anything.** The obvious implementation
walks a hand-written list backwards, so a child goes before its parent. That cannot work
here: `departments.leader` points at `employees` and `employees.department` points back,
so those two have no correct order at all. Emptying the optional relations inside the set
first removes the problem instead of ordering around it, and the remaining required
relations are resolved by deleting whatever can be deleted and going round again.

**A partial clear is a failure, not a success.** After the sweep, every owned collection
is counted and the command reports an error naming anything that survived. The first
version emptied fifteen of thirty-eight collections, hit a delete that a child blocked,
and returned no error — so the command printed a list of what it had cleared and the
operator had no reason to look further.

**The example accounts have a known password**, `demo-password-please-change`. They are
for a demonstration environment. Do not load this dataset into anything reachable from a
network you do not control.
