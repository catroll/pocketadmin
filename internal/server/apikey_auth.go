package server

import (
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/pocketbase/pocketbase/core"

	"pocketadmin/internal/developer"
	"pocketadmin/internal/rbac"
)

// ctxAPIKey holds the key record a request authenticated with.
const ctxAPIKey = "apiKey"

// apiKey returns the key record behind the current request, or nil.
func (s *Server) apiKey(c *gin.Context) *core.Record {
	raw, ok := c.Get(ctxAPIKey)
	if !ok {
		return nil
	}
	rec, _ := raw.(*core.Record)
	return rec
}

// requireAPIKey authenticates a machine caller.
//
// The order of the checks is deliberate. The key is resolved first, because
// everything else needs it. The key's own state is checked next, so a revoked or
// expired credential is refused before its address or its rate is considered. The
// address is checked before the rate, because a rejected address should not consume
// the allowance of a key it is not allowed to use. The rate is checked last, so
// only a request that would otherwise be served is counted.
//
// Every failure returns the same shape of body. The reason is included because it
// helps an integrator and reveals nothing: the caller already holds the key, so
// telling them it was revoked or that their address is not allowed is telling them
// about their own credential.
func (s *Server) requireAPIKey() gin.HandlerFunc {
	return func(c *gin.Context) {
		raw := bearerToken(c)
		if raw == "" {
			abortAPI(c, http.StatusUnauthorized, "missing_credentials",
				"Provide an API key as a bearer token.")
			return
		}

		rec, err := developer.FindByPlaintext(s.app, raw)
		if err != nil {
			// A malformed key and an unknown one are the same answer: the caller
			// learns nothing about which keys exist.
			abortAPI(c, http.StatusUnauthorized, "invalid_key", "That API key is not valid.")
			return
		}

		now := time.Now()
		if reason := developer.UnusableReason(rec, now); reason != "" {
			abortAPI(c, http.StatusUnauthorized, "key_unusable", reason)
			return
		}

		if !developer.AllowsAddress(rec, c.ClientIP()) {
			abortAPI(c, http.StatusForbidden, "address_not_allowed",
				"This key may not be used from this address.")
			return
		}

		limit := developer.RateLimitFor(rec, s.cfg.APIKeyRateLimit)
		if allowed, retryAfter := s.apiKeyLimiter.Allow(rec.Id, limit, now); !allowed {
			c.Header("Retry-After", strconv.Itoa(int(retryAfter.Seconds())+1))
			abortAPI(c, http.StatusTooManyRequests, "rate_limited",
				"This key has reached its rate limit.")
			return
		}

		actor, err := s.apiKeyActor(rec)
		if err != nil {
			// The owner has lost the profile the key depends on. Refusing is the
			// right answer: a key is a way for a person to act, and there is no
			// longer a person it can act for.
			abortAPI(c, http.StatusForbidden, "owner_unavailable",
				"The account this key belongs to is no longer active.")
			return
		}

		c.Set(ctxAPIKey, rec)
		c.Set(ctxActor, actor)

		developer.RecordUse(s.app, rec, c.ClientIP())
		c.Next()
	}
}

// apiKeyActor builds the authority a key carries.
//
// It is the intersection of the owner's live permissions and the key's scopes, and
// the intersection is the point. A scope can only ever narrow what the owner has:
// withdrawing a permission from the owner narrows every key they issued
// immediately, without anybody remembering to edit them, and a scope naming a
// permission the owner does not hold grants nothing.
//
// Without the intersection a key would be a way to become somebody else rather
// than a way for a person to act, which is the whole reason a scope list is not a
// permission list.
func (s *Server) apiKeyActor(rec *core.Record) (*rbac.Actor, error) {
	owner, err := s.app.FindRecordById("users", rec.GetString("owner"))
	if err != nil || owner == nil {
		return nil, developer.ErrInvalidKey
	}

	ownerActor, err := s.resolver.Resolve(owner)
	if err != nil || ownerActor == nil {
		return nil, developer.ErrInvalidKey
	}

	scopes := permissionCodesIn(s.app, rec.GetStringSlice("scopes"))
	effective := developer.ScopedPermissions(ownerActor.Permissions, scopes)

	// The owner's profile fields are carried over, not only the permissions.
	// Can() answers false for an actor whose admin profile is missing or inactive
	// whatever its permission set holds, so an actor assembled from the scope list
	// alone denies everything: the key appears to carry authority and can use none
	// of it.
	return &rbac.Actor{
		UserID:         owner.Id,
		Email:          owner.Email(),
		Name:           ownerActor.Name,
		AdminProfileID: ownerActor.AdminProfileID,
		AdminStatus:    ownerActor.AdminStatus,
		RoleCodes:      ownerActor.RoleCodes,
		Permissions:    effective,

		// A key is never a superuser, whatever its owner is. A break-glass account
		// issuing a key would otherwise hand out its own unlimited authority with
		// no scope able to narrow it.
		IsSuperuser: false,
	}, nil
}

// permissionCodesIn resolves permission record ids to their codes.
func permissionCodesIn(app core.App, ids []string) []string {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		rec, err := app.FindRecordById("permissions", id)
		if err != nil || rec == nil {
			continue
		}
		out = append(out, rec.GetString("code"))
	}
	return out
}

// bearerToken reads a bearer token from the Authorization header.
func bearerToken(c *gin.Context) string {
	header := strings.TrimSpace(c.GetHeader("Authorization"))
	if header == "" {
		return ""
	}

	// The scheme is case-insensitive per RFC 7235, and a client that sends
	// "bearer" is not wrong.
	if len(header) > 7 && strings.EqualFold(header[:7], "bearer ") {
		return strings.TrimSpace(header[7:])
	}
	return ""
}

// abortAPI writes a JSON error and stops the chain.
func abortAPI(c *gin.Context, status int, code, message string) {
	// A machine caller gets JSON, never a redirect to a sign-in page: a client
	// following that redirect would report a parse failure rather than an auth
	// failure.
	c.AbortWithStatusJSON(status, gin.H{
		"error": gin.H{"code": code, "message": message},
	})
}

var _ = slog.Info
