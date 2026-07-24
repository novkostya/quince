package demo

import (
	"net/http"
	"time"

	"github.com/novkostya/quince/core/internal/id"
	"github.com/novkostya/quince/core/internal/wire"
)

// The demo Provider implements httpapi.JobControl so `quince serve --demo` and the UI e2e drive the
// whole "Back up now" → progress → history + retry + cancel loop with NO hardware (qn.4b). qn.4a kept
// this 503 because no e2e posted jobs; qn.4b's e2e does (its own named condition, met). Scripted jobs
// move through the real state names with lifelike timing, and per-UDID single-flight is honored — the
// invariant the real engine enforces — shared between on-demand jobs and the ambient phone loop.

// udidSpare is the stable on-demand target seeded by Run() (NOT in the static seed, so goldens are
// unaffected): a paired, encryption-on iPhone present on USB+Wi-Fi that the ambient loop never
// touches, so "Back up now" and single-flight are deterministic against it.
const udidSpare = "00008130-0022446688AA0044"

// udidUnpaired is a fresh USB-connected but UNPAIRED device (also Run()-seeded), so the dashboard
// card's Pair button appears — the e2e proves the card Pair deep-links a pair intent that auto-opens
// the pairing dialog on the details page (qn.4b fix for (bq)).
const udidUnpaired = "00008120-0011002200330044"

// udidOffline is a powered-off device that HAS backups but no live transport (qn.6a): it proves the
// offline card (disabled "Back up now" with a reason, last-seen, version count) and a DEAD version.
const udidOffline = "00008110-0099887766554433"

// demoRun holds an in-flight scripted job so it can be cancelled and hold the single-flight slot.
type demoRun struct {
	jobID  string
	cancel chan struct{}
}

// startRun claims the single-flight slot for udid. ok=false if a job is already running for it.
func (p *Provider) startRun(udid, jobID string) (*demoRun, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, busy := p.running[udid]; busy {
		return nil, false
	}
	r := &demoRun{jobID: jobID, cancel: make(chan struct{})}
	p.running[udid] = r
	return r, true
}

func (p *Provider) endRun(udid string) {
	p.mu.Lock()
	delete(p.running, udid)
	p.mu.Unlock()
}

// StartBackup scripts an on-demand backup (contracts §1 POST /api/jobs). It mirrors the real
// engine's outcomes: 404 unknown device, 422 bad transport or auto-when-absent, 409 already running,
// 202 accepted. transport "auto" resolves against the fixture device's presence (design §4/(bp)).
func (p *Provider) StartBackup(udid, transport, retryOf string) (wire.Job, int, string) {
	p.mu.RLock()
	dev, ok := p.devices[udid]
	p.mu.RUnlock()
	if !ok {
		return wire.Job{}, http.StatusNotFound, "unknown device"
	}
	resolved, status, reason := demoResolveTransport(dev, transport)
	if status != 0 {
		return wire.Job{}, status, reason
	}

	jid := id.New()
	run, free := p.startRun(udid, jid)
	if !free {
		return wire.Job{}, http.StatusConflict, "a backup is already running for this device"
	}

	intent, attempt := jid, 1
	var retryPtr *string
	if retryOf != "" {
		if prev, ok := p.Job(retryOf); ok {
			intent, attempt = prev.IntentID, prev.Attempt+1
			retryPtr = strptr(retryOf)
		}
	}
	job := wire.Job{
		ID: jid, UDID: udid, Kind: "backup", Transport: resolved, State: "queued",
		Progress:  wire.JobProgress{Phase: "queued", Percent: f64ptr(0), Liveness: "active"},
		StartedAt: wire.Now(), RetryOf: retryPtr, IntentID: intent, Attempt: attempt,
	}
	p.mu.Lock()
	p.jobLog[jid] = nil
	p.mu.Unlock()
	p.setJob(job)
	go p.scriptBackup(run, job)
	return job, http.StatusAccepted, ""
}

// CancelJob cancels a running scripted job (contracts §1 POST /api/jobs/{id}/cancel → 202 Job). 409
// if the job is not running (already terminal / unknown-but-recorded), 404 if unknown.
func (p *Provider) CancelJob(jid string) (wire.Job, int, string) {
	p.mu.RLock()
	job, known := p.jobs[jid]
	var run *demoRun
	if r, ok := p.running[job.UDID]; ok && r.jobID == jid {
		run = r
	}
	p.mu.RUnlock()
	if !known {
		return wire.Job{}, http.StatusNotFound, "no such job"
	}
	if run == nil {
		return job, http.StatusConflict, "job is not running"
	}
	select {
	case <-run.cancel: // already cancelling
	default:
		close(run.cancel)
	}
	return job, http.StatusAccepted, ""
}

