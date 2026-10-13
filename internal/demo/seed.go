package demo

import (
	"fmt"
	"log/slog"
	"time"

	"github.com/pocketbase/pocketbase/core"

	"pocketadmin/internal/catalog"
	"pocketadmin/internal/monitoring"
)

// Seeder writes the example data.
type Seeder struct {
	app    core.App
	result *Result

	// now is fixed at the start so every date in the dataset is relative to one instant.
	// A seed that took a minute would otherwise produce records whose relative order
	// drifts from what the narrative says.
	now time.Time

	// ids remembers what was created, so the parts that reference each other do not have
	// to look anything up.
	ids map[string]string
}

// Seed writes the example data.
//
// With reset it clears the declared collections first, which is what makes a
// demonstration repeatable: run it twice and the second run produces the same screens as
// the first rather than a duplicate of everything.
func Seed(app core.App, reset bool) (*Result, error) {
	result := NewResult()

	if reset {
		if err := Clear(app, result); err != nil {
			return result, err
		}
	} else if existing := TotalOwned(app); existing > 0 {
		// Refusing rather than trying. The dataset has unique slugs, codes and order
		// numbers, so a second seed without a reset inserts until it hits one and then
		// stops -- leaving a half-duplicated state that is worse than either the old
		// data or the new. The command says which flag to use instead.
		return result, fmt.Errorf(
			"the example data is already present (%d row(s)); "+
				"run `demo seed --reset` to clear and write it again", existing)
	}

	s := &Seeder{
		app:    app,
		result: result,
		now:    time.Now().Truncate(time.Second),
		ids:    map[string]string{},
	}

	steps := []struct {
		name string
		run  func() error
	}{
		{"the organisation", s.organisation},
		{"the accounts", s.accounts},
		{"the catalogue", s.catalogue},
		{"membership", s.membership},
		{"promotions", s.promotions},
		{"subscriptions", s.subscriptions},
		{"orders and payments", s.commerce},
		{"announcements and the workbench", s.workbench},
		{"files", s.files},
		{"monitoring and the logs", s.telemetry},
	}

	for _, step := range steps {
		if err := step.run(); err != nil {
			return result, fmt.Errorf("demo: %s: %w", step.name, err)
		}
	}

	return result, nil
}

// create writes one record and counts it.
func (s *Seeder) create(collection string, fields map[string]any) (*core.Record, error) {
	col, err := s.app.FindCollectionByNameOrId(collection)
	if err != nil || col == nil {
		return nil, fmt.Errorf("the %s collection is missing", collection)
	}

	rec := core.NewRecord(col)
	for name, value := range fields {
		rec.Set(name, value)
	}

	if err := s.app.Save(rec); err != nil {
		return nil, fmt.Errorf("saving a %s: %w", collection, err)
	}

	s.result.Created[collection]++
	return rec, nil
}

// remember records an id under a name so later steps can reference it.
func (s *Seeder) remember(name string, rec *core.Record) *core.Record {
	if rec != nil {
		s.ids[name] = rec.Id
	}
	return rec
}

// id returns a remembered id, or the empty string.
func (s *Seeder) id(name string) string { return s.ids[name] }

// daysAgo returns a time a number of days before the fixed now.
func (s *Seeder) daysAgo(days int) time.Time { return s.now.AddDate(0, 0, -days) }

// hoursAgo returns a time a number of hours before the fixed now.
func (s *Seeder) hoursAgo(hours int) time.Time {
	return s.now.Add(-time.Duration(hours) * time.Hour)
}

// address is a plausible shipping address.
func address(name, city, street, postal string) map[string]any {
	return map[string]any{
		"name": name, "city": city, "street": street, "postalCode": postal, "country": "CN",
	}
}

// log reports a step as it happens, so a long seed is not a silent one.
func log(collection string, n int) {
	if n > 0 {
		slog.Info("demo: wrote", "collection", collection, "count", n)
	}
}

// openingStock receives goods into a variant.
//
// It goes through the catalogue's ledger rather than setting the cached balance, because
// the reconciliation screen compares the two. A demonstration whose ledger and cache
// disagreed would show a discrepancy nobody caused, which teaches the wrong thing about
// the screen.
func (s *Seeder) openingStock(variant *core.Record, quantity int) error {
	if err := catalog.ApplyStock(s.app, variant, catalog.Movement{
		Delta:   quantity,
		Reason:  catalog.ReasonPurchase,
		RefType: "demo",
		Note:    "opening stock",
	}); err != nil {
		return err
	}

	s.result.Created["stock_ledger"]++
	return nil
}

// ensureLocalNode returns this machine's monitoring row, creating it when it is missing.
//
// It goes through the monitoring package rather than writing the row, so the machine the
// demonstration attaches its readings to is the same one the sampler writes to.
func ensureLocalNode(app core.App) (*core.Record, error) {
	return monitoring.EnsureLocalNode(app)
}

// recordNodeSample updates a machine's liveness counters.
func recordNodeSample(app core.App, node *core.Record, at time.Time) error {
	return monitoring.RecordSample(app, node, at)
}
