// Package catalog holds the product catalogue: categories, products, their
// attributes and variants, and the stock ledger.
//
// Two rules run through all of it. Prices are integer minor units with an explicit
// currency and never a decimal (internal/money). Stock is a cache of an
// append-only ledger, and no code path changes a stock number without writing a
// ledger entry that says who did it and why.
package catalog

import (
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/pocketbase/pocketbase/core"
)

// Collection names.
const (
	CollectionCategories = "product_categories"
	CollectionProducts   = "products"
	CollectionAttributes = "product_attributes"
	CollectionVariants   = "product_variants"
	CollectionStock      = "stock_ledger"
)

// Product kinds.
const (
	KindPhysical         = "physical"
	KindDigital          = "digital"
	KindMembershipPlan   = "membership_plan"
	KindSubscriptionPlan = "subscription_plan"
)

// Stock modes.
const (
	StockVariant   = "variant"
	StockSingle    = "single"
	StockUnlimited = "unlimited"
)

// Stock movement reasons.
const (
	ReasonPurchase   = "purchase"
	ReasonSale       = "sale"
	ReasonReserve    = "reserve"
	ReasonRelease    = "release"
	ReasonReturn     = "return"
	ReasonAdjustment = "adjustment"
	ReasonDamage     = "damage"
	ReasonStocktake  = "stocktake"
	ReasonImport     = "import"
)

// Tag vocabulary, matched by the migration's stored select.
func Tags() []string {
	return []string{"new", "featured", "bestseller", "limited", "clearance", "seasonal", "gift", "bundle"}
}

// ---------------------------------------------------------------------------
// Slugs
// ---------------------------------------------------------------------------

// Slugify turns a name into a URL-safe slug.
//
// It is used to prefill the field, not to overrule the operator: a slug is a public
// address, changing one breaks every link to it, and an editor who has deliberately
// set one should not have it rewritten because they fixed a typo in the name.
func Slugify(name string) string {
	var out strings.Builder
	lastDash := true

	for _, r := range strings.ToLower(strings.TrimSpace(name)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			out.WriteRune(r)
			lastDash = false
		case unicode.IsLetter(r):
			// A letter outside ASCII, which is most of the world. It is kept: a
			// slug that dropped it would turn a Chinese product name into an empty
			// string, and browsers and search engines handle it.
			out.WriteRune(r)
			lastDash = false
		default:
			if !lastDash {
				out.WriteByte('-')
				lastDash = true
			}
		}
	}

	return strings.Trim(out.String(), "-")
}

// ---------------------------------------------------------------------------
// Variants
// ---------------------------------------------------------------------------

// Combination is one variant's attribute values.
type Combination map[string]string

// Label renders a combination for display, in attribute order.
func Label(order []string, combination Combination) string {
	parts := make([]string, 0, len(order))
	for _, name := range order {
		if value := combination[name]; value != "" {
			parts = append(parts, value)
		}
	}

	// A combination with no attributes is still a variant, and it needs a label
	// that reads as one rather than as an empty cell.
	if len(parts) == 0 {
		return "Default"
	}
	return strings.Join(parts, " / ")
}

// Matrix expands attributes into every combination.
//
// The order of `order` decides the order of the result, so the same attributes
// always produce the same sequence. Without that, regenerating a matrix would
// reshuffle the variants and every SKU would look new.
//
// The size is bounded. Twelve values across four attributes is twenty thousand
// combinations, and a form that generated them would hang the browser and fill the
// table with rows nobody asked for.
func Matrix(order []string, values map[string][]string) ([]Combination, error) {
	if len(order) == 0 {
		return nil, nil
	}

	const maxCombinations = 2000

	total := 1
	for _, name := range order {
		count := len(values[name])
		if count == 0 {
			// An attribute with no values cannot contribute to a combination.
			// Treating it as one empty value would generate variants that name an
			// attribute with nothing in it.
			return nil, fmt.Errorf("catalog: the attribute %q has no values", name)
		}
		total *= count
		if total > maxCombinations {
			return nil, fmt.Errorf(
				"catalog: those attributes would produce more than %d combinations", maxCombinations)
		}
	}

	out := make([]Combination, 0, total)

	var walk func(depth int, current Combination)
	walk = func(depth int, current Combination) {
		if depth == len(order) {
			snapshot := make(Combination, len(current))
			for k, v := range current {
				snapshot[k] = v
			}
			out = append(out, snapshot)
			return
		}

		name := order[depth]
		for _, value := range values[name] {
			current[name] = value
			walk(depth+1, current)
		}
	}
	walk(0, make(Combination, len(order)))

	return out, nil
}

// Matches reports whether two combinations are the same.
func Matches(a, b Combination) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

// ---------------------------------------------------------------------------
// Prices
// ---------------------------------------------------------------------------

// Price is an effective price after the variant falls back to its product.
type Price struct {
	List     int64
	Sale     int64
	Cost     int64
	Currency string
}

// Effective merges a variant's prices over its product's.
//
// A zero on the variant means "inherit", not "free". A variant that genuinely costs
// nothing is expressed by a sale price of zero on the product, and treating an
// unset variant price as free is how a shop gives away its stock by accident.
func Effective(product, variant *core.Record) Price {
	price := Price{
		List:     int64(product.GetFloat("listPrice")),
		Sale:     int64(product.GetFloat("salePrice")),
		Cost:     int64(product.GetFloat("costPrice")),
		Currency: product.GetString("currency"),
	}

	if variant == nil {
		return price
	}

	if v := int64(variant.GetFloat("listPrice")); v != 0 {
		price.List = v
	}
	if v := int64(variant.GetFloat("salePrice")); v != 0 {
		price.Sale = v
	}
	if v := int64(variant.GetFloat("costPrice")); v != 0 {
		price.Cost = v
	}
	if c := variant.GetString("currency"); c != "" {
		price.Currency = c
	}

	return price
}

