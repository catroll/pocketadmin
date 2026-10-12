// Package promotion holds campaigns, coupon templates and the grants issued from
// them, and the arithmetic that turns a coupon into an amount of money.
//
// A coupon is two things. The template describes the discount; a grant is one
// instance of it, held by one account, with its own expiry. Keeping them apart is
// what lets a public code be shared and what stops an edit to a template from
// rewriting the terms of every coupon already handed out.
//
// Every amount here is in minor units of one currency, and the percentage is stored
// in basis points. Both follow from internal/money: a whole-number column cannot
// hold 12.5%, and a floating-point one cannot represent 12.5% of 1999.
package promotion

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/types"
)

// Collection names.
const (
	CollectionCampaigns = "campaigns"
	CollectionCoupons   = "coupons"
	CollectionGrants    = "coupon_grants"
)

// Coupon types.
const (
	TypeFixedAmount  = "fixed_amount"
	TypePercentage   = "percentage"
	TypeFreeShipping = "free_shipping"
)

// Validity modes.
const (
	ValidityAbsolute = "absolute"
	ValidityRelative = "relative"
)

// Applicability.
const (
	ApplyAll        = "all"
	ApplyCategories = "categories"
	ApplyProducts   = "products"
	ApplyTiers      = "tiers"
)

// Grant statuses.
const (
	GrantIssued  = "issued"
	GrantUsed    = "used"
	GrantExpired = "expired"
	GrantVoided  = "voided"
)

// BasisPoints is one hundred percent: 10000 basis points.
//
// The unit is basis points rather than percent because a whole-number column cannot
// hold 12.5, and a float cannot be trusted with the arithmetic that follows.
const BasisPoints = 10000

// Line is one item in a cart.
type Line struct {
	ProductID  string
	CategoryID string
	Amount     int64
}

// Cart is what a coupon is evaluated against.
type Cart struct {
	// Subtotal is the total in minor units before any discount.
	Subtotal int64

	// Currency is the currency the amounts are in.
	Currency string

	// Lines are the individual items, needed only when a coupon applies to
	// particular products or categories.
	Lines []Line

	// TierIDs are the membership tiers the account holds.
	TierIDs []string

	// HolderID is the account the cart belongs to. It is carried here rather than
	// passed to Evaluate separately so a caller cannot forget it, which is how a
	// coupon belonging to one account gets used by another.
	HolderID string
}

// Evaluation is what a coupon does to a cart.
type Evaluation struct {
	// Applicable reports whether the coupon may be used at all.
	Applicable bool

	// Reason explains a refusal, in words an operator or a customer can act on.
	Reason string

	// Discount is the amount to take off, in minor units.
	Discount int64

	// FreeShipping marks a coupon that discounts the shipping rather than the
	// goods, which is a discount of nothing against the subtotal.
	FreeShipping bool
}

