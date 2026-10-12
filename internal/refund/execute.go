package refund

import (
	"errors"
	"fmt"
	"time"

	"github.com/pocketbase/pocketbase/core"

	"pocketadmin/internal/catalog"
)

// ErrNotExecutable says a refund is not in a state that can be sent.
var ErrNotExecutable = errors.New("refund: not in a state that can be executed")

// Execution is the outcome of sending a refund to a provider.
type Execution struct {
	// ProviderRefundID is the provider's identifier for the refund.
	ProviderRefundID string

	// Response is the provider's reply, redacted before it is stored.
	Response any

	// FailureCode and FailureMessage describe a refusal.
	FailureCode    string
	FailureMessage string
}

// Executor sends a refund to a payment provider.
//
// It is an interface rather than a call so the workflow can be tested without a
// provider, and so a shop that settles offline can supply one that records the
// decision and does nothing else.
type Executor interface {
	Refund(app core.App, rec *core.Record, amount int64) (Execution, error)
}

// ExecuteResult says what executing did.
type ExecuteResult struct {
	Succeeded bool

	// Retryable is false when the provider refused in a way that retrying cannot fix,
	// such as a card that has since been closed.
	Retryable bool

	Message string
}

// Execute sends an approved refund and applies the outcome.
//
// The order is updated only on success, and the update is a compensating record
// rather than an edit of history: the order's refunded total goes up and its items
// record their refunded quantities. Nothing is deleted and nothing is un-said.
func Execute(app core.App, rec *core.Record, executor Executor, now time.Time) (ExecuteResult, error) {
	if rec == nil {
		return ExecuteResult{}, fmt.Errorf("refund: no refund")
	}

	status := rec.GetString("status")
	if status != StatusApproved && status != StatusFailed && status != StatusProcessing {
		return ExecuteResult{}, fmt.Errorf("%w: it is %s", ErrNotExecutable, status)
	}
	if executor == nil {
		return ExecuteResult{}, fmt.Errorf("refund: no provider is configured to send refunds")
	}

	amount := int64(rec.GetFloat("amount"))

	// The executing state is recorded before the call, so a process that dies mid
	// request leaves a refund that is visibly in flight rather than one that looks
	// untouched and would be sent again.
	rec.Set("status", StatusProcessing)
	rec.Set("attempts", rec.GetInt("attempts")+1)
	if err := app.Save(rec); err != nil {
		return ExecuteResult{}, err
	}

	outcome, err := executor.Refund(app, rec, amount)
	if err != nil {
		rec.Set("status", StatusFailed)
		rec.Set("lastError", err.Error())
		if saveErr := app.Save(rec); saveErr != nil {
			return ExecuteResult{}, saveErr
		}
		return ExecuteResult{Retryable: true, Message: err.Error()}, nil
	}

	if outcome.FailureCode != "" || outcome.FailureMessage != "" {
		rec.Set("status", StatusFailed)
		rec.Set("lastError", outcome.FailureMessage)
		if saveErr := app.Save(rec); saveErr != nil {
			return ExecuteResult{}, saveErr
		}
		return ExecuteResult{
			Retryable: isRetryable(outcome.FailureCode),
			Message:   outcome.FailureMessage,
		}, nil
	}

	if outcome.ProviderRefundID != "" {
		rec.Set("providerRefundId", outcome.ProviderRefundID)
	}
	if outcome.Response != nil {
		rec.Set("providerResponse", outcome.Response)
	}
	rec.Set("status", StatusSucceeded)
	rec.Set("completedAt", now.UTC())
	rec.Set("lastError", "")
	if err := app.Save(rec); err != nil {
		return ExecuteResult{}, err
	}

	// The order's ledger is updated inside a transaction with the item quantities, so
	// an order cannot be left with a refunded total that disagrees with its lines.
	if err := applyToOrder(app, rec, now); err != nil {
		return ExecuteResult{}, err
	}

	return ExecuteResult{Succeeded: true, Message: "the refund was sent"}, nil
}

// isRetryable says whether a provider failure code is worth trying again.
//
// The default is *not* retryable. A provider that refused for a reason nobody has
// classified should be looked at by a person, and retrying an unclassified refusal
// risks sending the same refund twice to a provider that is about to accept it.
func isRetryable(code string) bool {
	switch code {
	case "timeout", "network_error", "rate_limited", "provider_unavailable", "try_again_later":
		return true
	default:
		// An empty code is not a transient failure. It means the provider refused
		// without classifying the refusal, and retrying it risks sending the same
		// refund twice to a provider that is about to accept it.
		return false
	}
}

