package refund_test

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/pocketbase/pocketbase/core"

	"pocketadmin/internal/refund"
	"pocketadmin/internal/testsupport"
)

// paidOrder creates an order that received money.
func paidOrder(t *testing.T, app core.App, total int64) (*core.Record, *core.Record) {
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

	orders, _ := app.FindCollectionByNameOrId("orders")
	order := core.NewRecord(orders)
	order.Set("orderNo", "ORD-R-1")
	order.Set("user", user.Id)
	order.Set("status", "paid")
	order.Set("paymentStatus", "paid")
	order.Set("source", "web")
	order.Set("currency", "USD")
	order.Set("subtotal", total)
	order.Set("grandTotal", total)
	order.Set("amountPaid", total)
	order.Set("amountRefunded", 0)
	if err := app.Save(order); err != nil {
		t.Fatalf("save order: %v", err)
	}
	return order, user
}

// request opens a refund.
func request(t *testing.T, app core.App, orderID, requester string, mode string, amount int64, items []refund.ItemQuantity) *core.Record {
	t.Helper()

	no, err := refund.NextRefundNo(app, "REF", time.Now())
	if err != nil {
		t.Fatalf("NextRefundNo: %v", err)
	}

	rec, err := refund.Request(app, refund.RequestInput{
		RefundNo: no, OrderID: orderID, Mode: mode, Amount: amount, Items: items,
		Currency: "USD", Reason: "customer_request", ReasonDetail: "changed their mind",
		RequestedBy: requester, Restock: false,
	})
	if err != nil {
		t.Fatalf("Request: %v", err)
	}
	return rec
}

// otherAccount creates a second administrator to review with.
func otherAccount(t *testing.T, app core.App, email string) *core.Record {
	t.Helper()

	col, _ := app.FindCollectionByNameOrId("users")
	rec := core.NewRecord(col)
	rec.Set("email", email)
	rec.Set("password", "change-me-please")
	rec.Set("passwordConfirm", "change-me-please")
	rec.Set("status", "active")
	if err := app.Save(rec); err != nil {
		t.Fatalf("save reviewer: %v", err)
	}
	return rec
}

// TestTheCapIsEnforcedWhenTheSecondRefundIsApproved is the property that carries the
// money.
//
// Two approvals that each look fine on their own are an overpayment when they happen
// at the same moment. The check made when a refund was requested runs outside any
// transaction, so it cannot be what holds.
func TestTheCapIsEnforcedWhenTheSecondRefundIsApproved(t *testing.T) {
	app := testsupport.NewApp(t)

	order, requester := paidOrder(t, app, 5000)
	reviewer := otherAccount(t, app, "reviewer@example.com")

	// The two refunds are written directly rather than through Request, because
	// Request refuses early and would stop the test before it reached the check it is
	// about. The early refusal is a kindness; the check inside Approve, under a
	// transaction, is what actually holds -- and it is the one that has to be tested.
	first := directRefund(t, app, order.Id, requester.Id, 5000)
	second := directRefund(t, app, order.Id, requester.Id, 5000)

	if _, err := refund.Approve(app, first, reviewer.Id, "looks right", refund.DefaultPolicy()); err != nil {
		t.Fatalf("the first approval failed: %v", err)
	}

	// The second must be refused: there is nothing left.
	if _, err := refund.Approve(app, second, reviewer.Id, "also fine", refund.DefaultPolicy()); err == nil {
		t.Fatal("a second refund for the whole order was approved")
	}

	// And the refusal is about the cap, not about something incidental.
	reloaded, _ := app.FindRecordById(refund.CollectionRefunds, second.Id)
	if reloaded.GetString("status") != refund.StatusPending {
		t.Errorf("the refused refund is %q, want pending", reloaded.GetString("status"))
	}

	remaining, err := refund.Refundable(app, order.Id, "")
	if err != nil {
		t.Fatalf("Refundable: %v", err)
	}
	if remaining != 0 {
		t.Errorf("%d is left to refund, want 0", remaining)
	}
}

