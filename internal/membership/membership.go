// Package membership holds the tier catalogue, its typed benefits, the purchasable
// plans and the memberships themselves.
//
// Benefits are typed rows rather than prose, and the discount rate is a number.
// That is not tidiness: pricing and checkout have to apply it, and a sentence
// cannot be parsed reliably enough to charge somebody correctly. "10% off" and "10
// percent off selected items" are the same string to a regular expression and
// different amounts of money to a customer.
package membership

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"time"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/types"
)

// Collection names.
const (
	CollectionTiers    = "member_tiers"
	CollectionBenefits = "member_tier_benefits"
	CollectionPlans    = "member_plans"
	CollectionMembers  = "user_memberships"
	CollectionUsers    = "users"
)

// Membership statuses.
const (
	StatusPending   = "pending"
	StatusActive    = "active"
	StatusExpired   = "expired"
	StatusCancelled = "cancelled"
	StatusRefunded  = "refunded"
)

// Sources.
const (
	SourcePurchase  = "purchase"
	SourceGift      = "gift"
	SourceTrial     = "trial"
	SourceAdmin     = "admin"
	SourcePromotion = "promotion"
)

// Benefit kinds.
const (
	BenefitDiscountRate     = "discount_rate"
	BenefitFreeShipping     = "free_shipping"
	BenefitPointsMultiplier = "points_multiplier"
	BenefitContentAccess    = "content_access"
	BenefitPrioritySupport  = "priority_support"
	BenefitCustom           = "custom"
)

// BenefitKinds returns the vocabulary, matched by the migration's stored select.
func BenefitKinds() []string {
	return []string{BenefitDiscountRate, BenefitFreeShipping, BenefitPointsMultiplier,
		BenefitContentAccess, BenefitPrioritySupport, BenefitCustom}
}

// ---------------------------------------------------------------------------
// Benefits
// ---------------------------------------------------------------------------

// Benefits is a tier's resolved, typed benefits.
//
// The fields are the shapes pricing actually needs. A screen reads Label; a
// checkout reads DiscountRate; a points calculation reads PointsMultiplier. None of
// them parses a string.
type Benefits struct {
	// DiscountRate is the fraction to charge: 0.9 means ten percent off. Zero means
	// no discount, which is also what "no membership" gives.
	DiscountRate float64

	// HasDiscount distinguishes "no discount configured" from "an explicit rate of
	// zero", which matters when a promotion is allowed to stack with a membership
	// discount only if one exists.
	HasDiscount bool

	FreeShipping     bool
	PointsMultiplier float64
	ContentAccess    []string
	PrioritySupport  bool

	// Labels are the display strings, for a screen.
	Labels []string

	// Custom is everything the platform does not interpret, kept so a site-specific
	// benefit survives a round trip.
	Custom []Benefit
}

// Benefit is one stored benefit row.
type Benefit struct {
	Kind  string
	Label string
	Value map[string]any
}

