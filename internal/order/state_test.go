package order_test

import (
	"strings"
	"testing"

	"pocketadmin/internal/order"
)

// TestLegalTransitions walks the whole graph, so a rule cannot be changed in one
// place without the test noticing.
func TestLegalTransitions(t *testing.T) {
	legal := map[string][]string{
		order.StatusPendingPayment: {order.StatusPaid, order.StatusFulfilling, order.StatusCancelled, order.StatusClosed},
		order.StatusPaid:           {order.StatusFulfilling, order.StatusCompleted, order.StatusRefunded},
		order.StatusFulfilling:     {order.StatusCompleted, order.StatusRefunded},
		order.StatusCompleted:      {order.StatusRefunded},
		order.StatusCancelled:      {},
		order.StatusClosed:         {},
		order.StatusRefunded:       {},
	}

	for from, targets := range legal {
		for _, to := range targets {
			if err := order.CanTransition(from, to); err != nil {
				t.Errorf("%s -> %s was refused: %v", from, to, err)
			}
		}
	}
}

// TestAPaidOrderIsRefundedRatherThanCancelled is the rule that has money in it.
//
// The two look like the same intent to somebody who has not done it before, and only
// one of them gives the customer their money back.
func TestAPaidOrderIsRefundedRatherThanCancelled(t *testing.T) {
	for _, from := range []string{order.StatusPaid, order.StatusFulfilling, order.StatusCompleted} {
		for _, to := range []string{order.StatusCancelled, order.StatusClosed} {
			err := order.CanTransition(from, to)
			if err == nil {
				t.Errorf("%s -> %s was allowed", from, to)
				continue
			}

			// The refusal has to say what to do instead, or the operator has learned
			// nothing.
			if !strings.Contains(err.Error(), "refund") {
				t.Errorf("%s -> %s was refused without suggesting a refund: %v", from, to, err)
			}
		}
	}
}

// TestTerminalStatesAreTerminal.
func TestTerminalStatesAreTerminal(t *testing.T) {
	for _, status := range []string{order.StatusCancelled, order.StatusClosed, order.StatusRefunded} {
		if !order.Terminal(status) {
			t.Errorf("%s is not reported as terminal", status)
		}

		for _, target := range order.Statuses() {
			if target == status {
				continue
			}
			if err := order.CanTransition(status, target); err == nil {
				t.Errorf("a %s order was allowed to become %s", status, target)
			}
		}
	}
}

// TestTheRefusalNamesTheOptions: "the system would not let me" is not an answer an
// operator can give a customer.
func TestTheRefusalNamesTheOptions(t *testing.T) {
	err := order.CanTransition(order.StatusPendingPayment, order.StatusRefunded)
	if err == nil {
		t.Fatal("an unpaid order was refunded")
	}

	message := err.Error()
	// Either end is named, and the legal moves are listed.
	for _, want := range []string{"awaiting payment", "refunded"} {
		if !strings.Contains(message, want) {
			t.Errorf("the message does not mention %q: %s", want, message)
		}
	}
	for _, option := range []string{"paid", "cancelled"} {
		if !strings.Contains(message, option) {
			t.Errorf("the message does not offer %q: %s", option, message)
		}
	}
}

// TestMovingToTheSameStatusIsRefused: it is a no-op, and reporting it as a
// transition would put a row in the timeline saying nothing happened.
func TestMovingToTheSameStatusIsRefused(t *testing.T) {
	if err := order.CanTransition(order.StatusPaid, order.StatusPaid); err == nil {
		t.Error("moving to the current status was allowed")
	}
}

// TestAnUnknownStatusIsRefused.
func TestAnUnknownStatusIsRefused(t *testing.T) {
	if err := order.CanTransition("something_else", order.StatusPaid); err == nil {
		t.Error("an unknown status was accepted")
	}
	if err := order.CanTransition(order.StatusPaid, "something_else"); err == nil {
		t.Error("an unknown target was accepted")
	}
}

// TestPaymentStatusIsDerivedFromTheAmounts: an order that says it is paid and shows
// nothing received is a reconciliation failure waiting to happen.
func TestPaymentStatusIsDerivedFromTheAmounts(t *testing.T) {
	cases := []struct {
		grand, paid, refunded int64
		refunding             bool
		want                  string
	}{
		{1000, 0, 0, false, order.PaymentUnpaid},
		{1000, 400, 0, false, order.PaymentPartial},
		{1000, 1000, 0, false, order.PaymentPaid},
		{1000, 1200, 0, false, order.PaymentPaid}, // overpaid is paid
		{1000, 1000, 400, false, order.PaymentPaid},
		{1000, 1000, 1000, false, order.PaymentRefunded},
		{1000, 1000, 0, true, order.PaymentRefunding},
		{0, 0, 0, false, order.PaymentUnpaid},
	}

	for _, tc := range cases {
		got := order.PaymentStatusFor(tc.grand, tc.paid, tc.refunded, tc.refunding)
		if got != tc.want {
			t.Errorf("grand %d paid %d refunded %d (refunding %v) = %q, want %q",
				tc.grand, tc.paid, tc.refunded, tc.refunding, got, tc.want)
		}
	}
}

// TestAPartlyPaidOrderStaysWhereItIs: treating it as paid would release stock for
// goods nobody has finished paying for.
func TestAPartlyPaidOrderStaysWhereItIs(t *testing.T) {
	if got := order.StatusAfterPayment(1000, 400, order.StatusPendingPayment); got != order.StatusPendingPayment {
		t.Errorf("a partly paid order became %q", got)
	}
	if got := order.StatusAfterPayment(1000, 1000, order.StatusPendingPayment); got != order.StatusPaid {
		t.Errorf("a fully paid order became %q, want paid", got)
	}
	// An order already further along is not moved backwards by a late payment.
	if got := order.StatusAfterPayment(1000, 1000, order.StatusFulfilling); got != order.StatusFulfilling {
		t.Errorf("a fulfilling order became %q", got)
	}
}