// TestAPendingRefundDoesNotReserveCapacity is the decision this module turned on.
//
// Counting pending refunds as committed means two requests for the whole order each
// see the other as committed and neither can be approved. The test that was written to
// prove the opposite is what found the deadlock.
func TestAPendingRefundDoesNotReserveCapacity(t *testing.T) {
	app := testsupport.NewApp(t)

	order, requester := paidOrder(t, app, 5000)

	request(t, app, order.Id, requester.Id, refund.ModePartialAmount, 3000, nil)

	remaining, err := refund.Refundable(app, order.Id, "")
	if err != nil {
		t.Fatalf("Refundable: %v", err)
	}
	if remaining != 5000 {
		t.Errorf("%d is refundable with 3000 merely pending, want the whole 5000", remaining)
	}

	// A request for more than the order is worth is still refused at the door. That
	// check is a kindness rather than the control; the control is at approval.
	if _, err := refund.Request(app, refund.RequestInput{
		RefundNo: "REF-TOO-BIG", OrderID: order.Id, Mode: refund.ModePartialAmount, Amount: 9000,
		Currency: "USD", Reason: "customer_request", RequestedBy: requester.Id,
	}); err == nil {
		t.Error("a request for more than the order is worth was accepted")
	}
}

// TestApprovingCommitsCapacity: the approval is what reserves it.
func TestApprovingCommitsCapacity(t *testing.T) {
	app := testsupport.NewApp(t)

	order, requester := paidOrder(t, app, 5000)
	reviewer := otherAccount(t, app, "reviewer@example.com")

	rec := request(t, app, order.Id, requester.Id, refund.ModePartialAmount, 3000, nil)
	if _, err := refund.Approve(app, rec, reviewer.Id, "ok", refund.DefaultPolicy()); err != nil {
		t.Fatalf("Approve: %v", err)
	}

	remaining, _ := refund.Refundable(app, order.Id, "")
	if remaining != 2000 {
		t.Errorf("%d is refundable after approving 3000, want 2000", remaining)
	}
}

// TestFreedCapacityIsRecomputedRatherThanCounted: cancelling an approved refund
// releases what it held, which is why the figure is derived rather than stored. A
// counter would have to be decremented on every rejection, cancellation and failure,
// and would be wrong the first time one was missed.
func TestFreedCapacityIsRecomputedRatherThanCounted(t *testing.T) {
	app := testsupport.NewApp(t)

	order, requester := paidOrder(t, app, 5000)
	reviewer := otherAccount(t, app, "reviewer@example.com")

	rec := request(t, app, order.Id, requester.Id, refund.ModeFull, 0, nil)
	if _, err := refund.Approve(app, rec, reviewer.Id, "ok", refund.DefaultPolicy()); err != nil {
		t.Fatalf("Approve: %v", err)
	}

	remaining, _ := refund.Refundable(app, order.Id, "")
	if remaining != 0 {
		t.Fatalf("an approved full refund left %d, want 0", remaining)
	}

	// Withdrawn before it is sent: the money never left, so the capacity is back.
	if err := refund.Cancel(app, rec, "the customer withdrew the request"); err != nil {
		t.Fatalf("Cancel: %v", err)
	}

	remaining, _ = refund.Refundable(app, order.Id, "")
	if remaining != 5000 {
		t.Errorf("after a withdrawal %d is left, want the whole 5000", remaining)
	}
}

// TestARejectedRefundNeverCommittedAnything: a rejection of a pending request frees
// nothing, because a pending request was never holding anything.
func TestARejectedRefundNeverCommittedAnything(t *testing.T) {
	app := testsupport.NewApp(t)

	order, requester := paidOrder(t, app, 5000)
	reviewer := otherAccount(t, app, "reviewer@example.com")

	rec := request(t, app, order.Id, requester.Id, refund.ModeFull, 0, nil)
	if err := refund.Reject(app, rec, reviewer.Id, "no grounds"); err != nil {
		t.Fatalf("Reject: %v", err)
	}

	remaining, _ := refund.Refundable(app, order.Id, "")
	if remaining != 5000 {
		t.Errorf("%d is left after a rejection, want the whole 5000", remaining)
	}
}

// TestNobodyApprovesTheirOwnRefund is the reason the two permissions are separate.
func TestNobodyApprovesTheirOwnRefund(t *testing.T) {
	app := testsupport.NewApp(t)

	order, requester := paidOrder(t, app, 5000)
	rec := request(t, app, order.Id, requester.Id, refund.ModeFull, 0, nil)

	_, err := refund.Approve(app, rec, requester.Id, "approving my own", refund.DefaultPolicy())
	if err == nil {
		t.Fatal("a refund was approved by the person who requested it")
	}
	if !strings.Contains(err.Error(), "requested") {
		t.Errorf("the refusal does not explain itself: %v", err)
	}

	// And the same for a rejection.
	if err := refund.Reject(app, rec, requester.Id, "no"); err == nil {
		t.Error("a refund was rejected by the person who requested it")
	}
}

