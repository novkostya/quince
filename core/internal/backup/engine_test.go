package backup

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/novkostya/quince/core/internal/bus"
	"github.com/novkostya/quince/core/internal/id"
	"github.com/novkostya/quince/core/internal/storage"
	"github.com/novkostya/quince/core/internal/store"
	"github.com/novkostya/quince/core/internal/wire"
)

// The stories run the REAL engine against a REAL qn.5 storage.Manager (copy backend on a temp
// /backups) + a REAL store + the fake idevicebackup2 — a true end-to-end integration, no phone.

const testUDID = "00008110000A1B2C3D4E5F60"

type harness struct {
	eng *Engine
	mgr *storage.Manager
	dev *fakeDevices
	bus *bus.Bus
	st  *store.Store
	dir string
}

func testCfg() Config {
	return Config{
		LivenessTimeout:      250 * time.Millisecond,
		SampleInterval:       8 * time.Millisecond,
		WaitForDeviceTimeout: 2 * time.Second,
		ProgressThrottle:     time.Millisecond,
		DiskLowFreeBytes:     0,
		RequireEncryption:    true,
	}
}

func newHarness(t *testing.T, p fakeParams, transport string, mods ...func(*Options, *fakeDevices)) *harness {
	t.Helper()
	dir := t.TempDir()
	backups := filepath.Join(dir, "backups")
	if err := os.MkdirAll(backups, 0o755); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(filepath.Join(dir, "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	b := bus.New()
	log := slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
	backend, name, _ := storage.Select(context.Background(),
		storage.Options{Backend: storage.BackendCopy, Backups: backups, AppVersion: "test"}, log)
	mgr := storage.NewManager(backend, name, st, st, b, backups,
		storage.RetentionPolicy{KeepRecent: 10}, id.New, log)

	dev := newFakeDevices()
	dev.set(testUDID, transport, "on")

	o := Options{
		BaseCtx: context.Background(), Store: st, Storage: mgr, VersionQ: mgr, Devices: dev, Bus: b,
		Log: log, Config: testCfg(), Backups: backups, NewID: id.New,
		Now:       func() time.Time { return time.Now().UTC() },
		FreeSpace: func(string) (uint64, error) { return 100 << 30, nil },
		Tool: ToolConfig{Bin: os.Args[0], ArgPrefix: fakeArgPrefix(), Env: fakeToolEnv(p),
			TargetRoot: filepath.Join(dir, "targets")},
	}
	for _, m := range mods {
		m(&o, dev)
	}
	return &harness{eng: New(o), mgr: mgr, dev: dev, bus: b, st: st, dir: dir}
}

func (h *harness) start(t *testing.T, transport, retryOf string) wire.Job {
	t.Helper()
	j, status, reason := h.eng.StartBackup(testUDID, transport, retryOf)
	if status != 202 {
		t.Fatalf("StartBackup: status=%d reason=%q", status, reason)
	}
	return j
}

func waitTerminal(t *testing.T, e *Engine, id string, d time.Duration) wire.Job {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if j, ok := e.Job(id); ok && isTerminal(j.State) {
			return j
		}
		time.Sleep(4 * time.Millisecond)
	}
	j, _ := e.Job(id)
	t.Fatalf("job %s did not terminate within %v (state=%s)", id, d, j.State)
	return wire.Job{}
}

func waitState(t *testing.T, e *Engine, id, state string, d time.Duration) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if j, ok := e.Job(id); ok && j.State == state {
			return
		}
		time.Sleep(3 * time.Millisecond)
	}
	j, _ := e.Job(id)
	t.Fatalf("job %s never reached %s within %v (state=%s)", id, state, d, j.State)
}

func isTerminal(s string) bool {
	return s == StateSucceeded || s == StateFailed || s == StateCancelled || s == StateConnectionLost
}

// --- fixture meta ---

type metaFile struct {
	Name            string `json:"name"`
	Transport       string `json:"transport"`
	TerminalState   string `json:"terminal_state"`
	ExitCode        int    `json:"exit_code"`
	Encrypted       bool   `json:"encrypted"`
	Kind            string `json:"kind"`
	Tree            string `json:"tree"`
	LineDelayMs     int    `json:"line_delay_ms"`
	StallAfterLine  int    `json:"stall_after_line"`
	StallMs         int    `json:"stall_ms"`
	StallChurnsTree bool   `json:"stall_churns_tree"`
	HangAfterLast   bool   `json:"hang_after_last"`
}

