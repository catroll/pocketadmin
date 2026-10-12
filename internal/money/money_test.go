package money_test

import (
	"testing"

	"pocketadmin/internal/money"
)

// TestParseAvoidsTheFloatTrap is the reason this package exists.
//
// "19.99" through a float64 is 19.989999999999998. Multiplying by a hundred and
// truncating gives 1998: a price one cent low, on every product, with nothing to
// show it happened. The parse reads the text as digits instead.
func TestParseAvoidsTheFloatTrap(t *testing.T) {
	cases := map[string]money.Amount{
		"19.99": 1999,
		"0.01":  1,
		"1.10":  110,
		"1.01":  101,
		"29.99": 2999,
		"1.07":  107,
		"2.03":  203,
		"0.29":  29,
		"0.57":  57,
		"1.15":  115,
		"16.08": 1608,
		"1.00":  100,
		"100":   10000,
		".5":    50,
		"0.5":   50,
	}

	for text, want := range cases {
		got, err := money.Parse(text, "USD")
		if err != nil {
			t.Errorf("Parse(%q): %v", text, err)
			continue
		}
		if got != want {
			t.Errorf("Parse(%q) = %d, want %d", text, got, want)
		}
	}
}

// TestParseRespectsTheCurrencyExponent: a bare "divide by a hundred" is wrong for
// a currency without minor units and for one with three.
func TestParseRespectsTheCurrencyExponent(t *testing.T) {
	// A yen has no minor unit: "1000" is a thousand yen, not ten.
	if got, err := money.Parse("1000", "JPY"); err != nil || got != 1000 {
		t.Errorf("Parse(1000, JPY) = %d, %v; want 1000", got, err)
	}
	if got, err := money.Parse("1000", "KRW"); err != nil || got != 1000 {
		t.Errorf("Parse(1000, KRW) = %d, %v; want 1000", got, err)
	}

	// A dinar has three.
	if got, err := money.Parse("1.234", "KWD"); err != nil || got != 1234 {
		t.Errorf("Parse(1.234, KWD) = %d, %v; want 1234", got, err)
	}
	// And the same digits in dollars is a different amount.
	if got, err := money.Parse("1.234", "USD"); err == nil {
		t.Errorf("Parse(1.234, USD) = %d, want a refusal: dollars have two decimals", got)
	}

	// The exponent is used for formatting too.
	if got := money.FormatPlain(1000, "JPY"); got != "1000" {
		t.Errorf("FormatPlain(1000, JPY) = %q, want 1000", got)
	}
	if got := money.FormatPlain(1234, "KWD"); got != "1.234" {
		t.Errorf("FormatPlain(1234, KWD) = %q, want 1.234", got)
	}
}

// TestParseRefusesTooManyDecimals: a form that accepts 19.999 dollars has to decide
// what it meant, and deciding silently is how a price ends up different from what
// was typed.
func TestParseRefusesTooManyDecimals(t *testing.T) {
	for _, text := range []string{"19.999", "0.001", "1.234"} {
		if _, err := money.Parse(text, "USD"); err == nil {
			t.Errorf("Parse(%q, USD) was accepted", text)
		}
	}
}

// TestParseRefusesRubbish.
func TestParseRefusesRubbish(t *testing.T) {
	for _, text := range []string{"abc", "1.2.3", "1..2", "12x", "1,2.3.4", "-", "."} {
		if got, err := money.Parse(text, "USD"); err == nil {
			t.Errorf("Parse(%q) = %d and no error", text, got)
		}
	}
}

// TestParseAcceptsWhatPeoplePaste: thousands separators, a currency symbol and a
// sign are all things a person copies from somewhere.
func TestParseAcceptsWhatPeoplePaste(t *testing.T) {
	cases := map[string]money.Amount{
		"1,234.56":  123456,
		"$1,234.56": 123456,
		"+5.00":     500,
		"-5.00":     -500,
		" 12.50 ":   1250,
		"1_000.00":  100000,
	}
	for text, want := range cases {
		got, err := money.Parse(text, "USD")
		if err != nil {
			t.Errorf("Parse(%q): %v", text, err)
			continue
		}
		if got != want {
			t.Errorf("Parse(%q) = %d, want %d", text, got, want)
		}
	}
}

// TestFormatRoundTripsThroughParse: what is shown has to be readable back.
func TestFormatRoundTripsThroughParse(t *testing.T) {
	amounts := []money.Amount{0, 1, 99, 100, 1999, 100000, -500}

	for _, amount := range amounts {
		for _, currency := range []string{"USD", "JPY", "KWD"} {
			plain := money.FormatPlain(amount, currency)

			back, err := money.Parse(plain, currency)
			if err != nil {
				t.Errorf("Parse(FormatPlain(%d, %s)) failed: %v", amount, currency, err)
				continue
			}
			if back != amount {
				t.Errorf("round trip of %d %s gave %d (via %q)", amount, currency, back, plain)
			}
		}
	}
}

