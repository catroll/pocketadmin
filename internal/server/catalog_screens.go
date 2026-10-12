package server

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/pocketbase/pocketbase/core"

	"pocketadmin/internal/audit"
	"pocketadmin/internal/catalog"
	"pocketadmin/internal/money"
	"pocketadmin/internal/permission"
)

// registerCatalogRoutes mounts the catalogue screens.
func (s *Server) registerCatalogRoutes(panel *gin.RouterGroup) {
	s.readGuarded(panel, http.MethodGet, "/catalog/products", permission.CatalogProductList, s.handleProductList)
	s.readGuarded(panel, http.MethodGet, "/catalog/products/new", permission.CatalogProductCreate, s.handleProductForm)
	s.readGuarded(panel, http.MethodGet, "/catalog/products/:id/edit", permission.CatalogProductUpdate, s.handleProductForm)
	s.readGuarded(panel, http.MethodGet, "/catalog/products/:id/variants", permission.CatalogVariantManage, s.handleVariantList)

	s.guarded(panel, http.MethodPost, "/catalog/products/save", permission.CatalogProductCreate, s.handleProductSave)
	s.guarded(panel, http.MethodPost, "/catalog/products/:id/delete", permission.CatalogProductDelete, s.handleProductDelete)
	s.guarded(panel, http.MethodPost, "/catalog/products/:id/bulk", permission.CatalogProductBulk, s.handleProductBulk)
	s.guarded(panel, http.MethodPost, "/catalog/products/:id/variants/generate", permission.CatalogVariantManage, s.handleVariantGenerate)
	s.guarded(panel, http.MethodPost, "/catalog/variants/:id/stock", permission.CatalogStockAdjust, s.handleStockAdjust)
	s.guarded(panel, http.MethodPost, "/catalog/variants/:id/reconcile", permission.CatalogStockAdjust, s.handleStockReconcile)
}

// handleProductList renders the catalogue.
func (s *Server) handleProductList(c *gin.Context) {
	filter := "deletedAt = ''"
	params := map[string]any{}

	if category := strings.TrimSpace(c.Query("category")); category != "" {
		filter += " && category = {:category}"
		params["category"] = category
	}
	if status := strings.TrimSpace(c.Query("status")); status != "" {
		filter += " && status = {:status}"
		params["status"] = status
	}
	if kind := strings.TrimSpace(c.Query("kind")); kind != "" {
		filter += " && kind = {:kind}"
		params["kind"] = kind
	}
	if brand := strings.TrimSpace(c.Query("brand")); brand != "" {
		filter += " && brand ~ {:brand}"
		params["brand"] = brand
	}
	if search := strings.TrimSpace(c.Query("q")); search != "" {
		filter += " && (name ~ {:q} || slug ~ {:q} || subtitle ~ {:q})"
		params["q"] = search
	}
	if low := strings.TrimSpace(c.Query("lowStock")); low != "" {
		// A single-stock product below its threshold. A variant-stocked product is
		// judged on its variants, which the detail screen shows.
		filter += " && stockMode = 'single' && lowStockThreshold > 0 && stock <= lowStockThreshold"
	}

	records, err := s.app.FindRecordsByFilter(catalog.CollectionProducts,
		filter, "-created", 200, 0, params)
	if err != nil {
		s.renderErrorPage(c, http.StatusInternalServerError, "Could not load the products", err.Error())
		return
	}

	rows := make([]productRow, 0, len(records))
	for _, rec := range records {
		rows = append(rows, s.productRow(rec))
	}

	actor := s.actor(c)
	s.render(c, http.StatusOK, "catalog_products.html", productsPage{
		base:       s.newBase(c, "Products", "catalog.products"),
		Rows:       rows,
		Categories: s.categoryOptions(),
		Query:      c.Query("q"),
		Category:   c.Query("category"),
		Status:     c.Query("status"),
		Kind:       c.Query("kind"),
		Brand:      c.Query("brand"),
		LowStock:   c.Query("lowStock"),
		Statuses:   []string{"draft", "on_sale", "off_shelf", "archived"},
		Kinds:      []string{catalog.KindPhysical, catalog.KindDigital, catalog.KindMembershipPlan, catalog.KindSubscriptionPlan},
		CanCreate:  actor.Can(permission.CatalogProductCreate),
		CanUpdate:  actor.Can(permission.CatalogProductUpdate),
		CanDelete:  actor.Can(permission.CatalogProductDelete),
		CanBulk:    actor.Can(permission.CatalogProductBulk),
	})
}