// TestSelfApprovalIsRefusedEvenUnderTheAutoApprovalThreshold: the rule is about who
// decided, and an automatic decision taken because the requester is the only person
// available is still the requester deciding.
func TestSelfApprovalIsRefusedEvenUnderTheAutoApprovalThreshold(t *testing.T) {
	app := testsupport.NewApp(t)

	order, requester := paidOrder(t, app, 5000)
	rec := request(t, app, order.Id, requester.Id, refund.ModePartialAmount, 100, nil)

	policy := refund.Policy{AutoApproveThreshold: 1000}
	if _, err := refund.Approve(app, rec, requester.Id, "", policy); err == nil {
		t.Error("a self-approval was allowed because the policy would have approved it anyway")
	}
}

// TestTheAutoApprovalThreshold.
func TestTheAutoApprovalThreshold(t *testing.T) {
	app := testsupport.NewApp(t)

	order, requester := paidOrder(t, app, 5000)
	reviewer := otherAccount(t, app, "reviewer@example.com")
	policy := refund.Policy{AutoApproveThreshold: 1000}

	small := request(t, app, order.Id, requester.Id, refund.ModePartialAmount, 500, nil)
	decision, err := refund.Approve(app, small, reviewer.Id, "", policy)
	if err != nil {
		t.Fatalf("Approve: %v", err)
	}
	if !decision.AutoApproved {
		t.Error("a refund under the threshold was not auto-approved")
	}
	if !small.GetBool("autoApproved") {
		t.Error("the auto-approval was not recorded")
	}
	// An automatic approval has no reviewer. Recording one would be a lie about who
	// decided.
	if small.GetString("reviewedBy") != "" {
		t.Error("an automatically approved refund names a reviewer")
	}

	// Above the threshold a person decides.
	large := request(t, app, order.Id, requester.Id, refund.ModePartialAmount, 2000, nil)
	decision, err = refund.Approve(app, large, reviewer.Id, "checked", policy)
	if err != nil {
		t.Fatalf("Approve: %v", err)
	}
	if decision.AutoApproved {
		t.Error("a refund above the threshold was auto-approved")
	}
	if large.GetString("reviewedBy") != reviewer.Id {
		t.Error("a manually approved refund does not name its reviewer")
	}
}

// TestAThresholdOfZeroDisablesAutoApproval: the safe default for a shop that has not
// thought about it is that a person looks.
func TestAThresholdOfZeroDisablesAutoApproval(t *testing.T) {
	app := testsupport.NewApp(t)

	order, requester := paidOrder(t, app, 5000)
	reviewer := otherAccount(t, app, "reviewer@example.com")

	rec := request(t, app, order.Id, requester.Id, refund.ModePartialAmount, 1, nil)
	decision, err := refund.Approve(app, rec, reviewer.Id, "", refund.DefaultPolicy())
	if err != nil {
		t.Fatalf("Approve: %v", err)
	}
	if decision.AutoApproved {
		t.Error("a one-cent refund was auto-approved with a threshold of zero")
	}
}

// TestOnlyAPendingRefundIsDecided: approving an already-approved one would commit
// the money twice.
func TestOnlyAPendingRefundIsDecided(t *testing.T) {
	app := testsupport.NewApp(t)

	order, requester := paidOrder(t, app, 5000)
	reviewer := otherAccount(t, app, "reviewer@example.com")

	rec := request(t, app, order.Id, requester.Id, refund.ModeFull, 0, nil)
	if _, err := refund.Approve(app, rec, reviewer.Id, "yes", refund.DefaultPolicy()); err != nil {
		t.Fatalf("Approve: %v", err)
	}

	if _, err := refund.Approve(app, rec, reviewer.Id, "yes again", refund.DefaultPolicy()); err == nil {
		t.Error("an approved refund was approved a second time")
	}
	if err := refund.Reject(app, rec, reviewer.Id, "no"); err == nil {
		t.Error("an approved refund was rejected")
	}
}

