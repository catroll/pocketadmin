package order_test

import (
	"testing"
	"time"

	"github.com/pocketbase/pocketbase/core"

	"pocketadmin/internal/catalog"
	"pocketadmin/internal/order"
	"pocketadmin/internal/testsupport"
)

// sellable creates a product with one variant holding stock.
func sellable(t *testing.T, app core.App, name string, stock int, price int) (*core.Record, *core.Record) {
	t.Helper()

	products, _ := app.FindCollectionByNameOrId("products")
	product := core.NewRecord(products)
	product.Set("kind", "physical")
	product.Set("name", name)
	product.Set("slug", "slug-"+name)
	product.Set("status", "on_sale")
	product.Set("stockMode", "variant")
	product.Set("currency", "USD")
	product.Set("listPrice", price)
	if err := app.Save(product); err != nil {
		t.Fatalf("save product: %v", err)
	}

	variants, _ := app.FindCollectionByNameOrId("product_variants")
	variant := core.NewRecord(variants)
	variant.Set("product", product.Id)
	variant.Set("label", "Default")
	variant.Set("sku", "SKU-"+name)
	variant.Set("status", "active")
	if err := app.Save(variant); err != nil {
		t.Fatalf("save variant: %v", err)
	}

	// The opening stock is written to the ledger rather than set on the variant. A
	// cached balance with no entry explaining it is exactly the discrepancy
	// ReconcileStock exists to find, and a fixture that creates one would make every
	// later reconciliation test meaningless.
	if stock > 0 {
		if err := catalog.ApplyStock(app, variant, catalog.Movement{
			Delta:  stock,
			Reason: catalog.ReasonPurchase,
			Note:   "opening stock",
		}); err != nil {
			t.Fatalf("opening stock: %v", err)
		}
	}

	return product, variant
}

// account creates a user.
func account(t *testing.T, app core.App, email string) *core.Record {
	t.Helper()

	col, _ := app.FindCollectionByNameOrId("users")
	rec := core.NewRecord(col)
	rec.Set("email", email)
	rec.Set("password", "change-me-please")
	rec.Set("passwordConfirm", "change-me-please")
	rec.Set("status", "active")
	if err := app.Save(rec); err != nil {
		t.Fatalf("save user: %v", err)
	}
	return rec
}

// stockOf reads a variant's cached balance.
func stockOf(t *testing.T, app core.App, variantID string) int {
	t.Helper()

	rec, err := app.FindRecordById("product_variants", variantID)
	if err != nil || rec == nil {
		t.Fatalf("read variant: %v", err)
	}
	return rec.GetInt("stock")
}

