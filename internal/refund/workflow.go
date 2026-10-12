package refund

import (
	"fmt"
	"strings"
	"time"

	"github.com/pocketbase/pocketbase/core"
)

// Policy is the refund configuration.
type Policy struct {
	// AutoApproveThreshold is the amount at or below which a refund is approved as
	// soon as it is requested. Zero disables auto-approval entirely, which is the
	// safe default: a shop that has not thought about it should have a person look.
	AutoApproveThreshold int64

	// RestockOnFullRefund restores the stock of a fully refunded order.
	RestockOnFullRefund bool
}

// DefaultPolicy is the policy when none is configured.
func DefaultPolicy() Policy {
	return Policy{AutoApproveThreshold: 0, RestockOnFullRefund: true}
}

// Decision says what happened when a refund was reviewed.
type Decision struct {
	// AutoApproved is true when the policy approved it without a person.
	AutoApproved bool

	// Reason explains a refusal to decide.
	Reason string
}

// Approve approves a refund.
//
// Three rules, and each is here rather than in the screen because a screen can be
// bypassed and this cannot:
//
//   - Nobody approves their own request. An approval workflow whose approver may be
//     the requester is a formality, and the whole reason the two permissions are
//     separate is that they are meant to be held by different people.
//   - The cap is re-checked inside a transaction. Two approvals that each look fine
//     on their own are an overpayment when they happen at the same moment, and the
//     check that ran when the request was made is outside any transaction.
//   - Only a pending refund is decided. Approving an already-approved one would
//     commit the money twice.
func Approve(app core.App, rec *core.Record, approverID, comment string, policy Policy) (Decision, error) {
	if rec == nil {
		return Decision{}, fmt.Errorf("refund: no refund")
	}
	if rec.GetString("status") != StatusPending {
		return Decision{}, fmt.Errorf("refund: only a pending refund can be approved, and this one is %s",
			rec.GetString("status"))
	}

	// A self-approval is refused even when the policy would have auto-approved it.
	// The rule is about who decided, and an automatic decision taken because the
	// requester is the only person available is still the requester deciding.
	if approverID != "" && approverID == rec.GetString("requestedBy") {
		return Decision{}, fmt.Errorf(
			"refund: a refund cannot be approved by the person who requested it; ask somebody else")
	}

	amount := int64(rec.GetFloat("amount"))
	auto := policy.AutoApproveThreshold > 0 && amount <= policy.AutoApproveThreshold

	err := app.RunInTransaction(func(tx core.App) error {
		// Reload inside the transaction. The record passed in may have been read
		// before another approval committed, and the cap is only meaningful against
		// the state the database is in now.
		current, err := tx.FindRecordById(CollectionRefunds, rec.Id)
		if err != nil || current == nil {
			return fmt.Errorf("refund: the refund vanished")
		}
		if current.GetString("status") != StatusPending {
			return fmt.Errorf("refund: another decision reached this refund first")
		}

		remaining, err := Refundable(tx, current.GetString("order"), current.Id)
		if err != nil {
			return err
		}
		if int64(current.GetFloat("amount")) > remaining {
			return fmt.Errorf(
				"refund: approving %d would exceed the %d left to refund on this order; "+
					"another refund has been approved since this one was requested",
				int64(current.GetFloat("amount")), remaining)
		}

		current.Set("status", StatusApproved)
		current.Set("autoApproved", auto)
		current.Set("reviewedAt", time.Now().UTC())
		if comment != "" {
			current.Set("reviewComment", comment)
		}
		// An automatic approval has no reviewer, and recording the requester as one
		// would be a lie about who decided.
		if !auto && approverID != "" {
			current.Set("reviewedBy", approverID)
		}

		if err := tx.Save(current); err != nil {
			return err
		}

		// The caller's record is updated so it reflects what was committed without a
		// second read.
		rec.Set("status", current.GetString("status"))
		rec.Set("autoApproved", current.GetBool("autoApproved"))
		rec.Set("reviewedAt", current.Get("reviewedAt"))
		rec.Set("reviewedBy", current.Get("reviewedBy"))
		return nil
	})
	if err != nil {
		return Decision{}, err
	}

	return Decision{AutoApproved: auto}, nil
}

// Reject rejects a refund.
//
// A rejection rejects it, whatever the capacity was: capacity is freed either way,
// and refusing to record a decision because the money would have been available is
// not a reason anyone would give.
func Reject(app core.App, rec *core.Record, reviewerID, comment string) error {
	if rec == nil {
		return fmt.Errorf("refund: no refund")
	}
	if rec.GetString("status") != StatusPending {
		return fmt.Errorf("refund: only a pending refund can be rejected, and this one is %s",
			rec.GetString("status"))
	}
	if reviewerID != "" && reviewerID == rec.GetString("requestedBy") {
		return fmt.Errorf("refund: a refund cannot be reviewed by the person who requested it")
	}
	if strings.TrimSpace(comment) == "" {
		// A refusal with no reason is a dead end for the customer and for whoever
		// answers the complaint.
		return fmt.Errorf("refund: rejecting a refund requires a reason")
	}

	rec.Set("status", StatusRejected)
	rec.Set("reviewedAt", time.Now().UTC())
	rec.Set("reviewComment", strings.TrimSpace(comment))
	if reviewerID != "" {
		rec.Set("reviewedBy", reviewerID)
	}

	return app.Save(rec)
}

// Cancel withdraws a refund that has not been executed.
func Cancel(app core.App, rec *core.Record, reason string) error {
	if rec == nil {
		return fmt.Errorf("refund: no refund")
	}
	switch rec.GetString("status") {
	case StatusPending, StatusApproved, StatusFailed:
	default:
		return fmt.Errorf("refund: a %s refund cannot be withdrawn", rec.GetString("status"))
	}

	rec.Set("status", StatusCancelled)
	if reason != "" {
		rec.Set("reviewComment", reason)
	}
	return app.Save(rec)
}
