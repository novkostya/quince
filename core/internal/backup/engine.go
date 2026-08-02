package backup

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sync"
	"time"

	"github.com/novkostya/quince/core/internal/bus"
	"github.com/novkostya/quince/core/internal/store"
	"github.com/novkostya/quince/core/internal/wire"
)

// udidPattern guards a UDID before it reaches a path or an argv (design §6; same allowlist shape
// as deviceops/storage — no separators or shell metacharacters).
var udidPattern = regexp.MustCompile(`^[A-Za-z0-9-]{8,64}$`)

func validUDID(u string) bool { return udidPattern.MatchString(u) }

// StorageForJob is the extra storage surface the engine needs beyond the running-flow Storage seam:
// did a commit for this job roll forward (a version now carries its job_id)? — used by
// reconciliation; and RepairWorkingCopy — the Reset action (discard the dirty working/). Kept
// separate so the primary Storage interface stays about the running flow. *storage.Manager
// satisfies both.
type StorageForJob interface {
	VersionForJob(udid, jobID string) (versionID string, ok bool)

	// ResolveChoice maps a requested storage_id to the concrete storage a job will use, or an HTTP
	// status and reason: "" → default, unknown → 404, unreachable → 409 (contracts §1).
	ResolveChoice(storageID string) (concrete string, status int, reason string)

	// BindJobStorage records which storage this job writes to, for the life of the job; UnbindJob
	// drops it. Every write-path call then resolves that storage from the jobID it already carries.
	BindJobStorage(jobID, storageID string) error
	UnbindJob(jobID string)
	// RepairWorking discards a device's dirty working/ on ONE storage, resolving WHICH from an
	// optional storage id — omitted resolves by dirty count (contracts §1, quince#448).
	RepairWorking(udid, storageID string) (status int, reason string)
}

// Options wires the engine.
type Options struct {
	BaseCtx   context.Context
	Store     JobStore
	Storage   Storage
	VersionQ  StorageForJob
	Devices   Devices
	Prober    EncryptionProber // optional: live encryption re-read at preflight (finding (i)-B)
	Announcer DeviceAnnouncer  // optional: republish the device after a commit (finding (v))
	Bus       *bus.Bus
	Log       *slog.Logger
	Config    Config
	Tool      ToolConfig
	Backups   string // the default storage's root (qn.6c), for the A3 preflight free-space check
	NewID     func() string
	Now       func() time.Time
	FreeSpace func(string) (uint64, error) // nil → statfsFree
}

// ToolConfig configures the idevicebackup2 supervisor. In production only Bin + the muxer
// addresses are set; ArgPrefix + Env are the test-only fake-CLI harness (Bin = the test binary).
// There is deliberately NO target-root knob: the target IS the storage backend's working/ parent
// (Seed's return), always on the storage filesystem, so idevicebackup2's free-space statfs is
// truthful by construction (qn.5b dropped the old symlink stub; qn.4c lab finding, bug 28b97de).
type ToolConfig struct {
	Bin           string   // "" → "idevicebackup2"
	ArgPrefix     []string // test-only leading args (-test.run=… "--")
	Env           []string // test-only extra child env (the fake harness knobs)
	UsbmuxdSocket string
	NetmuxdAddr   string
}

// Engine runs backups. One goroutine per job; a global per-UDID single-flight (never two jobs for
// one device — design §4). It implements httpapi.JobReader + httpapi.JobControl.
type Engine struct {
	baseCtx   context.Context
	st        JobStore
	storage   Storage
	versionQ  StorageForJob
	devices   Devices
	prober    EncryptionProber
	announcer DeviceAnnouncer
	bus       *bus.Bus
	log       *slog.Logger
	cfg       Config
	tool      *tool
	backups   string
	newID     func() string
	now       func() time.Time
	freeSpace func(string) (uint64, error)

	mu      sync.Mutex
	running map[string]*liveJob // by UDID
	logs    *logStore
}

type liveJob struct {
	mu         sync.Mutex
	row        store.JobRow
	cancel     context.CancelFunc
	killReason string // "cancel" | "timeout" | "shutdown"
	lastEmit   time.Time
}

// New constructs the engine (does not start reconciliation — call Reconcile before serving).
func New(o Options) *Engine {
	if o.BaseCtx == nil {
		o.BaseCtx = context.Background()
	}
	if o.NewID == nil {
		o.NewID = func() string { return "" }
	}
	if o.Now == nil {
		o.Now = func() time.Time { return time.Now().UTC() }
	}
	if o.FreeSpace == nil {
		o.FreeSpace = statfsFree
	}
	if o.Tool.Bin == "" {
		o.Tool.Bin = "idevicebackup2"
	}
	return &Engine{
		baseCtx: o.BaseCtx, st: o.Store, storage: o.Storage, versionQ: o.VersionQ,
		devices: o.Devices, prober: o.Prober, announcer: o.Announcer,
		bus: o.Bus, log: o.Log, cfg: o.Config, backups: o.Backups,
		newID: o.NewID, now: o.Now, freeSpace: o.FreeSpace,
		tool: &tool{bin: o.Tool.Bin, argPrefix: o.Tool.ArgPrefix, env: o.Tool.Env,
			usbmuxd: o.Tool.UsbmuxdSocket, netmuxd: o.Tool.NetmuxdAddr},
		running: map[string]*liveJob{}, logs: newLogStore(),
	}
}

// --- httpapi.JobControl ---

