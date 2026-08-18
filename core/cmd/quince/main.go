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
	"github.com/novkostya/quince/core/internal/backup"
	"github.com/novkostya/quince/core/internal/bus"
	"github.com/novkostya/quince/core/internal/config"
	"github.com/novkostya/quince/core/internal/demo"
	"github.com/novkostya/quince/core/internal/httpapi"
	"github.com/novkostya/quince/core/internal/id"
	"github.com/novkostya/quince/core/internal/notify"
	"github.com/novkostya/quince/core/internal/pushsvc"
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
	// …and it stays applied, in both directions, for the life of the process (quince#900).
	// Wiring-time subscription, which is what Subscribe requires; it fires on writes only, so
	// the call above is still what establishes the startup state.
	subscribeInsecureTransport(cfgSvc, authSvc, log)
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
	// The Web Push service (qn.12), nil in demo mode. ONE INSTANCE, feeding two consumers: the
	// notification routes and the notifier runner started in the live branch below. Two would each
	// race to mint a VAPID keypair on first use, which the store refuses correctly but which would
	// still mean two things claiming to own the same key's lifecycle.
	//
	// DECLARED AS THE CONCRETE TYPE and converted at the router, because `httpapi.NotificationReader`
	// holding a nil `*pushsvc.Service` is a non-nil interface — the same trap `reconcileReporter`
	// names three lines up, and here it would register the routes in demo mode onto a receiver that
	// has no store.
	var pushSvc *pushsvc.Service
	if !demoMode {
		// THE KEYPAIR IS NOT GENERATED HERE. `pushsvc` mints it on the first read of the public half,
		// so an install that never opens the notifications page never creates one — and the
		// generation rules, which are the Operator's (quince#1128), stay in one place rather than
		// being split between startup and a handler.
		pushSvc = pushsvc.New(st, func() string { return id.New() }, time.Now)
	}
	if demoMode {
		// configureDemoAuth owns the mode banner too, so this branch has NO `if *publicDemo` in it.
		// A second divergence point here would erode what the shared branch buys — see its doc.
		if err := configureDemo(cfgSvc, authSvc, log, *publicDemo); err != nil {
			return err
		}
		prov := demo.NewProvider(eventBus, log)
		// THE DEMO'S `default` IS READ FROM THE DECLARED CONFIG, NOT FABRICATED (quince#1036).
		//
		// `POST /api/config/storage/{name}/default` answered 200 and wrote the document, and
		// `GET /api/storages` went on reporting a literal — so the badge did not move and the button
		// did not go away. Indistinguishable from a no-op on the surface a reviewer clicks, and an
		// e2e written against it would have been green whatever the daemon did, which is the shape
		// quince#661 already cost this file once.
		//
		// AFTER configureDemo, which seeds these storages into the document, so the list and the
		// declaration agree on the FIRST request rather than converging later.
		prov.SetDefaultStorageName(func() string {
			for _, e := range declaredStorages(cfgSvc.Current().Storage) {
				if e.Default {
					return e.Name
				}
			}
			return ""
		})
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
		// THE LOAD'S TYPED FAILURE IS PASSED, and that is what lets the refusal tell a file quince
		// could not USE from an absent key (quince#508, quince#544). `Current()` alone cannot: every
		// failing load yields `Default()`, and its nil `Storage` reads identically to a file that
		// declares nothing. This was `Snapshot()`'s warnings, which `CheckStorages` then re-parsed
		// by prose prefix — see that function for why the string contract had to go.
		req := config.CheckStorages(cfgSvc.Current(), os.Environ(), cfgSvc.LoadFailure())

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
		//
		// UNREADABLE DOES **NOT** JOIN IT, AND THAT IS GATED RATHER THAN CHOSEN (quince#544). It looks
		// like the same case — quince cannot tell what the operator declared either way — and this
		// line refused on it for one CI run before `deploy/storageless-smoke`'s third arm refuted it:
		// *"it SERVES — so the add endpoint is reachable in this state"*. That arm exists for
		// quince#852. An unreadable config must SERVE, so `POST /api/config/storage` is reachable and
		// can answer `422`; refusing at startup removes the only surface from which the state is
		// repairable, which is the same argument quince#502 made for the zero-storage case.
		//
		// So the carve-out stays exactly as wide as it was. What quince#544 changes here is the
		// PREDICATE, not the behaviour: an unreadable config used to report `Missing`, and now
		// reports `Unreadable`.
		if req.Malformed {
			return req.Explain(os.Stderr, cfgPath)
		}
		// LegacyEnv is not fatal and never was — OK() excludes it — so it keeps flowing through as
		// the warning it is.
		//
		// `Unreadable` IS IN THIS SET, and leaving it out is the bug this line would otherwise have
		// (quince#544). Before the typed cause, an unreadable config reported `Missing` and reached
		// setup through it. Now that it reports itself, a `Missing || Empty` test would send it PAST
		// the setup branch into `buildLiveStack` with no declared storage — worse than the false
		// message the typed cause was added to fix, and invisible until something dereferenced it.
		storageless := req.Missing || req.Empty || req.Unreadable
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
		// THE NOTIFIER, and it is the last wire in qn.12 (quince#1124). Everything under it has been
		// merged and tested for weeks; until this line existed a quince install could subscribe a
		// phone, send itself a test, and then never hear from the daemon again.
		//
		// STARTED HERE RATHER THAN IN buildLiveStack because it is not part of the live stack: it
		// consumes that stack and the app DB and the config, and `backup` — the other caller of
		// buildLiveStack — is a one-shot CLI that must not open a push subscription to anybody.
		//
		// The engine supplies `RunningFor` and is nil when the muxer is unconfigured, in which case
		// there are no live devices to remind about and startNotifier does nothing.
		startNotifier(ctx, log, eventBus, cfgSvc, st, ls.devices, engineJobs(ls.engine), pushDeliverer(pushSvc))
	}

	// CONSTRUCTED HERE, ABOVE THE ROUTER, BECAUSE THE ROUTER NOW NEEDS IT (quince#908 slice 5). It
	// used to be built just before `runHTTP`, which was fine while `subscribeTLS` was its only
	// consumer; the certificate trial is a second one, and it lives in `httpapi`.
	//
	// `NewEmptyKeeper` CANNOT FAIL, which is what makes moving it safe: the startup REFUSAL is
	// `config.CheckTLS` below, and that stays exactly where it was, so a configured-but-unusable pair
	// still stops the process before anything serves.
	keeper := tlsx.NewEmptyKeeper()

	handler := httpapi.NewRouter(httpapi.Deps{
		Log: log, Version: version.String(), Mode: serveMode(demoMode, *publicDemo),
		DemoResetMinutes: reportableResetMinutes(bootstrap.DemoResetMinutes, *publicDemo, log),
		Config:           cfgSvc, Auth: authSvc, Bus: eventBus, Proxies: proxies,
		Devices: devices, Jobs: jobs, JobControl: jobControl, Versions: versions,
		VersionAdmin: versionAdmin, Muxer: muxer, Ops: ops, WorkingReset: workingReset,
		Storages: storages, Reconcile: reconcileReporter,
		// THE SAME Keeper `subscribeTLS` FEEDS, and the certificate trial points it at a pair
		// WITHOUT writing `config.yml` (quince#908 slice 5). One Keeper, two ways in: the applier,
		// for a config edit, and the trial, for a certificate nobody has proved yet. The second
		// writes nothing, which is the whole design — an abandoned trial leaves no trace in a file
		// the user hand-edits.
		Keeper: keeper,
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
		Reauth: auth.NewReauthCeremonies(), Proofs: auth.NewProofs(),
		// Nil in demo mode — the carve-out is the nil, not a branch in a handler (qn.6m D6).
		PasswordAdmin: passwordAdmin(demoMode, authSvc),
		// Web Push (qn.12). NIL IN DEMO MODE for the same reason `PasswordAdmin` is: the demo
		// fabricates its own world and has no real device to notify, and a nil leaves the routes
		// unregistered rather than putting a mode check inside a handler.
		//
		// THE KEYPAIR IS NOT GENERATED HERE. `pushsvc` mints it on the first read of the public half,
		// so an install that never opens the notifications page never creates one — and the
		// generation rules, which are the Operator's (quince#1128), stay in one place rather than
		// being split between startup and a handler.
		Notifications: notificationReader(pushSvc),
	})

	// THE CERTIFICATE CHECK IS ON THE SERVE PATH AND NOT IN Validate — the spec calls this
	// the rung's load-bearing measurement. Load() discards a config that fails Validate and
	// returns Default(), which has no TLS, so a certificate fault raised there would start
	// the daemon on plain http for somebody who asked for https. Placed OUTSIDE the demo
	// branch, unlike CheckStorages: TLS governs how every mode is reached, and a deployment
	// with TLS off never reaches the loader at all.
	//
	// THE KEEPER IS ALWAYS THERE NOW, EMPTY WHEN NO CERTIFICATE IS CONFIGURED (quince#900).
	// It used to be a nil *Keeper, and that nil was the thing that made turning TLS on need a
	// restart: `runHTTP` branched on it at the bind, so an install that started without a
	// certificate had no TLS half at all and no amount of config writing could give it one.
	// CheckTLS's contract is unchanged — it calls this only when the config asks for TLS, so a
	// configured-but-unusable pair still REFUSES TO START, which is the whole point of the
	// check being here rather than in Validate.
	// The Keeper itself is built above, before the router, which needs it. This is still where the
	// REFUSAL happens, and that is the part the comment above is about.
	if req := config.CheckTLS(cfgSvc.Current(), keeper.SetFiles); !req.OK() {
		return req.Explain(os.Stderr, cfgPath)
	}
	keeper.OnReloadError = func(err error) {
		// Not fatal: a half-written key mid-renewal is transient and the cached
		// certificate is still valid. WARN so a rotation that is genuinely broken leaves
		// a trail, rather than a browser error being the first anyone hears of it.
		log.Warn("tls certificate reload failed, still serving the previous one", "error", err)
	}
	subscribeTLS(cfgSvc, keeper, log)

	log.Info("quince serving",
		"version", version.String(), "listen", listen, "tls", keeper.HasCertificate(),
		"ui_embedded", webui.Built(), "demo", demoMode, "public_demo", *publicDemo)

	// READ LIVE, NOT CAPTURED — the same reasoning as StorageRequired above. The opt-in is a
	// live setting since quince#900, and capturing the boolean here would leave the plain half
	// deciding from the value the process started with while the auth service had already
	// moved on: two halves of one setting disagreeing, which is worse than either answer.
	return runHTTP(ctx, listen, handler, keeper,
		func() bool { return cfgSvc.Current().Sessions.AllowInsecureTransport },
		// LIVE FOR THE SAME REASON, and it is the predicate the redirect's PERMANENCE turns on
		// (Operator ruling 2026-08-17, quince#1157). `TLS.Enabled()` is *`config.yml` names a
		// pair*, which a certificate trial deliberately does not do: a trial points the keeper at
		// files and leaves the file untouched, so this reads false throughout one and true the
		// moment a confirm writes the pair.
		func() bool { return cfgSvc.Current().TLS.Enabled() }, log)
}