// TestFormatIsARendering: the symbol and the decimal point are what a person sees.
func TestFormatIsARendering(t *testing.T) {
	cases := []struct {
		amount   money.Amount
		currency string
		want     string
	}{
		{1999, "USD", "$19.99"},
		{0, "USD", "$0.00"},
		{100000, "USD", "$1,000.00"}, // no thousands separator is applied; see below
		{1000, "JPY", "¥1000"},
		{1234, "KWD", "KD1.234"},
		{-500, "USD", "-$5.00"},
	}

	for _, tc := range cases {
		got := money.Format(tc.amount, tc.currency)
		// The thousands separator case is asserted separately, because the format
		// is deliberately plain.
		if tc.want == "$1,000.00" {
			if got != "$1000.00" {
				t.Errorf("Format(%d, %s) = %q, want $1000.00", tc.amount, tc.currency, got)
			}
			continue
		}
		if got != tc.want {
			t.Errorf("Format(%d, %s) = %q, want %q", tc.amount, tc.currency, got, tc.want)
		}
	}
}

// TestValidatePriceRefusesASaleAboveTheList: a "sale" that costs more than the
// ordinary price is a data-entry error that would otherwise reach a customer, and
// it is invisible in the form because the two fields are filled in separately.
func TestValidatePriceRefusesASaleAboveTheList(t *testing.T) {
	if err := money.ValidatePrice(1999, 1499, "USD"); err != nil {
		t.Errorf("a genuine sale was refused: %v", err)
	}
	if err := money.ValidatePrice(1999, 1999, "USD"); err != nil {
		t.Errorf("a sale equal to the list price was refused: %v", err)
	}
	// Zero means "no sale", not "free".
	if err := money.ValidatePrice(1999, 0, "USD"); err != nil {
		t.Errorf("an absent sale price was refused: %v", err)
	}

	if err := money.ValidatePrice(1999, 2999, "USD"); err == nil {
		t.Error("a sale price above the list price was accepted")
	}
	if err := money.ValidatePrice(-100, 0, "USD"); err == nil {
		t.Error("a negative list price was accepted")
	}
	if err := money.ValidatePrice(1999, -1, "USD"); err == nil {
		t.Error("a negative sale price was accepted")
	}
	if err := money.ValidatePrice(1999, 0, "NOTACURRENCY"); err == nil {
		t.Error("an unrecognised currency was accepted")
	}
}

// TestMulRateRoundsHalfAwayFromZero: where the remaining fraction goes has to be
// decided once and consistently rather than left to the expression.
func TestMulRateRoundsHalfAwayFromZero(t *testing.T) {
	// 1999 * 0.9 = 1799.1, which rounds down.
	if got := money.MulRate(1999, 0.9); got != 1799 {
		t.Errorf("MulRate(1999, 0.9) = %d, want 1799", got)
	}
	// 1995 * 0.5 = 997.5, which rounds up.
	if got := money.MulRate(1995, 0.5); got != 998 {
		t.Errorf("MulRate(1995, 0.5) = %d, want 998", got)
	}
	// A negative amount rounds away from zero, so a discount on a credit is not
	// systematically smaller than on a charge.
	if got := money.MulRate(-1995, 0.5); got != -998 {
		t.Errorf("MulRate(-1995, 0.5) = %d, want -998", got)
	}
	if got := money.Percent(10000, 8.25); got != 825 {
		t.Errorf("Percent(10000, 8.25) = %d, want 825", got)
	}
}

// TestUnknownCurrencyIsFormattableButNotValidatable: a platform that refused to
// show a price because it did not recognise the currency would be worse than one
// that shows it slightly wrong.
func TestUnknownCurrencyIsFormattableButNotValidatable(t *testing.T) {
	if money.Known("XYZ") {
		t.Error("XYZ is reported as known")
	}
	if got := money.Format(1999, "XYZ"); got != "XYZ 19.99" {
		t.Errorf("Format(1999, XYZ) = %q", got)
	}
	if err := money.ValidatePrice(100, 0, "XYZ"); err == nil {
		t.Error("an unknown currency passed price validation")
	}
}

// TestKnownCurrenciesHaveConsistentMetadata: a duplicate or a nonsense exponent
// would be a schema-level mistake.
func TestKnownCurrenciesHaveConsistentMetadata(t *testing.T) {
	for _, code := range []string{"USD", "EUR", "JPY", "KWD", "CNY", "GBP"} {
		if !money.Known(code) {
			t.Errorf("%s is not known", code)
			continue
		}
		info := money.Info(code)
		if info.Exponent < 0 || info.Exponent > 4 {
			t.Errorf("%s has the exponent %d", code, info.Exponent)
		}
		if info.Symbol == "" {
			t.Errorf("%s has no symbol", code)
		}
		if string(info.Code) != code {
			t.Errorf("Info(%s).Code = %q", code, info.Code)
		}
	}

	// Case and whitespace are tolerated, because a currency code arrives from a
	// form and from a settings value.
	for _, code := range []string{"usd", " USD ", "Usd"} {
		if got := money.Info(code); got.Code != "USD" {
			t.Errorf("Info(%q).Code = %q, want USD", code, got.Code)
		}
	}
}