// productRow builds one row.
func (s *Server) productRow(rec *core.Record) productRow {
	row := productRow{
		Record:   rec,
		Name:     rec.GetString("name"),
		Slug:     rec.GetString("slug"),
		Subtitle: rec.GetString("subtitle"),
		Kind:     rec.GetString("kind"),
		Status:   rec.GetString("status"),
		Brand:    rec.GetString("brand"),
		SKUs:     s.variantCount(rec.Id),
		List:     money.Format(money.Amount(rec.GetFloat("listPrice")), rec.GetString("currency")),
		Currency: rec.GetString("currency"),
	}

	if sale := int64(rec.GetFloat("salePrice")); sale > 0 {
		row.Sale = money.Format(money.Amount(sale), rec.GetString("currency"))
		row.OnSale = sale < int64(rec.GetFloat("listPrice"))
	}

	if rec.GetString("stockMode") == catalog.StockSingle {
		row.Stock = rec.GetInt("stock")
		row.LowStock = rec.GetInt("lowStockThreshold") > 0 &&
			rec.GetInt("stock") <= rec.GetInt("lowStockThreshold")
		row.TrackStock = true
	}
	if category, err := s.app.FindRecordById(catalog.CollectionCategories, rec.GetString("category")); err == nil && category != nil {
		row.Category = category.GetString("name")
	}
	return row
}

// variantCount counts a product's variants.
func (s *Server) variantCount(productID string) int {
	records, err := s.app.FindRecordsByFilter(catalog.CollectionVariants,
		"product = {:p} && deletedAt = ''", "", 0, 0, map[string]any{"p": productID})
	if err != nil {
		return 0
	}
	return len(records)
}

// handleProductForm renders the editor.
func (s *Server) handleProductForm(c *gin.Context) {
	view := productForm{
		base:        s.newBase(c, "New product", "catalog.products"),
		IsNew:       true,
		Kinds:       []string{catalog.KindPhysical, catalog.KindDigital, catalog.KindMembershipPlan, catalog.KindSubscriptionPlan},
		StockModes:  []string{catalog.StockVariant, catalog.StockSingle, catalog.StockUnlimited},
		Statuses:    []string{"draft", "on_sale", "off_shelf", "archived"},
		Tags:        catalog.Tags(),
		Selected:    map[string]bool{},
		Categories:  s.categoryOptions(),
		Currency:    "USD",
		StockSingle: false,
	}

	if id := c.Param("id"); id != "" {
		rec, err := s.app.FindRecordById(catalog.CollectionProducts, id)
		if err != nil || rec == nil {
			s.renderErrorPage(c, http.StatusNotFound, "Not found", "That product does not exist.")
			return
		}
		view.IsNew = false
		view.Record = rec
		view.base = s.newBase(c, "Edit product", "catalog.products")

		currency := rec.GetString("currency")
		view.Name = rec.GetString("name")
		view.Slug = rec.GetString("slug")
		view.Subtitle = rec.GetString("subtitle")
		view.Description = rec.GetString("description")
		view.Kind = rec.GetString("kind")
		view.Status = rec.GetString("status")
		view.Brand = rec.GetString("brand")
		view.Category = rec.GetString("category")
		view.Currency = currency
		view.StockMode = rec.GetString("stockMode")
		view.StockSingle = rec.GetString("stockMode") == catalog.StockSingle
		view.Taxable = rec.GetBool("taxable")

		// Prices are rendered from minor units, not stored as text.
		view.ListPrice = money.FormatPlain(money.Amount(rec.GetFloat("listPrice")), currency)
		view.SalePrice = money.FormatPlain(money.Amount(rec.GetFloat("salePrice")), currency)
		view.CostPrice = money.FormatPlain(money.Amount(rec.GetFloat("costPrice")), currency)
		view.Stock = rec.GetInt("stock")
		view.LowStockThreshold = rec.GetInt("lowStockThreshold")
		view.WeightGrams = rec.GetInt("weightGrams")
		view.SEOTitle = rec.GetString("seoTitle")
		view.SEOKeywords = rec.GetString("seoKeywords")
		view.SEODescription = rec.GetString("seoDescription")

		for _, tag := range rec.GetStringSlice("tags") {
			view.Selected[tag] = true
		}
	}

	s.render(c, http.StatusOK, "catalog_product_form.html", view)
}