// StartBackup creates and launches a backup job (contracts §1 POST /api/jobs). Returns the Job +
// an HTTP status: 202 accepted, 409 already-running, 422 bad transport OR auto-when-absent, 404
// unknown device or retry_of, 500 store error. transport "auto" resolves against current presence
// (design §4, decisions (bp)): prefer USB when present, else Wi-Fi; a device on NEITHER transport is
// refused actionably. The Job stores the resolved CONCRETE transport (never "auto") — a guess would
// persist a dishonest Job.transport (state honesty).
func (e *Engine) StartBackup(udid, transport, storageID, retryOf string) (wire.Job, int, string) {
	if !validUDID(udid) {
		return wire.Job{}, http.StatusNotFound, "unknown device"
	}
	transport, status, reason := e.resolveTransport(udid, transport)
	if status != 0 {
		return wire.Job{}, status, reason
	}

	// THE STORAGE IS RESOLVED TO A CONCRETE ID BEFORE THE JOB ROW EXISTS, exactly as the transport
	// is: "" → the default, unknown → 404, unreachable → 409. `Job.storage_id` then records where
	// the backup was AIMED, never the word "default" — a Job storing the request rather than the
	// resolution would be dishonest the same way a `Job.transport` of "auto" would be.
	//
	// Resolved BEFORE the busy check on purpose: an unknown storage is a bad request whatever the
	// device happens to be doing, and answering 409 "already running" to it would hide a permanent
	// problem behind a transient one.
	concreteStorage, status, reason := e.versionQ.ResolveChoice(storageID)
	if status != 0 {
		return wire.Job{}, status, reason
	}

	e.mu.Lock()
	if _, busy := e.running[udid]; busy {
		e.mu.Unlock()
		return wire.Job{}, http.StatusConflict, "a backup is already running for this device"
	}
	id := e.newID()
	row := store.JobRow{
		ID: id, UDID: udid, Kind: "backup", Transport: transport, State: StateQueued,
		Phase: StateQueued, Liveness: LivenessActive, StartedAt: e.now(), IntentID: id, Attempt: 1,
		StorageID: &concreteStorage,
	}
	if retryOf != "" {
		prev, ok, err := e.st.GetJob(retryOf)
		if err != nil {
			e.mu.Unlock()
			return wire.Job{}, http.StatusInternalServerError, err.Error()
		}
		if !ok {
			e.mu.Unlock()
			return wire.Job{}, http.StatusNotFound, "retry_of names an unknown job"
		}
		row.RetryOf = &retryOf
		row.IntentID = prev.IntentID
		row.Attempt = prev.Attempt + 1
	}
	// BIND BEFORE THE ROW EXISTS ANYWHERE OBSERVABLE. Every write-path call resolves the job's
	// storage from its jobID, so a job that is running but unbound would silently write to the
	// default — the exact substitution ResolveChoice just refused to make.
	if err := e.versionQ.BindJobStorage(id, concreteStorage); err != nil {
		e.mu.Unlock()
		return wire.Job{}, http.StatusConflict, err.Error()
	}
	if err := e.st.InsertJob(row); err != nil {
		e.mu.Unlock()
		return wire.Job{}, http.StatusInternalServerError, err.Error()
	}
	ctx, cancel := context.WithCancel(e.baseCtx)
	lj := &liveJob{row: row, cancel: cancel}
	e.running[udid] = lj
	e.mu.Unlock()

	e.emit(row)
	go e.run(ctx, lj)
	return jobToWire(row), http.StatusAccepted, ""
}

// CancelJob cancels a running job (contracts §1 POST /api/jobs/{id}/cancel → 202). 409 if the job
// is not running (already terminal), 404 if unknown.
func (e *Engine) CancelJob(id string) (wire.Job, int, string) {
	e.mu.Lock()
	var target *liveJob
	for _, lj := range e.running {
		if lj.row.ID == id {
			target = lj
			break
		}
	}
	e.mu.Unlock()
	if target == nil {
		if row, ok, _ := e.st.GetJob(id); ok {
			return jobToWire(row), http.StatusConflict, "job is not running"
		}
		return wire.Job{}, http.StatusNotFound, "no such job"
	}
	target.mu.Lock()
	if target.killReason == "" {
		target.killReason = "cancel"
	}
	row := target.row
	cancel := target.cancel
	target.mu.Unlock()
	cancel()
	return jobToWire(row), http.StatusAccepted, ""
}

// ResetWorking discards a device's dirty working/ so the next backup starts clean from latest/ (the
// qn.5b Reset action, contracts §1 POST /api/devices/{udid}/reset-working). It refuses 409 while a
// backup is running for the device — resetting mid-backup would yank the tree from under
// idevicebackup2 — and 404s an unknown device. Idempotent: a device with no working/ is already
// clean (→ 202). It NEVER touches a committed version (Reset only discards the mutable working/).
func (e *Engine) ResetWorking(udid, storageID string) (int, string) {
	if !validUDID(udid) {
		return http.StatusNotFound, "unknown device"
	}
	if _, ok := e.devices.Device(udid); !ok {
		return http.StatusNotFound, "unknown device"
	}
	e.mu.Lock()
	_, busy := e.running[udid]
	e.mu.Unlock()
	if busy {
		return http.StatusConflict, "a backup is running for this device — cancel it before resetting"
	}
	if e.versionQ == nil {
		return http.StatusServiceUnavailable, "the storage subsystem is unavailable"
	}
	// The storage subsystem owns WHICH storage, because it holds the slots. The engine keeps
	// the checks that are its own — unknown device, backup running — in their existing order
	// and wording (quince#448 ruling).
	return e.versionQ.RepairWorking(udid, storageID)
}

