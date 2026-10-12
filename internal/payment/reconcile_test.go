package payment_test

import (
	"testing"
	"time"

	"pocketadmin/internal/payment"
	"pocketadmin/internal/testsupport"
)

// settlementOf builds a settlement line.
func settlementOf(id string, amount int64) payment.Settlement {
	return payment.Settlement{ProviderTxnID: id, Amount: amount, SettledAt: time.Now()}
}

// TestReconciliationMarksAnAgreedTransaction.
func TestReconciliationMarksAnAgreedTransaction(t *testing.T) {
	app := testsupport.NewApp(t)
	now := time.Now()

	order := orderWith(t, app, 5000)
	intent := intentFor(t, app, order.Id, 5000, now)

	if _, err := payment.ApplyCallback(app, intent, payment.Outcome{
		ProviderTxnID: "agree-1", Status: payment.TxnSucceeded, Amount: 5000,
	}, now); err != nil {
		t.Fatalf("ApplyCallback: %v", err)
	}

	discrepancies, err := payment.Reconcile(app, []payment.Settlement{
		settlementOf("agree-1", 5000),
	}, now)
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if len(discrepancies) != 0 {
		t.Errorf("%d discrepancies for an agreed settlement: %+v", len(discrepancies), discrepancies)
	}

	txn, _ := payment.FindByProviderTxn(app, "agree-1")
	if !txn.GetBool("reconciled") {
		t.Error("an agreed transaction was not marked reconciled")
	}
	if txn.GetDateTime("reconciledAt").IsZero() {
		t.Error("the reconciliation time was not recorded")
	}
}

// TestASettlementForAnUnknownTransactionIsReported: a payment arrived without a
// callback, which is money the shop has and does not know about.
func TestASettlementForAnUnknownTransactionIsReported(t *testing.T) {
	app := testsupport.NewApp(t)

	discrepancies, err := payment.Reconcile(app, []payment.Settlement{
		settlementOf("never-seen", 5000),
	}, time.Now())
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if len(discrepancies) != 1 {
		t.Fatalf("%d discrepancies, want 1", len(discrepancies))
	}
	if discrepancies[0].Kind != payment.MismatchMissingLocally {
		t.Errorf("the kind is %q", discrepancies[0].Kind)
	}
	if discrepancies[0].RemoteAmount != 5000 {
		t.Errorf("the remote amount is %d", discrepancies[0].RemoteAmount)
	}
}

// TestAnAmountDifferenceIsReported.
func TestAnAmountDifferenceIsReported(t *testing.T) {
	app := testsupport.NewApp(t)
	now := time.Now()

	order := orderWith(t, app, 5000)
	intent := intentFor(t, app, order.Id, 5000, now)

	if _, err := payment.ApplyCallback(app, intent, payment.Outcome{
		ProviderTxnID: "amount-1", Status: payment.TxnSucceeded, Amount: 5000,
	}, now); err != nil {
		t.Fatalf("ApplyCallback: %v", err)
	}

	discrepancies, err := payment.Reconcile(app, []payment.Settlement{
		settlementOf("amount-1", 4500),
	}, now)
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if len(discrepancies) != 1 {
		t.Fatalf("%d discrepancies, want 1", len(discrepancies))
	}
	if discrepancies[0].Kind != payment.MismatchAmount {
		t.Errorf("the kind is %q", discrepancies[0].Kind)
	}
	if discrepancies[0].LocalAmount != 5000 || discrepancies[0].RemoteAmount != 4500 {
		t.Errorf("the amounts are %d and %d", discrepancies[0].LocalAmount, discrepancies[0].RemoteAmount)
	}
}

// TestASucceededPaymentMissingFromTheReportIsReported: that is the shape of a forged
// callback, and it is why reconciliation looks both ways.
func TestASucceededPaymentMissingFromTheReportIsReported(t *testing.T) {
	app := testsupport.NewApp(t)
	now := time.Now()

	order := orderWith(t, app, 5000)
	intent := intentFor(t, app, order.Id, 5000, now)

	if _, err := payment.ApplyCallback(app, intent, payment.Outcome{
		ProviderTxnID: "forged-1", Status: payment.TxnSucceeded, Amount: 5000,
	}, now); err != nil {
		t.Fatalf("ApplyCallback: %v", err)
	}

	// A report from ten days later, so the transaction is past the settling window.
	later := now.Add(10 * 24 * time.Hour)
	discrepancies, err := payment.Reconcile(app, []payment.Settlement{
		settlementOf("something-else", 100),
	}, later)
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	found := false
	for _, d := range discrepancies {
		if d.ProviderTxnID == "forged-1" && d.Kind == payment.MismatchMissingRemotely {
			found = true
		}
	}
	if !found {
		t.Errorf("a succeeded payment absent from the report was not reported: %+v", discrepancies)
	}
}

