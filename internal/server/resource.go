package server

import (
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/pocketbase/pocketbase/core"

	"pocketadmin/internal/rbac"
	"pocketadmin/internal/tree"
)

// resource describes a managed collection: where it lives, which permission
// guards each operation, and any behaviour beyond plain CRUD.
//
// The list and form machinery is generic (see listColumns, buildFormFields and
// applyForm), so a module screen is mostly a declaration. Empty permission
// strings mean the operation is not offered at all, which is how a read-only
// collection such as login_logs is expressed.
type resource struct {
	// Collection is the PocketBase collection name.
	Collection string

	// BasePath is the URL prefix, for example "/identity/accounts".
	BasePath string

	// Title is the plural label; Singular labels a single record.
	Title    string
	Singular string

	// Active highlights the sidebar entry.
	Active string

	// Permissions guarding each operation.
	ListPerm   string
	CreatePerm string
	UpdatePerm string
	DeletePerm string

	// Order is the default sort, applied when the request specifies none.
	Order string

	// Filter narrows every query, typically to exclude soft-deleted rows.
	Filter string

	// ParentField marks a tree resource; re-parenting maintains the materialised
	// path.
	ParentField string

	// TreeView indents a row by its depth, turning a flat list into a visible
	// hierarchy. The depth comes from a `level` field when the collection has one
	// and is otherwise walked up the ParentField chain.
	TreeView bool

	// TreePathField names the materialised path column, when the hierarchy has
	// one. It must be declared rather than inferred from a field called "path":
	// menus carry a `path` that is a URL, and treating that as a tree path
	// silently disabled the ordering.
	TreePathField string

	// ChildOf names a relation that scopes this resource to a parent record given
	// in the query string, as `?parent=<id>`. It is how a sub-list such as a
	// dictionary's items is expressed without a second resource layer: the parent
	// narrows the list, seeds the create form and is carried through the form's
	// action and cancel links.
	ChildOf string

	// DerivedFields are computed by the server and must never be read back from a
	// form. They are excluded from the editor and skipped when applying a
	// submission, so a crafted post cannot rewrite a materialised path.
	DerivedFields []string

	// CountOf adds a read-only column counting records in another collection that
	// point at this one, such as the staff in a department.
	CountOf *countSpec

	// HasDetail marks a resource with its own detail screen at BasePath/:id, so
	// the list offers a view action next to edit and delete.
	HasDetail bool

	// RowActionPath, when set, adds a per-row link to BasePath+RowActionPath+<id>.
	// It carries a sub-list such as a dictionary's entries.
	RowActionPath  string
	RowActionLabel string
	RowActionIcon  string

	// LabelField is the field used when this collection is shown as a relation
	// option. Empty falls back to the collection's own presentable field.
	LabelField string

	// BeforeSave runs inside the create/update path, after the form has been
	// applied and before the record is persisted.
	BeforeSave func(s *Server, rec *core.Record, isNew bool) error

	// AfterSave runs once the record is persisted, for side effects such as
	// invalidating a cache.
	AfterSave func(s *Server, rec *core.Record, isNew bool)

	// BeforeDelete runs before a delete and may refuse it, for example when the
	// row is still referenced. Returning an error aborts with a message.
	BeforeDelete func(s *Server, rec *core.Record) error

	// AfterDelete runs once the row is gone, for side effects that must not
	// happen while the row still exists -- removing an object whose last
	// reference just disappeared, for example. It cannot refuse the delete.
	AfterDelete func(s *Server, rec *core.Record)
}

// parentID returns the parent record id this request is scoped to, or "".
func (r resource) parentID(c *gin.Context) string {
	if r.ChildOf == "" {
		return ""
	}
	return strings.TrimSpace(c.Query("parent"))
}

// parentFilter narrows a query to the parent record when one is given.
func (r resource) parentFilter(c *gin.Context) (string, map[string]any) {
	id := r.parentID(c)
	if id == "" {
		return "", nil
	}
	return r.ChildOf + " = {:childParent}", map[string]any{"childParent": id}
}

