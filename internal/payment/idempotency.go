package payment

import (
	"fmt"
	"time"

	"github.com/pocketbase/pocketbase/core"
)

// Collection names.
const (
	CollectionIntents      = "payment_intents"
	CollectionTransactions = "payment_transactions"
)

// Intent statuses.
const (
	IntentPending    = "pending"
	IntentProcessing = "processing"
	IntentSucceeded  = "succeeded"
	IntentFailed     = "failed"
	IntentCancelled  = "cancelled"
	IntentExpired    = "expired"
)

// Transaction statuses, the same vocabulary.
const (
	TxnPending    = "pending"
	TxnProcessing = "processing"
	TxnSucceeded  = "succeeded"
	TxnFailed     = "failed"
	TxnCancelled  = "cancelled"
	TxnExpired    = "expired"
)

// Methods.
const (
	MethodCard         = "card"
	MethodBankTransfer = "bank_transfer"
	MethodWallet       = "wallet"
	MethodCash         = "cash"
	MethodOther        = "other"
)

// Statuses returns the vocabulary, matched by the migration's stored select.
func Statuses() []string {
	return []string{IntentPending, IntentProcessing, IntentSucceeded, IntentFailed, IntentCancelled, IntentExpired}
}

// Methods returns the vocabulary.
func Methods() []string {
	return []string{MethodCard, MethodBankTransfer, MethodWallet, MethodCash, MethodOther}
}

// Outcome is what a provider says happened.
type Outcome struct {
	// ProviderTxnID is the provider's own identifier for the attempt. It is the
	// idempotency key: two callbacks naming the same transaction are one event.
	ProviderTxnID string

	Status string

	// Amount is what the provider says it took. It is compared against the
	// transaction rather than trusted, because a provider that takes a different
	// amount from the one requested is a discrepancy somebody has to see.
	Amount int64

	FeeAmount int64

	FailureCode    string
	FailureMessage string

	// Payload is the raw callback, kept redacted for the record.
	Payload any
}

// ApplyResult says what applying a callback did.
type ApplyResult struct {
	// Applied is false when the callback was a replay and nothing changed.
	Applied bool

	// Reason explains a replay, in words an operator can read.
	Reason string

	// AmountMismatch is set when the provider's amount differs from the request.
	AmountMismatch bool
}

// ApplyCallback applies a provider callback to a transaction exactly once.
//
// This is the whole point of the module. Providers retry: a callback that timed out
// on the network arrives again, sometimes several times, sometimes hours later. The
// provider's transaction id is the idempotency key, and a callback that names a
// transaction already recorded as succeeded changes nothing -- no second payment
// against the order, no second timeline entry, no second credit.
//
// A replay is not an error. It is the expected behaviour of a retrying provider, and
// answering it with a failure would make the provider retry again.
func ApplyCallback(app core.App, intent *core.Record, outcome Outcome, now time.Time) (ApplyResult, error) {
	if intent == nil {
		return ApplyResult{}, fmt.Errorf("payment: no payment intent")
	}
	if outcome.ProviderTxnID == "" {
		return ApplyResult{}, fmt.Errorf("payment: the callback named no transaction")
	}

	// Find the transaction this callback is about, by the provider's identifier.
	existing, err := FindByProviderTxn(app, outcome.ProviderTxnID)
	if err != nil {
		return ApplyResult{}, err
	}

	if existing != nil {
		// Count the replay before deciding anything. The count is how a duplicate
		// callback storm becomes visible rather than looking like one event.
		existing.Set("callbackCount", existing.GetInt("callbackCount")+1)
		if err := app.Save(existing); err != nil {
			return ApplyResult{}, err
		}

		// A succeeded transaction is never altered. Corrections go through a refund,
		// which is a record of its own.
		if existing.GetString("status") == TxnSucceeded {
			return ApplyResult{Applied: false,
				Reason: "that transaction has already been applied"}, nil
		}

		// A transaction that failed and is now reported succeeded is applied, because
		// that is a genuine change of state rather than a replay.
		if outcome.Status == TxnSucceeded {
			return applySuccess(app, intent, existing, outcome, now)
		}

		return ApplyResult{Applied: false,
			Reason: "that transaction was already recorded as " + existing.GetString("status")}, nil
	}

	// A new transaction.
	txn, err := NewTransaction(app, intent, outcome, now)
	if err != nil {
		return ApplyResult{}, err
	}

	if outcome.Status != TxnSucceeded {
		return ApplyResult{Applied: false,
			Reason: "the transaction was recorded as " + txn.GetString("status")}, nil
	}

	return applySuccess(app, intent, txn, outcome, now)
}

