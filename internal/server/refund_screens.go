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
	"pocketadmin/internal/refund"
	"pocketadmin/internal/settings"
)

// registerRefundRoutes mounts the refund screens.
func (s *Server) registerRefundRoutes(panel *gin.RouterGroup) {
	s.readGuarded(panel, http.MethodGet, "/refunds", permission.RefundList, s.handleRefundList)
	s.readGuarded(panel, http.MethodGet, "/refunds/:id", permission.RefundList, s.handleRefundDetail)
	s.readGuarded(panel, http.MethodGet, "/refunds/new", permission.RefundRequest, s.handleRefundForm)

	s.guarded(panel, http.MethodPost, "/refunds/request", permission.RefundRequest, s.handleRefundRequest)
	s.guarded(panel, http.MethodPost, "/refunds/:id/approve", permission.RefundApprove, s.handleRefundApprove)
	s.guarded(panel, http.MethodPost, "/refunds/:id/reject", permission.RefundReject, s.handleRefundReject)
	s.guarded(panel, http.MethodPost, "/refunds/:id/execute", permission.RefundExecute, s.handleRefundExecute)
	s.guarded(panel, http.MethodPost, "/refunds/:id/cancel", permission.RefundExecute, s.handleRefundCancel)
}

// refundPolicy reads the refund policy from settings.
//
// It is read through the settings engine rather than from a constant, because the
// threshold is a number a shop has to be able to change without a release.
func (s *Server) refundPolicy() refund.Policy {
	policy := refund.DefaultPolicy()

	if s.settings == nil {
		return policy
	}

	// The registry is passed rather than a key string, so a setting that is not
	// declared cannot be read: a typo fails to compile instead of silently returning
	// the default.
	reg := settings.Admin()
	policy.AutoApproveThreshold = int64(s.settings.IntGet(reg, "refund", "autoApproveThreshold"))
	policy.RestockOnFullRefund = s.settings.BoolGet(reg, "refund", "restockOnFullRefund")
	return policy
}

// handleRefundList renders the refund requests.
func (s *Server) handleRefundList(c *gin.Context) {
	clauses := []string{}
	params := map[string]any{}

	if status := strings.TrimSpace(c.Query("status")); status != "" {
		clauses = append(clauses, "status = {:status}")
		params["status"] = status
	}
	if search := strings.TrimSpace(c.Query("q")); search != "" {
		clauses = append(clauses, "(refundNo ~ {:q} || order.orderNo ~ {:q})")
		params["q"] = search
	}
	if c.Query("mine") != "" {
		if account := s.ownAccountQuiet(c); account != nil {
			clauses = append(clauses, "requestedBy = {:me}")
			params["me"] = account.Id
		}
	}

	records, err := s.app.FindRecordsByFilter(refund.CollectionRefunds,
		strings.Join(clauses, " && "), "-created", 200, 0, params)
	if err != nil {
		s.renderErrorPage(c, http.StatusInternalServerError, "Could not load the refunds", err.Error())
		return
	}

	actor := s.actor(c)
	selfID := ""
	if account := s.ownAccountQuiet(c); account != nil {
		selfID = account.Id
	}

	rows := make([]refundRow, 0, len(records))
	for _, rec := range records {
		row := refundRow{
			Record:       rec,
			RefundNo:     rec.GetString("refundNo"),
			Status:       rec.GetString("status"),
			Mode:         rec.GetString("mode"),
			Reason:       rec.GetString("reason"),
			Amount:       money.Format(money.Amount(rec.GetFloat("amount")), rec.GetString("currency")),
			AutoApproved: rec.GetBool("autoApproved"),
			Attempts:     rec.GetInt("attempts"),
			Created:      rec.GetDateTime("created").Time().Local().Format("2 Jan 15:04"),
			// The viewer's own request is marked, because the refusal to decide it is
			// easier to explain before they press the button than after.
			IsOwnRequest: rec.GetString("requestedBy") == selfID && selfID != "",
		}
		if order, err := s.app.FindRecordById("orders", rec.GetString("order")); err == nil && order != nil {
			row.OrderNo = order.GetString("orderNo")
			row.OrderID = order.Id
		}
		if requester, err := s.app.FindRecordById("users", rec.GetString("requestedBy")); err == nil && requester != nil {
			row.RequestedBy = requester.Email()
		}
		if reviewer := rec.GetString("reviewedBy"); reviewer != "" {
			if user, err := s.app.FindRecordById("users", reviewer); err == nil && user != nil {
				row.ReviewedBy = user.Email()
			}
		}
		rows = append(rows, row)
	}

	policy := s.refundPolicy()
	threshold := ""
	if policy.AutoApproveThreshold > 0 {
		threshold = money.Format(money.Amount(policy.AutoApproveThreshold), "USD")
	}

	s.render(c, http.StatusOK, "refunds.html", refundsPage{
		base:       s.newBase(c, "Refunds", "refunds"),
		Rows:       rows,
		Query:      c.Query("q"),
		Status:     c.Query("status"),
		Mine:       c.Query("mine") != "",
		Statuses:   refund.Statuses(),
		Threshold:  threshold,
		Restock:    policy.RestockOnFullRefund,
		CanApprove: actor.Can(permission.RefundApprove),
		CanRequest: actor.Can(permission.RefundRequest),
		CanExecute: actor.Can(permission.RefundExecute),
	})
}

