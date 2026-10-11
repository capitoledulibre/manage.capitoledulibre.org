// Package config loads the application configuration from environment
// variables.
package config

import (
	"fmt"
	"net"
	"net/mail"
	"net/netip"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// DefaultSiteName is the event name used when MANAGE_SITE_NAME is unset.
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

	// VolunteerReminderHour is the local hour at which volunteers get the
	// recap of their slots of the next day.
	VolunteerReminderHour int
	// Gravatar looks missing profile pictures up on gravatar.com, from the
	// server. GravatarURL is the service (a test server in tests).
	Gravatar    bool
	GravatarURL string

	// TrustedProxies are the peers whose X-Forwarded-For header is believed,
	// to rate-limit login requests per client. Anyone else could write any
	// address in it.
	TrustedProxies []netip.Prefix
}

func Load() (*Config, error) {
	c := &Config{
		Listen:            env("MANAGE_LISTEN", ":8080"),
		BaseURL:           strings.TrimRight(env("MANAGE_BASE_URL", "http://localhost:8080"), "/"),
		DataDir:           env("MANAGE_DATA_DIR", "./data"),
		Dev:               envBool("MANAGE_DEV"),
		SMTPHost:          env("MANAGE_SMTP_HOST", ""),
		SMTPUser:          env("MANAGE_SMTP_USER", ""),
		SMTPPassword:      env("MANAGE_SMTP_PASSWORD", ""),
		SMTPTLS:           env("MANAGE_SMTP_TLS", "starttls"),
		SiteName:          env("MANAGE_SITE_NAME", DefaultSiteName),
		SiteURL:           strings.TrimRight(env("MANAGE_SITE_URL", "https://conf.example.org"), "/"),
		ContactEmail:      env("MANAGE_CONTACT_EMAIL", "partners@example.org"),
		SiteLogo:          env("MANAGE_SITE_LOGO", ""),
		StaffNotifyEmails: envList("MANAGE_STAFF_NOTIFY_EMAILS"),
		OIDCIssuer:        env("MANAGE_OIDC_ISSUER", ""),
		OIDCClientID:      env("MANAGE_OIDC_CLIENT_ID", ""),
		OIDCClientSecret:  env("MANAGE_OIDC_CLIENT_SECRET", ""),
		AdminEmails:       envList("MANAGE_ADMIN_EMAILS"),
		AdminGroup:        env("MANAGE_ADMIN_GROUP", ""),
		ExportToken:       env("MANAGE_EXPORT_TOKEN", ""),
		Gravatar:          env("MANAGE_GRAVATAR", "1") == "1",
	}

	// The sender defaults to the event name and contact address.
	c.MailFrom = env("MANAGE_MAIL_FROM", (&mail.Address{Name: c.SiteName, Address: c.ContactEmail}).String())

	port, err := strconv.Atoi(env("MANAGE_SMTP_PORT", "587"))
	if err != nil {
		return nil, fmt.Errorf("MANAGE_SMTP_PORT: %w", err)
	}
	c.SMTPPort = port

	c.ReminderInterval, err = time.ParseDuration(env("MANAGE_REMINDER_INTERVAL", "6h"))
	if err != nil {
		return nil, fmt.Errorf("MANAGE_REMINDER_INTERVAL: %w", err)
	}

	if c.TrustedProxies, err = ParseProxies(env("MANAGE_TRUSTED_PROXIES", "private")); err != nil {
		return nil, err
	}

	c.VolunteerReminderHour, err = strconv.Atoi(env("MANAGE_VOLUNTEER_REMINDER_HOUR", "18"))
	if err != nil || c.VolunteerReminderHour < 0 || c.VolunteerReminderHour > 23 {
		return nil, fmt.Errorf("MANAGE_VOLUNTEER_REMINDER_HOUR must be an hour from 0 to 23")
	}

	switch c.SMTPTLS {
	case "starttls", "tls", "none":
	default:
		return nil, fmt.Errorf("MANAGE_SMTP_TLS must be starttls, tls or none")
	}

	return c, nil
}

// privateNets are the loopback and private ranges: a reverse proxy on the
// same host or in the same Docker network connects from one of them, and a
// client on the Internet cannot.
var privateNets = []string{"127.0.0.0/8", "::1/128", "10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "fc00::/7"}

// ParseProxies reads MANAGE_TRUSTED_PROXIES: a comma-separated list of
// addresses or CIDR ranges, where "private" stands for the loopback and
// private ranges and "none" trusts nobody (the server faces the Internet
// directly).
func ParseProxies(v string) ([]netip.Prefix, error) {
	var out []netip.Prefix

	for item := range strings.SplitSeq(v, ",") {
		item = strings.TrimSpace(item)

		switch item {
		case "", "none":
			continue
		case "private":
			for _, n := range privateNets {
				out = append(out, netip.MustParsePrefix(n))
			}

			continue
		}

		if p, err := netip.ParsePrefix(item); err == nil {
			out = append(out, p.Masked())
		} else if a, err := netip.ParseAddr(item); err == nil {
			out = append(out, netip.PrefixFrom(a, a.BitLen()))
		} else {
			return nil, fmt.Errorf("invalid MANAGE_TRUSTED_PROXIES entry %q: expected an address, a CIDR range, \"private\" or \"none\"", item)
		}
	}

	return out, nil
}

// MinExportTokenLength is the minimum length of MANAGE_EXPORT_TOKEN, so it
// cannot be guessed.
const MinExportTokenLength = 32

// ValidateServe checks the settings only required by the web server.
func (c *Config) ValidateServe() error {
	if !c.Dev && !c.OIDCEnabled() {
		return fmt.Errorf("OIDC is not configured (MANAGE_OIDC_ISSUER, MANAGE_OIDC_CLIENT_ID, MANAGE_OIDC_CLIENT_SECRET); set MANAGE_DEV=1 for local development")
	}

	if c.ExportToken != "" && len(c.ExportToken) < MinExportTokenLength {
		return fmt.Errorf("MANAGE_EXPORT_TOKEN must be at least %d characters (e.g. openssl rand -hex 32)", MinExportTokenLength)
	}

	return nil
}

// DevAdminLogin reports whether the password-less staff login is available:
// only in development mode, without OIDC, and when the application is served
// on a loopback address, so a forgotten MANAGE_DEV cannot open the admin
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
