package rbac

import (
	"strconv"
	"strings"

	"github.com/pocketbase/pocketbase/core"
)

// Data scope values, as seeded into the roles collection.
const (
	ScopeSelf               = "self"
	ScopeDepartment         = "department"
	ScopeCustom             = "custom"
	ScopeDepartmentAndBelow = "department_and_below"
	ScopeAll                = "all"
)

// ScopeDimension names the fields a data scope can narrow on.
//
// These are conventions rather than configuration: a collection either carries
// an owning account, a department, or neither.
var (
	// ownerFields are checked in order; the first present on a collection is used
	// for the "self" scope.
	ownerFields = []string{"user", "owner", "createdBy"}

	// departmentFields are checked in order; the first present is used for the
	// department scopes.
	departmentFields = []string{"department"}
)

// AppliedScope is the row restriction a data scope imposes on one query.
type AppliedScope struct {
	// Filter is a PocketBase filter fragment, combined with "&&" into the
	// caller's own filter. Empty means the scope does not narrow this collection.
	Filter string

	// Params are the placeholders referenced by Filter.
	Params map[string]any

	// DenyAll means the scope excludes every row. It is distinct from an empty
	// Filter, which means "not narrowed".
	DenyAll bool
}

// ScopeFor computes the row restriction an actor's data scope imposes on a
// collection.
//
// Data scope narrows the rows *within* an already granted capability; it does
// not grant or withhold the capability itself. That distinction matters when a
// collection has no dimension to narrow on: a department-scoped role listing
// roles, dictionaries or permission points is acting on configuration rather
// than on department-owned data, and there is nothing for the scope to apply to.
// Denying those rows would make a scoped role unable to administer what it was
// explicitly granted, so such a collection is left unnarrowed.
//
// Where the dimension does exist the scope is applied strictly, and an actor
// whose scope names a dimension it lacks (custom departments it was never given,
// for example) sees nothing rather than everything.
func ScopeFor(actor *Actor, col *core.Collection) AppliedScope {
	if actor == nil {
		// No actor means no authority. Every caller is behind authentication, so
		// this is a programming error; fail closed rather than open.
		return AppliedScope{DenyAll: true}
	}
	if actor.IsSuperuser || actor.DataScope == ScopeAll || actor.DataScope == "" {
		return AppliedScope{}
	}

	departmentField := firstField(col, departmentFields)
	ownerField := firstField(col, ownerFields)

	switch actor.DataScope {
	case ScopeSelf:
		if ownerField != "" {
			return AppliedScope{
				Filter: ownerField + " = {:scopeOwner}",
				Params: map[string]any{"scopeOwner": actor.UserID},
			}
		}
		// No owning column: fall back to the actor's own department, which is the
		// narrowest thing still expressible.
		return departmentScope(actor, departmentField, false)

	case ScopeDepartment:
		return departmentScope(actor, departmentField, false)

	case ScopeDepartmentAndBelow:
		return departmentScope(actor, departmentField, true)

	case ScopeCustom:
		return customScope(actor, departmentField)
	}

	// An unrecognised scope is a data error. Fail closed.
	return AppliedScope{DenyAll: true}
}

// departmentScope restricts to the actor's department, or to that department and
// everything beneath it.
func departmentScope(actor *Actor, field string, includeDescendants bool) AppliedScope {
	if field == "" {
		// Nothing to narrow on: this collection is configuration, not
		// department-owned data.
		return AppliedScope{}
	}

	if actor.DepartmentID == "" {
		// The scope names a dimension the actor does not have. Seeing nothing is
		// the safe reading; seeing everything would escalate.
		return AppliedScope{DenyAll: true}
	}

	if includeDescendants && actor.DepartmentPath != "" {
		// The materialised path makes "and below" a single prefix match, which is
		// the whole reason the path is maintained.
		return AppliedScope{
			Filter: field + ".path ~ {:scopePath}",
			Params: map[string]any{"scopePath": actor.DepartmentPath},
		}
	}

	return AppliedScope{
		Filter: field + " = {:scopeDept}",
		Params: map[string]any{"scopeDept": actor.DepartmentID},
	}
}

// customScope restricts to the departments named on the actor's roles.
func customScope(actor *Actor, field string) AppliedScope {
	if field == "" {
		return AppliedScope{}
	}

	ids := dedupe(actor.CustomDepartmentIDs)
	if len(ids) == 0 {
		// "Custom" with nothing selected is an empty set, not an absent rule.
		return AppliedScope{DenyAll: true}
	}

	clauses := make([]string, 0, len(ids))
	params := make(map[string]any, len(ids))
	for i, id := range ids {
		key := "scopeDept" + strconv.Itoa(i)
		clauses = append(clauses, field+" = {:"+key+"}")
		params[key] = id
	}

	return AppliedScope{Filter: "(" + strings.Join(clauses, " || ") + ")", Params: params}
}

// Description explains the scope in user-facing terms, so a narrowed list reads
// as a deliberate restriction rather than as missing data.
func (a AppliedScope) Description() string {
	switch {
	case a.DenyAll:
		return "Your data scope does not include any records in this list."
	case a.Filter == "":
		return ""
	default:
		return "Rows are limited by your data scope."
	}
}

// firstField returns the first named field present on a collection, or "".
func firstField(col *core.Collection, names []string) string {
	if col == nil {
		return ""
	}
	for _, name := range names {
		if col.Fields.GetByName(name) != nil {
			return name
		}
	}
	return ""
}

// dedupe removes empty and repeated ids while preserving order.
func dedupe(ids []string) []string {
	seen := make(map[string]bool, len(ids))
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out
}
