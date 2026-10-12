package server

import (
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/pocketbase/pocketbase/core"

	"pocketadmin/internal/audit"
	"pocketadmin/internal/money"
	"pocketadmin/internal/payment"
	"pocketadmin/internal/permission"
)

// registerPaymentRoutes mounts the payment screens.
func (s *Server) registerPaymentRoutes(panel *gin.RouterGroup) {
	s.readGuarded(panel, http.MethodGet, "/payments", permission.PaymentTransactionList, s.handlePaymentList)
	s.readGuarded(panel, http.MethodGet, "/payments/reconcile", permission.PaymentReconcile, s.handleReconcileForm)

	s.guarded(panel, http.MethodPost, "/payments/manual", permission.PaymentManualRecord, s.handleManualPayment)
	s.guarded(panel, http.MethodPost, "/payments/reconcile", permission.PaymentReconcile, s.handleReconcile)
	s.guarded(panel, http.MethodPost, "/payments/intents", permission.PaymentIntentCreate, s.handleCreateIntent)
}

// handlePaymentList renders the transactions and the intents behind them.
func (s *Server) handlePaymentList(c *gin.Context) {
	clauses := []string{}
	params := map[string]any{}

	if status := strings.TrimSpace(c.Query("status")); status != "" {
		clauses = append(clauses, "status = {:status}")
		params["status"] = status
	}
	if search := strings.TrimSpace(c.Query("q")); search != "" {
		clauses = append(clauses, "(order.orderNo ~ {:q} || providerTxnId ~ {:q})")
		params["q"] = search
	}
	if method := strings.TrimSpace(c.Query("method")); method != "" {
		clauses = append(clauses, "intent.method = {:method}")
		params["method"] = method
	}
	if c.Query("unreconciled") != "" {
		clauses = append(clauses, "status = 'succeeded' && reconciled = false")
	}

	filter := strings.Join(clauses, " && ")

	records, err := s.app.FindRecordsByFilter(payment.CollectionTransactions,
		filter, "-created", 200, 0, params)
	if err != nil {
		s.renderErrorPage(c, http.StatusInternalServerError, "Could not load the payments", err.Error())
		return
	}

	rows := make([]paymentRow, 0, len(records))
	for _, rec := range records {
		row := paymentRow{
			Record:     rec,
			Status:     rec.GetString("status"),
			ProviderID: rec.GetString("providerTxnId"),
			Amount:     money.Format(money.Amount(rec.GetFloat("amount")), rec.GetString("currency")),
			Currency:   rec.GetString("currency"),
			Created:    rec.GetDateTime("created").Time().Local().Format("2 Jan 2006 15:04"),
			Manual:     rec.GetBool("manual"),
			Reconciled: rec.GetBool("reconciled"),
			Callbacks:  rec.GetInt("callbackCount"),
			Failure:    rec.GetString("failureMessage"),
		}

		if order, err := s.app.FindRecordById("orders", rec.GetString("order")); err == nil && order != nil {
			row.OrderNo = order.GetString("orderNo")
			row.OrderID = order.Id
		}
		if intent, err := s.app.FindRecordById(payment.CollectionIntents, rec.GetString("intent")); err == nil && intent != nil {
			row.Method = intent.GetString("method")
		}
		rows = append(rows, row)
	}

	// The intents still waiting, which is what an operator looks at when a customer
	// says the payment did not go through.
	open, _ := s.app.FindRecordsByFilter(payment.CollectionIntents,
		"status = 'pending' || status = 'processing'", "-created", 50, 0)
	openRows := make([]openIntentRow, 0, len(open))
	now := time.Now()
	for _, rec := range open {
		row := openIntentRow{
			Record:   rec,
			Amount:   money.Format(money.Amount(rec.GetFloat("amount")), rec.GetString("currency")),
			Method:   rec.GetString("method"),
			Status:   rec.GetString("status"),
			Attempts: rec.GetInt("attemptCount"),
		}
		if order, err := s.app.FindRecordById("orders", rec.GetString("order")); err == nil && order != nil {
			row.OrderNo = order.GetString("orderNo")
			row.OrderID = order.Id
		}
		if expires := rec.GetDateTime("expiresAt"); !expires.IsZero() {
			row.Expires = expires.Time().Local().Format("2 Jan 15:04")
			row.Expiring = expires.Time().Before(now.Add(30 * time.Minute))
		}
		openRows = append(openRows, row)
	}

	actor := s.actor(c)
	s.render(c, http.StatusOK, "payments.html", paymentsPage{
		base:         s.newBase(c, "Payments", "payments.transactions"),
		Rows:         rows,
		OpenIntents:  openRows,
		Status:       c.Query("status"),
		Query:        c.Query("q"),
		Method:       c.Query("method"),
		Unreconciled: c.Query("unreconciled") != "",
		RawQuery:     c.Request.URL.RawQuery,
		Statuses:     payment.Statuses(),
		Methods:      payment.Methods(),
		CanRecord:    actor.Can(permission.PaymentManualRecord),
		CanReconcile: actor.Can(permission.PaymentReconcile),
	})
}

