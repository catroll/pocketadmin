package server

import (
	"encoding/csv"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/pocketbase/pocketbase/core"

	"pocketadmin/internal/audit"
	"pocketadmin/internal/money"
	"pocketadmin/internal/order"
	"pocketadmin/internal/permission"
)

// registerOrderRoutes mounts the order screens.
func (s *Server) registerOrderRoutes(panel *gin.RouterGroup) {
	s.readGuarded(panel, http.MethodGet, "/orders", permission.OrderList, s.handleOrderList)
	s.readGuarded(panel, http.MethodGet, "/orders/new", permission.OrderCreate, s.handleOrderForm)
	s.readGuarded(panel, http.MethodGet, "/orders/:id", permission.OrderView, s.handleOrderDetail)
	s.readGuarded(panel, http.MethodGet, "/orders/export.csv", permission.OrderExport, s.handleOrderExport)

	s.guarded(panel, http.MethodPost, "/orders/create", permission.OrderCreate, s.handleOrderCreate)
	s.guarded(panel, http.MethodPost, "/orders/:id/transition", permission.OrderFulfil, s.handleOrderTransition)
	s.guarded(panel, http.MethodPost, "/orders/:id/cancel", permission.OrderCancel, s.handleOrderCancel)
	s.guarded(panel, http.MethodPost, "/orders/:id/note", permission.OrderNote, s.handleOrderNote)
}

// orderFilter reads the search parameters.
//
// The whole filter is built in one place because the list and the export have to
// agree: an export that quietly includes rows the screen excluded is a spreadsheet
// that contradicts the page it came from.
func (s *Server) orderFilter(c *gin.Context) (string, map[string]any) {
	clauses := []string{"deletedAt = ''"}
	params := map[string]any{}

	if search := strings.TrimSpace(c.Query("q")); search != "" {
		clauses = append(clauses, "(orderNo ~ {:q} || user.email ~ {:q})")
		params["q"] = search
	}
	if status := strings.TrimSpace(c.Query("status")); status != "" {
		clauses = append(clauses, "status = {:status}")
		params["status"] = status
	}
	if payment := strings.TrimSpace(c.Query("paymentStatus")); payment != "" {
		clauses = append(clauses, "paymentStatus = {:payment}")
		params["payment"] = payment
	}
	if source := strings.TrimSpace(c.Query("source")); source != "" {
		clauses = append(clauses, "source = {:source}")
		params["source"] = source
	}
	if from := strings.TrimSpace(c.Query("from")); from != "" {
		if at, err := time.ParseInLocation("2006-01-02", from, time.Local); err == nil {
			clauses = append(clauses, "created >= {:from}")
			params["from"] = at.UTC()
		}
	}
	if to := strings.TrimSpace(c.Query("to")); to != "" {
		if at, err := time.ParseInLocation("2006-01-02", to, time.Local); err == nil {
			clauses = append(clauses, "created <= {:to}")
			params["to"] = at.Add(24*time.Hour - time.Nanosecond).UTC()
		}
	}
	if min := strings.TrimSpace(c.Query("minTotal")); min != "" {
		if amount, err := money.Parse(min, "USD"); err == nil {
			clauses = append(clauses, "grandTotal >= {:min}")
			params["min"] = amount.MinorUnits()
		}
	}
	if max := strings.TrimSpace(c.Query("maxTotal")); max != "" {
		if amount, err := money.Parse(max, "USD"); err == nil {
			clauses = append(clauses, "grandTotal <= {:max}")
			params["max"] = amount.MinorUnits()
		}
	}
	if coupon := strings.TrimSpace(c.Query("coupon")); coupon != "" {
		clauses = append(clauses, "couponGrants.code ~ {:coupon}")
		params["coupon"] = coupon
	}

	return strings.Join(clauses, " && "), params
}

