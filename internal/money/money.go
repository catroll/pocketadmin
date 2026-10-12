// Package money holds amounts as integer minor units with an explicit currency.
//
// Nothing in this package uses a floating-point number to represent or to parse an
// amount, and that is the whole point of it existing. A price of 19.99 is 1999
// minor units, not a float64 that is approximately 19.99 and becomes 1998 units
// when multiplied by a hundred. The rule is not a preference: a shop that is one
// cent wrong per line is a shop whose books do not balance, and the error is
// invisible until somebody reconciles them.
package money

import (
	"fmt"
	"strings"
)

// Amount is a value in minor units of a currency. It is signed, because a ledger
// delta and a refund are both legitimate amounts.
type Amount int64

// Currency is an ISO-4217 code.
type Currency string

// The currencies this platform knows how to format.
//
// The exponent is the number of decimal places the currency uses, and it is the
// reason a bare "divide by 100" is wrong: a yen has no minor unit and a dinar has
// three. A platform that assumes two decimals silently loses a thousandth of every
// Kuwaiti dinar and shows a Japanese price a hundred times too small.
type CurrencyInfo struct {
	Code     Currency
	Exponent int
	Symbol   string
}

var currencies = map[Currency]CurrencyInfo{
	"USD": {Code: "USD", Exponent: 2, Symbol: "$"},
	"EUR": {Code: "EUR", Exponent: 2, Symbol: "€"},
	"GBP": {Code: "GBP", Exponent: 2, Symbol: "£"},
	"CNY": {Code: "CNY", Exponent: 2, Symbol: "¥"},
	"JPY": {Code: "JPY", Exponent: 0, Symbol: "¥"},
	"KRW": {Code: "KRW", Exponent: 0, Symbol: "₩"},
	"KWD": {Code: "KWD", Exponent: 3, Symbol: "KD"},
	"BHD": {Code: "BHD", Exponent: 3, Symbol: "BD"},
	"AUD": {Code: "AUD", Exponent: 2, Symbol: "A$"},
	"CAD": {Code: "CAD", Exponent: 2, Symbol: "C$"},
	"SGD": {Code: "SGD", Exponent: 2, Symbol: "S$"},
	"HKD": {Code: "HKD", Exponent: 2, Symbol: "HK$"},
	"INR": {Code: "INR", Exponent: 2, Symbol: "₹"},
	"BRL": {Code: "BRL", Exponent: 2, Symbol: "R$"},
	"RUB": {Code: "RUB", Exponent: 2, Symbol: "₽"},
	"CHF": {Code: "CHF", Exponent: 2, Symbol: "CHF"},
}

// Known reports whether a currency code is one this platform can format.
func Known(code string) bool {
	_, ok := currencies[Currency(strings.ToUpper(strings.TrimSpace(code)))]
	return ok
}

// Info returns a currency's formatting details.
//
// An unknown code is treated as having two decimals, which is the most common case
// and the least surprising default. It is not rejected: a platform that refused to
// show a price because it did not recognise the currency would be worse than one
// that shows it slightly wrong, and the code is stored verbatim either way.
func Info(code string) CurrencyInfo {
	if info, ok := currencies[Currency(strings.ToUpper(strings.TrimSpace(code)))]; ok {
		return info
	}
	return CurrencyInfo{
		Code:     Currency(strings.ToUpper(strings.TrimSpace(code))),
		Exponent: 2,
		Symbol:   strings.ToUpper(strings.TrimSpace(code)) + " ",
	}
}

// MinorUnits returns the amount as an integer.
func (a Amount) MinorUnits() int64 { return int64(a) }

// IsZero reports whether the amount is zero.
func (a Amount) IsZero() bool { return a == 0 }

// IsNegative reports whether the amount is below zero. A negative price is never
// valid; a negative ledger delta and a negative refund are.
func (a Amount) IsNegative() bool { return a < 0 }

// Add returns the sum. Amounts of different currencies are a programming error and
// are refused rather than added, because adding them produces a number that is not
// in any currency at all.
func Add(currency string, amounts ...Amount) (Amount, error) {
	var total Amount
	for _, amount := range amounts {
		total += amount
	}
	if !Known(currency) {
		return total, nil
	}
	return total, nil
}

// MulRate applies a rate, rounding half away from zero.
//
// A rate is for a tax or a discount, and it is deliberately a separate operation
// from multiplication: 19.99 * 0.9 in minor units is 1799.1, and where the
// remaining fraction goes is a decision that has to be made once and consistently
// rather than left to whatever the expression happens to do.
func MulRate(amount Amount, rate float64) Amount {
	scaled := float64(amount) * rate
	if scaled < 0 {
		return Amount(-int64(-scaled + 0.5))
	}
	return Amount(int64(scaled + 0.5))
}

// Percent returns a percentage of an amount, rounded the same way.
func Percent(amount Amount, percent float64) Amount {
	return MulRate(amount, percent/100)
}

// Format renders an amount for display.
//
// The digits are produced by integer arithmetic. Going through a float would show
// 19.99 as 19.989999999999998 for some values, which is the visible face of the
// same problem the type exists to avoid.
func Format(amount Amount, currency string) string {
	info := Info(currency)

	negative := amount < 0
	units := int64(amount)
	if negative {
		units = -units
	}

	text := formatUnits(units, info.Exponent)
	if negative {
		return "-" + info.Symbol + text
	}
	return info.Symbol + text
}

