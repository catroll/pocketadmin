package workbench_test

import (
	"testing"
	"time"

	"github.com/pocketbase/pocketbase/core"

	"pocketadmin/internal/testsupport"
	"pocketadmin/internal/workbench"
)

// account creates a user and returns the record.
func account(t *testing.T, app core.App, email string) *core.Record {
	t.Helper()

	col, _ := app.FindCollectionByNameOrId("users")
	rec := core.NewRecord(col)
	rec.Set("email", email)
	rec.Set("password", "change-me-please")
	rec.Set("passwordConfirm", "change-me-please")
	rec.Set("status", "active")
	if err := app.Save(rec); err != nil {
		t.Fatalf("save user: %v", err)
	}
	return rec
}

// todo creates a todo for an owner.
func todo(t *testing.T, app core.App, ownerID, title string) *core.Record {
	t.Helper()

	col, _ := app.FindCollectionByNameOrId("todos")
	rec := core.NewRecord(col)
	rec.Set("title", title)
	rec.Set("owner", ownerID)
	rec.Set("status", workbench.TodoPending)
	if err := app.Save(rec); err != nil {
		t.Fatalf("save todo: %v", err)
	}
	return rec
}

// TestATodoBelongsToOneAccount is the invariant the screens rest on.
//
// There is no grant that makes one administrator's todo list readable by another,
// so this is not a permission check that could be relaxed -- it is the model.
func TestATodoBelongsToOneAccount(t *testing.T) {
	app := testsupport.NewApp(t)

	alice := account(t, app, "alice@example.com")
	bob := account(t, app, "bob@example.com")

	task := todo(t, app, alice.Id, "Alice's task")

	if !workbench.OwnedBy(task, alice.Id) {
		t.Error("the owner was not recognised as the owner")
	}
	if workbench.OwnedBy(task, bob.Id) {
		t.Error("another account was recognised as the owner")
	}
	if workbench.OwnedBy(task, "") {
		t.Error("an empty account was recognised as the owner")
	}
	if workbench.OwnedBy(nil, alice.Id) {
		t.Error("a nil record was recognised as owned")
	}
}

