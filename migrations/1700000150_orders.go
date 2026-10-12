package migrations

import (
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/migrations"
)

// ordersMigration creates the orders, their items and their timeline, and settles
// the forward dependencies the earlier waves reserved.
//
// Four collections were built without a field naming the order that caused them,
// because a relation to a collection that does not exist fails the migration rather
// than being inert. The orders collection exists now, so those fields are added
// here: a membership, a coupon grant and a subscription cycle all need to be able to
// say which order created or consumed them.
func init() {
	migrations.Register(func(app core.App) error {
		if err := createOrders(app); err != nil {
			return err
		}
		if err := createOrderItems(app); err != nil {
			return err
		}
		if err := createOrderEvents(app); err != nil {
			return err
		}
		if err := settleForwardDependencies(app); err != nil {
			return err
		}
		if err := seedPermissionPoints(app); err != nil {
			return err
		}
		return seedMenus(app)
	}, func(app core.App) error {
		// The reserved fields go first: their targets are removed below.
		for _, spec := range [][2]string{
			{"user_memberships", "order"},
			{"coupon_grants", "order"},
			{"subscription_cycles", "order"},
		} {
			if col := findCollection(app, spec[0]); col != nil && hasField(col, spec[1]) {
				col.Fields.RemoveByName(spec[1])
				if err := app.Save(col); err != nil {
					return err
				}
			}
		}

		for _, name := range []string{"order_events", "order_items", "orders"} {
			if col := findCollection(app, name); col != nil {
				if err := app.Delete(col); err != nil {
					return err
				}
			}
		}
		return nil
	}, "1700000150_orders.go")
}

// settleForwardDependencies adds the order relation the earlier waves reserved.
//
// Each is added only when it is absent, so re-running is harmless, and the index
// follows the field rather than preceding it -- creating an index on a column that
// does not exist yet fails, which is a sharp edge this project has hit before.
func settleForwardDependencies(app core.App) error {
	for _, spec := range []struct {
		collection string
		index      string
	}{
		{"user_memberships", "idx_memberships_order"},
		{"coupon_grants", "idx_grants_order"},
		{"subscription_cycles", "idx_cycles_order"},
	} {
		col := findCollection(app, spec.collection)
		if col == nil {
			continue
		}

		if !hasField(col, "order") {
			addField(col, rel(app, "order", "orders", false))
		}
		col.AddIndex(spec.index, false, "order", "")
		disableCascade(col, "order")

		if err := app.Save(col); err != nil {
			return err
		}
	}
	return nil
}

// createOrders creates the orders.
func createOrders(app core.App) error {
	col := findCollection(app, "orders")
	if col == nil {
		col = newBase("orders")
	}
	ensureTimestamps(col)

	addField(col, text("orderNo", true))
	addField(col, rel(app, "user", "users", true))
	addField(col, selectOf("status", true,
		"pending_payment", "paid", "fulfilling", "completed", "cancelled", "closed", "refunded"))
	addField(col, selectOf("paymentStatus", true,
		"unpaid", "partial", "paid", "refunding", "refunded"))
	addField(col, selectOf("source", true,
		"web", "ios", "android", "admin", "subscription", "import"))
	addField(col, text("currency", true))

	// The breakdown is stored rather than recomputed. A price edited next month must
	// not change what an order placed today says it cost.
	addField(col, &core.NumberField{Name: "subtotal"})
	addField(col, &core.NumberField{Name: "discountTotal"})
	addField(col, &core.NumberField{Name: "memberDiscount"})
	addField(col, &core.NumberField{Name: "couponDiscount"})
	addField(col, &core.NumberField{Name: "shippingFee"})
	addField(col, &core.NumberField{Name: "taxAmount"})
	addField(col, &core.NumberField{Name: "grandTotal"})
	addField(col, &core.NumberField{Name: "amountPaid"})
	addField(col, &core.NumberField{Name: "amountRefunded"})

	// Which coupons and which tier applied, recorded so the arithmetic can be
	// explained later without reconstructing the state of the world at the time.
	addField(col, multiRel(app, "couponGrants", "coupon_grants", 20))
	addField(col, rel(app, "memberTier", "member_tiers", false))
	addField(col, rel(app, "subscription", "subscriptions", false))

	addField(col, jsonField("shippingAddress", 4000))
	addField(col, jsonField("billingAddress", 4000))

	// Two notes, kept apart: one is the customer talking to the shop and the other is
	// the shop talking to itself, and merging them publishes the second.
	addField(col, text("customerNote", false))
	addField(col, text("adminNote", false))

	addField(col, &core.DateField{Name: "expiresAt"})
	addField(col, &core.DateField{Name: "paidAt"})
	addField(col, &core.DateField{Name: "completedAt"})
	addField(col, &core.DateField{Name: "cancelledAt"})
	addField(col, text("cancelReason", false))
	addField(col, rel(app, "placedBy", "users", false))
	addField(col, &core.DateField{Name: "deletedAt"})

	col.AddIndex("idx_orders_no", true, "orderNo", "")
	col.AddIndex("idx_orders_user", false, "user, created", "")
	col.AddIndex("idx_orders_status", false, "status", "")
	col.AddIndex("idx_orders_payment_status", false, "paymentStatus", "")
	col.AddIndex("idx_orders_expires", false, "expiresAt", "")
	col.AddIndex("idx_orders_subscription", false, "subscription", "")
	col.AddIndex("idx_orders_deleted", false, "deletedAt", "")

	disableCascade(col, "user", "couponGrants", "memberTier", "subscription", "placedBy")
	lockDown(col)

	return app.Save(col)
}