// ResolveBenefits reads a tier's benefits into a typed value.
//
// An unparsable payload is logged and skipped rather than failing the whole
// resolution: one malformed benefit should not remove every other benefit from a
// paying member, and a discount rate of zero is the safe direction to fail in.
func ResolveBenefits(app core.App, tierID string) Benefits {
	var out Benefits

	if tierID == "" {
		return out
	}

	records, err := app.FindRecordsByFilter(CollectionBenefits,
		"tier = {:t}", "sortOrder", 0, 0, map[string]any{"t": tierID})
	if err != nil {
		return out
	}

	for _, rec := range records {
		kind := rec.GetString("kind")
		label := rec.GetString("label")
		value := decodeJSONObject(rec.Get("value"))

		out.Labels = append(out.Labels, label)

		switch kind {
		case BenefitDiscountRate:
			rate, ok := numberField(value, "rate")
			if !ok {
				slog.Warn("a discount rate benefit has no rate", "tier", tierID, "value", value)
				continue
			}
			// A rate is a fraction between zero and one. A value outside that range
			// is far more likely to be a percentage somebody typed into the wrong
			// field than an intended 9000% discount, and applying it would charge a
			// negative amount.
			if rate < 0 || rate > 1 {
				slog.Warn("a discount rate is outside nought to one and was ignored",
					"tier", tierID, "rate", rate)
				continue
			}
			// The better discount wins when a tier declares two, rather than the last
			// one read: the order of rows is not something an editor controls.
			if !out.HasDiscount || rate < out.DiscountRate {
				out.DiscountRate = rate
			}
			out.HasDiscount = true

		case BenefitFreeShipping:
			out.FreeShipping = boolField(value, "enabled", true)

		case BenefitPointsMultiplier:
			if multiplier, ok := numberField(value, "multiplier"); ok && multiplier > 0 {
				out.PointsMultiplier = multiplier
			}

		case BenefitContentAccess:
			out.ContentAccess = append(out.ContentAccess, stringListField(value, "access")...)

		case BenefitPrioritySupport:
			out.PrioritySupport = boolField(value, "enabled", true)

		default:
			out.Custom = append(out.Custom, Benefit{Kind: kind, Label: label, Value: value})
		}
	}

	return out
}

// ---------------------------------------------------------------------------
// Resolution
// ---------------------------------------------------------------------------

// Membership is an active membership with its tier and benefits resolved.
type Membership struct {
	Record   *core.Record
	Tier     *core.Record
	Benefits Benefits

	// StartAt and ExpireAt come from the membership row. A zero ExpireAt means the
	// membership does not end.
	StartAt  time.Time
	ExpireAt time.Time

	Perpetual bool
}

// Active returns the membership in force for an account.
//
// When an account holds several -- a purchased tier and a gifted one, say -- the
// highest tier level wins, and among equals the one that lasts longest. Any other
// rule would mean a gift could downgrade a paying member, which is not what either
// party intended.
func Active(app core.App, userID string, now time.Time) (*Membership, error) {
	if userID == "" {
		return nil, nil
	}

	records, err := app.FindRecordsByFilter(CollectionMembers,
		"user = {:u} && status = {:s}", "", 0, 0,
		map[string]any{"u": userID, "s": StatusActive})
	if err != nil {
		return nil, fmt.Errorf("membership: find active memberships: %w", err)
	}

	var best *core.Record
	bestLevel := -1
	var bestExpiry time.Time

	for _, rec := range records {
		expiry := rec.GetDateTime("expireAt")

		// A membership past its expiry is not in force, even if the scheduled task
		// has not moved it yet. The task keeps the list honest; this keeps a request
		// from granting a benefit that ended yesterday.
		if !expiry.IsZero() && expiry.Time().Before(now) {
			continue
		}

		tier, err := app.FindRecordById(CollectionTiers, rec.GetString("tier"))
		if err != nil || tier == nil {
			continue
		}
		if tier.GetString("status") != "active" {
			// A disabled tier grants nothing, whatever memberships point at it.
			continue
		}

		level := tier.GetInt("level")
		better := level > bestLevel
		if level == bestLevel && best != nil {
			// Same level: the one that lasts longer wins, and perpetual beats any
			// expiry.
			if expiry.IsZero() {
				better = true
			} else if !bestExpiry.IsZero() && expiry.Time().After(bestExpiry) {
				better = true
			}
		}

		if better {
			best = rec
			bestLevel = level
			bestExpiry = func() time.Time {
				if expiry.IsZero() {
					return time.Time{}
				}
				return expiry.Time()
			}()
		}
	}

	if best == nil {
		return nil, nil
	}

	tier, _ := app.FindRecordById(CollectionTiers, best.GetString("tier"))

	result := &Membership{
		Record:    best,
		Tier:      tier,
		Benefits:  ResolveBenefits(app, best.GetString("tier")),
		Perpetual: best.GetDateTime("expireAt").IsZero(),
	}

	if start := best.GetDateTime("startAt"); !start.IsZero() {
		result.StartAt = start.Time()
	}
	if !result.Perpetual {
		result.ExpireAt = bestExpiry
	}

	return result, nil
}