// TestARecentPaymentIsNotReportedAsMissing: a payment taken an hour ago has not
// reached a statement yet, and reporting it would make every reconciliation noisy
// enough to ignore.
func TestARecentPaymentIsNotReportedAsMissing(t *testing.T) {
	app := testsupport.NewApp(t)
	now := time.Now()

	order := orderWith(t, app, 5000)
	intent := intentFor(t, app, order.Id, 5000, now)

	if _, err := payment.ApplyCallback(app, intent, payment.Outcome{
		ProviderTxnID: "recent-1", Status: payment.TxnSucceeded, Amount: 5000,
	}, now); err != nil {
		t.Fatalf("ApplyCallback: %v", err)
	}

	// The report arrives a minute later and does not mention it.
	discrepancies, err := payment.Reconcile(app, []payment.Settlement{
		settlementOf("unrelated", 100),
	}, now.Add(time.Minute))
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	for _, d := range discrepancies {
		if d.ProviderTxnID == "recent-1" {
			t.Errorf("a payment taken a minute ago was reported missing: %+v", d)
		}
	}
}

// TestReconciliationRepairsNothing: a mismatch is a question about money, and a
// system that silently adjusts its own figures to agree with a file it was handed has
// destroyed the evidence of whatever went wrong.
func TestReconciliationRepairsNothing(t *testing.T) {
	app := testsupport.NewApp(t)
	now := time.Now()

	order := orderWith(t, app, 5000)
	intent := intentFor(t, app, order.Id, 5000, now)

	if _, err := payment.ApplyCallback(app, intent, payment.Outcome{
		ProviderTxnID: "untouched-1", Status: payment.TxnSucceeded, Amount: 5000,
	}, now); err != nil {
		t.Fatalf("ApplyCallback: %v", err)
	}

	if _, err := payment.Reconcile(app, []payment.Settlement{
		settlementOf("untouched-1", 4000),
	}, now); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	// The local record still says 5000, and the order is still credited 5000.
	txn, _ := payment.FindByProviderTxn(app, "untouched-1")
	if got := int64(txn.GetFloat("amount")); got != 5000 {
		t.Errorf("reconciliation changed the local amount to %d", got)
	}
	if got := paidAmount(t, app, order.Id); got != 5000 {
		t.Errorf("reconciliation changed the order's credit to %d", got)
	}
	if txn.GetBool("reconciled") {
		t.Error("a disagreed transaction was marked reconciled")
	}
}

// TestExpiringAnIntentClearsItsSecret.
func TestExpiringAnIntentClearsItsSecret(t *testing.T) {
	app := testsupport.NewApp(t)
	now := time.Now()

	order := orderWith(t, app, 5000)
	intent, err := payment.CreateIntent(app, payment.IntentRequest{
		OrderID: order.Id, Amount: 5000, Currency: "USD", Method: payment.MethodCard,
		ClientSecret: "a-secret", ExpiresAt: now.Add(-time.Hour), Now: now.Add(-2 * time.Hour),
	})
	if err != nil {
		t.Fatalf("CreateIntent: %v", err)
	}

	expired, err := payment.ExpireIntents(app, now)
	if err != nil {
		t.Fatalf("ExpireIntents: %v", err)
	}
	if expired != 1 {
		t.Errorf("%d intents expired, want 1", expired)
	}

	// The record is reloaded, because ExpireIntents works on its own copy.
	reloaded, _ := app.FindRecordById(payment.CollectionIntents, intent.Id)
	if reloaded.GetString("status") != payment.IntentExpired {
		t.Errorf("the intent is %q, want expired", reloaded.GetString("status"))
	}
	if reloaded.GetString("clientSecret") != "" {
		t.Error("an expired intent kept its client secret, which is a credential with nothing left to authorise")
	}
}
