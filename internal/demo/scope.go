// Package demo loads a coherent set of example data for a demonstration or a test.
//
// It is a **subcommand**, never a step of installation. A fresh install is empty, and
// stays empty until somebody types the command; a panel that quietly filled itself with
// example customers would be worse than one that showed nothing.
//
// Every collection the tool touches is declared here, in two lists, and the command prints
// both before it does anything. The split is the whole safety property: configuration and
// identity are on the untouchable list, and no amount of resettings can take them.
package demo

// ownedCollections are cleared by a reset and repopulated by a seed.
//
// They hold the example data and nothing an operator configured. A reset returns all of
// them to the state this package defines, which is what makes a demonstration repeatable.
var ownedCollections = []string{
	// **Parent before child.** A reset walks this list backwards, so a collection
	// declared after another is emptied first. Getting the order wrong means a delete
	// fails on a record that still has children, and the clear stops there -- which is
	// what happened the first time this ran: five pairs were the wrong way round and the
	// reset emptied fifteen collections out of thirty-eight without saying so.
	//
	// The relations are deliberately not cascading, so this order is the only thing
	// making the sequence work.

	// The organisation: nothing points at these except the accounts below.
	"departments",
	"positions",
	"employees",

	// The catalogue. A variant belongs to a product, and the ledger belongs to a variant.
	"product_categories",
	"products",
	"product_attributes",
	"product_variants",
	"stock_ledger",

	// Membership: a benefit and a plan belong to a tier.
	"member_tiers",
	"member_tier_benefits",
	"member_plans",
	"user_memberships",

	// Promotions: a grant belongs to a coupon, and a coupon to a campaign.
	"campaigns",
	"coupons",
	"coupon_grants",

	// Subscriptions: a cycle belongs to a subscription, which belongs to a plan.
	"subscription_plans",
	"subscriptions",
	"subscription_cycles",

	// Orders and money. A refund points at an order and at a payment transaction, so it
	// comes after both; a transaction belongs to an intent and to an order.
	"orders",
	"order_items",
	"order_events",
	"payment_intents",
	"payment_transactions",
	"refunds",

	// Content: a read belongs to an announcement, a recipient to a message.
	"announcements",
	"announcement_reads",
	"messages",
	"message_recipients",
	"todos",
	"quick_links",
	"recent_visits",

	// Files: a file belongs to a folder.
	"file_folders",
	"files",
	"attachments",

	// Monitoring readings. The machines themselves are kept: the row for this machine is
	// created at start-up and a reset must not remove it.
	"host_metrics",
	"metric_snapshots",

	// Logs, so a demonstration of the audit screens has something in them.
	"security_audit_logs",
	"operation_logs",
	"login_logs",
	"exception_logs",
	"integration_logs",
	"job_runs",
	"system_updates",
}

// untouchedCollections are never cleared, and the command says so out loud.
//
// They are configuration, identity and the catalogue of what the platform can do. A
// demonstration resets the data somebody is looking at, never the setup they depend on:
// an operator who has configured their mail provider and their roles should be able to
// reload the example data without doing any of it again.
var untouchedCollections = []string{
	"users",
	"admins",
	"roles",
	"permissions",
	"menus",
	"dictionaries",
	"dictionary_items",
	"settings",
	"scheduled_jobs",
	"integration_providers",
	"refund_reason",
	"monitor_nodes",
	"api_keys",
	"sessions",
	"user_preferences",
}

// DemoUserPrefix marks the accounts this package creates.
//
// It is an email prefix rather than a column, because `users` is on the untouchable list
// and adding a marker field to it so a demo tool can find its own rows would be letting
// the tool shape the schema. The prefix is visible to an operator looking at the account
// list, which is where it matters.
const DemoUserPrefix = "demo."

// IsDemoUser reports whether an account was created by this package.
func IsDemoUser(email string) bool {
	return len(email) >= len(DemoUserPrefix) && email[:len(DemoUserPrefix)] == DemoUserPrefix
}

// Owned returns the collections a reset clears.
func Owned() []string { return append([]string(nil), ownedCollections...) }

// Untouched returns the collections a reset never clears.
func Untouched() []string { return append([]string(nil), untouchedCollections...) }
