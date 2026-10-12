package identity_test

import (
	"strings"
	"testing"
	"time"

	"pocketadmin/internal/identity"
)

// rfcSecret is the seed from RFC 6238 Appendix B: the ASCII string
// "12345678901234567890", base32 encoded without padding.
const rfcSecret = "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ"

// TestTOTPMatchesRFC6238Vectors checks the implementation against the published
// test vectors.
//
// Self-consistency would prove nothing here: an implementation that computed the
// wrong function would still verify its own codes. The only meaningful check is
// that it produces the codes every authenticator application produces.
//
// The published vectors are eight digits; this implementation is fixed at the
// six that authenticator apps use, which are the low six digits of the same
// value because the reduction is a modulo.
func TestTOTPMatchesRFC6238Vectors(t *testing.T) {
	// Every row is from RFC 6238 Appendix B; the expected value is the low six
	// digits of the published eight, since the reduction is a modulo.
	cases := []struct {
		unix int64
		want string
	}{
		{59, "287082"},
		{1111111109, "081804"},
		{1111111111, "050471"},
		{1234567890, "005924"},
		{2000000000, "279037"},
		{20000000000, "353130"},
	}

	for _, tc := range cases {
		at := time.Unix(tc.unix, 0).UTC()

		got, err := identity.TOTPCode(rfcSecret, at)
		if err != nil {
			t.Fatalf("TOTPCode at %d: %v", tc.unix, err)
		}
		if got != tc.want {
			t.Errorf("TOTPCode at %d = %q, want %q", tc.unix, got, tc.want)
		}
	}
}

// TestTOTPCodeShapes covers the boundary cases the fixed width has to survive.
func TestTOTPCodeShapes(t *testing.T) {
	// A code with leading zeros must keep them, or a user could never type it.
	found := false
	at := time.Unix(0, 0).UTC()
	for i := 0; i < 2000; i++ {
		code, err := identity.TOTPCode(rfcSecret, at.Add(time.Duration(i)*30*time.Second))
		if err != nil {
			t.Fatalf("TOTPCode: %v", err)
		}
		if len(code) != 6 {
			t.Fatalf("code %q is not six characters", code)
		}
		if strings.HasPrefix(code, "0") {
			found = true
		}
	}
	if !found {
		t.Error("no code with a leading zero was produced, which suggests zero padding is broken")
	}
}

// TestVerifyTOTPAcceptsTheCurrentStep is the ordinary case.
func TestVerifyTOTPAcceptsTheCurrentStep(t *testing.T) {
	now := time.Unix(1700000000, 0).UTC()
	code, err := identity.TOTPCode(rfcSecret, now)
	if err != nil {
		t.Fatalf("TOTPCode: %v", err)
	}

	if !identity.VerifyTOTP(rfcSecret, code, now) {
		t.Error("a code for the current step was rejected")
	}
}

// TestVerifyTOTPToleratesClockSkew: the code either side of now is accepted, so
// a slightly wrong clock or a code typed as the step rolls over still works.
func TestVerifyTOTPToleratesClockSkew(t *testing.T) {
	now := time.Unix(1700000000, 0).UTC()

	for _, offset := range []time.Duration{-30 * time.Second, 30 * time.Second} {
		code, err := identity.TOTPCode(rfcSecret, now.Add(offset))
		if err != nil {
			t.Fatalf("TOTPCode: %v", err)
		}
		if !identity.VerifyTOTP(rfcSecret, code, now) {
			t.Errorf("a code %s away was rejected, but the skew window allows it", offset)
		}
	}
}

// TestVerifyTOTPRejectsFarSteps: tolerance is one step, not unlimited.
func TestVerifyTOTPRejectsFarSteps(t *testing.T) {
	now := time.Unix(1700000000, 0).UTC()

	for _, offset := range []time.Duration{-120 * time.Second, 120 * time.Second} {
		code, err := identity.TOTPCode(rfcSecret, now.Add(offset))
		if err != nil {
			t.Fatalf("TOTPCode: %v", err)
		}
		if identity.VerifyTOTP(rfcSecret, code, now) {
			t.Errorf("a code %s away was accepted, which is outside the window", offset)
		}
	}
}

