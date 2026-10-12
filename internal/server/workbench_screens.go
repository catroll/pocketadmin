package server

import (
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/pocketbase/pocketbase/core"

	"pocketadmin/internal/audit"
	"pocketadmin/internal/permission"
	"pocketadmin/internal/richtext"
	"pocketadmin/internal/workbench"
)

// registerWorkbenchRoutes mounts the personal workbench.
//
// There are two separate questions on every route here and they need two
// separate answers.
//
// The first is whether the screen is reachable, which is a permission like any
// other. The second is whose rows the request may touch: a todo and a message
// delivery belong to exactly one account, and there is no grant that makes one
// administrator's list readable by another. That second answer is not delegable
// and is therefore enforced in the handlers -- every one of them resolves the todo
// or the delivery through ownTodo or the signed-in account's own id -- rather than
// being expressed as a permission somebody could be given.
//
// A guard that only answered the first question would leave the route looking
// protected while a fabricated id reached another account's row.
func (s *Server) registerWorkbenchRoutes(panel *gin.RouterGroup) {
	s.readGuarded(panel, http.MethodGet, "/workbench", permission.WorkbenchTodoList, s.handleWorkbench)
	s.readGuarded(panel, http.MethodGet, "/workbench/inbox", permission.WorkbenchMessageList, s.handleInbox)

	s.guarded(panel, http.MethodPost, "/workbench/todos", permission.WorkbenchTodoList, s.handleTodoSave)
	s.guarded(panel, http.MethodPost, "/workbench/todos/:id/state", permission.WorkbenchTodoList, s.handleTodoState)
	s.guarded(panel, http.MethodPost, "/workbench/todos/:id/delete", permission.WorkbenchTodoList, s.handleTodoDelete)

	s.guarded(panel, http.MethodPost, "/workbench/inbox/:id/read", permission.WorkbenchMessageList, s.handleMessageRead)
	s.guarded(panel, http.MethodPost, "/workbench/inbox/read-all", permission.WorkbenchMessageList, s.handleMessageReadAll)
	s.guarded(panel, http.MethodPost, "/workbench/inbox/:id/delete", permission.WorkbenchMessageList, s.handleMessageDelete)

	s.guarded(panel, http.MethodPost, "/workbench/send", permission.WorkbenchMessageSend, s.handleMessageSend)
}

// handleWorkbench renders the dashboard.
func (s *Server) handleWorkbench(c *gin.Context) {
	account := s.ownAccountQuiet(c)

	todos, err := s.app.FindRecordsByFilter(workbench.CollectionTodos,
		"owner = {:owner} && status != 'done' && status != 'cancelled'",
		"sortOrder,-priority,dueAt", 200, 0, map[string]any{"owner": recordID(account)})
	if err != nil {
		todos = nil
	}

	done, _ := s.app.FindRecordsByFilter(workbench.CollectionTodos,
		"owner = {:owner} && (status = 'done' || status = 'cancelled')",
		"-completedAt", 20, 0, map[string]any{"owner": recordID(account)})

	visits, _ := s.app.FindRecordsByFilter(workbench.CollectionVisits,
		"user = {:user}", "-visitedAt", 10, 0, map[string]any{"user": recordID(account)})

	links, _ := workbench.QuickLinks(s.app, recordID(account))
	// A tile the reader may not follow is hidden rather than shown and refused.
	links = workbench.FilterByPermission(links, s.actor(c).Can)

	inbox, _ := s.app.FindRecordsByFilter(workbench.CollectionRecipients,
		"user = {:user} && deletedAt = '' && readAt = ''", "-created", 10, 0,
		map[string]any{"user": recordID(account)})

	s.render(c, http.StatusOK, "workbench.html", workbenchPage{
		base:        s.newBase(c, "Workbench", "workbench"),
		Todos:       todos,
		Done:        done,
		Visits:      visits,
		Links:       links,
		Unread:      s.inboxRows(inbox),
		UnreadAll:   workbench.UnreadCount(s.app, recordID(account)),
		Tags:        workbench.TodoTags(),
		CanSend:     s.actor(c).Can(permission.WorkbenchMessageSend),
		Departments: s.departmentOptions(),
	})
}

