package catalog_test

import (
	"testing"

	"github.com/pocketbase/pocketbase/core"

	"pocketadmin/internal/catalog"
	"pocketadmin/internal/testsupport"
)

// TestMatrixGeneratesEveryCombination: the size and the order both matter, because
// regenerating a matrix must not reshuffle the variants.
func TestMatrixGeneratesEveryCombination(t *testing.T) {
	order := []string{"Colour", "Size"}
	values := map[string][]string{
		"Colour": {"Red", "Blue"},
		"Size":   {"S", "M", "L"},
	}

	combinations, err := catalog.Matrix(order, values)
	if err != nil {
		t.Fatalf("Matrix: %v", err)
	}
	if len(combinations) != 6 {
		t.Fatalf("generated %d combinations, want 6", len(combinations))
	}

	// The first attribute varies slowest, so the sequence is stable.
	if combinations[0]["Colour"] != "Red" || combinations[0]["Size"] != "S" {
		t.Errorf("first combination is %v", combinations[0])
	}
	if combinations[5]["Colour"] != "Blue" || combinations[5]["Size"] != "L" {
		t.Errorf("last combination is %v", combinations[5])
	}

	// Running it again produces the same sequence.
	again, _ := catalog.Matrix(order, values)
	for i := range combinations {
		if !catalog.Matches(combinations[i], again[i]) {
			t.Errorf("combination %d differs between runs: %v and %v", i, combinations[i], again[i])
		}
	}

	// Each combination is distinct.
	seen := map[string]bool{}
	for _, c := range combinations {
		key := c["Colour"] + "/" + c["Size"]
		if seen[key] {
			t.Errorf("the combination %s was produced twice", key)
		}
		seen[key] = true
	}
}

// TestMatrixRefusesAnExplosion: a form that generated twenty thousand combinations
// would hang the browser and fill the table with rows nobody asked for.
func TestMatrixRefusesAnExplosion(t *testing.T) {
	order := []string{"A", "B", "C", "D", "E", "F"}
	values := map[string][]string{}
	for _, name := range order {
		values[name] = []string{"1", "2", "3", "4", "5", "6", "7"}
	}

	if _, err := catalog.Matrix(order, values); err == nil {
		t.Error("a matrix of over a hundred thousand combinations was generated")
	}
}

// TestMatrixRefusesAnAttributeWithNoValues: treating it as one empty value would
// generate variants naming an attribute with nothing in it.
func TestMatrixRefusesAnAttributeWithNoValues(t *testing.T) {
	if _, err := catalog.Matrix([]string{"Colour"}, map[string][]string{"Colour": {}}); err == nil {
		t.Error("an attribute with no values was accepted")
	}
}

// TestMatrixWithNoAttributes: a product with no attributes has no matrix, and that
// is not an error.
func TestMatrixWithNoAttributes(t *testing.T) {
	combinations, err := catalog.Matrix(nil, nil)
	if err != nil {
		t.Fatalf("Matrix: %v", err)
	}
	if len(combinations) != 0 {
		t.Errorf("generated %d combinations from no attributes", len(combinations))
	}
}

// TestLabelReadsAsACombination.
func TestLabelReadsAsACombination(t *testing.T) {
	order := []string{"Colour", "Size"}

	label := catalog.Label(order, catalog.Combination{"Colour": "Red", "Size": "M"})
	if label != "Red / M" {
		t.Errorf("Label = %q, want Red / M", label)
	}

	// A variant with no attributes still needs a label that reads as one.
	if got := catalog.Label(nil, nil); got != "Default" {
		t.Errorf("an empty combination is labelled %q", got)
	}

	// A missing value is skipped rather than rendered as an empty segment.
	if got := catalog.Label(order, catalog.Combination{"Colour": "Red"}); got != "Red" {
		t.Errorf("Label = %q, want Red", got)
	}
}

// TestSlugify: it prefills a field, and it must not empty a non-Latin name.
func TestSlugify(t *testing.T) {
	cases := map[string]string{
		"Wool Coat":            "wool-coat",
		"  Wool   Coat  ":      "wool-coat",
		"Wool & Coat":          "wool-coat",
		"Winter 2026 Edition":  "winter-2026-edition",
		"T-Shirt (Large)":      "t-shirt-large",
		"---already-dashed---": "already-dashed",
		"":                     "",
		"!!!":                  "",
	}

	for input, want := range cases {
		if got := catalog.Slugify(input); got != want {
			t.Errorf("Slugify(%q) = %q, want %q", input, got, want)
		}
	}

	// A name in a non-Latin script must not become an empty slug.
	if got := catalog.Slugify("羊毛大衣"); got == "" {
		t.Error("a Chinese product name produced an empty slug")
	}
}

// ---------------------------------------------------------------------------
// Prices
// ---------------------------------------------------------------------------

