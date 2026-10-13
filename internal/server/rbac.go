package server

import (
	"net/http"
	"sort"
	"strings"

	"github.com/gin-gonic/gin"

	"pocketadmin/internal/permission"
	"pocketadmin/internal/rbac"
)

// ctxActor is the request-context key holding the resolved actor.
const ctxActor = "actor"

// Guard sentinels for routes that are deliberately not permission-guarded.
//
// Both are recorded in the registry rather than left blank, so the startup
// self-check can tell a considered decision from an oversight.
const (
	// superuserOnly marks a break-glass capability that is not delegated to roles.
	superuserOnly = "\x00superuser"

	// authenticatedOnly marks a route that acts solely on the caller's own data,
	// such as self-service preferences. Requiring a permission would wrongly
	// suggest an administrator could manage someone else's settings.
	authenticatedOnly = "\x00authenticated"

	// signatureVerified marks a route whose authority is an HMAC over the request body.
	signatureVerified = "\x00signature"

	// apiKeyScoped marks a machine-API route whose authority is a bearer key with a
	// scoped permission.
	apiKeyScoped = "\x00apikey"
)

// actor returns the resolved actor for the request, or nil.
func (s *Server) actor(c *gin.Context) *rbac.Actor {
	raw, ok := c.Get(ctxActor)
	if !ok {
		return nil
	}
	a, _ := raw.(*rbac.Actor)
	return a
}

// Can reports whether the current actor holds a permission point. It is bound
// into the template func map so a page can hide controls the actor cannot use.
func (s *Server) canFrom(c *gin.Context, code string) bool {
	return s.actor(c).Can(code)
}

// requirePermission aborts with 403 unless the actor holds the permission.
//
// The default is deny: no actor, no admin profile and an empty permission set
// all fail. Superusers pass everything so a damaged RBAC configuration can
// always be repaired from the panel.
func (s *Server) requirePermission(code string) gin.HandlerFunc {
	return func(c *gin.Context) {
		a := s.actor(c)
		if a == nil {
			s.redirectToLogin(c)
			return
		}
		if !a.Can(code) {
			s.setFlash(c, "danger", "You do not have permission to do that.")
			s.renderErrorPage(c, http.StatusForbidden, "Forbidden",
				"Your account does not hold the permission required for this action.")
			c.Abort()
			return
		}
		c.Next()
	}
}

// requireSuperuser aborts unless the actor signed in through the break-glass
// path. It guards the raw data browser, which exposes arbitrary collections and
// is therefore not delegated.
func (s *Server) requireSuperuser() gin.HandlerFunc {
	return func(c *gin.Context) {
		a := s.actor(c)
		if a == nil {
			s.redirectToLogin(c)
			return
		}
		if !a.IsSuperuser {
			s.setFlash(c, "danger", "That area is restricted to PocketBase superusers.")
			s.renderErrorPage(c, http.StatusForbidden, "Forbidden",
				"The raw data browser is restricted to PocketBase superuser accounts.")
			c.Abort()
			return
		}
		c.Next()
	}
}

// ---------------------------------------------------------------------------
// Route guard registry and the startup self-check
// ---------------------------------------------------------------------------

// registerGuard records the guard applied to a route.
//
// Every route is registered through safe() or guarded(), which call this. The
// registry is what makes the startup self-check possible: Gin exposes the routes
// it holds but not their middleware chains, so the requirement is recorded at
// registration time and verified against the router afterwards.
func (s *Server) registerGuard(method, path, guard string) {
	if s.guards == nil {
		s.guards = make(map[string]string)
	}
	s.guards[strings.ToUpper(method)+" "+path] = guard
}

// joinPath mirrors how Gin composes a group's base path with a route path, so
// the guard registry keys match what http.Routes() reports.
func joinPath(base, path string) string {
	if base == "" || base == "/" {
		return path
	}
	return strings.TrimSuffix(base, "/") + path
}

// guarded registers a state-changing route together with the permission it
// requires. There is no way to register an unsafe route without one, which is
// the point.
func (s *Server) guarded(g *gin.RouterGroup, method, path, code string, handlers ...gin.HandlerFunc) {
	s.registerGuard(method, joinPath(g.BasePath(), path), code)
	g.Handle(method, path, append([]gin.HandlerFunc{s.requirePermission(code)}, handlers...)...)
}

// superuserGuarded registers a state-changing route reserved for break-glass
// superusers.
func (s *Server) superuserGuarded(g *gin.RouterGroup, method, path string, handlers ...gin.HandlerFunc) {
	s.registerGuard(method, joinPath(g.BasePath(), path), superuserOnly)
	g.Handle(method, path, append([]gin.HandlerFunc{s.requireSuperuser()}, handlers...)...)
}

