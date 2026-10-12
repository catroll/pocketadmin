package server

import (
	"crypto/rand"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/pocketbase/pocketbase/core"

	"pocketadmin/internal/audit"
	"pocketadmin/internal/money"
	"pocketadmin/internal/permission"
	"pocketadmin/internal/promotion"
)

// registerPromotionRoutes mounts the promotion screens.
func (s *Server) registerPromotionRoutes(panel *gin.RouterGroup) {
	s.readGuarded(panel, http.MethodGet, "/promotions/coupons", permission.PromotionCouponList, s.handleCouponList)
	s.readGuarded(panel, http.MethodGet, "/promotions/coupons/new", permission.PromotionCouponManage, s.handleCouponForm)
	s.readGuarded(panel, http.MethodGet, "/promotions/coupons/:id/edit", permission.PromotionCouponManage, s.handleCouponForm)
	s.readGuarded(panel, http.MethodGet, "/promotions/campaigns", permission.PromotionCampaignList, s.handleCampaignList)
	s.readGuarded(panel, http.MethodGet, "/promotions/grants", permission.PromotionGrantList, s.handleGrantList)

	s.guarded(panel, http.MethodPost, "/promotions/coupons/save", permission.PromotionCouponManage, s.handleCouponSave)
	s.guarded(panel, http.MethodPost, "/promotions/coupons/:id/delete", permission.PromotionCouponManage, s.handleCouponDelete)
	s.guarded(panel, http.MethodPost, "/promotions/campaigns/save", permission.PromotionCampaignManage, s.handleCampaignSave)
	s.guarded(panel, http.MethodPost, "/promotions/grants/issue", permission.PromotionGrantIssue, s.handleGrantIssue)
	s.guarded(panel, http.MethodPost, "/promotions/grants/:id/void", permission.PromotionGrantVoid, s.handleGrantVoid)
}

// handleCouponList renders the coupon templates.
func (s *Server) handleCouponList(c *gin.Context) {
	filter := "deletedAt = ''"
	params := map[string]any{}

	if status := strings.TrimSpace(c.Query("status")); status != "" {
		filter += " && status = {:status}"
		params["status"] = status
	}
	if search := strings.TrimSpace(c.Query("q")); search != "" {
		filter += " && (name ~ {:q} || code ~ {:q})"
		params["q"] = search
	}

	records, err := s.app.FindRecordsByFilter(promotion.CollectionCoupons,
		filter, "-created", 200, 0, params)
	if err != nil {
		s.renderErrorPage(c, http.StatusInternalServerError, "Could not load the coupons", err.Error())
		return
	}

	rows := make([]couponRow, 0, len(records))
	for _, rec := range records {
		row := couponRow{
			Record:     rec,
			Name:       rec.GetString("name"),
			Code:       rec.GetString("code"),
			Type:       rec.GetString("type"),
			Status:     rec.GetString("status"),
			Currency:   rec.GetString("currency"),
			Validity:   rec.GetString("validityMode"),
			Stackable:  rec.GetBool("stackable"),
			WithMember: rec.GetBool("stacksWithMemberDiscount"),
		}

		// The value is rendered per type, because one column holds three units:
		// minor units, basis points and nothing at all.
		switch row.Type {
		case promotion.TypePercentage:
			row.Value = formatBasisPoints(rec.GetInt("value"))
		case promotion.TypeFixedAmount:
			row.Value = money.Format(money.Amount(rec.GetFloat("value")), row.Currency)
		default:
			row.Value = "free shipping"
		}

		if cap := int64(rec.GetFloat("maxDiscount")); cap > 0 {
			row.Cap = money.Format(money.Amount(cap), row.Currency)
		}
		if min := int64(rec.GetFloat("minSpend")); min > 0 {
			row.Minimum = money.Format(money.Amount(min), row.Currency)
		}
		if total := rec.GetInt("totalQuantity"); total > 0 {
			issued, _ := promotion.IssuedTo(s.app, rec.Id, "")
			_ = issued
			row.Quantity = itoa(total)
		} else {
			row.Quantity = "unlimited"
		}

		// The counters are read from the grants rather than from the denormalised
		// columns, because a count that only ever goes up is wrong the first time
		// anything else touches the rows.
		grants, _ := s.app.FindRecordsByFilter(promotion.CollectionGrants,
			"coupon = {:c}", "", 0, 0, map[string]any{"c": rec.Id})
		for _, g := range grants {
			switch g.GetString("status") {
			case promotion.GrantUsed:
				row.Used++
			case promotion.GrantVoided:
			default:
				row.Issued++
			}
		}
		rows = append(rows, row)
	}

	actor := s.actor(c)
	s.render(c, http.StatusOK, "promotion_coupons.html", couponsPage{
		base:      s.newBase(c, "Coupons", "promotions.coupons"),
		Rows:      rows,
		Query:     c.Query("q"),
		Status:    c.Query("status"),
		Statuses:  []string{"draft", "active", "paused", "expired"},
		CanManage: actor.Can(permission.PromotionCouponManage),
	})
}

