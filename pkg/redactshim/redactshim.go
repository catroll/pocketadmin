// Package redactshim exposes the audit module's redactor to packages that would
// otherwise have to import the whole audit module to scrub a payload.
//
// It is a deliberate one-line seam rather than a move: the redactor belongs with
// the audit log that depends on it, and an integration payload is the other place
// a credential travels.
package redactshim

import "pocketadmin/internal/audit"

// Redact removes secret-looking values from a structure.
func Redact(value any) any { return audit.Redact(value) }
