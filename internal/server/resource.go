package server

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/pocketbase/pocketbase/core"
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

	// DerivedFields are computed by the server and must never be read back from a
	// form. They are excluded from the editor and skipped when applying a
	// submission, so a crafted post cannot rewrite a materialised path.
	DerivedFields []string

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
	perPage := atoiDefault(c.Query("perPage"), s.cfg.PerPage)
	if perPage < 1 || perPage > 500 {
		perPage = s.cfg.PerPage
	}

	query := strings.TrimSpace(c.Query("q"))
	filter, params := searchFilter(col, query)
	if r.Filter != "" {
		if filter != "" {
			filter = "(" + r.Filter + ") && (" + filter + ")"
		} else {
			filter = r.Filter
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

	cols := listColumns(col)
	rows := make([]row, 0, len(records))
	for _, rec := range records {
		rows = append(rows, buildRow(col, rec, cols))
	}

	actor := s.actor(c)

	pageVM := resourceListPage{
		base:         s.newBase(c, r.Title, r.Active),
		Resource:     r,
		Collection:   col,
		Columns:      cols,
		Rows:         rows,
		Query:        query,
		Sort:         c.Query("sort"),
		Dir:          c.Query("dir"),
		QueryPrefix:  buildQueryPrefix(query, perPage),
		CanCreate:    actor.Can(r.CreatePerm),
		CanDelete:    actor.Can(r.DeletePerm),
		CanUpdate:    actor.Can(r.UpdatePerm),
		Pagination:   buildPagination(page, perPage, total),
		PerPageItems: perPageOptions(perPage),
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
	c.Redirect(http.StatusSeeOther, r.BasePath)
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
	c.Redirect(http.StatusSeeOther, r.BasePath)
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
			c.Redirect(http.StatusSeeOther, r.BasePath)
			return
		}
	}

	if err := s.app.Delete(rec); err != nil {
		s.setFlash(c, "danger", "Could not delete the "+r.Singular+": "+err.Error())
		c.Redirect(http.StatusSeeOther, r.BasePath)
		return
	}

	if r.AfterSave != nil {
		r.AfterSave(s, rec, false)
	}

	s.setFlash(c, "success", r.Singular+" deleted.")
	c.Redirect(http.StatusSeeOther, r.BasePath)
}

// renderResourceForm renders the shared create/edit form.
func (s *Server) renderResourceForm(c *gin.Context, r resource, col *core.Collection,
	rec *core.Record, isNew bool, errs []string) {

	action := r.BasePath + "/new"
	if !isNew {
		action = r.BasePath + "/" + rec.Id
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
		CancelURL:  r.BasePath,
		Fields:     buildFormFields(s.app, col, rec, isNew, r.derivedSet()),
		Errors:     errs,
		RecordID:   recID(rec),
		CanDelete:  !isNew && s.actor(c).Can(r.DeletePerm),
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

// recID returns a record id, or "" for a nil record.
func recID(rec *core.Record) string {
	if rec == nil {
		return ""
	}
	return rec.Id
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