// product creates a product record for a price test.
func product(t *testing.T, app core.App, list, sale float64, currency string) *core.Record {
	t.Helper()

	col, _ := app.FindCollectionByNameOrId(catalog.CollectionProducts)
	rec := core.NewRecord(col)
	rec.Set("kind", catalog.KindPhysical)
	rec.Set("name", "Test product")
	rec.Set("slug", "test-product")
	rec.Set("status", "on_sale")
	rec.Set("stockMode", catalog.StockSingle)
	rec.Set("listPrice", list)
	rec.Set("salePrice", sale)
	rec.Set("costPrice", 500)
	rec.Set("currency", currency)
	if err := app.Save(rec); err != nil {
		t.Fatalf("save product: %v", err)
	}
	return rec
}

// TestEffectiveFallsBackToTheProduct: a zero on a variant means inherit, not free.
//
// Treating an unset variant price as free is how a shop gives away its stock by
// accident.
func TestEffectiveFallsBackToTheProduct(t *testing.T) {
	app := testsupport.NewApp(t)
	parent := product(t, app, 1999, 1499, "USD")

	// A variant with no prices of its own.
	variants, _ := app.FindCollectionByNameOrId(catalog.CollectionVariants)
	bare := core.NewRecord(variants)
	bare.Set("product", parent.Id)
	bare.Set("sku", "SKU-BARE")
	bare.Set("status", "active")
	if err := app.Save(bare); err != nil {
		t.Fatalf("save variant: %v", err)
	}

	price := catalog.Effective(parent, bare)
	if price.List != 1999 || price.Sale != 1499 || price.Currency != "USD" {
		t.Errorf("a variant with no prices inherited %+v", price)
	}

	// A variant that overrides the list price but not the sale price keeps the
	// product's sale price, which is the behaviour a shop expects from "this size
	// costs more" without also restating the discount.
	bare.Set("listPrice", 2499)
	if err := app.Save(bare); err != nil {
		t.Fatalf("save: %v", err)
	}
	price = catalog.Effective(parent, bare)
	if price.List != 2499 {
		t.Errorf("the variant list price did not apply: %+v", price)
	}
	if price.Sale != 1499 {
		t.Errorf("the product sale price was lost: %+v", price)
	}
}

// TestChargedPrefersTheSalePriceOnlyWhenItIsLower: a sale price above the list is a
// data error, and the customer is charged the list price rather than the error.
func TestChargedPrefersTheSalePriceOnlyWhenItIsLower(t *testing.T) {
	cases := []struct {
		list, sale, want int64
	}{
		{1999, 1499, 1499},
		{1999, 0, 1999},    // no sale
		{1999, 1999, 1999}, // a sale equal to the list is no discount
		{1999, 2999, 1999}, // a sale above the list is refused in favour of the list
	}

	for _, tc := range cases {
		price := catalog.Price{List: tc.list, Sale: tc.sale, Currency: "USD"}
		if got := price.Charged(); got != tc.want {
			t.Errorf("Charged(%d, %d) = %d, want %d", tc.list, tc.sale, got, tc.want)
		}
	}

	if !(catalog.Price{List: 1999, Sale: 1499}).OnSale() {
		t.Error("a genuine discount is not reported as a sale")
	}
	if (catalog.Price{List: 1999, Sale: 1999}).OnSale() {
		t.Error("a sale equal to the list price is reported as a discount")
	}
	if (catalog.Price{List: 1999}).OnSale() {
		t.Error("an absent sale price is reported as a discount")
	}
}

// ---------------------------------------------------------------------------
// Stock
// ---------------------------------------------------------------------------

// variant creates a variant with a starting stock.
func variant(t *testing.T, app core.App, parent *core.Record, sku string) *core.Record {
	t.Helper()

	col, _ := app.FindCollectionByNameOrId(catalog.CollectionVariants)
	rec := core.NewRecord(col)
	rec.Set("product", parent.Id)
	rec.Set("sku", sku)
	rec.Set("status", "active")
	if err := app.Save(rec); err != nil {
		t.Fatalf("save variant: %v", err)
	}
	return rec
}

// TestStockIsTheLedgerBalance: every movement writes an entry, and the cached
// number is the running total of them.
func TestStockIsTheLedgerBalance(t *testing.T) {
	app := testsupport.NewApp(t)
	parent := product(t, app, 1999, 0, "USD")
	sku := variant(t, app, parent, "SKU-STOCK")

	movements := []catalog.Movement{
		{Delta: 100, Reason: catalog.ReasonPurchase, RefType: "purchase_order", RefID: "PO-1"},
		{Delta: -30, Reason: catalog.ReasonSale, RefType: "order", RefID: "ORD-1"},
		{Delta: -5, Reason: catalog.ReasonDamage, Note: "water damage"},
		{Delta: 3, Reason: catalog.ReasonReturn, RefType: "order", RefID: "ORD-2"},
		{Delta: 2, Reason: catalog.ReasonStocktake, Note: "counted"},
	}

	for _, movement := range movements {
		if err := catalog.ApplyStock(app, sku, movement); err != nil {
			t.Fatalf("ApplyStock(%+v): %v", movement, err)
		}
	}

	if got := sku.GetInt("stock"); got != 70 {
		t.Errorf("the balance is %d, want 70", got)
	}

	entries, err := app.FindRecordsByFilter(catalog.CollectionStock,
		"variant = {:v}", "created", 0, 0, map[string]any{"v": sku.Id})
	if err != nil {
		t.Fatalf("read the ledger: %v", err)
	}
	if len(entries) != len(movements) {
		t.Fatalf("%d ledger entries for %d movements", len(entries), len(movements))
	}

	// The materialised running balance matches a fresh sum at every step.
	running := 0
	for i, entry := range entries {
		running += entry.GetInt("delta")
		if entry.GetInt("balanceAfter") != running {
			t.Errorf("entry %d records a balance of %d, but the movements total %d",
				i, entry.GetInt("balanceAfter"), running)
		}
	}
}

