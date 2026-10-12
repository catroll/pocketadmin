package announcement_test

import (
	"testing"

	"github.com/pocketbase/pocketbase/core"

	"pocketadmin/internal/announcement"
	"pocketadmin/internal/testsupport"
)

// fixture builds an account, optionally with an admin record and a department.
func fixture(t *testing.T, app core.App, email string, isAdmin bool, departmentID string) *core.Record {
	t.Helper()

	users, _ := app.FindCollectionByNameOrId("users")
	user := core.NewRecord(users)
	user.Set("email", email)
	user.Set("password", "change-me-please")
	user.Set("passwordConfirm", "change-me-please")
	user.Set("status", "active")
	if err := app.Save(user); err != nil {
		t.Fatalf("save user: %v", err)
	}

	if isAdmin {
		admins, _ := app.FindCollectionByNameOrId("admins")
		admin := core.NewRecord(admins)
		admin.Set("user", user.Id)
		admin.Set("status", "active")
		if err := app.Save(admin); err != nil {
			t.Fatalf("save admin: %v", err)
		}
	}

	if departmentID != "" {
		employees, _ := app.FindCollectionByNameOrId("employees")
		employee := core.NewRecord(employees)
		employee.Set("user", user.Id)
		employee.Set("name", email)
		employee.Set("employeeNo", email)
		// The field is singular. Reading it as a slice was the defect that made
		// every department-targeted announcement report no audience.
		employee.Set("department", departmentID)
		employee.Set("status", "active")
		if err := app.Save(employee); err != nil {
			t.Fatalf("save employee: %v", err)
		}
	}

	return user
}

// department creates a department and returns its id.
func department(t *testing.T, app core.App, name, code string) string {
	t.Helper()

	col, _ := app.FindCollectionByNameOrId("departments")
	rec := core.NewRecord(col)
	rec.Set("name", name)
	rec.Set("code", code)
	rec.Set("status", "active")
	if err := app.Save(rec); err != nil {
		t.Fatalf("save department: %v", err)
	}
	return rec.Id
}

// announce creates a published announcement for an audience.
func announce(t *testing.T, app core.App, title, audience string, departmentIDs []string) *core.Record {
	t.Helper()

	col, _ := app.FindCollectionByNameOrId("announcements")
	rec := core.NewRecord(col)
	rec.Set("title", title)
	rec.Set("type", "notice")
	rec.Set("level", "normal")
	rec.Set("status", "published")
	rec.Set("audience", audience)
	rec.Set("content", "<p>body</p>")
	rec.Set("departments", departmentIDs)
	if err := app.Save(rec); err != nil {
		t.Fatalf("save announcement: %v", err)
	}
	return rec
}

// TestTargetsResolvesDepartmentMembers is the defect that shipped and was caught
// by reading a zero where a count belonged.
//
// The employee record names a single department. Reading it as a slice silently
// produced an empty set, so every department-targeted announcement claimed it had
// nobody to check against while the screen showed a read count.
func TestTargetsResolvesDepartmentMembers(t *testing.T) {
	app := testsupport.NewApp(t)

	engineering := department(t, app, "Engineering", "eng")
	sales := department(t, app, "Sales", "sales")

	engineer := fixture(t, app, "engineer@example.com", true, engineering)
	fixture(t, app, "sales@example.com", true, sales)

	ann := announce(t, app, "For engineering", announcement.AudienceDepartments, []string{engineering})

	targets, err := announcement.Targets(app, ann)
	if err != nil {
		t.Fatalf("Targets: %v", err)
	}

	if len(targets) != 1 {
		t.Fatalf("resolved %d targets, want 1: %v", len(targets), targets)
	}
	if targets[0] != engineer.Id {
		t.Errorf("the target is %s, want the engineer %s", targets[0], engineer.Id)
	}
}

// TestTargetsForEveryAudience: an announcement aimed at everyone reaches every
// account.
func TestTargetsForEveryAudience(t *testing.T) {
	app := testsupport.NewApp(t)

	fixture(t, app, "one@example.com", true, "")
	fixture(t, app, "two@example.com", true, "")

	ann := announce(t, app, "For everyone", announcement.AudienceAll, nil)

	targets, err := announcement.Targets(app, ann)
	if err != nil {
		t.Fatalf("Targets: %v", err)
	}
	if len(targets) != 2 {
		t.Errorf("resolved %d targets, want 2", len(targets))
	}
}