// handleProductSave creates or updates a product.
func (s *Server) handleProductSave(c *gin.Context) {
	id := strings.TrimSpace(c.PostForm("id"))

	col, err := s.app.FindCollectionByNameOrId(catalog.CollectionProducts)
	if err != nil || col == nil {
		s.renderErrorPage(c, http.StatusInternalServerError, "Schema missing", "The collection does not exist.")
		return
	}

	var rec *core.Record
	if id == "" {
		rec = core.NewRecord(col)
	} else {
		rec, err = s.app.FindRecordById(catalog.CollectionProducts, id)
		if err != nil || rec == nil {
			s.renderErrorPage(c, http.StatusNotFound, "Not found", "That product does not exist.")
			return
		}
	}

	name := strings.TrimSpace(c.PostForm("name"))
	currency := strings.ToUpper(strings.TrimSpace(c.PostForm("currency")))
	if name == "" || currency == "" {
		s.setFlash(c, "danger", "A product needs a name and a currency.")
		c.Redirect(http.StatusSeeOther, "/catalog/products")
		return
	}

	slug := strings.TrimSpace(c.PostForm("slug"))
	if slug == "" {
		slug = catalog.Slugify(name)
	}

	// Prices are parsed from the text that was typed, in minor units, without going
	// through a floating-point number. A price of 19.99 through a float64 is
	// 19.989999999999998, and truncating gives 1998.
	list, err := money.Parse(c.PostForm("listPrice"), currency)
	if err != nil {
		s.setFlash(c, "danger", "The list price: "+err.Error())
		c.Redirect(http.StatusSeeOther, "/catalog/products")
		return
	}
	sale, err := money.Parse(c.PostForm("salePrice"), currency)
	if err != nil {
		s.setFlash(c, "danger", "The sale price: "+err.Error())
		c.Redirect(http.StatusSeeOther, "/catalog/products")
		return
	}
	cost, err := money.Parse(c.PostForm("costPrice"), currency)
	if err != nil {
		s.setFlash(c, "danger", "The cost price: "+err.Error())
		c.Redirect(http.StatusSeeOther, "/catalog/products")
		return
	}

	// The sale price may not exceed the list price. A "sale" that costs more is a
	// data-entry error that would otherwise reach a customer, and it is invisible
	// in the form because the two fields are filled in separately.
	if err := money.ValidatePrice(list, sale, currency); err != nil {
		s.setFlash(c, "danger", err.Error())
		c.Redirect(http.StatusSeeOther, "/catalog/products")
		return
	}

	if cost > 0 && list > 0 && cost > list {
		// Not refused: a loss leader is a legitimate decision. It is reported,
		// because a cost above the price is far more often a typo than a strategy.
		s.setFlash(c, "warning", "The cost price is above the list price: every sale loses money.")
	}

	rec.Set("name", name)
	rec.Set("slug", slug)
	rec.Set("subtitle", strings.TrimSpace(c.PostForm("subtitle")))
	rec.Set("description", c.PostForm("description"))
	rec.Set("kind", nonEmptyString(c.PostForm("kind"), catalog.KindPhysical))
	rec.Set("status", nonEmptyString(c.PostForm("status"), "draft"))
	rec.Set("stockMode", nonEmptyString(c.PostForm("stockMode"), catalog.StockSingle))
	rec.Set("brand", strings.TrimSpace(c.PostForm("brand")))
	rec.Set("currency", currency)
	rec.Set("listPrice", list.MinorUnits())
	rec.Set("salePrice", sale.MinorUnits())
	rec.Set("costPrice", cost.MinorUnits())
	rec.Set("taxable", c.PostForm("taxable") != "")
	rec.Set("seoTitle", strings.TrimSpace(c.PostForm("seoTitle")))
	rec.Set("seoKeywords", strings.TrimSpace(c.PostForm("seoKeywords")))
	rec.Set("seoDescription", strings.TrimSpace(c.PostForm("seoDescription")))

	if category := strings.TrimSpace(c.PostForm("category")); category != "" {
		rec.Set("category", category)
	} else {
		rec.Set("category", nil)
	}

	for _, field := range []string{"stock", "lowStockThreshold", "weightGrams"} {
		if raw := strings.TrimSpace(c.PostForm(field)); raw != "" {
			rec.Set(field, raw)
		}
	}

	var tags []string
	for _, tag := range c.PostFormArray("tags") {
		if tag = strings.TrimSpace(tag); tag != "" {
			tags = append(tags, tag)
		}
	}
	rec.Set("tags", tags)

	if rec.IsNew() {
		if account := s.ownAccountQuiet(c); account != nil {
			rec.Set("createdBy", account.Id)
		}
		if rec.GetString("status") == "on_sale" {
			rec.Set("publishedAt", time.Now().UTC())
		}
	}

	if err := s.app.Save(rec); err != nil {
		s.setFlash(c, "danger", "Could not save: "+err.Error())
		c.Redirect(http.StatusSeeOther, "/catalog/products")
		return
	}

	s.audit(c, auditEntry{
		Event:      audit.EventSettingsChanged,
		Module:     "catalog",
		TargetType: "product",
		TargetID:   rec.Id,
		Severity:   severityInfo,
		Summary:    "Product \"" + name + "\" saved at " + money.Format(list, currency),
		Detail: map[string]any{
			"currency": currency, "listPrice": list.MinorUnits(),
			"salePrice": sale.MinorUnits(), "status": rec.GetString("status"),
		},
	})

	s.setFlash(c, "success", "Saved.")
	c.Redirect(http.StatusSeeOther, "/catalog/products/"+rec.Id+"/edit")
}