// listPath is the resource's list URL, carrying the parent when scoped.
func (r resource) listPath(c *gin.Context) string {
	if id := r.parentID(c); id != "" {
		return r.BasePath + "?parent=" + url.QueryEscape(id)
	}
	return r.BasePath
}

// offers reports whether an operation is both registered and permitted.
//
// An empty permission means the operation is not offered at all, and Can("") is
// true by design, so the two must be checked together: a read-only resource would
// otherwise render a delete control whose route does not exist.
func offers(perm string, actor *rbac.Actor) bool {
	return perm != "" && actor.Can(perm)
}

// derivedSet returns the resource's derived fields as a lookup set.
func (r resource) derivedSet() map[string]bool {
	if len(r.DerivedFields) == 0 {
		return nil
	}
	out := make(map[string]bool, len(r.DerivedFields))
	for _, f := range r.DerivedFields {
		out[f] = true
	}
	return out
}

// resourceCollection resolves the resource's collection, writing a 500 when the
// schema is missing. A missing collection means a migration did not run, not a
// user error.
func (s *Server) resourceCollection(c *gin.Context, r resource) (*core.Collection, bool) {
	col, err := s.app.FindCollectionByNameOrId(r.Collection)
	if err != nil || col == nil {
		s.renderErrorPage(c, http.StatusInternalServerError, "Schema missing",
			fmt.Sprintf("The %q collection does not exist. Run the migrations and try again.", r.Collection))
		return nil, false
	}
	return col, true
}

// resourceList renders a paginated, searchable list of a resource.
func (s *Server) resourceList(c *gin.Context, r resource) {
	col, ok := s.resourceCollection(c, r)
	if !ok {
		return
	}

	page := atoiDefault(c.Query("page"), 1)
	if page < 1 {
		page = 1
	}
	// The account's stored preference is the default; an explicit perPage in the
	// query still wins, so a one-off change does not rewrite the setting.
	perPage := atoiDefault(c.Query("perPage"), s.currentPageSize(c))
	if perPage < 1 || perPage > 500 {
		perPage = s.cfg.PerPage
	}

	actor := s.actor(c)

	query := strings.TrimSpace(c.Query("q"))
	filter, params := searchFilter(col, query)
	if r.Filter != "" {
		filter = andFilter(filter, r.Filter)
	}

	if pf, pp := r.parentFilter(c); pf != "" {
		filter = andFilter(filter, pf)
		if params == nil {
			params = make(map[string]any, len(pp))
		}
		for k, v := range pp {
			params[k] = v
		}
	}

	// Row-level data scope narrows what the actor may see within the capability
	// it was already granted.
	scope := rbac.ScopeFor(actor, col)
	if scope.DenyAll {
		s.render(c, http.StatusOK, "resource_list.html", resourceListPage{
			base:          s.newBase(c, r.Title, r.Active),
			Resource:      r,
			Collection:    col,
			Columns:       listColumns(col),
			Query:         query,
			QueryPrefix:   buildQueryPrefix(query, perPage),
			TreeView:      r.TreeView,
			ListPath:      r.listPath(c),
			ParentID:      r.parentID(c),
			ParentLabel:   s.parentLabel(c, r),
			HasDetail:     r.HasDetail,
			RowActionPath: r.RowActionPath,
			RowActionIcon: r.RowActionIcon,
			RowActionText: r.RowActionLabel,
			CanCreate:     offers(r.CreatePerm, actor),
			CanDelete:     offers(r.DeletePerm, actor),
			CanUpdate:     offers(r.UpdatePerm, actor),
			PerPageItems:  perPageOptions(perPage),
			ScopeNote:     scope.Description(),
		})
		return
	}
	if scope.Filter != "" {
		filter = andFilter(filter, scope.Filter)
		if params == nil {
			params = make(map[string]any, len(scope.Params))
		}
		for k, v := range scope.Params {
			params[k] = v
		}
	}

	sort := sortExpression(col, c.Query("sort"), c.Query("dir"))
	if sort == "" {
		sort = r.Order
	}
	if sort == "" {
		sort = defaultSort(col)
	}

	records, err := s.app.FindRecordsByFilter(col, filter, sort, perPage, (page-1)*perPage, params)
	if err != nil {
		s.renderErrorPage(c, http.StatusInternalServerError, "Could not load records", err.Error())
		return
	}

	total, err := s.countRecords(col, filter, params)
	if err != nil {
		s.renderErrorPage(c, http.StatusInternalServerError, "Could not count records", err.Error())
		return
	}

	if r.TreeView {
		// A hierarchy that sorts flat interleaves parents and children, so the
		// indent would show depths that do not nest. A materialised path already
		// groups correctly; without one the records are reordered parents-first.
		records = orderAsTree(records, r.ParentField, r.TreePathField)
	}

	cols := listColumns(col)
	if r.CountOf != nil {
		cols = append(cols, countColumn(r.CountOf))
	}
	if r.TreeView {
		// The indent marks the first column, so a tree has to lead with the name
		// rather than with the id, or the hierarchy is drawn down the wrong column.
		cols = leadWithLabel(cols, col)
	}

	rows := make([]row, 0, len(records))
	for _, rec := range records {
		built := buildRow(col, rec, cols)

		if r.CountOf != nil {
			n, err := s.countRelated(r.CountOf, rec.Id)
			if err != nil {
				slog.Warn("could not count related records", "collection", r.CountOf.Collection, "error", err)
			}
			if len(built.Cells) > 0 {
				built.Cells[len(built.Cells)-1] = cell{
					Kind: "text",
					Text: fmt.Sprint(n),
				}
			}
		}

		if r.TreeView {
			built.Indent = treeDepth(s.app, col, rec, r.ParentField)
		}
		rows = append(rows, built)
	}

	pageVM := resourceListPage{
		base:          s.newBase(c, r.Title, r.Active),
		Resource:      r,
		Collection:    col,
		Columns:       cols,
		Rows:          rows,
		Query:         query,
		Sort:          c.Query("sort"),
		Dir:           c.Query("dir"),
		QueryPrefix:   buildQueryPrefix(query, perPage),
		TreeView:      r.TreeView,
		ListPath:      r.listPath(c),
		ParentID:      r.parentID(c),
		ParentLabel:   s.parentLabel(c, r),
		HasDetail:     r.HasDetail,
		RowActionPath: r.RowActionPath,
		RowActionIcon: r.RowActionIcon,
		RowActionText: r.RowActionLabel,
		CanCreate:     offers(r.CreatePerm, actor),
		CanDelete:     offers(r.DeletePerm, actor),
		CanUpdate:     offers(r.UpdatePerm, actor),
		Pagination:    buildPagination(page, perPage, total),
		PerPageItems:  perPageOptions(perPage),
		ScopeNote:     scope.Description(),
	}

	s.render(c, http.StatusOK, "resource_list.html", pageVM)
}

