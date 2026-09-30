// Package config loads runtime settings from the environment (and an optional .env file).
package config

import (
	"bufio"
	"fmt"
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
	}
	tz := env("TIMEZONE", "Europe/Paris")
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return c, fmt.Errorf("invalid TIMEZONE %q: %w", tz, err)
	}
	c.Timezone = loc
	return c, nil
}

func env(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return def
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
