package server

import (
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/pocketbase/pocketbase/core"

	"pocketadmin/internal/audit"
	"pocketadmin/internal/money"
	"pocketadmin/internal/permission"
	"pocketadmin/internal/subscription"
)

// registerSubscriptionRoutes mounts the subscription screens.
func (s *Server) registerSubscriptionRoutes(panel *gin.RouterGroup) {
	s.readGuarded(panel, http.MethodGet, "/subscriptions", permission.SubscriptionList, s.handleSubscriptionList)
	s.readGuarded(panel, http.MethodGet, "/subscriptions/plans", permission.SubscriptionPlanList, s.handleSubscriptionPlans)
	s.readGuarded(panel, http.MethodGet, "/subscriptions/:id", permission.SubscriptionList, s.handleSubscriptionDetail)

	s.guarded(panel, http.MethodPost, "/subscriptions/plans", permission.SubscriptionPlanManage, s.handleSubscriptionPlanSave)
	s.guarded(panel, http.MethodPost, "/subscriptions/plans/:id/delete", permission.SubscriptionPlanManage, s.handleSubscriptionPlanDelete)
	s.guarded(panel, http.MethodPost, "/subscriptions/subscribe", permission.SubscriptionManage, s.handleSubscribe)
	s.guarded(panel, http.MethodPost, "/subscriptions/:id/change", permission.SubscriptionManage, s.handleSubscriptionChange)
	s.guarded(panel, http.MethodPost, "/subscriptions/:id/pause", permission.SubscriptionManage, s.handleSubscriptionPause)
	s.guarded(panel, http.MethodPost, "/subscriptions/:id/resume", permission.SubscriptionManage, s.handleSubscriptionResume)
	s.guarded(panel, http.MethodPost, "/subscriptions/:id/cancel", permission.SubscriptionCancel, s.handleSubscriptionCancel)
}

// handleSubscriptionList renders the subscriptions.
func (s *Server) handleSubscriptionList(c *gin.Context) {
	filter := ""
	params := map[string]any{}

	if status := strings.TrimSpace(c.Query("status")); status != "" {
		filter = "status = {:status}"
		params["status"] = status
	}
	if search := strings.TrimSpace(c.Query("q")); search != "" {
		if filter != "" {
			filter += " && "
		}
		filter += "user.email ~ {:q}"
		params["q"] = search
	}

	records, err := s.app.FindRecordsByFilter(subscription.CollectionSubscriptions,
		filter, "-created", 200, 0, params)
	if err != nil {
		s.renderErrorPage(c, http.StatusInternalServerError, "Could not load the subscriptions", err.Error())
		return
	}

	now := time.Now()
	rows := make([]subscriptionRow, 0, len(records))
	for _, rec := range records {
		row := subscriptionRow{
			Record:      rec,
			Status:      rec.GetString("status"),
			CancelAtEnd: rec.GetBool("cancelAtPeriodEnd"),
			Retries:     rec.GetInt("retryCount"),
			Cycles:      rec.GetInt("cycleCount"),
		}
		if user, err := s.app.FindRecordById("users", rec.GetString("user")); err == nil && user != nil {
			row.Email = user.Email()
		}
		if plan, err := s.app.FindRecordById(subscription.CollectionPlans, rec.GetString("plan")); err == nil && plan != nil {
			row.Plan = plan.GetString("name")
			row.Interval = describeInterval(plan.GetString("interval"), plan.GetInt("intervalCount"))
			row.Price = money.Format(money.Amount(subscription.PlanPrice(s.app, plan)), "USD")
		}
		if end := rec.GetDateTime("currentPeriodEnd"); !end.IsZero() {
			row.PeriodEnd = end.Time().Local().Format("2 Jan 2006")
			row.PeriodOver = end.Time().Before(now)
		}
		if at := rec.GetDateTime("pausedAt"); !at.IsZero() && rec.GetString("status") == subscription.StatusPaused {
			row.PausedSince = at.Time().Local().Format("2 Jan 2006")
		}
		rows = append(rows, row)
	}

	plans, _ := s.app.FindRecordsByFilter(subscription.CollectionPlans,
		"status = 'active'", "sortOrder", 0, 0)
	planOptions := make([]subscriptionPlanOption, 0, len(plans))
	for _, rec := range plans {
		planOptions = append(planOptions, subscriptionPlanOption{
			ID: rec.Id, Name: rec.GetString("name"),
			Interval: describeInterval(rec.GetString("interval"), rec.GetInt("intervalCount")),
		})
	}

	actor := s.actor(c)
	s.render(c, http.StatusOK, "subscriptions.html", subscriptionsPage{
		base:   s.newBase(c, "Subscriptions", "subscriptions.list"),
		Rows:   rows,
		Plans:  planOptions,
		Status: c.Query("status"),
		Query:  c.Query("q"),
		Statuses: []string{subscription.StatusTrialing, subscription.StatusActive,
			subscription.StatusPastDue, subscription.StatusPaused,
			subscription.StatusCancelled, subscription.StatusExpired},
		CanManage: actor.Can(permission.SubscriptionManage),
		CanCancel: actor.Can(permission.SubscriptionCancel),
	})
}

