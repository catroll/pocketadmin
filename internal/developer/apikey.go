// Package developer holds the machine-facing surface: API keys and the reference
// for the API they open.
//
// A key is a credential, so the properties that matter are the ones that limit
// what a leaked key can do and how long it can do it: only a hash is stored, the
// authority is the intersection of the owner's live permissions and the key's own
// scopes, and every key can be bounded by an expiry, a rate and a set of source
// addresses.
package developer

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/pocketbase/pocketbase/core"

	"pocketadmin/internal/permission"
)

// Collection name.
const CollectionAPIKeys = "api_keys"

// Statuses.
const (
	StatusActive  = "active"
	StatusRevoked = "revoked"
	StatusExpired = "expired"
)

// Prefix is the literal that starts every key this platform issues.
//
// A recognisable prefix is what lets a secret scanner find one in a repository
// before somebody has to notice it was committed.
const Prefix = "pak_"

// keyBytes is the entropy of a key. 32 bytes is well beyond what is brute-forced
// and keeps the encoded form short enough to paste.
const keyBytes = 32

// prefixLength is how many characters are stored in clear for lookup and display.
//
// The prefix is not a secret and is not treated as one: it exists so a key can be
// identified in a list and found in a database without holding the key. Eight
// characters of a 256-bit key leaves nothing to guess.
const prefixLength = 12

// GeneratedKey is a newly created key.
type GeneratedKey struct {
	// Plaintext is the key. It is returned once, at creation, and is not stored.
	Plaintext string

	// Prefix is the searchable, non-secret part.
	Prefix string

	// Hash is what is stored.
	Hash string
}

// GenerateKey creates a new key.
func GenerateKey() (GeneratedKey, error) {
	buf := make([]byte, keyBytes)
	if _, err := rand.Read(buf); err != nil {
		return GeneratedKey{}, fmt.Errorf("developer: generate a key: %w", err)
	}

	// URL-safe base64 without padding, so a key survives being pasted into a
	// header, a shell and a configuration file unquoted.
	encoded := base64.RawURLEncoding.EncodeToString(buf)
	plaintext := Prefix + encoded

	return GeneratedKey{
		Plaintext: plaintext,
		Prefix:    plaintext[:prefixLength],
		Hash:      HashKey(plaintext),
	}, nil
}

// HashKey returns the stored form of a key.
//
// SHA-256 without a salt is the correct choice here and would be the wrong one
// for a password. A password is short, guessable and reused, so it needs a slow,
// salted hash. A key is 256 bits of randomness, so there is nothing to guess and
// nothing to rainbow-table; a slow hash would only make every API request slower.
func HashKey(plaintext string) string {
	sum := sha256.Sum256([]byte(plaintext))
	return hex.EncodeToString(sum[:])
}

// LooksLikeKey reports whether a string is shaped like a key this platform issues.
//
// It is a cheap check before touching the database, so a request carrying a random
// Authorization header does not become a query.
func LooksLikeKey(value string) bool {
	return strings.HasPrefix(value, Prefix) && len(value) >= prefixLength
}

// FindByPlaintext resolves a key to its record, or reports that it does not exist.
//
// The lookup is by prefix, and the candidate is then compared by hash. Searching
// on the prefix alone would be enough to find the row, but comparing the hash is
// what proves the caller holds the whole key rather than only its visible part.
func FindByPlaintext(app core.App, plaintext string) (*core.Record, error) {
	if !LooksLikeKey(plaintext) {
		return nil, ErrInvalidKey
	}

	rec, err := app.FindFirstRecordByFilter(CollectionAPIKeys,
		"keyPrefix = {:p}", map[string]any{"p": plaintext[:prefixLength]})
	if err != nil || rec == nil {
		return nil, ErrInvalidKey
	}

	// Constant time, because a comparison that leaks how much of a hash matched is
	// a comparison that helps somebody reconstruct one.
	if subtle.ConstantTimeCompare([]byte(rec.GetString("keyHash")), []byte(HashKey(plaintext))) != 1 {
		return nil, ErrInvalidKey
	}

	return rec, nil
}

// ErrInvalidKey is returned when a key is absent, malformed or does not match.
//
// The same error covers every case, because telling a caller that a key exists but
// is wrong is telling them something they should not learn.
var ErrInvalidKey = fmt.Errorf("developer: the API key is not valid")