// Evaluate applies one coupon to a cart.
//
// The order of the checks is deliberate and is the order a customer would expect
// them explained in: is it usable at all, does the cart qualify, and only then how
// much comes off. Reporting "below the minimum spend" for a coupon that expired last
// week would send somebody to fill a basket for nothing.
func Evaluate(app core.App, coupon, grant *core.Record, cart Cart, now time.Time) Evaluation {
	if coupon == nil {
		return refuse("that coupon does not exist")
	}
	if coupon.GetString("status") != "active" {
		return refuse("that coupon is not active")
	}

	// The grant's own state, when there is one.
	if grant != nil {
		switch grant.GetString("status") {
		case GrantIssued:
		case GrantUsed:
			return refuse("that coupon has already been used")
		case GrantVoided:
			return refuse("that coupon was withdrawn")
		case GrantExpired:
			return refuse("that coupon has expired")
		}

		if expiry := grant.GetDateTime("expireAt"); !expiry.IsZero() && expiry.Time().Before(now) {
			return refuse("that coupon expired on " + expiry.Time().Format("2 Jan 2006"))
		}

		// A grant for a named account belongs to that account.
		if holder := grant.GetString("user"); holder != "" && holder != cart.HolderID {
			return refuse("that coupon belongs to another account")
		}
	}

	// The template's own window, when it uses one.
	if coupon.GetString("validityMode") == ValidityAbsolute {
		if from := coupon.GetDateTime("validFrom"); !from.IsZero() && from.Time().After(now) {
			return refuse("that coupon is not valid yet")
		}
		if to := coupon.GetDateTime("validTo"); !to.IsZero() && to.Time().Before(now) {
			return refuse("that coupon expired on " + to.Time().Format("2 Jan 2006"))
		}
	}

	// The currency has to match. A ten-dollar coupon applied to a euro basket is a
	// discount of ten of something, and there is no sensible conversion here.
	if couponCurrency := coupon.GetString("currency"); couponCurrency != "" &&
		cart.Currency != "" && !strings.EqualFold(couponCurrency, cart.Currency) {
		return refuse(fmt.Sprintf("that coupon is in %s and this order is in %s",
			couponCurrency, cart.Currency))
	}

	if minimum := int64(coupon.GetFloat("minSpend")); minimum > 0 && cart.Subtotal < minimum {
		return refuse("that coupon needs a larger order")
	}

	// How much of the cart the coupon may actually be applied to.
	eligible := eligibleAmount(coupon, cart)
	if eligible <= 0 {
		return refuse("that coupon does not apply to anything in this order")
	}

	freeShipping := coupon.GetString("type") == TypeFreeShipping

	var discount int64
	switch coupon.GetString("type") {
	case TypeFixedAmount:
		discount = int64(coupon.GetFloat("value"))

	case TypePercentage:
		// Basis points, so 1250 is twelve and a half percent. The multiplication is
		// done in integers and rounded once: rounding per line would accumulate a
		// difference between the sum of the parts and the whole.
		points := int64(coupon.GetFloat("value"))
		if points <= 0 {
			return refuse("that coupon has no discount configured")
		}
		if points > BasisPoints {
			// A discount above a hundred percent would make the order negative.
			// Clamping rather than refusing is deliberate: a misconfigured "free"
			// coupon should give the order away, not fail at the till.
			points = BasisPoints
		}
		discount = mulDiv(eligible, points, BasisPoints)

	case TypeFreeShipping:
		discount = 0

	default:
		return refuse("that coupon is of an unknown type")
	}

	// The cap. It applies to a percentage, and to a fixed amount it is simply a
	// second, smaller limit -- which is a legitimate thing to configure.
	if cap := int64(coupon.GetFloat("maxDiscount")); cap > 0 && discount > cap {
		discount = cap
	}

	// A discount never exceeds what it is discounting. Without this a fixed-amount
	// coupon on a small basket produces a negative total, which is a refund the shop
	// did not agree to.
	if discount > cart.Subtotal {
		discount = cart.Subtotal
	}
	if discount < 0 {
		discount = 0
	}

	if discount == 0 && !freeShipping {
		return refuse("that coupon takes nothing off this order")
	}

	return Evaluation{Applicable: true, Discount: discount, FreeShipping: freeShipping}
}

// eligibleAmount returns the part of the subtotal a coupon may discount.
func eligibleAmount(coupon *core.Record, cart Cart) int64 {
	// Exclusions apply whatever the applicability is: an exclusion is a statement
	// about a product, not about how the coupon was targeted.
	excluded := idsOf(decodeIDs(coupon.Get("excludeProducts")))

	switch coupon.GetString("applicability") {
	case ApplyAll, "":
		// The whole cart, minus anything excluded.
		if len(excluded) == 0 {
			return cart.Subtotal
		}
		var total int64
		for _, line := range cart.Lines {
			if !excluded[line.ProductID] {
				total += line.Amount
			}
		}
		return total

	case ApplyProducts:
		allowed := idsOf(decodeIDs(coupon.Get("products")))
		var total int64
		for _, line := range cart.Lines {
			if allowed[line.ProductID] && !excluded[line.ProductID] {
				total += line.Amount
			}
		}
		return total

	case ApplyCategories:
		allowed := idsOf(decodeIDs(coupon.Get("categories")))
		var total int64
		for _, line := range cart.Lines {
			if allowed[line.CategoryID] && !excluded[line.ProductID] {
				total += line.Amount
			}
		}
		return total

	case ApplyTiers:
		allowed := idsOf(decodeIDs(coupon.Get("tiers")))
		for _, tierID := range cart.TierIDs {
			if allowed[tierID] {
				// A tier-targeted coupon applies to the whole cart: the tier is a
				// statement about the customer, not about the goods.
				var total int64
				for _, line := range cart.Lines {
					if !excluded[line.ProductID] {
						total += line.Amount
					}
				}
				return total
			}
		}
		return 0
	}

	return cart.Subtotal
}

// idsOf turns a slice into a set.
func idsOf(ids []string) map[string]bool {
	out := make(map[string]bool, len(ids))
	for _, id := range ids {
		if id != "" {
			out[id] = true
		}
	}
	return out
}

// refuse builds a refusal.
func refuse(reason string) Evaluation {
	return Evaluation{Applicable: false, Reason: reason}
}

