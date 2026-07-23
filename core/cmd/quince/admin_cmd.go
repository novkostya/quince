package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/novkostya/quince/core/internal/bus"
	"github.com/novkostya/quince/core/internal/config"
	"github.com/novkostya/quince/core/internal/storage"
	"github.com/novkostya/quince/core/internal/store"
)

// The qn.4b operator escape-hatch CLIs (design §4; CLI-only, no REST/contract surface):
//
//	quince versions verify <version-id> | --udid <udid>   re-run structural verification
//	quince device repair-working-copy <udid>              rebuild working/ from the last good version
//
// Both operate on a reconciled *storage.Manager built WITHOUT the muxer / device registry / engine
// goroutines the full serve stack spins up (buildStorage) — they only touch storage.

// withStorage opens the store + config + bus, builds a reconciled storage.Manager, and runs fn.
func withStorage(fn func(mgr *storage.Manager) error) error {
	log := newLogger()
	bootstrap, bwarn := config.LoadBootstrap(os.Environ())
	for _, w := range bwarn {
		log.Warn("bootstrap warning", "path", w.Path, "message", w.Message)
	}
	st, err := store.Open(bootstrap.DBPath())
	if err != nil {
		return fmt.Errorf("open db %s: %w", bootstrap.DBPath(), err)
	}
	defer func() { _ = st.Close() }()
	cfgSvc := config.NewService(bootstrap.ConfigPath(), log)
	eventBus := bus.New()
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	return fn(buildStorage(ctx, bootstrap, cfgSvc, st, eventBus, log))
}

// versionsCmd implements `quince versions verify`. It re-runs the passwordless STRUCTURAL
// verification of a committed version (content verification is qn.8's and is NOT run here — state
// honesty). Exit 0 on verified; nonzero on a verification failure or an unknown version/device.
func versionsCmd(args []string) error {
	if len(args) == 0 || args[0] != "verify" {
		return errors.New("usage: quince versions verify <version-id> | quince versions verify --udid <udid>")
	}
	fs := flag.NewFlagSet("versions verify", flag.ContinueOnError)
	udid := fs.String("udid", "", "verify the device's latest committed version instead of a version id")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	return withStorage(func(mgr *storage.Manager) error {
		var (
			rep storage.VerifyReport
			ok  bool
		)
		switch {
		case *udid != "":
			if fs.NArg() != 0 {
				return errors.New("give either a version-id or --udid, not both")
			}
			if rep, ok = mgr.VerifyLatest(*udid); !ok {
				return fmt.Errorf("device %s has no committed version to verify", *udid)
			}
		case fs.NArg() == 1:
			if rep, ok = mgr.VerifyVersion(fs.Arg(0)); !ok {
				return fmt.Errorf("no such version %q", fs.Arg(0))
			}
		default:
			return errors.New("usage: quince versions verify <version-id> | quince versions verify --udid <udid>")
		}
		if rep.OK {
			fmt.Printf("version %s (device %s): structurally verified — %s %s backup\n",
				rep.VersionID, rep.UDID, encWord(rep.Encrypted), rep.Kind)
			return nil
		}
		return fmt.Errorf("version %s (device %s): structural verification FAILED — %s",
			rep.VersionID, rep.UDID, rep.Detail)
	})
}

// deviceCmd implements the qn.5b Reset escape hatch — `quince device reset-working <udid>` (or its
// back-compat alias `repair-working-copy`): DISCARD a device's dirty working/ so the next backup
// starts clean from latest/, losing only the partial and never a committed version. (Under the
// qn.5b per-job model the working copy is seeded from latest/ at job start, so the old "rebuild
// working from the last snapshot" is no longer needed — discarding it is the honest action.) Never
// automatic in v0.1; the UI surface is POST /api/devices/{udid}/reset-working.
func deviceCmd(args []string) error {
	if len(args) != 2 || (args[0] != "reset-working" && args[0] != "repair-working-copy") {
		return errors.New("usage: quince device reset-working <udid>")
	}
	udid := args[1]
	return withStorage(func(mgr *storage.Manager) error {
		if err := mgr.RepairWorkingCopy(udid); err != nil {
			return fmt.Errorf("reset working copy for %s: %w", udid, err)
		}
		fmt.Printf("working copy for device %s discarded — the next backup starts clean from the last version\n", udid)
		return nil
	})
}

func encWord(encrypted bool) string {
	if encrypted {
		return "encrypted"
	}
	return "unencrypted"
}