// handleProductDelete soft-deletes a product, unless an order references it.
func (s *Server) handleProductDelete(c *gin.Context) {
	rec, err := s.app.FindRecordById(catalog.CollectionProducts, c.Param("id"))
	if err != nil || rec == nil {
		s.renderErrorPage(c, http.StatusNotFound, "Not found", "That product does not exist.")
		return
	}

	// A product that has been ordered is never deleted, only taken off sale.
	// Removing it would leave the order line pointing at nothing, and an order that
	// cannot say what was bought is not a record of a sale.
	referenced, err := catalog.ReferencedByOrder(s.app, rec.Id)
	if err != nil {
		s.setFlash(c, "danger", "Could not check whether it has been ordered: "+err.Error())
		c.Redirect(http.StatusSeeOther, "/catalog/products")
		return
	}
	if referenced {
		s.setFlash(c, "danger",
			"That product has been ordered, so it cannot be deleted. Take it off sale instead.")
		c.Redirect(http.StatusSeeOther, "/catalog/products")
		return
	}

	rec.Set("deletedAt", time.Now().UTC())
	if err := s.app.Save(rec); err != nil {
		s.setFlash(c, "danger", "Could not delete: "+err.Error())
		c.Redirect(http.StatusSeeOther, "/catalog/products")
		return
	}

	s.audit(c, auditEntry{
		Event:      audit.EventAccountDeleted,
		Module:     "catalog",
		TargetType: "product",
		TargetID:   rec.Id,
		Severity:   severityWarning,
		Summary:    "Product \"" + rec.GetString("name") + "\" deleted",
	})
	s.setFlash(c, "success", "Deleted.")
	c.Redirect(http.StatusSeeOther, "/catalog/products")
}

