package integration

import (
	"sort"
	"strings"
)

// Type is what a provider is for.
type Type string

// Provider types.
const (
	TypeEmail   Type = "email"
	TypeSMS     Type = "sms"
	TypeStorage Type = "storage"
	TypeIDP     Type = "idp"
	TypePayment Type = "payment"
	TypeWebhook Type = "webhook"
)

// Channel is what appears on an integration log entry. It is the same vocabulary
// as the provider type except for identity, where the log says "auth": the log
// describes what the platform did, and it exchanged a token rather than being an
// identity provider.
func (t Type) Channel() string {
	if t == TypeIDP {
		return "auth"
	}
	return string(t)
}

// FieldKind is how a configuration field is rendered and validated.
type FieldKind string

// Field kinds.
const (
	FieldText     FieldKind = "text"
	FieldPassword FieldKind = "password"
	FieldNumber   FieldKind = "number"
	FieldBool     FieldKind = "bool"
	FieldSelect   FieldKind = "select"
	FieldList     FieldKind = "list"
)

// Field is one configuration input a driver needs.
type Field struct {
	Name string

	// Label is what the screen shows. A driver's field names are the provider's
	// vocabulary, not the operator's.
	Label string

	Kind FieldKind

	// Required marks a field the driver cannot work without.
	Required bool

	// Default is the value used when nothing is stored.
	Default string

	// Options are the permitted values of a select.
	Options []string

	// Help explains the field, including where to find the value. It is the
	// difference between a form an operator can complete and one they abandon.
	Help string
}

// Driver describes an implementation.
type Driver struct {
	Code string
	Type Type
	Name string

	// Description says what the driver is and what it needs.
	Description string

	// Config is the non-secret configuration.
	Config []Field

	// Secrets are the credential fields. They are encrypted at rest and never
	// returned to the browser.
	Secrets []Field

	// Testable reports whether a connectivity test is implemented. A driver that
	// cannot be tested says so rather than offering a button that always passes.
	Testable bool
}

