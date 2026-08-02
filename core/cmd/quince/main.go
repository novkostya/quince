// Command quince is the core daemon. Subcommands:
//
//	quince serve [--demo|--public-demo] [--listen :8080]  # serve the UI + API (contracts.md)
//	quince backup <udid> [--transport usb|wifi|auto]   # drive one backup to completion (lab CLI)
//	quince versions verify <id> | --udid <udid>        # re-run structural verification (qn.4b)
//	quince device reset-working <udid>                 # discard the dirty working/ (qn.5b Reset)
//	quince config validate [path]                      # validate config.yml; nonzero exit on error
//	quince version                                     # print the build version
//
// Bootstrap config is env-only (contracts.md §6: QUINCE_DATA/CACHE/BACKUPS/LISTEN);
// everything else lives in /data/config.yml, read at startup.
package main

import (
	"context"
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

	"github.com/novkostya/quince/core/internal/auth"
	"github.com/novkostya/quince/core/internal/bus"
	"github.com/novkostya/quince/core/internal/config"
	"github.com/novkostya/quince/core/internal/demo"
	"github.com/novkostya/quince/core/internal/httpapi"
	"github.com/novkostya/quince/core/internal/store"
	"github.com/novkostya/quince/core/internal/version"
	"github.com/novkostya/quince/core/internal/webui"
)

// lockdownSystemDir is where libimobiledevice reads/writes pairing records on Linux; qn.3
// persists its contents under $QUINCE_DATA so they survive a container recreate (amendment 1).
const lockdownSystemDir = "/var/lib/lockdown"

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "quince: "+err.Error())
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		usage()
		return errors.New("a subcommand is required")
	}
	switch args[0] {
	case "serve":
		return serve(args[1:])
	case "backup":
		return backupCmd(args[1:])
	case "versions":
		return versionsCmd(args[1:])
	case "device":
		return deviceCmd(args[1:])
	case "config":
		return configCmd(args[1:])
	case "version":
		fmt.Println(version.String())
		return nil
	case "-h", "--help", "help":
		usage()
		return nil
	default:
		usage()
		return fmt.Errorf("unknown subcommand %q", args[0])
	}
}

func usage() {
	fmt.Fprintf(os.Stderr, "quince %s\n\nUsage:\n"+
		"  quince serve [--demo|--public-demo] [--listen :8080]  serve the UI + API\n"+
		"  quince backup <udid> [--transport usb|wifi|auto]   drive one backup to completion\n"+
		"  quince versions verify <id> | --udid <udid>        re-run structural verification\n"+
		"  quince device reset-working <udid>                 discard the dirty working/\n"+
		"  quince config validate [path]                      validate config.yml\n"+
		"  quince version                                     print version\n", version.String())
}

