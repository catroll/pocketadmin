package integration_test

import (
	"testing"

	"github.com/pocketbase/pocketbase/core"

	"pocketadmin/internal/integration"
	"pocketadmin/internal/testsupport"
)

// withKey sets an encryption key for one test.
//
// The key must be a valid AES length. PocketBase refuses to start with one that is
// not, which is the right behaviour and is why this is exactly 32 bytes rather
// than a memorable phrase.
func withKey(t *testing.T) {
	t.Helper()

	const key = "0123456789abcdef0123456789abcdef"

	// The name the test application was built with, and the fallback. The app
	// reads whichever it was configured with, and a test that set only one of them
	// would be testing the wrong variable -- which is exactly what happened first.
	t.Setenv("pocketadmin_test", key)
	t.Setenv(integration.DefaultEncryptionEnv, key)
}

// TestSecretsRoundTrip: what is stored has to come back.
func TestSecretsRoundTrip(t *testing.T) {
	withKey(t)
	app := testsupport.NewApp(t)

	original := map[string]any{
		"password":   "SuperSecretSMTPPass",
		"username":   "mailer",
		"webhookKey": "whsec_abcdef",
	}

	stored, err := integration.EncryptSecrets(app, original)
	if err != nil {
		t.Fatalf("EncryptSecrets: %v", err)
	}
	if stored == "" {
		t.Fatal("nothing was stored")
	}

	// The point of the exercise: none of the plaintext is in the column.
	for name, value := range original {
		if text, ok := value.(string); ok && len(text) > 3 {
			if contains(stored, text) {
				t.Errorf("the value of %q appears in the stored form", name)
			}
		}
	}

	decoded := integration.DecryptSecrets(app, stored)
	for name, want := range original {
		if got := decoded[name]; got != want {
			t.Errorf("%q round-tripped to %v, want %v", name, got, want)
		}
	}
}

// TestEncryptingTwiceDiffersButDecryptsTheSame records a property that cost a
// round of investigation: AES-GCM uses a random nonce, so the same plaintext
// produces different ciphertext every time.
//
// It means comparing stored values is not a way to tell whether a secret changed.
// The test exists so the next person does not make the same mistake -- the first
// version of the live verification did, and reported a working feature as broken.
func TestEncryptingTwiceDiffersButDecryptsTheSame(t *testing.T) {
	withKey(t)
	app := testsupport.NewApp(t)

	secrets := map[string]any{"password": "same"}

	first, err := integration.EncryptSecrets(app, secrets)
	if err != nil {
		t.Fatalf("EncryptSecrets: %v", err)
	}
	second, err := integration.EncryptSecrets(app, secrets)
	if err != nil {
		t.Fatalf("EncryptSecrets: %v", err)
	}

	if first == second {
		t.Error("two encryptions of the same plaintext are identical, which means the nonce is fixed")
	}
	if integration.DecryptSecrets(app, first)["password"] != "same" {
		t.Error("the first ciphertext did not decrypt")
	}
	if integration.DecryptSecrets(app, second)["password"] != "same" {
		t.Error("the second ciphertext did not decrypt")
	}
}

// TestNoKeyRefusesToStore: storing a payment provider's key in clear because an
// environment variable was not set is a silent downgrade an operator could not
// notice.
func TestNoKeyRefusesToStore(t *testing.T) {
	t.Setenv("pocketadmin_test", "")
	t.Setenv(integration.DefaultEncryptionEnv, "")

	app := testsupport.NewApp(t)

	stored, err := integration.EncryptSecrets(app, map[string]any{"password": "secret"})
	if err == nil {
		t.Fatal("a secret was stored without an encryption key")
	}
	if stored != "" {
		t.Errorf("ciphertext was produced anyway: %q", stored)
	}

	// An empty secret map needs no key: there is nothing to protect.
	if _, err := integration.EncryptSecrets(app, nil); err != nil {
		t.Errorf("storing nothing required a key: %v", err)
	}
}

// TestDecryptingWithoutAKeyYieldsNothingRatherThanFailing: an operator whose key
// changed has to be able to open the screen and re-enter the credentials, which
// they cannot do if the page refuses to load.
func TestDecryptingWithoutAKeyYieldsNothingRatherThanFailing(t *testing.T) {
	withKey(t)
	app := testsupport.NewApp(t)

	stored, err := integration.EncryptSecrets(app, map[string]any{"password": "secret"})
	if err != nil {
		t.Fatalf("EncryptSecrets: %v", err)
	}

	// t.Setenv rather than os.Unsetenv: the latter is process-global and is not
	// restored, so it silently breaks every test that runs after it. The first
	// version of this did that and made an unrelated test fail.
	t.Setenv("pocketadmin_test", "")
	t.Setenv(integration.DefaultEncryptionEnv, "")

	decoded := integration.DecryptSecrets(app, stored)
	if len(decoded) != 0 {
		t.Errorf("secrets were recovered without a key: %v", decoded)
	}
}

