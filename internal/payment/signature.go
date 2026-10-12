// Package payment holds payment intents, the transactions attempted against them,
// provider callbacks and reconciliation.
//
// Two properties run through all of it. A callback is applied exactly once, however
// many times it arrives, because providers retry and a retried callback that charges
// an order twice is the worst defect this code could have. And a succeeded
// transaction is never altered: corrections go through a refund, which is a record of
// its own.
package payment

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// SignatureHeader names the headers a callback carries.
const (
	HeaderSignature = "X-PocketAdmin-Signature"
	HeaderTimestamp = "X-PocketAdmin-Timestamp"
	HeaderEventID   = "X-PocketAdmin-Event-Id"
)

// DefaultTolerance is how far a callback's timestamp may be from now.
//
// It bounds replay: a signature is valid forever on its own, so a callback captured
// once could be replayed indefinitely. Five minutes is long enough to survive a
// retry queue and short enough that a captured request is not a permanent key.
const DefaultTolerance = 5 * time.Minute

// SignatureError explains why a callback was rejected.
//
// The reasons are deliberately distinct, because "the signature did not match" and
// "this callback is five hours old" are different incidents: the first is a
// misconfiguration or an attack, the second is a clock or a queue.
type SignatureError struct {
	Reason string
}

func (e *SignatureError) Error() string { return "payment: " + e.Reason }

// Sign produces the signature for a body at a moment in time.
//
// The timestamp is inside the signed material, not merely alongside it. Signing the
// body alone would let an attacker take a valid signature and pair it with any
// timestamp they liked, which is the same as having no timestamp at all.
func Sign(secret string, timestamp time.Time, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(strconv.FormatInt(timestamp.Unix(), 10)))
	mac.Write([]byte("."))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

// Verify checks a callback's signature and its age.
//
// It returns a *SignatureError for anything wrong with the request, so a caller can
// tell a refusal apart from a failure of its own.
func Verify(secret, signature, timestamp string, body []byte, now time.Time, tolerance time.Duration) error {
	if secret == "" {
		return &SignatureError{Reason: "no signing secret is configured for this provider"}
	}
	if strings.TrimSpace(signature) == "" {
		return &SignatureError{Reason: "the callback carried no signature"}
	}
	if strings.TrimSpace(timestamp) == "" {
		return &SignatureError{Reason: "the callback carried no timestamp"}
	}

	seconds, err := strconv.ParseInt(strings.TrimSpace(timestamp), 10, 64)
	if err != nil {
		return &SignatureError{Reason: "the callback timestamp is not a number"}
	}

	if tolerance <= 0 {
		tolerance = DefaultTolerance
	}

	age := now.Sub(time.Unix(seconds, 0))
	if age < -tolerance {
		return &SignatureError{Reason: "the callback timestamp is in the future"}
	}
	if age > tolerance {
		return &SignatureError{Reason: fmt.Sprintf(
			"the callback is %s old, which is past the %s tolerance", age.Round(time.Second), tolerance)}
	}

	expected := Sign(secret, time.Unix(seconds, 0), body)

	// A constant-time comparison. A byte-by-byte one leaks how much of a forged
	// signature was correct, which is enough to forge one a byte at a time.
	if !hmac.Equal([]byte(expected), []byte(strings.ToLower(strings.TrimSpace(signature)))) {
		return &SignatureError{Reason: "the signature does not match"}
	}

	return nil
}