// handleOrderList renders the orders.
func (s *Server) handleOrderList(c *gin.Context) {
	filter, params := s.orderFilter(c)

	records, err := s.app.FindRecordsByFilter(order.CollectionOrders,
		filter, "-created", 100, 0, params)
	if err != nil {
		s.renderErrorPage(c, http.StatusInternalServerError, "Could not load the orders", err.Error())
		return
	}

	rows := make([]orderRow, 0, len(records))
	for _, rec := range records {
		row := orderRow{
			Record:    rec,
			OrderNo:   rec.GetString("orderNo"),
			Status:    rec.GetString("status"),
			Payment:   rec.GetString("paymentStatus"),
			Source:    rec.GetString("source"),
			Total:     money.Format(money.Amount(rec.GetFloat("grandTotal")), rec.GetString("currency")),
			Paid:      money.Format(money.Amount(rec.GetFloat("amountPaid")), rec.GetString("currency")),
			Created:   rec.GetDateTime("created").Time().Local().Format("2 Jan 2006 15:04"),
			ItemCount: 0,
		}
		if user, err := s.app.FindRecordById("users", rec.GetString("user")); err == nil && user != nil {
			row.Email = user.Email()
		}
		if expires := rec.GetDateTime("expiresAt"); !expires.IsZero() && rec.GetString("status") == order.StatusPendingPayment {
			row.Expires = expires.Time().Local().Format("2 Jan 15:04")
			row.Expiring = expires.Time().Before(time.Now().Add(time.Hour))
		}
		if items, err := s.app.FindRecordsByFilter(order.CollectionItems,
			"order = {:o}", "", 0, 0, map[string]any{"o": rec.Id}); err == nil {
			row.ItemCount = len(items)
		}
		rows = append(rows, row)
	}

	counts := map[string]int{}
	for _, row := range rows {
		counts[row.Status]++
	}

	actor := s.actor(c)
	s.render(c, http.StatusOK, "orders.html", ordersPage{
		base:      s.newBase(c, "Orders", "orders"),
		Rows:      rows,
		Counts:    counts,
		Query:     c.Query("q"),
		Status:    c.Query("status"),
		Payment:   c.Query("paymentStatus"),
		Source:    c.Query("source"),
		From:      c.Query("from"),
		To:        c.Query("to"),
		MinTotal:  c.Query("minTotal"),
		MaxTotal:  c.Query("maxTotal"),
		Coupon:    c.Query("coupon"),
		Statuses:  order.Statuses(),
		Payments:  order.PaymentStatuses(),
		Sources:   []string{order.SourceWeb, order.SourceIOS, order.SourceAndroid, order.SourceAdmin, order.SourceSubscription, order.SourceImport},
		HasFilter: c.Request.URL.RawQuery != "",
		CanExport: actor.Can(permission.OrderExport),
		CanCreate: actor.Can(permission.OrderCreate),
	})
}

