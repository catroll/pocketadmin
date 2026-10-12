package server

import (
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/pocketbase/pocketbase/core"

	"pocketadmin/internal/permission"
)

// Account statuses, as seeded on the users collection.
const (
	accountActive   = "active"
	accountInactive = "inactive"
	accountBanned   = "banned"
)

// Lockout policy.
const (
	// maxFailedAttempts is how many consecutive failures lock an account.
	maxFailedAttempts = 8

	// lockoutDuration is how long a lock lasts before it expires on its own. An
	// administrator can clear it sooner from the account screen.
	lockoutDuration = 15 * time.Minute
)

// registerAccountActionRoutes mounts the account lifecycle actions.
//
// These are separate from the profile routes because they change the account
// itself rather than what it is attached to: banning, deleting, resetting a
// credential and ending sessions are all account-level decisions.
func (s *Server) registerAccountActionRoutes(panel *gin.RouterGroup) {
	s.guarded(panel, http.MethodPost, "/identity/accounts/:id/status",
		permission.IdentityAccountUpdate, s.handleAccountStatus)
	s.guarded(panel, http.MethodPost, "/identity/accounts/:id/password",
		permission.IdentityAccountUpdate, s.handleAccountPasswordReset)
	s.guarded(panel, http.MethodPost, "/identity/accounts/:id/delete",
		permission.IdentityAccountDelete, s.handleAccountSoftDelete)
	s.guarded(panel, http.MethodPost, "/identity/accounts/:id/restore",
		permission.IdentityAccountUpdate, s.handleAccountRestore)
	s.guarded(panel, http.MethodPost, "/identity/accounts/:id/sessions/revoke-all",
		permission.IdentitySessionRevoke, s.handleAccountRevokeAllSessions)
}

// handleAccountStatus changes an account's status.
//
// Banning revokes every session immediately, so access ends on the next request
// rather than whenever the token would have expired.
func (s *Server) handleAccountStatus(c *gin.Context) {
	user, ok := s.findAccount(c)
	if !ok {
		return
	}

	if user.GetBool("isProtected") && s.isSelf(c, user.Id) {
		s.setFlash(c, "danger", "You cannot change the status of your own protected account.")
		c.Redirect(http.StatusSeeOther, "/identity/accounts/"+user.Id)
		return
	}

	status := strings.TrimSpace(c.PostForm("status"))
	switch status {
	case accountActive, accountInactive, accountBanned:
	default:
		s.setFlash(c, "danger", "Unrecognised account status.")
		c.Redirect(http.StatusSeeOther, "/identity/accounts/"+user.Id)
		return
	}

	previous := user.GetString("status")
	user.Set("status", status)

	// Clearing the lock is part of reactivating, so an operator can unlock a
	// locked-out account without waiting for the timer.
	if status == accountActive {
		user.Set("failedAttempts", 0)
		user.Set("lockedUntil", nil)
	}

	if err := s.app.Save(user); err != nil {
		s.setFlash(c, "danger", "Could not change the status: "+strings.Join(validationMessages(err), "; "))
		c.Redirect(http.StatusSeeOther, "/identity/accounts/"+user.Id)
		return
	}

	if status == accountBanned || status == accountInactive {
		s.revokeForStatusChange(user.Id, status)
		s.resolver.Invalidate(user.Id)
	}

	s.audit(c, auditEntry{
		Action:     auditStatusChange,
		Resource:   "account",
		ResourceID: user.Id,
		Risk:       "medium",
		Summary:    fmt.Sprintf("Account status changed from %s to %s.", previous, status),
		Detail:     map[string]any{"from": previous, "to": status},
		SubjectID:  user.Id, SubjectEmail: user.Email(),
	})
	s.setFlash(c, "success", fmt.Sprintf("Account status changed to %s.", status))
	c.Redirect(http.StatusSeeOther, "/identity/accounts/"+user.Id)
}

