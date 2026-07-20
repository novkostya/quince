package backup

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

// tool spawns idevicebackup2 with the qn.3 subprocess hygiene (argv arrays never a shell, own
// process group, ctx-killed as a group) pointed at a muxer via USBMUXD_SOCKET_ADDRESS.
type tool struct {
	bin        string   // "idevicebackup2" (prod) or the test binary (fake harness)
	argPrefix  []string // test-only leading args (empty in production): -test.run=… + "--"
	env        []string // test-only extra child env (empty in production): the fake harness knobs
	usbmuxd    string   // devices.usbmuxd_socket
	netmuxd    string   // devices.netmuxd_addr
	targetRoot string   // scratch root for the per-job symlink target (under $QUINCE_CACHE)
}

// socketAddr is the USBMUXD_SOCKET_ADDRESS for a transport (VERIFIED qn.3): UNIX:<path> for the
// usbmuxd unix socket, host:port for netmuxd.
func socketAddr(transport, usbmuxd, netmuxd string) string {
	if transport == TransportWiFi {
		return netmuxd
	}
	if strings.HasPrefix(usbmuxd, "/") {
		return "UNIX:" + usbmuxd
	}
	return usbmuxd
}

// prepareTarget builds the idevicebackup2 target: a scratch dir whose <udid> entry is a SYMLINK to
// the storage work dir. idevicebackup2 backup <target> writes the tree into <target>/<UDID>/ (a
// libimobiledevice convention — INTERFACE FACT to verify live), so the symlink makes it write
// straight into qn.5's work dir with no tree copy and no committed-state mutation. Cleaned per job.
func (t *tool) prepareTarget(jobID, udid, workDir string) (target string, cleanup func(), err error) {
	target = filepath.Join(t.targetRoot, jobID)
	if err := os.RemoveAll(target); err != nil {
		return "", nil, err
	}
	if err := os.MkdirAll(target, 0o755); err != nil {
		return "", nil, err
	}
	if err := os.Symlink(workDir, filepath.Join(target, udid)); err != nil {
		_ = os.RemoveAll(target)
		return "", nil, err
	}
	return target, func() { _ = os.RemoveAll(target) }, nil
}

// command builds the supervised idevicebackup2 process. argv (INTERFACE FACT — the exact flags are
// verified live in the built image): `idevicebackup2 [-n] -u <udid> backup <target>` — -n selects
// the network transport for Wi-Fi (lab-proven), -u pins the device. The whole group is SIGKILLed
// on ctx cancel (timeout / user cancel / shutdown). No password ever reaches this argv or env: the
// device encrypts with its own keybag under the assisted model (interface fact 5).
func (t *tool) command(ctx context.Context, transport, udid, target string) *exec.Cmd {
	args := append([]string{}, t.argPrefix...) // prod: empty; test: -test.run=… "--"
	if transport == TransportWiFi {
		args = append(args, "-n")
	}
	args = append(args, "-u", udid, "backup", target)

	cmd := exec.CommandContext(ctx, t.bin, args...)
	cmd.Env = append(os.Environ(), "USBMUXD_SOCKET_ADDRESS="+socketAddr(transport, t.usbmuxd, t.netmuxd))
	cmd.Env = append(cmd.Env, t.env...) // prod: empty; test: the fake-harness knobs
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process != nil {
			return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
		return nil
	}
	return cmd
}