// --- httpapi.JobReader ---

func (e *Engine) Jobs(udid, cursor string, limit int) ([]wire.Job, string) {
	rows, next, err := e.st.ListJobs(udid, cursor, limit)
	if err != nil {
		e.log.Error("backup: list jobs failed", "error", err)
		return []wire.Job{}, ""
	}
	out := make([]wire.Job, 0, len(rows))
	for _, r := range rows {
		out = append(out, jobToWire(r))
	}
	return out, next
}

func (e *Engine) Job(id string) (wire.Job, bool) {
	row, ok, err := e.st.GetJob(id)
	if err != nil || !ok {
		return wire.Job{}, false
	}
	return jobToWire(row), true
}

func (e *Engine) JobLog(id string) (string, bool) {
	if l, ok := e.logs.get(id); ok {
		return l, true
	}
	if _, ok, _ := e.st.GetJob(id); ok {
		return "", true // known job, no log tail retained
	}
	return "", false
}

// --- startup reconciliation (amendment 1; design §2) ---

// Reconcile flips crash-orphaned non-terminal job rows to connection_lost (discarding their work),
// EXCEPT a job whose commit rolled forward in storage reconciliation, which becomes succeeded. Run
// AFTER storage reconciliation and BEFORE serving (the two reconcilers compose here).
func (e *Engine) Reconcile() error {
	rows, err := e.st.ListNonTerminalJobs()
	if err != nil {
		return err
	}
	fin := e.now()
	for _, r := range rows {
		if e.versionQ != nil {
			if vid, ok := e.versionQ.VersionForJob(r.UDID, r.ID); ok {
				r.State, r.Phase, r.Percent = StateSucceeded, PhaseDone, f64(100)
				// The third terminal writer, and the one most likely to be missed: these rows are
				// crash-orphaned, so whatever liveness they carry was written by a process that no
				// longer exists (quince#313).
				r.Liveness = LivenessNone
				r.FinishedAt, r.VersionID = &fin, strptr(vid)
				r.ErrorCode, r.ErrorMessage = "", ""
				if err := e.st.UpdateJob(r); err != nil {
					e.log.Error("backup: reconcile roll-forward persist failed", "job", r.ID, "error", err)
					continue
				}
				e.emit(r)
				e.log.Info("backup: reconciled job → succeeded (commit rolled forward)", "job", r.ID, "version", vid)
				continue
			}
		}
		r.State, r.FinishedAt = StateConnectionLost, &fin
		// The FOURTH terminal writer. Same reason as the roll-forward above and more pointed here:
		// this row was orphaned by a crash, so its phase and liveness were written by a process that
		// no longer exists — the clearest possible case of a live claim outliving the thing it
		// described (quince#313).
		r.Phase, r.Liveness = "", LivenessNone
		r.ErrorCode, r.ErrorMessage = ErrInterrupted, "interrupted by a restart"
		if err := e.st.UpdateJob(r); err != nil {
			e.log.Error("backup: reconcile persist failed", "job", r.ID, "error", err)
			continue
		}
		if _, derr := e.storage.Discard(r.UDID, r.ID); derr != nil {
			e.log.Warn("backup: reconcile discard failed", "job", r.ID, "error", derr)
		}
		e.emit(r)
		e.log.Info("backup: reconciled orphaned job → connection_lost", "job", r.ID, "udid", r.UDID)
	}
	return nil
}

// --- the state machine ---

