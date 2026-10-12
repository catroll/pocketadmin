package announcement

import (
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/pocketbase/pocketbase/core"

	"pocketadmin/internal/richtext"
)

// Collection names.
const (
	CollectionAnnouncements = "announcements"
	CollectionReads         = "announcement_reads"
)

// Statuses.
const (
	StatusDraft     = "draft"
	StatusScheduled = "scheduled"
	StatusPublished = "published"
	StatusArchived  = "archived"
	StatusExpired   = "expired"
)

// Audiences.
const (
	AudienceAll         = "all"
	AudienceAdmins      = "admins"
	AudienceUsers       = "users"
	AudienceDepartments = "departments"
)

// Register is kept for callers that expect the announcement module to install its
// own hooks. It delegates to the shared rich-text registration, which covers every
// collection with an editor field rather than this one alone.
func Register(app core.App) {
	richtext.Register(app)
}

// ---------------------------------------------------------------------------
// Lifecycle
// ---------------------------------------------------------------------------

// PublishDue moves scheduled announcements whose publish time has arrived into the
// published state, and reports how many moved.
//
// The transition happens here rather than by comparing publishAt at render time,
// because "published" is a state an operator looks at and a reader may be notified
// about. A purely computed state cannot be the trigger for anything.
func PublishDue(app core.App, now time.Time) (int, error) {
	records, err := app.FindRecordsByFilter(CollectionAnnouncements,
		"status = 'scheduled' && publishAt != '' && publishAt <= {:now} && deletedAt = ''",
		"publishAt", 0, 0, map[string]any{"now": now.UTC()})
	if err != nil {
		return 0, fmt.Errorf("announcement: find due announcements: %w", err)
	}

	moved := 0
	for _, rec := range records {
		// An announcement whose window has already closed is published and then
		// expired in the same pass, rather than being left in a state that says it
		// is visible when it never was.
		if at := rec.GetDateTime("expireAt"); !at.IsZero() && at.Time().Before(now) {
			rec.Set("status", StatusExpired)
		} else {
			rec.Set("status", StatusPublished)
		}

		if err := app.Save(rec); err != nil {
			return moved, fmt.Errorf("announcement: publish %s: %w", rec.Id, err)
		}
		moved++
	}
	return moved, nil
}

// ExpireDue moves published announcements whose expiry has passed into the expired
// state, and reports how many moved.
func ExpireDue(app core.App, now time.Time) (int, error) {
	records, err := app.FindRecordsByFilter(CollectionAnnouncements,
		"status = 'published' && expireAt != '' && expireAt <= {:now} && deletedAt = ''",
		"expireAt", 0, 0, map[string]any{"now": now.UTC()})
	if err != nil {
		return 0, fmt.Errorf("announcement: find expired announcements: %w", err)
	}

	moved := 0
	for _, rec := range records {
		rec.Set("status", StatusExpired)
		if err := app.Save(rec); err != nil {
			return moved, fmt.Errorf("announcement: expire %s: %w", rec.Id, err)
		}
		moved++
	}
	return moved, nil
}

// Reconcile brings every announcement's status into line with its times.
//
// It exists for the same reason as the job scheduler's catch-up: a process that
// was stopped has missed transitions, and running the publish step repeatedly does
// not fix one that was skipped while nothing was running.
func Reconcile(app core.App, now time.Time) (published, expired int, err error) {
	if published, err = PublishDue(app, now); err != nil {
		return 0, 0, err
	}
	if expired, err = ExpireDue(app, now); err != nil {
		return published, 0, err
	}
	return published, expired, nil
}

// ---------------------------------------------------------------------------
// Audience
// ---------------------------------------------------------------------------

