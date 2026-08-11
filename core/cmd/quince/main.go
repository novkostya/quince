// Command quince is the core daemon. Subcommands:
//
//	quince serve [--demo|--public-demo] [--listen :8968]  # serve the UI + API (contracts.md)
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
	"crypto/tls"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
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
	"github.com/novkostya/quince/core/internal/tlsx"
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
	case "auth":
		return authCmd(args[1:])
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
		"  quince serve [--demo|--public-demo] [--listen :8968]  serve the UI + API\n"+
		"  quince backup <udid> [--transport usb|wifi|auto]   drive one backup to completion\n"+
		"  quince versions verify <id> | --udid <udid>        re-run structural verification\n"+
		"  quince device reset-working <udid>                 discard the dirty working/\n"+
		"  quince auth reset --yes                            clear the admin password + every passkey\n"+
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
		dbPath, cfgPath, cleanup = prepareDemoState(bootstrap.Cache)
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

	// quince#464/quince#555: who may be believed about `X-Forwarded-*`. Empty — the shipping
	// default — trusts nobody: the login limiter buckets on the peer address, and
	// `X-Forwarded-Proto` is believed from anyone, which is byte-for-byte what every deployment
	// does today. A malformed entry is WARNED and ignored rather than dropped silently.
	//
	// FROM THE BOOTSTRAP ENV, not config.yml (Operator ruling 2026-08-02, quince#549):
	// `--public-demo` deletes its config at startup, so the deployment that most needs a trust
	// list could never carry one, and in that mode every visitor can `PUT /api/config`.
	//
	// Built HERE, before the auth service, because two things consume it — the limiter's bucketing
	// and `SecureOrigin`'s header gate — and the second is a property of the Service.
	proxies, badProxies := auth.NewTrustedProxies(bootstrap.TrustedProxies)
	for _, b := range badProxies {
		log.Warn("QUINCE_TRUSTED_PROXIES entry is not an IP or CIDR and is IGNORED",
			"entry", b, "var", "QUINCE_TRUSTED_PROXIES")
	}

	cfgSvc := config.NewService(cfgPath, log)
	authSvc := auth.NewService(st, log)
	// The user's plain-http opt-in (qn.6f slice 8). Applied before either branch below,
	// because it governs cookies and every mode issues those — it is not a property of the
	// live stack. `--demo` then forces Secure off entirely, which is a superset of this
	// rather than a conflict.
	applyInsecureTransportOptIn(authSvc, cfgSvc.Current(), os.Stderr)
	authSvc.SetTrustedProxies(proxies)
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
	// reconcileReporter stays nil in --demo: fixtures are complete the moment they exist, so there is
	// no provisional state to declare and `reconciling` is honestly false. Left as the INTERFACE and
	// assigned only from a non-nil runner — assigning a typed nil pointer would make the interface
	// itself non-nil and the handler would call through it.
	var reconcileReporter httpapi.ReconcileReporter
	if demoMode {
		// configureDemoAuth owns the mode banner too, so this branch has NO `if *publicDemo` in it.
		// A second divergence point here would erode what the shared branch buys — see its doc.
		if err := configureDemo(cfgSvc, authSvc, log, *publicDemo); err != nil {
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
		// THE LOAD'S WARNINGS ARE PASSED, and that is what lets the refusal tell a PARSE FAILURE
		// from an absent key (quince#508). `Current()` alone cannot: a failed parse yields
		// `Default()`, and its nil `Storage` reads identically to a file that declares nothing.
		_, cfgWarnings, _ := cfgSvc.Snapshot()
		req := config.CheckStorages(cfgSvc.Current(), os.Environ(), cfgWarnings)

		// RULED 2026-08-07 (quince#502, option (a)): ANY ZERO-STORAGE START IS THE ONBOARDING STATE.
		// quince serves, refusing every API outside setupAllowed, and renders the storage step.
		//
		// What that replaced was a hard exit whose own words were "a quince that comes up with
		// nowhere to put backups looks healthy and silently protects nothing". THE HONESTY CHANGES
		// CHANNEL RATHER THAN BEING LOST: it does not look healthy — it refuses its own API and says
		// what is missing, in the UI, instead of on a stderr stream nobody sees. Serving is what
		// makes the remedy reachable at all, because /data/config.yml does not exist on a fresh
		// install and the alternative was: start → exit → hand-write YAML into a bind mount →
		// start again.
		//
		// THE COST WAS NAMED AND ACCEPTED, not discovered: *onboarding* and *misconfigured* are
		// byte-identical here, so a config whose `storage:` list someone emptied by hand gets the
		// setup page rather than a refusal. Ruled not a state-honesty downgrade, because the page is
		// true in both cases — there is no storage, and here is how to add one — and the daemon
		// becomes fixable from a browser instead of a shell.
		//
		// MALFORMED STILL REFUSES, and that is the ruling's own carve-out rather than caution. A
		// file that could not be PARSED is not an empty declaration: Load() falls back to Default()
		// on a parse failure, so serving here would silently ignore whatever the operator actually
		// wrote and invite them to add a storage to a document that already has one it cannot read
		// (quince#508).
		if req.Malformed {
			return req.Explain(os.Stderr, cfgPath)
		}
		// LegacyEnv is not fatal and never was — OK() excludes it — so it keeps flowing through as
		// the warning it is.
		storageless := req.Missing || req.Empty
		if storageless {
			log.Warn("no storage declared — SERVING SETUP ONLY until one is added",
				"config", cfgPath, "reachable", "auth, onboarding, config and the storage probes")
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
		// scanDeferred: the ~48-second per-device scan does NOT run before the listener binds
		// (qn.6i, quince#592). Roll-forward still does, synchronously, inside the build — the job-row
		// reconciler that follows it depends on it, and it costs syscalls rather than a walk.
		ls, err := buildLiveStack(ctx, bootstrap, cfgSvc, st, eventBus, log, scanDeferred)
		if err != nil {
			return err
		}
		devices, jobs, jobControl = ls.devices, ls.jobs, ls.jobControl
		versions, versionAdmin, muxer, ops = ls.versions, ls.versionAdmin, ls.muxer, ls.ops
		storages = ls.storages
		if ls.reconcile != nil {
			// TRIGGERED BEFORE THE ROUTER IS BUILT so `reconciling` is already true on the FIRST
			// request anybody can make. Trigger sets the flag synchronously; the pass itself runs on
			// the runner's goroutine. A trigger placed after the bind would leave a window in which
			// health answered `false` about a registry that had not been scanned — the false `false`
			// this rung exists to remove, reintroduced by ordering.
			ls.reconcile.Start(ctx)
			// The scheduled trigger (qn.6i PR 5). Started even when the interval is 0 — the loop has to
			// be running for the setting to be turnable back ON without a restart, which is what makes
			// it live rather than half-live.
			ls.reconcile.StartSchedule(ctx)
			ls.reconcile.Trigger("startup")
			reconcileReporter = ls.reconcile
		}
		if ls.engine != nil { // the engine holds per-UDID single-flight, so it owns Reset (qn.5b)
			workingReset = ls.engine
		}
	}

	handler := httpapi.NewRouter(httpapi.Deps{
		Log: log, Version: version.String(), Mode: serveMode(demoMode, *publicDemo),
		DemoResetMinutes: reportableResetMinutes(bootstrap.DemoResetMinutes, *publicDemo, log),
		Config:           cfgSvc, Auth: authSvc, Bus: eventBus, Proxies: proxies,
		Devices: devices, Jobs: jobs, JobControl: jobControl, Versions: versions,
		VersionAdmin: versionAdmin, Muxer: muxer, Ops: ops, WorkingReset: workingReset,
		Storages: storages, Reconcile: reconcileReporter,
		// READ LIVE, NOT CAPTURED. `storageless` above is the state at STARTUP; this closure is the
		// state NOW, and they stop agreeing the instant setup succeeds. Capturing the boolean would
		// leave a freshly-configured daemon refusing its own API until someone restarted it — the
		// exact restart this rung exists to remove.
		//
		// Nil in demo mode, where `--demo` fabricates its storages and the mode never applies.
		StorageRequired: storageRequired(demoMode, cfgSvc),
		// Passkeys (qn.6k). The ceremony store is per-process and in memory; the credentials
		// themselves live in the app DB, which is why both are wired and neither is optional here.
		Store: st, Passkeys: auth.NewPasskeyCeremonies(),
		// Nil in demo mode — the carve-out is the nil, not a branch in a handler (qn.6m D6).
		PasswordAdmin: passwordAdmin(demoMode, authSvc),
	})

	// THE CERTIFICATE CHECK IS ON THE SERVE PATH AND NOT IN Validate — the spec calls this
	// the rung's load-bearing measurement. Load() discards a config that fails Validate and
	// returns Default(), which has no TLS, so a certificate fault raised there would start
	// the daemon on plain http for somebody who asked for https. Placed OUTSIDE the demo
	// branch, unlike CheckStorages: TLS governs how every mode is reached, and a deployment
	// with TLS off never reaches the loader at all.
	var keeper *tlsx.Keeper
	if req := config.CheckTLS(cfgSvc.Current(), func(certFile, keyFile string) error {
		k, err := tlsx.NewKeeper(certFile, keyFile)
		keeper = k
		return err
	}); !req.OK() {
		return req.Explain(os.Stderr, cfgPath)
	}
	if keeper != nil {
		keeper.OnReloadError = func(err error) {
			// Not fatal: a half-written key mid-renewal is transient and the cached
			// certificate is still valid. WARN so a rotation that is genuinely broken leaves
			// a trail, rather than a browser error being the first anyone hears of it.
			log.Warn("tls certificate reload failed, still serving the previous one", "error", err)
		}
	}

	log.Info("quince serving",
		"version", version.String(), "listen", listen, "tls", keeper != nil,
		"ui_embedded", webui.Built(), "demo", demoMode, "public_demo", *publicDemo)

	return runHTTP(ctx, listen, handler, keeper, cfgSvc.Current().Sessions.AllowInsecureTransport, log)
}

// runHTTP runs the HTTP server, and when a certificate is configured runs BOTH protocols on the
// single port QUINCE_LISTEN names (gap A, Operator ruling 2026-08-02, option (c)).
//
// With no certificate this is the plain http.Server it always was, so the reverse-proxy and
// --demo tiers get none of the machinery below.
func runHTTP(ctx context.Context, listen string, handler http.Handler, keeper *tlsx.Keeper, allowInsecure bool, log *slog.Logger) error {
	if keeper == nil {
		srv := newHTTPServer(listen, handler)
		return runUntilDone(ctx, log, srv.ListenAndServe, srv.Shutdown)
	}

	ln, err := net.Listen("tcp", listen)
	if err != nil {
		// A BIND FAILURE IS A LOUD NAMED ERROR, NEVER A FALLBACK TO ANOTHER PORT. Under
		// network_mode: host — which Wi-Fi requires — nothing can be remapped, so silently
		// moving would leave quince at an address the user will never guess.
		return fmt.Errorf("listen on %s: %w", listen, err)
	}
	mux := tlsx.NewMux(ln)

	tlsSrv := newHTTPServer(listen, handler)
	tlsSrv.TLSConfig = &tls.Config{GetCertificate: keeper.GetCertificate, MinVersion: tls.VersionTLS12}

	// The plain half either redirects or serves, and WHICH is the user's setting rather than
	// ours. `sessions.allow_insecure_transport` beats the redirect (Operator, same ruling):
	// over a VPN the transport is already encrypted, and a redirect overriding an explicit,
	// off-by-default, surfaced opt-in would make that setting undeclarable on exactly the
	// deployments that want it — every one where a certificate also exists.
	//
	// The spec permitted slice 4 to ship the redirect UNCONDITIONAL, reasoning that slice 8's
	// flag did not exist yet so there was no user it could wrong. That expired when slice 8
	// merged first (quince#540), so the exception ships with the redirect rather than after.
	plainHandler := http.Handler(redirectToHTTPS())
	if allowInsecure {
		plainHandler = handler
	}
	plainSrv := newHTTPServer(listen, plainHandler)

	errCh := make(chan error, 2)
	serveHalf := func(name string, f func() error) {
		if err := f(); err != nil && !errors.Is(err, http.ErrServerClosed) && !errors.Is(err, net.ErrClosed) {
			errCh <- fmt.Errorf("%s server: %w", name, err)
		}
	}
	go serveHalf("https", func() error { return tlsSrv.ServeTLS(mux.TLS(), "", "") })
	go serveHalf("http", func() error { return plainSrv.Serve(mux.Plain()) })

	select {
	case err := <-errCh:
		_ = mux.Close()
		return err
	case <-ctx.Done():
		log.Info("shutdown signal received, draining")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		// Both servers, then the mux. Each Shutdown closes the listener it was given, which
		// is a `side` whose Close closes the whole mux — idempotent by design, so the second
		// is a no-op rather than the `use of closed network connection` two servers over one
		// listener would otherwise race into.
		errTLS := tlsSrv.Shutdown(shutdownCtx)
		errPlain := plainSrv.Shutdown(shutdownCtx)
		_ = mux.Close()
		return errors.Join(errTLS, errPlain)
	}
}

// runUntilDone is the pre-TLS serve loop, unchanged in behaviour and factored out so the
// no-certificate path stays visibly the same code it was.
func runUntilDone(ctx context.Context, log *slog.Logger, start func() error, shutdown func(context.Context) error) error {
	errCh := make(chan error, 1)
	go func() {
		if err := start(); err != nil && !errors.Is(err, http.ErrServerClosed) {
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
		return shutdown(shutdownCtx)
	}
}

// redirectToHTTPS sends plain-http callers to the same host and port over https, which is
// what makes one-port routing worth having: the URL the user typed keeps working.
//
// 301 PERMANENT, per the ruling, and it is cacheable on purpose — a bookmark upgrades itself
// once and stays upgraded. The cost is that it stays cached if the user later removes the
// certificate, sending them to an https URL that no longer answers. That is the recorded
// trade against `307`, and it is why turning TLS off is a config edit rather than something
// quince ever decides on its own.
func redirectToHTTPS() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// r.Host is the host:port the client actually asked for, which is what makes "same
		// URL, upgraded in place" true — including a non-default port, the normal case here
		// now that the default is :8968.
		u := *r.URL
		u.Scheme, u.Host = "https", r.Host
		http.Redirect(w, r, u.String(), http.StatusMovedPermanently)
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
// applyInsecureTransportOptIn wires `sessions.allow_insecure_transport` into the auth
// service and, when it is on, SAYS SO ON STDERR (qn.6f slice 8, Operator ruling
// 2026-08-02).
//
// The announcement is the point, not a courtesy. This is a degraded mode: the session
// cookie and the CSRF token cross the network in clear, so anyone who can read the path can
// impersonate the admin of an application that shows a person's entire digital life. `No
// silent caps or fallbacks` makes surfacing it mandatory, and a security relaxation that
// took effect quietly would be indistinguishable from a bug.
//
// STDERR RATHER THAN THE LOGGER, deliberately: this must be legible in `docker logs` on a
// box whose log level someone has turned down, and it is a startup fact rather than an
// event. It joins CheckStorages' refusal in writing directly to the stream.
//
// It is ONE of the three channels the ruling names. The config warning is the second. The
// third — a non-dismissible in-app banner — is NOT built here; quince#539.
func applyInsecureTransportOptIn(authSvc *auth.Service, cfg config.Config, w io.Writer) {
	if !cfg.Sessions.AllowInsecureTransport {
		return
	}
	authSvc.SetAllowInsecureTransport(true)
	_, _ = fmt.Fprint(w, "quince: sessions.allow_insecure_transport is ON — session and CSRF cookies\n"+
		"quince: will be served WITHOUT the Secure flag to plain-http clients, so they cross\n"+
		"quince: the network in clear and anyone who can read the path can sign in as you.\n"+
		"quince: This is a deliberate setting for a network you trust (a VPN, or a LAN you\n"+
		"quince: control). Turn it off in config.yml if you did not mean it.\n")
}

func configureDemoAuth(authSvc *auth.Service, log *slog.Logger, public bool) error {
	if !public {
		authSvc.SetInsecureCookies(true) // demo runs over plain http (localhost / e2e host)
		log.Info("demo mode: serving fixture data — set the admin password to begin")
		return nil
	}
	if err := authSvc.SetPassword(demoPassword, "startup"); err != nil {
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

// demoStatePaths is where a demo instance's throwaway state lives — under the CACHE dir, never the
// data dir, which is what makes the whole lifecycle safe to delete twice a run.
//
// Extracted so spec story 7 can assert the restart cycle over a temp dir (quince#444). That is not
// a test-only convenience: the location IS the story. Move these under the data dir and every
// reset would still appear to work — `removeDemoState` deletes whatever it is handed — while
// quietly wiping a real deployment's DB the first time somebody ran the binary with `--demo`.
func demoStatePaths(cache string) (dbPath, cfgPath string) {
	return filepath.Join(cache, "demo.db"), filepath.Join(cache, "demo-config.yml")
}

// configureDemo is everything the demo branch configures before the provider is built: the mode's
// auth (preset password or first-run, per D1) and the storage declaration quince#574 added.
//
// ONE FUNCTION SO A TEST CAN DRIVE THE SEQUENCE RATHER THAN A COPY OF IT. With the two calls
// inlined in serve(), every test called them individually and deleting one from serve() failed
// nothing — which is the same defect quince#575's review found in story 7's first revision, rebuilt
// here within a day. The lesson did not survive contact with a second call site, so it is now
// structural instead of remembered.
func configureDemo(cfgSvc *config.Service, authSvc *auth.Service, log *slog.Logger, public bool) error {
	if err := configureDemoAuth(authSvc, log, public); err != nil {
		return err
	}
	return seedDemoStorages(cfgSvc)
}

// seedDemoStorages writes the storages the demo provider serves into the demo's config document,
// so the document quince SERVES is one quince ACCEPTS BACK (quince#574, Operator ruling
// 2026-08-03).
//
// Without it `config.storage` is null in demo mode — nothing in that branch ever sets it — and
// `config.Service.Replace` refuses a save that declares no storage. So a visitor who opened
// Settings and pressed Save got a 422 having changed nothing, on the surface quince#444 calls the
// reason a live demo beats screenshots.
//
// IT GOES THROUGH Replace, not around it. That is the point of the ruling rather than an
// implementation detail: Replace is the same validating path a visitor's save takes, so seeding
// through it means the seeded document is proven acceptable by the very check that was rejecting
// it. A seed written straight to the file could satisfy this function and still 422 on save.
//
// FATAL on failure, deliberately. The ruling is explicit that no exemption is added anywhere: "if
// a 422 still appears after this lands, that is a real defect in the seeded document, not a case
// for an exemption". A demo that silently came up with an unsaveable config would restore exactly
// the bug this fixes, minus the symptom that led anyone to find it.
//
// Re-seeded on EVERY demo start, which is what makes it survive the reset: prepareDemoState has
// just deleted demo-config.yml, so a visitor's edits — including removing these entries — are
// gone by the time this runs (spec story 7, gated by quince#575).
func seedDemoStorages(cfgSvc *config.Service) error {
	c := cfgSvc.Current()
	entries := demo.StorageEntries()
	c.Storage = &entries
	errs, _, err := cfgSvc.Replace(c) // qn.6g: no appliers are registered at seed time
	if err != nil {
		return fmt.Errorf("seed the demo storage declaration: %w", err)
	}
	if len(errs) > 0 {
		return fmt.Errorf("the demo storage declaration does not validate (%s: %s) — "+
			"demo.StorageEntries is wrong; the save path is deliberately not exempt (quince#574)",
			errs[0].Path, errs[0].Message)
	}
	return nil
}

// prepareDemoState is a demo instance's whole state lifecycle: fresh throwaway paths, wiped on the
// way IN, and a cleanup that wipes again on a graceful way out. It returns the paths so the caller
// opens the store on them.
//
// WIPING AT STARTUP IS THE HALF THAT CARRIES THE GUARANTEE, and it is easy to read as belt-and-
// braces beside the deferred cleanup. It is not. A public demo is reset by something OUTSIDE the
// process restarting it (spec D4), and a container stop is entitled to be a SIGKILL — so the
// deferred half is exactly what a real reset cannot rely on.
//
// Delete this line and graceful restarts still reset, while killed ones break DIFFERENTLY IN EACH
// MODE — measured, not reasoned:
//
//   - `--public-demo` REFUSES TO START. The surviving DB still holds the password, so presetting it
//     returns ErrAlreadyConfigured and serve() fails. Loud, and the demo is simply down.
//   - `--demo` starts and silently inherits the previous run's DB and config — a visitor's damage
//     included — and comes up at needs_login instead of needs_setup, which quietly deletes e2e's
//     first-run set-password coverage.
//
// Neither is a reset. They are recorded separately because the loud one is the one a reader assumes
// covers both, and it is the quiet one that costs coverage nobody notices losing.
//
// Fresh state each run also keeps `--demo` starting at needs_setup, so e2e keeps exercising the
// first-run set-password flow (rung-ruled reading of "--demo seeds password demo"; the canonical
// demo password is "demo", entered at setup).
//
// One function rather than three lines inlined in serve(), so spec story 7's test drives THIS
// sequence rather than a copy of it — a test that reimplements the order it is asserting would pass
// with the startup wipe deleted (quince#444).
func prepareDemoState(cache string) (dbPath, cfgPath string, cleanup func()) {
	dbPath, cfgPath = demoStatePaths(cache)
	removeDemoState(dbPath, cfgPath)
	return dbPath, cfgPath, func() { removeDemoState(dbPath, cfgPath) }
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

// serveMode maps the flags to the mode string GET /api/health reports (public-demo spec story 5).
// The login screen reads it to decide whether to print the password, so getting it wrong either
// leaks demo copy into the shipping product or leaves a public visitor with no way to sign in.
func serveMode(demo, public bool) string {
	switch {
	case public:
		return httpapi.ModePublicDemo
	case demo:
		return httpapi.ModeDemo
	default:
		return httpapi.ModeNormal
	}
}

// reportableResetMinutes gates QUINCE_DEMO_RESET_MINUTES on the ONE mode that is actually reset on
// a schedule (spec story 6). --public-demo is restarted from outside the process (D4); nothing
// restarts a --demo instance or the shipping product, so reporting an interval there would put a
// destructive promise on a screen where it is simply false.
//
// The var set OUTSIDE that mode WARNS rather than being dropped. It is the shape of mistake this
// deployment invites — copy the compose file, drop the flag, keep the env — and its symptom is
// nothing at all: no reset happens and no notice appears, which is also exactly what a correct
// non-demo deployment looks like. `no silent caps or fallbacks` is about precisely this case.
//
// It takes an int and a bool rather than the whole Bootstrap so a test can drive every combination
// without building a deployment.
func reportableResetMinutes(minutes int, public bool, log *slog.Logger) int {
	if public {
		return minutes
	}
	if minutes > 0 {
		log.Warn("QUINCE_DEMO_RESET_MINUTES is set but this instance is not --public-demo: "+
			"nothing here resets on a schedule, so the interval is ignored and no reset notice "+
			"is shown", "minutes", minutes)
	}
	return 0
}

// storageRequired reports, per request, whether quince still has no storage declared — the
// first-run setup state (qn.6e, Operator ruling 2026-08-07, option (a)).
//
// IT ASKS THE CONFIG SERVICE RATHER THAN REMEMBERING, which is what makes the mode self-clearing:
// adding a storage ends it with no restart and nothing to reset, because there is no stored flag to
// go stale. "The zero-storage condition IS the state" is the ruling's own phrasing and this is the
// whole of its implementation.
//
// Nil in demo mode: `--demo` fabricates its storages and never reaches the live stack, so a guard
// there would refuse a mode that has nothing to configure.
func storageRequired(demoMode bool, cfgSvc *config.Service) func() bool {
	if demoMode {
		return nil
	}
	return func() bool {
		scfg := cfgSvc.Current().Storage
		return scfg == nil || len(*scfg) == 0
	}
}

// passwordAdmin decides whether PUT/DELETE /api/auth/password have a real implementation behind
// them — qn.6m D6, and the second demo carve-out in this file to be spelled the same way.
//
// NIL IN DEMO MODE, and the nil IS the carve-out. `httpapi.NewRouter` turns it into
// `UnavailablePasswordAdmin`, which refuses both operations with 503 and a stated reason, so the
// surface still exists and explains itself. quince#841 is explicit that this is the shape to use:
// "quince has no demo flag at the API layer … no handler contains an `if demo` branch", and adding
// one here would be the first of a second pattern.
//
// WHY THE DEMO MUST REFUSE, which is a product fact rather than a technical one: the public demo
// presets a shared password and PUBLISHES it on the login screen. One visitor changing it — or
// removing it to go passwordless against a passkey only they hold — locks out every other visitor
// until the periodic reset. That is the same reasoning that keeps the demo's other destructive
// surfaces unwired, and it is why ruling B says passwordless is "never on the demo".
func passwordAdmin(demoMode bool, authSvc *auth.Service) httpapi.PasswordAdmin {
	if demoMode {
		return nil
	}
	return authSvc
}