// TestStockRefusesToGoNegative: stock below zero is not a number to be recorded, it
// is a sign that something else is wrong.
func TestStockRefusesToGoNegative(t *testing.T) {
	app := testsupport.NewApp(t)
	parent := product(t, app, 1999, 0, "USD")
	sku := variant(t, app, parent, "SKU-NEG")

	if err := catalog.ApplyStock(app, sku, catalog.Movement{Delta: 10, Reason: catalog.ReasonPurchase}); err != nil {
		t.Fatalf("ApplyStock: %v", err)
	}

	err := catalog.ApplyStock(app, sku, catalog.Movement{Delta: -11, Reason: catalog.ReasonSale})
	if err == nil {
		t.Fatal("stock was allowed to go negative")
	}

	// The refusal changed nothing: no entry, no balance change.
	if got := sku.GetInt("stock"); got != 10 {
		t.Errorf("the balance moved to %d despite the refusal", got)
	}
	entries, _ := app.FindRecordsByFilter(catalog.CollectionStock,
		"variant = {:v}", "", 0, 0, map[string]any{"v": sku.Id})
	if len(entries) != 1 {
		t.Errorf("%d ledger entries after a refused movement, want 1", len(entries))
	}
}

// TestReconcileRepairsTheCache: the balance is a cache, and recomputing it is
// always possible because the ledger is append-only.
func TestReconcileRepairsTheCache(t *testing.T) {
	app := testsupport.NewApp(t)
	parent := product(t, app, 1999, 0, "USD")
	sku := variant(t, app, parent, "SKU-RECON")

	for i := 0; i < 3; i++ {
		if err := catalog.ApplyStock(app, sku, catalog.Movement{Delta: 10, Reason: catalog.ReasonPurchase}); err != nil {
			t.Fatalf("ApplyStock: %v", err)
		}
	}
	if sku.GetInt("stock") != 30 {
		t.Fatalf("the balance is %d, want 30", sku.GetInt("stock"))
	}

	// Corrupt the cache the way a crash between the two writes would.
	sku.Set("stock", 999)
	if err := app.Save(sku); err != nil {
		t.Fatalf("save: %v", err)
	}

	balance, err := catalog.ReconcileStock(app, sku.Id)
	if err != nil {
		t.Fatalf("ReconcileStock: %v", err)
	}
	if balance != 30 {
		t.Errorf("reconciled to %d, want 30", balance)
	}

	reloaded, _ := app.FindRecordById(catalog.CollectionVariants, sku.Id)
	if reloaded.GetInt("stock") != 30 {
		t.Errorf("the repaired balance is %d", reloaded.GetInt("stock"))
	}
}

// TestAvailableExcludesReserved: selling against reserved stock means the second
// customer's order cannot be fulfilled, and they find out after paying.
func TestAvailableExcludesReserved(t *testing.T) {
	app := testsupport.NewApp(t)
	parent := product(t, app, 1999, 0, "USD")
	sku := variant(t, app, parent, "SKU-AVAIL")

	if err := catalog.ApplyStock(app, sku, catalog.Movement{Delta: 50, Reason: catalog.ReasonPurchase}); err != nil {
		t.Fatalf("ApplyStock: %v", err)
	}

	if got := catalog.Available(sku); got != 50 {
		t.Errorf("available is %d, want 50", got)
	}

	sku.Set("lockedStock", 20)
	if err := app.Save(sku); err != nil {
		t.Fatalf("save: %v", err)
	}

	if got := catalog.Available(sku); got != 30 {
		t.Errorf("available is %d with 20 reserved, want 30", got)
	}
	if got := catalog.Reserved(sku); got != 20 {
		t.Errorf("reserved is %d, want 20", got)
	}
}

// TestReferencedByOrderIsFalseBeforeTheOrderCollectionsExist: their absence is not
// an error, because before they exist nothing can have been ordered.
func TestReferencedByOrderIsFalseBeforeTheOrderCollectionsExist(t *testing.T) {
	app := testsupport.NewApp(t)
	parent := product(t, app, 1999, 0, "USD")

	referenced, err := catalog.ReferencedByOrder(app, parent.Id)
	if err != nil {
		t.Fatalf("ReferencedByOrder: %v", err)
	}
	if referenced {
		t.Error("a product was reported as ordered before any order collection exists")
	}
}
