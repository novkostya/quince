// Package muxsup is quince's in-container muxer supervisor (design §1/§2, ruled 2026-07-20
// decisions log (ar); generalized to co-supervise netmuxd in qn.4c, (by)/(bz)). With
// devices.manage_muxer: true (the SIMPLE one-container profile) it owns the lifecycle of every
// muxer daemon quince is configured to reach — usbmuxd for USB, netmuxd for Wi-Fi: each a
// supervised subprocess in its own process group under the serve context, restarted on crash with
// capped backoff, killed on shutdown — and it refuses loudly at startup if something already
// serves that daemon's address (no silent adoption). It powers POST /api/devices/rescan by
// restarting the USB daemon, which re-enumerates USB devices an unprivileged container's absent
// hotplug never delivered; the muxd client's existing reconnect→Reset→replay reconcile does the
// rest (no new device-table code). Rescan is deliberately USB-only: restarting netmuxd would tear
// a live Wi-Fi backup (ruled (bz)). Restart-policy config, liveness tuning and a live UI
// muxer-health panel remain qn.7.
package muxsup

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Muxer supervision states surfaced to /api/health (design §10).
const (
	StateStarting = "starting" // constructed, not yet running a child
	StateRunning  = "running"  // the daemon's child is up
	StateDegraded = "degraded" // address externally served, or the child is crash-looping
	StateStopped  = "stopped"  // serve context cancelled; no child
	StateExternal = "external" // not managed by quince — dialed only (manage_muxer: false)
)

// Transport roles a muxer serves (design §3 transports).
const (
	RoleUSB  = "usb"
	RoleWiFi = "wifi"
)

const (
	defaultBackoffMin  = 500 * time.Millisecond
	defaultBackoffMax  = 30 * time.Second
	defaultTermGrace   = 3 * time.Second        // SIGTERM → grace → SIGKILL on the process group
	defaultProbe       = 300 * time.Millisecond // refuse-loudly probe-dial timeout
	defaultHealthyRun  = 30 * time.Second       // a child up this long resets the crash counter
	crashLoopThreshold = 3                      // consecutive fast exits → degraded in health
)

// DefaultNetmuxdSocket is the private unix socket the managed netmuxd listens on. It is
// deliberately NOT usbmuxd's path: netmuxd DELETES whatever socket --socket-path names and binds
// its own — verified against the shipped v0.4.3 binary, where pointing it at a live usbmuxd's
// socket left usbmuxd running with its inode gone (a silent USB blackout). See Netmuxd.
const DefaultNetmuxdSocket = "/var/run/netmuxd"

// Spec describes one supervised muxer daemon: how to run it, how to tell whether its address is
// already served, and how to describe it in /api/health. Build one with Usbmuxd or Netmuxd —
// their argv is verified live against the shipped binaries, never remembered (hard rule).
type Spec struct {
	Name    string   // daemon name: health label + log prefix ("usbmuxd", "netmuxd")
	Role    string   // the transport it serves (RoleUSB / RoleWiFi)
	Bin     string   // binary to exec
	Args    []string // argv after Bin (argv array, never a shell string)
	Env     []string // extra child env (appended to the inherited environment)
	Network string   // probe network: "unix" or "tcp"
	Address string   // probe address: socket path (unix) or host:port (tcp) — authoritative
	Rescan  bool     // whether POST /api/devices/rescan restarts this daemon (USB only)
}

// Usbmuxd is the spec for the in-container usbmuxd listening at socket. The child is invoked as
// `usbmuxd -f -S <socket>`: -f keeps it a supervised foreground child (never self-daemonising),
// and -S makes devices.usbmuxd_socket authoritative — verified live against `usbmuxd --help`
// (1.1.1_git20250201 ships -S/--socket), not assumed from the client-side env (qn.2b amendment 1).
func Usbmuxd(socket string) Spec {
	return Spec{
		Name:    "usbmuxd",
		Role:    RoleUSB,
		Bin:     "usbmuxd",
		Args:    []string{"-f", "-S", socket},
		Network: "unix",
		Address: socket,
		Rescan:  true, // rescan exists for USB hotplug the container never receives
	}
}