// handleRefundForm renders the request form for an order.
func (s *Server) handleRefundForm(c *gin.Context) {
	orderID := strings.TrimSpace(c.Query("order"))

	view := refundForm{
		base:   s.newBase(c, "Request a refund", "refunds"),
		Modes:  refund.Modes(),
		Reason: s.refundReasons(),
	}

	if orderID != "" {
		order, err := s.app.FindRecordById("orders", orderID)
		if err != nil || order == nil {
			// An operator reads the order number off the order page, so it is accepted
			// here too.
			order, err = s.app.FindFirstRecordByFilter("orders",
				"orderNo = {:n}", map[string]any{"n": orderID})
		}
		if err == nil && order != nil {
			view.OrderID = order.Id
			view.OrderNo = order.GetString("orderNo")
			view.Currency = order.GetString("currency")
			view.Paid = money.Format(money.Amount(order.GetFloat("amountPaid")), view.Currency)
			view.Refunded = money.Format(money.Amount(order.GetFloat("amountRefunded")), view.Currency)
			if remaining, err := refund.Refundable(s.app, order.Id, ""); err == nil {
				view.Remaining = money.Format(money.Amount(remaining), view.Currency)
			}

			items, _ := s.app.FindRecordsByFilter("order_items",
				"order = {:o}", "created", 0, 0, map[string]any{"o": order.Id})
			for _, item := range items {
				left := item.GetInt("quantity") - item.GetInt("refundedQuantity")
				view.Items = append(view.Items, refundItemOption{
					ID:       item.Id,
					Name:     item.GetString("productName"),
					Variant:  item.GetString("variantLabel"),
					Unit:     money.Format(money.Amount(item.GetFloat("unitPrice")), view.Currency),
					Quantity: item.GetInt("quantity"),
					Left:     left,
				})
			}
		}
	}

	s.render(c, http.StatusOK, "refund_form.html", view)
}