// handleSubscriptionPlans renders the plans.
func (s *Server) handleSubscriptionPlans(c *gin.Context) {
	records, err := s.app.FindRecordsByFilter(subscription.CollectionPlans,
		"", "sortOrder", 0, 0)
	if err != nil {
		records = nil
	}

	rows := make([]subscriptionPlanRow, 0, len(records))
	for _, rec := range records {
		row := subscriptionPlanRow{
			Record:    rec,
			Name:      rec.GetString("name"),
			Code:      rec.GetString("code"),
			Interval:  describeInterval(rec.GetString("interval"), rec.GetInt("intervalCount")),
			Trial:     rec.GetInt("trialDays"),
			MaxCycles: rec.GetInt("maxCycles"),
			Status:    rec.GetString("status"),
		}
		if product, err := s.app.FindRecordById("products", rec.GetString("product")); err == nil && product != nil {
			row.Product = product.GetString("name")
			row.Price = money.Format(money.Amount(subscription.PlanPrice(s.app, rec)), product.GetString("currency"))
		}
		row.Subscribers, _ = subscription.ActiveCount(s.app, rec.Id)
		rows = append(rows, row)
	}

	// Only products not already backing a plan, because the relation is unique and
	// offering a choice that will be refused wastes the operator's time.
	products, _ := s.app.FindRecordsByFilter("products",
		"kind = 'subscription_plan' && deletedAt = ''", "name", 0, 0)
	productOptions := make([]productOption, 0, len(products))
	for _, rec := range products {
		if existing, _ := s.app.FindFirstRecordByFilter(subscription.CollectionPlans,
			"product = {:p}", map[string]any{"p": rec.Id}); existing != nil {
			continue
		}
		productOptions = append(productOptions, productOption{ID: rec.Id, Name: rec.GetString("name")})
	}

	actor := s.actor(c)
	s.render(c, http.StatusOK, "subscription_plans.html", subscriptionPlansPage{
		base:      s.newBase(c, "Subscription plans", "subscriptions.plans"),
		Rows:      rows,
		Products:  productOptions,
		Intervals: subscription.Intervals(),
		CanManage: actor.Can(permission.SubscriptionPlanManage),
	})
}