// handleProductBulk applies one change to several products.
func (s *Server) handleProductBulk(c *gin.Context) {
	ids := c.PostFormArray("ids")
	if len(ids) == 0 {
		s.setFlash(c, "danger", "Nothing was selected.")
		c.Redirect(http.StatusSeeOther, "/catalog/products")
		return
	}

	action := c.PostForm("action")
	changed := 0
	skipped := 0

	for _, id := range ids {
		rec, err := s.app.FindRecordById(catalog.CollectionProducts, strings.TrimSpace(id))
		if err != nil || rec == nil {
			skipped++
			continue
		}

		switch action {
		case "on_sale":
			rec.Set("status", "on_sale")
			if rec.GetDateTime("publishedAt").IsZero() {
				rec.Set("publishedAt", time.Now().UTC())
			}
		case "off_shelf":
			rec.Set("status", "off_shelf")
		case "archive":
			rec.Set("status", "archived")
		case "price_change":
			// A percentage applied to every selected product, in minor units and
			// rounded once per product. Rounding once per product rather than once
			// per batch is what keeps a discounted catalogue summing correctly.
			percent, err := parsePercent(c.PostForm("percent"))
			if err != nil {
				s.setFlash(c, "danger", err.Error())
				c.Redirect(http.StatusSeeOther, "/catalog/products")
				return
			}
			list := money.Amount(rec.GetFloat("listPrice"))
			sale := money.Amount(rec.GetFloat("salePrice"))

			rec.Set("listPrice", money.Percent(list, percent).MinorUnits())
			if sale > 0 {
				rec.Set("salePrice", money.Percent(sale, percent).MinorUnits())
			}
		case "tag":
			tag := strings.TrimSpace(c.PostForm("tag"))
			if tag == "" {
				continue
			}
			tags := rec.GetStringSlice("tags")
			if !containsString(tags, tag) {
				rec.Set("tags", append(tags, tag))
			}
		case "category":
			rec.Set("category", strings.TrimSpace(c.PostForm("category")))
		default:
			s.setFlash(c, "danger", "Unrecognised action.")
			c.Redirect(http.StatusSeeOther, "/catalog/products")
			return
		}

		if err := s.app.Save(rec); err != nil {
			skipped++
			continue
		}
		changed++
	}

	s.audit(c, auditEntry{
		Event:      audit.EventSettingsChanged,
		Module:     "catalog",
		TargetType: "product",
		Severity:   severityWarning,
		Summary:    "Bulk " + action + " on " + itoa(changed) + " product(s)",
		Detail:     map[string]any{"action": action, "changed": changed, "skipped": skipped},
	})

	s.setFlash(c, "success", "Changed "+itoa(changed)+" product(s).")
	c.Redirect(http.StatusSeeOther, "/catalog/products")
}

// handleVariantList renders a product's variants and their stock.
func (s *Server) handleVariantList(c *gin.Context) {
	product, err := s.app.FindRecordById(catalog.CollectionProducts, c.Param("id"))
	if err != nil || product == nil {
		s.renderErrorPage(c, http.StatusNotFound, "Not found", "That product does not exist.")
		return
	}

	variants, err := s.app.FindRecordsByFilter(catalog.CollectionVariants,
		"product = {:p} && deletedAt = ''", "sku", 0, 0, map[string]any{"p": product.Id})
	if err != nil {
		variants = nil
	}

	attributes, _ := s.app.FindRecordsByFilter(catalog.CollectionAttributes,
		"product = {:p}", "sortOrder", 0, 0, map[string]any{"p": product.Id})

	ledger, _ := s.app.FindRecordsByFilter(catalog.CollectionStock,
		"", "-created", 50, 0)

	rows := make([]variantRow, 0, len(variants))
	for _, rec := range variants {
		price := catalog.Effective(product, rec)
		row := variantRow{
			Record:    rec,
			SKU:       rec.GetString("sku"),
			Barcode:   rec.GetString("barcode"),
			Label:     rec.GetString("label"),
			Status:    rec.GetString("status"),
			Stock:     rec.GetInt("stock"),
			Locked:    rec.GetInt("lockedStock"),
			Available: catalog.Available(rec),
			Charged:   money.Format(money.Amount(price.Charged()), price.Currency),
			OnSale:    price.OnSale(),
		}
		if row.Label == "" {
			row.Label = "Default"
		}
		rows = append(rows, row)
	}

	s.render(c, http.StatusOK, "catalog_variants.html", variantsPage{
		base:       s.newBase(c, product.GetString("name"), "catalog.products"),
		Product:    product,
		Rows:       rows,
		Attributes: attributes,
		Ledger:     ledger,
		CanManage:  s.actor(c).Can(permission.CatalogVariantManage),
		CanAdjust:  s.actor(c).Can(permission.CatalogStockAdjust),
	})
}

