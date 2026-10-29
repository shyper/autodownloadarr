// Command autodownloadarr runs the AutoDownloadarr server.
package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/shyper/autodownloadarr/internal/api"
	"github.com/shyper/autodownloadarr/internal/auth"
	"github.com/shyper/autodownloadarr/internal/buildinfo"
	"github.com/shyper/autodownloadarr/internal/config"
	"github.com/shyper/autodownloadarr/internal/db"
	"github.com/shyper/autodownloadarr/internal/events"
	"github.com/shyper/autodownloadarr/internal/movies"
	"github.com/shyper/autodownloadarr/internal/notify"
	"github.com/shyper/autodownloadarr/internal/scheduler"
	"github.com/shyper/autodownloadarr/internal/secrets"
	"github.com/shyper/autodownloadarr/internal/series"
	"github.com/shyper/autodownloadarr/internal/store"
	"github.com/shyper/autodownloadarr/internal/watchlist"
	"github.com/shyper/autodownloadarr/internal/webui"
)

const usage = `Usage: autodownloadarr [command]

Commands:
  serve                     run the server (default)
  healthcheck               exit 0 if the local server is healthy
  reset-password <user>     set a new password, read from stdin
  download-nzb <file.nzb>   download (and extract) an NZB with the built-in client
  version                   print the version

Configuration is read from AUTODOWNLOADARR_* environment variables.
`

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	cmd := "serve"
	if len(args) > 0 {
		cmd, args = args[0], args[1:]
	}
	switch cmd {
	case "serve":
		return serve()
	case "healthcheck":
		return healthcheck()
	case "reset-password":
		return resetPassword(args)
	case "download-nzb":
		return downloadNZB(args)
	case "version":
		fmt.Println(buildinfo.Version)
		return 0
	case "help", "-h", "--help":
		fmt.Print(usage)
		return 0
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n%s", cmd, usage)
		return 2
	}
}

func serve() int {
	cfg, err := config.Load(os.Getenv)
	if err != nil {
		fmt.Fprintln(os.Stderr, "invalid configuration:", err)
		return 2
	}
	log := newLogger(cfg)
	if err := runServer(cfg, log); err != nil {
		log.Error("server stopped with error", "err", err)
		return 1
	}
	return 0
}

