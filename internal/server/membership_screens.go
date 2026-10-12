package server

import (
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/pocketbase/pocketbase/core"

	"pocketadmin/internal/audit"
	"pocketadmin/internal/membership"
	"pocketadmin/internal/money"
	"pocketadmin/internal/permission"
)

// registerMembershipRoutes mounts the membership screens.
func (s *Server) registerMembershipRoutes(panel *gin.RouterGroup) {
	s.readGuarded(panel, http.MethodGet, "/membership/members", permission.MembershipMemberList, s.handleMemberList)
	s.readGuarded(panel, http.MethodGet, "/membership/tiers", permission.MembershipTierList, s.handleTierList)
	s.readGuarded(panel, http.MethodGet, "/membership/tiers/:id", permission.MembershipTierList, s.handleTierDetail)
	s.readGuarded(panel, http.MethodGet, "/membership/plans", permission.MembershipPlanList, s.handlePlanList)

	s.guarded(panel, http.MethodPost, "/membership/tiers", permission.MembershipTierManage, s.handleTierSave)
	s.guarded(panel, http.MethodPost, "/membership/tiers/:id/default", permission.MembershipTierManage, s.handleTierDefault)
	s.guarded(panel, http.MethodPost, "/membership/tiers/:id/delete", permission.MembershipTierManage, s.handleTierDelete)
	s.guarded(panel, http.MethodPost, "/membership/tiers/:id/benefits", permission.MembershipTierManage, s.handleBenefitSave)
	s.guarded(panel, http.MethodPost, "/membership/plans", permission.MembershipPlanManage, s.handlePlanSave)
	s.guarded(panel, http.MethodPost, "/membership/members", permission.MembershipMemberGrant, s.handleMemberGrant)
	s.guarded(panel, http.MethodPost, "/membership/members/:id/extend", permission.MembershipMemberGrant, s.handleMemberExtend)
	s.guarded(panel, http.MethodPost, "/membership/members/:id/tier", permission.MembershipMemberGrant, s.handleMemberChangeTier)
	s.guarded(panel, http.MethodPost, "/membership/members/:id/end", permission.MembershipMemberRevoke, s.handleMemberEnd)
}