// Netmuxd is the spec for the in-container netmuxd serving Wi-Fi at addr (host:port, from
// devices.netmuxd_addr) with its own private unix socket at socketPath. Every flag is verified
// live against the shipped, pinned v0.4.3 binary (qn.4c; rung-ruled, ratified (bz)):
//
//   - --host/--port split from addr, so devices.netmuxd_addr is authoritative (the -S discipline).
//   - --socket-path keeps netmuxd OFF usbmuxd's socket. netmuxd deletes and rebinds whatever path
//     it is given (observed: "Deleting old Unix socket" over a live usbmuxd = silent USB blackout),
//     so the default /var/run/usbmuxd is never acceptable here. --disable-unix would also avoid the
//     collision but puts netmuxd in "host mode", where it depends on another unix-mode daemon being
//     alive — coupling Wi-Fi health to USB health, exactly backwards for two independent transports.
//   - --disable-usb: stack D2 makes usbmuxd the USB anchor until qn.7's netmuxd-USB audition;
//     without it both daemons claim the same USB device. This is the one flag the single-muxer
//     flip removes.
//   - RUST_LOG=info (only when unset) makes netmuxd's discovery/pairing/heartbeat lines visible in
//     container logs; it is silent below "error" otherwise.
func Netmuxd(addr, socketPath string) (Spec, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return Spec{}, err
	}
	if host == "" || port == "" {
		return Spec{}, errors.New("netmuxd address needs both host and port")
	}
	if socketPath == "" {
		return Spec{}, errors.New("netmuxd needs a private unix socket path")
	}
	s := Spec{
		Name:    "netmuxd",
		Role:    RoleWiFi,
		Bin:     "netmuxd",
		Args:    []string{"--host", host, "--port", port, "--socket-path", socketPath, "--disable-usb"},
		Network: "tcp",
		Address: addr,
		Rescan:  false, // restarting netmuxd would tear a live Wi-Fi backup ((bz))
	}
	if os.Getenv("RUST_LOG") == "" {
		s.Env = []string{"RUST_LOG=info"}
	}
	return s, nil
}

// SocketPathFor derives the managed netmuxd's private socket path: alongside the usbmuxd socket
// (so one bind-mounted /var/run serves both), else the default. The caller must still reject a
// path equal to the usbmuxd socket — see the collision guard in the serve wiring.
func SocketPathFor(usbmuxdSocket string) string {
	if usbmuxdSocket == "" {
		return DefaultNetmuxdSocket
	}
	return filepath.Join(filepath.Dir(usbmuxdSocket), "netmuxd")
}

// Status is one muxer's view for /api/health: which daemon, the transport it serves, whether
// quince manages it, its state, a human detail (last exit reason / why degraded), and whether
// rescan applies to it.
type Status struct {
	Name    string
	Role    string
	Managed bool
	State   string
	Detail  string
	Rescan  bool
}

type rescanReq struct{ resp chan RescanResult }

// RescanResult is the outcome of a rescan trigger: Accepted → HTTP 202; else 409 with Reason.
type RescanResult struct {
	Accepted bool
	Reason   string
}

// Supervisor owns one managed muxer daemon. Construct with New; run its lifecycle with Run under
// the serve context; trigger a re-enumeration with Rescan; read health with Status.
type Supervisor struct {
	spec Spec
	log  *slog.Logger

	backoffMin, backoffMax, grace, probe, healthyRun time.Duration

	rescan chan rescanReq
	mu     sync.Mutex
	st     Status
}

// New returns a Supervisor for the daemon described by spec.
func New(spec Spec, log *slog.Logger) *Supervisor {
	return &Supervisor{
		spec:       spec,
		log:        log,
		backoffMin: defaultBackoffMin,
		backoffMax: defaultBackoffMax,
		grace:      defaultTermGrace,
		probe:      defaultProbe,
		healthyRun: defaultHealthyRun,
		rescan:     make(chan rescanReq),
		st: Status{
			Name: spec.Name, Role: spec.Role, Managed: true,
			State: StateStarting, Rescan: spec.Rescan,
		},
	}
}

// Status reports this daemon's health slice. Safe for concurrent use.
func (s *Supervisor) Status() Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.st
}

func (s *Supervisor) set(state, detail string) {
	s.mu.Lock()
	s.st.State, s.st.Detail = state, detail
	s.mu.Unlock()
}

// Rescan triggers a managed re-enumeration and blocks for the outcome (bounded by ctx):
// accepted (→ HTTP 202) when the supervisor restarts/takes over the daemon; !accepted (→ 409)
// with a reason when the address is still externally served. The HTTP handler passes a
// short-timeout ctx so a wedged supervisor can't hang the request.
func (s *Supervisor) Rescan(ctx context.Context) (accepted bool, reason string) {
	req := rescanReq{resp: make(chan RescanResult, 1)}
	select {
	case s.rescan <- req:
	case <-ctx.Done():
		return false, "rescan request timed out"
	}
	select {
	case r := <-req.resp:
		return r.Accepted, r.Reason
	case <-ctx.Done():
		return false, "rescan request timed out"
	}
}

