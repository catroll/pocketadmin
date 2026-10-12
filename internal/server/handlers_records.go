package server

import (
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/search"
)

const (
	// maxPerPage caps the page size a client may request.
	maxPerPage = 200
)

// handleRecordList renders a paginated, searchable record table.
func (s *Server) handleRecordList(c *gin.Context) {
	col, ok := s.findCollection(c)
	if !ok {
		return
	}

	perPage := atoiDefault(c.Query("perPage"), s.cfg.PerPage)
	if perPage < 1 || perPage > maxPerPage {
		perPage = s.cfg.PerPage
	}

	page := atoiDefault(c.Query("page"), 1)
	if page < 1 {
		page = 1
	}

	query := strings.TrimSpace(c.Query("q"))

	dir := c.Query("dir")
	if dir != "desc" {
		dir = "asc"
	}

	sortField := c.Query("sort")
	sortExpr := sortExpression(col, sortField, dir)
	if sortExpr == "" {
		// Fall back to a stable default and reflect it back into the UI.
		sortExpr = defaultSort(col)
		switch {
		case sortExpr == "":
			sortField = ""
		case strings.HasPrefix(sortExpr, "-"):
			sortField, dir = sortExpr[1:], "desc"
		default:
			sortField, dir = sortExpr, "asc"
		}
	}

	filter, params := searchFilter(col, query)

	offset := (page - 1) * perPage
	records, err := s.app.FindRecordsByFilter(col, filter, sortExpr, perPage, offset, dbx.Params(params))
	if err != nil {
		s.renderErrorPage(c, http.StatusBadRequest, "Could not list records", err.Error())
		return
	}

	total, err := s.countRecords(col, filter, params)
	if err != nil {
		// Fall back to a lower bound so the table still renders.
		total = int64(offset + len(records))
	}

	columns := listColumns(col)
	rows := make([]row, 0, len(records))
	for _, rec := range records {
		rows = append(rows, buildRow(col, rec, columns))
	}

	totalPages := int((total + int64(perPage) - 1) / int64(perPage))

	from, to := int64(0), int64(0)
	if len(records) > 0 {
		from = int64(offset) + 1
		to = int64(offset) + int64(len(records))
	}

	nextDir := "desc"
	if dir == "desc" {
		nextDir = "asc"
	}

	// Only q and perPage live in the prefix; sort/dir/page are appended by the
	// template so that url.Values.Get always sees a single value per key.
	prefix := url.Values{}
	if query != "" {
		prefix.Set("q", query)
	}
	prefix.Set("perPage", strconv.Itoa(perPage))

	pageData := recordsPage{
		base:           s.newBase(c, fmt.Sprintf("%s · Records", col.Name), "collections"),
		Collection:     col,
		Columns:        columns,
		Rows:           rows,
		Query:          query,
		Sort:           sortField,
		Dir:            dir,
		NextDir:        nextDir,
		QueryPrefix:    prefix.Encode(),
		PerPageOptions: perPageOptions(perPage),
		Pagination: pagination{
			Page:       page,
			PerPage:    perPage,
			Total:      total,
			TotalPages: totalPages,
			HasPrev:    page > 1,
			HasNext:    page < totalPages,
			From:       from,
			To:         to,
			Pages:      pageWindow(page, totalPages),
		},
	}
	pageData.CurrentCollection = col.Name

	s.render(c, http.StatusOK, "records.html", pageData)
}

// handleRecordNew renders an empty create form.
func (s *Server) handleRecordNew(c *gin.Context) {
	col, ok := s.findCollection(c)
	if !ok {
		return
	}
	if !s.requireWritableCollection(c, col) {
		return
	}

	rec := core.NewRecord(col)
	if col.IsAuth() {
		rec.RefreshTokenKey()
	}

	s.renderForm(c, http.StatusOK, col, rec, true, nil)
}

// handleRecordCreate validates and stores a new record.
func (s *Server) handleRecordCreate(c *gin.Context) {
	col, ok := s.findCollection(c)
	if !ok {
		return
	}
	if !s.requireWritableCollection(c, col) {
		return
	}

	rec := core.NewRecord(col)
	if col.IsAuth() {
		rec.RefreshTokenKey()
	}

	if err := s.applyForm(c, col, rec); err != nil {
		s.renderForm(c, http.StatusBadRequest, col, rec, true, []string{err.Error()})
		return
	}

	if err := s.app.Save(rec); err != nil {
		s.renderForm(c, http.StatusBadRequest, col, rec, true, validationMessages(err))
		return
	}

	s.setFlash(c, "success", fmt.Sprintf("Record created in %q.", col.Name))
	c.Redirect(http.StatusSeeOther, listURL(col))
}

// handleRecordEdit renders the edit form for an existing record.
func (s *Server) handleRecordEdit(c *gin.Context) {
	col, ok := s.findCollection(c)
	if !ok {
		return
	}

	rec, ok := s.findRecord(c, col)
	if !ok {
		return
	}

	s.renderForm(c, http.StatusOK, col, rec, false, nil)
}

