package payment_test

import (
	"testing"
	"time"

	"github.com/pocketbase/pocketbase/core"

	"pocketadmin/internal/payment"
	"pocketadmin/internal/testsupport"
)

// orderWith creates a user and a pending order for a given total.
func orderWith(t *testing.T, app core.App, total int64) *core.Record {
	t.Helper()

	users, _ := app.FindCollectionByNameOrId("users")
	user := core.NewRecord(users)
	user.Set("email", "buyer@example.com")
	user.Set("password", "change-me-please")
	user.Set("passwordConfirm", "change-me-please")
	user.Set("status", "active")
	if err := app.Save(user); err != nil {
		t.Fatalf("save user: %v", err)
	}

	col, _ := app.FindCollectionByNameOrId("orders")
	rec := core.NewRecord(col)
	rec.Set("orderNo", "ORD-TEST-00001")
	rec.Set("user", user.Id)
	rec.Set("status", "pending_payment")
	rec.Set("paymentStatus", "unpaid")
	rec.Set("source", "web")
	rec.Set("currency", "USD")
	rec.Set("subtotal", total)
	rec.Set("grandTotal", total)
	rec.Set("amountPaid", 0)
	rec.Set("amountRefunded", 0)
	if err := app.Save(rec); err != nil {
		t.Fatalf("save order: %v", err)
	}
	return rec
}

// intentFor opens a payment intent for an order.
func intentFor(t *testing.T, app core.App, orderID string, amount int64, now time.Time) *core.Record {
	t.Helper()

	rec, err := payment.CreateIntent(app, payment.IntentRequest{
		OrderID:  orderID,
		Amount:   amount,
		Currency: "USD",
		Method:   payment.MethodCard,
		Now:      now,
	})
	if err != nil {
		t.Fatalf("CreateIntent: %v", err)
	}
	return rec
}

// paidAmount reads an order's paid total.
func paidAmount(t *testing.T, app core.App, orderID string) int64 {
	t.Helper()

	rec, err := app.FindRecordById("orders", orderID)
	if err != nil || rec == nil {
		t.Fatalf("read order: %v", err)
	}
	return int64(rec.GetFloat("amountPaid"))
}

// TestAReplayedCallbackIsAppliedOnce is the reason this module exists.
//
// Providers retry. A callback that timed out on the network arrives again, sometimes
// several times, sometimes hours later. A second application would charge the order
// twice and credit stock twice, and the customer would find out from their statement.
func TestAReplayedCallbackIsAppliedOnce(t *testing.T) {
	app := testsupport.NewApp(t)
	now := time.Now()

	order := orderWith(t, app, 5000)
	intent := intentFor(t, app, order.Id, 5000, now)

	outcome := payment.Outcome{
		ProviderTxnID: "prov-txn-1",
		Status:        payment.TxnSucceeded,
		Amount:        5000,
	}

	first, err := payment.ApplyCallback(app, intent, outcome, now)
	if err != nil {
		t.Fatalf("ApplyCallback: %v", err)
	}
	if !first.Applied {
		t.Fatalf("the first callback was not applied: %s", first.Reason)
	}
	if got := paidAmount(t, app, order.Id); got != 5000 {
		t.Fatalf("the order is credited %d, want 5000", got)
	}

	// The same callback, five more times.
	for i := 0; i < 5; i++ {
		result, err := payment.ApplyCallback(app, intent, outcome, now.Add(time.Duration(i)*time.Minute))
		if err != nil {
			t.Fatalf("replay %d: %v", i, err)
		}
		if result.Applied {
			t.Errorf("replay %d was applied a second time", i)
		}
		if result.Reason == "" {
			t.Errorf("replay %d gave no reason", i)
		}
	}

	// The credit has not moved.
	if got := paidAmount(t, app, order.Id); got != 5000 {
		t.Errorf("after five replays the order is credited %d, want 5000", got)
	}

	// And exactly one transaction exists.
	count, err := app.CountRecords(payment.CollectionTransactions)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 1 {
		t.Errorf("%d transactions exist after six callbacks, want 1", count)
	}

	// The replay count is visible, because a duplicate callback storm should look
	// like one rather than like nothing.
	txn, _ := payment.FindByProviderTxn(app, "prov-txn-1")
	if txn == nil {
		t.Fatal("the transaction vanished")
	}
	if got := txn.GetInt("callbackCount"); got != 6 {
		t.Errorf("the callback count is %d, want 6", got)
	}
}

