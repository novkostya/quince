package muxsup

import (
	"context"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The supervisor spawns a real subprocess; in CI that subprocess is THIS test binary re-exec'd
// as a fake muxer daemon (the stdlib GO_WANT_HELPER_PROCESS pattern — same discipline as the fake
// muxd socket in package muxd). No real muxer or device is involved. Since qn.4c the fake can
// serve a TCP listener as well as a unix socket, so netmuxd's supervision is proven the same way
// usbmuxd's is.

func testLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// fakeSpec builds a Spec whose daemon is this test binary running TestHelperProcess in the given
// mode, listening on network/address (the same address the supervisor probes).
func fakeSpec(name, role, network, address, mode string) Spec {
	return Spec{
		Name:    name,
		Role:    role,
		Bin:     os.Args[0],
		Args:    []string{"-test.run=TestHelperProcess", "--"},
		Env:     []string{"GO_WANT_HELPER_PROCESS=1", "MUXSUP_HELPER_MODE=" + mode, "MUXSUP_HELPER_LISTEN=" + network + ":" + address},
		Network: network,
		Address: address,
		Rescan:  role == RoleUSB,
	}
}

// fakeSupervisor wires a Spec with fast timers so lifecycle tests finish in ms.
func fakeSupervisor(spec Spec, startsFile string) *Supervisor {
	if startsFile != "" {
		spec.Env = append(spec.Env, "MUXSUP_HELPER_STARTS="+startsFile)
	}
	s := New(spec, testLog())
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

func dialable(network, address string) bool {
	c, err := net.DialTimeout(network, address, 150*time.Millisecond)
	if err != nil {
		return false
	}
	_ = c.Close()
	return true
}

func stateOf(s *Supervisor) string { return s.Status().State }

// freePort returns a 127.0.0.1 host:port nothing is listening on right now.
func freePort(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve port: %v", err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	return addr
}

// --- story 2: the verified interface facts, locked into a test ---------------------------------

// TestUsbmuxdSpec pins qn.2b's verified invocation: -f (supervised foreground child) and -S
// (devices.usbmuxd_socket is authoritative), a unix probe, and rescan applying to it.
func TestUsbmuxdSpec(t *testing.T) {
	got := Usbmuxd("/var/run/usbmuxd")
	if got.Bin != "usbmuxd" || strings.Join(got.Args, " ") != "-f -S /var/run/usbmuxd" {
		t.Fatalf("argv = %q %q; want usbmuxd -f -S /var/run/usbmuxd", got.Bin, got.Args)
	}
	if got.Network != "unix" || got.Address != "/var/run/usbmuxd" {
		t.Fatalf("probe = %s://%s; want unix:///var/run/usbmuxd", got.Network, got.Address)
	}
	if got.Role != RoleUSB || !got.Rescan {
		t.Fatalf("role/rescan = %q/%v; want usb/true", got.Role, got.Rescan)
	}
}

// TestNetmuxdSpec pins the qn.4c-verified invocation (ratified (bz)). Every flag here was run
// against the shipped, pinned v0.4.3 binary: --host/--port make devices.netmuxd_addr
// authoritative, --socket-path keeps netmuxd off usbmuxd's socket (which it would DELETE and
// rebind), --disable-usb keeps usbmuxd the USB anchor (stack D2). A drift in this argv is a
// silent USB blackout or a dead Wi-Fi transport, so it is asserted exactly.
func TestNetmuxdSpec(t *testing.T) {
	got, err := Netmuxd("127.0.0.1:27015", "/var/run/netmuxd")
	if err != nil {
		t.Fatalf("Netmuxd: %v", err)
	}
	want := "--host 127.0.0.1 --port 27015 --socket-path /var/run/netmuxd --disable-usb"
	if got.Bin != "netmuxd" || strings.Join(got.Args, " ") != want {
		t.Fatalf("argv = %q %q; want netmuxd %s", got.Bin, got.Args, want)
	}
	if got.Network != "tcp" || got.Address != "127.0.0.1:27015" {
		t.Fatalf("probe = %s://%s; want tcp://127.0.0.1:27015", got.Network, got.Address)
	}
	if got.Role != RoleWiFi || got.Rescan {
		t.Fatalf("role/rescan = %q/%v; want wifi/false (rescan must never restart netmuxd)", got.Role, got.Rescan)
	}
	for _, a := range got.Args { // the collision-prone default must never appear
		if a == "/var/run/usbmuxd" {
			t.Fatal("netmuxd argv points at usbmuxd's socket — it would delete and rebind it")
		}
	}
	if os.Getenv("RUST_LOG") == "" && strings.Join(got.Env, " ") != "RUST_LOG=info" {
		t.Fatalf("env = %q; want RUST_LOG=info so netmuxd's discovery lines reach container logs", got.Env)
	}
}

// TestNetmuxdSpecRejectsBadAddress: an address that is not host:port is a loud error, never a
// daemon spawned against a guess.
func TestNetmuxdSpecRejectsBadAddress(t *testing.T) {
	for _, addr := range []string{"", "127.0.0.1", "no-port:"} {
		if _, err := Netmuxd(addr, "/var/run/netmuxd"); err == nil {
			t.Fatalf("Netmuxd(%q) = nil error; want a rejection", addr)
		}
	}
	if _, err := Netmuxd("127.0.0.1:27015", ""); err == nil {
		t.Fatal("Netmuxd with no socket path = nil error; want a rejection")
	}
}

func TestSocketPathFor(t *testing.T) {
	if got := SocketPathFor("/var/run/usbmuxd"); got != "/var/run/netmuxd" {
		t.Fatalf("SocketPathFor = %q; want /var/run/netmuxd", got)
	}
	if got := SocketPathFor(""); got != DefaultNetmuxdSocket {
		t.Fatalf("SocketPathFor(\"\") = %q; want %q", got, DefaultNetmuxdSocket)
	}
}

// TestSpecArgvReachesTheChild proves the argv/env in a Spec is what the process actually gets
// (argv array, never a shell string — program hard rule).
func TestSpecArgvReachesTheChild(t *testing.T) {
	dir := t.TempDir()
	recorded := filepath.Join(dir, "argv")
	spec := fakeSpec("netmuxd", RoleWiFi, "tcp", freePort(t), "serve")
	spec.Args = append(spec.Args, "--disable-usb")
	spec.Env = append(spec.Env, "MUXSUP_HELPER_ARGV="+recorded)

	s := fakeSupervisor(spec, "")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.Run(ctx)

	// Wait for CONTENT, not existence: the assertion is about what the child recorded, and a
	// file that exists can still be empty (quince#399). The helper writes atomically, so any
	// non-empty read here is a complete one.
	var b []byte
	waitUntil(t, func() bool {
		read, err := os.ReadFile(recorded)
		b = read
		return err == nil && len(read) > 0
	}, 3*time.Second, "child to record its argv")
	if !strings.Contains(string(b), "--disable-usb") {
		t.Fatalf("child argv = %q; want it to carry --disable-usb", string(b))
	}
}

// --- stories 3–4: the qn.2b supervision guarantees, over BOTH probe networks -------------------

// specsUnderTest runs the lifecycle stories for a unix-socket daemon (usbmuxd's shape) and a TCP
// daemon (netmuxd's), so netmuxd inherits proof, not just code.
func specsUnderTest(t *testing.T, mode string) map[string]Spec {
	t.Helper()
	return map[string]Spec{
		"unix": fakeSpec("usbmuxd", RoleUSB, "unix", filepath.Join(t.TempDir(), "m.sock"), mode),
		"tcp":  fakeSpec("netmuxd", RoleWiFi, "tcp", freePort(t), mode),
	}
}

// TestSupervisorStartsAndStops: with a free address, the supervisor spawns the child (which
// listens), reports running, and on ctx cancel kills it — after which the address is dead.
func TestSupervisorStartsAndStops(t *testing.T) {
	for name, spec := range specsUnderTest(t, "serve") {
		t.Run(name, func(t *testing.T) {
			s := fakeSupervisor(spec, "")
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan struct{})
			go func() { defer close(done); s.Run(ctx) }()

			waitUntil(t, func() bool { return dialable(spec.Network, spec.Address) }, 3*time.Second, "child to serve")
			st := s.Status()
			if st.State != StateRunning || !st.Managed {
				t.Fatalf("status = %+v; want running+managed", st)
			}
			if st.Name != spec.Name || st.Role != spec.Role {
				t.Fatalf("status names the wrong daemon: %+v", st)
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
			if dialable(spec.Network, spec.Address) {
				t.Fatal("address still served after shutdown — child not killed")
			}
		})
	}
}

// TestSupervisorCrashLoopDegrades: a child that keeps exiting non-zero is restarted with backoff
// and, past the threshold, flips to degraded with the exit reason (amendment 4a) — for both
// daemons, so a crash-looping netmuxd is visible in health rather than an endless log.
func TestSupervisorCrashLoopDegrades(t *testing.T) {
	for name, spec := range specsUnderTest(t, "crash") {
		t.Run(name, func(t *testing.T) {
			s := fakeSupervisor(spec, "")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan struct{})
			go func() { defer close(done); s.Run(ctx) }()

			waitUntil(t, func() bool { return stateOf(s) == StateDegraded }, 5*time.Second, "crash loop to degrade")
			if s.Status().Detail == "" {
				t.Fatal("degraded status has no detail (want the last exit reason)")
			}
			cancel()
			<-done
		})
	}
}

// TestSupervisorRefusesServedAddress (story 4): an address already served at startup is never
// taken over by a second daemon — the supervisor goes degraded without spawning. Proven on TCP
// here (netmuxd's case: an external netmuxd, or anything else, already on the port); the unix
// case is TestSupervisorRefusesServedSocket.
func TestSupervisorRefusesServedAddress(t *testing.T) {
	addr := freePort(t)
	external, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatalf("pre-occupy port: %v", err)
	}
	defer func() { _ = external.Close() }()
	go func() {
		for {
			c, err := external.Accept()
			if err != nil {
				return
			}
			_ = c.Close()
		}
	}()

	s := fakeSupervisor(fakeSpec("netmuxd", RoleWiFi, "tcp", addr, "serve"), "")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); s.Run(ctx) }()

	waitUntil(t, func() bool { return stateOf(s) == StateDegraded }, 3*time.Second, "refuse-loudly degrade")
	time.Sleep(80 * time.Millisecond) // it must NOT spawn a competitor: stay degraded
	st := s.Status()
	if st.State != StateDegraded {
		t.Fatalf("state = %q after refusing; want it to stay degraded (no spawn)", st.State)
	}
	if !strings.Contains(st.Detail, "already served") {
		t.Fatalf("degraded detail = %q; want it to say the address is already served", st.Detail)
	}

	cancel()
	<-done
}