// DiscountRate is the fraction to charge an account: 1.0 means no discount.
//
// It is the function pricing and checkout call, and it returns a number rather than
// a sentence for the reason the package exists.
func DiscountRate(app core.App, userID string, now time.Time) float64 {
	active, err := Active(app, userID, now)
	if err != nil || active == nil || !active.Benefits.HasDiscount {
		return 1.0
	}
	return active.Benefits.DiscountRate
}

// ---------------------------------------------------------------------------
// Granting
// ---------------------------------------------------------------------------

// Grant describes a membership to create or change.
type Grant struct {
	UserID   string
	TierID   string
	PlanID   string
	Source   string
	Operator string
	Reason   string
	Remarks  string

	// DurationDays is used when ExpireAt is zero. Zero duration means perpetual.
	DurationDays int

	// ExpireAt overrides DurationDays when set.
	ExpireAt time.Time

	// StartAt defaults to now.
	StartAt time.Time
}

// GrantMembership creates a membership.
//
// The reason is required for a manual grant. A membership that appears without one
// is a support question nobody can answer: the record says it was given, and
// nothing says why.
func GrantMembership(app core.App, grant Grant) (*core.Record, error) {
	if grant.UserID == "" || grant.TierID == "" {
		return nil, fmt.Errorf("membership: a grant needs an account and a tier")
	}

	source := grant.Source
	if source == "" {
		source = SourceAdmin
	}
	if source == SourceAdmin && grant.Reason == "" {
		return nil, fmt.Errorf("membership: granting a tier by hand requires a reason")
	}

	col, err := app.FindCollectionByNameOrId(CollectionMembers)
	if err != nil || col == nil {
		return nil, fmt.Errorf("membership: the memberships collection is missing")
	}

	start := grant.StartAt
	if start.IsZero() {
		start = time.Now()
	}

	rec := core.NewRecord(col)
	rec.Set("user", grant.UserID)
	rec.Set("tier", grant.TierID)
	rec.Set("source", source)
	rec.Set("status", StatusActive)
	rec.Set("startAt", start.UTC())
	rec.Set("reason", grant.Reason)
	rec.Set("remarks", grant.Remarks)

	if grant.PlanID != "" {
		rec.Set("plan", grant.PlanID)
	}
	if grant.Operator != "" {
		rec.Set("grantedBy", grant.Operator)
	}

	if expiry := expiryFor(grant, start); !expiry.IsZero() {
		rec.Set("expireAt", expiry.UTC())
	}

	if err := app.Save(rec); err != nil {
		return nil, fmt.Errorf("membership: save the grant: %w", err)
	}

	return rec, nil
}

// expiryFor works out when a membership ends, or the zero time for perpetual.
func expiryFor(grant Grant, start time.Time) time.Time {
	if !grant.ExpireAt.IsZero() {
		return grant.ExpireAt
	}
	if grant.DurationDays <= 0 {
		// Zero days means the membership does not expire. That is a different thing
		// from a very long membership and stays distinguishable, which is why the
		// field is left empty rather than set far in the future.
		return time.Time{}
	}
	return start.AddDate(0, 0, grant.DurationDays)
}

// Extend adds days to a membership, or replaces its expiry when it is perpetual.
func Extend(app core.App, rec *core.Record, days int, operator, reason string) error {
	if days == 0 {
		return nil
	}
	if reason == "" {
		return fmt.Errorf("membership: extending a membership requires a reason")
	}

	from := time.Now()
	if current := rec.GetDateTime("expireAt"); !current.IsZero() && current.Time().After(from) {
		// Extending an unexpired membership adds to what is left rather than to
		// today, so a renewal does not shorten a membership that still has time on
		// it.
		from = current.Time()
	}

	rec.Set("expireAt", from.AddDate(0, 0, days).UTC())
	rec.Set("reason", reason)
	if operator != "" {
		rec.Set("grantedBy", operator)
	}
	// An extension revives an expired membership, because that is what a renewal
	// is for.
	if rec.GetString("status") == StatusExpired {
		rec.Set("status", StatusActive)
	}

	return app.Save(rec)
}