// handleVariantGenerate expands attributes into variants.
func (s *Server) handleVariantGenerate(c *gin.Context) {
	product, err := s.app.FindRecordById(catalog.CollectionProducts, c.Param("id"))
	if err != nil || product == nil {
		s.renderErrorPage(c, http.StatusNotFound, "Not found", "That product does not exist.")
		return
	}

	attributes, err := s.app.FindRecordsByFilter(catalog.CollectionAttributes,
		"product = {:p}", "sortOrder", 0, 0, map[string]any{"p": product.Id})
	if err != nil || len(attributes) == 0 {
		s.setFlash(c, "danger", "This product has no attributes, so there is no matrix to generate.")
		c.Redirect(http.StatusSeeOther, "/catalog/products/"+product.Id+"/variants")
		return
	}

	order := make([]string, 0, len(attributes))
	values := map[string][]string{}

	for _, attribute := range attributes {
		name := attribute.GetString("name")
		order = append(order, name)

		// PocketBase returns a json column as text when the record was loaded from
		// the database rather than set in this process, which is the case on every
		// request. Asserting the Go slice directly yields nothing, and the matrix
		// silently generates no variants -- which is how this first behaved.
		values[name] = attributeValues(attribute.Get("values"))
	}

	combinations, err := catalog.Matrix(order, values)
	if err != nil {
		s.setFlash(c, "danger", err.Error())
		c.Redirect(http.StatusSeeOther, "/catalog/products/"+product.Id+"/variants")
		return
	}

	existing, _ := s.app.FindRecordsByFilter(catalog.CollectionVariants,
		"product = {:p}", "", 0, 0, map[string]any{"p": product.Id})

	col, _ := s.app.FindCollectionByNameOrId(catalog.CollectionVariants)

	created := 0
	skipped := 0

	for _, combination := range combinations {
		label := catalog.Label(order, combination)

		matched := false
		for _, variant := range existing {
			if catalog.Matches(decodeOptions(variant.Get("options")), combination) {
				matched = true
				break
			}
		}
		if matched {
			// Existing variants are left alone, including their stock. Regenerating
			// a matrix must not reset a count that people have been maintaining.
			skipped++
			continue
		}

		// The combination is converted rather than assigned: it is a named map type
		// and the column takes a plain one.
		options := make(map[string]any, len(combination))
		for key, value := range combination {
			options[key] = value
		}

		rec := core.NewRecord(col)
		rec.Set("product", product.Id)
		rec.Set("options", options)
		rec.Set("label", label)
		rec.Set("sku", s.nextSKU(product, label))
		rec.Set("status", "active")

		if err := s.app.Save(rec); err != nil {
			s.setFlash(c, "danger", "Could not create a variant: "+err.Error())
			c.Redirect(http.StatusSeeOther, "/catalog/products/"+product.Id+"/variants")
			return
		}
		created++
	}

	s.audit(c, auditEntry{
		Event:      audit.EventSettingsChanged,
		Module:     "catalog",
		TargetType: "product",
		TargetID:   product.Id,
		Severity:   severityInfo,
		Summary:    "Generated " + itoa(created) + " variant(s) for \"" + product.GetString("name") + "\"",
		Detail:     map[string]any{"created": created, "existing": skipped},
	})

	if created == 0 {
		s.setFlash(c, "info", "Every combination already exists.")
	} else {
		s.setFlash(c, "success", "Created "+itoa(created)+" variant(s); "+itoa(skipped)+" already existed.")
	}
	c.Redirect(http.StatusSeeOther, "/catalog/products/"+product.Id+"/variants")
}

