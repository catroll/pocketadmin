package demo

import (
	"fmt"
	"log/slog"
	"strings"

	"github.com/pocketbase/pocketbase/core"
)

// Result reports what a reset or a seed did.
type Result struct {
	// Cleared is the number of rows removed, per collection.
	Cleared map[string]int

	// Created is the number of rows written.
	Created map[string]int

	// Skipped names collections that were asked for and do not exist, which is normal
	// for a database that predates a module.
	Skipped []string
}

// NewResult returns an empty result.
func NewResult() *Result {
	return &Result{Cleared: map[string]int{}, Created: map[string]int{}}
}

// TotalCleared sums what was removed.
func (r *Result) TotalCleared() int {
	total := 0
	for _, n := range r.Cleared {
		total += n
	}
	return total
}

// TotalCreated sums what was written.
func (r *Result) TotalCreated() int {
	total := 0
	for _, n := range r.Created {
		total += n
	}
	return total
}

// Clear removes the example data.
//
// The order is the reverse of the declaration, so a collection is emptied before anything
// it points at. The relations are not cascading -- that was a deliberate choice elsewhere
// -- so the reverse order is what stops a delete failing on a record that still has
// children.
func Clear(app core.App, result *Result) error {
	if result == nil {
		result = NewResult()
	}

	// Two phases, and the first is what makes the second order-independent.
	//
	// The declared order was originally relied on, with the list walked backwards so a
	// child went before its parent. That cannot work: `departments.leader` points at
	// `employees` and `employees.department` points back, so those two have no correct
	// order at all -- whichever is deleted first is blocked by the other. Five more
	// single-direction mistakes were found by deriving the requirement from the schemas
	// rather than from a second hand-written list.
	//
	// Detaching first removes the problem instead of ordering around it: every relation
	// that points inside the set being cleared is emptied, and then nothing refers to
	// anything.
	if err := detachReferences(app); err != nil {
		return err
	}

	// Swept repeatedly rather than once per collection.
	//
	// A required relation still has to be deleted child-first, and the correct order
	// depends on the schema rather than on a list somebody maintains. Deleting whatever
	// can be deleted and going round again derives that order from the data: after the
	// detach phase nothing outside the set refers in, so the only thing blocking a row
	// is a row in the set that is itself about to go.
	//
	// The bound is a safety net. A cycle of required relations would make no progress,
	// and the loop says so rather than spinning.
	const maxSweeps = 20
	for sweep := 0; sweep < maxSweeps; sweep++ {
		progress := 0

		for _, name := range ownedCollections {
			col, err := app.FindCollectionByNameOrId(name)
			if err != nil || col == nil {
				if sweep == 0 {
					// A database that predates a module does not have its tables. That is
					// not a failure: the point is to reach the declared state, and a table
					// that does not exist is already in it.
					result.Skipped = append(result.Skipped, name)
				}
				continue
			}

			deleted, err := emptyCollection(app, name)
			if err != nil {
				// A row that cannot be deleted yet is not an error in a sweep: something
				// else still refers to it. It is only an error if a whole sweep makes no
				// progress at all.
				continue
			}

			result.Cleared[name] += deleted
			progress += deleted
		}

		if progress == 0 {
			break
		}
	}

	// Anything still present after a sweep that removed nothing is stuck on a relation
	// the reset cannot break, and saying so is better than reporting success.
	if err := checkEmptied(app); err != nil {
		return err
	}

	// Deleted in the order written so a page that lists accounts does not show one being
	// removed and readded.
	for _, user := range demoUsers(app) {
		if err := app.Delete(user); err != nil {
			return fmt.Errorf("demo: removing the example account %s: %w", user.Email(), err)
		}
		result.Cleared["users"]++
	}

	return nil
}