func runServer(cfg config.Config, log *slog.Logger) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	log.Info("starting AutoDownloadarr", "version", buildinfo.Version, "config_dir", cfg.ConfigDir)
	if err := os.MkdirAll(cfg.ConfigDir, 0o750); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}
	sqldb, err := db.Open(ctx, cfg.DatabasePath())
	if err != nil {
		return err
	}
	defer func() { _ = sqldb.Close() }()

	key, err := secrets.LoadOrCreateKey(filepath.Join(cfg.ConfigDir, "secret.key"))
	if err != nil {
		return err
	}
	box, err := secrets.New(key)
	if err != nil {
		return err
	}
	st := store.New(sqldb, box)

	authSvc, err := auth.NewService(sqldb, auth.WithSessionTTL(cfg.SessionTTL))
	if err != nil {
		return err
	}
	limiter := auth.NewLimiter(10, 15*time.Minute)
	bus := events.New(log)
	notify.New(st, log).Start(ctx, bus)

	sched := scheduler.New(sqldb, log)
	sched.Every("purge-expired-sessions", time.Hour, authSvc.PurgeExpiredSessions)
	sched.Every("prune-login-limiter", 10*time.Minute, func(context.Context) error {
		limiter.Prune()
		return nil
	})

	svc := &movies.Services{Store: st, Log: log, TMDBBaseURL: cfg.TMDBURL}
	defer svc.Close()
	manager := movies.NewManager(st, svc, sched, bus, log)
	if err := manager.Start(ctx); err != nil {
		return fmt.Errorf("start movie manager: %w", err)
	}
	seriesManager := series.NewManager(st, svc, sched, bus, log, manager.Limiter())
	if err := seriesManager.Start(ctx); err != nil {
		return fmt.Errorf("start series manager: %w", err)
	}
	watchlistManager := watchlist.NewManager(st, svc, manager, seriesManager, log)
	sched.Every("plex-watchlist", time.Minute, watchlistManager.Poll)

	var setupToken string
	required, err := authSvc.SetupRequired(ctx)
	if err != nil {
		return err
	}
	if required {
		setupToken = auth.NewSetupToken()
		log.Warn("no account exists yet: open the web UI and enter this setup token to create the admin account",
			"setup_token", setupToken)
	}

	srv := &http.Server{
		Addr: cfg.ListenAddr,
		Handler: api.New(api.Options{
			Log:        log,
			DB:         sqldb,
			Auth:       authSvc,
			Limiter:    limiter,
			Bus:        bus,
			Store:      st,
			Services:   svc,
			Movies:     manager,
			Series:     seriesManager,
			SetupToken: setupToken,
			TrustProxy: cfg.TrustProxy,
			Static:     webui.Files(),
			Version:    buildinfo.Version,
			Done:       ctx.Done(),
		}),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       2 * time.Minute,
		ErrorLog:          slog.NewLogLogger(log.Handler(), slog.LevelWarn),
	}

	schedDone := make(chan error, 1)
	go func() { schedDone <- sched.Run(ctx) }()

	serveErr := make(chan error, 1)
	go func() {
		log.Info("listening", "addr", cfg.ListenAddr)
		serveErr <- srv.ListenAndServe()
	}()

	select {
	case err := <-serveErr:
		stop()
		if err := <-schedDone; err != nil {
			log.Warn("scheduler", "err", err)
		}
		return fmt.Errorf("http server: %w", err)
	case <-ctx.Done():
	}

	log.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Warn("http shutdown", "err", err)
	}
	if err := <-schedDone; err != nil {
		return err
	}
	log.Info("stopped")
	return nil
}

func newLogger(cfg config.Config) *slog.Logger {
	opts := &slog.HandlerOptions{Level: cfg.LogLevel}
	if cfg.LogFormat == "json" {
		return slog.New(slog.NewJSONHandler(os.Stdout, opts))
	}
	return slog.New(slog.NewTextHandler(os.Stdout, opts))
}

// healthcheck is used by the Docker HEALTHCHECK, which has no curl/wget.
func healthcheck() int {
	cfg, err := config.Load(os.Getenv)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+localAddr(cfg.ListenAddr)+"/api/v1/health", nil)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		fmt.Fprintln(os.Stderr, "unhealthy:", resp.Status)
		return 1
	}
	return 0
}

// localAddr turns a listen address into one we can connect to locally.
func localAddr(listen string) string {
	host, port, err := net.SplitHostPort(listen)
	if err != nil {
		return "127.0.0.1:8686"
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	return net.JoinHostPort(host, port)
}

func resetPassword(args []string) int {
	if len(args) != 1 {
		fmt.Fprint(os.Stderr, "usage: autodownloadarr reset-password <username>\nThe new password is read from stdin.\n")
		return 2
	}
	cfg, err := config.Load(os.Getenv)
	if err != nil {
		fmt.Fprintln(os.Stderr, "invalid configuration:", err)
		return 2
	}
	ctx := context.Background()
	sqldb, err := db.Open(ctx, cfg.DatabasePath())
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer func() { _ = sqldb.Close() }()
	svc, err := auth.NewService(sqldb)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	fmt.Fprint(os.Stderr, "New password: ")
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	password := strings.TrimRight(line, "\r\n")

	switch err := svc.ResetPassword(ctx, args[0], password); {
	case errors.Is(err, auth.ErrUserNotFound), errors.Is(err, auth.ErrWeakPassword):
		fmt.Fprintln(os.Stderr, err)
		return 1
	case err != nil:
		fmt.Fprintln(os.Stderr, "reset password:", err)
		return 1
	}
	fmt.Fprintln(os.Stderr, "Password updated. All sessions of this user have been signed out.")
	return 0
}