// handleSubscriptionDetail renders one subscription with its cycles.
func (s *Server) handleSubscriptionDetail(c *gin.Context) {
	rec, err := s.app.FindRecordById(subscription.CollectionSubscriptions, c.Param("id"))
	if err != nil || rec == nil {
		s.renderErrorPage(c, http.StatusNotFound, "Not found", "That subscription does not exist.")
		return
	}

	cycles, _ := s.app.FindRecordsByFilter(subscription.CollectionCycles,
		"subscription = {:s}", "cycleNumber", 100, 0, map[string]any{"s": rec.Id})

	plan, _ := s.app.FindRecordById(subscription.CollectionPlans, rec.GetString("plan"))

	view := subscriptionDetailPage{
		base:      s.newBase(c, "Subscription", "subscriptions.list"),
		Record:    rec,
		Cycles:    cycles,
		Plan:      plan,
		CanManage: s.actor(c).Can(permission.SubscriptionManage),
		CanCancel: s.actor(c).Can(permission.SubscriptionCancel),
	}

	if user, err := s.app.FindRecordById("users", rec.GetString("user")); err == nil && user != nil {
		view.Email = user.Email()
	}
	if plan != nil {
		view.Interval = describeInterval(plan.GetString("interval"), plan.GetInt("intervalCount"))
		view.Price = money.Format(money.Amount(subscription.PlanPrice(s.app, plan)), "USD")
	}

	// A proration preview for the plan an operator is considering, so the figure is
	// visible before the change rather than after it.
	if target := c.Query("preview"); target != "" && plan != nil {
		if next, err := s.app.FindRecordById(subscription.CollectionPlans, target); err == nil && next != nil {
			period := subscription.Period{
				Start: rec.GetDateTime("currentPeriodStart").Time(),
				End:   rec.GetDateTime("currentPeriodEnd").Time(),
			}
			result := subscription.Prorate(subscription.PlanPrice(s.app, plan), subscription.PlanPrice(s.app, next), period, time.Now())

			view.Preview = &subscriptionPreview{
				PlanName:      next.GetString("name"),
				Credit:        money.Format(money.Amount(result.Credit), "USD"),
				Charge:        money.Format(money.Amount(result.Charge), "USD"),
				Due:           money.Format(money.Amount(result.Due), "USD"),
				RemainingDays: result.RemainingDays,
				TotalDays:     result.TotalDays,
				IsDowngrade:   result.Due <= 0,
			}
		}
	}

	plans, _ := s.app.FindRecordsByFilter(subscription.CollectionPlans,
		"status = 'active'", "sortOrder", 0, 0)
	planOptions := make([]subscriptionPlanOption, 0, len(plans))
	for _, p := range plans {
		planOptions = append(planOptions, subscriptionPlanOption{
			ID: p.Id, Name: p.GetString("name"),
			Interval: describeInterval(p.GetString("interval"), p.GetInt("intervalCount")),
		})
	}
	view.Plans = planOptions

	s.render(c, http.StatusOK, "subscription_detail.html", view)
}

// handleSubscriptionPlanSave creates or updates a plan.
func (s *Server) handleSubscriptionPlanSave(c *gin.Context) {
	id := strings.TrimSpace(c.PostForm("id"))

	col, err := s.app.FindCollectionByNameOrId(subscription.CollectionPlans)
	if err != nil || col == nil {
		s.renderErrorPage(c, http.StatusInternalServerError, "Schema missing", "The collection does not exist.")
		return
	}

	var rec *core.Record
	if id == "" {
		rec = core.NewRecord(col)
	} else {
		rec, err = s.app.FindRecordById(subscription.CollectionPlans, id)
		if err != nil || rec == nil {
			s.renderErrorPage(c, http.StatusNotFound, "Not found", "That plan does not exist.")
			return
		}
	}

	name := strings.TrimSpace(c.PostForm("name"))
	code := strings.TrimSpace(c.PostForm("code"))
	if name == "" || code == "" {
		s.setFlash(c, "danger", "A plan needs a name and a code.")
		c.Redirect(http.StatusSeeOther, "/subscriptions/plans")
		return
	}

	count := 1
	if raw := strings.TrimSpace(c.PostForm("intervalCount")); raw != "" {
		if parsed, err := parseIntStrict(raw); err == nil && parsed > 0 {
			count = parsed
		}
	}

	rec.Set("name", name)
	rec.Set("code", code)
	rec.Set("interval", nonEmptyString(c.PostForm("interval"), string(subscription.IntervalMonth)))
	rec.Set("intervalCount", count)
	rec.Set("status", nonEmptyString(c.PostForm("status"), "active"))

	for _, field := range []string{"trialDays", "maxCycles", "sortOrder"} {
		if raw := strings.TrimSpace(c.PostForm(field)); raw != "" {
			rec.Set(field, raw)
		}
	}

	if product := strings.TrimSpace(c.PostForm("product")); product != "" {
		rec.Set("product", product)
	} else if rec.IsNew() {
		s.setFlash(c, "danger", "A plan needs a product to be billed through.")
		c.Redirect(http.StatusSeeOther, "/subscriptions/plans")
		return
	}

	if err := s.app.Save(rec); err != nil {
		s.setFlash(c, "danger", "Could not save: "+err.Error())
		c.Redirect(http.StatusSeeOther, "/subscriptions/plans")
		return
	}

	s.audit(c, auditEntry{
		Event:      audit.EventSettingsChanged,
		Module:     "subscriptions",
		TargetType: "subscription_plan",
		TargetID:   rec.Id,
		Severity:   severityWarning,
		Summary:    "Subscription plan \"" + name + "\" saved: " + describeInterval(rec.GetString("interval"), rec.GetInt("intervalCount")),
	})
	s.setFlash(c, "success", "Saved.")
	c.Redirect(http.StatusSeeOther, "/subscriptions/plans")
}