// handleInbox renders the message inbox.
func (s *Server) handleInbox(c *gin.Context) {
	account := s.ownAccountQuiet(c)
	userID := recordID(account)

	filter := "user = {:user} && deletedAt = ''"
	params := map[string]any{"user": userID}

	if c.Query("show") != "all" {
		filter += " && readAt = ''"
	}
	if search := strings.TrimSpace(c.Query("q")); search != "" {
		// The message title lives on the parent, so the search is a back-relation
		// filter rather than a column on the delivery.
		filter += " && message.title ~ {:q}"
		params["q"] = search
	}

	records, err := s.app.FindRecordsByFilter(workbench.CollectionRecipients,
		filter, "-created", 100, 0, params)
	if err != nil {
		s.renderErrorPage(c, http.StatusInternalServerError, "Could not load the inbox", err.Error())
		return
	}

	s.render(c, http.StatusOK, "workbench_inbox.html", inboxPage{
		base:    s.newBase(c, "Inbox", "workbench"),
		Rows:    s.inboxRows(records),
		ShowAll: c.Query("show") == "all",
		Query:   c.Query("q"),
		Unread:  workbench.UnreadCount(s.app, userID),
	})
}

// inboxRows expands each delivery into its message.
func (s *Server) inboxRows(records []*core.Record) []inboxRow {
	rows := make([]inboxRow, 0, len(records))

	for _, rec := range records {
		message, err := s.app.FindRecordById(workbench.CollectionMessages, rec.GetString("message"))
		if err != nil || message == nil {
			continue
		}

		row := inboxRow{
			Delivery: rec,
			Message:  message,
			Title:    message.GetString("title"),
			// The body is rendered as text in the list: a preview is not a place
			// for markup, and the sanitised body is shown in full on the message.
			Preview: messagePlain(message.GetString("content")),
			Level:   message.GetString("level"),
			Type:    message.GetString("type"),
			Link:    message.GetString("link"),
			Unread:  rec.GetDateTime("readAt").IsZero(),
		}
		if sender, err := s.app.FindRecordById("users", message.GetString("sender")); err == nil && sender != nil {
			row.Sender = sender.Email()
		} else {
			row.Sender = "the platform"
		}
		if at := rec.GetDateTime("createdAt"); !at.IsZero() {
			row.When = relativeTime(at)
		}
		rows = append(rows, row)
	}
	return rows
}

// handleTodoSave creates or updates a todo.
func (s *Server) handleTodoSave(c *gin.Context) {
	account := s.ownAccountQuiet(c)
	if account == nil {
		s.setFlash(c, "danger", "A todo belongs to an account, and a superuser has none.")
		c.Redirect(http.StatusSeeOther, "/workbench")
		return
	}

	col, err := s.app.FindCollectionByNameOrId(workbench.CollectionTodos)
	if err != nil || col == nil {
		s.renderErrorPage(c, http.StatusInternalServerError, "Schema missing", "The collection does not exist.")
		return
	}

	id := strings.TrimSpace(c.PostForm("id"))
	var rec *core.Record

	if id == "" {
		rec = core.NewRecord(col)
		// The owner is the signed-in account and is not taken from the form.
		// Reading it from the submission would let a request file a todo into
		// somebody else's list.
		rec.Set("owner", account.Id)
	} else {
		existing, err := s.app.FindRecordById(workbench.CollectionTodos, id)
		if err != nil || existing == nil {
			s.renderErrorPage(c, http.StatusNotFound, "Not found", "That todo does not exist.")
			return
		}
		// Ownership, not permission: there is no grant that makes this readable.
		if !workbench.OwnedBy(existing, account.Id) {
			s.renderErrorPage(c, http.StatusForbidden, "Forbidden", "That todo belongs to another account.")
			return
		}
		rec = existing
	}

	title := strings.TrimSpace(c.PostForm("title"))
	if title == "" {
		s.setFlash(c, "danger", "A todo needs a title.")
		c.Redirect(http.StatusSeeOther, "/workbench")
		return
	}
	rec.Set("title", title)
	rec.Set("description", c.PostForm("description"))

	if priority := c.PostForm("priority"); priority != "" {
		rec.Set("priority", priority)
	}
	if relatedType := strings.TrimSpace(c.PostForm("relatedType")); relatedType != "" {
		rec.Set("relatedType", relatedType)
		rec.Set("relatedId", strings.TrimSpace(c.PostForm("relatedId")))
	} else {
		rec.Set("relatedType", nil)
		rec.Set("relatedId", nil)
	}

	for _, field := range []string{"dueAt", "remindAt"} {
		at, err := parseLocalTime(c.PostForm(field))
		if err != nil {
			s.setFlash(c, "danger", "That date and time could not be read.")
			c.Redirect(http.StatusSeeOther, "/workbench")
			return
		}
		setOptionalDate(rec, field, at)
	}

	var tags []string
	for _, tag := range c.PostFormArray("tags") {
		if tag = strings.TrimSpace(tag); tag != "" {
			tags = append(tags, tag)
		}
	}
	rec.Set("tags", tags)

	if rec.IsNew() {
		rec.Set("status", workbench.TodoPending)
	}

	if err := s.app.Save(rec); err != nil {
		s.setFlash(c, "danger", "Could not save: "+err.Error())
		c.Redirect(http.StatusSeeOther, "/workbench")
		return
	}

	s.setFlash(c, "success", "Saved.")
	c.Redirect(http.StatusSeeOther, "/workbench")
}

