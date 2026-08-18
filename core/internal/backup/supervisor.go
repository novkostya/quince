package backup

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"syscall"

	"github.com/novkostya/quince/core/internal/muxaddr"
)

// MuxerFor resolves the muxer endpoint for one device on one transport (quince#1219 item D). It
// mirrors deviceops.MuxerFor rather than importing it: the two packages share a resolver value
// built once in live.go, not a dependency.
type MuxerFor func(udid, transport string) (muxaddr.Endpoint, error)

// StaticMuxer is the MuxerFor for one muxer address serving every device — what the tests want,
// and the honest shape of "route by source" with a single source. Production resolves against
// the device registry instead.
func StaticMuxer(ep muxaddr.Endpoint) MuxerFor {
	return func(string, string) (muxaddr.Endpoint, error) { return ep, nil }
}

// tool spawns idevicebackup2 with the qn.3 subprocess hygiene (argv arrays never a shell, own
// process group, ctx-killed as a group) pointed at a muxer via USBMUXD_SOCKET_ADDRESS.
type tool struct {
	bin       string   // "idevicebackup2" (prod) or the test binary (fake harness)
	argPrefix []string // test-only leading args (empty in production): -test.run=… + "--"
	env       []string // test-only extra child env (empty in production): the fake harness knobs
	// muxerFor answers WHICH MUXER reported this device (quince#1219 item D). It replaces a pair
	// of endpoints chosen by TRANSPORT — which assumed one daemon per kind of connection, and
	// misrouted a multi-hour transfer to a muxer that could not see the phone.
	muxerFor MuxerFor
}

// socketAddr is the USBMUXD_SOCKET_ADDRESS for one device (VERIFIED qn.3). This was the THIRD
// copy of the grammar and the one easiest to miss: it prefixed "UNIX:" for a usbmuxd path but
// returned the Wi-Fi address verbatim, so a unix-socket Wi-Fi muxer would leave presence and
// device ops working while every BACKUP over Wi-Fi reached the child as a bare path libusbmuxd
// reads as host:port (quince#897 item 1, qn.6p D3). The spelling is now the endpoint's, in all
// three places, and the endpoint is now the DEVICE's rather than the transport's.
func (t *tool) socketAddr(udid, transport string) (string, error) {
	if t.muxerFor == nil {
		return "", errors.New("backup: no muxer resolver wired")
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

// The idevicebackup2 TARGET is the storage backend's working/ parent (Seed's return). The tool
// writes the tree into <target>/<UDID>/ by its own libimobiledevice convention (INTERFACE FACT,
// confirmed live), so quince points it straight there — NO symlink stub (qn.5b dropped the old
// <target>/<UDID> symlink dance). This also fixes the free-space bug class (28b97de) structurally:
// idevicebackup2 answers mobilebackup2's free-space query with a statfs of the target directory it
// was handed, and that target is now always on the STORAGE filesystem by construction (it is the
// device's own working/ parent), never a scratch/cache fs — so the phone is told the truth and no
// longer refuses a large backup with `ErrorCode 105: Insufficient free disk space`.

// command builds the supervised idevicebackup2 process. argv (INTERFACE FACT — the exact flags are
// verified live in the built image): `idevicebackup2 [-n] -u <udid> backup <target>` — -n selects
// the network transport for Wi-Fi (lab-proven), -u pins the device. The whole group is SIGKILLed
// on ctx cancel (timeout / user cancel / shutdown). No password ever reaches this argv or env: the
// device encrypts with its own keybag under the assisted model (interface fact 5).
func (t *tool) command(ctx context.Context, transport, udid, target, gatePath string) (*exec.Cmd, error) {
	addr, err := t.socketAddr(udid, transport)
	if err != nil {
		return nil, err
	}
	args := append([]string{}, t.argPrefix...) // prod: empty; test: -test.run=… "--"
	if transport == TransportWiFi {
		args = append(args, "-n")
	}
	// qn.6b candidate C: --gate pauses the tool after the Backup request (passcode already fired),
	// before the message loop, until <gatePath> appears — so quince seeds working/ in parallel and
	// the on-device passcode prompt shows in ~1–2 s instead of after the ~O(files) seed. Empty
	// gatePath = upstream behaviour (no pause), used for a resume (no seed to overlap).
	if gatePath != "" {
		args = append(args, "--gate", gatePath)
	}
	args = append(args, "-u", udid, "backup", target)

	cmd := exec.CommandContext(ctx, t.bin, args...)
	cmd.Env = append(os.Environ(), "USBMUXD_SOCKET_ADDRESS="+addr)
	cmd.Env = append(cmd.Env, t.env...) // prod: empty; test: the fake-harness knobs
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process != nil {
			return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
		return nil
	}
	return cmd, nil
}