func (e *Engine) run(ctx context.Context, lj *liveJob) {
	defer e.release(lj)
	udid := lj.row.UDID

	e.transition(lj, func(r *store.JobRow) { r.State = StateWaitingForDevice; r.Phase = StateWaitingForDevice })
	if !e.awaitDevice(ctx, lj) {
		switch {
		case e.killReasonOf(lj) == "cancel":
			e.terminate(lj, StateCancelled, ErrCancelled, "cancelled")
		case ctx.Err() != nil:
			e.terminate(lj, StateConnectionLost, ErrInterrupted, "interrupted by shutdown")
		default:
			e.terminate(lj, StateFailed, ErrDeviceNotVisible,
				"device did not appear on "+lj.row.Transport+" within the wait window")
		}
		return // nothing seeded yet — no work to discard
	}

	e.transition(lj, func(r *store.JobRow) { r.State = StatePreflight; r.Phase = StatePreflight })
	if code, reason := e.preflight(ctx, lj); code != "" {
		e.terminate(lj, StateFailed, code, reason)
		return // preflight refuses BEFORE Seed → nothing to discard
	}

	// seeding: prepare the work area. A COLD seed clones latest/ → working/<udid> (O(files); ~23 s
	// on a 34 GB device); qn.6b overlaps that clone with the tool startup via candidate C's --gate so
	// the on-device passcode prompt appears in ~1–2 s instead of after the seed (qn.6a (cu)/(cz)). A
	// resume or first-backup has nothing to clone (seedPending=false) → start the tool straight away.
	// The `seeding` state still narrates "Preparing — cloning from your last backup…" during the clone.
	e.transition(lj, func(r *store.JobRow) { r.State = StateSeeding; r.Phase = PhaseSeeding; r.Liveness = LivenessActive })
	target, seedPending, err := e.storage.PrepareWork(udid, lj.row.ID)
	if err != nil {
		// A seed failure has committed nothing; any partial working/ is discarded-and-re-seeded on
		// the next attempt by the qn.5b Finding B sentinel guard, so there is nothing to discard here.
		e.terminate(lj, StateFailed, ErrBackupFailed, "seed work area: "+err.Error())
		return
	}

	var res superviseResult
	if seedPending {
		res = e.superviseGatedSeed(ctx, lj, target)
	} else {
		e.transition(lj, func(r *store.JobRow) { r.State = StateBackingUp; r.Phase = PhaseStarting; r.Liveness = LivenessActive })
		res = e.supervise(ctx, lj, target)
	}
	switch res.kind {
	case outcomeSeedErr:
		// The gated clone failed after the tool launched. Same terminal shape as the pre-launch seed
		// error above; the Finding B sentinel guard owns any partial working/, so no discard.
		e.terminate(lj, StateFailed, ErrBackupFailed, "seed work area: "+res.detail)
	case outcomeCancel:
		e.terminate(lj, StateCancelled, ErrCancelled, "cancelled")
		e.discard(lj)
	case outcomeTimeout:
		e.terminate(lj, StateConnectionLost, ErrDeviceDisconnected,
			"no activity for "+e.cfg.LivenessTimeout.String()+" — connection lost")
		e.discard(lj)
	case outcomeShutdown:
		e.terminate(lj, StateConnectionLost, ErrInterrupted, "interrupted by shutdown")
		e.discard(lj)
	case outcomeProcErr:
		e.terminate(lj, StateFailed, ErrBackupFailed, "backup failed: "+res.detail)
		e.discard(lj)
	case outcomeProcOK:
		if !res.backupSuccessful {
			e.terminate(lj, StateFailed, ErrBackupFailed, "process exited 0 without 'Backup Successful'")
			e.discard(lj)
			return
		}
		e.transition(lj, func(r *store.JobRow) { r.State = StateVerifying; r.Phase = StateVerifying })
		ok, detail, _, _ := e.storage.VerifyWork(udid, lj.row.ID)
		if !ok {
			e.terminate(lj, StateFailed, ErrVerifyFailed, "structural verification failed: "+detail)
			e.discard(lj)
			return
		}
		e.transition(lj, func(r *store.JobRow) { r.State = StateCommitting; r.Phase = StateCommitting })
		v, err := e.storage.CommitJob(udid, lj.row.ID)
		if err != nil {
			// design §4: a commit failure preserves the working state for inspection (no discard).
			e.terminate(lj, StateFailed, ErrCommitFailed, "commit: "+err.Error())
			return
		}
		e.succeed(lj, v.ID)
	}
}

// preflight checks presence, pairing, the encryption policy, and disk headroom — all BEFORE the
// seeding step, so a refusal leaves nothing to discard. code=="" on success; the Seed itself runs
// in the separate `seeding` state (qn.6a) so the clone latency is narrated, not dead air.
func (e *Engine) preflight(ctx context.Context, lj *liveJob) (code, reason string) {
	udid, transport := lj.row.UDID, lj.row.Transport
	dev, ok := e.devices.Device(udid)
	if !ok || !presentOn(dev, transport) {
		return ErrDeviceNotVisible, "device is not present on " + transport
	}
	if dev.Paired == "no" {
		return ErrNotPaired, "device is not paired — pair it first"
	}
	if e.cfg.RequireEncryption {
		if code, reason := e.checkEncryption(ctx, udid, transport, dev.BackupEncryption); code != "" {
			return code, reason
		}
	}
	if e.freeSpace != nil && e.backups != "" {
		if free, err := e.freeSpace(e.backups); err == nil && free < e.cfg.DiskLowFreeBytes {
			return ErrDiskLow, fmt.Sprintf(
				"target filesystem is low on space (%d MiB free) — free space before backing up", free>>20)
		}
	}
	return "", ""
}

// checkEncryption enforces backup.require_encryption WITHOUT trusting a stale reading. The
// registry's value can be `unknown` merely because enrichment ran while lockdown was cold, which
// used to hard-fail a device that does encrypt, with no retry ((bw), finding (i)-B). So anything
// other than a cached "on" is re-read live (one probe, ~a second) and the decision is made on the
// fresh value:
//
//	on      → proceed;
//	off     → refuse, actionable ("enable encryption first");
//	unknown → refuse, but say the true reason (we could not ask the device) instead of implying
//	          the user turned encryption off.
//
// Proceeding on `unknown` is deliberately NOT an option: require_encryption means "do not take an
// unencrypted backup", and discovering that after writing gigabytes — then discarding them —
// is worse than an actionable refusal (state honesty over optimism).
func (e *Engine) checkEncryption(ctx context.Context, udid, transport, cached string) (code, reason string) {
	state := cached
	if state != "on" && e.prober != nil {
		if fresh, ok := e.prober.RefreshEncryption(ctx, udid, transport); ok {
			e.log.Info("backup: re-read encryption state at preflight", "udid", udid, "cached", cached, "fresh", fresh)
			state = fresh
		}
	}
	switch state {
	case "on":
		return "", ""
	case "off":
		return ErrEncryptionRequired,
			"backup encryption is required but this device has it turned off — enable encryption first"
	default:
		return ErrEncryptionRequired,
			"backup encryption is required but this device's encryption state could not be confirmed " +
				"(the device may be locked) — unlock it and try again"
	}
}

func (e *Engine) awaitDevice(ctx context.Context, lj *liveJob) bool {
	if dev, ok := e.devices.Device(lj.row.UDID); ok && presentOn(dev, lj.row.Transport) {
		return true
	}
	deadline := time.NewTimer(e.cfg.WaitForDeviceTimeout)
	defer deadline.Stop()
	tick := time.NewTicker(e.cfg.SampleInterval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return false
		case <-deadline.C:
			return false
		case <-tick.C:
			if dev, ok := e.devices.Device(lj.row.UDID); ok && presentOn(dev, lj.row.Transport) {
				return true
			}
		}
	}
}

