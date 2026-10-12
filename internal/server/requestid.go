package server

import (
	"crypto/rand"
	"encoding/hex"

	"github.com/gin-gonic/gin"
)

// ctxRequestID is the request-context key holding the correlation id.
const ctxRequestID = "requestId"

// requestIDHeader is the response header carrying it back to the caller.
const requestIDHeader = "X-Request-Id"

// requestID returns middleware that gives every request a correlation id.
//
// The id is what lets an operation log entry, a security audit entry and an
// exception entry for one request be read together, and it is echoed in a
// response header so a user reporting a problem can quote it. An inbound id is
// honoured so a proxy or a client can supply one, but it is bounded and
// sanitised first: it ends up in log columns and a response header.
func requestID() gin.HandlerFunc {
	return func(c *gin.Context) {
		id := sanitiseRequestID(c.GetHeader(requestIDHeader))
		if id == "" {
			id = newRequestID()
		}

		c.Set(ctxRequestID, id)
		c.Header(requestIDHeader, id)
		c.Next()
	}
}

// requestIDFrom returns the current request's correlation id, or "".
func requestIDFrom(c *gin.Context) string {
	raw, ok := c.Get(ctxRequestID)
	if !ok {
		return ""
	}
	id, _ := raw.(string)
	return id
}

// maxRequestIDLen bounds an inbound id so a caller cannot fill a log column.
const maxRequestIDLen = 64

// sanitiseRequestID keeps an inbound id only when it is plainly safe.
func sanitiseRequestID(raw string) string {
	if raw == "" || len(raw) > maxRequestIDLen {
		return ""
	}
	for _, r := range raw {
		switch {
		case r >= 'a' && r <= 'z',
			r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9',
			r == '-', r == '_':
		default:
			return ""
		}
	}
	return raw
}

// newRequestID returns a random correlation id.
func newRequestID() string {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		// A missing id is better than a failed request; the log entry simply
		// cannot be correlated, which the empty column shows.
		return ""
	}
	return hex.EncodeToString(buf)
}