// TestCreatingAnOrderReservesStock.
func TestCreatingAnOrderReservesStock(t *testing.T) {
	app := testsupport.NewApp(t)
	now := time.Now()

	product, variant := sellable(t, app, "widget", 10, 1999)
	user := account(t, app, "buyer@example.com")

	rec, err := order.Create(app, order.CreateRequest{
		UserID: user.Id,
		Lines: []order.Line{{
			ProductID: product.Id, VariantID: variant.Id,
			ProductName: "Widget", SKU: "SKU-widget", UnitPrice: 1999, Quantity: 3, Currency: "USD",
		}},
		Pricing: order.Input{Currency: "USD"},
		Now:     now,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	if got := stockOf(t, app, variant.Id); got != 7 {
		t.Errorf("stock is %d after ordering three of ten, want 7", got)
	}
	if rec.GetString("status") != order.StatusPendingPayment {
		t.Errorf("the status is %q", rec.GetString("status"))
	}
	if rec.GetInt("subtotal") != 5997 {
		t.Errorf("the subtotal is %d, want 5997", rec.GetInt("subtotal"))
	}
}

// TestAFailedReservationLeavesNoOrderBehind is a regression test for a real defect.
//
// The stock reservation runs after the order is written, because a ledger entry names
// the order that took the stock. When it failed, the order stayed: an order that
// exists but holds nothing, which somebody can pay for and nobody can fulfil. It
// surfaced during verification as an order count one higher than the orders that had
// actually been placed.
func TestAFailedReservationLeavesNoOrderBehind(t *testing.T) {
	app := testsupport.NewApp(t)
	now := time.Now()

	product, variant := sellable(t, app, "scarce", 2, 1000)
	user := account(t, app, "greedy@example.com")

	lines := []order.Line{{
		ProductID: product.Id, VariantID: variant.Id,
		ProductName: "Scarce", SKU: "SKU-scarce", UnitPrice: 1000, Quantity: 999, Currency: "USD",
	}}

	_, err := order.Create(app, order.CreateRequest{
		UserID:  user.Id,
		Lines:   lines,
		Pricing: order.Input{Currency: "USD"},
		Now:     now,
	})
	if err == nil {
		t.Fatal("an order for more stock than exists was accepted")
	}

	// No order, no items, and the stock is where it started.
	count, err := app.CountRecords(order.CollectionOrders)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 0 {
		t.Errorf("%d order(s) survived a failed reservation, want 0", count)
	}

	items, _ := app.CountRecords(order.CollectionItems)
	if items != 0 {
		t.Errorf("%d item(s) survived, want 0", items)
	}

	if got := stockOf(t, app, variant.Id); got != 2 {
		t.Errorf("stock is %d after a failed order, want the original 2", got)
	}
}

// TestTheLedgerRecordsTheReservation: the ledger is the record of what happened, and
// the cached balance is only a cache of it.
func TestTheLedgerRecordsTheReservation(t *testing.T) {
	app := testsupport.NewApp(t)
	now := time.Now()

	product, variant := sellable(t, app, "ledger", 5, 1000)
	user := account(t, app, "ledger@example.com")

	lines := []order.Line{{
		ProductID: product.Id, VariantID: variant.Id,
		ProductName: "Ledger", UnitPrice: 1000, Quantity: 2, Currency: "USD",
	}}

	if _, err := order.Create(app, order.CreateRequest{
		UserID: user.Id, Lines: lines,
		Pricing: order.Input{Currency: "USD"}, Now: now,
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	entries, err := app.FindRecordsByFilter("stock_ledger",
		"variant = {:v} && reason = {:r}", "", 0, 0,
		map[string]any{"v": variant.Id, "r": catalog.ReasonReserve})
	if err != nil {
		t.Fatalf("read ledger: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("%d ledger entries, want 1", len(entries))
	}
	if entries[0].GetInt("delta") != -2 {
		t.Errorf("the ledger records %d, want -2", entries[0].GetInt("delta"))
	}
	// The opening stock is on the ledger too, which is why this filters by reason.
	if entries[0].GetString("refType") != "order" {
		t.Errorf("the ledger names %q, want an order", entries[0].GetString("refType"))
	}

	// The catalogue can reconcile the cache against the ledger. It returns the
	// balance the ledger arrives at, so a cache that had drifted is repaired and the
	// figure the reconciliation agrees on is this one.
	balance, err := catalog.ReconcileStock(app, variant.Id)
	if err != nil {
		t.Fatalf("ReconcileStock: %v", err)
	}
	if balance != 3 {
		t.Errorf("the ledger balances at %d, want 3 (five received, two reserved)", balance)
	}
	if got := stockOf(t, app, variant.Id); got != balance {
		t.Errorf("the cache says %d and the ledger says %d", got, balance)
	}
}

// TestCancellingReturnsTheStock.
func TestCancellingReturnsTheStock(t *testing.T) {
	app := testsupport.NewApp(t)
	now := time.Now()

	product, variant := sellable(t, app, "cancel", 5, 1000)
	user := account(t, app, "cancel@example.com")

	lines := []order.Line{{
		ProductID: product.Id, VariantID: variant.Id,
		ProductName: "Cancel", UnitPrice: 1000, Quantity: 2, Currency: "USD",
	}}

	rec, err := order.Create(app, order.CreateRequest{
		UserID: user.Id, Lines: lines,
		Pricing: order.Input{Currency: "USD"}, Now: now,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if got := stockOf(t, app, variant.Id); got != 3 {
		t.Fatalf("stock is %d after ordering two, want 3", got)
	}

	if err := order.CancelOrClose(app, rec, false, "", "changed their mind", now); err != nil {
		t.Fatalf("CancelOrClose: %v", err)
	}

	if got := stockOf(t, app, variant.Id); got != 5 {
		t.Errorf("stock is %d after cancelling, want the original 5", got)
	}

	// And the return is on the timeline as well as in the ledger.
	events, _ := app.FindRecordsByFilter(order.CollectionEvents,
		"order = {:o} && event = 'stock_released'", "", 0, 0, map[string]any{"o": rec.Id})
	if len(events) != 1 {
		t.Errorf("%d release events on the timeline, want 1", len(events))
	}
}

// TestEndingAnOrderRequiresAReason.
func TestEndingAnOrderRequiresAReason(t *testing.T) {
	app := testsupport.NewApp(t)
	now := time.Now()

	product, variant := sellable(t, app, "reason", 5, 1000)
	user := account(t, app, "reason@example.com")

	lines := []order.Line{{
		ProductID: product.Id, VariantID: variant.Id,
		ProductName: "Reason", UnitPrice: 1000, Quantity: 1, Currency: "USD",
	}}

	rec, _ := order.Create(app, order.CreateRequest{
		UserID: user.Id, Lines: lines,
		Pricing: order.Input{Currency: "USD"}, Now: now,
	})

	if err := order.CancelOrClose(app, rec, false, "", "", now); err == nil {
		t.Error("an order was ended without a reason")
	}
	if got := stockOf(t, app, variant.Id); got != 4 {
		t.Errorf("stock is %d after a refused cancellation, want 4", got)
	}
}

// TestAPartPaymentDoesNotClaimTheOrderChanged: a part payment is recorded on the
// amounts without a status transition, so no timeline entry claims otherwise.
func TestAPartPaymentDoesNotClaimTheOrderChanged(t *testing.T) {
	app := testsupport.NewApp(t)
	now := time.Now()

	product, variant := sellable(t, app, "partial", 5, 1000)
	user := account(t, app, "partial@example.com")

	lines := []order.Line{{
		ProductID: product.Id, VariantID: variant.Id,
		ProductName: "Partial", UnitPrice: 1000, Quantity: 2, Currency: "USD",
	}}

	rec, _ := order.Create(app, order.CreateRequest{
		UserID: user.Id, Lines: lines,
		Pricing: order.Input{Currency: "USD"}, Now: now,
	})

	if err := order.ApplyPayment(app, rec, 500, "", "part of it", now); err != nil {
		t.Fatalf("ApplyPayment: %v", err)
	}

	if rec.GetString("status") != order.StatusPendingPayment {
		t.Errorf("a part payment moved the order to %q", rec.GetString("status"))
	}
	if rec.GetString("paymentStatus") != order.PaymentPartial {
		t.Errorf("the payment status is %q, want partial", rec.GetString("paymentStatus"))
	}
	if rec.GetInt("amountPaid") != 500 {
		t.Errorf("the amount paid is %d", rec.GetInt("amountPaid"))
	}

	// The rest settles it, and that is a transition.
	if err := order.ApplyPayment(app, rec, 1500, "", "the rest", now); err != nil {
		t.Fatalf("ApplyPayment: %v", err)
	}
	if rec.GetString("status") != order.StatusPaid {
		t.Errorf("a full payment left the order at %q", rec.GetString("status"))
	}
	if rec.GetString("paymentStatus") != order.PaymentPaid {
		t.Errorf("the payment status is %q", rec.GetString("paymentStatus"))
	}
}

// TestTheOrderNumberIsUnique.
func TestTheOrderNumberIsUnique(t *testing.T) {
	app := testsupport.NewApp(t)
	now := time.Now()

	product, variant := sellable(t, app, "numbered", 100, 1000)
	user := account(t, app, "numbered@example.com")

	seen := map[string]bool{}
	for i := 0; i < 5; i++ {
		lines := []order.Line{{
			ProductID: product.Id, VariantID: variant.Id,
			ProductName: "Numbered", UnitPrice: 1000, Quantity: 1, Currency: "USD",
		}}

		rec, err := order.Create(app, order.CreateRequest{
			UserID: user.Id, Lines: lines,
			Pricing: order.Input{Currency: "USD"}, Now: now,
		})
		if err != nil {
			t.Fatalf("Create %d: %v", i, err)
		}

		number := rec.GetString("orderNo")
		if number == "" {
			t.Fatal("an order was created with no number")
		}
		if seen[number] {
			t.Errorf("the order number %q was reused", number)
		}
		seen[number] = true
	}
}