// Viewable returns the filter that limits announcements to those an account may
// see, with its parameters.
//
// This is the read-side half of audience targeting. It is applied to every query
// the panel makes, including the detail page and the read-state lookup, because a
// filter applied only to the list is a filter that leaks through a direct link.
func (e *Engine) Viewable(account *core.Record, canAuthor bool) (string, map[string]any) {
	// The visible statuses are the same for everyone.
	base := "deletedAt = '' && (status = 'published' || status = 'expired')"

	// Whoever can write an announcement sees all of them. An author has to be
	// able to check what they published, and a publisher has to be able to see
	// what they are about to withdraw; a filter that hid them from the people
	// responsible for them would be actively unhelpful.
	//
	// This is also what makes the filter meaningful at all. Signing into this
	// panel requires an admins record, so every reader here is an administrator by
	// that test -- deciding visibility on it would show everyone everything and
	// the audience field would do nothing.
	if account == nil || canAuthor {
		return base, nil
	}

	params := map[string]any{}

	// The targeted audiences an account can be reached by. The clauses are built
	// only for the audiences that apply: a clause that is always false is not
	// expressed as a false literal, because the query language has no such
	// literal and the whole filter is rejected.
	clauses := []string{
		"audience = 'all'",
		"audience = 'users'",
	}
	if e.isAdministrator(account) {
		clauses = append(clauses, "audience = 'admins'")
	}

	// A department announcement reaches an account through its employee record.
	// The subquery is expressed as a relation filter rather than a list of ids so
	// a department change takes effect without rewriting anything.
	if dept := e.departmentOf(account); dept != "" {
		// The identifier suffix is required. A multi-relation field compared with
		// "?=" against a bare value matches nothing and reports no error, so the
		// reader quietly sees fewer announcements than they were targeted by.
		clauses = append(clauses, "(audience = 'departments' && departments.id ?= {:dept})")
		params["dept"] = dept
	}

	return base + " && (" + strings.Join(clauses, " || ") + ")", params
}

// departmentOf returns the department an account belongs to, or "".
//
// An employee record names one department, so this returns one value. The panel
// has no notion of an account in several departments, and pretending otherwise
// would match announcements the account was never in the audience for.
func (e *Engine) departmentOf(account *core.Record) string {
	employees, err := e.app.FindRecordsByFilter("employees",
		"user = {:u}", "", 1, 0, map[string]any{"u": account.Id})
	if err != nil || len(employees) == 0 {
		return ""
	}
	return employees[0].GetString("department")
}

// isAdministrator reports whether an account is a panel administrator.
//
// There is one account collection, so "administrator" is not a property of the
// collection: it is whether the account has an active admins record. Deciding it
// by collection name made every account an administrator, which silently collapsed
// the admins and users audiences into one.
func (e *Engine) isAdministrator(account *core.Record) bool {
	if account == nil {
		return false
	}
	if e.adminIDs == nil {
		e.adminIDs = map[string]bool{}
		admins, err := e.app.FindAllRecords("admins")
		if err == nil {
			for _, admin := range admins {
				if admin.GetString("status") == "active" {
					e.adminIDs[admin.GetString("user")] = true
				}
			}
		}
	}
	return e.adminIDs[account.Id]
}

// Targets returns the account ids an announcement is aimed at.
//
// It is what "who has not read this" is computed against: the set of readers the
// announcement was meant for, minus the ones who have read it. Returning the
// audience rather than querying every account keeps the answer correct for a
// department-targeted announcement.
func Targets(app core.App, rec *core.Record) ([]string, error) {
	audience := rec.GetString("audience")

	switch audience {
	case AudienceAdmins, AudienceUsers, AudienceAll:
		records, err := app.FindAllRecords("users")
		if err != nil {
			return nil, err
		}
		ids := make([]string, 0, len(records))
		for _, u := range records {
			ids = append(ids, u.Id)
		}
		return ids, nil

	case AudienceDepartments:
		departmentIDs := rec.GetStringSlice("departments")
		if len(departmentIDs) == 0 {
			return nil, nil
		}

		// Membership reaches an account through its employee record, so the
		// department relation is two hops away from the account.
		employees, err := app.FindAllRecords("employees")
		if err != nil {
			return nil, err
		}

		seen := map[string]bool{}
		ids := []string{}
		for _, employee := range employees {
			// The employee record carries a single department, not a set of them.
			// Reading it as a slice silently produced an empty list, which made
			// every department-targeted announcement report that it had no
			// audience to check against.
			if !containsString(departmentIDs, employee.GetString("department")) {
				continue
			}
			userID := employee.GetString("user")
			if userID == "" || seen[userID] {
				continue
			}
			seen[userID] = true
			ids = append(ids, userID)
		}
		return ids, nil

	default:
		return nil, nil
	}
}