// resourceNew renders the create form.
func (s *Server) resourceNew(c *gin.Context, r resource) {
	col, ok := s.resourceCollection(c, r)
	if !ok {
		return
	}
	s.renderResourceForm(c, r, col, nil, true, nil)
}

// resourceCreate applies a submitted create form.
func (s *Server) resourceCreate(c *gin.Context, r resource) {
	col, ok := s.resourceCollection(c, r)
	if !ok {
		return
	}

	rec := core.NewRecord(col)
	if err := s.applyForm(c, col, rec, r.derivedSet()); err != nil {
		s.renderResourceForm(c, r, col, rec, true, validationMessages(err))
		return
	}

	// A child resource takes its parent from the query string, so the relation is
	// never a free choice the form could get wrong.
	if id := r.parentID(c); id != "" {
		rec.Set(r.ChildOf, id)
	}

	if r.BeforeSave != nil {
		if err := r.BeforeSave(s, rec, true); err != nil {
			s.renderResourceForm(c, r, col, rec, true, validationMessages(err))
			return
		}
	}

	if err := s.app.Save(rec); err != nil {
		s.renderResourceForm(c, r, col, rec, true, validationMessages(err))
		return
	}

	if r.AfterSave != nil {
		r.AfterSave(s, rec, true)
	}

	s.setFlash(c, "success", r.Singular+" created.")
	c.Redirect(http.StatusSeeOther, r.listPath(c))
}

