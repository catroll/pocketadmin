package order_test

import (
	"testing"

	"pocketadmin/internal/order"
)

// line builds a line with a quantity of one.
func line(productID string, unitPrice int64) order.Line {
	return order.Line{
		ProductID:   productID,
		ProductName: "Product " + productID,
		UnitPrice:   unitPrice,
		Quantity:    1,
		Currency:    "USD",
	}
}

// TestTheSubtotalIsTheSumOfTheLines.
func TestTheSubtotalIsTheSumOfTheLines(t *testing.T) {
	pricing := order.Compute(order.Input{
		Lines:    []order.Line{line("a", 1999), line("b", 2500)},
		Currency: "USD",
	})

	if pricing.Subtotal != 4499 {
		t.Errorf("the subtotal is %d, want 4499", pricing.Subtotal)
	}
	if pricing.GrandTotal != 4499 {
		t.Errorf("the grand total is %d, want 4499 with no discount, shipping or tax", pricing.GrandTotal)
	}
	if pricing.DiscountTotal != 0 {
		t.Errorf("the discount total is %d", pricing.DiscountTotal)
	}
}

// TestTheMembershipDiscountIsAFraction: 0.9 means ten percent off, and the arithmetic
// is integer throughout.
func TestTheMembershipDiscountIsAFraction(t *testing.T) {
	pricing := order.Compute(order.Input{
		Lines:      []order.Line{line("a", 10000)},
		Currency:   "USD",
		MemberRate: 0.9,
	})

	if pricing.MemberDiscount != 1000 {
		t.Errorf("the member discount is %d, want 1000", pricing.MemberDiscount)
	}
	if pricing.GrandTotal != 9000 {
		t.Errorf("the grand total is %d, want 9000", pricing.GrandTotal)
	}
}

// TestARateOfOneIsNoDiscount: the default has to be a no-op, or an account with no
// membership would be discounted or surcharged by accident.
func TestARateOfOneIsNoDiscount(t *testing.T) {
	for _, rate := range []float64{1.0, 0, -1, 2} {
		pricing := order.Compute(order.Input{
			Lines:      []order.Line{line("a", 10000)},
			Currency:   "USD",
			MemberRate: rate,
		})
		if pricing.MemberDiscount != 0 {
			t.Errorf("a rate of %v produced a discount of %d", rate, pricing.MemberDiscount)
		}
	}
}

// TestCouponsAreAppliedAfterTheMembershipDiscount: two independent percentages of the
// same number do not sum to a discount of the two together, and the difference has to
// go somewhere.
func TestCouponsAreAppliedAfterTheMembershipDiscount(t *testing.T) {
	pricing := order.Compute(order.Input{
		Lines:      []order.Line{line("a", 10000)},
		Currency:   "USD",
		MemberRate: 0.9, // 1000 off
		Coupons:    []order.Coupon{{GrantID: "g1", Amount: 500, StacksWithMember: true}},
	})

	if pricing.MemberDiscount != 1000 {
		t.Errorf("the member discount is %d, want 1000", pricing.MemberDiscount)
	}
	if pricing.CouponDiscount != 500 {
		t.Errorf("the coupon discount is %d, want 500", pricing.CouponDiscount)
	}
	if pricing.DiscountTotal != 1500 {
		t.Errorf("the discount total is %d, want 1500", pricing.DiscountTotal)
	}
	if pricing.GrandTotal != 8500 {
		t.Errorf("the grand total is %d, want 8500", pricing.GrandTotal)
	}
}

// TestACouponThatDoesNotStackRemovesTheMemberDiscount: the two are alternatives, not
// a sum.
func TestACouponThatDoesNotStackRemovesTheMemberDiscount(t *testing.T) {
	pricing := order.Compute(order.Input{
		Lines:      []order.Line{line("a", 10000)},
		Currency:   "USD",
		MemberRate: 0.9,
		Coupons:    []order.Coupon{{GrantID: "g1", Amount: 1500, StacksWithMember: false}},
	})

	if pricing.MemberDiscount != 0 {
		t.Errorf("the member discount survived a non-stacking coupon: %d", pricing.MemberDiscount)
	}
	if pricing.CouponDiscount != 1500 {
		t.Errorf("the coupon discount is %d, want 1500", pricing.CouponDiscount)
	}
	if pricing.GrandTotal != 8500 {
		t.Errorf("the grand total is %d, want 8500", pricing.GrandTotal)
	}
}

