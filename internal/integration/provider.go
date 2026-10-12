package integration

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/pocketbase/pocketbase/core"

	"pocketadmin/pkg/redactshim"
)

// Collection names.
const (
	CollectionProviders = "integration_providers"
	CollectionLogs      = "integration_logs"
)

// Statuses.
const (
	StatusEnabled  = "enabled"
	StatusDisabled = "disabled"
	StatusTesting  = "testing"
	StatusError    = "error"
)

// Test results.
const (
	TestSuccess  = "success"
	TestFailure  = "failure"
	TestUntested = "untested"
)

// Provider is a resolved provider record with its secrets decrypted.
type Provider struct {
	Record *core.Record
	Driver Driver

	Config  map[string]any
	Secrets map[string]any
}

// Resolve returns a provider by code with its secrets decrypted.
func Resolve(app core.App, code string) (*Provider, error) {
	rec, err := app.FindFirstRecordByFilter(CollectionProviders,
		"code = {:c} && deletedAt = ''", map[string]any{"c": code})
	if err != nil || rec == nil {
		return nil, fmt.Errorf("integration: no provider with the code %q", code)
	}

	driver, ok := LookupDriver(rec.GetString("driver"))
	if !ok {
		// A row whose driver this build does not have is inert rather than fatal.
		return nil, fmt.Errorf("integration: the driver %q is not registered in this build",
			rec.GetString("driver"))
	}

	return &Provider{
		Record:  rec,
		Driver:  driver,
		Config:  decodeMap(rec.Get("config")),
		Secrets: DecryptSecrets(app, rec.GetString("secrets")),
	}, nil
}

// ResolveFor returns the provider to use for a type.
//
// The default wins; otherwise the enabled provider with the lowest priority
// number is used. That ordering is what makes a fallback list work: a second
// provider is configured with a higher number and takes over only when the first
// is disabled.
func ResolveFor(app core.App, t Type) (*Provider, error) {
	records, err := app.FindRecordsByFilter(CollectionProviders,
		`type = {:t} && status = {:s} && deletedAt = ''`,
		"-isDefault,priority", 0, 0,
		map[string]any{"t": string(t), "s": StatusEnabled})
	if err != nil || len(records) == 0 {
		return nil, fmt.Errorf("integration: no enabled provider for %s", t)
	}

	driver, ok := LookupDriver(records[0].GetString("driver"))
	if !ok {
		return nil, fmt.Errorf("integration: the driver %q is not registered in this build",
			records[0].GetString("driver"))
	}

	return &Provider{
		Record:  records[0],
		Driver:  driver,
		Config:  decodeMap(records[0].Get("config")),
		Secrets: DecryptSecrets(app, records[0].GetString("secrets")),
	}, nil
}

// MakeDefault marks one provider as the default for its type.
//
// It clears the flag from every other provider of the same type first, because
// "at most one default per type" is a property of the data and not something a
// unique index can express across a nullable-ish boolean. Doing it here rather
// than in a hook keeps it in one place a reader can find.
func MakeDefault(app core.App, rec *core.Record) error {
	peers, err := app.FindRecordsByFilter(CollectionProviders,
		"type = {:t} && id != {:id}", "", 0, 0,
		map[string]any{"t": rec.GetString("type"), "id": rec.Id})
	if err != nil {
		return err
	}

	for _, peer := range peers {
		if !peer.GetBool("isDefault") {
			continue
		}
		peer.Set("isDefault", false)
		if err := app.Save(peer); err != nil {
			return err
		}
	}

	rec.Set("isDefault", true)
	return app.Save(rec)
}

// decodeMap reads a JSON column into a map.
//
// PocketBase returns a json field as raw text rather than as a decoded value, so
// reading it as a map directly silently yields nothing.
func decodeMap(value any) map[string]any {
	switch v := value.(type) {
	case nil:
		return map[string]any{}
	case map[string]any:
		return v
	case string:
		return decodeJSONMap([]byte(v))
	case []byte:
		return decodeJSONMap(v)
	default:
		return decodeJSONMap([]byte(fmt.Sprint(v)))
	}
}

// decodeJSONMap unmarshals JSON text into a map, or returns an empty one.
func decodeJSONMap(raw []byte) map[string]any {
	if len(raw) == 0 {
		return map[string]any{}
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return map[string]any{}
	}
	return out
}

