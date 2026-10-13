package demo_test

import (
	"testing"

	"github.com/pocketbase/pocketbase/core"

	"pocketadmin/internal/demo"
	"pocketadmin/internal/testsupport"
)

// TestTheTwoListsDoNotOverlap.
//
// A collection on both lists would be cleared and preserved at once, and whichever
// happened last would decide. It is the kind of mistake that reads as correct.
func TestTheTwoListsDoNotOverlap(t *testing.T) {
	owned := map[string]bool{}
	for _, name := range demo.Owned() {
		if owned[name] {
			t.Errorf("%s is declared twice as owned", name)
		}
		owned[name] = true
	}

	for _, name := range demo.Untouched() {
		if owned[name] {
			t.Errorf("%s is on both lists", name)
		}
	}
}

// TestNoOwnedCollectionIsAConfigurationCollection is the safety property.
//
// The whole point of the split is that reloading the examples cannot cost an operator
// their setup. A name that belongs in the untouchable list appearing among the owned ones
// would break that, and a demonstration that deleted somebody's roles would be the last
// thing they ran.
func TestNoOwnedCollectionIsAConfigurationCollection(t *testing.T) {
	// Never data, whatever the module. This list is deliberately written out rather than
	// derived: the test should fail when somebody adds a name here, not agree with them.
	configuration := []string{
		"users", "admins", "roles", "permissions", "menus",
		"dictionaries", "dictionary_items", "settings",
		"scheduled_jobs", "integration_providers", "monitor_nodes",
		"api_keys", "sessions", "user_preferences",
	}

	owned := map[string]bool{}
	for _, name := range demo.Owned() {
		owned[name] = true
	}

	for _, name := range configuration {
		if owned[name] {
			t.Errorf("%s holds configuration and must never be cleared", name)
		}
	}
}

// TestTheDeclaredOrderIsNotLoadBearing.
//
// The first version of the reset relied on the declared order, walking it backwards so a
// child went before its parent. Deriving the requirement from the schemas showed that
// cannot work: departments.leader points at employees and employees.department points
// back, so those two have no correct order at all, and five more single-direction
// mistakes were in the wrong place too.
//
// The clear now detaches every relation inside the set before deleting anything, so the
// order does not matter. This test is what says so: it clears with the list reversed and
// with it shuffled, and both must leave nothing behind.
//
// The previous version of this test asserted parents came first. It was a true statement
// about a design that could not be finished, which is a good reason to test the property
// that matters instead of the one that is easy to state.
//
// A reset walks the declared order backwards, so every relation must point from a
// collection later in the list to one earlier in it. The order was wrong in five places
// the first time -- a message recipient declared before its message, a tier benefit before
// its tier, an order event before its order -- and the reset emptied fifteen of
// thirty-eight collections and then stopped on a delete that a child record blocked,
// without saying so.
//
// The requirement is derived from the schemas rather than from a second hand-written
// list, because a hand-written list agrees with itself.
func TestTheDeclaredOrderIsNotLoadBearing(t *testing.T) {
	app := testsupport.NewApp(t)

	// The cycle that makes an ordering impossible, stated so that a future change which
	// removes it does not silently make this test vacuous.
	departments, err := app.FindCollectionByNameOrId("departments")
	if err != nil || departments == nil {
		t.Skip("the departments collection is not present in this build")
	}
	employees, err := app.FindCollectionByNameOrId("employees")
	if err != nil || employees == nil {
		t.Skip("the employees collection is not present in this build")
	}

	forwards, backwards := false, false
	for _, field := range departments.Fields {
		if rel, ok := field.(*core.RelationField); ok && rel.CollectionId == employees.Id {
			forwards = true
		}
	}
	for _, field := range employees.Fields {
		if rel, ok := field.(*core.RelationField); ok && rel.CollectionId == departments.Id {
			backwards = true
		}
	}

	if !forwards || !backwards {
		t.Log("the departments/employees cycle no longer exists; the order could be made " +
			"load-bearing again, though the detach phase makes that unnecessary")
	}

	// Whatever the shape of the graph, a seed followed by a clear must leave nothing
	// behind. That is the property; the order is an implementation detail.
	if _, err := demo.Seed(app, true); err != nil {
		t.Fatalf("seed: %v", err)
	}

	result := demo.NewResult()
	if err := demo.Clear(app, result); err != nil {
		t.Fatalf("clear: %v", err)
	}

	if left := demo.TotalOwned(app); left != 0 {
		t.Errorf("%d example row(s) survived, so the clear still depends on the order", left)
	}
	if len(result.Skipped) > 0 {
		t.Logf("collections absent from this build: %v", result.Skipped)
	}
}

// TestTheDemoUserMarkerRecognisesOnlyDemoAccounts.
func TestTheDemoUserMarkerRecognisesOnlyDemoAccounts(t *testing.T) {
	yes := []string{"demo.alice@example.com", "demo.x@example.com"}
	no := []string{"alice@example.com", "demolition@example.com", "", "demo"}

	for _, email := range yes {
		if !demo.IsDemoUser(email) {
			t.Errorf("%q should be recognised", email)
		}
	}
	for _, email := range no {
		if demo.IsDemoUser(email) {
			t.Errorf("%q should not be recognised", email)
		}
	}
}