// TestSupervisorRefusesServedSocket (qn.2b story 3, unix): refuse-loudly, then recover — once the
// external server goes away, Rescan re-probes and takes over (amendment 4b).
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

	s := fakeSupervisor(fakeSpec("usbmuxd", RoleUSB, "unix", socket, "serve"), "")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); s.Run(ctx) }()

	waitUntil(t, func() bool { return stateOf(s) == StateDegraded }, 3*time.Second, "refuse-loudly degrade")

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
	waitUntil(t, func() bool { return stateOf(s) == StateRunning && dialable("unix", socket) }, 3*time.Second, "takeover after recovery")

	cancel()
	<-done
}

// TestSupervisorRescanRestarts (qn.2b story 4): rescan on a healthy managed muxer restarts the
// daemon (a second start is recorded) so USB devices re-enumerate.
func TestSupervisorRescanRestarts(t *testing.T) {
	dir := t.TempDir()
	socket := filepath.Join(dir, "m.sock")
	starts := filepath.Join(dir, "starts")
	s := fakeSupervisor(fakeSpec("usbmuxd", RoleUSB, "unix", socket, "serve"), starts)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); s.Run(ctx) }()

	waitUntil(t, func() bool { return startCount(starts) >= 1 && dialable("unix", socket) }, 3*time.Second, "first start")

	if accepted, reason := s.Rescan(rescanCtx(t)); !accepted {
		t.Fatalf("rescan = (accepted=%v, reason=%q); want accepted (202)", accepted, reason)
	}
	waitUntil(t, func() bool { return startCount(starts) >= 2 }, 3*time.Second, "restart after rescan")
	waitUntil(t, func() bool { return dialable("unix", socket) }, 3*time.Second, "socket served again post-restart")

	cancel()
	<-done
}

