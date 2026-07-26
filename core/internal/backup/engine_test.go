package backup

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
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
		Tool:      ToolConfig{Bin: os.Args[0], ArgPrefix: fakeArgPrefix(), Env: fakeToolEnv(p)},
	}
	for _, m := range mods {
		m(&o, dev)
	}
	h := &harness{eng: New(o), mgr: mgr, dev: dev, bus: b, st: st, dir: dir}

	// Registered AFTER t.TempDir() above, and cleanups run LIFO — so this drains the engine before
	// the TempDir RemoveAll walks the tree. See drain for why that ordering is load-bearing.
	t.Cleanup(func() { h.drain(t) })
	return h
}

// drain cancels every job still live on the engine and waits for each run goroutine to release its
// per-UDID slot. A test that returns with a job still running leaves a supervised idevicebackup2
// writing under t.TempDir() while RemoveAll walks it — "directory not empty" (issue #9).
//
// Terminal state is NOT the quiescence signal, so waiting on the job row is not enough: run() emits
// the terminal row, THEN discards working/, and only frees the slot on its way out (defer release).
// The gap is already known to this file — see startWhenReleased's note about "the brief single-flight
// window between a job's terminal row and the release of its per-UDID slot". e.running going empty
// is the signal that every run goroutine has finished touching the tree.
func (h *harness) drain(t *testing.T) {
	t.Helper()
	for _, id := range h.liveJobIDs() {
		h.eng.CancelJob(id)
	}
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if len(h.liveJobIDs()) == 0 {
			return
		}
		time.Sleep(4 * time.Millisecond)
	}
	// Loudly, not silently: proceeding into RemoveAll with a live writer is the very race this
	// exists to close, and a test that cannot quiesce its engine has not proven what it claims.
	t.Errorf("engine did not quiesce within 30s — jobs still live: %v", h.liveJobIDs())
}

// liveJobIDs snapshots the ids of the engine's currently-running jobs. The two locks are taken in
// sequence, never nested: e.mu only to copy the map's values, then each lj.mu to read its row.
func (h *harness) liveJobIDs() []string {
	h.eng.mu.Lock()
	live := make([]*liveJob, 0, len(h.eng.running))
	for _, lj := range h.eng.running {
		live = append(live, lj)
	}
	h.eng.mu.Unlock()

	ids := make([]string, 0, len(live))
	for _, lj := range live {
		lj.mu.Lock()
		ids = append(ids, lj.row.ID)
		lj.mu.Unlock()
	}
	return ids
}

func (h *harness) start(t *testing.T, transport, retryOf string) wire.Job {
	t.Helper()
	j, status, reason := h.eng.StartBackup(testUDID, transport, retryOf)
	if status != 202 {
		t.Fatalf("StartBackup: status=%d reason=%q", status, reason)
	}
	return j
}

// waitCeiling is the absolute backstop for the waits below: a job that keeps reporting progress
// forever would otherwise never fail them, and dying at `go test`'s 10-minute panic is a worse
// failure than a named one. Reaching it is always a bug, never load — an assertion #37 made
// untested and #57 then put in question when CI reached it. It has since been reproduced under load
// and run down: the ceiling was right, and the bug it caught is #59. So the claim stands as written,
// and whyWaitEnded is what makes the NEXT one readable without a load repro to interpret it.
const waitCeiling = 2 * time.Minute

// gracePhases are the phases where elapsed time is the EXPECTED behaviour rather than evidence of a
// stall, so the no-progress window does not accrue while a job sits in one. Every field progressSig
// reads is legitimately static throughout them, so without this the window degenerates back into the
// wall-clock budget this file exists to remove — which is how the #31 test still failed under load
// with the first version of this fix applied, at phase=waiting_for_passcode.
//
// This mirrors the engine's own rule rather than inventing one. sampler.sample accrues no idle
// "before the FIRST sign of life (a re-exec / process startup can take longer than a short timeout),
// or while paused for the passcode" — the same three graces, for the same reason.
//
// What still bounds a job parked in one of these: waitCeiling, plus the engine's OWN configured
// timeouts (WaitForDeviceTimeout; the passcode pause is deliberate and unbounded by design). What
// the window still guards is `receiving` — the phase that claims to be moving, and so the only one
// where stillness is diagnostic rather than expected.
var gracePhases = map[string]bool{
	StateWaitingForDevice:   true, // mirrored into phase by run()
	PhaseSeeding:            true, // an O(files) clone with no per-file signal
	PhaseStarting:           true, // the re-exec: "before the FIRST sign of life"
	PhaseWaitingForPasscode: true, // the engine freezes its own liveness clock here
}