// handleMemberList renders the memberships.
func (s *Server) handleMemberList(c *gin.Context) {
	now := time.Now()

	filter := ""
	params := map[string]any{}

	if status := strings.TrimSpace(c.Query("status")); status != "" {
		filter = "status = {:status}"
		params["status"] = status
	}
	if tierID := strings.TrimSpace(c.Query("tier")); tierID != "" {
		if filter != "" {
			filter += " && "
		}
		filter += "tier = {:tier}"
		params["tier"] = tierID
	}
	if source := strings.TrimSpace(c.Query("source")); source != "" {
		if filter != "" {
			filter += " && "
		}
		filter += "source = {:source}"
		params["source"] = source
	}
	if window := strings.TrimSpace(c.Query("expiresWithin")); window != "" {
		if days, err := parseIntStrict(window); err == nil && days > 0 {
			if filter != "" {
				filter += " && "
			}
			filter += "expireAt != '' && expireAt <= {:until}"
			params["until"] = now.AddDate(0, 0, days).UTC()
		}
	}

	records, err := s.app.FindRecordsByFilter(membership.CollectionMembers,
		filter, "-created", 200, 0, params)
	if err != nil {
		s.renderErrorPage(c, http.StatusInternalServerError, "Could not load the memberships", err.Error())
		return
	}

	rows := make([]memberRow, 0, len(records))
	for _, rec := range records {
		row := memberRow{
			Record: rec,
			Status: rec.GetString("status"),
			Source: rec.GetString("source"),
			Reason: rec.GetString("reason"),
		}
		if user, err := s.app.FindRecordById("users", rec.GetString("user")); err == nil && user != nil {
			row.Email = user.Email()
		}
		if tierRec, err := s.app.FindRecordById(membership.CollectionTiers, rec.GetString("tier")); err == nil && tierRec != nil {
			row.Tier = tierRec.GetString("name")
		}
		if at := rec.GetDateTime("startAt"); !at.IsZero() {
			row.Start = at.Time().Local().Format("2 Jan 2006")
		}
		if at := rec.GetDateTime("expireAt"); !at.IsZero() {
			row.Expires = at.Time().Local().Format("2 Jan 2006")
			// A lapsed membership the scheduled task has not moved yet is still
			// shown as lapsed, because that is what it is.
			row.Expired = at.Time().Before(now)
		} else {
			row.Expires = "never"

			// A perpetual membership is never expiring.
			row.Perpetual = true
		}
		rows = append(rows, row)
	}

	tiers, _ := s.app.FindRecordsByFilter(membership.CollectionTiers, "", "level", 0, 0)
	tierOptions := make([]tierOption, 0, len(tiers))
	for _, rec := range tiers {
		tierOptions = append(tierOptions, tierOption{
			ID: rec.Id, Name: rec.GetString("name"), Level: rec.GetInt("level"),
		})
	}

	actor := s.actor(c)
	s.render(c, http.StatusOK, "membership_members.html", membersPage{
		base:          s.newBase(c, "Members", "membership.members"),
		Rows:          rows,
		Tiers:         tierOptions,
		Status:        c.Query("status"),
		Tier:          c.Query("tier"),
		Source:        c.Query("source"),
		ExpiresWithin: c.Query("expiresWithin"),
		Statuses: []string{membership.StatusPending, membership.StatusActive,
			membership.StatusExpired, membership.StatusCancelled, membership.StatusRefunded},
		Sources: []string{membership.SourcePurchase, membership.SourceGift,
			membership.SourceTrial, membership.SourceAdmin, membership.SourcePromotion},
		CanGrant:  actor.Can(permission.MembershipMemberGrant),
		CanRevoke: actor.Can(permission.MembershipMemberRevoke),
	})
}

// handleTierList renders the tier catalogue.
func (s *Server) handleTierList(c *gin.Context) {
	records, err := s.app.FindRecordsByFilter(membership.CollectionTiers, "", "level", 0, 0)
	if err != nil {
		records = nil
	}

	now := time.Now()
	rows := make([]tierRow, 0, len(records))
	for _, rec := range records {
		count, _ := membership.ActiveMemberCount(s.app, rec.Id, now)
		benefits := membership.ResolveBenefits(s.app, rec.Id)

		rows = append(rows, tierRow{
			Record:      rec,
			Name:        rec.GetString("name"),
			Code:        rec.GetString("code"),
			Level:       rec.GetInt("level"),
			Status:      rec.GetString("status"),
			IsDefault:   rec.GetBool("isDefault"),
			Members:     count,
			Labels:      benefits.Labels,
			Discount:    benefits.DiscountRate,
			HasDiscount: benefits.HasDiscount,
		})
	}

	actor := s.actor(c)
	s.render(c, http.StatusOK, "membership_tiers.html", tiersPage{
		base:      s.newBase(c, "Membership tiers", "membership.tiers"),
		Rows:      rows,
		CanManage: actor.Can(permission.MembershipTierManage),
	})
}

// handleTierDetail shows a tier's benefits.
func (s *Server) handleTierDetail(c *gin.Context) {
	rec, err := s.app.FindRecordById(membership.CollectionTiers, c.Param("id"))
	if err != nil || rec == nil {
		s.renderErrorPage(c, http.StatusNotFound, "Not found", "That tier does not exist.")
		return
	}

	benefits, _ := s.app.FindRecordsByFilter(membership.CollectionBenefits,
		"tier = {:t}", "sortOrder", 0, 0, map[string]any{"t": rec.Id})

	resolved := membership.ResolveBenefits(s.app, rec.Id)

	s.render(c, http.StatusOK, "membership_tier_detail.html", tierDetailPage{
		base:      s.newBase(c, rec.GetString("name"), "membership.tiers"),
		Record:    rec,
		Benefits:  benefits,
		Resolved:  resolved,
		Kinds:     membership.BenefitKinds(),
		CanManage: s.actor(c).Can(permission.MembershipTierManage),
	})
}

