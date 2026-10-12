package order

import (
	"encoding/json"
	"fmt"
	"math"
	"time"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/types"

	"pocketadmin/internal/money"
)

// Line is one item to be priced.
type Line struct {
	ProductID  string
	VariantID  string
	CategoryID string

	// The snapshots. A price edited next month must not change what an order placed
	// today says it cost, so these are copied onto the order rather than looked up
	// when it is read.
	ProductName  string
	VariantLabel string
	SKU          string
	Image        string

	UnitPrice int64
	Quantity  int
	Currency  string
}

// Subtotal returns the line's undiscounted total.
func (l Line) Subtotal() int64 {
	if l.Quantity <= 0 {
		return 0
	}
	return l.UnitPrice * int64(l.Quantity)
}

// Coupon is a discount to apply.
type Coupon struct {
	// GrantID is the issued instance, recorded on the order so the arithmetic can be
	// explained later.
	GrantID string

	// Amount is what the coupon takes off, already computed by the promotion engine
	// against a cart. It is not recomputed here: two places that decide what a coupon
	// is worth are two places that can disagree.
	Amount int64

	// StacksWithMember says whether the membership discount survives alongside this
	// coupon.
	StacksWithMember bool
}

// Input is everything the pricing needs.
type Input struct {
	Lines    []Line
	Currency string

	// MemberRate is the fraction to charge after the membership discount: 0.9 means
	// ten percent off, and 1.0 means no discount.
	MemberRate float64

	Coupons []Coupon

	ShippingFee int64

	// TaxRate is a percentage.
	TaxRate float64

	// TaxInclusive says whether the listed prices already contain the tax. The two
	// are different arithmetic and the setting is the shop's, not the order's.
	TaxInclusive bool
}

// Pricing is the amount breakdown.
type Pricing struct {
	Subtotal       int64
	MemberDiscount int64
	CouponDiscount int64
	DiscountTotal  int64
	ShippingFee    int64
	TaxAmount      int64
	GrandTotal     int64
	Currency       string
}

// LineTotals is what each line contributes after discount.
type LineTotal struct {
	Line           Line
	Subtotal       int64
	DiscountAmount int64
	TaxAmount      int64
	Total          int64
}

// Compute builds the breakdown.
//
// Everything is integer minor units and every division rounds half away from zero,
// so the figures an operator sees add up to the amount charged. That property is the
// reason the member discount and the coupons are applied in a fixed order rather than
// computed independently from the subtotal: two independent percentages of the same
// number do not sum to a discount of the two together, and the difference has to go
// somewhere.
func Compute(in Input) Pricing {
	out := Pricing{Currency: in.Currency}

	for _, line := range in.Lines {
		out.Subtotal += line.Subtotal()
	}

	if out.Subtotal <= 0 {
		// Shipping is still charged on an empty order if somebody configured it, but
		// there is nothing to discount and no tax to compute on goods.
		out.ShippingFee = in.ShippingFee
		out.GrandTotal = in.ShippingFee
		return out
	}

	// The membership discount first, because a coupon is evaluated against what is
	// left. A coupon that does not stack with the membership discount removes it
	// entirely rather than being added on top -- the two are alternatives, not a sum.
	memberRate := in.MemberRate
	if memberRate <= 0 || memberRate > 1 {
		memberRate = 1
	}

	stacks := true
	for _, coupon := range in.Coupons {
		if !coupon.StacksWithMember {
			stacks = false
			break
		}
	}

	if stacks && memberRate < 1 {
		// The rate is used directly as the fraction to charge, and the discount is
		// what is left. Deriving the complement first looks equivalent and is not:
		// 1 - 0.9 is 0.09999999999999998 in binary floating point, which truncates to
		// 999 basis points and under-discounts by a unit. Computing the charge and
		// subtracting keeps the arithmetic on the number the caller supplied.
		charged := roundDiv(out.Subtotal*rateToBasisPoints(memberRate), 10000)
		out.MemberDiscount = out.Subtotal - charged
	}

	afterMember := out.Subtotal - out.MemberDiscount

	for _, coupon := range in.Coupons {
		out.CouponDiscount += coupon.Amount
	}

	// A discount never exceeds what is left. Without this an over-generous coupon
	// combination produces a negative subtotal, which is a refund nobody agreed to.
	if out.CouponDiscount > afterMember {
		out.CouponDiscount = afterMember
	}
	if out.CouponDiscount < 0 {
		out.CouponDiscount = 0
	}

	out.DiscountTotal = out.MemberDiscount + out.CouponDiscount

	discounted := out.Subtotal - out.DiscountTotal
	if discounted < 0 {
		discounted = 0
	}

	out.ShippingFee = in.ShippingFee

	if in.TaxRate > 0 {
		if in.TaxInclusive {
			// The tax is already inside the price, so it is extracted rather than
			// added. Charging it again is the mistake this branch exists to prevent,
			// and it is invisible on a receipt that only shows a total.
			base := discounted + out.ShippingFee
			points := percentageToBasisPoints(in.TaxRate)
			out.TaxAmount = roundDiv(base*points, 10000+points)
		} else {
			out.TaxAmount = roundDiv((discounted+out.ShippingFee)*percentageToBasisPoints(in.TaxRate), 10000)
		}
	}

	out.GrandTotal = discounted + out.ShippingFee
	if !in.TaxInclusive {
		out.GrandTotal += out.TaxAmount
	}

	if out.GrandTotal < 0 {
		out.GrandTotal = 0
	}

	return out
}

