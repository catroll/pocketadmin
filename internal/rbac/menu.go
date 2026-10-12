package rbac

import (
	"sort"
	"sync"

	"github.com/pocketbase/pocketbase/core"

	"pocketadmin/internal/permission"
)

// CollectionMenus is the navigation tree's collection name.
const CollectionMenus = "menus"

// MenuNode is one rendered navigation entry.
type MenuNode struct {
	ID       string
	Name     string // stable route name
	Title    string
	Type     string // directory | menu | link
	Path     string
	Icon     string
	Sort     int
	Children []MenuNode
}

// menuRow is the cached projection input, read once from the collection.
type menuRow struct {
	id            string
	name          string
	title         string
	menuType      string
	path          string
	icon          string
	permission    string
	parent        string
	sort          int
	platform      string
	status        string
	superuserOnly bool
}

// Projector turns the menu collection into the navigation an actor may see.
//
// Navigation is derived, never authored per role: a node is visible when its
// permission code is one the actor holds. The menu collection is small and
// changes rarely, so it is read once and cached until a menu write invalidates
// it.
type Projector struct {
	app core.App

	mu     sync.RWMutex
	rows   []menuRow
	loaded bool
}

// NewProjector returns a projector bound to an application.
func NewProjector(app core.App) *Projector {
	return &Projector{app: app}
}

// Invalidate drops the cached menu rows.
//
// Called by any code path that writes to the menus collection, so the next
// request re-reads rather than serving stale navigation.
func (p *Projector) Invalidate() {
	p.mu.Lock()
	p.rows = nil
	p.loaded = false
	p.mu.Unlock()
}

// Visible returns the navigation tree the actor may see, ordered by sort order.
//
// A node is included when it is active, belongs to the admin platform, is not a
// button, and either carries no permission code or one the actor holds. A node
// whose parent is not visible is dropped even if the node itself is granted, so
// a granted child never appears orphaned.
func (p *Projector) Visible(actor *Actor) []MenuNode {
	// A page rendered before authentication (the login form) has no actor, and a
	// nil actor holds nothing. Substituting an empty one here keeps every caller
	// free to pass whatever it has; such a page renders no sidebar anyway.
	if actor == nil {
		actor = &Actor{Permissions: permission.NewSet()}
	}

	rows := p.load()

	visible := make(map[string]bool, len(rows))
	for _, r := range rows {
		if r.platform != "admin" || r.status != "active" || r.menuType == "button" {
			continue
		}
		// Some capabilities are deliberately not delegated to roles, so they
		// cannot be expressed as a permission code.
		if r.superuserOnly && !actor.IsSuperuser {
			continue
		}
		if r.permission != "" && !actor.Can(r.permission) {
			continue
		}
		visible[r.id] = true
	}

	// Drop orphans: a node whose parent exists but is not itself visible.
	roots := make([]menuRow, 0, len(rows))
	children := make(map[string][]menuRow, len(rows))
	for _, r := range rows {
		if !visible[r.id] {
			continue
		}
		if r.parent == "" {
			roots = append(roots, r)
			continue
		}
		if visible[r.parent] {
			children[r.parent] = append(children[r.parent], r)
		}
	}

	sortRows(roots)
	return buildNodes(roots, children, 0)
}

// maxMenuDepth bounds recursion so a cycle in the parent relation cannot hang
// the request.
const maxMenuDepth = 8

func buildNodes(rows []menuRow, children map[string][]menuRow, depth int) []MenuNode {
	if depth >= maxMenuDepth {
		return nil
	}

	out := make([]MenuNode, 0, len(rows))
	for _, r := range rows {
		kids := children[r.id]
		sortRows(kids)

		// A directory that has no visible children is itself invisible: rendering
		// it would show an expandable heading that leads nowhere, for a module
		// the actor has no access to at all.
		if r.menuType == "directory" && len(kids) == 0 {
			continue
		}

		node := MenuNode{
			ID:    r.id,
			Name:  r.name,
			Title: r.title,
			Type:  r.menuType,
			Path:  r.path,
			Icon:  r.icon,
			Sort:  r.sort,
		}
		node.Children = buildNodes(kids, children, depth+1)

		out = append(out, node)
	}
	return out
}

func sortRows(rows []menuRow) {
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].sort != rows[j].sort {
			return rows[i].sort < rows[j].sort
		}
		return rows[i].title < rows[j].title
	})
}

// load returns the cached rows, reading them from the database on first use.
func (p *Projector) load() []menuRow {
	p.mu.RLock()
	if p.loaded {
		rows := p.rows
		p.mu.RUnlock()
		return rows
	}
	p.mu.RUnlock()

	records, err := p.app.FindAllRecords(CollectionMenus)
	if err != nil {
		return nil
	}

	rows := make([]menuRow, 0, len(records))
	for _, rec := range records {
		rows = append(rows, menuRow{
			id:            rec.Id,
			name:          rec.GetString("name"),
			title:         rec.GetString("title"),
			menuType:      rec.GetString("type"),
			path:          rec.GetString("path"),
			icon:          rec.GetString("icon"),
			permission:    rec.GetString("permissionCode"),
			parent:        rec.GetString("parent"),
			sort:          rec.GetInt("sortOrder"),
			platform:      rec.GetString("platform"),
			status:        rec.GetString("status"),
			superuserOnly: rec.GetBool("superuserOnly"),
		})
	}

	p.mu.Lock()
	p.rows = rows
	p.loaded = true
	p.mu.Unlock()

	return rows
}