// drivers is the catalogue.
//
// It is fixed in code for the same reason the job handlers are: an operator
// chooses which implementation to use and how to configure it, and cannot
// introduce behaviour. A code that is not here cannot be stored -- the column is a
// closed select built from this list.
func drivers() []Driver {
	return []Driver{
		// Email ---------------------------------------------------------------
		{
			Code: "smtp", Type: TypeEmail, Name: "SMTP",
			Description: "Send through a mail server. Works with any provider that offers one; the credentials are the mailbox or relay account.",
			Testable:    true,
			Config: []Field{
				{Name: "host", Label: "Host", Kind: FieldText, Required: true, Help: "Hostname of the mail server."},
				{Name: "port", Label: "Port", Kind: FieldNumber, Default: "587",
					Help: "587 for STARTTLS, 465 for implicit TLS, 25 for a relay."},
				{Name: "encryption", Label: "Encryption", Kind: FieldSelect, Default: "starttls",
					Options: []string{"none", "starttls", "tls"}},
				{Name: "fromAddress", Label: "From address", Kind: FieldText, Required: true},
				{Name: "fromName", Label: "From name", Kind: FieldText},
			},
			Secrets: []Field{
				{Name: "username", Label: "Username", Kind: FieldText},
				{Name: "password", Label: "Password", Kind: FieldPassword, Required: true},
			},
		},
		{
			Code: "sendgrid", Type: TypeEmail, Name: "SendGrid",
			Description: "Send through SendGrid's transactional API.",
			Config: []Field{
				{Name: "fromAddress", Label: "From address", Kind: FieldText, Required: true},
				{Name: "fromName", Label: "From name", Kind: FieldText},
			},
			Secrets: []Field{
				{Name: "apiKey", Label: "API key", Kind: FieldPassword, Required: true},
			},
		},
		{
			Code: "ses", Type: TypeEmail, Name: "Amazon SES",
			Description: "Send through Amazon SES.",
			Config: []Field{
				{Name: "region", Label: "Region", Kind: FieldText, Required: true, Default: "us-east-1"},
				{Name: "fromAddress", Label: "From address", Kind: FieldText, Required: true},
			},
			Secrets: []Field{
				{Name: "accessKeyId", Label: "Access key id", Kind: FieldText, Required: true},
				{Name: "secretAccessKey", Label: "Secret access key", Kind: FieldPassword, Required: true},
			},
		},

		// SMS -----------------------------------------------------------------
		{
			Code: "aliyun_sms", Type: TypeSMS, Name: "Aliyun SMS",
			Description: "Send through Aliyun's SMS service. Uses a template rather than free text, which the provider requires.",
			Config: []Field{
				{Name: "signName", Label: "Sign name", Kind: FieldText, Required: true,
					Help: "The approved sender signature."},
				{Name: "templateCode", Label: "Template code", Kind: FieldText, Required: true},
				{Name: "region", Label: "Region", Kind: FieldText, Default: "cn-hangzhou"},
			},
			Secrets: []Field{
				{Name: "accessKeyId", Label: "Access key id", Kind: FieldText, Required: true},
				{Name: "accessKeySecret", Label: "Access key secret", Kind: FieldPassword, Required: true},
			},
		},
		{
			Code: "tencent_sms", Type: TypeSMS, Name: "Tencent Cloud SMS",
			Description: "Send through Tencent Cloud's SMS service.",
			Config: []Field{
				{Name: "sdkAppId", Label: "SDK app id", Kind: FieldText, Required: true},
				{Name: "signName", Label: "Sign name", Kind: FieldText, Required: true},
				{Name: "templateId", Label: "Template id", Kind: FieldText, Required: true},
				{Name: "region", Label: "Region", Kind: FieldText, Default: "ap-guangzhou"},
			},
			Secrets: []Field{
				{Name: "secretId", Label: "Secret id", Kind: FieldText, Required: true},
				{Name: "secretKey", Label: "Secret key", Kind: FieldPassword, Required: true},
			},
		},
		{
			Code: "twilio", Type: TypeSMS, Name: "Twilio",
			Description: "Send through Twilio.",
			Config: []Field{
				{Name: "fromNumber", Label: "From number", Kind: FieldText, Required: true,
					Help: "In E.164 form, for example +15005550006."},
			},
			Secrets: []Field{
				{Name: "accountSid", Label: "Account SID", Kind: FieldText, Required: true},
				{Name: "authToken", Label: "Auth token", Kind: FieldPassword, Required: true},
			},
		},

		// Storage -------------------------------------------------------------
		{
			Code: "local", Type: TypeStorage, Name: "Local disk",
			Description: "Keep uploaded files on this server. The default, and the only driver that needs no credentials.",
			Config: []Field{
				{Name: "directory", Label: "Directory", Kind: FieldText,
					Help: "Relative paths are resolved against the data directory."},
				{Name: "publicBaseUrl", Label: "Public base URL", Kind: FieldText,
					Help: "Where the files are reachable from, when they are served by something else."},
			},
		},
		{
			Code: "s3", Type: TypeStorage, Name: "S3-compatible",
			Description: "Store files in Amazon S3 or anything that speaks its API: MinIO, Ceph, Backblaze, Cloudflare R2.",
			Testable:    true,
			Config: []Field{
				{Name: "endpoint", Label: "Endpoint", Kind: FieldText, Required: true},
				{Name: "bucket", Label: "Bucket", Kind: FieldText, Required: true},
				{Name: "region", Label: "Region", Kind: FieldText, Default: "us-east-1"},
				{Name: "pathPrefix", Label: "Path prefix", Kind: FieldText},
				{Name: "publicBaseUrl", Label: "Public base URL", Kind: FieldText},
			},
			Secrets: []Field{
				{Name: "accessKeyId", Label: "Access key id", Kind: FieldText, Required: true},
				{Name: "secretAccessKey", Label: "Secret access key", Kind: FieldPassword, Required: true},
			},
		},
		{
			Code: "oss", Type: TypeStorage, Name: "Aliyun OSS",
			Description: "Store files in Aliyun Object Storage Service.",
			Config: []Field{
				{Name: "endpoint", Label: "Endpoint", Kind: FieldText, Required: true,
					Help: "For example oss-cn-hangzhou.aliyuncs.com."},
				{Name: "bucket", Label: "Bucket", Kind: FieldText, Required: true},
				{Name: "pathPrefix", Label: "Path prefix", Kind: FieldText},
			},
			Secrets: []Field{
				{Name: "accessKeyId", Label: "Access key id", Kind: FieldText, Required: true},
				{Name: "accessKeySecret", Label: "Access key secret", Kind: FieldPassword, Required: true},
			},
		},
		{
			Code: "cos", Type: TypeStorage, Name: "Tencent COS",
			Description: "Store files in Tencent Cloud Object Storage.",
			Config: []Field{
				{Name: "region", Label: "Region", Kind: FieldText, Required: true},
				{Name: "bucket", Label: "Bucket", Kind: FieldText, Required: true},
				{Name: "pathPrefix", Label: "Path prefix", Kind: FieldText},
			},
			Secrets: []Field{
				{Name: "secretId", Label: "Secret id", Kind: FieldText, Required: true},
				{Name: "secretKey", Label: "Secret key", Kind: FieldPassword, Required: true},
			},
		},

		// Identity ------------------------------------------------------------
		{
			Code: "oauth2", Type: TypeIDP, Name: "OAuth 2.0",
			Description: "Sign in through a provider that speaks OAuth 2.0.",
			Config: []Field{
				{Name: "authorizeUrl", Label: "Authorize URL", Kind: FieldText, Required: true},
				{Name: "tokenUrl", Label: "Token URL", Kind: FieldText, Required: true},
				{Name: "userInfoUrl", Label: "User info URL", Kind: FieldText, Required: true},
				{Name: "scopes", Label: "Scopes", Kind: FieldList, Default: "openid,email,profile"},
				{Name: "attributeMap", Label: "Attribute mapping", Kind: FieldText,
					Help: "Provider field to account field, for example email:email,name:displayName."},
			},
			Secrets: []Field{
				{Name: "clientId", Label: "Client id", Kind: FieldText, Required: true},
				{Name: "clientSecret", Label: "Client secret", Kind: FieldPassword, Required: true},
			},
		},
		{
			Code: "oidc", Type: TypeIDP, Name: "OpenID Connect",
			Description: "Sign in through a provider that publishes an OpenID Connect discovery document.",
			Config: []Field{
				{Name: "issuerUrl", Label: "Issuer URL", Kind: FieldText, Required: true,
					Help: "The discovery document is read from this address."},
				{Name: "scopes", Label: "Scopes", Kind: FieldList, Default: "openid,email,profile"},
				{Name: "attributeMap", Label: "Attribute mapping", Kind: FieldText},
			},
			Secrets: []Field{
				{Name: "clientId", Label: "Client id", Kind: FieldText, Required: true},
				{Name: "clientSecret", Label: "Client secret", Kind: FieldPassword, Required: true},
			},
		},
		{
			Code: "ldap", Type: TypeIDP, Name: "LDAP",
			Description: "Sign in against a directory server.",
			Testable:    true,
			Config: []Field{
				{Name: "host", Label: "Host", Kind: FieldText, Required: true},
				{Name: "port", Label: "Port", Kind: FieldNumber, Default: "389"},
				{Name: "useTLS", Label: "Use TLS", Kind: FieldBool},
				{Name: "baseDn", Label: "Base DN", Kind: FieldText, Required: true},
				{Name: "bindDn", Label: "Bind DN", Kind: FieldText, Required: true},
				{Name: "userFilter", Label: "User filter", Kind: FieldText,
					Default: "(uid={username})"},
				{Name: "attributeMap", Label: "Attribute mapping", Kind: FieldText},
			},
			Secrets: []Field{
				{Name: "bindPassword", Label: "Bind password", Kind: FieldPassword, Required: true},
			},
		},

		// Payment -------------------------------------------------------------
		{
			Code: "stripe", Type: TypePayment, Name: "Stripe",
			Description: "Take payments through Stripe. Webhook signatures are verified with the signing secret.",
			Testable:    true,
			Config: []Field{
				{Name: "successUrl", Label: "Success URL", Kind: FieldText},
				{Name: "cancelUrl", Label: "Cancel URL", Kind: FieldText},
			},
			Secrets: []Field{
				{Name: "secretKey", Label: "Secret key", Kind: FieldPassword, Required: true},
				{Name: "publishableKey", Label: "Publishable key", Kind: FieldText},
				{Name: "webhookSecret", Label: "Webhook signing secret", Kind: FieldPassword},
			},
		},
		{
			Code: "paypal", Type: TypePayment, Name: "PayPal",
			Description: "Take payments through PayPal.",
			Config: []Field{
				{Name: "successUrl", Label: "Success URL", Kind: FieldText},
				{Name: "cancelUrl", Label: "Cancel URL", Kind: FieldText},
			},
			Secrets: []Field{
				{Name: "clientId", Label: "Client id", Kind: FieldText, Required: true},
				{Name: "clientSecret", Label: "Client secret", Kind: FieldPassword, Required: true},
				{Name: "webhookId", Label: "Webhook id", Kind: FieldText},
			},
		},
		{
			Code: "alipay", Type: TypePayment, Name: "Alipay",
			Description: "Take payments through Alipay.",
			Config: []Field{
				{Name: "gateway", Label: "Gateway", Kind: FieldText, Default: "https://openapi.alipay.com/gateway.do"},
				{Name: "appId", Label: "App id", Kind: FieldText, Required: true},
				{Name: "notifyUrl", Label: "Notify URL", Kind: FieldText},
				{Name: "returnUrl", Label: "Return URL", Kind: FieldText},
			},
			Secrets: []Field{
				{Name: "privateKey", Label: "Application private key", Kind: FieldPassword, Required: true},
				{Name: "alipayPublicKey", Label: "Alipay public key", Kind: FieldPassword},
			},
		},
		{
			Code: "wechatpay", Type: TypePayment, Name: "WeChat Pay",
			Description: "Take payments through WeChat Pay.",
			Config: []Field{
				{Name: "mchId", Label: "Merchant id", Kind: FieldText, Required: true},
				{Name: "appId", Label: "App id", Kind: FieldText, Required: true},
				{Name: "notifyUrl", Label: "Notify URL", Kind: FieldText},
			},
			Secrets: []Field{
				{Name: "apiV3Key", Label: "API v3 key", Kind: FieldPassword, Required: true},
				{Name: "serialNo", Label: "Certificate serial number", Kind: FieldText, Required: true},
				{Name: "privateKey", Label: "Merchant private key", Kind: FieldPassword, Required: true},
			},
		},

		// Webhook -------------------------------------------------------------
		{
			Code: "http", Type: TypeWebhook, Name: "Outbound webhook",
			Description: "Post platform events to an HTTP endpoint.",
			Testable:    true,
			Config: []Field{
				{Name: "url", Label: "URL", Kind: FieldText, Required: true},
				{Name: "method", Label: "Method", Kind: FieldSelect, Default: "POST",
					Options: []string{"POST", "PUT", "PATCH"}},
				{Name: "events", Label: "Events", Kind: FieldList,
					Help: "Comma-separated. Empty sends everything."},
				{Name: "timeoutSeconds", Label: "Timeout", Kind: FieldNumber, Default: "10"},
			},
			Secrets: []Field{
				{Name: "signingSecret", Label: "Signing secret", Kind: FieldPassword,
					Help: "Sent as an HMAC signature header so the receiver can verify the payload."},
				{Name: "headerName", Label: "Custom header name", Kind: FieldText},
				{Name: "headerValue", Label: "Custom header value", Kind: FieldPassword},
			},
		},
	}
}