// ComputeLines distributes the discount and the tax across the lines.
//
// The remainder from each division goes to the largest line rather than being
// dropped, so the line totals sum to the order total. Without that an order can show
// items that add up to a penny less than the amount charged, and every reconciliation
// afterwards has to know why.
func ComputeLines(in Input, pricing Pricing) []LineTotal {
	out := make([]LineTotal, 0, len(in.Lines))

	for _, line := range in.Lines {
		out = append(out, LineTotal{Line: line, Subtotal: line.Subtotal()})
	}
	if pricing.Subtotal <= 0 {
		return out
	}

	// The discount is spread in proportion to each line's share of the subtotal.
	var assignedDiscount int64
	for i := range out {
		share := roundDiv(out[i].Subtotal*pricing.DiscountTotal, pricing.Subtotal)
		out[i].DiscountAmount = share
		assignedDiscount += share
	}

	// Whatever rounding left over lands on the largest line, which is the one where a
	// unit is least visible and least arguable.
	if remainder := pricing.DiscountTotal - assignedDiscount; remainder != 0 {
		if largest := largestIndex(out); largest >= 0 {
			out[largest].DiscountAmount += remainder
		}
	}

	var assignedTax int64
	for i := range out {
		base := out[i].Subtotal - out[i].DiscountAmount
		if base < 0 {
			base = 0
		}
		share := int64(0)
		if pricing.TaxAmount != 0 {
			denominator := pricing.Subtotal - pricing.DiscountTotal + pricing.ShippingFee
			if denominator > 0 {
				share = roundDiv(base*pricing.TaxAmount, denominator)
			}
		}
		out[i].TaxAmount = share
		assignedTax += share
	}
	if remainder := pricing.TaxAmount - assignedTax; remainder != 0 {
		if largest := largestIndex(out); largest >= 0 {
			out[largest].TaxAmount += remainder
		}
	}

	for i := range out {
		out[i].Total = out[i].Subtotal - out[i].DiscountAmount + out[i].TaxAmount
	}

	return out
}

// percentageToBasisPoints converts a percentage, such as a tax rate of 8.25, to
// basis points. It rounds rather than truncating.
//
// Truncation is a real defect and not a theoretical one: 8.2 * 100 is
// 819.9999999999999 in binary floating point, so a straight conversion taxes at
// 8.19% and the error is invisible on any single receipt. Rounding to the nearest
// basis point is the closest a float can honestly get to the number somebody typed.
func percentageToBasisPoints(percent float64) int64 {
	if percent == 0 {
		return 0
	}
	return int64(math.Round(percent * 100))
}