// applyToOrder records a succeeded refund against its order.
func applyToOrder(app core.App, rec *core.Record, now time.Time) error {
	return app.RunInTransaction(func(tx core.App) error {
		order, err := tx.FindRecordById("orders", rec.GetString("order"))
		if err != nil || order == nil {
			return fmt.Errorf("refund: the refund names an order that does not exist")
		}

		refunded := int64(order.GetFloat("amountRefunded")) + int64(rec.GetFloat("amount"))
		paid := int64(order.GetFloat("amountPaid"))
		grand := int64(order.GetFloat("grandTotal"))

		order.Set("amountRefunded", refunded)
		order.Set("paymentStatus", paymentStatusFor(grand, paid, refunded))

		// A full refund completes the order's life. A partial one leaves it where it
		// is, because the customer still has part of what they bought.
		if refunded >= paid && paid > 0 {
			if order.GetString("status") != "refunded" {
				order.Set("status", "refunded")
			}
		}

		if err := tx.Save(order); err != nil {
			return err
		}

		// The refunded quantity per line, so a later refund by item knows what is
		// left. It is added rather than set, because a partial refund touches only
		// some lines.
		var items []ItemQuantity
		decodeItems(rec.Get("items"), &items)

		for _, want := range items {
			item, err := tx.FindRecordById("order_items", want.OrderItemID)
			if err != nil || item == nil {
				continue
			}

			item.Set("refundedQuantity", item.GetInt("refundedQuantity")+want.Quantity)
			item.Set("refundedAmount", item.GetFloat("refundedAmount")+
				float64(int64(item.GetFloat("unitPrice"))*int64(want.Quantity)))
			if err := tx.Save(item); err != nil {
				return err
			}
		}

		if rec.GetBool("restock") {
			if err := restockItems(tx, rec, items, now); err != nil {
				return err
			}
		}

		return nil
	})
}

// restockItems returns refunded goods to the catalogue.
//
// Only for a refund by item, and only for what was actually chosen. A refund by
// amount says nothing about which goods came back, and guessing would put stock on
// the shelf that may still be in the customer's hands.
func restockItems(app core.App, rec *core.Record, items []ItemQuantity, now time.Time) error {
	if len(items) == 0 {
		return nil
	}

	for _, want := range items {
		item, err := app.FindRecordById("order_items", want.OrderItemID)
		if err != nil || item == nil {
			continue
		}

		variantID := item.GetString("variant")
		if variantID == "" {
			// A product with no variant has no stock ledger entry to return to.
			continue
		}

		variant, err := app.FindRecordById("product_variants", variantID)
		if err != nil || variant == nil {
			continue
		}

		// The ledger is written first, so the cached balance is never ahead of the
		// record of what happened.
		if err := catalog.ApplyStock(app, variant, catalog.Movement{
			Delta:   want.Quantity,
			Reason:  catalog.ReasonReturn,
			RefType: "refund",
			RefID:   rec.Id,
			Note:    "refund " + rec.GetString("refundNo"),
		}); err != nil {
			return err
		}
	}
	return nil
}

// paymentStatusFor mirrors the order package's derivation.
func paymentStatusFor(grandTotal, amountPaid, amountRefunded int64) string {
	if amountRefunded > 0 && amountRefunded >= amountPaid {
		return "refunded"
	}
	if amountPaid <= 0 {
		return "unpaid"
	}
	if amountPaid >= grandTotal {
		return "paid"
	}
	return "partial"
}

// Retryable returns the failed refunds worth trying again.
func Retryable(app core.App) ([]*core.Record, error) {
	return app.FindRecordsByFilter(CollectionRefunds,
		"status = {:failed} && attempts < {:max}", "created", 100, 0,
		map[string]any{"failed": StatusFailed, "max": 5})
}

// ExpiringRefundNo builds the next refund number.
func NextRefundNo(app core.App, prefix string, now time.Time) (string, error) {
	if prefix == "" {
		prefix = "REF"
	}

	count, err := app.CountRecords(CollectionRefunds)
	if err != nil {
		return "", err
	}

	base := fmt.Sprintf("%s-%s-%05d", prefix, now.Format("200601"), count+1)
	candidate := base
	for attempt := 0; attempt < 100; attempt++ {
		existing, _ := app.FindFirstRecordByFilter(CollectionRefunds,
			"refundNo = {:n}", map[string]any{"n": candidate})
		if existing == nil {
			return candidate, nil
		}
		candidate = fmt.Sprintf("%s-%05d", base, attempt+2)
	}
	return fmt.Sprintf("%s-%d", base, now.UnixNano()%100000), nil
}