// handleReconcileForm renders the settlement comparison.
func (s *Server) handleReconcileForm(c *gin.Context) {
	s.render(c, http.StatusOK, "payment_reconcile.html", reconcilePage{
		base: s.newBase(c, "Reconcile settlements", "payments.transactions"),
	})
}

// handleReconcile compares a pasted settlement report against the local records.
func (s *Server) handleReconcile(c *gin.Context) {
	raw := strings.TrimSpace(c.PostForm("settlements"))
	if raw == "" {
		s.setFlash(c, "danger", "Paste a settlement report first.")
		c.Redirect(http.StatusSeeOther, "/payments/reconcile")
		return
	}

	settlements, problems := parseSettlements(raw)
	if len(settlements) == 0 {
		s.setFlash(c, "danger", "No settlement lines could be read: "+strings.Join(problems, "; "))
		c.Redirect(http.StatusSeeOther, "/payments/reconcile")
		return
	}

	discrepancies, err := payment.Reconcile(s.app, settlements, time.Now())
	if err != nil {
		s.setFlash(c, "danger", "Could not reconcile: "+err.Error())
		c.Redirect(http.StatusSeeOther, "/payments/reconcile")
		return
	}

	now := time.Now()
	rows := make([]discrepancyRow, 0, len(discrepancies))
	for _, d := range discrepancies {
		rows = append(rows, discrepancyRow{
			ProviderTxnID: d.ProviderTxnID,
			Kind:          d.Kind,
			Detail:        d.Detail,
			Local:         money.Format(money.Amount(d.LocalAmount), "USD"),
			Remote:        money.Format(money.Amount(d.RemoteAmount), "USD"),
		})
	}

	// Two settlements for the same transaction are reported, because the second is
	// money that arrived twice for one sale.
	seen := map[string]int{}
	for _, settlement := range settlements {
		seen[settlement.ProviderTxnID]++
	}
	duplicates := []string{}
	for id, count := range seen {
		if count > 1 {
			duplicates = append(duplicates, id)
		}
	}

	s.audit(c, auditEntry{
		Event:      audit.EventDataExported,
		Module:     "payments",
		TargetType: "payment_transaction",
		Severity:   severityWarning,
		Summary: "Reconciled " + itoa(len(settlements)) + " settlement line(s), " +
			itoa(len(discrepancies)) + " discrepancy(ies)",
		Detail: map[string]any{
			"lines": len(settlements), "discrepancies": len(discrepancies),
			"unreadable": problems, "duplicates": duplicates,
		},
	})

	view := reconcilePage{
		base:          s.newBase(c, "Reconcile settlements", "payments.transactions"),
		Raw:           raw,
		Lines:         len(settlements),
		Discrepancies: rows,
		Problems:      problems,
		Duplicates:    duplicates,
		Matched:       len(settlements) - len(discrepancies) - len(duplicates),
		Ran:           true,
	}
	if view.Matched < 0 {
		view.Matched = 0
	}
	_ = now

	s.render(c, http.StatusOK, "payment_reconcile.html", view)
}

// handleManualPayment records an offline payment.
func (s *Server) handleManualPayment(c *gin.Context) {
	orderID := strings.TrimSpace(c.PostForm("order"))
	reason := strings.TrimSpace(c.PostForm("reason"))

	if orderID == "" {
		s.setFlash(c, "danger", "Choose an order.")
		c.Redirect(http.StatusSeeOther, "/payments")
		return
	}

	order, err := s.app.FindRecordById("orders", orderID)
	if err != nil || order == nil {
		// An operator reads the human-readable number off the order page, so both it
		// and the identifier are accepted. Refusing the number they can see would be
		// an unhelpful kind of correctness.
		order, err = s.app.FindFirstRecordByFilter("orders",
			"orderNo = {:n}", map[string]any{"n": strings.TrimSpace(c.PostForm("order"))})
		if err != nil || order == nil {
			s.setFlash(c, "danger", "No order has that number.")
			c.Redirect(http.StatusSeeOther, "/payments")
			return
		}
	}

	currency := order.GetString("currency")
	amount, err := money.Parse(c.PostForm("amount"), currency)
	if err != nil {
		s.setFlash(c, "danger", "The amount: "+err.Error())
		c.Redirect(http.StatusSeeOther, "/payments")
		return
	}

	var operator string
	if account := s.ownAccountQuiet(c); account != nil {
		operator = account.Id
	}

	txn, err := payment.RecordManualPayment(s.app, payment.ManualRequest{
		OrderID:    orderID,
		Amount:     amount.MinorUnits(),
		Currency:   currency,
		Method:     nonEmptyString(c.PostForm("method"), payment.MethodBankTransfer),
		Reason:     reason,
		RecordedBy: operator,
		Now:        time.Now(),
	})
	if err != nil {
		s.setFlash(c, "danger", err.Error())
		c.Redirect(http.StatusSeeOther, "/payments")
		return
	}

	s.audit(c, auditEntry{
		Event:      audit.EventSettingsChanged,
		Module:     "payments",
		TargetType: "payment_transaction",
		TargetID:   txn.Id,
		// Critical, because this is the one path that credits an order with no
		// provider confirming anything.
		Severity: severityCritical,
		Summary: "A payment of " + money.Format(amount, currency) + " was recorded by hand for order " +
			order.GetString("orderNo") + ": " + reason,
		Detail: map[string]any{"method": txn.GetString("method"), "operator": operator},
	})
	s.setFlash(c, "success", "Recorded.")
	c.Redirect(http.StatusSeeOther, "/payments")
}

