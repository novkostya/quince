package deviceops

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"

	"howett.net/plist"

	"github.com/novkostya/quince/core/internal/device"
)

// run executes one CLI, capturing stdout/stderr. The child is group-isolated and ctx-killed
// (setpgid); its only added env is the muxer pointer (never a secret). Short-lived one-shot.
func (t *Tools) run(ctx context.Context, bin, transport string, args ...string) (stdout, stderr string, err error) {
	cmd := exec.CommandContext(ctx, bin, t.args(args...)...)
	setpgid(cmd)
	cmd.Env = t.childEnv(transport)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err = cmd.Run()
	return out.String(), errb.String(), err
}

// --- idevicepair validate ---

type validateResult int

const (
	validateUnknown   validateResult = iota
	validatePaired                   // SUCCESS: Validated
	validateNotPaired                // not paired with this host
	validateLocked                   // a pairing record exists but the device is passcode-locked
)

func pairedString(vr validateResult) string {
	switch vr {
	case validatePaired:
		return "yes"
	case validateNotPaired:
		return "no"
	default:
		// validateLocked: the "passcode is set" response is returned for any LOCKED device
		// regardless of whether a pairing record exists (lab-confirmed 2026-07-20 — it appeared
		// on a fresh host with no record), so pairing is genuinely undeterminable while locked.
		return "unknown"
	}
}

func (t *Tools) validate(ctx context.Context, udid, transport string) (validateResult, error) {
	if !validUDID(udid) {
		return validateUnknown, ErrBadUDID
	}
	args := append(networkArgs(transport), "-u", udid, "validate")
	out, errOut, err := t.run(ctx, t.Idevicepair, transport, args...)
	combined := out + errOut
	switch {
	case err == nil && strings.Contains(out, "SUCCESS: Validated"):
		return validatePaired, nil
	case strings.Contains(combined, "is not paired with this host"):
		return validateNotPaired, nil
	case strings.Contains(combined, "passcode is set"):
		return validateLocked, nil
	case err == nil:
		return validatePaired, nil // clean exit without a recognized line → paired
	default:
		return validateUnknown, fmt.Errorf("idevicepair validate: %w: %s", err, strings.TrimSpace(combined))
	}
}

// Validate reports whether the device is CONFIRMED paired with this host (contracts §1 POST
// .../pair/validate → {paired}). A locked device ("passcode is set") is not a confirmation —
// that response is returned regardless of pairing state — so it reports false, honestly (the
// caller can unlock and retry).
func (t *Tools) Validate(ctx context.Context, udid, transport string) (bool, error) {
	vr, err := t.validate(ctx, udid, transport)
	if err != nil {
		return false, err
	}
	return vr == validatePaired, nil
}

// --- ideviceinfo ---

func parseInfoPlist(xmlStr string) (name, model, ios string) {
	var m map[string]any
	if _, err := plist.Unmarshal([]byte(xmlStr), &m); err != nil {
		return "", "", ""
	}
	name, _ = m["DeviceName"].(string)
	model, _ = m["ProductType"].(string)
	ios, _ = m["ProductVersion"].(string)
	return name, model, ios
}

// info reads DeviceName/ProductType/ProductVersion. simple=true uses -s (no auto-pairing) so
// an unpaired device is never pushed into a Trust prompt by a background read; the full read
// (a trusted session) is used only once validate confirms a pairing exists.
func (t *Tools) info(ctx context.Context, udid, transport string, simple bool) (name, model, ios string) {
	args := networkArgs(transport)
	if simple {
		args = append(args, "-s")
	}
	args = append(args, "-u", udid, "-x")
	out, _, err := t.run(ctx, t.Ideviceinfo, transport, args...)
	if err != nil {
		return "", "", ""
	}
	return parseInfoPlist(out)
}

// willEncrypt reads lockdown com.apple.mobile.backup/WillEncrypt → the backup_encryption
// state (design §3). Requires a trusted session, so it is queried only for paired devices.
//
// An ABSENT key — exit 0 with empty output — means "off", not "unknown" (qn.4a lab finding (i)-A,
// fixed in qn.4c): a device that has never had a backup password simply has no WillEncrypt key,
// and reporting `unknown` there made the UI hide the not-encrypted warning and ask for a *current*
// password the device does not have. `unknown` stays reserved for a genuine failure to read (a
// cold or locked lockdown, an unparseable value) — the case where quince really does not know.
func (t *Tools) willEncrypt(ctx context.Context, udid, transport string) string {
	args := append(networkArgs(transport), "-u", udid, "-q", backupDomain, "-k", "WillEncrypt")
	return scalarTriState(t.run(ctx, t.Ideviceinfo, transport, args...))
}

