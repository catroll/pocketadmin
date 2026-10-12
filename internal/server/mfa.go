package server

import (
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/gin-contrib/sessions"
	"github.com/gin-gonic/gin"
	"github.com/pocketbase/pocketbase/core"

	"pocketadmin/internal/identity"
	"pocketadmin/internal/rbac"
)

// MFA session keys.
//
// A correct password does not complete a sign-in when a second factor is
// enrolled. The account id is held in a pending state instead, and no session
// token is issued until the code is verified, so an abandoned second step leaves
// nothing usable behind.
const (
	sessionPendingMFAKey = "mfa_uid"
	sessionMFADeadline   = "mfa_deadline"
	sessionPendingNext   = "mfa_next"

	// mfaPendingTTL is how long the code prompt stays valid.
	mfaPendingTTL = 5 * time.Minute
)

// registerMFARoutes mounts the sign-in second step.
//
// These sit outside the panel's authenticated group: the account is not signed in
// until the second factor is verified, so requireAuth must not run yet.
func (s *Server) registerMFARoutes(public *gin.RouterGroup) {
	public.GET("/login/mfa", s.handleMFAChallenge)
	public.POST("/login/mfa", s.handleMFAVerify)
}

// handleMFAChallenge renders the code prompt.
func (s *Server) handleMFAChallenge(c *gin.Context) {
	sess := sessions.Default(c)

	if !s.hasPendingMFA(c) {
		c.Redirect(http.StatusSeeOther, "/login")
		return
	}

	s.render(c, http.StatusOK, "mfa.html", mfaPage{
		base:  s.newBase(c, "Two-factor authentication", ""),
		Email: pendingMFAEmail(sess),
	})
}

// handleMFAVerify checks the submitted second factor and completes the sign-in.
func (s *Server) handleMFAVerify(c *gin.Context) {
	sess := sessions.Default(c)

	uid, _ := sess.Get(sessionPendingMFAKey).(string)
	if uid == "" {
		c.Redirect(http.StatusSeeOther, "/login")
		return
	}
	if deadline, ok := sess.Get(sessionMFADeadline).(int64); !ok || time.Now().Unix() > deadline {
		s.clearPendingMFA(c)
		s.setFlash(c, "warning", "That sign-in took too long. Please sign in again.")
		c.Redirect(http.StatusSeeOther, "/login")
		return
	}

	rec, err := s.app.FindRecordById(rbac.CollectionUsers, uid)
	if err != nil || rec == nil {
		s.clearPendingMFA(c)
		s.redirectToLogin(c)
		return
	}

	code := strings.TrimSpace(c.PostForm("code"))
	next, _ := sess.Get(sessionPendingNext).(string)
	if next == "" {
		next = "/dashboard"
	}

	if !s.verifySecondFactor(c, rec, code) {
		s.logLogin(c, rec.Id, rec.Email(), "mfa_failed", "invalid second factor", "medium")
		s.render(c, http.StatusUnauthorized, "mfa.html", mfaPage{
			base:  s.newBase(c, "Two-factor authentication", ""),
			Email: rec.Email(),
			Error: "That code is not valid. Check your authenticator app and try again.",
		})
		return
	}

	s.clearPendingMFA(c)

	if !s.startSession(c, rec.Id, sessionKindUser) {
		s.renderLoginFailure(c, rec.Email(), next, "Could not start a session. Please try again.")
		return
	}

	now := time.Now().UTC()
	rec.Set("lastLoginAt", now)
	rec.Set("lastLoginIp", c.ClientIP())
	s.clearFailedAttempts(rec)
	if err := s.app.Save(rec); err != nil {
		slog.Warn("could not record the last sign-in", "user", rec.Id, "error", err)
	}

	s.logLogin(c, rec.Id, rec.Email(), "success", "second factor verified", "low")
	c.Redirect(http.StatusSeeOther, safeNext(next))
}

// hasPendingMFA reports whether a sign-in is waiting on its second factor.
func (s *Server) hasPendingMFA(c *gin.Context) bool {
	sess := sessions.Default(c)

	uid, _ := sess.Get(sessionPendingMFAKey).(string)
	if uid == "" {
		return false
	}
	deadline, ok := sess.Get(sessionMFADeadline).(int64)
	return ok && time.Now().Unix() <= deadline
}

// beginPendingMFA parks a half-authenticated sign-in until the code is verified.
func (s *Server) beginPendingMFA(c *gin.Context, rec *core.Record, next string) bool {
	sess := sessions.Default(c)

	sess.Set(sessionPendingMFAKey, rec.Id)
	sess.Set(sessionMFADeadline, time.Now().Add(mfaPendingTTL).Unix())
	sess.Set(sessionPendingNext, next)
	sess.Set(sessionPendingMFAEmail, rec.Email())

	// A stale session must not survive into the pending state.
	sess.Delete(sessionUserKey)
	sess.Delete(sessionKindKey)
	sess.Delete(sessionTokenKey)

	if err := sess.Save(); err != nil {
		slog.Error("could not persist the pending second factor", "user", rec.Id, "error", err)
		return false
	}
	return true
}

// clearPendingMFA drops the half-authenticated state.
func (s *Server) clearPendingMFA(c *gin.Context) {
	sess := sessions.Default(c)
	sess.Delete(sessionPendingMFAKey)
	sess.Delete(sessionMFADeadline)
	sess.Delete(sessionPendingNext)
	sess.Delete(sessionPendingMFAEmail)
	_ = sess.Save()
}

// pendingMFAEmail returns the address to show on the code prompt.
func pendingMFAEmail(sess sessions.Session) string {
	email, _ := sess.Get(sessionPendingMFAEmail).(string)
	return email
}

// sessionPendingMFAEmail carries the address for display only, so the prompt can
// name the account without a query on a request that is not yet authenticated.
const sessionPendingMFAEmail = "mfa_email"

// verifySecondFactor accepts either a current authenticator code or an unused
// recovery code, consuming the latter.
func (s *Server) verifySecondFactor(c *gin.Context, rec *core.Record, code string) bool {
	code = strings.TrimSpace(code)
	if code == "" {
		return false
	}

	if secret := rec.GetString("totpSecret"); secret != "" {
		if identity.VerifyTOTP(secret, code, time.Now()) {
			return true
		}
	}

	// A recovery code is the way back in when the authenticator is lost. Each one
	// works once, so a leaked code is worth a single sign-in rather than a
	// permanent bypass.
	stored := rec.GetStringSlice("recoveryCodeHashes")
	if len(stored) == 0 {
		return false
	}

	remaining, ok := identity.ConsumeRecoveryCode(stored, code)
	if !ok {
		return false
	}

	rec.Set("recoveryCodeHashes", remaining)
	if err := s.app.Save(rec); err != nil {
		slog.Error("could not consume a recovery code", "user", rec.Id, "error", err)
		// Refuse rather than accept a code that would still be usable.
		return false
	}

	s.audit(c, auditEntry{
		Action:     auditRecoveryCodeUsed,
		Resource:   "account",
		ResourceID: rec.Id,
		Risk:       "high",
		Summary:    "A recovery code was used to complete a sign-in.",
		SubjectID:  rec.Id, SubjectEmail: rec.Email(),
	})
	return true
}

// mfaPage is the second-factor prompt.
type mfaPage struct {
	base
	Email string
	Error string
}
