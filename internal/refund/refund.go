// Package refund holds refund requests, their approval workflow and their execution.
//
// Two rules carry the money here. A refund is capped at what the order actually
// received, and the cap is enforced inside a transaction -- because two approvals
// that each look fine on their own are an overpayment when they happen at the same
// moment. And nobody approves their own request, because an approval workflow whose
// approver can be the requester is a formality rather than a control.
package refund

import (
	"encoding/json"
	"fmt"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/types"
)

// Collection name.
const CollectionRefunds = "refunds"

// Modes.
const (
	ModeFull          = "full"
	ModePartialAmount = "partial_amount"
	ModePartialItems  = "partial_items"
)

// Statuses.
const (
	StatusPending    = "pending"
	StatusApproved   = "approved"
	StatusRejected   = "rejected"
	StatusProcessing = "processing"
	StatusSucceeded  = "succeeded"
	StatusFailed     = "failed"
	StatusCancelled  = "cancelled"
)

// Statuses returns the vocabulary, matched by the migration's stored select.
func Statuses() []string {
	return []string{StatusPending, StatusApproved, StatusRejected, StatusProcessing,
		StatusSucceeded, StatusFailed, StatusCancelled}
}

// Modes returns the vocabulary.
func Modes() []string { return []string{ModeFull, ModePartialAmount, ModePartialItems} }

// Commits reports whether a refund has taken capacity.
//
// A **pending** refund does not. This is the decision that matters in this module, and
// it was reached by writing the test that should have passed: counting pending refunds
// as committed means two requests for the whole order each see the other as committed
// and **neither can be approved**. A deadlock is not a conservative choice, it is a
// broken one.
//
// So the specification's wording is taken at face value -- the cap is enforced *at
// approval time*, under a transaction -- and a request is a question rather than a
// reservation. The consequence is that many requests can be open at once and only the
// approvals are serialised, which is the property that actually protects the money:
// the sum of everything approved never exceeds what was paid.
//
// A **failed** refund does keep its capacity, which is the counter-intuitive case. It
// may be sent again by the retry task, and freeing its capacity would let somebody else
// refund the same money meanwhile -- and the retry does not go through an approval, so
// nothing would catch the overdraw. Releasing it is an explicit act: cancel the refund.
func Commits(status string) bool {
	switch status {
	case StatusApproved, StatusProcessing, StatusSucceeded, StatusFailed:
		return true
	default:
		return false
	}
}

// Refundable returns what is left that could be approved against an order.
//
// Everything already approved and not yet released is subtracted, and the refund being
// evaluated is excluded so a re-check does not count itself twice. Pending refunds are
// not subtracted; see Commits for why.
//
// The figure is computed rather than stored, because a stored counter would have to be
// decremented on every rejection and every withdrawal and would be wrong the first time
// one was missed.
func Refundable(app core.App, orderID, excludeRefundID string) (int64, error) {
	order, err := app.FindRecordById("orders", orderID)
	if err != nil || order == nil {
		return 0, fmt.Errorf("refund: no such order")
	}

	paid := int64(order.GetFloat("amountPaid"))
	committed, err := Committed(app, orderID, excludeRefundID)
	if err != nil {
		return 0, err
	}

	remaining := paid - committed
	if remaining < 0 {
		// The committed figure can exceed what was paid if a payment was reversed
		// after a refund was approved. Reporting a negative capacity would let a
		// caller add to it and conclude there was room.
		return 0, nil
	}
	return remaining, nil
}

// Committed returns the total of an order's refunds that still commit money.
func Committed(app core.App, orderID, excludeRefundID string) (int64, error) {
	records, err := app.FindRecordsByFilter(CollectionRefunds,
		"order = {:o}", "", 0, 0, map[string]any{"o": orderID})
	if err != nil {
		return 0, err
	}

	var total int64
	for _, rec := range records {
		if rec.Id == excludeRefundID {
			continue
		}
		if !Commits(rec.GetString("status")) {
			continue
		}
		total += int64(rec.GetFloat("amount"))
	}
	return total, nil
}

// ItemQuantity is one line of a partial refund by item.
type ItemQuantity struct {
	OrderItemID string `json:"orderItemId"`
	Quantity    int    `json:"quantity"`
}

// ComputeAmount works out what a refund is worth, and what it leaves behind.
//
// For a refund by item the amount is derived from the order's own snapshot prices
// rather than from the catalogue, because the catalogue may have changed since. The
// line's share of any order-level discount is subtracted in proportion, so refunding
// every item of a discounted order does not refund more than was paid for it.
func ComputeAmount(app core.App, order *core.Record, mode string, amount int64, items []ItemQuantity) (int64, error) {
	if order == nil {
		return 0, fmt.Errorf("refund: no order")
	}

	switch mode {
	case ModeFull:
		// What is left, not what was paid: an order that has already been partly
		// refunded cannot be refunded in full a second time.
		return Refundable(app, order.Id, "")

	case ModePartialAmount:
		if amount <= 0 {
			return 0, fmt.Errorf("refund: a refund must be for a positive amount")
		}
		return amount, nil

	case ModePartialItems:
		if len(items) == 0 {
			return 0, fmt.Errorf("refund: a refund by item needs at least one item")
		}
		return amountForItems(app, order, items)

	default:
		return 0, fmt.Errorf("refund: %q is not a refund mode", mode)
	}
}