// Run drives the supervision loop until ctx is cancelled. It probe-refuses an already-served
// address, otherwise spawns and supervises the daemon — restarting on crash with capped backoff,
// flipping to degraded on a crash loop, and killing the process group on shutdown or rescan.
func (s *Supervisor) Run(ctx context.Context) {
	backoff := s.backoffMin
	crashes := 0
	for {
		if ctx.Err() != nil {
			s.set(StateStopped, "")
			return
		}

		// Refuse loudly: never start a second daemon over an address someone already serves.
		if s.probeServed() {
			s.set(StateDegraded, s.spec.Address+" is already served by another process")
			s.log.Error("muxsup: refusing to start — address already served (set devices.manage_muxer: false if an external muxer is intended)",
				"daemon", s.spec.Name, "address", s.spec.Address)
			req, ok := s.waitRescan(ctx)
			if !ok {
				s.set(StateStopped, "")
				return
			}
			if s.probeServed() { // still served → stay degraded, report why
				req.resp <- RescanResult{Reason: s.spec.Address + " is still served by an external process"}
				continue
			}
			req.resp <- RescanResult{Accepted: true} // freed up → take over
			backoff, crashes = s.backoffMin, 0
		}

		oc, reason, ranFor, req := s.superviseChild(ctx)
		switch oc {
		case outcomeStopped:
			s.set(StateStopped, "")
			return
		case outcomeRescan:
			req.resp <- RescanResult{Accepted: true} // restart → re-enumerate
			backoff, crashes = s.backoffMin, 0
			continue
		case outcomeExited:
			if ranFor >= s.healthyRun {
				crashes = 0 // it stayed up a healthy while → not a crash loop, just a restart
			}
			crashes++
			if crashes >= crashLoopThreshold {
				s.set(StateDegraded, s.spec.Name+" keeps exiting: "+reason)
				s.log.Error("muxsup: daemon is crash-looping", "daemon", s.spec.Name, "reason", reason, "restarts", crashes)
			} else {
				s.log.Warn("muxsup: daemon exited; restarting", "daemon", s.spec.Name, "reason", reason, "restarts", crashes)
			}
			switch s.sleepOrRescan(ctx, backoff) {
			case waitStopped:
				s.set(StateStopped, "")
				return
			case waitRescan:
				backoff, crashes = s.backoffMin, 0
			case waitElapsed:
				backoff = nextBackoff(backoff, s.backoffMax)
			}
		}
	}
}

type outcome int

const (
	outcomeExited  outcome = iota // the child exited on its own (crash)
	outcomeRescan                 // a rescan asked for a restart
	outcomeStopped                // serve context cancelled
)

// superviseChild starts one daemon and blocks until it exits, a rescan arrives, or ctx is
// cancelled. On rescan/cancel it kills the whole process group (SIGTERM → grace → SIGKILL).
// The returned reason (for outcomeExited) and *rescanReq (for outcomeRescan) feed Run.
func (s *Supervisor) superviseChild(ctx context.Context) (oc outcome, reason string, ranFor time.Duration, req *rescanReq) {
	cmd := exec.Command(s.spec.Bin, s.spec.Args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true} // own group → the whole tree is reapable
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr         // daemons log to stdout/stderr → container logs
	if s.spec.Env != nil {
		cmd.Env = append(os.Environ(), s.spec.Env...)
	}
	if err := cmd.Start(); err != nil {
		return outcomeExited, "start failed: " + err.Error(), 0, nil
	}
	started := time.Now()
	s.set(StateRunning, "")
	s.log.Info("muxsup: daemon started", "daemon", s.spec.Name, "pid", cmd.Process.Pid, "address", s.spec.Address)

	waitCh := make(chan error, 1)
	go func() { waitCh <- cmd.Wait() }()

	select {
	case <-ctx.Done():
		s.terminate(cmd, waitCh)
		return outcomeStopped, "", time.Since(started), nil
	case r := <-s.rescan:
		s.terminate(cmd, waitCh)
		return outcomeRescan, "", time.Since(started), &r
	case err := <-waitCh:
		return outcomeExited, exitReason(err), time.Since(started), nil
	}
}

// terminate signals the child's process group (SIGTERM, then SIGKILL after the grace period)
// and drains its Wait. Killing by negative pgid reaps any grandchildren too (design §1).
func (s *Supervisor) terminate(cmd *exec.Cmd, waitCh <-chan error) {
	if cmd.Process == nil {
		return
	}
	pgid := cmd.Process.Pid // == pgid because Setpgid put the child at the head of a new group
	_ = syscall.Kill(-pgid, syscall.SIGTERM)
	select {
	case <-waitCh:
	case <-time.After(s.grace):
		_ = syscall.Kill(-pgid, syscall.SIGKILL)
		<-waitCh
	}
}

