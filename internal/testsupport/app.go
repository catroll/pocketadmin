// Package testsupport boots an application for tests.
//
// Every module's tests need the same thing: an isolated database with the
// platform's migrations applied and the same model hooks production registers.
// That last part matters -- a test that asserts behaviour production does not
// have is worse than no test -- so the hooks are bound here rather than in each
// package, where one of them would eventually be forgotten.
package testsupport

import (
	"testing"

	"github.com/pocketbase/pocketbase/core"

	"pocketadmin/internal/organization"
	"pocketadmin/internal/tree"

	// Registers the platform's migrations as a side effect.
	_ "pocketadmin/migrations"
)

// NewApp boots an isolated application and registers the model hooks.
//
// The data directory comes from t.TempDir, so each test sees a fresh database and
// nothing leaks between them.
func NewApp(t *testing.T) core.App {
	t.Helper()

	app := core.NewBaseApp(core.BaseAppConfig{
		DataDir:       t.TempDir(),
		EncryptionEnv: "pocketadmin_test",
	})

	if err := app.Bootstrap(); err != nil {
		t.Fatalf("bootstrap test app: %v", err)
	}
	if err := app.RunAllMigrations(); err != nil {
		t.Fatalf("run migrations: %v", err)
	}

	// These are the hooks main registers after migrating. A test app without them
	// would silently differ from production.
	organization.Register(app)
	tree.Register(app, tree.Options{Collection: "file_folders", Label: "folder"})

	return app
}
