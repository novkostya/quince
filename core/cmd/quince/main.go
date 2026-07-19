// Command quince is the core daemon. Subcommands:
//
//	quince serve [--demo] [--listen :8080]   # serve the UI + API (contracts.md)
//	quince config validate [path]            # validate config.yml; nonzero exit on error
//	quince version                           # print the build version
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
	"github.com/novkostya/quince/core/internal/device"
	"github.com/novkostya/quince/core/internal/httpapi"
	"github.com/novkostya/quince/core/internal/muxd"
	"github.com/novkostya/quince/core/internal/muxsup"
	"github.com/novkostya/quince/core/internal/store"
	"github.com/novkostya/quince/core/internal/version"
	"github.com/novkostya/quince/core/internal/webui"
)

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
		"  quince serve [--demo] [--listen :8080]   serve the UI + API\n"+
		"  quince config validate [path]            validate config.yml\n"+
		"  quince version                           print version\n", version.String())
}

func serve(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	demoMode := fs.Bool("demo", false, "serve in-memory fixture data (no hardware)")
	listenFlag := fs.String("listen", "", "override listen address (else QUINCE_LISTEN)")
	if err := fs.Parse(args); err != nil {
		return err
	}

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
	if *demoMode {
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
	var jobs httpapi.JobReader = httpapi.Empty{}
	var versions httpapi.VersionReader = httpapi.Empty{}
	var muxer httpapi.MuxerControl = httpapi.UnmanagedMuxer{}
	if *demoMode {
		authSvc.SetInsecureCookies(true) // demo runs over plain http (localhost / e2e host)
		prov := demo.NewProvider(eventBus, log)
		prov.Run(ctx)
		devices, jobs, versions = prov, prov, prov
		log.Info("demo mode: serving fixture data — set the admin password to begin")
	} else {
		// Live device tracking (qn.2): one muxd client per configured muxer socket feeds the
		// registry — default topology is usbmuxd for USB + netmuxd for Wi-Fi (stack D2); the
		// single-muxer flip is config-only since the loop skips empty sockets. Jobs and
		// versions land with their rungs (qn.4/qn.5). Muxd-unreachable is honest, not fatal:
		// the client backs off and the device list is simply empty until a device attaches.
		dcfg := cfgSvc.Current().Devices
		// Managed muxer (SIMPLE profile, qn.2b): quince supervises the in-container usbmuxd —
		// spawn under serveCtx, restart w/ backoff, refuse loudly if the socket is already
		// served. HARDENED/external (manage_muxer: false): quince only dials, muxer stays
		// Unmanaged (rescan → 409). The supervisor listens on dcfg.UsbmuxdSocket (its -S path),
		// which is exactly the socket the muxd client below dials.
		if dcfg.ManageMuxer && dcfg.UsbmuxdSocket != "" {
			sup := muxsup.New(dcfg.UsbmuxdSocket, log)
			go sup.Run(ctx)
			muxer = sup
			log.Info("supervising in-container usbmuxd", "socket", dcfg.UsbmuxdSocket)
		} else {
			log.Info("muxer is external (devices.manage_muxer: false or no usbmuxd_socket) — dialing only")
		}
		reg := device.NewRegistry(eventBus, log)
		for _, addr := range []string{dcfg.UsbmuxdSocket, dcfg.NetmuxdAddr} {
			if addr == "" {
				continue
			}
			client := muxd.NewClient(addr, log)
			sink := reg.Sink(addr)
			go client.Run(ctx, sink)
		}
		devices = reg
		log.Info("device registry watching muxers",
			"usbmuxd", dcfg.UsbmuxdSocket, "netmuxd", dcfg.NetmuxdAddr)
	}

	srv := &http.Server{
		Addr: listen,
		Handler: httpapi.NewRouter(httpapi.Deps{
			Log: log, Version: version.String(), Config: cfgSvc, Auth: authSvc, Bus: eventBus,
			Devices: devices, Jobs: jobs, Versions: versions, Muxer: muxer,
		}),
		ReadHeaderTimeout: 10 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		log.Info("quince serving",
			"version", version.String(), "listen", listen,
			"ui_embedded", webui.Built(), "demo", *demoMode)
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