// probeServed reports whether the daemon's address is already accepting connections (something
// else is serving it) — a unix socket path or a TCP host:port, per the spec's network. A dial
// error (including a stale socket file with no listener, or a closed port) means free.
func (s *Supervisor) probeServed() bool {
	c, err := net.DialTimeout(s.spec.Network, s.spec.Address, s.probe)
	if err != nil {
		return false
	}
	_ = c.Close()
	return true
}

type waitResult int

const (
	waitElapsed waitResult = iota
	waitRescan
	waitStopped
)

// sleepOrRescan waits out a backoff, but a rescan cuts it short (accepted → immediate restart)
// and ctx cancellation ends the loop.
func (s *Supervisor) sleepOrRescan(ctx context.Context, d time.Duration) waitResult {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return waitStopped
	case <-t.C:
		return waitElapsed
	case req := <-s.rescan:
		req.resp <- RescanResult{Accepted: true}
		return waitRescan
	}
}

// waitRescan blocks in the degraded/refused state for a rescan (which re-probes) or ctx done.
func (s *Supervisor) waitRescan(ctx context.Context) (rescanReq, bool) {
	select {
	case <-ctx.Done():
		return rescanReq{}, false
	case req := <-s.rescan:
		return req, true
	}
}

func nextBackoff(d, max time.Duration) time.Duration {
	d *= 2
	if d > max {
		return max
	}
	return d
}

func exitReason(err error) string {
	if err == nil {
		return "exited cleanly (code 0)"
	}
	return err.Error() // e.g. "exit status 1", "signal: killed"
}

// --- the group -------------------------------------------------------------------------------

// Group is every muxer quince knows about: the supervised children plus dial-only entries
// (external muxers under manage_muxer: false), so /api/health reports each daemon honestly
// instead of one ambiguous aggregate (clean break ruled (bz)). It satisfies the httpapi
// MuxerControl seam structurally.
type Group struct {
	sups      []*Supervisor
	unmanaged []Status
}

// NewGroup returns an empty group.
func NewGroup() *Group { return &Group{} }

// Supervise adds a managed daemon (started by Run).
func (g *Group) Supervise(s *Supervisor) { g.sups = append(g.sups, s) }

// AddUnmanaged records a daemon quince only dials (external / manage_muxer: false), so health
// still shows it — with managed:false, so nobody reads an absent entry as "no muxer".
func (g *Group) AddUnmanaged(name, role, address string) {
	g.unmanaged = append(g.unmanaged, Status{
		Name: name, Role: role, Managed: false, State: StateExternal,
		Detail: address + " is served by an external muxer — quince does not own it",
	})
}

// Run supervises every managed daemon until ctx is cancelled, returning when all have stopped.
func (g *Group) Run(ctx context.Context) {
	var wg sync.WaitGroup
	for _, s := range g.sups {
		wg.Add(1)
		go func(s *Supervisor) { defer wg.Done(); s.Run(ctx) }(s)
	}
	wg.Wait()
}

// Statuses reports every known daemon, managed first, in configuration order.
func (g *Group) Statuses() []Status {
	out := make([]Status, 0, len(g.sups)+len(g.unmanaged))
	for _, s := range g.sups {
		out = append(out, s.Status())
	}
	return append(out, g.unmanaged...)
}

// Rescan restarts the daemon rescan applies to — the USB muxer, whose restart re-enumerates
// devices an unprivileged container's absent hotplug missed. netmuxd is deliberately excluded:
// restarting it would tear a live Wi-Fi backup ((bz)). With no such managed daemon the caller
// gets a 409 with the honest reason.
func (g *Group) Rescan(ctx context.Context) (accepted bool, reason string) {
	for _, s := range g.sups {
		if s.spec.Rescan {
			return s.Rescan(ctx)
		}
	}
	if len(g.unmanaged) > 0 {
		return false, "the USB muxer is external (devices.manage_muxer: false) — quince does not own it"
	}
	return false, "no managed USB muxer to restart"
}

// Names lists the configured daemons for logging ("usbmuxd, netmuxd").
func (g *Group) Names() string {
	names := make([]string, 0, len(g.sups))
	for _, s := range g.sups {
		names = append(names, s.spec.Name)
	}
	return strings.Join(names, ", ")
}