// handleRefundRequest opens a refund.
func (s *Server) handleRefundRequest(c *gin.Context) {
	orderID := strings.TrimSpace(c.PostForm("order"))
	order, err := s.app.FindRecordById("orders", orderID)
	if err != nil || order == nil {
		order, err = s.app.FindFirstRecordByFilter("orders",
			"orderNo = {:n}", map[string]any{"n": orderID})
	}
	if err != nil || order == nil {
		s.setFlash(c, "danger", "No order has that number.")
		c.Redirect(http.StatusSeeOther, "/refunds/new")
		return
	}

	var requester string
	if account := s.ownAccountQuiet(c); account != nil {
		requester = account.Id
	}

	mode := nonEmptyString(c.PostForm("mode"), refund.ModeFull)
	currency := order.GetString("currency")

	var amount int64
	if mode == refund.ModePartialAmount {
		parsed, err := money.Parse(c.PostForm("amount"), currency)
		if err != nil {
			s.setFlash(c, "danger", "The amount: "+err.Error())
			c.Redirect(http.StatusSeeOther, "/refunds/new?order="+order.Id)
			return
		}
		amount = parsed.MinorUnits()
	}

	// The chosen quantities arrive as parallel arrays, which is what an HTML form can
	// express without JavaScript.
	var items []refund.ItemQuantity
	if mode == refund.ModePartialItems {
		ids := c.PostFormArray("itemId")
		quantities := c.PostFormArray("itemQuantity")
		for i, id := range ids {
			if strings.TrimSpace(id) == "" || i >= len(quantities) {
				continue
			}
			quantity, err := parseIntStrict(quantities[i])
			if err != nil || quantity <= 0 {
				continue
			}
			items = append(items, refund.ItemQuantity{OrderItemID: id, Quantity: quantity})
		}
	}

	number, err := refund.NextRefundNo(s.app, "REF", time.Now())
	if err != nil {
		s.setFlash(c, "danger", "Could not allocate a refund number: "+err.Error())
		c.Redirect(http.StatusSeeOther, "/refunds")
		return
	}

	rec, err := refund.Request(s.app, refund.RequestInput{
		RefundNo:     number,
		OrderID:      order.Id,
		Mode:         mode,
		Amount:       amount,
		Items:        items,
		Currency:     currency,
		Reason:       nonEmptyString(c.PostForm("reason"), "other"),
		ReasonDetail: strings.TrimSpace(c.PostForm("reasonDetail")),
		RequestedBy:  requester,
		Restock:      c.PostForm("restock") != "",
	})
	if err != nil {
		s.setFlash(c, "danger", err.Error())
		c.Redirect(http.StatusSeeOther, "/refunds/new?order="+order.Id)
		return
	}

	// Under the threshold it is approved immediately, so an operator does not have to
	// wait for a second person to agree to something the policy already allows. The
	// self-approval rule still applies, and a refusal here is not an error: the refund
	// simply stays pending.
	if decision, err := refund.Approve(s.app, rec, requester, "within the auto-approval threshold", s.refundPolicy()); err == nil && decision.AutoApproved {
		s.setFlash(c, "success", "Refund "+number+" was requested and approved automatically.")
	} else {
		s.setFlash(c, "success", "Refund "+number+" was requested and is waiting for a decision.")
	}

	s.audit(c, auditEntry{
		Event:      audit.EventSettingsChanged,
		Module:     "refunds",
		TargetType: "refund",
		TargetID:   rec.Id,
		Severity:   severityWarning,
		Summary: "Refund " + number + " requested for order " + order.GetString("orderNo") +
			" for " + money.Format(money.Amount(rec.GetFloat("amount")), currency),
		Detail: map[string]any{"mode": mode, "reason": rec.GetString("reason")},
	})
	c.Redirect(http.StatusSeeOther, "/refunds/"+rec.Id)
}