// resourceEdit renders the update form.
func (s *Server) resourceEdit(c *gin.Context, r resource) {
	col, ok := s.resourceCollection(c, r)
	if !ok {
		return
	}

	rec, ok := s.findRecord(c, col)
	if !ok {
		return
	}
	s.renderResourceForm(c, r, col, rec, false, nil)
}

// resourceUpdate applies a submitted update form.
func (s *Server) resourceUpdate(c *gin.Context, r resource) {
	col, ok := s.resourceCollection(c, r)
	if !ok {
		return
	}

	rec, ok := s.findRecord(c, col)
	if !ok {
		return
	}

	if err := s.applyForm(c, col, rec, r.derivedSet()); err != nil {
		s.renderResourceForm(c, r, col, rec, false, validationMessages(err))
		return
	}

	if r.BeforeSave != nil {
		if err := r.BeforeSave(s, rec, false); err != nil {
			s.renderResourceForm(c, r, col, rec, false, validationMessages(err))
			return
		}
	}

	if err := s.app.Save(rec); err != nil {
		s.renderResourceForm(c, r, col, rec, false, validationMessages(err))
		return
	}

	if r.AfterSave != nil {
		r.AfterSave(s, rec, false)
	}

	s.setFlash(c, "success", r.Singular+" updated.")
	c.Redirect(http.StatusSeeOther, r.listPath(c))
}

// resourceDelete removes a record.
//
// The delete is a hard delete; collections that need history carry a deletedAt
// field and are soft-deleted by their module's own handler instead.
func (s *Server) resourceDelete(c *gin.Context, r resource) {
	col, ok := s.resourceCollection(c, r)
	if !ok {
		return
	}

	rec, ok := s.findRecord(c, col)
	if !ok {
		return
	}

	if r.BeforeDelete != nil {
		if err := r.BeforeDelete(s, rec); err != nil {
			s.setFlash(c, "danger", err.Error())
			c.Redirect(http.StatusSeeOther, r.listPath(c))
			return
		}
	}

	if err := s.app.Delete(rec); err != nil {
		s.setFlash(c, "danger", "Could not delete the "+r.Singular+": "+err.Error())
		c.Redirect(http.StatusSeeOther, r.listPath(c))
		return
	}

	if r.AfterDelete != nil {
		r.AfterDelete(s, rec)
	}

	s.setFlash(c, "success", r.Singular+" deleted.")
	c.Redirect(http.StatusSeeOther, r.listPath(c))
}

// renderResourceForm renders the shared create/edit form.
func (s *Server) renderResourceForm(c *gin.Context, r resource, col *core.Collection,
	rec *core.Record, isNew bool, errs []string) {

	action := r.BasePath + "/new"
	if !isNew {
		action = r.BasePath + "/" + rec.Id
	}
	if id := r.parentID(c); id != "" {
		action += "?parent=" + url.QueryEscape(id)
	}

	title := "New " + strings.ToLower(r.Singular)
	if !isNew {
		title = "Edit " + strings.ToLower(r.Singular)
	}

	s.render(c, http.StatusOK, "resource_form.html", resourceFormPage{
		base:       s.newBase(c, title, r.Active),
		Resource:   r,
		IsNew:      isNew,
		Action:     action,
		CancelURL:  r.listPath(c),
		Fields:     buildFormFields(s.app, col, rec, isNew, r.derivedSet()),
		Errors:     errs,
		RecordID:   recID(rec),
		CanDelete:  !isNew && offers(r.DeletePerm, s.actor(c)),
		DeletePath: r.BasePath + "/" + recID(rec) + "/delete",
	})
}