// Lockdown domains quince reads. Named because a scalar read is dispatched by domain, and a bare
// string literal at the call site is what let one fake answer two different reads (qn.7).
const (
	backupDomain = "com.apple.mobile.backup"
	// wifiSyncDomain is verified to EXIST: it is in ideviceinfo's known-domain list at the pinned
	// libimobiledevice 1.4.0 (tools/ideviceinfo.c, tagged iOS 4.0+), so a -q read of it is accepted
	// by the shipped binary. The KEY inside it is a different question — see wifiSyncKey.
	wifiSyncDomain = "com.apple.mobile.wireless_lockdown"
)

// wifiSyncKey is the lockdown key holding the Wi-Fi-sync flag. MEASURED on hardware 2026-07-31
// (qn.7 story 3), which is the only reason it is not still the empty string.
//
// The domain was dumped on a real device over Wi-Fi and returned six keys. This one is a boolean
// and read `true` on a device whose Wi-Fi sync was known to be on. The others: EnableWifiDebugging
// (a different feature), SupportsWifi and SupportsWifiSyncing (capability flags, uniformly true),
// and two strings whose values embed the device's MAC and link-local address — Operator-private,
// recorded only in the private layer.
//
// PROVENANCE MATTERS HERE, so it is in the code rather than only in a PR thread. The roadmap
// GUESSED this name, and the guess was right — but it could not be known to be: the string appears
// NOWHERE in libimobiledevice 1.4.0 (measured, grep, no hits), so nothing corroborated it until a
// device did. Shipping the guess unverified would have been correct by luck, and the failure mode
// if it had been wrong is silent: `ideviceinfo -q <domain> -k <wrong-key>` exits 0 printing
// nothing, which scalarTriState maps to "off" — a confident lie about every device, and the shape
// of qn.4a finding (i)-A, which shipped once already.
//
// STILL OWED, and deliberately not papered over: the OFF/ON differential. A single read with the
// flag ON cannot prove this key is what CHANGES rather than SupportsWifiSyncing, which was also
// true. The discrimination rests on internal evidence — within that one dump, EnableWifiDebugging
// was `false` while this was `true`, so the Enable* family is state rather than capability — which
// is strong, and is still an inference. Toggling needs Finder, the detour qn.7 exists to remove.
// One 30-second toggle converts it to a measurement; until then this comment is the honest record.
const wifiSyncKey = "EnableWifiConnections"

// wifiSync reads the device's Wi-Fi-sync flag → the wifi_sync state (design §3), with willEncrypt's
// unknown-vs-off rule: an absent key means "off", and "unknown" is reserved for a genuine failure
// to read. Queried only for paired devices, for the same trusted-session reason.
//
// Returns "unknown" without touching the device while the key is unset. That is not a stub standing
// in for work — it is the honest answer to "is Wi-Fi sync on?" when quince does not yet know which
// key to ask about.
func (t *Tools) wifiSync(ctx context.Context, udid, transport string) string {
	if t.wifiSyncKey == "" {
		return "unknown"
	}
	args := append(networkArgs(transport), "-u", udid, "-q", wifiSyncDomain, "-k", t.wifiSyncKey)
	return scalarTriState(t.run(ctx, t.Ideviceinfo, transport, args...))
}

// ErrWifiSyncUnverifiable is returned when the key is not known, so quince cannot write it. It is a
// distinct error rather than a generic failure because the remedy is a hardware measurement, not a
// retry — and a caller that cannot tell those apart will retry forever.
var ErrWifiSyncUnverifiable = errors.New("wi-fi sync key is unmeasured; refusing to write a guessed key")

// ErrWifiSyncNotApplied is the story-7 case: the tool reported success and the device did not
// change. Distinct from a write error for the same reason — it means the device declined silently,
// which is a state to surface, not an operation to repeat.
var ErrWifiSyncNotApplied = errors.New("device reported success but the value did not change")

// ErrWifiSyncUnreadable is the write that was ACCEPTED and could not be read back — distinct from
// ErrWifiSyncNotApplied, which asserts the state is UNCHANGED. Conflating the two put a false
// sentence in front of the user: `contracts.md` defines `wifi_sync_not_applied` as "accepted and not
// applied; the state is UNCHANGED, not unknown", and a failed read establishes neither half of that
// (quince#363, ruled 2026-07-31).
//
// It is not always a failure. Disabling over Wi-Fi severs the transport the read-back would use, so
// on that one path this error IS the expected consequence of success — see runWifiSync, which is
// where that judgement belongs, because only the caller knows the action and the transport.
var ErrWifiSyncUnreadable = errors.New("write accepted but the value could not be read back")