// handleCouponForm renders the coupon editor.
func (s *Server) handleCouponForm(c *gin.Context) {
	view := couponForm{
		base:       s.newBase(c, "New coupon", "promotions.coupons"),
		IsNew:      true,
		Types:      []string{promotion.TypeFixedAmount, promotion.TypePercentage, promotion.TypeFreeShipping},
		Validities: []string{promotion.ValidityAbsolute, promotion.ValidityRelative},
		Applies:    []string{promotion.ApplyAll, promotion.ApplyCategories, promotion.ApplyProducts, promotion.ApplyTiers},
		Statuses:   []string{"draft", "active", "paused", "expired"},
		Currency:   "USD",
		Campaigns:  s.campaignOptions(),
	}

	if id := c.Param("id"); id != "" {
		rec, err := s.app.FindRecordById(promotion.CollectionCoupons, id)
		if err != nil || rec == nil {
			s.renderErrorPage(c, http.StatusNotFound, "Not found", "That coupon does not exist.")
			return
		}
		view.IsNew = false
		view.Record = rec
		view.base = s.newBase(c, "Edit coupon", "promotions.coupons")

		view.Name = rec.GetString("name")
		view.Code = rec.GetString("code")
		view.Type = rec.GetString("type")
		view.Status = rec.GetString("status")
		view.Currency = rec.GetString("currency")
		view.Validity = rec.GetString("validityMode")
		view.Applicability = rec.GetString("applicability")
		view.ValidDays = rec.GetInt("validDays")
		view.TotalQuantity = rec.GetInt("totalQuantity")
		view.PerUserLimit = rec.GetInt("perUserLimit")
		view.MaxStacked = rec.GetInt("maxStacked")
		view.Stackable = rec.GetBool("stackable")
		view.WithMember = rec.GetBool("stacksWithMemberDiscount")
		view.Campaign = rec.GetString("campaign")

		// The three units are rendered back into the one a person typed.
		switch view.Type {
		case promotion.TypePercentage:
			view.Value = formatBasisPoints(rec.GetInt("value"))
		case promotion.TypeFixedAmount:
			view.Value = money.FormatPlain(money.Amount(rec.GetFloat("value")), view.Currency)
		}
		if cap := rec.GetFloat("maxDiscount"); cap > 0 {
			view.MaxDiscount = money.FormatPlain(money.Amount(cap), view.Currency)
		}
		if min := rec.GetFloat("minSpend"); min > 0 {
			view.MinSpend = money.FormatPlain(money.Amount(min), view.Currency)
		}
		if from := rec.GetDateTime("validFrom"); !from.IsZero() {
			view.ValidFrom = from.Time().Local().Format("2006-01-02")
		}
		if to := rec.GetDateTime("validTo"); !to.IsZero() {
			view.ValidTo = to.Time().Local().Format("2006-01-02")
		}
	}

	s.render(c, http.StatusOK, "promotion_coupon_form.html", view)
}

