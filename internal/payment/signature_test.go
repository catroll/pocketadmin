package payment_test

import (
	"strconv"
	"testing"
	"time"

	"pocketadmin/internal/payment"
)

// TestASignatureVerifies.
func TestASignatureVerifies(t *testing.T) {
	secret := "shared-secret"
	now := time.Now()
	body := []byte(`{"event":"payment.succeeded","id":"txn-1"}`)

	signature := payment.Sign(secret, now, body)

	if err := payment.Verify(secret, signature, strconv.FormatInt(now.Unix(), 10), body,
		now, 0); err != nil {
		t.Errorf("a valid signature was refused: %v", err)
	}
}

// TestATamperedBodyIsRefused: which is the whole point of signing it.
func TestATamperedBodyIsRefused(t *testing.T) {
	secret := "shared-secret"
	now := time.Now()
	body := []byte(`{"event":"payment.succeeded","amount":100}`)

	signature := payment.Sign(secret, now, body)
	tampered := []byte(`{"event":"payment.succeeded","amount":999999}`)

	err := payment.Verify(secret, signature, strconv.FormatInt(now.Unix(), 10), tampered, now, 0)
	if err == nil {
		t.Fatal("a tampered body was accepted")
	}
}

// TestTheTimestampIsInsideTheSignature: signing the body alone would let an attacker
// pair a valid signature with any timestamp they liked, which is the same as having no
// timestamp at all.
func TestTheTimestampIsInsideTheSignature(t *testing.T) {
	secret := "shared-secret"
	now := time.Now()
	body := []byte(`{"event":"payment.succeeded"}`)

	signature := payment.Sign(secret, now, body)

	// The same signature with a different timestamp.
	other := strconv.FormatInt(now.Add(time.Hour).Unix(), 10)
	if err := payment.Verify(secret, signature, other, body, now, 0); err == nil {
		t.Error("a signature was accepted against a timestamp it was not made for")
	}
}

// TestAnOldCallbackIsRefused: a signature is valid forever on its own, so a captured
// callback could be replayed indefinitely.
func TestAnOldCallbackIsRefused(t *testing.T) {
	secret := "shared-secret"
	now := time.Now()
	body := []byte(`{"event":"payment.succeeded"}`)

	old := now.Add(-6 * time.Minute)
	signature := payment.Sign(secret, old, body)
	stamp := strconv.FormatInt(old.Unix(), 10)

	err := payment.Verify(secret, signature, stamp, body, now, 0)
	if err == nil {
		t.Fatal("a six-minute-old callback was accepted with a five-minute tolerance")
	}

	// Within the tolerance it is fine, because a retry queue is not an attack.
	recent := now.Add(-4 * time.Minute)
	signature = payment.Sign(secret, recent, body)
	if err := payment.Verify(secret, signature, strconv.FormatInt(recent.Unix(), 10), body, now, 0); err != nil {
		t.Errorf("a four-minute-old callback was refused: %v", err)
	}
}

// TestAFutureTimestampIsRefused: a clock far ahead would otherwise buy a long replay
// window.
func TestAFutureTimestampIsRefused(t *testing.T) {
	secret := "shared-secret"
	now := time.Now()
	body := []byte(`{"event":"payment.succeeded"}`)

	ahead := now.Add(time.Hour)
	signature := payment.Sign(secret, ahead, body)

	if err := payment.Verify(secret, signature, strconv.FormatInt(ahead.Unix(), 10), body, now, 0); err == nil {
		t.Error("a callback signed an hour in the future was accepted")
	}
}

// TestTheReasonsAreDistinct: "the signature did not match" and "this callback is five
// hours old" are different incidents.
func TestTheReasonsAreDistinct(t *testing.T) {
	secret := "shared-secret"
	now := time.Now()
	body := []byte(`{"event":"payment.succeeded"}`)

	valid := payment.Sign(secret, now, body)
	stamp := strconv.FormatInt(now.Unix(), 10)

	cases := []struct {
		name      string
		secret    string
		signature string
		timestamp string
		body      []byte
		now       time.Time
	}{
		{"no secret", "", valid, stamp, body, now},
		{"no signature", secret, "", stamp, body, now},
		{"no timestamp", secret, valid, "", body, now},
		{"bad timestamp", secret, valid, "not-a-number", body, now},
		{"wrong secret", "other-secret", valid, stamp, body, now},
		{"old", secret, payment.Sign(secret, now.Add(-time.Hour), body), strconv.FormatInt(now.Add(-time.Hour).Unix(), 10), body, now},
	}

	seen := map[string]bool{}
	for _, tc := range cases {
		err := payment.Verify(tc.secret, tc.signature, tc.timestamp, tc.body, tc.now, 0)
		if err == nil {
			t.Errorf("%s was accepted", tc.name)
			continue
		}

		var signatureErr *payment.SignatureError
		if !asSignatureError(err, &signatureErr) {
			t.Errorf("%s produced a plain error rather than a signature error: %v", tc.name, err)
			continue
		}
		if signatureErr.Reason == "" {
			t.Errorf("%s gave no reason", tc.name)
		}
		seen[signatureErr.Reason] = true
	}

	// Every failure has its own explanation, so an operator can tell a
	// misconfiguration from a clock problem from an attack.
	if len(seen) < len(cases) {
		t.Errorf("%d distinct reasons for %d failures: %v", len(seen), len(cases), seen)
	}
}

// TestASignatureIsCaseInsensitive: a header may arrive uppercased.
func TestASignatureIsCaseInsensitive(t *testing.T) {
	secret := "shared-secret"
	now := time.Now()
	body := []byte(`{"event":"payment.succeeded"}`)

	signature := payment.Sign(secret, now, body)
	stamp := strconv.FormatInt(now.Unix(), 10)

	upper := ""
	for _, r := range signature {
		if r >= 'a' && r <= 'f' {
			upper += string(r - 32)
			continue
		}
		upper += string(r)
	}

	if err := payment.Verify(secret, upper, stamp, body, now, 0); err != nil {
		t.Errorf("an uppercased signature was refused: %v", err)
	}
}

// asSignatureError is errors.As, written out so the test reads as one assertion.
func asSignatureError(err error, target **payment.SignatureError) bool {
	if e, ok := err.(*payment.SignatureError); ok {
		*target = e
		return true
	}
	return false
}