// handleAccountPasswordReset sets a new password and ends every session.
//
// Ending the sessions is the point rather than a side effect: a password reset is
// how an operator responds to a suspected compromise, and leaving the old
// sessions alive would defeat it.
func (s *Server) handleAccountPasswordReset(c *gin.Context) {
	user, ok := s.findAccount(c)
	if !ok {
		return
	}

	password := c.PostForm("password")
	confirm := c.PostForm("passwordConfirm")

	if len(password) < 8 {
		s.setFlash(c, "danger", "The new password must be at least 8 characters.")
		c.Redirect(http.StatusSeeOther, "/identity/accounts/"+user.Id)
		return
	}
	if password != confirm {
		s.setFlash(c, "danger", "The two passwords do not match.")
		c.Redirect(http.StatusSeeOther, "/identity/accounts/"+user.Id)
		return
	}

	user.SetPassword(password)
	user.Set("passwordChangedAt", time.Now().UTC())
	user.Set("failedAttempts", 0)
	user.Set("lockedUntil", nil)

	if err := s.app.Save(user); err != nil {
		s.setFlash(c, "danger", "Could not set the password: "+strings.Join(validationMessages(err), "; "))
		c.Redirect(http.StatusSeeOther, "/identity/accounts/"+user.Id)
		return
	}

	if err := s.revokeSessionsForUser(user.Id, "", sessionRevokedPassword); err != nil {
		slog.Warn("could not revoke sessions after a password reset", "user", user.Id, "error", err)
	}

	s.audit(c, auditEntry{
		Action:     auditPasswordReset,
		Resource:   "account",
		ResourceID: user.Id,
		Risk:       "medium",
		Summary:    "Password set by an administrator; every session revoked.",
		SubjectID:  user.Id, SubjectEmail: user.Email(),
	})
	s.setFlash(c, "success", "Password set and every session revoked.")
	c.Redirect(http.StatusSeeOther, "/identity/accounts/"+user.Id)
}

// handleAccountSoftDelete marks an account deleted and ends its sessions.
//
// The row is kept: audit history, orders and HR records reference the account, so
// removing it would orphan them. A soft-deleted account cannot sign in and is
// hidden from the list.
func (s *Server) handleAccountSoftDelete(c *gin.Context) {
	user, ok := s.findAccount(c)
	if !ok {
		return
	}

	if s.isSelf(c, user.Id) {
		s.setFlash(c, "danger", "You cannot delete the account you are signed in as.")
		c.Redirect(http.StatusSeeOther, "/identity/accounts/"+user.Id)
		return
	}
	if user.GetBool("isProtected") {
		s.setFlash(c, "danger", "The bootstrap account cannot be deleted.")
		c.Redirect(http.StatusSeeOther, "/identity/accounts/"+user.Id)
		return
	}

	user.Set("deletedAt", time.Now().UTC())
	user.Set("status", accountInactive)

	if err := s.app.Save(user); err != nil {
		s.setFlash(c, "danger", "Could not delete the account: "+err.Error())
		c.Redirect(http.StatusSeeOther, "/identity/accounts/"+user.Id)
		return
	}

	if err := s.revokeSessionsForUser(user.Id, "", sessionRevokedBanned); err != nil {
		slog.Warn("could not revoke sessions after a soft delete", "user", user.Id, "error", err)
	}
	s.resolver.Invalidate(user.Id)

	s.audit(c, auditEntry{
		Action:     auditSoftDelete,
		Resource:   "account",
		ResourceID: user.Id,
		Risk:       "high",
		Summary:    "Account marked deleted; sessions revoked.",
		SubjectID:  user.Id, SubjectEmail: user.Email(),
	})
	s.setFlash(c, "success", "Account marked deleted and its sessions revoked. The record is kept.")
	c.Redirect(http.StatusSeeOther, "/identity/accounts")
}