// TestTargetsIsEmptyWithoutDepartments: a department announcement with no
// departments selected targets nobody, and saying so is better than falling back
// to everyone.
func TestTargetsIsEmptyWithoutDepartments(t *testing.T) {
	app := testsupport.NewApp(t)
	fixture(t, app, "one@example.com", true, "")

	ann := announce(t, app, "Nowhere", announcement.AudienceDepartments, nil)

	targets, err := announcement.Targets(app, ann)
	if err != nil {
		t.Fatalf("Targets: %v", err)
	}
	if len(targets) != 0 {
		t.Errorf("resolved %d targets for an announcement with no departments", len(targets))
	}
}

// TestViewableIsAuthorAware: whoever can write an announcement sees all of them.
//
// Signing into this panel requires an admins record, so every reader here is an
// administrator by that test. Deciding visibility on it showed everyone
// everything and made the audience field do nothing.
func TestViewableIsAuthorAware(t *testing.T) {
	app := testsupport.NewApp(t)

	engineering := department(t, app, "Engineering", "eng")
	sales := department(t, app, "Sales", "sales")

	engineer := fixture(t, app, "engineer@example.com", true, engineering)
	fixture(t, app, "sales@example.com", true, sales)

	engine := announcement.NewEngine(app)

	announce(t, app, "Everyone", announcement.AudienceAll, nil)
	announce(t, app, "Departments", announcement.AudienceDepartments, []string{engineering})

	visible := func(account *core.Record, canAuthor bool, label string) int {
		filter, params := engine.Viewable(account, canAuthor)
		records, err := app.FindRecordsByFilter(announcement.CollectionAnnouncements, filter, "", 0, 0, params)
		if err != nil {
			t.Fatalf("%s: the filter was rejected: %v\nfilter: %s", label, err, filter)
		}
		return len(records)
	}

	// An author sees both.
	if got := visible(engineer, true, "author"); got != 2 {
		t.Errorf("an author saw %d announcements, want 2", got)
	}

	// A reader in the targeted department sees both.
	if got := visible(engineer, false, "targeted reader"); got != 2 {
		t.Errorf("a targeted reader saw %d, want 2", got)
	}

	// A reader outside it sees only the one aimed at everyone.
	if got := visible(fixture(t, app, "outsider@example.com", true, sales), false, "outside reader"); got != 1 {
		t.Errorf("a reader outside the department saw %d, want 1", got)
	}

	// An account with no department sees only the one aimed at everyone, and the
	// filter is still valid -- which is the case that produced a 500 when the
	// unavailable audience was expressed as a false literal.
	if got := visible(fixture(t, app, "nodept@example.com", true, ""), false, "no department"); got != 1 {
		t.Errorf("a reader with no department saw %d, want 1", got)
	}
}

// TestSanitiserRunsOnEveryWritePath: the model hook is what makes the render path
// safe, so it has to fire for a record saved directly, not only through a handler.
func TestSanitiserRunsOnEveryWritePath(t *testing.T) {
	app := testsupport.NewApp(t)

	col, _ := app.FindCollectionByNameOrId("announcements")
	rec := core.NewRecord(col)
	rec.Set("title", "Hostile")
	rec.Set("type", "notice")
	rec.Set("level", "normal")
	rec.Set("status", "draft")
	rec.Set("audience", "all")
	rec.Set("content", `<p>ok</p><script>alert(1)</script><img src=x onerror="alert(1)">`)

	if err := app.Save(rec); err != nil {
		t.Fatalf("save: %v", err)
	}

	stored := rec.GetString("content")
	if stored == "" {
		t.Fatal("no content was stored")
	}
	for _, bad := range []string{"<script", "onerror", "alert(1)"} {
		if contains(stored, bad) {
			t.Errorf("%q survived the model hook: %q", bad, stored)
		}
	}
	if !contains(stored, "<p>ok</p>") {
		t.Errorf("the ordinary markup was removed: %q", stored)
	}
}

// contains is a small helper so the test does not import strings twice over.
func contains(haystack, needle string) bool {
	if len(needle) > len(haystack) {
		return false
	}
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
