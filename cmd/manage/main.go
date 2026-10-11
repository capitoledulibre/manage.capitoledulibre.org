// Command manage runs the management application of an event: partners,
// volunteers and the staff administration.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"
	_ "time/tzdata"

	"manage/internal/config"
	"manage/internal/export"
	"manage/internal/files"
	"manage/internal/mail"
	"manage/internal/server"
	"manage/internal/store"
)

func main() {
	// The database, its backups, exports and uploads hold personal data:
	// none of them for other users of the machine.
	syscall.Umask(0o077)

	cmd := "serve"
	if len(os.Args) > 1 {
		cmd = os.Args[1]
	}

	var err error

	switch cmd {
	case "serve":
		err = serve()
	case "export":
		err = exportCmd(os.Args[2:])
	case "backup":
		err = backupCmd(os.Args[2:])
	case "help", "-h", "--help":
		usage()
	default:
		usage()
		os.Exit(2)
	}

	if err != nil {
		slog.Error(cmd+" failed", "err", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `Usage:
  manage [serve]                        run the web server
  manage export [-event slug] [-o f]    write the partners JSON export
  manage backup [path]                  write a consistent copy of the
                                        database, safe while the server runs

Configuration is read from MANAGE_* environment variables (see README).`)
}

func open(ctx context.Context, cfg *config.Config) (*store.Store, error) {
	if err := os.MkdirAll(cfg.DataDir, 0o750); err != nil {
		return nil, err
	}

	return store.Open(ctx, filepath.Join(cfg.DataDir, "manage.db"))
}

// backupCmd snapshots the database without stopping the server, so it can
// run from cron while the event is live. It reads the database as it is: a
// wrong MANAGE_DATA_DIR fails instead of backing up a fresh empty one.
func backupCmd(args []string) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	path := fmt.Sprintf("manage-%s.db", time.Now().Format("20060102-150405"))
	if len(args) > 0 {
		path = args[0]
	}

	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("%s already exists, refusing to overwrite it", path)
	}

	ctx := context.Background()

	st, err := store.OpenExisting(filepath.Join(cfg.DataDir, "manage.db"))
	if err != nil {
		return err
	}
	defer st.Close()

	if err := st.BackupTo(ctx, path); err != nil {
		return err
	}

	info, err := os.Stat(path)
	if err != nil {
		return err
	}

	fmt.Printf("backup written to %s (%.1f MiB)\n", path, float64(info.Size())/(1<<20))

	return nil
}

func serve() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	if err := cfg.ValidateServe(); err != nil {
		return err
	}

	if cfg.Dev {
		slog.Warn("development mode enabled: do not use in production")
	}

	if cfg.Dev && !cfg.OIDCEnabled() && !cfg.DevAdminLogin() {
		slog.Warn("password-less staff login disabled: MANAGE_BASE_URL is not a loopback address")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	st, err := open(ctx, cfg)
	if err != nil {
		return err
	}
	defer st.Close()

	fs, err := files.New(filepath.Join(cfg.DataDir, "files"))
	if err != nil {
		return err
	}

	srv, err := server.New(ctx, cfg, st, fs, mail.New(cfg))
	if err != nil {
		return err
	}

	srv.Start(ctx)

	httpSrv := &http.Server{
		Addr:              cfg.Listen,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       2 * time.Minute,
		WriteTimeout:      2 * time.Minute,
		IdleTimeout:       2 * time.Minute,
	}

	// ListenAndServe returns as soon as a shutdown starts: wait for it to
	// finish the requests under way, then for the e-mails and jobs they
	// started, before the deferred Close takes the database away.
	shutdown := make(chan struct{})

	go func() {
		defer close(shutdown)

		<-ctx.Done()

		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		_ = httpSrv.Shutdown(shutdownCtx)
	}()

	slog.Info("listening", "addr", cfg.Listen, "base_url", cfg.BaseURL, "dev", cfg.Dev, "oidc", cfg.OIDCEnabled())

	if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}

	<-shutdown
	srv.Wait()
	slog.Info("stopped")

	return nil
}

func exportCmd(args []string) error {
	fl := flag.NewFlagSet("export", flag.ExitOnError)
	eventSlug := fl.String("event", "", "event slug (default: current event)")
	output := fl.String("o", "-", "output file, - for stdout")

	if err := fl.Parse(args); err != nil {
		return err
	}

	// The export only needs the base URL and data directory.
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	ctx := context.Background()

	st, err := store.OpenExisting(filepath.Join(cfg.DataDir, "manage.db"))
	if err != nil {
		return err
	}
	defer st.Close()

	var e *store.Event
	if *eventSlug != "" {
		e, err = st.GetEventBySlug(ctx, *eventSlug)
	} else {
		e, err = st.CurrentEvent(ctx)
	}

	if err != nil {
		return fmt.Errorf("event: %w", err)
	}

	doc, err := export.Build(ctx, st, cfg.BaseURL, e)
	if err != nil {
		return err
	}

	out := os.Stdout

	if *output != "-" {
		f, err := os.Create(*output)
		if err != nil {
			return err
		}
		defer f.Close()

		out = f
	}

	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")

	return enc.Encode(doc)
}
