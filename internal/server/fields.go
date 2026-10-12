package server

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/filesystem"
	"github.com/pocketbase/pocketbase/tools/types"
)

const (
	// maxListColumns keeps record tables readable by capping the field count.
	maxListColumns = 8

	// maxRelationOptions caps how many related records are offered in a
	// dropdown, so a large relation target cannot blow up the form.
	maxRelationOptions = 500

	// longTextLimit is where free-text cells get elided.
	longTextLimit = 80
)

// managedFields are owned by PocketBase and are never edited from the panel.
var managedFields = map[string]bool{
	"id":             true,
	"collectionId":   true,
	"collectionName": true,
	"created":        true,
	"updated":        true,
	"tokenKey":       true,
	"expand":         true,
}

// hiddenListFields are excluded from record tables.
var hiddenListFields = map[string]bool{
	"tokenKey": true,
	"password": true,
}

// humanize turns a snake_case / camelCase field name into a readable label.
func humanize(name string) string {
	if name == "" {
		return ""
	}

	var b strings.Builder
	b.WriteString(strings.ToUpper(name[:1]))

	for i := 1; i < len(name); i++ {
		ch := name[i]
		switch {
		case ch == '_' || ch == '-':
			b.WriteString(" ")
		case ch >= 'A' && ch <= 'Z':
			b.WriteString(" ")
			b.WriteByte(ch)
		default:
			b.WriteByte(ch)
		}
	}

	label := b.String()
	if label == "Id" {
		return "ID"
	}
	return label
}

// ---------------------------------------------------------------------------
// Record tables
// ---------------------------------------------------------------------------

// listColumns picks the columns rendered in the record table: the id followed
// by the collection's visible fields.
func listColumns(col *core.Collection) []column {
	cols := make([]column, 0, len(col.Fields)+1)
	cols = append(cols, column{Name: "id", Label: "ID", Type: core.FieldTypeText, Sort: "id"})

	for _, f := range col.Fields {
		name := f.GetName()
		if name == "id" || hiddenListFields[name] || f.GetHidden() {
			continue
		}
		cols = append(cols, column{
			Name:  name,
			Label: humanize(name),
			Type:  f.Type(),
			Sort:  name,
		})
	}

	if len(cols) > maxListColumns {
		cols = cols[:maxListColumns]
	}

	return cols
}

// buildRow renders one record into table cells matching cols.
func buildRow(col *core.Collection, rec *core.Record, cols []column) row {
	cells := make([]cell, 0, len(cols))
	for _, c := range cols {
		cells = append(cells, buildCell(col, rec, c))
	}
	return row{ID: rec.Id, Cells: cells}
}

func buildCell(col *core.Collection, rec *core.Record, c column) cell {
	switch c.Type {
	case core.FieldTypeBool:
		return cell{Kind: "bool", Bool: rec.GetBool(c.Name)}

	case core.FieldTypeNumber:
		raw := rec.GetRaw(c.Name)
		if raw == nil {
			return cell{Kind: "empty"}
		}
		return cell{Kind: "text", Text: strconv.FormatFloat(rec.GetFloat(c.Name), 'f', -1, 64)}

	case core.FieldTypeDate, core.FieldTypeAutodate:
		dt := rec.GetDateTime(c.Name)
		if dt.IsZero() {
			return cell{Kind: "empty"}
		}
		return cell{Kind: "date", Text: dt.Time().Format("2006-01-02 15:04:05")}

	case core.FieldTypeFile:
		files := buildFileRefs(col, rec, c.Name)
		if len(files) == 0 {
			return cell{Kind: "empty"}
		}
		return cell{Kind: "file", Files: files, Text: fmt.Sprintf("%d file(s)", len(files))}

	case core.FieldTypeJSON:
		raw := rec.Get(c.Name)
		if raw == nil {
			return cell{Kind: "empty"}
		}
		pretty := jsonPretty(raw)
		return cell{Kind: "json", Text: truncate(strings.ReplaceAll(pretty, "\n", " "), longTextLimit), Title: pretty}

	case core.FieldTypeSelect, core.FieldTypeRelation:
		values := rec.GetStringSlice(c.Name)
		if len(values) == 0 {
			return cell{Kind: "empty"}
		}
		return cell{Kind: "list", Items: values, Text: truncate(strings.Join(values, ", "), longTextLimit)}

	case core.FieldTypeEmail:
		value := rec.GetString(c.Name)
		if value == "" {
			return cell{Kind: "empty"}
		}
		return cell{Kind: "text", Text: value}

	default:
		value := rec.GetString(c.Name)
		if value == "" {
			// fall back to a raw rendering for exotic field types
			if raw := rec.GetRaw(c.Name); raw != nil {
				value = fmt.Sprintf("%v", raw)
			}
		}
		if value == "" {
			return cell{Kind: "empty"}
		}
		if len([]rune(value)) > longTextLimit {
			return cell{Kind: "text", Text: truncate(value, longTextLimit), Title: value, Truncated: true}
		}
		return cell{Kind: "text", Text: value}
	}
}