// nextSKU derives a SKU from the product slug and the variant label.
func (s *Server) nextSKU(product *core.Record, label string) string {
	base := strings.ToUpper(product.GetString("slug"))
	if base == "" {
		base = strings.ToUpper(catalog.Slugify(product.GetString("name")))
	}

	suffix := strings.ToUpper(catalog.Slugify(label))
	if suffix == "" || suffix == "DEFAULT" {
		suffix = "STD"
	}

	candidate := base + "-" + suffix

	// SKUs are unique, so a collision is resolved by numbering rather than by
	// failing: two products can legitimately share a label.
	for attempt := 2; attempt < 100; attempt++ {
		existing, _ := s.app.FindFirstRecordByFilter(catalog.CollectionVariants,
			"sku = {:sku}", map[string]any{"sku": candidate})
		if existing == nil {
			return candidate
		}
		candidate = base + "-" + suffix + "-" + itoa(attempt)
	}
	return base + "-" + suffix + "-" + itoa(int(time.Now().UnixNano()%100000))
}

// handleStockAdjust records a stock movement.
func (s *Server) handleStockAdjust(c *gin.Context) {
	variant, err := s.app.FindRecordById(catalog.CollectionVariants, c.Param("id"))
	if err != nil || variant == nil {
		s.renderErrorPage(c, http.StatusNotFound, "Not found", "That variant does not exist.")
		return
	}

	delta, err := parseIntStrict(c.PostForm("delta"))
	if err != nil {
		s.setFlash(c, "danger", "The change must be a whole number, positive or negative.")
		c.Redirect(http.StatusSeeOther, "/catalog/products/"+variant.GetString("product")+"/variants")
		return
	}

	reason := nonEmptyString(c.PostForm("reason"), catalog.ReasonAdjustment)
	var operator string
	if account := s.ownAccountQuiet(c); account != nil {
		operator = account.Id
	}

	// The ledger is written first and the cached balance second, in one place. A
	// stock number that can be edited directly has no history, and "why is this 3
	// and not 5" is the question that matters when a count is wrong.
	movement := catalog.Movement{
		Delta:    delta,
		Reason:   reason,
		Operator: operator,
		Note:     strings.TrimSpace(c.PostForm("note")),
	}

	if err := catalog.ApplyStock(s.app, variant, movement); err != nil {
		s.setFlash(c, "danger", err.Error())
		c.Redirect(http.StatusSeeOther, "/catalog/products/"+variant.GetString("product")+"/variants")
		return
	}

	s.audit(c, auditEntry{
		Event:      audit.EventSettingsChanged,
		Module:     "catalog",
		TargetType: "product_variant",
		TargetID:   variant.Id,
		Severity:   severityWarning,
		Summary: "Stock for " + variant.GetString("sku") + " changed by " +
			itoa(delta) + " (" + reason + "), now " + itoa(variant.GetInt("stock")),
		Detail: map[string]any{"delta": delta, "reason": reason, "balance": variant.GetInt("stock")},
	})

	s.setFlash(c, "success", "Stock is now "+itoa(variant.GetInt("stock"))+".")
	c.Redirect(http.StatusSeeOther, "/catalog/products/"+variant.GetString("product")+"/variants")
}

// handleStockReconcile recomputes a variant's balance from its ledger.
func (s *Server) handleStockReconcile(c *gin.Context) {
	variant, err := s.app.FindRecordById(catalog.CollectionVariants, c.Param("id"))
	if err != nil || variant == nil {
		s.renderErrorPage(c, http.StatusNotFound, "Not found", "That variant does not exist.")
		return
	}

	balance, err := catalog.ReconcileStock(s.app, variant.Id)
	if err != nil {
		s.setFlash(c, "danger", "Could not reconcile: "+err.Error())
	} else {
		s.setFlash(c, "success", "Recomputed from the ledger: "+itoa(balance)+".")
	}
	c.Redirect(http.StatusSeeOther, "/catalog/products/"+variant.GetString("product")+"/variants")
}

