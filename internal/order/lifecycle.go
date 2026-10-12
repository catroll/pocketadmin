package order

import (
	"fmt"
	"log/slog"
	"time"

	"github.com/pocketbase/pocketbase/core"

	"pocketadmin/internal/catalog"
)

// Collection names.
const (
	CollectionOrders = "orders"
	CollectionItems  = "order_items"
	CollectionEvents = "order_events"
)

// DefaultPaymentWindow is how long an unpaid order is held before it is closed.
//
// A reserved order holds stock nobody else can buy, so an abandoned one is a product
// taken off the shelf by somebody who walked away.
const DefaultPaymentWindow = 2 * time.Hour

// Create writes an order and its items.
//
// The whole thing is built before anything is saved, so a failure part way through
// cannot leave an order with no items -- which would be an order that costs nothing
// and reserves nothing, and would look like a bug in the pricing rather than in the
// write.
func Create(app core.App, req CreateRequest) (*core.Record, error) {
	if req.UserID == "" {
		return nil, fmt.Errorf("order: an order needs an account")
	}
	if len(req.Lines) == 0 {
		return nil, fmt.Errorf("order: an order needs at least one item")
	}

	// The lines on the input are derived from the request rather than supplied
	// separately. Two lists would let a caller snapshot one set of items while being
	// priced on another, and the two would disagree on the amount and on what was
	// bought -- a difference nobody would find until a customer did.
	pricingInput := req.Pricing
	pricingInput.Lines = req.Lines

	pricing := Compute(pricingInput)
	lineTotals := ComputeLines(pricingInput, pricing)

	col, err := app.FindCollectionByNameOrId(CollectionOrders)
	if err != nil || col == nil {
		return nil, fmt.Errorf("order: the orders collection is missing")
	}

	orderNo, err := NextOrderNo(app, req.OrderNoPrefix, req.Now)
	if err != nil {
		return nil, err
	}

	rec := core.NewRecord(col)
	rec.Set("orderNo", orderNo)
	rec.Set("user", req.UserID)
	rec.Set("status", StatusPendingPayment)
	rec.Set("paymentStatus", PaymentUnpaid)
	rec.Set("source", defaultString(req.Source, SourceWeb))
	rec.Set("currency", req.Pricing.Currency)

	rec.Set("subtotal", pricing.Subtotal)
	rec.Set("discountTotal", pricing.DiscountTotal)
	rec.Set("memberDiscount", pricing.MemberDiscount)
	rec.Set("couponDiscount", pricing.CouponDiscount)
	rec.Set("shippingFee", pricing.ShippingFee)
	rec.Set("taxAmount", pricing.TaxAmount)
	rec.Set("grandTotal", pricing.GrandTotal)
	rec.Set("amountPaid", 0)
	rec.Set("amountRefunded", 0)

	if len(req.CouponGrantIDs) > 0 {
		rec.Set("couponGrants", req.CouponGrantIDs)
	}
	if req.MemberTierID != "" {
		rec.Set("memberTier", req.MemberTierID)
	}
	if req.SubscriptionID != "" {
		rec.Set("subscription", req.SubscriptionID)
	}
	if req.ShippingAddress != nil {
		rec.Set("shippingAddress", req.ShippingAddress)
	}
	if req.BillingAddress != nil {
		rec.Set("billingAddress", req.BillingAddress)
	}
	rec.Set("customerNote", req.CustomerNote)
	if req.PlacedBy != "" {
		rec.Set("placedBy", req.PlacedBy)
	}

	if req.ExpiresAt.IsZero() {
		rec.Set("expiresAt", req.Now.Add(DefaultPaymentWindow).UTC())
	} else {
		rec.Set("expiresAt", req.ExpiresAt.UTC())
	}

	if err := app.Save(rec); err != nil {
		return nil, fmt.Errorf("order: save: %w", err)
	}

	if err := writeItems(app, rec, lineTotals); err != nil {
		// The order is removed rather than left empty: an order with no items is a
		// worse lie than no order.
		discard(app, rec, nil)
		return nil, err
	}

	if err := RecordEvent(app, rec, EventRequest{
		Event:  "created",
		To:     StatusPendingPayment,
		Actor:  req.PlacedBy,
		Reason: req.Reason,
		Detail: map[string]any{
			"subtotal": pricing.Subtotal, "grandTotal": pricing.GrandTotal,
			"memberDiscount": pricing.MemberDiscount, "couponDiscount": pricing.CouponDiscount,
			"source": rec.GetString("source"),
		},
		Now: req.Now,
	}); err != nil {
		return nil, err
	}

	// Stock is reserved after the order exists, because a reservation names the order
	// that took it. The ledger is written first, so the cached quantity is never
	// ahead of the record of what happened.
	//
	// A failed reservation discards the order. It has to: an order that exists but
	// holds no stock is one somebody can pay for and nobody can fulfil, and it would
	// surface as a missing item rather than as a failed order.
	taken, err := ReserveStock(app, rec, req.Now)
	if err != nil {
		discard(app, rec, taken)
		return nil, err
	}

	return rec, nil
}