func loadMeta(t *testing.T, name string) metaFile {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "transcripts", name+".meta.json"))
	if err != nil {
		t.Fatal(err)
	}
	var m metaFile
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func (m metaFile) params(t *testing.T) fakeParams {
	return fakeParams{
		TranscriptPath: transcriptPath(t, m.Name+".txt"), Tree: m.Tree, Encrypted: m.Encrypted,
		Kind: m.Kind, ExitCode: m.ExitCode, Hang: m.HangAfterLast, StallAfter: m.StallAfterLine,
		StallMs: m.StallMs, StallChurn: m.StallChurnsTree, LineDelayMs: m.LineDelayMs,
	}
}

// --- fake device registry ---

type fakeDevices struct {
	mu   sync.Mutex
	devs map[string]wire.Device
}

func newFakeDevices() *fakeDevices { return &fakeDevices{devs: map[string]wire.Device{}} }

func (f *fakeDevices) set(udid, transport, enc string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	now := "2026-07-20T00:00:00Z"
	tr := wire.Transports{}
	if transport == TransportWiFi {
		tr.WiFi = &now
	} else {
		tr.USB = &now
	}
	f.devs[udid] = wire.Device{UDID: udid, Name: "test-iphone", Transports: tr, Paired: "yes",
		BackupEncryption: enc, LastSeen: now}
}

func (f *fakeDevices) remove(udid string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.devs, udid)
}

func (f *fakeDevices) Device(udid string) (wire.Device, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	d, ok := f.devs[udid]
	return d, ok
}

// eventCollector drains the bus and records the job.updated phases and job.log presence seen.
type eventCollector struct {
	mu     sync.Mutex
	phases map[string]bool
	logs   []string
}

func collect(t *testing.T, b *bus.Bus) (*eventCollector, func()) {
	sub := b.Subscribe(1024)
	ec := &eventCollector{phases: map[string]bool{}}
	done := make(chan struct{})
	go func() {
		for {
			select {
			case <-done:
				return
			case env := <-sub.C():
				ec.mu.Lock()
				switch d := env.Data.(type) {
				case wire.Job:
					ec.phases[d.Progress.Phase] = true
				case wire.JobLogChunk:
					ec.logs = append(ec.logs, d.Chunk)
				}
				ec.mu.Unlock()
			}
		}
	}()
	return ec, func() { close(done); b.Unsubscribe(sub) }
}

func (ec *eventCollector) sawPhase(p string) bool {
	ec.mu.Lock()
	defer ec.mu.Unlock()
	return ec.phases[p]
}

func (ec *eventCollector) logContains(s string) bool {
	ec.mu.Lock()
	defer ec.mu.Unlock()
	for _, c := range ec.logs {
		if strings.Contains(c, s) {
			return true
		}
	}
	return false
}

// ============================ Stories ============================

// Story 2: a clean full encrypted USB backup drives the state machine to a committed, verified
// version in qn.5 storage.
func TestStoryFullUSBSuccess(t *testing.T) {
	m := loadMeta(t, "full-usb-success")
	h := newHarness(t, m.params(t), m.Transport)
	job := h.start(t, m.Transport, "")
	final := waitTerminal(t, h.eng, job.ID, 5*time.Second)
	if final.State != StateSucceeded {
		t.Fatalf("state=%s error=%v", final.State, final.Error)
	}
	if final.VersionID == nil {
		t.Fatal("succeeded job carries no version_id")
	}
	vs := h.mgr.Versions(testUDID)
	if len(vs) != 1 {
		t.Fatalf("want 1 committed version, got %d", len(vs))
	}
	if vs[0].ID != *final.VersionID {
		t.Fatalf("job version_id %s != committed %s", *final.VersionID, vs[0].ID)
	}
	if vs[0].StructureVerifiedAt == nil {
		t.Fatal("committed version is not structure-verified")
	}
	if !vs[0].Encrypted || vs[0].Kind != "full" {
		t.Fatalf("version encrypted=%v kind=%s, want true/full", vs[0].Encrypted, vs[0].Kind)
	}
}

