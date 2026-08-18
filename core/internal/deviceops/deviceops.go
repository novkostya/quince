// Package deviceops drives the Apple device operations quince cannot get from the muxer
// (design §2 `device ops`, stack D2): pairing, lockdown identity, and backup-encryption
// management, by running the proven libimobiledevice CLIs (idevicepair / ideviceinfo /
// idevicebackup2) as supervised argv subprocesses — never shell strings — pointed at the
// configured muxer via USBMUXD_SOCKET_ADDRESS. It also owns the async Op lifecycle for
// pair/encryption (contracts §2) and the enrichment driver that overlays identity onto the
// device.Registry on attach.
//
// Secrets discipline (design §6, the rung's central rule): the backup-encryption password
// reaches idevicebackup2 over the child's controlling pty (interactive mode) — NEVER argv
// (world-readable /proc/<pid>/cmdline), never an env var, never logged, never stored. The
// pairing record idevicepair writes is a private-key-grade secret persisted 0600 under
// $QUINCE_DATA (amendment 1), never served, never logged.
package deviceops

import (
	"errors"
	"log/slog"
	"os"
	"os/exec"
	"regexp"
	"syscall"

	"github.com/novkostya/quince/core/internal/device"
	"github.com/novkostya/quince/core/internal/muxaddr"
)

// Transports (matching the muxd/wire strings). Pairing is USB-only at the protocol floor
// (stack D2); the Wi-Fi socket is netmuxd, reached with the -n "network device" flag.
const (
	TransportUSB  = "usb"
	TransportWiFi = "wifi"
)

// udidPattern is the strict allowlist a UDID must match before it can reach any argv
// (design §6 "UDIDs validated against strict patterns before use"). Real UDIDs are 40-hex
// or the newer 8-4-… hyphenated form; the synthetic test UDIDs are upper-hex+hyphen. Keep
// it to the characters both forms use — no shell metacharacters, spaces, or dots.
var udidPattern = regexp.MustCompile(`^[A-Za-z0-9-]{8,64}$`)

// ErrBadUDID is returned when a UDID fails validation (never reaches a subprocess).
var ErrBadUDID = errors.New("deviceops: invalid udid")

func validUDID(udid string) bool { return udidPattern.MatchString(udid) }

// Tools runs the libimobiledevice CLIs. Binary names are overridable so tests inject a
// helper-process fake (the muxsup GO_WANT_HELPER_PROCESS discipline); env carries extra
// child environment the tests use to select fake behaviour (production adds only
// USBMUXD_SOCKET_ADDRESS).
type Tools struct {
	Idevicepair    string // default "idevicepair"
	Ideviceinfo    string // default "ideviceinfo"
	Idevicebackup2 string // default "idevicebackup2"
	// muxerFor answers WHERE to reach the muxer that reported a given device (quince#1219 item D).
	// It replaces a pair of configured endpoints held here and chosen by TRANSPORT: the registry
	// keys presence by (source, udid, transport) and this package discarded the source, so an op
	// on a device seen by muxer B could be dispatched at muxer A because both were labelled `usb`.
	//
	// It also closes a latent path that no fallback could close honestly: with no endpoint for a
	// transport, socketAddr returned "" and libusbmuxd only NULL-checks USBMUXD_SOCKET_ADDRESS —
	//
	//	char *usbmuxd_socket_addr = getenv("USBMUXD_SOCKET_ADDRESS");
	//	if (usbmuxd_socket_addr) { ... }
	//
	// — so SET-BUT-EMPTY was used rather than falling back to the compiled-in default, and the CLI
	// failed somewhere the operator could not read. A resolver returns an ERROR instead, and the op
	// says which device nothing reports.
	muxerFor  MuxerFor
	Log       *slog.Logger
	env       []string // extra child env (tests only)
	argPrefix []string // prepended to every argv (tests only: re-exec as the fake CLI)
	// wifiSyncKey is the lockdown key wifiSync reads; empty means "do not ask the device", which is
	// what it meant for every build before story 3 measured the name (see the wifiSyncKey const).
	// The empty branch stays: it is the honest answer whenever the key is unknown, not scaffolding
	// left behind. It lives on Tools rather than in a package var because enrichment reads it from a
	// background goroutine while tests set it — as a package var that is a data race, and
	// `go test -race` caught it as one.
	wifiSyncKey string
}