// TestMergeSecretsKeepsWhatWasNotResubmitted is the behaviour the edit form
// depends on.
//
// Without it, a form that shows the mask would overwrite every credential with the
// literal mask the first time somebody changed an unrelated field -- and the
// alternative, sending the real secret back to the browser for resubmission, is
// exactly what must not happen.
func TestMergeSecretsKeepsWhatWasNotResubmitted(t *testing.T) {
	stored := map[string]any{"password": "stored-password", "apiKey": "stored-key"}

	cases := []struct {
		name      string
		submitted map[string]any
		wantPass  any
		wantKey   any
	}{
		{
			name:      "the mask means unchanged",
			submitted: map[string]any{"password": integration.Mask, "apiKey": integration.Mask},
			wantPass:  "stored-password",
			wantKey:   "stored-key",
		},
		{
			name:      "an empty field means unchanged",
			submitted: map[string]any{"password": "", "apiKey": "   "},
			wantPass:  "stored-password",
			wantKey:   "stored-key",
		},
		{
			name:      "an absent field means unchanged",
			submitted: map[string]any{},
			wantPass:  "stored-password",
			wantKey:   "stored-key",
		},
		{
			name:      "a new value replaces it",
			submitted: map[string]any{"password": "new-password"},
			wantPass:  "new-password",
			wantKey:   "stored-key",
		},
		{
			name:      "one changes and the other does not",
			submitted: map[string]any{"password": "new-password", "apiKey": integration.Mask},
			wantPass:  "new-password",
			wantKey:   "stored-key",
		},
		{
			name:      "a new credential is added",
			submitted: map[string]any{"webhookSecret": "fresh"},
			wantPass:  "stored-password",
			wantKey:   "stored-key",
		},
	}

	for _, tc := range cases {
		merged := integration.MergeSecrets(stored, tc.submitted)

		if merged["password"] != tc.wantPass {
			t.Errorf("%s: password = %v, want %v", tc.name, merged["password"], tc.wantPass)
		}
		if merged["apiKey"] != tc.wantKey {
			t.Errorf("%s: apiKey = %v, want %v", tc.name, merged["apiKey"], tc.wantKey)
		}
		if tc.name == "a new credential is added" && merged["webhookSecret"] != "fresh" {
			t.Errorf("a newly supplied credential was dropped: %v", merged)
		}
	}
}

// TestSecretsForBrowserCarriesNamesOnly: the form needs to know which credentials
// exist so it can draw them, and nothing about what they are.
func TestSecretsForBrowserCarriesNamesOnly(t *testing.T) {
	stored := map[string]any{"password": "SuperSecret", "apiKey": "SG.real-key"}

	forBrowser := integration.SecretsForBrowser(stored)

	if len(forBrowser) != 2 {
		t.Fatalf("expected two fields, got %v", forBrowser)
	}
	for name, value := range forBrowser {
		if value != integration.Mask {
			t.Errorf("%s carries %q rather than the mask", name, value)
		}
	}
	if _, ok := forBrowser["password"]; !ok {
		t.Error("the field names were not preserved")
	}
}

// TestValidationRefusesUnknownAndMissingFields: the driver's declaration is the
// contract, in both directions.
func TestValidationRefusesUnknownAndMissingFields(t *testing.T) {
	driver, ok := integration.LookupDriver("smtp")
	if !ok {
		t.Fatal("the smtp driver is not registered")
	}

	// A complete configuration.
	complete := map[string]any{"host": "smtp.example.com", "port": "587", "fromAddress": "a@b.c"}
	secrets := map[string]any{"password": "p"}
	if problems := driver.Validate(complete, secrets); len(problems) > 0 {
		t.Errorf("a complete configuration was refused: %v", problems)
	}

	// A missing required field.
	if problems := driver.Validate(map[string]any{"host": "x"}, secrets); len(problems) == 0 {
		t.Error("a configuration missing a required field was accepted")
	}

	// A missing required credential.
	if problems := driver.Validate(complete, map[string]any{}); len(problems) == 0 {
		t.Error("a configuration with no password was accepted")
	}

	// A field the driver does not declare.
	withExtra := map[string]any{"host": "x", "fromAddress": "a@b.c", "notAField": "y"}
	if problems := driver.Validate(withExtra, secrets); len(problems) == 0 {
		t.Error("an undeclared setting was accepted")
	}

	// A credential the driver does not declare.
	if problems := driver.Validate(complete, map[string]any{"password": "p", "extra": "x"}); len(problems) == 0 {
		t.Error("an undeclared credential was accepted")
	}
}

