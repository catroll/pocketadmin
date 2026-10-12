package server

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/pocketbase/pocketbase/core"
)

// Session identity constants.
//
// The cookie holds a signed session carrying the account id, which of the two
// identity paths it came from, and a session token. The token's hash is stored
// in the `sessions` collection, which is what makes revocation possible:
// PocketBase's own auth tokens are stateless JWTs with no server-side kill
// switch.
const (
	sessionKindKey       = "kind"
	sessionTokenKey      = "sid"
	sessionKindUser      = "user"
	sessionKindSuperuser = "superuser"

	// sessionTTL is how long an admin session stays valid.
	sessionTTL = 8 * time.Hour

	// sessionContextAdmin marks a back-office session.
	sessionContextAdmin = "admin"

	// sessionRevokedLogout and friends are the recorded revocation reasons.
	sessionRevokedLogout      = "logout"
	sessionRevokedPassword    = "password_reset"
	sessionRevokedAdmin       = "admin_revoked"
	sessionRevokedBanned      = "banned"
	sessionRevokedProfileGone = "profile_detached"
	sessionCollectionName     = "sessions"
	sessionUserAgentMaxLen    = 512
	sessionTouchMinInterval   = time.Minute
	sessionTokenBytes         = 32
)

// createSessionRecord mints a session token for an account and stores its hash.
//
// The raw token is returned for the caller to place in the cookie and is never
// persisted; only the SHA-256 digest is.
func (s *Server) createSessionRecord(c *gin.Context, userID string) (string, error) {
	col, err := s.app.FindCollectionByNameOrId(sessionCollectionName)
	if err != nil {
		return "", fmt.Errorf("sessions collection missing: %w", err)
	}

	raw := make([]byte, sessionTokenBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate session token: %w", err)
	}
	token := hex.EncodeToString(raw)

	rec := core.NewRecord(col)
	rec.Set("user", userID)
	rec.Set("context", sessionContextAdmin)
	rec.Set("tokenHash", hashToken(token))
	rec.Set("ip", c.ClientIP())
	rec.Set("userAgent", truncate(c.Request.UserAgent(), sessionUserAgentMaxLen))
	rec.Set("device", deviceLabel(c.Request.UserAgent()))
	rec.Set("lastSeenAt", time.Now().UTC())
	rec.Set("expiresAt", time.Now().UTC().Add(sessionTTL))

	if err := s.app.Save(rec); err != nil {
		return "", fmt.Errorf("persist session: %w", err)
	}

	return token, nil
}

// validateSessionRecord loads a session by its raw token and rejects revoked or
// expired ones.
func (s *Server) validateSessionRecord(token string) (*core.Record, error) {
	if token == "" {
		return nil, fmt.Errorf("session token missing")
	}

	rec, err := s.app.FindFirstRecordByFilter(sessionCollectionName,
		"tokenHash = {:h}", map[string]any{"h": hashToken(token)})
	if err != nil || rec == nil {
		return nil, fmt.Errorf("session not found")
	}

	if !rec.GetDateTime("revokedAt").IsZero() {
		return nil, fmt.Errorf("session revoked")
	}

	if expires := rec.GetDateTime("expiresAt"); !expires.IsZero() && expires.Time().Before(time.Now()) {
		return nil, fmt.Errorf("session expired")
	}

	return rec, nil
}

// touchSessionRecord slides lastSeenAt forward at most once a minute, so an
// active session does not cause a write on every request.
func (s *Server) touchSessionRecord(rec *core.Record) {
	if last := rec.GetDateTime("lastSeenAt"); !last.IsZero() &&
		time.Since(last.Time()) < sessionTouchMinInterval {
		return
	}
	rec.Set("lastSeenAt", time.Now().UTC())
	// A failed touch is not worth failing the request over: the session simply
	// looks idle for a little longer.
	_ = s.app.Save(rec)
}

// revokeSessionRecord marks a session revoked and records why. Revoking an
// already-revoked session is a no-op.
func (s *Server) revokeSessionRecord(rec *core.Record, reason string) error {
	if rec == nil || !rec.GetDateTime("revokedAt").IsZero() {
		return nil
	}
	rec.Set("revokedAt", time.Now().UTC())
	rec.Set("revokedReason", reason)
	return s.app.Save(rec)
}

// revokeSessionsForUser revokes every live session for an account, optionally
// limited to one context.
//
// Called on password reset, ban and admin-profile detach, so the change takes
// effect on the next request rather than at the next token expiry.
func (s *Server) revokeSessionsForUser(userID, context, reason string) error {
	if userID == "" {
		return nil
	}

	filter := "user = {:user} && revokedAt = ''"
	params := map[string]any{"user": userID}
	if context != "" {
		filter += " && context = {:context}"
		params["context"] = context
	}

	records, err := s.app.FindRecordsByFilter(sessionCollectionName, filter, "", 0, 0, params)
	if err != nil {
		return nil
	}

	for _, rec := range records {
		if err := s.revokeSessionRecord(rec, reason); err != nil {
			return err
		}
	}
	return nil
}

// hashToken returns the hex SHA-256 of a session token.
func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// deviceLabel is a coarse device description derived from the user agent.
func deviceLabel(ua string) string {
	switch {
	case ua == "":
		return "unknown"
	case strings.Contains(ua, "iPad"), strings.Contains(ua, "Tablet"):
		return "tablet"
	case strings.Contains(ua, "iPhone"), strings.Contains(ua, "Android"), strings.Contains(ua, "Mobile"):
		return "mobile"
	default:
		return "desktop"
	}
}
