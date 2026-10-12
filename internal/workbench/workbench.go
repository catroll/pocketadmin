// Package workbench holds the personal side of the panel: a todo list, an inbox,
// recently visited pages and quick links.
//
// A todo is personal and so is an inbox. Ownership is therefore not a permission:
// there is no grant that lets one administrator read another's todos, and the
// enforcement is a filter on the owner rather than a check that can be handed out.
// The permissions this module declares govern whether the screens are reachable at
// all.
package workbench

import (
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/pocketbase/pocketbase/core"
)

// Collection names.
const (
	CollectionTodos      = "todos"
	CollectionMessages   = "messages"
	CollectionRecipients = "message_recipients"
	CollectionVisits     = "recent_visits"
	CollectionQuickLinks = "quick_links"
)

// TodoTags returns the tag vocabulary.
//
// It is a compile-time list so the collection's stored enum and the tags the
// screens offer cannot drift: both are built from this.
func TodoTags() []string {
	return []string{"follow-up", "finance", "operations", "support", "review", "blocked"}
}

// Todo statuses.
const (
	TodoPending    = "pending"
	TodoInProgress = "in_progress"
	TodoDone       = "done"
	TodoCancelled  = "cancelled"
)

// Todo statuses that are finished rather than outstanding.
func isClosed(status string) bool {
	return status == TodoDone || status == TodoCancelled
}

// ---------------------------------------------------------------------------
// Todos
// ---------------------------------------------------------------------------

// TodoFilter limits a todo query to one owner.
//
// Every read and every write goes through this. A todo screen that forgot it
// would show one administrator another's list, and the mistake would be invisible
// because the page would render perfectly.
func TodoFilter(ownerID string) (string, map[string]any) {
	return "owner = {:owner}", map[string]any{"owner": ownerID}
}

// OwnedBy reports whether a todo belongs to an account.
func OwnedBy(rec *core.Record, ownerID string) bool {
	return rec != nil && ownerID != "" && rec.GetString("owner") == ownerID
}

// CompleteTodo marks a todo done and stamps the completion time.
func CompleteTodo(app core.App, rec *core.Record) error {
	rec.Set("status", TodoDone)
	rec.Set("completedAt", time.Now().UTC())
	return app.Save(rec)
}

// ReopenTodo returns a finished todo to the pending state.
//
// The completion time is cleared rather than left behind, because a todo that
// says it was completed and is also outstanding is a row that contradicts itself.
func ReopenTodo(app core.App, rec *core.Record) error {
	rec.Set("status", TodoPending)
	rec.Set("completedAt", nil)
	return app.Save(rec)
}

// DueReminders returns the outstanding todos whose reminder has arrived.
//
// The reminder is not cleared: a reminder that has fired stays on the row so the
// screen can show that it was due, and clearing it would make a repeated sweep
// re-notify from scratch every time. The notifications themselves are deduplicated
// by the message delivery record.
func DueReminders(app core.App, now time.Time) ([]*core.Record, error) {
	records, err := app.FindRecordsByFilter(CollectionTodos,
		"remindAt != '' && remindAt <= {:now} && status != 'done' && status != 'cancelled'",
		"remindAt", 0, 0, map[string]any{"now": now.UTC()})
	if err != nil {
		return nil, fmt.Errorf("workbench: find due reminders: %w", err)
	}
	return records, nil
}

// ---------------------------------------------------------------------------
// Inbox
// ---------------------------------------------------------------------------

// InboxFilter limits a message query to one account's un-deleted deliveries.
func InboxFilter(userID string) (string, map[string]any) {
	return "user = {:user} && deletedAt = ''", map[string]any{"user": userID}
}

// UnreadCount returns how many messages an account has not read.
func UnreadCount(app core.App, userID string) int {
	if userID == "" {
		return 0
	}
	records, err := app.FindRecordsByFilter(CollectionRecipients,
		"user = {:user} && readAt = '' && deletedAt = ''", "", 0, 0,
		map[string]any{"user": userID})
	if err != nil {
		return 0
	}
	return len(records)
}

// MarkMessageRead records that an account has read one message.
func MarkMessageRead(app core.App, recipientID, userID string) error {
	rec, err := app.FindRecordById(CollectionRecipients, recipientID)
	if err != nil || rec == nil {
		return fmt.Errorf("workbench: no such delivery")
	}
	// A delivery belongs to one account. Without this check, knowing an id would
	// be enough to mark somebody else's message read.
	if rec.GetString("user") != userID {
		return fmt.Errorf("workbench: that delivery belongs to another account")
	}
	if !rec.GetDateTime("readAt").IsZero() {
		return nil
	}

	rec.Set("readAt", time.Now().UTC())
	return app.Save(rec)
}

