// Package config loads the application configuration from environment
// variables.
package config

import (
	"fmt"
	"net"
	"net/mail"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// DefaultSiteName is the event name used when PARTNERS_SITE_NAME is unset.
const DefaultSiteName = "Example Conf"

type Config struct {
	// Listen is the HTTP listen address, e.g. ":8080".
	Listen string
	// BaseURL is the public URL of the application, without trailing slash.
	// It is used to build magic links and logo URLs.
	BaseURL string
	// DataDir holds the SQLite database and uploaded files.
	DataDir string
	// SiteName is the event name displayed in pages and emails.
	SiteName string
	// SiteURL is the public website of the event, linked in the footer.
	SiteURL string
	// ContactEmail is shown to partners and used as the default sender.
	ContactEmail string
	// SiteLogo is an optional path to the event logo (SVG, PNG, JPEG or
	// WebP), displayed in the header and used as favicon.
	SiteLogo string

	// Dev enables development helpers: emails are printed to the log and a
	// password-less admin login is available when OIDC is not configured.
	Dev bool

	SMTPHost     string
	SMTPPort     int
	SMTPUser     string
	SMTPPassword string
	// SMTPTLS is "starttls" (default), "tls" (implicit TLS) or "none".
	SMTPTLS  string
	MailFrom string
	// StaffNotifyEmails receive a notification when a partner changes
	// something on their dashboard.
	StaffNotifyEmails []string

	OIDCIssuer       string
	OIDCClientID     string
	OIDCClientSecret string
	// AdminEmails and AdminGroup restrict who may log in as staff. A user is
	// allowed when their email is listed or when the groups claim contains
	// AdminGroup.
	AdminEmails []string
	AdminGroup  string

	// ExportToken protects the JSON export endpoint.
	ExportToken string

	// ReminderInterval is how often the reminder job runs.
	ReminderInterval time.Duration
}

func Load() (*Config, error) {
	c := &Config{
		Listen:            env("PARTNERS_LISTEN", ":8080"),
		BaseURL:           strings.TrimRight(env("PARTNERS_BASE_URL", "http://localhost:8080"), "/"),
		DataDir:           env("PARTNERS_DATA_DIR", "./data"),
		Dev:               envBool("PARTNERS_DEV"),
		SMTPHost:          env("PARTNERS_SMTP_HOST", ""),
		SMTPUser:          env("PARTNERS_SMTP_USER", ""),
		SMTPPassword:      env("PARTNERS_SMTP_PASSWORD", ""),
		SMTPTLS:           env("PARTNERS_SMTP_TLS", "starttls"),
		SiteName:          env("PARTNERS_SITE_NAME", DefaultSiteName),
		SiteURL:           strings.TrimRight(env("PARTNERS_SITE_URL", "https://conf.example.org"), "/"),
		ContactEmail:      env("PARTNERS_CONTACT_EMAIL", "partners@example.org"),
		SiteLogo:          env("PARTNERS_SITE_LOGO", ""),
		StaffNotifyEmails: envList("PARTNERS_STAFF_NOTIFY_EMAILS"),
		OIDCIssuer:        env("PARTNERS_OIDC_ISSUER", ""),
		OIDCClientID:      env("PARTNERS_OIDC_CLIENT_ID", ""),
		OIDCClientSecret:  env("PARTNERS_OIDC_CLIENT_SECRET", ""),
		AdminEmails:       envList("PARTNERS_ADMIN_EMAILS"),
		AdminGroup:        env("PARTNERS_ADMIN_GROUP", ""),
		ExportToken:       env("PARTNERS_EXPORT_TOKEN", ""),
	}

	// The sender defaults to the event name and contact address.
	c.MailFrom = env("PARTNERS_MAIL_FROM", (&mail.Address{Name: c.SiteName, Address: c.ContactEmail}).String())

	port, err := strconv.Atoi(env("PARTNERS_SMTP_PORT", "587"))
	if err != nil {
		return nil, fmt.Errorf("PARTNERS_SMTP_PORT: %w", err)
	}
	c.SMTPPort = port

	c.ReminderInterval, err = time.ParseDuration(env("PARTNERS_REMINDER_INTERVAL", "6h"))
	if err != nil {
		return nil, fmt.Errorf("PARTNERS_REMINDER_INTERVAL: %w", err)
	}

	switch c.SMTPTLS {
	case "starttls", "tls", "none":
	default:
		return nil, fmt.Errorf("PARTNERS_SMTP_TLS must be starttls, tls or none")
	}

	return c, nil
}

// ValidateServe checks the settings only required by the web server.
func (c *Config) ValidateServe() error {
	if !c.Dev && !c.OIDCEnabled() {
		return fmt.Errorf("OIDC is not configured (PARTNERS_OIDC_ISSUER, PARTNERS_OIDC_CLIENT_ID, PARTNERS_OIDC_CLIENT_SECRET); set PARTNERS_DEV=1 for local development")
	}

	return nil
}

// DevAdminLogin reports whether the password-less staff login is available:
// only in development mode, without OIDC, and when the application is served
// on a loopback address, so a forgotten PARTNERS_DEV cannot open the admin
// area on a public server.
func (c *Config) DevAdminLogin() bool {
	if !c.Dev || c.OIDCEnabled() {
		return false
	}

	u, err := url.Parse(c.BaseURL)
	if err != nil {
		return false
	}

	host := u.Hostname()
	if host == "localhost" {
		return true
	}

	ip := net.ParseIP(host)

	return ip != nil && ip.IsLoopback()
}

func (c *Config) OIDCEnabled() bool {
	return c.OIDCIssuer != "" && c.OIDCClientID != "" && c.OIDCClientSecret != ""
}

// SecureCookies reports whether cookies should carry the Secure flag.
func (c *Config) SecureCookies() bool {
	return strings.HasPrefix(c.BaseURL, "https://")
}

func env(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}

	return def
}

func envBool(key string) bool {
	v, _ := strconv.ParseBool(os.Getenv(key))

	return v
}

func envList(key string) []string {
	var out []string

	for _, s := range strings.Split(os.Getenv(key), ",") {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, strings.ToLower(s))
		}
	}

	return out
}
