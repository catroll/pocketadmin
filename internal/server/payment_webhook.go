package server

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/pocketbase/pocketbase/core"

	"pocketadmin/internal/audit"
	"pocketadmin/internal/integration"
	"pocketadmin/internal/payment"
)

// maxCallbackBytes bounds a provider callback.
//
// The body is read into memory to verify its signature, so an unbounded one is a way
// to exhaust the process. A megabyte is far more than any provider sends and far less
// than would matter.
const maxCallbackBytes = 1 << 20

// registerPaymentWebhooks mounts the provider callback endpoint.
//
// It is mounted on the root engine rather than the panel group, because the caller is
// a payment provider and has no session. It is the one unauthenticated write path in
// the application, which is why the signature is not optional: without a configured
// secret the endpoint refuses every request rather than accepting unverified ones.
func (s *Server) registerPaymentWebhooks(r *gin.Engine) {
	// Signature-guarded rather than session-guarded: the caller is a provider. See
	// signatureGuarded, and note that the handler refuses everything when the
	// provider has no signing secret.
	s.signatureGuarded(r.Group(""), http.MethodPost, "/webhooks/payment/:provider", s.handlePaymentWebhook)
}

// callbackPayload is the shape a provider sends.
//
// It is deliberately small. A provider that sends something else is not understood,
// and guessing at an unknown payload would mean applying a payment on the strength of
// a field nobody has read.
type callbackPayload struct {
	// The provider's own transaction identifier. Required: it is the idempotency key,
	// and without it nothing can be applied safely.
	TransactionID string `json:"transactionId"`
	IntentID      string `json:"intentId"`

	Status string `json:"status"`

	// Minor units, in the order's currency.
	Amount    int64 `json:"amount"`
	FeeAmount int64 `json:"feeAmount"`

	FailureCode    string `json:"failureCode"`
	FailureMessage string `json:"failureMessage"`
}

// handlePaymentWebhook receives a provider callback.
func (s *Server) handlePaymentWebhook(c *gin.Context) {
	providerCode := strings.TrimSpace(c.Param("provider"))

	// The body is read before anything else, because the signature is over the bytes
	// as they arrived. Re-encoding the parsed value would produce different bytes and
	// the signature would never match.
	body, err := io.ReadAll(io.LimitReader(c.Request.Body, maxCallbackBytes))
	if err != nil {
		s.webhookRefuse(c, providerCode, "the body could not be read")
		return
	}

	provider, secret, err := s.paymentProvider(providerCode)
	if err != nil || provider == nil {
		// A provider that is not configured is not a reason to say which providers
		// are: the answer is the same shape for every one of them.
		s.webhookRefuse(c, providerCode, "that provider is not configured")
		return
	}

	if err := payment.Verify(
		secret,
		c.GetHeader(payment.HeaderSignature),
		c.GetHeader(payment.HeaderTimestamp),
		body,
		time.Now(),
		0,
	); err != nil {
		// The refusal is specific in the log and vague in the response. A caller who
		// signed correctly does not need it, and a caller who did not should not be
		// told which part was wrong.
		slog.Warn("a payment callback failed verification",
			"provider", providerCode, "error", err, "remote", c.ClientIP())
		s.webhookRefuse(c, providerCode, "the callback could not be verified")
		return
	}

	var payload callbackPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		s.webhookRefuse(c, providerCode, "the body is not the expected shape")
		return
	}
	if payload.TransactionID == "" {
		s.webhookRefuse(c, providerCode, "the callback named no transaction")
		return
	}

	intent, err := s.resolveIntent(provider.Id, payload)
	if err != nil || intent == nil {
		s.webhookRefuse(c, providerCode, "the callback names no known payment")
		return
	}

	// The payload is stored redacted, because it is a provider's message and may
	// carry card fragments or a secret.
	redacted := audit.Redact(payload)

	result, err := payment.ApplyCallback(s.app, intent, payment.Outcome{
		ProviderTxnID:  payload.TransactionID,
		Status:         mapProviderStatus(payload.Status),
		Amount:         payload.Amount,
		FeeAmount:      payload.FeeAmount,
		FailureCode:    payload.FailureCode,
		FailureMessage: payload.FailureMessage,
		Payload:        redacted,
	}, time.Now())
	if err != nil {
		slog.Error("a payment callback could not be applied",
			"provider", providerCode, "transaction", payload.TransactionID, "error", err)
		s.webhookRefuse(c, providerCode, "the callback could not be applied")
		return
	}

	if !result.Applied {
		// A replay is answered with success, not with a failure. Providers retry on
		// failure, so refusing a duplicate would make it retry the duplicate forever.
		s.auditSystem(auditEntry{
			Event:      audit.EventSettingsChanged,
			Module:     "payments",
			TargetType: "payment_transaction",
			TargetID:   payload.TransactionID,
			Severity:   severityInfo,
			Summary:    "A duplicate payment callback was ignored: " + result.Reason,
			Detail:     map[string]any{"provider": providerCode},
		})
		c.JSON(http.StatusOK, gin.H{"status": "duplicate", "applied": false, "reason": result.Reason})
		return
	}

	entry := auditEntry{
		Event:      audit.EventSettingsChanged,
		Module:     "payments",
		TargetType: "payment_transaction",
		TargetID:   payload.TransactionID,
		Severity:   severityWarning,
		Summary:    "A payment callback was applied for " + intent.GetString("order"),
		Detail:     map[string]any{"provider": providerCode, "amount": payload.Amount},
	}
	if result.AmountMismatch {
		entry.Severity = severityCritical
		entry.Summary = "A payment callback was applied with a different amount from the one requested"
		entry.Detail["reportedAmount"] = payload.Amount
		entry.Detail["requestedAmount"] = intent.GetFloat("amount")
	}
	s.auditSystem(entry)

	c.JSON(http.StatusOK, gin.H{"status": "accepted", "applied": true})
}

