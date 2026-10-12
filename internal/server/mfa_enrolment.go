package server

import (
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/pocketbase/pocketbase/core"

	"pocketadmin/internal/audit"
	"pocketadmin/internal/identity"
	"pocketadmin/internal/rbac"
)

// registerMFAManagementRoutes mounts self-service second-factor management.
//
// Like the preferences screen, these act only on the caller's own account, so
// they carry no permission beyond being signed in.
func (s *Server) registerMFAManagementRoutes(panel *gin.RouterGroup) {
	s.selfGuarded(panel, http.MethodPost, "/preferences/mfa/start", s.handleMFAStart)
	s.selfGuarded(panel, http.MethodPost, "/preferences/mfa/enable", s.handleMFAEnable)
	s.selfGuarded(panel, http.MethodPost, "/preferences/mfa/disable", s.handleMFADisable)
	s.selfGuarded(panel, http.MethodPost, "/preferences/mfa/cancel", s.handleMFACancel)
	s.selfGuarded(panel, http.MethodPost, "/preferences/mfa/recovery", s.handleMFARegenerate)
}

// handleMFAStart generates a candidate secret and shows it for confirmation.
//
// The secret is stored as pending and only becomes the account's real secret once
// a code generated from it has been verified. Enrolling a secret the user never
// managed to scan would otherwise lock them out of their own account.
func (s *Server) handleMFAStart(c *gin.Context) {
	rec, ok := s.ownAccount(c)
	if !ok {
		return
	}

	if rec.GetBool("mfaEnabled") {
		s.setFlash(c, "warning", "Two-factor authentication is already enabled. Disable it first.")
		c.Redirect(http.StatusSeeOther, "/preferences")
		return
	}

	secret, err := identity.GenerateTOTPSecret()
	if err != nil {
		s.setFlash(c, "danger", "Could not generate a secret: "+err.Error())
		c.Redirect(http.StatusSeeOther, "/preferences")
		return
	}

	rec.Set("totpPendingSecret", secret)
	if err := s.app.Save(rec); err != nil {
		s.setFlash(c, "danger", "Could not save the secret: "+err.Error())
		c.Redirect(http.StatusSeeOther, "/preferences")
		return
	}

	s.setFlash(c, "info", "Scan the secret with your authenticator app, then confirm with a code.")
	c.Redirect(http.StatusSeeOther, "/preferences")
}

// handleMFAEnable confirms the candidate secret and enables the second factor.
func (s *Server) handleMFAEnable(c *gin.Context) {
	rec, ok := s.ownAccount(c)
	if !ok {
		return
	}

	pending := rec.GetString("totpPendingSecret")
	if pending == "" {
		s.setFlash(c, "warning", "Start the enrolment first.")
		c.Redirect(http.StatusSeeOther, "/preferences")
		return
	}

	code := strings.TrimSpace(c.PostForm("code"))
	if !identity.VerifyTOTP(pending, code, time.Now()) {
		s.setFlash(c, "danger", "That code is not valid for the secret shown. Check the app and try again.")
		c.Redirect(http.StatusSeeOther, "/preferences")
		return
	}

	codes, err := identity.GenerateRecoveryCodes()
	if err != nil {
		s.setFlash(c, "danger", "Could not generate recovery codes: "+err.Error())
		c.Redirect(http.StatusSeeOther, "/preferences")
		return
	}

	rec.Set("totpSecret", pending)
	rec.Set("totpPendingSecret", "")
	rec.Set("mfaEnabled", true)
	rec.Set("totpEnrolledAt", time.Now().UTC())
	rec.Set("recoveryCodeHashes", hashRecoveryCodes(codes))

	if err := s.app.Save(rec); err != nil {
		s.setFlash(c, "danger", "Could not enable two-factor authentication: "+err.Error())
		c.Redirect(http.StatusSeeOther, "/preferences")
		return
	}

	s.audit(c, auditEntry{
		Event:      audit.EventMFAEnrolled,
		TargetType: "account",
		TargetID:   rec.Id,
		Severity:   audit.SeverityWarning,
		Summary:    "Two-factor authentication enabled with an authenticator app.",
		SubjectID:  rec.Id, SubjectEmail: rec.Email(),
	})

	// Shown once, and only once: they are stored as digests, so the plain values
	// cannot be produced again.
	s.setFlash(c, "success", "Two-factor authentication enabled. Your recovery codes: "+
		strings.Join(codes, "  ")+
		"  — write these down now, they will not be shown again.")
	c.Redirect(http.StatusSeeOther, "/preferences")
}