// handleCouponSave creates or updates a coupon.
func (s *Server) handleCouponSave(c *gin.Context) {
	id := strings.TrimSpace(c.PostForm("id"))

	col, err := s.app.FindCollectionByNameOrId(promotion.CollectionCoupons)
	if err != nil || col == nil {
		s.renderErrorPage(c, http.StatusInternalServerError, "Schema missing", "The collection does not exist.")
		return
	}

	var rec *core.Record
	if id == "" {
		rec = core.NewRecord(col)
	} else {
		rec, err = s.app.FindRecordById(promotion.CollectionCoupons, id)
		if err != nil || rec == nil {
			s.renderErrorPage(c, http.StatusNotFound, "Not found", "That coupon does not exist.")
			return
		}
	}

	name := strings.TrimSpace(c.PostForm("name"))
	couponType := nonEmptyString(c.PostForm("type"), promotion.TypePercentage)
	currency := strings.ToUpper(strings.TrimSpace(c.PostForm("currency")))

	if name == "" {
		s.setFlash(c, "danger", "A coupon needs a name.")
		c.Redirect(http.StatusSeeOther, "/promotions/coupons")
		return
	}

	rec.Set("name", name)
	rec.Set("code", strings.TrimSpace(c.PostForm("code")))
	rec.Set("type", couponType)
	rec.Set("status", nonEmptyString(c.PostForm("status"), "draft"))
	rec.Set("currency", currency)
	rec.Set("validityMode", nonEmptyString(c.PostForm("validityMode"), promotion.ValidityAbsolute))
	rec.Set("applicability", nonEmptyString(c.PostForm("applicability"), promotion.ApplyAll))
	rec.Set("stackable", c.PostForm("stackable") != "")
	rec.Set("stacksWithMemberDiscount", c.PostForm("stacksWithMemberDiscount") != "")

	// The value's unit follows the type. A percentage is stored in basis points,
	// which is what keeps 12.5% representable in an integer column; a fixed amount
	// is parsed in minor units without going through a float.
	switch couponType {
	case promotion.TypePercentage:
		percent, err := parsePercent(c.PostForm("value"))
		if err != nil {
			s.setFlash(c, "danger", "The percentage: "+err.Error())
			c.Redirect(http.StatusSeeOther, "/promotions/coupons")
			return
		}
		if percent < 0 || percent > 100 {
			s.setFlash(c, "danger", "A percentage discount is between 0 and 100.")
			c.Redirect(http.StatusSeeOther, "/promotions/coupons")
			return
		}
		rec.Set("value", int(percent*100))

	case promotion.TypeFixedAmount:
		amount, err := money.Parse(c.PostForm("value"), currency)
		if err != nil {
			s.setFlash(c, "danger", "The amount: "+err.Error())
			c.Redirect(http.StatusSeeOther, "/promotions/coupons")
			return
		}
		rec.Set("value", amount.MinorUnits())
	}

	// The caps and the minimum are money, and are parsed the same way.
	for field, label := range map[string]string{
		"maxDiscount": "The maximum discount",
		"minSpend":    "The minimum spend",
	} {
		raw := strings.TrimSpace(c.PostForm(field))
		if raw == "" {
			rec.Set(field, 0)
			continue
		}
		amount, err := money.Parse(raw, currency)
		if err != nil {
			s.setFlash(c, "danger", label+": "+err.Error())
			c.Redirect(http.StatusSeeOther, "/promotions/coupons")
			return
		}
		rec.Set(field, amount.MinorUnits())
	}

	for _, field := range []string{"validDays", "totalQuantity", "perUserLimit", "maxStacked"} {
		if raw := strings.TrimSpace(c.PostForm(field)); raw != "" {
			rec.Set(field, raw)
		}
	}

	for _, field := range []string{"validFrom", "validTo"} {
		raw := strings.TrimSpace(c.PostForm(field))
		if raw == "" {
			rec.Set(field, nil)
			continue
		}
		at, err := time.ParseInLocation("2006-01-02", raw, time.Local)
		if err != nil {
			s.setFlash(c, "danger", "That date could not be read.")
			c.Redirect(http.StatusSeeOther, "/promotions/coupons")
			return
		}
		// The end of the chosen day for validTo, so "valid to the 5th" includes the
		// 5th rather than stopping at its first instant.
		if field == "validTo" {
			at = at.Add(24*time.Hour - time.Nanosecond)
		}
		rec.Set(field, at.UTC())
	}

	if campaign := strings.TrimSpace(c.PostForm("campaign")); campaign != "" {
		rec.Set("campaign", campaign)
	} else {
		rec.Set("campaign", nil)
	}

	if rec.IsNew() {
		if account := s.ownAccountQuiet(c); account != nil {
			rec.Set("createdBy", account.Id)
		}
	}

	if err := s.app.Save(rec); err != nil {
		s.setFlash(c, "danger", "Could not save: "+err.Error())
		c.Redirect(http.StatusSeeOther, "/promotions/coupons")
		return
	}

	s.audit(c, auditEntry{
		Event:      audit.EventSettingsChanged,
		Module:     "promotions",
		TargetType: "coupon",
		TargetID:   rec.Id,
		Severity:   severityWarning,
		Summary:    "Coupon \"" + name + "\" saved",
		Detail:     map[string]any{"type": couponType, "value": rec.GetFloat("value"), "currency": currency},
	})
	s.setFlash(c, "success", "Saved.")
	c.Redirect(http.StatusSeeOther, "/promotions/coupons")
}