// TestASucceededTransactionIsNeverAltered: corrections go through a refund, which is
// a record of its own.
func TestASucceededTransactionIsNeverAltered(t *testing.T) {
	app := testsupport.NewApp(t)
	now := time.Now()

	order := orderWith(t, app, 5000)
	intent := intentFor(t, app, order.Id, 5000, now)

	if _, err := payment.ApplyCallback(app, intent, payment.Outcome{
		ProviderTxnID: "prov-txn-2", Status: payment.TxnSucceeded, Amount: 5000,
	}, now); err != nil {
		t.Fatalf("ApplyCallback: %v", err)
	}

	// A later callback claiming a failure must not un-succeed it.
	result, err := payment.ApplyCallback(app, intent, payment.Outcome{
		ProviderTxnID: "prov-txn-2", Status: payment.TxnFailed,
		FailureCode: "card_declined", FailureMessage: "declined after the fact",
	}, now.Add(time.Minute))
	if err != nil {
		t.Fatalf("ApplyCallback: %v", err)
	}
	if result.Applied {
		t.Error("a failure callback altered a succeeded transaction")
	}

	txn, _ := payment.FindByProviderTxn(app, "prov-txn-2")
	if txn.GetString("status") != payment.TxnSucceeded {
		t.Errorf("the transaction is now %q", txn.GetString("status"))
	}
	if got := paidAmount(t, app, order.Id); got != 5000 {
		t.Errorf("the order is credited %d, want 5000", got)
	}
}

// TestAFailedTransactionCanLaterSucceed: that is a genuine change of state rather
// than a replay.
func TestAFailedTransactionCanLaterSucceed(t *testing.T) {
	app := testsupport.NewApp(t)
	now := time.Now()

	order := orderWith(t, app, 5000)
	intent := intentFor(t, app, order.Id, 5000, now)

	if _, err := payment.ApplyCallback(app, intent, payment.Outcome{
		ProviderTxnID: "prov-txn-3", Status: payment.TxnFailed,
		FailureCode: "insufficient_funds", FailureMessage: "try again",
	}, now); err != nil {
		t.Fatalf("ApplyCallback: %v", err)
	}
	if got := paidAmount(t, app, order.Id); got != 0 {
		t.Fatalf("a failed callback credited %d", got)
	}

	// The same transaction, now reported succeeded.
	result, err := payment.ApplyCallback(app, intent, payment.Outcome{
		ProviderTxnID: "prov-txn-3", Status: payment.TxnSucceeded, Amount: 5000,
	}, now.Add(time.Minute))
	if err != nil {
		t.Fatalf("ApplyCallback: %v", err)
	}
	if !result.Applied {
		t.Fatalf("a failure that later succeeded was not applied: %s", result.Reason)
	}
	if got := paidAmount(t, app, order.Id); got != 5000 {
		t.Errorf("the order is credited %d, want 5000", got)
	}
}

// TestTheAmountIsComparedNotTrusted: a callback that took a different amount from
// the one requested is a discrepancy somebody has to look at.
func TestTheAmountIsComparedNotTrusted(t *testing.T) {
	app := testsupport.NewApp(t)
	now := time.Now()

	order := orderWith(t, app, 5000)
	intent := intentFor(t, app, order.Id, 5000, now)

	result, err := payment.ApplyCallback(app, intent, payment.Outcome{
		ProviderTxnID: "prov-txn-4", Status: payment.TxnSucceeded, Amount: 999999,
	}, now)
	if err != nil {
		t.Fatalf("ApplyCallback: %v", err)
	}
	if !result.AmountMismatch {
		t.Error("a callback for a different amount was not flagged")
	}

	// The order is credited what was requested, not what the callback claimed.
	if got := paidAmount(t, app, order.Id); got != 5000 {
		t.Errorf("the order is credited %d, want the requested 5000", got)
	}
}

// TestTwoTransactionsOnOneOrderCreditBoth: multiple attempts are allowed, and a
// partial payment is a real thing.
func TestTwoTransactionsOnOneOrderCreditBoth(t *testing.T) {
	app := testsupport.NewApp(t)
	now := time.Now()

	order := orderWith(t, app, 5000)

	first := intentFor(t, app, order.Id, 2000, now)
	if _, err := payment.ApplyCallback(app, first, payment.Outcome{
		ProviderTxnID: "partial-1", Status: payment.TxnSucceeded, Amount: 2000,
	}, now); err != nil {
		t.Fatalf("ApplyCallback: %v", err)
	}

	// The order is partly paid, not paid.
	rec, _ := app.FindRecordById("orders", order.Id)
	if rec.GetString("paymentStatus") != "partial" {
		t.Errorf("the payment status is %q, want partial", rec.GetString("paymentStatus"))
	}
	if rec.GetString("status") != "pending_payment" {
		t.Errorf("a partial payment moved the order to %q", rec.GetString("status"))
	}

	second := intentFor(t, app, order.Id, 3000, now)
	if _, err := payment.ApplyCallback(app, second, payment.Outcome{
		ProviderTxnID: "partial-2", Status: payment.TxnSucceeded, Amount: 3000,
	}, now); err != nil {
		t.Fatalf("ApplyCallback: %v", err)
	}

	if got := paidAmount(t, app, order.Id); got != 5000 {
		t.Errorf("the order is credited %d, want 5000", got)
	}

	rec, _ = app.FindRecordById("orders", order.Id)
	if rec.GetString("paymentStatus") != "paid" {
		t.Errorf("the payment status is %q, want paid", rec.GetString("paymentStatus"))
	}
	if rec.GetString("status") != "paid" {
		t.Errorf("the order status is %q, want paid", rec.GetString("status"))
	}
	if rec.GetDateTime("paidAt").IsZero() {
		t.Error("the order is paid with no paidAt time")
	}
}

