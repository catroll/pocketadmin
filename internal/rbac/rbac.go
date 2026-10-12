// Package rbac resolves what an authenticated account is allowed to do.
//
// The platform is users-centric: an account may hold an `admins` profile, and
// that profile carries roles. A role grants permission points and menu nodes.
// The actor assembled here is what every authorisation decision reads.
package rbac

import (
	"fmt"
	"sync"

	"github.com/pocketbase/pocketbase/core"

	"pocketadmin/internal/permission"
)

// Collection names owned by the identity and rbac modules.
const (
	CollectionUsers  = "users"
	CollectionAdmins = "admins"
	CollectionRoles  = "roles"

	// StatusActive is the only admin profile status that grants access.
	StatusActive = "active"
)

// Actor is an authenticated principal together with its resolved authority.
type Actor struct {
	// UserID is the account id. Every actor has one: break-glass included.
	UserID string

	// Email and Name are used for display.
	Email string
	Name  string

	// IsSuperuser marks a PocketBase _superusers account signing in through the
	// break-glass path. It bypasses permission checks so an operator can always
	// recover a damaged RBAC configuration.
	IsSuperuser bool

	// AdminProfileID is empty for an account with no back-office access.
	AdminProfileID string

	// AdminStatus mirrors the profile status.
	AdminStatus string

	// RoleCodes lists the active roles the profile holds, for display.
	RoleCodes []string

	// Permissions is the union of every active role's permission points.
	Permissions permission.Set

	// DataScope is the broadest scope among the actor's roles.
	DataScope string

	// DepartmentID and DepartmentPath drive row-level scoping. DepartmentPath is
	// the materialised path, so "and below" is a prefix match.
	DepartmentID   string
	DepartmentPath string

	// CustomDepartmentIDs are the departments named by the actor's roles, used by
	// the "custom" data scope.
	CustomDepartmentIDs []string

	// versionKey changes whenever the underlying profile or its roles change, so
	// the resolver can tell a stale cache entry from a fresh one.
	versionKey string
}

// IsAdmin reports whether the actor may use the back office at all.
//
// A superuser always may; an ordinary account needs an active admin profile.
func (a *Actor) IsAdmin() bool {
	if a == nil {
		return false
	}
	if a.IsSuperuser {
		return true
	}
	return a.AdminProfileID != "" && a.AdminStatus == StatusActive
}

// Can reports whether the actor holds a permission point.
//
// Superusers hold everything. The default for everyone else is deny: an unknown
// code, an empty set and a missing profile all return false.
func (a *Actor) Can(code string) bool {
	if a == nil {
		return false
	}
	if a.IsSuperuser {
		return true
	}
	if !a.IsAdmin() {
		return false
	}
	return a.Permissions.Has(code)
}

// CanAny reports whether the actor holds at least one of the codes.
func (a *Actor) CanAny(codes ...string) bool {
	for _, c := range codes {
		if a.Can(c) {
			return true
		}
	}
	return false
}

// DisplayName returns the best available label for the actor.
func (a *Actor) DisplayName() string {
	if a == nil {
		return ""
	}
	if a.Name != "" {
		return a.Name
	}
	return a.Email
}

// Resolver builds actors and caches their resolved authority.
//
// Permission resolution touches the user, the admin profile, the profile's roles
// and each role's permissions. Doing that on every request would be wasteful, so
// the result is cached against the profile's and roles' `updated` timestamps.
type Resolver struct {
	app core.App

	mu    sync.RWMutex
	cache map[string]*Actor
}

// NewResolver returns a resolver bound to an application.
func NewResolver(app core.App) *Resolver {
	return &Resolver{app: app, cache: make(map[string]*Actor)}
}

// Resolve builds the actor for an account.
//
// An account with no admin profile, a disabled profile or no roles resolves
// successfully but holds no permissions: it can sign in and see its own
// preferences, and nothing else.
func (r *Resolver) Resolve(user *core.Record) (*Actor, error) {
	if user == nil {
		return nil, fmt.Errorf("resolve actor: no user record")
	}

	actor := &Actor{
		UserID:      user.Id,
		Email:       user.Email(),
		Name:        user.GetString("name"),
		Permissions: permission.NewSet(),
	}

	profile, err := r.adminProfile(user.Id)
	if err != nil {
		return nil, err
	}
	if profile == nil {
		return actor, nil
	}

	actor.AdminProfileID = profile.Id
	actor.AdminStatus = profile.GetString("status")

	// The profile's own timestamp is part of the cache key, so editing the
	// profile (status, roles, department) invalidates the entry.
	actor.versionKey = profile.Id + ":" + profile.GetString("updated")

	// A department scope needs the department's materialised path, not just its
	// id: "and below" is a prefix match on that path. The department's own
	// timestamp joins the cache key, because moving or renaming it changes every
	// descendant's path without touching this profile.
	if deptID := profile.GetString("department"); deptID != "" {
		actor.DepartmentID = deptID
		if dept, err := r.app.FindRecordById("departments", deptID); err == nil && dept != nil {
			actor.DepartmentPath = dept.GetString("path")
			actor.versionKey += "|dept:" + dept.Id + ":" + dept.GetString("updated")
		}
	}

	if cached := r.lookup(user.Id, actor.versionKey); cached != nil {
		return cached, nil
	}

	if err := r.applyRoles(actor, profile); err != nil {
		return nil, err
	}

	r.store(user.Id, actor)
	return actor, nil
}