// handleCouponDelete soft-deletes a coupon.
func (s *Server) handleCouponDelete(c *gin.Context) {
	rec, err := s.app.FindRecordById(promotion.CollectionCoupons, c.Param("id"))
	if err != nil || rec == nil {
		s.renderErrorPage(c, http.StatusNotFound, "Not found", "That coupon does not exist.")
		return
	}

	// A coupon that has been issued is taken out of circulation rather than deleted.
	// Removing it would leave every grant pointing at nothing, and the codes people
	// are holding would stop meaning anything -- including the ones they paid
	// attention to.
	issued, _ := s.app.FindRecordsByFilter(promotion.CollectionGrants,
		"coupon = {:c}", "", 1, 0, map[string]any{"c": rec.Id})
	if len(issued) > 0 {
		rec.Set("status", "expired")
		if err := s.app.Save(rec); err != nil {
			s.setFlash(c, "danger", "Could not expire it: "+err.Error())
			c.Redirect(http.StatusSeeOther, "/promotions/coupons")
			return
		}
		s.setFlash(c, "warning",
			"That coupon has been issued, so it was expired rather than deleted. The codes already in circulation stop working.")
		c.Redirect(http.StatusSeeOther, "/promotions/coupons")
		return
	}

	rec.Set("deletedAt", time.Now().UTC())
	if err := s.app.Save(rec); err != nil {
		s.setFlash(c, "danger", "Could not delete: "+err.Error())
		c.Redirect(http.StatusSeeOther, "/promotions/coupons")
		return
	}

	s.audit(c, auditEntry{
		Event:      audit.EventAccountDeleted,
		Module:     "promotions",
		TargetType: "coupon",
		TargetID:   rec.Id,
		Severity:   severityWarning,
		Summary:    "Coupon \"" + rec.GetString("name") + "\" deleted",
	})
	s.setFlash(c, "success", "Deleted.")
	c.Redirect(http.StatusSeeOther, "/promotions/coupons")
}