// TestACallbackNamesNoTransaction: without an idempotency key nothing can be applied
// safely, so it is refused rather than guessed at.
func TestACallbackNamesNoTransaction(t *testing.T) {
	app := testsupport.NewApp(t)
	now := time.Now()

	order := orderWith(t, app, 5000)
	intent := intentFor(t, app, order.Id, 5000, now)

	if _, err := payment.ApplyCallback(app, intent, payment.Outcome{
		Status: payment.TxnSucceeded, Amount: 5000,
	}, now); err == nil {
		t.Error("a callback with no transaction id was accepted")
	}
}

// TestTheIntentIsMarkedSucceededAndItsSecretCleared.
func TestTheIntentIsMarkedSucceededAndItsSecretCleared(t *testing.T) {
	app := testsupport.NewApp(t)
	now := time.Now()

	order := orderWith(t, app, 5000)
	intent, err := payment.CreateIntent(app, payment.IntentRequest{
		OrderID: order.Id, Amount: 5000, Currency: "USD", Method: payment.MethodCard,
		ClientSecret: "secret-should-be-cleared", Now: now,
	})
	if err != nil {
		t.Fatalf("CreateIntent: %v", err)
	}
	if intent.GetString("clientSecret") == "" {
		t.Fatal("the secret was not stored")
	}

	if _, err := payment.ApplyCallback(app, intent, payment.Outcome{
		ProviderTxnID: "prov-txn-5", Status: payment.TxnSucceeded, Amount: 5000,
	}, now); err != nil {
		t.Fatalf("ApplyCallback: %v", err)
	}

	if intent.GetString("status") != payment.IntentSucceeded {
		t.Errorf("the intent is %q", intent.GetString("status"))
	}
	if intent.GetString("clientSecret") != "" {
		t.Error("the client secret survived the payment")
	}
}

// TestRecordingAPaymentByHandRequiresAReasonAndAnOperator: this is the one path that
// credits an order with no provider confirming anything.
func TestRecordingAPaymentByHandRequiresAReasonAndAnOperator(t *testing.T) {
	app := testsupport.NewApp(t)
	now := time.Now()
	order := orderWith(t, app, 5000)

	// The operator has to be a real account: the relation is validated on save, and a
	// made-up identifier is refused.
	operator := order.GetString("user")

	base := payment.ManualRequest{
		OrderID: order.Id, Amount: 5000, Currency: "USD",
		Method: payment.MethodBankTransfer, Reason: "bank transfer received", RecordedBy: operator, Now: now,
	}

	// No reason.
	bad := base
	bad.Reason = ""
	if _, err := payment.RecordManualPayment(app, bad); err == nil {
		t.Error("a manual payment with no reason was accepted")
	}

	// A reason that is only whitespace is no reason.
	bad = base
	bad.Reason = "   "
	if _, err := payment.RecordManualPayment(app, bad); err == nil {
		t.Error("a manual payment with a blank reason was accepted")
	}

	// No operator.
	bad = base
	bad.RecordedBy = ""
	if _, err := payment.RecordManualPayment(app, bad); err == nil {
		t.Error("a manual payment with no operator was accepted")
	}

	// A non-positive amount.
	bad = base
	bad.Amount = 0
	if _, err := payment.RecordManualPayment(app, bad); err == nil {
		t.Error("a manual payment of nothing was accepted")
	}

	// And the real thing works.
	txn, err := payment.RecordManualPayment(app, base)
	if err != nil {
		t.Fatalf("RecordManualPayment: %v", err)
	}
	if !txn.GetBool("manual") {
		t.Error("the transaction is not marked manual")
	}
	if txn.GetString("manualReason") != "bank transfer received" {
		t.Errorf("the reason is %q", txn.GetString("manualReason"))
	}
	if got := paidAmount(t, app, order.Id); got != 5000 {
		t.Errorf("the order is credited %d", got)
	}
}

// TestAManualPaymentHasNoIdempotencyKey: several such rows must be able to coexist,
// which is why the unique index is partial.
func TestAManualPaymentHasNoIdempotencyKey(t *testing.T) {
	app := testsupport.NewApp(t)
	now := time.Now()
	order := orderWith(t, app, 5000)
	operator := order.GetString("user")

	for i := 0; i < 3; i++ {
		if _, err := payment.RecordManualPayment(app, payment.ManualRequest{
			OrderID: order.Id, Amount: 1000, Currency: "USD",
			Method: payment.MethodCash, Reason: "instalment", RecordedBy: operator, Now: now,
		}); err != nil {
			t.Fatalf("RecordManualPayment %d: %v", i, err)
		}
	}

	count, _ := app.CountRecords(payment.CollectionTransactions)
	if count != 3 {
		t.Errorf("%d manual transactions exist, want 3 (the unique index must be partial)", count)
	}
	if got := paidAmount(t, app, order.Id); got != 3000 {
		t.Errorf("the order is credited %d, want 3000", got)
	}
}