// handleTierSave creates or updates a tier.
func (s *Server) handleTierSave(c *gin.Context) {
	id := strings.TrimSpace(c.PostForm("id"))

	col, err := s.app.FindCollectionByNameOrId(membership.CollectionTiers)
	if err != nil || col == nil {
		s.renderErrorPage(c, http.StatusInternalServerError, "Schema missing", "The collection does not exist.")
		return
	}

	var rec *core.Record
	if id == "" {
		rec = core.NewRecord(col)
	} else {
		rec, err = s.app.FindRecordById(membership.CollectionTiers, id)
		if err != nil || rec == nil {
			s.renderErrorPage(c, http.StatusNotFound, "Not found", "That tier does not exist.")
			return
		}
	}

	name := strings.TrimSpace(c.PostForm("name"))
	code := strings.TrimSpace(c.PostForm("code"))
	if name == "" || code == "" {
		s.setFlash(c, "danger", "A tier needs a name and a code.")
		c.Redirect(http.StatusSeeOther, "/membership/tiers")
		return
	}

	rec.Set("name", name)
	rec.Set("code", code)
	rec.Set("icon", strings.TrimSpace(c.PostForm("icon")))
	rec.Set("description", c.PostForm("description"))
	rec.Set("status", nonEmptyString(c.PostForm("status"), "active"))

	if raw := strings.TrimSpace(c.PostForm("level")); raw != "" {
		rec.Set("level", raw)
	}
	if raw := strings.TrimSpace(c.PostForm("sortOrder")); raw != "" {
		rec.Set("sortOrder", raw)
	}

	if err := s.app.Save(rec); err != nil {
		s.setFlash(c, "danger", "Could not save: "+err.Error())
		c.Redirect(http.StatusSeeOther, "/membership/tiers")
		return
	}

	// A first tier with nothing marked becomes the default, because a registration
	// with no tier to grant is a hole a careful operator would otherwise have to
	// notice.
	if def, _ := membership.DefaultTier(s.app); def == nil {
		if err := membership.MakeDefault(s.app, rec); err != nil {
			s.setFlash(c, "warning", "Saved, but it could not be made the default: "+err.Error())
		}
	}

	s.audit(c, auditEntry{
		Event:      audit.EventSettingsChanged,
		Module:     "membership",
		TargetType: "member_tier",
		TargetID:   rec.Id,
		Severity:   severityWarning,
		Summary:    "Membership tier \"" + name + "\" saved at level " + itoa(rec.GetInt("level")),
	})
	s.setFlash(c, "success", "Saved.")
	c.Redirect(http.StatusSeeOther, "/membership/tiers/"+rec.Id)
}

// handleTierDefault makes a tier the registration default.
func (s *Server) handleTierDefault(c *gin.Context) {
	rec, err := s.app.FindRecordById(membership.CollectionTiers, c.Param("id"))
	if err != nil || rec == nil {
		s.renderErrorPage(c, http.StatusNotFound, "Not found", "That tier does not exist.")
		return
	}

	if err := membership.MakeDefault(s.app, rec); err != nil {
		s.setFlash(c, "danger", "Could not set the default: "+err.Error())
		c.Redirect(http.StatusSeeOther, "/membership/tiers")
		return
	}

	s.audit(c, auditEntry{
		Event:      audit.EventSettingsChanged,
		Module:     "membership",
		TargetType: "member_tier",
		TargetID:   rec.Id,
		Severity:   severityWarning,
		Summary:    "\"" + rec.GetString("name") + "\" is now the tier granted on registration",
	})
	s.setFlash(c, "success", "New accounts will be granted "+rec.GetString("name")+".")
	c.Redirect(http.StatusSeeOther, "/membership/tiers")
}