// handleRefundApprove approves a refund.
func (s *Server) handleRefundApprove(c *gin.Context) {
	rec, err := s.app.FindRecordById(refund.CollectionRefunds, c.Param("id"))
	if err != nil || rec == nil {
		s.renderErrorPage(c, http.StatusNotFound, "Not found", "That refund does not exist.")
		return
	}

	var approver string
	if account := s.ownAccountQuiet(c); account != nil {
		approver = account.Id
	}

	decision, err := refund.Approve(s.app, rec, approver,
		strings.TrimSpace(c.PostForm("comment")), s.refundPolicy())
	if err != nil {
		// The refusal is shown as it comes: it names the cap or the self-approval
		// rule, which is more use than a generic failure.
		s.setFlash(c, "danger", err.Error())
		c.Redirect(http.StatusSeeOther, "/refunds/"+rec.Id)
		return
	}

	summary := "Refund " + rec.GetString("refundNo") + " approved"
	if decision.AutoApproved {
		summary += " automatically, within the threshold"
	}
	s.audit(c, auditEntry{
		Event:      audit.EventSettingsChanged,
		Module:     "refunds",
		TargetType: "refund",
		TargetID:   rec.Id,
		Severity:   severityWarning,
		Summary:    summary,
		Detail:     map[string]any{"autoApproved": decision.AutoApproved, "approver": approver},
	})
	s.setFlash(c, "success", "Approved. Send it when you are ready.")
	c.Redirect(http.StatusSeeOther, "/refunds/"+rec.Id)
}

// handleRefundReject rejects a refund.
func (s *Server) handleRefundReject(c *gin.Context) {
	rec, err := s.app.FindRecordById(refund.CollectionRefunds, c.Param("id"))
	if err != nil || rec == nil {
		s.renderErrorPage(c, http.StatusNotFound, "Not found", "That refund does not exist.")
		return
	}

	var reviewer string
	if account := s.ownAccountQuiet(c); account != nil {
		reviewer = account.Id
	}

	comment := strings.TrimSpace(c.PostForm("comment"))
	if err := refund.Reject(s.app, rec, reviewer, comment); err != nil {
		s.setFlash(c, "danger", err.Error())
		c.Redirect(http.StatusSeeOther, "/refunds/"+rec.Id)
		return
	}

	s.audit(c, auditEntry{
		Event:      audit.EventSettingsChanged,
		Module:     "refunds",
		TargetType: "refund",
		TargetID:   rec.Id,
		Severity:   severityWarning,
		Summary:    "Refund " + rec.GetString("refundNo") + " rejected: " + comment,
	})
	s.setFlash(c, "success", "Rejected.")
	c.Redirect(http.StatusSeeOther, "/refunds/"+rec.Id)
}

// handleRefundExecute sends an approved refund.
func (s *Server) handleRefundExecute(c *gin.Context) {
	rec, err := s.app.FindRecordById(refund.CollectionRefunds, c.Param("id"))
	if err != nil || rec == nil {
		s.renderErrorPage(c, http.StatusNotFound, "Not found", "That refund does not exist.")
		return
	}

	// The provider is resolved from the order's payment transaction, so a refund goes
	// back the way the money came in.
	executor := s.refundExecutor(rec)

	result, err := refund.Execute(s.app, rec, executor, time.Now())
	if err != nil {
		s.setFlash(c, "danger", err.Error())
		c.Redirect(http.StatusSeeOther, "/refunds/"+rec.Id)
		return
	}

	severity := severityWarning
	summary := "Refund " + rec.GetString("refundNo") + " sent"
	if !result.Succeeded {
		severity = severityCritical
		summary = "Refund " + rec.GetString("refundNo") + " failed: " + result.Message
	}

	s.audit(c, auditEntry{
		Event:      audit.EventSettingsChanged,
		Module:     "refunds",
		TargetType: "refund",
		TargetID:   rec.Id,
		Severity:   severity,
		Summary:    summary,
		Detail:     map[string]any{"retryable": result.Retryable, "message": result.Message},
	})

	if result.Succeeded {
		s.setFlash(c, "success", "The refund was sent.")
	} else if result.Retryable {
		s.setFlash(c, "warning", "It failed in a way that may pass on a retry: "+result.Message)
	} else {
		s.setFlash(c, "danger", "It failed and should not simply be retried: "+result.Message)
	}
	c.Redirect(http.StatusSeeOther, "/refunds/"+rec.Id)
}

