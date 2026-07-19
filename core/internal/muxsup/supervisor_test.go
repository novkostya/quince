package muxsup

import (
	"context"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// The supervisor spawns a real subprocess; in CI that subprocess is THIS test binary re-exec'd
// as a fake "usbmuxd" (the stdlib GO_WANT_HELPER_PROCESS pattern — same discipline as the fake
// muxd socket in package muxd). No real muxer or device is involved.

func testLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// fakeSupervisor builds a Supervisor whose "usbmuxd" is this test binary running
// TestHelperProcess in the given mode, with fast timers so lifecycle tests finish in ms.
func fakeSupervisor(socket, mode string, startsFile string) *Supervisor {
	s := New(socket, testLog())
	s.name = os.Args[0]
	s.args = []string{"-test.run=TestHelperProcess", "--", "-f", "-S", socket}
	s.env = []string{"GO_WANT_HELPER_PROCESS=1", "MUXSUP_HELPER_MODE=" + mode}
	if startsFile != "" {
		s.env = append(s.env, "MUXSUP_HELPER_STARTS="+startsFile)
	}
	s.backoffMin = 2 * time.Millisecond
	s.backoffMax = 10 * time.Millisecond
	s.grace = 500 * time.Millisecond
	s.probe = 150 * time.Millisecond
	s.healthyRun = time.Hour // never reset the crash counter during a test
	return s
}

func waitUntil(t *testing.T, cond func() bool, timeout time.Duration, msg string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", msg)
}

func dialable(socket string) bool {
	c, err := net.DialTimeout("unix", socket, 100*time.Millisecond)
	if err != nil {
		return false
	}
	_ = c.Close()
	return true
}

func stateOf(s *Supervisor) string {
	_, st, _ := s.MuxerStatus()
	return st
}

// TestSupervisorStartsAndStops: with a free socket, the supervisor spawns the child (which
// listens), reports running, and on ctx cancel kills it — after which the socket is dead.
func TestSupervisorStartsAndStops(t *testing.T) {
	socket := filepath.Join(t.TempDir(), "m.sock")
	s := fakeSupervisor(socket, "serve", "")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); s.Run(ctx) }()

	waitUntil(t, func() bool { return dialable(socket) }, 3*time.Second, "child to serve the socket")
	if stateOf(s) != StateRunning {
		t.Fatalf("state = %q, want running", stateOf(s))
	}
	if managed, _, _ := s.MuxerStatus(); !managed {
		t.Fatal("MuxerStatus reports unmanaged; want managed")
	}

	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not return after ctx cancel")
	}
	if stateOf(s) != StateStopped {
		t.Fatalf("final state = %q, want stopped", stateOf(s))
	}
	if dialable(socket) {
		t.Fatal("socket still served after shutdown — child not killed")
	}
}

// TestSupervisorCrashLoopDegrades: a child that keeps exiting non-zero is restarted with
// backoff and, past the threshold, flips the muxer to degraded with the exit reason — proving
// both restart-after-crash and crash-loop honesty (amendment 4a).
func TestSupervisorCrashLoopDegrades(t *testing.T) {
	socket := filepath.Join(t.TempDir(), "m.sock")
	s := fakeSupervisor(socket, "crash", "")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { defer close(done); s.Run(ctx) }()

	waitUntil(t, func() bool { return stateOf(s) == StateDegraded }, 5*time.Second, "crash loop to degrade")
	if _, _, detail := s.MuxerStatus(); detail == "" {
		t.Fatal("degraded status has no detail (want the last exit reason)")
	}
	cancel()
	<-done
}