// Story 3: the passcode prompt surfaces the waiting_for_passcode phase and the liveness clock
// PAUSES across the wait — a 300 ms silent no-churn gap survives a 150 ms timeout only because of
// the pause, so reaching succeeded proves it.
func TestStoryWaitingForPasscode(t *testing.T) {
	m := loadMeta(t, "waiting-for-passcode")
	h := newHarness(t, m.params(t), m.Transport)
	ec, stop := collect(t, h.bus)
	defer stop()
	job := h.start(t, m.Transport, "")
	final := waitTerminal(t, h.eng, job.ID, 5*time.Second)
	if final.State != StateSucceeded {
		t.Fatalf("state=%s error=%v (the passcode pause did not hold)", final.State, final.Error)
	}
	if !ec.sawPhase(PhaseWaitingForPasscode) {
		t.Fatal("never surfaced the waiting_for_passcode phase")
	}
}

// Story 4 (headline): a Wi-Fi torn session freezes → the engine ends connection_lost via the
// liveness timeout, discards the work, commits NO version, and leaves latest/ untouched.
func TestStoryWifiTornSession(t *testing.T) {
	m := loadMeta(t, "wifi-torn-session")
	h := newHarness(t, m.params(t), m.Transport)
	job := h.start(t, m.Transport, "")
	final := waitTerminal(t, h.eng, job.ID, 5*time.Second)
	if final.State != StateConnectionLost {
		t.Fatalf("state=%s, want connection_lost", final.State)
	}
	if final.VersionID != nil {
		t.Fatal("a torn session must not commit a version")
	}
	if vs := h.mgr.Versions(testUDID); len(vs) != 0 {
		t.Fatalf("latest/ must be untouched — got %d versions", len(vs))
	}
}

// Story 5: a multi-minute silence where the tree still churns is NOT a stall — the job survives a
// 400 ms churning stall under a 150 ms timeout and completes.
func TestStorySilentStallSurvives(t *testing.T) {
	m := loadMeta(t, "silent-stall")
	h := newHarness(t, m.params(t), m.Transport)
	job := h.start(t, m.Transport, "")
	final := waitTerminal(t, h.eng, job.ID, 6*time.Second)
	if final.State != StateSucceeded {
		t.Fatalf("state=%s — a churning silence was wrongly treated as a stall", final.State)
	}
}

// Story 6: process success (exit 0 + Backup Successful) but a tree that fails storage.Verify →
// failed, no version. Reuses the happy transcript with a torn tree.
func TestStoryVerifyGate(t *testing.T) {
	m := loadMeta(t, "full-usb-success")
	p := m.params(t)
	p.Tree = "torn"
	h := newHarness(t, p, m.Transport)
	job := h.start(t, m.Transport, "")
	final := waitTerminal(t, h.eng, job.ID, 5*time.Second)
	if final.State != StateFailed {
		t.Fatalf("state=%s, want failed", final.State)
	}
	if final.Error == nil || final.Error.Code != ErrVerifyFailed {
		t.Fatalf("error=%v, want %s", final.Error, ErrVerifyFailed)
	}
	if vs := h.mgr.Versions(testUDID); len(vs) != 0 {
		t.Fatalf("no version may exist after a verify failure, got %d", len(vs))
	}
}

// Story 7: never two jobs for one UDID; a different UDID runs concurrently.
func TestStorySingleFlight(t *testing.T) {
	m := loadMeta(t, "silent-stall") // runs long enough to still be in flight
	h := newHarness(t, m.params(t), m.Transport)
	j1 := h.start(t, m.Transport, "")
	waitState(t, h.eng, j1.ID, StateBackingUp, 2*time.Second)

	_, s2, _ := h.eng.StartBackup(testUDID, m.Transport, "")
	if s2 != 409 {
		t.Fatalf("second start for same UDID = %d, want 409", s2)
	}

	other := "00008110FFEEDDCCBBAA9988"
	h.dev.set(other, m.Transport, "on")
	_, s3, _ := h.eng.StartBackup(other, m.Transport, "")
	if s3 != 202 {
		t.Fatalf("different UDID = %d, want 202", s3)
	}

	waitTerminal(t, h.eng, j1.ID, 6*time.Second)
	if j3, ok := h.eng.Job(""); ok {
		_ = j3
	}
}