// --- story 6: the group -----------------------------------------------------------------------

// TestGroupRescanRestartsOnlyTheUSBMuxer is the state-honesty guard behind ruling (bz): rescan
// exists for USB hotplug, and restarting netmuxd would tear a live Wi-Fi backup. With both
// daemons supervised, a rescan must restart usbmuxd and leave netmuxd's child untouched.
func TestGroupRescanRestartsOnlyTheUSBMuxer(t *testing.T) {
	dir := t.TempDir()
	usbStarts, netStarts := filepath.Join(dir, "usb"), filepath.Join(dir, "net")
	usb := fakeSupervisor(fakeSpec("usbmuxd", RoleUSB, "unix", filepath.Join(dir, "m.sock"), "serve"), usbStarts)
	netmux := fakeSupervisor(fakeSpec("netmuxd", RoleWiFi, "tcp", freePort(t), "serve"), netStarts)

	g := NewGroup()
	g.Supervise(usb)
	g.Supervise(netmux)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); g.Run(ctx) }()

	waitUntil(t, func() bool { return startCount(usbStarts) >= 1 && startCount(netStarts) >= 1 }, 3*time.Second, "both daemons to start")

	if accepted, reason := g.Rescan(rescanCtx(t)); !accepted {
		t.Fatalf("group rescan = (accepted=%v, reason=%q); want accepted", accepted, reason)
	}
	waitUntil(t, func() bool { return startCount(usbStarts) >= 2 }, 3*time.Second, "usbmuxd restart")
	time.Sleep(100 * time.Millisecond) // give a wrong implementation time to restart netmuxd too
	if n := startCount(netStarts); n != 1 {
		t.Fatalf("netmuxd started %d times; want exactly 1 — rescan must never restart it (a live Wi-Fi backup would tear)", n)
	}

	cancel()
	<-done
}

