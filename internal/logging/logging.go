// Package logging installs the application's structured logger.
//
// Every application log is emitted through log/slog as one JSON object per
// line on stdout, so the output can be collected and queried without a parsing
// layer. PocketBase's own operational logs are independent of this: it always
// persists them to the "_logs" collection and only echoes them to stdout in dev
// mode.
package logging

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// Setup builds a slog logger that writes to stdout, installs it as the process
// default and returns it.
//
// level accepts debug, info, warn/warning or error (empty means info). format
// accepts json (the default when empty) or text.
func Setup(level, format string) (*slog.Logger, error) {
	lvl, err := parseLevel(level)
	if err != nil {
		return nil, err
	}

	opts := &slog.HandlerOptions{Level: lvl}

	var handler slog.Handler
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "", "json":
		handler = slog.NewJSONHandler(os.Stdout, opts)
	case "text":
		handler = slog.NewTextHandler(os.Stdout, opts)
	default:
		return nil, fmt.Errorf("unsupported log format %q: want json or text", format)
	}

	logger := slog.New(handler)
	slog.SetDefault(logger)

	return logger, nil
}

func parseLevel(level string) (slog.Level, error) {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "", "info":
		return slog.LevelInfo, nil
	case "debug":
		return slog.LevelDebug, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return 0, fmt.Errorf("unsupported log level %q: want debug, info, warn or error", level)
	}
}

// RequestLogger returns Gin middleware that records every request as a single
// structured entry, replacing gin.Logger's plain-text output.
//
// The log level follows the response status: 5xx logs at error, 4xx at warn
// and everything else at info.
func RequestLogger(logger *slog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		path := c.Request.URL.Path
		query := c.Request.URL.RawQuery

		c.Next()

		if query != "" {
			path += "?" + query
		}

		// A handler that never wrote a body reports -1; normalise it so the
		// field stays a plain byte count.
		size := c.Writer.Size()
		if size < 0 {
			size = 0
		}

		attrs := []slog.Attr{
			slog.String("method", c.Request.Method),
			slog.String("path", path),
			slog.Int("status", c.Writer.Status()),
			slog.Duration("latency", time.Since(start)),
			slog.String("clientIp", c.ClientIP()),
			slog.Int("bytes", size),
		}

		if errs := c.Errors.ByType(gin.ErrorTypePrivate).String(); errs != "" {
			attrs = append(attrs, slog.String("errors", errs))
		}

		level := slog.LevelInfo
		switch {
		case c.Writer.Status() >= 500:
			level = slog.LevelError
		case c.Writer.Status() >= 400:
			level = slog.LevelWarn
		}

		// Use a background context: the request context is frequently already
		// cancelled by the time the response has been written.
		logger.LogAttrs(context.Background(), level, "http request", attrs...)
	}
}
