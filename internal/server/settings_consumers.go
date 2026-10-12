package server

import (
	"context"
	"strings"
	"time"

	"pocketadmin/internal/audit"
	"pocketadmin/internal/settings"
	"pocketadmin/internal/storage"
)

// The settings module is the real source for the two seams that earlier modules
// had to default: the file library's upload policy and the audit retention
// periods. Both were defined by the module that needed them (docs/decisions.md
// D-012) and are filled in here.

// uploadPolicy builds the storage policy from the back-office settings.
//
// It falls back to configuration and then to the compiled-in default, so a
// missing settings row leaves the library working rather than refusing every
// upload.
func (s *Server) uploadPolicy() storage.Policy {
	policy := storage.DefaultPolicy()

	reg := settings.Admin()

	if size := s.settings.IntGet(reg, "storage", "uploadMaxSize"); size > 0 {
		policy.MaxSize = int64(size)
	}

	// The allow-list is a JSON array, so it arrives as a slice of any.
	if raw := s.settings.Get(reg, "storage", "uploadAllowedExtensions"); raw != nil {
		if list := stringSlice(raw); len(list) > 0 {
			policy.AllowedExtensions = list
		}
	}

	policy.MaxImageWidth = s.settings.IntGet(reg, "storage", "imageMaxWidth")
	policy.MaxImageHeight = s.settings.IntGet(reg, "storage", "imageMaxHeight")

	return policy
}

// auditRetention builds the retention periods from the back-office settings.
//
// Zero means keep forever at both layers, so an unset or zero value preserves
// evidence rather than discarding it.
func (s *Server) auditRetention() audit.Retention {
	reg := settings.Admin()

	return audit.Retention{
		OperationLog: days(s.settings.IntGet(reg, "audit", "operationLogRetentionDays")),
		LoginLog:     days(s.settings.IntGet(reg, "audit", "loginLogRetentionDays")),
		SecurityLog:  days(s.settings.IntGet(reg, "audit", "securityLogRetentionDays")),
		ExceptionLog: days(s.settings.IntGet(reg, "audit", "exceptionLogRetentionDays")),
	}
}

// days converts a day count to a duration, treating zero as "keep forever".
func days(n int) time.Duration {
	if n <= 0 {
		return 0
	}
	return time.Duration(n) * 24 * time.Hour
}

// stringSlice renders a decoded JSON value as a list of strings.
func stringSlice(value any) []string {
	switch v := value.(type) {
	case []string:
		return v
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			text := strings.TrimSpace(settings.ScalarString(item))
			if text == "" {
				continue
			}
			out = append(out, text)
		}
		return out
	case string:
		// A comma-separated list is what a person types when a form has no
		// repeating field.
		parts := strings.Split(v, ",")
		out := make([]string, 0, len(parts))
		for _, part := range parts {
			if text := strings.TrimSpace(part); text != "" {
				out = append(out, text)
			}
		}
		return out
	default:
		return nil
	}
}

// storageProviderCode returns the provider that should hold new uploads.
//
// It reads the back-office setting first and falls back to the seeded local disk
// provider, so an installation that has never opened the integrations screen still
// records where its files are rather than leaving the column empty.
func (s *Server) storageProviderCode() string {
	reg := settings.Admin()
	if code := s.settings.StringGet(reg, "storage", "provider"); code != "" {
		return code
	}
	return "local"
}

// contextWithTimeout bounds an outbound request made during a page render.
func contextWithTimeout(d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), d)
}