// mulDiv returns a*b/c using integer arithmetic, rounding half away from zero.
//
// The intermediate product is widened deliberately. a*b for a large basket and
// basis points overflows an int64 only above about nine hundred trillion minor
// units, which is more money than exists, but the widening costs nothing and makes
// the reasoning local rather than a claim about the caller's inputs.
func mulDiv(a, b, c int64) int64 {
	if c == 0 {
		return 0
	}

	negative := (a < 0) != (b < 0)
	x, y := a, b
	if x < 0 {
		x = -x
	}
	if y < 0 {
		y = -y
	}

	// Split b so the product cannot overflow: a*b/c == (a/c)*b + (a%c)*b/c.
	whole := x / c
	remainder := x % c
	result := whole*y + remainder*y/c

	// Round the discarded part rather than truncating, so a discount is not
	// systematically a fraction of a unit smaller than it should be.
	if (remainder*y)%c*2 >= c {
		result++
	}

	if negative {
		return -result
	}
	return result
}

// ---------------------------------------------------------------------------
// Validity and issuance
// ---------------------------------------------------------------------------

// GrantExpiry works out when a grant expires.
//
// A relative coupon counts from the moment of the grant, which is what "thirty days
// from now" means; an absolute one has a fixed window and the grant inherits it. The
// two are not interchangeable and the mode says which is in force.
func GrantExpiry(coupon *core.Record, issuedAt time.Time) time.Time {
	if coupon.GetString("validityMode") == ValidityRelative {
		days := coupon.GetInt("validDays")
		if days <= 0 {
			// A relative coupon with no period would never expire, which is almost
			// certainly a misconfiguration rather than an intention. It is given the
			// absolute window instead, and if there is none, no expiry.
			if to := coupon.GetDateTime("validTo"); !to.IsZero() {
				return to.Time()
			}
			return time.Time{}
		}
		return issuedAt.AddDate(0, 0, days)
	}

	if to := coupon.GetDateTime("validTo"); !to.IsZero() {
		return to.Time()
	}
	return time.Time{}
}

// CanIssue reports whether another grant may be issued, and why not.
//
// The counters are read from the grants rather than trusted from the template: a
// denormalised count that only ever goes up is wrong the first time anything else
// touches the rows, and a limit enforced against a wrong number is not enforced.
func CanIssue(app core.App, coupon *core.Record) (bool, string) {
	total := coupon.GetInt("totalQuantity")
	if total <= 0 {
		return true, ""
	}

	// The placeholder names the parameter it reads, and getting the two out of step
	// is a parse error rather than a missing value, which is worth knowing: the
	// message says "unexpected character" and points nowhere near the cause.
	issued, err := app.FindRecordsByFilter(CollectionGrants,
		"coupon = {:coupon} && status != {:voided}", "", 0, 0,
		map[string]any{"coupon": coupon.Id, "voided": GrantVoided})
	if err != nil {
		return false, "the issued coupons could not be counted: " + err.Error()
	}

	if len(issued) >= total {
		return false, fmt.Sprintf("only %d were available and all have been issued", total)
	}
	return true, ""
}

// IssuedTo counts how many grants an account holds from one coupon.
func IssuedTo(app core.App, couponID, userID string) (int, error) {
	if userID == "" {
		return 0, nil
	}

	records, err := app.FindRecordsByFilter(CollectionGrants,
		"coupon = {:coupon} && user = {:user} && status != {:voided}", "", 0, 0,
		map[string]any{"coupon": couponID, "user": userID, "voided": GrantVoided})
	if err != nil {
		return 0, err
	}
	return len(records), nil
}

// CanIssueTo reports whether one more grant may be issued to an account.
func CanIssueTo(app core.App, coupon *core.Record, userID string) (bool, string) {
	limit := coupon.GetInt("perUserLimit")
	if limit <= 0 {
		return true, ""
	}

	count, err := IssuedTo(app, coupon.Id, userID)
	if err != nil {
		return false, "the account's coupons could not be counted: " + err.Error()
	}
	if count >= limit {
		return false, fmt.Sprintf("this account may hold %d and already holds %d", limit, count)
	}
	return true, ""
}

// decodeIDs reads a relation column.
//
// PocketBase returns a relation field as types.JSONRaw, which is a named []byte and
// so does not match a []byte case in a type switch. That mistake is invisible where
// it is made, which is why it is written once.
func decodeIDs(value any) []string {
	switch v := value.(type) {
	case []string:
		return v
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			if text, ok := item.(string); ok {
				out = append(out, text)
			}
		}
		return out
	case types.JSONRaw:
		return decodeIDsBytes([]byte(v))
	case []byte:
		return decodeIDsBytes(v)
	case string:
		return decodeIDsBytes([]byte(v))
	default:
		return nil
	}
}

// decodeIDsBytes unmarshals a JSON array of identifiers.
func decodeIDsBytes(raw []byte) []string {
	if len(raw) == 0 {
		return nil
	}
	var out []string
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil
	}
	return out
}
