package httpapi

import (
	"crypto/subtle"
	"errors"
	"log/slog"
	"sync"
	"time"
)

// certTrialWindow is how long a trial certificate serves before quince puts the previous one back.
//
// # TEN MINUTES, AND THE NUMBER IS TAKEN FROM THE PRIOR ART RATHER THAN FROM A PARENTHESIS
//
// quince#908 §5 wrote *"thirty seconds of silence is a failure, three is someone reading"*, which
// reads as ~30s. That was an illustration inside a sentence about something else, and the Operator's
// ruling of 2026-08-14 required the number to be brought back with the PR rather than inherited.
//
// THE SAME PROBLEM HAS A FORTY-YEAR-OLD ANSWER, and the two ends of it disagree by a factor of forty:
//
//   - **Junos `commit confirmed`: 10 minutes.** *"If the commit is not confirmed within a certain
//     time (10 minutes by default), the operating system automatically rolls back to the previous
//     configuration"* — juniper.net, Commit the Configuration. Cisco IOS XE's equivalent
//     (`configure revert timer`) documents no default but bounds the range at 1–120 minutes.
//   - **NetworkManager `nmcli … checkpoint`: 15 seconds.** Same mechanism, same fear — an operator
//     cutting off the ssh session they are typing into.
//
// THE DEFAULT TRACKS WHO CONFIRMS. `nmcli`'s confirmation is the next line of a script, so 15
// seconds is generous. Junos expects a human to re-establish a session and look around, so it is
// 600. Ours is a human who must read an instruction, open the https address in a browser, almost
// certainly click through a certificate interstitial, and press a button. That is the Junos case.
//
// THE MECHANISM ITSELF CONTRIBUTES NOTHING TO THE BUDGET — measured, in
// TestConfirmOverTheTLSHalfCostsNothingMeasurable: the apply → https-confirm round trip against a
// real router over a real handshake ran in **11.5–19.8 ms across five runs** (under `-race`, which
// inflates it). That is 0.003% of this window. So the whole of this number is human time, and there
// is no engineering term in it to trade against.
//
// AND THE COST IS ASYMMETRIC. Too short abandons a certificate that was working, from a user who did
// everything right, and fails the same way on every retry. Too long leaves somebody looking at a
// certificate their browser rejects — which is BOUNDED, ends by itself, and is visible to them.
//
// NOT A CONFIG KEY. D12 says every SETTING has a sane default and is editable; this is an internal
// timeout with no operator decision behind it, and a key nobody would ever set is the noise that
// ruling exists to keep out of `config.yml`.
const certTrialWindow = 10 * time.Minute

// certKeeper is the part of `*tlsx.Keeper` this package uses: point the running daemon at a pair,
// with no file anywhere in it.
//
// AN INTERFACE SO `httpapi` DOES NOT IMPORT `tlsx`, and so a test can drive the trial without a real
// certificate.
//
// `HasCertificate` IS HERE FOR A SECOND CONSUMER, NOT FOR THE TRIAL (quince#940 §1). It answers *is
// the daemon serving a certificate RIGHT NOW*, which is what lets `GET /api/onboarding/https` tell a
// configured-but-broken pair apart from no TLS at all — see `tlsUnusableCode`.
type certKeeper interface {
	SetFiles(certFile, keyFile string) error
	HasCertificate() bool
}