// --- supervision ---

type outcomeKind int

const (
	outcomeProcOK outcomeKind = iota
	outcomeProcErr
	outcomeTimeout
	outcomeCancel
	outcomeShutdown
	outcomeSeedErr // qn.6b: the gated clone (SeedWork) failed after the tool launched
)

type superviseResult struct {
	kind             outcomeKind
	detail           string
	backupSuccessful bool
}

type superviseState struct {
	mu          sync.Mutex
	paused      bool
	outputSince bool
	success     bool
	failReason  string // the tool's own last error line, for an honest failure message
}

// gated-seed rendezvous constants (candidate C).
const (
	gateFileName    = ".quince-gate"        // quince touches this in deviceDir when the seed is done
	infoPlistName   = "Info.plist"          // idevicebackup2 writes this into working/<udid> pre-request
	gateInfoPoll    = 50 * time.Millisecond // poll cadence while waiting for the fresh Info.plist
	gateInfoTimeout = 60 * time.Second      // ceiling for the tool to write Info.plist (handshake ~1–2 s)
)

// runningTool is an already-started idevicebackup2 whose output is being drained; runToolLoop runs
// the sampler over it, waits, and maps the outcome. Split from supervise so candidate C can start the
// tool, overlap the seed, then hand the same process to the sampler loop.
type runningTool struct {
	parent  context.Context    // the job ctx — parent.Err()!=nil means shutdown
	runCtx  context.Context    // cancelable child; a group SIGKILL fires when this is cancelled
	cancel  context.CancelFunc // cancels runCtx (kill), from startTool
	cmd     *exec.Cmd
	ss      *superviseState
	readers *sync.WaitGroup
	tree    string // working/<udid> — where Manifest.db churns; the sampler watches this
}

// startTool builds + starts idevicebackup2 (with --gate when gatePath != "") and begins draining its
// stdout/stderr. The caller MUST eventually call runToolLoop (which Waits + maps the outcome) OR call
// rt.cancel() + rt.readers.Wait() + rt.cmd.Wait() on an abort — else the child and its context leak.
// qn.5b: target IS the working/ parent handed to idevicebackup2; the tool writes the tree into
// <target>/<UDID> by its own convention (no symlink stub; free-space statfs truthful by construction).
func (e *Engine) startTool(parent context.Context, lj *liveJob, target, gatePath string) (*runningTool, error) {
	udid := lj.row.UDID
	runCtx, cancel := context.WithCancel(parent)
	cmd := e.tool.command(runCtx, lj.row.Transport, udid, target, gatePath)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		cancel()
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		cancel()
		return nil, fmt.Errorf("start idevicebackup2: %w", err)
	}
	ss := &superviseState{}
	readers := &sync.WaitGroup{}
	scan := func(rc io.Reader) {
		defer readers.Done()
		sc := bufio.NewScanner(rc)
		sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		sc.Split(scanFrames) // split on \r too, so progress redraws are per-frame tokens (qn.6a #3)
		for sc.Scan() {
			e.handleLine(lj, ss, sc.Text())
		}
	}
	readers.Add(2)
	go scan(stdout)
	go scan(stderr)
	return &runningTool{
		parent: parent, runCtx: runCtx, cancel: cancel, cmd: cmd,
		ss: ss, readers: readers, tree: filepath.Join(target, udid),
	}, nil
}

// runToolLoop runs the liveness sampler over an already-started tool, drains its output to EOF,
// waits, and maps the outcome (design §4). It owns rt.cancel.
func (e *Engine) runToolLoop(lj *liveJob, rt *runningTool) superviseResult {
	defer rt.cancel()

	smp := newSampler(e.cfg, rt.tree, e.freeSpace, e.now())
	ticker := time.NewTicker(e.cfg.SampleInterval)
	defer ticker.Stop()
	sampleDone := make(chan struct{})
	go func() {
		for {
			select {
			case <-rt.runCtx.Done():
				return
			case <-sampleDone:
				return
			case <-ticker.C:
				rt.ss.mu.Lock()
				paused, out := rt.ss.paused, rt.ss.outputSince
				rt.ss.outputSince = false
				rt.ss.mu.Unlock()
				liveness, kill, low := smp.sample(e.now(), paused, out)
				e.progress(lj, func(r *store.JobRow) { r.Liveness = liveness })
				if low != nil {
					e.warnDiskLow(lj, low)
				}
				if kill {
					lj.mu.Lock()
					if lj.killReason == "" {
						lj.killReason = "timeout"
					}
					lj.mu.Unlock()
					rt.cancel()
					return
				}
			}
		}
	}()

	// Drain both pipes to EOF BEFORE Wait: Wait closes the pipes, so calling it first can truncate
	// a lagging scanner and lose the tail (e.g. the final "Backup Successful.") under load — the
	// documented ordering. The process closing stdout/stderr (normal exit, or a group SIGKILL from
	// cancel/timeout/shutdown) is what ends the scanners.
	rt.readers.Wait()
	close(sampleDone)
	waitErr := rt.cmd.Wait()

	lj.mu.Lock()
	reason := lj.killReason
	lj.mu.Unlock()
	rt.ss.mu.Lock()
	success, failReason := rt.ss.success, rt.ss.failReason
	rt.ss.mu.Unlock()

	switch {
	case reason == "cancel":
		return superviseResult{kind: outcomeCancel, backupSuccessful: success}
	case reason == "timeout":
		return superviseResult{kind: outcomeTimeout, backupSuccessful: success}
	case rt.parent.Err() != nil:
		return superviseResult{kind: outcomeShutdown, backupSuccessful: success}
	case waitErr != nil:
		// Prefer the tool's OWN last error line over the exit status: a user can act on
		// "Insufficient free disk space on drive to back up", never on "exit status 151"
		// (qn.4c lab finding — the Operator got the latter three times in a row).
		detail := waitErr.Error()
		if failReason != "" {
			detail = failReason
		}
		return superviseResult{kind: outcomeProcErr, detail: detail, backupSuccessful: success}
	default:
		return superviseResult{kind: outcomeProcOK, backupSuccessful: success}
	}
}

