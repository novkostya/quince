package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
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
	fs := flag.NewFlagSet("backup", flag.ContinueOnError)
	transport := fs.String("transport", backup.TransportAuto, "usb | wifi | auto")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("usage: quince backup <udid> [--transport usb|wifi|auto]")
	}
	udid := fs.Arg(0)

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
	if code := backup.DriveToCompletion(ctx, ls.engine, eventBus, udid, *transport, os.Stdout); code != 0 {
		return errors.New("backup did not complete successfully")
	}
	return nil
}
