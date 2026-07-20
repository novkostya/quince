package deviceops

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

// Pairing records under /var/lib/lockdown are PRIVATE-KEY-GRADE secrets (design §6): the host
// identity (SystemConfiguration.plist) and per-device records together let any holder talk to
// the iPhone as a trusted host. qn.3 is the rung that creates them (via the UI pair flow), so
// it must also make them survive a container recreate — otherwise a fresh container gets a new
// host identity and the device demands Trust again (amendment 1, decisions log).
//
// Mechanism (rung-ruled): a whole-dir copy between the system dir and a persistent dir under
// $QUINCE_DATA — deliberately NOT a symlink/RemoveAll of /var/lib/lockdown, which would be
// unsafe against a package-created dir or an operator's own bind mount. Records are 0600, the
// dir 0700; nothing here is ever logged or served.

// LockdownStore syncs pairing records between the libimobiledevice system dir and persistent
// storage under $QUINCE_DATA.
type LockdownStore struct {
	sysDir     string // e.g. /var/lib/lockdown
	persistDir string // <dataDir>/lockdown
	log        *slog.Logger
}

// NewLockdownStore returns a store persisting under <dataDir>/lockdown. dataDir is $QUINCE_DATA
// (a mounted volume); sysDir is where libimobiledevice reads/writes (/var/lib/lockdown).
func NewLockdownStore(dataDir, sysDir string, log *slog.Logger) *LockdownStore {
	return &LockdownStore{sysDir: sysDir, persistDir: filepath.Join(dataDir, "lockdown"), log: log}
}

// Restore copies persisted records into the system dir at startup so a fresh container keeps
// its pairings without a re-Trust. It never overwrites a record already present in the system
// dir (a live/bind-mounted record wins).
func (l *LockdownStore) Restore() {
	if err := os.MkdirAll(l.sysDir, 0o700); err != nil {
		l.log.Warn("lockdown: could not ensure system dir; pairing may not restore", "error", err)
		return
	}
	n, err := syncPlists(l.persistDir, l.sysDir, false)
	if err != nil {
		l.log.Warn("lockdown: restore failed; pairing may not survive a recreate", "error", err)
		return
	}
	if n > 0 {
		l.log.Info("lockdown: restored persisted pairing records", "count", n)
	}
}

// Backup copies the current system records into persistent storage after a successful pair,
// overwriting older copies (host identity may have changed).
func (l *LockdownStore) Backup() {
	if err := os.MkdirAll(l.persistDir, 0o700); err != nil {
		l.log.Warn("lockdown: could not ensure persistent dir; pairing may not survive a recreate", "error", err)
		return
	}
	if _, err := syncPlists(l.sysDir, l.persistDir, true); err != nil {
		l.log.Warn("lockdown: backup failed; pairing may not survive a recreate", "error", err)
	}
}

// syncPlists copies every *.plist from srcDir into dstDir (0600). When overwrite is false, an
// existing destination file is left untouched. A missing srcDir is not an error (nothing yet).
func syncPlists(srcDir, dstDir string, overwrite bool) (int, error) {
	entries, err := os.ReadDir(srcDir)
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	n := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".plist") {
			continue
		}
		dst := filepath.Join(dstDir, e.Name())
		if !overwrite {
			if _, err := os.Stat(dst); err == nil {
				continue
			}
		}
		if err := copyFile(filepath.Join(srcDir, e.Name()), dst); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}