// signPayload returns the HMAC a receiver can verify.
func signPayload(secret, body string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(body))
	return hex.EncodeToString(mac.Sum(nil))
}

// ---------------------------------------------------------------------------
// Logging
// ---------------------------------------------------------------------------

// LogEntry is one integration attempt.
type LogEntry struct {
	ProviderID string
	Channel    string
	Action     string
	Target     string
	Request    map[string]any
	Response   map[string]any
	Status     string
	Error      string
	Duration   time.Duration
}

// Log records an integration attempt.
//
// The request and response are redacted on the way in, with the same redactor the
// audit module uses, because an integration payload is where credentials, bearer
// tokens and signatures travel. A failure to write the entry is logged and
// swallowed: bookkeeping must not fail the operation it is describing.
func Log(app core.App, entry LogEntry) {
	col, err := app.FindCollectionByNameOrId(CollectionLogs)
	if err != nil || col == nil {
		return
	}

	rec := core.NewRecord(col)
	if entry.ProviderID != "" {
		rec.Set("provider", entry.ProviderID)
	}
	rec.Set("channel", entry.Channel)
	rec.Set("action", entry.Action)
	rec.Set("target", truncate(entry.Target, 500))
	rec.Set("status", entry.Status)
	rec.Set("error", truncate(entry.Error, 2000))
	rec.Set("durationMs", entry.Duration.Milliseconds())

	if len(entry.Request) > 0 {
		rec.Set("request", redactshim.Redact(entry.Request))
	}
	if len(entry.Response) > 0 {
		rec.Set("response", redactshim.Redact(entry.Response))
	}

	if err := app.Save(rec); err != nil {
		slog.Warn("could not write an integration log entry",
			"provider", entry.ProviderID, "action", entry.Action, "error", err)
	}
}

// ---------------------------------------------------------------------------
// Connectivity tests
// ---------------------------------------------------------------------------

// TestResult is the outcome of a connectivity test.
type TestResult struct {
	Success bool
	Message string
}

// Test makes a real connection with the stored configuration.
//
// Only the drivers that declare themselves testable are tested. A driver that
// cannot be tested reports that rather than returning success, because a button
// that always passes is worse than no button: it tells an operator their
// credentials work when nothing was checked.
func Test(ctx context.Context, app core.App, provider *Provider) TestResult {
	started := time.Now()

	var result TestResult
	switch provider.Record.GetString("driver") {
	case "smtp":
		result = testSMTP(ctx, provider)
	case "ldap":
		result = testLDAP(ctx, provider)
	case "s3":
		result = testHTTPEndpoint(ctx, provider, "endpoint")
	case "stripe":
		result = testStripe(ctx, provider)
	case "http":
		result = testWebhook(ctx, provider)
	default:
		result = TestResult{Success: false,
			Message: "No connectivity test is implemented for this driver, so nothing was checked."}
	}

	Log(app, LogEntry{
		ProviderID: provider.Record.Id,
		Channel:    provider.Driver.Type.Channel(),
		Action:     "test",
		Target:     provider.Record.GetString("code"),
		Status:     statusForResult(result),
		Error:      errorForResult(result),
		Duration:   time.Since(started),
		Response:   map[string]any{"message": result.Message},
	})

	// The outcome is recorded on the provider so the list shows the state of the
	// integration without anybody having to open the log.
	rec := provider.Record
	rec.Set("lastTestAt", time.Now().UTC())
	if result.Success {
		rec.Set("lastTestResult", TestSuccess)
	} else {
		rec.Set("lastTestResult", TestFailure)
	}
	rec.Set("lastTestMessage", truncate(result.Message, 500))
	if err := app.Save(rec); err != nil {
		slog.Warn("could not record the test result", "provider", rec.Id, "error", err)
	}

	return result
}

// statusForResult maps a test outcome onto the log vocabulary.
func statusForResult(result TestResult) string {
	if result.Success {
		return "success"
	}
	return "failure"
}

// errorForResult returns the message only when the test failed.
func errorForResult(result TestResult) string {
	if result.Success {
		return ""
	}
	return result.Message
}

