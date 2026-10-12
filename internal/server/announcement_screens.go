package server

import (
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/pocketbase/pocketbase/core"

	"pocketadmin/internal/announcement"
	"pocketadmin/internal/audit"
	"pocketadmin/internal/permission"
	"pocketadmin/internal/richtext"
)

// registerAnnouncementRoutes mounts the announcement screens.
//
// These are not generic resources: the list is filtered by audience, every row
// carries publish and archive actions that a generic form cannot express, and the
// detail page carries the read state.
func (s *Server) registerAnnouncementRoutes(panel *gin.RouterGroup) {
	s.readGuarded(panel, http.MethodGet, "/announcements", permission.AnnouncementList, s.handleAnnouncementList)
	s.readGuarded(panel, http.MethodGet, "/announcements/new", permission.AnnouncementCreate, s.handleAnnouncementForm)
	s.readGuarded(panel, http.MethodGet, "/announcements/:id", permission.AnnouncementList, s.handleAnnouncementDetail)
	s.readGuarded(panel, http.MethodGet, "/announcements/:id/edit", permission.AnnouncementUpdate, s.handleAnnouncementForm)

	s.guarded(panel, http.MethodPost, "/announcements/save", permission.AnnouncementCreate, s.handleAnnouncementSave)
	s.guarded(panel, http.MethodPost, "/announcements/:id/publish", permission.AnnouncementPublish, s.handleAnnouncementPublish)
	s.guarded(panel, http.MethodPost, "/announcements/:id/archive", permission.AnnouncementPublish, s.handleAnnouncementArchive)
	s.guarded(panel, http.MethodPost, "/announcements/:id/delete", permission.AnnouncementDelete, s.handleAnnouncementDelete)
	s.guarded(panel, http.MethodPost, "/announcements/:id/read", permission.AnnouncementList, s.handleAnnouncementRead)
}

// handleAnnouncementList renders what the signed-in account may see.
func (s *Server) handleAnnouncementList(c *gin.Context) {
	// An author sees everything; a reader sees what is aimed at them. The
	// distinction is the authoring permission, not whether the account happens to
	// have an admins record -- every account that can sign in has one.
	canAuthor := s.actor(c).Can(permission.AnnouncementUpdate)
	filter, params := s.announcements.Viewable(s.ownAccountQuiet(c), canAuthor)

	// Pinned first, then newest. A pin is a deliberate act and should win over
	// the calendar, but only within the reader's own view.
	if search := strings.TrimSpace(c.Query("q")); search != "" {
		filter += " && (title ~ {:q} || content ~ {:q})"
		params["q"] = search
	}
	if status := strings.TrimSpace(c.Query("status")); status != "" {
		filter += " && status = {:status}"
		params["status"] = status
	}

	records, err := s.app.FindRecordsByFilter(announcement.CollectionAnnouncements,
		filter, "-pinned,-publishAt,-created", 100, 0, params)
	if err != nil {
		s.renderErrorPage(c, http.StatusInternalServerError, "Could not load announcements", err.Error())
		return
	}

	read := s.readAnnouncementSet(c)

	rows := make([]announcementRow, 0, len(records))
	for _, rec := range records {
		rows = append(rows, announcementRow{
			Record:  rec,
			Title:   rec.GetString("title"),
			Type:    rec.GetString("type"),
			Level:   rec.GetString("level"),
			Status:  rec.GetString("status"),
			Pinned:  rec.GetBool("pinned"),
			Preview: richtext.SanitizePlain(rec.GetString("content")),
			Reads:   rec.GetInt("readCount"),
			IsRead:  read[rec.Id],
		})
	}

	actor := s.actor(c)

	s.render(c, http.StatusOK, "announcements.html", announcementsPage{
		base:       s.newBase(c, "Announcements", "announcement"),
		Rows:       rows,
		Query:      c.Query("q"),
		Status:     c.Query("status"),
		CanCreate:  actor.Can(permission.AnnouncementCreate),
		CanPublish: actor.Can(permission.AnnouncementPublish),
		CanDelete:  actor.Can(permission.AnnouncementDelete),
		Statuses: []string{announcement.StatusDraft, announcement.StatusScheduled,
			announcement.StatusPublished, announcement.StatusExpired, announcement.StatusArchived},
	})
}

// readAnnouncementSet returns the ids this account has read.
func (s *Server) readAnnouncementSet(c *gin.Context) map[string]bool {
	set := map[string]bool{}

	account := s.ownAccountQuiet(c)
	if account == nil {
		return set
	}

	reads, err := s.app.FindRecordsByFilter(announcement.CollectionReads,
		"user = {:u}", "", 0, 0, map[string]any{"u": account.Id})
	if err != nil {
		return set
	}
	for _, rec := range reads {
		set[rec.GetString("announcement")] = true
	}
	return set
}