// runHTTP runs BOTH protocols on the single port QUINCE_LISTEN names (gap A, Operator ruling
// 2026-08-02, option (c)) — ALWAYS, whether or not a certificate is configured (quince#900).
//
// THE MUX IS BOUND UNCONDITIONALLY, AND THAT IS THE WHOLE POINT OF THIS FUNCTION NOW. There
// used to be a `keeper == nil` early return here, and it is what made turning TLS on a
// restart: an install that started without a certificate had no TLS half, so writing one into
// config.yml could not produce a listener that would serve it.
//
// IT COSTS EVERY CONNECTION A ONE-BYTE SNIFF, INCLUDING ON INSTALLS THAT NEVER USE TLS, and
// that is a decision rather than a side effect (quince#900). An HTTP-only quince — the
// reverse-proxy and --demo tiers — used to bypass the mux entirely; now it peeks one byte and
// carries `peekTimeout` for a client that connects and sends nothing. The trade is that TLS
// becomes reachable at runtime, which is what makes an apply-and-revert flow possible in
// memory instead of as an on-disk two-phase commit across a restart.
func runHTTP(ctx context.Context, listen string, handler http.Handler, keeper *tlsx.Keeper, allowInsecure, tlsDeclared func() bool, log *slog.Logger) error {
	ln, err := net.Listen("tcp", listen)
	if err != nil {
		// A BIND FAILURE IS A LOUD NAMED ERROR, NEVER A FALLBACK TO ANOTHER PORT. Under
		// network_mode: host — which Wi-Fi requires — nothing can be remapped, so silently
		// moving would leave quince at an address the user will never guess.
		return fmt.Errorf("listen on %s: %w", listen, err)
	}
	return serveBothProtocols(ctx, ln, listen, handler, keeper, allowInsecure, tlsDeclared, log)
}