// MarkAllRead marks every unread delivery for an account.
func MarkAllRead(app core.App, userID string) (int, error) {
	records, err := app.FindRecordsByFilter(CollectionRecipients,
		"user = {:user} && readAt = '' && deletedAt = ''", "", 0, 0,
		map[string]any{"user": userID})
	if err != nil {
		return 0, err
	}

	now := time.Now().UTC()
	marked := 0
	for _, rec := range records {
		rec.Set("readAt", now)
		if err := app.Save(rec); err != nil {
			return marked, err
		}
		marked++
	}
	return marked, nil
}

// DeleteMessage hides one delivery from an account's inbox.
//
// It is a per-recipient soft delete. The message itself and every other
// recipient's copy are untouched: one person clearing their inbox says nothing
// about anybody else's.
func DeleteMessage(app core.App, recipientID, userID string) error {
	rec, err := app.FindRecordById(CollectionRecipients, recipientID)
	if err != nil || rec == nil {
		return fmt.Errorf("workbench: no such delivery")
	}
	if rec.GetString("user") != userID {
		return fmt.Errorf("workbench: that delivery belongs to another account")
	}

	rec.Set("deletedAt", time.Now().UTC())
	return app.Save(rec)
}

// ---------------------------------------------------------------------------
// Sending and broadcasting
// ---------------------------------------------------------------------------

// Message is a message to deliver.
type Message struct {
	Title   string
	Content string
	Type    string
	Level   string
	Link    string

	// Sender is the account sending it, or "" for the platform.
	Sender string

	// Audience decides how the targets fields are read.
	Audience string

	// Recipients are the accounts to deliver to. When empty and the audience is
	// direct, the message is delivered to nobody rather than to everybody --
	// falling back to a broadcast would turn an empty selection into the widest
	// possible send.
	Recipients []string

	// RoleIDs and DepartmentIDs are used when the audience is role or department.
	RoleIDs       []string
	DepartmentIDs []string
}

// Send creates a message and a delivery record for every recipient.
//
// Broadcasts are expanded here, at send time, rather than left as a query. A
// delivery that is computed at read time cannot record that somebody read it,
// cannot be deleted by one recipient without affecting the others, and changes
// meaning when the audience changes -- a new member of a department would
// retroactively receive last year's notices.
func Send(app core.App, msg Message) (created int, err error) {
	targets, err := resolveTargets(app, msg)
	if err != nil {
		return 0, err
	}
	if len(targets) == 0 {
		return 0, nil
	}

	msgCol, err := app.FindCollectionByNameOrId(CollectionMessages)
	if err != nil || msgCol == nil {
		return 0, fmt.Errorf("workbench: the messages collection is missing")
	}
	recCol, err := app.FindCollectionByNameOrId(CollectionRecipients)
	if err != nil || recCol == nil {
		return 0, fmt.Errorf("workbench: the deliveries collection is missing")
	}

	message := core.NewRecord(msgCol)
	message.Set("title", msg.Title)
	message.Set("content", msg.Content)
	message.Set("type", nonEmpty(msg.Type, "notice"))
	message.Set("level", nonEmpty(msg.Level, "info"))
	message.Set("link", msg.Link)
	message.Set("audience", nonEmpty(msg.Audience, "direct"))
	if msg.Sender != "" {
		message.Set("sender", msg.Sender)
	}
	message.Set("roles", msg.RoleIDs)
	message.Set("departments", msg.DepartmentIDs)

	if err := app.Save(message); err != nil {
		return 0, fmt.Errorf("workbench: create the message: %w", err)
	}

	for _, userID := range targets {
		delivery := core.NewRecord(recCol)
		delivery.Set("message", message.Id)
		delivery.Set("user", userID)

		if err := app.Save(delivery); err != nil {
			// A duplicate is the unique index refusing a second delivery to the
			// same person, which is the outcome that was wanted rather than a
			// failure: a targeted list can legitimately name somebody twice.
			if strings.Contains(strings.ToLower(err.Error()), "unique") {
				continue
			}
			return created, fmt.Errorf("workbench: deliver the message: %w", err)
		}
		created++
	}

	return created, nil
}

// resolveTargets turns an audience into the accounts to deliver to.
func resolveTargets(app core.App, msg Message) ([]string, error) {
	seen := map[string]bool{}
	out := []string{}

	add := func(ids ...string) {
		for _, id := range ids {
			if id == "" || seen[id] {
				continue
			}
			seen[id] = true
			out = append(out, id)
		}
	}

	switch msg.Audience {
	case "direct":
		add(msg.Recipients...)

	case "all":
		users, err := app.FindAllRecords("users")
		if err != nil {
			return nil, err
		}
		for _, u := range users {
			add(u.Id)
		}

	case "role":
		// A role reaches an account through its admin record.
		admins, err := app.FindAllRecords("admins")
		if err != nil {
			return nil, err
		}
		for _, admin := range admins {
			if admin.GetString("status") != "active" {
				continue
			}
			for _, roleID := range admin.GetStringSlice("roles") {
				if contains(msg.RoleIDs, roleID) {
					add(admin.GetString("user"))
					break
				}
			}
		}

	case "department":
		employees, err := app.FindAllRecords("employees")
		if err != nil {
			return nil, err
		}
		for _, employee := range employees {
			// The employee record names one department, not a set.
			if !contains(msg.DepartmentIDs, employee.GetString("department")) {
				continue
			}
			add(employee.GetString("user"))
		}

	default:
		return nil, fmt.Errorf("workbench: unknown audience %q", msg.Audience)
	}

	return out, nil
}

