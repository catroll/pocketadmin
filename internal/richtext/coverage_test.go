package richtext_test

import (
	"strings"
	"testing"

	"github.com/pocketbase/pocketbase/core"

	"pocketadmin/internal/richtext"
	"pocketadmin/internal/testsupport"
)

// TestEveryEditorFieldIsSanitised is the drift guard.
//
// Five of the six editor fields in this platform were unprotected when the
// sanitiser was first written: only announcements were covered, and a product
// description, a todo, a message, a category description and a release note all
// stored whatever HTML was submitted. The hazard belongs to the field type, so the
// coverage belongs to the field type too -- and this test is what keeps a new
// editor field from being added without it.
func TestEveryEditorFieldIsSanitised(t *testing.T) {
	app := testsupport.NewApp(t)

	collections, err := app.FindAllCollections()
	if err != nil {
		t.Fatalf("read the schema: %v", err)
	}

	missing := []string{}

	for _, col := range collections {
		// PocketBase's own collections are not this platform's to guard.
		if strings.HasPrefix(col.Name, "_") {
			continue
		}

		for _, field := range col.Fields {
			if _, ok := field.(*core.EditorField); !ok {
				continue
			}

			covered := false
			for _, name := range richtext.Fields[col.Name] {
				if name == field.GetName() {
					covered = true
					break
				}
			}
			if !covered {
				missing = append(missing, col.Name+"."+field.GetName())
			}
		}
	}

	if len(missing) > 0 {
		t.Errorf("these rich-text fields are stored without sanitising:\n  %s\n"+
			"Add them to richtext.Fields.", strings.Join(missing, "\n  "))
	}
}

// TestTheListHasNoStaleEntries: an entry naming a field that no longer exists is a
// sign the list drifted the other way, which is harmless but worth knowing.
func TestTheListHasNoStaleEntries(t *testing.T) {
	app := testsupport.NewApp(t)

	for name, fields := range richtext.Fields {
		col, err := app.FindCollectionByNameOrId(name)
		if err != nil || col == nil {
			t.Errorf("richtext.Fields names the collection %q, which does not exist", name)
			continue
		}

		for _, field := range fields {
			got := col.Fields.GetByName(field)
			if got == nil {
				t.Errorf("richtext.Fields names %s.%s, which does not exist", name, field)
				continue
			}
			if _, ok := got.(*core.EditorField); !ok {
				t.Errorf("%s.%s is listed as rich text but is a %T", name, field, got)
			}
		}
	}
}

// TestTheHookActuallyFires is the behaviour behind the guard: a record written
// straight to the database, through no screen and no handler, comes back clean.
func TestTheHookActuallyFires(t *testing.T) {
	app := testsupport.NewApp(t)

	hostile := `<p>Fine</p><script>alert(1)</script><img src=x onerror="alert(2)">`

	cases := []struct {
		collection string
		field      string
		seed       func(rec *core.Record)
	}{
		{
			collection: "products",
			field:      "description",
			seed: func(rec *core.Record) {
				rec.Set("kind", "physical")
				rec.Set("name", "Probe")
				rec.Set("slug", "probe-"+rec.Id)
				rec.Set("status", "draft")
				rec.Set("stockMode", "single")
				rec.Set("currency", "USD")
			},
		},
		{
			collection: "product_categories",
			field:      "description",
			seed: func(rec *core.Record) {
				rec.Set("name", "Probe")
				rec.Set("slug", "probe-"+rec.Id)
				rec.Set("status", "active")
			},
		},
		{
			collection: "system_updates",
			field:      "releaseNotes",
			seed: func(rec *core.Record) {
				rec.Set("version", "9.9.9")
				rec.Set("channel", "stable")
				rec.Set("status", "available")
			},
		},
	}

	for _, tc := range cases {
		col, err := app.FindCollectionByNameOrId(tc.collection)
		if err != nil {
			t.Fatalf("%s: %v", tc.collection, err)
		}

		rec := core.NewRecord(col)
		tc.seed(rec)
		rec.Set(tc.field, hostile)

		if err := app.Save(rec); err != nil {
			t.Errorf("%s: save: %v", tc.collection, err)
			continue
		}

		stored := rec.GetString(tc.field)
		for _, bad := range []string{"<script", "onerror", "alert("} {
			if strings.Contains(stored, bad) {
				t.Errorf("%s.%s still contains %q: %s", tc.collection, tc.field, bad, stored)
			}
		}
		if !strings.Contains(stored, "<p>Fine</p>") {
			t.Errorf("%s.%s lost its ordinary markup: %s", tc.collection, tc.field, stored)
		}
	}
}
