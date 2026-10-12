// Package order holds the order lifecycle: its state machine, the amount breakdown,
// the item snapshots and the timeline.
//
// Two rules run through all of it. An order records what it cost, not what the
// catalogue says today, so every price and name is a snapshot. And every transition
// is checked against a state machine that explains itself, because "the system would
// not let me" is not an answer an operator can give a customer.
package order

import (
	"fmt"
	"sort"
)

// Statuses.
const (
	StatusPendingPayment = "pending_payment"
	StatusPaid           = "paid"
	StatusFulfilling     = "fulfilling"
	StatusCompleted      = "completed"
	StatusCancelled      = "cancelled"
	StatusClosed         = "closed"
	StatusRefunded       = "refunded"
)

// Payment statuses.
const (
	PaymentUnpaid    = "unpaid"
	PaymentPartial   = "partial"
	PaymentPaid      = "paid"
	PaymentRefunding = "refunding"
	PaymentRefunded  = "refunded"
)

// Sources.
const (
	SourceWeb          = "web"
	SourceIOS          = "ios"
	SourceAndroid      = "android"
	SourceAdmin        = "admin"
	SourceSubscription = "subscription"
	SourceImport       = "import"
)

// Statuses returns the vocabulary, matched by the migration's stored select.
func Statuses() []string {
	return []string{StatusPendingPayment, StatusPaid, StatusFulfilling, StatusCompleted,
		StatusCancelled, StatusClosed, StatusRefunded}
}

// PaymentStatuses returns the payment vocabulary.
func PaymentStatuses() []string {
	return []string{PaymentUnpaid, PaymentPartial, PaymentPaid, PaymentRefunding, PaymentRefunded}
}

// transitions is the state machine.
//
// It is a map rather than a switch so the whole graph can be read in one place and
// asserted in one test: a transition rule spread through the code that calls it is a
// rule nobody can check.
var transitions = map[string][]string{
	StatusPendingPayment: {StatusPaid, StatusFulfilling, StatusCancelled, StatusClosed},
	StatusPaid:           {StatusFulfilling, StatusCompleted, StatusRefunded},
	StatusFulfilling:     {StatusCompleted, StatusRefunded},
	StatusCompleted:      {StatusRefunded},

	// The three terminal states. A refunded order stays refunded: a refund is not
	// undone, it is followed by a new order.
	StatusCancelled: {},
	StatusClosed:    {},
	StatusRefunded:  {},
}

// Terminal reports whether a status ends the order's life.
func Terminal(status string) bool {
	return len(transitions[status]) == 0
}

// CanTransition reports whether a move is legal, and explains it when it is not.
//
// The explanation names both ends and, where a different move is the right one, says
// which. An operator told "you cannot cancel a paid order" has learned nothing; one
// told to refund it has learned what to do.
func CanTransition(from, to string) error {
	if from == to {
		return fmt.Errorf("the order is already %s", describe(from))
	}

	allowed, known := transitions[from]
	if !known {
		return fmt.Errorf("%q is not an order status", from)
	}

	for _, candidate := range allowed {
		if candidate == to {
			return nil
		}
	}

	// A paid order is not cancelled; it is refunded. Saying so is the whole point of
	// the state machine, because the two look like the same intent to somebody who
	// has not done it before, and only one of them gives the customer their money.
	if from == StatusPaid || from == StatusFulfilling || from == StatusCompleted {
		if to == StatusCancelled || to == StatusClosed {
			return fmt.Errorf(
				"an order that is %s cannot be %s: refund it instead, so the customer gets their money back",
				describe(from), describe(to))
		}
	}

	if Terminal(from) {
		return fmt.Errorf("an order that is %s is finished and cannot become %s",
			describe(from), describe(to))
	}

	options := make([]string, 0, len(allowed))
	for _, candidate := range allowed {
		options = append(options, describe(candidate))
	}
	sort.Strings(options)

	if len(options) == 0 {
		return fmt.Errorf("a %s order cannot become %s", describe(from), describe(to))
	}
	return fmt.Errorf("an order that is %s cannot become %s; it can become %s",
		describe(from), describe(to), joinWords(options))
}

// describe renders a status for a sentence.
func describe(status string) string {
	switch status {
	case StatusPendingPayment:
		return "awaiting payment"
	case StatusPaid:
		return "paid"
	case StatusFulfilling:
		return "being fulfilled"
	case StatusCompleted:
		return "completed"
	case StatusCancelled:
		return "cancelled"
	case StatusClosed:
		return "closed"
	case StatusRefunded:
		return "refunded"
	default:
		return status
	}
}

// joinWords renders a list as "a, b or c".
func joinWords(items []string) string {
	switch len(items) {
	case 0:
		return "nothing"
	case 1:
		return items[0]
	case 2:
		return items[0] + " or " + items[1]
	default:
		out := ""
		for i, item := range items {
			switch i {
			case len(items) - 1:
				out += " or " + item
			default:
				out += item + ", "
			}
		}
		return out
	}
}

// PaymentStatusFor derives the payment status from the amounts.
//
// It is derived rather than set by each caller because the two must agree: an order
// that says it is paid and shows nothing received is a reconciliation failure
// waiting to happen, and deriving it from the amounts makes disagreement impossible.
func PaymentStatusFor(grandTotal, amountPaid, amountRefunded int64, anyRefundInFlight bool) string {
	if anyRefundInFlight {
		return PaymentRefunding
	}
	if amountRefunded > 0 && amountRefunded >= amountPaid {
		return PaymentRefunded
	}
	if amountPaid <= 0 {
		return PaymentUnpaid
	}
	if amountPaid >= grandTotal {
		return PaymentPaid
	}
	return PaymentPartial
}

// StatusAfterPayment returns the order status a payment moves it to.
//
// A fully paid order becomes paid; a partly paid one stays where it is, because the
// order is not settled and treating it as paid would release stock for goods nobody
// has finished paying for.
func StatusAfterPayment(grandTotal, amountPaid int64, current string) string {
	if amountPaid < grandTotal {
		return current
	}
	if current == StatusPendingPayment {
		return StatusPaid
	}
	return current
}

// NextStates returns the legal next statuses, sorted.
//
// The screen asks the state machine rather than keeping its own list: a second list
// is a second place for the two to disagree, and the one on the button is the one an
// operator would believe.
func NextStates(from string) []string {
	allowed := transitions[from]
	out := make([]string, len(allowed))
	copy(out, allowed)
	sort.Strings(out)
	return out
}

// Events returns the timeline vocabulary, matched by the migration's stored select.
func Events() []string {
	return []string{"created", "payment_started", "paid", "payment_failed", "fulfilling",
		"completed", "cancelled", "closed", "refund_requested", "refunded",
		"note_added", "stock_released"}
}
