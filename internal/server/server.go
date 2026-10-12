// Package server implements the AdminLTE management UI on top of an embedded
// PocketBase application, served by Gin.
package server

import (
	"bytes"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
	"github.com/pocketbase/pocketbase/core"
	"golang.org/x/crypto/bcrypt"

	"pocketadmin/internal/config"
	"pocketadmin/internal/logging"
	"pocketadmin/internal/rbac"
	"pocketadmin/internal/version"
	webassets "pocketadmin/web"
)

const (
	ctxUser = "pb_user"
	ctxCSRF = "csrf_token"

	// maxUploadBody bounds panel request bodies, file fields included.
	maxUploadBody = 128 << 20

	// sessionCookieName is the panel's own session cookie. It is intentionally
	// distinct from anything PocketBase sets.
	sessionCookieName = "pocketadmin_session"
)

// hiddenCollections are PocketBase's internal bookkeeping collections. They are
// reachable through the collections page but are kept out of the sidebar.
var hiddenCollections = map[string]bool{
	"_externalAuths": true,
	"_authOrigins":   true,
	"_mfas":          true,
	"_otps":          true,
}

// Server hosts the admin panel.
type Server struct {
	app  core.App
	cfg  *config.Config
	http *gin.Engine

	// resolver caches each account's resolved authority.
	resolver *rbac.Resolver

	// menus projects the navigation an actor may see.
	menus *rbac.Projector

	// guards records the permission each route requires, keyed
	// "METHOD /path". selfCheckRoutes compares it against the router.
	guards map[string]string

	// dummyPasswordHash equalizes login timing for unknown accounts.
	dummyPasswordHash string
}

// New builds the Gin engine, the template set and all panel routes.
//
// The returned handler serves only the panel. PocketBase's API is mounted
// alongside it by the caller (see main.go), so PocketBase requests never pass
// through Gin's response writer.
func New(app core.App, cfg *config.Config, logger *slog.Logger) (*Server, error) {
	templates, err := buildTemplates()
	if err != nil {
		return nil, err
	}

	dummy, err := bcrypt.GenerateFromPassword([]byte("pocketadmin-timing-equalizer"), bcrypt.DefaultCost)
	if err != nil {
		return nil, fmt.Errorf("prepare login timing guard: %w", err)
	}

	s := &Server{
		app:               app,
		cfg:               cfg,
		dummyPasswordHash: string(dummy),
		resolver:          rbac.NewResolver(app),
		menus:             rbac.NewProjector(app),
		guards:            make(map[string]string),
	}

	gin.SetMode(gin.ReleaseMode)

	r := gin.New()
	r.MaxMultipartMemory = 16 << 20
	r.HTMLRender = htmlRenderer{templates: templates}
	r.Use(logging.RequestLogger(logger), gin.Recovery())

	store := cookie.NewStore(cfg.SessionKey)
	store.Options(sessions.Options{
		Path:     "/",
		MaxAge:   int((8 * time.Hour).Seconds()),
		HttpOnly: true,
		Secure:   cfg.SecureCookie,
		SameSite: http.SameSiteLaxMode,
	})
	r.Use(sessions.Sessions(sessionCookieName, store))
	r.Use(s.loadFlash())

	// The static asset bundle is embedded in the binary.
	r.StaticFS("/static", http.FS(webassets.StaticFS))

	// Anything the panel does not claim is a panel 404.
	r.NoRoute(s.handleNotFound)

	// Public routes.
	//
	// csrf runs here too: on safe methods it only issues and persists the
	// session token that the rendered form embeds, while on the login/logout
	// POSTs it performs the actual validation.
	public := r.Group("/")
	public.Use(limitBody(maxUploadBody), s.csrf())
	{
		public.GET("/login", s.handleLoginForm)
		public.POST("/login", s.handleLogin)
		public.POST("/logout", s.handleLogout)
	}

	// Authenticated panel routes.
	//
	// Group middleware runs between the two nesting levels, so limitBody and
	// csrf wrap requireAuth exactly once per request.
	panel := r.Group("/")
	panel.Use(limitBody(maxUploadBody), s.csrf(), s.requireAuth(), s.blockWritesWhileImpersonating())
	{
		s.safe(panel, http.MethodGet, "/", func(c *gin.Context) {
			c.Redirect(http.StatusSeeOther, "/dashboard")
		})
		s.safe(panel, http.MethodGet, "/dashboard", s.handleDashboard)
		s.safe(panel, http.MethodGet, "/collections", s.handleCollections)
		s.safe(panel, http.MethodGet, "/collections/:collection/records", s.handleRecordList)
		s.safe(panel, http.MethodGet, "/collections/:collection/records/new", s.handleRecordNew)
		s.safe(panel, http.MethodGet, "/collections/:collection/records/:id/edit", s.handleRecordEdit)

		// The raw data browser writes directly to arbitrary collections, so it
		// is not delegated to roles: it stays a break-glass capability.
		s.superuserGuarded(panel, http.MethodPost, "/collections/:collection/records", s.handleRecordCreate)
		s.superuserGuarded(panel, http.MethodPost, "/collections/:collection/records/:id", s.handleRecordUpdate)
		s.superuserGuarded(panel, http.MethodPost, "/collections/:collection/records/:id/delete", s.handleRecordDelete)

		s.registerModuleRoutes(panel)
	}

	// selfCheckRoutes reads the router, so the engine must be assigned first.
	s.http = r

	// Fail startup rather than serve an unprotected state-changing route.
	if err := s.selfCheckRoutes(); err != nil {
		return nil, err
	}

	return s, nil
}

