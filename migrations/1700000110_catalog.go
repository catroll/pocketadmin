package migrations

import (
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/migrations"
)

// catalogMigration creates the product catalogue: the category tree, products,
// their attributes and variants, and the stock ledger.
//
// Prices are integer minor units with an explicit currency, never a decimal. The
// reasoning is in internal/money, and it applies to every money column in every
// business module that follows.
func init() {
	migrations.Register(func(app core.App) error {
		if err := createProductCategories(app); err != nil {
			return err
		}
		if err := createProducts(app); err != nil {
			return err
		}
		if err := createProductAttributes(app); err != nil {
			return err
		}
		if err := createProductVariants(app); err != nil {
			return err
		}
		if err := createStockLedger(app); err != nil {
			return err
		}
		if err := seedPermissionPoints(app); err != nil {
			return err
		}
		return seedMenus(app)
	}, func(app core.App) error {
		for _, name := range []string{"stock_ledger", "product_variants", "product_attributes",
			"products", "product_categories"} {
			if col := findCollection(app, name); col != nil {
				if err := app.Delete(col); err != nil {
					return err
				}
			}
		}
		return nil
	}, "1700000110_catalog.go")
}

// createProductCategories creates the category tree.
//
// The tree uses the same shared implementation as departments and file folders.
// `parent` is added on a second save because a collection cannot reference itself
// before it exists, which is a sharp edge the shared implementation already knows
// about.
func createProductCategories(app core.App) error {
	col := findCollection(app, "product_categories")
	if col == nil {
		col = newBase("product_categories")
	}
	ensureTimestamps(col)

	addField(col, text("name", true))
	addField(col, text("slug", true))
	addField(col, text("path", false))
	addField(col, &core.NumberField{Name: "level"})
	addField(col, text("icon", false))
	addField(col, text("image", false))
	addField(col, &core.NumberField{Name: "sortOrder"})
	addField(col, selectOf("status", true, "active", "hidden"))
	addField(col, &core.EditorField{Name: "description"})
	addField(col, &core.DateField{Name: "deletedAt"})

	col.AddIndex("idx_pcat_slug", true, "slug", "")
	col.AddIndex("idx_pcat_path", false, "path", "")
	col.AddIndex("idx_pcat_status", false, "status", "")
	col.AddIndex("idx_pcat_deleted", false, "deletedAt", "")

	lockDown(col)

	if err := app.Save(col); err != nil {
		return err
	}

	// The self-relation goes on in a second save, once the collection exists.
	col = findCollection(app, "product_categories")
	if col != nil && !hasField(col, "parent") {
		addField(col, rel(app, "parent", "product_categories", false))
		col.AddIndex("idx_pcat_parent", false, "parent", "")
		if err := app.Save(col); err != nil {
			return err
		}
	}
	return nil
}

// createProducts creates the products.
func createProducts(app core.App) error {
	col := findCollection(app, "products")
	if col == nil {
		col = newBase("products")
	}
	ensureTimestamps(col)

	addField(col, selectOf("kind", true,
		"physical", "digital", "membership_plan", "subscription_plan"))
	addField(col, text("name", true))
	addField(col, text("slug", true))
	addField(col, text("subtitle", false))
	addField(col, &core.EditorField{Name: "description"})
	addField(col, rel(app, "category", "product_categories", false))
	addField(col, text("brand", false))
	addField(col, multiSelectOf("tags", 8,
		"new", "featured", "bestseller", "limited", "clearance", "seasonal", "gift", "bundle"))
	addField(col, selectOf("status", true, "draft", "on_sale", "off_shelf", "archived"))

	// Money is integer minor units with an explicit currency. costPrice is kept off
	// the product API: it is what the shop paid, and showing it to a customer or a
	// competitor is a mistake nobody recovers from.
	addField(col, &core.NumberField{Name: "listPrice"})
	addField(col, &core.NumberField{Name: "salePrice"})
	addField(col, &core.NumberField{Name: "costPrice"})
	addField(col, text("currency", true))
	addField(col, &core.BoolField{Name: "taxable"})

	addField(col, selectOf("stockMode", true, "variant", "single", "unlimited"))
	addField(col, &core.NumberField{Name: "stock"})
	addField(col, &core.NumberField{Name: "lowStockThreshold"})
	addField(col, &core.NumberField{Name: "weightGrams"})

	addField(col, text("seoTitle", false))
	addField(col, text("seoKeywords", false))
	addField(col, text("seoDescription", false))
	addField(col, &core.NumberField{Name: "sortOrder"})
	addField(col, &core.DateField{Name: "publishedAt"})
	addField(col, rel(app, "createdBy", "users", false))
	addField(col, &core.DateField{Name: "deletedAt"})

	col.AddIndex("idx_products_slug", true, "slug", "")
	col.AddIndex("idx_products_status_sort", false, "status, sortOrder", "")
	col.AddIndex("idx_products_category", false, "category", "")
	col.AddIndex("idx_products_kind", false, "kind", "")
	col.AddIndex("idx_products_published", false, "publishedAt", "")
	col.AddIndex("idx_products_deleted", false, "deletedAt", "")

	disableCascade(col, "category", "createdBy")
	lockDown(col)

	return app.Save(col)
}

