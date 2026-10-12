// Package pbapp wires the embedded PocketBase instance.
//
// PocketBase is used as a library: it owns the SQLite database, the data
// directory, migrations, settings and the whole REST API. The admin panel adds
// a Gin-served AdminLTE UI on top and mounts PocketBase's own REST API and
// bundled superuser dashboard on the same port.
package pbapp

import (
	"fmt"
	"net/http"

	"github.com/pocketbase/pocketbase"
	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/ui"
)

// contentSecurityPolicy mirrors the one PocketBase applies to its dashboard.
const contentSecurityPolicy = "default-src 'self'; style-src 'self' 'unsafe-inline'; " +
	"img-src 'self' data: blob:; connect-src 'self'; script-src 'self'; frame-ancestors 'none'"

// New creates a PocketBase instance bound to dataDir.
//
// The returned app is not bootstrapped yet; callers must run Bootstrap and
// RunAllMigrations before serving traffic.
func New(dataDir string, dev bool) *pocketbase.PocketBase {
	return pocketbase.NewWithConfig(pocketbase.Config{
		HideStartBanner: true,
		DefaultDataDir:  dataDir,
		DefaultDev:      dev,
	})
}

// APIHandler builds the PocketBase HTTP handler (REST API + bundled dashboard)
// as a plain http.Handler so it can be mounted inside the Gin router.
//
// This mirrors the route registration that apis.Serve performs, minus the
// listener: the caller owns the server lifecycle.
func APIHandler(app core.App) (http.Handler, error) {
	pbRouter, err := apis.NewRouter(app)
	if err != nil {
		return nil, fmt.Errorf("build pocketbase router: %w", err)
	}

	pbRouter.Bind(apis.CORS(apis.CORSConfig{
		AllowOrigins: []string{"*"},
		AllowMethods: []string{
			http.MethodGet, http.MethodHead, http.MethodPut,
			http.MethodPatch, http.MethodPost, http.MethodDelete,
		},
	}))

	// Serve the embedded superuser dashboard at /_/ when the UI was compiled in.
	if ui.DistDirFS != nil {
		pbRouter.GET("/_/{path...}", apis.Static(ui.DistDirFS, false)).
			BindFunc(func(e *core.RequestEvent) error {
				if !e.App.IsDev() &&
					e.Request.PathValue(apis.StaticWildcardParam) != "" &&
					e.Response.Header().Get("Cache-Control") == "" {
					e.Response.Header().Set("Cache-Control", "max-age=1209600, stale-while-revalidate=86400")
				}

				if e.Response.Header().Get("Content-Security-Policy") == "" {
					e.Response.Header().Set("Content-Security-Policy", contentSecurityPolicy)
				}

				return e.Next()
			}).
			Bind(apis.Gzip())
	}

	handler, err := pbRouter.BuildMux()
	if err != nil {
		return nil, fmt.Errorf("build pocketbase mux: %w", err)
	}

	return handler, nil
}
