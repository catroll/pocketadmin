package settings

// adminKeys declares the back-office surface.
//
// Every key carries a default, and the default is the documentation: a reader
// should not have to find the code that consumes a setting to learn what happens
// when nobody has set it.
func adminKeys() []Key {
	return []Key{
		// console ------------------------------------------------------------
		{Category: "console", Key: "consoleName", Type: TypeString, Default: "PocketAdmin",
			Description: "Brand name shown in the header, the page titles and the sign-in page."},
		{Category: "console", Key: "logo", Type: TypeFile, Default: "",
			Description: "Logo shown in the sidebar. Upload it through the file library."},
		{Category: "console", Key: "favicon", Type: TypeFile, Default: "",
			Description: "Browser tab icon."},
		{Category: "console", Key: "accentColor", Type: TypeString, Default: "#0d6efd",
			Description: "Accent colour used by buttons and links.",
			Pattern:     `^#[0-9a-fA-F]{6}$`},
		{Category: "console", Key: "defaultTheme", Type: TypeSelect, Default: "light",
			Description: "Theme a new account starts with.",
			Options:     []string{"light", "dark", "auto"}},
		{Category: "console", Key: "defaultLanguage", Type: TypeSelect, Default: "en",
			Description: "Language a new account starts with.",
			Options:     []string{"en", "zh-CN"}},
		{Category: "console", Key: "defaultPageSize", Type: TypeNumber, Default: float64(30),
			Description: "Rows per page a new account starts with.",
			Min:         ptr(5), Max: ptr(500)},
		{Category: "console", Key: "footerText", Type: TypeString, Default: "",
			Description: "Text shown in the panel footer beside the version."},

		// security -----------------------------------------------------------
		{Category: "security", Key: "passwordMinLength", Type: TypeNumber, Default: float64(8),
			Description: "Shortest accepted password.",
			Min:         ptr(6), Max: ptr(128)},
		{Category: "security", Key: "passwordRequireClasses", Type: TypeNumber, Default: float64(1),
			Description: "How many of upper, lower, digit and symbol a password must contain.",
			Min:         ptr(0), Max: ptr(4)},
		{Category: "security", Key: "passwordExpiryDays", Type: TypeNumber, Default: float64(0),
			Description: "Days before a password expires. Zero never expires it.",
			Min:         ptr(0), Max: ptr(3650)},
		{Category: "security", Key: "sessionTimeoutMinutes", Type: TypeNumber, Default: float64(480),
			Description: "How long a session stays valid.",
			Min:         ptr(5), Max: ptr(10080)},
		{Category: "security", Key: "idleTimeoutMinutes", Type: TypeNumber, Default: float64(0),
			Description: "Sign out after this many minutes of inactivity. Zero disables it.",
			Min:         ptr(0), Max: ptr(1440)},
		{Category: "security", Key: "maxLoginAttempts", Type: TypeNumber, Default: float64(8),
			Description: "Consecutive failures before an account locks.",
			Min:         ptr(1), Max: ptr(100)},
		{Category: "security", Key: "lockoutMinutes", Type: TypeNumber, Default: float64(15),
			Description: "How long a lockout lasts.",
			Min:         ptr(1), Max: ptr(10080)},
		{Category: "security", Key: "captchaEnabled", Type: TypeBool, Default: false,
			Description: "Require a challenge on the sign-in form. No provider is wired up yet."},
		{Category: "security", Key: "mfaRequired", Type: TypeBool, Default: false,
			Description: "Require a second factor for every administrator."},
		{Category: "security", Key: "ipAllowList", Type: TypeJSON, Default: []any{},
			Description: "Addresses or CIDR ranges allowed to reach the panel. Empty allows all."},

		// notification -------------------------------------------------------
		{Category: "notification", Key: "notifyOnJobFailure", Type: TypeBool, Default: true,
			Description: "Notify administrators when a scheduled task fails."},
		{Category: "notification", Key: "notifyOnSecurityEvent", Type: TypeBool, Default: true,
			Description: "Notify administrators when a critical security event is recorded."},
		{Category: "notification", Key: "notifyOnNewOrder", Type: TypeBool, Default: false,
			Description: "Notify administrators when an order is placed."},
		{Category: "notification", Key: "notifyOnRefundRequest", Type: TypeBool, Default: true,
			Description: "Notify administrators when a refund is requested."},
		{Category: "notification", Key: "defaultChannels", Type: TypeJSON, Default: []any{},
			Description: "Channels used when an event does not name its own."},
		{Category: "notification", Key: "digestFrequency", Type: TypeSelect, Default: "off",
			Description: "How often a summary is sent.",
			Options:     []string{"off", "daily", "weekly"}},

		// storage ------------------------------------------------------------
		//
		// These are not in the seeded key table in database-design.md, which is a
		// gap rather than a decision: features.md gives the file library an upload
		// policy, and a policy has to be configured somewhere. Recorded in
		// docs/decisions.md D-019.
		{Category: "storage", Key: "provider", Type: TypeString, Default: "local",
			Description: "Which integration provider holds new uploads. Existing files keep the provider they were stored with."},
		{Category: "storage", Key: "uploadMaxSize", Type: TypeNumber, Default: float64(33554432),
			Description: "Largest accepted upload, in bytes. The file library refuses anything larger before writing it.",
			Min:         ptr(1024), Max: ptr(1073741824)},
		{Category: "storage", Key: "uploadAllowedExtensions", Type: TypeJSON,
			Default: []any{".jpg", ".jpeg", ".png", ".gif", ".webp", ".svg",
				".pdf", ".txt", ".md", ".csv",
				".doc", ".docx", ".xls", ".xlsx", ".ppt", ".pptx", ".zip", ".gz"},
			Description: "Permitted file types, matched against the type sniffed from the content rather than the name supplied."},
		{Category: "storage", Key: "imageMaxWidth", Type: TypeNumber, Default: float64(0),
			Description: "Reject an image wider than this. Zero disables the check.",
			Min:         ptr(0), Max: ptr(100000)},
		{Category: "storage", Key: "imageMaxHeight", Type: TypeNumber, Default: float64(0),
			Description: "Reject an image taller than this. Zero disables the check.",
			Min:         ptr(0), Max: ptr(100000)},

		// audit --------------------------------------------------------------
		//
		// The retention periods features.md requires. Also absent from the seeded
		// key table; also recorded in D-019.
		{Category: "audit", Key: "operationLogRetentionDays", Type: TypeNumber, Default: float64(90),
			Description: "How long operation log entries are kept. Zero keeps them forever.",
			Min:         ptr(0), Max: ptr(3650)},
		{Category: "audit", Key: "loginLogRetentionDays", Type: TypeNumber, Default: float64(180),
			Description: "How long sign-in history is kept. Zero keeps it forever.",
			Min:         ptr(0), Max: ptr(3650)},
		{Category: "audit", Key: "securityLogRetentionDays", Type: TypeNumber, Default: float64(0),
			Description: "How long security events are kept. Zero, the default, keeps them forever -- these are the entries most likely to be needed long after the fact.",
			Min:         ptr(0), Max: ptr(3650)},
		{Category: "audit", Key: "exceptionLogRetentionDays", Type: TypeNumber, Default: float64(30),
			Description: "How long exceptions are kept. Zero keeps them forever.",
			Min:         ptr(0), Max: ptr(3650)},

		// banner -------------------------------------------------------------
		{Category: "banner", Key: "enabled", Type: TypeBool, Default: false,
			Description: "Show an announcement banner above the panel content."},
		{Category: "banner", Key: "message", Type: TypeString, Default: "",
			Description: "Text of the banner."},
		{Category: "banner", Key: "level", Type: TypeSelect, Default: "info",
			Description: "Severity of the banner, which selects its colour.",
			Options:     []string{"info", "success", "warning", "danger"}},
		{Category: "banner", Key: "dismissible", Type: TypeBool, Default: true,
			Description: "Whether a reader may hide the banner."},

		// refunds ------------------------------------------------------------
		{Category: "refund", Key: "autoApproveThreshold", Type: TypeNumber, Default: float64(0),
			Description: "A refund at or below this amount, in minor units, is approved as soon as it is requested. " +
				"Zero disables automatic approval, which is the default: a shop that has not thought about it should " +
				"have a person look. A refund is never auto-approved for the person who requested it, whatever this is set to."},
		{Category: "refund", Key: "restockOnFullRefund", Type: TypeBool, Default: true,
			Description: "Return the goods of a fully refunded order to stock. Only applies to a refund by item: " +
				"a refund by amount says nothing about which goods came back."},
	}

}

// ptr returns a pointer to a float, for the Min and Max bounds.
func ptr(v float64) *float64 { return &v }