// handleOrderExport writes a CSV of the filtered list.
func (s *Server) handleOrderExport(c *gin.Context) {
	filter, params := s.orderFilter(c)

	records, err := s.app.FindRecordsByFilter(order.CollectionOrders,
		filter, "-created", 5000, 0, params)
	if err != nil {
		s.renderErrorPage(c, http.StatusInternalServerError, "Could not load the orders", err.Error())
		return
	}

	c.Header("Content-Type", "text/csv; charset=utf-8")
	c.Header("Content-Disposition", `attachment; filename="orders.csv"`)

	// A byte-order mark, so a spreadsheet opened by double-clicking reads the file as
	// UTF-8 rather than as the local codepage. Without it an accented name arrives
	// mangled and looks like the export is broken.
	_, _ = c.Writer.Write([]byte{0xEF, 0xBB, 0xBF})

	writer := csv.NewWriter(c.Writer)
	defer writer.Flush()

	_ = writer.Write([]string{"Order", "Placed", "Account", "Status", "Payment",
		"Currency", "Subtotal", "Member discount", "Coupon discount", "Shipping",
		"Tax", "Grand total", "Paid", "Refunded", "Source"})

	for _, rec := range records {
		email := ""
		if user, err := s.app.FindRecordById("users", rec.GetString("user")); err == nil && user != nil {
			email = user.Email()
		}

		_ = writer.Write([]string{
			rec.GetString("orderNo"),
			rec.GetDateTime("created").Time().UTC().Format(time.RFC3339),
			email,
			rec.GetString("status"),
			rec.GetString("paymentStatus"),
			rec.GetString("currency"),
			strconv.FormatInt(int64(rec.GetFloat("subtotal")), 10),
			strconv.FormatInt(int64(rec.GetFloat("memberDiscount")), 10),
			strconv.FormatInt(int64(rec.GetFloat("couponDiscount")), 10),
			strconv.FormatInt(int64(rec.GetFloat("shippingFee")), 10),
			strconv.FormatInt(int64(rec.GetFloat("taxAmount")), 10),
			strconv.FormatInt(int64(rec.GetFloat("grandTotal")), 10),
			strconv.FormatInt(int64(rec.GetFloat("amountPaid")), 10),
			strconv.FormatInt(int64(rec.GetFloat("amountRefunded")), 10),
			rec.GetString("source"),
		})
	}

	s.audit(c, auditEntry{
		Event:      audit.EventDataExported,
		Module:     "orders",
		TargetType: "order",
		Severity:   severityWarning,
		Summary:    "Exported " + itoa(len(records)) + " order(s) as CSV",
		Detail:     map[string]any{"filter": c.Request.URL.RawQuery},
	})
}

// handleOrderDetail renders one order.
func (s *Server) handleOrderDetail(c *gin.Context) {
	rec, err := s.app.FindRecordById(order.CollectionOrders, c.Param("id"))
	if err != nil || rec == nil {
		s.renderErrorPage(c, http.StatusNotFound, "Not found", "That order does not exist.")
		return
	}

	items, _ := s.app.FindRecordsByFilter(order.CollectionItems,
		"order = {:o}", "created", 0, 0, map[string]any{"o": rec.Id})
	events, _ := s.app.FindRecordsByFilter(order.CollectionEvents,
		"order = {:o}", "-created", 200, 0, map[string]any{"o": rec.Id})

	view := orderDetailPage{
		base:      s.newBase(c, rec.GetString("orderNo"), "orders"),
		Record:    rec,
		Items:     items,
		Events:    events,
		Currency:  rec.GetString("currency"),
		CanFulfil: s.actor(c).Can(permission.OrderFulfil),
		CanCancel: s.actor(c).Can(permission.OrderCancel),
		CanNote:   s.actor(c).Can(permission.OrderNote),
	}

	if user, err := s.app.FindRecordById("users", rec.GetString("user")); err == nil && user != nil {
		view.Email = user.Email()
	}
	if tier, err := s.app.FindRecordById("member_tiers", rec.GetString("memberTier")); err == nil && tier != nil {
		view.MemberTier = tier.GetString("name")
	}

	// The legal next states, computed from the state machine rather than listed again
	// here. A second list is a second place for the two to disagree, and the one on
	// the screen is the one an operator would believe.
	view.NextStates = order.NextStates(rec.GetString("status"))

	view.Subtotal = money.Format(money.Amount(rec.GetFloat("subtotal")), view.Currency)
	view.MemberDiscount = money.Format(money.Amount(rec.GetFloat("memberDiscount")), view.Currency)
	view.CouponDiscount = money.Format(money.Amount(rec.GetFloat("couponDiscount")), view.Currency)
	view.DiscountTotal = money.Format(money.Amount(rec.GetFloat("discountTotal")), view.Currency)
	view.Shipping = money.Format(money.Amount(rec.GetFloat("shippingFee")), view.Currency)
	view.Tax = money.Format(money.Amount(rec.GetFloat("taxAmount")), view.Currency)
	view.GrandTotal = money.Format(money.Amount(rec.GetFloat("grandTotal")), view.Currency)
	view.Paid = money.Format(money.Amount(rec.GetFloat("amountPaid")), view.Currency)
	view.Refunded = money.Format(money.Amount(rec.GetFloat("amountRefunded")), view.Currency)

	rows := make([]orderItemRow, 0, len(items))
	for _, item := range items {
		rows = append(rows, orderItemRow{
			Record:      item,
			Name:        item.GetString("productName"),
			Variant:     item.GetString("variantLabel"),
			SKU:         item.GetString("sku"),
			Image:       item.GetString("image"),
			Quantity:    item.GetInt("quantity"),
			UnitPrice:   money.Format(money.Amount(item.GetFloat("unitPrice")), view.Currency),
			Subtotal:    money.Format(money.Amount(item.GetFloat("subtotal")), view.Currency),
			Discount:    money.Format(money.Amount(item.GetFloat("discountAmount")), view.Currency),
			Total:       money.Format(money.Amount(item.GetFloat("total")), view.Currency),
			RefundedQty: item.GetInt("refundedQuantity"),
		})
	}
	view.ItemRows = rows

	s.render(c, http.StatusOK, "order_detail.html", view)
}

