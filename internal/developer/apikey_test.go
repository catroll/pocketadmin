package developer_test

import (
	"strings"
	"testing"
	"time"

	"github.com/pocketbase/pocketbase/core"

	"pocketadmin/internal/developer"
	"pocketadmin/internal/permission"
	"pocketadmin/internal/testsupport"
)

// TestGeneratedKeyIsStoredOnlyAsAHash is the property that makes a database dump
// not a set of working credentials.
func TestGeneratedKeyIsStoredOnlyAsAHash(t *testing.T) {
	first, err := developer.GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	second, err := developer.GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}

	if first.Plaintext == second.Plaintext {
		t.Fatal("two generated keys are identical")
	}
	if !strings.HasPrefix(first.Plaintext, developer.Prefix) {
		t.Errorf("the key does not carry the recognisable prefix: %q", first.Plaintext)
	}
	if !strings.HasPrefix(first.Plaintext, first.Prefix) {
		t.Error("the stored prefix is not a prefix of the key")
	}
	if first.Hash == first.Plaintext {
		t.Error("the hash is the key")
	}
	if strings.Contains(first.Hash, first.Plaintext) {
		t.Error("the plaintext appears inside the hash")
	}
	if len(first.Hash) != 64 {
		t.Errorf("the hash is %d characters, want 64 hex characters", len(first.Hash))
	}
	if developer.HashKey(first.Plaintext) != first.Hash {
		t.Error("hashing the key does not reproduce the stored hash")
	}

	// Two different keys must not collide on the display prefix either, or the
	// list would show the same identifier for different credentials.
	if first.Prefix == second.Prefix {
		t.Error("two keys share a prefix")
	}
}

// TestLooksLikeKeyRejectsForeignSecrets: a bearer token that is not ours should
// not become a query.
func TestLooksLikeKeyRejectsForeignSecrets(t *testing.T) {
	valid, _ := developer.GenerateKey()

	if !developer.LooksLikeKey(valid.Plaintext) {
		t.Error("a generated key was not recognised")
	}
	for _, value := range []string{
		"", "pak_", "pak_short", "sk-live-abcdefghijklmnop",
		"eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.payload.signature",
	} {
		if developer.LooksLikeKey(value) {
			t.Errorf("%q was accepted as one of our keys", value)
		}
	}
}

// setup creates an app with one account and returns it.
func setup(t *testing.T) (core.App, *core.Record) {
	t.Helper()

	app := testsupport.NewApp(t)

	users, _ := app.FindCollectionByNameOrId("users")
	user := core.NewRecord(users)
	user.Set("email", "owner@example.com")
	user.Set("password", "change-me-please")
	user.Set("passwordConfirm", "change-me-please")
	user.Set("status", "active")
	if err := app.Save(user); err != nil {
		t.Fatalf("save user: %v", err)
	}
	return app, user
}

// createKey writes a key record and returns it.
func createKey(t *testing.T, app core.App, ownerID, ipList string, expiresAt time.Time) *core.Record {
	t.Helper()

	col, _ := app.FindCollectionByNameOrId(developer.CollectionAPIKeys)
	generated, _ := developer.GenerateKey()

	rec := core.NewRecord(col)
	rec.Set("name", "test key")
	rec.Set("keyPrefix", generated.Prefix)
	rec.Set("keyHash", generated.Hash)
	rec.Set("owner", ownerID)
	rec.Set("status", developer.StatusActive)
	rec.Set("ipAllowList", ipList)
	if !expiresAt.IsZero() {
		rec.Set("expiresAt", expiresAt.UTC())
	}
	if err := app.Save(rec); err != nil {
		t.Fatalf("save key: %v", err)
	}
	return rec
}

// TestScopesAreTheIntersectionWithTheOwner is the security property the whole
// scoping design rests on.
//
// A scope can only ever narrow what the owner has. Without the intersection a key
// would be a way to become somebody else rather than a way for a person to act.
func TestScopesAreTheIntersectionWithTheOwner(t *testing.T) {
	owner := permission.NewSet("announcement:announcement:list", "jobs:job:list")

	cases := []struct {
		name   string
		scopes []string
		want   []string
	}{
		{
			name:   "within the owner",
			scopes: []string{"announcement:announcement:list"},
			want:   []string{"announcement:announcement:list"},
		},
		{
			name:   "a subset of the owner",
			scopes: []string{"announcement:announcement:list", "jobs:job:list"},
			want:   []string{"announcement:announcement:list", "jobs:job:list"},
		},
		{
			name:   "beyond the owner",
			scopes: []string{"rbac:role:update"},
			want:   nil,
		},
		{
			name:   "partly beyond the owner",
			scopes: []string{"announcement:announcement:list", "rbac:role:update"},
			want:   []string{"announcement:announcement:list"},
		},
		{
			name:   "nothing at all",
			scopes: nil,
			want:   nil,
		},
		{
			name:   "an owner with no permissions grants nothing",
			scopes: []string{"announcement:announcement:list"},
			want:   nil,
		},
	}

	for _, tc := range cases {
		held := owner
		if tc.name == "an owner with no permissions grants nothing" {
			held = permission.NewSet()
		}

		got := developer.ScopedPermissions(held, tc.scopes)
		if len(got) != len(tc.want) {
			t.Errorf("%s: got %d permissions %v, want %v", tc.name, len(got), got.Codes(), tc.want)
			continue
		}
		for _, code := range tc.want {
			if !got.Has(code) {
				t.Errorf("%s: %q is missing from %v", tc.name, code, got.Codes())
			}
		}
	}
}

