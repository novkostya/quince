package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/novkostya/quince/core/internal/backup"
	"github.com/novkostya/quince/core/internal/bus"
	"github.com/novkostya/quince/core/internal/config"
	"github.com/novkostya/quince/core/internal/store"
)

// backupCmd is the minimal headless CLI (the lab harness). It builds the live stack and drives ONE
// backup for <udid> to completion, streaming state to stdout; exit 0 on succeeded, else nonzero.
// --transport defaults to auto (the engine resolves it against current presence — design §4/(bp));
// usb|wifi are explicit.
func backupCmd(args []string) error {
	udid, transport, err := parseBackupArgs(args)
	if err != nil {
		return err
	}

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

	ls := buildLiveStack(ctx, bootstrap, cfgSvc, st, eventBus, log)
	if code := backup.DriveToCompletion(ctx, ls.engine, eventBus, udid, transport, os.Stdout); code != 0 {
		return errors.New("backup did not complete successfully")
	}
	return nil
}

// parseBackupArgs parses the backup subcommand args into (udid, transport). Extracted so the arg
// handling is unit-testable without the live stack.
func parseBackupArgs(args []string) (udid, transport string, err error) {
	fs := flag.NewFlagSet("backup", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	tp := fs.String("transport", backup.TransportAuto, "usb | wifi | auto")
	// Go's flag package stops at the first non-flag token, so a bare Parse drops any flag placed
	// AFTER the positional udid (`backup <udid> --transport usb`). Loop: consume leading flags,
	// peel off the positional, repeat — so flags before OR after the udid are all honoured.
	var positional []string
	rest := args
	for {
		if perr := fs.Parse(rest); perr != nil {
			return "", "", perr
		}
		rest = fs.Args()
		if len(rest) == 0 {
			break
		}
		positional = append(positional, rest[0])
		rest = rest[1:]
	}
	if len(positional) != 1 {
		return "", "", errors.New("usage: quince backup <udid> [--transport usb|wifi|auto]")
	}
	return positional[0], *tp, nil
}