// args builds the full argv for a child: the test-only prefix (empty in production) then the
// real CLI arguments.
func (t *Tools) args(cliArgs ...string) []string {
	if len(t.argPrefix) == 0 {
		return cliArgs
	}
	return append(append([]string{}, t.argPrefix...), cliArgs...)
}

// MuxerFor resolves the muxer endpoint for one device on one transport. Its two implementations
// are the live registry (live.go, backed by Registry.SourceFor) and the tests' fixed answer; it is
// a func rather than an interface so the device package is not imported for one method.
type MuxerFor func(udid, transport string) (muxaddr.Endpoint, error)

// StaticMuxer is the MuxerFor for a deployment with one muxer address for every device — the
// answer tests want, and the honest shape of "route by source" when there is only one source.
// Production does NOT use it: buildLiveStack resolves against the registry.
func StaticMuxer(ep muxaddr.Endpoint) MuxerFor {
	return func(string, string) (muxaddr.Endpoint, error) { return ep, nil }
}

// NewTools returns Tools with the real binary names and the muxer resolver.
func NewTools(muxerFor MuxerFor, log *slog.Logger) *Tools {
	return &Tools{
		Idevicepair:    "idevicepair",
		Ideviceinfo:    "ideviceinfo",
		Idevicebackup2: "idevicebackup2",
		muxerFor:       muxerFor,
		Log:            log,
		wifiSyncKey:    wifiSyncKey,
	}
}

// socketAddr is the USBMUXD_SOCKET_ADDRESS value for one device (verified live — qn.3 interface
// fact 2). The spelling is the ENDPOINT's, not this function's: it used to prefix "UNIX:"
// unconditionally for USB and return the Wi-Fi address verbatim, so a unix-socket Wi-Fi muxer
// reached the CLIs as a bare path and libusbmuxd read it as a host:port (quince#897 item 1).
//
// THE DEVICE, NOT THE TRANSPORT, PICKS THE MUXER (quince#1219 item D). It takes a udid because
// the transport alone never identified a daemon — it named a kind of connection, and quince
// assumed one daemon per kind.
func (t *Tools) socketAddr(udid, transport string) (string, error) {
	if t.muxerFor == nil {
		return "", errors.New("deviceops: no muxer resolver wired")
	}
	ep, err := t.muxerFor(udid, transport)
	if err != nil {
		return "", err
	}
	if ep.IsZero() {
		return "", muxaddr.ErrEmpty
	}
	return ep.Env(), nil
}

// childEnv builds the subprocess environment: the inherited env + the muxer pointer + any
// test-injected extras. Never carries a secret (the encryption password goes over the pty).
//
// IT CAN NOW FAIL, and that is the point: the caller must not run a CLI it cannot point at a
// muxer. Returning the inherited environment unchanged would let libusbmuxd fall back to its
// compiled-in default socket — a silent fallback to a daemon the operator never named.
func (t *Tools) childEnv(udid, transport string) ([]string, error) {
	addr, err := t.socketAddr(udid, transport)
	if err != nil {
		return nil, err
	}
	env := append(os.Environ(), "USBMUXD_SOCKET_ADDRESS="+addr)
	return append(env, t.env...), nil
}

// networkFlag returns the "-n" argument set for a Wi-Fi (network) device, empty for USB.
func networkArgs(transport string) []string {
	if transport == TransportWiFi {
		return []string{"-n"}
	}
	return nil
}

// cancelKillGroup makes ctx cancellation SIGKILL the child's whole process group (design §1).
// Used on its own by the pty path, where creack/pty sets Setsid (a new session ⇒ new group,
// pgid == pid) and we must not also set Setpgid.
func cancelKillGroup(cmd *exec.Cmd) {
	cmd.Cancel = func() error {
		if cmd.Process != nil {
			return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
		return nil
	}
}

// setpgid puts the child at the head of its own process group and arranges for ctx
// cancellation to signal the whole group (design §1 subprocess hygiene). These CLIs are
// short-lived one-shots, so this is the group-kill guard, not a long-running supervisor.
func setpgid(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cancelKillGroup(cmd)
}

// Identity is re-exported so callers can build device overlays without importing device
// directly; it is the same type the registry's Enrich consumes.
type Identity = device.Identity
