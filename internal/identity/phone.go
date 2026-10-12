// Package identity holds the rules of the account model that more than one layer
// needs: the migration that constrains the data and the handler that writes it
// must agree, or the constraint and the application would disagree about what
// counts as the same value.
package identity

// NormalisePhone strips the separators people type and keeps a leading plus.
//
// Uniqueness has to compare the number rather than its formatting: "+86 138 0000
// 0000" and "+8613800000000" are the same phone, and treating them as different
// would defeat the constraint. Normalising on write also means the stored value
// is what a notification transport can dial.
func NormalisePhone(raw string) string {
	out := make([]rune, 0, len(raw))
	for i, r := range raw {
		switch {
		case r >= '0' && r <= '9':
			out = append(out, r)
		case r == '+' && i == 0:
			out = append(out, r)
		}
	}
	return string(out)
}