// scriptBackup drives one on-demand job through the state machine with lifelike timing, honoring
// cancellation and shutdown. Success commits a fresh version and links it; cancel ends `cancelled`.
func (p *Provider) scriptBackup(run *demoRun, job wire.Job) {
	defer p.endRun(job.UDID)

	steps := []struct {
		state, phase   string
		pct            float64
		files          int64
		liveness, note string
		wait           time.Duration
	}{
		{"queued", "queued", 0, 0, "active", "queued backup for " + job.UDID + " (" + job.Transport + ")", 900 * time.Millisecond},
		{"preflight", "preflight", 0, 0, "active", "preflight: validate ok · encryption on · disk ok", 900 * time.Millisecond},
		{"seeding", "seeding", 0, 0, "active", "preparing: cloning from your last backup…", 1200 * time.Millisecond},
		{"backing_up", "receiving", 28, 55, "active", "receiving files… 55", 900 * time.Millisecond},
		{"backing_up", "receiving", 66, 170, "active", "receiving files… 170", 900 * time.Millisecond},
		{"backing_up", "receiving", 94, 240, "active", "receiving files… 240", 900 * time.Millisecond},
		{"verifying", "verifying", 100, 255, "active", "verifying: Backup Successful · Manifest.db ok", 700 * time.Millisecond},
		{"committing", "committing", 100, 255, "active", "committing: latest/ rebuilt", 700 * time.Millisecond},
	}
	job.Progress.BytesTotal = 3_200_000_000

	for _, s := range steps {
		job.State = s.state
		job.Progress.Phase = s.phase
		if s.state == "seeding" {
			job.Progress.Percent = nil // indeterminate clone (O(files)) — not "0%"
			job.Progress.BytesDone = 0
		} else {
			job.Progress.Percent = f64ptr(s.pct)
			job.Progress.BytesDone = int64(float64(job.Progress.BytesTotal) * s.pct / 100)
		}
		job.Progress.FilesReceived = s.files
		job.Progress.Liveness = s.liveness
		p.setJob(job)
		if s.note != "" {
			p.logJobFor(job.ID, s.note)
		}
		switch p.waitStep(run, s.wait) {
		case "cancel":
			p.finishCancelled(&job)
			return
		case "stop":
			return
		}
	}

	ver := p.commitDemoVersionFor(job.UDID, job.ID)
	fin := wire.Now()
	job.State = "succeeded"
	job.Progress.Phase = "done"
	job.FinishedAt = &fin
	job.VersionID = &ver.ID
	p.setJob(job)
	p.logJobFor(job.ID, "backup completed · structure verified")
	p.bus.PublishEvent(wire.EventVersionCreated, ver)
	p.refreshLastBackup(job.UDID, job.ID, fin, "succeeded")
}

// waitStep sleeps for d, returning "ok" (elapsed), "cancel" (the job was cancelled via the API), or
// "stop" (the server is shutting down). Shared by on-demand jobs and the ambient phone loop.
func (p *Provider) waitStep(run *demoRun, d time.Duration) string {
	p.mu.RLock()
	ctx := p.baseCtx
	p.mu.RUnlock()
	t := time.NewTimer(d)
	defer t.Stop()
	var done <-chan struct{}
	if ctx != nil {
		done = ctx.Done()
	}
	select {
	case <-done:
		return "stop"
	case <-run.cancel:
		return "cancel"
	case <-t.C:
		return "ok"
	}
}

func (p *Provider) finishCancelled(job *wire.Job) {
	fin := wire.Now()
	job.State = "cancelled"
	job.FinishedAt = &fin
	job.Error = &wire.JobError{Code: "cancelled", Message: "cancelled"}
	p.setJob(*job)
	p.logJobFor(job.ID, "backup cancelled")
}

// demoResolveTransport mirrors the engine's resolveTransport (design §4/(bp)) for the fixture world.
func demoResolveTransport(dev wire.Device, requested string) (transport string, status int, reason string) {
	switch requested {
	case "usb", "wifi":
		return requested, 0, ""
	case "auto":
		switch {
		case dev.Transports.USB != nil:
			return "usb", 0, ""
		case dev.Transports.WiFi != nil:
			return "wifi", 0, ""
		default:
			return "", http.StatusUnprocessableEntity,
				"device is not currently connected — connect it over USB or Wi-Fi, or choose a transport"
		}
	default:
		return "", http.StatusUnprocessableEntity, "transport must be usb, wifi, or auto"
	}
}