// handleCreateIntent opens a payment attempt against an order.
func (s *Server) handleCreateIntent(c *gin.Context) {
	orderID := strings.TrimSpace(c.PostForm("order"))
	order, err := s.app.FindRecordById("orders", orderID)
	if err != nil || order == nil {
		s.setFlash(c, "danger", "Choose an order.")
		c.Redirect(http.StatusSeeOther, "/payments")
		return
	}

	currency := order.GetString("currency")
	amount, err := money.Parse(c.PostForm("amount"), currency)
	if err != nil {
		s.setFlash(c, "danger", "The amount: "+err.Error())
		c.Redirect(http.StatusSeeOther, "/payments")
		return
	}

	var operator string
	if account := s.ownAccountQuiet(c); account != nil {
		operator = account.Id
	}

	intent, err := payment.CreateIntent(s.app, payment.IntentRequest{
		OrderID:   orderID,
		Amount:    amount.MinorUnits(),
		Currency:  currency,
		Method:    nonEmptyString(c.PostForm("method"), payment.MethodCard),
		CreatedBy: operator,
		Now:       time.Now(),
	})
	if err != nil {
		s.setFlash(c, "danger", err.Error())
		c.Redirect(http.StatusSeeOther, "/payments")
		return
	}

	s.audit(c, auditEntry{
		Event:      audit.EventSettingsChanged,
		Module:     "payments",
		TargetType: "payment_intent",
		TargetID:   intent.Id,
		Severity:   severityWarning,
		Summary:    "A payment was started for order " + order.GetString("orderNo"),
		Detail:     map[string]any{"amount": amount.MinorUnits(), "method": intent.GetString("method")},
	})
	s.setFlash(c, "success", "Payment started.")
	c.Redirect(http.StatusSeeOther, "/payments")
}

// parseSettlements reads a settlement report.
//
// The format is one line per transaction: an identifier, an amount in minor units,
// and optionally a fee. It is deliberately the simplest thing that can be copied out
// of a provider's own report, because a format that needs a converter is a format
// that gets converted wrongly.
func parseSettlements(raw string) ([]payment.Settlement, []string) {
	out := make([]payment.Settlement, 0)
	problems := make([]string, 0)

	for number, line := range strings.Split(raw, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}

		fields := strings.FieldsFunc(trimmed, func(r rune) bool {
			return r == ',' || r == '\t' || r == ';' || r == ' '
		})
		if len(fields) < 2 {
			problems = append(problems, "line "+itoa(number+1)+" has no amount")
			continue
		}

		id := strings.TrimSpace(fields[0])
		amount, err := parseIntStrict(fields[1])
		if err != nil {
			problems = append(problems, "line "+itoa(number+1)+" has an amount that is not a number")
			continue
		}

		settlement := payment.Settlement{ProviderTxnID: id, Amount: int64(amount)}
		if len(fields) >= 3 {
			if fee, err := parseIntStrict(fields[2]); err == nil {
				settlement.FeeAmount = int64(fee)
			}
		}
		out = append(out, settlement)
	}

	return out, problems
}

// paymentRow is one transaction.
type paymentRow struct {
	Record     *core.Record
	OrderNo    string
	OrderID    string
	Status     string
	ProviderID string
	Method     string
	Amount     string
	Currency   string
	Created    string
	Manual     bool
	Reconciled bool
	Callbacks  int
	Failure    string
}

// openIntentRow is one payment still waiting.
type openIntentRow struct {
	Record   *core.Record
	OrderNo  string
	OrderID  string
	Amount   string
	Method   string
	Status   string
	Expires  string
	Expiring bool
	Attempts int
}

// paymentsPage is the transaction list.
type paymentsPage struct {
	base
	Rows         []paymentRow
	OpenIntents  []openIntentRow
	Status       string
	Query        string
	Method       string
	Unreconciled bool
	RawQuery     string
	Statuses     []string
	Methods      []string
	CanRecord    bool
	CanReconcile bool
}

// discrepancyRow is one difference between a report and the records.
type discrepancyRow struct {
	ProviderTxnID string
	Kind          string
	Detail        string
	Local         string
	Remote        string
}

// reconcilePage is the settlement comparison.
type reconcilePage struct {
	base
	Raw           string
	Lines         int
	Matched       int
	Discrepancies []discrepancyRow
	Problems      []string
	Duplicates    []string
	Ran           bool
}