// TestDiscountsNeverExceedTheSubtotal: an over-generous combination would otherwise
// produce a negative total, which is a refund nobody agreed to.
func TestDiscountsNeverExceedTheSubtotal(t *testing.T) {
	pricing := order.Compute(order.Input{
		Lines:      []order.Line{line("a", 1000)},
		Currency:   "USD",
		MemberRate: 0.9,
		Coupons: []order.Coupon{
			{GrantID: "g1", Amount: 5000, StacksWithMember: true},
			{GrantID: "g2", Amount: 5000, StacksWithMember: true},
		},
	})

	if pricing.GrandTotal < 0 {
		t.Errorf("the grand total is %d, which is negative", pricing.GrandTotal)
	}
	if pricing.GrandTotal != 0 {
		t.Errorf("the grand total is %d, want 0", pricing.GrandTotal)
	}
	if pricing.DiscountTotal > pricing.Subtotal {
		t.Errorf("the discount %d exceeds the subtotal %d", pricing.DiscountTotal, pricing.Subtotal)
	}
}

// TestTaxIsAddedWhenPricesExcludeIt.
func TestTaxIsAddedWhenPricesExcludeIt(t *testing.T) {
	pricing := order.Compute(order.Input{
		Lines:    []order.Line{line("a", 10000)},
		Currency: "USD",
		TaxRate:  8.25,
	})

	if pricing.TaxAmount != 825 {
		t.Errorf("the tax is %d, want 825", pricing.TaxAmount)
	}
	if pricing.GrandTotal != 10825 {
		t.Errorf("the grand total is %d, want 10825", pricing.GrandTotal)
	}
}

// TestTaxIsExtractedWhenPricesIncludeIt: charging it again is invisible on a receipt
// that only shows a total.
func TestTaxIsExtractedWhenPricesIncludeIt(t *testing.T) {
	pricing := order.Compute(order.Input{
		Lines:        []order.Line{line("a", 10825)},
		Currency:     "USD",
		TaxRate:      8.25,
		TaxInclusive: true,
	})

	if pricing.TaxAmount != 825 {
		t.Errorf("the tax extracted is %d, want 825", pricing.TaxAmount)
	}
	// The total is the listed price: the tax was already inside it.
	if pricing.GrandTotal != 10825 {
		t.Errorf("the grand total is %d, want the listed 10825", pricing.GrandTotal)
	}
}

// TestTaxIsChargedOnTheDiscountedAmount: taxing the pre-discount subtotal charges the
// customer tax on money they did not spend.
func TestTaxIsChargedOnTheDiscountedAmount(t *testing.T) {
	pricing := order.Compute(order.Input{
		Lines:      []order.Line{line("a", 10000)},
		Currency:   "USD",
		MemberRate: 0.5, // 5000 off
		TaxRate:    10,
	})

	if pricing.TaxAmount != 500 {
		t.Errorf("the tax is %d, want 500 (ten percent of the discounted 5000)", pricing.TaxAmount)
	}
	if pricing.GrandTotal != 5500 {
		t.Errorf("the grand total is %d, want 5500", pricing.GrandTotal)
	}
}

// TestShippingIsChargedAndTaxed.
func TestShippingIsChargedAndTaxed(t *testing.T) {
	pricing := order.Compute(order.Input{
		Lines:       []order.Line{line("a", 10000)},
		Currency:    "USD",
		ShippingFee: 500,
		TaxRate:     10,
	})

	if pricing.ShippingFee != 500 {
		t.Errorf("the shipping fee is %d", pricing.ShippingFee)
	}
	if pricing.TaxAmount != 1050 {
		t.Errorf("the tax is %d, want 1050 (ten percent of 10500)", pricing.TaxAmount)
	}
	if pricing.GrandTotal != 11550 {
		t.Errorf("the grand total is %d, want 11550", pricing.GrandTotal)
	}
}

// TestAnEmptyOrderIsNotAnError.
func TestAnEmptyOrderIsNotAnError(t *testing.T) {
	pricing := order.Compute(order.Input{Currency: "USD", ShippingFee: 500})
	if pricing.Subtotal != 0 {
		t.Errorf("an empty order has a subtotal of %d", pricing.Subtotal)
	}
	if pricing.GrandTotal != 500 {
		t.Errorf("an empty order with shipping totals %d, want 500", pricing.GrandTotal)
	}
}

// TestTheLineTotalsSumToTheOrderTotal is the property that makes the figures on a
// screen trustworthy.
func TestTheLineTotalsSumToTheOrderTotal(t *testing.T) {
	// Deliberately awkward numbers, so a rounding remainder has to be placed.
	in := order.Input{
		Lines: []order.Line{
			line("a", 1999),
			line("b", 3333),
			line("c", 777),
		},
		Currency:   "USD",
		MemberRate: 0.93,
		Coupons:    []order.Coupon{{GrantID: "g1", Amount: 250, StacksWithMember: true}},
		TaxRate:    8.25,
	}

	pricing := order.Compute(in)
	lines := order.ComputeLines(in, pricing)

	var subtotal, discount, tax, total int64
	for _, lt := range lines {
		subtotal += lt.Subtotal
		discount += lt.DiscountAmount
		tax += lt.TaxAmount
		total += lt.Total
	}

	if subtotal != pricing.Subtotal {
		t.Errorf("the line subtotals total %d, the order says %d", subtotal, pricing.Subtotal)
	}
	if discount != pricing.DiscountTotal {
		t.Errorf("the line discounts total %d, the order says %d", discount, pricing.DiscountTotal)
	}
	if tax != pricing.TaxAmount {
		t.Errorf("the line taxes total %d, the order says %d", tax, pricing.TaxAmount)
	}
	if total != pricing.GrandTotal {
		t.Errorf("the line totals sum to %d, the order says %d", total, pricing.GrandTotal)
	}
}