// rateToBasisPoints converts a fraction, such as a membership rate of 0.9 meaning
// charge ninety percent, to basis points.
//
// It is a separate function from the one above because the two units are different
// and confusing them is silent: a rate of 0.9 passed through the percentage
// conversion becomes 90 basis points, which is a ninety-nine percent discount.
func rateToBasisPoints(rate float64) int64 {
	if rate == 0 {
		return 0
	}
	return int64(math.Round(rate * 10000))
}

// largestIndex returns the index of the line with the largest subtotal.
func largestIndex(totals []LineTotal) int {
	best := -1
	var bestAmount int64 = -1

	for i := range totals {
		if totals[i].Subtotal > bestAmount {
			best = i
			bestAmount = totals[i].Subtotal
		}
	}
	return best
}

// roundDiv divides, rounding half away from zero.
func roundDiv(numerator, denominator int64) int64 {
	if denominator == 0 {
		return 0
	}

	negative := (numerator < 0) != (denominator < 0)
	n, d := numerator, denominator
	if n < 0 {
		n = -n
	}
	if d < 0 {
		d = -d
	}

	result := n / d
	if (n%d)*2 >= d {
		result++
	}

	if negative {
		return -result
	}
	return result
}

// ---------------------------------------------------------------------------
// Creation
// ---------------------------------------------------------------------------

// Order number generation
//
// The number is human-readable because it is read aloud on a support call and typed
// into a search box. A bare identifier is neither.

// NextOrderNo builds the next order number from a prefix.
//
// The sequence is derived from the count of orders rather than from a stored counter,
// because a counter is a second source of truth that can be lost. The uniqueness
// index is what actually guarantees it; this only has to produce a plausible
// candidate.
func NextOrderNo(app core.App, prefix string, now time.Time) (string, error) {
	if prefix == "" {
		prefix = "ORD"
	}

	count, err := app.CountRecords("orders")
	if err != nil {
		return "", err
	}

	base := fmt.Sprintf("%s-%s-%05d", prefix, now.Format("200601"), count+1)

	// A collision is possible when orders are deleted, so the candidate is checked
	// and advanced rather than trusted.
	candidate := base
	for attempt := 0; attempt < 100; attempt++ {
		existing, _ := app.FindFirstRecordByFilter("orders",
			"orderNo = {:n}", map[string]any{"n": candidate})
		if existing == nil {
			return candidate, nil
		}
		candidate = fmt.Sprintf("%s-%05d", base, attempt+2)
	}

	return fmt.Sprintf("%s-%d", base, now.UnixNano()%100000), nil
}

// decodeJSON reads a stored json column.
//
// PocketBase returns a json field as types.JSONRaw, which is a named []byte and so
// does not match a []byte case in a type switch. The mistake is invisible where it is
// made, which is why it is written once.
func decodeJSON(value any) any {
	switch v := value.(type) {
	case nil:
		return nil
	case types.JSONRaw:
		return decodeBytes([]byte(v))
	case []byte:
		return decodeBytes(v)
	case string:
		return decodeBytes([]byte(v))
	default:
		return value
	}
}

// Snapshot renders a line's snapshots as a map for the item record.
func (l Line) Snapshot() map[string]any {
	return map[string]any{
		"unitPrice":    l.UnitPrice,
		"quantity":     l.Quantity,
		"currency":     l.Currency,
		"productName":  l.ProductName,
		"variantLabel": l.VariantLabel,
		"sku":          l.SKU,
		"image":        l.Image,
	}
}

// Money renders an amount for a message.
func Money(amount int64, currency string) string {
	return money.Format(money.Amount(amount), currency)
}

// decodeBytes unmarshals JSON text, returning the text when it does not parse.
func decodeBytes(raw []byte) any {
	if len(raw) == 0 {
		return nil
	}
	var out any
	if err := json.Unmarshal(raw, &out); err != nil {
		return string(raw)
	}
	return out
}