// handleCampaignList renders the campaigns.
func (s *Server) handleCampaignList(c *gin.Context) {
	records, err := s.app.FindRecordsByFilter(promotion.CollectionCampaigns,
		"", "-created", 200, 0)
	if err != nil {
		records = nil
	}

	now := time.Now()
	rows := make([]campaignRow, 0, len(records))
	for _, rec := range records {
		row := campaignRow{
			Record:    rec,
			Name:      rec.GetString("name"),
			Code:      rec.GetString("code"),
			Status:    rec.GetString("status"),
			BudgetCap: int64(rec.GetFloat("budgetCap")),
			Spent:     int64(rec.GetFloat("spentAmount")),
		}
		if from := rec.GetDateTime("startAt"); !from.IsZero() {
			row.Starts = from.Time().Local().Format("2 Jan 2006")
		}
		if to := rec.GetDateTime("endAt"); !to.IsZero() {
			row.Ends = to.Time().Local().Format("2 Jan 2006")
			row.Ended = to.Time().Before(now)
		}

		// The spent figure is recomputed from the grants that consumed it, because a
		// denormalised total that only ever goes up is wrong the first time anything
		// else touches the rows -- and this is the number a budget cap is enforced
		// against.
		coupons, _ := s.app.FindRecordsByFilter(promotion.CollectionCoupons,
			"campaign = {:c}", "", 0, 0, map[string]any{"c": rec.Id})
		for _, coupon := range coupons {
			grants, _ := s.app.FindRecordsByFilter(promotion.CollectionGrants,
				"coupon = {:c} && status = {:s}", "", 0, 0,
				map[string]any{"c": coupon.Id, "s": promotion.GrantUsed})
			for _, g := range grants {
				row.Spent += int64(g.GetFloat("discountApplied"))
			}
		}
		row.Coupons = len(coupons)

		if row.BudgetCap > 0 {
			row.OverBudget = row.Spent >= row.BudgetCap
		}
		rows = append(rows, row)
	}

	actor := s.actor(c)
	s.render(c, http.StatusOK, "promotion_campaigns.html", campaignsPage{
		base:      s.newBase(c, "Campaigns", "promotions.campaigns"),
		Rows:      rows,
		CanManage: actor.Can(permission.PromotionCampaignManage),
	})
}

// handleCampaignSave creates or updates a campaign.
func (s *Server) handleCampaignSave(c *gin.Context) {
	id := strings.TrimSpace(c.PostForm("id"))

	col, err := s.app.FindCollectionByNameOrId(promotion.CollectionCampaigns)
	if err != nil || col == nil {
		s.renderErrorPage(c, http.StatusInternalServerError, "Schema missing", "The collection does not exist.")
		return
	}

	var rec *core.Record
	if id == "" {
		rec = core.NewRecord(col)
	} else {
		rec, err = s.app.FindRecordById(promotion.CollectionCampaigns, id)
		if err != nil || rec == nil {
			s.renderErrorPage(c, http.StatusNotFound, "Not found", "That campaign does not exist.")
			return
		}
	}

	name := strings.TrimSpace(c.PostForm("name"))
	code := strings.TrimSpace(c.PostForm("code"))
	if name == "" || code == "" {
		s.setFlash(c, "danger", "A campaign needs a name and a code.")
		c.Redirect(http.StatusSeeOther, "/promotions/campaigns")
		return
	}

	rec.Set("name", name)
	rec.Set("code", code)
	rec.Set("description", c.PostForm("description"))
	rec.Set("status", nonEmptyString(c.PostForm("status"), "draft"))

	for field, label := range map[string]string{"startAt": "The start", "endAt": "The end"} {
		raw := strings.TrimSpace(c.PostForm(field))
		if raw == "" {
			rec.Set(field, nil)
			continue
		}
		at, err := time.ParseInLocation("2006-01-02", raw, time.Local)
		if err != nil {
			s.setFlash(c, "danger", label+" date could not be read.")
			c.Redirect(http.StatusSeeOther, "/promotions/campaigns")
			return
		}
		rec.Set(field, at.UTC())
	}

	// The budget is money and is parsed as money.
	raw := strings.TrimSpace(c.PostForm("budgetCap"))
	if raw == "" {
		rec.Set("budgetCap", 0)
	} else {
		amount, err := money.Parse(raw, strings.ToUpper(strings.TrimSpace(c.PostForm("currency"))))
		if err != nil {
			s.setFlash(c, "danger", "The budget: "+err.Error())
			c.Redirect(http.StatusSeeOther, "/promotions/campaigns")
			return
		}
		rec.Set("budgetCap", amount.MinorUnits())
	}

	if rec.IsNew() {
		if account := s.ownAccountQuiet(c); account != nil {
			rec.Set("createdBy", account.Id)
		}
	}

	if err := s.app.Save(rec); err != nil {
		s.setFlash(c, "danger", "Could not save: "+err.Error())
		c.Redirect(http.StatusSeeOther, "/promotions/campaigns")
		return
	}

	s.audit(c, auditEntry{
		Event:      audit.EventSettingsChanged,
		Module:     "promotions",
		TargetType: "campaign",
		TargetID:   rec.Id,
		Severity:   severityWarning,
		Summary:    "Campaign \"" + name + "\" saved",
	})
	s.setFlash(c, "success", "Saved.")
	c.Redirect(http.StatusSeeOther, "/promotions/campaigns")
}