// Story 8: cancel kills the process group; the job ends cancelled with no version.
func TestStoryCancel(t *testing.T) {
	m := loadMeta(t, "silent-stall")
	h := newHarness(t, m.params(t), m.Transport)
	j := h.start(t, m.Transport, "")
	waitState(t, h.eng, j.ID, StateBackingUp, 2*time.Second)

	_, cs, reason := h.eng.CancelJob(j.ID)
	if cs != 202 {
		t.Fatalf("cancel = %d (%s), want 202", cs, reason)
	}
	final := waitTerminal(t, h.eng, j.ID, 5*time.Second)
	if final.State != StateCancelled {
		t.Fatalf("state=%s, want cancelled", final.State)
	}
	if vs := h.mgr.Versions(testUDID); len(vs) != 0 {
		t.Fatalf("no version after cancel, got %d", len(vs))
	}
}

// Cancel while still waiting for the device → cancelled (not a misleading connection_lost).
func TestCancelDuringWaitForDevice(t *testing.T) {
	m := loadMeta(t, "full-usb-success")
	h := newHarness(t, m.params(t), m.Transport, func(o *Options, d *fakeDevices) {
		o.Config.WaitForDeviceTimeout = 5 * time.Second
		d.remove(testUDID) // absent → the job parks in waiting_for_device
	})
	j := h.start(t, m.Transport, "")
	waitState(t, h.eng, j.ID, StateWaitingForDevice, 2*time.Second)
	if _, cs, _ := h.eng.CancelJob(j.ID); cs != 202 {
		t.Fatalf("cancel = %d, want 202", cs)
	}
	final := waitTerminal(t, h.eng, j.ID, 3*time.Second)
	if final.State != StateCancelled {
		t.Fatalf("cancel during waiting_for_device = %s, want cancelled", final.State)
	}
}

// Story 9a: absent device → failed device_not_visible, no process.
func TestStoryPreflightDeviceAbsent(t *testing.T) {
	m := loadMeta(t, "full-usb-success")
	h := newHarness(t, m.params(t), m.Transport, func(o *Options, d *fakeDevices) {
		o.Config.WaitForDeviceTimeout = 120 * time.Millisecond
		d.remove(testUDID)
	})
	job := h.start(t, m.Transport, "")
	final := waitTerminal(t, h.eng, job.ID, 5*time.Second)
	if final.State != StateFailed || final.Error == nil || final.Error.Code != ErrDeviceNotVisible {
		t.Fatalf("state=%s error=%v, want failed/%s", final.State, final.Error, ErrDeviceNotVisible)
	}
	if len(h.mgr.Versions(testUDID)) != 0 {
		t.Fatal("no version when the device never appeared")
	}
}

// Story 9b: require_encryption + WillEncrypt=false → actionable preflight fail, no process.
func TestStoryPreflightEncryptionRequired(t *testing.T) {
	m := loadMeta(t, "full-usb-success")
	h := newHarness(t, m.params(t), m.Transport, func(o *Options, d *fakeDevices) {
		d.set(testUDID, m.Transport, "off")
	})
	job := h.start(t, m.Transport, "")
	final := waitTerminal(t, h.eng, job.ID, 5*time.Second)
	if final.State != StateFailed || final.Error == nil || final.Error.Code != ErrEncryptionRequired {
		t.Fatalf("state=%s error=%v, want failed/%s", final.State, final.Error, ErrEncryptionRequired)
	}
}

// Story 9c: policy relaxed + unencrypted device → proceeds; the committed version is badged
// encrypted:false (no silent downgrade).
func TestStoryPreflightEncryptionRelaxed(t *testing.T) {
	m := loadMeta(t, "full-usb-success")
	p := m.params(t)
	p.Encrypted = false // an unencrypted tree (real SQLite Manifest.db) that passes plain verify
	h := newHarness(t, p, m.Transport, func(o *Options, d *fakeDevices) {
		o.Config.RequireEncryption = false
		d.set(testUDID, m.Transport, "off")
	})
	job := h.start(t, m.Transport, "")
	final := waitTerminal(t, h.eng, job.ID, 5*time.Second)
	if final.State != StateSucceeded {
		t.Fatalf("state=%s error=%v, want succeeded", final.State, final.Error)
	}
	vs := h.mgr.Versions(testUDID)
	if len(vs) != 1 || vs[0].Encrypted {
		t.Fatalf("want 1 version badged encrypted:false, got %+v", vs)
	}
}