// handleSubscriptionPlanDelete removes a plan nothing is subscribed to.
func (s *Server) handleSubscriptionPlanDelete(c *gin.Context) {
	rec, err := s.app.FindRecordById(subscription.CollectionPlans, c.Param("id"))
	if err != nil || rec == nil {
		s.renderErrorPage(c, http.StatusNotFound, "Not found", "That plan does not exist.")
		return
	}

	// A plan with subscribers is never deleted. Removing it would leave every
	// subscription pointing at nothing, and the accounts would silently stop being
	// billed -- which is a revenue loss nobody would notice until the month end.
	count, err := subscription.ActiveCount(s.app, rec.Id)
	if err != nil {
		s.setFlash(c, "danger", "Could not count its subscribers: "+err.Error())
		c.Redirect(http.StatusSeeOther, "/subscriptions/plans")
		return
	}
	if count > 0 {
		s.setFlash(c, "danger",
			itoa(count)+" subscription(s) use this plan, so it cannot be deleted. Disable it instead.")
		c.Redirect(http.StatusSeeOther, "/subscriptions/plans")
		return
	}

	if err := s.app.Delete(rec); err != nil {
		s.setFlash(c, "danger", "Could not delete: "+err.Error())
		c.Redirect(http.StatusSeeOther, "/subscriptions/plans")
		return
	}

	s.audit(c, auditEntry{
		Event:      audit.EventAccountDeleted,
		Module:     "subscriptions",
		TargetType: "subscription_plan",
		TargetID:   rec.Id,
		Severity:   severityWarning,
		Summary:    "Subscription plan \"" + rec.GetString("name") + "\" deleted",
	})
	s.setFlash(c, "success", "Deleted.")
	c.Redirect(http.StatusSeeOther, "/subscriptions/plans")
}

// handleSubscribe starts a subscription for an account.
func (s *Server) handleSubscribe(c *gin.Context) {
	email := strings.TrimSpace(c.PostForm("email"))
	planID := strings.TrimSpace(c.PostForm("plan"))

	if email == "" || planID == "" {
		s.setFlash(c, "danger", "Choose an account and a plan.")
		c.Redirect(http.StatusSeeOther, "/subscriptions")
		return
	}

	user, err := s.app.FindFirstRecordByFilter("users",
		"email = {:e}", map[string]any{"e": email})
	if err != nil || user == nil {
		s.setFlash(c, "danger", "No account has that address.")
		c.Redirect(http.StatusSeeOther, "/subscriptions")
		return
	}

	rec, err := subscription.Start(s.app, user.Id, planID, time.Now())
	if err != nil {
		s.setFlash(c, "danger", err.Error())
		c.Redirect(http.StatusSeeOther, "/subscriptions")
		return
	}

	s.audit(c, auditEntry{
		Event:      audit.EventSettingsChanged,
		Module:     "subscriptions",
		TargetType: "subscription",
		TargetID:   rec.Id,
		Severity:   severityWarning,
		Summary:    "Subscribed " + email + " with status " + rec.GetString("status"),
	})
	s.setFlash(c, "success", "Subscribed.")
	c.Redirect(http.StatusSeeOther, "/subscriptions/"+rec.Id)
}