// handleAnnouncementDetail renders one announcement and its read state.
func (s *Server) handleAnnouncementDetail(c *gin.Context) {
	rec, ok := s.findRecord(c, mustCollection(s.app, announcement.CollectionAnnouncements))
	if !ok {
		return
	}

	// Reading the page is what marks it read. A separate "mark read" click is a
	// step nobody takes, and the count would report far fewer readers than there
	// were.
	if account := s.ownAccountQuiet(c); account != nil {
		if err := announcement.MarkRead(s.app, rec.Id, account.Id); err != nil {
			slog.Warn("could not record a read", "announcement", rec.Id, "error", err)
		}
	}

	view := announcementDetail{
		base:    s.newBase(c, rec.GetString("title"), "announcement"),
		Record:  rec,
		Content: rec.GetString("content"),
		Reads:   rec.GetInt("readCount"),
	}

	// Who has not read it, which is the question the read count cannot answer.
	actor := s.actor(c)
	if actor.Can(permission.AnnouncementReads) {
		view.CanSeeReaders = true

		targets, err := announcement.Targets(s.app, rec)
		if err == nil && len(targets) > 0 {
			view.TargetCount = len(targets)

			reads, _ := s.app.FindRecordsByFilter(announcement.CollectionReads,
				"announcement = {:a}", "", 0, 0, map[string]any{"a": rec.Id})

			readBy := map[string]bool{}
			for _, r := range reads {
				readBy[r.GetString("user")] = true
			}

			for _, id := range targets {
				if readBy[id] {
					// Counted here rather than taking the announcement's own read
					// count, which includes readers outside the audience -- an
					// administrator checking their own notice would otherwise push
					// the numerator above the denominator and the fraction would
					// read as nonsense.
					view.ReadByTargets++
					continue
				}
				if user, err := s.app.FindRecordById("users", id); err == nil {
					view.Unread = append(view.Unread, user.Email())
				}
			}
			view.UnreadCount = len(view.Unread)
		}
	}

	s.render(c, http.StatusOK, "announcement_detail.html", view)
}

// handleAnnouncementForm renders the editor for a new or existing announcement.
func (s *Server) handleAnnouncementForm(c *gin.Context) {
	view := announcementForm{
		base:   s.newBase(c, "New announcement", "announcement"),
		IsNew:  true,
		Types:  []string{"notice", "announcement", "maintenance", "update"},
		Levels: []string{"normal", "important", "urgent"},
		Audiences: []string{announcement.AudienceAll, announcement.AudienceAdmins,
			announcement.AudienceUsers, announcement.AudienceDepartments},
		Departments:   s.departmentOptions(),
		SelectedDepts: map[string]bool{},
	}

	if id := c.Param("id"); id != "" {
		rec, err := s.app.FindRecordById(announcement.CollectionAnnouncements, id)
		if err != nil || rec == nil {
			s.renderErrorPage(c, http.StatusNotFound, "Not found", "That announcement does not exist.")
			return
		}
		view.IsNew = false
		view.Record = rec
		view.Title = rec.GetString("title")
		view.Content = rec.GetString("content")
		view.Type = rec.GetString("type")
		view.Level = rec.GetString("level")
		view.Audience = rec.GetString("audience")
		view.Pinned = rec.GetBool("pinned")
		view.base = s.newBase(c, "Edit announcement", "announcement")

		for _, id := range rec.GetStringSlice("departments") {
			view.SelectedDepts[id] = true
		}
		if at := rec.GetDateTime("publishAt"); !at.IsZero() {
			view.PublishAt = at.Time().Local().Format("2006-01-02T15:04")
		}
		if at := rec.GetDateTime("expireAt"); !at.IsZero() {
			view.ExpireAt = at.Time().Local().Format("2006-01-02T15:04")
		}
	}

	s.render(c, http.StatusOK, "announcement_form.html", view)
}