// ChangeTier moves a membership to another tier, recording why.
//
// It is recorded rather than edited in place because the tier a person held and
// when they held it is the question a dispute turns on.
func ChangeTier(app core.App, rec *core.Record, tierID, operator, reason string) error {
	if tierID == "" {
		return fmt.Errorf("membership: no tier")
	}
	if reason == "" {
		return fmt.Errorf("membership: changing a tier requires a reason")
	}
	if rec.GetString("tier") == tierID {
		return nil
	}

	rec.Set("tier", tierID)
	rec.Set("reason", reason)
	if operator != "" {
		rec.Set("grantedBy", operator)
	}

	return app.Save(rec)
}

// SetStatus moves a membership to a terminal state.
func SetStatus(app core.App, rec *core.Record, status, operator, reason string) error {
	switch status {
	case StatusCancelled, StatusRefunded:
	default:
		return fmt.Errorf("membership: %q is not a terminal state", status)
	}
	if reason == "" {
		return fmt.Errorf("membership: ending a membership requires a reason")
	}

	rec.Set("status", status)
	rec.Set("reason", reason)
	if operator != "" {
		rec.Set("grantedBy", operator)
	}

	return app.Save(rec)
}

// ---------------------------------------------------------------------------
// Expiry
// ---------------------------------------------------------------------------

// ExpireDue moves memberships whose expiry has passed into the expired state.
//
// The transition is performed rather than computed at read time because "expired"
// is a state an operator queries by and a member may be notified about, and a
// purely computed state cannot be the trigger for anything.
func ExpireDue(app core.App, now time.Time) (int, error) {
	records, err := app.FindRecordsByFilter(CollectionMembers,
		"status = {:s} && expireAt != '' && expireAt <= {:now}",
		"expireAt", 0, 0, map[string]any{"s": StatusActive, "now": now.UTC()})
	if err != nil {
		return 0, fmt.Errorf("membership: find expired memberships: %w", err)
	}

	expired := 0
	for _, rec := range records {
		rec.Set("status", StatusExpired)
		if err := app.Save(rec); err != nil {
			return expired, fmt.Errorf("membership: expire %s: %w", rec.Id, err)
		}
		expired++
	}
	return expired, nil
}

// ExpiringSoon returns the memberships that end within a window.
func ExpiringSoon(app core.App, within time.Duration, now time.Time) ([]*core.Record, error) {
	until := now.Add(within)

	return app.FindRecordsByFilter(CollectionMembers,
		"status = {:s} && expireAt != '' && expireAt > {:now} && expireAt <= {:until}",
		"expireAt", 0, 0,
		map[string]any{"s": StatusActive, "now": now.UTC(), "until": until.UTC()})
}

// ---------------------------------------------------------------------------
// Tiers
// ---------------------------------------------------------------------------

// DefaultTier returns the tier granted on registration.
func DefaultTier(app core.App) (*core.Record, error) {
	rec, err := app.FindFirstRecordByFilter(CollectionTiers,
		"isDefault = true && status = 'active'", nil)
	if err != nil {
		return nil, nil
	}
	return rec, nil
}

// MakeDefault marks one tier as the registration default.
//
// It clears the flag from every other tier first, because "exactly one default" is
// a property of the data and no unique index can express it. The same shape as the
// integration provider default.
func MakeDefault(app core.App, rec *core.Record) error {
	peers, err := app.FindAllRecords(CollectionTiers)
	if err != nil {
		return err
	}

	for _, peer := range peers {
		if peer.Id == rec.Id || !peer.GetBool("isDefault") {
			continue
		}
		peer.Set("isDefault", false)
		if err := app.Save(peer); err != nil {
			return err
		}
	}

	rec.Set("isDefault", true)
	return app.Save(rec)
}