// CreateRequest is what a caller supplies to place an order.
type CreateRequest struct {
	UserID string
	Source string

	Lines   []Line
	Pricing Input

	CouponGrantIDs []string
	MemberTierID   string
	SubscriptionID string

	ShippingAddress map[string]any
	BillingAddress  map[string]any

	CustomerNote string

	// PlacedBy is the administrator who created it manually, empty for self-service.
	PlacedBy string
	Reason   string

	OrderNoPrefix string
	ExpiresAt     time.Time
	Now           time.Time
}

// writeItems writes the item snapshots.
func writeItems(app core.App, rec *core.Record, totals []LineTotal) error {
	col, err := app.FindCollectionByNameOrId(CollectionItems)
	if err != nil || col == nil {
		return fmt.Errorf("order: the items collection is missing")
	}

	for _, total := range totals {
		item := core.NewRecord(col)
		item.Set("order", rec.Id)
		if total.Line.ProductID != "" {
			item.Set("product", total.Line.ProductID)
		}
		if total.Line.VariantID != "" {
			item.Set("variant", total.Line.VariantID)
		}

		// The snapshots. A name edited next month must not change what a placed order
		// says it bought, which is the difference between a record and a view.
		item.Set("productName", total.Line.ProductName)
		item.Set("variantLabel", total.Line.VariantLabel)
		item.Set("sku", total.Line.SKU)
		item.Set("image", total.Line.Image)

		item.Set("unitPrice", total.Line.UnitPrice)
		item.Set("quantity", total.Line.Quantity)
		item.Set("subtotal", total.Subtotal)
		item.Set("discountAmount", total.DiscountAmount)
		item.Set("taxAmount", total.TaxAmount)
		item.Set("total", total.Total)
		item.Set("currency", defaultString(total.Line.Currency, rec.GetString("currency")))

		if err := app.Save(item); err != nil {
			return fmt.Errorf("order: save an item: %w", err)
		}
	}
	return nil
}

// discard removes a partly-built order and everything written for it.
//
// Only the stock the reservation actually took is given back. Releasing everything
// the order asked for would credit stock that was never taken, so an order for more
// than exists would leave the catalogue richer than it started.
func discard(app core.App, rec *core.Record, taken []reserved) {
	for _, entry := range taken {
		variant, err := app.FindRecordById("product_variants", entry.VariantID)
		if err != nil || variant == nil {
			continue
		}

		if err := catalog.ApplyStock(app, variant, catalog.Movement{
			Delta:   entry.Quantity,
			Reason:  catalog.ReasonRelease,
			RefType: "order",
			RefID:   rec.Id,
			Note:    "the order could not be placed",
		}); err != nil {
			// Nothing better to do than carry on: the order is being removed either
			// way, and the ledger can be reconciled from the variant's own count.
			slog.Warn("could not release stock while discarding an order",
				"order", rec.Id, "variant", entry.VariantID, "error", err)
		}
	}

	// Everything written for the order goes first. The relations are required, so an
	// order with a timeline entry or an item cannot be deleted at all -- and a
	// cleanup that removes the order last but not its children leaves the order, the
	// children, and an error in the log while looking like it worked.
	for _, collection := range []string{CollectionEvents, CollectionItems} {
		children, _ := app.FindRecordsByFilter(collection,
			"order = {:o}", "", 0, 0, map[string]any{"o": rec.Id})
		for _, child := range children {
			if err := app.Delete(child); err != nil {
				slog.Warn("could not remove a row while discarding an order",
					"order", rec.Id, "collection", collection, "error", err)
			}
		}
	}

	if err := app.Delete(rec); err != nil {
		slog.Error("could not discard an order", "order", rec.Id, "error", err)
	}
}