// handleTodoState completes, reopens, starts or cancels a todo.
func (s *Server) handleTodoState(c *gin.Context) {
	rec, account, ok := s.ownTodo(c)
	if !ok {
		return
	}

	switch c.PostForm("status") {
	case workbench.TodoDone:
		if err := workbench.CompleteTodo(s.app, rec); err != nil {
			s.setFlash(c, "danger", "Could not complete: "+err.Error())
		}
	case workbench.TodoPending:
		if err := workbench.ReopenTodo(s.app, rec); err != nil {
			s.setFlash(c, "danger", "Could not reopen: "+err.Error())
		}
	case workbench.TodoInProgress, workbench.TodoCancelled:
		rec.Set("status", c.PostForm("status"))
		if err := s.app.Save(rec); err != nil {
			s.setFlash(c, "danger", "Could not change the state: "+err.Error())
		}
	default:
		s.setFlash(c, "danger", "Unrecognised state.")
	}

	_ = account
	c.Redirect(http.StatusSeeOther, "/workbench")
}

// handleTodoDelete removes a todo.
func (s *Server) handleTodoDelete(c *gin.Context) {
	rec, _, ok := s.ownTodo(c)
	if !ok {
		return
	}

	if err := s.app.Delete(rec); err != nil {
		s.setFlash(c, "danger", "Could not delete: "+err.Error())
	}
	c.Redirect(http.StatusSeeOther, "/workbench")
}

// ownTodo loads a todo and refuses unless the signed-in account owns it.
func (s *Server) ownTodo(c *gin.Context) (*core.Record, *core.Record, bool) {
	account := s.ownAccountQuiet(c)
	if account == nil {
		s.renderErrorPage(c, http.StatusForbidden, "Forbidden",
			"A superuser account has no personal workbench.")
		return nil, nil, false
	}

	rec, err := s.app.FindRecordById(workbench.CollectionTodos, c.Param("id"))
	if err != nil || rec == nil {
		s.renderErrorPage(c, http.StatusNotFound, "Not found", "That todo does not exist.")
		return nil, nil, false
	}

	if !workbench.OwnedBy(rec, account.Id) {
		s.renderErrorPage(c, http.StatusForbidden, "Forbidden", "That todo belongs to another account.")
		return nil, nil, false
	}
	return rec, account, true
}

// handleMessageRead marks one delivery read.
func (s *Server) handleMessageRead(c *gin.Context) {
	account := s.ownAccountQuiet(c)
	if account == nil {
		c.Redirect(http.StatusSeeOther, "/workbench/inbox")
		return
	}

	if err := workbench.MarkMessageRead(s.app, c.Param("id"), account.Id); err != nil {
		s.setFlash(c, "danger", err.Error())
	}
	c.Redirect(http.StatusSeeOther, "/workbench/inbox")
}

// handleMessageReadAll marks every unread delivery read.
func (s *Server) handleMessageReadAll(c *gin.Context) {
	account := s.ownAccountQuiet(c)
	if account == nil {
		c.Redirect(http.StatusSeeOther, "/workbench/inbox")
		return
	}

	marked, err := workbench.MarkAllRead(s.app, account.Id)
	if err != nil {
		s.setFlash(c, "danger", "Could not mark them read: "+err.Error())
	} else {
		s.setFlash(c, "success", "Marked "+itoa(marked)+" message(s) read.")
	}
	c.Redirect(http.StatusSeeOther, "/workbench/inbox")
}