func serve(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	demoFlag := fs.Bool("demo", false, "serve in-memory fixture data (no hardware)")
	publicDemo := fs.Bool("public-demo", false, "as --demo, for a public instance: password preset, Secure cookies left alone")
	listenFlag := fs.String("listen", "", "override listen address (else QUINCE_LISTEN)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	// --public-demo IMPLIES the --demo fixture stack and differs in exactly two behaviours, both
	// isolated in configureDemoAuth (spec D1). Everything downstream keys off demoMode, so the
	// fixture wiring is shared verbatim rather than duplicated — which is what keeps "otherwise
	// identical to --demo" true by construction instead of by discipline.
	demoMode := *demoFlag || *publicDemo

	log := newLogger()

	bootstrap, bwarn := config.LoadBootstrap(os.Environ())
	for _, w := range bwarn {
		log.Warn("bootstrap warning", "path", w.Path, "message", w.Message)
	}
	for _, w := range config.ValidateDirs(bootstrap) {
		log.Warn("startup dir check", "path", w.Path, "message", w.Message)
	}

	listen := bootstrap.Listen
	if *listenFlag != "" {
		listen = *listenFlag
	}

	dbPath := bootstrap.DBPath()
	cfgPath := bootstrap.ConfigPath()
	var cleanup func()
	if demoMode {
		// Fresh throwaway state each run so the first-run set-password flow is exercised
		// (rung-ruled reading of "--demo seeds password demo": demo starts at needs_setup;
		// the canonical demo password is "demo", entered at setup).
		dbPath = filepath.Join(bootstrap.Cache, "demo.db")
		cfgPath = filepath.Join(bootstrap.Cache, "demo-config.yml")
		removeDemoState(dbPath, cfgPath)
		cleanup = func() { removeDemoState(dbPath, cfgPath) }
	}

	st, err := store.Open(dbPath)
	if err != nil {
		return fmt.Errorf("open db %s: %w", dbPath, err)
	}
	defer func() {
		_ = st.Close()
		if cleanup != nil {
			cleanup()
		}
	}()

	cfgSvc := config.NewService(cfgPath, log)
	authSvc := auth.NewService(st, log)
	eventBus := bus.New()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// devices is assigned in both branches below; jobs/versions stay Empty until qn.4/qn.5.
	// muxer defaults to Unmanaged (external / --demo): quince owns no muxer to restart, so
	// rescan → 409. The managed supervisor is wired in the non-demo branch when configured.
	var devices httpapi.DeviceReader
	var jobs httpapi.JobReader         // assigned in both branches (demo → provider, else → engine)
	var jobControl httpapi.JobControl  // nil in demo → router serves 503 on the command surface
	var versions httpapi.VersionReader // assigned in both branches (demo → provider, else → storage)
	var versionAdmin httpapi.VersionAdmin
	var storages httpapi.StorageReader // assigned in both branches (demo → provider, else → storage)
	var muxer httpapi.MuxerControl = httpapi.UnmanagedMuxer{}
	var ops httpapi.DeviceOps             // assigned in both branches below (demo → provider, else → manager)
	var workingReset httpapi.WorkingReset // nil in demo → router serves 503 on the reset surface
	if demoMode {
		// configureDemoAuth owns the mode banner too, so this branch has NO `if *publicDemo` in it.
		// A second divergence point here would erode what the shared branch buys — see its doc.
		if err := configureDemoAuth(authSvc, log, *publicDemo); err != nil {
			return err
		}
		prov := demo.NewProvider(eventBus, log)
		prov.Run(ctx)
		devices, jobs, versions, ops = prov, prov, prov, prov
		storages = prov
		versionAdmin = prov
		jobControl = prov // qn.4b: the demo command surface is live (scripts on-demand jobs, no hardware)
	} else {
		// qn.6c gap 3: refuse to serve with no declared storage. Deliberately INSIDE this branch
		// rather than above it — `--demo` serves fixture data and never touches storage (it takes
		// the branch above, which does not call buildLiveStack), so a check placed before the
		// branch would refuse every demo and every ui-e2e run over a subsystem they do not use.
		// Placed before buildLiveStack so nothing is probed or reconciled on a config that cannot
		// serve.
		if req := config.CheckStorages(cfgSvc.Current(), os.Environ()); !req.OK() {
			return req.Explain(os.Stderr, cfgPath)
		}
		// TWO STORAGES THAT ARE ONE STORAGE cannot be served (quince#458). A zfs collision is
		// not a degraded mode to surface — every per-storage guarantee this rung added is void
		// beneath it, so it joins the one class of hard refusal rather than becoming a warning.
		if bad := config.CheckStorageBackends(cfgSvc.Current().Storage); len(bad) > 0 {
			for _, m := range bad {
				fmt.Fprintf(os.Stderr, "quince: %s\n", m)
			}
			return fmt.Errorf("storage configuration in %s cannot be served", cfgPath)
		}
		// The live stack (qn.2 registry + qn.2b muxer supervision + qn.3 device ops + qn.5
		// storage + qn.4a backup engine), with startup reconciliation run in-order BEFORE serving
		// (storage → job rows). Shared verbatim with the `backup` CLI.
		ls, err := buildLiveStack(ctx, bootstrap, cfgSvc, st, eventBus, log)
		if err != nil {
			return err
		}
		devices, jobs, jobControl = ls.devices, ls.jobs, ls.jobControl
		versions, versionAdmin, muxer, ops = ls.versions, ls.versionAdmin, ls.muxer, ls.ops
		storages = ls.storages
		if ls.engine != nil { // the engine holds per-UDID single-flight, so it owns Reset (qn.5b)
			workingReset = ls.engine
		}
	}

	srv := newHTTPServer(listen, httpapi.NewRouter(httpapi.Deps{
		Log: log, Version: version.String(), Config: cfgSvc, Auth: authSvc, Bus: eventBus,
		Devices: devices, Jobs: jobs, JobControl: jobControl, Versions: versions,
		VersionAdmin: versionAdmin, Muxer: muxer, Ops: ops, WorkingReset: workingReset,
		Storages: storages,
	}))

	errCh := make(chan error, 1)
	go func() {
		log.Info("quince serving",
			"version", version.String(), "listen", listen,
			"ui_embedded", webui.Built(), "demo", demoMode, "public_demo", *publicDemo)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		return fmt.Errorf("http server: %w", err)
	case <-ctx.Done():
		log.Info("shutdown signal received, draining")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
	}
}

// demoPassword is the canonical demo password, ruled on quince#444, 2026-08-01. Under `--demo` it
// is what the operator types at first-run setup; under `--public-demo` it is preset at startup and
// printed on the login screen. It is PUBLISHED BY RULING and therefore not a secret — `test`
// remains the fixture password, unrelated and unchanged.
const demoPassword = "demo"

// configureDemoAuth applies the only two behaviours that separate `--public-demo` from `--demo`
// (docs/specs/public-demo/public-demo.md, D1). Split out because they are the whole difference
// between the modes, and a difference that lives inline in a 100-line function is one nothing can
// assert.
//
// PUBLIC: preset the password, and leave the Secure flag alone.
//
//   - Presetting IS what makes it immutable (D3), with no new refusal needed: `SetPassword` has
//     exactly one caller, `POST /api/auth/setup`, which 409s once a password exists, and there is
//     no change-password endpoint. So the instance starts at `needs_login` and setup is already
//     closed.
//   - NOT calling `SetInsecureCookies` is the point of the mode existing. `auth/service.go` says
//     of that flag: *"Never set in production"*, and a public instance on the internet over HTTPS
//     issuing real session cookies is production in the only sense that matters. Left alone, the
//     ordinary rule applies: `Secure` on HTTPS or behind a proxy setting `X-Forwarded-Proto`.
//
// PLAIN --demo is unchanged, and deliberately so: it forces `Secure` off because the e2e host is
// plain http and NOT loopback, and it starts at `needs_setup` so the first-run flow stays covered.
// Presetting the password there would delete that coverage — a test-coverage loss disguised as a
// feature flag, which is the argument that made this a separate mode rather than a flag on --demo.
// It also emits the mode's startup banner, and that is deliberate rather than incidental: the
// shared demo branch used to print "set the admin password to begin" for BOTH modes, which under
// --public-demo instructs the operator to do something the same binary refuses with a 409. That is
// the price of sharing the branch — a mode-specific message inside shared code is wrong for one
// mode — and the fix is to move the message to where the modes already differ, not to add a second
// place where they differ.
func configureDemoAuth(authSvc *auth.Service, log *slog.Logger, public bool) error {
	if !public {
		authSvc.SetInsecureCookies(true) // demo runs over plain http (localhost / e2e host)
		log.Info("demo mode: serving fixture data — set the admin password to begin")
		return nil
	}
	if err := authSvc.SetPassword(demoPassword); err != nil {
		return fmt.Errorf("preset the public-demo password: %w", err)
	}
	log.Info("public demo mode: serving fixture data — the password is preset and setup is closed; "+
		"Secure cookies follow the request, as in production",
		"password", demoPassword) // published by ruling (quince#444) — a secret would never be logged
	return nil
}

// Server-level deadlines (quince#466). Only ReadHeaderTimeout was set, which left IdleTimeout
// inheriting ReadTimeout's zero — and Go documents that as *no timeout at all*, so an idle
// keep-alive connection was held for as long as the peer cared to hold it. On a LAN that is
// untidy; on the public host quince#444 proposes, connection-holding is the cheapest attack there
// is, and a periodic restart is the only thing currently reclaiming them.
//
// WriteTimeout is SAFE for the WebSocket despite that socket being long-lived, and this was
// checked against the pinned toolchain rather than reasoned about: net/http's `hijackLocked` calls
// `rwc.SetDeadline(time.Time{})` (go1.26.5, `net/http/server.go:325`), so an upgraded connection
// carries no server deadline at all. quince#466 was filed asserting the opposite — that a
// WriteTimeout "applied to a hijacked WebSocket connection would be actively wrong" — and that was
// simply untrue.
//
// So the bound that actually constrains WriteTimeout is the largest ORDINARY response, which is
// GET /api/jobs/{id}/log: handlers_read.go writes the whole log in one io.WriteString with no
// flushing, so a big log to a slow reader has to fit inside it. Hence 120s rather than something
// tight — this is a ceiling on abuse, not a latency budget.
const (
	readHeaderTimeout = 10 * time.Second  // the classic Slowloris bound, unchanged
	readTimeout       = 30 * time.Second  // whole request; bodies are capped at 1 MiB (middleware.go)
	writeTimeout      = 120 * time.Second // whole response; sized for a large job log, not for latency
	idleTimeout       = 120 * time.Second // keep-alive idle — the defect quince#466 actually names
)

// newHTTPServer builds the serving http.Server. Split out so the timeouts are assertable: they are
// a security property with no behavioural surface, so nothing else would notice them going missing.
func newHTTPServer(listen string, h http.Handler) *http.Server {
	return &http.Server{
		Addr:              listen,
		Handler:           h,
		ReadHeaderTimeout: readHeaderTimeout,
		ReadTimeout:       readTimeout,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       idleTimeout,
	}
}

func configCmd(args []string) error {
	if len(args) == 0 || args[0] != "validate" {
		return errors.New("usage: quince config validate [path]")
	}
	bootstrap, _ := config.LoadBootstrap(os.Environ())
	path := bootstrap.ConfigPath()
	if len(args) > 1 {
		path = args[1]
	}
	l := config.Load(path)
	for _, w := range l.Warnings {
		fmt.Fprintf(os.Stderr, "warning: %s: %s\n", w.Path, w.Message)
	}
	if !l.OK {
		for _, e := range l.Errors {
			fmt.Fprintf(os.Stderr, "error: %s: %s\n", e.Path, e.Message)
		}
		return fmt.Errorf("config invalid: %s", path)
	}
	fmt.Printf("config OK: %s\n", path)
	return nil
}

func removeDemoState(dbPath, cfgPath string) {
	for _, p := range []string{dbPath, dbPath + "-wal", dbPath + "-shm", cfgPath} {
		_ = os.Remove(p)
	}
}

func newLogger() *slog.Logger {
	opts := &slog.HandlerOptions{Level: slog.LevelInfo}
	if isTTY(os.Stdout) {
		return slog.New(slog.NewTextHandler(os.Stdout, opts))
	}
	return slog.New(slog.NewJSONHandler(os.Stdout, opts))
}

func isTTY(f *os.File) bool {
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}