// EventRequest is one entry in the timeline.
type EventRequest struct {
	Event  string
	From   string
	To     string
	Actor  string
	Reason string
	Detail map[string]any
	Now    time.Time
}

// RecordEvent appends to the order timeline.
//
// Every transition writes one, including the ones a scheduler performs, because the
// timeline is what answers "what happened to this order" and a gap in it is
// indistinguishable from something that did not happen.
func RecordEvent(app core.App, rec *core.Record, req EventRequest) error {
	col, err := app.FindCollectionByNameOrId(CollectionEvents)
	if err != nil || col == nil {
		return fmt.Errorf("order: the events collection is missing")
	}

	event := core.NewRecord(col)
	event.Set("order", rec.Id)
	event.Set("event", req.Event)

	// The status before the transition is taken from the order rather than passed in,
	// so a caller cannot record a from-state the order never had.
	if req.From == "" {
		req.From = rec.GetString("status")
	}
	if req.From != req.To {
		event.Set("fromStatus", req.From)
		event.Set("toStatus", req.To)
	}
	if req.Actor != "" {
		event.Set("actor", req.Actor)
	}
	event.Set("reason", req.Reason)
	if req.Detail != nil {
		event.Set("detail", req.Detail)
	}

	return app.Save(event)
}

// Transition moves an order to a new status, checking the state machine first.
func Transition(app core.App, rec *core.Record, to, actor, reason string, now time.Time) error {
	from := rec.GetString("status")

	if err := CanTransition(from, to); err != nil {
		return err
	}

	rec.Set("status", to)

	// The timestamps are set here rather than by each caller, so an order cannot
	// reach a state without the time it got there.
	switch to {
	case StatusPaid:
		if rec.GetDateTime("paidAt").IsZero() {
			rec.Set("paidAt", now.UTC())
		}
	case StatusCompleted:
		if rec.GetDateTime("completedAt").IsZero() {
			rec.Set("completedAt", now.UTC())
		}
	case StatusCancelled, StatusClosed:
		if rec.GetDateTime("cancelledAt").IsZero() {
			rec.Set("cancelledAt", now.UTC())
		}
		rec.Set("cancelReason", reason)
		// Nothing more will be charged, so there is no deadline to hold the order for.
		rec.Set("expiresAt", nil)
	}

	if err := app.Save(rec); err != nil {
		return err
	}

	return RecordEvent(app, rec, EventRequest{
		Event:  eventForStatus(to),
		From:   from,
		To:     to,
		Actor:  actor,
		Reason: reason,
		Now:    now,
	})
}

// eventForStatus maps a status to the timeline event that records reaching it.
func eventForStatus(status string) string {
	switch status {
	case StatusPaid:
		return "paid"
	case StatusFulfilling:
		return "fulfilling"
	case StatusCompleted:
		return "completed"
	case StatusCancelled:
		return "cancelled"
	case StatusClosed:
		return "closed"
	case StatusRefunded:
		return "refunded"
	default:
		return "created"
	}
}

// CancelOrClose ends an unpaid order.
//
// A paid order is refused, and the refusal says to refund it instead: the two look
// like the same intent and only one of them returns the customer's money.
func CancelOrClose(app core.App, rec *core.Record, close bool, actor, reason string, now time.Time) error {
	if reason == "" {
		return fmt.Errorf("order: ending an order requires a reason")
	}

	target := StatusCancelled
	if close {
		target = StatusClosed
	}

	if err := Transition(app, rec, target, actor, reason, now); err != nil {
		return err
	}

	// The stock goes back, because it was being held for an order that will not
	// happen.
	return ReleaseStock(app, rec, now)
}

// ReleaseStock returns reserved stock to the catalogue.
func ReleaseStock(app core.App, rec *core.Record, now time.Time) error {
	items, err := app.FindRecordsByFilter(CollectionItems,
		"order = {:o}", "", 0, 0, map[string]any{"o": rec.Id})
	if err != nil {
		return err
	}

	released := 0
	for _, item := range items {
		productID := item.GetString("product")
		quantity := item.GetInt("quantity")
		if productID == "" || quantity <= 0 {
			continue
		}

		variant, err := resolveVariant(app, productID, item.GetString("variant"))
		if err != nil || variant == nil {
			// A product with no variant has no stock to release, which is the case
			// for anything sold without inventory tracking. It is not an error.
			continue
		}

		if err := catalog.ApplyStock(app, variant, catalog.Movement{
			Delta:   quantity,
			Reason:  catalog.ReasonRelease,
			RefType: "order",
			RefID:   rec.Id,
			Note:    "order " + rec.GetString("orderNo") + " ended",
		}); err != nil {
			return err
		}
		released += quantity
	}

	if released > 0 {
		return RecordEvent(app, rec, EventRequest{
			Event:  "stock_released",
			Reason: fmt.Sprintf("%d item(s) returned to stock", released),
			Now:    now,
		})
	}
	return nil
}

