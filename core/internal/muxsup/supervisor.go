// Package muxsup is quince's in-container muxer supervisor (design §1/§2, ruled 2026-07-20
// decisions log (ar); MINIMAL scope in qn.2b). With devices.manage_muxer: true (the SIMPLE
// one-container profile) it owns the usbmuxd lifecycle: a supervised subprocess in its own
// process group under the serve context, restarted on crash with capped backoff, killed on
// shutdown — and it refuses loudly at startup if something already serves the socket (no
// silent adoption). It powers POST /api/devices/rescan by restarting the daemon, which
// re-enumerates USB devices an unprivileged container's absent hotplug never delivered; the
// muxd client's existing reconnect→Reset→replay reconcile does the rest (no new device-table
// code). netmuxd co-supervision, a restart policy, and live UI health are FULL scope (qn.7).
package muxsup

import (
	"context"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"
)

// Muxer supervision states surfaced to /api/health (contracts §10-adjacent; MINIMAL field).
const (
	StateStarting = "starting" // constructed, not yet running a child
	StateRunning  = "running"  // a usbmuxd child is up
	StateDegraded = "degraded" // socket externally served, or the child is crash-looping
	StateStopped  = "stopped"  // serve context cancelled; no child
)

const (
	defaultBackoffMin  = 500 * time.Millisecond
	defaultBackoffMax  = 30 * time.Second
	defaultTermGrace   = 3 * time.Second        // SIGTERM → grace → SIGKILL on the process group
	defaultProbe       = 300 * time.Millisecond // refuse-loudly probe-dial timeout
	defaultHealthyRun  = 30 * time.Second       // a child up this long resets the crash counter
	crashLoopThreshold = 3                      // consecutive fast exits → degraded in health
)

// Status is the supervisor's view for /api/health: whether quince manages the muxer, the
// current state, and a human detail (last exit reason / why degraded).
type Status struct {
	Managed bool
	State   string
	Detail  string
}

type rescanReq struct{ resp chan RescanResult }

// RescanResult is the outcome of a rescan trigger: Accepted → HTTP 202; else 409 with Reason.
type RescanResult struct {
	Accepted bool
	Reason   string
}

// Supervisor owns one managed usbmuxd. Construct with New; run its lifecycle with Run under
// the serve context; trigger a re-enumeration with Rescan; read health with MuxerStatus.
type Supervisor struct {
	socket string   // devices.usbmuxd_socket — the daemon's -S listen path (authoritative)
	name   string   // the muxer binary (usbmuxd); overridable in tests
	args   []string // its argv; default: -f (foreground) -S <socket>
	env    []string // extra child env (nil → inherit); tests use it for the helper-process fake
	log    *slog.Logger

	backoffMin, backoffMax, grace, probe, healthyRun time.Duration

	rescan chan rescanReq
	mu     sync.Mutex
	st     Status
}

// New returns a Supervisor for the usbmuxd listening at socket. The child is invoked as
// `usbmuxd -f -S <socket>`: -f keeps it a supervised foreground child (never self-daemonising),
// and -S makes devices.usbmuxd_socket authoritative — verified live against `usbmuxd --help`
// (1.1.1_git20250201 ships -S/--socket), not assumed from the client-side env (qn.2b amendment 1).
func New(socket string, log *slog.Logger) *Supervisor {
	return &Supervisor{
		socket:     socket,
		name:       "usbmuxd",
		args:       []string{"-f", "-S", socket},
		log:        log,
		backoffMin: defaultBackoffMin,
		backoffMax: defaultBackoffMax,
		grace:      defaultTermGrace,
		probe:      defaultProbe,
		healthyRun: defaultHealthyRun,
		rescan:     make(chan rescanReq),
		st:         Status{Managed: true, State: StateStarting},
	}
}

// MuxerStatus reports (managed, state, detail) for /api/health. Safe for concurrent use.
func (s *Supervisor) MuxerStatus() (managed bool, state, detail string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.st.Managed, s.st.State, s.st.Detail
}

func (s *Supervisor) set(state, detail string) {
	s.mu.Lock()
	s.st.State, s.st.Detail = state, detail
	s.mu.Unlock()
}

// Rescan triggers a managed re-enumeration and blocks for the outcome (bounded by ctx):
// accepted (→ HTTP 202) when the supervisor restarts/takes over the daemon; !accepted (→ 409)
// with a reason when the socket is still externally served. Returns primitives so httpapi's
// consumer-defined MuxerControl needs no import of this package. The HTTP handler passes a
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
// socket, otherwise spawns and supervises usbmuxd — restarting on crash with capped backoff,
// flipping to degraded on a crash loop, and killing the process group on shutdown or rescan.
func (s *Supervisor) Run(ctx context.Context) {
	backoff := s.backoffMin
	crashes := 0
	for {
		if ctx.Err() != nil {
			s.set(StateStopped, "")
			return
		}

		// Refuse loudly: never start a second daemon over a socket someone already serves.
		if s.probeServed() {
			s.set(StateDegraded, "socket "+s.socket+" is already served by another process")
			s.log.Error("muxsup: refusing to start — socket already served (set devices.manage_muxer: false if an external muxer is intended)",
				"socket", s.socket)
			req, ok := s.waitRescan(ctx)
			if !ok {
				s.set(StateStopped, "")
				return
			}
			if s.probeServed() { // still served → stay degraded, report why
				req.resp <- RescanResult{Reason: "socket " + s.socket + " is still served by an external process"}
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
				s.set(StateDegraded, "usbmuxd keeps exiting: "+reason)
				s.log.Error("muxsup: usbmuxd is crash-looping", "reason", reason, "restarts", crashes)
			} else {
				s.log.Warn("muxsup: usbmuxd exited; restarting", "reason", reason, "restarts", crashes)
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

// superviseChild starts one usbmuxd and blocks until it exits, a rescan arrives, or ctx is
// cancelled. On rescan/cancel it kills the whole process group (SIGTERM → grace → SIGKILL).
// The returned reason (for outcomeExited) and *rescanReq (for outcomeRescan) feed Run.
func (s *Supervisor) superviseChild(ctx context.Context) (oc outcome, reason string, ranFor time.Duration, req *rescanReq) {
	cmd := exec.Command(s.name, s.args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true} // own group → the whole tree is reapable
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr         // usbmuxd -f logs to stderr → container logs
	if s.env != nil {
		cmd.Env = append(os.Environ(), s.env...)
	}
	if err := cmd.Start(); err != nil {
		return outcomeExited, "start failed: " + err.Error(), 0, nil
	}
	started := time.Now()
	s.set(StateRunning, "")
	s.log.Info("muxsup: usbmuxd started", "pid", cmd.Process.Pid, "socket", s.socket)

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

// probeServed reports whether the unix socket is already accepting connections (something else
// is serving it). A dial error (including a stale socket file with no listener) means free.
func (s *Supervisor) probeServed() bool {
	c, err := net.DialTimeout("unix", s.socket, s.probe)
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