// TestTheLineTotalNeverGoesNegative: a line cannot be worth less than nothing, and a
// negative line total would be a refund the shop never made.
func TestTheLineTotalNeverGoesNegative(t *testing.T) {
	in := order.Input{
		Lines: []order.Line{
			line("small", 1),
			line("large", 100000),
		},
		Currency: "USD",
		Coupons:  []order.Coupon{{GrantID: "g1", Amount: 100000, StacksWithMember: true}},
	}

	pricing := order.Compute(in)
	for _, lt := range order.ComputeLines(in, pricing) {
		if lt.Total < 0 {
			t.Errorf("the line %s totals %d", lt.Line.ProductID, lt.Total)
		}
		if lt.DiscountAmount < 0 {
			t.Errorf("the line %s has a negative discount %d", lt.Line.ProductID, lt.DiscountAmount)
		}
	}
}

// TestAQuantityMultipliesTheLine.
func TestAQuantityMultipliesTheLine(t *testing.T) {
	multi := order.Line{ProductID: "a", ProductName: "A", UnitPrice: 1999, Quantity: 3, Currency: "USD"}
	if got := multi.Subtotal(); got != 5997 {
		t.Errorf("three at 1999 is %d, want 5997", got)
	}

	pricing := order.Compute(order.Input{Lines: []order.Line{multi}, Currency: "USD"})
	if pricing.Subtotal != 5997 {
		t.Errorf("the order subtotal is %d", pricing.Subtotal)
	}

	// A zero or negative quantity contributes nothing rather than a negative amount.
	for _, qty := range []int{0, -1} {
		odd := order.Line{ProductID: "a", UnitPrice: 1999, Quantity: qty}
		if got := odd.Subtotal(); got != 0 {
			t.Errorf("a quantity of %d gives %d, want 0", qty, got)
		}
	}
}

// TestAFloatingPointRateIsNotTruncated is a regression test for a real defect.
//
// 1 - 0.9 is 0.09999999999999998 in binary floating point, which truncated to 999
// basis points and under-discounted every membership by a unit. The rate is now used
// directly as the fraction to charge, and the discount is what is left.
//
// The tax path had the same defect from the other direction: 8.2 * 100 is
// 819.9999999999999, so a straight conversion to basis points taxed at 8.19% and the
// error was invisible on any single receipt.
func TestAFloatingPointRateIsNotTruncated(t *testing.T) {
	// A membership rate of a half, a tenth and a fifth. Each of these is a value that
	// cannot be represented exactly in binary floating point.
	cases := []struct {
		rate     float64
		subtotal int64
		want     int64
	}{
		{0.9, 10000, 1000},
		{0.5, 10000, 5000},
		{0.8, 10000, 2000},
		{0.7, 10000, 3000},
		{0.95, 10000, 500},
		{0.99, 10000, 100},
		{0.9, 1999, 200}, // 1999 * 0.9 = 1799.1, so 200 off
	}

	for _, tc := range cases {
		pricing := order.Compute(order.Input{
			Lines:      []order.Line{line("a", tc.subtotal)},
			Currency:   "USD",
			MemberRate: tc.rate,
		})
		if pricing.MemberDiscount != tc.want {
			t.Errorf("a rate of %v on %d discounts %d, want %d",
				tc.rate, tc.subtotal, pricing.MemberDiscount, tc.want)
		}
		if pricing.Subtotal-pricing.MemberDiscount != tc.subtotal-tc.want {
			t.Errorf("a rate of %v on %d charges %d, want %d",
				tc.rate, tc.subtotal, pricing.GrandTotal, tc.subtotal-tc.want)
		}
	}
}

// TestATaxRateWithADecimalIsNotTruncated.
func TestATaxRateWithADecimalIsNotTruncated(t *testing.T) {
	cases := []struct {
		rate     float64
		subtotal int64
		want     int64
	}{
		{8.25, 10000, 825},
		{8.2, 10000, 820}, // 8.2 * 100 truncates to 819
		{7.5, 10000, 750},
		{19.6, 10000, 1960}, // 19.6 * 100 truncates to 1959
		{0.1, 10000, 10},
	}

	for _, tc := range cases {
		pricing := order.Compute(order.Input{
			Lines:    []order.Line{line("a", tc.subtotal)},
			Currency: "USD",
			TaxRate:  tc.rate,
		})
		if pricing.TaxAmount != tc.want {
			t.Errorf("a tax rate of %v on %d is %d, want %d",
				tc.rate, tc.subtotal, pricing.TaxAmount, tc.want)
		}
	}
}