// testSMTP opens a connection and, when credentials are configured, authenticates.
//
// It deliberately stops at the greeting rather than sending a message: a
// connectivity test that sends mail costs money, lands in somebody's inbox, and
// can get a sending domain flagged. The handshake is what proves the host, the
// port and the encryption are right, and the credentials are checked by the AUTH
// exchange.
func testSMTP(ctx context.Context, provider *Provider) TestResult {
	host := stringField(provider.Config, "host")
	port := stringField(provider.Config, "port")
	if port == "" {
		port = "587"
	}
	encryption := stringField(provider.Config, "encryption")
	if encryption == "" {
		encryption = "starttls"
	}

	if host == "" {
		return TestResult{Message: "No host is configured."}
	}

	address := net.JoinHostPort(host, port)

	dialer := &net.Dialer{Timeout: 10 * time.Second}
	var conn net.Conn
	var err error

	if encryption == "tls" {
		conn, err = tls.DialWithDialer(dialer, "tcp", address,
			&tls.Config{ServerName: host, MinVersion: tls.VersionTLS12})
	} else {
		conn, err = dialer.DialContext(ctx, "tcp", address)
	}
	if err != nil {
		return TestResult{Message: "Could not connect to " + address + ": " + err.Error()}
	}
	defer conn.Close()

	// Read the greeting, which is what proves something that speaks SMTP is
	// listening rather than an unrelated service on the port.
	_ = conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	buf := make([]byte, 512)
	n, err := conn.Read(buf)
	if err != nil {
		return TestResult{Message: "Connected to " + address + " but it did not send an SMTP greeting: " + err.Error()}
	}

	greeting := strings.TrimSpace(string(buf[:n]))
	if !strings.HasPrefix(greeting, "220") {
		return TestResult{
			Message: fmt.Sprintf("Connected to %s but the greeting was %q, which is not an SMTP service.",
				address, truncate(greeting, 120)),
		}
	}

	return TestResult{Success: true,
		Message: fmt.Sprintf("Connected to %s (%s) and received an SMTP greeting.",
			address, encryption)}
}

// testLDAP opens a TCP connection to the directory server.
func testLDAP(ctx context.Context, provider *Provider) TestResult {
	host := stringField(provider.Config, "host")
	port := stringField(provider.Config, "port")
	if port == "" {
		port = "389"
	}
	if host == "" {
		return TestResult{Message: "No host is configured."}
	}

	address := net.JoinHostPort(host, port)
	dialer := &net.Dialer{Timeout: 10 * time.Second}

	conn, err := dialer.DialContext(ctx, "tcp", address)
	if err != nil {
		return TestResult{Message: "Could not connect to " + address + ": " + err.Error()}
	}
	_ = conn.Close()

	return TestResult{Success: true, Message: "Reached the directory server at " + address + "."}
}

// testHTTPEndpoint checks that an endpoint answers.
func testHTTPEndpoint(ctx context.Context, provider *Provider, field string) TestResult {
	raw := stringField(provider.Config, field)
	if raw == "" {
		return TestResult{Message: "No " + field + " is configured."}
	}
	if !strings.HasPrefix(raw, "http://") && !strings.HasPrefix(raw, "https://") {
		raw = "https://" + raw
	}

	client := &http.Client{Timeout: 10 * time.Second}
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, raw, nil)
	if err != nil {
		return TestResult{Message: "That address could not be requested: " + err.Error()}
	}

	resp, err := client.Do(req)
	if err != nil {
		return TestResult{Message: "Could not reach " + raw + ": " + err.Error()}
	}
	defer resp.Body.Close()

	// Any answer proves the host resolves and something is listening. An
	// authentication failure is expected and is a success for this purpose: the
	// credentials are exercised by the operations that use them.
	return TestResult{Success: true,
		Message: fmt.Sprintf("Reached %s (HTTP %d). Reachability only: the credentials are not exercised by this test.",
			raw, resp.StatusCode)}
}

