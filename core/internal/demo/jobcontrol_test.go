package demo

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/novkostya/quince/core/internal/bus"
	"github.com/novkostya/quince/core/internal/wire"
)

func newRunningProvider(t *testing.T) *Provider {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
	p := NewProvider(bus.New(), log)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	p.Run(ctx) // seeds the on-demand spare device + failed job, sets baseCtx
	return p
}

func waitDemoTerminal(t *testing.T, p *Provider, id string, d time.Duration) wire.Job {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if j, ok := p.Job(id); ok && terminal(j.State) {
			return j
		}
		time.Sleep(5 * time.Millisecond)
	}
	j, _ := p.Job(id)
	t.Fatalf("demo job %s did not terminate in %v (state=%s)", id, d, j.State)
	return wire.Job{}
}

func terminal(s string) bool {
	return s == "succeeded" || s == "failed" || s == "cancelled" || s == "connection_lost"
}

// Story 9: the demo command surface is live — POST scripts a job that progresses to succeeded,
// auto resolves against the fixture device, a second concurrent start → 409, and the guards match
// the real engine (404 unknown device, 422 bad transport).
func TestDemoJobControlStartAndSingleFlight(t *testing.T) {
	p := newRunningProvider(t)

	job, status, reason := p.StartBackup(udidSpare, "auto", "")
	if status != 202 {
		t.Fatalf("start = %d (%s)", status, reason)
	}
	if job.Transport != "usb" { // spare is present on USB+WiFi → prefer USB
		t.Fatalf("auto resolved to %q, want usb", job.Transport)
	}

	if _, s, _ := p.StartBackup(udidSpare, "auto", ""); s != 409 {
		t.Fatalf("concurrent start for the same device = %d, want 409", s)
	}

	final := waitDemoTerminal(t, p, job.ID, 10*time.Second)
	if final.State != "succeeded" || final.VersionID == nil {
		t.Fatalf("job = %s version=%v, want succeeded with a version", final.State, final.VersionID)
	}

	if _, s, _ := p.StartBackup("no-such-udid", "auto", ""); s != 404 {
		t.Fatalf("unknown device = %d, want 404", s)
	}
	if _, s, _ := p.StartBackup(udidSpare, "carrier-pigeon", ""); s != 422 {
		t.Fatalf("bad transport = %d, want 422", s)
	}
}

// Story 9 (cancel): a running scripted job cancels to `cancelled`; an unknown job → 404.
func TestDemoJobControlCancel(t *testing.T) {
	p := newRunningProvider(t)

	job, status, _ := p.StartBackup(udidSpare, "usb", "")
	if status != 202 {
		t.Fatalf("start = %d", status)
	}
	if _, s, _ := p.CancelJob(job.ID); s != 202 {
		t.Fatalf("cancel = %d, want 202", s)
	}
	final := waitDemoTerminal(t, p, job.ID, 5*time.Second)
	if final.State != "cancelled" {
		t.Fatalf("state=%s, want cancelled", final.State)
	}
	if _, s, _ := p.CancelJob("no-such-job"); s != 404 {
		t.Fatalf("cancel unknown = %d, want 404", s)
	}
}

// Story 5 (demo path): a retry inherits intent_id and increments attempt — the seeded failed job for
// the spare device is retried, folding into one intent group.
func TestDemoJobControlRetryInheritsIntent(t *testing.T) {
	p := newRunningProvider(t)

	jobs, _ := p.Jobs(udidSpare, "", 10)
	var failed wire.Job
	for _, j := range jobs {
		if j.State == "connection_lost" {
			failed = j
		}
	}
	if failed.ID == "" {
		t.Fatal("expected a seeded failed job for the spare device")
	}

	retry, s, reason := p.StartBackup(udidSpare, "auto", failed.ID)
	if s != 202 {
		t.Fatalf("retry start = %d (%s)", s, reason)
	}
	if retry.RetryOf == nil || *retry.RetryOf != failed.ID {
		t.Fatalf("retry_of = %v, want %s", retry.RetryOf, failed.ID)
	}
	if retry.IntentID != failed.IntentID || retry.Attempt != failed.Attempt+1 {
		t.Fatalf("retry intent=%s attempt=%d, want %s/%d", retry.IntentID, retry.Attempt, failed.IntentID, failed.Attempt+1)
	}
}

// auto with a device present on NO transport → 422 (mirrors the engine, design §4/(bp)).
func TestDemoAutoWhenAbsentRefuses(t *testing.T) {
	p := newRunningProvider(t)
	// The iPad churns Wi-Fi on/off; construct a definitively-absent device to assert the rule.
	p.mu.Lock()
	p.devices["OFFLINEDEVICE0000000000000000"] = wire.Device{
		UDID: "OFFLINEDEVICE0000000000000000", Name: "offline", Paired: "yes", BackupEncryption: "on",
	}
	p.mu.Unlock()
	if _, s, _ := p.StartBackup("OFFLINEDEVICE0000000000000000", "auto", ""); s != 422 {
		t.Fatalf("auto with an absent device = %d, want 422", s)
	}
}