// handleTierDelete removes a tier that nothing depends on.
func (s *Server) handleTierDelete(c *gin.Context) {
	rec, err := s.app.FindRecordById(membership.CollectionTiers, c.Param("id"))
	if err != nil || rec == nil {
		s.renderErrorPage(c, http.StatusNotFound, "Not found", "That tier does not exist.")
		return
	}

	now := time.Now()

	// A tier with members is never deleted. Removing it would leave every membership
	// pointing at nothing, and the people holding them would silently lose what they
	// paid for.
	count, err := membership.ActiveMemberCount(s.app, rec.Id, now)
	if err != nil {
		s.setFlash(c, "danger", "Could not count its members: "+err.Error())
		c.Redirect(http.StatusSeeOther, "/membership/tiers")
		return
	}
	if count > 0 {
		s.setFlash(c, "danger",
			itoa(count)+" account(s) hold this tier, so it cannot be deleted. Disable it instead.")
		c.Redirect(http.StatusSeeOther, "/membership/tiers")
		return
	}

	// Nor is a tier a plan grants.
	referenced, err := membership.ReferencedByPlan(s.app, rec.Id)
	if err != nil {
		s.setFlash(c, "danger", "Could not check its plans: "+err.Error())
		c.Redirect(http.StatusSeeOther, "/membership/tiers")
		return
	}
	if referenced {
		s.setFlash(c, "danger", "A membership plan grants this tier, so it cannot be deleted.")
		c.Redirect(http.StatusSeeOther, "/membership/tiers")
		return
	}

	if rec.GetBool("isDefault") {
		s.setFlash(c, "danger",
			"This is the tier granted on registration, so it cannot be deleted. Make another the default first.")
		c.Redirect(http.StatusSeeOther, "/membership/tiers")
		return
	}

	// The benefits go with it: they describe the tier and mean nothing without it.
	benefits, _ := s.app.FindRecordsByFilter(membership.CollectionBenefits,
		"tier = {:t}", "", 0, 0, map[string]any{"t": rec.Id})
	for _, benefit := range benefits {
		if err := s.app.Delete(benefit); err != nil {
			s.setFlash(c, "danger", "Could not remove its benefits: "+err.Error())
			c.Redirect(http.StatusSeeOther, "/membership/tiers")
			return
		}
	}

	if err := s.app.Delete(rec); err != nil {
		s.setFlash(c, "danger", "Could not delete: "+err.Error())
		c.Redirect(http.StatusSeeOther, "/membership/tiers")
		return
	}

	s.audit(c, auditEntry{
		Event:      audit.EventAccountDeleted,
		Module:     "membership",
		TargetType: "member_tier",
		TargetID:   rec.Id,
		Severity:   severityWarning,
		Summary:    "Membership tier \"" + rec.GetString("name") + "\" deleted",
	})
	s.setFlash(c, "success", "Deleted.")
	c.Redirect(http.StatusSeeOther, "/membership/tiers")
}