// handleGrantList renders the issued coupons.
func (s *Server) handleGrantList(c *gin.Context) {
	filter := ""
	params := map[string]any{}

	if status := strings.TrimSpace(c.Query("status")); status != "" {
		filter = "status = {:status}"
		params["status"] = status
	}
	if code := strings.TrimSpace(c.Query("code")); code != "" {
		if filter != "" {
			filter += " && "
		}
		filter += "code ~ {:code}"
		params["code"] = code
	}

	records, err := s.app.FindRecordsByFilter(promotion.CollectionGrants,
		filter, "-created", 200, 0, params)
	if err != nil {
		records = nil
	}

	rows := make([]grantRow, 0, len(records))
	for _, rec := range records {
		row := grantRow{
			Record:   rec,
			Code:     rec.GetString("code"),
			Status:   rec.GetString("status"),
			Applied:  int64(rec.GetFloat("discountApplied")),
			Currency: "USD",
		}
		if coupon, err := s.app.FindRecordById(promotion.CollectionCoupons, rec.GetString("coupon")); err == nil && coupon != nil {
			row.Coupon = coupon.GetString("name")
			if c := coupon.GetString("currency"); c != "" {
				row.Currency = c
			}
		}
		if user, err := s.app.FindRecordById("users", rec.GetString("user")); err == nil && user != nil {
			row.Holder = user.Email()
		}
		if at := rec.GetDateTime("expireAt"); !at.IsZero() {
			row.Expires = at.Time().Local().Format("2 Jan 2006")
			row.Expired = at.Time().Before(time.Now())
		}
		rows = append(rows, row)
	}

	coupons, _ := s.app.FindRecordsByFilter(promotion.CollectionCoupons,
		"status = 'active' && deletedAt = ''", "name", 0, 0)
	couponOptions := make([]couponOption, 0, len(coupons))
	for _, rec := range coupons {
		couponOptions = append(couponOptions, couponOption{
			ID: rec.Id, Name: rec.GetString("name"), Code: rec.GetString("code"),
		})
	}

	actor := s.actor(c)
	s.render(c, http.StatusOK, "promotion_grants.html", grantsPage{
		base:     s.newBase(c, "Issued coupons", "promotions.grants"),
		Rows:     rows,
		Coupons:  couponOptions,
		Status:   c.Query("status"),
		Code:     c.Query("code"),
		Statuses: []string{promotion.GrantIssued, promotion.GrantUsed, promotion.GrantExpired, promotion.GrantVoided},
		CanIssue: actor.Can(permission.PromotionGrantIssue),
		CanVoid:  actor.Can(permission.PromotionGrantVoid),
	})
}

