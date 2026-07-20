package deviceops

import (
	"bytes"
	"context"
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
	case validatePaired, validateLocked:
		return "yes"
	case validateNotPaired:
		return "no"
	default:
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

// Validate reports whether the device is paired with this host (contracts §1 POST
// .../pair/validate → {paired}). A passcode-locked device whose pairing record exists counts
// as paired (no unpair needed); "not paired" is the only false.
func (t *Tools) Validate(ctx context.Context, udid, transport string) (bool, error) {
	vr, err := t.validate(ctx, udid, transport)
	if err != nil {
		return false, err
	}
	return vr == validatePaired || vr == validateLocked, nil
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
func (t *Tools) willEncrypt(ctx context.Context, udid, transport string) string {
	args := append(networkArgs(transport), "-u", udid, "-q", "com.apple.mobile.backup", "-k", "WillEncrypt")
	out, _, err := t.run(ctx, t.Ideviceinfo, transport, args...)
	if err != nil {
		return "unknown"
	}
	switch strings.TrimSpace(out) {
	case "true":
		return "on"
	case "false":
		return "off"
	default:
		return "unknown"
	}
}

// Info builds the lockdown identity overlay for a device (enrichment). It never triggers a
// pairing: the full read + WillEncrypt run only when validate already reports a pairing
// record; an unpaired device gets the public fields via a simple connection. Undetermined
// fields stay "" / "unknown" — never guessed (state honesty).
func (t *Tools) Info(ctx context.Context, udid, transport string) (device.Identity, error) {
	if !validUDID(udid) {
		return device.Identity{}, ErrBadUDID
	}
	vr, _ := t.validate(ctx, udid, transport)
	id := device.Identity{Paired: pairedString(vr)}
	if vr == validatePaired || vr == validateLocked {
		id.Name, id.Model, id.IOSVersion = t.info(ctx, udid, transport, false)
		id.BackupEncryption = t.willEncrypt(ctx, udid, transport)
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