// TestVerifyTOTPRejectsMalformedInput covers what a user or a script might send.
func TestVerifyTOTPRejectsMalformedInput(t *testing.T) {
	now := time.Unix(1700000000, 0).UTC()
	valid, err := identity.TOTPCode(rfcSecret, now)
	if err != nil {
		t.Fatalf("TOTPCode: %v", err)
	}

	cases := map[string]string{
		"empty":          "",
		"too short":      valid[:5],
		"too long":       valid + "0",
		"letters":        "abcdef",
		"mixed":          "12a456",
		"padded":         " 12345",
		"a wrong code":   "000000",
		"another wrong":  "111111",
		"negative sign":  "-12345",
		"plus prefixed":  "+12345",
		"space separate": "12 456",
	}

	for name, code := range cases {
		// A wrong code is only "malformed" if it happens to equal the valid one.
		if code == valid {
			continue
		}
		if identity.VerifyTOTP(rfcSecret, code, now) {
			t.Errorf("%s (%q) was accepted", name, code)
		}
	}
}

// TestVerifyTOTPRejectsAnotherSecret: a code from a different secret must not
// verify, or enrolment would not bind the factor to the account.
func TestVerifyTOTPRejectsAnotherSecret(t *testing.T) {
	other, err := identity.GenerateTOTPSecret()
	if err != nil {
		t.Fatalf("GenerateTOTPSecret: %v", err)
	}

	now := time.Unix(1700000000, 0).UTC()
	code, err := identity.TOTPCode(other, now)
	if err != nil {
		t.Fatalf("TOTPCode: %v", err)
	}

	if identity.VerifyTOTP(rfcSecret, code, now) {
		t.Error("a code generated from another secret was accepted")
	}
}

// TestGenerateTOTPSecretIsUsable covers the round trip and the shape.
func TestGenerateTOTPSecretIsUsable(t *testing.T) {
	secret, err := identity.GenerateTOTPSecret()
	if err != nil {
		t.Fatalf("GenerateTOTPSecret: %v", err)
	}

	if len(secret) != 32 {
		t.Errorf("a 20 byte secret should encode to 32 base32 characters, got %d", len(secret))
	}
	if strings.Contains(secret, "=") {
		t.Error("the secret carries padding, which authenticator apps do not expect")
	}

	now := time.Unix(1700000000, 0).UTC()
	code, err := identity.TOTPCode(secret, now)
	if err != nil {
		t.Fatalf("TOTPCode: %v", err)
	}
	if !identity.VerifyTOTP(secret, code, now) {
		t.Error("a freshly generated secret did not verify its own code")
	}
}

// TestGenerateTOTPSecretIsUnique: two enrolments must never share a secret.
func TestGenerateTOTPSecretIsUnique(t *testing.T) {
	seen := make(map[string]bool, 64)
	for i := 0; i < 64; i++ {
		secret, err := identity.GenerateTOTPSecret()
		if err != nil {
			t.Fatalf("GenerateTOTPSecret: %v", err)
		}
		if seen[secret] {
			t.Fatal("GenerateTOTPSecret returned the same secret twice")
		}
		seen[secret] = true
	}
}

// TestTOTPProvisioningURICarriesTheParameters covers the string an authenticator
// app is given: everything it needs must be in it.
func TestTOTPProvisioningURICarriesTheParameters(t *testing.T) {
	uri := identity.TOTPProvisioningURI(rfcSecret, "boss@example.com", "PocketAdmin")

	for _, want := range []string{
		"otpauth://totp/",
		"secret=" + rfcSecret,
		"issuer=PocketAdmin",
		"algorithm=SHA1",
		"digits=6",
		"period=30",
	} {
		if !strings.Contains(uri, want) {
			t.Errorf("the provisioning URI is missing %q: %s", want, uri)
		}
	}

	// The label is a display string and must survive an address with an @ in it.
	if !strings.Contains(uri, "boss") {
		t.Errorf("the account label is missing from %s", uri)
	}
}