// TestUnusableReasonCoversEveryRefusal: the reasons are what an integrator sees,
// so each state has to produce one.
func TestUnusableReasonCoversEveryRefusal(t *testing.T) {
	app, owner := setup(t)
	now := time.Now()

	active := createKey(t, app, owner.Id, "", time.Time{})
	if reason := developer.UnusableReason(active, now); reason != "" {
		t.Errorf("an active key was refused: %q", reason)
	}

	revoked := createKey(t, app, owner.Id, "", time.Time{})
	revoked.Set("status", developer.StatusRevoked)
	_ = app.Save(revoked)
	if reason := developer.UnusableReason(revoked, now); reason == "" {
		t.Error("a revoked key was accepted")
	}

	expired := createKey(t, app, owner.Id, "", now.Add(-time.Hour))
	if reason := developer.UnusableReason(expired, now); reason == "" {
		t.Error("a key whose expiry has passed was accepted")
	}

	future := createKey(t, app, owner.Id, "", now.Add(time.Hour))
	if reason := developer.UnusableReason(future, now); reason != "" {
		t.Errorf("a key that has not expired was refused: %q", reason)
	}
}

// TestAddressAllowList: an empty list allows any address, which is the documented
// behaviour and the dangerous one, so the rest has to be exact.
func TestAddressAllowList(t *testing.T) {
	app, owner := setup(t)

	cases := []struct {
		list string
		ip   string
		want bool
	}{
		{"", "203.0.113.7", true},
		{"203.0.113.7", "203.0.113.7", true},
		{"203.0.113.7", "203.0.113.8", false},
		{"10.0.0.0/8", "10.4.5.6", true},
		{"10.0.0.0/8", "11.4.5.6", false},
		{"10.0.0.0/8, 203.0.113.7", "203.0.113.7", true},
		{"10.0.0.0/8, 203.0.113.7", "10.1.2.3", true},
		{"10.0.0.0/8, 203.0.113.7", "198.51.100.1", false},
		{"10.0.0.0/8", "not-an-address", false},
		{" 10.0.0.0/8 , 203.0.113.7 ", "10.9.9.9", true},
		// A malformed entry must not open the gate for everything else.
		{"nonsense", "203.0.113.7", false},
		{"10.0.0.0/8, nonsense", "203.0.113.7", false},
	}

	for _, tc := range cases {
		rec := createKey(t, app, owner.Id, tc.list, time.Time{})
		if got := developer.AllowsAddress(rec, tc.ip); got != tc.want {
			t.Errorf("list %q with address %q = %v, want %v", tc.list, tc.ip, got, tc.want)
		}
	}
}

// TestRateLimitFallsBackToTheDefault: a key with no limit of its own takes the
// configured one, and zero means no limit.
func TestRateLimitFallsBackToTheDefault(t *testing.T) {
	app, owner := setup(t)

	own := createKey(t, app, owner.Id, "", time.Time{})
	own.Set("rateLimitPerMinute", 25)
	_ = app.Save(own)

	if got := developer.RateLimitFor(own, 100); got != 25 {
		t.Errorf("a key with its own limit reported %d, want 25", got)
	}

	inherited := createKey(t, app, owner.Id, "", time.Time{})
	if got := developer.RateLimitFor(inherited, 100); got != 100 {
		t.Errorf("a key with no limit reported %d, want the default 100", got)
	}
	if got := developer.RateLimitFor(inherited, 0); got != 0 {
		t.Errorf("a zero default reported %d, want 0 (no limit)", got)
	}
}