// ActiveMemberCount counts the memberships currently on a tier.
func ActiveMemberCount(app core.App, tierID string, now time.Time) (int, error) {
	records, err := app.FindRecordsByFilter(CollectionMembers,
		"tier = {:t} && status = {:s}", "", 0, 0,
		map[string]any{"t": tierID, "s": StatusActive})
	if err != nil {
		return 0, err
	}

	// A membership past its expiry has not been moved yet but is not active either.
	count := 0
	for _, rec := range records {
		if expiry := rec.GetDateTime("expireAt"); !expiry.IsZero() && expiry.Time().Before(now) {
			continue
		}
		count++
	}
	return count, nil
}

// ReferencedByPlan reports whether a tier is granted by a membership plan.
func ReferencedByPlan(app core.App, tierID string) (bool, error) {
	rec, err := app.FindFirstRecordByFilter(CollectionPlans,
		"tier = {:t}", map[string]any{"t": tierID})
	if err != nil || rec == nil {
		return false, nil
	}
	return true, nil
}

// ---------------------------------------------------------------------------
// Decoding
// ---------------------------------------------------------------------------

// decodeJSONObject reads a json column into a map.
//
// PocketBase returns a json field as types.JSONRaw, which is a named []byte and so
// does not match a []byte case in a type switch. The mistake is invisible where it
// is made, which is why it is written once here.
func decodeJSONObject(value any) map[string]any {
	switch v := value.(type) {
	case nil:
		return map[string]any{}
	case map[string]any:
		return v
	case types.JSONRaw:
		return decodeObjectBytes([]byte(v))
	case []byte:
		return decodeObjectBytes(v)
	case string:
		return decodeObjectBytes([]byte(v))
	default:
		return map[string]any{}
	}
}

// numberField reads a numeric member of a payload.
func numberField(values map[string]any, name string) (float64, bool) {
	raw, ok := values[name]
	if !ok {
		return 0, false
	}
	switch v := raw.(type) {
	case float64:
		return v, true
	case int:
		return float64(v), true
	case int64:
		return float64(v), true
	case string:
		// A number that arrived from a form is text. It is parsed here rather than
		// rejected, because a settings screen writes what the operator typed.
		var out float64
		if _, err := fmt.Sscanf(v, "%g", &out); err == nil {
			return out, true
		}
		return 0, false
	default:
		return 0, false
	}
}

// boolField reads a boolean member, defaulting when it is absent.
func boolField(values map[string]any, name string, fallback bool) bool {
	raw, ok := values[name]
	if !ok {
		// The presence of the row is the statement for a flag benefit: a
		// free_shipping row with no payload means free shipping.
		return fallback
	}
	switch v := raw.(type) {
	case bool:
		return v
	case string:
		return v == "true" || v == "1" || v == "on"
	case float64:
		return v != 0
	default:
		return fallback
	}
}

// stringListField reads a list of strings from a payload.
func stringListField(values map[string]any, name string) []string {
	raw, ok := values[name]
	if !ok {
		return nil
	}

	var out []string
	switch v := raw.(type) {
	case []any:
		for _, item := range v {
			if text, ok := item.(string); ok && text != "" {
				out = append(out, text)
			}
		}
	case []string:
		out = append(out, v...)
	case string:
		if v != "" {
			out = append(out, v)
		}
	}

	sort.Strings(out)
	return out
}

// decodeObjectBytes unmarshals JSON text into a map.
func decodeObjectBytes(raw []byte) map[string]any {
	if len(raw) == 0 {
		return map[string]any{}
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return map[string]any{}
	}
	return out
}