// seedOnDemandDevice adds the stable "Back up now" target + a failed job (so the retry affordance is
// exercisable) at Run() time — kept OUT of the static seed so golden contract tests are unaffected.
func (p *Provider) seedOnDemandDevice() {
	now := wire.Now()
	dev := wire.Device{
		UDID: udidSpare, Name: "spare-iphone", Model: "iPhone16,1", IOSVersion: "26.0.1",
		Transports:       wire.Transports{USB: &now, WiFi: &now},
		Paired:           "yes",
		BackupEncryption: "on",
		LastSeen:         now,
	}
	failedID := id.New()
	failed := wire.Job{
		ID: failedID, UDID: udidSpare, Kind: "backup", Transport: "wifi", State: "connection_lost",
		Progress:  wire.JobProgress{Phase: "receiving", Percent: f64ptr(41), FilesReceived: 88, Liveness: "suspected_stall"},
		StartedAt: now, FinishedAt: &now,
		Error:    &wire.JobError{Code: "device_disconnected", Message: "the device left Wi-Fi mid-backup — reconnect and retry"},
		IntentID: failedID, Attempt: 1,
	}
	unpaired := wire.Device{
		UDID: udidUnpaired, Name: "new-iphone", Model: "iPhone16,2", IOSVersion: "26.0.1",
		Transports:       wire.Transports{USB: &now}, // USB only (pairing needs a cable)
		Paired:           "no",
		BackupEncryption: "unknown",
		LastSeen:         now,
	}
	// An OFFLINE device: no transports, but it has backups (a live one + a DEAD one) and a last-seen
	// in the past — proves the qn.6a offline card + the "artifact gone — remove?" dead-version row.
	offline := wire.Device{
		UDID: udidOffline, Name: "attic-ipad", Model: "iPad13,4", IOSVersion: "18.5",
		Transports:       wire.Transports{}, // powered off — no transport
		Paired:           "yes",
		BackupEncryption: "on",
		LastSeen:         "2026-07-18T09:15:00Z",
		LastBackup:       &wire.LastBackup{At: "2026-07-18T09:15:00Z", JobID: strptr(id.New()), Status: "succeeded"},
	}
	liveVerID, deadVerID := id.New(), id.New()
	liveVer := wire.Version{
		ID: liveVerID, UDID: udidOffline, Backend: "zfs",
		ZFSSnapshot: strptr("tank/backups/iphone-backup/" + udidOffline + "@quince-2026-07-18T09-15-" + liveVerID),
		BrowseRoot:  "/backups/" + udidOffline + "/.zfs/snapshot/quince-2026-07-18T09-15-" + liveVerID + "/latest",
		CreatedAt:   "2026-07-18T09:15:00Z", JobID: strptr(id.New()), Kind: "incremental",
		Encrypted: true, IsLatest: true, StructureVerifiedAt: strptr("2026-07-18T09:15:00Z"),
		LogicalBytes: 12_400_000_000, PhysicalBytes: 90_000_000,
	}
	// A DEAD version: its artifact is gone (reconciliation marked it missing). Rendered explicitly
	// dead — no size, no Unlock, a Remove action (qn.6a (cr)).
	deadVer := wire.Version{
		ID: deadVerID, UDID: udidOffline, Backend: "zfs",
		CreatedAt: "2026-07-10T09:15:00Z", JobID: strptr(id.New()), Kind: "full",
		Encrypted: true, IsLatest: false, Missing: true,
		LogicalBytes: 11_800_000_000, PhysicalBytes: 11_800_000_000,
	}

	p.mu.Lock()
	p.devices[udidSpare] = dev
	p.devices[udidUnpaired] = unpaired
	p.devices[udidOffline] = offline
	p.order = append(p.order, udidSpare, udidUnpaired, udidOffline)
	p.jobs[failedID] = failed
	p.versions[liveVerID] = liveVer
	p.versions[deadVerID] = deadVer
	p.verOrder = append(p.verOrder, liveVerID, deadVerID)
	p.mu.Unlock()
	p.bus.PublishEvent(wire.EventDeviceAttached, wire.DeviceEvent{Device: dev, Transport: "usb"})
	p.bus.PublishEvent(wire.EventDeviceAttached, wire.DeviceEvent{Device: unpaired, Transport: "usb"})
	p.bus.PublishEvent(wire.EventJobUpdated, failed)
}
