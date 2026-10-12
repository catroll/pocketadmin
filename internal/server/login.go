package server

import (
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/gin-contrib/sessions"
	"github.com/gin-gonic/gin"
	"github.com/pocketbase/pocketbase/core"
	"golang.org/x/crypto/bcrypt"

	"pocketadmin/internal/rbac"
)

// handleLogin authenticates an operator.
//
// Two identity paths are tried in order:
//
//  1. A platform `users` account. This is the normal path and requires an active
//     admin profile, because back-office access is a grant rather than a
//     property of the account.
//  2. A PocketBase `_superusers` record, kept as break-glass so an operator can
//     always reach the panel and repair a damaged RBAC configuration.
//
// The two are checked in that order so that a real platform account is never
// shadowed by a superuser with the same address.
func (s *Server) handleLogin(c *gin.Context) {
	email := strings.TrimSpace(c.PostForm("email"))
	password := c.PostForm("password")
	next := safeNext(c.PostForm("next"))

	if rec, err := s.app.FindAuthRecordByEmail(rbac.CollectionUsers, email); err == nil && rec != nil {
		s.loginPlatformAccount(c, rec, email, password, next)
		return
	}

	if rec, err := s.app.FindAuthRecordByEmail(core.CollectionNameSuperusers, email); err == nil && rec != nil {
		if rec.ValidatePassword(password) && rec.IsSuperuser() {
			if s.startSession(c, rec.Id, sessionKindSuperuser) {
				s.logLogin(c, "", email, "success", "break-glass superuser", "low")
				c.Redirect(http.StatusSeeOther, next)
				return
			}
			s.renderLoginFailure(c, email, next, "Could not start a session. Please try again.")
			return
		}
		s.logLogin(c, "", email, "failure", "invalid superuser password", "low")
		s.renderLoginFailure(c, email, next, "Invalid email or password.")
		return
	}

	// Burn a comparable amount of CPU so the response time does not reveal
	// whether the account exists.
	_ = bcrypt.CompareHashAndPassword([]byte(s.dummyPasswordHash), []byte(password))
	s.logLogin(c, "", email, "failure", "no matching account", "low")
	s.renderLoginFailure(c, email, next, "Invalid email or password.")
}

// loginPlatformAccount authenticates a `users` record and checks that it may use
// the back office.
func (s *Server) loginPlatformAccount(c *gin.Context, rec *core.Record, email, password, next string) {
	// A soft-deleted account keeps its row for history but must not sign in.
	if !rec.GetDateTime("deletedAt").IsZero() {
		s.logLogin(c, rec.Id, email, "failure", "account deleted", "medium")
		s.renderLoginFailure(c, email, next, "Invalid email or password.")
		return
	}

	// A lock is checked before the password so that a locked account cannot be
	// used to keep guessing while it is locked.
	if lock := checkLock(rec); lock.Locked {
		s.logLogin(c, rec.Id, email, "locked",
			"locked until "+lock.Until.Format(time.RFC3339), "medium")
		s.renderLoginFailure(c, email, next,
			"Too many failed attempts. Try again after "+lock.Until.Local().Format("15:04")+".")
		return
	}

	if !rec.ValidatePassword(password) {
		s.lockAccount(rec)
		s.logLogin(c, rec.Id, email, "failure", "invalid password", "low")
		s.renderLoginFailure(c, email, next, "Invalid email or password.")
		return
	}

	status := rec.GetString("status")
	if status == "banned" || status == "inactive" {
		s.logLogin(c, rec.Id, email, "locked", "account "+status, "medium")
		s.renderLoginFailure(c, email, next, "This account is not permitted to sign in.")
		return
	}

	actor, err := s.resolver.Resolve(rec)
	if err != nil {
		s.renderLoginFailure(c, email, next, "Could not load your access rights. Please try again.")
		return
	}

	// Signing in is allowed without an admin profile so the account can reach its
	// own preferences; back-office access is a separate grant.
	if !actor.IsAdmin() {
		s.logLogin(c, rec.Id, email, "failure", "no active admin profile", "medium")
		s.renderLoginFailure(c, email, next,
			"This account does not have back-office access. Ask an administrator to grant it.")
		return
	}

	// A correct password is only the first factor when one is enrolled. Parking
	// the sign-in here rather than issuing a session means an abandoned second
	// step leaves nothing usable behind.
	if rec.GetBool("mfaEnabled") {
		if !s.beginPendingMFA(c, rec, next) {
			s.renderLoginFailure(c, email, next, "Could not start the second step. Please try again.")
			return
		}
		s.logLogin(c, rec.Id, email, "mfa_required", "password accepted, awaiting the second factor", "low")
		c.Redirect(http.StatusSeeOther, "/login/mfa")
		return
	}

	if !s.startSession(c, rec.Id, sessionKindUser) {
		s.renderLoginFailure(c, email, next, "Could not start a session. Please try again.")
		return
	}

	now := time.Now().UTC()
	rec.Set("lastLoginAt", now)
	rec.Set("lastLoginIp", c.ClientIP())
	s.clearFailedAttempts(rec)
	if err := s.app.Save(rec); err != nil {
		slog.Warn("could not record the last sign-in", "user", rec.Id, "error", err)
	}

	s.logLogin(c, rec.Id, email, "success", "", "low")
	c.Redirect(http.StatusSeeOther, next)
}

// startSession writes the cookie session and its revocable database record.
func (s *Server) startSession(c *gin.Context, accountID, kind string) bool {
	sess := sessions.Default(c)

	token := ""
	if kind == sessionKindUser {
		var err error
		if token, err = s.createSessionRecord(c, accountID); err != nil {
			slog.Error("could not create a session record", "user", accountID, "error", err)
			return false
		}
	}

	sess.Set(sessionUserKey, accountID)
	sess.Set(sessionKindKey, kind)
	if token != "" {
		sess.Set(sessionTokenKey, token)
	}

	if err := sess.Save(); err != nil {
		slog.Error("could not persist the session cookie", "user", accountID, "error", err)
		return false
	}
	return true
}

// handleLogout revokes the session record and clears the cookie.
func (s *Server) handleLogout(c *gin.Context) {
	sess := sessions.Default(c)

	if token, _ := sess.Get(sessionTokenKey).(string); token != "" {
		if rec, err := s.validateSessionRecord(token); err == nil && rec != nil {
			if err := s.revokeSessionRecord(rec, sessionRevokedLogout); err != nil {
				slog.Warn("could not revoke the session record", "error", err)
			}
		}
	}

	s.clearSession(c)
	c.Redirect(http.StatusSeeOther, "/login")
}