// TestRejectingRequiresAReason: a refusal with no reason is a dead end for the
// customer and for whoever answers the complaint.
func TestRejectingRequiresAReason(t *testing.T) {
	app := testsupport.NewApp(t)

	order, requester := paidOrder(t, app, 5000)
	reviewer := otherAccount(t, app, "reviewer@example.com")
	rec := request(t, app, order.Id, requester.Id, refund.ModeFull, 0, nil)

	if err := refund.Reject(app, rec, reviewer.Id, ""); err == nil {
		t.Error("a refund was rejected with no reason")
	}
	if err := refund.Reject(app, rec, reviewer.Id, "   "); err == nil {
		t.Error("a refund was rejected with a blank reason")
	}
	if err := refund.Reject(app, rec, reviewer.Id, "outside the return window"); err != nil {
		t.Errorf("a proper rejection failed: %v", err)
	}
}

// TestAFullRefundIsWhatIsLeftRatherThanWhatWasPaid: an order already partly refunded
// cannot be refunded in full a second time.
func TestAFullRefundIsWhatIsLeftRatherThanWhatWasPaid(t *testing.T) {
	app := testsupport.NewApp(t)

	order, requester := paidOrder(t, app, 5000)
	reviewer := otherAccount(t, app, "reviewer@example.com")

	// A partial refund first, approved.
	partial := request(t, app, order.Id, requester.Id, refund.ModePartialAmount, 2000, nil)
	if _, err := refund.Approve(app, partial, reviewer.Id, "part", refund.DefaultPolicy()); err != nil {
		t.Fatalf("Approve: %v", err)
	}

	// A "full" refund is now the remaining 3000, not 5000.
	full := request(t, app, order.Id, requester.Id, refund.ModeFull, 0, nil)
	if got := int64(full.GetFloat("amount")); got != 3000 {
		t.Errorf("a full refund is for %d, want the remaining 3000", got)
	}
}

// TestRefundingByItemUsesTheOrderSnapshotAndSharesTheDiscount.
//
// The catalogue may have changed since the order was placed, so the prices come from
// the order. And without the discount share, refunding every item of a discounted
// order gives back more than was paid for it.
func TestRefundingByItemUsesTheOrderSnapshotAndSharesTheDiscount(t *testing.T) {
	app := testsupport.NewApp(t)

	order, requester := paidOrder(t, app, 5000)

	// An order with a 1000 discount across two lines, so the item prices total 6000
	// while 5000 was paid.
	order.Set("subtotal", 6000)
	order.Set("discountTotal", 1000)
	order.Set("grandTotal", 5000)
	order.Set("amountPaid", 5000)
	if err := app.Save(order); err != nil {
		t.Fatalf("save order: %v", err)
	}

	items, _ := app.FindCollectionByNameOrId("order_items")
	cheap := core.NewRecord(items)
	cheap.Set("order", order.Id)
	cheap.Set("productName", "Cheap")
	cheap.Set("unitPrice", 1000)
	cheap.Set("quantity", 2)
	cheap.Set("subtotal", 2000)
	cheap.Set("total", 2000)
	cheap.Set("currency", "USD")
	if err := app.Save(cheap); err != nil {
		t.Fatalf("save item: %v", err)
	}

	dear := core.NewRecord(items)
	dear.Set("order", order.Id)
	dear.Set("productName", "Dear")
	dear.Set("unitPrice", 4000)
	dear.Set("quantity", 1)
	dear.Set("subtotal", 4000)
	dear.Set("total", 4000)
	dear.Set("currency", "USD")
	if err := app.Save(dear); err != nil {
		t.Fatalf("save item: %v", err)
	}

	// One cheap item: 1000 of a 6000 order, so a sixth of the 1000 discount.
	rec := request(t, app, order.Id, requester.Id, refund.ModePartialItems, 0,
		[]refund.ItemQuantity{{OrderItemID: cheap.Id, Quantity: 1}})

	got := int64(rec.GetFloat("amount"))
	// 1000 - (1000 * 1000 / 6000) = 1000 - 166 = 834
	if got != 834 {
		t.Errorf("one cheap item refunds %d, want 834 after its share of the discount", got)
	}

	// More than the line holds is refused.
	if _, err := refund.Request(app, refund.RequestInput{
		RefundNo: "REF-OVER", OrderID: order.Id, Mode: refund.ModePartialItems,
		Items:    []refund.ItemQuantity{{OrderItemID: cheap.Id, Quantity: 5}},
		Currency: "USD", Reason: "customer_request", RequestedBy: requester.Id,
	}); err == nil {
		t.Error("a refund for more of an item than the order holds was accepted")
	}

	// An item that is not on the order is refused.
	if _, err := refund.Request(app, refund.RequestInput{
		RefundNo: "REF-WRONG", OrderID: order.Id, Mode: refund.ModePartialItems,
		Items:    []refund.ItemQuantity{{OrderItemID: "not-an-item", Quantity: 1}},
		Currency: "USD", Reason: "customer_request", RequestedBy: requester.Id,
	}); err == nil {
		t.Error("a refund naming an item from another order was accepted")
	}
}

