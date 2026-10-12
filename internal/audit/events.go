// Package audit holds the security audit log's vocabulary and the redaction
// rules that decide what may be written into any log.
//
// The event names are constants for the same reason permission points are: the
// collection stores them as a closed select, so a value that is not declared
// cannot be written at all. That is the right property for an audit catalogue --
// the set of auditable events should be knowable and reviewable rather than
// whatever a caller happens to pass -- but it means a typo silently loses the
// entry. As a constant it does not compile, and a drift test asserts the
// constants and the stored enum agree.
package audit

// Event is a security-relevant occurrence.
type Event string

// The auditable events.
//
// The first block is the catalogue named in database-design.md § 3.9. The second
// is what this platform actually emits beyond it: an audit log that cannot record
// that multi-factor authentication was switched off would be missing the events
// most worth reviewing, so the catalogue is extended rather than truncated. The
// extension is recorded in docs/decisions.md D-011.
const (
	EventPermissionDenied Event = "permission_denied"
	EventPasswordChanged  Event = "password_changed"
	EventPasswordReset    Event = "password_reset"
	EventRoleChanged      Event = "role_changed"
	EventProfileAttached  Event = "profile_attached"
	EventProfileDetached  Event = "profile_detached"
	EventAccountLocked    Event = "account_locked"
	EventSessionRevoked   Event = "session_revoked"
	EventSettingsChanged  Event = "settings_changed"

	// Scheduled tasks. These are separate events rather than one "job changed"
	// because the question an investigator asks is which of them happened: a
	// schedule change is a configuration change, a manual run is somebody
	// executing work now, and pruning is the audit log deleting its own entries.
	EventJobScheduleChanged Event = "job_schedule_changed"
	EventJobStateChanged    Event = "job_state_changed"
	EventJobRunManually     Event = "job_run_manually"
	EventRetentionPruned    Event = "retention_pruned"

	EventAPIKeyCreated        Event = "api_key_created"
	EventAPIKeyRevoked        Event = "api_key_revoked"
	EventDataExported         Event = "data_exported"
	EventImpersonationStarted Event = "impersonation_started"
	EventLoginAnomaly         Event = "login_anomaly"
	EventRefundApproved       Event = "refund_approved"
	EventManualPaymentMarked  Event = "manual_payment_marked"
	EventCouponVoided         Event = "coupon_voided"

	EventImpersonationStopped Event = "impersonation_stopped"
	EventAccountStatusChanged Event = "account_status_changed"
	EventAccountDeleted       Event = "account_deleted"
	EventAccountRestored      Event = "account_restored"
	EventMFAEnrolled          Event = "mfa_enrolled"
	EventMFADisabled          Event = "mfa_disabled"
	EventRecoveryCodeUsed     Event = "recovery_code_used"
	EventRecoveryRegenerated  Event = "recovery_codes_regenerated"
	EventFileUploaded         Event = "file_uploaded"
	EventFileDeleted          Event = "file_deleted"
	EventExportGenerated      Event = "export_generated"
)

// Severity ranks an entry for review.
type Severity string

// Severities.
const (
	SeverityInfo     Severity = "info"
	SeverityWarning  Severity = "warning"
	SeverityCritical Severity = "critical"
)

// ActorType says what kind of principal acted.
type ActorType string

// Actor types.
const (
	ActorUser   ActorType = "user"
	ActorSystem ActorType = "system"
	ActorAPIKey ActorType = "api_key"
)

// AllEvents returns every declared event, in a stable order.
//
// The seed migration and the drift test both walk it, so an event that is
// declared here and absent from the stored enum is caught rather than discovered
// when someone tries to record it.
func AllEvents() []Event {
	return []Event{
		EventPermissionDenied,
		EventPasswordChanged,
		EventPasswordReset,
		EventRoleChanged,
		EventProfileAttached,
		EventProfileDetached,
		EventAccountLocked,
		EventSessionRevoked,
		EventSettingsChanged,
		EventJobScheduleChanged,
		EventJobStateChanged,
		EventJobRunManually,
		EventRetentionPruned,
		EventAPIKeyCreated,
		EventAPIKeyRevoked,
		EventDataExported,
		EventImpersonationStarted,
		EventLoginAnomaly,
		EventRefundApproved,
		EventManualPaymentMarked,
		EventCouponVoided,
		EventImpersonationStopped,
		EventAccountStatusChanged,
		EventAccountDeleted,
		EventAccountRestored,
		EventMFAEnrolled,
		EventMFADisabled,
		EventRecoveryCodeUsed,
		EventRecoveryRegenerated,
		EventFileUploaded,
		EventFileDeleted,
		EventExportGenerated,
	}
}

// EventStrings returns every declared event as strings, for a schema enum.
func EventStrings() []string {
	events := AllEvents()
	out := make([]string, 0, len(events))
	for _, e := range events {
		out = append(out, string(e))
	}
	return out
}

// Valid reports whether an event is declared.
func Valid(e Event) bool {
	for _, declared := range AllEvents() {
		if declared == e {
			return true
		}
	}
	return false
}
