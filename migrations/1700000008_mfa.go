package migrations

import (
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/migrations"
)

// mfaMigration adds the fields for a time-based one-time password second factor.
//
// PocketBase ships MFA, but only in the sense of "require a second factor the
// server already knows how to send": its _otps collection holds a code delivered
// out of band, and its _mfas collection holds the challenge between the first and
// second step. Neither stores an authenticator secret, and there is no TOTP
// anywhere in the module. An authenticator app is the factor that needs no mail
// transport, no SMS provider and no phone number, so it is the one worth having
// here; the computation lives in internal/identity.
//
// The secret fields are Hidden. That is not the whole protection and should not
// be mistaken for it: Hidden excludes a field from PocketBase's public export,
// which is what a non-superuser request gets, but a superuser request returns the
// full record. A superuser is already omnipotent -- they could read the database
// or impersonate anyone -- so this is a boundary worth naming rather than
// overstating. What actually keeps the secret away from ordinary callers is that
// these collections carry no API rules at all, so only a superuser can reach them.
//
// The panel reads them through core.App, which bypasses both.
func init() {
	migrations.Register(func(app core.App) error {
		col := findCollection(app, "users")
		if col == nil {
			return nil
		}

		addField(col, &core.BoolField{Name: "mfaEnabled"})

		// The confirmed secret, and the candidate during enrolment. Keeping them
		// apart means an abandoned enrolment cannot lock anyone out: the candidate
		// only becomes the real secret once a code from it has been verified.
		addField(col, &core.TextField{Name: "totpSecret", Hidden: true, Max: 128})
		addField(col, &core.TextField{Name: "totpPendingSecret", Hidden: true, Max: 128})

		addField(col, &core.DateField{Name: "totpEnrolledAt"})

		// Recovery codes are stored as digests, never in the clear.
		addField(col, jsonField("recoveryCodeHashes", 20000))

		return app.Save(col)
	}, func(app core.App) error {
		col := findCollection(app, "users")
		if col == nil {
			return nil
		}
		for _, name := range []string{
			"mfaEnabled", "totpSecret", "totpPendingSecret", "totpEnrolledAt", "recoveryCodeHashes",
		} {
			col.Fields.RemoveByName(name)
		}
		return app.Save(col)
	}, "1700000008_mfa.go")
}
