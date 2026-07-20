package backup

import (
	"context"
	"fmt"
	"io"

	"github.com/novkostya/quince/core/internal/bus"
	"github.com/novkostya/quince/core/internal/store"
	"github.com/novkostya/quince/core/internal/wire"
)

// DriveToCompletion starts one backup and streams its state changes to out until it terminates —
// the body of the `quince backup` CLI (the rung's own lab harness). It subscribes BEFORE starting
// so no transition is missed. Returns a process exit code: 0 on succeeded, 1 otherwise.
func DriveToCompletion(ctx context.Context, eng *Engine, b *bus.Bus, udid, transport string, out io.Writer) int {
	p := func(format string, a ...any) { _, _ = fmt.Fprintf(out, format, a...) }
	sub := b.Subscribe(256)
	defer b.Unsubscribe(sub)

	job, status, reason := eng.StartBackup(udid, transport, "")
	if status != 202 {
		p("cannot start backup: %s (status %d)\n", reason, status)
		return 1
	}
	p("started backup %s (%s over %s)\n", job.ID, udid, transport)

	last := job.State
	for {
		select {
		case <-ctx.Done():
			p("interrupted\n")
			return 1
		case env := <-sub.C():
			j, ok := env.Data.(wire.Job)
			if !ok || j.ID != job.ID {
				continue
			}
			if j.State != last {
				last = j.State
				p("  → %s%s\n", j.State, progressNote(j))
			}
			if store.JobIsTerminal(j.State) {
				if j.State == StateSucceeded {
					p("backup succeeded — version %s\n", strDeref(j.VersionID))
					return 0
				}
				p("backup %s: %s\n", j.State, jobErrMessage(j))
				return 1
			}
		}
	}
}

func progressNote(j wire.Job) string {
	if j.Progress.Phase == PhaseWaitingForPasscode {
		return " (enter the passcode on the device)"
	}
	if j.State == StateBackingUp && j.Progress.Percent != nil {
		return fmt.Sprintf(" (%.0f%%, %d files)", *j.Progress.Percent, j.Progress.FilesReceived)
	}
	return ""
}

func jobErrMessage(j wire.Job) string {
	if j.Error != nil {
		return j.Error.Message
	}
	return "no detail"
}

func strDeref(p *string) string {
	if p == nil {
		return "?"
	}
	return *p
}