// progressSig is everything the engine updates as a job advances. Comparing it across polls answers
// "did this job move?" — which is a claim about the ENGINE. Comparing wall-clock against a fixed
// budget answers "was this machine fast enough?", which is a claim about the runner, and that is the
// claim that keeps failing on a loaded one (issue #31).
func progressSig(j wire.Job) string {
	p := j.Progress
	pct := "nil"
	if p.Percent != nil {
		pct = strconv.FormatFloat(*p.Percent, 'f', 2, 64)
	}
	return strings.Join([]string{
		j.State, p.Phase, p.Liveness, pct,
		strconv.FormatInt(p.BytesDone, 10), strconv.FormatInt(p.FilesReceived, 10),
	}, "|")
}

// waitTerminal waits for a job to reach a terminal state. `d` is a NO-PROGRESS window, not a total
// budget: it restarts every time the job visibly advances, so a slow machine buys time while a
// genuinely stuck engine still fails in `d`. That is strictly stronger than the fixed budget it
// replaces — an engine that took 30 s to notice a dead transport used to pass a 60 s budget and now
// fails, because 30 s of silence is 30 s of silence however fast the box is.
//
// On failure it reports what it observed — phase and liveness alongside state — because `state=
// backing_up` alone does not say whether the job was progressing, which is exactly what made the
// #31 CI failure unreadable. whyWaitEnded says which bound fired and why.
func waitTerminal(t *testing.T, e *Engine, id string, d time.Duration) wire.Job {
	t.Helper()
	start := time.Now()
	last, lastMoved, movedBy := "", time.Now(), "nothing yet"
	for {
		j, ok := e.Job(id)
		if ok && isTerminal(j.State) {
			return j
		}
		if ok {
			if sig := progressSig(j); sig != last {
				last, lastMoved, movedBy = sig, time.Now(), "progress"
			} else if gracePhases[j.Progress.Phase] {
				// a phase that is MEANT to wait accrues no idle
				lastMoved, movedBy = time.Now(), "the grace phase "+j.Progress.Phase
			}
		}
		if stalled, elapsed := time.Since(lastMoved), time.Since(start); stalled >= d || elapsed >= waitCeiling {
			t.Fatalf("job %s did not terminate: %s", id, whyWaitEnded(e, id, stalled, d, elapsed, movedBy))
		}
		time.Sleep(4 * time.Millisecond)
	}
}

// whyWaitEnded names WHICH of the two bounds ended a wait, because they are claims about different
// things and only one of them fired. The window firing says the engine stopped moving. The ceiling
// firing says the window never got the chance to accrue — so reporting a stall at all is reporting
// the wrong quantity, and reporting one of `0s` (what a grace phase produces, since it resets the
// window on every poll) is the project's own "an error message is a claim" rule failing in the
// meaningless direction, which is what #57 was filed for.
//
// It also reports whether the engine still OWNS the job, because that single fact separates the two
// ways a non-terminal row outlives its job, and they have nothing to do with each other: a run
// goroutine that is genuinely stuck (still owned), versus a run goroutine that finished and left
// behind a row that disagrees with it (no longer owned). The second is the fingerprint of #59 — a
// terminal row overwritten by a stale progress write — and finding it the first time cost a load
// repro and a SIGQUIT dump, which is exactly the cost this message exists to stop paying.
func whyWaitEnded(e *Engine, id string, stalled, window, elapsed time.Duration, movedBy string) string {
	cause := fmt.Sprintf("no progress for %v", stalled.Round(time.Millisecond))
	if stalled < window {
		cause = fmt.Sprintf("exceeded the %v ceiling; the %v no-progress window never accrued "+
			"because it was last reset by %s", waitCeiling, window, movedBy)
	}
	return fmt.Sprintf("%s (elapsed %v, %s)", cause, elapsed.Round(time.Millisecond), describe(e, id))
}

