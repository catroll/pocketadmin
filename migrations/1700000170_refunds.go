package migrations

import (
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/migrations"
)

// refundsMigration creates the refund requests and the dictionary their reasons come
// from.
//
// A refund is a record with an approver rather than a field on an order, because the
// question an auditor asks is not "was this refunded" but "who decided, when, and on
// what grounds". A field cannot answer that, and a soft-deleted one cannot be trusted
// to.
func init() {
	migrations.Register(func(app core.App) error {
		if err := seedRefundReasons(app); err != nil {
			return err
		}
		if err := createRefunds(app); err != nil {
			return err
		}
		if err := seedPermissionPoints(app); err != nil {
			return err
		}
		return seedMenus(app)
	}, func(app core.App) error {
		for _, name := range []string{"refunds", "refund_reason"} {
			if col := findCollection(app, name); col != nil {
				if err := app.Delete(col); err != nil {
					return err
				}
			}
		}
		return nil
	}, "1700000170_refunds.go")
}

// seedRefundReasons creates the reason dictionary the refund's reason field mirrors.
func seedRefundReasons(app core.App) error {
	col := findCollection(app, "refund_reason")
	if col == nil {
		col = newBase("refund_reason")
	}
	ensureTimestamps(col)

	addField(col, text("code", true))
	addField(col, text("label", true))
	addField(col, &core.NumberField{Name: "sortOrder"})
	addField(col, selectOf("status", true, "active", "disabled"))

	col.AddIndex("idx_refund_reason_code", true, "code", "")
	disableCascade(col)
	lockDown(col)

	if err := app.Save(col); err != nil {
		return err
	}

	reasons := []struct {
		code, label string
		order       int
	}{
		{"customer_request", "The customer changed their mind", 10},
		{"not_as_described", "Not as described", 20},
		{"damaged", "Arrived damaged", 30},
		{"not_received", "Never arrived", 40},
		{"duplicate", "Charged twice", 50},
		{"fraud", "Fraudulent", 60},
		{"goodwill", "Goodwill", 70},
		{"other", "Other", 999},
	}

	for _, reason := range reasons {
		existing, _ := app.FindFirstRecordByFilter(col.Id,
			"code = {:c}", map[string]any{"c": reason.code})
		if existing != nil {
			continue
		}

		rec := core.NewRecord(col)
		rec.Set("code", reason.code)
		rec.Set("label", reason.label)
		rec.Set("sortOrder", reason.order)
		rec.Set("status", "active")
		if err := app.Save(rec); err != nil {
			return err
		}
	}
	return nil
}

// createRefunds creates the refund requests.
func createRefunds(app core.App) error {
	col := findCollection(app, "refunds")
	if col == nil {
		col = newBase("refunds")
	}
	ensureTimestamps(col)

	addField(col, text("refundNo", true))
	addField(col, rel(app, "order", "orders", true))
	// The payment being refunded. Optional, because an offline payment may have no
	// transaction to point at.
	addField(col, rel(app, "transaction", "payment_transactions", false))

	addField(col, selectOf("mode", true, "full", "partial_amount", "partial_items"))
	// For a partial refund by item: [{ orderItemId, quantity }]. Stored as json
	// because it is a list whose length is the customer's, not the schema's.
	addField(col, jsonField("items", 8000))
	addField(col, &core.NumberField{Name: "amount"})
	addField(col, text("currency", true))

	addField(col, selectOf("status", true,
		"pending", "approved", "rejected", "processing", "succeeded", "failed", "cancelled"))

	// The reason mirrors the dictionary, and the detail is free text. Both exist
	// because a code alone never explains an unusual case and free text alone cannot
	// be counted.
	addField(col, text("reason", true))
	addField(col, text("reasonDetail", false))
	addField(col, multiRel(app, "attachments", "files", 20))

	addField(col, rel(app, "requestedBy", "users", true))
	// True when the amount was under the configured threshold. It is recorded rather
	// than inferred, because the threshold can be changed afterwards and a decision
	// has to be explainable against the policy in force when it was taken.
	addField(col, &core.BoolField{Name: "autoApproved"})

	addField(col, rel(app, "reviewedBy", "users", false))
	addField(col, &core.DateField{Name: "reviewedAt"})
	addField(col, text("reviewComment", false))

	addField(col, text("providerRefundId", false))
	addField(col, jsonField("providerResponse", 8000))
	addField(col, &core.NumberField{Name: "attempts"})
	addField(col, text("lastError", false))
	addField(col, &core.DateField{Name: "completedAt"})
	// Whether approving restores stock. Recorded per refund rather than read from
	// settings at execution time, so a policy change cannot alter what an approved
	// refund does.
	addField(col, &core.BoolField{Name: "restock"})

	col.AddIndex("idx_refunds_no", true, "refundNo", "")
	col.AddIndex("idx_refunds_order", false, "order", "")
	col.AddIndex("idx_refunds_status", false, "status", "")
	col.AddIndex("idx_refunds_requested", false, "requestedBy, created", "")
	col.AddIndex("idx_refunds_reviewed", false, "reviewedBy", "")
	col.AddIndex("idx_refunds_created", false, "created", "")

	disableCascade(col, "order", "transaction", "attachments", "requestedBy", "reviewedBy")
	lockDown(col)

	return app.Save(col)
}