// serveBothProtocols is runHTTP's body once the port is bound, split out so a test can drive
// the REAL routing over a `127.0.0.1:0` listener (quince#900).
//
// The alternative was to have the test rebuild this wiring beside the code — a mux, two
// servers, and the plain-half choice — which is the shape that passes while the thing it
// claims to cover has changed underneath it. `listen` still rides along because
// `newHTTPServer` records it as `Addr`; nothing serves from it once `Serve` is handed a
// listener.
func serveBothProtocols(ctx context.Context, ln net.Listener, listen string, handler http.Handler, keeper *tlsx.Keeper, allowInsecure, tlsDeclared func() bool, log *slog.Logger) error {
	mux := tlsx.NewMux(ln)

	tlsSrv := newHTTPServer(listen, handler)
	tlsSrv.TLSConfig = &tls.Config{GetCertificate: keeper.GetCertificate, MinVersion: tls.VersionTLS12}

	plainSrv := newHTTPServer(listen, plainHalf(handler, keeper, allowInsecure, tlsDeclared))

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

// plainHalf is the handler on the non-TLS side of the mux: it redirects to https, or serves
// the app, and it decides WHICH PER REQUEST rather than at the bind (quince#900).
//
// It used to be chosen once, when the listener was created, which was correct while both
// inputs were fixed for the life of the process. Both are now live — a certificate can be
// applied or cleared through `PUT /api/config`, and so can the opt-in — so a choice made at
// bind time would be a third copy of the setting, going stale the moment either moves.
//
// TWO INPUTS DECIDE WHETHER TO REDIRECT, AND THE ORDER OF THE TEST IS THE SAFETY PROPERTY.
//
//   - `keeper.HasCertificate()` — LOADED, not merely configured. Redirecting when nothing can
//     complete a handshake is the trap this whole feature exists inside: once the plain half
//     redirects, EVERY http request is redirected into the failure, so the client has no
//     working channel left to ask for a revert. Asking whether a certificate is actually
//     loaded is what makes an unusable one a non-event instead of a lockout.
//   - `allowInsecure()` — `sessions.allow_insecure_transport` beats the redirect (Operator
//     ruling 2026-08-02). Over a VPN the transport is already encrypted, and a redirect
//     overriding an explicit, off-by-default, surfaced opt-in would make that setting
//     undeclarable on exactly the deployments that want it — every one where a certificate
//     also exists.
//
// A THIRD DECIDES HOW LONG THE ANSWER LASTS, and it is a different question from both of
// those: `tlsDeclared()` is *`config.yml` names a pair*, and it chooses 301 over 307. See
// `redirectToHTTPS`.
func plainHalf(app http.Handler, keeper *tlsx.Keeper, allowInsecure, tlsDeclared func() bool) http.Handler {
	redirect := redirectToHTTPS(tlsDeclared)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if keeper.HasCertificate() && !allowInsecure() {
			redirect.ServeHTTP(w, r)
			return
		}
		app.ServeHTTP(w, r)
	})
}