// TestOwnerFilterIsScoped: the filter every workbench read uses must return one
// account's rows and nobody else's.
func TestOwnerFilterIsScoped(t *testing.T) {
	app := testsupport.NewApp(t)

	alice := account(t, app, "alice@example.com")
	bob := account(t, app, "bob@example.com")

	todo(t, app, alice.Id, "Alice's task")
	todo(t, app, bob.Id, "Bob's task")

	filter, params := workbench.TodoFilter(alice.Id)
	records, err := app.FindRecordsByFilter(workbench.CollectionTodos, filter, "", 0, 0, params)
	if err != nil {
		t.Fatalf("filter rejected: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("Alice's filter returned %d todos, want 1", len(records))
	}
	if records[0].GetString("title") != "Alice's task" {
		t.Errorf("the filter returned %q", records[0].GetString("title"))
	}
}

// TestCompletingAndReopeningKeepsTheRowHonest: a todo that says it was completed
// and is also outstanding contradicts itself.
func TestCompletingAndReopeningKeepsTheRowHonest(t *testing.T) {
	app := testsupport.NewApp(t)
	alice := account(t, app, "alice@example.com")
	task := todo(t, app, alice.Id, "Task")

	if err := workbench.CompleteTodo(app, task); err != nil {
		t.Fatalf("CompleteTodo: %v", err)
	}
	if task.GetString("status") != workbench.TodoDone {
		t.Errorf("status = %q, want done", task.GetString("status"))
	}
	if task.GetDateTime("completedAt").IsZero() {
		t.Error("no completion time was stamped")
	}

	if err := workbench.ReopenTodo(app, task); err != nil {
		t.Fatalf("ReopenTodo: %v", err)
	}
	if task.GetString("status") != workbench.TodoPending {
		t.Errorf("status = %q, want pending", task.GetString("status"))
	}
	if !task.GetDateTime("completedAt").IsZero() {
		t.Error("the completion time survived a reopen")
	}
}

// TestDueRemindersSkipsFinishedWork: a completed todo is not a reminder.
func TestDueRemindersSkipsFinishedWork(t *testing.T) {
	app := testsupport.NewApp(t)
	alice := account(t, app, "alice@example.com")

	past := time.Now().Add(-time.Hour).UTC()

	outstanding := todo(t, app, alice.Id, "Outstanding")
	outstanding.Set("remindAt", past)
	if err := app.Save(outstanding); err != nil {
		t.Fatalf("save: %v", err)
	}

	finished := todo(t, app, alice.Id, "Finished")
	finished.Set("remindAt", past)
	finished.Set("status", workbench.TodoDone)
	if err := app.Save(finished); err != nil {
		t.Fatalf("save: %v", err)
	}

	future := todo(t, app, alice.Id, "Future")
	future.Set("remindAt", time.Now().Add(time.Hour).UTC())
	if err := app.Save(future); err != nil {
		t.Fatalf("save: %v", err)
	}

	due, err := workbench.DueReminders(app, time.Now())
	if err != nil {
		t.Fatalf("DueReminders: %v", err)
	}
	if len(due) != 1 {
		t.Fatalf("found %d due reminders, want 1", len(due))
	}
	if due[0].GetString("title") != "Outstanding" {
		t.Errorf("the wrong todo was due: %q", due[0].GetString("title"))
	}
}

// ---------------------------------------------------------------------------
// Delivery
// ---------------------------------------------------------------------------

// delivery returns the delivery record for a message and an account, or nil.
func delivery(t *testing.T, app core.App, messageID, userID string) *core.Record {
	t.Helper()

	rec, err := app.FindFirstRecordByFilter(workbench.CollectionRecipients,
		"message = {:m} && user = {:u}", map[string]any{"m": messageID, "u": userID})
	if err != nil {
		return nil
	}
	return rec
}

// TestABroadcastBecomesOneDeliveryPerRecipient is what makes a read receipt, a
// per-recipient delete and a stable audience possible.
func TestABroadcastBecomesOneDeliveryPerRecipient(t *testing.T) {
	app := testsupport.NewApp(t)

	alice := account(t, app, "alice@example.com")
	bob := account(t, app, "bob@example.com")
	account(t, app, "carol@example.com")

	created, err := workbench.Send(app, workbench.Message{
		Title: "All hands", Content: "<p>Meeting</p>", Audience: "all",
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if created != 3 {
		t.Fatalf("created %d deliveries, want 3", created)
	}

	messages, _ := app.FindAllRecords(workbench.CollectionMessages)
	if len(messages) != 1 {
		t.Fatalf("created %d messages, want 1", len(messages))
	}

	for _, who := range []*core.Record{alice, bob} {
		if delivery(t, app, messages[0].Id, who.Id) == nil {
			t.Errorf("%s has no delivery", who.Email())
		}
	}
}

// TestDirectToNobodyStaysNobody: an empty recipient list must not fall back to
// everyone. A mistake there turns a selection that failed to load into the widest
// possible send.
func TestDirectToNobodyStaysNobody(t *testing.T) {
	app := testsupport.NewApp(t)
	account(t, app, "alice@example.com")

	created, err := workbench.Send(app, workbench.Message{
		Title: "Nobody", Content: "<p>x</p>", Audience: "direct",
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if created != 0 {
		t.Errorf("a direct message with no recipients reached %d people", created)
	}

	recipients, _ := app.FindAllRecords(workbench.CollectionRecipients)
	if len(recipients) != 0 {
		t.Errorf("%d deliveries were created", len(recipients))
	}
}

// TestDeliveryToTheSamePersonTwiceIsCollapsed: a targeted list can legitimately
// name somebody twice, and the second delivery is the unique index doing its job
// rather than a failure.
func TestDeliveryToTheSamePersonTwiceIsCollapsed(t *testing.T) {
	app := testsupport.NewApp(t)
	alice := account(t, app, "alice@example.com")

	created, err := workbench.Send(app, workbench.Message{
		Title: "Twice", Content: "<p>x</p>", Audience: "direct",
		Recipients: []string{alice.Id, alice.Id},
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if created != 1 {
		t.Errorf("created %d deliveries for one person named twice, want 1", created)
	}
}

// TestOneRecipientCannotReadOrDeleteAnothersDelivery is the other half of
// ownership. Knowing a delivery id must not be enough.
func TestOneRecipientCannotReadOrDeleteAnothersDelivery(t *testing.T) {
	app := testsupport.NewApp(t)

	alice := account(t, app, "alice@example.com")
	bob := account(t, app, "bob@example.com")

	if _, err := workbench.Send(app, workbench.Message{
		Title: "For everyone", Content: "<p>x</p>", Audience: "all",
	}); err != nil {
		t.Fatalf("Send: %v", err)
	}

	messages, _ := app.FindAllRecords(workbench.CollectionMessages)
	aliceDelivery := delivery(t, app, messages[0].Id, alice.Id)
	if aliceDelivery == nil {
		t.Fatal("Alice has no delivery")
	}

	if err := workbench.MarkMessageRead(app, aliceDelivery.Id, bob.Id); err == nil {
		t.Error("Bob was allowed to mark Alice's message read")
	}
	if err := workbench.DeleteMessage(app, aliceDelivery.Id, bob.Id); err == nil {
		t.Error("Bob was allowed to delete Alice's message")
	}

	// And Alice's own copy is untouched by either attempt.
	reloaded, _ := app.FindRecordById(workbench.CollectionRecipients, aliceDelivery.Id)
	if !reloaded.GetDateTime("readAt").IsZero() {
		t.Error("Alice's delivery was marked read")
	}
	if !reloaded.GetDateTime("deletedAt").IsZero() {
		t.Error("Alice's delivery was deleted")
	}
}

// TestPerRecipientDeleteLeavesTheOthers: one person clearing their inbox says
// nothing about anybody else's.
func TestPerRecipientDeleteLeavesTheOthers(t *testing.T) {
	app := testsupport.NewApp(t)

	alice := account(t, app, "alice@example.com")
	bob := account(t, app, "bob@example.com")

	if _, err := workbench.Send(app, workbench.Message{
		Title: "For everyone", Content: "<p>x</p>", Audience: "all",
	}); err != nil {
		t.Fatalf("Send: %v", err)
	}

	messages, _ := app.FindAllRecords(workbench.CollectionMessages)
	bobDelivery := delivery(t, app, messages[0].Id, bob.Id)

	if err := workbench.DeleteMessage(app, bobDelivery.Id, bob.Id); err != nil {
		t.Fatalf("DeleteMessage: %v", err)
	}

	aliceDelivery := delivery(t, app, messages[0].Id, alice.Id)
	if aliceDelivery == nil {
		t.Fatal("Alice's delivery disappeared")
	}
	if !aliceDelivery.GetDateTime("deletedAt").IsZero() {
		t.Error("Alice's delivery was deleted by Bob clearing his")
	}

	// And Bob's inbox no longer offers it.
	unread := workbench.UnreadCount(app, bob.Id)
	if unread != 0 {
		t.Errorf("Bob's unread count is %d after deleting, want 0", unread)
	}
}

// TestMarkAllReadOnlyTouchesTheCallersDeliveries: a bulk action is the easiest
// place to accidentally act on everybody's rows.
func TestMarkAllReadOnlyTouchesTheCallersDeliveries(t *testing.T) {
	app := testsupport.NewApp(t)

	alice := account(t, app, "alice@example.com")
	bob := account(t, app, "bob@example.com")

	if _, err := workbench.Send(app, workbench.Message{
		Title: "For everyone", Content: "<p>x</p>", Audience: "all",
	}); err != nil {
		t.Fatalf("Send: %v", err)
	}

	marked, err := workbench.MarkAllRead(app, bob.Id)
	if err != nil {
		t.Fatalf("MarkAllRead: %v", err)
	}
	if marked != 1 {
		t.Errorf("marked %d, want 1", marked)
	}

	if got := workbench.UnreadCount(app, bob.Id); got != 0 {
		t.Errorf("Bob still has %d unread", got)
	}
	if got := workbench.UnreadCount(app, alice.Id); got != 1 {
		t.Errorf("Alice's unread count changed to %d", got)
	}
}

// TestAudienceTargetingReachesTheRightPeople: role and department broadcasts go
// through records rather than through a permission, so they are worth asserting.
func TestAudienceTargetingReachesTheRightPeople(t *testing.T) {
	app := testsupport.NewApp(t)

	alice := account(t, app, "alice@example.com")
	bob := account(t, app, "bob@example.com")

	// A department, an employee in it, and an admin holding a role.
	deptCol, _ := app.FindCollectionByNameOrId("departments")
	dept := core.NewRecord(deptCol)
	dept.Set("name", "Engineering")
	dept.Set("code", "eng")
	dept.Set("status", "active")
	if err := app.Save(dept); err != nil {
		t.Fatalf("save department: %v", err)
	}

	empCol, _ := app.FindCollectionByNameOrId("employees")
	employee := core.NewRecord(empCol)
	employee.Set("user", alice.Id)
	employee.Set("name", "Alice")
	employee.Set("employeeNo", "E1")
	employee.Set("department", dept.Id)
	employee.Set("status", "active")
	if err := app.Save(employee); err != nil {
		t.Fatalf("save employee: %v", err)
	}

	roleCol, _ := app.FindCollectionByNameOrId("roles")
	role := core.NewRecord(roleCol)
	role.Set("name", "Ops")
	role.Set("code", "ops")
	role.Set("status", "active")
	if err := app.Save(role); err != nil {
		t.Fatalf("save role: %v", err)
	}

	adminCol, _ := app.FindCollectionByNameOrId("admins")
	admin := core.NewRecord(adminCol)
	admin.Set("user", bob.Id)
	admin.Set("status", "active")
	admin.Set("roles", []string{role.Id})
	if err := app.Save(admin); err != nil {
		t.Fatalf("save admin: %v", err)
	}

	// By department: only Alice.
	created, err := workbench.Send(app, workbench.Message{
		Title: "Engineering", Content: "<p>x</p>", Audience: "department",
		DepartmentIDs: []string{dept.Id},
	})
	if err != nil {
		t.Fatalf("Send by department: %v", err)
	}
	if created != 1 {
		t.Errorf("a department broadcast reached %d people, want 1", created)
	}

	// By role: only Bob.
	created, err = workbench.Send(app, workbench.Message{
		Title: "Ops", Content: "<p>x</p>", Audience: "role",
		RoleIDs: []string{role.Id},
	})
	if err != nil {
		t.Fatalf("Send by role: %v", err)
	}
	if created != 1 {
		t.Errorf("a role broadcast reached %d people, want 1", created)
	}
}

// ---------------------------------------------------------------------------
// Visits and links
// ---------------------------------------------------------------------------

// TestAVisitIsOneRowWithACount: a page visited twenty times is one entry, or the
// list fills with the page the operator is working in.
func TestAVisitIsOneRowWithACount(t *testing.T) {
	app := testsupport.NewApp(t)
	alice := account(t, app, "alice@example.com")

	for i := 0; i < 5; i++ {
		if err := workbench.RecordVisit(app, alice.Id, "Workbench", "/workbench", "fas fa-columns"); err != nil {
			t.Fatalf("RecordVisit: %v", err)
		}
	}

	records, err := app.FindRecordsByFilter(workbench.CollectionVisits, "user = {:u}", "", 0, 0,
		map[string]any{"u": alice.Id})
	if err != nil {
		t.Fatalf("read visits: %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("%d rows for one page visited five times, want 1", len(records))
	}
	if records[0].GetInt("visitCount") != 5 {
		t.Errorf("visitCount = %d, want 5", records[0].GetInt("visitCount"))
	}
}

// TestVisitsArePrunedAndNonPagesAreIgnored: static assets and the API are not
// pages a person navigated to, and the list is bounded.
func TestVisitsArePrunedAndNonPagesAreIgnored(t *testing.T) {
	app := testsupport.NewApp(t)
	alice := account(t, app, "alice@example.com")

	for _, path := range []string{"/static/app.css", "/api/collections/users/records", ""} {
		if err := workbench.RecordVisit(app, alice.Id, "Ignore", path, ""); err != nil {
			t.Fatalf("RecordVisit: %v", err)
		}
	}
	records, _ := app.FindRecordsByFilter(workbench.CollectionVisits, "", "", 0, 0)
	if len(records) != 0 {
		t.Errorf("%d non-pages were recorded", len(records))
	}

	// Many distinct pages, so the bound has something to prune.
	for i := 0; i < 40; i++ {
		path := "/page/" + string(rune('a'+i%26)) + string(rune('a'+i/26))
		if err := workbench.RecordVisit(app, alice.Id, "Page", path, ""); err != nil {
			t.Fatalf("RecordVisit: %v", err)
		}
	}
	records, _ = app.FindRecordsByFilter(workbench.CollectionVisits, "user = {:u}", "", 0, 0,
		map[string]any{"u": alice.Id})
	if len(records) > 30 {
		t.Errorf("%d visits were kept, want at most 30", len(records))
	}
}

// TestQuickLinksFallBackToTheDefaults: an account with its own links sees those;
// one without sees the defaults.
func TestQuickLinksFallBackToTheDefaults(t *testing.T) {
	app := testsupport.NewApp(t)
	alice := account(t, app, "alice@example.com")

	col, _ := app.FindCollectionByNameOrId(workbench.CollectionQuickLinks)

	for _, title := range []string{"Default one", "Default two"} {
		rec := core.NewRecord(col)
		rec.Set("title", title)
		rec.Set("path", "/"+title)
		if err := app.Save(rec); err != nil {
			t.Fatalf("save default link: %v", err)
		}
	}

	links, err := workbench.QuickLinks(app, alice.Id)
	if err != nil {
		t.Fatalf("QuickLinks: %v", err)
	}
	if len(links) != 2 {
		t.Fatalf("Alice saw %d default links, want 2", len(links))
	}

	own := core.NewRecord(col)
	own.Set("title", "Mine")
	own.Set("path", "/mine")
	own.Set("user", alice.Id)
	if err := app.Save(own); err != nil {
		t.Fatalf("save own link: %v", err)
	}

	links, _ = workbench.QuickLinks(app, alice.Id)
	if len(links) != 1 || links[0].GetString("title") != "Mine" {
		// A merge would resurrect a default the account deliberately removed.
		t.Errorf("Alice saw %d links; her own should replace the defaults", len(links))
	}
}

// TestLinksWithoutPermissionAreHidden: a shortcut whose target the reader may not
// open is a trap, not a shortcut.
func TestLinksWithoutPermissionAreHidden(t *testing.T) {
	app := testsupport.NewApp(t)

	col, _ := app.FindCollectionByNameOrId(workbench.CollectionQuickLinks)
	mk := func(title, code string) *core.Record {
		rec := core.NewRecord(col)
		rec.Set("title", title)
		rec.Set("path", "/"+title)
		rec.Set("permissionCode", code)
		if err := app.Save(rec); err != nil {
			t.Fatalf("save link: %v", err)
		}
		return rec
	}

	mk("Open", "")
	mk("Allowed", "a:allowed")
	mk("Denied", "a:denied")

	links, _ := workbench.QuickLinks(app, "")
	visible := workbench.FilterByPermission(links, func(code string) bool { return code == "a:allowed" })

	if len(visible) != 2 {
		t.Fatalf("%d links are visible, want 2", len(visible))
	}
	for _, rec := range visible {
		if rec.GetString("title") == "Denied" {
			t.Error("a link the actor may not follow was shown")
		}
	}
}
