package demo

import (
	"context"
	"time"

	"github.com/novkostya/quince/core/internal/id"
	"github.com/novkostya/quince/core/internal/wire"
)

// Run starts the live timeline: device churn, a looping scripted backup (with a
// silent-stall → recovery arc mirroring lab reality), and periodic op / session /
// version events — so every WS event type fires end to end. It also seeds the on-demand
// backup target (qn.4b: a stable device the UI e2e drives "Back up now" against, plus a
// failed job so the retry affordance is exercisable). It returns immediately; goroutines
// exit when ctx is cancelled.
func (p *Provider) Run(ctx context.Context) {
	p.mu.Lock()
	p.baseCtx = ctx
	p.mu.Unlock()
	p.seedOnDemandDevice()
	go p.deviceChurn(ctx)
	go p.jobLoop(ctx)
	go p.miscEvents(ctx)
}

func sleep(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

func (p *Provider) setJob(j wire.Job) {
	p.mu.Lock()
	p.jobs[j.ID] = j
	p.mu.Unlock()
	p.bus.PublishEvent(wire.EventJobUpdated, j)
}

const demoLogCap = 500 // bound the per-job demo log buffer served by GET /api/jobs/{id}/log

func (p *Provider) logJob(chunk string) { p.logJobFor(jobID, chunk) }

func (p *Provider) logJobFor(jid, chunk string) {
	p.mu.Lock()
	buf := append(p.jobLog[jid], chunk)
	if len(buf) > demoLogCap {
		buf = buf[len(buf)-demoLogCap:]
	}
	p.jobLog[jid] = buf
	p.mu.Unlock()
	p.bus.PublishEvent(wire.EventJobLog, wire.JobLogChunk{JobID: jid, Chunk: chunk})
}

// deviceChurn toggles the iPad's Wi-Fi presence so the dashboard shows a device appearing
// and vanishing (story 1). A detached device is removed from the list; both REST and WS
// agree it is gone.
func (p *Provider) deviceChurn(ctx context.Context) {
	present := true
	for {
		if !sleep(ctx, 20*time.Second) {
			return
		}
		if present {
			p.mu.Lock()
			pad := p.devices[udidPad]
			delete(p.devices, udidPad)
			p.order = removeUDID(p.order, udidPad) // drop ONLY the pad — keep phone + on-demand devices
			p.mu.Unlock()
			p.bus.PublishEvent(wire.EventDeviceDetached, wire.DeviceEvent{Device: pad, Transport: "wifi"})
		} else {
			now := wire.Now()
			p.mu.Lock()
			pad := wire.Device{
				UDID: udidPad, Name: "studio-ipad", Model: "iPad13,4", IOSVersion: "18.5",
				Transports: wire.Transports{WiFi: &now}, Paired: "yes",
				BackupEncryption: "off", LastSeen: now,
			}
			p.devices[udidPad] = pad
			p.order = append(removeUDID(p.order, udidPad), udidPad) // re-add pad without dropping the others
			p.mu.Unlock()
			p.bus.PublishEvent(wire.EventDeviceAttached, wire.DeviceEvent{Device: pad, Transport: "wifi"})
		}
		present = !present
	}
}

// removeUDID returns order without udid, preserving every other entry. deviceChurn uses it so
// toggling the pad's Wi-Fi presence never drops the Run()-seeded on-demand devices (spare-iphone,
// new-iphone) — the qn.4b e2e (story 4) depends on their persistence past the first churn tick.
// Before this, churn hardcoded p.order to phone(+pad) and wiped the on-demand devices after 20 s,
// so story 4 only passed if it happened to run inside that window (flaked red on slower CI runners).
func removeUDID(order []string, udid string) []string {
	out := make([]string, 0, len(order))
	for _, u := range order {
		if u != udid {
			out = append(out, u)
		}
	}
	return out
}

// jobLoop re-drives the scripted backup forever with a pause between runs.
func (p *Provider) jobLoop(ctx context.Context) {
	for {
		if !p.runOneBackup(ctx) {
			return
		}
		if !sleep(ctx, 15*time.Second) {
			return
		}
	}
}

func (p *Provider) runOneBackup(ctx context.Context) bool {
	// Hold the single-flight slot for the phone so an on-demand "Back up now" for it (and the
	// engine invariant) stays honest; skip a cycle if something else already owns the phone.
	run, free := p.startRun(udidPhone, jobID)
	if !free {
		return sleep(ctx, 3*time.Second)
	}
	defer p.endRun(udidPhone)

	start := wire.Now()
	p.mu.Lock()
	p.jobLog[jobID] = nil // fresh run: reset the so-far log served over REST
	p.mu.Unlock()
	j := wire.Job{
		ID: jobID, UDID: udidPhone, Kind: "backup", Transport: "wifi",
		State:     "queued",
		Progress:  wire.JobProgress{Phase: "queued", Percent: f64ptr(0), BytesTotal: 3_600_000_000, Liveness: "active"},
		StartedAt: start, IntentID: intentID, Attempt: 1,
	}

	type step struct {
		state, phase string
		pct          float64
		files        int64
		liveness     string
		log          string
		wait         time.Duration
	}
	steps := []step{
		{"queued", "queued", 0, 0, "active", "queued backup for family-iphone (wifi)", 1500 * time.Millisecond},
		{"waiting_for_device", "waiting_for_device", 0, 0, "active", "waiting for device on wifi…", 1500 * time.Millisecond},
		{"preflight", "preflight", 0, 0, "active", "preflight: validate ok · encryption on · 18.2 GB free", 1500 * time.Millisecond},
		{"backing_up", "receiving", 12, 40, "active", "receiving files… 40", 1500 * time.Millisecond},
		{"backing_up", "receiving", 34, 120, "active", "receiving files… 120", 1500 * time.Millisecond},
		{"backing_up", "receiving", 52, 190, "silent_but_connected", "device is preparing… this can take several minutes", 2500 * time.Millisecond},
		{"backing_up", "receiving", 52, 190, "suspected_stall", "still connected, no data for a while (normal on wifi)", 2500 * time.Millisecond},
		{"backing_up", "receiving", 71, 260, "active", "receiving resumed… 260", 1500 * time.Millisecond},
		{"backing_up", "receiving", 92, 330, "active", "receiving files… 330", 1500 * time.Millisecond},
		{"verifying", "verifying", 100, 355, "active", "verifying: Backup Successful · Manifest.db ok · blobs resolve", 2000 * time.Millisecond},
		{"committing", "committing", 100, 355, "active", "committing: exchange working→latest · snapshot @quince-…", 2000 * time.Millisecond},
	}

	for _, s := range steps {
		j.State = s.state
		j.Progress.Phase = s.phase
		j.Progress.Percent = f64ptr(s.pct)
		j.Progress.BytesDone = int64(float64(j.Progress.BytesTotal) * s.pct / 100)
		j.Progress.FilesReceived = s.files
		j.Progress.Liveness = s.liveness
		p.setJob(j)
		if s.log != "" {
			p.logJob(s.log)
		}
		switch p.waitStep(run, s.wait) {
		case "cancel":
			p.finishCancelled(&j) // e.g. the fixed phone job was cancelled via the API
			return true           // the loop re-runs after its pause
		case "stop":
			return false
		}
	}

	// Success: create a fresh version, link it to the job, finish.
	newVer := p.commitDemoVersionFor(udidPhone, jobID)
	fin := wire.Now()
	j.State = "succeeded"
	j.Progress.Phase = "done"
	j.FinishedAt = &fin
	j.VersionID = &newVer.ID
	p.setJob(j)
	p.logJob("backup completed · structure verified")
	p.bus.PublishEvent(wire.EventVersionCreated, newVer)

	// Refresh the device's last_backup and announce it (device.updated) so the card doesn't
	// go stale — this is what exercises the device.updated WS event end to end.
	p.refreshLastBackup(udidPhone, jobID, fin, "succeeded")
	return true
}

// commitDemoVersionFor prepends a new fixture version for a device, trimming to a reasonable count.
func (p *Provider) commitDemoVersionFor(udid, jid string) wire.Version {
	now := wire.Now()
	vid := id.New()
	v := wire.Version{
		ID: vid, UDID: udid, Backend: "zfs",
		ZFSSnapshot:         strptr("tank/backups/iphone-backup/" + udid + "@quince-2026-07-18T02-30-" + vid),
		BrowseRoot:          "/backups/" + udid + "/.zfs/snapshot/quince-2026-07-18T02-30-" + vid + "/latest",
		CreatedAt:           now,
		JobID:               strptr(jid),
		Kind:                "incremental",
		Encrypted:           true,
		IsLatest:            true,
		StructureVerifiedAt: &now,
		LogicalBytes:        42_500_000_000,
		PhysicalBytes:       260_000_000,
	}
	p.mu.Lock()
	// demote the previous latest for this device
	for id2, prev := range p.versions {
		if prev.UDID == udid && prev.IsLatest {
			prev.IsLatest = false
			p.versions[id2] = prev
		}
	}
	p.versions[vid] = v
	p.verOrder = append([]string{vid}, p.verOrder...)
	if len(p.verOrder) > 8 { // trim oldest to bound growth
		drop := p.verOrder[len(p.verOrder)-1]
		p.verOrder = p.verOrder[:len(p.verOrder)-1]
		delete(p.versions, drop)
	}
	p.mu.Unlock()
	return v
}

// refreshLastBackup updates a device's last_backup summary and announces device.updated so the
// dashboard card never goes stale after a scripted job finishes.
func (p *Provider) refreshLastBackup(udid, jid, at, status string) {
	p.mu.Lock()
	dev, ok := p.devices[udid]
	if !ok {
		p.mu.Unlock()
		return
	}
	dev.LastBackup = &wire.LastBackup{At: at, JobID: &jid, Status: status}
	dev.LastSeen = at
	p.devices[udid] = dev
	p.mu.Unlock()
	p.bus.PublishEvent(wire.EventDeviceUpdated, dev)
}

// miscEvents exercises the remaining WS event types: op.updated (pair narration),
// session.locked, and a transient version create/delete.
func (p *Provider) miscEvents(ctx context.Context) {
	for {
		if !sleep(ctx, 40*time.Second) {
			return
		}
		// A short pair-op narration.
		op := wire.Op{ID: id.New(), UDID: udidPad, Kind: "pair", State: "running", Message: "starting pairing…"}
		p.bus.PublishEvent(wire.EventOpUpdated, op)
		if !sleep(ctx, 2*time.Second) {
			return
		}
		op.State = "waiting_for_user"
		op.Message = "tap Trust on the iPad, then enter its passcode"
		p.bus.PublishEvent(wire.EventOpUpdated, op)
		if !sleep(ctx, 3*time.Second) {
			return
		}
		op.State = "succeeded"
		op.Message = "paired"
		p.bus.PublishEvent(wire.EventOpUpdated, op)

		// A session.locked demo (no real session exists yet in qn.1).
		p.bus.PublishEvent(wire.EventSessionLocked, wire.SessionLocked{SessionID: id.New(), Reason: "ttl"})

		// A transient version that is created then deleted, to exercise version.deleted.
		tv := wire.Version{
			ID: id.New(), UDID: udidPad, Backend: "copy",
			BrowseRoot: "/backups/" + udidPad + "/latest", CreatedAt: wire.Now(),
			JobID: strptr(id.New()), Kind: "full", Encrypted: false, IsLatest: true,
			LogicalBytes: 8_000_000_000, PhysicalBytes: 8_000_000_000,
		}
		p.bus.PublishEvent(wire.EventVersionCreated, tv)
		if !sleep(ctx, 5*time.Second) {
			return
		}
		p.bus.PublishEvent(wire.EventVersionDeleted, tv)
	}
}