// TestEveryDriverIsCoherent: the catalogue is what the stored select is built
// from, so a driver with no type or a duplicate code would be a schema error.
func TestEveryDriverIsCoherent(t *testing.T) {
	seen := map[string]bool{}

	for _, driver := range integration.AllDrivers() {
		if seen[driver.Code] {
			t.Errorf("the driver code %q is registered twice", driver.Code)
		}
		seen[driver.Code] = true

		if driver.Name == "" {
			t.Errorf("%q has no display name", driver.Code)
		}
		if driver.Description == "" {
			t.Errorf("%q has no description", driver.Code)
		}

		validType := false
		for _, name := range integration.Types() {
			if string(driver.Type) == name {
				validType = true
			}
		}
		if !validType {
			t.Errorf("%q has the unknown type %q", driver.Code, driver.Type)
		}

		// A select's default has to be one of its options.
		for _, field := range append(append([]integration.Field{}, driver.Config...), driver.Secrets...) {
			if field.Kind == integration.FieldSelect && field.Default != "" {
				found := false
				for _, option := range field.Options {
					if option == field.Default {
						found = true
					}
				}
				if !found {
					t.Errorf("%s.%s defaults to %q, which is not one of its options",
						driver.Code, field.Name, field.Default)
				}
			}
			if field.Label == "" {
				t.Errorf("%s.%s has no label", driver.Code, field.Name)
			}
		}
	}

	// The stored select is built from this, so the two cannot drift.
	if len(integration.DriverCodes()) != len(seen) {
		t.Errorf("DriverCodes has %d entries for %d drivers",
			len(integration.DriverCodes()), len(seen))
	}
}

// TestProviderResolutionPrefersTheDefault: the fallback ordering is what makes a
// second provider take over only when the first is disabled.
func TestProviderResolutionPrefersTheDefault(t *testing.T) {
	withKey(t)
	app := testsupport.NewApp(t)

	col, _ := app.FindCollectionByNameOrId(integration.CollectionProviders)

	mk := func(code, name string, isDefault bool, priority int, status string) *core.Record {
		rec := core.NewRecord(col)
		rec.Set("name", name)
		rec.Set("code", code)
		rec.Set("type", string(integration.TypeEmail))
		rec.Set("driver", "smtp")
		rec.Set("status", status)
		rec.Set("isDefault", isDefault)
		rec.Set("priority", priority)
		rec.Set("config", map[string]any{"host": "h", "fromAddress": "a@b.c"})
		secrets, _ := integration.EncryptSecrets(app, map[string]any{"password": "p"})
		rec.Set("secrets", secrets)
		if err := app.Save(rec); err != nil {
			t.Fatalf("save provider: %v", err)
		}
		return rec
	}

	mk("low-priority-number", "High priority", false, 10, integration.StatusEnabled)
	mk("is-the-default", "The default", true, 900, integration.StatusEnabled)

	resolved, err := integration.ResolveFor(app, integration.TypeEmail)
	if err != nil {
		t.Fatalf("ResolveFor: %v", err)
	}
	if resolved.Record.GetString("code") != "is-the-default" {
		t.Errorf("resolved %q; the default should win over priority",
			resolved.Record.GetString("code"))
	}

	// Making the other one default clears the first.
	other, _ := app.FindFirstRecordByFilter(integration.CollectionProviders,
		"code = 'low-priority-number'", nil)
	if err := integration.MakeDefault(app, other); err != nil {
		t.Fatalf("MakeDefault: %v", err)
	}

	peers, _ := app.FindRecordsByFilter(integration.CollectionProviders,
		`type = 'email' && isDefault = true`, "", 0, 0)
	if len(peers) != 1 {
		t.Fatalf("%d providers are default for one type, want 1", len(peers))
	}
	if peers[0].GetString("code") != "low-priority-number" {
		t.Errorf("the wrong provider became the default: %q", peers[0].GetString("code"))
	}

	// Disabling the default falls through to the other enabled provider: that is
	// what a fallback list is for, and it is the behaviour worth asserting.
	peers[0].Set("status", integration.StatusDisabled)
	if err := app.Save(peers[0]); err != nil {
		t.Fatalf("save: %v", err)
	}

	fallback, err := integration.ResolveFor(app, integration.TypeEmail)
	if err != nil {
		t.Fatalf("disabling the default left nothing to fall back to: %v", err)
	}
	if fallback.Record.GetString("code") != "is-the-default" {
		t.Errorf("fell back to %q, want the other enabled provider",
			fallback.Record.GetString("code"))
	}

	// With nothing enabled there is no provider, and the caller is told so rather
	// than being handed a disabled one.
	otherEnabled, _ := app.FindFirstRecordByFilter(integration.CollectionProviders,
		"code = 'is-the-default'", nil)
	otherEnabled.Set("status", integration.StatusDisabled)
	if err := app.Save(otherEnabled); err != nil {
		t.Fatalf("save: %v", err)
	}
	if _, err := integration.ResolveFor(app, integration.TypeEmail); err == nil {
		t.Error("a provider was resolved with every provider disabled")
	}
}

// contains reports whether a string holds a substring.
func contains(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
