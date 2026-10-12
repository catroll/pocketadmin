package server

import (
	"log/slog"
	"net/http"
	"runtime/debug"

	"github.com/gin-gonic/gin"
	"github.com/pocketbase/pocketbase/core"

	"pocketadmin/internal/audit"
)

// exceptionRecovery replaces gin.Recovery with one that records the panic.
//
// gin.Recovery prints to stderr and returns 500. That is enough to keep serving,
// but a panic is the most important thing an operator can be told about and a
// stderr line scrolls away. Recording it makes it visible on the exceptions
// screen alongside the request that caused it.
func (s *Server) exceptionRecovery() gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			recovered := recover()
			if recovered == nil {
				return
			}

			stack := debug.Stack()
			s.recordException(c, "panic", recovered, stack)

			// The same behaviour as gin.Recovery: the request fails, the server
			// keeps running.
			slog.Error("recovered from a panic",
				"path", c.Request.URL.Path, "error", recovered,
				"requestId", requestIDFrom(c))

			if !c.Writer.Written() {
				c.AbortWithStatus(http.StatusInternalServerError)
			} else {
				c.Abort()
			}
		}()

		c.Next()
	}
}

// recordException writes one entry to the exception log.
func (s *Server) recordException(c *gin.Context, level string, cause any, stack []byte) {
	col, err := s.app.FindCollectionByNameOrId("exception_logs")
	if err != nil {
		slog.Error("the exception log collection is missing; an exception was not recorded",
			"error", err)
		return
	}

	rec := core.NewRecord(col)
	rec.Set("level", level)
	rec.Set("message", truncate(describeCause(cause), 2000))
	rec.Set("stack", truncate(string(stack), 64000))
	rec.Set("module", moduleFromPath(c.Request.URL.Path))
	rec.Set("path", c.Request.URL.Path)
	rec.Set("method", c.Request.Method)
	rec.Set("requestId", requestIDFrom(c))
	rec.Set("ip", c.ClientIP())
	rec.Set("userAgent", truncate(c.Request.UserAgent(), sessionUserAgentMaxLen))
	rec.Set("resolved", false)

	if actor := s.actor(c); actor != nil && !actor.IsSuperuser {
		rec.Set("user", actor.UserID)
	}

	// Query parameters, redacted: a stack trace alone does not say what was asked
	// for, and the parameters are often the reason it failed.
	if c.Request.URL.RawQuery != "" {
		rec.Set("params", audit.Redact(map[string]any{"query": c.Request.URL.RawQuery}))
	}

	if err := s.app.Save(rec); err != nil {
		slog.Error("could not write an exception log entry", "error", err)
	}
}

// describeCause renders a recovered value as a message.
func describeCause(cause any) string {
	switch v := cause.(type) {
	case error:
		return v.Error()
	case string:
		return v
	default:
		return "non-error panic"
	}
}
