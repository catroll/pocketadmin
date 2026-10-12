package server

import (
	"bytes"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/pocketbase/pocketbase/core"

	"pocketadmin/internal/audit"
)

// maxLoggedBody bounds how much of a request body is read for the log.
//
// The body has to be read to be redacted, and reading an unbounded one into
// memory to write a log line would make the log a denial-of-service vector.
const maxLoggedBody = 8 << 10

// operationLogContentTypes are the bodies worth reading. A multipart upload is
// not: the interesting part is the field names, and the bytes are the file.
func loggableContentType(ct string) bool {
	ct = strings.ToLower(ct)
	return strings.Contains(ct, "application/json") ||
		strings.Contains(ct, "application/x-www-form-urlencoded")
}

// operationLog records every state-changing request.
//
// It is deliberately a middleware rather than a call in each handler: a handler
// that forgets to log is a gap nobody notices, and the log's value is that it is
// complete. Reads are not recorded -- they change nothing, and logging every page
// view would bury the entries that matter.
func (s *Server) operationLog() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !isUnsafeMethod(c.Request.Method) {
			c.Next()
			return
		}

		start := time.Now()

		// The body is read for the log and put back for the handler. A handler
		// that could not read the body it was sent would be a worse bug than a
		// missing log line.
		body := readAndRestoreBody(c)

		c.Next()

		s.writeOperationLog(c, body, time.Since(start))
	}
}

// readAndRestoreBody reads a bounded prefix of the body and restores it.
func readAndRestoreBody(c *gin.Context) []byte {
	if c.Request.Body == nil || !loggableContentType(c.GetHeader("Content-Type")) {
		return nil
	}

	limited := io.LimitReader(c.Request.Body, maxLoggedBody)
	body, err := io.ReadAll(limited)
	if err != nil {
		return nil
	}

	// Restore what was read, followed by the rest of the original stream, so the
	// handler sees the whole body.
	c.Request.Body = io.NopCloser(io.MultiReader(bytes.NewReader(body), c.Request.Body))
	return body
}

// writeOperationLog persists one entry.
func (s *Server) writeOperationLog(c *gin.Context, body []byte, elapsed time.Duration) {
	col, err := s.app.FindCollectionByNameOrId("operation_logs")
	if err != nil {
		slog.Error("the operation log collection is missing; a request was not recorded", "error", err)
		return
	}

	status := c.Writer.Status()

	rec := core.NewRecord(col)
	rec.Set("module", moduleFromPath(c.Request.URL.Path))
	rec.Set("action", c.Request.Method+" "+c.FullPath())
	rec.Set("method", c.Request.Method)
	rec.Set("path", c.Request.URL.Path)
	rec.Set("statusCode", status)
	rec.Set("result", resultForStatus(status))
	rec.Set("durationMs", elapsed.Milliseconds())
	rec.Set("ip", c.ClientIP())
	rec.Set("userAgent", truncate(c.Request.UserAgent(), sessionUserAgentMaxLen))
	rec.Set("requestId", requestIDFrom(c))

	actorType, actorID, actorEmail := s.auditActor(c)
	rec.Set("actorType", string(actorType))
	if actorID != "" {
		rec.Set("user", actorID)
	}
	rec.Set("actorEmail", actorEmail)

	if status >= 400 {
		rec.Set("errorMessage", truncate(http.StatusText(status), 255))
	}

	// Query parameters are recorded too: a delete with a confirmation flag, a
	// filter, a page -- they are part of what the request did.
	params := map[string]any{}
	if c.Request.URL.RawQuery != "" {
		params["query"] = c.Request.URL.RawQuery
	}
	if len(body) > 0 {
		// A urlencoded body is parsed into fields rather than stored as one
		// string. Storing it raw would put the field names out of the redactor's
		// reach, and the form encoding is the one a browser actually uses -- so
		// the secrets most likely to appear are exactly the ones it would miss.
		if fields, ok := parseFormBody(c.GetHeader("Content-Type"), body); ok {
			for key, values := range fields {
				if len(values) == 1 {
					params[key] = values[0]
					continue
				}
				params[key] = values
			}
		} else {
			params["body"] = string(body)
		}
	}
	// A multipart body is parsed by the handler rather than read here, so its
	// fields are collected afterwards. Only the field names and values are
	// recorded: the file contents are the upload, already stored elsewhere, and
	// copying them into a log column would be a second copy of every attachment.
	for key, values := range multipartFields(c) {
		if _, exists := params[key]; exists {
			continue
		}
		if len(values) == 1 {
			params[key] = values[0]
			continue
		}
		params[key] = values
	}
	for _, name := range multipartFileNames(c) {
		params[name] = "[file]"
	}

	if len(params) > 0 {
		// Redacted before storage: a secret that reaches a log has leaked.
		rec.Set("params", audit.Redact(params))
	}

	if err := s.app.Save(rec); err != nil {
		slog.Error("could not write an operation log entry",
			"path", c.Request.URL.Path, "error", err)
	}
}

// resultForStatus maps a response status onto the log's result vocabulary.
func resultForStatus(status int) string {
	switch {
	case status == http.StatusForbidden || status == http.StatusUnauthorized:
		return "denied"
	case status >= 400:
		return "failure"
	default:
		return "success"
	}
}

// moduleFromPath reads the module from the first path segment.
func moduleFromPath(p string) string {
	trimmed := strings.Trim(p, "/")
	if trimmed == "" {
		return ""
	}
	if i := strings.IndexByte(trimmed, '/'); i >= 0 {
		return trimmed[:i]
	}
	return trimmed
}

// multipartFields returns the non-file form fields of a parsed multipart request.
func multipartFields(c *gin.Context) map[string][]string {
	if c.Request.MultipartForm == nil {
		return nil
	}
	return c.Request.MultipartForm.Value
}

// multipartFileNames returns the names of the uploaded file fields. The contents
// are not read.
func multipartFileNames(c *gin.Context) []string {
	if c.Request.MultipartForm == nil || len(c.Request.MultipartForm.File) == 0 {
		return nil
	}
	names := make([]string, 0, len(c.Request.MultipartForm.File))
	for name := range c.Request.MultipartForm.File {
		names = append(names, name)
	}
	return names
}

// parseFormBody decodes a urlencoded body into fields.
//
// It reports false for anything else, so a JSON body the redactor can already
// walk is left as text rather than mangled by a form parser that does not apply
// to it.
func parseFormBody(contentType string, body []byte) (map[string][]string, bool) {
	if !strings.Contains(strings.ToLower(contentType), "application/x-www-form-urlencoded") {
		return nil, false
	}

	values, err := url.ParseQuery(string(body))
	if err != nil {
		// A malformed body is still worth recording, but not as fields: the
		// parse failed, so the field names cannot be trusted to be field names.
		return nil, false
	}
	return values, true
}