// handleOrderForm renders the administrative order builder.
func (s *Server) handleOrderForm(c *gin.Context) {
	products, _ := s.app.FindRecordsByFilter("products",
		"status = 'on_sale' && deletedAt = ''", "name", 200, 0)

	options := make([]orderProductOption, 0, len(products))
	for _, rec := range products {
		variants, _ := s.app.FindRecordsByFilter("product_variants",
			"product = {:p}", "sortOrder", 50, 0, map[string]any{"p": rec.Id})

		option := orderProductOption{
			ID:       rec.Id,
			Name:     rec.GetString("name"),
			SKU:      rec.GetString("sku"),
			Currency: rec.GetString("currency"),
		}
		if price := int64(rec.GetFloat("salePrice")); price > 0 {
			option.Price = price
		} else {
			option.Price = int64(rec.GetFloat("listPrice"))
		}
		option.PriceText = money.Format(money.Amount(option.Price), option.Currency)

		for _, variant := range variants {
			option.Variants = append(option.Variants, orderVariantOption{
				ID:    variant.Id,
				Label: variant.GetString("label"),
				SKU:   variant.GetString("sku"),
				Stock: variant.GetInt("stock"),
			})
		}
		options = append(options, option)
	}

	s.render(c, http.StatusOK, "order_form.html", orderFormPage{
		base:     s.newBase(c, "New order", "orders"),
		Products: options,
	})
}