// Handler returns the root HTTP handler.
func (s *Server) Handler() http.Handler { return s.http }

// handleNotFound renders the panel's 404 page for paths the panel does not
// own. PocketBase's API and dashboard prefixes are routed away before reaching
// Gin, so anything here is genuinely unknown.
func (s *Server) handleNotFound(c *gin.Context) {
	s.renderErrorPage(c, http.StatusNotFound, "Page not found",
		"The page you requested does not exist. Check the address and try again.")
}

// renderErrorPage renders the panel's error view, or bounces anonymous visitors
// to the login page so the panel's shape is not disclosed.
func (s *Server) renderErrorPage(c *gin.Context, code int, title, detail string) {
	if s.currentUser(c) == nil && s.currentUserFromSession(c) == nil {
		s.redirectToLogin(c)
		return
	}

	s.render(c, code, "error.html", errorPage{
		base:    s.newBase(c, title, ""),
		Code:    code,
		Message: title,
		Detail:  detail,
	})
}

// pageBuffer collects a rendered page so it can be inspected before it is sent.
//
// It exists because render.Render writes to an http.ResponseWriter: catching a
// template error before the status is committed needs somewhere to write that is
// not the client.
type pageBuffer struct {
	bytes.Buffer
	status int
	header http.Header
}

func newPageBuffer() *pageBuffer {
	return &pageBuffer{status: http.StatusOK, header: make(http.Header)}
}

func (b *pageBuffer) Header() http.Header { return b.header }

func (b *pageBuffer) WriteHeader(status int) { b.status = status }

func (b *pageBuffer) Write(p []byte) (int, error) { return b.Buffer.Write(p) }

// render writes a page using the per-page template set registered in
// htmlRenderer.
//
// The page is rendered into a buffer before anything is written. Gin streams a
// template straight to the response, so an execution error halfway down produces
// a 200 with a half-drawn page: the browser shows a layout that simply stops, and
// nothing in the response says why. Buffering turns that into a 500 and a logged
// error while the headers can still be changed.
func (s *Server) render(c *gin.Context, status int, name string, data any) {
	buf := newPageBuffer()

	if err := s.http.HTMLRender.Instance(name, data).Render(buf); err != nil {
		slog.Error("could not render a page", "template", name, "error", err)
		c.String(http.StatusInternalServerError, "Could not render this page.")
		return
	}

	for key, values := range buf.header {
		for _, v := range values {
			c.Writer.Header().Add(key, v)
		}
	}

	c.Data(status, "text/html; charset=utf-8", buf.Bytes())
}

// newBase assembles the data shared by every page.
func (s *Server) newBase(c *gin.Context, title, active string) base {
	actor := s.actor(c)
	if actor == nil {
		// Pages rendered before the auth middleware runs (the login form) have no
		// actor yet.
		if rec := s.currentUserFromSession(c); rec != nil {
			actor = &rbac.Actor{UserID: rec.Id, Email: rec.Email(), IsSuperuser: true}
		}
	}

	email := ""
	if actor != nil {
		email = actor.Email
	}

	csrf, _ := c.Get(ctxCSRF)
	csrfToken, _ := csrf.(string)

	return base{
		Title:             title,
		Active:            active,
		CSRF:              csrfToken,
		UserEmail:         email,
		AppName:           s.cfg.AppName,
		PBVersion:         version.PocketBase(),
		PanelVer:          version.Panel,
		Nav:               s.navCollections(),
		Flash:             s.flashFrom(c),
		Actor:             actor,
		Perms:             actorPermissions(actor),
		Superuser:         actor != nil && actor.IsSuperuser,
		Menus:             s.navFromActor(c),
		Theme:             s.preferenceTheme(c),
		SidebarCollapsed:  s.preferenceSidebarCollapsed(c),
		Impersonating:     s.impersonatorID(c) != "",
		ImpersonatorEmail: s.impersonatorEmail(c),
	}
}

// navCollections lists the collections shown in the sidebar.
func (s *Server) navCollections() []navCollection {
	collections, err := s.app.FindAllCollections()
	if err != nil {
		return nil
	}

	out := make([]navCollection, 0, len(collections))
	for _, col := range collections {
		if hiddenCollections[col.Name] {
			continue
		}
		out = append(out, navCollection{
			Name:   col.Name,
			Type:   col.Type,
			System: col.System,
		})
	}

	sort.SliceStable(out, func(i, j int) bool {
		if out[i].System != out[j].System {
			return !out[i].System // user collections first
		}
		return out[i].Name < out[j].Name
	})

	return out
}

// ---------------------------------------------------------------------------
// Shared request helpers
// ---------------------------------------------------------------------------

// findCollection resolves the :collection route parameter and writes a 404
// response when it is unknown.
func (s *Server) findCollection(c *gin.Context) (*core.Collection, bool) {
	name := c.Param("collection")

	col, err := s.app.FindCollectionByNameOrId(name)
	if err != nil {
		s.renderErrorPage(c, http.StatusNotFound, "Collection not found",
			fmt.Sprintf("No collection named %q exists.", name))
		return nil, false
	}

	return col, true
}

func atoiDefault(raw string, fallback int) int {
	n, err := strconv.Atoi(raw)
	if err != nil {
		return fallback
	}
	return n
}