// handleGrantIssue issues a coupon code.
func (s *Server) handleGrantIssue(c *gin.Context) {
	couponID := strings.TrimSpace(c.PostForm("coupon"))
	email := strings.TrimSpace(c.PostForm("email"))

	coupon, err := s.app.FindRecordById(promotion.CollectionCoupons, couponID)
	if err != nil || coupon == nil {
		s.setFlash(c, "danger", "Choose a coupon.")
		c.Redirect(http.StatusSeeOther, "/promotions/grants")
		return
	}

	// The issuance limits are checked here rather than trusted to the screen.
	if ok, why := promotion.CanIssue(s.app, coupon); !ok {
		s.setFlash(c, "danger", "That coupon cannot be issued: "+why)
		c.Redirect(http.StatusSeeOther, "/promotions/grants")
		return
	}

	var holderID string
	if email != "" {
		user, err := s.app.FindFirstRecordByFilter("users",
			"email = {:e}", map[string]any{"e": email})
		if err != nil || user == nil {
			s.setFlash(c, "danger", "No account has that address.")
			c.Redirect(http.StatusSeeOther, "/promotions/grants")
			return
		}
		holderID = user.Id

		if ok, why := promotion.CanIssueTo(s.app, coupon, holderID); !ok {
			s.setFlash(c, "danger", "That account cannot hold another: "+why)
			c.Redirect(http.StatusSeeOther, "/promotions/grants")
			return
		}
	}

	col, err := s.app.FindCollectionByNameOrId(promotion.CollectionGrants)
	if err != nil || col == nil {
		s.renderErrorPage(c, http.StatusInternalServerError, "Schema missing", "The collection does not exist.")
		return
	}

	now := time.Now()
	rec := core.NewRecord(col)
	rec.Set("coupon", coupon.Id)
	rec.Set("code", s.nextCouponCode(coupon))
	rec.Set("status", promotion.GrantIssued)
	rec.Set("issuedAt", now.UTC())
	if holderID != "" {
		rec.Set("user", holderID)
	}
	if expiry := promotion.GrantExpiry(coupon, now); !expiry.IsZero() {
		rec.Set("expireAt", expiry.UTC())
	}
	if account := s.ownAccountQuiet(c); account != nil {
		rec.Set("issuedBy", account.Id)
	}

	if err := s.app.Save(rec); err != nil {
		s.setFlash(c, "danger", "Could not issue: "+err.Error())
		c.Redirect(http.StatusSeeOther, "/promotions/grants")
		return
	}

	s.audit(c, auditEntry{
		Event:      audit.EventSettingsChanged,
		Module:     "promotions",
		TargetType: "coupon_grant",
		TargetID:   rec.Id,
		Severity:   severityWarning,
		Summary:    "Issued coupon \"" + coupon.GetString("name") + "\" as " + rec.GetString("code"),
		Detail:     map[string]any{"holder": holderID, "expires": rec.GetString("expireAt")},
	})
	s.setFlash(c, "success", "Issued "+rec.GetString("code")+".")
	c.Redirect(http.StatusSeeOther, "/promotions/grants")
}

// nextCouponCode derives a unique code for a grant.
//
// A public code on the template is reused only when the coupon is a shared one, and
// even then a grant needs its own record; a private coupon gets a generated code.
func (s *Server) nextCouponCode(coupon *core.Record) string {
	if code := strings.TrimSpace(coupon.GetString("code")); code != "" {
		return code
	}

	base := "CPN-" + strings.ToUpper(coupon.Id[:6])
	for attempt := 0; attempt < 50; attempt++ {
		candidate := base + "-" + randomCode(6)
		existing, _ := s.app.FindFirstRecordByFilter(promotion.CollectionGrants,
			"code = {:c}", map[string]any{"c": candidate})
		if existing == nil {
			return candidate
		}
	}
	return base + "-" + randomCode(10)
}

// handleGrantVoid withdraws an issued coupon.
func (s *Server) handleGrantVoid(c *gin.Context) {
	rec, err := s.app.FindRecordById(promotion.CollectionGrants, c.Param("id"))
	if err != nil || rec == nil {
		s.renderErrorPage(c, http.StatusNotFound, "Not found", "That coupon does not exist.")
		return
	}

	if rec.GetString("status") == promotion.GrantUsed {
		s.setFlash(c, "danger", "That coupon has been used and cannot be withdrawn.")
		c.Redirect(http.StatusSeeOther, "/promotions/grants")
		return
	}

	rec.Set("status", promotion.GrantVoided)
	rec.Set("voidReason", strings.TrimSpace(c.PostForm("reason")))
	if err := s.app.Save(rec); err != nil {
		s.setFlash(c, "danger", "Could not withdraw it: "+err.Error())
		c.Redirect(http.StatusSeeOther, "/promotions/grants")
		return
	}

	s.audit(c, auditEntry{
		Event:      audit.EventSettingsChanged,
		Module:     "promotions",
		TargetType: "coupon_grant",
		TargetID:   rec.Id,
		Severity:   severityCritical,
		Summary:    "Coupon " + rec.GetString("code") + " withdrawn",
	})
	s.setFlash(c, "success", "Withdrawn.")
	c.Redirect(http.StatusSeeOther, "/promotions/grants")
}

