package i18n_test

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"pocketadmin/internal/i18n"
)

// This file guards the class of untranslated text that the other coverage test cannot
// see: a value the server supplies at render time.
//
// TestNoTemplateShowsUntranslatedEnglish looks for English between tags, and a template
// that prints {{.Status}} contains no English -- so it passes, and the page shows
// `pending_payment` to a reader who chose Chinese. The template has to name a key that the
// value completes, and the catalogue has to hold a message for every value the server can
// put in it. Neither is visible to a scan of the templates for English, which is why the
// vocabularies are written down here.
//
// The table is a copy of the server's vocabularies rather than a call into them: the
// screens build several of the lists from inline literals, and importing every domain
// package into the catalogue's test to reach the rest would make the catalogue depend on
// PocketBase to prove a message exists. Each entry says where its values come from, so a
// value added to the server can be traced back here.

// enumFamily is one closed vocabulary the server layer can put into a message key.
type enumFamily struct {
	// prefix is the catalogue key prefix; the value is appended after a dot, so a
	// family with prefix `orders.status` and value `paid` needs `orders.status.paid`.
	prefix string

	// source says where the vocabulary is declared, so a reader can check the list
	// rather than trust it.
	source string

	// values is every value the server can supply.
	values []string
}

// serverEnums lists the closed vocabularies, module by module.
var serverEnums = []enumFamily{
	{
		prefix: "account.login",
		source: "internal/server/login.go and internal/server/mfa.go: the result passed to logLogin",
		values: []string{"success", "failure", "locked", "mfa_required", "mfa_failed"},
	},
	{
		prefix: "account.status",
		source: "internal/server/account_actions.go: accountInactive and accountBanned, plus active",
		values: []string{"active", "inactive", "banned"},
	},
	{
		prefix: "announcements.audience",
		source: "internal/announcement/announcement.go: the Audience* constants",
		values: []string{"all", "admins", "users", "departments"},
	},
	{
		prefix: "announcements.level",
		source: "internal/server/announcement_screens.go: the Levels list",
		values: []string{"normal", "important", "urgent"},
	},
	{
		prefix: "announcements.status",
		source: "internal/announcement/announcement.go: the Status* constants",
		values: []string{"draft", "scheduled", "published", "expired", "archived"},
	},
	{
		prefix: "announcements.type",
		source: "internal/server/announcement_screens.go: the Types list",
		values: []string{"notice", "announcement", "maintenance", "update"},
	},
	{
		prefix: "catalog.products.kind",
		source: "internal/catalog/catalog.go: the Kind* constants",
		values: []string{"physical", "digital", "membership_plan", "subscription_plan"},
	},
	{
		prefix: "catalog.products.status",
		source: "internal/server/catalog_screens.go: the Statuses list",
		values: []string{"draft", "on_sale", "off_shelf", "archived"},
	},
	{
		prefix: "catalog.products.stockMode",
		source: "internal/catalog/catalog.go: the Stock* constants",
		values: []string{"variant", "single", "unlimited"},
	},
	{
		prefix: "catalog.tags",
		source: "internal/catalog/catalog.go: Tags()",
		values: []string{"new", "featured", "bestseller", "limited", "clearance", "seasonal", "gift", "bundle"},
	},
	{
		prefix: "catalog.variants.reason",
		source: "internal/catalog/catalog.go: the Reason* constants",
		values: []string{"purchase", "sale", "reserve", "release", "return", "adjustment", "damage", "stocktake", "import"},
	},
	{
		prefix: "developer.keys.status",
		source: "internal/developer/apikey.go: the Status* constants",
		values: []string{"active", "revoked", "expired"},
	},
	{
		prefix: "integrations.channel",
		source: "internal/server/integration_screens.go: the Channels list on the log screen",
		values: []string{"email", "sms", "storage", "auth", "payment", "webhook"},
	},
	{
		prefix: "integrations.status",
		source: "internal/integration/provider.go: the Status* constants",
		values: []string{"enabled", "disabled", "testing", "error"},
	},
	{
		prefix: "jobs.status",
		source: "internal/jobs/runner.go: the Status* constants, plus the enabled/paused/disabled task states",
		values: []string{"enabled", "paused", "disabled", "running", "success", "failed", "skipped"},
	},
	{
		prefix: "membership.benefitKind",
		source: "internal/membership/membership.go: BenefitKinds()",
		values: []string{"discount_rate", "free_shipping", "points_multiplier", "content_access", "priority_support", "custom"},
	},
	{
		prefix: "membership.source",
		source: "internal/membership/membership.go: the Source* constants",
		values: []string{"purchase", "gift", "trial", "admin", "promotion"},
	},
	{
		prefix: "membership.status",
		source: "internal/membership/membership.go: the Status* constants, plus the active/disabled tier states",
		values: []string{"pending", "active", "expired", "cancelled", "refunded", "disabled"},
	},
	{
		prefix: "monitoring.health",
		source: "internal/monitoring/health.go: the Status* constants",
		values: []string{"ok", "degraded", "down"},
	},
	{
		prefix: "orders.paymentStatus",
		source: "internal/order/state.go: PaymentStatuses()",
		values: []string{"unpaid", "partial", "paid", "refunding", "refunded"},
	},
	{
		prefix: "orders.source",
		source: "internal/server/order_screens.go: the Sources list",
		values: []string{"web", "ios", "android", "admin", "subscription", "import"},
	},
	{
		prefix: "orders.status",
		source: "internal/order/state.go: Statuses()",
		values: []string{"pending_payment", "paid", "fulfilling", "completed", "cancelled", "closed", "refunded"},
	},
	{
		prefix: "payments.method",
		source: "internal/payment/idempotency.go: Methods()",
		values: []string{"card", "bank_transfer", "wallet", "cash", "other"},
	},
	{
		prefix: "payments.reconcile.kind",
		source: "internal/payment: the discrepancy kinds reconciled against a settlement report",
		values: []string{"missing_locally", "missing_remotely", "amount"},
	},
	{
		prefix: "payments.status",
		source: "internal/payment/idempotency.go: Statuses()",
		values: []string{"pending", "processing", "succeeded", "failed", "cancelled", "expired"},
	},
	{
		prefix: "promotions.campaigns.status",
		source: "internal/server/promotion_screens.go: the campaign Statuses list",
		values: []string{"draft", "active", "paused", "expired"},
	},
	{
		prefix: "promotions.coupons.status",
		source: "internal/server/promotion_screens.go: the coupon Statuses list",
		values: []string{"draft", "active", "paused", "expired"},
	},
	{
		prefix: "promotions.coupon.type",
		source: "internal/promotion/discount.go: the Type* constants",
		values: []string{"fixed_amount", "percentage", "free_shipping"},
	},
	{
		prefix: "promotions.coupon.validity",
		source: "internal/promotion/discount.go: the Validity* constants",
		values: []string{"absolute", "relative"},
	},
	{
		prefix: "promotions.grants.status",
		source: "internal/promotion/discount.go: the Grant* constants",
		values: []string{"issued", "used", "expired", "voided"},
	},
	{
		prefix: "refunds.mode",
		source: "internal/refund/refund.go: Modes()",
		values: []string{"full", "partial_amount", "partial_items"},
	},
	{
		prefix: "refunds.status",
		source: "internal/refund/refund.go: Statuses()",
		values: []string{"pending", "approved", "rejected", "processing", "succeeded", "failed", "cancelled"},
	},
	{
		prefix: "subscriptions.cycle",
		source: "internal/subscription/period.go: the Cycle* constants",
		values: []string{"pending", "paid", "failed", "skipped"},
	},
	{
		prefix: "subscriptions.interval",
		source: "internal/subscription/period.go: Intervals()",
		values: []string{"day", "week", "month", "year"},
	},
	{
		prefix: "subscriptions.status",
		source: "internal/subscription/period.go: the Status* constants",
		values: []string{"trialing", "active", "past_due", "paused", "cancelled", "expired"},
	},
	{
		prefix: "workbench.level",
		source: "internal/server/workbench_screens.go: the level select on the compose form",
		values: []string{"info", "success", "warning", "error"},
	},
	{
		prefix: "workbench.tag",
		source: "internal/workbench/workbench.go: TodoTags()",
		values: []string{"follow-up", "finance", "operations", "support", "review", "blocked"},
	},
	{
		prefix: "workbench.type",
		source: "internal/workbench/workbench.go: the message types producers set (todo, system, notice)",
		values: []string{"todo", "system", "notice"},
	},
}

