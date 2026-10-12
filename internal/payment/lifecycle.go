package payment

import (
	"fmt"
	"strings"
	"time"

	"github.com/pocketbase/pocketbase/core"
)

// DefaultIntentWindow is how long a payment intent stays usable.
//
// It matches the order's own payment window, because an intent that outlived the
// order holding its stock would let somebody pay for goods that had already gone back
// on the shelf.
const DefaultIntentWindow = 2 * time.Hour

// CreateIntent opens a payment attempt against an order.
func CreateIntent(app core.App, req IntentRequest) (*core.Record, error) {
	if req.OrderID == "" {
		return nil, fmt.Errorf("payment: a payment needs an order")
	}
	if req.Amount <= 0 {
		return nil, fmt.Errorf("payment: a payment must be for a positive amount")
	}

	col, err := app.FindCollectionByNameOrId(CollectionIntents)
	if err != nil || col == nil {
		return nil, fmt.Errorf("payment: the intents collection is missing")
	}

	rec := core.NewRecord(col)
	rec.Set("order", req.OrderID)
	if req.ProviderID != "" {
		rec.Set("provider", req.ProviderID)
	}
	rec.Set("amount", req.Amount)
	rec.Set("currency", req.Currency)
	rec.Set("method", methodOrOther(req.Method))
	rec.Set("status", IntentPending)
	rec.Set("attemptCount", 0)

	if req.ProviderIntentID != "" {
		rec.Set("providerIntentId", req.ProviderIntentID)
	}
	if req.ClientSecret != "" {
		// Stored because the customer's browser needs it, and cleared the moment the
		// intent settles. A secret that outlives its intent is a credential with
		// nothing left to authorise.
		rec.Set("clientSecret", req.ClientSecret)
	}
	if req.CreatedBy != "" {
		rec.Set("createdBy", req.CreatedBy)
	}

	expires := req.ExpiresAt
	if expires.IsZero() {
		expires = req.Now.Add(DefaultIntentWindow)
	}
	rec.Set("expiresAt", expires.UTC())

	if err := app.Save(rec); err != nil {
		return nil, fmt.Errorf("payment: save: %w", err)
	}
	return rec, nil
}

// IntentRequest is what a caller supplies to open a payment.
type IntentRequest struct {
	OrderID    string
	ProviderID string
	Amount     int64
	Currency   string
	Method     string

	ProviderIntentID string
	ClientSecret     string
	CreatedBy        string

	ExpiresAt time.Time
	Now       time.Time
}

// methodOrOther falls back for an unknown method rather than refusing.
func methodOrOther(method string) string {
	switch method {
	case MethodCard, MethodBankTransfer, MethodWallet, MethodCash, MethodOther:
		return method
	default:
		return MethodOther
	}
}

// RecordManualPayment records money received outside any provider.
//
// The reason is required and the operator is recorded. This is the one path that
// credits an order without a provider confirming anything, so it is the one path
// where the record of who decided and why is the only evidence there is.
func RecordManualPayment(app core.App, req ManualRequest) (*core.Record, error) {
	if req.OrderID == "" {
		return nil, fmt.Errorf("payment: a payment needs an order")
	}
	if req.Amount <= 0 {
		return nil, fmt.Errorf("payment: a payment must be for a positive amount")
	}
	if len([]rune(strings.TrimSpace(req.Reason))) < 4 {
		return nil, fmt.Errorf("payment: recording a payment by hand requires a reason")
	}
	if req.RecordedBy == "" {
		return nil, fmt.Errorf("payment: recording a payment by hand requires the operator")
	}

	order, err := app.FindRecordById("orders", req.OrderID)
	if err != nil || order == nil {
		return nil, fmt.Errorf("payment: no such order")
	}

	currency := req.Currency
	if currency == "" {
		currency = order.GetString("currency")
	}

	// An intent is recorded as well as the transaction, so a manual payment appears
	// in the same list as every other attempt. A payment that exists only as a
	// transaction would be invisible to anybody looking at the order's attempts.
	intent, err := CreateIntent(app, IntentRequest{
		OrderID:   req.OrderID,
		Amount:    req.Amount,
		Currency:  currency,
		Method:    methodOrOther(req.Method),
		CreatedBy: req.RecordedBy,
		Now:       req.Now,
	})
	if err != nil {
		return nil, err
	}

	intent.Set("status", IntentSucceeded)
	intent.Set("clientSecret", "")
	if err := app.Save(intent); err != nil {
		return nil, err
	}

	col, err := app.FindCollectionByNameOrId(CollectionTransactions)
	if err != nil || col == nil {
		return nil, fmt.Errorf("payment: the transactions collection is missing")
	}

	txn := core.NewRecord(col)
	txn.Set("intent", intent.Id)
	txn.Set("order", req.OrderID)
	txn.Set("amount", req.Amount)
	txn.Set("currency", currency)
	txn.Set("status", TxnSucceeded)
	txn.Set("manual", true)
	txn.Set("manualReason", strings.TrimSpace(req.Reason))
	txn.Set("recordedBy", req.RecordedBy)
	// No provider named it, so there is no idempotency key. The partial unique index
	// is what allows several such rows.

	if err := app.Save(txn); err != nil {
		return nil, err
	}

	if err := creditOrder(app, txn, req.Now); err != nil {
		return nil, err
	}

	return txn, nil
}

// ManualRequest is a hand-recorded payment.
type ManualRequest struct {
	OrderID    string
	Amount     int64
	Currency   string
	Method     string
	Reason     string
	RecordedBy string
	Now        time.Time
}

