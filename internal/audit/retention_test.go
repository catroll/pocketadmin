package audit_test

import (
	"testing"
	"time"

	"pocketadmin/internal/audit"
)

// TestZeroRetentionKeepsEverything: the default must be "keep", because a
// retention period that defaults to something short deletes evidence nobody
// meant to discard.
func TestZeroRetentionKeepsEverything(t *testing.T) {
	for _, log := range audit.LogCollections {
		if log.Duration(audit.Retention{}) != 0 {
			t.Errorf("the zero retention for %s is not zero", log.Name)
		}
	}
}

// TestEveryLogHasARetentionPeriod: a log that cannot be configured is a log that
// grows without bound, which is the other way this goes wrong.
func TestEveryLogHasARetentionPeriod(t *testing.T) {
	custom := audit.Retention{
		OperationLog: time.Hour,
		LoginLog:     2 * time.Hour,
		SecurityLog:  3 * time.Hour,
		ExceptionLog: 4 * time.Hour,
	}

	for _, log := range audit.LogCollections {
		if log.Duration(custom) <= 0 {
			t.Errorf("%s has no retention period", log.Name)
		}
	}
}

// TestLogCollectionsCoverEveryLog: pruning is only as complete as this list, and
// a log missing from it grows forever unnoticed.
func TestLogCollectionsCoverEveryLog(t *testing.T) {
	want := map[string]bool{
		"operation_logs":      true,
		"login_logs":          true,
		"security_audit_logs": true,
		"exception_logs":      true,
	}

	got := map[string]bool{}
	for _, log := range audit.LogCollections {
		if got[log.Name] {
			t.Errorf("%s is listed twice", log.Name)
		}
		got[log.Name] = true
	}

	for name := range want {
		if !got[name] {
			t.Errorf("%s is not covered by retention", name)
		}
	}
	for name := range got {
		if !want[name] {
			t.Errorf("retention names %s, which is not one of the four logs", name)
		}
	}
}
