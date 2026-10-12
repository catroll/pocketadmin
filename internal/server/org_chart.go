package server

import (
	"net/http"
	"sort"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/pocketbase/pocketbase/core"

	"pocketadmin/internal/permission"
)

// orgChartPage is the organisation chart.
type orgChartPage struct {
	base

	// Roots is the department tree, each node carrying its head count.
	Roots []orgNode

	// Unassigned counts staff who belong to no department, which is the number an
	// administrator most often wants and the tree cannot show.
	Unassigned int64

	// TotalStaff is every active member of staff.
	TotalStaff int64

	// MaxDepth is how deep the chart goes, used to size the layout.
	MaxDepth int
}

// orgNode is one department in the chart.
type orgNode struct {
	ID       string
	Name     string
	Code     string
	Level    int
	Members  int64
	Children []orgNode
}

// registerOrgChartRoutes mounts the organisation chart.
func (s *Server) registerOrgChartRoutes(panel *gin.RouterGroup) {
	s.readGuarded(panel, http.MethodGet, "/organization/chart",
		permission.OrganizationDepartmentList, s.handleOrgChart)
}

// handleOrgChart renders the department hierarchy with head counts.
//
// The tree list is already available and is what an administrator edits; this is
// the read-only view of the same structure, which answers "how big is each part
// of the organisation" without the columns getting in the way.
func (s *Server) handleOrgChart(c *gin.Context) {
	col, err := s.app.FindCollectionByNameOrId("departments")
	if err != nil || col == nil {
		s.renderErrorPage(c, http.StatusInternalServerError, "Schema missing", "The departments collection does not exist.")
		return
	}

	departments, err := s.app.FindRecordsByFilter("departments", "deletedAt = ''", "path", 0, 0)
	if err != nil {
		s.renderErrorPage(c, http.StatusInternalServerError, "Could not load departments", err.Error())
		return
	}

	counts, err := s.staffCountsByDepartment()
	if err != nil {
		s.renderErrorPage(c, http.StatusInternalServerError, "Could not count staff", err.Error())
		return
	}

	roots, maxDepth := buildOrgTree(departments, counts)

	var total int64
	if employees := mustCollection(s.app, "employees"); employees != nil {
		if n, err := s.countRecords(employees, "deletedAt = ''", nil); err == nil {
			total = n
		}
	}

	s.render(c, http.StatusOK, "org_chart.html", orgChartPage{
		base:       s.newBase(c, "Organisation chart", "organization.departments"),
		Roots:      roots,
		Unassigned: counts[""],
		TotalStaff: total,
		MaxDepth:   maxDepth,
	})
}

// staffCountsByDepartment counts live staff per department.
//
// It is one grouped query rather than a count per node, because a chart renders
// every department at once and an N+1 here would be proportional to the whole
// organisation rather than to a page.
func (s *Server) staffCountsByDepartment() (map[string]int64, error) {
	counts := make(map[string]int64)

	rows, err := s.app.DB().NewQuery(
		"SELECT department, COUNT(*) AS total FROM employees " +
			"WHERE deletedAt IS NULL OR deletedAt = '' GROUP BY department").
		Rows()
	if err != nil {
		return counts, err
	}
	defer rows.Close()

	for rows.Next() {
		var department string
		var total int64
		if err := rows.Scan(&department, &total); err != nil {
			return counts, err
		}
		counts[department] += total
	}

	return counts, rows.Err()
}

// buildOrgTree assembles the department hierarchy and rolls head counts upward.
//
// A manager looking at a chart wants the size of the whole branch, not just the
// people sitting directly in it, so each node reports its own members plus
// everyone beneath it.
func buildOrgTree(departments []*core.Record, direct map[string]int64) ([]orgNode, int) {
	byID := make(map[string]*core.Record, len(departments))
	for _, rec := range departments {
		byID[rec.Id] = rec
	}

	children := make(map[string][]*core.Record, len(departments))
	var roots []*core.Record
	for _, rec := range departments {
		parentID := rec.GetString("parent")
		if parentID == "" {
			roots = append(roots, rec)
			continue
		}
		if _, present := byID[parentID]; !present {
			// A parent outside the set (deleted or filtered) is shown as a root
			// rather than hidden.
			roots = append(roots, rec)
			continue
		}
		children[parentID] = append(children[parentID], rec)
	}

	sortRecords(roots)
	maxDepth := 0

	var build func(list []*core.Record, level int) []orgNode
	build = func(list []*core.Record, level int) []orgNode {
		if level >= maxTreeDepth {
			return nil
		}
		if level > maxDepth {
			maxDepth = level
		}

		out := make([]orgNode, 0, len(list))
		for _, rec := range list {
			kids := children[rec.Id]
			sortRecords(kids)

			node := orgNode{
				ID:       rec.Id,
				Name:     rec.GetString("name"),
				Code:     rec.GetString("code"),
				Level:    level,
				Members:  direct[rec.Id],
				Children: build(kids, level+1),
			}
			// Roll the subtree up into this node.
			for _, child := range node.Children {
				node.Members += child.Members
			}
			out = append(out, node)
		}
		return out
	}

	return build(roots, 0), maxDepth
}

// sortRecords orders departments the way the tree list does.
func sortRecords(records []*core.Record) {
	sort.SliceStable(records, func(i, j int) bool {
		pi, pj := records[i].GetString("path"), records[j].GetString("path")
		if pi != pj {
			return pi < pj
		}
		return strings.Compare(records[i].GetString("name"), records[j].GetString("name")) < 0
	})
}

// mustCollection resolves a collection, returning nil when it is absent.
func mustCollection(app core.App, name string) *core.Collection {
	col, err := app.FindCollectionByNameOrId(name)
	if err != nil {
		return nil
	}
	return col
}
