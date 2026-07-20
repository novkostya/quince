package deviceops

import (
	"context"
	"fmt"
	"os/exec"
	"strings"

	"github.com/creack/pty"
)

// The backup-encryption password NEVER reaches idevicebackup2 via argv (world-readable
// /proc/<pid>/cmdline) or an env var. Instead we run the CLI in interactive mode (-i) over a
// controlling pty and type the password(s) at its getpass prompts — the pty fd is private to
// the process, and the password is never in the argument vector, the environment, or a log
// (design §6; interface fact 1, verified live: this build supports -i). This is the load-
// bearing secret path; story 5 proves the password appears in no argv/log/audit.

// ptyAnswer feeds one secret at a prompt: when promptSubstr appears in the child's output
// (case-insensitive), value is typed. Substrings are chosen to be unambiguous in order
// ("old backup password" before "new backup password").
type ptyAnswer struct {
	promptSubstr string
	value        string
}

// deviceConfirmMarkers detect the "confirm on the device by entering the passcode" phase so
// the manager can narrate waiting_for_user (assisted model, stack D13).
func isDeviceConfirm(s string) bool {
	l := strings.ToLower(s)
	return strings.Contains(l, "passcode") && strings.Contains(l, "the device")
}

// runInteractiveBackup2 runs `idevicebackup2 -i …` over a pty, answering its password
// prompts from answers (in order) and firing onDeviceConfirm once the device-side passcode
// step begins. It returns the full captured output and the child's exit error. args must NOT
// contain any password (interface fact 1 — argv is forbidden).
func (t *Tools) runInteractiveBackup2(ctx context.Context, transport string, args []string, answers []ptyAnswer, onDeviceConfirm func()) (string, error) {
	cmd := exec.CommandContext(ctx, t.Idevicebackup2, t.args(args...)...)
	cmd.Env = t.childEnv(transport)
	cancelKillGroup(cmd) // pty.Start sets Setsid (own session/group); ctx-cancel kills the group

	f, err := pty.Start(cmd)
	if err != nil {
		return "", fmt.Errorf("idevicebackup2 pty start: %w", err)
	}
	defer func() { _ = f.Close() }()

	captured := feedPTY(f, answers, onDeviceConfirm)

	// Reap the child. A pty read on Linux may surface EIO instead of EOF once the child
	// exits; feedPTY treats any read error as end-of-stream, so Wait carries the real status.
	if err := cmd.Wait(); err != nil {
		return captured, fmt.Errorf("idevicebackup2: %w: %s", err, strings.TrimSpace(lastLine(captured)))
	}
	return captured, nil
}

// feedPTY reads the child's pty output, typing each answer when its prompt appears (resetting
// the pending window after each write so a repeated identical prompt — e.g. the confirm entry
// for `encryption on` — is answered again), and firing onDeviceConfirm once. It returns all
// captured output when the stream ends (EOF/EIO at child exit).
func feedPTY(f interface {
	Read([]byte) (int, error)
	WriteString(string) (int, error)
}, answers []ptyAnswer, onDeviceConfirm func()) string {
	var captured, pending strings.Builder
	confirmFired := false
	ai := 0
	buf := make([]byte, 512)
	for {
		n, err := f.Read(buf)
		if n > 0 {
			chunk := string(buf[:n])
			captured.WriteString(chunk)
			pending.WriteString(chunk)

			if !confirmFired && onDeviceConfirm != nil && isDeviceConfirm(captured.String()) {
				confirmFired = true
				onDeviceConfirm()
			}
			for ai < len(answers) && containsFold(pending.String(), answers[ai].promptSubstr) {
				_, _ = f.WriteString(answers[ai].value + "\n")
				ai++
				pending.Reset()
			}
		}
		if err != nil {
			return captured.String()
		}
	}
}

func containsFold(haystack, needle string) bool {
	return strings.Contains(strings.ToLower(haystack), strings.ToLower(needle))
}

func lastLine(s string) string {
	s = strings.TrimRight(s, "\r\n")
	if i := strings.LastIndexAny(s, "\r\n"); i >= 0 {
		return s[i+1:]
	}
	return s
}

// backup2Opts builds the interactive option prefix: -i (request passwords interactively) -u
// UDID, plus -n for a Wi-Fi (network) device. No password ever appears here.
func backup2Opts(udid, transport string) []string {
	opts := []string{"-i", "-u", udid}
	return append(opts, networkArgs(transport)...)
}

// Encryption enables or disables backup encryption on the device (contracts §1 action
// enable/disable → `idevicebackup2 encryption on|off`). The password is typed over the pty.
// enable prompts for the new password twice (set + confirm); disable prompts for the current
// password once. onDeviceConfirm fires when the phone's passcode-confirm step begins.
func (t *Tools) Encryption(ctx context.Context, udid, transport string, enable bool, password string, onDeviceConfirm func()) error {
	if !validUDID(udid) {
		return ErrBadUDID
	}
	state := "off"
	answers := []ptyAnswer{{"backup password", password}}
	if enable {
		state = "on"
		answers = []ptyAnswer{{"backup password", password}, {"backup password", password}} // set + confirm
	}
	args := append(backup2Opts(udid, transport), "encryption", state)
	_, err := t.runInteractiveBackup2(ctx, transport, args, answers, onDeviceConfirm)
	return err
}

// ChangePassword changes the device backup password (contracts §1 action change_password →
// `idevicebackup2 changepw`): prompts old, then new (with a confirm entry). Both secrets are
// typed over the pty, never argv/env.
func (t *Tools) ChangePassword(ctx context.Context, udid, transport, oldPassword, newPassword string, onDeviceConfirm func()) error {
	if !validUDID(udid) {
		return ErrBadUDID
	}
	answers := []ptyAnswer{
		{"old backup password", oldPassword},
		{"new backup password", newPassword},
		{"new backup password", newPassword}, // confirm entry, if the CLI asks twice
	}
	args := append(backup2Opts(udid, transport), "changepw")
	_, err := t.runInteractiveBackup2(ctx, transport, args, answers, onDeviceConfirm)
	return err
}