// AllDrivers returns every driver, ordered by type then code.
func AllDrivers() []Driver {
	out := drivers()
	sort.Slice(out, func(i, j int) bool {
		if out[i].Type != out[j].Type {
			return out[i].Type < out[j].Type
		}
		return out[i].Code < out[j].Code
	})
	return out
}

// LookupDriver returns a driver by code.
func LookupDriver(code string) (Driver, bool) {
	for _, d := range drivers() {
		if d.Code == code {
			return d, true
		}
	}
	return Driver{}, false
}

// DriversOfType returns the drivers registered for a type.
func DriversOfType(t Type) []Driver {
	var out []Driver
	for _, d := range drivers() {
		if d.Type == t {
			out = append(out, d)
		}
	}
	return out
}

// DriverCodes returns every driver code, for the stored select.
func DriverCodes() []string {
	out := make([]string, 0, len(drivers()))
	for _, d := range drivers() {
		out = append(out, d.Code)
	}
	sort.Strings(out)
	return out
}

// Types returns every provider type.
func Types() []string {
	return []string{string(TypeEmail), string(TypeSMS), string(TypeStorage),
		string(TypeIDP), string(TypePayment), string(TypeWebhook)}
}

// TypeLabel names a type for a screen.
func TypeLabel(t Type) string {
	switch t {
	case TypeEmail:
		return "Email"
	case TypeSMS:
		return "SMS"
	case TypeStorage:
		return "Object storage"
	case TypeIDP:
		return "Identity provider"
	case TypePayment:
		return "Payment"
	case TypeWebhook:
		return "Webhook"
	default:
		return string(t)
	}
}