// UnusableReason reports why a key may not be used, or "".
func UnusableReason(rec *core.Record, now time.Time) string {
	switch rec.GetString("status") {
	case StatusRevoked:
		return "the key has been revoked"
	case StatusExpired:
		return "the key has expired"
	case StatusActive:
	default:
		return "the key is not active"
	}

	if at := rec.GetDateTime("expiresAt"); !at.IsZero() && at.Time().Before(now) {
		return "the key has expired"
	}
	return ""
}

// AllowsAddress reports whether a request from an address may use a key.
//
// An empty list allows any address, which is the documented behaviour and also the
// dangerous one, so it is the caller's decision to display that clearly.
func AllowsAddress(rec *core.Record, ip string) bool {
	raw := strings.TrimSpace(rec.GetString("ipAllowList"))
	if raw == "" {
		return true
	}

	parsed := net.ParseIP(strings.TrimSpace(ip))
	if parsed == nil {
		return false
	}

	for _, entry := range strings.Split(raw, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		if strings.Contains(entry, "/") {
			if _, network, err := net.ParseCIDR(entry); err == nil && network.Contains(parsed) {
				return true
			}
			continue
		}
		if allowed := net.ParseIP(entry); allowed != nil && allowed.Equal(parsed) {
			return true
		}
	}
	return false
}

// ScopedPermissions returns the permissions a key actually carries.
//
// It is the intersection of the owner's live permissions and the key's declared
// scopes, and the intersection is the whole point: a scope can only ever narrow
// what the owner has. Removing a permission from the owner narrows their keys
// immediately, without anybody having to remember to edit them, and a scope for a
// permission the owner does not hold grants nothing.
//
// A permission granted to a key but not to its owner is the failure this prevents:
// a key is a way for a person to act, not a way to become somebody else.
func ScopedPermissions(ownerPermissions permission.Set, scopes []string) permission.Set {
	out := make(permission.Set, len(scopes))

	for _, code := range scopes {
		code = strings.TrimSpace(code)
		if code == "" {
			continue
		}
		if ownerPermissions.Has(code) {
			out[code] = struct{}{}
		}
	}
	return out
}

// RateLimitFor returns the effective requests-per-minute for a key.
//
// A key with no limit of its own takes the configured default. A zero default
// means no limit, which is what an installation that has not thought about it
// gets -- the alternative, a silent small number, would break a working
// integration on upgrade.
func RateLimitFor(rec *core.Record, fallback int) int {
	if own := rec.GetInt("rateLimitPerMinute"); own > 0 {
		return own
	}
	return fallback
}

// RecordUse notes that a key was used.
//
// The write is best-effort: a failure to count a request must not fail the
// request, or the bookkeeping becomes an outage.
func RecordUse(app core.App, rec *core.Record, ip string) {
	usage := rec.GetInt("usageCount") + 1
	rec.Set("usageCount", usage)
	rec.Set("lastUsedAt", time.Now().UTC())
	rec.Set("lastUsedIp", ip)

	if err := app.Save(rec); err != nil {
		// Deliberately not returned. A key that works but is not counted is a
		// smaller problem than a working integration that stops.
		return
	}
}

// Revoke marks a key unusable.
//
// The row is kept rather than deleted: a revoked key is the record that access was
// withdrawn, and deleting it would lose the answer to "did this credential ever
// exist".
func Revoke(app core.App, rec *core.Record, byUserID string) error {
	if rec.GetString("status") == StatusRevoked {
		return nil
	}

	rec.Set("status", StatusRevoked)
	rec.Set("revokedAt", time.Now().UTC())
	if byUserID != "" {
		rec.Set("revokedBy", byUserID)
	}
	return app.Save(rec)
}

// ExpireStale marks keys whose expiry has passed and reports how many.
//
// The expiry is enforced on every request anyway, so this is about the list
// telling the truth: a key shown as active that cannot be used is a key an
// operator will waste time investigating.
func ExpireStale(app core.App, now time.Time) (int, error) {
	records, err := app.FindRecordsByFilter(CollectionAPIKeys,
		"status = 'active' && expiresAt != '' && expiresAt <= {:now}",
		"expiresAt", 0, 0, map[string]any{"now": now.UTC()})
	if err != nil {
		return 0, fmt.Errorf("developer: find expired keys: %w", err)
	}

	expired := 0
	for _, rec := range records {
		rec.Set("status", StatusExpired)
		if err := app.Save(rec); err != nil {
			return expired, fmt.Errorf("developer: expire %s: %w", rec.Id, err)
		}
		expired++
	}
	return expired, nil
}