// buildFileRefs lists the files stored on a record field.
func buildFileRefs(col *core.Collection, rec *core.Record, field string) []fileRef {
	names := rec.GetStringSlice(field)
	if len(names) == 0 {
		return nil
	}

	refs := make([]fileRef, 0, len(names))
	for _, name := range names {
		refs = append(refs, fileRef{
			Name:    name,
			URL:     fileURL(col, rec.Id, name),
			IsImage: isImage(name),
		})
	}
	return refs
}

// ---------------------------------------------------------------------------
// Search
// ---------------------------------------------------------------------------

// searchableFields returns the field names that the free-text search covers.
func searchableFields(col *core.Collection) []string {
	var names []string

	for _, f := range col.Fields {
		name := f.GetName()
		if hiddenListFields[name] || f.GetHidden() {
			continue
		}

		switch f.Type() {
		case core.FieldTypeText, core.FieldTypeEmail, core.FieldTypeURL,
			core.FieldTypeEditor, core.FieldTypeSelect:
			names = append(names, name)
		}
	}

	return names
}

// searchFilter builds a PocketBase filter expression that matches q against
// every searchable field. Values are always passed as bound parameters.
func searchFilter(col *core.Collection, q string) (string, map[string]any) {
	if strings.TrimSpace(q) == "" {
		return "", nil
	}

	fields := searchableFields(col)
	if len(fields) == 0 {
		return "", nil
	}

	clauses := make([]string, 0, len(fields))
	for _, name := range fields {
		clauses = append(clauses, fmt.Sprintf("%s ~ {:q}", name))
	}

	return "(" + strings.Join(clauses, " || ") + ")", map[string]any{"q": q}
}

// sortExpression validates the requested sort against the collection schema so
// that an arbitrary string can never reach the query builder.
//
// Returns "" when the field is unknown for this collection.
func sortExpression(col *core.Collection, field, dir string) string {
	if field == "" {
		return ""
	}

	// "id" is always present; every other key must be a real field, because
	// collections created through the API do not automatically get the
	// "created"/"updated" autodate fields.
	allowed := map[string]bool{"id": true}
	for _, f := range col.Fields {
		allowed[f.GetName()] = true
	}

	if !allowed[field] {
		return ""
	}

	if dir == "desc" {
		return "-" + field
	}
	return field
}

// defaultSort picks a stable ordering for a collection, preferring the
// creation timestamp when the collection actually has one.
func defaultSort(col *core.Collection) string {
	if col.IsView() {
		return ""
	}
	if col.Fields.GetByName("created") != nil {
		return "-created"
	}
	return "-id"
}

// ---------------------------------------------------------------------------
// Forms
// ---------------------------------------------------------------------------

