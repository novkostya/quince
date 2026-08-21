package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/novkostya/quince/core/internal/auth"
	"github.com/novkostya/quince/core/internal/bus"
	"github.com/novkostya/quince/core/internal/config"
	"github.com/novkostya/quince/core/internal/storage"
	"github.com/novkostya/quince/core/internal/store"
)

// The operator escape-hatch CLIs (design §4; CLI-only, no REST/contract surface):
//
//	quince versions verify <version-id> | --udid <udid>   re-run structural verification
//	quince device repair-working-copy <udid>              rebuild working/ from the last good version
//	quince auth reset --yes                               clear the password + every passkey (qn.6k)
//
// The first two operate on a reconciled *storage.Manager built WITHOUT the muxer / device registry
// / engine goroutines the full serve stack spins up (buildStorage) — they only touch storage.
// `auth reset` touches NEITHER storage nor the muxers, so it opens the store alone; giving it the
// storage stack would make an account-recovery command fail on a box whose disk is unreachable,
// which is a state it must work in.

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
	mgr, _, err := buildStorage(ctx, bootstrap, cfgSvc, st, eventBus, log, scanSynchronous)
	if err != nil {
		return err
	}
	return fn(mgr)
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
// AN ESCAPE HATCH THAT CANNOT EXPRESS WHAT THE API CAN IS A SECOND BUG WAITING (quince#448),
// so `--storage <name>` mirrors the API's storage_id and omitting it resolves by dirty count
// with the same refusal listing the candidates.
func deviceCmd(args []string) error {
	var storageName string
	if len(args) == 4 && args[2] == "--storage" {
		storageName, args = args[3], args[:2]
	}
	if len(args) != 2 || (args[0] != "reset-working" && args[0] != "repair-working-copy") {
		return errors.New("usage: quince device reset-working <udid> [--storage <name>]")
	}
	udid := args[1]
	return withStorage(func(mgr *storage.Manager) error {
		id, err := mgr.StorageIDByName(storageName)
		if err != nil {
			return err
		}
		status, reason := mgr.RepairWorking(udid, id)
		if status != 202 {
			// The API's refusal text verbatim — it already names the candidates and says to
			// choose one, and rewording it for the terminal would let the two drift.
			return errors.New(reason)
		}
		fmt.Println(reason)
		return nil
	})
}

func encWord(encrypted bool) string {
	if encrypted {
		return "encrypted"
	}
	return "unencrypted"
}

// authCmd is the console escape hatch — qn.6k slice 2, Operator ruling on quince#657. It ships
// BEFORE passkeys can be registered, because a credential that is the only way in, on a phone that
// is lost, locks the user out of their own backups.
//
// It opens the store DIRECTLY rather than through withStorage: recovery must work on a box whose
// disk is unreachable, and reconciling storage first would make it fail exactly there.
func authCmd(args []string) error {
	if len(args) == 0 || args[0] != "reset" {
		return errors.New("usage: quince auth reset --yes")
	}

	// --yes IS REQUIRED, and it is not ceremony. What follows a reset is not "the box is safe": it
	// is `needs_setup`, and POST /api/auth/setup is pre-auth by necessity — so between the reset and
	// somebody setting a new password, THE FIRST CALLER TO REACH THAT ENDPOINT OWNS THE BOX. On a
	// LAN that is a window with other people in it. The flag makes the operator say it on purpose;
	// the warning below tells them what they just opened.
	if len(args) != 2 || args[1] != "--yes" {
		return errors.New("usage: quince auth reset --yes  (refusing without --yes: this clears the " +
			"admin password and every passkey, and leaves quince in first-run setup, which the next " +
			"caller on the network can claim)")
	}

	bootstrap, bwarn := config.LoadBootstrap(os.Environ())
	log := newLogger()
	for _, w := range bwarn {
		log.Warn("bootstrap warning", "path", w.Path, "message", w.Message)
	}
	st, err := store.Open(bootstrap.DBPath())
	if err != nil {
		return fmt.Errorf("open db %s: %w", bootstrap.DBPath(), err)
	}
	defer func() { _ = st.Close() }()

	res, err := auth.Reset(st)
	if err != nil {
		// PARTIAL WORK IS REPORTED, NEVER SWALLOWED. auth.Reset returns what it had already done
		// when it failed, and printing that is the difference between "re-run it" and "work out
		// what state this box is in".
		fmt.Printf("auth reset: FAILED after %s\n", resetSummary(res))
		return err
	}

	fmt.Printf("auth reset: %s\n", resetSummary(res))
	fmt.Println("quince is now in first-run setup. THE NEXT CALLER TO REACH IT CAN SET THE PASSWORD —")
	fmt.Println("set one yourself before this box is reachable by anyone else.")
	return nil
}

// resetSummary says what actually happened in counts, never "done". A reset that removed 2 passkeys
// tells the operator this box had credentials; one that removed 0 tells them it did not. Both are
// facts they did not have before, and neither survives being summarised.
//
// AND IT NAMES THE DEVICE-SCOPED ONES SEPARATELY (qn.13 D9). A reset is a RECOVERY act — the admin
// lost their phone and is getting back in — and its consequence for other people is invisible in a
// flat total: "3 passkey(s) removed" does not tell them that two household members can no longer
// reach the devices they were given. They find out from the household member instead, which is the
// *state honesty* rule failing at the one moment it is load-bearing.
//
// SAID ONLY WHEN THERE WERE ANY. On an install that has never shared a device the clause is noise,
// and a parenthetical about a feature nobody used is how a summary stops being read.
func resetSummary(r auth.ResetResult) string {
	pw := "no password was set"
	if r.HadPassword {
		pw = "password cleared"
	}
	pk := fmt.Sprintf("%d passkey(s) removed", r.Passkeys)
	if r.ScopedPasskeys > 0 {
		// PLAIN ABOUT WHO IT AFFECTS rather than about the schema. "device-scoped" is quince's word
		// for it; the admin's question is who just lost access, so the sentence answers that.
		pk = fmt.Sprintf("%s (%d of them shared a single device with someone — that access is gone too)",
			pk, r.ScopedPasskeys)
	}
	return fmt.Sprintf("%s, %s, %d session(s) invalidated", pw, pk, r.Sessions)
}
