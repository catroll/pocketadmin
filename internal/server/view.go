package server

import (
	"github.com/pocketbase/pocketbase/core"

	"pocketadmin/internal/permission"
	"pocketadmin/internal/rbac"
)

// Can reports whether the signed-in account holds a permission point.
//
// A superuser holds everything, so a damaged RBAC configuration can always be
// repaired from the panel. An empty code means "no permission needed" and is
// always allowed, which keeps navigation entries without a code visible.
func (b base) Can(code string) bool {
	if code == "" || b.Superuser {
		return true
	}
	return b.Perms.Has(code)
}

// base carries the data that every rendered page needs.
//
// Page-specific view models embed base, so templates can reference `.Title`,
// `.CSRF`, `.Nav`, ... directly on the outer struct.
type base struct {
	Title     string
	Active    string
	CSRF      string
	UserEmail string
	AppName   string
	PBVersion string
	PanelVer  string
	Nav       []navCollection
	Flash     *flash

	// Actor is the resolved principal, or nil on pages rendered before
	// authentication. Templates use it to hide controls the user cannot use.
	Actor *rbac.Actor

	// Perms is the actor's permission set and Superuser marks break-glass, so a
	// template can hide an action rather than render it and fail on submit:
	// {{if .Can "rbac:role:create"}}. Can is a method rather than a func field
	// because templates invoke methods with arguments, not fields.
	Perms     permission.Set
	Superuser bool

	// Menus is the navigation tree projected from the actor's permissions.
	Menus []rbac.MenuNode

	// Theme and SidebarCollapsed come from the account's stored preferences, so
	// the choice is applied rather than only recorded.
	Theme            string
	SidebarCollapsed bool

	// Impersonating marks a support session viewing the panel as someone else,
	// with the operator's address for the banner.
	Impersonating     bool
	ImpersonatorEmail string

	// CurrentCollection highlights the active collection in the sidebar.
	CurrentCollection string
}

// flash is a one-shot notification rendered once and then dropped.
type flash struct {
	Type    string // success | danger | warning | info
	Message string
}

// navCollection is one entry of the sidebar collection list.
type navCollection struct {
	Name   string
	Type   string
	System bool
	Count  int64
	Active bool
}

// ---------------------------------------------------------------------------
// Dashboard
// ---------------------------------------------------------------------------

type dashboardPage struct {
	base
	Stats       dashboardStats
	Collections []collectionStat
	Chart       chartData
}

type dashboardStats struct {
	Collections   int
	Records       int64
	Superusers    int64
	AuthRecords   int64
	Files         int
	DataDir       string
	DatabaseBytes int64
	UploadBytes   int64
}

type collectionStat struct {
	Name        string
	Type        string
	System      bool
	FieldCount  int
	RecordCount int64
	IsView      bool
}

type chartData struct {
	Labels []string
	Values []int64
}

// ---------------------------------------------------------------------------
// Collections
// ---------------------------------------------------------------------------

type collectionsPage struct {
	base
	Collections []collectionStat
	TotalRecs   int64
}

// ---------------------------------------------------------------------------
// Record listing
// ---------------------------------------------------------------------------

type recordsPage struct {
	base
	Collection *core.Collection
	Columns    []column
	Rows       []row
	Pagination pagination
	Query      string
	Sort       string
	Dir        string
	NextDir    string

	// QueryPrefix carries only the filters that must survive a sort or page
	// change (q, perPage). Sort/dir/page are appended explicitly by the links.
	QueryPrefix string

	// PerPageOptions are the selectable page sizes. The configured default is
	// always included so it can be rendered as the selected option.
	PerPageOptions []int
}

type pagination struct {
	Page       int
	PerPage    int
	Total      int64
	TotalPages int
	HasPrev    bool
	HasNext    bool
	From       int64
	To         int64
	Pages      []int
}

type column struct {
	Name  string
	Label string
	Type  string
	Sort  string // sort key to emit, "" when not sortable
}

type row struct {
	ID    string
	Cells []cell

	// Indent is how deep the row sits in a hierarchy, for a tree view.
	Indent int
}

// cell is a single rendered table cell. Kind selects the template branch.
type cell struct {
	Kind      string // text | bool | list | file | json | date | empty
	Text      string
	Title     string
	Bool      bool
	Items     []string
	Files     []fileRef
	Truncated bool
}

type fileRef struct {
	Name    string
	URL     string
	IsImage bool
	Size    string
}

// ---------------------------------------------------------------------------
// Record create / edit form
// ---------------------------------------------------------------------------

type recordFormPage struct {
	base
	Collection *core.Collection
	IsNew      bool
	Action     string
	RecordID   string
	Fields     []formField
	Errors     []string
}

// formField describes a single input generated from a PocketBase field.
type formField struct {
	Name        string
	Label       string
	Type        string
	Help        string
	Required    bool
	ReadOnly    bool
	Input       string // text | number | checkbox | textarea | select | file | datetime | geopoint | password | json | readonly
	Value       string
	Placeholder string
	Step        string
	Multiple    bool
	Checked     bool
	Options     []formOption
	Selected    map[string]bool
	Files       []fileRef
	MaxSelect   int
}

type formOption struct {
	Value string
	Label string
}

// ---------------------------------------------------------------------------
// Module resources (identity, organization, rbac)
// ---------------------------------------------------------------------------

// resourceListPage renders any managed collection as a table.
type resourceListPage struct {
	base

	Resource   resource
	Collection *core.Collection
	Columns    []column
	Rows       []row

	Pagination pagination
	Query      string
	Sort       string
	Dir        string

	// QueryPrefix carries q and perPage so they survive a sort or page change.
	QueryPrefix string

	// PerPageItems are the selectable page sizes, including the current one.
	PerPageItems []int

	// HasDetail renders a view action linking to the resource's detail screen.
	HasDetail bool

	// TreeView indents rows by depth to show a hierarchy.
	TreeView bool

	// RowActionPath and friends render a per-row link to a sub-list.
	RowActionPath string
	RowActionIcon string
	RowActionText string

	// ListPath is the list URL, carrying the parent when the resource is scoped to
	// one. ParentID and ParentLabel describe that parent.
	ListPath    string
	ParentID    string
	ParentLabel string

	// ScopeNote explains a row restriction to the user, so a short list reads as
	// a deliberate scope rather than as missing data.
	ScopeNote string

	// Capability flags hide controls the actor may not use, so a page never
	// offers an action that would fail on submit.
	CanCreate bool
	CanUpdate bool
	CanDelete bool
}

// resourceFormPage renders any managed collection's create/edit form.
type resourceFormPage struct {
	base

	Resource  resource
	IsNew     bool
	Action    string
	CancelURL string
	Fields    []formField
	Errors    []string
	RecordID  string

	CanDelete  bool
	DeletePath string
}

// ---------------------------------------------------------------------------
// Error page
// ---------------------------------------------------------------------------

type errorPage struct {
	base
	Code    int
	Message string
	Detail  string
}
