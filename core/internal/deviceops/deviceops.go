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
	UsbmuxdSocket  string // devices.usbmuxd_socket (a path) — USB muxer
	NetmuxdAddr    string // devices.netmuxd_addr (host:port) — Wi-Fi muxer
	Log            *slog.Logger
	env            []string // extra child env (tests only)
	argPrefix      []string // prepended to every argv (tests only: re-exec as the fake CLI)
}

// args builds the full argv for a child: the test-only prefix (empty in production) then the
// real CLI arguments.
func (t *Tools) args(cliArgs ...string) []string {
	if len(t.argPrefix) == 0 {
		return cliArgs
	}
	return append(append([]string{}, t.argPrefix...), cliArgs...)
}

// NewTools returns Tools with the real binary names and the configured muxer sockets.
func NewTools(usbmuxdSocket, netmuxdAddr string, log *slog.Logger) *Tools {
	return &Tools{
		Idevicepair:    "idevicepair",
		Ideviceinfo:    "ideviceinfo",
		Idevicebackup2: "idevicebackup2",
		UsbmuxdSocket:  usbmuxdSocket,
		NetmuxdAddr:    netmuxdAddr,
		Log:            log,
	}
}

// socketAddr is the USBMUXD_SOCKET_ADDRESS value for a transport: UNIX:<path> for the usbmuxd
// unix socket, host:port for netmuxd's TCP listener (verified live — qn.3 interface fact 2).
func (t *Tools) socketAddr(transport string) string {
	if transport == TransportWiFi {
		return t.NetmuxdAddr
	}
	return "UNIX:" + t.UsbmuxdSocket
}

// childEnv builds the subprocess environment: the inherited env + the muxer pointer + any
// test-injected extras. Never carries a secret (the encryption password goes over the pty).
func (t *Tools) childEnv(transport string) []string {
	env := append(os.Environ(), "USBMUXD_SOCKET_ADDRESS="+t.socketAddr(transport))
	return append(env, t.env...)
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