// TestSupervisorRefusesServedSocket (story 3): a socket already served at startup is never
// taken over by a second daemon — the supervisor goes degraded without spawning. Then, once
// the external server goes away, Rescan re-probes and takes over (amendment 4b: rescan is the
// recovery path out of degraded).
func TestSupervisorRefusesServedSocket(t *testing.T) {
	socket := filepath.Join(t.TempDir(), "m.sock")
	external, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatalf("pre-occupy socket: %v", err)
	}
	acceptDone := make(chan struct{})
	go func() { // drain accepts so DialTimeout in probeServed succeeds
		defer close(acceptDone)
		for {
			c, err := external.Accept()
			if err != nil {
				return
			}
			_ = c.Close()
		}
	}()

	s := fakeSupervisor(socket, "serve", "")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); s.Run(ctx) }()

	waitUntil(t, func() bool { return stateOf(s) == StateDegraded }, 3*time.Second, "refuse-loudly degrade")
	// It must NOT have spawned a competing daemon: give it a beat, stay degraded.
	time.Sleep(80 * time.Millisecond)
	if stateOf(s) != StateDegraded {
		t.Fatalf("state = %q after refusing; want it to stay degraded (no spawn)", stateOf(s))
	}

	// A rescan while still externally served is refused with a reason (409).
	if accepted, reason := s.Rescan(rescanCtx(t)); accepted || reason == "" {
		t.Fatalf("rescan while served = (accepted=%v, reason=%q); want !accepted with a reason", accepted, reason)
	}

	// External server leaves → the socket frees → rescan takes over (202) and the child serves.
	_ = external.Close()
	<-acceptDone
	_ = os.Remove(socket)
	if accepted, reason := s.Rescan(rescanCtx(t)); !accepted {
		t.Fatalf("rescan after socket freed = (accepted=%v, reason=%q); want accepted (takeover)", accepted, reason)
	}
	waitUntil(t, func() bool { return stateOf(s) == StateRunning && dialable(socket) }, 3*time.Second, "takeover after recovery")

	cancel()
	<-done
}

// TestSupervisorRescanRestarts (story 4): rescan on a healthy managed muxer restarts the
// daemon (a second start is recorded) so USB devices re-enumerate; the restart drives the muxd
// client's reconnect→Reset→replay reconcile (proven in package muxd/device).
func TestSupervisorRescanRestarts(t *testing.T) {
	dir := t.TempDir()
	socket := filepath.Join(dir, "m.sock")
	starts := filepath.Join(dir, "starts")
	s := fakeSupervisor(socket, "serve", starts)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); s.Run(ctx) }()

	waitUntil(t, func() bool { return startCount(starts) >= 1 && dialable(socket) }, 3*time.Second, "first start")

	if accepted, reason := s.Rescan(rescanCtx(t)); !accepted {
		t.Fatalf("rescan = (accepted=%v, reason=%q); want accepted (202)", accepted, reason)
	}
	waitUntil(t, func() bool { return startCount(starts) >= 2 }, 3*time.Second, "restart after rescan")
	waitUntil(t, func() bool { return dialable(socket) }, 3*time.Second, "socket served again post-restart")

	cancel()
	<-done
}

func rescanCtx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func startCount(path string) int {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	return len(b)
}

// TestHelperProcess is the fake usbmuxd. It is inert unless re-exec'd by the supervisor with
// GO_WANT_HELPER_PROCESS=1. MUXSUP_HELPER_MODE selects behaviour; MUXSUP_HELPER_STARTS (if set)
// gets one byte appended per start so a test can count restarts.
func TestHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
		return
	}
	if p := os.Getenv("MUXSUP_HELPER_STARTS"); p != "" {
		if f, err := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644); err == nil {
			_, _ = f.Write([]byte("x"))
			_ = f.Close()
		}
	}
	switch os.Getenv("MUXSUP_HELPER_MODE") {
	case "crash":
		os.Exit(1)
	case "serve":
		socket := socketArg(os.Args)
		_ = os.Remove(socket) // unlink any stale socket, like real usbmuxd
		ln, err := net.Listen("unix", socket)
		if err != nil {
			os.Exit(2)
		}
		for {
			c, err := ln.Accept()
			if err != nil {
				os.Exit(0)
			}
			_ = c.Close()
		}
	default:
		os.Exit(0)
	}
}

func socketArg(args []string) string {
	for i, a := range args {
		if a == "-S" && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}
