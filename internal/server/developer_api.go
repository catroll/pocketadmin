package server

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"pocketadmin/internal/announcement"
	"pocketadmin/internal/developer"
	"pocketadmin/internal/jobs"
	"pocketadmin/internal/monitoring"
	"pocketadmin/internal/permission"
)

// registerDeveloperAPIRoutes mounts the machine-facing JSON API.
//
// It lives outside /api and /_ on purpose: those prefixes belong to PocketBase,
// which dispatches them before Gin sees the request. A JSON API under /api would
// be PocketBase's, not this platform's.
// It takes the engine rather than the panel group on purpose. The panel group
// carries the session middleware, which would intercept every key-authenticated
// request and redirect it to the sign-in page before the key middleware ran.
func (s *Server) registerDeveloperAPIRoutes(r *gin.Engine) {
	api := r.Group("/developer/api/v1", s.requireAPIKey())

	api.GET("/whoami", s.handleAPIWhoami)
	api.GET("/announcements", s.requireAPIPermission(permission.AnnouncementList), s.handleAPIAnnouncements)
	api.GET("/jobs", s.requireAPIPermission(permission.JobsJobList), s.handleAPIJobs)

	// A node reports its figures here. The permission is the gate and the key's own
	// node binding is the identity: a key that carries the permission but is bound to
	// no machine is refused rather than allowed to report for any of them.
	s.apiGuarded(api, http.MethodPost, "/metrics", permission.MetricsSubmit, s.handleAPIMetrics)
}

// requireAPIPermission refuses a caller whose effective authority lacks a point.
//
// It is a separate guard from the panel's because a machine caller gets JSON, and
// because the answer here comes from the key's scoped authority rather than from a
// session's roles.
func (s *Server) requireAPIPermission(code string) gin.HandlerFunc {
	return func(c *gin.Context) {
		actor := s.actor(c)
		if actor == nil || !actor.Can(code) {
			abortAPI(c, http.StatusForbidden, "insufficient_scope",
				"This key does not carry the permission required for that resource.")
			return
		}
		c.Next()
	}
}

// handleAPIWhoami reports what the key can do.
//
// It is the endpoint an integrator uses to find out why something is refused, so
// it lists both what the key was scoped for and what it actually carries: the two
// differ whenever the owner has lost a permission, and that difference is the
// answer.
func (s *Server) handleAPIWhoami(c *gin.Context) {
	rec := s.apiKey(c)
	actor := s.actor(c)

	declared := permissionCodesIn(s.app, rec.GetStringSlice("scopes"))

	effective := make([]string, 0, len(actor.Permissions))
	for code := range actor.Permissions {
		effective = append(effective, code)
	}

	owner, _ := s.app.FindRecordById("users", rec.GetString("owner"))
	ownerEmail := ""
	if owner != nil {
		ownerEmail = owner.Email()
	}

	c.JSON(http.StatusOK, gin.H{
		// Reported because it is the first thing to check when a call is refused:
		// an actor whose profile is missing or inactive denies everything,
		// whatever its scope list says.
		"actor": gin.H{
			"adminProfile": actor.AdminProfileID != "",
			"adminStatus":  actor.AdminStatus,
			"isAdmin":      actor.IsAdmin(),
		},
		"key": gin.H{
			"name":      rec.GetString("name"),
			"prefix":    rec.GetString("keyPrefix"),
			"status":    rec.GetString("status"),
			"expiresAt": rec.GetDateTime("expiresAt"),
		},
		"owner":     ownerEmail,
		"declared":  declared,
		"effective": effective,
	})
}

