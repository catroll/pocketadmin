package server

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"net/http"
	"net/url"
	"strings"

	"github.com/gin-contrib/sessions"
	"github.com/gin-gonic/gin"
	"github.com/pocketbase/pocketbase/core"

	"pocketadmin/internal/rbac"
)

const (
	// formCSRFField is the hidden input name every panel form carries.
	formCSRFField = "_csrf"

	sessionUserKey = "uid"
	sessionCSRFKey = "csrf"
	flashKey       = "flash"
)

// csrf issues and validates the per-session CSRF token for the panel's own
// state-changing routes.
//
// PocketBase's API routes are deliberately excluded from this middleware: they
// authenticate with a bearer token instead of the panel cookie, so they cannot
// be driven by a cross-site form post.
func (s *Server) csrf() gin.HandlerFunc {
	return func(c *gin.Context) {
		sess := sessions.Default(c)

		token, _ := sess.Get(sessionCSRFKey).(string)
		if token == "" {
			var err error
			if token, err = randomToken(); err != nil {
				c.AbortWithStatus(http.StatusInternalServerError)
				return
			}
			sess.Set(sessionCSRFKey, token)
			if err := sess.Save(); err != nil {
				c.AbortWithStatus(http.StatusInternalServerError)
				return
			}
		}
		c.Set(ctxCSRF, token)

		if isUnsafeMethod(c.Request.Method) {
			sent := c.PostForm(formCSRFField)
			if sent == "" {
				sent = c.GetHeader("X-CSRF-Token")
			}
			if subtle.ConstantTimeCompare([]byte(sent), []byte(token)) != 1 {
				c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
					"error": "invalid or missing CSRF token; reload the page and try again",
				})
				return
			}
		}

		c.Next()
	}
}

// limitBody caps the size of panel request bodies so that an oversized upload
// cannot exhaust memory before PocketBase validates the individual fields.
func limitBody(max int64) gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.Body != nil {
			c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, max)
		}
		c.Next()
	}
}

// requireAuth resolves the signed-in account from the session cookie and aborts
// with a redirect when it is missing or no longer valid.
//
// Two identity paths are supported:
//
//   - a `users` account, which is the platform's normal sign-in identity. It is
//     accepted even without an admin profile so the account can reach its own
//     preferences; every module route then fails closed on its permission.
//   - a PocketBase `_superusers` record, kept as break-glass so an operator can
//     always repair a damaged RBAC configuration.
func (s *Server) requireAuth() gin.HandlerFunc {
	return func(c *gin.Context) {
		sess := sessions.Default(c)

		uid, _ := sess.Get(sessionUserKey).(string)
		if uid == "" {
			s.clearSession(c)
			s.redirectToLogin(c)
			return
		}

		switch sess.Get(sessionKindKey) {
		case sessionKindSuperuser:
			rec, err := s.app.FindRecordById(core.CollectionNameSuperusers, uid)
			if err != nil || rec == nil || !rec.IsSuperuser() {
				s.clearSession(c)
				s.redirectToLogin(c)
				return
			}
			c.Set(ctxActor, rbac.SuperuserActor(rec))
			c.Next()
			return

		case sessionKindUser:
			// The session record is the authority, not the cookie. Revoking a row
			// has to end the session on the next request, or revoke-one, revoke-all,
			// a ban and a password reset would all be cosmetic until the holder
			// happened to sign out.
			token, _ := sess.Get(sessionTokenKey).(string)
			sessionRec, err := s.validateSessionRecord(token)
			if err != nil {
				s.clearSession(c)
				s.redirectToLogin(c)
				return
			}
			s.touchSessionRecord(sessionRec)

			rec, err := s.app.FindRecordById(rbac.CollectionUsers, uid)
			if err != nil || rec == nil || rec.GetString("status") == "banned" ||
				!rec.GetDateTime("deletedAt").IsZero() {
				s.clearSession(c)
				s.redirectToLogin(c)
				return
			}

			a, err := s.resolver.Resolve(rec)
			if err != nil {
				s.clearSession(c)
				s.redirectToLogin(c)
				return
			}

			c.Set(ctxActor, a)
			c.Set(ctxUser, rec)
			c.Next()
			return
		}

		s.clearSession(c)
		s.redirectToLogin(c)
	}
}

