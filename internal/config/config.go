// Package config loads runtime settings from the environment (and an optional .env file).
package config

import (
	"bufio"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config is the runtime configuration of the application.
type Config struct {
	Addr         string         // listen address, e.g. ":8080"
	DBPath       string         // path of the SQLite file
	UploadDir    string         // directory for uploaded images
	BaseURL      string         // public URL, used in e-mails, no trailing slash
	Timezone     *time.Location // display timezone
	Secure       bool           // set Secure flag on cookies (behind HTTPS)
	DevMode      bool           // print magic links to stdout instead of e-mailing
	ReminderHour int            // local hour at which the daily reminder is sent
	Gravatar     bool           // look missing profile pictures up on gravatar.com

	// Staff (site-wide administrators) log in through OpenID Connect. Only
	// the addresses in AdminEmails, or the members of the AdminGroup claim,
	// are let in.
	OIDCIssuer       string
	OIDCClientID     string
	OIDCClientSecret string
	AdminEmails      []string
	AdminGroup       string

	// TrustedProxies are the peers whose X-Forwarded-For header is believed.
	// Anyone else could write any address in it and dodge the rate limits.
	TrustedProxies []netip.Prefix

	SMTPHost string
	SMTPPort int
	SMTPUser string
	SMTPPass string
	MailFrom string
}

// Load reads .env (if present) then the environment.
// Use CheckMail before serving to make sure e-mails can go out.
func Load() (Config, error) {
	loadDotEnv(".env")
	c := Config{
		Addr:         env("ADDR", ":8080"),
		DBPath:       env("DB_PATH", "benevoles.db"),
		UploadDir:    env("UPLOAD_DIR", "uploads"),
		BaseURL:      strings.TrimRight(env("BASE_URL", "http://localhost:8080"), "/"),
		Secure:       env("COOKIE_SECURE", "") == "1",
		DevMode:      env("DEV_MODE", "") == "1",
		ReminderHour: envInt("REMINDER_HOUR", 18),
		Gravatar:     env("GRAVATAR", "1") == "1",
		SMTPHost:     env("SMTP_HOST", ""),
		SMTPPort:     envInt("SMTP_PORT", 587),
		SMTPUser:     env("SMTP_USER", ""),
		SMTPPass:     env("SMTP_PASS", ""),
		MailFrom:     env("MAIL_FROM", "benevoles@example.org"),

		OIDCIssuer:       env("OIDC_ISSUER", ""),
		OIDCClientID:     env("OIDC_CLIENT_ID", ""),
		OIDCClientSecret: env("OIDC_CLIENT_SECRET", ""),
		AdminEmails:      envList("ADMIN_EMAILS"),
		AdminGroup:       env("ADMIN_GROUP", ""),
	}
	proxies, err := parseProxies(env("TRUSTED_PROXIES", "private"))
	if err != nil {
		return c, err
	}
	c.TrustedProxies = proxies
	tz := env("TIMEZONE", "Europe/Paris")
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return c, fmt.Errorf("invalid TIMEZONE %q: %w", tz, err)
	}
	c.Timezone = loc
	return c, nil
}

// privateNets are the loopback and private ranges: a reverse proxy on the
// same host or in the same Docker network connects from one of them, and a
// client on the Internet cannot.
var privateNets = []string{"127.0.0.0/8", "::1/128", "10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "fc00::/7"}

// parseProxies reads TRUSTED_PROXIES: a comma-separated list of addresses or
// CIDR ranges, where "private" stands for the loopback and private ranges and
// "none" trusts nobody (the server faces the Internet directly).
func parseProxies(v string) ([]netip.Prefix, error) {
	var out []netip.Prefix
	for _, item := range strings.Split(v, ",") {
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
			return nil, fmt.Errorf("invalid TRUSTED_PROXIES entry %q: expected an address, a CIDR range, \"private\" or \"none\"", item)
		}
	}
	return out, nil
}

// OIDCEnabled tells whether the staff login through OpenID Connect is set up.
func (c Config) OIDCEnabled() bool {
	return c.OIDCIssuer != "" && c.OIDCClientID != "" && c.OIDCClientSecret != ""
}

// DevAdminLogin tells whether the password-less staff login is available:
// only in development mode, without OIDC, and when the application is served
// on a loopback address, so that a DEV_MODE forgotten on a public server
// cannot open the administration to anyone.
func (c Config) DevAdminLogin() bool {
	if !c.DevMode || c.OIDCEnabled() {
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

// CheckAdminLogin fails when nobody could log in as staff.
func (c Config) CheckAdminLogin() error {
	if c.OIDCEnabled() {
		if len(c.AdminEmails) == 0 && c.AdminGroup == "" {
			return fmt.Errorf("OIDC is configured but nobody is allowed in: set ADMIN_EMAILS or ADMIN_GROUP")
		}
		return nil
	}
	if c.DevMode {
		return nil
	}
	return fmt.Errorf("the staff login needs OIDC (OIDC_ISSUER, OIDC_CLIENT_ID, OIDC_CLIENT_SECRET); set DEV_MODE=1 for local development")
}

func env(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return def
}

// envList reads a comma-separated list, lower-cased (it holds e-mail
// addresses).
func envList(key string) []string {
	var out []string
	for _, v := range strings.Split(os.Getenv(key), ",") {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, strings.ToLower(v))
		}
	}
	return out
}

func envInt(key string, def int) int {
	v, ok := os.LookupEnv(key)
	if !ok || v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}

// loadDotEnv sets variables from a KEY=VALUE file without overriding the
// real environment. Missing file is fine.
func loadDotEnv(path string) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k = strings.TrimSpace(k)
		v = strings.Trim(strings.TrimSpace(v), `"'`)
		if _, exists := os.LookupEnv(k); !exists {
			os.Setenv(k, v)
		}
	}
}

// CheckMail fails when no way of delivering e-mails is configured.
func (c Config) CheckMail() error {
	if c.SMTPHost == "" && !c.DevMode {
		return fmt.Errorf("SMTP_HOST is not set: configure SMTP or set DEV_MODE=1 to print login links on stdout")
	}
	return nil
}