// SuperuserActor builds a break-glass actor from a _superusers record.
func SuperuserActor(rec *core.Record) *Actor {
	if rec == nil {
		return nil
	}
	return &Actor{
		UserID:      rec.Id,
		Email:       rec.Email(),
		IsSuperuser: true,
		Permissions: permission.NewSet(),
		DataScope:   "all",
	}
}

// Invalidate drops the cached authority for an account.
//
// Called when a role, a role's grants or a profile assignment changes, so the
// next request re-reads the database instead of serving a stale permission set.
func (r *Resolver) Invalidate(userID string) {
	if userID == "" {
		return
	}
	r.mu.Lock()
	delete(r.cache, userID)
	r.mu.Unlock()
}

// InvalidateAll clears the whole cache, for changes that affect many accounts at
// once (a role's permission grants, for example).
func (r *Resolver) InvalidateAll() {
	r.mu.Lock()
	r.cache = make(map[string]*Actor)
	r.mu.Unlock()
}

// adminProfile loads the account's admin profile, or nil when it has none.
func (r *Resolver) adminProfile(userID string) (*core.Record, error) {
	rec, err := r.app.FindFirstRecordByFilter(
		CollectionAdmins,
		"user = {:user} && deletedAt = ''",
		map[string]any{"user": userID},
	)
	if err != nil || rec == nil {
		// No profile is a normal state, not an error.
		return nil, nil
	}
	return rec, nil
}

// applyRoles unions the granted permissions of every active role the profile
// holds, and widens the data scope to the broadest one found.
func (r *Resolver) applyRoles(actor *Actor, profile *core.Record) error {
	roleIDs := profile.GetStringSlice("roles")
	if len(roleIDs) == 0 {
		return nil
	}

	seen := make(map[string]bool, len(roleIDs))
	for _, id := range roleIDs {
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true

		role, err := r.app.FindRecordById(CollectionRoles, id)
		if err != nil || role == nil {
			continue
		}
		if role.GetString("status") != "active" || !role.GetDateTime("deletedAt").IsZero() {
			continue
		}

		actor.RoleCodes = append(actor.RoleCodes, role.GetString("code"))
		actor.versionKey += "|" + role.Id + ":" + role.GetString("updated")

		for _, code := range role.GetStringSlice("permissions") {
			point, err := r.app.FindRecordById("permissions", code)
			if err != nil || point == nil {
				continue
			}
			if point.GetString("status") != "active" {
				continue
			}
			actor.Permissions[point.GetString("code")] = struct{}{}
		}

		// A "custom" scope is only as wide as the departments its roles name, so
		// the ids accumulate across every role rather than replacing each other.
		actor.CustomDepartmentIDs = append(actor.CustomDepartmentIDs, role.GetStringSlice("customDepartments")...)

		actor.DataScope = widerScope(actor.DataScope, role.GetString("dataScope"))
	}

	return nil
}

// scopeRank orders data scopes from narrowest to broadest.
var scopeRank = map[string]int{
	"":                     -1,
	"self":                 0,
	"department":           1,
	"custom":               2,
	"department_and_below": 3,
	"all":                  4,
}

// widerScope returns whichever of the two scopes grants more.
func widerScope(a, b string) string {
	if scopeRank[b] > scopeRank[a] {
		return b
	}
	return a
}

func (r *Resolver) lookup(userID, versionKey string) *Actor {
	r.mu.RLock()
	defer r.mu.RUnlock()

	cached, ok := r.cache[userID]
	if !ok || cached.versionKey != versionKey {
		return nil
	}
	return cached
}

func (r *Resolver) store(userID string, actor *Actor) {
	r.mu.Lock()
	r.cache[userID] = actor
	r.mu.Unlock()
}