// handleOrderCreate places an order on somebody's behalf.
func (s *Server) handleOrderCreate(c *gin.Context) {
	email := strings.TrimSpace(c.PostForm("email"))
	if email == "" {
		s.setFlash(c, "danger", "An order needs an account.")
		c.Redirect(http.StatusSeeOther, "/orders/new")
		return
	}

	user, err := s.app.FindFirstRecordByFilter("users",
		"email = {:e}", map[string]any{"e": email})
	if err != nil || user == nil {
		s.setFlash(c, "danger", "No account has that address.")
		c.Redirect(http.StatusSeeOther, "/orders/new")
		return
	}

	// The lines arrive as parallel arrays, which is what an HTML form can express
	// without JavaScript.
	productIDs := c.PostFormArray("product")
	variantIDs := c.PostFormArray("variant")
	quantities := c.PostFormArray("quantity")

	lines := make([]order.Line, 0, len(productIDs))
	currency := "USD"

	for i, productID := range productIDs {
		if strings.TrimSpace(productID) == "" {
			continue
		}

		quantity := 0
		if i < len(quantities) {
			if parsed, err := parseIntStrict(quantities[i]); err == nil {
				quantity = parsed
			}
		}
		if quantity <= 0 {
			continue
		}

		product, err := s.app.FindRecordById("products", productID)
		if err != nil || product == nil {
			continue
		}

		variantID := ""
		if i < len(variantIDs) {
			variantID = variantIDs[i]
		}

		// The snapshots are taken now, once, so the order records what it bought.
		line := order.Line{
			ProductID:   productID,
			VariantID:   variantID,
			CategoryID:  product.GetString("category"),
			ProductName: product.GetString("name"),
			SKU:         product.GetString("sku"),
			UnitPrice:   int64(product.GetFloat("listPrice")),
			Quantity:    quantity,
			Currency:    product.GetString("currency"),
		}
		if sale := int64(product.GetFloat("salePrice")); sale > 0 && sale < line.UnitPrice {
			line.UnitPrice = sale
		}
		if line.Currency != "" {
			currency = line.Currency
		}

		if variantID != "" {
			if variant, err := s.app.FindRecordById("product_variants", variantID); err == nil && variant != nil {
				line.VariantLabel = variant.GetString("label")
				if variantSKU := variant.GetString("sku"); variantSKU != "" {
					line.SKU = variantSKU
				}
				if price := int64(variant.GetFloat("price")); price > 0 {
					line.UnitPrice = price
				}
			}
		}

		lines = append(lines, line)
	}

	if len(lines) == 0 {
		s.setFlash(c, "danger", "Add at least one item.")
		c.Redirect(http.StatusSeeOther, "/orders/new")
		return
	}

	// The membership discount is read from the account's tier, so an administrative
	// order is priced the same way a self-service one would be.
	memberRate := 1.0
	memberTierID := ""
	if active, err := membershipActive(s.app, user.Id, time.Now()); err == nil && active != nil {
		memberTierID = active.TierID
		memberRate = active.Rate
	}

	input := order.Input{
		Lines:      lines,
		Currency:   currency,
		MemberRate: memberRate,
	}

	if raw := strings.TrimSpace(c.PostForm("shippingFee")); raw != "" {
		if amount, err := money.Parse(raw, currency); err == nil {
			input.ShippingFee = amount.MinorUnits()
		}
	}
	if raw := strings.TrimSpace(c.PostForm("taxRate")); raw != "" {
		if rate, err := strconv.ParseFloat(raw, 64); err == nil {
			input.TaxRate = rate
		}
	}

	var placedBy string
	if account := s.ownAccountQuiet(c); account != nil {
		placedBy = account.Id
	}

	rec, err := order.Create(s.app, order.CreateRequest{
		UserID:       user.Id,
		Source:       order.SourceAdmin,
		Lines:        lines,
		Pricing:      input,
		MemberTierID: memberTierID,
		CustomerNote: strings.TrimSpace(c.PostForm("customerNote")),
		PlacedBy:     placedBy,
		Reason:       "created by an administrator",
		Now:          time.Now(),
	})
	if err != nil {
		s.setFlash(c, "danger", err.Error())
		c.Redirect(http.StatusSeeOther, "/orders/new")
		return
	}

	s.audit(c, auditEntry{
		Event:      audit.EventSettingsChanged,
		Module:     "orders",
		TargetType: "order",
		TargetID:   rec.Id,
		Severity:   severityWarning,
		Summary:    "Created order " + rec.GetString("orderNo") + " for " + email,
		Detail:     map[string]any{"grandTotal": rec.GetFloat("grandTotal"), "currency": currency},
	})
	s.setFlash(c, "success", "Order "+rec.GetString("orderNo")+" created.")
	c.Redirect(http.StatusSeeOther, "/orders/"+rec.Id)
}