// handleRefundCancel withdraws a refund that has not been sent.
func (s *Server) handleRefundCancel(c *gin.Context) {
	rec, err := s.app.FindRecordById(refund.CollectionRefunds, c.Param("id"))
	if err != nil || rec == nil {
		s.renderErrorPage(c, http.StatusNotFound, "Not found", "That refund does not exist.")
		return
	}

	reason := strings.TrimSpace(c.PostForm("reason"))
	if err := refund.Cancel(s.app, rec, reason); err != nil {
		s.setFlash(c, "danger", err.Error())
		c.Redirect(http.StatusSeeOther, "/refunds/"+rec.Id)
		return
	}

	s.audit(c, auditEntry{
		Event:      audit.EventSettingsChanged,
		Module:     "refunds",
		TargetType: "refund",
		TargetID:   rec.Id,
		Severity:   severityWarning,
		Summary:    "Refund " + rec.GetString("refundNo") + " withdrawn: " + reason,
	})
	s.setFlash(c, "success", "Withdrawn, and its capacity released.")
	c.Redirect(http.StatusSeeOther, "/refunds/"+rec.Id)
}

// handleRefundDetail renders one refund.
func (s *Server) handleRefundDetail(c *gin.Context) {
	rec, err := s.app.FindRecordById(refund.CollectionRefunds, c.Param("id"))
	if err != nil || rec == nil {
		s.renderErrorPage(c, http.StatusNotFound, "Not found", "That refund does not exist.")
		return
	}

	view := refundDetailPage{
		base:       s.newBase(c, rec.GetString("refundNo"), "refunds"),
		Record:     rec,
		Currency:   rec.GetString("currency"),
		Amount:     money.Format(money.Amount(rec.GetFloat("amount")), rec.GetString("currency")),
		CanApprove: s.actor(c).Can(permission.RefundApprove),
		CanReject:  s.actor(c).Can(permission.RefundReject),
		CanExecute: s.actor(c).Can(permission.RefundExecute),
	}

	if order, err := s.app.FindRecordById("orders", rec.GetString("order")); err == nil && order != nil {
		view.OrderNo = order.GetString("orderNo")
		view.OrderID = order.Id
		view.OrderPaid = money.Format(money.Amount(order.GetFloat("amountPaid")), view.Currency)
		view.OrderRefunded = money.Format(money.Amount(order.GetFloat("amountRefunded")), view.Currency)
		view.OrderStatus = order.GetString("status")
	}
	if requester, err := s.app.FindRecordById("users", rec.GetString("requestedBy")); err == nil && requester != nil {
		view.RequestedBy = requester.Email()
	}
	if reviewer := rec.GetString("reviewedBy"); reviewer != "" {
		if user, err := s.app.FindRecordById("users", reviewer); err == nil && user != nil {
			view.ReviewedBy = user.Email()
		}
	}

	// The viewer's own request cannot be decided by them, so the buttons are not
	// offered. The rule is enforced in the package either way; this only avoids
	// offering an action that will be refused.
	if account := s.ownAccountQuiet(c); account != nil {
		view.IsOwnRequest = account.Id == rec.GetString("requestedBy")
	}

	var items []refund.ItemQuantity
	decodeRefundItems(rec.Get("items"), &items)
	for _, want := range items {
		if item, err := s.app.FindRecordById("order_items", want.OrderItemID); err == nil && item != nil {
			view.Items = append(view.Items, refundLine{
				Name:     item.GetString("productName"),
				Variant:  item.GetString("variantLabel"),
				Quantity: want.Quantity,
				Unit:     money.Format(money.Amount(item.GetFloat("unitPrice")), view.Currency),
			})
		}
	}

	s.render(c, http.StatusOK, "refund_detail.html", view)
}