// ---------------------------------------------------------------------------
// Recovery codes
// ---------------------------------------------------------------------------

// TestRecoveryCodeDigestsMatch covers the round trip.
func TestRecoveryCodeDigestsMatch(t *testing.T) {
	codes, err := identity.GenerateRecoveryCodes()
	if err != nil {
		t.Fatalf("GenerateRecoveryCodes: %v", err)
	}
	if len(codes) != 10 {
		t.Fatalf("expected 10 codes, got %d", len(codes))
	}

	digests := make([]string, 0, len(codes))
	for _, c := range codes {
		digests = append(digests, identity.HashRecoveryCode(c))
	}

	remaining, ok := identity.ConsumeRecoveryCode(digests, codes[0])
	if !ok {
		t.Fatal("a freshly issued recovery code did not match its digest")
	}
	if len(remaining) != len(digests)-1 {
		t.Errorf("consuming a code should leave %d, got %d", len(digests)-1, len(remaining))
	}
}

// TestRecoveryCodeIsSingleUse is the whole point of consuming it.
func TestRecoveryCodeIsSingleUse(t *testing.T) {
	codes, err := identity.GenerateRecoveryCodes()
	if err != nil {
		t.Fatalf("GenerateRecoveryCodes: %v", err)
	}

	digests := []string{identity.HashRecoveryCode(codes[0])}

	remaining, ok := identity.ConsumeRecoveryCode(digests, codes[0])
	if !ok {
		t.Fatal("the first use should succeed")
	}
	if _, ok := identity.ConsumeRecoveryCode(remaining, codes[0]); ok {
		t.Error("the same recovery code was accepted twice")
	}
}

// TestRecoveryCodeIgnoresFormatting: the dash is for legibility, not meaning.
func TestRecoveryCodeIgnoresFormatting(t *testing.T) {
	code := "ABCDE-23456"

	for _, typed := range []string{"ABCDE-23456", "abcde23456", "ABCDE 23456", " ABCDE-23456 "} {
		if identity.HashRecoveryCode(typed) != identity.HashRecoveryCode(code) {
			t.Errorf("%q should digest the same as %q", typed, code)
		}
	}
}

// TestRecoveryCodesAvoidAmbiguousCharacters: these are read off a screen and
// typed by hand, so 0/O and 1/I/L must not appear.
func TestRecoveryCodesAvoidAmbiguousCharacters(t *testing.T) {
	codes, err := identity.GenerateRecoveryCodes()
	if err != nil {
		t.Fatalf("GenerateRecoveryCodes: %v", err)
	}

	for _, c := range codes {
		if strings.ContainsAny(c, "01OIL") {
			t.Errorf("recovery code %q contains a character that is easy to misread", c)
		}
	}
}

// TestRecoveryCodesAreUnique: a duplicate would be one fewer way back in.
func TestRecoveryCodesAreUnique(t *testing.T) {
	codes, err := identity.GenerateRecoveryCodes()
	if err != nil {
		t.Fatalf("GenerateRecoveryCodes: %v", err)
	}

	seen := make(map[string]bool, len(codes))
	for _, c := range codes {
		if seen[c] {
			t.Fatalf("duplicate recovery code %q", c)
		}
		seen[c] = true
	}
}

// TestConsumeRecoveryCodeRejectsUnknown covers a wrong code.
func TestConsumeRecoveryCodeRejectsUnknown(t *testing.T) {
	codes, err := identity.GenerateRecoveryCodes()
	if err != nil {
		t.Fatalf("GenerateRecoveryCodes: %v", err)
	}

	digests := []string{identity.HashRecoveryCode(codes[0])}
	remaining, ok := identity.ConsumeRecoveryCode(digests, "ZZZZZ-ZZZZZ")
	if ok {
		t.Error("an unknown recovery code was accepted")
	}
	if len(remaining) != 1 {
		t.Error("a rejected attempt must not consume a code")
	}
}