// handleSubscriptionChange schedules a plan change.
func (s *Server) handleSubscriptionChange(c *gin.Context) {
	rec, err := s.app.FindRecordById(subscription.CollectionSubscriptions, c.Param("id"))
	if err != nil || rec == nil {
		s.renderErrorPage(c, http.StatusNotFound, "Not found", "That subscription does not exist.")
		return
	}

	planID := strings.TrimSpace(c.PostForm("plan"))
	if err := subscription.ScheduleChange(s.app, rec, planID); err != nil {
		s.setFlash(c, "danger", err.Error())
		c.Redirect(http.StatusSeeOther, "/subscriptions/"+rec.Id)
		return
	}

	s.audit(c, auditEntry{
		Event:      audit.EventSettingsChanged,
		Module:     "subscriptions",
		TargetType: "subscription",
		TargetID:   rec.Id,
		Severity:   severityWarning,
		Summary:    "A plan change was scheduled for the next cycle",
		Detail:     map[string]any{"plan": planID},
	})
	s.setFlash(c, "success", "The change takes effect at the next cycle, so nothing already paid for is lost.")
	c.Redirect(http.StatusSeeOther, "/subscriptions/"+rec.Id)
}

// handleSubscriptionPause stops billing without ending the subscription.
func (s *Server) handleSubscriptionPause(c *gin.Context) {
	rec, err := s.app.FindRecordById(subscription.CollectionSubscriptions, c.Param("id"))
	if err != nil || rec == nil {
		s.renderErrorPage(c, http.StatusNotFound, "Not found", "That subscription does not exist.")
		return
	}

	if err := subscription.Pause(s.app, rec, time.Now()); err != nil {
		s.setFlash(c, "danger", err.Error())
		c.Redirect(http.StatusSeeOther, "/subscriptions/"+rec.Id)
		return
	}

	s.audit(c, auditEntry{
		Event:      audit.EventSettingsChanged,
		Module:     "subscriptions",
		TargetType: "subscription",
		TargetID:   rec.Id,
		Severity:   severityWarning,
		Summary:    "Subscription paused",
	})
	s.setFlash(c, "success", "Paused. Resuming extends the period by the days lost.")
	c.Redirect(http.StatusSeeOther, "/subscriptions/"+rec.Id)
}

// handleSubscriptionResume unpauses, or cancels a pending cancellation.
func (s *Server) handleSubscriptionResume(c *gin.Context) {
	rec, err := s.app.FindRecordById(subscription.CollectionSubscriptions, c.Param("id"))
	if err != nil || rec == nil {
		s.renderErrorPage(c, http.StatusNotFound, "Not found", "That subscription does not exist.")
		return
	}

	if rec.GetString("status") == subscription.StatusPaused {
		if err := subscription.Unpause(s.app, rec, time.Now()); err != nil {
			s.setFlash(c, "danger", err.Error())
			c.Redirect(http.StatusSeeOther, "/subscriptions/"+rec.Id)
			return
		}
		s.setFlash(c, "success", "Resumed; the period was extended by the time paused.")
	} else {
		if err := subscription.Resume(s.app, rec, time.Now()); err != nil {
			s.setFlash(c, "danger", err.Error())
			c.Redirect(http.StatusSeeOther, "/subscriptions/"+rec.Id)
			return
		}
		s.setFlash(c, "success", "The cancellation was withdrawn.")
	}

	s.audit(c, auditEntry{
		Event:      audit.EventSettingsChanged,
		Module:     "subscriptions",
		TargetType: "subscription",
		TargetID:   rec.Id,
		Severity:   severityWarning,
		Summary:    "Subscription resumed",
	})
	c.Redirect(http.StatusSeeOther, "/subscriptions/"+rec.Id)
}