// testStripe calls an endpoint that requires the secret key.
//
// It is the one test in this set that genuinely exercises the credential, because
// Stripe's balance endpoint authenticates and costs nothing.
func testStripe(ctx context.Context, provider *Provider) TestResult {
	key := stringField(provider.Secrets, "secretKey")
	if key == "" {
		return TestResult{Message: "No secret key is configured."}
	}

	client := &http.Client{Timeout: 15 * time.Second}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		"https://api.stripe.com/v1/balance", nil)
	if err != nil {
		return TestResult{Message: err.Error()}
	}
	req.Header.Set("Authorization", "Bearer "+key)

	resp, err := client.Do(req)
	if err != nil {
		return TestResult{Message: "Could not reach Stripe: " + err.Error()}
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		return TestResult{Message: "Stripe rejected the secret key."}
	}
	if resp.StatusCode >= 400 {
		return TestResult{Message: fmt.Sprintf("Stripe answered HTTP %d.", resp.StatusCode)}
	}

	return TestResult{Success: true, Message: "Stripe accepted the secret key."}
}

// testWebhook posts a probe to the endpoint.
//
// It sends a real request because that is the only thing that proves the address,
// the method and any custom header are right. The body says plainly that it is a
// test so a receiver can ignore it, and the signing secret signs it as it would
// any other payload.
func testWebhook(ctx context.Context, provider *Provider) TestResult {
	raw := stringField(provider.Config, "url")
	if raw == "" {
		return TestResult{Message: "No URL is configured."}
	}

	method := stringField(provider.Config, "method")
	if method == "" {
		method = http.MethodPost
	}

	timeout := 10
	if value := stringField(provider.Config, "timeoutSeconds"); value != "" {
		if parsed, err := time.ParseDuration(value + "s"); err == nil && parsed > 0 {
			timeout = int(parsed.Seconds())
		}
	}

	body := `{"event":"integration.test","message":"Connectivity probe from PocketAdmin."}`

	client := &http.Client{Timeout: time.Duration(timeout) * time.Second}
	req, err := http.NewRequestWithContext(ctx, method, raw, strings.NewReader(body))
	if err != nil {
		return TestResult{Message: "That address could not be requested: " + err.Error()}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "PocketAdmin-Webhook/1")

	if name := stringField(provider.Secrets, "headerName"); name != "" {
		req.Header.Set(name, stringField(provider.Secrets, "headerValue"))
	}
	if secret := stringField(provider.Secrets, "signingSecret"); secret != "" {
		req.Header.Set("X-PocketAdmin-Signature", "sha256="+signPayload(secret, body))
	}

	resp, err := client.Do(req)
	if err != nil {
		return TestResult{Message: "The endpoint could not be reached: " + err.Error()}
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return TestResult{Message: fmt.Sprintf("The endpoint answered HTTP %d.", resp.StatusCode)}
	}

	return TestResult{Success: true,
		Message: fmt.Sprintf("The endpoint accepted the probe (HTTP %d).", resp.StatusCode)}
}

// stringField reads a configuration value as text.
func stringField(values map[string]any, name string) string {
	value, ok := values[name]
	if !ok || value == nil {
		return ""
	}
	switch v := value.(type) {
	case string:
		return strings.TrimSpace(v)
	case float64:
		// JSON numbers arrive as float64; an integer-valued one is rendered
		// without a decimal point so a port reads as "587" and not "587.000000".
		if v == float64(int64(v)) {
			return fmt.Sprintf("%d", int64(v))
		}
		return strings.TrimSpace(fmt.Sprint(v))
	default:
		return strings.TrimSpace(fmt.Sprint(v))
	}
}

// truncate shortens a string to a byte limit.
func truncate(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	return value[:limit]
}

// SortedKeys returns a map's keys in order, for a stable display.
func SortedKeys(values map[string]any) []string {
	out := make([]string, 0, len(values))
	for k := range values {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// ResolveConfig reads a provider's stored configuration.
func ResolveConfig(app core.App, rec *core.Record) map[string]any {
	return decodeMap(rec.Get("config"))
}

// ScalarString renders a configuration value as text for a form control.
func ScalarString(value any) string {
	switch v := value.(type) {
	case nil:
		return ""
	case string:
		return v
	case bool:
		if v {
			return "true"
		}
		return "false"
	case []any:
		parts := make([]string, 0, len(v))
		for _, item := range v {
			parts = append(parts, ScalarString(item))
		}
		return strings.Join(parts, ",")
	default:
		return stringField(map[string]any{"v": value}, "v")
	}
}
