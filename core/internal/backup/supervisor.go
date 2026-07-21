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
	bin       string   // "idevicebackup2" (prod) or the test binary (fake harness)
	argPrefix []string // test-only leading args (empty in production): -test.run=… + "--"
	env       []string // test-only extra child env (empty in production): the fake harness knobs
	usbmuxd   string   // devices.usbmuxd_socket
	netmuxd   string   // devices.netmuxd_addr
}

// targetStubDir is where a job's symlink stub lives: beside the storage work dir, so a statfs of
// the stub reports the STORAGE filesystem's free space (see prepareTarget). Hidden + per-job.
const targetStubDir = ".quince-targets"

// targetRootFor derives the stub root from the work dir the storage backend handed us:
//
//	zfs:        /backups/<udid>/working        → /backups/<udid>/.quince-targets
//	namespace:  /backups/<udid>/work/<jobid>   → /backups/<udid>/work/.quince-targets
//
// Both are quince-writable and on the storage filesystem. On zfs the stub sits inside the
// snapshotted dataset, which is harmless: the per-job cleanup runs when the child exits, before
// the version snapshot is cut at commit (a crash-orphaned stub is swept by reconciliation).
func targetRootFor(workDir string) string {
	return filepath.Join(filepath.Dir(workDir), targetStubDir)
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
// libimobiledevice convention — INTERFACE FACT, confirmed live), so the symlink makes it write
// straight into qn.5's work dir with no tree copy and no committed-state mutation. Cleaned per job.
//
// The stub dir MUST live on the same filesystem as the work dir (lab finding, qn.4c gate 11).
// mobilebackup2 asks the host how much free space it has, and idevicebackup2 answers with a statfs
// of the target directory it was handed — it does NOT follow the <UDID> symlink. With the stub on
// a small scratch filesystem (quince used $QUINCE_CACHE), the phone is told that filesystem's free
// space and REFUSES the backup: `ErrorCode 105: Insufficient free disk space` → exit 151, zero
// bytes, no actionable message. Proven on real hardware: the same device refused with the stub on a
// 26 GB cache filesystem and began transferring with it on the 546 GB storage filesystem.
// Placing the stub beside the work dir makes the answer truthful on every backend, and needs no
// extra writable location — the device's own storage area is already quince-writable (the parent
// dataset root is NOT, under the zfs hook profile, which is why a naive <backups>/… root fails).
func (t *tool) prepareTarget(jobID, udid, workDir string) (target string, cleanup func(), err error) {
	target = filepath.Join(targetRootFor(workDir), jobID)
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
