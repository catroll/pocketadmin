package audit

import (
	"fmt"
	"log/slog"

	"github.com/pocketbase/pocketbase/core"
)

// EnsureEventEnum widens the stored event select to cover the catalogue.
//
// The migration that creates the collection builds the enum from EventStrings, so
// a fresh database is always correct. An existing one is not: migrations do not
// run again, and a new event added by a later release would be rejected at write
// time. The rejection is silent from the caller's point of view -- it is logged,
// because a failed audit write is logged loudly, but the action has already
// happened and the record of it does not exist.
//
// It runs at startup rather than in a migration because that is the only point
// that is reached by both a fresh install and an upgrade. The same problem was
// solved the same way for the permission action enum.
func EnsureEventEnum(app core.App) error {
	col, err := app.FindCollectionByNameOrId("security_audit_logs")
	if err != nil || col == nil {
		// Nothing to heal before the collection exists.
		return nil
	}

	field, ok := col.Fields.GetByName("event").(*core.SelectField)
	if !ok {
		return fmt.Errorf("audit: the event field is missing or is not a select")
	}

	missing := make([]string, 0, 4)
	have := make(map[string]bool, len(field.Values))
	for _, v := range field.Values {
		have[v] = true
	}
	for _, event := range EventStrings() {
		if !have[event] {
			missing = append(missing, event)
		}
	}

	if len(missing) == 0 {
		return nil
	}

	// Only ever add. Removing a value would invalidate rows already stored under
	// it, and an audit log that cannot represent its own history is not one.
	field.Values = append(field.Values, missing...)

	if err := app.Save(col); err != nil {
		return fmt.Errorf("audit: widen the event enum: %w", err)
	}

	slog.Info("widened the audit event catalogue",
		"added", missing, "total", len(field.Values))
	return nil
}