// handleAPIAnnouncements returns the published announcements a key's owner may
// see.
func (s *Server) handleAPIAnnouncements(c *gin.Context) {
	// The audience filter is the same one the panel uses, resolved from the key's
	// owner, so a key cannot read an announcement its owner could not.
	actor := s.actor(c)
	ownerRec, _ := s.app.FindRecordById("users", actor.UserID)

	filter, params := s.announcements.Viewable(ownerRec, actor.Can(permission.AnnouncementUpdate))

	records, err := s.app.FindRecordsByFilter(announcement.CollectionAnnouncements,
		filter, "-pinned,-publishAt", 100, 0, params)
	if err != nil {
		abortAPI(c, http.StatusInternalServerError, "read_failed", "The announcements could not be read.")
		return
	}

	items := make([]gin.H, 0, len(records))
	for _, rec := range records {
		items = append(items, gin.H{
			"id":        rec.Id,
			"title":     rec.GetString("title"),
			"type":      rec.GetString("type"),
			"level":     rec.GetString("level"),
			"status":    rec.GetString("status"),
			"pinned":    rec.GetBool("pinned"),
			"publishAt": rec.GetDateTime("publishAt"),
		})
	}

	c.JSON(http.StatusOK, gin.H{"items": items, "totalItems": len(items)})
}

// handleAPIJobs returns the scheduled tasks and their state.
func (s *Server) handleAPIJobs(c *gin.Context) {
	records, err := s.app.FindRecordsByFilter(jobs.CollectionJobs, "", "name", 200, 0)
	if err != nil {
		abortAPI(c, http.StatusInternalServerError, "read_failed", "The tasks could not be read.")
		return
	}

	items := make([]gin.H, 0, len(records))
	for _, rec := range records {
		items = append(items, gin.H{
			"code":      rec.GetString("code"),
			"name":      rec.GetString("name"),
			"cron":      rec.GetString("cron"),
			"status":    rec.GetString("status"),
			"lastRun":   rec.GetDateTime("lastRunAt"),
			"nextRun":   rec.GetDateTime("nextRunAt"),
			"lastState": rec.GetString("lastStatus"),
		})
	}

	c.JSON(http.StatusOK, gin.H{"items": items, "totalItems": len(items)})
}

var _ = developer.StatusActive

// handleAPIMetrics accepts a host reading from a remote node.
//
// The key must be bound to a machine. A key that carries the submission permission but
// names no machine is refused with a message that says so, because the alternative --
// letting it report for whichever machine it likes -- would let one compromised agent
// rewrite another machine's history.
func (s *Server) handleAPIMetrics(c *gin.Context) {
	key := s.apiKey(c)
	if key == nil {
		abortAPI(c, http.StatusUnauthorized, "unauthenticated", "A key is required.")
		return
	}

	nodeID := key.GetString("monitorNode")
	if nodeID == "" {
		abortAPI(c, http.StatusForbidden, "no_node",
			"This key carries the submission permission but is not bound to a machine. "+
				"Bind it to one so its readings are recorded against the right machine.")
		return
	}

	node, err := s.app.FindRecordById(monitoring.CollectionNodes, nodeID)
	if err != nil || node == nil {
		abortAPI(c, http.StatusForbidden, "no_node",
			"This key is bound to a machine that no longer exists.")
		return
	}

	var submission monitoring.Submission
	if err := c.ShouldBindJSON(&submission); err != nil {
		abortAPI(c, http.StatusBadRequest, "malformed",
			"The body is not a host reading: "+err.Error())
		return
	}

	now := time.Now()
	if err := submission.Validate(now); err != nil {
		abortAPI(c, http.StatusUnprocessableEntity, "implausible", err.Error())
		return
	}

	stored, err := monitoring.Store(s.app, node, submission, now)
	if err != nil {
		abortAPI(c, http.StatusInternalServerError, "store_failed", err.Error())
		return
	}

	if !stored {
		// A machine the operator has stopped recording. The agent is told plainly
		// rather than having its readings silently dropped.
		c.JSON(http.StatusAccepted, gin.H{
			"status": "ignored",
			"reason": "reporting is switched off for this machine",
		})
		return
	}

	c.JSON(http.StatusAccepted, gin.H{"status": "recorded", "node": node.GetString("code")})
}
