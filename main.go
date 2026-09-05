// Command benevoles runs the volunteer management web application.
//
// Usage:
//
//	benevoles serve                 start the HTTP server (default)
//	benevoles make-admin EMAIL      grant administrator rights to an account
//	benevoles remind [YYYY-MM-DD]   send the reminder e-mails for a day (default: tomorrow)
//	benevoles backup [PATH]         write a consistent copy of the database
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"benevoles/internal/config"
	"benevoles/internal/db"
	"benevoles/internal/mail"
	"benevoles/internal/reminders"
	"benevoles/internal/web"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatal(err)
	}
	database, err := db.Open(cfg.DBPath)
	if err != nil {
		log.Fatalf("open database: %v", err)
	}
	defer database.Close()
	if err := database.Migrate(); err != nil {
		log.Fatalf("migrate: %v", err)
	}
	mailer := &mail.Mailer{Host: cfg.SMTPHost, Port: cfg.SMTPPort, User: cfg.SMTPUser, Pass: cfg.SMTPPass, From: cfg.MailFrom, Dev: cfg.DevMode}
	sender := &reminders.Sender{DB: database, Mailer: mailer, Loc: cfg.Timezone, BaseURL: cfg.BaseURL, OrgName: database.GetSetting("org_name", "Bénévoles")}

	cmd := "serve"
	if len(os.Args) > 1 {
		cmd = os.Args[1]
	}
	switch cmd {
	case "serve":
		if err := cfg.CheckMail(); err != nil {
			log.Fatal(err)
		}
		srv, err := web.New(database, cfg, mailer)
		if err != nil {
			log.Fatal(err)
		}
		if n, _ := database.CountAdmins(); n == 0 {
			log.Printf("no administrator yet: log in once, then run `benevoles make-admin you@example.org`")
		}
		go sender.RunDaily(cfg.ReminderHour)
		server := &http.Server{
			Addr: cfg.Addr, Handler: srv.Handler(),
			ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 30 * time.Second,
			WriteTimeout: 60 * time.Second, IdleTimeout: 120 * time.Second,
		}
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		go func() {
			log.Printf("listening on %s (%s)", cfg.Addr, cfg.BaseURL)
			if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				log.Fatal(err)
			}
		}()
		<-ctx.Done()
		log.Print("shutting down")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			log.Printf("shutdown: %v", err)
		}
		// Notifications are sent off the request goroutine; give them a chance
		// to reach the SMTP server before the process exits.
		srv.WaitMail()

	case "make-admin":
		if len(os.Args) < 3 {
			log.Fatal("usage: benevoles make-admin EMAIL")
		}
		u, err := database.GetOrCreateUser(os.Args[2])
		if err != nil {
			log.Fatal(err)
		}
		if err := database.SetAdmin(u.ID, true); err != nil {
			log.Fatal(err)
		}
		fmt.Printf("%s is now an administrator\n", u.Email)

	case "remind":
		day := time.Now().In(cfg.Timezone).Add(24 * time.Hour)
		if len(os.Args) > 2 {
			day, err = time.ParseInLocation("2006-01-02", os.Args[2], cfg.Timezone)
			if err != nil {
				log.Fatal(err)
			}
		}
		n, err := sender.SendForDay(day)
		if err != nil {
			log.Fatal(err)
		}
		fmt.Printf("%d reminder(s) sent\n", n)

	case "backup":
		// VACUUM INTO snapshots the database without stopping the server, so
		// this can run from cron while the event is live.
		path := fmt.Sprintf("benevoles-%s.db", time.Now().Format("20060102-150405"))
		if len(os.Args) > 2 {
			path = os.Args[2]
		}
		if _, err := os.Stat(path); err == nil {
			log.Fatalf("%s already exists, refusing to overwrite it", path)
		}
		if err := database.BackupTo(path); err != nil {
			log.Fatal(err)
		}
		info, err := os.Stat(path)
		if err != nil {
			log.Fatal(err)
		}
		fmt.Printf("backup written to %s (%.1f MiB)\n", path, float64(info.Size())/(1024*1024))

	default:
		log.Fatalf("unknown command %q (serve, make-admin, remind, backup)", cmd)
	}
}