// describe renders a job's observable state for a failure message.
func describe(e *Engine, id string) string {
	j, ok := e.Job(id)
	if !ok {
		return "job row absent"
	}
	pct := "nil"
	if j.Progress.Percent != nil {
		pct = strconv.FormatFloat(*j.Progress.Percent, 'f', 1, 64)
	}
	return fmt.Sprintf("state=%s phase=%s liveness=%s percent=%s files=%d engine_owns=%s",
		j.State, j.Progress.Phase, j.Progress.Liveness, pct, j.Progress.FilesReceived, yesNo(engineOwns(e, id)))
}

// engineOwns reports whether the engine still has a run goroutine for this job. Reported as plain
// fact, never interpreted here: a terminal row can legitimately still be owned during the
// single-flight window startWhenReleased documents, and a non-terminal row that is NOT owned is the
// one combination that cannot be explained by a slow box. See whyWaitEnded for what it is for.
//
// The two locks are taken in sequence, never nested — the discipline liveJobIDs documents.
func engineOwns(e *Engine, id string) bool {
	e.mu.Lock()
	live := make([]*liveJob, 0, len(e.running))
	for _, lj := range e.running {
		live = append(live, lj)
	}
	e.mu.Unlock()
	for _, lj := range live {
		lj.mu.Lock()
		match := lj.row.ID == id
		lj.mu.Unlock()
		if match {
			return true
		}
	}
	return false
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

// TestWaitFailureNamesWhichBoundFired guards #57 at the point it actually went wrong: when the
// CEILING ends a wait, the message must not report a stall, because none was observed. "no progress
// for 0s" is not false so much as meaningless, and a message that names no mechanism is what turned
// one CI failure into a load repro and a SIGQUIT dump before anyone could say what had happened.
//
// engineOwns is asserted both ways here. The `no` case is the one with diagnostic value — a
// non-terminal row the engine no longer owns is #59's fingerprint — and the load repro exercises it;
// the `yes` case is asserted because a lookup that silently never matched would report `no` for
// everything and still read as a perfectly clean failure message.
func TestWaitFailureNamesWhichBoundFired(t *testing.T) {
	m := loadMeta(t, "disk-full-105")
	h := newHarness(t, m.params(t), m.Transport)
	const absent = "01NOSUCHJOB0000000000000000"

	// The ceiling fired: the window had not elapsed, so nothing stalled.
	msg := whyWaitEnded(h.eng, absent, 0, 10*time.Second, waitCeiling, "the grace phase "+PhaseWaitingForPasscode)
	if strings.Contains(msg, "no progress for") {
		t.Fatalf("ceiling message reports a stall it never observed: %q", msg)
	}
	for _, want := range []string{"ceiling", "never accrued", PhaseWaitingForPasscode} {
		if !strings.Contains(msg, want) {
			t.Fatalf("ceiling message = %q; want it to name %q", msg, want)
		}
	}

	// The window fired: the stall IS the finding, and the message says so and blames nothing else.
	msg = whyWaitEnded(h.eng, absent, 10*time.Second, 10*time.Second, 12*time.Second, "progress")
	if !strings.Contains(msg, "no progress for 10s") {
		t.Fatalf("window message = %q; want the stall it observed", msg)
	}
	if strings.Contains(msg, "ceiling") {
		t.Fatalf("window message blames the ceiling instead: %q", msg)
	}

	if engineOwns(h.eng, absent) {
		t.Fatal("engineOwns said yes for a job the engine never ran")
	}
	// Registered directly rather than by running a job: ownership would otherwise be a race against
	// the job finishing, and a flaky test is the last thing this file needs. Removed before the
	// harness drain, which would cancel a liveJob that has no cancel func.
	h.eng.mu.Lock()
	h.eng.running["owned-udid"] = &liveJob{row: store.JobRow{ID: "01OWNEDJOB00000000000000000"}}
	h.eng.mu.Unlock()
	defer func() {
		h.eng.mu.Lock()
		delete(h.eng.running, "owned-udid")
		h.eng.mu.Unlock()
	}()
	if !engineOwns(h.eng, "01OWNEDJOB00000000000000000") {
		t.Fatal("engineOwns said no for a job in e.running")
	}
}

// startWhenReleased starts a retry, tolerating the brief single-flight window between a job's
// terminal row and the release of its per-UDID slot (see the note at its call site).
func startWhenReleased(t *testing.T, e *Engine, transport, retryOf string) (wire.Job, int, string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		job, status, reason := e.StartBackup(testUDID, transport, retryOf)
		if status != 409 || time.Now().After(deadline) {
			return job, status, reason
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// waitState waits for a job to reach one state. `d` is a no-progress window, as in waitTerminal.
// A job that runs to a terminal state without ever passing through the wanted one fails IMMEDIATELY
// rather than waiting the window out: it can no longer get there, and the missed state is the
// finding — worth naming at once instead of dressing it as a timeout.
func waitState(t *testing.T, e *Engine, id, state string, d time.Duration) {
	t.Helper()
	start := time.Now()
	last, lastMoved, movedBy := "", time.Now(), "nothing yet"
	for {
		j, ok := e.Job(id)
		if ok && j.State == state {
			return
		}
		if ok && isTerminal(j.State) {
			t.Fatalf("job %s terminated as %s without ever reaching %s (elapsed %v, %s)",
				id, j.State, state, time.Since(start).Round(time.Millisecond), describe(e, id))
		}
		if ok {
			if sig := progressSig(j); sig != last {
				last, lastMoved, movedBy = sig, time.Now(), "progress"
			} else if gracePhases[j.Progress.Phase] {
				// a phase that is MEANT to wait accrues no idle
				lastMoved, movedBy = time.Now(), "the grace phase "+j.Progress.Phase
			}
		}
		if stalled, elapsed := time.Since(lastMoved), time.Since(start); stalled >= d || elapsed >= waitCeiling {
			t.Fatalf("job %s never reached %s: %s", id, state, whyWaitEnded(e, id, stalled, d, elapsed, movedBy))
		}
		time.Sleep(3 * time.Millisecond)
	}
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

// qn.6a ((cu)/(cv)): the backup passes through the `seeding` phase — quince cloning latest/ → working/
// before idevicebackup2 starts — between preflight and backing_up, so the UI narrates the clone wait
// instead of dead air before the on-device passcode prompt.
func TestSeedingPhaseEmitted(t *testing.T) {
	m := loadMeta(t, "full-usb-success")
	h := newHarness(t, m.params(t), m.Transport)
	ec, stop := collect(t, h.bus)
	defer stop()
	job := h.start(t, m.Transport, "")
	final := waitTerminal(t, h.eng, job.ID, 5*time.Second)
	if final.State != StateSucceeded {
		t.Fatalf("state=%s error=%v", final.State, final.Error)
	}
	if !ec.sawPhase(PhaseSeeding) {
		t.Fatal("no job.updated carried the seeding phase (between preflight and backing_up)")
	}
}

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
	// The second device's job is deliberately left running — that IS the assertion, that it was
	// never blocked by the first. The harness drain owns its lifetime from here (issue #9): before
	// it, this test returned with that job still writing into t.TempDir().
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

// fakeAnnouncer is the DeviceAnnouncer seam: it records which devices were re-published.
type fakeAnnouncer struct {
	mu    sync.Mutex
	udids []string
}

func (f *fakeAnnouncer) AnnounceBackup(udid string) {
	f.mu.Lock()
	f.udids = append(f.udids, udid)
	f.mu.Unlock()
}

func (f *fakeAnnouncer) seen() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.udids...)
}

// qn.4c story 9 / findings (iv)+(v): a successful commit re-publishes the device, so the card
// lands on its real "Last backup …" line without a page refresh. Announcing is deliberately tied
// to COMMIT SUCCESS — the moment the device's committed-version history actually changed.
func TestSucceededCommitAnnouncesTheDevice(t *testing.T) {
	m := loadMeta(t, "full-usb-success")
	ann := &fakeAnnouncer{}
	h := newHarness(t, m.params(t), m.Transport, func(o *Options, _ *fakeDevices) { o.Announcer = ann })

	job := h.start(t, m.Transport, "")
	final := waitTerminal(t, h.eng, job.ID, 20*time.Second)
	if final.State != StateSucceeded {
		t.Fatalf("state=%s error=%v; want succeeded", final.State, final.Error)
	}
	if got := ann.seen(); len(got) != 1 || got[0] != testUDID {
		t.Fatalf("announced %v; want exactly one announce for %s", got, testUDID)
	}
}

// The mirror image: a job that fails announces nothing — there is no new committed version, so
// the device's last_backup is unchanged and a device.updated would be noise (state honesty).
func TestFailedJobAnnouncesNothing(t *testing.T) {
	m := loadMeta(t, "full-usb-success")
	ann := &fakeAnnouncer{}
	h := newHarness(t, m.params(t), m.Transport, func(o *Options, d *fakeDevices) {
		o.Announcer = ann
		d.set(testUDID, m.Transport, "off") // preflight refuses (require_encryption)
	})

	job := h.start(t, m.Transport, "")
	if final := waitTerminal(t, h.eng, job.ID, 5*time.Second); final.State != StateFailed {
		t.Fatalf("state=%s; want failed", final.State)
	}
	if got := ann.seen(); len(got) != 0 {
		t.Fatalf("announced %v after a failed job; want nothing", got)
	}
}

// fakeProber is the EncryptionProber seam: it answers with a fixed live reading and counts calls.
type fakeProber struct {
	state string
	ok    bool
	calls int
}

func (f *fakeProber) RefreshEncryption(context.Context, string, string) (string, bool) {
	f.calls++
	return f.state, f.ok
}

// qn.4c story 8 / finding (i)-B: a device whose CACHED encryption state is `unknown` (enrichment
// ran while lockdown was cold) used to be hard-refused at preflight even though it does encrypt.
// Preflight now re-reads live and proceeds on the fresh "on".
func TestPreflightReprobesUnknownEncryption(t *testing.T) {
	m := loadMeta(t, "full-usb-success")
	prober := &fakeProber{state: "on", ok: true}
	h := newHarness(t, m.params(t), m.Transport, func(o *Options, d *fakeDevices) {
		d.set(testUDID, m.Transport, "unknown") // the cold-lockdown reading
		o.Prober = prober
	})
	job := h.start(t, m.Transport, "")
	final := waitTerminal(t, h.eng, job.ID, 20*time.Second)
	if final.State != StateSucceeded {
		t.Fatalf("state=%s error=%v; want succeeded — a live re-read said the device encrypts", final.State, final.Error)
	}
	if prober.calls != 1 {
		t.Fatalf("prober called %d times; want exactly 1 (one probe per preflight)", prober.calls)
	}
}

// The other half of (i)-B: when the live re-read says the device really has encryption OFF, the
// refusal stands — and its message is the actionable one, not the "couldn't confirm" one.
func TestPreflightRefusesWhenLiveReadSaysOff(t *testing.T) {
	m := loadMeta(t, "full-usb-success")
	h := newHarness(t, m.params(t), m.Transport, func(o *Options, d *fakeDevices) {
		d.set(testUDID, m.Transport, "unknown")
		o.Prober = &fakeProber{state: "off", ok: true}
	})
	job := h.start(t, m.Transport, "")
	final := waitTerminal(t, h.eng, job.ID, 5*time.Second)
	if final.State != StateFailed || final.Error == nil || final.Error.Code != ErrEncryptionRequired {
		t.Fatalf("state=%s error=%v, want failed/%s", final.State, final.Error, ErrEncryptionRequired)
	}
	if !strings.Contains(final.Error.Message, "turned off") {
		t.Fatalf("message = %q; want the actionable enable-encryption wording", final.Error.Message)
	}
}

// And when even the live read cannot tell (locked device, probe failed), the job still refuses —
// but says the TRUE reason instead of implying the user disabled encryption (state honesty).
func TestPreflightRefusesHonestlyWhenEncryptionUnconfirmable(t *testing.T) {
	m := loadMeta(t, "full-usb-success")
	h := newHarness(t, m.params(t), m.Transport, func(o *Options, d *fakeDevices) {
		d.set(testUDID, m.Transport, "unknown")
		o.Prober = &fakeProber{ok: false} // the probe itself failed
	})
	job := h.start(t, m.Transport, "")
	final := waitTerminal(t, h.eng, job.ID, 5*time.Second)
	if final.State != StateFailed || final.Error == nil || final.Error.Code != ErrEncryptionRequired {
		t.Fatalf("state=%s error=%v, want failed/%s", final.State, final.Error, ErrEncryptionRequired)
	}
	if !strings.Contains(final.Error.Message, "could not be confirmed") {
		t.Fatalf("message = %q; want it to say the state could not be confirmed", final.Error.Message)
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
	// A job's ROW goes terminal before its work is discarded and the per-UDID single-flight slot
	// is released (release is deferred past discard — correct: a new job must not race the old
	// one's work dir). So an instant retry can legitimately see 409 for a moment; wait it out
	// rather than asserting on teardown timing. NOTE (qn.4c, filed): the 409 message a user would
	// see in that window says "a backup is already running for this device", which reads as wrong
	// right after the UI announced a failure — worth a friendlier reason for the one-tap Retry.
	j2, s2, reason := startWhenReleased(t, h.eng, m.Transport, j1.ID)
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
	// Seed returns the idevicebackup2 TARGET (working/ parent); the tree lands at <target>/<udid>.
	writeTree(filepath.Join(wd, testUDID), fakeParams{Tree: "complete", Encrypted: true, Kind: "full"}, false)
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

// StartBackup input guards: an unknown or empty transport → 422 (no job minted). transport-auto
// RESOLUTION is covered by engine_transport_test.go (TestAutoResolves* / TestAutoWhenAbsent*).
func TestStartBackupTransportGuards(t *testing.T) {
	h := newHarness(t, fakeParams{}, TransportUSB)
	if _, s, _ := h.eng.StartBackup(testUDID, "carrier-pigeon", ""); s != 422 {
		t.Fatalf("bad transport = %d, want 422", s)
	}
	if _, s, _ := h.eng.StartBackup(testUDID, "", ""); s != 422 {
		t.Fatalf("empty transport = %d, want 422", s)
	}
}

// TestResetWorking covers the qn.5b Reset action on the engine (contracts §1 reset-working):
// 404 unknown device, 202 discards a dirty working/, 409 while a backup is running.
func TestResetWorking(t *testing.T) {
	t.Run("unknown device 404", func(t *testing.T) {
		h := newHarness(t, fakeParams{}, TransportUSB)
		if s, _ := h.eng.ResetWorking("SYNTHETIC-UDID-UNKNOWN-0009"); s != http.StatusNotFound {
			t.Fatalf("reset unknown device = %d, want 404", s)
		}
	})
	t.Run("dirty working 202 discards", func(t *testing.T) {
		h := newHarness(t, fakeParams{}, TransportUSB)
		wd, err := h.mgr.Seed(testUDID, "jobX")
		if err != nil {
			t.Fatal(err)
		}
		writeTree(filepath.Join(wd, testUDID), fakeParams{Tree: "complete", Encrypted: true, Kind: "full"}, false)
		if s, _ := h.eng.ResetWorking(testUDID); s != http.StatusAccepted {
			t.Fatalf("reset dirty working = %d, want 202", s)
		}
		if _, err := os.Stat(wd); !os.IsNotExist(err) {
			t.Fatal("working/ should be discarded after reset")
		}
	})
	t.Run("backup running 409", func(t *testing.T) {
		m := loadMeta(t, "silent-stall") // stays in backing_up long enough to observe
		h := newHarness(t, m.params(t), m.Transport)
		j := h.start(t, m.Transport, "")
		waitState(t, h.eng, j.ID, StateBackingUp, 2*time.Second)
		if s, _ := h.eng.ResetWorking(testUDID); s != http.StatusConflict {
			t.Fatalf("reset while a backup runs = %d, want 409", s)
		}
		h.eng.CancelJob(j.ID)
		waitTerminal(t, h.eng, j.ID, 3*time.Second)
	})
}

// --- qn.5b: the free-space bug (28b97de) is structurally impossible (no symlink stub) ------------

// TestBackupTargetIsOnStorageFilesystem is the qn.5b structural replacement for the deleted
// symlink-stub regression guard. mobilebackup2 asks the host how much free space it has, and
// idevicebackup2 answers with a statfs of the target directory it was handed. qn.5b points the tool
// STRAIGHT at the storage backend's working/ parent (Seed's return) — always on the storage
// filesystem by construction, with NO symlink stub — so the statfs is truthful and the device can
// never be told the wrong filesystem's free space (the 28b97de failure). The tree lands at
// <target>/<udid> by idevicebackup2's own convention.
func TestBackupTargetIsOnStorageFilesystem(t *testing.T) {
	udid := "SYNTHETIC-UDID-AAAA-0001"
	// Seed returns exactly this shape: <backups>/<udid>/working.
	target := filepath.Join(t.TempDir(), "backups", udid, "working")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	tl := &tool{bin: "idevicebackup2"}

	// The tool is pointed straight at the target — no stub, no symlink derivation.
	cmd := tl.command(context.Background(), TransportUSB, udid, target, "")
	if got := cmd.Args[len(cmd.Args)-1]; got != target {
		t.Fatalf("idevicebackup2 target = %q, want the storage working/ parent %q (no stub)", got, target)
	}

	// The tree lands at <target>/<udid>, under the device's storage area → the statfs is truthful.
	tree := filepath.Join(target, udid)
	deviceArea := filepath.Dir(target)
	if !strings.HasPrefix(tree, deviceArea+string(filepath.Separator)) {
		t.Fatalf("tree %q is not under the device's storage area %q — its statfs would report the wrong fs", tree, deviceArea)
	}
}

// TestFailedBackupReportsTheDeviceReason replays the lab transcript of that refusal and asserts
// the job carries the device's OWN words. Before this, a user saw "exit status 151" — true,
// useless, and (as the Operator found) indistinguishable from any other failure.
func TestFailedBackupReportsTheDeviceReason(t *testing.T) {
	m := loadMeta(t, "disk-full-105")
	h := newHarness(t, m.params(t), m.Transport)

	job := h.start(t, m.Transport, "")
	final := waitTerminal(t, h.eng, job.ID, 10*time.Second)
	if final.State != StateFailed || final.Error == nil {
		t.Fatalf("state=%s error=%v; want failed with an error", final.State, final.Error)
	}
	if final.Error.Code != ErrBackupFailed {
		t.Fatalf("code = %q, want %q", final.Error.Code, ErrBackupFailed)
	}
	if !strings.Contains(final.Error.Message, "Insufficient free disk space") {
		t.Fatalf("message = %q; want the device's own reason, not an exit code", final.Error.Message)
	}
	if strings.Contains(final.Error.Message, "exit status") {
		t.Fatalf("message = %q; the bare exit status is what made this failure unreadable", final.Error.Message)
	}
	if len(h.mgr.Versions(testUDID)) != 0 {
		t.Fatal("a refused backup must leave no version")
	}
}