// handleBenefitSave adds or replaces a benefit.
func (s *Server) handleBenefitSave(c *gin.Context) {
	tierRec, err := s.app.FindRecordById(membership.CollectionTiers, c.Param("id"))
	if err != nil || tierRec == nil {
		s.renderErrorPage(c, http.StatusNotFound, "Not found", "That tier does not exist.")
		return
	}

	kind := strings.TrimSpace(c.PostForm("kind"))
	label := strings.TrimSpace(c.PostForm("label"))

	col, err := s.app.FindCollectionByNameOrId(membership.CollectionBenefits)
	if err != nil || col == nil {
		s.renderErrorPage(c, http.StatusInternalServerError, "Schema missing", "The collection does not exist.")
		return
	}

	// The payload's shape follows the kind. It is built here rather than taken from
	// the form as free JSON, so an editor cannot store a discount rate that is
	// actually a sentence.
	value := map[string]any{}
	switch kind {
	case membership.BenefitDiscountRate:
		percent, err := parsePercent(c.PostForm("percent"))
		if err != nil {
			s.setFlash(c, "danger", "The discount: "+err.Error())
			c.Redirect(http.StatusSeeOther, "/membership/tiers/"+tierRec.Id)
			return
		}
		if percent < 0 || percent > 100 {
			s.setFlash(c, "danger", "A discount is between 0 and 100 percent.")
			c.Redirect(http.StatusSeeOther, "/membership/tiers/"+tierRec.Id)
			return
		}
		// Stored as a fraction, which is what pricing multiplies by. The percentage
		// exists only in this form.
		value["rate"] = round4(1 - percent/100)
		if label == "" {
			label = trimPercent(percent) + "% off"
		}

	case membership.BenefitPointsMultiplier:
		multiplier, err := parsePercent(c.PostForm("multiplier"))
		if err != nil || multiplier <= 0 {
			s.setFlash(c, "danger", "A points multiplier must be a positive number.")
			c.Redirect(http.StatusSeeOther, "/membership/tiers/"+tierRec.Id)
			return
		}
		value["multiplier"] = multiplier
		if label == "" {
			label = trimPercent(multiplier) + "x points"
		}

	case membership.BenefitContentAccess:
		value["access"] = splitList(c.PostForm("access"))
		if label == "" {
			label = "Content access"
		}

	default:
		// A flag benefit carries no payload: the row's presence is the statement.
		if label == "" {
			label = kind
		}
	}

	rec := core.NewRecord(col)
	rec.Set("tier", tierRec.Id)
	rec.Set("kind", kind)
	rec.Set("value", value)
	rec.Set("label", label)
	if raw := strings.TrimSpace(c.PostForm("sortOrder")); raw != "" {
		rec.Set("sortOrder", raw)
	}

	if err := s.app.Save(rec); err != nil {
		s.setFlash(c, "danger", "Could not save the benefit: "+err.Error())
		c.Redirect(http.StatusSeeOther, "/membership/tiers/"+tierRec.Id)
		return
	}

	s.audit(c, auditEntry{
		Event:      audit.EventSettingsChanged,
		Module:     "membership",
		TargetType: "member_tier",
		TargetID:   tierRec.Id,
		Severity:   severityWarning,
		Summary:    "Benefit \"" + label + "\" (" + kind + ") added to " + tierRec.GetString("name"),
		Detail:     value,
	})
	s.setFlash(c, "success", "Benefit added.")
	c.Redirect(http.StatusSeeOther, "/membership/tiers/"+tierRec.Id)
}

// handlePlanList renders the purchasable plans.
func (s *Server) handlePlanList(c *gin.Context) {
	records, err := s.app.FindRecordsByFilter(membership.CollectionPlans,
		"", "sortOrder", 0, 0)
	if err != nil {
		records = nil
	}

	rows := make([]planRow, 0, len(records))
	for _, rec := range records {
		row := planRow{
			Record:   rec,
			Name:     rec.GetString("name"),
			Code:     rec.GetString("code"),
			Status:   rec.GetString("status"),
			Duration: rec.GetInt("durationDays"),
		}
		if tierRec, err := s.app.FindRecordById(membership.CollectionTiers, rec.GetString("tier")); err == nil && tierRec != nil {
			row.Tier = tierRec.GetString("name")
		}
		if product, err := s.app.FindRecordById("products", rec.GetString("product")); err == nil && product != nil {
			row.Product = product.GetString("name")
			// The price lives on the product, which is what lets a membership be
			// bought through the ordinary order pipeline.
			row.Price = money.Format(money.Amount(product.GetFloat("salePrice")), product.GetString("currency"))
			if product.GetFloat("salePrice") == 0 {
				row.Price = money.Format(money.Amount(product.GetFloat("listPrice")), product.GetString("currency"))
			}
		}
		rows = append(rows, row)
	}

	tiers, _ := s.app.FindRecordsByFilter(membership.CollectionTiers, "status = 'active'", "level", 0, 0)
	tierOptions := make([]tierOption, 0, len(tiers))
	for _, rec := range tiers {
		tierOptions = append(tierOptions, tierOption{ID: rec.Id, Name: rec.GetString("name"), Level: rec.GetInt("level")})
	}

	// Only products that are not already a plan, so a plan cannot point at a product
	// another plan already uses -- the collection's unique index would refuse it, and
	// offering the choice would waste the operator's time.
	products, _ := s.app.FindRecordsByFilter("products",
		"kind = 'membership_plan' && deletedAt = ''", "name", 0, 0)
	productOptions := make([]productOption, 0, len(products))
	for _, rec := range products {
		if existing, _ := s.app.FindFirstRecordByFilter(membership.CollectionPlans,
			"product = {:p}", map[string]any{"p": rec.Id}); existing != nil {
			continue
		}
		productOptions = append(productOptions, productOption{ID: rec.Id, Name: rec.GetString("name")})
	}

	actor := s.actor(c)
	s.render(c, http.StatusOK, "membership_plans.html", plansPage{
		base:      s.newBase(c, "Membership plans", "membership.plans"),
		Rows:      rows,
		Tiers:     tierOptions,
		Products:  productOptions,
		CanManage: actor.Can(permission.MembershipPlanManage),
	})
}