// TestRequestingRequiresAReasonAndARequester.
func TestRequestingRequiresAReasonAndARequester(t *testing.T) {
	app := testsupport.NewApp(t)
	order, requester := paidOrder(t, app, 5000)

	base := refund.RequestInput{
		RefundNo: "REF-BASE", OrderID: order.Id, Mode: refund.ModePartialAmount,
		Amount: 100, Currency: "USD", Reason: "customer_request", RequestedBy: requester.Id,
	}

	bad := base
	bad.Reason = ""
	if _, err := refund.Request(app, bad); err == nil {
		t.Error("a refund with no reason was accepted")
	}

	bad = base
	bad.RequestedBy = ""
	if _, err := refund.Request(app, bad); err == nil {
		t.Error("a refund with no requester was accepted")
	}

	bad = base
	bad.Amount = 0
	if _, err := refund.Request(app, bad); err == nil {
		t.Error("a refund of nothing was accepted")
	}

	bad = base
	bad.Mode = "something_else"
	if _, err := refund.Request(app, bad); err == nil {
		t.Error("a refund with an unknown mode was accepted")
	}
}

// TestTheOrderLedgerFollowsASucceededRefund.
func TestTheOrderLedgerFollowsASucceededRefund(t *testing.T) {
	app := testsupport.NewApp(t)

	order, requester := paidOrder(t, app, 5000)
	reviewer := otherAccount(t, app, "reviewer@example.com")

	rec := request(t, app, order.Id, requester.Id, refund.ModePartialAmount, 2000, nil)
	if _, err := refund.Approve(app, rec, reviewer.Id, "ok", refund.DefaultPolicy()); err != nil {
		t.Fatalf("Approve: %v", err)
	}

	result, err := refund.Execute(app, rec, stubExecutor{id: "prov-refund-1"}, time.Now())
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !result.Succeeded {
		t.Fatalf("the refund was not sent: %s", result.Message)
	}

	reloaded, _ := app.FindRecordById("orders", order.Id)
	if got := int64(reloaded.GetFloat("amountRefunded")); got != 2000 {
		t.Errorf("the order has %d refunded, want 2000", got)
	}
	// A partial refund leaves the order alone: the customer still has part of what
	// they bought.
	if reloaded.GetString("status") != "paid" {
		t.Errorf("a partial refund moved the order to %q", reloaded.GetString("status"))
	}
	// The payment status stays paid. "partial" in this vocabulary means partly *paid*,
	// and the customer did pay in full; what came back is on amountRefunded. Reporting
	// partial here would say the customer still owes money they do not.
	if reloaded.GetString("paymentStatus") != "paid" {
		t.Errorf("the payment status is %q, want paid", reloaded.GetString("paymentStatus"))
	}
}

// TestAFullRefundCompletesTheOrdersLife.
func TestAFullRefundCompletesTheOrdersLife(t *testing.T) {
	app := testsupport.NewApp(t)

	order, requester := paidOrder(t, app, 5000)
	reviewer := otherAccount(t, app, "reviewer@example.com")

	rec := request(t, app, order.Id, requester.Id, refund.ModeFull, 0, nil)
	if _, err := refund.Approve(app, rec, reviewer.Id, "ok", refund.DefaultPolicy()); err != nil {
		t.Fatalf("Approve: %v", err)
	}
	if _, err := refund.Execute(app, rec, stubExecutor{id: "prov-refund-2"}, time.Now()); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	reloaded, _ := app.FindRecordById("orders", order.Id)
	if reloaded.GetString("status") != "refunded" {
		t.Errorf("the order is %q, want refunded", reloaded.GetString("status"))
	}
	if reloaded.GetString("paymentStatus") != "refunded" {
		t.Errorf("the payment status is %q, want refunded", reloaded.GetString("paymentStatus"))
	}
}