// handleAnnouncementSave creates or updates an announcement.
func (s *Server) handleAnnouncementSave(c *gin.Context) {
	id := strings.TrimSpace(c.PostForm("id"))

	col, err := s.app.FindCollectionByNameOrId(announcement.CollectionAnnouncements)
	if err != nil || col == nil {
		s.renderErrorPage(c, http.StatusInternalServerError, "Schema missing", "The collection does not exist.")
		return
	}

	var rec *core.Record
	if id == "" {
		rec = core.NewRecord(col)
	} else {
		rec, err = s.app.FindRecordById(announcement.CollectionAnnouncements, id)
		if err != nil || rec == nil {
			s.renderErrorPage(c, http.StatusNotFound, "Not found", "That announcement does not exist.")
			return
		}
	}

	title := strings.TrimSpace(c.PostForm("title"))
	if title == "" {
		s.setFlash(c, "danger", "An announcement needs a title.")
		c.Redirect(http.StatusSeeOther, "/announcements")
		return
	}

	rec.Set("title", title)
	// The content is sanitised by the model hook, so a value set here goes
	// through the same door as one set by the API.
	rec.Set("content", c.PostForm("content"))

	for _, field := range []string{"type", "level", "audience"} {
		if value := strings.TrimSpace(c.PostForm(field)); value != "" {
			rec.Set(field, value)
		}
	}

	rec.Set("pinned", c.PostForm("pinned") != "")

	publishAt, err := parseLocalTime(c.PostForm("publishAt"))
	if err != nil {
		s.setFlash(c, "danger", "The publish time is not a valid date and time.")
		c.Redirect(http.StatusSeeOther, "/announcements")
		return
	}
	expireAt, err := parseLocalTime(c.PostForm("expireAt"))
	if err != nil {
		s.setFlash(c, "danger", "The expiry time is not a valid date and time.")
		c.Redirect(http.StatusSeeOther, "/announcements")
		return
	}

	if !publishAt.IsZero() && !expireAt.IsZero() && expireAt.Before(publishAt) {
		s.setFlash(c, "danger", "The expiry time is before the publish time, so it would never be visible.")
		c.Redirect(http.StatusSeeOther, "/announcements")
		return
	}

	setOptionalDate(rec, "publishAt", publishAt)
	setOptionalDate(rec, "expireAt", expireAt)

	// Choosing a future publish time schedules it; choosing a past or absent one
	// publishes it immediately. The status follows the times rather than being a
	// separate control an operator has to keep in step with them.
	switch {
	case rec.GetString("status") == announcement.StatusArchived:
		// Leave an archived announcement archived; editing it does not republish.
	case !publishAt.IsZero() && publishAt.After(time.Now()):
		rec.Set("status", announcement.StatusScheduled)
	default:
		rec.Set("status", announcement.StatusPublished)
	}

	if rec.IsNew() {
		if account := s.ownAccountQuiet(c); account != nil {
			rec.Set("author", account.Id)
		}
	}

	var departments []string
	for _, id := range c.PostFormArray("departments") {
		if id = strings.TrimSpace(id); id != "" {
			departments = append(departments, id)
		}
	}
	rec.Set("departments", departments)

	if err := s.app.Save(rec); err != nil {
		s.setFlash(c, "danger", "Could not save: "+err.Error())
		c.Redirect(http.StatusSeeOther, "/announcements")
		return
	}

	event := audit.EventSettingsChanged
	s.audit(c, auditEntry{
		Event:      event,
		Module:     "announcement",
		TargetType: "announcement",
		TargetID:   rec.Id,
		Severity:   severityInfo,
		Summary:    "Announcement \"" + title + "\" saved as " + rec.GetString("status"),
	})

	s.setFlash(c, "success", "Saved.")
	c.Redirect(http.StatusSeeOther, "/announcements/"+rec.Id)
}

// handleAnnouncementPublish publishes immediately.
func (s *Server) handleAnnouncementPublish(c *gin.Context) {
	rec, ok := s.findRecord(c, mustCollection(s.app, announcement.CollectionAnnouncements))
	if !ok {
		return
	}

	rec.Set("status", announcement.StatusPublished)
	// Publishing now means the publish time is now; leaving a future one behind
	// would make the row disagree with itself.
	rec.Set("publishAt", time.Now().UTC())

	if err := s.app.Save(rec); err != nil {
		s.setFlash(c, "danger", "Could not publish: "+err.Error())
		c.Redirect(http.StatusSeeOther, "/announcements")
		return
	}

	s.audit(c, auditEntry{
		Event:      audit.EventSettingsChanged,
		Module:     "announcement",
		TargetType: "announcement",
		TargetID:   rec.Id,
		Severity:   severityWarning,
		Summary:    "Announcement \"" + rec.GetString("title") + "\" published",
	})
	s.setFlash(c, "success", "Published.")
	c.Redirect(http.StatusSeeOther, "/announcements/"+rec.Id)
}