// attributeValues reads an attribute's stored value list.
//
// The column is json and arrives as types.JSONRaw, which a type switch does not
// match as []byte. Getting that wrong produced an empty list rather than an error,
// so the matrix generated no variants and said nothing about why.
func attributeValues(raw any) []string {
	items := decodeJSONArray(raw)

	out := make([]string, 0, len(items))
	for _, item := range items {
		switch v := item.(type) {
		case map[string]any:
			if value, ok := v["value"].(string); ok && strings.TrimSpace(value) != "" {
				out = append(out, strings.TrimSpace(value))
			}
		case string:
			if strings.TrimSpace(v) != "" {
				out = append(out, strings.TrimSpace(v))
			}
		}
	}
	return out
}

// decodeOptions reads a variant's stored options.
func decodeOptions(value any) catalog.Combination {
	out := catalog.Combination{}

	for key, raw := range decodeJSONObject(value) {
		if text, ok := raw.(string); ok {
			out[key] = text
		}
	}
	return out
}

// containsString reports whether a slice holds a value.
func containsString(have []string, want string) bool {
	for _, item := range have {
		if item == want {
			return true
		}
	}
	return false
}

// categoryOptions lists the categories for a picker.
func (s *Server) categoryOptions() []categoryOption {
	records, err := s.app.FindRecordsByFilter(catalog.CollectionCategories,
		"deletedAt = ''", "path", 0, 0)
	if err != nil {
		return nil
	}

	out := make([]categoryOption, 0, len(records))
	for _, rec := range records {
		out = append(out, categoryOption{
			ID:    rec.Id,
			Label: strings.Repeat("&nbsp;&nbsp;", rec.GetInt("level")) + rec.GetString("name"),
		})
	}
	return out
}

// parsePercent reads a percentage from a form.
func parsePercent(raw string) (float64, error) {
	raw = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(raw), "%"))
	if raw == "" {
		return 0, nil
	}

	value, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return 0, err
	}
	if value < -90 || value > 1000 {
		return 0, errPercentRange
	}
	return value, nil
}

var errPercentRange = errRange("the percentage must be between -90 and 1000")

type errRange string

func (e errRange) Error() string { return string(e) }

// parseIntStrict reads a whole number, refusing anything else.
func parseIntStrict(raw string) (int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, errRange("empty")
	}
	return strconv.Atoi(raw)
}

// productRow is one product on the list.
type productRow struct {
	Record     *core.Record
	Name       string
	Slug       string
	Subtitle   string
	Kind       string
	Status     string
	Brand      string
	Category   string
	List       string
	Sale       string
	Currency   string
	OnSale     bool
	Stock      int
	TrackStock bool
	LowStock   bool
	SKUs       int
}

// productsPage is the catalogue list.
type productsPage struct {
	base
	Rows       []productRow
	Categories []categoryOption
	Query      string
	Category   string
	Status     string
	Kind       string
	Brand      string
	LowStock   string
	Statuses   []string
	Kinds      []string
	CanCreate  bool
	CanUpdate  bool
	CanDelete  bool
	CanBulk    bool
}

// productForm is the product editor.
type productForm struct {
	base
	Record            *core.Record
	IsNew             bool
	Name              string
	Slug              string
	Subtitle          string
	Description       string
	Kind              string
	Status            string
	Brand             string
	Category          string
	Currency          string
	ListPrice         string
	SalePrice         string
	CostPrice         string
	StockMode         string
	StockSingle       bool
	Stock             int
	LowStockThreshold int
	WeightGrams       int
	Taxable           bool
	SEOTitle          string
	SEOKeywords       string
	SEODescription    string
	Kinds             []string
	StockModes        []string
	Statuses          []string
	Tags              []string
	Selected          map[string]bool
	Categories        []categoryOption
}

// variantRow is one variant.
type variantRow struct {
	Record    *core.Record
	SKU       string
	Barcode   string
	Label     string
	Status    string
	Stock     int
	Locked    int
	Available int
	Charged   string
	OnSale    bool
}

// variantsPage is the variant list for one product.
type variantsPage struct {
	base
	Product    *core.Record
	Rows       []variantRow
	Attributes []*core.Record
	Ledger     []*core.Record
	CanManage  bool
	CanAdjust  bool
}

// categoryOption is one selectable category.
type categoryOption struct {
	ID    string
	Label string
}