// certTrial holds the one certificate that is being tried out, and puts the previous one back if
// nobody proves it works (quince#908 slice 5).
//
// # NOTHING IS WRITTEN TO `config.yml` UNTIL THE PROOF ARRIVES
//
// Operator, 2026-08-14: *"we're not going to actually write tls setting entry to config.yml for that
// 30 seconds and only write config once probe has succeeded?"* The first version of this did write
// first and wrote a second time to undo, which left a certificate that never worked visible in a
// hand-edited file for ten minutes. **D12 says that file contains only what the user set, and a
// certificate somebody tried and abandoned was never something they set.**
//
// So the trial lives HERE and in the Keeper, which is what actually serves TLS and needs no file to
// do it. Three things follow, and all three are improvements rather than consolations:
//
//   - **A restart mid-window is fail-SAFE.** The trial evaporates and the daemon comes up on the
//     pair `config.yml` still names — the last one that worked. The write-first shape came up still
//     serving an unconfirmed certificate with no timer watching it.
//   - **The undo cannot fail.** Dropping the Keeper back is not a file write, so there is no
//     "revert failed, the bad pair is still configured" branch to log and live with.
//   - **The ruling's own hazard dissolves.** *"The revert must restore BOTH settings atomically —
//     miss it and the user has a broken certificate AND no plain-http fallback"* has nothing to
//     restore.
//
// WHAT IT COSTS, STATED RATHER THAN DISCOVERED LATER: for up to ten minutes the daemon serves a
// certificate `config.yml` does not name. That is hidden state, which this project forbids
// elsewhere, so it is surfaced — see `pending`, the apply response, and the WARN at trial start.
//
// SERVER-SIDE, NOT CLIENT-SIDE, AND THAT IS STRUCTURAL. Once the trial pair is live, `plainHalf`
// redirects http to https — into the handshake that may be exactly what is broken — so a
// client-driven rollback would travel over the channel whose failure it exists to recover from.
//
// ONE AT A TIME. A second apply supersedes the first rather than queueing beside it: two live trials
// would each hold a different "previous" and the later undo would restore a pair from two steps ago.
type certTrial struct {
	log    *slog.Logger
	keeper certKeeper
	// afterFunc is time.AfterFunc in production and a fake in tests, so the ten-minute window is
	// reached without waiting for it. It returns an interface rather than a *time.Timer for that
	// reason alone — a fake cannot build one whose Stop means anything.
	afterFunc func(time.Duration, func()) trialTimer
	now       func() time.Time

	mu   sync.Mutex
	live *certTrialPending
	// gen increments on every trial and NEVER resets. It is what makes a superseded timer harmless:
	// see expire.
	gen uint64
}

// trialTimer is the part of *time.Timer this file uses.
type trialTimer interface{ Stop() bool }

type certTrialPending struct {
	gen   uint64
	token string
	// trialCert/trialKey are what CONFIRM commits to `config.yml`. The write-first shape did not
	// need these — the file already held them — and holding them is the whole difference.
	trialCert string
	trialKey  string
	// prevCert/prevKey are what EXPIRY hands back to the Keeper. They are the pair `config.yml`
	// still names, captured when the trial began, because an authenticated admin editing the config
	// mid-window would otherwise make "the previous pair" ambiguous.
	prevCert string
	prevKey  string
	// deadline carries Go's MONOTONIC reading, because it came from `time.Now().Add(…)` and nothing
	// has stripped it. Comparisons against another `time.Now()` therefore use the monotonic clock
	// and are immune to an NTP step or an operator setting the clock mid-window.
	deadline time.Time
	// deadlineWall is the same instant with the monotonic reading STRIPPED (`Round(0)`), kept
	// because the monotonic clock has its own blind spot: on Linux it does not advance while the
	// machine is SUSPENDED. A laptop or VM asleep for an hour wakes with the monotonic clock
	// believing no time has passed, and the wall clock knowing better.
	//
	// SO THE TRIAL IS OVER IF EITHER CLOCK SAYS SO, and that direction is the safe one: expiring
	// early costs a user one retry, expiring late leaves a certificate their browser rejects in
	// place. Neither clock is trusted alone and the earlier verdict wins.
	//
	// THE PRIMITIVE THAT WOULD DO THIS IN ONE READ IS `CLOCK_BOOTTIME` — monotonic AND counting
	// time spent suspended, i.e. "ticks since boot". Go does not expose it: `time.Now`'s monotonic
	// reading is `CLOCK_MONOTONIC`, which stops while the machine sleeps, and reaching BOOTTIME
	// means `golang.org/x/sys/unix.ClockGettime` plus a per-OS fallback. The OR of these two fields
	// is the same guarantee assembled from clocks the standard library already has, which is worth
	// more than a syscall and a build tag for a ten-minute window.
	deadlineWall time.Time
	timer        trialTimer
}