// buildFormFields turns a collection schema into form inputs, pre-filled from
// rec (which may be a fresh empty record).
func buildFormFields(app core.App, col *core.Collection, rec *core.Record, isNew bool) []formField {
	fields := make([]formField, 0, len(col.Fields))

	for _, f := range col.Fields {
		name := f.GetName()
		if managedFields[name] {
			continue
		}

		ff := formField{
			Name:     name,
			Label:    humanize(name),
			Type:     f.Type(),
			Selected: map[string]bool{},
		}

		switch fld := f.(type) {
		case *core.TextField:
			ff.Input = "text"
			ff.Value = rec.GetString(name)
			ff.Required = fld.Required
			ff.Help = fld.Help

		case *core.EmailField:
			ff.Input = "email"
			ff.Value = rec.GetString(name)
			ff.Required = fld.Required
			ff.Help = fld.Help

		case *core.URLField:
			ff.Input = "url"
			ff.Value = rec.GetString(name)
			ff.Required = fld.Required
			ff.Help = fld.Help

		case *core.NumberField:
			ff.Input = "number"
			ff.Step = "any"
			ff.Required = fld.Required
			ff.Help = fld.Help
			if rec.GetRaw(name) != nil {
				ff.Value = strconv.FormatFloat(rec.GetFloat(name), 'f', -1, 64)
			}

		case *core.BoolField:
			ff.Input = "checkbox"
			ff.Checked = rec.GetBool(name)

		case *core.DateField:
			ff.Input = "datetime-local"
			ff.Required = fld.Required
			ff.Help = fld.Help
			if dt := rec.GetDateTime(name); !dt.IsZero() {
				ff.Value = dt.Time().Format("2006-01-02T15:04")
			}

		case *core.SelectField:
			ff.Input = "select"
			ff.Multiple = fld.MaxSelect > 1
			ff.MaxSelect = fld.MaxSelect
			ff.Required = fld.Required
			ff.Help = fld.Help
			for _, value := range fld.Values {
				ff.Options = append(ff.Options, formOption{Value: value, Label: value})
			}
			for _, value := range rec.GetStringSlice(name) {
				ff.Selected[value] = true
			}

		case *core.RelationField:
			ff.Input = "relation"
			ff.Multiple = fld.MaxSelect > 1
			ff.MaxSelect = fld.MaxSelect
			ff.Required = fld.Required
			ff.Help = fld.Help
			ff.Options = relationOptions(app, fld)
			for _, value := range rec.GetStringSlice(name) {
				ff.Selected[value] = true
			}

		case *core.FileField:
			ff.Input = "file"
			ff.Multiple = fld.MaxSelect > 1
			ff.MaxSelect = fld.MaxSelect
			ff.Required = fld.Required && isNew
			ff.Help = fld.Help
			ff.Files = buildFileRefs(col, rec, name)

		case *core.EditorField:
			ff.Input = "textarea"
			ff.Value = rec.GetString(name)
			ff.Required = fld.Required
			ff.Help = fld.Help

		case *core.JSONField:
			ff.Input = "json"
			ff.Required = fld.Required
			ff.Help = fld.Help
			if raw := rec.GetRaw(name); raw != nil {
				ff.Value = jsonPretty(rec.Get(name))
			}

		case *core.GeoPointField:
			ff.Input = "geopoint"
			ff.Required = fld.Required
			ff.Help = fld.Help
			ff.Value = formatGeoPoint(rec.GetGeoPoint(name))

		case *core.PasswordField:
			ff.Input = "password"
			ff.Required = isNew
			if isNew {
				ff.Help = "Choose a password for the new account."
			} else {
				ff.Help = "Leave blank to keep the current password unchanged."
			}

		case *core.AutodateField:
			ff.Input = "readonly"
			ff.ReadOnly = true
			ff.Value = formatTime(rec.GetDateTime(name))
			ff.Help = "Managed automatically by PocketBase."

		default:
			ff.Input = "text"
			ff.Value = rec.GetString(name)
		}

		fields = append(fields, ff)
	}

	return fields
}

// relationOptions loads the selectable records of a relation target.
func relationOptions(app core.App, fld *core.RelationField) []formOption {
	related, err := app.FindCollectionByNameOrId(fld.CollectionId)
	if err != nil {
		return nil
	}

	records, err := app.FindAllRecords(related)
	if err != nil {
		return nil
	}

	if len(records) > maxRelationOptions {
		records = records[:maxRelationOptions]
	}

	options := make([]formOption, 0, len(records))
	for _, rec := range records {
		options = append(options, formOption{Value: rec.Id, Label: recordLabel(related, rec)})
	}

	return options
}