// amountForItems totals the chosen quantities at the prices the order recorded.
func amountForItems(app core.App, order *core.Record, items []ItemQuantity) (int64, error) {
	orderItems, err := app.FindRecordsByFilter("order_items",
		"order = {:o}", "", 0, 0, map[string]any{"o": order.Id})
	if err != nil {
		return 0, err
	}

	byID := make(map[string]*core.Record, len(orderItems))
	var linesTotal int64
	for _, item := range orderItems {
		byID[item.Id] = item
		linesTotal += int64(item.GetFloat("subtotal"))
	}

	var chosen int64
	for _, want := range items {
		item, ok := byID[want.OrderItemID]
		if !ok {
			return 0, fmt.Errorf("refund: that item is not on this order")
		}
		if want.Quantity <= 0 {
			return 0, fmt.Errorf("refund: a quantity must be positive")
		}

		already := item.GetInt("refundedQuantity")
		if already+want.Quantity > item.GetInt("quantity") {
			return 0, fmt.Errorf("refund: %q has %d of %d already refunded",
				item.GetString("productName"), already, item.GetInt("quantity"))
		}

		chosen += int64(item.GetFloat("unitPrice")) * int64(want.Quantity)
	}

	if linesTotal <= 0 {
		return chosen, nil
	}

	// The line's share of the order-level discount, in proportion. Without this a
	// discounted order refunded item by item gives back more than was paid for it.
	orderSubtotal := int64(order.GetFloat("subtotal"))
	discount := int64(order.GetFloat("discountTotal"))
	if orderSubtotal > 0 && discount > 0 {
		share := discount * chosen / orderSubtotal
		chosen -= share
		if chosen < 0 {
			chosen = 0
		}
	}

	return chosen, nil
}

// Request opens a refund request.
func Request(app core.App, req RequestInput) (*core.Record, error) {
	if req.OrderID == "" {
		return nil, fmt.Errorf("refund: a refund needs an order")
	}
	if req.RequestedBy == "" {
		return nil, fmt.Errorf("refund: a refund needs a requester")
	}
	if req.Reason == "" {
		return nil, fmt.Errorf("refund: a refund needs a reason")
	}

	order, err := app.FindRecordById("orders", req.OrderID)
	if err != nil || order == nil {
		return nil, fmt.Errorf("refund: no such order")
	}

	amount, err := ComputeAmount(app, order, req.Mode, req.Amount, req.Items)
	if err != nil {
		return nil, err
	}
	if amount <= 0 {
		return nil, fmt.Errorf("refund: there is nothing left to refund on that order")
	}

	// The cap is checked here as well as at approval. Refusing early is kinder than
	// accepting a request that cannot be approved, and the approval check is what
	// actually holds -- this one runs outside a transaction and can be raced.
	if remaining, err := Refundable(app, order.Id, ""); err != nil {
		return nil, err
	} else if amount > remaining {
		return nil, fmt.Errorf("refund: %d is more than the %d left to refund on that order",
			amount, remaining)
	}

	col, err := app.FindCollectionByNameOrId(CollectionRefunds)
	if err != nil || col == nil {
		return nil, fmt.Errorf("refund: the refunds collection is missing")
	}

	rec := core.NewRecord(col)
	rec.Set("refundNo", req.RefundNo)
	rec.Set("order", order.Id)
	if req.TransactionID != "" {
		rec.Set("transaction", req.TransactionID)
	}
	rec.Set("mode", req.Mode)
	rec.Set("amount", amount)
	rec.Set("currency", req.Currency)
	rec.Set("status", StatusPending)
	rec.Set("reason", req.Reason)
	rec.Set("reasonDetail", req.ReasonDetail)
	rec.Set("requestedBy", req.RequestedBy)
	rec.Set("attempts", 0)
	rec.Set("restock", req.Restock)

	if len(req.Items) > 0 {
		rec.Set("items", req.Items)
	}
	if len(req.AttachmentIDs) > 0 {
		rec.Set("attachments", req.AttachmentIDs)
	}

	if err := app.Save(rec); err != nil {
		return nil, fmt.Errorf("refund: save: %w", err)
	}
	return rec, nil
}

// RequestInput is what a caller supplies to ask for a refund.
type RequestInput struct {
	RefundNo      string
	OrderID       string
	TransactionID string
	Mode          string
	Amount        int64
	Items         []ItemQuantity
	Currency      string
	Reason        string
	ReasonDetail  string
	RequestedBy   string
	AttachmentIDs []string
	Restock       bool
}

// decodeItems reads a stored refund item list.
//
// PocketBase returns a json field as types.JSONRaw, which is a named []byte and so
// does not match a []byte case in a type switch. The mistake is invisible where it is
// made, which is why it is written once.
func decodeItems(value any, out *[]ItemQuantity) {
	if value == nil || out == nil {
		return
	}

	var raw []byte
	switch v := value.(type) {
	case types.JSONRaw:
		raw = []byte(v)
	case []byte:
		raw = v
	case string:
		raw = []byte(v)
	default:
		return
	}

	if len(raw) == 0 {
		return
	}
	_ = json.Unmarshal(raw, out)
}