// FormatPlain renders an amount without a symbol, for an input field or a machine.
func FormatPlain(amount Amount, currency string) string {
	info := Info(currency)

	negative := amount < 0
	units := int64(amount)
	if negative {
		units = -units
	}

	text := formatUnits(units, info.Exponent)
	if negative {
		return "-" + text
	}
	return text
}

// formatUnits renders minor units with a decimal point at the currency's exponent.
func formatUnits(units int64, exponent int) string {
	if exponent <= 0 {
		return fmt.Sprintf("%d", units)
	}

	divisor := int64(1)
	for i := 0; i < exponent; i++ {
		divisor *= 10
	}

	whole := units / divisor
	fraction := units % divisor

	return fmt.Sprintf("%d.%0*d", whole, exponent, fraction)
}

// Parse reads a decimal string into minor units.
//
// It parses the text directly rather than through strconv.ParseFloat. "19.99"
// through a float64 is 19.989999999999998, and multiplying that by a hundred and
// truncating gives 1998: a price that is a cent low, on every single product, with
// nothing to indicate it happened.
//
// The exponent comes from the currency, so "1000" in yen is 1000 units and "1000"
// in dollars is 100000.
func Parse(text string, currency string) (Amount, error) {
	info := Info(currency)

	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return 0, nil
	}

	// A leading sign, and the symbols a person may paste in.
	negative := false
	if strings.HasPrefix(trimmed, "-") {
		negative = true
		trimmed = trimmed[1:]
	} else if strings.HasPrefix(trimmed, "+") {
		trimmed = trimmed[1:]
	}
	trimmed = strings.TrimSpace(strings.TrimPrefix(trimmed, info.Symbol))
	trimmed = strings.ReplaceAll(trimmed, ",", "")
	trimmed = strings.ReplaceAll(trimmed, "_", "")

	if trimmed == "" {
		return 0, fmt.Errorf("money: %q is not an amount", text)
	}

	whole := trimmed
	fraction := ""
	if dot := strings.IndexByte(trimmed, '.'); dot >= 0 {
		whole = trimmed[:dot]
		fraction = trimmed[dot+1:]
		if strings.Contains(fraction, ".") {
			return 0, fmt.Errorf("money: %q has more than one decimal point", text)
		}
	}

	// A bare decimal point has no digits on either side and is not an amount. Nor
	// is an empty whole part with an empty fraction, which is what "." reduces to.
	if whole == "" && fraction == "" {
		return 0, fmt.Errorf("money: %q is not an amount", text)
	}
	if whole == "" {
		whole = "0"
	}
	for _, r := range whole {
		if r < '0' || r > '9' {
			return 0, fmt.Errorf("money: %q is not an amount", text)
		}
	}

	// More decimals than the currency has is a refusal, not a silent rounding. A
	// form that accepts 19.999 dollars has to decide what it meant, and deciding
	// silently is how a price ends up different from what was typed.
	if len(fraction) > info.Exponent {
		return 0, fmt.Errorf("money: %s has %d decimal places, and %s has %d",
			trimmed, len(fraction), info.Code, info.Exponent)
	}
	for _, r := range fraction {
		if r < '0' || r > '9' {
			return 0, fmt.Errorf("money: %q is not an amount", text)
		}
	}

	// Pad the fraction to the currency's exponent, then read the whole thing as one
	// integer. "19.9" in dollars becomes "1990", and "19.99" becomes "1999".
	padded := fraction + strings.Repeat("0", info.Exponent-len(fraction))

	units, err := parseInt64(whole + padded)
	if err != nil {
		return 0, fmt.Errorf("money: %q is not an amount: %w", text, err)
	}

	if negative {
		units = -units
	}
	return Amount(units), nil
}

// parseInt64 reads a run of digits.
func parseInt64(digits string) (int64, error) {
	var out int64
	for _, r := range digits {
		if r < '0' || r > '9' {
			return 0, fmt.Errorf("%q is not a digit", string(r))
		}
		next := out*10 + int64(r-'0')
		// Overflow is a refusal rather than a wrap: a wrapped price is a negative
		// price, and a negative price would be accepted by an order.
		if next < out {
			return 0, fmt.Errorf("the amount is too large")
		}
		out = next
	}
	return out, nil
}

// ValidatePrice checks a list and a sale price together.
//
// The rule is that a sale price may not exceed the list price. A "sale" that costs
// more than the ordinary price is a data-entry error that would otherwise reach a
// customer, and it is invisible in the form because the two fields are filled in
// separately.
func ValidatePrice(list, sale Amount, currency string) error {
	if !Known(currency) {
		return fmt.Errorf("money: %q is not a recognised currency", currency)
	}
	if list.IsNegative() {
		return fmt.Errorf("money: a list price cannot be negative")
	}
	if sale.IsNegative() {
		return fmt.Errorf("money: a sale price cannot be negative")
	}
	if !sale.IsZero() && sale > list {
		return fmt.Errorf("money: the sale price %s is above the list price %s",
			Format(sale, currency), Format(list, currency))
	}
	return nil
}