// NotifySystem sends a message from the platform to one account.
//
// It is what a scheduled task uses to tell somebody something. A failure is logged
// rather than returned to the caller in the places it is used, because a task that
// failed only because it could not send a notification has still done its work.
func NotifySystem(app core.App, userID string, msg Message) error {
	if userID == "" {
		return nil
	}
	msg.Audience = "direct"
	msg.Recipients = []string{userID}
	msg.Sender = ""

	created, err := Send(app, msg)
	if err != nil {
		slog.Warn("could not send a system message", "user", userID, "error", err)
		return err
	}
	if created == 1 {
		return nil
	}
	// The delivery already existed, which is the case a repeated sweep produces.
	return nil
}

// ---------------------------------------------------------------------------
// Recent visits
// ---------------------------------------------------------------------------

// maxRecentVisits is how many pages are remembered per account.
const maxRecentVisits = 30

// RecordVisit notes that an account opened a page.
//
// The row is keyed on the account and the path, so a page visited twenty times is
// one entry with a count. Without that, the list would fill with the page the
// operator is currently working in and say nothing useful.
func RecordVisit(app core.App, userID, title, path, icon string) error {
	if userID == "" || path == "" {
		return nil
	}
	// Static assets and the API are not pages a person navigated to.
	if strings.HasPrefix(path, "/api/") || strings.HasPrefix(path, "/static/") {
		return nil
	}

	col, err := app.FindCollectionByNameOrId(CollectionVisits)
	if err != nil || col == nil {
		return nil
	}

	rec, err := app.FindFirstRecordByFilter(CollectionVisits,
		"user = {:user} && path = {:path}", map[string]any{"user": userID, "path": path})
	if err != nil && rec == nil {
		rec = nil
	}

	if rec == nil {
		rec = core.NewRecord(col)
		rec.Set("user", userID)
		rec.Set("path", path)
		rec.Set("visitCount", 0)
	}

	rec.Set("title", title)
	rec.Set("icon", icon)
	rec.Set("visitCount", rec.GetInt("visitCount")+1)
	rec.Set("visitedAt", time.Now().UTC())

	if err := app.Save(rec); err != nil {
		return err
	}

	return pruneVisits(app, userID)
}

// pruneVisits keeps the most recent visits within the bound.
func pruneVisits(app core.App, userID string) error {
	records, err := app.FindRecordsByFilter(CollectionVisits,
		"user = {:user}", "-visitedAt", 0, 0, map[string]any{"user": userID})
	if err != nil || len(records) <= maxRecentVisits {
		return nil
	}

	for _, rec := range records[maxRecentVisits:] {
		if err := app.Delete(rec); err != nil {
			return err
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// Quick links
// ---------------------------------------------------------------------------

// QuickLinks returns the tiles an account should see.
//
// An account with its own links sees those; one without sees the defaults. The
// fallback is all-or-nothing rather than a merge, because a merged list would
// resurrect a default the account deliberately removed.
func QuickLinks(app core.App, userID string) ([]*core.Record, error) {
	records, err := app.FindRecordsByFilter(CollectionQuickLinks,
		"user = {:user}", "sortOrder", 0, 0, map[string]any{"user": userID})
	if err != nil {
		return nil, err
	}
	if len(records) > 0 {
		return records, nil
	}

	// The defaults are the rows with no owner.
	return app.FindRecordsByFilter(CollectionQuickLinks,
		"user = ''", "sortOrder", 0, 0)
}

// FilterByPermission removes links the account may not follow.
//
// A shortcut whose target the reader may not open is not a shortcut. Hiding it is
// the difference between a helpful tile and a trap that produces a 403.
func FilterByPermission(records []*core.Record, can func(code string) bool) []*core.Record {
	if can == nil {
		return records
	}

	out := make([]*core.Record, 0, len(records))
	for _, rec := range records {
		if code := rec.GetString("permissionCode"); code != "" && !can(code) {
			continue
		}
		out = append(out, rec)
	}
	return out
}

// nonEmpty returns a value or a fallback.
func nonEmpty(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

// contains reports whether a slice holds a value.
func contains(have []string, want string) bool {
	if want == "" {
		return false
	}
	for _, h := range have {
		if h == want {
			return true
		}
	}
	return false
}
