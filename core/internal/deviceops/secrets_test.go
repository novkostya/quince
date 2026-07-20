package deviceops

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/novkostya/quince/core/internal/bus"
)

// TestPasswordNeverInArgvEnvOrLog is qn.3's headline gate (story 5): the backup-encryption
// password must reach idevicebackup2 ONLY over the controlling pty — never argv (world-
// readable /proc/<pid>/cmdline), never an env var, never a log line, never the audit trail.
func TestPasswordNeverInArgvEnvOrLog(t *testing.T) {
	const secret = "SECRET-PW-should-never-leak-9f3x"

	capPath := filepath.Join(t.TempDir(), "capture.json")
	devs := newFakeDevices()
	devs.add(usbDevice(fakeUDID))

	var logBuf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	m := NewManager(context.Background(),
		fakeTools("DEVICEOPS_FAKE=paired", "DEVICEOPS_CAPTURE="+capPath),
		devs, bus.New(), nil, logger)

	opID, status, _ := m.Encryption(context.Background(), fakeUDID, "enable", secret, "", "")
	if status != 202 {
		t.Fatalf("enable status = %d", status)
	}
	op := waitOp(t, m, opID)
	if op.State != "succeeded" {
		t.Fatalf("enable op = %+v", op)
	}

	// The child recorded its argv/env and what it actually received over the pty.
	raw, err := os.ReadFile(capPath)
	if err != nil {
		t.Fatalf("read capture: %v", err)
	}
	var cap capture
	if err := json.Unmarshal(raw, &cap); err != nil {
		t.Fatalf("parse capture: %v", err)
	}

	for _, a := range cap.Argv {
		if strings.Contains(a, secret) {
			t.Fatalf("password leaked into argv: %q", a)
		}
	}
	for _, e := range cap.Env {
		if strings.Contains(e, secret) {
			t.Fatalf("password leaked into env: %q", e)
		}
	}
	// It DID arrive over the pty (proving the wrapper delivered it the only allowed way).
	got := strings.Join(cap.Received, "|")
	if !strings.Contains(got, secret) {
		t.Fatalf("password never reached the child over the pty (received=%q)", got)
	}
	// Nor in any log line, the op message, or the op error.
	if strings.Contains(logBuf.String(), secret) {
		t.Fatal("password leaked into a log line")
	}
	if strings.Contains(op.Message, secret) || (op.Error != nil && strings.Contains(op.Error.Message, secret)) {
		t.Fatalf("password leaked into the op: %+v", op)
	}
}

// TestFailedEncryptionErrorCarriesNoSecret guards the failure path too (state honesty +
// secrets): a failed op reports an error without the password.
func TestFailedEncryptionErrorCarriesNoSecret(t *testing.T) {
	const secret = "SECRET-PW-fail-path-7k2q"
	devs := newFakeDevices()
	devs.add(usbDevice(fakeUDID))
	m := newTestManager(t, devs, "DEVICEOPS_FAKE=enc_fail")

	opID, _, _ := m.Encryption(context.Background(), fakeUDID, "enable", secret, "", "")
	op := waitOp(t, m, opID)
	if op.State != "failed" || op.Error == nil {
		t.Fatalf("want failed op with error, got %+v", op)
	}
	if strings.Contains(op.Error.Message, secret) || strings.Contains(op.Message, secret) {
		t.Fatalf("password leaked into a failed op: %+v", op)
	}
}