// Story 10: the job reads + events + log serve correctly.
func TestStoryJobsReadAndEvents(t *testing.T) {
	m := loadMeta(t, "full-usb-success")
	h := newHarness(t, m.params(t), m.Transport)
	ec, stop := collect(t, h.bus)
	defer stop()
	job := h.start(t, m.Transport, "")
	final := waitTerminal(t, h.eng, job.ID, 5*time.Second)
	if final.State != StateSucceeded {
		t.Fatalf("state=%s", final.State)
	}
	got, ok := h.eng.Job(job.ID)
	if !ok || got.State != StateSucceeded {
		t.Fatalf("Job() = %+v ok=%v", got, ok)
	}
	logtxt, ok := h.eng.JobLog(job.ID)
	if !ok || !strings.Contains(logtxt, "Backup Successful") {
		t.Fatalf("JobLog missing the success line: ok=%v", ok)
	}
	list, _ := h.eng.Jobs(testUDID, "", 50)
	if len(list) != 1 || list[0].ID != job.ID {
		t.Fatalf("Jobs() = %d rows", len(list))
	}
	if !ec.sawPhase(PhaseDone) {
		t.Fatal("no terminal job.updated event")
	}
	if !ec.logContains("Backup Successful") {
		t.Fatal("no job.log chunk carried the success line")
	}
	if _, ok := h.eng.JobLog("no-such-job"); ok {
		t.Fatal("JobLog of an unknown job must report not-found")
	}
}

// Story 12: retry-chain fields — a first job is its own intent; a retry inherits intent_id and
// increments attempt.
func TestStoryRetryChainFields(t *testing.T) {
	m := loadMeta(t, "wifi-torn-session")
	h := newHarness(t, m.params(t), m.Transport)
	j1 := h.start(t, m.Transport, "")
	f1 := waitTerminal(t, h.eng, j1.ID, 5*time.Second)
	if f1.IntentID != j1.ID || f1.Attempt != 1 || f1.RetryOf != nil {
		t.Fatalf("first job: intent=%s attempt=%d retry_of=%v", f1.IntentID, f1.Attempt, f1.RetryOf)
	}
	j2, s2, reason := h.eng.StartBackup(testUDID, m.Transport, j1.ID)
	if s2 != 202 {
		t.Fatalf("retry start = %d (%s)", s2, reason)
	}
	got2, _ := h.eng.Job(j2.ID)
	if got2.RetryOf == nil || *got2.RetryOf != j1.ID {
		t.Fatalf("retry_of = %v, want %s", got2.RetryOf, j1.ID)
	}
	if got2.IntentID != j1.ID || got2.Attempt != 2 {
		t.Fatalf("retry intent=%s attempt=%d, want %s/2", got2.IntentID, got2.Attempt, j1.ID)
	}
	waitTerminal(t, h.eng, j2.ID, 5*time.Second)
}

// Story 13 (amendment 1): startup reconciliation flips a crash-orphaned backing_up row to
// connection_lost, and a committing row whose commit rolled forward (a version now carries its
// job_id) to succeeded — proving storage reconciliation ran first.
func TestStoryStartupReconciliation(t *testing.T) {
	h := newHarness(t, fakeParams{}, TransportUSB) // no job started; we craft store state directly
	started := time.Now().UTC()

	// Orphan A: a backing_up row from a crash → connection_lost.
	if err := h.st.InsertJob(store.JobRow{ID: "AAAA", UDID: testUDID, Kind: "backup", Transport: "usb",
		State: StateBackingUp, Phase: PhaseReceiving, Liveness: LivenessActive, StartedAt: started,
		IntentID: "AAAA", Attempt: 1}); err != nil {
		t.Fatal(err)
	}
	// Roll-forward B: a committing row whose commit completed (a version carries job_id=BBBB).
	if err := h.st.InsertJob(store.JobRow{ID: "BBBB", UDID: testUDID, Kind: "backup", Transport: "usb",
		State: StateCommitting, Phase: StateCommitting, Liveness: LivenessActive, StartedAt: started,
		IntentID: "BBBB", Attempt: 1}); err != nil {
		t.Fatal(err)
	}
	wd, err := h.mgr.Seed(testUDID, "BBBB")
	if err != nil {
		t.Fatal(err)
	}
	writeTree(wd, fakeParams{Tree: "complete", Encrypted: true, Kind: "full"})
	if _, err := h.mgr.CommitJob(testUDID, "BBBB"); err != nil {
		t.Fatalf("seed the rolled-forward version: %v", err)
	}

	if err := h.eng.Reconcile(); err != nil {
		t.Fatal(err)
	}

	a, _ := h.eng.Job("AAAA")
	if a.State != StateConnectionLost || a.Error == nil || a.Error.Code != ErrInterrupted {
		t.Fatalf("orphan A = %s error=%v, want connection_lost/%s", a.State, a.Error, ErrInterrupted)
	}
	b, _ := h.eng.Job("BBBB")
	if b.State != StateSucceeded || b.VersionID == nil {
		t.Fatalf("rolled-forward B = %s version=%v, want succeeded with a version", b.State, b.VersionID)
	}
}

