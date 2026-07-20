package storage

import (
	"context"
	"os/exec"
	"syscall"
)

// setpgid puts a child at the head of its own process group and SIGKILLs the whole group on
// ctx cancellation (design §1/§6 subprocess hygiene — the same discipline as internal/muxsup
// and internal/deviceops). zfs ops are short-lived one-shots, so this is the group-kill guard.
func setpgid(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process != nil {
			return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
		return nil
	}
}

// execRun runs argv (never a shell string) with the process-group hygiene, returning combined
// output. It is the production zfsCLI.run; tests inject a fake that records argv + simulates.
func execRun(ctx context.Context, argv []string) (string, error) {
	if len(argv) == 0 {
		return "", context.Canceled
	}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	setpgid(cmd)
	out, err := cmd.CombinedOutput()
	return string(out), err
}