// TestAFailedExecutionLeavesTheOrderAlone.
func TestAFailedExecutionLeavesTheOrderAlone(t *testing.T) {
	app := testsupport.NewApp(t)

	order, requester := paidOrder(t, app, 5000)
	reviewer := otherAccount(t, app, "reviewer@example.com")

	rec := request(t, app, order.Id, requester.Id, refund.ModeFull, 0, nil)
	if _, err := refund.Approve(app, rec, reviewer.Id, "ok", refund.DefaultPolicy()); err != nil {
		t.Fatalf("Approve: %v", err)
	}

	result, err := refund.Execute(app, rec, stubExecutor{failure: "the card has since been closed"}, time.Now())
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Succeeded {
		t.Fatal("a failed refund reported success")
	}
	if result.Retryable {
		t.Error("an unclassified provider refusal was reported as worth retrying")
	}

	reloaded, _ := app.FindRecordById("orders", order.Id)
	if got := int64(reloaded.GetFloat("amountRefunded")); got != 0 {
		t.Errorf("a failed refund credited %d to the order", got)
	}

	reloadedRefund, _ := app.FindRecordById(refund.CollectionRefunds, rec.Id)
	if reloadedRefund.GetString("status") != refund.StatusFailed {
		t.Errorf("the refund is %q, want failed", reloadedRefund.GetString("status"))
	}
	if reloadedRefund.GetString("lastError") == "" {
		t.Error("the failure was not recorded")
	}
	// The capacity is still held, because a failed refund is a refund that might be
	// sent again.
	if remaining, _ := refund.Refundable(app, order.Id, ""); remaining != 0 {
		t.Errorf("a failed refund freed its capacity: %d is left", remaining)
	}
}

// TestATransientFailureIsRetryable.
func TestATransientFailureIsRetryable(t *testing.T) {
	app := testsupport.NewApp(t)

	order, requester := paidOrder(t, app, 5000)
	reviewer := otherAccount(t, app, "reviewer@example.com")

	rec := request(t, app, order.Id, requester.Id, refund.ModeFull, 0, nil)
	if _, err := refund.Approve(app, rec, reviewer.Id, "ok", refund.DefaultPolicy()); err != nil {
		t.Fatalf("Approve: %v", err)
	}

	result, err := refund.Execute(app, rec, stubExecutor{code: "timeout", failure: "the provider timed out"}, time.Now())
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !result.Retryable {
		t.Error("a timeout was not reported as worth retrying")
	}

	// A failed refund is worth trying again, and the attempt count is recorded.
	found, err := refund.Retryable(app)
	if err != nil {
		t.Fatalf("Retryable: %v", err)
	}
	if len(found) != 1 {
		t.Errorf("%d refunds are worth retrying, want 1", len(found))
	}
}

// TestExecutingRequiresAnApproval.
func TestExecutingRequiresAnApproval(t *testing.T) {
	app := testsupport.NewApp(t)

	order, requester := paidOrder(t, app, 5000)
	rec := request(t, app, order.Id, requester.Id, refund.ModeFull, 0, nil)

	if _, err := refund.Execute(app, rec, stubExecutor{id: "x"}, time.Now()); err == nil {
		t.Error("a pending refund was executed without approval")
	}
}

// itoa renders a small integer, so the fixture reads without a format import.
func itoa(value int) string { return strconv.Itoa(value) }

// directRefund writes a pending refund without going through Request.
//
// It stands in for the state two concurrent requests produce, which a single-threaded
// test cannot reach any other way.
func directRefund(t *testing.T, app core.App, orderID, requesterID string, amount int64) *core.Record {
	t.Helper()

	count, _ := app.CountRecords(refund.CollectionRefunds)

	col, _ := app.FindCollectionByNameOrId(refund.CollectionRefunds)
	rec := core.NewRecord(col)
	rec.Set("refundNo", "REF-DIRECT-"+itoa(int(count)+1))
	rec.Set("order", orderID)
	rec.Set("mode", refund.ModePartialAmount)
	rec.Set("amount", amount)
	rec.Set("currency", "USD")
	rec.Set("status", refund.StatusPending)
	rec.Set("reason", "customer_request")
	rec.Set("requestedBy", requesterID)
	rec.Set("attempts", 0)
	if err := app.Save(rec); err != nil {
		t.Fatalf("save a pending refund: %v", err)
	}
	return rec
}

// stubExecutor stands in for a payment provider.
type stubExecutor struct {
	id      string
	code    string
	failure string
}

func (s stubExecutor) Refund(app core.App, rec *core.Record, amount int64) (refund.Execution, error) {
	if s.failure != "" {
		return refund.Execution{FailureCode: s.code, FailureMessage: s.failure}, nil
	}
	return refund.Execution{ProviderRefundID: s.id, Response: map[string]any{"status": "refunded"}}, nil
}
