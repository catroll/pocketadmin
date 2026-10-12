// Command pocketadmin serves an AdminLTE management panel on top of an
// embedded PocketBase instance.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	pbcmd "github.com/pocketbase/pocketbase/cmd"

	"pocketadmin/internal/config"
	"pocketadmin/internal/logging"
	"pocketadmin/internal/pbapp"
	"pocketadmin/internal/server"

	// Registering the platform's schema migrations is a side effect of the
	// package's init functions.
	_ "pocketadmin/migrations"
)

func main() {
	// Install JSON logging on stdout before anything else so that even a
	// configuration failure is reported in the expected format.
	if _, err := logging.Setup(config.DefaultLogLevel, config.DefaultLogFormat); err != nil {
		panic(err)
	}

	if err := run(); err != nil {
		slog.Error("fatal", "error", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load(os.Args[1:])
	if err != nil {
		return err
	}

	// Re-install the logger with the level and format from the configuration.
	logger, err := logging.Setup(cfg.Log.Level, cfg.Log.Format)
	if err != nil {
		return err
	}

	// Normalise argv before PocketBase's cobra command tree sees it. Cobra only
	// skips a flag's value for "--flag value" and "-f value", so a single-dash
	// long flag such as "-dir /path" would leave "/path" looking like a
	// subcommand. Passing the resolved values as "--flag=value" removes the
	// ambiguity entirely.
	os.Args = normaliseArgs(cfg)

	// PocketBase owns the database, migrations, settings and REST API.
	app := pbapp.New(cfg.DataDir, cfg.Dev)

	// When a subcommand is given (eg. `pocketadmin superuser upsert ...`) hand
	// control to PocketBase's own CLI so the database can be bootstrapped
	// before the panel ever needs to serve a login.
	if len(cfg.Args) > 0 {
		app.RootCmd.AddCommand(pbcmd.NewSuperuserCommand(app))
		return app.Execute()
	}

	if err := app.Bootstrap(); err != nil {
		return err
	}
	defer func() {
		if err := app.ClearBootstrap(); err != nil {
			logger.Warn("cleanup failed", "error", err)
		}
	}()

	if err := app.RunAllMigrations(); err != nil {
		return err
	}

	api, err := pbapp.APIHandler(app)
	if err != nil {
		return err
	}

	panel, err := server.New(app, cfg, logger)
	if err != nil {
		return err
	}

	// One port, two routers: PocketBase owns its API and bundled dashboard
	// prefixes, Gin serves everything else. Dispatching with a stdlib mux keeps
	// PocketBase requests out of Gin's response writer entirely.
	root := http.NewServeMux()
	for _, prefix := range []string{"/api", "/api/", "/_", "/_/"} {
		root.Handle(prefix, api)
	}
	root.Handle("/", panel.Handler())

	httpServer := &http.Server{
		Addr:              cfg.Addr,
		Handler:           root,
		ReadHeaderTimeout: 15 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	serveErr := make(chan error, 1)
	go func() {
		logger.Info("panel listening",
			"url", "http://"+cfg.Addr,
			"pocketbaseUI", "http://"+cfg.Addr+"/_/",
			"dataDir", cfg.DataDir,
			"configFile", cfg.ConfigFile,
			"logLevel", cfg.Log.Level,
			"logFormat", cfg.Log.Format,
		)

		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErr <- err
		}
	}()

	select {
	case err := <-serveErr:
		return err
	case <-ctx.Done():
		logger.Info("shutdown signal received")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	return httpServer.Shutdown(shutdownCtx)
}

// normaliseArgs rebuilds argv from the resolved configuration so that
// PocketBase's cobra parser cannot mistake a flag value for a subcommand.
func normaliseArgs(cfg *config.Config) []string {
	args := []string{os.Args[0], "--dir=" + cfg.DataDir}
	if cfg.Dev {
		args = append(args, "--dev")
	}
	return append(args, cfg.Args...)
}