// recordLabel produces a human friendly label for a related record.
func recordLabel(col *core.Collection, rec *core.Record) string {
	// Prefer a text field explicitly flagged as presentable.
	for _, f := range col.Fields {
		tf, ok := f.(*core.TextField)
		if !ok || !tf.Presentable {
			continue
		}
		if value := rec.GetString(tf.Name); value != "" {
			return value
		}
	}

	for _, candidate := range []string{"name", "title", "label", "slug", "username", "subject"} {
		if value := rec.GetString(candidate); value != "" {
			return value
		}
	}

	if col.IsAuth() {
		if email := rec.Email(); email != "" {
			return email
		}
	}

	return rec.Id
}

func formatGeoPoint(point types.GeoPoint) string {
	if point.Lon == 0 && point.Lat == 0 {
		return ""
	}
	return fmt.Sprintf("%v,%v", point.Lon, point.Lat)
}

// ---------------------------------------------------------------------------
// Form submission
// ---------------------------------------------------------------------------

// applyForm copies submitted values onto rec.
//
// Values are handed to Record.Set so that PocketBase's own field PrepareValue
// implementations perform the string -> typed conversion (numbers, dates,
// selects, relations).
func (s *Server) applyForm(c *gin.Context, col *core.Collection, rec *core.Record) error {
	for _, f := range col.Fields {
		name := f.GetName()
		if managedFields[name] {
			continue
		}

		switch fld := f.(type) {
		case *core.AutodateField:
			// managed by PocketBase

		case *core.BoolField:
			rec.Set(name, c.PostForm(name) != "")

		case *core.FileField:
			if err := s.applyFileField(c, rec, fld); err != nil {
				return err
			}

		case *core.SelectField:
			values := c.PostFormArray(name)
			if fld.MaxSelect <= 1 {
				rec.Set(name, firstOrEmpty(values))
				continue
			}
			rec.Set(name, values)

		case *core.RelationField:
			values := c.PostFormArray(name)
			if fld.MaxSelect <= 1 {
				rec.Set(name, firstOrEmpty(values))
				continue
			}
			rec.Set(name, values)

		case *core.PasswordField:
			if password := c.PostForm(name); password != "" {
				rec.SetPassword(password)
			}

		case *core.NumberField:
			raw := strings.TrimSpace(c.PostForm(name))
			if raw == "" {
				rec.Set(name, nil)
				continue
			}
			rec.Set(name, raw)

		default:
			rec.Set(name, strings.TrimSpace(c.PostForm(name)))
		}
	}

	return nil
}

// applyFileField appends newly uploaded files and removes the ones the operator
// ticked for deletion. PocketBase's "+" and "-" field modifiers implement both
// operations without touching the untouched files.
func (s *Server) applyFileField(c *gin.Context, rec *core.Record, fld *core.FileField) error {
	for _, name := range c.PostFormArray(fld.Name + "-") {
		if name == "" {
			continue
		}
		rec.Set(fld.Name+"-", name)
	}

	uploads, err := collectUploads(c, fld.Name)
	if err != nil {
		return err
	}
	if len(uploads) == 0 {
		return nil
	}

	if fld.MaxSelect > 1 {
		rec.Set(fld.Name+"+", uploads)
		return nil
	}

	rec.Set(fld.Name+"+", uploads[0])

	return nil
}

// collectUploads converts the multipart files submitted for a field.
func collectUploads(c *gin.Context, field string) ([]*filesystem.File, error) {
	if !strings.HasPrefix(c.ContentType(), "multipart/form-data") {
		return nil, nil
	}

	form, err := c.MultipartForm()
	if err != nil {
		return nil, fmt.Errorf("read uploaded files: %w", err)
	}
	if form == nil {
		return nil, nil
	}

	headers := form.File[field]
	uploads := make([]*filesystem.File, 0, len(headers))

	for _, header := range headers {
		if header.Size == 0 {
			continue
		}
		file, err := filesystem.NewFileFromMultipart(header)
		if err != nil {
			return nil, fmt.Errorf("read upload %q: %w", header.Filename, err)
		}
		uploads = append(uploads, file)
	}

	return uploads, nil
}

func firstOrEmpty(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return values[0]
}
