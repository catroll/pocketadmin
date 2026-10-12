package integration

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/security"
)

// Mask is what the browser sees in place of a stored secret.
//
// It is a fixed string rather than a partially revealed one. Showing the first
// and last characters of a credential is a common habit and a poor one: it leaks
// length and shape, it is enough to confirm a guess, and it gives an operator the
// false impression that they can recognise the value.
const Mask = "********"

// ErrNoEncryptionKey is returned when a secret would have to be stored without
// one.
var ErrNoEncryptionKey = fmt.Errorf(
	"integration: no encryption key is configured, so secrets cannot be stored safely")

// encryptionKey returns the application's encryption key, or "".
//
// It is the same key PocketBase uses for its own settings, so an installation that
// has already set POCKETBASE_ENCRYPTION_KEY needs no further configuration.
func encryptionKey(app core.App) string {
	name := strings.TrimSpace(app.EncryptionEnv())
	if name == "" {
		// An app built without the field returns the empty string rather than a
		// default, and os.Getenv("") is always empty -- so a fallback is what stops
		// a configured key from being invisible.
		name = DefaultEncryptionEnv
	}
	return strings.TrimSpace(os.Getenv(name))
}

// DefaultEncryptionEnv is the variable consulted when the application does not
// name one. It is PocketBase's own default, so an installation already using it
// for settings encryption needs no further configuration.
const DefaultEncryptionEnv = "POCKETBASE_ENCRYPTION_KEY"

// EncryptSecrets turns a plaintext secret map into the stored form.
//
// It refuses rather than falling back to plaintext. Storing a payment provider's
// key in clear because an environment variable was not set is a silent downgrade,
// and the operator would have no way to notice: the provider would work, and the
// credentials would be readable by anyone with the database.
func EncryptSecrets(app core.App, secrets map[string]any) (string, error) {
	if len(secrets) == 0 {
		return "", nil
	}

	key := encryptionKey(app)
	if key == "" {
		return "", ErrNoEncryptionKey
	}

	encoded, err := json.Marshal(secrets)
	if err != nil {
		return "", fmt.Errorf("integration: encode secrets: %w", err)
	}

	cipherText, err := security.Encrypt(encoded, key)
	if err != nil {
		return "", fmt.Errorf("integration: encrypt secrets: %w", err)
	}
	return cipherText, nil
}

// DecryptSecrets turns the stored form back into a map.
//
// A failure to decrypt is logged and treated as "no secrets", not as an error that
// stops the page rendering. The usual cause is that the encryption key changed,
// and an operator needs to be able to open the screen and re-enter the credentials
// -- which they cannot do if the screen refuses to load.
func DecryptSecrets(app core.App, stored string) map[string]any {
	if strings.TrimSpace(stored) == "" {
		return map[string]any{}
	}

	key := encryptionKey(app)
	if key == "" {
		slog.Error("stored integration secrets cannot be read: no encryption key is configured")
		return map[string]any{}
	}

	plain, err := security.Decrypt(stored, key)
	if err != nil {
		slog.Error("stored integration secrets could not be decrypted; they will need to be re-entered",
			"error", err)
		return map[string]any{}
	}

	var secrets map[string]any
	if err := json.Unmarshal(plain, &secrets); err != nil {
		slog.Error("stored integration secrets are not readable", "error", err)
		return map[string]any{}
	}
	return secrets
}

// MergeSecrets applies a submission to the stored secrets.
//
// A field the form left empty or at the mask keeps its stored value, and only a
// field carrying something new replaces it. Without that rule the edit form would
// have to send the real secret back to the browser for the operator to resubmit,
// which is exactly what must not happen -- and a form that showed the mask would
// otherwise overwrite every credential with the literal string "********" the
// first time somebody changed an unrelated field.
//
// An explicit empty string clears a secret, which is a different intent from
// leaving the field alone; the screens express that with a separate control.
func MergeSecrets(stored map[string]any, submitted map[string]any) map[string]any {
	out := make(map[string]any, len(stored)+len(submitted))
	for k, v := range stored {
		out[k] = v
	}

	for name, value := range submitted {
		text, isText := value.(string)

		// The mask means "unchanged". It is what the browser echoed back.
		if isText && (text == Mask || strings.TrimSpace(text) == "") {
			continue
		}
		out[name] = value
	}
	return out
}

// SecretsForBrowser returns the secret field names with the mask in place of their
// values.
//
// Only the names cross to the browser, never the values. The form needs to know
// which fields exist so it can draw them, and it needs to show that something is
// stored, and that is the whole of what it needs.
func SecretsForBrowser(stored map[string]any) map[string]string {
	out := make(map[string]string, len(stored))
	for name := range stored {
		out[name] = Mask
	}
	return out
}