// campaignOptions lists the campaigns for a picker.
func (s *Server) campaignOptions() []campaignOption {
	records, err := s.app.FindRecordsByFilter(promotion.CollectionCampaigns,
		"", "name", 0, 0)
	if err != nil {
		return nil
	}

	out := make([]campaignOption, 0, len(records))
	for _, rec := range records {
		out = append(out, campaignOption{ID: rec.Id, Name: rec.GetString("name")})
	}
	return out
}

// formatBasisPoints renders a stored basis-point value as a percentage.
func formatBasisPoints(points int) string {
	whole := points / 100
	fraction := points % 100
	if fraction == 0 {
		return itoa(whole) + "%"
	}
	if fraction%10 == 0 {
		return itoa(whole) + "." + itoa(fraction/10) + "%"
	}
	return itoa(whole) + "." + itoa(fraction) + "%"
}

// couponRow is one coupon on the list.
type couponRow struct {
	Record     *core.Record
	Name       string
	Code       string
	Type       string
	Status     string
	Value      string
	Currency   string
	Cap        string
	Minimum    string
	Quantity   string
	Validity   string
	Issued     int
	Used       int
	Stackable  bool
	WithMember bool
}

// couponsPage is the coupon list.
type couponsPage struct {
	base
	Rows      []couponRow
	Query     string
	Status    string
	Statuses  []string
	CanManage bool
}

// couponForm is the coupon editor.
type couponForm struct {
	base
	Record        *core.Record
	IsNew         bool
	Name          string
	Code          string
	Type          string
	Status        string
	Value         string
	Currency      string
	MaxDiscount   string
	MinSpend      string
	Validity      string
	ValidFrom     string
	ValidTo       string
	ValidDays     int
	Applicability string
	TotalQuantity int
	PerUserLimit  int
	MaxStacked    int
	Stackable     bool
	WithMember    bool
	Campaign      string
	Types         []string
	Validities    []string
	Applies       []string
	Statuses      []string
	Campaigns     []campaignOption
}

// campaignRow is one campaign.
type campaignRow struct {
	Record     *core.Record
	Name       string
	Code       string
	Status     string
	Starts     string
	Ends       string
	Ended      bool
	BudgetCap  int64
	Spent      int64
	OverBudget bool
	Coupons    int
}

// campaignsPage is the campaign list.
type campaignsPage struct {
	base
	Rows      []campaignRow
	CanManage bool
}

// grantRow is one issued coupon.
type grantRow struct {
	Record   *core.Record
	Code     string
	Coupon   string
	Holder   string
	Status   string
	Expires  string
	Expired  bool
	Applied  int64
	Currency string
}

// grantsPage is the grant list.
type grantsPage struct {
	base
	Rows     []grantRow
	Coupons  []couponOption
	Status   string
	Code     string
	Statuses []string
	CanIssue bool
	CanVoid  bool
}

// couponOption is one selectable coupon.
type couponOption struct {
	ID   string
	Name string
	Code string
}

// campaignOption is one selectable campaign.
type campaignOption struct {
	ID   string
	Name string
}

// randomCode returns a random uppercase alphanumeric string.
//
// It is generated with crypto/rand rather than math/rand: a coupon code is a bearer
// token, and a predictable one can be guessed by somebody who has seen another.
func randomCode(length int) string {
	const alphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"

	buf := make([]byte, length)
	if _, err := rand.Read(buf); err != nil {
		return strings.Repeat("X", length)
	}

	out := make([]byte, length)
	for i, b := range buf {
		out[i] = alphabet[int(b)%len(alphabet)]
	}
	return string(out)
}
