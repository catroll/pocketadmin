package identity

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// TOTP parameters, fixed by RFC 6238 and by what authenticator applications
// actually implement: a 30 second step, six digits, SHA-1.
//
// SHA-1 is not a weakness here. TOTP's security rests on the secret being
// unpredictable and the window being short, not on collision resistance, and
// every authenticator app in use speaks this profile. Choosing a different hash
// would produce codes nothing could verify.
const (
	totpStep      = 30 * time.Second
	totpDigits    = 6
	totpSecretLen = 20

	// totpSkew is how many steps either side of now are accepted, so a clock a
	// little out of step or a code typed as the step rolls over still works.
	totpSkew = 1
)

// totpEncoding is base32 without padding, which is what the otpauth URI scheme
// and authenticator apps expect.
var totpEncoding = base32.StdEncoding.WithPadding(base32.NoPadding)

// GenerateTOTPSecret returns a new base32 secret.
func GenerateTOTPSecret() (string, error) {
	buf := make([]byte, totpSecretLen)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate a TOTP secret: %w", err)
	}
	return totpEncoding.EncodeToString(buf), nil
}

// TOTPCode returns the code for a secret at a moment in time.
func TOTPCode(secret string, at time.Time) (string, error) {
	key, err := decodeTOTPSecret(secret)
	if err != nil {
		return "", err
	}

	counter := uint64(at.Unix() / int64(totpStep.Seconds()))

	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], counter)

	mac := hmac.New(sha1.New, key)
	mac.Write(msg[:])
	sum := mac.Sum(nil)

	// Dynamic truncation, RFC 4226 section 5.4: the low nibble of the last byte
	// selects a 4 byte window, and the top bit is masked off so the result is
	// positive regardless of platform.
	offset := sum[len(sum)-1] & 0x0f
	value := binary.BigEndian.Uint32(sum[offset:offset+4]) & 0x7fffffff

	mod := uint32(1)
	for i := 0; i < totpDigits; i++ {
		mod *= 10
	}

	return fmt.Sprintf("%0*d", totpDigits, value%mod), nil
}

// VerifyTOTP reports whether a code is valid for a secret at a moment in time.
//
// It accepts codes one step either side of now. The comparison is constant time,
// and every candidate is evaluated rather than returning early, so the time taken
// does not reveal how close a wrong code was.
func VerifyTOTP(secret, code string, at time.Time) bool {
	code = strings.TrimSpace(code)
	if len(code) != totpDigits {
		return false
	}
	for _, r := range code {
		if r < '0' || r > '9' {
			return false
		}
	}

	valid := false
	for offset := -totpSkew; offset <= totpSkew; offset++ {
		want, err := TOTPCode(secret, at.Add(time.Duration(offset)*totpStep))
		if err != nil {
			return false
		}
		if subtle.ConstantTimeCompare([]byte(want), []byte(code)) == 1 {
			valid = true
		}
	}
	return valid
}

// TOTPProvisioningURI builds the otpauth:// URI an authenticator app consumes.
//
// The issuer and account are labels only; they are what the user sees in the app
// and never take part in the computation.
func TOTPProvisioningURI(secret, account, issuer string) string {
	label := account
	if issuer != "" {
		label = issuer + ":" + account
	}

	params := url.Values{}
	params.Set("secret", secret)
	if issuer != "" {
		params.Set("issuer", issuer)
	}
	params.Set("algorithm", "SHA1")
	params.Set("digits", fmt.Sprint(totpDigits))
	params.Set("period", fmt.Sprint(int(totpStep.Seconds())))

	return "otpauth://totp/" + url.PathEscape(label) + "?" + params.Encode()
}

// decodeTOTPSecret accepts a secret with or without padding and in either case.
func decodeTOTPSecret(secret string) ([]byte, error) {
	normalised := strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(secret), " ", ""))
	normalised = strings.TrimRight(normalised, "=")

	key, err := totpEncoding.DecodeString(normalised)
	if err != nil {
		return nil, fmt.Errorf("invalid TOTP secret: %w", err)
	}
	return key, nil
}

// ---------------------------------------------------------------------------
// Recovery codes
// ---------------------------------------------------------------------------

// recoveryCodeCount is how many single-use codes an enrolment issues.
const recoveryCodeCount = 10

// recoveryCodeAlphabet omits characters that are easy to confuse when read from
// a screen and typed: 0/O, 1/I/L.
const recoveryCodeAlphabet = "23456789ABCDEFGHJKMNPQRSTUVWXYZ"

// GenerateRecoveryCodes returns recovery codes in plain text.
//
// They are shown once and stored only as digests: a recovery code is a
// credential, and a database dump must not hand over a second way in.
func GenerateRecoveryCodes() ([]string, error) {
	codes := make([]string, 0, recoveryCodeCount)

	for i := 0; i < recoveryCodeCount; i++ {
		code, err := randomCode(10)
		if err != nil {
			return nil, err
		}
		// Grouped for legibility; the grouping is stripped before comparison.
		codes = append(codes, code[:5]+"-"+code[5:])
	}
	return codes, nil
}

// NormaliseRecoveryCode strips formatting so a code typed with or without its
// dash compares equal.
func NormaliseRecoveryCode(code string) string {
	out := make([]rune, 0, len(code))
	for _, r := range strings.ToUpper(strings.TrimSpace(code)) {
		if r == '-' || r == ' ' {
			continue
		}
		out = append(out, r)
	}
	return string(out)
}

// HashRecoveryCode returns the stored digest of a recovery code.
//
// A plain SHA-256 is right here rather than a password hash: the code is long,
// random and single-use, so there is nothing to brute force, and verification
// must be cheap because it only happens after a correct password.
func HashRecoveryCode(code string) string {
	sum := sha256.Sum256([]byte(NormaliseRecoveryCode(code)))
	return fmt.Sprintf("%x", sum)
}

// ConsumeRecoveryCode removes a matching code from the stored digests and reports
// whether one matched. The returned slice is what should be persisted.
func ConsumeRecoveryCode(stored []string, code string) (remaining []string, ok bool) {
	want := HashRecoveryCode(code)

	remaining = make([]string, 0, len(stored))
	for _, h := range stored {
		if subtle.ConstantTimeCompare([]byte(h), []byte(want)) == 1 {
			ok = true
			// Consumed: not carried over, so it cannot be used twice.
			continue
		}
		remaining = append(remaining, h)
	}
	return remaining, ok
}

// randomCode returns n characters from the unambiguous alphabet.
func randomCode(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate a recovery code: %w", err)
	}

	out := make([]byte, n)
	for i, b := range buf {
		// The alphabet length does not divide 256, so the modulo is very slightly
		// biased. For a 10 character code from a 31 character alphabet that is
		// immaterial, and rejecting the tail would complicate the loop for no
		// practical gain.
		out[i] = recoveryCodeAlphabet[int(b)%len(recoveryCodeAlphabet)]
	}
	return string(out), nil
}