// handlePlanSave creates or updates a plan.
func (s *Server) handlePlanSave(c *gin.Context) {
	id := strings.TrimSpace(c.PostForm("id"))

	col, err := s.app.FindCollectionByNameOrId(membership.CollectionPlans)
	if err != nil || col == nil {
		s.renderErrorPage(c, http.StatusInternalServerError, "Schema missing", "The collection does not exist.")
		return
	}

	var rec *core.Record
	if id == "" {
		rec = core.NewRecord(col)
	} else {
		rec, err = s.app.FindRecordById(membership.CollectionPlans, id)
		if err != nil || rec == nil {
			s.renderErrorPage(c, http.StatusNotFound, "Not found", "That plan does not exist.")
			return
		}
	}

	name := strings.TrimSpace(c.PostForm("name"))
	code := strings.TrimSpace(c.PostForm("code"))
	product := strings.TrimSpace(c.PostForm("product"))
	tierID := strings.TrimSpace(c.PostForm("tier"))

	if name == "" || code == "" || tierID == "" {
		s.setFlash(c, "danger", "A plan needs a name, a code and a tier.")
		c.Redirect(http.StatusSeeOther, "/membership/plans")
		return
	}
	if rec.IsNew() && product == "" {
		s.setFlash(c, "danger", "A plan needs a product to be bought through.")
		c.Redirect(http.StatusSeeOther, "/membership/plans")
		return
	}

	rec.Set("name", name)
	rec.Set("code", code)
	rec.Set("tier", tierID)
	rec.Set("status", nonEmptyString(c.PostForm("status"), "active"))
	if product != "" {
		rec.Set("product", product)
	}
	if raw := strings.TrimSpace(c.PostForm("durationDays")); raw != "" {
		rec.Set("durationDays", raw)
	}
	if raw := strings.TrimSpace(c.PostForm("sortOrder")); raw != "" {
		rec.Set("sortOrder", raw)
	}

	if err := s.app.Save(rec); err != nil {
		s.setFlash(c, "danger", "Could not save: "+err.Error())
		c.Redirect(http.StatusSeeOther, "/membership/plans")
		return
	}

	s.audit(c, auditEntry{
		Event:      audit.EventSettingsChanged,
		Module:     "membership",
		TargetType: "member_plan",
		TargetID:   rec.Id,
		Severity:   severityWarning,
		Summary:    "Membership plan \"" + name + "\" saved",
		Detail:     map[string]any{"tier": tierID, "durationDays": rec.GetInt("durationDays")},
	})
	s.setFlash(c, "success", "Saved.")
	c.Redirect(http.StatusSeeOther, "/membership/plans")
}