// expired reports whether this trial's window has closed, by both clocks.
func (p *certTrialPending) expired(now time.Time) bool {
	return now.After(p.deadline) || now.Round(0).After(p.deadlineWall)
}

func newCertTrial(log *slog.Logger, keeper certKeeper) *certTrial {
	return &certTrial{
		log:       log,
		keeper:    keeper,
		afterFunc: func(d time.Duration, f func()) trialTimer { return time.AfterFunc(d, f) },
		now:       time.Now,
	}
}

// begin points the running daemon at (trialCert, trialKey) and schedules a return to (prevCert,
// prevKey). It returns the deadline, or an error if the daemon would not take the pair.
//
// THE KEEPER IS PUT BACK IF IT REFUSES. `SetFiles` keeps the new paths even when the load fails —
// that is its documented self-heal, so a pair that becomes readable later is picked up — which is
// exactly wrong here: a trial that never started must leave nothing behind for the next handshake to
// find. So a failure restores the previous pair before returning.
func (c *certTrial) begin(token, trialCert, trialKey, prevCert, prevKey string) (time.Time, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if err := c.keeper.SetFiles(trialCert, trialKey); err != nil {
		if rerr := c.keeper.SetFiles(prevCert, prevKey); rerr != nil {
			c.log.Error("could not restore the previous certificate after a failed trial",
				"cert_file", prevCert, "key_file", prevKey, "error", rerr)
		}
		return time.Time{}, err
	}

	// A SECOND APPLY SUPERSEDES THE FIRST. Stopping the old timer is the tidy half; the half that
	// matters is the generation check in expire, because Stop returning false means the timer has
	// ALREADY fired and its function may be running right now, past any lock we hold.
	if c.live != nil {
		c.live.timer.Stop()
	}
	c.gen++
	gen := c.gen
	deadline := c.now().Add(certTrialWindow)
	c.live = &certTrialPending{
		gen: gen, token: token,
		trialCert: trialCert, trialKey: trialKey,
		prevCert: prevCert, prevKey: prevKey,
		deadline: deadline, deadlineWall: deadline.Round(0),
		// THE TIMER IS A NUDGE, NOT THE TRUTH (Operator, 2026-08-14). It exists to put the Keeper
		// back promptly and to write the log line; nothing reads it to decide whether the window is
		// open. See `expired`.
		timer: c.afterFunc(certTrialWindow, func() { c.expire(gen) }),
	}

	// LOGGED AS A WARNING, because from here until the deadline the daemon is serving a certificate
	// `config.yml` does not name. That divergence is the price of not writing the file, and an
	// operator reading the log should find it rather than deduce it.
	c.log.Warn("serving a TRIAL certificate — config.yml is unchanged and will stay so unless this is confirmed",
		"cert_file", trialCert, "key_file", trialKey,
		"reverts_to_cert_file", prevCert, "reverts_to_key_file", prevKey,
		"deadline", deadline.UTC().Format(time.RFC3339))
	return deadline, nil
}

// confirm cancels the trial if token names it, and returns the pair the caller must now WRITE.
//
// IT RETURNS THE PAIR RATHER THAN WRITING IT, so this type never touches `config.Service`. The
// handler owns the write, beside the `Configured()` guard that makes the route legal at all.
//
// CONSTANT-TIME COMPARE, THOUGH THE TOKEN IS NOT A SECRET. What it protects is not access — the
// route is pre-auth in a window where anyone reaching the port can claim the whole install — but the
// CORRELATION between a confirmation and the trial it confirms.
//
// A TOKEN FROM A SUPERSEDED TRIAL IS REFUSED, which is the point of it being a token rather than a
// boolean: a stale tab would otherwise attest that https works about a certificate that is no longer
// the one being served.
// THE DEADLINE IS CHECKED HERE RATHER THAN TRUSTED TO THE TIMER, and that closes a real hole
// (Operator, 2026-08-14). A timer fires late — a GC pause, a loaded box, a suspended VM — and the
// first version of this checked only *is a trial live*, so a confirmation arriving after the window
// closed but before the callback ran would SUCCEED, writing a certificate whose trial had already
// expired into `config.yml`. Expiry is a fact to evaluate, not an event to hope fired.
func (c *certTrial) confirm(token string) (certFile, keyFile string, ok bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.live == nil || c.live.expired(c.now()) {
		return "", "", false
	}
	if subtle.ConstantTimeCompare([]byte(token), []byte(c.live.token)) != 1 {
		return "", "", false
	}
	c.live.timer.Stop()
	cert, key := c.live.trialCert, c.live.trialKey
	c.live = nil
	return cert, key, true
}