// ExpireIntents cancels the intents whose window has passed.
func ExpireIntents(app core.App, now time.Time) (int, error) {
	records, err := app.FindRecordsByFilter(CollectionIntents,
		"(status = {:pending} || status = {:processing}) && expiresAt != '' && expiresAt < {:now}",
		"expiresAt", 200, 0,
		map[string]any{"pending": IntentPending, "processing": IntentProcessing, "now": now.UTC()})
	if err != nil {
		return 0, err
	}

	expired := 0
	for _, rec := range records {
		rec.Set("status", IntentExpired)
		rec.Set("clientSecret", "")
		if err := app.Save(rec); err != nil {
			continue
		}
		expired++
	}
	return expired, nil
}

// ---------------------------------------------------------------------------
// Reconciliation
// ---------------------------------------------------------------------------

// Settlement is one line of a provider's settlement report.
type Settlement struct {
	ProviderTxnID string
	Amount        int64
	FeeAmount     int64
	SettledAt     time.Time
}

// Discrepancy is one difference between a settlement report and the local records.
type Discrepancy struct {
	ProviderTxnID string
	Kind          string
	Detail        string
	LocalAmount   int64
	RemoteAmount  int64
}

// Discrepancy kinds.
const (
	// MismatchMissingLocally is a settlement for a transaction this system has never
	// seen, which means a payment arrived without a callback.
	MismatchMissingLocally = "missing_locally"

	// MismatchMissingRemotely is a transaction this system recorded as succeeded that
	// the provider did not settle, which means a callback was forged or replayed from
	// another environment.
	MismatchMissingRemotely = "missing_remotely"

	// MismatchAmount is a settlement for a different amount.
	MismatchAmount = "amount"

	// MismatchStatus is a settlement for a transaction that is not succeeded here.
	MismatchStatus = "status"
)

// Reconcile compares a settlement report against the local transactions.
//
// It reports differences and repairs nothing. A mismatch is a question about money,
// and a system that silently adjusts its own figures to agree with a file it was
// handed has destroyed the evidence of whatever went wrong.
func Reconcile(app core.App, settlements []Settlement, now time.Time) ([]Discrepancy, error) {
	discrepancies := make([]Discrepancy, 0)
	seen := make(map[string]bool, len(settlements))

	for _, settlement := range settlements {
		if settlement.ProviderTxnID == "" {
			continue
		}
		seen[settlement.ProviderTxnID] = true

		txn, err := FindByProviderTxn(app, settlement.ProviderTxnID)
		if err != nil {
			return nil, err
		}

		if txn == nil {
			discrepancies = append(discrepancies, Discrepancy{
				ProviderTxnID: settlement.ProviderTxnID,
				Kind:          MismatchMissingLocally,
				Detail:        "the provider settled a transaction this system has no record of",
				RemoteAmount:  settlement.Amount,
			})
			continue
		}

		if txn.GetString("status") != TxnSucceeded {
			discrepancies = append(discrepancies, Discrepancy{
				ProviderTxnID: settlement.ProviderTxnID,
				Kind:          MismatchStatus,
				Detail:        "the provider settled a transaction recorded here as " + txn.GetString("status"),
				LocalAmount:   int64(txn.GetFloat("amount")),
				RemoteAmount:  settlement.Amount,
			})
			continue
		}

		if local := int64(txn.GetFloat("amount")); local != settlement.Amount {
			discrepancies = append(discrepancies, Discrepancy{
				ProviderTxnID: settlement.ProviderTxnID,
				Kind:          MismatchAmount,
				Detail:        "the settled amount differs",
				LocalAmount:   local,
				RemoteAmount:  settlement.Amount,
			})
			continue
		}

		// Agreement. The transaction is marked settled, which is what the reconciled
		// flag exists for: a transaction nobody has confirmed against a statement is
		// not the same as one that has been.
		if !txn.GetBool("reconciled") {
			txn.Set("reconciled", true)
			txn.Set("reconciledAt", now.UTC())
			if !settlement.SettledAt.IsZero() {
				txn.Set("settledAt", settlement.SettledAt.UTC())
			}
			if settlement.FeeAmount > 0 {
				txn.Set("feeAmount", settlement.FeeAmount)
			}
			if err := app.Save(txn); err != nil {
				return nil, err
			}
		}
	}

	// The other direction: a transaction recorded here as succeeded that the report
	// does not mention. That is the shape of a forged callback, and it is the reason
	// reconciliation looks both ways.
	//
	// Only succeeded, unreconciled transactions are considered, and only those old
	// enough to have settled. A payment taken an hour ago has not reached a statement
	// yet, and reporting it would make every reconciliation noisy enough to ignore.
	cutoff := now.Add(-7 * 24 * time.Hour)
	local, err := app.FindRecordsByFilter(CollectionTransactions,
		"status = {:s} && reconciled = false && created < {:cutoff}",
		"created", 500, 0,
		map[string]any{"s": TxnSucceeded, "cutoff": cutoff.UTC()})
	if err != nil {
		return nil, err
	}

	for _, txn := range local {
		id := txn.GetString("providerTxnId")
		if id == "" || seen[id] {
			continue
		}
		discrepancies = append(discrepancies, Discrepancy{
			ProviderTxnID: id,
			Kind:          MismatchMissingRemotely,
			Detail:        "recorded here as succeeded but not present in the settlement report",
			LocalAmount:   int64(txn.GetFloat("amount")),
		})
	}

	return discrepancies, nil
}