// handleMessageDelete hides one delivery.
func (s *Server) handleMessageDelete(c *gin.Context) {
	account := s.ownAccountQuiet(c)
	if account == nil {
		c.Redirect(http.StatusSeeOther, "/workbench/inbox")
		return
	}

	if err := workbench.DeleteMessage(s.app, c.Param("id"), account.Id); err != nil {
		s.setFlash(c, "danger", err.Error())
	}
	c.Redirect(http.StatusSeeOther, "/workbench/inbox")
}

// handleMessageSend broadcasts a message.
func (s *Server) handleMessageSend(c *gin.Context) {
	title := strings.TrimSpace(c.PostForm("title"))
	if title == "" {
		s.setFlash(c, "danger", "A message needs a title.")
		c.Redirect(http.StatusSeeOther, "/workbench")
		return
	}

	msg := workbench.Message{
		Title:    title,
		Content:  c.PostForm("content"),
		Type:     nonEmptyString(c.PostForm("type"), "notice"),
		Level:    nonEmptyString(c.PostForm("level"), "info"),
		Link:     strings.TrimSpace(c.PostForm("link")),
		Audience: nonEmptyString(c.PostForm("audience"), "direct"),
	}

	if account := s.ownAccountQuiet(c); account != nil {
		msg.Sender = account.Id
	}
	msg.Recipients = c.PostFormArray("recipients")
	msg.RoleIDs = c.PostFormArray("roles")
	msg.DepartmentIDs = c.PostFormArray("departments")

	created, err := workbench.Send(s.app, msg)
	if err != nil {
		s.setFlash(c, "danger", "Could not send: "+err.Error())
		c.Redirect(http.StatusSeeOther, "/workbench")
		return
	}

	// A broadcast reaches other people, so it is recorded: who sent what to whom
	// is exactly the question asked afterwards.
	s.audit(c, auditEntry{
		Event:      audit.EventSettingsChanged,
		Module:     "workbench",
		TargetType: "message",
		Severity:   severityInfo,
		Summary:    "Sent \"" + title + "\" to " + itoa(created) + " recipient(s) as " + msg.Audience,
		Detail:     map[string]any{"audience": msg.Audience, "recipients": created},
	})

	s.setFlash(c, "success", "Delivered to "+itoa(created)+" recipient(s).")
	c.Redirect(http.StatusSeeOther, "/workbench")
}

// recordVisitsMiddleware notes the page an account opened.
//
// It runs on the way out so the title the handler chose is available, and it
// ignores anything that is not a panel page.
func (s *Server) recordVisitsMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Next()

		if c.Request.Method != http.MethodGet || c.Writer.Status() >= 400 {
			return
		}

		account := s.ownAccountQuiet(c)
		if account == nil {
			return
		}

		title := c.GetString(ctxPageTitle)
		if title == "" {
			return
		}

		if err := workbench.RecordVisit(s.app, account.Id, title, c.Request.URL.Path, c.GetString(ctxPageIcon)); err != nil {
			slog.Debug("could not record a visit", "path", c.Request.URL.Path, "error", err)
		}
	}
}

// recordID returns a record's id, or "" for nil.
func recordID(rec *core.Record) string {
	if rec == nil {
		return ""
	}
	return rec.Id
}

// nonEmptyString returns a value or a fallback.
func nonEmptyString(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

// itoa renders an int without importing strconv at every call site.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	negative := n < 0
	if negative {
		n = -n
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	if negative {
		return "-" + string(digits)
	}
	return string(digits)
}

// workbenchPage is the dashboard.
type workbenchPage struct {
	base
	Todos       []*core.Record
	Done        []*core.Record
	Visits      []*core.Record
	Links       []*core.Record
	Unread      []inboxRow
	UnreadAll   int
	Tags        []string
	CanSend     bool
	Departments []departmentOption
}

// inboxPage is the message list.
type inboxPage struct {
	base
	Rows    []inboxRow
	ShowAll bool
	Query   string
	Unread  int
}

// inboxRow is one delivery with its message.
type inboxRow struct {
	Delivery *core.Record
	Message  *core.Record
	Title    string
	Preview  string
	Level    string
	Type     string
	Link     string
	Sender   string
	When     string
	Unread   bool
}

// messagePlain renders a message body as text for a preview.
func messagePlain(html string) string {
	return richtext.TextFromHTML(html, 160)
}

var _ = time.Now