// TestGroupStatuses: health reports every daemon — supervised ones with their live state, dialed
// external ones as managed:false, never an empty list that reads as "no muxers".
func TestGroupStatuses(t *testing.T) {
	g := NewGroup()
	g.Supervise(fakeSupervisor(fakeSpec("usbmuxd", RoleUSB, "unix", filepath.Join(t.TempDir(), "m.sock"), "serve"), ""))
	g.AddUnmanaged("netmuxd", RoleWiFi, "127.0.0.1:27015", connectedClient())

	got := g.Statuses()
	if len(got) != 2 {
		t.Fatalf("statuses = %+v; want 2 entries", got)
	}
	if got[0].Name != "usbmuxd" || !got[0].Managed || !got[0].Rescan {
		t.Fatalf("managed entry = %+v; want usbmuxd managed with rescan", got[0])
	}
	if got[1].Name != "netmuxd" || got[1].Managed || got[1].State != StateExternal || got[1].Detail == "" {
		t.Fatalf("unmanaged entry = %+v; want netmuxd external with a reason", got[1])
	}
}

// fakeDialer is a muxsup.Dialer under test control: what health says, and whether Reread ran.
type fakeDialer struct {
	connected bool
	detail    string
	rereads   int
}

func (f *fakeDialer) Health() (bool, string) { return f.connected, f.detail }
func (f *fakeDialer) Reread()                { f.rereads++ }