// refundExecutor resolves the executor for a refund.
//
// A refund with no payment transaction was settled offline, and an offline refund is
// recorded rather than sent. That is a real case and not a failure: a shop that took
// cash gives cash back.
func (s *Server) refundExecutor(rec *core.Record) refund.Executor {
	if rec.GetString("transaction") == "" {
		return offlineExecutor{}
	}
	return offlineExecutor{}
}

// offlineExecutor records the decision without calling a provider.
type offlineExecutor struct{}

func (offlineExecutor) Refund(app core.App, rec *core.Record, amount int64) (refund.Execution, error) {
	// A live provider integration is registered per driver, and this build ships no
	// payment driver that can move money. Recording the decision is what an offline
	// settlement needs, and claiming a provider reference would be a fiction.
	return refund.Execution{
		ProviderRefundID: "offline-" + rec.GetString("refundNo"),
		Response:         map[string]any{"settled": "offline"},
	}, nil
}

// refundReasons lists the reason dictionary.
func (s *Server) refundReasons() []refundReasonOption {
	records, err := s.app.FindRecordsByFilter("refund_reason",
		"status = 'active'", "sortOrder", 0, 0)
	if err != nil {
		return nil
	}

	out := make([]refundReasonOption, 0, len(records))
	for _, rec := range records {
		out = append(out, refundReasonOption{Code: rec.GetString("code"), Label: rec.GetString("label")})
	}
	return out
}

// decodeRefundItems reads a stored item list through the shared JSON helper.
func decodeRefundItems(value any, out *[]refund.ItemQuantity) {
	decoded := decodeJSON(value)
	list, ok := decoded.([]any)
	if !ok {
		return
	}

	for _, entry := range list {
		object, ok := entry.(map[string]any)
		if !ok {
			continue
		}

		id, _ := object["orderItemId"].(string)
		quantity := 0
		switch v := object["quantity"].(type) {
		case float64:
			quantity = int(v)
		case int:
			quantity = v
		}
		if id != "" && quantity > 0 {
			*out = append(*out, refund.ItemQuantity{OrderItemID: id, Quantity: quantity})
		}
	}
}

// refundRow is one refund on the list.
type refundRow struct {
	Record       *core.Record
	RefundNo     string
	OrderNo      string
	OrderID      string
	Status       string
	Mode         string
	Reason       string
	Amount       string
	RequestedBy  string
	ReviewedBy   string
	Created      string
	AutoApproved bool
	Attempts     int
	IsOwnRequest bool
}

// refundsPage is the refund list.
type refundsPage struct {
	base
	Rows       []refundRow
	Query      string
	Status     string
	Mine       bool
	Statuses   []string
	Threshold  string
	Restock    bool
	CanApprove bool
	CanRequest bool
	CanExecute bool
}

// refundItemOption is one order line available to refund.
type refundItemOption struct {
	ID       string
	Name     string
	Variant  string
	Unit     string
	Quantity int
	Left     int
}

// refundReasonOption is one reason from the dictionary.
type refundReasonOption struct {
	Code  string
	Label string
}

// refundForm is the request form.
type refundForm struct {
	base
	OrderID   string
	OrderNo   string
	Currency  string
	Paid      string
	Refunded  string
	Remaining string
	Items     []refundItemOption
	Modes     []string
	Reason    []refundReasonOption
}

// refundLine is one refunded line on the detail page.
type refundLine struct {
	Name     string
	Variant  string
	Quantity int
	Unit     string
}

// refundDetailPage is one refund.
type refundDetailPage struct {
	base
	Record        *core.Record
	OrderNo       string
	OrderID       string
	OrderPaid     string
	OrderRefunded string
	OrderStatus   string
	RequestedBy   string
	ReviewedBy    string
	Currency      string
	Amount        string
	Items         []refundLine
	IsOwnRequest  bool
	CanApprove    bool
	CanReject     bool
	CanExecute    bool
}