// handleRecordUpdate validates and stores changes to an existing record.
func (s *Server) handleRecordUpdate(c *gin.Context) {
	col, ok := s.findCollection(c)
	if !ok {
		return
	}
	if !s.requireWritableCollection(c, col) {
		return
	}

	rec, ok := s.findRecord(c, col)
	if !ok {
		return
	}

	if err := s.applyForm(c, col, rec); err != nil {
		s.renderForm(c, http.StatusBadRequest, col, rec, false, []string{err.Error()})
		return
	}

	if err := s.app.Save(rec); err != nil {
		s.renderForm(c, http.StatusBadRequest, col, rec, false, validationMessages(err))
		return
	}

	s.setFlash(c, "success", "Record updated.")
	c.Redirect(http.StatusSeeOther, listURL(col))
}

// handleRecordDelete removes a record.
func (s *Server) handleRecordDelete(c *gin.Context) {
	col, ok := s.findCollection(c)
	if !ok {
		return
	}
	if !s.requireWritableCollection(c, col) {
		return
	}

	rec, ok := s.findRecord(c, col)
	if !ok {
		return
	}

	if err := s.app.Delete(rec); err != nil {
		s.setFlash(c, "danger", "Could not delete the record: "+err.Error())
	} else {
		s.setFlash(c, "success", "Record deleted.")
	}

	c.Redirect(http.StatusSeeOther, listURL(col))
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// renderForm renders the create/edit form.
func (s *Server) renderForm(c *gin.Context, status int, col *core.Collection, rec *core.Record, isNew bool, errs []string) {
	action := listURL(col)
	recordID := ""

	if !isNew {
		action = action + "/" + url.PathEscape(rec.Id)
		recordID = rec.Id
	}

	title := fmt.Sprintf("New record · %s", col.Name)
	if !isNew {
		title = fmt.Sprintf("Edit record · %s", col.Name)
	}

	page := recordFormPage{
		base:       s.newBase(c, title, "collections"),
		Collection: col,
		IsNew:      isNew,
		Action:     action,
		RecordID:   recordID,
		Fields:     buildFormFields(s.app, col, rec, isNew),
		Errors:     errs,
	}
	page.CurrentCollection = col.Name

	s.render(c, status, "record_form.html", page)
}

// requireWritableCollection rejects writes against view collections.
func (s *Server) requireWritableCollection(c *gin.Context, col *core.Collection) bool {
	if !col.IsView() {
		return true
	}

	s.renderErrorPage(c, http.StatusBadRequest, "Read-only collection",
		fmt.Sprintf("%q is a view collection and cannot store records.", col.Name))
	return false
}

// findRecord resolves the :id route parameter within a collection.
func (s *Server) findRecord(c *gin.Context, col *core.Collection) (*core.Record, bool) {
	id := c.Param("id")

	rec, err := s.app.FindRecordById(col, id)
	if err != nil {
		s.renderErrorPage(c, http.StatusNotFound, "Record not found",
			fmt.Sprintf("No record %q in collection %q.", id, col.Name))
		return nil, false
	}

	return rec, true
}

// countRecords returns the total number of records matching a filter, reusing
// the same resolver PocketBase applies in FindRecordsByFilter.
func (s *Server) countRecords(col *core.Collection, filter string, params map[string]any) (int64, error) {
	query := s.app.RecordQuery(col)

	if filter != "" {
		resolver := core.NewRecordFieldResolver(s.app, col, nil, true)

		expr, err := search.FilterData(filter).BuildExpr(resolver, dbx.Params(params))
		if err != nil {
			return 0, err
		}

		query.AndWhere(expr)

		if err := resolver.UpdateQuery(query); err != nil {
			return 0, err
		}
	}

	var total int64
	if err := query.Select("count(*)").Row(&total); err != nil {
		return 0, err
	}

	return total, nil
}

func listURL(col *core.Collection) string {
	return fmt.Sprintf("/collections/%s/records", url.PathEscape(col.Name))
}

// perPageOptions returns the page sizes offered by the record list.
//
// The current value is always included so that a perPage configured outside
// the standard set (via YAML, env or query string) is still represented in the
// selector instead of silently falling back to the first option.
func perPageOptions(current int) []int {
	options := []int{10, 20, 30, 50, 100}

	for _, v := range options {
		if v == current {
			return options
		}
	}

	options = append(options, current)
	sort.Ints(options)

	return options
}

// pageWindow returns at most seven page numbers centred on the current page.
func pageWindow(current, total int) []int {
	if total <= 0 || current <= 0 {
		return nil
	}

	const window = 7

	start := current - window/2
	if start < 1 {
		start = 1
	}

	end := start + window - 1
	if end > total {
		end = total
		if start = end - window + 1; start < 1 {
			start = 1
		}
	}

	return seq(start, end)
}

// validationMessages splits PocketBase's joined validation output into one
// message per failing field.
func validationMessages(err error) []string {
	if err == nil {
		return nil
	}

	parts := strings.Split(err.Error(), "; ")

	messages := make([]string, 0, len(parts))
	for _, part := range parts {
		if part = strings.TrimSpace(part); part != "" {
			messages = append(messages, part)
		}
	}

	if len(messages) == 0 {
		return []string{err.Error()}
	}

	return messages
}