// handleOrderTransition moves an order through its state machine.
func (s *Server) handleOrderTransition(c *gin.Context) {
	rec, err := s.app.FindRecordById(order.CollectionOrders, c.Param("id"))
	if err != nil || rec == nil {
		s.renderErrorPage(c, http.StatusNotFound, "Not found", "That order does not exist.")
		return
	}

	var actor string
	if account := s.ownAccountQuiet(c); account != nil {
		actor = account.Id
	}

	target := strings.TrimSpace(c.PostForm("status"))
	reason := strings.TrimSpace(c.PostForm("reason"))

	if err := order.Transition(s.app, rec, target, actor, reason, time.Now()); err != nil {
		// The state machine's refusal is shown as-is: it names both ends and what to
		// do instead, which is more use than a generic failure.
		s.setFlash(c, "danger", err.Error())
		c.Redirect(http.StatusSeeOther, "/orders/"+rec.Id)
		return
	}

	s.audit(c, auditEntry{
		Event:      audit.EventSettingsChanged,
		Module:     "orders",
		TargetType: "order",
		TargetID:   rec.Id,
		Severity:   severityWarning,
		Summary:    "Order " + rec.GetString("orderNo") + " moved to " + target,
	})
	s.setFlash(c, "success", "The order is now "+target+".")
	c.Redirect(http.StatusSeeOther, "/orders/"+rec.Id)
}

// handleOrderCancel cancels or closes an order.
func (s *Server) handleOrderCancel(c *gin.Context) {
	rec, err := s.app.FindRecordById(order.CollectionOrders, c.Param("id"))
	if err != nil || rec == nil {
		s.renderErrorPage(c, http.StatusNotFound, "Not found", "That order does not exist.")
		return
	}

	var actor string
	if account := s.ownAccountQuiet(c); account != nil {
		actor = account.Id
	}

	close := c.PostForm("action") == "close"
	reason := strings.TrimSpace(c.PostForm("reason"))

	if err := order.CancelOrClose(s.app, rec, close, actor, reason, time.Now()); err != nil {
		s.setFlash(c, "danger", err.Error())
		c.Redirect(http.StatusSeeOther, "/orders/"+rec.Id)
		return
	}

	s.audit(c, auditEntry{
		Event:      audit.EventSettingsChanged,
		Module:     "orders",
		TargetType: "order",
		TargetID:   rec.Id,
		Severity:   severityCritical,
		Summary:    "Order " + rec.GetString("orderNo") + " " + rec.GetString("status") + ": " + reason,
	})
	s.setFlash(c, "success", "The order was "+rec.GetString("status")+" and its stock returned.")
	c.Redirect(http.StatusSeeOther, "/orders/"+rec.Id)
}

// handleOrderNote adds an administrator note.
func (s *Server) handleOrderNote(c *gin.Context) {
	rec, err := s.app.FindRecordById(order.CollectionOrders, c.Param("id"))
	if err != nil || rec == nil {
		s.renderErrorPage(c, http.StatusNotFound, "Not found", "That order does not exist.")
		return
	}

	note := strings.TrimSpace(c.PostForm("adminNote"))
	rec.Set("adminNote", note)
	if err := s.app.Save(rec); err != nil {
		s.setFlash(c, "danger", "Could not save the note: "+err.Error())
		c.Redirect(http.StatusSeeOther, "/orders/"+rec.Id)
		return
	}

	var actor string
	if account := s.ownAccountQuiet(c); account != nil {
		actor = account.Id
	}

	// The note goes on the timeline as well as in the field, because the field holds
	// only the latest one and the timeline is what answers what was said when.
	if err := order.RecordEvent(s.app, rec, order.EventRequest{
		Event: "note_added", Actor: actor, Reason: "administrator note", Now: time.Now(),
	}); err != nil {
		s.setFlash(c, "warning", "Saved, but the timeline entry failed: "+err.Error())
		c.Redirect(http.StatusSeeOther, "/orders/"+rec.Id)
		return
	}

	s.setFlash(c, "success", "Note saved.")
	c.Redirect(http.StatusSeeOther, "/orders/"+rec.Id)
}