// createOrderItems creates the item snapshots.
func createOrderItems(app core.App) error {
	col := findCollection(app, "order_items")
	if col == nil {
		col = newBase("order_items")
	}
	ensureTimestamps(col)

	addField(col, rel(app, "order", "orders", true))
	// The product and variant are kept for reporting. The snapshots below are what
	// the order actually says it bought, because a catalogue edit must never change
	// a placed order.
	addField(col, rel(app, "product", "products", false))
	addField(col, rel(app, "variant", "product_variants", false))
	addField(col, text("productName", true))
	addField(col, text("variantLabel", false))
	addField(col, text("sku", false))
	addField(col, text("image", false))

	addField(col, &core.NumberField{Name: "unitPrice"})
	addField(col, &core.NumberField{Name: "quantity"})
	addField(col, &core.NumberField{Name: "subtotal"})
	addField(col, &core.NumberField{Name: "discountAmount"})
	addField(col, &core.NumberField{Name: "taxAmount"})
	addField(col, &core.NumberField{Name: "total"})
	addField(col, text("currency", false))

	// Updated as refunds are approved, so an order can say how much of a line has
	// already gone back.
	addField(col, &core.NumberField{Name: "refundedQuantity"})
	addField(col, &core.NumberField{Name: "refundedAmount"})

	col.AddIndex("idx_items_order", false, "order", "")
	col.AddIndex("idx_items_product", false, "product", "")
	col.AddIndex("idx_items_variant", false, "variant", "")

	disableCascade(col, "order", "product", "variant")
	lockDown(col)

	return app.Save(col)
}

// createOrderEvents creates the order timeline.
func createOrderEvents(app core.App) error {
	col := findCollection(app, "order_events")
	if col == nil {
		col = newBase("order_events")
	}
	ensureTimestamps(col)

	addField(col, rel(app, "order", "orders", true))
	addField(col, selectOf("event", true,
		"created", "payment_started", "paid", "payment_failed", "fulfilling",
		"completed", "cancelled", "closed", "refund_requested", "refunded",
		"note_added", "stock_released"))
	// Both ends of the transition are recorded, so the timeline can be read without
	// inferring what a row's predecessor was.
	addField(col, text("fromStatus", false))
	addField(col, text("toStatus", false))
	// Empty for a system event, which is what the scheduled tasks produce.
	addField(col, rel(app, "actor", "users", false))
	addField(col, text("reason", false))
	addField(col, jsonField("detail", 8000))

	col.AddIndex("idx_events_order", false, "order, created", "")
	col.AddIndex("idx_events_event", false, "event", "")
	col.AddIndex("idx_events_created", false, "created", "")

	disableCascade(col, "order", "actor")
	lockDown(col)

	return app.Save(col)
}