// handleAccountRestore undoes a soft delete.
func (s *Server) handleAccountRestore(c *gin.Context) {
	user, ok := s.findAccount(c)
	if !ok {
		return
	}

	user.Set("deletedAt", nil)
	user.Set("status", accountActive)
	if err := s.app.Save(user); err != nil {
		s.setFlash(c, "danger", "Could not restore the account: "+err.Error())
		c.Redirect(http.StatusSeeOther, "/identity/accounts/"+user.Id)
		return
	}

	s.audit(c, auditEntry{
		Action:     auditRestore,
		Resource:   "account",
		ResourceID: user.Id,
		Risk:       "medium",
		Summary:    "Account restored.",
		SubjectID:  user.Id, SubjectEmail: user.Email(),
	})
	s.setFlash(c, "success", "Account restored. Its profiles are unchanged.")
	c.Redirect(http.StatusSeeOther, "/identity/accounts/"+user.Id)
}

// handleAccountRevokeAllSessions ends every session for an account.
func (s *Server) handleAccountRevokeAllSessions(c *gin.Context) {
	user, ok := s.findAccount(c)
	if !ok {
		return
	}

	if err := s.revokeSessionsForUser(user.Id, "", sessionRevokedAdmin); err != nil {
		s.setFlash(c, "danger", "Could not revoke the sessions: "+err.Error())
		c.Redirect(http.StatusSeeOther, "/identity/accounts/"+user.Id)
		return
	}

	s.audit(c, auditEntry{
		Action:     auditSessionRevokeAll,
		Resource:   "account",
		ResourceID: user.Id,
		Risk:       "medium",
		Summary:    "Every session for the account was revoked.",
		SubjectID:  user.Id, SubjectEmail: user.Email(),
	})
	s.setFlash(c, "success", "Every session for this account was revoked.")
	c.Redirect(http.StatusSeeOther, "/identity/accounts/"+user.Id)
}

// revokeForStatusChange ends the sessions a status change invalidates.
func (s *Server) revokeForStatusChange(userID, status string) {
	reason := sessionRevokedBanned
	if status == accountInactive {
		reason = sessionRevokedProfileGone
	}
	if err := s.revokeSessionsForUser(userID, "", reason); err != nil {
		slog.Warn("could not revoke sessions after a status change", "user", userID, "error", err)
	}
}

// isSelf reports whether the signed-in account is the one being acted on.
func (s *Server) isSelf(c *gin.Context, userID string) bool {
	actor := s.actor(c)
	return actor != nil && actor.UserID == userID
}

// ---------------------------------------------------------------------------
// Lockout
// ---------------------------------------------------------------------------

// lockState describes whether an account is currently locked out.
type lockState struct {
	Locked    bool
	Until     time.Time
	Attempts  int
	Remaining int
}

// checkLock reports whether an account is locked out.
//
// A lock expires on its own, so a locked account does not need an administrator
// to become usable again -- but one can clear it sooner.
func checkLock(rec *core.Record) lockState {
	until := rec.GetDateTime("lockedUntil")
	attempts := rec.GetInt("failedAttempts")

	state := lockState{Attempts: attempts}
	if until.IsZero() {
		return state
	}

	if until.Time().After(time.Now()) {
		state.Locked = true
		state.Until = until.Time()
		return state
	}

	// The lock has expired; report it as clear rather than as a lock in the past.
	return state
}

// lockAccount records a failed attempt and locks the account once the threshold
// is reached.
func (s *Server) lockAccount(rec *core.Record) {
	attempts := rec.GetInt("failedAttempts") + 1
	rec.Set("failedAttempts", attempts)

	if attempts >= maxFailedAttempts {
		rec.Set("lockedUntil", time.Now().UTC().Add(lockoutDuration))
		slog.Warn("account locked after repeated failed sign-ins",
			"user", rec.Id, "attempts", attempts)
	}

	if err := s.app.Save(rec); err != nil {
		slog.Warn("could not record a failed sign-in attempt", "user", rec.Id, "error", err)
	}
}

// clearFailedAttempts resets the counter after a successful sign-in.
func (s *Server) clearFailedAttempts(rec *core.Record) {
	if rec.GetInt("failedAttempts") == 0 && rec.GetDateTime("lockedUntil").IsZero() {
		return
	}
	rec.Set("failedAttempts", 0)
	rec.Set("lockedUntil", nil)
}