func connectedClient() Dialer { return &fakeDialer{connected: true} }

// TestExternalStateFollowsTheDialingClient is quince#897 item 2's regression, and the whole of
// qn.6p D5. `external` was a constant string built at construction, so health reported a muxer as
// served while the daemon logged `connection refused` against that same address.
//
// The state is re-read at every Statuses() call, so ONE group answers differently as the
// connection comes and goes — which is what "continuing rather than one-shot" means in practice.
func TestExternalStateFollowsTheDialingClient(t *testing.T) {
	g := NewGroup()
	dialer := &fakeDialer{}
	g.AddUnmanaged("netmuxd", RoleWiFi, "/run/mux/usbmuxd", dialer)

	// Before the first dial lands: not connected, nothing to report. `unreachable` here would be
	// a failure claim about an attempt nobody has made.
	if got := g.Statuses()[0]; got.State != StateStarting {
		t.Errorf("before the first dial: state = %q, want %q (detail %q)", got.State, StateStarting, got.Detail)
	}

	dialer.connected, dialer.detail = true, ""
	if got := g.Statuses()[0]; got.State != StateExternal {
		t.Errorf("connected: state = %q, want %q", got.State, StateExternal)
	}

	// The muxer dies. Health must follow WITHOUT the group being rebuilt, and must carry the
	// dialer's own words rather than a generic phrase.
	dialer.connected, dialer.detail = false, "dial unix /run/mux/usbmuxd: connect: connection refused"
	got := g.Statuses()[0]
	if got.State != StateUnreachable {
		t.Errorf("after the muxer died: state = %q, want %q", got.State, StateUnreachable)
	}
	if !strings.Contains(got.Detail, "connection refused") {
		t.Errorf("detail = %q, want the dialer's own error", got.Detail)
	}
	if !strings.Contains(got.Detail, "/run/mux/usbmuxd") {
		t.Errorf("detail = %q, want the address it could not reach", got.Detail)
	}

	// And back, because an external muxer that restarts must stop being reported as broken.
	dialer.connected, dialer.detail = true, ""
	if got := g.Statuses()[0]; got.State != StateExternal {
		t.Errorf("after the muxer came back: state = %q, want %q", got.State, StateExternal)
	}
}

// A configured muxer that NOTHING dials is a wiring bug. It must not read as `external`, which is
// the healthy-looking word that would hide it.
func TestExternalWithNoDialerSaysSo(t *testing.T) {
	g := NewGroup()
	g.AddUnmanaged("netmuxd", RoleWiFi, "127.0.0.1:27015", nil)
	got := g.Statuses()[0]
	if got.State == StateExternal {
		t.Fatalf("an undialed muxer reported %q — the one word that hides the bug", got.State)
	}
	if !strings.Contains(got.Detail, "nothing is dialing it") {
		t.Errorf("detail = %q, want it to name the missing dialer", got.Detail)
	}
}