// containsString reports whether a slice holds a value.
func containsString(have []string, want string) bool {
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

// ---------------------------------------------------------------------------
// Read state
// ---------------------------------------------------------------------------

// MarkRead records that an account has read an announcement.
//
// It is idempotent, and the counter is adjusted rather than incremented blindly:
// the unique index on (announcement, user) prevents a second row, but a naive
// counter would still count the second visit and the number on the screen would
// drift above the number of readers.
func MarkRead(app core.App, announcementID, userID string) error {
	if announcementID == "" || userID == "" {
		return nil
	}

	existing, err := app.FindFirstRecordByFilter(CollectionReads,
		"announcement = {:a} && user = {:u}",
		map[string]any{"a": announcementID, "u": userID})
	if err != nil && existing == nil {
		// No row yet; fall through to create one. A genuine query fault would
		// also land here, in which case the unique index refuses the insert and
		// the error is returned below.
		existing = nil
	}
	if existing != nil {
		return nil
	}

	col, err := app.FindCollectionByNameOrId(CollectionReads)
	if err != nil || col == nil {
		return fmt.Errorf("announcement: the read collection is missing")
	}

	rec := core.NewRecord(col)
	rec.Set("announcement", announcementID)
	rec.Set("user", userID)
	rec.Set("readAt", time.Now().UTC())

	if err := app.Save(rec); err != nil {
		// A concurrent read is the likely cause and is not an error: the row
		// exists, which is the outcome that was wanted.
		slog.Debug("a read record already existed", "announcement", announcementID, "error", err)
		return nil
	}

	return recalcReadCount(app, announcementID)
}

// recalcReadCount recounts an announcement's readers.
//
// It counts rather than increments. The unique index is the thing keeping the
// number honest, and recounting from it means the stored counter can always be
// repaired by asking the rows.
func recalcReadCount(app core.App, announcementID string) error {
	ann, err := app.FindRecordById(CollectionAnnouncements, announcementID)
	if err != nil || ann == nil {
		return nil
	}

	reads, err := app.FindRecordsByFilter(CollectionReads,
		"announcement = {:a}", "", 0, 0, map[string]any{"a": announcementID})
	if err != nil {
		return nil
	}

	if ann.GetInt("readCount") == len(reads) {
		return nil
	}
	ann.Set("readCount", len(reads))
	return app.Save(ann)
}

// RecalculateReadCounts repairs every stored counter from the read rows.
//
// It exists because the counter is denormalised: a number that only ever goes up
// by one is a number that is wrong the first time anything else touches the rows,
// and a task that recounts makes that recoverable.
func RecalculateReadCounts(app core.App) (int, error) {
	announcements, err := app.FindAllRecords(CollectionAnnouncements)
	if err != nil {
		return 0, err
	}

	fixed := 0
	for _, ann := range announcements {
		reads, err := app.FindRecordsByFilter(CollectionReads,
			"announcement = {:a}", "", 0, 0, map[string]any{"a": ann.Id})
		if err != nil {
			continue
		}
		if ann.GetInt("readCount") != len(reads) {
			ann.Set("readCount", len(reads))
			if err := app.Save(ann); err != nil {
				return fixed, err
			}
			fixed++
		}
	}
	return fixed, nil
}

// Engine answers audience questions.
//
// It caches which accounts are administrators because the visibility filter is
// built on every page render and asking the admins collection each time would be a
// query per render for an answer that changes rarely.
type Engine struct {
	app core.App

	mu       sync.Mutex
	adminIDs map[string]bool
}

// NewEngine returns an engine bound to an application.
func NewEngine(app core.App) *Engine { return &Engine{app: app} }

// Invalidate drops the cached administrator set, for a change to the admins
// collection.
func (e *Engine) Invalidate() {
	e.mu.Lock()
	e.adminIDs = nil
	e.mu.Unlock()
}