// handleMFADisable turns the second factor off.
//
// It requires both the password and a current code, so neither a walked-away
// session nor a stolen phone is enough on its own to remove the factor.
func (s *Server) handleMFADisable(c *gin.Context) {
	rec, ok := s.ownAccount(c)
	if !ok {
		return
	}

	if !rec.GetBool("mfaEnabled") {
		s.setFlash(c, "warning", "Two-factor authentication is not enabled.")
		c.Redirect(http.StatusSeeOther, "/preferences")
		return
	}

	if !rec.ValidatePassword(c.PostForm("password")) {
		s.setFlash(c, "danger", "Your password is not correct.")
		c.Redirect(http.StatusSeeOther, "/preferences")
		return
	}

	code := strings.TrimSpace(c.PostForm("code"))
	if !identity.VerifyTOTP(rec.GetString("totpSecret"), code, time.Now()) {
		stored := rec.GetStringSlice("recoveryCodeHashes")
		if _, ok := identity.ConsumeRecoveryCode(stored, code); !ok {
			s.setFlash(c, "danger", "A current code is required to disable two-factor authentication.")
			c.Redirect(http.StatusSeeOther, "/preferences")
			return
		}
	}

	rec.Set("mfaEnabled", false)
	rec.Set("totpSecret", "")
	rec.Set("totpPendingSecret", "")
	rec.Set("totpEnrolledAt", nil)
	rec.Set("recoveryCodeHashes", []string{})

	if err := s.app.Save(rec); err != nil {
		s.setFlash(c, "danger", "Could not disable two-factor authentication: "+err.Error())
		c.Redirect(http.StatusSeeOther, "/preferences")
		return
	}

	s.audit(c, auditEntry{
		Event:      audit.EventMFADisabled,
		TargetType: "account",
		TargetID:   rec.Id,
		Severity:   audit.SeverityCritical,
		Summary:    "Two-factor authentication disabled.",
		SubjectID:  rec.Id, SubjectEmail: rec.Email(),
	})
	s.setFlash(c, "success", "Two-factor authentication disabled.")
	c.Redirect(http.StatusSeeOther, "/preferences")
}

// handleMFACancel abandons a pending enrolment.
func (s *Server) handleMFACancel(c *gin.Context) {
	rec, ok := s.ownAccount(c)
	if !ok {
		return
	}

	rec.Set("totpPendingSecret", "")
	if err := s.app.Save(rec); err != nil {
		s.setFlash(c, "danger", "Could not cancel the enrolment: "+err.Error())
		c.Redirect(http.StatusSeeOther, "/preferences")
		return
	}

	s.setFlash(c, "success", "Enrolment cancelled.")
	c.Redirect(http.StatusSeeOther, "/preferences")
}

// handleMFARegenerate issues a fresh set of recovery codes.
func (s *Server) handleMFARegenerate(c *gin.Context) {
	rec, ok := s.ownAccount(c)
	if !ok {
		return
	}

	if !rec.GetBool("mfaEnabled") {
		s.setFlash(c, "warning", "Two-factor authentication is not enabled.")
		c.Redirect(http.StatusSeeOther, "/preferences")
		return
	}
	if !rec.ValidatePassword(c.PostForm("password")) {
		s.setFlash(c, "danger", "Your password is not correct.")
		c.Redirect(http.StatusSeeOther, "/preferences")
		return
	}

	codes, err := identity.GenerateRecoveryCodes()
	if err != nil {
		s.setFlash(c, "danger", "Could not generate recovery codes: "+err.Error())
		c.Redirect(http.StatusSeeOther, "/preferences")
		return
	}

	rec.Set("recoveryCodeHashes", hashRecoveryCodes(codes))
	if err := s.app.Save(rec); err != nil {
		s.setFlash(c, "danger", "Could not store the recovery codes: "+err.Error())
		c.Redirect(http.StatusSeeOther, "/preferences")
		return
	}

	s.audit(c, auditEntry{
		Event:      audit.EventRecoveryRegenerated,
		TargetType: "account",
		TargetID:   rec.Id,
		Severity:   audit.SeverityWarning,
		Summary:    "Recovery codes regenerated; the previous set no longer works.",
		SubjectID:  rec.Id, SubjectEmail: rec.Email(),
	})
	s.setFlash(c, "success", "New recovery codes: "+strings.Join(codes, "  ")+
		"  — the previous set no longer works.")
	c.Redirect(http.StatusSeeOther, "/preferences")
}

// ownAccount loads the signed-in account, refusing a break-glass superuser that
// has no `users` row.
func (s *Server) ownAccount(c *gin.Context) (*core.Record, bool) {
	actor := s.actor(c)
	if actor == nil {
		s.redirectToLogin(c)
		return nil, false
	}
	if actor.IsSuperuser {
		s.setFlash(c, "warning", "A break-glass superuser manages its own credentials in the PocketBase dashboard.")
		c.Redirect(http.StatusSeeOther, "/preferences")
		return nil, false
	}

	rec, err := s.app.FindRecordById(rbac.CollectionUsers, actor.UserID)
	if err != nil || rec == nil {
		s.setFlash(c, "danger", "Could not load your account.")
		c.Redirect(http.StatusSeeOther, "/preferences")
		return nil, false
	}
	return rec, true
}

// hashRecoveryCodes turns freshly issued codes into the digests that are stored.
func hashRecoveryCodes(codes []string) []string {
	out := make([]string, 0, len(codes))
	for _, c := range codes {
		out = append(out, identity.HashRecoveryCode(c))
	}
	return out
}

// mfaEnrolment is the view state of the enrolment card.
type mfaEnrolment struct {
	Enabled       bool
	PendingSecret string
	PendingURI    string
	EnrolledAt    string
	RecoveryLeft  int
}

// mfaStateFor builds the enrolment card's state.
func (s *Server) mfaStateFor(rec *core.Record, issuer string) mfaEnrolment {
	if rec == nil {
		return mfaEnrolment{}
	}

	state := mfaEnrolment{
		Enabled:      rec.GetBool("mfaEnabled"),
		RecoveryLeft: len(rec.GetStringSlice("recoveryCodeHashes")),
	}

	if at := rec.GetDateTime("totpEnrolledAt"); !at.IsZero() {
		state.EnrolledAt = at.Time().Local().Format("2 Jan 2006 15:04")
	}

	if pending := rec.GetString("totpPendingSecret"); pending != "" {
		state.PendingSecret = pending
		state.PendingURI = identity.TOTPProvisioningURI(pending, rec.Email(), issuer)
	}

	return state
}