// Story 14a (A3): free space below the floor at preflight → actionable disk_low fail, no process.
func TestStoryDiskLowPreflightRefuse(t *testing.T) {
	m := loadMeta(t, "full-usb-success")
	h := newHarness(t, m.params(t), m.Transport, func(o *Options, d *fakeDevices) {
		o.Config.DiskLowFreeBytes = 10 << 30
		o.FreeSpace = func(string) (uint64, error) { return 1 << 30, nil }
	})
	job := h.start(t, m.Transport, "")
	final := waitTerminal(t, h.eng, job.ID, 5*time.Second)
	if final.State != StateFailed || final.Error == nil || final.Error.Code != ErrDiskLow {
		t.Fatalf("state=%s error=%v, want failed/%s", final.State, final.Error, ErrDiskLow)
	}
}

// Story 14b (A3): free space that drops below the floor DURING the backup → a disk_low warning on
// the job.log stream, never a kill (the backup still completes).
func TestStoryDiskLowWarnsDuringBackup(t *testing.T) {
	m := loadMeta(t, "silent-stall")
	var calls int32
	h := newHarness(t, m.params(t), m.Transport, func(o *Options, d *fakeDevices) {
		o.Config.DiskLowFreeBytes = 10 << 30
		o.FreeSpace = func(string) (uint64, error) {
			if atomic.AddInt32(&calls, 1) == 1 {
				return 100 << 30, nil // preflight passes
			}
			return 1 << 30, nil // sampler sees low
		}
	})
	ec, stop := collect(t, h.bus)
	defer stop()
	job := h.start(t, m.Transport, "")
	final := waitTerminal(t, h.eng, job.ID, 6*time.Second)
	if final.State != StateSucceeded {
		t.Fatalf("state=%s — a disk_low warning must not kill the backup", final.State)
	}
	if !ec.logContains("low on space") {
		t.Fatal("no disk_low warning reached the job.log stream")
	}
}

// Story 11: the CLI driver runs one job to success, streams its state changes, and exits 0.
func TestStoryCLIDrivesToSuccess(t *testing.T) {
	m := loadMeta(t, "full-usb-success")
	h := newHarness(t, m.params(t), m.Transport)
	var buf bytes.Buffer
	code := DriveToCompletion(context.Background(), h.eng, h.bus, testUDID, m.Transport, &buf)
	out := buf.String()
	if code != 0 {
		t.Fatalf("exit=%d\n%s", code, out)
	}
	if !strings.Contains(out, "succeeded") {
		t.Fatalf("no success line:\n%s", out)
	}
	if !strings.Contains(out, StateBackingUp) {
		t.Fatalf("state changes were not streamed:\n%s", out)
	}
}

// Story 11: a failing backup exits nonzero from the CLI driver.
func TestStoryCLIFailingBackupExitsNonzero(t *testing.T) {
	m := loadMeta(t, "wifi-torn-session")
	h := newHarness(t, m.params(t), m.Transport)
	var buf bytes.Buffer
	code := DriveToCompletion(context.Background(), h.eng, h.bus, testUDID, m.Transport, &buf)
	if code == 0 {
		t.Fatalf("a torn session must exit nonzero:\n%s", buf.String())
	}
	if !strings.Contains(buf.String(), StateConnectionLost) {
		t.Fatalf("connection_lost not streamed:\n%s", buf.String())
	}
}

// StartBackup input guards: auto → 422; unknown transport → 422.
func TestStartBackupTransportGuards(t *testing.T) {
	h := newHarness(t, fakeParams{}, TransportUSB)
	if _, s, _ := h.eng.StartBackup(testUDID, TransportAuto, ""); s != 422 {
		t.Fatalf("auto transport = %d, want 422", s)
	}
	if _, s, _ := h.eng.StartBackup(testUDID, "carrier-pigeon", ""); s != 422 {
		t.Fatalf("bad transport = %d, want 422", s)
	}
}