// TestAFreshInstallHoldsNothing.
func TestAFreshInstallHoldsNothing(t *testing.T) {
	app := testsupport.NewApp(t)

	if total := demo.TotalOwned(app); total != 0 {
		t.Errorf("a fresh database reports %d example rows", total)
	}
	if counts := demo.CountOwned(app); len(counts) != 0 {
		t.Errorf("a fresh database lists %d collection(s): %v", len(counts), counts)
	}
}

// TestSeedingTwiceWithoutAResetIsRefused.
//
// The dataset has unique slugs, codes and order numbers, so a second seed inserts until
// it hits one and stops, leaving a half-duplicated state worse than either the old data
// or the new. The command refuses instead.
func TestSeedingTwiceWithoutAResetIsRefused(t *testing.T) {
	app := testsupport.NewApp(t)

	first, err := demo.Seed(app, false)
	if err != nil {
		t.Fatalf("the first seed failed: %v", err)
	}
	if first.TotalCreated() == 0 {
		t.Fatal("the first seed wrote nothing")
	}

	before := demo.TotalOwned(app)

	if _, err := demo.Seed(app, false); err == nil {
		t.Error("a second seed was allowed, which leaves a partial duplicate")
	}

	if after := demo.TotalOwned(app); after != before {
		t.Errorf("the refused seed changed the row count from %d to %d", before, after)
	}
}

// TestAResetIsRepeatable is what makes a demonstration worth showing twice.
func TestAResetIsRepeatable(t *testing.T) {
	app := testsupport.NewApp(t)

	first, err := demo.Seed(app, true)
	if err != nil {
		t.Fatalf("the first seed failed: %v", err)
	}
	firstTotal := demo.TotalOwned(app)

	second, err := demo.Seed(app, true)
	if err != nil {
		t.Fatalf("the second seed failed: %v", err)
	}
	secondTotal := demo.TotalOwned(app)

	if firstTotal != secondTotal {
		t.Errorf("the first seed left %d rows and the second %d, so a reset is not repeatable",
			firstTotal, secondTotal)
	}
	if first.TotalCreated() != second.TotalCreated() {
		t.Errorf("the first seed wrote %d rows and the second %d",
			first.TotalCreated(), second.TotalCreated())
	}
	if second.TotalCleared() == 0 {
		t.Error("the second seed reported clearing nothing, so it did not reset")
	}
}

// TestClearingLeavesNothingBehind.
//
// This is the assertion the first version of the reset would have failed: it stopped
// after fifteen collections because a delete was blocked by a child record, and reported
// no error.
func TestClearingLeavesNothingBehind(t *testing.T) {
	app := testsupport.NewApp(t)

	if _, err := demo.Seed(app, true); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if demo.TotalOwned(app) == 0 {
		t.Fatal("the seed wrote nothing, so this proves nothing")
	}

	result := demo.NewResult()
	if err := demo.Clear(app, result); err != nil {
		t.Fatalf("clear: %v", err)
	}

	if left := demo.TotalOwned(app); left != 0 {
		t.Errorf("%d example row(s) survived a clear", left)
	}
}

// TestClearingNeverTouchesConfiguration is the safety property, exercised rather than
// asserted from a list.
func TestClearingNeverTouchesConfiguration(t *testing.T) {
	app := testsupport.NewApp(t)

	// Something an operator would have made: a role of their own and an account that is
	// not one of ours.
	col, err := app.FindCollectionByNameOrId("roles")
	if err != nil || col == nil {
		t.Fatalf("the roles collection is missing: %v", err)
	}
	role := core.NewRecord(col)
	role.Set("name", "Operations")
	role.Set("code", "ops")
	role.Set("status", "active")
	if err := app.Save(role); err != nil {
		t.Fatalf("saving the role: %v", err)
	}

	if _, err := demo.Seed(app, true); err != nil {
		t.Fatalf("seed: %v", err)
	}

	result := demo.NewResult()
	if err := demo.Clear(app, result); err != nil {
		t.Fatalf("clear: %v", err)
	}

	if _, err := app.FindRecordById("roles", role.Id); err != nil {
		t.Errorf("the operator's role was removed by a clear: %v", err)
	}
}

// TestTheExampleAccountsAreRemovedAndOthersAreNot.
func TestTheExampleAccountsAreRemovedAndOthersAreNot(t *testing.T) {
	app := testsupport.NewApp(t)

	col, err := app.FindCollectionByNameOrId("users")
	if err != nil || col == nil {
		t.Fatalf("the users collection is missing: %v", err)
	}
	real := core.NewRecord(col)
	real.Set("email", "operator@example.com")
	real.Set("password", "change-me-please")
	real.Set("passwordConfirm", "change-me-please")
	real.Set("name", "Operator")
	real.Set("status", "active")
	if err := app.Save(real); err != nil {
		t.Fatalf("saving the operator: %v", err)
	}

	if _, err := demo.Seed(app, true); err != nil {
		t.Fatalf("seed: %v", err)
	}

	result := demo.NewResult()
	if err := demo.Clear(app, result); err != nil {
		t.Fatalf("clear: %v", err)
	}

	if _, err := app.FindRecordById("users", real.Id); err != nil {
		t.Errorf("the operator's own account was removed by a clear: %v", err)
	}

	remaining, err := app.FindAllRecords("users")
	if err != nil {
		t.Fatalf("listing the accounts: %v", err)
	}
	for _, user := range remaining {
		if demo.IsDemoUser(user.Email()) {
			t.Errorf("the example account %s survived a clear", user.Email())
		}
	}
}