// supervise starts idevicebackup2 (no gate) and runs it to completion — the resume / first-backup
// path where there is no clone to overlap.
func (e *Engine) supervise(parent context.Context, lj *liveJob, target string) superviseResult {
	rt, err := e.startTool(parent, lj, target, "")
	if err != nil {
		return superviseResult{kind: outcomeProcErr, detail: err.Error()}
	}
	return e.runToolLoop(lj, rt)
}

// superviseGatedSeed is candidate C: launch idevicebackup2 with --gate so the on-device passcode
// prompt fires in ~1–2 s, capture the fresh Info.plist it writes, clone latest/ → working/<udid>
// while the tool waits at the gate, restore the fresh Info.plist over the clone's stale one, then
// open the gate. Only reached when a cold clone is pending (seedPending). On any pre-loop failure the
// tool is reaped; the Finding B sentinel guard owns the partial working/.
func (e *Engine) superviseGatedSeed(ctx context.Context, lj *liveJob, target string) superviseResult {
	udid := lj.row.UDID
	// The gate file lives in deviceDir (parent of working/) so the clone's rm-rf of working/<udid>
	// never removes it. Clear any stale gate left by a crashed attempt, and clean up on the way out.
	gatePath := filepath.Join(filepath.Dir(target), gateFileName)
	_ = os.Remove(gatePath)
	defer func() { _ = os.Remove(gatePath) }()

	rt, err := e.startTool(ctx, lj, target, gatePath)
	if err != nil {
		return superviseResult{kind: outcomeProcErr, detail: err.Error()}
	}

	// Wait for the tool to write its fresh Info.plist (spike C12: ~1 s after launch, before the
	// gate), or to exit early (a pairing/handshake failure) — in which case hand off to the sampler
	// loop so the tool's OWN error surfaces, not a synthetic seed error.
	infoPath := filepath.Join(rt.tree, infoPlistName)
	info, toolExited := e.awaitInfoPlist(ctx, infoPath, rt)
	if toolExited {
		return e.runToolLoop(lj, rt)
	}

	abort := func(kind outcomeKind, detail string) superviseResult {
		rt.cancel()
		rt.readers.Wait()
		_ = rt.cmd.Wait()
		return superviseResult{kind: kind, detail: detail}
	}

	// Clone latest/ → working/<udid> while the tool waits at the gate (the ~O(files) seed).
	if err := e.storage.SeedWork(udid, lj.row.ID); err != nil {
		return abort(outcomeSeedErr, err.Error())
	}
	// Restore the fresh Info.plist the clone overwrote with latest's: the committed artifact's
	// Info.plist must reflect THIS device (state honesty), and the incremental handshake sends it to
	// the device. We own the seed — this is the (cz) preserve/rewrite of that one file.
	if len(info) > 0 {
		if err := os.WriteFile(infoPath, info, 0o644); err != nil {
			return abort(outcomeSeedErr, "restore Info.plist after seed: "+err.Error())
		}
	}
	// Open the gate → the tool proceeds into the message loop against the seeded tree.
	if err := os.WriteFile(gatePath, []byte("go\n"), 0o644); err != nil {
		return abort(outcomeSeedErr, "open gate: "+err.Error())
	}

	// The transfer begins now: narrate backing_up and run the liveness sampler over the tool.
	e.transition(lj, func(r *store.JobRow) { r.State = StateBackingUp; r.Phase = PhaseStarting; r.Liveness = LivenessActive })
	return e.runToolLoop(lj, rt)
}

// awaitInfoPlist waits for idevicebackup2 to write its fresh Info.plist into working/<udid> (returned
// so the caller can restore it after the clone), or reports the tool exited/was cancelled first. The
// read is stabilised (size unchanged across two polls) because the tool writes Info.plist in place, so
// a poll can otherwise catch a partial write.
func (e *Engine) awaitInfoPlist(ctx context.Context, infoPath string, rt *runningTool) (info []byte, toolExited bool) {
	exited := make(chan struct{})
	go func() { rt.readers.Wait(); close(exited) }()

	ticker := time.NewTicker(gateInfoPoll)
	defer ticker.Stop()
	deadline := time.NewTimer(gateInfoTimeout)
	defer deadline.Stop()

	var lastSize int64 = -1
	for {
		select {
		case <-ctx.Done():
			return nil, true // cancel/shutdown — runToolLoop maps it once the tool is killed
		case <-exited:
			// Output closed before Info.plist → the tool failed to start the backup. One last check
			// covers the race where Info.plist landed exactly as the tool exited.
			if b, ok := readStableInfo(infoPath, &lastSize); ok {
				return b, false
			}
			return nil, true
		case <-deadline.C:
			// A wedged handshake — hand off to the sampler loop (its backstop kills a stuck tool).
			return nil, true
		case <-ticker.C:
			if b, ok := readStableInfo(infoPath, &lastSize); ok {
				return b, false
			}
		}
	}
}