// TestFindByPlaintextResolvesAndRefuses covers the lookup path.
func TestFindByPlaintextResolvesAndRefuses(t *testing.T) {
	app, owner := setup(t)

	generated, _ := developer.GenerateKey()
	col, _ := app.FindCollectionByNameOrId(developer.CollectionAPIKeys)
	rec := core.NewRecord(col)
	rec.Set("name", "lookup")
	rec.Set("keyPrefix", generated.Prefix)
	rec.Set("keyHash", generated.Hash)
	rec.Set("owner", owner.Id)
	rec.Set("status", developer.StatusActive)
	if err := app.Save(rec); err != nil {
		t.Fatalf("save: %v", err)
	}

	found, err := developer.FindByPlaintext(app, generated.Plaintext)
	if err != nil {
		t.Fatalf("FindByPlaintext: %v", err)
	}
	if found.Id != rec.Id {
		t.Error("the wrong key was resolved")
	}

	// A key sharing the prefix but with a different body must not resolve: the
	// prefix is visible in the list, so it cannot be sufficient on its own.
	forged := generated.Prefix + "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	if _, err := developer.FindByPlaintext(app, forged); err == nil {
		t.Error("a forged key body resolved")
	}
	if _, err := developer.FindByPlaintext(app, "sk-something-else"); err == nil {
		t.Error("a foreign secret resolved")
	}
}

// TestRevokeIsIdempotentAndRecorded: a revoked key is the record that access was
// withdrawn, so the row stays.
func TestRevokeIsIdempotentAndRecorded(t *testing.T) {
	app, owner := setup(t)
	rec := createKey(t, app, owner.Id, "", time.Time{})

	if err := developer.Revoke(app, rec, owner.Id); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	if rec.GetString("status") != developer.StatusRevoked {
		t.Errorf("status = %q, want revoked", rec.GetString("status"))
	}
	if rec.GetDateTime("revokedAt").IsZero() {
		t.Error("no revocation time was stamped")
	}

	// A second revoke is not an error, and does not move the time.
	first := rec.GetDateTime("revokedAt")
	if err := developer.Revoke(app, rec, owner.Id); err != nil {
		t.Fatalf("second Revoke: %v", err)
	}
	if !rec.GetDateTime("revokedAt").Equal(first) {
		t.Error("a second revoke changed the revocation time")
	}

	reloaded, err := app.FindRecordById(developer.CollectionAPIKeys, rec.Id)
	if err != nil {
		t.Fatal("the revoked key was deleted; the record of withdrawal was lost")
	}
	if reloaded.GetString("status") != developer.StatusRevoked {
		t.Error("the revocation did not persist")
	}
}

// TestExpireStaleOnlyMovesKeysThatNeedMoving: the expiry is enforced per request
// anyway, so this is about the list telling the truth.
func TestExpireStaleOnlyMovesKeysThatNeedMoving(t *testing.T) {
	app, owner := setup(t)
	now := time.Now()

	stale := createKey(t, app, owner.Id, "", now.Add(-time.Hour))
	fresh := createKey(t, app, owner.Id, "", now.Add(time.Hour))
	forever := createKey(t, app, owner.Id, "", time.Time{})

	moved, err := developer.ExpireStale(app, now)
	if err != nil {
		t.Fatalf("ExpireStale: %v", err)
	}
	if moved != 1 {
		t.Errorf("expired %d keys, want 1", moved)
	}

	// Reloaded rather than inspected in place: ExpireStale loads and saves its
	// own copies, so the records this test holds are not the ones it changed.
	statusOf := func(rec *core.Record) string {
		reloaded, err := app.FindRecordById(developer.CollectionAPIKeys, rec.Id)
		if err != nil {
			t.Fatalf("reload: %v", err)
		}
		return reloaded.GetString("status")
	}

	if got := statusOf(stale); got != developer.StatusExpired {
		t.Errorf("the stale key is %q, want expired", got)
	}
	if got := statusOf(fresh); got != developer.StatusActive {
		t.Errorf("a key that has not expired is %q", got)
	}
	if got := statusOf(forever); got != developer.StatusActive {
		t.Errorf("a key with no expiry is %q", got)
	}
}

// TestRateLimiterCountsWithinAWindow: the limiter is what stops a runaway client.
func TestRateLimiterCountsWithinAWindow(t *testing.T) {
	limiter := developer.NewLimiter()
	now := time.Now()

	for i := 0; i < 3; i++ {
		if allowed, _ := limiter.Allow("key-1", 3, now); !allowed {
			t.Fatalf("request %d was refused below the limit", i+1)
		}
	}

	allowed, retryAfter := limiter.Allow("key-1", 3, now)
	if allowed {
		t.Error("a request above the limit was allowed")
	}
	if retryAfter <= 0 {
		t.Error("no retry-after was reported")
	}

	// Another key is unaffected: a limit is per credential, not global.
	if allowed, _ := limiter.Allow("key-2", 3, now); !allowed {
		t.Error("a second key was refused because the first was busy")
	}

	// A zero limit means no limit.
	for i := 0; i < 100; i++ {
		if allowed, _ := limiter.Allow("key-3", 0, now); !allowed {
			t.Fatal("a key with no limit was refused")
		}
	}

	// The window rolls over.
	later := now.Add(61 * time.Second)
	if allowed, _ := limiter.Allow("key-1", 3, later); !allowed {
		t.Error("the limit did not reset after the window")
	}
}