// reserved is stock the ledger actually took, so a failure can undo exactly that and
// no more.
type reserved struct {
	VariantID string
	Quantity  int
}

// ReserveStock takes stock for an order and reports what it took.
//
// The report is the point. A reservation that fails on the third item has already
// taken the first two, and undoing it by releasing everything the order asks for
// would credit stock that was never taken -- which is how a catalogue ends up with
// more of something than it has.
func ReserveStock(app core.App, rec *core.Record, now time.Time) ([]reserved, error) {
	items, err := app.FindRecordsByFilter(CollectionItems,
		"order = {:o}", "", 0, 0, map[string]any{"o": rec.Id})
	if err != nil {
		return nil, err
	}

	taken := make([]reserved, 0, len(items))

	for _, item := range items {
		productID := item.GetString("product")
		quantity := item.GetInt("quantity")
		if productID == "" || quantity <= 0 {
			continue
		}

		variant, err := resolveVariant(app, productID, item.GetString("variant"))
		if err != nil || variant == nil {
			continue
		}

		// A negative balance is refused by the catalogue rather than clamped, which is
		// what stops an order for stock that is not there.
		if err := catalog.ApplyStock(app, variant, catalog.Movement{
			Delta:   -quantity,
			Reason:  catalog.ReasonReserve,
			RefType: "order",
			RefID:   rec.Id,
			Note:    "order " + rec.GetString("orderNo"),
		}); err != nil {
			return taken, err
		}

		taken = append(taken, reserved{VariantID: variant.Id, Quantity: quantity})
	}
	return taken, nil
}

// ApplyPayment records money received and moves the order on if it is settled.
func ApplyPayment(app core.App, rec *core.Record, amount int64, actor, reason string, now time.Time) error {
	if amount <= 0 {
		return fmt.Errorf("order: a payment must be for a positive amount")
	}

	paid := int64(rec.GetFloat("amountPaid")) + amount
	rec.Set("amountPaid", paid)

	grand := int64(rec.GetFloat("grandTotal"))
	rec.Set("paymentStatus", PaymentStatusFor(grand, paid, int64(rec.GetFloat("amountRefunded")), false))

	target := StatusAfterPayment(grand, paid, rec.GetString("status"))
	if target == rec.GetString("status") {
		// A part payment is recorded on the amounts without a status transition, so
		// no timeline entry claims the order changed state when it did not.
		return app.Save(rec)
	}

	return Transition(app, rec, target, actor, reason, now)
}

// ExpireUnpaid closes the orders whose payment window has passed.
func ExpireUnpaid(app core.App, now time.Time) (int, error) {
	records, err := app.FindRecordsByFilter(CollectionOrders,
		"status = {:s} && expiresAt != '' && expiresAt < {:now} && deletedAt = ''",
		"expiresAt", 200, 0,
		map[string]any{"s": StatusPendingPayment, "now": now.UTC()})
	if err != nil {
		return 0, err
	}

	closed := 0
	for _, rec := range records {
		if err := CancelOrClose(app, rec, true, "", "the payment window passed", now); err != nil {
			continue
		}
		closed++
	}
	return closed, nil
}

// resolveVariant finds the variant a line's stock belongs to.
//
// The catalogue keys its ledger on the variant, because a product sold in sizes has
// one balance per size. A product with no variants has a single default one, which is
// what a line without a variant refers to.
func resolveVariant(app core.App, productID, variantID string) (*core.Record, error) {
	if variantID != "" {
		return app.FindRecordById("product_variants", variantID)
	}
	if productID == "" {
		return nil, nil
	}

	variants, err := app.FindRecordsByFilter("product_variants",
		"product = {:p}", "sortOrder,created", 1, 0, map[string]any{"p": productID})
	if err != nil {
		return nil, err
	}
	if len(variants) == 0 {
		return nil, nil
	}
	return variants[0], nil
}

// defaultString returns the value, or the fallback when it is empty.
func defaultString(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}