// SetWifiSync writes the device's Wi-Fi-sync flag and VERIFIES IT LANDED, per decisions/0004 — a
// mutation must be verified to have mutated.
//
// The re-read is not belt-and-braces. `lockdownd_set_value` returning success means the device
// accepted the request, not that the setting took effect, and this is a domain quince has never
// written before: nobody has established that iOS applies this key without a reboot, a respring, or
// a Trust re-confirm. Trusting the exit code would make quince report "Wi-Fi sync is on" on the
// strength of having asked.
//
// Uses run, NOT the pty path: the value is a boolean, and pty.go exists to keep a PASSWORD out of
// argv. Importing that machinery here would guard nothing.
func (t *Tools) SetWifiSync(ctx context.Context, udid, transport string, enable bool) error {
	if t.wifiSyncKey == "" {
		return ErrWifiSyncUnverifiable
	}
	want := "false"
	if enable {
		want = "true"
	}
	args := append(networkArgs(transport), "-u", udid, "-q", wifiSyncDomain, "-k", t.wifiSyncKey, "--set-bool", want)
	if _, stderr, err := t.run(ctx, t.Ideviceinfo, transport, args...); err != nil {
		return fmt.Errorf("ideviceinfo --set-bool: %w: %s", err, lastLine(stderr))
	}

	// Read back through the SAME path the UI will show, so a mismatch here is exactly the mismatch
	// a user would see rather than a private notion of success.
	got := t.wifiSync(ctx, udid, transport)
	if got == "unknown" {
		// NOT "not applied" — nothing here says the value is unchanged, only that it is unread.
		return fmt.Errorf("%w: the read-back returned no value", ErrWifiSyncUnreadable)
	}
	if (got == "on") != enable {
		return fmt.Errorf("%w: wanted %s, device still reports %s", ErrWifiSyncNotApplied, want, got)
	}
	return nil
}

// scalarTriState maps an `ideviceinfo -k` scalar read onto on | off | unknown. Shared by the two
// reads so the absent-key rule cannot drift between them: exit 0 with EMPTY output is "off" (the
// key is absent — the device saying no), a non-zero exit or an unparseable value is "unknown".
func scalarTriState(out, _ string, err error) string {
	if err != nil {
		return "unknown"
	}
	switch strings.TrimSpace(out) {
	case "true":
		return "on"
	case "false", "":
		return "off"
	default:
		return "unknown"
	}
}

// Info builds the lockdown identity overlay for a device (enrichment). It NEVER triggers a
// pairing: the full read + WillEncrypt run only for a CONFIRMED validatePaired (an established
// trust session, so no handshake). Any other state — not paired, or locked ("passcode is
// set", which is NOT a confirmation, lab finding 2026-07-20) — uses the simple read (-s), which
// cannot auto-pair, so a background enrichment can never surface an unexpected Trust prompt.
// Undetermined fields stay "" / "unknown" — never guessed (state honesty). Name/encryption
// fill in on the next enrichment once the device is unlocked + paired (e.g. reEnrich after the
// explicit pair op).
func (t *Tools) Info(ctx context.Context, udid, transport string) (device.Identity, error) {
	if !validUDID(udid) {
		return device.Identity{}, ErrBadUDID
	}
	vr, _ := t.validate(ctx, udid, transport)
	id := device.Identity{Paired: pairedString(vr)}
	if vr == validatePaired {
		id.Name, id.Model, id.IOSVersion = t.info(ctx, udid, transport, false)
		id.BackupEncryption = t.willEncrypt(ctx, udid, transport)
		id.WifiSync = t.wifiSync(ctx, udid, transport)
	} else {
		id.Name, id.Model, id.IOSVersion = t.info(ctx, udid, transport, true)
	}
	return id, nil
}

// --- idevicepair pair (single attempt; the manager owns the waiting_for_user poll loop) ---

type pairOutcome int

const (
	pairFailed       pairOutcome = iota
	pairPaired                   // SUCCESS: Paired
	pairNeedTrust                // accept the trust dialog, then attempt to pair again
	pairNeedPasscode             // passcode is set; enter it on the device and retry
	pairDenied                   // the user denied the trust dialog
	pairNotUSB                   // pairing not possible over this connection
)

// pairAttempt runs one `idevicepair pair` and classifies the outcome (verified strings —
// interface fact 3). The message is plain-language narration for the Op.
func (t *Tools) pairAttempt(ctx context.Context, udid, transport string) (pairOutcome, string, error) {
	if !validUDID(udid) {
		return pairFailed, "", ErrBadUDID
	}
	args := append(networkArgs(transport), "-u", udid, "pair")
	out, errOut, err := t.run(ctx, t.Idevicepair, transport, args...)
	combined := out + errOut
	switch {
	case err == nil && strings.Contains(out, "SUCCESS: Paired"):
		return pairPaired, "Paired with this computer.", nil
	case strings.Contains(combined, "accept the trust dialog"):
		return pairNeedTrust, "Tap Trust on the device to allow this computer, then it will finish automatically.", nil
	case strings.Contains(combined, "passcode is set"):
		return pairNeedPasscode, "Enter the passcode on the device to continue pairing.", nil
	case strings.Contains(combined, "denied the trust dialog"):
		return pairDenied, "The trust request was declined on the device.", nil
	case strings.Contains(combined, "not possible over this connection"):
		return pairNotUSB, "Pairing needs a USB connection.", nil
	default:
		return pairFailed, "", fmt.Errorf("idevicepair pair: %w: %s", err, strings.TrimSpace(combined))
	}
}