// createProductAttributes creates the typed attributes.
//
// The values live in a JSON column rather than a child collection because an
// attribute is always read and edited as a set: a form shows every value of one
// attribute at once, and a child table would mean a query per attribute to draw a
// single form.
func createProductAttributes(app core.App) error {
	col := findCollection(app, "product_attributes")
	if col == nil {
		col = newBase("product_attributes")
	}
	ensureTimestamps(col)

	addField(col, rel(app, "product", "products", true))
	addField(col, text("name", true))
	addField(col, jsonField("values", 20000))
	addField(col, &core.NumberField{Name: "sortOrder"})

	col.AddIndex("idx_pattr_unique", true, "product, name", "")
	col.AddIndex("idx_pattr_sort", false, "product, sortOrder", "")

	disableCascade(col, "product")
	lockDown(col)

	return app.Save(col)
}

// createProductVariants creates the SKUs.
func createProductVariants(app core.App) error {
	col := findCollection(app, "product_variants")
	if col == nil {
		col = newBase("product_variants")
	}
	ensureTimestamps(col)

	addField(col, rel(app, "product", "products", true))
	addField(col, text("sku", true))
	addField(col, text("barcode", false))
	// The attribute combination, for example {"Colour":"Red","Size":"M"}. Stored as
	// JSON so a variant is not tied to a fixed set of attribute columns.
	addField(col, jsonField("options", 4000))
	addField(col, text("label", false))

	// Each of these falls back to the product when it is zero, so a variant only
	// carries what differs from its parent.
	addField(col, &core.NumberField{Name: "listPrice"})
	addField(col, &core.NumberField{Name: "salePrice"})
	addField(col, &core.NumberField{Name: "costPrice"})
	addField(col, text("currency", false))

	// stock is materialised from the ledger, which is append-only. It is a cache of
	// the ledger's running balance, not a number anybody edits.
	addField(col, &core.NumberField{Name: "stock"})
	addField(col, &core.NumberField{Name: "lockedStock"})
	addField(col, &core.NumberField{Name: "lowStockThreshold"})
	addField(col, &core.NumberField{Name: "weightGrams"})

	addField(col, selectOf("status", true, "active", "disabled"))
	addField(col, &core.DateField{Name: "deletedAt"})

	col.AddIndex("idx_pvariant_sku", true, "sku", "")
	col.AddIndex("idx_pvariant_product_status", false, "product, status", "")
	col.AddIndex("idx_pvariant_barcode", false, "barcode", "")
	col.AddIndex("idx_pvariant_stock", false, "stock", "")

	disableCascade(col, "product")
	lockDown(col)

	return app.Save(col)
}

// createStockLedger creates the append-only stock movements.
//
// The ledger is the source of truth and `product_variants.stock` is a cache of its
// running balance. That ordering matters: a stock number that can be edited
// directly has no history, and "why is this 3 and not 5" is the question that
// matters when a count is wrong.
func createStockLedger(app core.App) error {
	col := findCollection(app, "stock_ledger")
	if col == nil {
		col = newBase("stock_ledger")
	}
	ensureTimestamps(col)

	addField(col, rel(app, "variant", "product_variants", true))
	addField(col, &core.NumberField{Name: "delta"})
	// The running balance after this movement. It is materialised rather than
	// computed on read so a statement can be read top to bottom without summing
	// every earlier row.
	addField(col, &core.NumberField{Name: "balanceAfter"})
	addField(col, selectOf("reason", true,
		"purchase", "sale", "reserve", "release", "return", "adjustment",
		"damage", "stocktake", "import"))
	addField(col, text("refType", false))
	addField(col, text("refId", false))
	addField(col, rel(app, "operator", "users", false))
	addField(col, text("note", false))

	col.AddIndex("idx_stock_variant_created", false, "variant, created", "")
	col.AddIndex("idx_stock_reason", false, "reason", "")
	col.AddIndex("idx_stock_ref", false, "refType, refId", "")

	disableCascade(col, "variant", "operator")
	lockDown(col)

	return app.Save(col)
}