// resourceRoutes mounts the standard CRUD routes for a resource.
//
// Read routes are registered with safe() and write routes with guarded(), so the
// startup self-check can prove nothing was left unprotected. Operations whose
// permission is empty are not registered at all.
func (s *Server) resourceRoutes(panel *gin.RouterGroup, r resource) {
	if r.ListPerm != "" {
		s.readGuarded(panel, http.MethodGet, r.BasePath, r.ListPerm,
			func(c *gin.Context) { s.resourceList(c, r) })
	}
	if r.CreatePerm != "" {
		s.readGuarded(panel, http.MethodGet, r.BasePath+"/new", r.CreatePerm,
			func(c *gin.Context) { s.resourceNew(c, r) })
		s.guarded(panel, http.MethodPost, r.BasePath, r.CreatePerm,
			func(c *gin.Context) { s.resourceCreate(c, r) })
	}
	if r.UpdatePerm != "" {
		s.readGuarded(panel, http.MethodGet, r.BasePath+"/:id/edit", r.UpdatePerm,
			func(c *gin.Context) { s.resourceEdit(c, r) })
		s.guarded(panel, http.MethodPost, r.BasePath+"/:id", r.UpdatePerm,
			func(c *gin.Context) { s.resourceUpdate(c, r) })
	}
	if r.DeletePerm != "" {
		s.guarded(panel, http.MethodPost, r.BasePath+"/:id/delete", r.DeletePerm,
			func(c *gin.Context) { s.resourceDelete(c, r) })
	}
}

// countSpec describes a related-record count shown as a list column.
type countSpec struct {
	// Collection holds the records to count.
	Collection string

	// Field is the relation on that collection pointing at the listed row.
	Field string

	// Label heads the column.
	Label string

	// Filter narrows what counts, for example to active staff only.
	Filter string
}

// countRelated counts the records that point at one row.
//
// This is a query per row rather than one grouped query. A list is capped at a
// few hundred rows and the relation column is indexed, so the round trips are
// cheap; a grouped query would mean hand-written SQL in the generic list path,
// which is a worse trade for a read-only badge.
func (s *Server) countRelated(spec *countSpec, id string) (int64, error) {
	if spec == nil || id == "" {
		return 0, nil
	}

	filter := spec.Field + " = {:id}"
	params := map[string]any{"id": id}
	if spec.Filter != "" {
		filter = "(" + spec.Filter + ") && " + filter
	}

	col, err := s.app.FindCollectionByNameOrId(spec.Collection)
	if err != nil || col == nil {
		return 0, nil
	}
	return s.countRecords(col, filter, params)
}

// countColumn builds the extra column for a resource that declares a count.
func countColumn(spec *countSpec) column {
	return column{
		Name:  "relatedCount",
		Label: spec.Label,
		Type:  core.FieldTypeNumber,
		Sort:  "",
	}
}

// treeNode is the minimal shape the ordering algorithm needs, so the algorithm
// itself does not depend on PocketBase.
type treeNode struct {
	ID     string
	Parent string
}

// orderAsTree reorders records so every parent precedes its children.
//
// A materialised path already sorts into tree order, so this only fills the gap
// for a hierarchy that has no path column.
func orderAsTree(records []*core.Record, parentField, treePathField string) []*core.Record {
	if parentField == "" || treePathField != "" || len(records) < 2 {
		return records
	}

	nodes := make([]treeNode, 0, len(records))
	for _, rec := range records {
		nodes = append(nodes, treeNode{ID: rec.Id, Parent: rec.GetString(parentField)})
	}

	order := orderTreeNodes(nodes)
	out := make([]*core.Record, 0, len(order))
	for _, i := range order {
		out = append(out, records[i])
	}
	return out
}

// orderTreeNodes returns the indices of nodes in parents-first order.
//
// A node whose parent is absent from the set is treated as a root rather than
// dropped, so paging or a filter never hides a row. A cycle is broken rather
// than followed, and anything left unwalked is appended so no record is lost.
func orderTreeNodes(nodes []treeNode) []int {
	index := make(map[string]int, len(nodes))
	for i, n := range nodes {
		index[n.ID] = i
	}

	children := make(map[string][]int, len(nodes))
	var roots []int
	for i, n := range nodes {
		if n.Parent == "" {
			roots = append(roots, i)
			continue
		}
		if _, present := index[n.Parent]; !present {
			roots = append(roots, i)
			continue
		}
		children[n.Parent] = append(children[n.Parent], i)
	}

	order := make([]int, 0, len(nodes))
	visited := make([]bool, len(nodes))

	var walk func(list []int, depth int)
	walk = func(list []int, depth int) {
		if depth > maxTreeDepth {
			return
		}
		for _, i := range list {
			if visited[i] {
				continue
			}
			visited[i] = true
			order = append(order, i)
			walk(children[nodes[i].ID], depth+1)
		}
	}
	walk(roots, 0)

	for i := range nodes {
		if !visited[i] {
			order = append(order, i)
		}
	}
	return order
}