// Charged returns what a customer actually pays.
func (p Price) Charged() int64 {
	if p.Sale > 0 && p.Sale <= p.List {
		return p.Sale
	}
	return p.List
}

// OnSale reports whether the sale price is doing anything.
func (p Price) OnSale() bool {
	return p.Sale > 0 && p.Sale < p.List
}

// ---------------------------------------------------------------------------
// Stock ledger
// ---------------------------------------------------------------------------

// Movement is one stock change.
type Movement struct {
	Delta    int
	Reason   string
	RefType  string
	RefID    string
	Operator string
	Note     string
}

// ApplyStock writes a ledger entry and updates the variant's cached balance.
//
// The ledger is written first and the cache second, both in the caller's
// transaction. That order matters: a crash between them leaves the ledger correct
// and the cache stale, which `ReconcileStock` can repair. The other order would
// leave a stock number with no entry explaining it, which nothing can repair.
//
// A negative resulting balance is refused. Stock that has gone below zero is not a
// number to be recorded; it is a sign that something else is wrong -- an order was
// placed against stock that was never received -- and accepting it means the
// discrepancy surfaces later, in a customer's inbox rather than on this screen.
func ApplyStock(app core.App, variant *core.Record, movement Movement) error {
	if variant == nil {
		return fmt.Errorf("catalog: no variant")
	}
	if movement.Delta == 0 {
		return nil
	}

	current := variant.GetInt("stock")
	balance := current + movement.Delta

	if balance < 0 {
		return fmt.Errorf("catalog: %s has %d in stock and the change is %+d, which would leave %d",
			variant.GetString("sku"), current, movement.Delta, balance)
	}

	col, err := app.FindCollectionByNameOrId(CollectionStock)
	if err != nil || col == nil {
		return fmt.Errorf("catalog: the stock ledger is missing")
	}

	entry := core.NewRecord(col)
	entry.Set("variant", variant.Id)
	entry.Set("delta", movement.Delta)
	entry.Set("balanceAfter", balance)
	entry.Set("reason", movement.Reason)
	entry.Set("refType", movement.RefType)
	entry.Set("refId", movement.RefID)
	entry.Set("note", movement.Note)
	if movement.Operator != "" {
		entry.Set("operator", movement.Operator)
	}
	entry.Set("created", time.Now().UTC())

	if err := app.Save(entry); err != nil {
		return fmt.Errorf("catalog: write the ledger entry: %w", err)
	}

	variant.Set("stock", balance)
	if err := app.Save(variant); err != nil {
		return fmt.Errorf("catalog: update the balance: %w", err)
	}

	return nil
}

// ReconcileStock recomputes a variant's cached balance from its ledger.
//
// It exists because the balance is a cache: a crash between the two writes, or an
// import that wrote one and not the other, leaves a number that disagrees with its
// own history. Recomputing is always possible because the ledger is append-only,
// and that is the property that makes the cache safe to keep.
func ReconcileStock(app core.App, variantID string) (int, error) {
	entries, err := app.FindRecordsByFilter(CollectionStock,
		"variant = {:v}", "created", 0, 0, map[string]any{"v": variantID})
	if err != nil {
		return 0, err
	}

	balance := 0
	for _, entry := range entries {
		balance += entry.GetInt("delta")
	}

	variant, err := app.FindRecordById(CollectionVariants, variantID)
	if err != nil || variant == nil {
		return balance, fmt.Errorf("catalog: no such variant")
	}

	if variant.GetInt("stock") == balance {
		return balance, nil
	}

	variant.Set("stock", balance)
	if err := app.Save(variant); err != nil {
		return balance, err
	}
	return balance, nil
}

// Reserved returns how much of a variant's stock is held by unpaid orders.
func Reserved(variant *core.Record) int {
	if variant == nil {
		return 0
	}
	return variant.GetInt("lockedStock")
}

// Available returns what can actually be sold.
//
// It is stock minus what unpaid orders are holding. Selling against reserved stock
// means the second customer's order cannot be fulfilled, and they find out after
// paying.
func Available(variant *core.Record) int {
	if variant == nil {
		return 0
	}
	return variant.GetInt("stock") - variant.GetInt("lockedStock")
}

// ---------------------------------------------------------------------------
// Deletion
// ---------------------------------------------------------------------------

// ReferencedByOrder reports whether anything has bought a product.
//
// A product that has been ordered is never deleted, only taken off sale. Removing
// it would leave the order line pointing at nothing, and an order that cannot say
// what was bought is not a record of a sale.
func ReferencedByOrder(app core.App, productID string) (bool, error) {
	// The order collections arrive in a later wave. Their absence is not an error:
	// before they exist, nothing can have been ordered.
	for _, collection := range []string{"order_items", "subscriptions"} {
		col, err := app.FindCollectionByNameOrId(collection)
		if err != nil || col == nil {
			continue
		}

		found, err := app.FindFirstRecordByFilter(collection,
			"product = {:p}", map[string]any{"p": productID})
		if err == nil && found != nil {
			return true, nil
		}
	}
	return false, nil
}