// handleMemberGrant grants a membership by hand.
func (s *Server) handleMemberGrant(c *gin.Context) {
	email := strings.TrimSpace(c.PostForm("email"))
	tierID := strings.TrimSpace(c.PostForm("tier"))
	reason := strings.TrimSpace(c.PostForm("reason"))

	if email == "" || tierID == "" || reason == "" {
		s.setFlash(c, "danger", "A manual grant needs an account, a tier and a reason.")
		c.Redirect(http.StatusSeeOther, "/membership/members")
		return
	}

	user, err := s.app.FindFirstRecordByFilter("users",
		"email = {:e}", map[string]any{"e": email})
	if err != nil || user == nil {
		s.setFlash(c, "danger", "No account has that address.")
		c.Redirect(http.StatusSeeOther, "/membership/members")
		return
	}

	days := 0
	if raw := strings.TrimSpace(c.PostForm("durationDays")); raw != "" {
		if parsed, err := parseIntStrict(raw); err == nil {
			days = parsed
		}
	}

	var operator string
	if account := s.ownAccountQuiet(c); account != nil {
		operator = account.Id
	}

	rec, err := membership.GrantMembership(s.app, membership.Grant{
		UserID:       user.Id,
		TierID:       tierID,
		Source:       membership.SourceAdmin,
		Operator:     operator,
		Reason:       reason,
		DurationDays: days,
	})
	if err != nil {
		s.setFlash(c, "danger", err.Error())
		c.Redirect(http.StatusSeeOther, "/membership/members")
		return
	}

	s.audit(c, auditEntry{
		Event:      audit.EventSettingsChanged,
		Module:     "membership",
		TargetType: "user_membership",
		TargetID:   rec.Id,
		Severity:   severityWarning,
		Summary:    "Granted a membership to " + email + ": " + reason,
		Detail:     map[string]any{"tier": tierID, "durationDays": days},
	})
	s.setFlash(c, "success", "Granted.")
	c.Redirect(http.StatusSeeOther, "/membership/members")
}

// handleMemberExtend adds days to a membership.
func (s *Server) handleMemberExtend(c *gin.Context) {
	rec, ok := s.findRecord(c, mustCollection(s.app, membership.CollectionMembers))
	if !ok {
		return
	}

	days, err := parseIntStrict(c.PostForm("days"))
	if err != nil {
		s.setFlash(c, "danger", "The number of days must be a whole number.")
		c.Redirect(http.StatusSeeOther, "/membership/members")
		return
	}

	var operator string
	if account := s.ownAccountQuiet(c); account != nil {
		operator = account.Id
	}

	if err := membership.Extend(s.app, rec, days, operator, strings.TrimSpace(c.PostForm("reason"))); err != nil {
		s.setFlash(c, "danger", err.Error())
		c.Redirect(http.StatusSeeOther, "/membership/members")
		return
	}

	s.audit(c, auditEntry{
		Event:      audit.EventSettingsChanged,
		Module:     "membership",
		TargetType: "user_membership",
		TargetID:   rec.Id,
		Severity:   severityWarning,
		Summary:    "Extended a membership by " + itoa(days) + " day(s)",
	})
	s.setFlash(c, "success", "Extended by "+itoa(days)+" day(s).")
	c.Redirect(http.StatusSeeOther, "/membership/members")
}

// handleMemberChangeTier moves a membership to another tier.
func (s *Server) handleMemberChangeTier(c *gin.Context) {
	rec, ok := s.findRecord(c, mustCollection(s.app, membership.CollectionMembers))
	if !ok {
		return
	}

	var operator string
	if account := s.ownAccountQuiet(c); account != nil {
		operator = account.Id
	}

	tierID := strings.TrimSpace(c.PostForm("tier"))
	if err := membership.ChangeTier(s.app, rec, tierID, operator, strings.TrimSpace(c.PostForm("reason"))); err != nil {
		s.setFlash(c, "danger", err.Error())
		c.Redirect(http.StatusSeeOther, "/membership/members")
		return
	}

	s.audit(c, auditEntry{
		Event:      audit.EventSettingsChanged,
		Module:     "membership",
		TargetType: "user_membership",
		TargetID:   rec.Id,
		Severity:   severityWarning,
		Summary:    "Membership moved to another tier",
		Detail:     map[string]any{"tier": tierID},
	})
	s.setFlash(c, "success", "Tier changed.")
	c.Redirect(http.StatusSeeOther, "/membership/members")
}