// membershipActive is the small piece of the membership package the order screen
// needs, kept here so the two packages do not have to import each other.
func membershipActive(app core.App, userID string, now time.Time) (*activeMembership, error) {
	records, err := app.FindRecordsByFilter("user_memberships",
		"user = {:u} && (status = 'active' || status = 'trialing')",
		"-created", 20, 0, map[string]any{"u": userID})
	if err != nil {
		return nil, err
	}

	best := &activeMembership{Rate: 1.0}
	bestLevel := -1

	for _, rec := range records {
		if expiry := rec.GetDateTime("expireAt"); !expiry.IsZero() && expiry.Time().Before(now) {
			continue
		}

		if rate, err := membershipDiscountRate(app, rec.GetString("tier")); err == nil && rate < best.Rate {
			best.Rate = rate
		}
		if !best.Set {
			best.TierID = rec.GetString("tier")
			best.Set = true
			bestLevel = 0
		}
		_ = bestLevel
	}

	if !best.Set {
		return nil, nil
	}
	return best, nil
}

// membershipDiscountRate reads a tier's discount rate.
func membershipDiscountRate(app core.App, tierID string) (float64, error) {
	benefits, err := app.FindRecordsByFilter("member_tier_benefits",
		"tier = {:t} && kind = 'discount_rate'", "", 0, 0, map[string]any{"t": tierID})
	if err != nil {
		return 1.0, err
	}

	for _, benefit := range benefits {
		value := decodeJSONObject(benefit.Get("value"))
		if rate, ok := value["rate"].(float64); ok && rate > 0 && rate <= 1 {
			return rate, nil
		}
	}
	return 1.0, nil
}

// activeMembership is what the order screen needs from a membership.
type activeMembership struct {
	TierID string
	Rate   float64
	Set    bool
}

// orderRow is one order on the list.
type orderRow struct {
	Record    *core.Record
	OrderNo   string
	Email     string
	Status    string
	Payment   string
	Source    string
	Total     string
	Paid      string
	Created   string
	Expires   string
	Expiring  bool
	ItemCount int
}

// ordersPage is the order list.
type ordersPage struct {
	base
	Rows      []orderRow
	Counts    map[string]int
	Query     string
	Status    string
	Payment   string
	Source    string
	From      string
	To        string
	MinTotal  string
	MaxTotal  string
	Coupon    string
	RawQuery  string
	Statuses  []string
	Payments  []string
	Sources   []string
	HasFilter bool
	CanExport bool
	CanCreate bool
}

// orderItemRow is one item on an order.
type orderItemRow struct {
	Record      *core.Record
	Name        string
	Variant     string
	SKU         string
	Image       string
	Quantity    int
	UnitPrice   string
	Subtotal    string
	Discount    string
	Total       string
	RefundedQty int
}

// orderDetailPage is one order.
type orderDetailPage struct {
	base
	Record         *core.Record
	Email          string
	MemberTier     string
	Currency       string
	ItemRows       []orderItemRow
	Items          []*core.Record
	Events         []*core.Record
	NextStates     []string
	Subtotal       string
	MemberDiscount string
	CouponDiscount string
	DiscountTotal  string
	Shipping       string
	Tax            string
	GrandTotal     string
	Paid           string
	Refunded       string
	CanFulfil      bool
	CanCancel      bool
	CanNote        bool
}

// orderProductOption is one selectable product on the builder.
type orderProductOption struct {
	ID        string
	Name      string
	SKU       string
	Currency  string
	Price     int64
	PriceText string
	Variants  []orderVariantOption
}

// orderVariantOption is one selectable variant.
type orderVariantOption struct {
	ID    string
	Label string
	SKU   string
	Stock int
}

// orderFormPage is the administrative order builder.
type orderFormPage struct {
	base
	Products []orderProductOption
}
