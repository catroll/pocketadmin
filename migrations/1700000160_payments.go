package migrations

import (
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/migrations"
)

// paymentsMigration creates the payment intents and the transactions attempted
// against them.
//
// An intent is what the shop wants to collect; a transaction is one attempt at
// collecting it. They are separate because a card is declined and retried, and a
// record that says "this order was charged" has to be distinguishable from "this
// order was charged four times before it worked".
func init() {
	migrations.Register(func(app core.App) error {
		if err := createPaymentIntents(app); err != nil {
			return err
		}
		if err := createPaymentTransactions(app); err != nil {
			return err
		}
		if err := seedPermissionPoints(app); err != nil {
			return err
		}
		return seedMenus(app)
	}, func(app core.App) error {
		for _, name := range []string{"payment_transactions", "payment_intents"} {
			if col := findCollection(app, name); col != nil {
				if err := app.Delete(col); err != nil {
					return err
				}
			}
		}
		return nil
	}, "1700000160_payments.go")
}

// createPaymentIntents creates the intents.
func createPaymentIntents(app core.App) error {
	col := findCollection(app, "payment_intents")
	if col == nil {
		col = newBase("payment_intents")
	}
	ensureTimestamps(col)

	addField(col, rel(app, "order", "orders", true))
	addField(col, rel(app, "provider", "integration_providers", false))
	addField(col, &core.NumberField{Name: "amount"})
	addField(col, text("currency", true))
	addField(col, selectOf("method", true, "card", "bank_transfer", "wallet", "cash", "other"))
	addField(col, selectOf("status", true,
		"pending", "processing", "succeeded", "failed", "cancelled", "expired"))

	addField(col, text("providerIntentId", false))
	// Never logged, never rendered, and cleared once the intent is settled.
	addField(col, text("clientSecret", false))
	addField(col, &core.DateField{Name: "expiresAt"})
	// Empty for a customer-initiated payment.
	addField(col, rel(app, "createdBy", "users", false))
	addField(col, &core.NumberField{Name: "attemptCount"})

	col.AddIndex("idx_intents_order", false, "order", "")
	col.AddIndex("idx_intents_status", false, "status", "")
	col.AddIndex("idx_intents_provider_intent", false, "providerIntentId", "")
	col.AddIndex("idx_intents_expires", false, "expiresAt", "")

	disableCascade(col, "order", "provider", "createdBy")
	lockDown(col)

	return app.Save(col)
}

// createPaymentTransactions creates the attempts.
func createPaymentTransactions(app core.App) error {
	col := findCollection(app, "payment_transactions")
	if col == nil {
		col = newBase("payment_transactions")
	}
	ensureTimestamps(col)

	addField(col, rel(app, "intent", "payment_intents", true))
	// Denormalised for reporting, so a transaction list does not have to walk back
	// through the intent to say which order it belongs to.
	addField(col, rel(app, "order", "orders", true))
	addField(col, rel(app, "provider", "integration_providers", false))

	// The idempotency key. It is unique when set, and a replay of the same provider
	// transaction finds the existing row rather than writing a second one. It is
	// optional because a transaction exists before a provider has named it.
	addField(col, text("providerTxnId", false))

	addField(col, &core.NumberField{Name: "amount"})
	addField(col, text("currency", true))
	addField(col, &core.NumberField{Name: "feeAmount"})
	addField(col, selectOf("status", true,
		"pending", "processing", "succeeded", "failed", "cancelled", "expired"))
	addField(col, text("failureCode", false))
	addField(col, text("failureMessage", false))

	// The raw callback, redacted before it is stored, written once and counted.
	addField(col, jsonField("callbackPayload", 32000))
	addField(col, &core.NumberField{Name: "callbackCount"})

	addField(col, &core.DateField{Name: "settledAt"})
	addField(col, &core.BoolField{Name: "reconciled"})
	addField(col, &core.DateField{Name: "reconciledAt"})

	addField(col, &core.BoolField{Name: "manual"})
	addField(col, text("manualReason", false))
	addField(col, rel(app, "recordedBy", "users", false))

	// The unique index is partial: several transactions have no provider id yet, and
	// a plain unique index would allow exactly one such row in the whole table.
	col.AddIndex("idx_txns_provider_txn", true, "providerTxnId", "providerTxnId != ''")
	col.AddIndex("idx_txns_intent", false, "intent", "")
	col.AddIndex("idx_txns_order", false, "order", "")
	col.AddIndex("idx_txns_status", false, "status", "")
	col.AddIndex("idx_txns_reconcile", false, "settledAt, reconciled", "")
	col.AddIndex("idx_txns_created", false, "created", "")

	disableCascade(col, "intent", "order", "provider", "recordedBy")
	lockDown(col)

	return app.Save(col)
}