// handleMemberEnd cancels or refunds a membership.
func (s *Server) handleMemberEnd(c *gin.Context) {
	rec, ok := s.findRecord(c, mustCollection(s.app, membership.CollectionMembers))
	if !ok {
		return
	}

	var operator string
	if account := s.ownAccountQuiet(c); account != nil {
		operator = account.Id
	}

	status := c.PostForm("status")
	if err := membership.SetStatus(s.app, rec, status, operator, strings.TrimSpace(c.PostForm("reason"))); err != nil {
		s.setFlash(c, "danger", err.Error())
		c.Redirect(http.StatusSeeOther, "/membership/members")
		return
	}

	s.audit(c, auditEntry{
		Event:      audit.EventSettingsChanged,
		Module:     "membership",
		TargetType: "user_membership",
		TargetID:   rec.Id,
		Severity:   severityCritical,
		Summary:    "Membership " + status + ": " + rec.GetString("reason"),
	})
	s.setFlash(c, "success", "Membership "+status+".")
	c.Redirect(http.StatusSeeOther, "/membership/members")
}

// ---- helpers and view models ----

// splitList reads a comma or newline separated list.
func splitList(raw string) []string {
	fields := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == '\n' || r == ';'
	})

	out := make([]string, 0, len(fields))
	for _, field := range fields {
		if trimmed := strings.TrimSpace(field); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

// round4 rounds to four decimal places, which is as much precision as a rate needs.
func round4(value float64) float64 {
	return float64(int(value*10000+0.5)) / 10000
}

// trimPercent renders a percentage without a trailing zero.
func trimPercent(value float64) string {
	if value == float64(int(value)) {
		return itoa(int(value))
	}
	return strings.TrimRight(strings.TrimRight(
		money.FormatPlain(money.Amount(value*100), "USD"), "0"), ".")
}

// memberRow is one membership.
type memberRow struct {
	Record    *core.Record
	Email     string
	Tier      string
	Status    string
	Source    string
	Reason    string
	Start     string
	Expires   string
	Expired   bool
	Perpetual bool
}

// membersPage is the membership list.
type membersPage struct {
	base
	Rows          []memberRow
	Tiers         []tierOption
	Status        string
	Tier          string
	Source        string
	ExpiresWithin string
	Statuses      []string
	Sources       []string
	CanGrant      bool
	CanRevoke     bool
}

// tierRow is one tier on the catalogue.
type tierRow struct {
	Record      *core.Record
	Name        string
	Code        string
	Level       int
	Status      string
	IsDefault   bool
	Members     int
	Labels      []string
	Discount    float64
	HasDiscount bool
}

// tiersPage is the tier catalogue.
type tiersPage struct {
	base
	Rows      []tierRow
	CanManage bool
}

// tierDetailPage is one tier with its benefits.
type tierDetailPage struct {
	base
	Record    *core.Record
	Benefits  []*core.Record
	Resolved  membership.Benefits
	Kinds     []string
	CanManage bool
}

// planRow is one plan.
type planRow struct {
	Record   *core.Record
	Name     string
	Code     string
	Tier     string
	Product  string
	Price    string
	Status   string
	Duration int
}

// plansPage is the plan list.
type plansPage struct {
	base
	Rows      []planRow
	Tiers     []tierOption
	Products  []productOption
	CanManage bool
}

// tierOption is one selectable tier.
type tierOption struct {
	ID    string
	Name  string
	Level int
}

// productOption is one selectable product.
type productOption struct {
	ID   string
	Name string
}

var _ = time.Now