// handleSubscriptionCancel stops a subscription.
func (s *Server) handleSubscriptionCancel(c *gin.Context) {
	rec, err := s.app.FindRecordById(subscription.CollectionSubscriptions, c.Param("id"))
	if err != nil || rec == nil {
		s.renderErrorPage(c, http.StatusNotFound, "Not found", "That subscription does not exist.")
		return
	}

	var operator string
	if account := s.ownAccountQuiet(c); account != nil {
		operator = account.Id
	}

	atPeriodEnd := c.PostForm("when") != "immediately"

	if err := subscription.Cancel(s.app, rec, atPeriodEnd, operator,
		strings.TrimSpace(c.PostForm("reason")), time.Now()); err != nil {
		s.setFlash(c, "danger", err.Error())
		c.Redirect(http.StatusSeeOther, "/subscriptions/"+rec.Id)
		return
	}

	s.audit(c, auditEntry{
		Event:      audit.EventSettingsChanged,
		Module:     "subscriptions",
		TargetType: "subscription",
		TargetID:   rec.Id,
		Severity:   severityCritical,
		Summary:    "Subscription cancelled: " + rec.GetString("cancelReason"),
		Detail:     map[string]any{"atPeriodEnd": atPeriodEnd},
	})

	if atPeriodEnd {
		s.setFlash(c, "success", "It stays in force until the period ends, then stops.")
	} else {
		s.setFlash(c, "success", "Cancelled immediately.")
	}
	c.Redirect(http.StatusSeeOther, "/subscriptions/"+rec.Id)
}

// describeInterval renders a billing interval.
func describeInterval(interval string, count int) string {
	if count <= 0 {
		count = 1
	}
	if count == 1 {
		return "every " + interval
	}
	return "every " + itoa(count) + " " + interval + "s"
}

// subscriptionRow is one subscription.
type subscriptionRow struct {
	Record      *core.Record
	Email       string
	Plan        string
	Interval    string
	Price       string
	Status      string
	PeriodEnd   string
	PeriodOver  bool
	PausedSince string
	CancelAtEnd bool
	Retries     int
	Cycles      int
}

// subscriptionsPage is the subscription list.
type subscriptionsPage struct {
	base
	Rows      []subscriptionRow
	Plans     []subscriptionPlanOption
	Status    string
	Query     string
	Statuses  []string
	CanManage bool
	CanCancel bool
}

// subscriptionPlanRow is one plan.
type subscriptionPlanRow struct {
	Record      *core.Record
	Name        string
	Code        string
	Product     string
	Price       string
	Interval    string
	Trial       int
	MaxCycles   int
	Status      string
	Subscribers int
}

// subscriptionPlansPage is the plan list.
type subscriptionPlansPage struct {
	base
	Rows      []subscriptionPlanRow
	Products  []productOption
	Intervals []string
	CanManage bool
}

// subscriptionDetailPage is one subscription.
type subscriptionDetailPage struct {
	base
	Record    *core.Record
	Plan      *core.Record
	Email     string
	Interval  string
	Price     string
	Cycles    []*core.Record
	Plans     []subscriptionPlanOption
	Preview   *subscriptionPreview
	CanManage bool
	CanCancel bool
}

// subscriptionPreview is a proration figure shown before a change.
type subscriptionPreview struct {
	PlanName      string
	Credit        string
	Charge        string
	Due           string
	RemainingDays int
	TotalDays     int
	IsDowngrade   bool
}

// subscriptionPlanOption is one selectable plan.
type subscriptionPlanOption struct {
	ID       string
	Name     string
	Interval string
}