// webhookRefuse answers a refused callback.
//
// The status is 400 for everything. A provider treats 5xx as "retry" and 4xx as
// "stop", and every refusal here is a request that will never become valid by being
// sent again.
func (s *Server) webhookRefuse(c *gin.Context, providerCode, reason string) {
	slog.Warn("a payment callback was refused",
		"provider", providerCode, "reason", reason, "remote", c.ClientIP())
	c.JSON(http.StatusBadRequest, gin.H{"status": "refused", "reason": reason})
}

// mapProviderStatus maps a provider's vocabulary onto ours.
//
// An unknown status becomes pending rather than succeeded. The safe direction to be
// wrong in is the one that does not credit money.
func mapProviderStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "succeeded", "success", "paid", "captured", "completed":
		return payment.TxnSucceeded
	case "failed", "failure", "declined", "error":
		return payment.TxnFailed
	case "cancelled", "canceled", "voided":
		return payment.TxnCancelled
	case "expired":
		return payment.TxnExpired
	case "processing", "in_progress", "pending":
		return payment.TxnProcessing
	default:
		return payment.TxnPending
	}
}

// paymentProvider resolves a provider by its code and returns its signing secret.
func (s *Server) paymentProvider(code string) (*core.Record, string, error) {
	provider, err := s.app.FindFirstRecordByFilter("integration_providers",
		"type = 'payment' && code = {:c}", map[string]any{"c": code})
	if err != nil || provider == nil {
		return nil, "", nil
	}

	// The integrations vocabulary is enabled, disabled, testing or error -- not
	// "active". Getting that wrong made every correctly configured provider return an
	// empty secret, so every callback was refused and the endpoint looked like it was
	// working. It is checked against the same constant the integrations screens use.
	if provider.GetString("status") != integration.StatusEnabled {
		return provider, "", nil
	}

	// The secrets are decrypted from the provider's own column. A provider can hold
	// a webhook secret separate from its API key, which is what a signing secret is.
	secrets := integration.DecryptSecrets(s.app, provider.GetString("secrets"))

	// The signing secret is a named field in the provider's configuration.
	// The names are tried in order of how specifically they describe a signing
	// secret. A provider configured with only an API key still verifies, using that
	// key, which is what several providers do.
	secret := ""
	for _, key := range []string{"webhookSecret", "signingSecret", "secretKey", "apiKey"} {
		if value := integration.ScalarString(secrets[key]); value != "" {
			secret = value
			break
		}
	}

	// An empty secret is returned as an empty string rather than an error, and
	// payment.Verify refuses it. The endpoint must never accept an unsigned callback
	// just because a provider was configured without a secret.
	return provider, secret, nil
}

// resolveIntent finds the payment intent a callback is about.
//
// It is looked up by the intent identifier when the provider sends one and by the
// provider's own intent identifier otherwise, so a provider that uses one or the
// other works without a special case per provider.
func (s *Server) resolveIntent(providerID string, payload callbackPayload) (*core.Record, error) {
	if payload.IntentID != "" {
		rec, err := s.app.FindRecordById(payment.CollectionIntents, payload.IntentID)
		if err == nil && rec != nil {
			return rec, nil
		}

		rec, err = s.app.FindFirstRecordByFilter(payment.CollectionIntents,
			"providerIntentId = {:p}", map[string]any{"p": payload.IntentID})
		if err == nil && rec != nil {
			return rec, nil
		}
	}

	// A callback with no intent named is matched through the transaction, which is
	// how a provider that only knows its own transaction identifier is handled.
	if existing, err := payment.FindByProviderTxn(s.app, payload.TransactionID); err == nil && existing != nil {
		return s.app.FindRecordById(payment.CollectionIntents, existing.GetString("intent"))
	}

	return nil, nil
}