// readStableInfo returns the file's bytes only once its size is stable across two polls (guarding the
// partial-write window); *last carries the previous poll's size.
func readStableInfo(path string, last *int64) ([]byte, bool) {
	fi, err := os.Stat(path)
	if err != nil || fi.Size() == 0 {
		*last = -1
		return nil, false
	}
	if fi.Size() != *last {
		*last = fi.Size() // changed since last poll → wait one more tick for the write to settle
		return nil, false
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	return b, true
}

func (e *Engine) handleLine(lj *liveJob, ss *superviseState, line string) {
	p := parseLine(line)

	// A pure progress redraw ("[..] 38% Finished", "[..] 2% (23/938 MB)") updates the bar but is NOT
	// logged: at frame rate it floods the pane and the ring buffer (gate-11 finding #3, (cj)). The
	// progress fields still update below, and job.updated carries them (throttled). Everything with
	// real narration — phase headers ("Receiving files"), the passcode wait, the tool's error line,
	// "Backup Successful" — is logged verbatim.
	redraw := (p.overallPercent != nil || p.hasBytes) &&
		!p.waitingPasscode && !p.phaseReceiving && !p.success && p.failReason == ""
	if !redraw {
		e.logs.append(lj.row.ID, line+"\n")
		e.bus.PublishEvent(wire.EventJobLog, wire.JobLogChunk{JobID: lj.row.ID, Chunk: line + "\n"})
	}

	ss.mu.Lock()
	ss.outputSince = true
	if p.success {
		ss.success = true
	}
	if p.waitingPasscode {
		ss.paused = true
	}
	if p.phaseReceiving {
		ss.paused = false
	}
	if p.failReason != "" {
		ss.failReason = p.failReason // keep the LAST one: it is the proximate cause
	}
	ss.mu.Unlock()

	if !p.waitingPasscode && !p.phaseReceiving && p.overallPercent == nil && !p.hasBytes {
		return // pure log line: no progress change
	}
	e.progress(lj, func(r *store.JobRow) {
		if p.waitingPasscode {
			r.Phase = PhaseWaitingForPasscode
		}
		if p.phaseReceiving {
			r.Phase = PhaseReceiving
			r.FilesReceived++
		}
		if p.overallPercent != nil {
			r.Percent = p.overallPercent
		}
		if p.hasBytes {
			r.BytesDone, r.BytesTotal = p.bytesDone, p.bytesTotal
		}
	})
}

func (e *Engine) warnDiskLow(lj *liveJob, low *diskLowInfo) {
	msg := fmt.Sprintf(
		"WARNING: target filesystem low on space — %d MiB free; backup continues, free space to avoid a disk-full failure",
		low.free>>20)
	e.logs.append(lj.row.ID, msg+"\n")
	e.bus.PublishEvent(wire.EventJobLog, wire.JobLogChunk{JobID: lj.row.ID, Chunk: msg + "\n"})
	e.log.Warn("backup: target filesystem low on space", "job", lj.row.ID, "free_mib", low.free>>20)
}

// --- transitions ---

// THE PER-JOB WRITE ORDER IS THE LOCK'S JOB, AND THE LOCK MUST SPAN THE WRITE (quince#178).
//
// Both writers below used to snapshot the row under lj.mu, RELEASE the lock, and only then hit
// SQLite. That makes the order of the two DB writes independent of the order of the two mutations,
// so a sampler goroutine descheduled between its snapshot and its write lands a STALE row on top of
// a terminal one:
//
//	sampler   lock; set liveness; copy row {backing_up}; unlock ......... descheduled .......
//	run       lock; set state=failed; copy row {failed}; unlock; WRITE {failed}; release
//	sampler   ... resumes ...................................................  WRITE {backing_up}
//
// The job is then over — the engine no longer owns it — while the stored row says `backing_up`
// and `waiting_for_passcode`. It is user-visible and it is a state-honesty violation: quince goes
// on telling somebody to enter a passcode for a backup that already failed, and only startup
// reconciliation ever corrects it. It also produced quince#178's ~8% CI flake, because whether the
// test observed the terminal row before the stale write overwrote it was a coin toss.
//
// Holding lj.mu across mutate → persist → emit makes each job's writes total-ordered, so the last
// write is always the last mutation. The mutex is per job, so this serialises a job against ITSELF
// (its sampler versus its run goroutine) and never against another job. Bus.Publish is non-blocking
// — it drops into a full subscriber buffer rather than waiting — and re-enters nothing here, so the
// emit is safe under the lock too, and keeping it there is what stops a stale EVENT racing a fresh
// one the same way.

// transition mutates, persists (BEFORE the event — crash-safe), and emits immediately: for state
// changes, which must never be dropped.
func (e *Engine) transition(lj *liveJob, mutate func(*store.JobRow)) {
	lj.mu.Lock()
	defer lj.mu.Unlock()
	mutate(&lj.row)
	row := lj.row
	if err := e.st.UpdateJob(row); err != nil {
		e.log.Error("backup: persist transition failed", "job", row.ID, "state", row.State, "error", err)
	}
	e.emit(row)
}

// progress mutates always but persists+emits only on the throttle cadence (≤2/s, contract §3); the
// in-memory row keeps the latest, and the next state transition persists it in full.
func (e *Engine) progress(lj *liveJob, mutate func(*store.JobRow)) {
	lj.mu.Lock()
	defer lj.mu.Unlock()
	mutate(&lj.row)
	now := e.now()
	if now.Sub(lj.lastEmit) < e.cfg.ProgressThrottle {
		return
	}
	lj.lastEmit = now
	row := lj.row
	if err := e.st.UpdateJob(row); err != nil {
		e.log.Error("backup: persist progress failed", "job", row.ID, "error", err)
	}
	e.emit(row)
}

func (e *Engine) terminate(lj *liveJob, state, code, msg string) {
	e.transition(lj, func(r *store.JobRow) {
		r.State = state
		fin := e.now()
		r.FinishedAt = &fin
		r.ErrorCode, r.ErrorMessage = code, msg
		// THE PHASE MUST NOT OUTLIVE THE JOB (quince#313). `succeed` clears it; this did not — so a
		// job that failed while parked at `waiting_for_passcode`, which is the ordinary shape of a
		// Wi-Fi backup nobody unlocked, stayed terminal-with-a-live-phase and both consumers went on
		// telling the user to enter a passcode for a job that was over. That is the `State honesty`
		// rule failing at its own example.
		//
		// EMPTY RATHER THAN `PhaseDone`: `done` is what success means here, so a failed job claiming
		// it would trade one false statement for another. Empty says the honest thing — there is no
		// phase, because nothing is in progress.
		//
		// `Liveness` goes with it, for the same reason and in the same breath: it is the other field
		// that describes a live process, and `succeed` settles it too. `Percent` is deliberately LEFT
		// ALONE — on a failure it is the last true measurement of how far the job got, which is
		// information about the past rather than a claim about now.
		r.Phase, r.Liveness = "", LivenessNone
	})
	e.log.Info("backup: job terminal", "job", lj.row.ID, "udid", lj.row.UDID, "state", state, "code", code)
}

func (e *Engine) succeed(lj *liveJob, versionID string) {
	e.transition(lj, func(r *store.JobRow) {
		r.State, r.Phase, r.Percent = StateSucceeded, PhaseDone, f64(100)
		// TERMINAL MEANS NO LIVENESS, succeeded included (quince#313, ruled on that PR). This set
		// `active` — a claim that a process is running, on a job that had just finished. Latent
		// rather than user-visible, because both consumers gate on terminal state; what it costs is
		// the contract and the engine disagreeing, and somebody later removing a consumer guard on
		// the strength of the contract.
		//
		// `Phase` stays `done` here and is cleared only on failure. The asymmetry is deliberate:
		// `done` is a TRUE statement about a succeeded job, where `active` is not.
		r.Liveness = LivenessNone
		fin := e.now()
		r.FinishedAt, r.VersionID = &fin, &versionID
	})
	e.log.Info("backup: job succeeded", "job", lj.row.ID, "udid", lj.row.UDID, "version", versionID)
	// The device's last_backup now reads differently (it derives from committed versions), so ask
	// the registry to re-publish it: the dashboard card lands on "Last backup … · succeeded"
	// without a page refresh (qn.4a findings (iv)+(v)). Nil-safe: without an announcer the card
	// catches up on the next fetch.
	if e.announcer != nil {
		e.announcer.AnnounceBackup(lj.row.UDID)
	}
}

func (e *Engine) discard(lj *liveJob) {
	note, err := e.storage.Discard(lj.row.UDID, lj.row.ID)
	if err != nil {
		e.log.Warn("backup: discard failed", "job", lj.row.ID, "error", err)
		return
	}
	if note != "" {
		e.log.Info("backup: work discarded", "job", lj.row.ID, "note", note)
	}
}

func (e *Engine) emit(row store.JobRow) { e.bus.PublishEvent(wire.EventJobUpdated, jobToWire(row)) }

func (e *Engine) release(lj *liveJob) {
	e.mu.Lock()
	delete(e.running, lj.row.UDID)
	e.mu.Unlock()
	// The storage binding lives exactly as long as the job does. Dropped here rather than at
	// each terminal state, because release is the one path every ending job takes — success,
	// failure, cancel and shutdown alike.
	e.versionQ.UnbindJob(lj.row.ID)
}

func (e *Engine) killReasonOf(lj *liveJob) string {
	lj.mu.Lock()
	defer lj.mu.Unlock()
	return lj.killReason
}

// resolveTransport turns the requested transport into a concrete usb|wifi, or an HTTP error to
// return from StartBackup. Explicit usb|wifi passes through unchanged (keeping the start-then-connect
// waiting_for_device flow — presence is NOT required at Start). "auto" (design §4, decisions (bp))
// resolves against CURRENT presence — prefer USB when present, else Wi-Fi — and refuses a device on
// neither transport with an actionable 422 (no job minted): a guessed transport would persist a
// dishonest Job.transport. status == 0 means success (the resolved transport is returned).
func (e *Engine) resolveTransport(udid, requested string) (transport string, status int, reason string) {
	switch requested {
	case TransportUSB, TransportWiFi:
		return requested, 0, ""
	case TransportAuto:
		dev, ok := e.devices.Device(udid)
		switch {
		case ok && presentOn(dev, TransportUSB):
			return TransportUSB, 0, ""
		case ok && presentOn(dev, TransportWiFi):
			return TransportWiFi, 0, ""
		default:
			return "", http.StatusUnprocessableEntity,
				"device is not currently connected — connect it over USB or Wi-Fi, or choose a transport"
		}
	default:
		return "", http.StatusUnprocessableEntity, "transport must be usb, wifi, or auto"
	}
}

func presentOn(dev wire.Device, transport string) bool {
	switch transport {
	case TransportUSB:
		return dev.Transports.USB != nil
	case TransportWiFi:
		return dev.Transports.WiFi != nil
	}
	return false
}