// handleAnnouncementArchive withdraws an announcement.
func (s *Server) handleAnnouncementArchive(c *gin.Context) {
	rec, ok := s.findRecord(c, mustCollection(s.app, announcement.CollectionAnnouncements))
	if !ok {
		return
	}

	rec.Set("status", announcement.StatusArchived)
	if err := s.app.Save(rec); err != nil {
		s.setFlash(c, "danger", "Could not archive: "+err.Error())
		c.Redirect(http.StatusSeeOther, "/announcements")
		return
	}

	s.audit(c, auditEntry{
		Event:      audit.EventSettingsChanged,
		Module:     "announcement",
		TargetType: "announcement",
		TargetID:   rec.Id,
		Severity:   severityWarning,
		Summary:    "Announcement \"" + rec.GetString("title") + "\" archived",
	})
	s.setFlash(c, "success", "Archived. It is no longer shown to readers.")
	c.Redirect(http.StatusSeeOther, "/announcements/"+rec.Id)
}

// handleAnnouncementDelete soft-deletes an announcement.
//
// The row and its read records stay. The read state is evidence that people were
// told something, and removing the announcement would take that with it -- which
// is exactly the record an incident review wants.
func (s *Server) handleAnnouncementDelete(c *gin.Context) {
	rec, ok := s.findRecord(c, mustCollection(s.app, announcement.CollectionAnnouncements))
	if !ok {
		return
	}

	rec.Set("deletedAt", time.Now().UTC())
	if account := s.ownAccountQuiet(c); account != nil {
		rec.Set("deletedBy", account.Id)
	}

	if err := s.app.Save(rec); err != nil {
		s.setFlash(c, "danger", "Could not delete: "+err.Error())
		c.Redirect(http.StatusSeeOther, "/announcements")
		return
	}

	s.audit(c, auditEntry{
		Event:      audit.EventAccountDeleted,
		Module:     "announcement",
		TargetType: "announcement",
		TargetID:   rec.Id,
		Severity:   severityWarning,
		Summary:    "Announcement \"" + rec.GetString("title") + "\" deleted",
	})
	s.setFlash(c, "success", "Deleted.")
	c.Redirect(http.StatusSeeOther, "/announcements")
}

// handleAnnouncementRead marks an announcement read for the current account.
func (s *Server) handleAnnouncementRead(c *gin.Context) {
	rec, ok := s.findRecord(c, mustCollection(s.app, announcement.CollectionAnnouncements))
	if !ok {
		return
	}

	account := s.ownAccountQuiet(c)
	if account == nil {
		s.setFlash(c, "danger", "A superuser account has no reader identity.")
		c.Redirect(http.StatusSeeOther, "/announcements/"+rec.Id)
		return
	}

	if err := announcement.MarkRead(s.app, rec.Id, account.Id); err != nil {
		s.setFlash(c, "danger", "Could not record the read: "+err.Error())
	}
	c.Redirect(http.StatusSeeOther, "/announcements/"+rec.Id)
}

// parseLocalTime reads a datetime-local field.
func parseLocalTime(raw string) (time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, nil
	}
	return time.ParseInLocation("2006-01-02T15:04", raw, time.Local)
}

// setOptionalDate stores a time, or clears the field when it is zero.
func setOptionalDate(rec *core.Record, field string, at time.Time) {
	if at.IsZero() {
		rec.Set(field, nil)
		return
	}
	rec.Set(field, at.UTC())
}

// departmentOptions lists the departments for the audience picker.
func (s *Server) departmentOptions() []departmentOption {
	records, err := s.app.FindRecordsByFilter("departments", "status != 'archived'", "path", 0, 0)
	if err != nil {
		return nil
	}

	out := make([]departmentOption, 0, len(records))
	for _, rec := range records {
		out = append(out, departmentOption{
			ID:    rec.Id,
			Label: strings.Repeat("&nbsp;&nbsp;", rec.GetInt("level")) + rec.GetString("name"),
		})
	}
	return out
}

// announcementRow is one announcement on the list.
type announcementRow struct {
	Record  *core.Record
	Title   string
	Type    string
	Level   string
	Status  string
	Pinned  bool
	Preview string
	Reads   int
	IsRead  bool
}

// announcementsPage is the list.
type announcementsPage struct {
	base
	Rows       []announcementRow
	Query      string
	Status     string
	Statuses   []string
	CanCreate  bool
	CanPublish bool
	CanDelete  bool
}

// announcementDetail is one announcement.
type announcementDetail struct {
	base
	Record        *core.Record
	Content       string
	Reads         int
	CanSeeReaders bool
	ReadByTargets int
	TargetCount   int
	UnreadCount   int
	Unread        []string
}

// announcementForm is the editor.
type announcementForm struct {
	base
	Record        *core.Record
	IsNew         bool
	Title         string
	Content       string
	Type          string
	Level         string
	Audience      string
	Pinned        bool
	PublishAt     string
	ExpireAt      string
	Types         []string
	Levels        []string
	Audiences     []string
	Departments   []departmentOption
	SelectedDepts map[string]bool
}

// departmentOption is one row of the audience picker.
type departmentOption struct {
	ID    string
	Label string
}