// detachReferences empties every relation that points at a collection being cleared.
//
// Only relations with both ends inside the set are touched. A record pointing at
// something the reset preserves -- a user, a role, a settings row -- keeps its reference,
// because nothing is deleting the other end.
//
// It runs over the whole set before anything is deleted, so no collection's removal can
// be blocked by a record in another. The work is bounded by the number of relation fields
// in the set, not by the number of rows, because the update is one statement per field.
func detachReferences(app core.App) error {
	owned := map[string]bool{}
	for _, name := range ownedCollections {
		owned[name] = true
	}

	// Which collections exist, and their ids, so a relation's target can be resolved
	// without a lookup per row.
	ids := map[string]string{}
	for _, name := range ownedCollections {
		col, err := app.FindCollectionByNameOrId(name)
		if err != nil || col == nil {
			continue
		}
		ids[col.Id] = name
	}

	for _, name := range ownedCollections {
		col, err := app.FindCollectionByNameOrId(name)
		if err != nil || col == nil {
			continue
		}

		for _, field := range col.Fields {
			rel, ok := field.(*core.RelationField)
			if !ok {
				continue
			}
			if _, inside := ids[rel.CollectionId]; !inside {
				continue
			}

			// A required relation cannot be emptied -- the schema refuses it, and
			// rightly: a variant with no product is not a state the panel can produce.
			// Those are resolved by the deletion sweep below instead, which removes a
			// child before its parent. Detaching is for the optional ones, and it is
			// what breaks a cycle: `departments.leader` and `employees.department` are
			// both optional, and emptying one of them is enough to make an order exist.
			if rel.Required {
				continue
			}

			// A record carries the saved state it was read with, so the field is emptied
			// on each and saved. PocketBase has no bulk update that respects the model
			// hooks, and the counts here are small.
			records, err := app.FindAllRecords(name)
			if err != nil {
				return fmt.Errorf("demo: reading %s to detach %s: %w", name, field.GetName(), err)
			}
			for _, rec := range records {
				if rec.GetString(field.GetName()) == "" {
					continue
				}
				rec.Set(field.GetName(), nil)
				if err := app.Save(rec); err != nil {
					return fmt.Errorf("demo: detaching %s.%s: %w", name, field.GetName(), err)
				}
			}
		}
	}

	return nil
}

// emptyCollection deletes every row in a collection, in batches.
//
// In batches so a collection with a hundred thousand rows does not hold a write lock for
// the whole of it, and so progress is visible as it goes rather than after a long silence.
func emptyCollection(app core.App, name string) (int, error) {
	total := 0

	for batch := 0; batch < 1000; batch++ {
		records, err := app.FindRecordsByFilter(name, "", "id", 500, 0)
		if err != nil {
			return total, err
		}
		if len(records) == 0 {
			return total, nil
		}

		for _, rec := range records {
			if err := app.Delete(rec); err != nil {
				return total, err
			}
			total++
		}

		if len(records) < 500 {
			return total, nil
		}
	}

	return total, nil
}

// demoUsers returns the accounts this package created.
//
// Only the marked ones. An operator's own accounts are on the untouchable list and this
// is what honours that: a reset removes the example people and nobody else.
func demoUsers(app core.App) []*core.Record {
	records, err := app.FindAllRecords("users")
	if err != nil {
		return nil
	}

	out := make([]*core.Record, 0, len(records))
	for _, rec := range records {
		if IsDemoUser(rec.Email()) {
			out = append(out, rec)
		}
	}
	return out
}

// CountOwned reports how many rows a reset would remove, without removing them.
//
// It is what the command prints before it does anything, so an operator sees the size of
// what they are about to lose.
func CountOwned(app core.App) map[string]int {
	counts := map[string]int{}

	for _, name := range ownedCollections {
		col, err := app.FindCollectionByNameOrId(name)
		if err != nil || col == nil {
			continue
		}

		n, err := app.CountRecords(name)
		if err != nil {
			slog.Debug("could not count a collection", "collection", name, "error", err)
			continue
		}
		if n > 0 {
			counts[name] = int(n)
		}
	}

	// Only non-empty collections are listed. Reporting a zero would make the map
	// non-empty for an empty database, and a caller asking "is there anything here"
	// by its length would always be told yes -- which is exactly what happened: the
	// guard on a second seed refused on a fresh install.
	if users := len(demoUsers(app)); users > 0 {
		counts["users"] = users
	}
	return counts
}

// TotalOwned is how many example rows a reset would remove.
func TotalOwned(app core.App) int {
	total := 0
	for _, n := range CountOwned(app) {
		total += n
	}
	return total
}

// checkEmptied reports any owned collection that still holds rows.
//
// It is what turns a partial clear into a failure rather than a quiet success. The first
// version of the reset emptied fifteen of thirty-eight collections, hit a delete that a
// child record blocked, and returned no error at all -- so the command printed a list of
// what it had cleared and the operator had no reason to look further.
func checkEmptied(app core.App) error {
	var stuck []string

	for _, name := range ownedCollections {
		if _, err := app.FindCollectionByNameOrId(name); err != nil {
			continue
		}

		count, err := app.CountRecords(name)
		if err != nil || count == 0 {
			continue
		}
		stuck = append(stuck, fmt.Sprintf("%s (%d)", name, count))
	}

	if len(stuck) > 0 {
		return fmt.Errorf("demo: %d collection(s) could not be emptied: %s",
			len(stuck), strings.Join(stuck, ", "))
	}
	return nil
}