// safe registers a read-only route. Safe methods change no state, so they are
// guarded only by authentication.
//
// Use readGuarded instead when a read still needs a permission: browsing a
// collection is a read, but it is no less sensitive than editing it.
func (s *Server) safe(g *gin.RouterGroup, method, path string, handlers ...gin.HandlerFunc) {
	s.registerGuard(method, joinPath(g.BasePath(), path), "")
	g.Handle(method, path, handlers...)
}

// readGuarded registers a read-only route that nevertheless requires a
// permission.
//
// Rendering a list is a read, so the startup self-check does not demand a guard
// here; but leaving reads ungated would let any signed-in account enumerate
// every screen by URL.
func (s *Server) readGuarded(g *gin.RouterGroup, method, path, code string, handlers ...gin.HandlerFunc) {
	s.registerGuard(method, joinPath(g.BasePath(), path), code)
	g.Handle(method, path, append([]gin.HandlerFunc{s.requirePermission(code)}, handlers...)...)
}

// selfGuarded registers a state-changing route that requires only a signed-in
// account.
//
// The panel group already applies requireAuth, so no extra middleware is added;
// the registry entry exists so the startup self-check can distinguish this from a
// route someone forgot to guard.
func (s *Server) selfGuarded(g *gin.RouterGroup, method, path string, handlers ...gin.HandlerFunc) {
	s.registerGuard(method, joinPath(g.BasePath(), path), authenticatedOnly)
	g.Handle(method, path, handlers...)
}

// apiGuarded registers a state-changing route on the machine-facing API.
//
// The authority is a bearer key plus a scoped permission, checked by requireAPIKey and
// requireAPIPermission. It is a named helper rather than a bare registration because the
// startup self-check inspects unsafe methods: the machine API's read routes are GET and
// pass it unremarked, and the first write route added to that API stopped the process
// from starting. Naming the authority is the same answer as signatureGuarded's, and it
// is greppable.
func (s *Server) apiGuarded(g *gin.RouterGroup, method, path, code string, handlers ...gin.HandlerFunc) {
	s.registerGuard(method, joinPath(g.BasePath(), path), apiKeyScoped)
	g.Handle(method, path, append([]gin.HandlerFunc{s.requireAPIPermission(code)}, handlers...)...)
}

// signatureGuarded registers a state-changing route whose authority is a verified
// signature rather than a session.
//
// The caller is a payment provider, which has no account here and never will. Its
// authority is the HMAC over the body, checked inside the handler, and the handler
// refuses every request when no signing secret is configured -- so the endpoint is
// closed by default rather than open by default.
//
// It is a named registration rather than an entry in the self-check's exemption list
// on purpose. An exemption list grows quietly; a helper with this name is greppable,
// and the next person to add one has to write the same word.
func (s *Server) signatureGuarded(g *gin.RouterGroup, method, path string, handlers ...gin.HandlerFunc) {
	s.registerGuard(method, joinPath(g.BasePath(), path), signatureVerified)
	g.Handle(method, path, handlers...)
}

// selfCheckRoutes walks the registered routes and fails when a state-changing
// route carries no guard.
//
// This turns "someone forgot the permission check" from a production
// vulnerability into a failed startup. Safe methods are exempt by design.
func (s *Server) selfCheckRoutes() error {
	var missing []string

	for _, route := range s.http.Routes() {
		if !isUnsafeMethod(route.Method) {
			continue
		}

		// Login, the second factor and logout are intentionally public: the first
		// two must be reachable before an actor exists, and logout only clears a
		// session. The second factor is public by necessity rather than by choice:
		// the account is not signed in until it is verified, which is the whole
		// point of it.
		switch route.Path {
		case "/login", "/logout", "/login/mfa":
			continue
		}

		key := strings.ToUpper(route.Method) + " " + route.Path
		if guard, ok := s.guards[key]; !ok || guard == "" {
			missing = append(missing, key)
		}
	}

	if len(missing) > 0 {
		sort.Strings(missing)
		return &unguardedRoutesError{routes: missing}
	}

	return nil
}

// unguardedRoutesError reports unsafe routes registered without a guard.
type unguardedRoutesError struct{ routes []string }

func (e *unguardedRoutesError) Error() string {
	return "unsafe routes registered without a permission guard: " + strings.Join(e.routes, ", ")
}

// navFromActor projects the actor's visible navigation.
func (s *Server) navFromActor(c *gin.Context) []rbac.MenuNode {
	return s.menus.Visible(s.actor(c))
}

// actorPermissions returns the actor's permission set for template rendering.
func actorPermissions(actor *rbac.Actor) permission.Set {
	if actor == nil {
		return permission.NewSet()
	}
	return actor.Permissions
}

// allPermissions is a template helper listing every declared point, grouped for
// the role editor.
func allPermissions() []permission.Point {
	return permission.All()
}