// redirectToHTTPS sends plain-http callers to the same host and port over https, which is
// what makes one-port routing worth having: the URL the user typed keeps working.
//
// PERMANENT ONLY WHEN THE STATE IS PERMANENT — Operator ruling 2026-08-17 (quince#1157),
// amending the ruling that chose an unconditional 301.
//
//	tlsDeclared()   `config.yml` names a pair   →  301 Moved Permanently
//	!tlsDeclared()  serving TLS it did not ask for  →  307 Temporary Redirect
//
// THE 301 IS STILL WORTH HAVING AND IS NOT BEING RETREATED FROM: it is cacheable on purpose, so
// a bookmark upgrades itself once and stays upgraded. Its recorded cost was that it stays cached
// if the user later removes the certificate, and the ruling accepted that because removing one
// was a hand edit — *"turning TLS off is a config edit rather than something quince ever decides
// on its own."*
//
// `certTrial` FALSIFIED THAT CLAUSE, WHICH IS WHY THIS IS AN AMENDMENT RATHER THAN A FIX. A
// trial serves a certificate for the length of its window and then puts the previous one
// back BY ITSELF — quince deciding, on a timer, to stop serving TLS, the one event the
// ruling assumed could not happen. The trial landed after the ruling and nothing pointed
// back at it.
//
// WHAT IT COSTS THE USER IT STRANDS, since that is what decides it. Someone whose certificate
// works never sees this. It lands on the one whose certificate did NOT work — already the user
// being asked to trust the timed rollback as a safety net. The browser holds a permanent
// upgrade to an origin that stopped existing, quince answers plain http, and the failure NAMES
// NO CAUSE: from the browser's side the connection simply fails. Recovery is clearing the
// cached redirect for that host, which the product does not mention and a first-run user will
// not guess.
//
// THE PREDICATE IS THE CONFIG, NOT `is a trial running`. Those differ in the state that
// matters — a pair loaded and unconfirmed with no trial in flight — and a trial-shaped test
// would need revisiting the moment anything else can withdraw TLS.
//
// ALREADY-POISONED CACHES ARE OUT OF REACH and the ruling says so: anyone who ran a trial on an
// earlier build still holds the 301, and no server-side change reaches them.
func redirectToHTTPS(tlsDeclared func() bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// r.Host is the host:port the client actually asked for, which is what makes "same
		// URL, upgraded in place" true — including a non-default port, the normal case here
		// now that the default is :8968.
		u := *r.URL
		u.Scheme, u.Host = "https", r.Host
		code := http.StatusTemporaryRedirect
		if tlsDeclared() {
			code = http.StatusMovedPermanently
		}
		http.Redirect(w, r, u.String(), code)
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
//
// THE SETTER IS NOW CALLED UNCONDITIONALLY, AND THE EARLY RETURN MOVED BELOW IT (quince#900).
// It used to return before the setter when the opt-in was off, which was harmless at startup —
// the field's zero value is already `false` — but it made this function express only one
// direction, and the applier below shares it. A function that can only ever arm a flag is not
// something a revert can be built on.
func applyInsecureTransportOptIn(authSvc *auth.Service, cfg config.Config, w io.Writer) {
	authSvc.SetAllowInsecureTransport(cfg.Sessions.AllowInsecureTransport)
	if !cfg.Sessions.AllowInsecureTransport {
		return
	}
	_, _ = fmt.Fprint(w, "quince: sessions.allow_insecure_transport is ON — session and CSRF cookies\n"+
		"quince: will be served WITHOUT the Secure flag to plain-http clients, so they cross\n"+
		"quince: the network in clear and anyone who can read the path can sign in as you.\n"+
		"quince: This is a deliberate setting for a network you trust (a VPN, or a LAN you\n"+
		"quince: control). Turn it off in config.yml if you did not mean it.\n")
}

// subscribeInsecureTransport makes `sessions.allow_insecure_transport` LIVE — the fifth
// consumer of the config seam (qn.6g), and the half of quince#900 that is not about sockets.
//
// THE SETTING HAD TWO CONSUMERS AND MOVING ONLY ONE WOULD BE WORSE THAN MOVING NEITHER. The
// plain half now reads it per request (see plainHalf); this is the other one. A handler that
// had stopped allowing insecure transport while the auth service still thought it was allowed
// would be two halves of one security setting disagreeing, and the disagreement would show up
// as a cookie without `Secure` on a deployment that had just turned the opt-in off.
//
// IT WARNS ON THE WAY UP, AND THE WARNING IS NOT DECORATIVE. `DegradedModeWarnings` runs on
// the LOAD path only, so before this a `PUT` that switched the opt-in on returned nothing
// about it — acceptable while the write did not take effect until a restart, and not
// acceptable now that it takes effect immediately. `No silent caps or fallbacks` is the rule,
// and the applier's returned warnings are the channel that reaches the Settings form.
//
// The stderr banner is NOT re-printed. It is a startup fact — see applyInsecureTransportOptIn
// on why it writes to the stream — where this is an event, and the log line is what an event
// gets.
func subscribeInsecureTransport(cfgSvc *config.Service, authSvc *auth.Service, log *slog.Logger) {
	cfgSvc.Subscribe("sessions", func(old, next config.Config) []config.Warning {
		if old.Sessions == next.Sessions {
			return nil // an edit to some other section
		}
		authSvc.SetAllowInsecureTransport(next.Sessions.AllowInsecureTransport)
		log.Info("sessions.allow_insecure_transport applied without a restart",
			"allow_insecure_transport", next.Sessions.AllowInsecureTransport)
		return config.DegradedModeWarnings(next)
	})
}

// subscribeTLS makes `tls.cert_file` and `.key_file` LIVE — the sixth consumer of the config
// seam, and the last thing quince#900 needs to turn TLS on without a restart.
//
// The mux is already bound whether or not a certificate exists (see runHTTP), so the socket
// has stopped being what stands in the way. What was missing is anyone telling the Keeper that
// the config named a different pair — this is that.
//
// IT WARNS AND NEVER REFUSES, and the asymmetry with startup is deliberate rather than a
// weakening. `config.CheckTLS` stops the process for an unusable certificate, because starting
// on plain http for somebody who asked for https is a silent downgrade. Here the file has
// ALREADY been written — an Applier runs after the write and cannot refuse it, by design — so
// refusing would express nothing. The daemon keeps serving the certificate it already had, the
// warning rides out with the `PUT` response, and `SetFiles` keeps the new paths so the next
// handshake picks them up the moment the files become readable.
//
// SO A BAD EDIT CANNOT LOCK ANYBODY OUT, and that is the property to check if this ever
// changes. The plain half redirects on `HasCertificate()` — LOADED, not configured — so a
// config naming a certificate that does not parse leaves the plain half serving rather than
// redirecting into a handshake that cannot complete.
func subscribeTLS(cfgSvc *config.Service, keeper *tlsx.Keeper, log *slog.Logger) {
	cfgSvc.Subscribe("tls", func(old, next config.Config) []config.Warning {
		if old.TLS == next.TLS {
			return nil // an edit to some other section
		}
		if err := keeper.SetFiles(next.TLS.CertFile, next.TLS.KeyFile); err != nil {
			// ASKED AFTER THE FAILED SetFiles, WHICH IS WHAT MAKES IT THE RIGHT QUESTION: a failed
			// load keeps whatever was already loaded, so this is exactly "is there an incumbent to
			// go on serving" (quince#916 review).
			serving := keeper.HasCertificate()
			log.Warn("tls certificate could not be applied",
				"cert_file", next.TLS.CertFile, "key_file", next.TLS.KeyFile,
				"still_serving_previous", serving, "error", err)
			return []config.Warning{{
				Path:    tlsWarningPath(old.TLS, next.TLS),
				Message: "saved, but NOT applied: " + err.Error() + ". " + tlsNotAppliedRemedy(serving),
			}}
		}
		log.Info("tls certificate applied without a restart",
			"enabled", next.TLS.Enabled(), "cert_file", next.TLS.CertFile)
		return nil
	})
}

// tlsNotAppliedRemedy says what an unapplied certificate MEANS, and the two answers are not
// variations on one sentence (quince#916 review).
//
// The message this replaces said "quince is still serving the certificate it had … https is
// unchanged" in both cases. That is true of a rotation or a path change, which is what it was
// written for. It is FALSE of the ordinary first-run mistake — a typo in the path on the first
// save — where the Keeper holds nothing, there is no certificate it had, and "https is
// unchanged" means unchanged from not working at all. A user reading that would have no reason
// to think anything was wrong with their https, because they would believe they had some.
func tlsNotAppliedRemedy(serving bool) string {
	if serving {
		return "quince is still serving the certificate it had — it will pick this pair up as " +
			"soon as both files are readable, with no restart. Until then https is unchanged."
	}
	return "quince has NO certificate to serve, so https is not working — plain http still is, " +
		"and is not being redirected. It will start serving TLS as soon as both files are " +
		"readable, with no restart."
}

// tlsWarningPath names the key that actually moved, rather than always `tls.cert_file`
// (quince#916 review). Both keys reach the same loader and the same error, so the fault cannot
// be attributed between them — but WHICH ONE THE USER EDITED can be, and that is what a path on
// a warning is for.
//
// It falls back to `tls.cert_file` when both moved, which is the common case (turning TLS on or
// off sets or clears the pair) and the one where either answer is equally true.
func tlsWarningPath(old, next config.TLSConfig) string {
	if old.CertFile == next.CertFile && old.KeyFile != next.KeyFile {
		return "tls.key_file"
	}
	return "tls.cert_file"
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
	errs, _, err := cfgSvc.Replace(c, config.SourceDemoSeed) // qn.6g: no appliers are registered at seed time
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

// The three converters below all do one thing: turn a possibly-nil CONCRETE pointer into an
// interface that is HONESTLY nil.
//
// A TYPED NIL IS NOT NIL. `var p *pushsvc.Service = nil` assigned straight to an interface field
// makes that field non-nil and holding nothing, so every `!= nil` carve-out in this daemon silently
// inverts: the demo would register the notification routes, and the notifier would start with a
// deliverer that panics on its first send. `reconcileReporter` carries the same warning at its
// declaration, and these functions are what make the pattern uniform rather than remembered.
//
// THEY ARE NOT CEREMONY. The nil IS the carve-out — it is how a mode is expressed here without any
// handler asking what mode it is running in — so the nil has to be real for the design to hold.

// notificationReader hands the router the push service, or nothing at all in demo mode (qn.12).
func notificationReader(s *pushsvc.Service) httpapi.NotificationReader {
	if s == nil {
		return nil
	}
	return s
}

// pushDeliverer hands the notifier the same service as a `notify.Deliverer` (qn.12).
func pushDeliverer(s *pushsvc.Service) notify.Deliverer {
	if s == nil {
		return nil
	}
	return s
}

// engineJobs hands the notifier the backup engine's per-device busy check (qn.12). The engine is nil
// when no muxer is configured, and a daemon with no muxer has no live device to remind about.
func engineJobs(e *backup.Engine) notify.Jobs {
	if e == nil {
		return nil
	}
	return e
}