// clearSession drops the panel session cookie.
func (s *Server) clearSession(c *gin.Context) {
	sess := sessions.Default(c)
	sess.Clear()
	_ = sess.Save()
}

// currentUser returns the authenticated `users` record, or nil.
//
// A break-glass superuser has no `users` record, so callers that need a label
// should prefer currentActor.
func (s *Server) currentUser(c *gin.Context) *core.Record {
	raw, ok := c.Get(ctxUser)
	if !ok {
		return nil
	}
	rec, _ := raw.(*core.Record)
	return rec
}

func (s *Server) redirectToLogin(c *gin.Context) {
	next := c.Request.URL.RequestURI()
	target := "/login"
	if next != "" && next != "/" {
		target += "?next=" + url.QueryEscape(next)
	}
	c.Redirect(http.StatusSeeOther, target)
	c.Abort()
}

// handleLoginForm renders the sign-in page.
func (s *Server) handleLoginForm(c *gin.Context) {
	if s.currentUserFromSession(c) != nil {
		c.Redirect(http.StatusSeeOther, safeNext(c.Query("next")))
		return
	}

	s.render(c, http.StatusOK, "login.html", struct {
		base
		Next  string
		Email string
		Error string
	}{
		base:  s.newBase(c, "Sign in", ""),
		Next:  c.Query("next"),
		Email: "",
	})
}

func (s *Server) renderLoginFailure(c *gin.Context, email, next, message string) {
	s.render(c, http.StatusUnauthorized, "login.html", struct {
		base
		Next  string
		Email string
		Error string
	}{
		base:  s.newBase(c, "Sign in", ""),
		Next:  next,
		Email: email,
		Error: message,
	})
}

// currentUserFromSession is a cheap "is there a session?" probe used before the
// auth middleware runs (i.e. on the login page itself). It deliberately does not
// touch the database beyond resolving the account.
func (s *Server) currentUserFromSession(c *gin.Context) *core.Record {
	sess := sessions.Default(c)

	uid, _ := sess.Get(sessionUserKey).(string)
	if uid == "" {
		return nil
	}

	switch sess.Get(sessionKindKey) {
	case sessionKindSuperuser:
		rec, err := s.app.FindRecordById(core.CollectionNameSuperusers, uid)
		if err != nil || rec == nil || !rec.IsSuperuser() {
			return nil
		}
		return rec
	case sessionKindUser:
		rec, err := s.app.FindRecordById(rbac.CollectionUsers, uid)
		if err != nil || rec == nil || rec.GetString("status") == "banned" ||
			!rec.GetDateTime("deletedAt").IsZero() {
			return nil
		}
		return rec
	}

	return nil
}

// setFlash stores a one-shot notification for the next rendered page.
func (s *Server) setFlash(c *gin.Context, kind, message string) {
	sess := sessions.Default(c)
	sess.Set(flashKey, []string{kind, message})
	_ = sess.Save()
}

// loadFlash promotes a pending flash message into the request context.
func (s *Server) loadFlash() gin.HandlerFunc {
	return func(c *gin.Context) {
		sess := sessions.Default(c)
		if raw := sess.Get(flashKey); raw != nil {
			if pair, ok := raw.([]string); ok && len(pair) == 2 {
				c.Set(flashKey, &flash{Type: pair[0], Message: pair[1]})
			}
			sess.Delete(flashKey)
			_ = sess.Save()
		}
		c.Next()
	}
}

func (s *Server) flashFrom(c *gin.Context) *flash {
	if raw, ok := c.Get(flashKey); ok {
		if f, ok := raw.(*flash); ok {
			return f
		}
	}
	return nil
}

func randomToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

func isUnsafeMethod(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return false
	default:
		return true
	}
}

// safeNext only allows same-origin absolute paths, which prevents the login
// redirect from being turned into an open redirect.
func safeNext(next string) string {
	if next == "" || !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") {
		return "/dashboard"
	}
	return next
}