// leadWithLabel moves the column that names a record to the front.
//
// It prefers a field flagged presentable, then the usual naming fields, and
// leaves the order untouched when none is found.
func leadWithLabel(cols []column, col *core.Collection) []column {
	label := ""
	for _, f := range col.Fields {
		tf, ok := f.(*core.TextField)
		if !ok || !tf.Presentable {
			continue
		}
		label = tf.Name
		break
	}
	if label == "" {
		for _, candidate := range []string{"name", "title", "label", "code", "email"} {
			if col.Fields.GetByName(candidate) != nil {
				label = candidate
				break
			}
		}
	}
	if label == "" {
		return cols
	}

	idx := -1
	for i, c := range cols {
		if c.Name == label {
			idx = i
			break
		}
	}
	if idx <= 0 {
		return cols
	}

	reordered := make([]column, 0, len(cols))
	reordered = append(reordered, cols[idx])
	reordered = append(reordered, cols[:idx]...)
	reordered = append(reordered, cols[idx+1:]...)
	return reordered
}

// maxTreeDepth bounds a depth walk so a parent cycle cannot hang a request.
const maxTreeDepth = 32

// treeDepth returns how far a record sits below the root of its hierarchy.
func treeDepth(app core.App, col *core.Collection, rec *core.Record, parentField string) int {
	return tree.Depth(app, col, rec, parentField, "level")
}

// parentLabel resolves a readable name for the record this sub-list belongs to,
// so the screen can say what it is showing rather than only an id.
func (s *Server) parentLabel(c *gin.Context, r resource) string {
	id := r.parentID(c)
	if id == "" {
		return ""
	}

	col := s.childParentCollection(r)
	if col == nil {
		return ""
	}

	rec, err := s.app.FindRecordById(col, id)
	if err != nil || rec == nil {
		return ""
	}
	return recordLabel(col, rec)
}

// childParentCollection resolves the collection a ChildOf relation points at.
func (s *Server) childParentCollection(r resource) *core.Collection {
	col, err := s.app.FindCollectionByNameOrId(r.Collection)
	if err != nil || col == nil {
		return nil
	}

	fld, ok := col.Fields.GetByName(r.ChildOf).(*core.RelationField)
	if !ok || fld.CollectionId == "" {
		return nil
	}

	parent, err := s.app.FindCollectionByNameOrId(fld.CollectionId)
	if err != nil {
		return nil
	}
	return parent
}

// recID returns a record id, or "" for a nil record.
func recID(rec *core.Record) string {
	if rec == nil {
		return ""
	}
	return rec.Id
}

// andFilter combines two PocketBase filter fragments, tolerating either being
// empty so callers can chain conditions without checking.
func andFilter(a, b string) string {
	switch {
	case a == "":
		return b
	case b == "":
		return a
	default:
		return "(" + a + ") && (" + b + ")"
	}
}

// buildQueryPrefix carries the filters that must survive a sort or page change.
func buildQueryPrefix(query string, perPage int) string {
	parts := make([]string, 0, 2)
	if query != "" {
		parts = append(parts, "q="+url.QueryEscape(query))
	}
	parts = append(parts, fmt.Sprintf("perPage=%d", perPage))
	return strings.Join(parts, "&")
}

// buildPagination assembles the pagination view model.
func buildPagination(page, perPage int, total int64) pagination {
	totalPages := 0
	if perPage > 0 {
		totalPages = int((total + int64(perPage) - 1) / int64(perPage))
	}

	p := pagination{
		Page:       page,
		PerPage:    perPage,
		Total:      total,
		TotalPages: totalPages,
		HasPrev:    page > 1,
		HasNext:    page < totalPages,
	}

	if total > 0 {
		p.From = int64((page-1)*perPage) + 1
		p.To = p.From + int64(perPage) - 1
		if p.To > total {
			p.To = total
		}
	}

	p.Pages = pageWindow(page, totalPages)
	return p
}