// secretsByName indexes a driver's secret fields.
func (d Driver) secretsByName() map[string]Field {
	out := make(map[string]Field, len(d.Secrets))
	for _, f := range d.Secrets {
		out[f.Name] = f
	}
	return out
}

// configByName indexes a driver's configuration fields.
func (d Driver) configByName() map[string]Field {
	out := make(map[string]Field, len(d.Config))
	for _, f := range d.Config {
		out[f.Name] = f
	}
	return out
}

// Validate checks a configuration against the driver's declaration.
//
// It refuses an unknown field rather than storing it, so a driver cannot be handed
// configuration it does not understand and silently ignore it.
func (d Driver) Validate(config, secrets map[string]any) []string {
	var problems []string

	knownConfig := d.configByName()
	for name := range config {
		if _, ok := knownConfig[name]; !ok {
			problems = append(problems, "unknown setting: "+name)
		}
	}
	for _, field := range d.Config {
		if !field.Required {
			continue
		}
		if text, _ := config[field.Name].(string); strings.TrimSpace(text) == "" {
			problems = append(problems, field.Label+" is required")
		}
	}

	knownSecrets := d.secretsByName()
	for name := range secrets {
		if _, ok := knownSecrets[name]; !ok {
			problems = append(problems, "unknown credential: "+name)
		}
	}
	for _, field := range d.Secrets {
		if !field.Required {
			continue
		}
		if text, _ := secrets[field.Name].(string); strings.TrimSpace(text) == "" {
			problems = append(problems, field.Label+" is required")
		}
	}

	sort.Strings(problems)
	return problems
}