// TestGroupRescanWithoutManagedUSBMuxer: rescan is honestly refused (409 + reason) when quince
// owns no USB muxer — the manage_muxer:false case and the netmuxd-only case alike.
func TestGroupRescanWithoutManagedUSBMuxer(t *testing.T) {
	g := NewGroup()
	g.Supervise(fakeSupervisor(fakeSpec("netmuxd", RoleWiFi, "tcp", freePort(t), "serve"), ""))
	if accepted, reason := g.Rescan(rescanCtx(t)); accepted || reason == "" {
		t.Fatalf("rescan = (accepted=%v, reason=%q); want refused with a reason", accepted, reason)
	}

	external := NewGroup()
	external.AddUnmanaged("usbmuxd", RoleUSB, "/var/run/usbmuxd", connectedClient())
	accepted, reason := external.Rescan(rescanCtx(t))
	if accepted || !strings.Contains(reason, "external") {
		t.Fatalf("external rescan = (accepted=%v, reason=%q); want refused naming the external muxer", accepted, reason)
	}
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

// TestHelperProcess is the fake muxer daemon. It is inert unless re-exec'd by the supervisor with
// GO_WANT_HELPER_PROCESS=1. MUXSUP_HELPER_MODE selects behaviour, MUXSUP_HELPER_LISTEN says what
// to serve ("unix:/path" or "tcp:host:port"), MUXSUP_HELPER_STARTS (if set) gets one byte per
// start so a test can count restarts, and MUXSUP_HELPER_ARGV (if set) records the child's argv.
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
	if p := os.Getenv("MUXSUP_HELPER_ARGV"); p != "" {
		// Write via a same-dir temp + rename, so the recorder never exposes a created-but-empty
		// file: os.WriteFile is OpenFile(O_CREATE|O_TRUNC) followed by Write, and a reader that
		// lands between the two sees "" (quince#399). Same reason config.AtomicWrite exists.
		tmp := p + ".tmp"
		if err := os.WriteFile(tmp, []byte(strings.Join(os.Args, " ")), 0o644); err == nil {
			_ = os.Rename(tmp, p)
		}
	}
	switch os.Getenv("MUXSUP_HELPER_MODE") {
	case "crash":
		os.Exit(1)
	case "serve":
		network, address, _ := strings.Cut(os.Getenv("MUXSUP_HELPER_LISTEN"), ":")
		if network == "unix" {
			_ = os.Remove(address) // unlink any stale socket, like the real daemons
		}
		ln, err := net.Listen(network, address)
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

// TestRescanRereadsTheExternalMuxer is qn.6p D6. With nothing supervised, rescan used to be a flat
// 409 — "quince does not own it" — which was true and useless: the problem rescan exists for did
// not go away when the daemon moved out of quince's container, only the remedy did.
//
// It now drops the connection so the muxer replays its attached set, which reconciles the device
// table against the muxer's current truth. Every dialed muxer is re-read, not just one, because
// there is no daemon whose restart would cover the others.
func TestRescanRereadsTheExternalMuxer(t *testing.T) {
	usb, wifi := &fakeDialer{connected: true}, &fakeDialer{connected: true}
	g := NewGroup()
	g.AddUnmanaged("usbmuxd", RoleUSB, "/run/mux/usbmuxd", usb)
	g.AddUnmanaged("netmuxd", RoleWiFi, "127.0.0.1:27015", wifi)

	accepted, reason := g.Rescan(rescanCtx(t))
	if !accepted {
		t.Fatalf("rescan refused with %q; want it to re-read the external muxers", reason)
	}
	if usb.rereads != 1 || wifi.rereads != 1 {
		t.Errorf("rereads = usb:%d wifi:%d; want 1 each — no daemon restart covers the others",
			usb.rereads, wifi.rereads)
	}
	// It must NOT start anything: there is no daemon here to start, and claiming otherwise is the
	// dishonesty this change exists to remove.
	if g.Names() != "" {
		t.Errorf("rescan produced supervised daemons %q; it must restart nothing", g.Names())
	}
}

// A muxer configured with no dialer cannot be re-read, and rescan says which of the two nothings
// it met rather than collapsing them into one refusal (Operator, quince#940: a diagnostic that
// collapses distinguishable causes is a defect even when every word of it is true).
func TestRescanDistinguishesUndialedFromUnconfigured(t *testing.T) {
	undialed := NewGroup()
	undialed.AddUnmanaged("usbmuxd", RoleUSB, "/run/mux/usbmuxd", nil)
	accepted, reason := undialed.Rescan(rescanCtx(t))
	if accepted || !strings.Contains(reason, "no dialer") {
		t.Errorf("undialed rescan = (%v, %q); want a refusal naming the missing dialer", accepted, reason)
	}

	empty := NewGroup()
	accepted, reason = empty.Rescan(rescanCtx(t))
	if accepted || !strings.Contains(reason, "no muxer is configured") {
		t.Errorf("unconfigured rescan = (%v, %q); want a refusal naming the absent config", accepted, reason)
	}
}