// TestEveryServerEnumValueHasAMessage.
//
// Every value a screen can print through `<prefix>.%s` needs a message in every shipped
// locale. A missing one is not an error anywhere else: the fallback shows the key, so the
// page renders and the other tests pass while a reader sees `catalog.products.kind.digital`.
func TestEveryServerEnumValueHasAMessage(t *testing.T) {
	for _, family := range serverEnums {
		for _, value := range family.values {
			key := family.prefix + "." + value
			for _, locale := range i18n.Locales() {
				if got := i18n.T(locale, key); got == key {
					t.Errorf("%s has no message for %q (from %s); a screen showing that value "+
						"would print the key", locale, key, family.source)
				}
			}
		}
	}
}

// TestEveryVocabularyIsUsedByATemplate is the other direction: a family written down here
// that no template asks for is a stale entry, and a family a template asks for that is not
// here is one whose values nobody enumerated -- exactly the gap this file exists to close.
func TestEveryVocabularyIsUsedByATemplate(t *testing.T) {
	known := map[string]bool{}
	for _, family := range serverEnums {
		known[family.prefix] = true
	}

	root := filepath.Join("..", "..", "web", "templates")
	call := regexp.MustCompile(`printf\s+"([^"]*?)\.%s"`)

	used := map[string]bool{}
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(path, ".html") {
			return err
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, m := range call.FindAllStringSubmatch(string(raw), -1) {
			used[m[1]] = true
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking the templates: %v", err)
	}

	var names []string
	for prefix := range used {
		names = append(names, prefix)
	}
	sort.Strings(names)

	for _, prefix := range names {
		if !known[prefix] {
			t.Errorf("a template completes the key %q from a server value, but no vocabulary "+
				"is written down for it; add it to serverEnums with its source and every value",
				prefix)
		}
	}

	var stale []string
	for prefix := range known {
		if !used[prefix] {
			stale = append(stale, prefix)
		}
	}
	sort.Strings(stale)
	for _, prefix := range stale {
		t.Errorf("serverEnums names %q, but no template completes that key from a server "+
			"value; either the screen changed or the entry is stale", prefix)
	}
}