// applySuccess moves a transaction and its intent to succeeded and credits the order.
func applySuccess(app core.App, intent, txn *core.Record, outcome Outcome, now time.Time) (ApplyResult, error) {
	result := ApplyResult{Applied: true}

	// The amount is compared, not trusted. A provider that took a different amount
	// from the one requested is a discrepancy somebody has to look at, and quietly
	// accepting it would make the order's own figures wrong.
	expected := int64(txn.GetFloat("amount"))
	if outcome.Amount > 0 && outcome.Amount != expected {
		result.AmountMismatch = true
	}

	txn.Set("status", TxnSucceeded)
	if outcome.FeeAmount > 0 {
		txn.Set("feeAmount", outcome.FeeAmount)
	}
	if outcome.Payload != nil {
		txn.Set("callbackPayload", outcome.Payload)
	}
	if err := app.Save(txn); err != nil {
		return result, err
	}

	intent.Set("status", IntentSucceeded)
	intent.Set("clientSecret", "")
	if err := app.Save(intent); err != nil {
		return result, err
	}

	// The order is credited with what the transaction says, not with what the
	// callback says, so a mismatched callback cannot inflate the amount paid.
	if err := creditOrder(app, txn, now); err != nil {
		return result, err
	}

	return result, nil
}

// creditOrder adds a succeeded transaction to its order's paid amount.
func creditOrder(app core.App, txn *core.Record, now time.Time) error {
	order, err := app.FindRecordById("orders", txn.GetString("order"))
	if err != nil || order == nil {
		return fmt.Errorf("payment: the transaction names an order that does not exist")
	}

	paid := int64(order.GetFloat("amountPaid")) + int64(txn.GetFloat("amount"))
	grand := int64(order.GetFloat("grandTotal"))

	order.Set("amountPaid", paid)
	order.Set("paymentStatus", paymentStatusFor(grand, paid, int64(order.GetFloat("amountRefunded"))))
	if paid >= grand && order.GetString("status") == "pending_payment" {
		order.Set("status", "paid")
		order.Set("paidAt", now.UTC())
	}

	return app.Save(order)
}

// paymentStatusFor mirrors the order package's derivation.
//
// It is written out rather than imported because the payment package must not depend
// on the order package: the dependency runs the other way, and a cycle here would be
// resolved by one of them owning a rule that belongs to the other.
func paymentStatusFor(grandTotal, amountPaid, amountRefunded int64) string {
	if amountRefunded > 0 && amountRefunded >= amountPaid {
		return "refunded"
	}
	if amountPaid <= 0 {
		return "unpaid"
	}
	if amountPaid >= grandTotal {
		return "paid"
	}
	return "partial"
}

// NewTransaction records a fresh attempt.
func NewTransaction(app core.App, intent *core.Record, outcome Outcome, now time.Time) (*core.Record, error) {
	col, err := app.FindCollectionByNameOrId(CollectionTransactions)
	if err != nil || col == nil {
		return nil, fmt.Errorf("payment: the transactions collection is missing")
	}

	txn := core.NewRecord(col)
	txn.Set("intent", intent.Id)
	txn.Set("order", intent.GetString("order"))
	if provider := intent.GetString("provider"); provider != "" {
		txn.Set("provider", provider)
	}
	if outcome.ProviderTxnID != "" {
		txn.Set("providerTxnId", outcome.ProviderTxnID)
	}

	// The amount is the intent's, not the callback's. A callback claiming a larger
	// amount than was asked for does not get to charge it.
	txn.Set("amount", intent.GetFloat("amount"))
	txn.Set("currency", intent.GetString("currency"))
	txn.Set("status", statusOrPending(outcome.Status))
	txn.Set("callbackCount", 1)

	if outcome.FailureCode != "" {
		txn.Set("failureCode", outcome.FailureCode)
	}
	if outcome.FailureMessage != "" {
		txn.Set("failureMessage", outcome.FailureMessage)
	}
	if outcome.Payload != nil {
		txn.Set("callbackPayload", outcome.Payload)
	}

	if err := app.Save(txn); err != nil {
		return nil, err
	}

	intent.Set("attemptCount", intent.GetInt("attemptCount")+1)
	if err := app.Save(intent); err != nil {
		return nil, err
	}

	return txn, nil
}

// statusOrPending falls back to pending for an unknown status rather than storing
// something the collection would refuse.
func statusOrPending(status string) string {
	switch status {
	case TxnPending, TxnProcessing, TxnSucceeded, TxnFailed, TxnCancelled, TxnExpired:
		return status
	default:
		return TxnPending
	}
}

// FindByProviderTxn looks a transaction up by the provider's identifier.
func FindByProviderTxn(app core.App, providerTxnID string) (*core.Record, error) {
	if providerTxnID == "" {
		return nil, nil
	}

	rec, err := app.FindFirstRecordByFilter(CollectionTransactions,
		"providerTxnId = {:t}", map[string]any{"t": providerTxnID})
	if err != nil {
		// FindFirstRecordByFilter reports "no rows" as an error, which is not one
		// here: a transaction that has not been seen before is the ordinary case.
		return nil, nil
	}
	return rec, nil
}