// pending reports the live trial's deadline, if there is one. This is the read that keeps the
// divergence from being hidden state: a surface that wants to say *serving a trial certificate,
// unconfirmed, until HH:MM* asks here.
func (c *certTrial) pending() (time.Time, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	// SAME CHECK AS `confirm`, so health cannot advertise a window that has already closed while a
	// late timer has yet to run.
	if c.live == nil || c.live.expired(c.now()) {
		return time.Time{}, false
	}
	return c.live.deadline, true
}

// expire hands the Keeper back the pair `config.yml` names — and does nothing at all if this is no
// longer the trial anybody is waiting on.
//
// THE GENERATION CHECK IS THE WHOLE SAFETY PROPERTY, and `timer.Stop()` is not a substitute for it.
// Stop returns false when the timer has already fired, and by then this function may be blocked on
// the mutex `begin` or `confirm` is holding. Without the check it would drop a SECOND trial that has
// since started, or one that has just been confirmed — pulling a certificate the user was told was
// accepted. The window is small and it is exactly the window a user retrying an apply is inside.
func (c *certTrial) expire(gen uint64) {
	c.mu.Lock()
	if c.live == nil || c.live.gen != gen {
		c.mu.Unlock()
		return
	}
	prevCert, prevKey := c.live.prevCert, c.live.prevKey
	c.live = nil
	c.mu.Unlock()

	c.log.Warn("trial certificate was not confirmed — returning to the pair config.yml names",
		"window", certTrialWindow.String(), "cert_file", prevCert, "key_file", prevKey)
	if err := c.keeper.SetFiles(prevCert, prevKey); err != nil {
		// NOTHING WAS WRITTEN, SO NOTHING IS LOST — and that is the difference from the shape this
		// replaced. A failure here means the pair `config.yml` names is not loadable right now; the
		// file is already correct, and `Keeper`'s own self-heal picks the pair up as soon as it can
		// be read. The write-first shape had no such fallback: a failed revert left the file wrong.
		c.log.Error("could not return to the configured certificate — config.yml is unchanged and correct",
			"cert_file", prevCert, "key_file", prevKey, "error", err)
	}
}

// errNoKeeper is what `unavailableKeeper` returns. The apply route turns it into a 503 with a stated
// reason rather than a 500, because "this quince has no TLS listener" is a fact about the deployment
// and not a fault.
var errNoKeeper = errors.New("no TLS keeper is wired: this quince cannot serve a certificate")

// unavailableKeeper stands in wherever no TLS listener exists — `--demo`, the admin CLIs, every test
// router that does not ask for one. It is the same shape as `UnavailableDeviceOps` and the four
// others beside it in `NewRouter`, and for the same reason: the surface EXISTS and refuses with a
// stated reason, so no handler needs an `if demo` in it (quince#841).
type unavailableKeeper struct{}

func (unavailableKeeper) SetFiles(string, string) error { return errNoKeeper }

// HasCertificate is false because there is no TLS listener at all — which is the honest answer and
// is also the SAFE one: `tlsUnusableCode` reports a broken certificate only when the config ASKS for
// TLS, and a router with no keeper is one where nothing asked.
func (unavailableKeeper) HasCertificate() bool { return false }
